package cursoride

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// CursorIDEWatcher monitors Cursor IDE databases for changes and notifies when sessions are updated
type CursorIDEWatcher struct {
	projectPath       string
	workspace         *WorkspaceMatch
	globalDbPath      string
	debugRaw          bool
	sessionCallback   func(*spi.AgentChatSession)
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	mu                sync.RWMutex
	lastCheck         time.Time
	knownComposers    map[string]int64 // composerID -> lastUpdatedAt
	checkInterval     time.Duration    // How often to check DB if no file events (default: 2 minutes)
	throttleDuration  time.Duration    // Minimum time between checks (default: 10 seconds)
	lastThrottledCall time.Time        // Track last throttled call
	pendingCheck      bool             // Whether a check is pending after throttle
	fsWatcher         *fsnotify.Watcher
}

// NewCursorIDEWatcher creates a new watcher for Cursor IDE databases
func NewCursorIDEWatcher(
	projectPath string,
	debugRaw bool,
	sessionCallback func(*spi.AgentChatSession),
	checkInterval time.Duration,
) (*CursorIDEWatcher, error) {
	// Find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace for project: %w", err)
	}

	// Get global database path
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}

	// Create fsnotify watcher
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Set default check interval if not provided
	if checkInterval == 0 {
		checkInterval = 2 * time.Minute
	}

	return &CursorIDEWatcher{
		projectPath:       projectPath,
		workspace:         workspace,
		globalDbPath:      globalDbPath,
		debugRaw:          debugRaw,
		sessionCallback:   sessionCallback,
		ctx:               ctx,
		cancel:            cancel,
		knownComposers:    make(map[string]int64),
		checkInterval:     checkInterval,
		throttleDuration:  10 * time.Second, // Configurable in code as requested
		lastThrottledCall: time.Now(),
		pendingCheck:      false,
		fsWatcher:         fsWatcher,
	}, nil
}

// Start begins monitoring the Cursor IDE databases
func (w *CursorIDEWatcher) Start() error {
	slog.Info("Starting Cursor IDE watcher",
		"projectPath", w.projectPath,
		"workspaceID", w.workspace.ID,
		"workspaceDbPath", w.workspace.DBPath,
		"globalDbPath", w.globalDbPath,
		"checkInterval", w.checkInterval,
		"throttleDuration", w.throttleDuration)

	// Set up file watching on workspace database
	if err := w.watchDatabaseFiles(w.workspace.DBPath); err != nil {
		slog.Warn("Failed to watch workspace database files", "error", err)
	}

	// Set up file watching on global database
	if err := w.watchDatabaseFiles(w.globalDbPath); err != nil {
		slog.Warn("Failed to watch global database files", "error", err)
	}

	// Start the file event handler goroutine
	w.wg.Add(1)
	go w.handleFileEvents()

	// Start the safety net polling goroutine
	w.wg.Add(1)
	go w.safetyNetPoller()

	// Perform initial check after a short delay
	time.AfterFunc(5*time.Second, func() {
		w.triggerCheck("initial")
	})

	slog.Info("Cursor IDE watcher started successfully")
	return nil
}

// Stop gracefully stops the watcher
func (w *CursorIDEWatcher) Stop() {
	slog.Info("Stopping Cursor IDE watcher")

	// Perform one final check before stopping
	slog.Info("Performing final check for changes before stopping")
	w.checkForChanges("shutdown")

	// Close fsnotify watcher
	if w.fsWatcher != nil {
		if err := w.fsWatcher.Close(); err != nil {
			slog.Warn("Error closing fsnotify watcher", "error", err)
		}
	}

	// Cancel context and wait for goroutines
	w.cancel()
	w.wg.Wait()

	slog.Info("Cursor IDE watcher stopped")
}

// watchDatabaseFiles sets up file watching for a database and its WAL file
func (w *CursorIDEWatcher) watchDatabaseFiles(dbPath string) error {
	// Watch the database file itself
	if err := w.fsWatcher.Add(dbPath); err != nil {
		// Log but don't fail - we'll rely on polling
		slog.Warn("Failed to watch database file", "path", dbPath, "error", err)
		return err
	}
	slog.Debug("Watching database file", "path", dbPath)

	// Watch the WAL file if it exists
	walPath := dbPath + "-wal"
	if _, err := os.Stat(walPath); err == nil {
		if err := w.fsWatcher.Add(walPath); err != nil {
			slog.Warn("Failed to watch WAL file", "path", walPath, "error", err)
			// Don't return error - WAL is optional
		} else {
			slog.Debug("Watching WAL file", "path", walPath)
		}
	}

	return nil
}

// handleFileEvents processes file system events from fsnotify
func (w *CursorIDEWatcher) handleFileEvents() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Only care about Write and Create events
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				slog.Debug("Database file changed", "path", event.Name, "op", event.Op)
				w.triggerCheck("file-change")
			}

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			slog.Warn("File watcher error", "error", err)
		}
	}
}

// safetyNetPoller ensures we check the database periodically even if file events are missed
func (w *CursorIDEWatcher) safetyNetPoller() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			slog.Debug("Safety net poll triggered")
			w.triggerCheck("poll")
		}
	}
}

// triggerCheck requests a check, respecting throttle limits
func (w *CursorIDEWatcher) triggerCheck(trigger string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	timeSinceLastCall := now.Sub(w.lastThrottledCall)

	if timeSinceLastCall < w.throttleDuration {
		// Within throttle window - mark as pending
		if !w.pendingCheck {
			w.pendingCheck = true
			// Schedule a check after throttle duration expires
			delay := w.throttleDuration - timeSinceLastCall
			slog.Debug("Check throttled, will retry after delay",
				"trigger", trigger,
				"delay", delay)

			go func() {
				time.Sleep(delay)
				w.executePendingCheck()
			}()
		}
		return
	}

	// Execute immediately
	w.lastThrottledCall = now
	w.pendingCheck = false
	go w.checkForChanges(trigger)
}

// executePendingCheck executes a pending check if one was scheduled
func (w *CursorIDEWatcher) executePendingCheck() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.pendingCheck {
		return
	}

	now := time.Now()
	timeSinceLastCall := now.Sub(w.lastThrottledCall)

	if timeSinceLastCall >= w.throttleDuration {
		w.lastThrottledCall = now
		w.pendingCheck = false
		go w.checkForChanges("throttled")
	}
}

// checkForChanges queries the databases and processes any new or updated sessions
func (w *CursorIDEWatcher) checkForChanges(trigger string) {
	slog.Debug("Checking for changes", "trigger", trigger)

	w.mu.Lock()
	lastCheck := w.lastCheck
	w.lastCheck = time.Now()
	w.mu.Unlock()

	// Load composer IDs from workspace database
	composerIDs, err := LoadWorkspaceComposerIDs(w.workspace.DBPath)
	if err != nil {
		slog.Error("Failed to load workspace composer IDs", "error", err)
		return
	}

	if len(composerIDs) == 0 {
		slog.Debug("No composers found in workspace")
		return
	}

	// Load composer data from global database
	composers, err := LoadComposerDataBatch(w.globalDbPath, composerIDs)
	if err != nil {
		slog.Error("Failed to load composer data", "error", err)
		return
	}

	// Track new/updated sessions
	newCount := 0
	updatedCount := 0

	for composerID, composer := range composers {
		// Skip if conversation is empty
		if len(composer.Conversation) == 0 {
			continue
		}

		// Check if this is a new or updated session
		w.mu.RLock()
		knownTimestamp, exists := w.knownComposers[composerID]
		w.mu.RUnlock()

		currentTimestamp := composer.LastUpdatedAt
		if currentTimestamp == 0 {
			currentTimestamp = composer.CreatedAt
		}

		// Determine if we should process this session
		shouldProcess := false
		if !exists {
			// New session
			shouldProcess = true
			newCount++
			slog.Info("Found new session", "composerID", composerID, "name", composer.Name)
		} else if currentTimestamp > knownTimestamp {
			// Updated session
			shouldProcess = true
			updatedCount++
			slog.Info("Found updated session",
				"composerID", composerID,
				"name", composer.Name,
				"oldTimestamp", knownTimestamp,
				"newTimestamp", currentTimestamp)
		}

		if shouldProcess {
			// Update known timestamp
			w.mu.Lock()
			w.knownComposers[composerID] = currentTimestamp
			w.mu.Unlock()

			// Convert to AgentChatSession
			session, err := ConvertToAgentChatSession(composer)
			if err != nil {
				slog.Warn("Failed to convert composer to session",
					"composerID", composerID,
					"error", err)
				continue
			}

			// Write debug output if requested
			if w.debugRaw {
				if err := writeDebugOutput(session); err != nil {
					slog.Warn("Failed to write debug output",
						"sessionID", session.SessionID,
						"error", err)
				}
			}

			// Invoke callback
			slog.Info("Invoking callback for session",
				"sessionID", session.SessionID,
				"slug", session.Slug)
			w.sessionCallback(session)
		}
	}

	elapsed := time.Since(lastCheck)
	slog.Info("Completed check for changes",
		"trigger", trigger,
		"totalComposers", len(composers),
		"newSessions", newCount,
		"updatedSessions", updatedCount,
		"elapsedSinceLastCheck", elapsed)
}
