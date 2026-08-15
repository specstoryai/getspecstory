package musecode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var (
	watcherCtx           context.Context
	watcherCancel        context.CancelFunc
	watcherWg            sync.WaitGroup
	watcherCallback      func(*spi.AgentChatSession)
	watcherMutex         sync.RWMutex
	watcherDebugRaw      bool
	watcherWorkspaceRoot string
)

func init() {
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
}

const (
	// watchWindowDays is how many days of lookback before today stay under an
	// active fsnotify watch; the window also includes today, so up to
	// watchWindowDays+1 date directories are watched at once.
	//
	// Muse's store is global and date-sharded, exactly the shape that caused
	// Codex's fd exhaustion (changelog v2.6.0): fsnotify's kqueue backend on
	// macOS holds an open file descriptor for every file in a watched
	// directory, so watching all history pins one fd per session ever
	// recorded. The trailing window keeps fd usage flat while still
	// live-streaming sessions that span midnight or were resumed outside
	// SpecStory.
	watchWindowDays = 7

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

// StopWatcher gracefully stops the watcher goroutine.
func StopWatcher() {
	slog.Info("StopWatcher: Signaling Muse watcher to stop")
	watcherCancel()
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

	set := &watchSet{watcher: watcher, sessionsRoot: sessionsRoot, watched: make(map[string]bool)}

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()
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
			case <-watcherCtx.Done():
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
	}()

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

	for offset := 0; offset <= watchWindowDays; offset++ {
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
	if !dirWithinWatchWindow(event.Name, s.sessionsRoot, watchWindowCutoff(time.Now())) {
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
			// Subagent transcripts are never sessions of their own.
			if entry.Name() == subagentDirName {
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

// watchWindowCutoff returns the earliest date (local midnight) whose directory
// is still watched.
func watchWindowCutoff(now time.Time) time.Time {
	year, month, day := now.AddDate(0, 0, -watchWindowDays).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}

// museDirDepth returns how deep a directory sits under the sessions root:
// 1 = year, 2 = month, 3 = day, 4 = session directory. 0 means the path is not
// part of the store layout (which includes anything inside a session, such as
// subagent/ and tool-outputs/).
func museDirDepth(path string, sessionsRoot string) int {
	rel, err := filepath.Rel(sessionsRoot, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return 0
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 4 {
		return 0
	}
	return len(parts)
}

// dirWithinWatchWindow reports whether a store directory could hold sessions
// dated on or after cutoff, and therefore deserves an fsnotify watch. A session
// directory inherits the date of the day directory holding it.
func dirWithinWatchWindow(path string, sessionsRoot string, cutoff time.Time) bool {
	depth := museDirDepth(path, sessionsRoot)
	if depth == 0 {
		return false
	}

	rel, err := filepath.Rel(sessionsRoot, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")

	year, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	if depth == 1 {
		return year >= cutoff.Year()
	}

	month, err := strconv.Atoi(parts[1])
	if err != nil || month < 1 || month > 12 {
		return false
	}
	if depth == 2 {
		return year > cutoff.Year() || (year == cutoff.Year() && time.Month(month) >= cutoff.Month())
	}

	day, err := strconv.Atoi(parts[2])
	if err != nil {
		return false
	}
	date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, cutoff.Location())
	// time.Date normalizes out-of-range values (Feb 30 becomes March 2); a
	// round-trip mismatch means the components were not a real calendar date.
	if date.Year() != year || date.Month() != time.Month(month) || date.Day() != day {
		return false
	}
	return !date.Before(cutoff)
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
