package grokbuild

import (
	"context"
	"errors"
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
// so older sessions are monitored by bounded periodic reconciliation instead.
const maxWatchedSessions = 20

// watchDebounce collapses the burst of writes a single turn produces. Grok
// appends to updates.jsonl on every streaming chunk, so an undebounced watcher
// would re-parse the whole transcript dozens of times per turn.
const watchDebounce = 300 * time.Millisecond

// refreshInterval picks up session directories created after the watch started.
const refreshInterval = 30 * time.Second

var (
	watcherLifecycle     sync.Mutex
	watcherErrors        chan error
	watcherCancel        context.CancelFunc
	watcherWg            sync.WaitGroup
	watcherCallback      func(*spi.AgentChatSession)
	watcherMutex         sync.RWMutex
	watcherDebugRaw      bool
	watcherWorkspaceRoot string
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

// StopWatcher drains changes from disk and joins all callback delivery.
func StopWatcher() {
	watcherLifecycle.Lock()
	defer watcherLifecycle.Unlock()
	stopWatcherLocked()
}

func stopWatcherLocked() {
	if watcherCancel != nil {
		watcherCancel()
		watcherWg.Wait()
		watcherCancel = nil
	}
}

// WatchGrokProject establishes the baseline before returning to the caller.
// A new native session can finish immediately after the agent is launched.
func WatchGrokProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	watcherLifecycle.Lock()
	defer watcherLifecycle.Unlock()
	stopWatcherLocked()
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return err
	}
	sessionsDir, err := GetGrokSessionsDir()
	if err != nil {
		return err
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create Grok watcher: %w", err)
	}
	state := &grokWatchState{
		watcher: watcher, projectPath: projectPath, sessionsDir: sessionsDir,
		watched: map[string]bool{}, signatures: map[string]sessionSignature{}, pending: map[string]bool{},
	}
	started := time.Now()
	// Capture before installing watches, then reconcile again after registration.
	// The start time also catches a write that races the initial stat itself.
	if err := state.refresh(true, started); err != nil {
		_ = watcher.Close()
		return err
	}
	if err := state.refresh(false, started); err != nil {
		_ = watcher.Close()
		return err
	}
	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)
	ctx, cancel := context.WithCancel(context.Background())
	watcherCancel = cancel
	watcherErrors = make(chan error, 1)
	failures := watcherErrors
	watcherWg.Go(func() {
		defer func() { _ = watcher.Close() }()
		if err := state.run(ctx); err != nil {
			failures <- err
		}
	})
	return nil
}

// File signatures include all sidecars that affect conversion. Periodic stat
// reconciliation catches dormant sessions outside the bounded fsnotify set.
type nativeFileSignature struct {
	size     int64
	modified time.Time
	exists   bool
}
type sessionSignature [4]nativeFileSignature

func signatureFor(dir string) sessionSignature {
	var result sessionSignature
	for i, name := range []string{chatHistoryFile, updatesFile, eventsFile, summaryFile} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			result[i] = nativeFileSignature{info.Size(), info.ModTime(), true}
		}
	}
	return result
}

type grokWatchState struct {
	watcher                            *fsnotify.Watcher
	projectPath, sessionsDir, groupDir string
	watched                            map[string]bool
	signatures                         map[string]sessionSignature
	pending                            map[string]bool
}

func (s *grokWatchState) refresh(baseline bool, started time.Time) error {
	groupDir, err := ResolveGrokProjectDir(s.projectPath)
	if err != nil {
		var missing *GrokPathError
		if !errors.As(err, &missing) {
			return err
		}
	}
	s.groupDir = groupDir
	desired := map[string]bool{}
	if groupDir != "" {
		desired = desiredWatchDirs(groupDir)
		desired[groupDir] = true
		entries, err := os.ReadDir(groupDir)
		if err != nil {
			return err
		}
		current := map[string]sessionSignature{}
		for _, entry := range entries {
			if !entry.IsDir() || !uuidLike.MatchString(entry.Name()) {
				continue
			}
			dir := filepath.Join(groupDir, entry.Name())
			signature := signatureFor(dir)
			previous, known := s.signatures[dir]
			if !baseline && (!known || previous != signature) {
				s.pending[dir] = true
			}
			if baseline {
				for _, file := range signature {
					if file.exists && !file.modified.Before(started) {
						s.pending[dir] = true
					}
				}
			}
			current[dir] = signature
		}
		s.signatures = current
	}
	// Watch the nearest existing ancestor until the project store appears.
	ancestor := s.sessionsDir
	for {
		info, err := os.Stat(ancestor)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("grok watch path %q is not a directory", ancestor)
			}
			desired[ancestor] = true
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("no existing ancestor for %q", s.sessionsDir)
		}
		ancestor = parent
	}
	for dir := range s.watched {
		if !desired[dir] {
			_ = s.watcher.Remove(dir)
			delete(s.watched, dir)
		}
	}
	for dir := range desired {
		if s.watched[dir] {
			continue
		}
		if err := s.watcher.Add(dir); err != nil {
			return fmt.Errorf("watch Grok directory %q: %w", dir, err)
		}
		s.watched[dir] = true
	}
	return nil
}

func (s *grokWatchState) flush() {
	dirs := make([]string, 0, len(s.pending))
	for dir := range s.pending {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		processSessionChange(dir)
		delete(s.pending, dir)
	}
}

func (s *grokWatchState) run(ctx context.Context) error {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	debounce := time.NewTimer(watchDebounce)
	defer debounce.Stop()
	for {
		select {
		case <-ctx.Done():
			// fsnotify may still have unread events when the child exits. The final
			// disk reconciliation captures those writes before callbacks are joined.
			err := s.refresh(false, time.Time{})
			s.flush()
			return err
		case <-ticker.C:
			if err := s.refresh(false, time.Time{}); err != nil {
				return err
			}
			s.flush()
		case event, ok := <-s.watcher.Events:
			if !ok {
				return fmt.Errorf("grok filesystem event stream closed")
			}
			if dir := sessionDirFor(s.groupDir, event.Name); dir != "" && isTranscriptChange(event) {
				s.pending[dir] = true
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove) {
				if err := s.refresh(false, time.Time{}); err != nil {
					return err
				}
			}
			if len(s.pending) > 0 {
				debounce.Reset(watchDebounce)
			}
		case <-debounce.C:
			if err := s.refresh(false, time.Time{}); err != nil {
				return err
			}
			s.flush()
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return fmt.Errorf("grok filesystem error stream closed")
			}
			slog.Warn("Grok filesystem event error; reconciling", "error", err)
			if err := s.refresh(false, time.Time{}); err != nil {
				return err
			}
			s.flush()
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

	spi.DeliverSession("grok", callback, agentSession)
}
