package cursorcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// CursorWatcher monitors Cursor SQLite databases for changes
type CursorWatcher struct {
	projectPath     string
	hashDir         string // The hash directory for this project
	pollInterval    time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	callbackWg      sync.WaitGroup              // Track callback goroutines
	lastCounts      map[string]int              // Track record counts per session
	knownSessions   map[string]bool             // Sessions that existed at startup (don't process unless resumed)
	resumedSession  string                      // Session ID being resumed (watch for changes)
	sessionCallback func(*spi.AgentChatSession) // Callback for session updates
	debugRaw        bool                        // Whether to write debug raw data files
	mu              sync.RWMutex                // Protects maps
}

// NewCursorWatcher creates a new Cursor database watcher
func NewCursorWatcher(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) (*CursorWatcher, error) {
	// Get the project hash directory
	hashDir, err := GetProjectHashDir(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get project hash directory: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &CursorWatcher{
		projectPath:     projectPath,
		hashDir:         hashDir,
		pollInterval:    5 * time.Second, // 5-second polling interval
		ctx:             ctx,
		cancel:          cancel,
		lastCounts:      make(map[string]int),
		knownSessions:   make(map[string]bool),
		resumedSession:  "",
		sessionCallback: sessionCallback,
		debugRaw:        debugRaw,
	}, nil
}

// SetInitialState sets the sessions that existed at startup and which session is being resumed
func (w *CursorWatcher) SetInitialState(existingSessionIDs map[string]bool, resumedSessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.knownSessions = existingSessionIDs
	w.resumedSession = resumedSessionID
	if resumedSessionID != "" {
		slog.Info("Watcher will monitor resumed session for changes", "sessionId", resumedSessionID)
	}
	slog.Debug("Watcher initialized with existing sessions", "count", len(existingSessionIDs))
}

// Start begins monitoring the Cursor databases
func (w *CursorWatcher) Start() error {
	slog.Info("Starting Cursor CLI watcher",
		"projectPath", w.projectPath,
		"hashDir", w.hashDir,
		"pollInterval", w.pollInterval)

	// Check if hash directory exists
	stat, err := os.Stat(w.hashDir)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("Project hash directory doesn't exist yet, will watch for it", "hashDir", w.hashDir)
		} else {
			slog.Warn("Cannot access project hash directory", "hashDir", w.hashDir, "error", err)
		}
		// Start watching for the directory to be created
		w.wg.Add(1)
		go w.watchForDirectory()
		return nil
	}

	// Log that we found the directory
	slog.Info("Project hash directory exists", "hashDir", w.hashDir, "isDir", stat.IsDir())

	// Directory exists, start watching sessions
	w.wg.Add(1)
	go w.watchLoop()

	return nil
}

// Stop gracefully stops the watcher
func (w *CursorWatcher) Stop() {
	slog.Info("Stopping Cursor watcher")

	// Perform one final check before stopping to catch any last-minute changes
	slog.Info("Performing final check for changes before stopping")
	w.checkForChanges()

	// Now stop the watcher goroutines
	w.cancel()
	w.wg.Wait()

	// Wait for any pending callback goroutines to complete
	slog.Info("Waiting for pending callbacks to complete")
	w.callbackWg.Wait()

	slog.Info("Cursor watcher stopped")
}

// watchForDirectory waits for the project hash directory to be created
func (w *CursorWatcher) watchForDirectory() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			// Check if directory now exists
			if _, err := os.Stat(w.hashDir); err == nil {
				slog.Info("Project hash directory detected, starting session watcher", "hashDir", w.hashDir)
				// Directory exists now, start watching sessions
				w.wg.Add(1)
				go w.watchLoop()
				return
			}
		}
	}
}

// watchLoop is the main monitoring loop
func (w *CursorWatcher) watchLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Do an initial check immediately
	w.checkForChanges()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			// Uncomment this to see the ticker in action when debugging the watcher
			//slog.Debug("Watcher tick - checking for changes")
			w.checkForChanges()
		}
	}
}

// checkForChanges scans all session directories for database changes
func (w *CursorWatcher) checkForChanges() {

	// Get all session directories
	sessionIDs, err := GetCursorSessionDirs(w.hashDir)
	if err != nil {
		slog.Debug("Failed to get session directories", "error", err)
		return
	}

	for _, sessionID := range sessionIDs {
		// Check if this is a NEW session or the resumed session
		w.mu.RLock()
		isKnown := w.knownSessions[sessionID]
		isResumed := sessionID == w.resumedSession
		w.mu.RUnlock()

		// Skip known sessions unless it's the one being resumed
		if isKnown && !isResumed {
			continue
		}

		// Check if this session has a store.db file
		dbPath := filepath.Join(w.hashDir, sessionID, "store.db")

		// Check if database exists
		fileInfo, err := os.Stat(dbPath)
		if err != nil {
			continue // Skip sessions without store.db
		}

		// For NEW sessions, process them once and then watch for changes
		if !isKnown {
			// Check if we've already seen this new session
			if w.hasSessionChanged(sessionID, fileInfo, dbPath) {
				slog.Info("Detected NEW Cursor session", "sessionId", sessionID)
				w.processSessionChanges(sessionID, dbPath)
			}
		} else if isResumed {
			// For resumed session, check for changes
			slog.Debug("Polling resumed session for changes", "sessionId", sessionID)
			if w.hasSessionChanged(sessionID, fileInfo, dbPath) {
				slog.Info("Detected changes in resumed Cursor session", "sessionId", sessionID)
				w.processSessionChanges(sessionID, dbPath)
			} else {
				slog.Debug("No changes detected in resumed session", "sessionId", sessionID)
			}
		}
	}
}

// hasSessionChanged checks if a session database has new records
func (w *CursorWatcher) hasSessionChanged(sessionID string, fileInfo os.FileInfo, dbPath string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Always check record count - don't rely on file modification time
	// SQLite with WAL mode may not update file mtime when records are added

	// Open database to count records (with WAL mode)
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		slog.Error("Failed to open database", "sessionId", sessionID, "error", err)
		return false
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("Failed to close database", "error", err)
		}
	}()

	// Enable WAL mode for non-blocking reads
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		slog.Debug("Failed to enable WAL mode in watcher", "error", err)
	}

	// Count total records in the blobs table
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM blobs").Scan(&count)
	if err != nil {
		slog.Error("Failed to count blobs", "sessionId", sessionID, "error", err)
		return false
	}

	// Check if record count changed
	lastCount, countExists := w.lastCounts[sessionID]
	w.lastCounts[sessionID] = count

	if !countExists {
		// First time seeing this session - only process if it has content
		if count > 0 {
			slog.Debug("First check of session with content", "sessionId", sessionID, "recordCount", count)
			return true
		}
		slog.Debug("First check of session but empty", "sessionId", sessionID)
		return false
	}

	if count > lastCount {
		// Records added to existing session
		slog.Debug("Session has new records", "sessionId", sessionID, "oldCount", lastCount, "newCount", count)
		return true
	}

	// Uncomment this to see the ticker in action when debugging the watcher
	//slog.Debug("No new records in session", "sessionId", sessionID, "count", count)
	return false
}

// processSessionChanges handles changes detected in a session
func (w *CursorWatcher) processSessionChanges(sessionID string, dbPath string) {
	slog.Info("Processing Cursor session changes", "sessionId", sessionID)

	// Read the session data
	sessionPath := filepath.Dir(dbPath) // Get the session directory from db path
	createdAt, slug, blobRecords, _, err := ReadSessionData(sessionPath)
	if err != nil {
		slog.Error("Failed to read session data", "sessionId", sessionID, "error", err)
		return
	}

	if len(blobRecords) == 0 {
		slog.Debug("Session has no message records", "sessionId", sessionID)
		return
	}

	// Generate SessionData from blob records
	sessionData, err := GenerateAgentSession(blobRecords, w.projectPath, sessionID, createdAt, slug)
	if err != nil {
		slog.Error("Failed to generate SessionData", "sessionId", sessionID, "error", err)
		return
	}

	// Marshal blob records to JSON for raw data
	rawDataJSON, err := json.Marshal(blobRecords)
	if err != nil {
		slog.Error("Failed to marshal blob records", "sessionId", sessionID, "error", err)
		return
	}

	// Write provider-specific debug output if requested
	if w.debugRaw {
		if err := writeDebugOutput(sessionID, string(rawDataJSON), nil); err != nil {
			slog.Debug("Failed to write debug output", "sessionID", sessionID, "error", err)
			// Don't fail the operation if debug output fails
		}
	}

	// Create the AgentChatSession
	agentSession := &spi.AgentChatSession{
		SessionID:   sessionID,
		CreatedAt:   createdAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataJSON),
	}

	// Call the callback asynchronously to avoid blocking the watcher
	if w.sessionCallback != nil {
		w.callbackWg.Add(1)
		go func(s *spi.AgentChatSession) {
			defer w.callbackWg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Session callback panicked", "panic", r, "sessionId", s.SessionID)
				}
			}()
			w.sessionCallback(s)
		}(agentSession)

		// Log that we detected changes and invoked the callback
		slog.Info("Detected session changes, callback invoked", "sessionId", sessionID)
	}
}

// WatchCursorProject starts monitoring a Cursor project
func WatchCursorProject(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) (*CursorWatcher, error) {
	watcher, err := NewCursorWatcher(projectPath, debugRaw, sessionCallback)
	if err != nil {
		return nil, err
	}

	if err := watcher.Start(); err != nil {
		return nil, err
	}

	return watcher, nil
}
