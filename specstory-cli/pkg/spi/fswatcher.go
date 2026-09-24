package spi

import "github.com/fsnotify/fsnotify"

// NewFSWatcher returns an fsnotify watcher that is safe to Add/Remove paths on
// from the same goroutine that reads its Events and Errors channels.
//
// Why: on Windows, fsnotify runs a single I/O goroutine that both delivers
// events/errors and services Add/Remove requests. Add and Remove block until
// that goroutine replies, and it blocks until someone receives what it is
// sending. A watch loop that calls Add while handling an event therefore
// deadlocks the moment another event or error is ready to be delivered.
// Relaying both channels through an unbounded queue keeps the I/O goroutine
// free to answer, while still delivering every event in order.
func NewFSWatcher() (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// The backend holds its own references to the original channels, so
	// swapping the exported fields leaves it sending to the relays.
	events, errs := watcher.Events, watcher.Errors
	watcher.Events = make(chan fsnotify.Event)
	watcher.Errors = make(chan error)
	go relay(events, watcher.Events)
	go relay(errs, watcher.Errors)
	return watcher, nil
}

// relay forwards values from in to out without ever blocking in on out.
// When in closes (the watcher was closed), out is closed immediately and any
// still-queued values are discarded, matching fsnotify's own Close semantics;
// waiting for a consumer that has stopped reading would leak this goroutine.
func relay[T any](in <-chan T, out chan<- T) {
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
		case send <- next:
			queue = queue[1:]
			if len(queue) == 0 {
				// Release the backing array so a past burst doesn't pin memory.
				queue = nil
			}
		}
	}
}
