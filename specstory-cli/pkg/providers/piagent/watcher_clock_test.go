package piagent

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// OS watchers must never cross the clock bubble boundary: on Windows even
// Add/Remove exchange messages with a background goroutine over reply channels
// created by the caller. Keep registration and delivery entirely in memory;
// the other watcher tests cover the real fsnotify integration without synctest.
func withPiWatchClock(t *testing.T, test func(*testing.T, *piClockDirectoryWatcher)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		fs := &piClockDirectoryWatcher{paths: make(map[string]bool)}
		t.Cleanup(func() { _ = fs.Close() })
		test(t, fs)
	})
}

type piClockDirectoryWatcher struct {
	mu     sync.Mutex
	paths  map[string]bool
	closed bool
}

func (fs *piClockDirectoryWatcher) Add(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.closed {
		return fsnotify.ErrClosed
	}
	fs.paths[path] = true
	return nil
}

func (fs *piClockDirectoryWatcher) Remove(path string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !fs.paths[path] {
		return fsnotify.ErrNonExistentWatch
	}
	delete(fs.paths, path)
	return nil
}

func (fs *piClockDirectoryWatcher) WatchList() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	paths := make([]string, 0, len(fs.paths))
	for path := range fs.paths {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func (fs *piClockDirectoryWatcher) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.closed = true
	clear(fs.paths)
	return nil
}

type piClockWatcher struct {
	w      *piWatcher
	events chan fsnotify.Event
}

func startPiClockWatcher(t *testing.T, fs *piClockDirectoryWatcher, project, dir string, callback func(*spi.AgentChatSession)) *piClockWatcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &piWatcher{
		fs: fs, dir: dir, flat: true, candidates: []string{project}, callback: callback,
		stamps: make(map[string]fileStamp), baseline: make(map[string]bool), pending: make(map[string]time.Time),
		cancel: cancel, done: make(chan struct{}),
	}
	if err := w.ensureWatch(); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := w.recordBaseline(); err != nil {
		cancel()
		t.Fatal(err)
	}
	watcherMutex.Lock()
	if activeWatcher != nil {
		watcherMutex.Unlock()
		cancel()
		t.Fatal("previous watcher is still active")
	}
	activeWatcher = w
	watcherMutex.Unlock()
	f := &piClockWatcher{w: w, events: make(chan fsnotify.Event, 16)}
	watchErrors := make(chan error)
	w.wg.Go(func() {
		defer close(w.done)
		w.err = w.run(ctx, f.events, watchErrors)
	})
	t.Cleanup(func() {
		stopWatcher(w)
		if w.err != nil {
			t.Errorf("watcher failed: %v", w.err)
		}
	})
	// Startup reconciliation and timer registration must finish before the test
	// writes anything; otherwise startup could mask missing-event/timer bugs.
	synctest.Wait()
	return f
}

func (f *piClockWatcher) write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	f.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
	synctest.Wait()
}

func waitForPiSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(failure)
	}
}
