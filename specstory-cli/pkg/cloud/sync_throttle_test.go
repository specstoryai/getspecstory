package cloud

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSemaphoreThrottling(t *testing.T) {
	// Initialize sync manager with semaphore
	InitSyncManager(true)
	syncMgr := GetSyncManager()
	if syncMgr == nil {
		t.Fatal("Failed to get sync manager")
	}

	// Track concurrent operations
	var currentConcurrent int32
	var maxConcurrent int32

	// Use a WaitGroup to track all goroutines
	var wg sync.WaitGroup

	// Launch 20 operations that would normally run concurrently
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Try to acquire semaphore (blocks until available)
			release := syncMgr.acquireHTTPSemaphore("test-session", "TEST")
			defer release()

			// Track concurrent operations
			current := atomic.AddInt32(&currentConcurrent, 1)

			// Update max if needed
			for {
				max := atomic.LoadInt32(&maxConcurrent)
				if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
					break
				}
			}

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			// Decrement counter
			atomic.AddInt32(&currentConcurrent, -1)
		}(i)
	}

	// Wait for all operations to complete
	wg.Wait()

	// Verify that we never exceeded the max concurrent requests
	if maxConcurrent > MaxConcurrentHTTPRequests {
		t.Errorf("Exceeded max concurrent requests: got %d, want <= %d",
			maxConcurrent, MaxConcurrentHTTPRequests)
	}

	// We should have hit the max at some point with 20 operations
	if maxConcurrent < MaxConcurrentHTTPRequests {
		t.Errorf("Did not reach expected concurrency: got %d, want %d",
			maxConcurrent, MaxConcurrentHTTPRequests)
	}
}

func TestHTTPSemaphoreBlocksWhenFull(t *testing.T) {
	// Initialize sync manager with semaphore
	InitSyncManager(true)
	syncMgr := GetSyncManager()
	if syncMgr == nil {
		t.Fatal("Failed to get sync manager")
	}

	// Fill up all semaphore slots
	var releases []func()
	for i := 0; i < MaxConcurrentHTTPRequests; i++ {
		release := syncMgr.acquireHTTPSemaphore("test-session", "FILL")
		releases = append(releases, release)
	}

	// Try to acquire one more - should be blocked because semaphore is full
	select {
	case syncMgr.httpSemaphore <- struct{}{}:
		t.Error("Should not have been able to acquire semaphore when full")
		<-syncMgr.httpSemaphore // Clean up
	default:
		// Good - semaphore is full as expected
	}

	// Release all
	for _, release := range releases {
		release()
	}

	// Now we should be able to acquire again
	release := syncMgr.acquireHTTPSemaphore("test-session", "AFTER")
	release()
}
