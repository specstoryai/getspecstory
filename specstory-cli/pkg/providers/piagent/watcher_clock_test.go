package piagent

import (
	"context"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// The OS notification goroutine lives outside the clock bubble. Tests still
// register real directory watches and parse real files, but explicitly deliver
// events on bubble-owned channels. This lets the production timers advance in
// virtual time, including when testing a completely missing filesystem event.
func withPiWatchClock(t *testing.T, test func(*testing.T, *fsnotify.Watcher)) {
	t.Helper()
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fs.Close() }()
	synctest.Test(t, func(t *testing.T) { test(t, fs) })
}

type piClockWatcher struct {
	w      *piWatcher
	events chan fsnotify.Event
}

func startPiClockWatcher(t *testing.T, fs *fsnotify.Watcher, project, dir string, callback func(*spi.AgentChatSession)) *piClockWatcher {
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
		w.err = w.runWithEvents(ctx, f.events, watchErrors)
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
