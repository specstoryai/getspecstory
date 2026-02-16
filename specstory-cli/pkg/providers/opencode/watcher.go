package opencode

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// debounceWindow is the time to wait after the last file change before processing.
// This prevents multiple reloads when OpenCode writes multiple files in rapid succession.
const debounceWindow = 100 * time.Millisecond

// cleanupInterval is how often we clean up old entries from the lastProcessed map.
const cleanupInterval = 5 * time.Minute

// cleanupThreshold is the age after which entries are removed from lastProcessed.
// Entries older than this are no longer needed for debouncing.
const cleanupThreshold = 10 * time.Minute

// Watcher monitors OpenCode storage directories for changes and invokes a callback
// when session data is updated. It watches the message and part directories to detect
// new content and reloads the affected session.
type Watcher struct {
	projectHash   string
	projectPath   string
	fsWatcher     *fsnotify.Watcher
	callback      func(*spi.AgentChatSession)
	debugRaw      bool
	lastProcessed map[string]time.Time // Tracks last processing time per session for debouncing
	mu            sync.Mutex           // Protects lastProcessed
	stopCleanup   chan struct{}        // Signals the cleanup goroutine to stop
}

// NewWatcher creates a new storage watcher for an OpenCode project.
// The callback is invoked when session data changes, with the updated AgentChatSession.
func NewWatcher(projectPath string, callback func(*spi.AgentChatSession)) (*Watcher, error) {
	slog.Debug("NewWatcher: Creating watcher", "projectPath", projectPath)

	projectHash, err := ComputeProjectHash(projectPath)
	if err != nil {
		return nil, err
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		projectHash:   projectHash,
		projectPath:   projectPath,
		fsWatcher:     fsWatcher,
		callback:      callback,
		lastProcessed: make(map[string]time.Time),
		stopCleanup:   make(chan struct{}),
	}, nil
}

// SetDebugRaw enables or disables debug raw file output.
func (w *Watcher) SetDebugRaw(debugRaw bool) {
	w.debugRaw = debugRaw
}

// Start begins watching for changes in OpenCode storage directories.
// This method blocks until the context is cancelled or an error occurs.
// It watches:
// - storage/session/{projectHash}/ for new sessions
// - storage/message/ for new messages (filtered to current project's sessions)
// - storage/part/ for new parts (filtered to current project's sessions)
func (w *Watcher) Start(ctx context.Context) error {
	slog.Info("Watcher.Start: Starting OpenCode file watcher",
		"projectHash", w.projectHash,
		"projectPath", w.projectPath)

	storageDir, err := GetStorageDir()
	if err != nil {
		return err
	}

	// Watch the session directory for new sessions
	sessionsDir := filepath.Join(storageDir, "session", w.projectHash)
	if err := w.watchDirRecursive(sessionsDir); err != nil {
		// Session directory may not exist yet - this is OK, we'll create watches when it appears
		slog.Debug("Watcher.Start: Could not watch sessions directory (may not exist yet)",
			"path", sessionsDir,
			"error", err)
	}

	// Watch the message directory for new messages
	// We watch the entire message directory and filter by session ID in the event handler
	messagesDir := filepath.Join(storageDir, "message")
	if err := w.watchDirRecursive(messagesDir); err != nil {
		slog.Debug("Watcher.Start: Could not watch messages directory",
			"path", messagesDir,
			"error", err)
	}

	// Watch the part directory for new parts
	// We watch the entire part directory and filter by message -> session in the event handler
	partsDir := filepath.Join(storageDir, "part")
	if err := w.watchDirRecursive(partsDir); err != nil {
		slog.Debug("Watcher.Start: Could not watch parts directory",
			"path", partsDir,
			"error", err)
	}

	slog.Info("Watcher.Start: File watcher started, waiting for events")

	// Start the cleanup goroutine to prevent memory leak in lastProcessed map
	go w.cleanupLoop()

	// Event processing loop
	for {
		select {
		case <-ctx.Done():
			slog.Info("Watcher.Start: Context cancelled, stopping watcher")
			return ctx.Err()

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				slog.Info("Watcher.Start: Events channel closed")
				return nil
			}
			w.handleEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				slog.Info("Watcher.Start: Errors channel closed")
				return nil
			}
			log.UserWarn("File watcher error: %v", err)
			slog.Error("Watcher.Start: Watcher error", "error", err)
		}
	}
}

// Stop gracefully stops the watcher and releases resources.
func (w *Watcher) Stop() error {
	slog.Info("Watcher.Stop: Stopping watcher")
	close(w.stopCleanup)
	return w.fsWatcher.Close()
}

// cleanupLoop periodically removes old entries from the lastProcessed map to prevent
// unbounded memory growth. Entries older than cleanupThreshold are removed since they
// are no longer needed for debouncing.
func (w *Watcher) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCleanup:
			return
		case <-ticker.C:
			w.cleanupOldEntries()
		}
	}
}

// cleanupOldEntries removes entries from lastProcessed that are older than cleanupThreshold.
func (w *Watcher) cleanupOldEntries() {
	w.mu.Lock()
	defer w.mu.Unlock()

	threshold := time.Now().Add(-cleanupThreshold)
	removed := 0

	for sessionID, lastTime := range w.lastProcessed {
		if lastTime.Before(threshold) {
			delete(w.lastProcessed, sessionID)
			removed++
		}
	}

	if removed > 0 {
		slog.Debug("Watcher.cleanupOldEntries: Cleaned up old entries",
			"removed", removed,
			"remaining", len(w.lastProcessed))
	}
}

// watchDirRecursive adds a directory and all its subdirectories to the watcher.
// This is needed because fsnotify doesn't watch recursively by default.
func (w *Watcher) watchDirRecursive(dir string) error {
	// Check if directory exists
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}

	// Add the directory itself
	if err := w.fsWatcher.Add(dir); err != nil {
		return err
	}
	slog.Debug("Watcher.watchDirRecursive: Added directory to watch", "path", dir)

	// Walk subdirectories
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			subdir := filepath.Join(dir, entry.Name())
			if err := w.watchDirRecursive(subdir); err != nil {
				// Log but continue with other directories
				slog.Debug("Watcher.watchDirRecursive: Failed to watch subdirectory",
					"path", subdir,
					"error", err)
			}
		}
	}

	return nil
}

// handleEvent processes a file system event and triggers session reload if needed.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Only process create and write events for JSON files
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}

	path := event.Name

	// If a new directory was created, add it and all subdirectories to the watch list.
	// We use watchDirRecursive instead of fsWatcher.Add to handle cases where the new
	// directory already contains subdirectories (e.g., when a tool creates a nested structure).
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if err := w.watchDirRecursive(path); err != nil {
				slog.Debug("Watcher.handleEvent: Failed to add new directory to watch",
					"path", path,
					"error", err)
			} else {
				slog.Debug("Watcher.handleEvent: Added new directory to watch", "path", path)
			}
			return
		}
	}

	// Only process JSON files
	if !strings.HasSuffix(path, ".json") {
		return
	}

	slog.Debug("Watcher.handleEvent: Processing file event",
		"operation", event.Op.String(),
		"path", path)

	// Extract session ID from the path
	sessionID := w.extractSessionID(path)
	if sessionID == "" {
		slog.Debug("Watcher.handleEvent: Could not extract session ID from path", "path", path)
		return
	}

	// Verify this session belongs to our project
	if !w.isSessionForProject(sessionID) {
		slog.Debug("Watcher.handleEvent: Session not for this project",
			"sessionID", sessionID,
			"projectHash", w.projectHash)
		return
	}

	// Debounce: check if we've processed this session recently
	w.mu.Lock()
	lastTime := w.lastProcessed[sessionID]
	now := time.Now()
	if now.Sub(lastTime) < debounceWindow {
		w.mu.Unlock()
		slog.Debug("Watcher.handleEvent: Debouncing session reload",
			"sessionID", sessionID,
			"timeSinceLastProcess", now.Sub(lastTime))
		return
	}
	w.lastProcessed[sessionID] = now
	w.mu.Unlock()

	// Schedule the actual reload after the debounce window.
	// This allows multiple rapid writes to coalesce into a single reload.
	go func(sid string) {
		time.Sleep(debounceWindow)

		// Debounce check: If another event arrived for this session while we were sleeping,
		// it would have updated lastProcessed[sid] to a newer timestamp. By comparing against
		// the 'now' value we captured before sleeping, we can detect this:
		// - If timestamps match: no newer event came in, we should proceed with reload
		// - If timestamps differ: a newer event is pending, skip this reload (newer goroutine will handle it)
		// This ensures only the most recent event triggers a reload, preventing duplicate processing.
		w.mu.Lock()
		if w.lastProcessed[sid] != now {
			w.mu.Unlock()
			slog.Debug("Watcher.handleEvent: Skipping reload, newer event pending", "sessionID", sid)
			return
		}
		w.mu.Unlock()

		w.reloadAndCallback(sid)
	}(sessionID)
}

// extractSessionID extracts the session ID from a file path.
// Handles paths like:
// - storage/session/{hash}/ses_XXX.json -> ses_XXX
// - storage/message/ses_XXX/msg_YYY.json -> ses_XXX
// - storage/part/msg_XXX/prt_YYY.json -> requires lookup via message
func (w *Watcher) extractSessionID(path string) string {
	// Pattern for session files: .../session/{hash}/ses_XXX.json
	sessionFileRegex := regexp.MustCompile(`/session/[^/]+/(ses_[^/]+)\.json$`)
	if matches := sessionFileRegex.FindStringSubmatch(path); len(matches) > 1 {
		return matches[1]
	}

	// Pattern for message directory: .../message/ses_XXX/...
	messagePathRegex := regexp.MustCompile(`/message/(ses_[^/]+)/`)
	if matches := messagePathRegex.FindStringSubmatch(path); len(matches) > 1 {
		return matches[1]
	}

	// Pattern for part directory: .../part/msg_XXX/...
	// We need to look up the message to find its session ID
	partPathRegex := regexp.MustCompile(`/part/(msg_[^/]+)/`)
	if matches := partPathRegex.FindStringSubmatch(path); len(matches) > 1 {
		messageID := matches[1]
		return w.lookupSessionIDForMessage(messageID)
	}

	return ""
}

// lookupSessionIDForMessage finds the session ID for a given message ID by reading the message file.
func (w *Watcher) lookupSessionIDForMessage(messageID string) string {
	storageDir, err := GetStorageDir()
	if err != nil {
		return ""
	}

	// Walk through message directories to find which session this message belongs to
	messageDir := filepath.Join(storageDir, "message")
	entries, err := os.ReadDir(messageDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "ses_") {
			continue
		}

		sessionID := entry.Name()
		messagePath := filepath.Join(messageDir, sessionID, messageID+".json")
		if _, err := os.Stat(messagePath); err == nil {
			return sessionID
		}
	}

	return ""
}

// isSessionForProject checks if a session belongs to the current project.
// It does this by checking if the session exists in the project's session directory.
func (w *Watcher) isSessionForProject(sessionID string) bool {
	sessionsDir, err := GetSessionsDir(w.projectHash)
	if err != nil {
		return false
	}

	sessionPath := filepath.Join(sessionsDir, sessionID+".json")
	_, err = os.Stat(sessionPath)
	return err == nil
}

// reloadAndCallback loads the session data and invokes the callback.
func (w *Watcher) reloadAndCallback(sessionID string) {
	slog.Info("Watcher.reloadAndCallback: Reloading session", "sessionID", sessionID)

	// Load and assemble the full session
	fullSession, err := LoadAndAssembleSession(w.projectHash, sessionID)
	if err != nil {
		slog.Error("Watcher.reloadAndCallback: Failed to load session",
			"sessionID", sessionID,
			"error", err)
		return
	}

	// Convert to AgentChatSession
	chatSession := convertToAgentChatSession(fullSession, w.projectPath, w.debugRaw)
	if chatSession == nil {
		slog.Debug("Watcher.reloadAndCallback: Session converted to nil (empty or filtered)",
			"sessionID", sessionID)
		return
	}

	// Invoke callback in a goroutine to avoid blocking
	if w.callback != nil {
		slog.Info("Watcher.reloadAndCallback: Invoking callback for session", "sessionID", sessionID)
		go func(s *spi.AgentChatSession) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Watcher.reloadAndCallback: Callback panicked", "panic", r)
				}
			}()
			w.callback(s)
		}(chatSession)
	}
}

// ProcessExistingSessions loads and processes all existing sessions for the project.
// This should be called before starting the watcher to ensure we capture sessions
// that were created before this run started.
func (w *Watcher) ProcessExistingSessions() {
	slog.Info("Watcher.ProcessExistingSessions: Processing existing sessions",
		"projectHash", w.projectHash)

	// Load all sessions for the project
	fullSessions, err := LoadAllSessionsForProject(w.projectHash)
	if err != nil {
		slog.Error("Watcher.ProcessExistingSessions: Failed to load existing sessions",
			"error", err)
		return
	}

	slog.Info("Watcher.ProcessExistingSessions: Found sessions", "count", len(fullSessions))

	// Process each session
	for _, fullSession := range fullSessions {
		if fullSession == nil || fullSession.Session == nil {
			continue
		}

		chatSession := convertToAgentChatSession(fullSession, w.projectPath, w.debugRaw)
		if chatSession == nil {
			continue
		}

		// Invoke callback
		if w.callback != nil {
			slog.Debug("Watcher.ProcessExistingSessions: Processing session",
				"sessionID", chatSession.SessionID)
			go func(s *spi.AgentChatSession) {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("Watcher.ProcessExistingSessions: Callback panicked", "panic", r)
					}
				}()
				w.callback(s)
			}(chatSession)
		}
	}
}
