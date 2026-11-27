package spi

import (
	"errors"
	"testing"
	"time"
)

func TestRelay(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "single value", count: 1},
		{name: "burst larger than any channel buffer", count: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make(chan int)
			out := make(chan int)
			go relay(in, out, nil)

			// Nobody reads out while sending; each send must still complete.
			// This models fsnotify's I/O goroutine delivering events while the
			// consumer is blocked inside Add/Remove.
			sent := make(chan struct{})
			go func() {
				for i := range tt.count {
					in <- i
				}
				close(sent)
			}()
			select {
			case <-sent:
			case <-time.After(5 * time.Second):
				t.Fatal("relay blocked the sender while the consumer was not reading")
			}

			for want := range tt.count {
				if got := <-out; got != want {
					t.Fatalf("got %d, want %d (order not preserved)", got, want)
				}
			}
		})
	}
}

func TestRelay_ClosesOutputWhenInputCloses(t *testing.T) {
	tests := []struct {
		name   string
		queued int
	}{
		{name: "empty queue", queued: 0},
		{name: "unread values are discarded", queued: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make(chan int)
			out := make(chan int)
			go relay(in, out, nil)
			for i := range tt.queued {
				in <- i
			}
			close(in)

			// Drain whatever may have been handed over before the close was
			// observed; out must then close rather than block forever.
			deadline := time.After(5 * time.Second)
			for {
				select {
				case _, ok := <-out:
					if !ok {
						return
					}
				case <-deadline:
					t.Fatal("relay did not close its output after input closed")
				}
			}
		})
	}
}

func TestFSWatcherClose(t *testing.T) {
	errBackend := errors.New("backend failed")
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })

	tests := []struct {
		name          string
		closeBackend  func() error
		wantErr       error
		wantAbandoned bool
	}{
		{name: "clean close", closeBackend: func() error { return nil }},
		{name: "backend error is returned", closeBackend: func() error { return errBackend }, wantErr: errBackend},
		{
			name:          "stuck backend is abandoned",
			closeBackend:  func() error { <-hang; return nil },
			wantErr:       ErrFSWatcherCloseTimeout,
			wantAbandoned: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &FSWatcher{
				closeBackend: tt.closeBackend,
				closeTimeout: 50 * time.Millisecond,
				abandoned:    make(chan struct{}),
			}
			if err := w.Close(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Close() = %v, want %v", err, tt.wantErr)
			}
			select {
			case <-w.abandoned:
				if !tt.wantAbandoned {
					t.Error("watcher was abandoned after a close that returned")
				}
			default:
				if tt.wantAbandoned {
					t.Error("watcher was not abandoned, so its consumers would never see the channels close")
				}
			}
		})
	}
}

// An abandoned watcher's input never closes, so consumers depend on the relay closing the output
// itself, and fsnotify's I/O goroutine depends on it to keep accepting sends.
func TestRelay_ClosesOutputWhenAbandoned(t *testing.T) {
	in := make(chan int)
	out := make(chan int)
	abandoned := make(chan struct{})
	go relay(in, out, abandoned)
	close(abandoned)

	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("got a value after abandoning, want a closed channel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not close its output when abandoned")
	}

	select {
	case in <- 1:
	case <-time.After(5 * time.Second):
		t.Fatal("relay stopped draining its input, which would block fsnotify's I/O goroutine")
	}
}
