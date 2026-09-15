package qwencode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var (
	// Starting and stopping share a lock so Wait cannot race a new Go call.
	watcherLifecycle sync.Mutex
	activeWatcher    *qwenWatcher
	watcherMutex     sync.RWMutex
	watcherDebugRaw  bool
)

type fileStamp struct {
	size  int64
	mtime time.Time
}

// qwenWatcher owns one start's state. Only its worker reads or writes stamps
// and watches; callbacks run in that worker, in order, and Stop joins it.
type qwenWatcher struct {
	watcher     *fsnotify.Watcher
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	projectPath string
	chatsDir    string
	ancestor    string
	stamps      map[string]fileStamp
	callback    func(*spi.AgentChatSession)
	debugRaw    bool
}

func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
}

// StopWatcher performs a final disk reconciliation before returning. A child
// can write and exit before fsnotify delivers its last event, so draining only
// the event queue is insufficient. Synchronous delivery also joins every save.
func StopWatcher() {
	watcherLifecycle.Lock()
	defer watcherLifecycle.Unlock()
	if activeWatcher == nil {
		return
	}
	activeWatcher.cancel()
	activeWatcher.wg.Wait()
	activeWatcher = nil
	slog.Info("StopWatcher: Qwen watcher stopped; saves drained")
}

// WatchQwenProject arms watches before returning. Existing files establish a
// silent baseline; files appearing during bootstrap are adopted by reconcile.
func WatchQwenProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	watcherLifecycle.Lock()
	defer watcherLifecycle.Unlock()
	if activeWatcher != nil {
		return fmt.Errorf("qwen watcher is already running")
	}
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return err
	}
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(projectsDir, SanitizeQwenCwd(projectPath), "chats")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create Qwen watcher: %w", err)
	}
	watcherMutex.RLock()
	debugRaw := watcherDebugRaw
	watcherMutex.RUnlock()
	ctx, cancel := context.WithCancel(context.Background())
	w := &qwenWatcher{watcher: watcher, cancel: cancel, projectPath: projectPath, chatsDir: dir, stamps: make(map[string]fileStamp), callback: callback, debugRaw: debugRaw}
	if err := w.reconcile(true); err != nil {
		cancel()
		_ = watcher.Close()
		return err
	}
	activeWatcher = w
	w.wg.Go(func() {
		defer func() { _ = watcher.Close() }()
		// New files in this flat store are found by the safety-net scan. Watching
		// chats itself would hold one kqueue descriptor per historical file on macOS.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		reconcile := func() {
			if err := w.reconcile(false); err != nil {
				slog.Warn("Qwen watcher: Reconcile failed", "path", dir, "error", err)
			}
		}
		reconcile()
		for {
			select {
			case <-ctx.Done():
				reconcile()
				return
			case <-ticker.C:
				reconcile()
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Dir(event.Name) == dir && isSessionFile(filepath.Base(event.Name)) && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
					// Always deliver relevant events, even if the filesystem's timestamp
					// resolution hides a same-size rewrite. Content dedup belongs to cmd.
					w.emit(event.Name)
				}
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					reconcile()
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Warn("Qwen watcher: Filesystem event error", "error", err)
				reconcile()
			}
		}
	})
	slog.Info("WatchQwenProject: Qwen watcher armed", "projectPath", projectPath, "path", dir)
	return nil
}

// reconcile maintains watches only on this project's recent transcript files
// and one ancestor directory. Metadata scans stop at chats/*.jsonl. Old files
// acquire a watch again when their mtime advances after an external resume.
func (w *qwenWatcher) reconcile(baseline bool) error {
	ancestor := filepath.Dir(w.chatsDir)
	for {
		info, err := os.Stat(ancestor)
		if err == nil && info.IsDir() {
			break
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stat Qwen ancestor: %w", err)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("no directory to watch for %s", w.chatsDir)
		}
		ancestor = parent
	}
	if ancestor != w.ancestor {
		if err := w.watcher.Add(ancestor); err != nil {
			return fmt.Errorf("watch Qwen ancestor %q: %w", ancestor, err)
		}
		if w.ancestor != "" {
			_ = w.watcher.Remove(w.ancestor)
		}
		w.ancestor = ancestor
	}
	entries, err := os.ReadDir(w.chatsDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read Qwen chats: %w", err)
	}
	cutoff := spi.WatchWindowCutoff(time.Now())
	live := make(map[string]bool)
	watches := make(map[string]bool)
	for _, path := range w.watcher.WatchList() {
		watches[path] = true
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !isSessionFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(w.chatsDir, entry.Name())
		live[path] = true
		stamp := fileStamp{info.Size(), info.ModTime()}
		previous, seen := w.stamps[path]
		if !watches[path] {
			if err := w.watcher.Add(path); err != nil {
				slog.Debug("Qwen watcher: File unavailable for watch", "path", path, "error", err)
			}
		}
		if baseline {
			w.stamps[path] = stamp
		} else if !seen || previous != stamp {
			w.emit(path)
		}
	}
	for path := range w.stamps {
		if !live[path] {
			delete(w.stamps, path)
			_ = w.watcher.Remove(path)
		}
	}
	return nil
}

func (w *qwenWatcher) emit(path string) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	// Stamp before parsing: a write during parsing is still detected next time.
	w.stamps[path] = fileStamp{info.Size(), info.ModTime()}
	session, err := ParseSessionFile(path)
	if err != nil {
		slog.Debug("Qwen watcher: Session unavailable", "path", path, "error", err)
		return
	}
	if !sessionBelongsToProject(session, w.projectPath) {
		return
	}
	deliverSession(w.callback, convertToAgentChatSession(session, w.projectPath, w.debugRaw))
}

func deliverSession(callback func(*spi.AgentChatSession), session *spi.AgentChatSession) {
	if callback == nil || session == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Qwen watcher: Session callback panicked", "sessionId", session.SessionID, "panic", r)
		}
	}()
	callback(session)
}
