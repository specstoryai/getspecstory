package musecode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var (
	// watcherCancel stops the currently running watcher goroutine; nil when no
	// watcher is running. Each start creates its own context rather than
	// sharing one made at package load, so stopping a watcher never poisons a
	// later start.
	watcherCancel        context.CancelFunc
	watcherWg            sync.WaitGroup
	watcherCallback      func(*spi.AgentChatSession)
	watcherMutex         sync.RWMutex
	watcherDebugRaw      bool
	watcherWorkspaceRoot string
)

const (
	// museStoreDepth is how many path components sit below the sessions root:
	// unlike Codex's rollout files, each session is a directory inside its day
	// directory (YYYY/MM/DD/<session-id>), so four levels belong to the store
	// layout. Anything deeper (subagent/, tool-outputs/) is inside a session.
	//
	// Muse's store is global and date-sharded, exactly the shape that caused
	// Codex's fd exhaustion, so only a trailing window of date directories is
	// ever watched — see spi.WatchWindowDays.
	museStoreDepth = 4

	// watchRefreshInterval is how often the watched set is re-evaluated, which
	// is what carries the watcher across a day rollover: the new day's
	// directory is picked up and directories that aged out are released.
	watchRefreshInterval = 10 * time.Minute

	// watchBootstrapInterval is the retry cadence used while the session store
	// does not exist yet (Muse has never run on this machine). There is no
	// directory to hang an fsnotify watch on until then.
	watchBootstrapInterval = 5 * time.Second
)

func SetWatcherCallback(callback func(*spi.AgentChatSession)) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = callback
}

func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
}

func SetWatcherWorkspaceRoot(workspaceRoot string) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherWorkspaceRoot = workspaceRoot
}

func getWatcherWorkspaceRoot() string {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherWorkspaceRoot
}

func getWatcherDebugRaw() bool {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherDebugRaw
}

// StopWatcher gracefully stops the watcher goroutine. Safe to call when no
// watcher is running.
func StopWatcher() {
	watcherMutex.Lock()
	cancel := watcherCancel
	watcherCancel = nil
	watcherMutex.Unlock()
	if cancel == nil {
		return
	}

	slog.Info("StopWatcher: Signaling Muse watcher to stop")
	cancel()
	watcherWg.Wait()
	slog.Info("StopWatcher: Muse watcher stopped")
}

// WatchMuseProject watches Muse Code's session store for transcript changes
// belonging to projectPath. The store is global rather than project-scoped, so
// every change is attributed by reading the transcript's own workspace root.
func WatchMuseProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	// Default the path before recording it as the workspace root, so sessions
	// converted by the watcher never carry an empty root.
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return err
	}

	// At most one watcher runs per process: replace any previous one before
	// publishing the new callback, so a stopping watcher can never deliver
	// into the new consumer.
	StopWatcher()

	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)

	sessionsRoot, err := GetMuseSessionsDir()
	if err != nil {
		return fmt.Errorf("failed to get muse sessions dir: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	watcherMutex.Lock()
	watcherCancel = cancel
	watcherMutex.Unlock()

	set := &watchSet{watcher: watcher, sessionsRoot: sessionsRoot, watched: make(map[string]bool)}

	watcherWg.Go(func() {
		defer func() {
			if err := watcher.Close(); err != nil {
				slog.Debug("Muse watcher: error closing watcher", "error", err)
			}
		}()

		set.refresh()

		timer := time.NewTimer(set.refreshInterval())
		defer timer.Stop()

		slog.Info("Muse watcher: watching session store", "sessionsRoot", sessionsRoot)

		for {
			select {
			case <-ctx.Done():
				slog.Info("Muse watcher: context cancelled, stopping")
				return

			case <-timer.C:
				set.refresh()
				timer.Reset(set.refreshInterval())

			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				set.handleEvent(event)

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Muse watcher error", "error", err)
			}
		}
	})

	return nil
}

// watchSet tracks the fsnotify watches currently held on the session store.
// Not synchronized: it is owned by the single watcher goroutine.
type watchSet struct {
	watcher      *fsnotify.Watcher
	sessionsRoot string
	watched      map[string]bool
}

// refreshInterval slows down once the store exists: the fast cadence only
// exists to notice a first-ever `muse` run promptly.
func (s *watchSet) refreshInterval() time.Duration {
	if info, err := os.Stat(s.sessionsRoot); err != nil || !info.IsDir() {
		return watchBootstrapInterval
	}
	return watchRefreshInterval
}

// refresh brings the watched set in line with the trailing window: adds the
// directories that should be watched now, and releases the ones that aged out.
// Called at startup and on every tick, which is what carries the watcher across
// a day rollover.
func (s *watchSet) refresh() {
	if info, err := os.Stat(s.sessionsRoot); err != nil || !info.IsDir() {
		slog.Debug("Muse watcher: sessions root not present yet", "sessionsRoot", s.sessionsRoot)
		return
	}

	desired := desiredWatchDirs(s.sessionsRoot, time.Now())

	for dir := range desired {
		s.addWatch(dir)
	}
	for dir := range s.watched {
		if !desired[dir] {
			s.removeWatch(dir)
		}
	}
}

// desiredWatchDirs returns every directory that should carry a watch right now:
// the sessions root, the date directories inside the trailing window, and the
// session directories inside those days. Only directories that exist are
// returned, so the caller never tries to watch a day Muse has not created.
func desiredWatchDirs(sessionsRoot string, now time.Time) map[string]bool {
	desired := map[string]bool{sessionsRoot: true}

	for offset := 0; offset <= spi.WatchWindowDays; offset++ {
		date := now.AddDate(0, 0, -offset)
		yearDir := filepath.Join(sessionsRoot, fmt.Sprintf("%04d", date.Year()))
		monthDir := filepath.Join(yearDir, fmt.Sprintf("%02d", int(date.Month())))
		dayDir := filepath.Join(monthDir, fmt.Sprintf("%02d", date.Day()))

		// Ancestors are watched too, so the day directory's creation is seen
		// the moment Muse starts the first session of a new day/month/year.
		for _, dir := range []string{yearDir, monthDir, dayDir} {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				desired[dir] = true
			}
		}

		entries, err := os.ReadDir(dayDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			// Session directories only; subagent transcripts live one level
			// deeper and are never sessions of their own.
			if entry.IsDir() && entry.Name() != subagentDirName {
				desired[filepath.Join(dayDir, entry.Name())] = true
			}
		}
	}

	return desired
}

func (s *watchSet) addWatch(dir string) {
	if s.watched[dir] {
		return
	}
	if err := s.watcher.Add(dir); err != nil {
		slog.Debug("Muse watcher: failed to add watch", "directory", dir, "error", err)
		return
	}
	s.watched[dir] = true
	slog.Debug("Muse watcher: added watch", "directory", dir)
}

func (s *watchSet) removeWatch(dir string) {
	if !s.watched[dir] {
		return
	}
	// Removal can fail if the directory was deleted; the kernel already
	// released its fds in that case, so just drop our bookkeeping.
	if err := s.watcher.Remove(dir); err != nil {
		slog.Debug("Muse watcher: failed to remove watch", "directory", dir, "error", err)
	}
	delete(s.watched, dir)
	slog.Debug("Muse watcher: removed watch", "directory", dir)
}

// handleEvent reacts to one fsnotify event: a new directory inside the window
// starts being watched immediately (so a session created seconds from now is
// live, not waiting for the next refresh), and a transcript write is converted
// and published.
func (s *watchSet) handleEvent(event fsnotify.Event) {
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}

	if filepath.Base(event.Name) == sessionFileName {
		if IsSubagentTranscript(event.Name) {
			return
		}
		processSessionChange(event.Name)
		return
	}

	// Anything else is only interesting when it is a directory that belongs in
	// the window: a new day directory, or a new session directory inside one.
	if !event.Has(fsnotify.Create) {
		return
	}
	if info, err := os.Stat(event.Name); err != nil || !info.IsDir() {
		return
	}
	if filepath.Base(event.Name) == subagentDirName {
		return
	}
	if !spi.DateDirWithinWatchWindow(event.Name, s.sessionsRoot, museStoreDepth, spi.WatchWindowCutoff(time.Now())) {
		return
	}
	s.addWatch(event.Name)
	s.adoptExisting(event.Name)
}

// adoptExisting reconciles a directory that appeared while the watcher was
// already running, picking up whatever Muse put inside it before the watch
// landed.
//
// fsnotify only reports what happens after a watch is added, and Muse creates a
// session by making the day directory, the session directory and the transcript
// in immediate succession. Adding a watch on the day directory is therefore too
// late for everything already inside it: the first session of a day would be
// invisible until the next refresh, by which point the transcript is fully
// written and emits no further events. Walking the new directory once on
// arrival is what makes that session show up.
func (s *watchSet) adoptExisting(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		slog.Debug("Muse watcher: could not adopt new directory", "directory", dir, "error", err)
		return
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		switch {
		case entry.IsDir():
			// Never descend past the store layout. subagent/ and tool-outputs/
			// live inside a session directory: the former holds transcripts that
			// are not sessions of their own, and watching the latter would pin an
			// open file descriptor per spilled tool output on kqueue platforms to
			// no purpose, since the transcript that matters sits in the session
			// directory itself.
			if !s.withinStoreLayout(path) {
				continue
			}
			s.addWatch(path)
			s.adoptExisting(path)
		case entry.Name() == sessionFileName:
			if !IsSubagentTranscript(path) {
				processSessionChange(path)
			}
		}
	}
}

// withinStoreLayout reports whether dir is one of the directories the store
// layout is made of (YYYY/MM/DD/<session-id> under the sessions root) rather
// than something a session created inside its own directory.
//
// This is the same depth rule handleEvent gets from
// spi.DateDirWithinWatchWindow, minus the date cutoff: callers of this have
// already established the directory is in the window, and re-applying the
// cutoff would only make walking a directory depend on today's date.
func (s *watchSet) withinStoreLayout(dir string) bool {
	rel, err := filepath.Rel(s.sessionsRoot, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	return len(strings.Split(filepath.ToSlash(rel), "/")) <= museStoreDepth
}

// processSessionChange parses a changed transcript and publishes it, but only
// when it belongs to the project being watched: Muse's store is global, so a
// session for an unrelated project can land in the same directory.
func processSessionChange(filePath string) {
	slog.Debug("processSessionChange: Detected Muse session file change", "file", filePath)

	session, err := ParseSessionFile(filePath)
	if err != nil {
		slog.Error("processSessionChange: Failed to parse session file", "file", filePath, "error", err)
		return
	}
	if len(session.Events) == 0 {
		slog.Debug("processSessionChange: Session file has no conversation events yet", "file", filePath)
		return
	}

	workspaceRoot := getWatcherWorkspaceRoot()
	if session.WorkspaceRoot == "" || spi.CanonicalizePathOrClean(session.WorkspaceRoot) != spi.CanonicalizePathOrClean(workspaceRoot) {
		slog.Debug("processSessionChange: Session belongs to another project, skipping",
			"file", filePath, "sessionRoot", session.WorkspaceRoot, "watchedRoot", workspaceRoot)
		return
	}

	agentSession := convertToAgentChatSession(session, workspaceRoot, getWatcherDebugRaw())
	triggerCallback(agentSession)
}

// triggerCallback is a helper to call the watcher callback with proper locking.
//
// Delivery is synchronous so transcript changes reach the consumer in the order
// fsnotify reported them, but a panic in the consumer is contained here: it
// would otherwise unwind the fsnotify event goroutine and take down the whole
// process over one malformed session.
func triggerCallback(agentSession *spi.AgentChatSession) {
	watcherMutex.RLock()
	cb := watcherCallback
	watcherMutex.RUnlock()

	if cb == nil || agentSession == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Error("muse: session callback panicked", "sessionId", agentSession.SessionID, "panic", r)
		}
	}()
	cb(agentSession)
}
