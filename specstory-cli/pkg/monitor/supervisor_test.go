package monitor

import (
	"os/exec"
	"sync"
	"testing"
	"time"
)

// fakeClock is an injectable clock so idle-reaping tests never have to
// actually wait out the timeout.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// spawnCounter is a spawn stub that launches a real (but harmless) child
// process — sleep by default — instead of recursively spawning specstory.
type spawnCounter struct {
	mu    sync.Mutex
	calls int
	argv  []string
}

func newSpawnCounter(argv ...string) *spawnCounter {
	if len(argv) == 0 {
		argv = []string{"sleep", "60"}
	}
	return &spawnCounter{argv: argv}
}

func (sc *spawnCounter) spawn(projectPath string) (*exec.Cmd, error) {
	sc.mu.Lock()
	sc.calls++
	sc.mu.Unlock()
	// Match the SpawnWatch contract: return a STARTED command.
	cmd := exec.Command(sc.argv[0], sc.argv[1:]...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func (sc *spawnCounter) count() int {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.calls
}

// newTestSupervisor wires a Supervisor with fake clock + spawn stub, and
// guarantees no child outlives the test even on failure.
func newTestSupervisor(t *testing.T, idleTimeout time.Duration, sc *spawnCounter) (*Supervisor, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	s := NewSupervisor(idleTimeout)
	s.now = clock.Now
	s.spawn = sc.spawn
	t.Cleanup(s.Shutdown)
	return s, clock
}

// childFor returns the tracked child for projectPath, or nil.
func (s *Supervisor) childFor(projectPath string) *child {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.children[projectPath]
}

func (s *Supervisor) childCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.children)
}

// waitForExit fails the test if the child's process hasn't been reaped within
// a generous timeout.
func waitForExit(t *testing.T, c *child, context string) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s: child pid %d did not exit", context, c.cmd.Process.Pid)
	}
	if c.cmd.ProcessState == nil {
		t.Fatalf("%s: child done but never reaped (ProcessState nil)", context)
	}
}

func TestSupervisor_SpawnRefreshAndNoDoubleSpawn(t *testing.T) {
	sc := newSpawnCounter()
	s, clock := newTestSupervisor(t, 5*time.Minute, sc)

	s.NotifyActivity("/repo/a")
	if got := sc.count(); got != 1 {
		t.Fatalf("spawn count after first activity = %d, want 1", got)
	}
	c := s.childFor("/repo/a")
	if c == nil {
		t.Fatal("child not registered after NotifyActivity")
	}
	first := c.lastActivity

	// Repeat activity must refresh the idle clock, not spawn a second child.
	clock.Advance(1 * time.Minute)
	s.NotifyActivity("/repo/a")
	if got := sc.count(); got != 1 {
		t.Errorf("spawn count after repeat activity = %d, want 1 (no double spawn)", got)
	}
	if got := s.childFor("/repo/a").lastActivity; !got.After(first) {
		t.Errorf("lastActivity not refreshed: %v -> %v", first, got)
	}
}

func TestSupervisor_ReapIdleAndRespawn(t *testing.T) {
	sc := newSpawnCounter()
	s, clock := newTestSupervisor(t, 5*time.Minute, sc)

	s.NotifyActivity("/repo/a")
	c := s.childFor("/repo/a")

	// Not yet idle: nothing reaped.
	clock.Advance(4 * time.Minute)
	s.reapOnce()
	if s.childFor("/repo/a") == nil {
		t.Fatal("child reaped before idle timeout")
	}

	// Idle past the timeout: removed from the table and actually terminated.
	clock.Advance(2 * time.Minute)
	s.reapOnce()
	if s.childFor("/repo/a") != nil {
		t.Fatal("idle child still in table after reap")
	}
	waitForExit(t, c, "idle reap")

	// New activity after the reap respawns.
	s.NotifyActivity("/repo/a")
	if got := sc.count(); got != 2 {
		t.Errorf("spawn count after post-reap activity = %d, want 2", got)
	}
	if s.childFor("/repo/a") == nil {
		t.Error("child not respawned after reap")
	}
}

func TestSupervisor_ChildExitsOnItsOwn(t *testing.T) {
	// "true" exits immediately, simulating a crashed/killed watch child.
	sc := newSpawnCounter("true")
	s, _ := newTestSupervisor(t, 5*time.Minute, sc)

	s.NotifyActivity("/repo/a")
	c := s.childFor("/repo/a")
	if c == nil {
		t.Fatal("child not registered")
	}
	waitForExit(t, c, "self-exit")

	t.Run("reap loop clears exited child", func(t *testing.T) {
		s.reapOnce()
		if s.childFor("/repo/a") != nil {
			t.Error("exited child still in table after reapOnce")
		}
	})

	t.Run("activity respawns exited child", func(t *testing.T) {
		s.NotifyActivity("/repo/a")
		if got := sc.count(); got != 2 {
			t.Errorf("spawn count = %d, want 2 (respawn after self-exit)", got)
		}
	})
}

func TestSupervisor_ShutdownKillsAll(t *testing.T) {
	sc := newSpawnCounter()
	s, _ := newTestSupervisor(t, 5*time.Minute, sc)

	s.NotifyActivity("/repo/a")
	s.NotifyActivity("/repo/b")
	childA := s.childFor("/repo/a")
	childB := s.childFor("/repo/b")
	if childA == nil || childB == nil {
		t.Fatal("children not registered")
	}

	s.Shutdown()

	// Shutdown must not return before every child is actually gone: orphaned
	// watchers are the whole reason this method exists.
	waitForExit(t, childA, "shutdown child a")
	waitForExit(t, childB, "shutdown child b")
	if got := s.childCount(); got != 0 {
		t.Errorf("children in table after Shutdown = %d, want 0", got)
	}
}
