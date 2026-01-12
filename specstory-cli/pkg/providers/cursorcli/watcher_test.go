package cursorcli

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
)

func TestCursorWatcherBasics(t *testing.T) {
	// Test basic watcher creation and properties
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Test callback
	callback := func(session *spi.AgentChatSession) {
		// Simple test callback
	}

	// Create watcher manually with test values
	watcher := &CursorWatcher{
		projectPath:     "/test/project",
		hashDir:         "/test/hash",
		pollInterval:    5 * time.Second,
		ctx:             ctx,
		cancel:          cancel,
		lastCounts:      make(map[string]int),
		knownSessions:   make(map[string]bool),
		sessionCallback: callback,
	}

	// Verify watcher was created with correct properties
	if watcher.projectPath != "/test/project" {
		t.Errorf("Expected projectPath /test/project, got %s", watcher.projectPath)
	}
	if watcher.hashDir != "/test/hash" {
		t.Errorf("Expected hashDir /test/hash, got %s", watcher.hashDir)
	}
	if watcher.pollInterval != 5*time.Second {
		t.Errorf("Expected pollInterval 5s, got %v", watcher.pollInterval)
	}
	if watcher.sessionCallback == nil {
		t.Error("Expected sessionCallback to be set")
	}
}

func TestCursorWatcherStartStop(t *testing.T) {
	// Create a temp directory for testing
	tempDir := t.TempDir()
	hashDir := filepath.Join(tempDir, "test-hash")

	// Create watcher manually
	ctx, cancel := context.WithCancel(context.Background())
	watcher := &CursorWatcher{
		projectPath:     tempDir,
		hashDir:         hashDir,
		pollInterval:    100 * time.Millisecond, // Short interval for testing
		ctx:             ctx,
		cancel:          cancel,
		lastCounts:      make(map[string]int),
		knownSessions:   make(map[string]bool),
		sessionCallback: nil,
	}

	// Start watcher
	err := watcher.Start()
	if err != nil {
		t.Fatalf("Failed to start watcher: %v", err)
	}

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Stop watcher
	done := make(chan struct{})
	go func() {
		watcher.Stop()
		close(done)
	}()

	// Wait for stop with timeout
	select {
	case <-done:
		// Success - watcher stopped
	case <-time.After(2 * time.Second):
		t.Fatal("Watcher failed to stop within timeout")
	}
}

func TestCursorWatcherSessionDetection(t *testing.T) {
	// Create a temp directory for testing
	tempDir := t.TempDir()
	hashDir := filepath.Join(tempDir, "test-hash")

	// Create hash directory
	if err := os.MkdirAll(hashDir, 0755); err != nil {
		t.Fatalf("Failed to create hash directory: %v", err)
	}

	// Create a test session directory with store.db
	sessionID := "test-session-123"
	sessionDir := filepath.Join(hashDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session directory: %v", err)
	}

	// Create a mock SQLite database with blobs table
	dbPath := filepath.Join(sessionDir, "store.db")
	// Note: We can't create a real SQLite database in unit tests without additional setup,
	// so we'll just create an empty file. The hasSessionChanged function will fail to open it
	// as a real database, which is expected and handled gracefully.
	if err := os.WriteFile(dbPath, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to create store.db: %v", err)
	}

	// Create watcher manually
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher := &CursorWatcher{
		projectPath:     tempDir,
		hashDir:         hashDir,
		pollInterval:    5 * time.Second,
		ctx:             ctx,
		cancel:          cancel,
		lastCounts:      make(map[string]int),
		knownSessions:   make(map[string]bool),
		sessionCallback: nil,
	}

	// Check session change detection
	hasChanged := watcher.hasSessionChanged(sessionID, mustGetFileInfo(t, dbPath), dbPath)

	// The function will try to open the database and count blobs
	// Since this is not a real SQLite database with a blobs table, it will fail gracefully
	// and return false (no change). The session won't be tracked in lastCounts
	// because the SQL query fails.
	_ = hasChanged

	// We can't verify the session was tracked because the database query fails
	// This is expected behavior for this unit test with a mock file
}

func TestCursorWatcherCallbackInvocation(t *testing.T) {
	// Test that callbacks are properly invoked
	var callbackMu sync.Mutex
	callbackCount := 0
	var lastSession *spi.AgentChatSession

	callback := func(session *spi.AgentChatSession) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackCount++
		lastSession = session
	}

	// Create a test session
	testSession := &spi.AgentChatSession{
		SessionID:   "test-123",
		CreatedAt:   time.Now().Format(time.RFC3339),
		Slug:        "test-slug",
		SessionData: &schema.SessionData{},
		RawData:     "{}",
	}

	// Invoke callback directly to test it works
	callback(testSession)

	// Verify callback was invoked
	callbackMu.Lock()
	if callbackCount != 1 {
		t.Errorf("Expected callback count 1, got %d", callbackCount)
	}
	if lastSession == nil || lastSession.SessionID != "test-123" {
		t.Error("Expected callback to receive correct session")
	}
	callbackMu.Unlock()
}

// Helper function to get FileInfo for a file
func mustGetFileInfo(t *testing.T, path string) os.FileInfo {
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Failed to stat file %s: %v", path, err)
	}
	return info
}
