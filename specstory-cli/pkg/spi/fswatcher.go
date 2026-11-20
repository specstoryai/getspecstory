package spi

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// fsWatcherCloseTimeout is how long Close waits for fsnotify. A healthy close takes milliseconds,
// so this only needs to be short enough that nobody mistakes a stuck shutdown for a slow one.
const fsWatcherCloseTimeout = 5 * time.Second

// ErrFSWatcherCloseTimeout means Close gave up on fsnotify and abandoned the watcher.
var ErrFSWatcherCloseTimeout = errors.New("timed out closing filesystem watcher")

// FSWatcher is an fsnotify watcher that is safe to Add/Remove on from the goroutine reading its
// channels, and whose Close always returns. Use NewFSWatcher to create one.
type FSWatcher struct {
	*fsnotify.Watcher

	closeBackend func() error
	closeTimeout time.Duration
	abandoned    chan struct{}
	abandonOnce  sync.Once
}

// NewFSWatcher returns a watcher that is safe to Add/Remove paths on from the same goroutine that
// reads its Events and Errors channels.
//
// Why: on Windows, fsnotify runs a single I/O goroutine that both delivers
// events/errors and services Add/Remove requests. Add and Remove block until
// that goroutine replies, and it blocks until someone receives what it is
// sending. A watch loop that calls Add while handling an event therefore
// deadlocks the moment another event or error is ready to be delivered.
// Relaying both channels through an unbounded queue keeps the I/O goroutine
// free to answer, while still delivering every event in order.
func NewFSWatcher() (*FSWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// The backend holds its own references to the original channels, so
	// swapping the exported fields leaves it sending to the relays.
	events, errs := watcher.Events, watcher.Errors
	watcher.Events = make(chan fsnotify.Event)
	watcher.Errors = make(chan error)
	w := &FSWatcher{
		Watcher:      watcher,
		closeBackend: watcher.Close,
		closeTimeout: fsWatcherCloseTimeout,
		abandoned:    make(chan struct{}),
	}
	go relay(events, watcher.Events, w.abandoned)
	go relay(errs, watcher.Errors, w.abandoned)
	return w, nil
}

// Close shuts the watcher down, giving up after a timeout rather than hanging the caller.
//
// Why: on Windows, fsnotify's sendError can consume the signal Close is waiting on, leaving Close
// blocked forever if an error is being delivered as it starts. Watch loops close the watcher on
// their way out and Stop methods wait for those loops, so that one lost signal would hang shutdown.
//
// Giving up closes Events and Errors as a normal Close does, since many watch loops only exit on
// that. It leaks fsnotify's goroutines and handles, which is acceptable because it is rare and
// callers are shutting down.
func (w *FSWatcher) Close() error {
	closed := make(chan error, 1)
	go func() { closed <- w.closeBackend() }()

	select {
	case err := <-closed:
		return err
	case <-time.After(w.closeTimeout):
		slog.Warn("Timed out closing filesystem watcher; abandoning it", "timeout", w.closeTimeout)
		w.abandonOnce.Do(func() { close(w.abandoned) })
		return ErrFSWatcherCloseTimeout
	}
}

// relay forwards values from in to out without ever blocking in on out.
// When in closes (the watcher was closed), out is closed immediately and any
// still-queued values are discarded, matching fsnotify's own Close semantics;
// waiting for a consumer that has stopped reading would leak this goroutine.
//
// abandoned does the same for a watcher whose Close gave up, when in may never close. in is still
// drained so that fsnotify's I/O goroutine can't block on a send, leaving it free to release a
// watch when its directory is deleted.
func relay[T any](in <-chan T, out chan<- T, abandoned <-chan struct{}) {
	defer close(out)
	var queue []T
	for {
		// A nil channel never becomes ready, which disables the send case
		// while there is nothing queued.
		var send chan<- T
		var next T
		if len(queue) > 0 {
			send = out
			next = queue[0]
		}
		select {
		case value, ok := <-in:
			if !ok {
				return
			}
			queue = append(queue, value)
		case <-abandoned:
			go func() {
				for range in {
				}
			}()
			return
		case send <- next:
			queue = queue[1:]
			if len(queue) == 0 {
				// Release the backing array so a past burst doesn't pin memory.
				queue = nil
			}
		}
	}
}
