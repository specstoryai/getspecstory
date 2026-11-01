package monitor

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// shutdownGracePeriod is how long a child gets to exit after SIGTERM before it
// is SIGKILLed. Orphaned watch children are the operational footgun of the
// monitor, so termination always escalates rather than waiting forever.
const shutdownGracePeriod = 5 * time.Second

// child tracks one running `specstory watch` process.
type child struct {
	cmd          *exec.Cmd
	lastActivity time.Time
	// done is closed by the per-child Wait goroutine once the OS process has
	// been reaped, so the supervisor can distinguish alive from exited without
	// blocking, and terminators can wait for actual process death.
	done chan struct{}
}

// exited reports (without blocking) whether the child's process has been reaped.
func (c *child) exited() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Supervisor owns the table of running watch children: it spawns on activity,
// reaps on idleness or self-exit, and terminates everything on shutdown.
type Supervisor struct {
	mu          sync.Mutex
	children    map[string]*child
	idleTimeout time.Duration

	// spawn returns a STARTED command (SpawnWatch by default). Injectable so
	// tests can launch a stub like /bin/sleep instead of real specstory.
	spawn func(projectPath string) (*exec.Cmd, error)
	// now is the clock (time.Now by default); injectable so idle-reaping tests
	// don't have to actually wait out the timeout.
	now func() time.Time
}

// NewSupervisor creates a Supervisor that reaps children idle for longer than
// idleTimeout.
func NewSupervisor(idleTimeout time.Duration) *Supervisor {
	return &Supervisor{
		children:    make(map[string]*child),
		idleTimeout: idleTimeout,
		spawn:       SpawnWatch,
		now:         time.Now,
	}
}

// NotifyActivity records activity for projectPath: it refreshes the idle clock
// of a running child, respawns one that exited on its own, or spawns a new one.
func (s *Supervisor) NotifyActivity(projectPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.children[projectPath]; ok {
		if !c.exited() {
			c.lastActivity = s.now()
			return
		}
		// The child died on its own (crash, external kill) but activity is
		// back — drop the dead entry and respawn below.
		delete(s.children, projectPath)
		slog.Info("Monitor: child had exited; respawning on new activity", "project", projectPath)
	}
	s.spawnLocked(projectPath)
}

// spawnLocked starts a watch child and registers it. Caller must hold s.mu.
// A spawn failure is logged, not fatal: the next activity event retries.
func (s *Supervisor) spawnLocked(projectPath string) {
	cmd, err := s.spawn(projectPath)
	if err != nil {
		slog.Error("Monitor: failed to spawn watch child", "project", projectPath, "error", err)
		return
	}

	c := &child{cmd: cmd, lastActivity: s.now(), done: make(chan struct{})}
	s.children[projectPath] = c

	// Reap the OS process the moment it exits so it never lingers as a zombie;
	// table cleanup happens later in reapOnce/NotifyActivity, which observe
	// the closed done channel.
	go func() {
		_ = cmd.Wait()
		close(c.done)
		slog.Info("Monitor: watch child exited", "project", projectPath, "pid", cmd.Process.Pid)
	}()

	slog.Info("Monitor: spawned watch child", "project", projectPath, "pid", cmd.Process.Pid)
}

// Run drives the reap loop until ctx is cancelled. The tick interval is
// min(30s, idleTimeout/2) so short timeouts are honored promptly without
// busy-polling for long ones (floored at 1s to avoid spinning in extreme
// configurations).
func (s *Supervisor) Run(ctx context.Context) {
	interval := 30 * time.Second
	if half := s.idleTimeout / 2; half < interval {
		interval = half
	}
	if interval < time.Second {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reapOnce()
		}
	}
}

// reapOnce removes children that exited on their own and SIGTERMs children
// idle past the timeout. Termination happens off-lock and asynchronously: a
// stuck child must not stall the supervisor, and the terminator escalates to
// SIGKILL on its own.
func (s *Supervisor) reapOnce() {
	type reapTarget struct {
		project string
		c       *child
	}
	var idle []reapTarget

	s.mu.Lock()
	now := s.now()
	for project, c := range s.children {
		if c.exited() {
			delete(s.children, project)
			slog.Info("Monitor: reaped child that exited on its own", "project", project)
			continue
		}
		if now.Sub(c.lastActivity) >= s.idleTimeout {
			delete(s.children, project)
			idle = append(idle, reapTarget{project, c})
		}
	}
	s.mu.Unlock()

	for _, t := range idle {
		slog.Info("Monitor: reaping idle watch child", "project", t.project, "pid", t.c.cmd.Process.Pid, "idleTimeout", s.idleTimeout)
		go terminateChild(t.c)
	}
}

// Shutdown SIGTERMs ALL children in parallel and blocks until each has
// actually exited (bounded by the SIGKILL escalation in terminateChild).
// Robustness here is non-negotiable: children the monitor loses track of keep
// running as orphaned watchers.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	children := s.children
	s.children = make(map[string]*child)
	s.mu.Unlock()

	var wg sync.WaitGroup
	for project, c := range children {
		wg.Add(1)
		go func(project string, c *child) {
			defer wg.Done()
			slog.Info("Monitor: shutting down watch child", "project", project, "pid", c.cmd.Process.Pid)
			terminateChild(c)
		}(project, c)
	}
	wg.Wait()
	slog.Info("Monitor: all watch children shut down", "count", len(children))
}

// terminateChild asks a child to exit with SIGTERM (watch handles it
// gracefully via signal.NotifyContext), escalating to SIGKILL after the grace
// period. Blocks until the process is reaped; the post-SIGKILL wait is bounded
// only as a last-resort guard against a kernel-stuck process.
func terminateChild(c *child) {
	// Signal errors (process already gone) are expected races, not failures;
	// the done channel below is the source of truth.
	_ = c.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-c.done:
	case <-time.After(shutdownGracePeriod):
		slog.Warn("Monitor: watch child ignored SIGTERM, killing", "pid", c.cmd.Process.Pid)
		_ = c.cmd.Process.Kill()
		select {
		case <-c.done:
		case <-time.After(shutdownGracePeriod):
			// SIGKILL is not survivable; reaching here means the process is
			// stuck in the kernel. Nothing more we can do but say so.
			slog.Error("Monitor: watch child did not exit after SIGKILL", "pid", c.cmd.Process.Pid)
		}
	}
}
