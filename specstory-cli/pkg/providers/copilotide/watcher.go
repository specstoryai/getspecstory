package copilotide

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// WatchChatSessions watches the chatSessions directory for new/modified session files
func WatchChatSessions(
	ctx context.Context,
	workspaceDir string,
	projectPath string,
	debugRaw bool,
	sessionCallback func(*spi.AgentChatSession),
) error {
	chatSessionsPath := GetChatSessionsPath(workspaceDir)

	slog.Info("Starting VS Code Copilot watcher",
		"workspaceDir", workspaceDir,
		"chatSessionsPath", chatSessionsPath)

	// Create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			slog.Warn("Failed to close watcher", "error", err)
		}
	}()

	// Watch the chatSessions directory
	if err := watcher.Add(chatSessionsPath); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	slog.Info("Watching chatSessions directory", "path", chatSessionsPath)

	// Track known sessions and their modification times
	knownSessions := make(map[string]int64)

	// Debouncing map - track last processed time for each file
	lastProcessed := make(map[string]time.Time)
	debounceWindow := 500 * time.Millisecond

	// Process existing sessions first
	existingSessions, err := LoadAllSessionFiles(workspaceDir)
	if err != nil {
		slog.Warn("Failed to load existing sessions", "error", err)
	} else {
		for _, sessionPath := range existingSessions {
			composer, err := LoadSessionFile(sessionPath)
			if err != nil {
				slog.Warn("Failed to load session", "path", sessionPath, "error", err)
				continue
			}

			// Track as known
			knownSessions[composer.SessionID] = composer.LastMessageDate

			slog.Debug("Tracked existing session", "sessionId", composer.SessionID)
		}
	}

	// Event loop
	for {
		select {
		case <-ctx.Done():
			slog.Info("Watcher context canceled, stopping")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher events channel closed")
			}

			// Only process JSON files
			if !strings.HasSuffix(event.Name, ".json") {
				continue
			}

			// Only process Write and Create events
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			// Debounce rapid events for the same file
			now := time.Now()
			if lastTime, ok := lastProcessed[event.Name]; ok && now.Sub(lastTime) < debounceWindow {
				slog.Debug("Debouncing rapid event", "path", event.Name)
				continue
			}
			lastProcessed[event.Name] = now

			slog.Debug("File event detected", "path", event.Name, "op", event.Op)

			// Load the session file
			composer, err := LoadSessionFile(event.Name)
			if err != nil {
				slog.Warn("Failed to load session after event", "path", event.Name, "error", err)
				continue
			}

			// Check if this is new or updated
			sessionID := composer.SessionID
			lastKnownTime, exists := knownSessions[sessionID]

			isNew := !exists
			isUpdated := exists && composer.LastMessageDate > lastKnownTime

			if isNew || isUpdated {
				// Update known sessions
				knownSessions[sessionID] = composer.LastMessageDate

				if isNew {
					slog.Info("New session detected", "sessionId", sessionID, "name", composer.Name)
				} else {
					slog.Info("Session updated", "sessionId", sessionID, "name", composer.Name)
				}

				// Convert to AgentChatSession
				session := ConvertToSessionData(*composer, projectPath)

				// Write debug files if requested
				if debugRaw {
					if err := WriteDebugFiles(composer, sessionID); err != nil {
						slog.Warn("Failed to write debug files", "sessionId", sessionID, "error", err)
					}
				}

				// Invoke callback
				slog.Info("Invoking callback for session", "sessionId", sessionID, "slug", session.Slug)
				sessionCallback(&session)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher errors channel closed")
			}
			slog.Warn("Watcher error", "error", err)
		}
	}
}

// GetSessionIDFromPath extracts session ID from a file path
func GetSessionIDFromPath(path string) string {
	filename := filepath.Base(path)
	// Remove .json extension
	return strings.TrimSuffix(filename, ".json")
}
