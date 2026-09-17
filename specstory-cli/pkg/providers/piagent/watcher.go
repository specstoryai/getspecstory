package piagent

import (
	"context"
	"errors"
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
	watcherMutex    sync.Mutex // serializes start/stop and configuration for the next run
	activeWatcher   *piWatcher
	watcherCallback func(*spi.AgentChatSession)
	watcherDebugRaw bool
)

const (
	// Coalesce a burst before parsing its final record; reconciliation also catches
	// writes missed while a synchronous consumer was saving the previous update.
	piDebounceDelay     = 100 * time.Millisecond
	piReconcileInterval = 2 * time.Second
)

type fileStamp struct {
	size  int64
	mtime time.Time
}

// piWatcher owns one run's state. Only its worker reads or changes stamps and
// pending paths, so parsing, delivery and the final sweep cannot overtake each other.
type piWatcher struct {
	fs          *fsnotify.Watcher
	dir         string
	flat        bool
	candidates  []string
	callback    func(*spi.AgentChatSession)
	debugRaw    bool
	stamps      map[string]fileStamp
	baseline    map[string]bool // queued startup events override snapshots taken after registration
	pending     map[string]time.Time
	watched     string
	watchedInfo os.FileInfo
	cancel      context.CancelFunc
	done        chan struct{}
	wg          sync.WaitGroup
	err         error // published by closing done, after the final callback has returned
}

// SetWatcherCallback sets the callback for the next watcher run.
func SetWatcherCallback(callback func(*spi.AgentChatSession)) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = callback
}

// ClearWatcherCallback releases the callback configured for the next run.
func ClearWatcherCallback() { SetWatcherCallback(nil) }

// SetWatcherDebugRaw configures debug output for the next watcher run.
func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
}

// StopWatcher waits for the worker's final scan and every synchronous save.
// Keep the active run installed until it has joined, so a concurrent start
// cannot reuse state while an old callback is still writing history.
func StopWatcher() {
	watcherMutex.Lock()
	w := activeWatcher
	watcherMutex.Unlock()
	stopWatcher(w)
}

func stopWatcher(w *piWatcher) {
	if w == nil {
		return
	}
	watcherMutex.Lock()
	w.cancel()
	watcherMutex.Unlock()
	w.wg.Wait()
	watcherMutex.Lock()
	if activeWatcher == w {
		activeWatcher = nil
	}
	watcherMutex.Unlock()
}

// WatchForProjectDir establishes the baseline and filesystem watch before
// returning, so an immediately launched agent cannot outrun watcher startup.
func WatchForProjectDir(projectPath string) error {
	_, err := startProjectWatcher(projectPath)
	return err
}

func startProjectWatcher(projectPath string) (*piWatcher, error) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	if activeWatcher != nil {
		return nil, fmt.Errorf("pi: a session watcher is already running")
	}
	_, flat, err := piSessionsRoot()
	if err != nil {
		return nil, err
	}
	dir, err := ProjectSessionDir(projectPath)
	if err != nil {
		return nil, err
	}
	candidates, err := projectCandidates(projectPath)
	if err != nil {
		return nil, err
	}
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("pi: creating file watcher: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &piWatcher{fs: fs, dir: dir, flat: flat, candidates: candidates,
		callback: watcherCallback, debugRaw: watcherDebugRaw, stamps: make(map[string]fileStamp), baseline: make(map[string]bool),
		pending: make(map[string]time.Time), cancel: cancel, done: make(chan struct{})}
	if err := w.ensureWatch(); err != nil {
		cancel()
		_ = fs.Close()
		return nil, err
	}
	// Register before recording existing files. A write during that scan can
	// already be reflected in a baseline stamp, so its queued event must override
	// that stamp. A store absent at registration has no pre-existing history.
	if w.watched == w.dir {
		if err := w.recordBaseline(); err != nil {
			cancel()
			_ = fs.Close()
			return nil, err
		}
	}
	activeWatcher = w
	w.wg.Go(func() {
		defer close(w.done)
		defer func() { _ = fs.Close() }()
		w.err = w.run(ctx)
		if w.err != nil {
			slog.Error("WatchAgent: pi session watcher failed", "projectPath", projectPath, "error", w.err)
		}
	})
	return w, nil
}

func (w *piWatcher) recordBaseline() error {
	files, err := jsonlFilesInDir(w.dir)
	if err != nil {
		return err
	}
	for _, path := range files {
		stamp, err := stampFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		w.stamps[path] = stamp
		w.baseline[path] = true
	}
	return nil
}

// ensureWatch follows the nearest existing directory until the project store
// appears. File identity detects replacement even when the path is unchanged.
func (w *piWatcher) ensureWatch() error {
	dir := w.dir
	var info os.FileInfo
	for {
		var err error
		info, err = os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("pi: session watch path is not a directory: %s", dir)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("pi: inspecting watch directory %s: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return fmt.Errorf("pi: no existing watch ancestor for %s", w.dir)
		}
		dir = parent
	}
	if dir == w.watched && w.watchedInfo != nil && os.SameFile(info, w.watchedInfo) {
		for _, path := range w.fs.WatchList() {
			if path == dir {
				return nil
			}
		}
	}
	if w.watched != "" {
		if err := w.fs.Remove(w.watched); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			return fmt.Errorf("pi: removing directory watch %s: %w", w.watched, err)
		}
	}
	if err := w.fs.Add(dir); err != nil {
		return fmt.Errorf("pi: watching directory %s: %w", dir, err)
	}
	w.watched, w.watchedInfo = dir, info
	slog.Info("WatchAgent: watching pi session directory", "path", dir, "targetPath", w.dir)
	return nil
}

func (w *piWatcher) run(ctx context.Context) (err error) {
	// The last write may still be in the kernel's event queue at process exit.
	// A final on-disk scan, on this same worker, also flushes debounced updates.
	defer func() { err = errors.Join(err, w.reconcile()) }()
	if err := w.reconcile(); err != nil {
		return err
	}
	debounce := time.NewTicker(piDebounceDelay)
	defer debounce.Stop()
	reconcile := time.NewTicker(piReconcileInterval)
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-w.fs.Events:
			if !ok {
				return fmt.Errorf("pi: filesystem event stream closed")
			}
			// Ancestor events can announce an entire directory that arrived already
			// populated. Adopt it by scanning, without applying an mtime cutoff.
			if w.watched != w.dir || event.Name == w.dir {
				if err := w.ensureWatch(); err != nil {
					return err
				}
				if err := w.reconcile(); err != nil {
					return err
				}
			} else if filepath.Dir(event.Name) == w.dir && strings.HasSuffix(event.Name, ".jsonl") &&
				(event.Has(fsnotify.Create) || event.Has(fsnotify.Write)) {
				if w.baseline[event.Name] {
					delete(w.baseline, event.Name)
					delete(w.stamps, event.Name)
				}
				w.pending[event.Name] = time.Now().Add(piDebounceDelay)
			}
		case watchErr, ok := <-w.fs.Errors:
			if !ok {
				return fmt.Errorf("pi: filesystem error stream closed")
			}
			if !errors.Is(watchErr, fsnotify.ErrEventOverflow) {
				return fmt.Errorf("pi: filesystem watcher: %w", watchErr)
			}
			slog.Warn("WatchAgent: pi filesystem events overflowed; reconciling sessions", "error", watchErr)
			if err := w.reconcile(); err != nil {
				return err
			}
		case now := <-debounce.C:
			// ReadDir orders paths deterministically; reconciliation consumes due paths
			// together and avoids random callback order from iterating a map.
			due := false
			for _, deadline := range w.pending {
				if !now.Before(deadline) {
					due = true
					break
				}
			}
			if due {
				if err := w.reconcileDue(now); err != nil {
					return err
				}
			}
		case <-reconcile.C:
			if err := w.ensureWatch(); err != nil {
				return err
			}
			if err := w.reconcileDue(time.Now()); err != nil {
				return err
			}
		}
	}
}

// reconcile ignores debounce deadlines for startup adoption and shutdown.
func (w *piWatcher) reconcile() error { return w.reconcileDue(time.Time{}) }

func (w *piWatcher) reconcileDue(now time.Time) error {
	files, err := jsonlFilesInDir(w.dir)
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(files))
	for _, path := range files {
		present[path] = true
		if deadline, pending := w.pending[path]; pending && !now.IsZero() && now.Before(deadline) {
			continue
		}
		delete(w.pending, path)
		stamp, err := stampFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if previous, seen := w.stamps[path]; seen && previous == stamp {
			continue
		}
		w.emitSession(path, stamp)
	}
	// Prune disappeared files so replacements are adopted and a long watch does
	// not retain an ever-growing catalog of sessions that no longer exist.
	for path := range w.stamps {
		if !present[path] {
			delete(w.stamps, path)
			delete(w.baseline, path)
		}
	}
	for path := range w.pending {
		if !present[path] {
			delete(w.pending, path)
		}
	}
	return nil
}

func stampFile(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{size: info.Size(), mtime: info.ModTime()}, nil
}

func (w *piWatcher) emitSession(path string, stamp fileStamp) {
	snapshot, err := readEntries(path)
	if err != nil {
		slog.Debug("WatchAgent: pi session is not ready", "path", path, "error", err)
		return
	}
	// Filter the same snapshot we convert, before writing any debug artifacts.
	// Reopening the header could attribute a replaced file to another project.
	if !headerBelongsToProject(path, snapshot.header, w.candidates, w.flat) {
		delete(w.baseline, path)
		w.stamps[path] = stamp
		return
	}
	chat, err := snapshot.agentSession(path, w.candidates[0], w.debugRaw)
	if err != nil {
		// Leave the previous stamp in place for a partially written session so
		// reconciliation can retry even if no further filesystem event arrives.
		slog.Debug("WatchAgent: pi session is not ready", "path", path, "error", err)
		return
	}
	delete(w.baseline, path)
	w.stamps[path] = stamp
	spi.DeliverSession("Pi", w.callback, chat)
}
