package monitor

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// eventRecorder collects callback invocations thread-safely for assertions.
type eventRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *eventRecorder) record(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
}

func (r *eventRecorder) contains(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.paths {
		if p == path {
			return true
		}
	}
	return false
}

// count returns how many times path has been recorded, so tests can assert
// that NEW events keep arriving after a state transition (contains alone
// cannot distinguish old notifications from fresh ones).
func (r *eventRecorder) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, p := range r.paths {
		if p == path {
			n++
		}
	}
	return n
}

// waitFor polls cond until it is true or the (deliberately generous, for flake
// resistance) timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// startRootWatch runs WatchRootForNewEntries in a goroutine and gives the
// watcher a moment to establish before the test generates events.
func startRootWatch(t *testing.T, ctx context.Context, root string, onCreate func(string)) chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- WatchRootForNewEntries(ctx, root, onCreate)
	}()
	// The initial watch registration happens synchronously inside the
	// goroutine; a short settle keeps event generation from racing it.
	time.Sleep(250 * time.Millisecond)
	return errCh
}

func TestWatchRootForNewEntries(t *testing.T) {
	tests := []struct {
		name string
		// setup runs before the watcher starts (pre-existing state).
		setup func(t *testing.T, root string)
		// act generates events and returns the path the callback must see.
		act func(t *testing.T, root string) string
	}{
		{
			name:  "file created in root",
			setup: func(t *testing.T, root string) {},
			act: func(t *testing.T, root string) string {
				p := filepath.Join(root, "new-entry")
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatalf("failed to create file: %v", err)
				}
				return p
			},
		},
		{
			name:  "directory created in root",
			setup: func(t *testing.T, root string) {},
			act: func(t *testing.T, root string) string {
				p := filepath.Join(root, "new-dir")
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
				return p
			},
		},
		{
			name: "file created inside pre-existing subdirectory",
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
					t.Fatalf("failed to create subdir: %v", err)
				}
			},
			act: func(t *testing.T, root string) string {
				p := filepath.Join(root, "sub", "inner-file")
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatalf("failed to create file: %v", err)
				}
				return p
			},
		},
		{
			name:  "file created inside newly created subdirectory",
			setup: func(t *testing.T, root string) {},
			act: func(t *testing.T, root string) string {
				sub := filepath.Join(root, "made-later")
				if err := os.Mkdir(sub, 0o755); err != nil {
					t.Fatalf("failed to create dir: %v", err)
				}
				// Give the watcher time to pick up the new directory before
				// writing into it (mirrors real agent behavior, which is never
				// instantaneous either).
				time.Sleep(250 * time.Millisecond)
				p := filepath.Join(sub, "later-file")
				if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
					t.Fatalf("failed to create file: %v", err)
				}
				return p
			},
		},
		{
			name: "write to pre-existing file",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "existing.jsonl"), []byte("a\n"), 0o644); err != nil {
					t.Fatalf("failed to create file: %v", err)
				}
			},
			act: func(t *testing.T, root string) string {
				p := filepath.Join(root, "existing.jsonl")
				f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					t.Fatalf("failed to open file: %v", err)
				}
				if _, err := f.WriteString("b\n"); err != nil {
					t.Fatalf("failed to append: %v", err)
				}
				if err := f.Close(); err != nil {
					t.Fatalf("failed to close: %v", err)
				}
				return p
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			rec := &eventRecorder{}
			errCh := startRootWatch(t, ctx, root, rec.record)

			want := tt.act(t, root)
			waitFor(t, 5*time.Second, func() bool { return rec.contains(want) },
				"callback never observed "+want)

			cancel()
			select {
			case err := <-errCh:
				if err != nil {
					t.Errorf("WatchRootForNewEntries() error = %v, want nil on cancellation", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("WatchRootForNewEntries() did not return after context cancellation")
			}
		})
	}
}

func TestWatchRootForNewEntries_MissingRoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := WatchRootForNewEntries(ctx, filepath.Join(t.TempDir(), "nope"), func(string) {})
	if err == nil {
		t.Error("WatchRootForNewEntries() on a missing root: expected error, got nil")
	}
}
