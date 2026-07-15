package copilotide

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// WatchChatSessions watches the chatSessions directory of every given workspace for
// new/modified session files. A project can match several workspace entries (opened
// directly as a folder and via .code-workspace files), and a session update can land
// in any of them, so a single fsnotify watcher is attached to all their directories.
//
// Limitation: workspaceDirs comes from the matcher with requireChatSessions=true, so a
// matching workspace whose chatSessions directory does not exist yet is not included
// and won't be picked up until the watch restarts — watching for the directory's
// creation would require a parent-directory watch that this watcher doesn't have.
func (p *Provider) WatchChatSessions(
	ctx context.Context,
	workspaceDirs []string,
	projectPath string,
	debugRaw bool,
	sessionCallback func(*spi.AgentChatSession),
) error {
	slog.Info("Starting Copilot watcher",
		"app", p.variant.AppName,
		"workspaceCount", len(workspaceDirs))

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

	// Tracks in-flight sessionCallback goroutines so we don't return while a
	// callback is still writing. Runs before the watcher-close defer (LIFO).
	var callbackWg sync.WaitGroup
	defer callbackWg.Wait()

	// Watch the chatSessions directory of every matching workspace. A failure to add
	// one directory (e.g. deleted since matching) must not silence the others, so it
	// only degrades to a warning; the watch fails outright only when nothing is watched.
	watchedCount := 0
	for _, workspaceDir := range workspaceDirs {
		chatSessionsPath := GetChatSessionsPath(workspaceDir)
		if err := watcher.Add(chatSessionsPath); err != nil {
			slog.Warn("Failed to watch chatSessions directory",
				"path", chatSessionsPath, "error", err)
			continue
		}
		watchedCount++
		slog.Info("Watching chatSessions directory", "path", chatSessionsPath)
	}
	if watchedCount == 0 {
		return fmt.Errorf("failed to watch any chatSessions directory (%d candidates)", len(workspaceDirs))
	}

	// Track known sessions and their modification times
	knownSessions := make(map[string]int64)

	// Debouncing map - track last processed time for each file
	lastProcessed := make(map[string]time.Time)
	debounceWindow := 500 * time.Millisecond

	// Process existing sessions first, across all watched workspaces. knownSessions is
	// keyed by session ID, so a session present in several workspaces is tracked once
	// with the newest LastMessageDate seen — later events only fire the callback when
	// they carry something newer.
	for _, workspaceDir := range workspaceDirs {
		existingSessions, err := LoadAllSessionFiles(workspaceDir)
		if err != nil {
			slog.Warn("Failed to load existing sessions", "workspace", workspaceDir, "error", err)
			continue
		}
		for _, sessionPath := range existingSessions {
			composer, err := LoadSessionFile(sessionPath)
			if err != nil {
				slog.Warn("Failed to load session", "path", sessionPath, "error", err)
				continue
			}

			// Track as known, keeping the newest LastMessageDate across workspaces
			if known, exists := knownSessions[composer.SessionID]; !exists || composer.LastMessageDate > known {
				knownSessions[composer.SessionID] = composer.LastMessageDate
			}

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

			// Only process JSON and JSONL files
			if !strings.HasSuffix(event.Name, ".json") && !strings.HasSuffix(event.Name, ".jsonl") {
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

			slog.Debug("File event detected", "path", event.Name, "op", event.Op)

			// Load the session file
			composer, err := LoadSessionFile(event.Name)
			if err != nil {
				// Don't record the debounce timestamp on failure: the file was
				// likely caught mid-write, and the follow-up write event must
				// not be swallowed by the debounce window or the update is lost.
				slog.Warn("Failed to load session after event", "path", event.Name, "error", err)
				continue
			}
			lastProcessed[event.Name] = now

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

				// Load state file (optional) from the workspace the event came from.
				// The event path is <workspaceDir>/chatSessions/<file>, so the owning
				// workspace directory is two levels up from the changed file.
				eventWorkspaceDir := filepath.Dir(filepath.Dir(event.Name))
				state, err := LoadStateFile(eventWorkspaceDir, sessionID)
				if err != nil {
					slog.Warn("Failed to load state file", "sessionId", sessionID, "error", err)
				}

				// Convert to AgentChatSession
				session := p.ConvertToSessionData(*composer, projectPath, state)

				// Write debug files if requested
				if debugRaw {
					if err := WriteDebugFiles(composer, sessionID); err != nil {
						slog.Warn("Failed to write debug files", "sessionId", sessionID, "error", err)
					}
				}

				// Invoke callback asynchronously so a panic during processing (e.g.
				// markdown write or cloud sync) doesn't crash the watcher, and slow
				// callback I/O doesn't block the fsnotify event loop.
				slog.Info("Invoking callback for session", "sessionId", sessionID, "slug", session.Slug)
				callbackWg.Add(1)
				go func(s *spi.AgentChatSession) {
					defer callbackWg.Done()
					defer func() {
						if r := recover(); r != nil {
							slog.Error("Session callback panicked", "panic", r, "sessionId", s.SessionID)
						}
					}()
					sessionCallback(s)
				}(&session)
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
	// Remove .json or .jsonl extension
	if strings.HasSuffix(filename, ".jsonl") {
		return strings.TrimSuffix(filename, ".jsonl")
	}
	return strings.TrimSuffix(filename, ".json")
}
