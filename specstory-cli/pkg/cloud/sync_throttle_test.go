package cloud

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		wg.Go(func() {
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
		})
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

func TestSyncSessionToCloud_DoesNotBuildSessionDataWhenSendCannotStart(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "cloud disabled", enabled: false},
		{name: "cloud enabled but unauthenticated", enabled: true},
	}

	oldAuthChecked := authChecked
	oldIsAuthenticated := isAuthenticated
	oldHasSessionToken := hasSessionToken
	t.Cleanup(func() {
		authChecked = oldAuthChecked
		isAuthenticated = oldIsAuthenticated
		hasSessionToken = oldHasSessionToken
		InitSyncManager(false)
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitSyncManager(tt.enabled)
			authChecked = true
			isAuthenticated = false
			hasSessionToken = false
			built := false
			markdownBuilt := false
			SyncSessionToCloud("session-id", "session.md", func() string {
				markdownBuilt = true
				return "markdown"
			}, nil, func() string {
				built = true
				return "{}"
			}, "Claude Code", "title", false, true)
			if markdownBuilt {
				t.Error("markdown was assembled even though no send could start")
			}
			if built {
				t.Error("SessionData JSON was built even though no send could start")
			}
		})
	}
}

func TestSyncSessionToCloud_SingleFlightLatestWins(t *testing.T) {
	// Hold the first PUT open so overlapping autosaves must queue behind a real
	// in-flight HTTP request rather than merely behind the debounce timer.
	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{}, 1)
	var active int32
	var maxActive int32
	var requestsMu sync.Mutex
	var markdowns []string
	var sessionDataSnapshots []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		current := atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		for {
			maximum := atomic.LoadInt32(&maxActive)
			if current <= maximum || atomic.CompareAndSwapInt32(&maxActive, maximum, current) {
				break
			}
		}

		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("open request gzip: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request APIRequest
		decodeErr := json.NewDecoder(gz).Decode(&request)
		_ = gz.Close()
		if decodeErr != nil {
			t.Errorf("decode request: %v", decodeErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestsMu.Lock()
		markdowns = append(markdowns, request.Markdown)
		sessionDataSnapshots = append(sessionDataSnapshots, request.SessionData)
		requestCount := len(markdowns)
		requestsMu.Unlock()
		if requestCount == 1 {
			firstStarted <- struct{}{}
		}
		<-releaseFirst
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, ".project.json"), []byte(`{"project_name":"test","git_id":"project-id"}`), 0600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	oldSpecstoryDir := specstoryDir
	oldAPIBaseURL := apiBaseURL
	oldHasSessionToken := hasSessionToken
	oldSessionAccessToken := sessionAccessToken
	oldDeviceID := deviceID
	SetSpecstoryDir(tempDir)
	SetAPIBaseURL(server.URL)
	hasSessionToken = true
	sessionAccessToken = "test-token"
	deviceID = "test-device"
	t.Cleanup(func() {
		SetSpecstoryDir(oldSpecstoryDir)
		SetAPIBaseURL(oldAPIBaseURL)
		hasSessionToken = oldHasSessionToken
		sessionAccessToken = oldSessionAccessToken
		deviceID = oldDeviceID
	})

	InitSyncManager(true)
	syncMgr := GetSyncManager()
	syncMgr.debounceInterval = 0
	t.Cleanup(func() { InitSyncManager(false) })

	currentSessionData := "first"
	sessionDataJSON := func() string { return currentSessionData }
	sync := func(markdown string) {
		currentSessionData = markdown
		SyncSessionToCloud("session-id", "session.md", func() string { return markdown }, nil, sessionDataJSON, "Claude Code", "title", false, true)
	}
	sync("first")
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first cloud send did not start")
	}
	sync("superseded")
	sync("latest")
	currentSessionData = "a later cache mutation"

	// Give an incorrect overlapping sender an opportunity to enter the handler.
	time.Sleep(50 * time.Millisecond)
	close(releaseFirst)
	syncMgr.wg.Wait()

	if got := atomic.LoadInt32(&maxActive); got != 1 {
		t.Errorf("concurrent sends for one session = %d, want 1", got)
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if len(markdowns) != 2 || markdowns[0] != "first" || markdowns[1] != "latest" {
		t.Errorf("uploaded markdowns = %v, want [first latest]", markdowns)
	}
	if len(sessionDataSnapshots) != 2 || sessionDataSnapshots[0] != "first" || sessionDataSnapshots[1] != "latest" {
		t.Errorf("uploaded SessionData snapshots = %v, want [first latest]", sessionDataSnapshots)
	}
}

func TestShutdownFlushUploadsSnapshotQueuedBehindInFlightSend(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{}, 1)
	var requestsMu sync.Mutex
	var markdowns []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request APIRequest
		decodeErr := json.NewDecoder(gz).Decode(&request)
		_ = gz.Close()
		if decodeErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestsMu.Lock()
		markdowns = append(markdowns, request.Markdown)
		count := len(markdowns)
		requestsMu.Unlock()
		if count == 1 {
			firstStarted <- struct{}{}
			<-releaseFirst
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, ".project.json"), []byte(`{"project_name":"test","git_id":"project-id"}`), 0600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	oldSpecstoryDir := specstoryDir
	oldAPIBaseURL := apiBaseURL
	oldHasSessionToken := hasSessionToken
	oldSessionAccessToken := sessionAccessToken
	oldDeviceID := deviceID
	SetSpecstoryDir(tempDir)
	SetAPIBaseURL(server.URL)
	hasSessionToken = true
	sessionAccessToken = "test-token"
	deviceID = "test-device"
	t.Cleanup(func() {
		SetSpecstoryDir(oldSpecstoryDir)
		SetAPIBaseURL(oldAPIBaseURL)
		hasSessionToken = oldHasSessionToken
		sessionAccessToken = oldSessionAccessToken
		deviceID = oldDeviceID
	})

	InitSyncManager(true)
	syncMgr := GetSyncManager()
	syncMgr.debounceInterval = time.Hour
	t.Cleanup(func() { InitSyncManager(false) })

	sync := func(markdown string) {
		SyncSessionToCloud("session-id", "session.md", func() string { return markdown }, nil, func() string { return markdown }, "Claude Code", "title", false, true)
	}
	sync("first")
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first cloud send did not start")
	}
	sync("latest")

	// Shutdown flushes while the first send is still in flight.
	syncMgr.flushAllPending()
	close(releaseFirst)
	syncMgr.wg.Wait()

	requestsMu.Lock()
	defer requestsMu.Unlock()
	if len(markdowns) != 2 || markdowns[1] != "latest" {
		t.Errorf("uploaded markdowns = %v, want [first latest]", markdowns)
	}
}
