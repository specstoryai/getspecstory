package spi

import (
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
			go relay(in, out)

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
			go relay(in, out)
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
