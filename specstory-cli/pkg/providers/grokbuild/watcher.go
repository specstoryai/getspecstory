package grokbuild

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// maxWatchedSessions bounds how many session directories are watched at once.
//
// Why: on macOS fsnotify's kqueue backend holds an open file descriptor per file
// in a watched directory, and a Grok session directory holds around twenty files.
// Watching a project's whole history would pin descriptors in proportion to it,
// which is the failure that exhausted the system file table for Codex and Claude
// (changelog v2.6.0). Recent sessions are the only ones that receive writes.
const maxWatchedSessions = 20

// watchDebounce collapses the burst of writes a single turn produces. Grok
// appends to updates.jsonl on every streaming chunk, so an undebounced watcher
// would re-parse the whole transcript dozens of times per turn.
const watchDebounce = 300 * time.Millisecond

// refreshInterval picks up session directories created after the watch started.
const refreshInterval = 30 * time.Second

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

func StopWatcher() {
	watcherCancel()
	watcherWg.Wait()
}

// WatchGrokProject watches a project's Grok sessions for changes.
//
// The store may not exist yet on a machine that has never run Grok, and the
// project's own group directory appears only on its first session, so the
// watcher walks down the chain waiting for each level in turn.
func WatchGrokProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return err
	}

	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)

	sessionsDir, err := GetGrokSessionsDir()
	if err != nil {
		return fmt.Errorf("failed to resolve the Grok sessions directory: %w", err)
	}
	grokHome := filepath.Dir(sessionsDir)

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()

		if err := waitForDirectory(watcherCtx, grokHome, "Grok home directory"); err != nil {
			slog.Debug("Grok watcher: stopped waiting for the home directory", "error", err)
			return
		}
		if err := waitForDirectory(watcherCtx, sessionsDir, "Grok sessions directory"); err != nil {
			slog.Debug("Grok watcher: stopped waiting for the sessions directory", "error", err)
			return
		}

		groupDir, err := waitForProjectDir(watcherCtx, sessionsDir, projectPath)
		if err != nil {
			slog.Debug("Grok watcher: stopped waiting for the project directory", "error", err)
			return
		}

		watchSessions(watcherCtx, groupDir)
	}()

	return nil
}

// waitForProjectDir waits for the group directory holding this project's
// sessions. Matching is by decoded working directory rather than by encoding the
// project path, so it stays correct whatever Grok's escape table does.
func waitForProjectDir(ctx context.Context, sessionsDir, projectPath string) (string, error) {
	if dir, err := ResolveGrokProjectDir(projectPath); err == nil {
		return dir, nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return "", fmt.Errorf("failed to create a watcher for the sessions directory: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(sessionsDir); err != nil {
		return "", fmt.Errorf("failed to watch %q: %w", sessionsDir, err)
	}

	// Re-check after adding the watch to close the race window.
	if dir, err := ResolveGrokProjectDir(projectPath); err == nil {
		return dir, nil
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return "", fmt.Errorf("the sessions directory watch closed unexpectedly")
			}
			if !event.Has(fsnotify.Create) {
				continue
			}
			// Re-resolve on any creation rather than inspecting the new entry:
			// the group directory can appear before its first session does.
			if dir, err := ResolveGrokProjectDir(projectPath); err == nil {
				return dir, nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return "", fmt.Errorf("the sessions directory watch closed unexpectedly")
			}
			return "", fmt.Errorf("watch error on the sessions directory: %w", err)
		}
	}
}

// watchSessions watches the group directory plus a bounded set of recent session
// directories, refreshing that set as new sessions appear.
func watchSessions(ctx context.Context, groupDir string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Error("Grok watcher: failed to create watcher", "error", err)
		return
	}
	defer func() { _ = watcher.Close() }()

	// Watching the group directory means a brand new session is seen immediately.
	if err := watcher.Add(groupDir); err != nil {
		slog.Error("Grok watcher: failed to watch the project directory", "dir", groupDir, "error", err)
		return
	}

	watched := map[string]bool{}
	syncWatches(watcher, groupDir, watched)

	pending := map[string]bool{}
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	// One timer, reset per burst, rather than a fresh time.After per event.
	// Grok appends to updates.jsonl on every streaming chunk, so a timer per
	// event would allocate thousands of short-lived timers across a session.
	// It starts stopped and drained: nothing is pending yet.
	debounce := time.NewTimer(watchDebounce)
	if !debounce.Stop() {
		<-debounce.C
	}
	defer debounce.Stop()

	// resetDebounce restarts the quiet period, draining a already-fired timer
	// first so the next receive reflects this burst rather than the last one.
	resetDebounce := func() {
		if !debounce.Stop() {
			select {
			case <-debounce.C:
			default:
			}
		}
		debounce.Reset(watchDebounce)
	}

	// flush processes everything the debounce is holding. It runs on shutdown as
	// well as on the timer, because otherwise the final turn of a session, which
	// is the one that just triggered the exit, would never be saved.
	flush := func() {
		for dir := range pending {
			processSessionChange(dir)
			delete(pending, dir)
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return

		case <-ticker.C:
			syncWatches(watcher, groupDir, watched)

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if dir := sessionDirFor(groupDir, event.Name); dir != "" {
				if event.Has(fsnotify.Create) && !watched[dir] {
					// A new session started; watch it now rather than at the next tick.
					syncWatches(watcher, groupDir, watched)
				}
				if isTranscriptChange(event) {
					pending[dir] = true
					resetDebounce()
				}
			}

		case <-debounce.C:
			flush()

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("Grok watcher: watch error", "error", err)
		}
	}
}

// isTranscriptChange reports whether an event touches a file that changes what
// the markdown should say. The sidecars that churn constantly during a turn but
// carry nothing renderable are deliberately excluded.
func isTranscriptChange(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) {
		return false
	}
	switch filepath.Base(event.Name) {
	case chatHistoryFile, updatesFile, summaryFile, eventsFile:
		return true
	default:
		return false
	}
}

// sessionDirFor maps a changed path back to the session directory it belongs to,
// or returns empty when the path is not inside a session of this project.
func sessionDirFor(groupDir, path string) string {
	dir := filepath.Dir(path)
	if dir == groupDir {
		// The path is the session directory itself (a creation event).
		if uuidLike.MatchString(filepath.Base(path)) {
			return path
		}
		return ""
	}
	if filepath.Dir(dir) != groupDir || !uuidLike.MatchString(filepath.Base(dir)) {
		return ""
	}
	return dir
}

// syncWatches keeps watches on the most recently updated session directories and
// drops the ones that age out, so descriptor use stays flat however long a watch
// runs.
func syncWatches(watcher *fsnotify.Watcher, groupDir string, watched map[string]bool) {
	desired := desiredWatchDirs(groupDir)

	for dir := range watched {
		if !desired[dir] {
			_ = watcher.Remove(dir)
			delete(watched, dir)
		}
	}
	for dir := range desired {
		if watched[dir] {
			continue
		}
		if err := watcher.Add(dir); err != nil {
			slog.Debug("Grok watcher: failed to watch session directory", "dir", dir, "error", err)
			continue
		}
		watched[dir] = true
	}
}

// desiredWatchDirs returns the session directories worth watching: the most
// recently modified, capped at maxWatchedSessions, excluding subagents.
func desiredWatchDirs(groupDir string) map[string]bool {
	entries, err := os.ReadDir(groupDir)
	if err != nil {
		return map[string]bool{}
	}

	type candidate struct {
		path    string
		modTime time.Time
	}

	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() || !uuidLike.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(groupDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Prefer the transcript's own mtime; the directory's changes for
		// unrelated reasons such as lock files.
		modTime := info.ModTime()
		if transcript, err := os.Stat(filepath.Join(path, chatHistoryFile)); err == nil {
			modTime = transcript.ModTime()
		}
		candidates = append(candidates, candidate{path: path, modTime: modTime})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	desired := map[string]bool{}
	for i, c := range candidates {
		if i >= maxWatchedSessions {
			break
		}
		desired[c.path] = true
	}
	return desired
}

// processSessionChange re-parses a session and publishes it.
//
// The transcript is always re-read whole. Grok rewrites chat_history.jsonl on
// compaction and rewind, so it can shrink, and a stored byte offset would then
// read garbage.
func processSessionChange(dir string) {
	session, err := ParseSessionDir(dir)
	if err != nil {
		slog.Debug("Grok watcher: failed to parse session", "dir", dir, "error", err)
		return
	}
	if len(session.Records) == 0 {
		return
	}
	// A session directory can exist for several seconds before summary.json
	// lands, and session_kind is the only thing that marks a subagent. Holding
	// back until the metadata arrives is what stops a spawned subagent from
	// being published as a session of its own.
	if session.Cwd == "" {
		slog.Debug("Grok watcher: waiting for session metadata", "dir", dir)
		return
	}
	if session.IsSubagent() {
		return
	}

	agentSession := convertToAgentChatSession(session, getWatcherWorkspaceRoot(), getWatcherDebugRaw())
	triggerCallback(agentSession)
}

// triggerCallback delivers a session to the watcher callback under the lock.
//
// Delivery is synchronous so transcript changes reach the consumer in the order
// fsnotify reported them, but a panic in the consumer is contained here: it
// would otherwise unwind the fsnotify event goroutine and take down the whole
// process over one malformed session.
func triggerCallback(agentSession *spi.AgentChatSession) {
	watcherMutex.RLock()
	callback := watcherCallback
	watcherMutex.RUnlock()

	if callback == nil || agentSession == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Error("grok: session callback panicked", "sessionId", agentSession.SessionID, "panic", r)
		}
	}()
	callback(agentSession)
}

// waitForDirectory blocks until a directory exists, watching its parent.
func waitForDirectory(ctx context.Context, dir string, label string) error {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return nil
	}

	parent := filepath.Dir(dir)
	child := filepath.Base(dir)

	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("parent directory %q does not exist for the %s: %w", parent, label, err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create a watcher for the %s: %w", label, err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(parent); err != nil {
		return fmt.Errorf("failed to watch %q for the %s: %w", parent, label, err)
	}

	// Re-check after adding the watch to close the race window.
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("the watch on %q closed unexpectedly", parent)
			}
			if event.Has(fsnotify.Create) && filepath.Base(event.Name) == child {
				return nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("the watch on %q closed unexpectedly", parent)
			}
			return fmt.Errorf("watch error for the %s: %w", label, err)
		}
	}
}
