package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/SpecStoryCLI/pkg/log"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

var (
	watcherCtx      context.Context
	watcherCancel   context.CancelFunc
	watcherWg       sync.WaitGroup
	watcherCallback func(*spi.AgentChatSession) // Callback for session updates
	watcherDebugRaw bool                        // Whether to write debug raw data files
	watcherMutex    sync.RWMutex                // Protects watcherCallback and watcherDebugRaw
)

func init() {
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
}

// SetWatcherCallback sets the callback function for session updates
func SetWatcherCallback(callback func(*spi.AgentChatSession)) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = callback
	slog.Info("SetWatcherCallback: Callback set", "isNil", callback == nil)
}

// ClearWatcherCallback clears the callback function
func ClearWatcherCallback() {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = nil
	slog.Info("ClearWatcherCallback: Callback cleared")
}

// SetWatcherDebugRaw sets whether to write debug raw data files
func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
	slog.Debug("SetWatcherDebugRaw: Debug raw set", "debugRaw", debugRaw)
}

// getWatcherDebugRaw returns the current debug raw setting (thread-safe)
func getWatcherDebugRaw() bool {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherDebugRaw
}

// getWatcherCallback returns the current callback function (thread-safe)
func getWatcherCallback() func(*spi.AgentChatSession) {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherCallback
}

// StopWatcher gracefully stops the watcher goroutine
func StopWatcher() {
	slog.Info("StopWatcher: Signaling watcher to stop")
	watcherCancel()
	slog.Info("StopWatcher: Waiting for watcher goroutine to finish")
	watcherWg.Wait()
	slog.Info("StopWatcher: Watcher stopped")
}

// WatchForProjectDir watches for a project directory that matches the current working directory
func WatchForProjectDir() error {
	slog.Info("WatchForProjectDir: Determining project directory to monitor")
	claudeProjectDir, err := GetClaudeCodeProjectDir("")
	if err != nil {
		slog.Error("WatchForProjectDir: Failed to get project directory", "error", err)
		// Check if error is specifically about missing Claude projects directory
		if strings.Contains(err.Error(), "claude projects directory not found") {
			slog.Error("WatchForProjectDir: Claude projects directory not found while starting setup watcher")
			// Start watching for Claude directory creation
			return WatchForClaudeSetup()
		}
		return fmt.Errorf("failed to get project directory: %v", err)
	}

	slog.Info("WatchForProjectDir: Project directory found", "directory", claudeProjectDir)
	return startProjectWatcher(claudeProjectDir)
}

// startProjectWatcher starts watching a specific project directory
func startProjectWatcher(claudeProjectDir string) error {
	slog.Info("startProjectWatcher: Creating file watcher", "directory", claudeProjectDir)

	// Create a new watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %v", err)
	}

	// Increment wait group before starting goroutine
	watcherWg.Add(1)

	// Start watching in a goroutine
	go func() {
		// Decrement wait group when done
		defer watcherWg.Done()

		// Log when goroutine starts
		slog.Info("startProjectWatcher: Goroutine started")

		// Defer cleanup
		defer func() {
			slog.Info("startProjectWatcher: Goroutine exiting, closing watcher")
			_ = watcher.Close() // Best effort cleanup; errors here are not recoverable
		}()

		// Check if project directory already exists
		if _, err := os.Stat(claudeProjectDir); err == nil {
			slog.Info("startProjectWatcher: Project directory exists", "directory", claudeProjectDir)
			// Claude Code triggers file system events when it starts writing, so we'll catch the session on the first write event.
			slog.Info("startProjectWatcher: Waiting for JSONL file system events")
		} else {
			// Project directory doesn't exist yet, watch parent directory (or other error)
			parentDir := filepath.Dir(claudeProjectDir)
			projectName := filepath.Base(claudeProjectDir)
			slog.Warn("startProjectWatcher: Cannot access project directory, watching parent",
				"error", err,
				"parentDir", parentDir,
				"projectName", projectName)

			// Add parent directory to watcher
			if err := watcher.Add(parentDir); err != nil {
				log.UserError("Error watching parent directory: %v", err)
				slog.Error("startProjectWatcher: Failed to watch parent directory", "error", err)
				return
			}

			// Wait for project directory to be created
			waitingForCreation := true
			slog.Info("startProjectWatcher: Now watching for events in parent directory")
			for waitingForCreation {
				select {
				case <-watcherCtx.Done():
					slog.Info("startProjectWatcher: Context cancelled while waiting for directory creation")
					return
				case event, ok := <-watcher.Events:
					if !ok {
						slog.Info("startProjectWatcher: Watcher events channel closed")
						return
					}
					slog.Info("startProjectWatcher: Parent directory event",
						"operation", event.Op.String(),
						"path", event.Name,
						"lookingFor", projectName)
					eventBaseName := filepath.Base(event.Name)
					slog.Info("startProjectWatcher: Event basename comparison",
						"basename", eventBaseName,
						"comparingWith", projectName,
						"equalFold", strings.EqualFold(eventBaseName, projectName))
					if event.Has(fsnotify.Create) && strings.EqualFold(filepath.Base(event.Name), projectName) {
						slog.Info("startProjectWatcher: Detected project directory creation",
							"path", event.Name)
						slog.Info("startProjectWatcher: Initial scan disabled; waiting for JSONL events")
						// Use the actual created directory name from the event
						claudeProjectDir = filepath.Join(parentDir, filepath.Base(event.Name))
						// Remove parent directory and continue to watch project directory
						_ = watcher.Remove(parentDir) // OK if this fails; we're moving to project dir
						waitingForCreation = false
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						slog.Info("startProjectWatcher: Watcher errors channel closed")
						return
					}
					log.UserWarn("Watcher error while waiting for project directory: %v", err)
					slog.Error("startProjectWatcher: Watcher error", "error", err)
				}
			}
		}

		// Add the project directory to the watcher
		// Re-stat to get the actual directory name with correct case
		actualProjectDir := claudeProjectDir
		if entries, err := os.ReadDir(filepath.Dir(claudeProjectDir)); err == nil {
			baseName := filepath.Base(claudeProjectDir)
			for _, entry := range entries {
				if strings.EqualFold(entry.Name(), baseName) {
					actualProjectDir = filepath.Join(filepath.Dir(claudeProjectDir), entry.Name())
					break
				}
			}
		}
		slog.Info("startProjectWatcher: Now monitoring project directory for changes",
			"directory", actualProjectDir)
		if err := watcher.Add(actualProjectDir); err != nil {
			log.UserWarn("Error watching project directory: %v", err)
			slog.Error("startProjectWatcher: Failed to monitor directory", "error", err)
			return
		}
		slog.Info("startProjectWatcher: Successfully monitoring project directory for future changes")

		// Watch for file events
		for {
			select {
			case <-watcherCtx.Done():
				slog.Info("startProjectWatcher: Context cancelled, stopping watcher")
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Only process JSONL files
				if !strings.HasSuffix(event.Name, ".jsonl") {
					continue
				}

				if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
					continue
				}

				// Handle different event types
				switch {
				case event.Has(fsnotify.Create):
					slog.Info("New JSONL file created", "file", event.Name)
					scanJSONLFiles(claudeProjectDir, event.Name)
				case event.Has(fsnotify.Write):
					slog.Info("JSONL file modified", "file", event.Name)
					slog.Info("Triggering scan due to file modification")
					scanJSONLFiles(claudeProjectDir, event.Name)
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.UserError("Watcher error: %v", err)
			}
		}
	}()

	return nil
}

// scanJSONLFiles scans JSONL files and optionally filters processing to a specific changed file
func scanJSONLFiles(claudeProjectDir string, changedFile ...string) {
	// Ensure logs are flushed even if we panic
	defer func() {
		if r := recover(); r != nil {
			log.UserWarn("PANIC in ScanJSONLFiles: %v", r)
			slog.Error("ScanJSONLFiles: PANIC recovered", "panic", r)
		}
	}()

	slog.Info("ScanJSONLFiles: === START SCAN ===", "timestamp", time.Now().Format(time.RFC3339))
	var targetFile string
	if len(changedFile) > 0 && changedFile[0] != "" {
		targetFile = changedFile[0]
		slog.Info("ScanJSONLFiles: Scanning JSONL files with changed file",
			"directory", claudeProjectDir,
			"changedFile", targetFile)
	} else {
		slog.Info("ScanJSONLFiles: Scanning JSONL files", "directory", claudeProjectDir)
	}
	// Parse files and create history files:
	parser := NewJSONLParser()
	var targetSessionUuid string

	// Set the target file for conditional debug output
	if targetFile != "" {
		SetDebugTargetFile(targetFile)
		defer ClearDebugTargetFile()

		sessionID, err := extractSessionIDFromFile(targetFile)
		if err != nil {
			slog.Warn("ScanJSONLFiles: Failed to extract session ID from file",
				"file", targetFile,
				"error", err)
			slog.Info("ScanJSONLFiles: Skipping scan due to file read error", "file", targetFile)
			return
		}
		if sessionID == "" {
			slog.Info("ScanJSONLFiles: Skipping scan; session ID not present yet", "file", targetFile)
			return
		}
		targetSessionUuid = sessionID
		slog.Info("ScanJSONLFiles: Extracted session ID from changed file",
			"sessionUuid", targetSessionUuid,
			"file", targetFile)
	}

	var parseErr error
	if targetSessionUuid != "" {
		parseErr = parser.ParseProjectSessionsForSession(claudeProjectDir, true, targetSessionUuid)
	} else {
		parseErr = parser.ParseProjectSessions(claudeProjectDir, true)
	}
	if parseErr != nil {
		log.UserWarn("Error parsing project sessions: %v", parseErr)
		slog.Error("ScanJSONLFiles: Error parsing project sessions", "error", parseErr)
		return
	}

	// Process JSONL sessions (isAutosave = true, silent = false for autosave)
	slog.Info("ScanJSONLFiles: Processing sessions",
		"sessionCount", len(parser.Sessions),
		"targetSessionUuid", targetSessionUuid)

	// Get the session callback
	callback := getWatcherCallback()
	if callback == nil {
		slog.Error("ScanJSONLFiles: No callback provided - this should not happen")
		return
	}

	slog.Info("ScanJSONLFiles: Processing sessions with callback")
	// Process sessions and call the callback for each
	for _, session := range parser.Sessions {
		// Skip empty sessions
		if len(session.Records) == 0 {
			continue
		}

		// Skip if we're targeting a specific session and this isn't it
		if targetSessionUuid != "" && session.SessionUuid != targetSessionUuid {
			continue
		}

		// Convert to AgentChatSession (workspaceRoot extracted from records' cwd field)
		agentSession := convertToAgentChatSession(session, "", getWatcherDebugRaw())
		if agentSession != nil {
			slog.Info("ScanJSONLFiles: Calling callback for session", "sessionId", agentSession.SessionID)
			// Call the callback in a goroutine to avoid blocking
			go func(s *spi.AgentChatSession) {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("ScanJSONLFiles: Callback panicked", "panic", r)
					}
				}()
				callback(s)
			}(agentSession)
		}
	}
	slog.Info("ScanJSONLFiles: Processing complete")
	slog.Info("ScanJSONLFiles: === END SCAN ===", "timestamp", time.Now().Format(time.RFC3339))
}

// WatchForClaudeSetup watches for the creation of Claude directories
func WatchForClaudeSetup() error {
	slog.Info("WatchForClaudeSetup: Starting Claude directory setup watcher")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	claudeDir := filepath.Join(homeDir, ".claude")
	projectsDir := filepath.Join(claudeDir, "projects")
	slog.Info("WatchForClaudeSetup: Directories",
		"homeDir", homeDir,
		"claudeDir", claudeDir,
		"projectsDir", projectsDir)

	// Determine what to watch
	var watchDir string
	var watchingFor string

	if _, err := os.Stat(claudeDir); os.IsNotExist(err) {
		// .claude doesn't exist, watch home directory
		watchDir = homeDir
		watchingFor = ".claude"
		slog.Warn("WatchForClaudeSetup: .claude directory does not exist")
		slog.Info("Claude directory not found, watching for creation of ~/.claude\n")
	} else {
		// .claude exists but projects doesn't, watch .claude
		watchDir = claudeDir
		watchingFor = "projects"
		slog.Warn("WatchForClaudeSetup: .claude directory exists, checking for projects")
		slog.Info("Claude directory found, watching for creation of ~/.claude/projects\n")
	}
	slog.Info("WatchForClaudeSetup: Will watch directory",
		"watchDir", watchDir,
		"watchingFor", watchingFor)

	// Create watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.UserWarn("Failed to create file watcher: %v", err)
		slog.Error("WatchForClaudeSetup: Failed to create file watcher", "error", err)
		return fmt.Errorf("failed to create file watcher: %v", err)
	}

	// Add directory to watch
	slog.Info("WatchForClaudeSetup: Adding directory to watcher", "directory", watchDir)
	if err := watcher.Add(watchDir); err != nil {
		_ = watcher.Close() // Clean up on error; close errors less important than Add error
		return fmt.Errorf("failed to watch directory %s: %v", watchDir, err)
	}
	slog.Info("WatchForClaudeSetup: Successfully started watching", "directory", watchDir)

	// Start watching in a goroutine
	go func() {
		defer func() { _ = watcher.Close() }() // Cleanup on exit; errors not recoverable

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				slog.Info("WatchForClaudeSetup: Received event",
					"operation", event.Op.String(),
					"path", event.Name)
				// Check if this is the directory we're waiting for (case-insensitive)
				if event.Has(fsnotify.Create) && strings.EqualFold(filepath.Base(event.Name), watchingFor) {
					slog.Info("Detected creation", "path", event.Name)
					slog.Info("WatchForClaudeSetup: Target directory created", "path", event.Name)

					// Wait a second to let things settle
					slog.Info("WatchForClaudeSetup: Waiting 1 second for things to settle")
					time.Sleep(1 * time.Second)

					// Check what we should do next
					if watchingFor == ".claude" {
						slog.Info("WatchForClaudeSetup: .claude was created, checking if projects exists")
						// We were watching for .claude, now check if projects exists
						if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
							slog.Info("WatchForClaudeSetup: projects directory does not exist, switching watcher")
							// Need to watch for projects directory
							_ = watcher.Remove(watchDir) // OK if fails; switching watch targets
							watchDir = claudeDir
							watchingFor = "projects"
							slog.Info("WatchForClaudeSetup: Switching to watch",
								"watchDir", watchDir,
								"watchingFor", watchingFor)
							if err := watcher.Add(watchDir); err != nil {
								log.UserWarn("Error switching to watch .claude directory: %v", err)
								slog.Error("WatchForClaudeSetup: Failed to switch watcher", "error", err)
								return
							}
							slog.Info("Now watching for creation of ~/.claude/projects\n")
							continue
						} else {
							slog.Info("WatchForClaudeSetup: projects directory already exists!")
						}
					}

					// If we get here, projects directory should exist
					slog.Info("WatchForClaudeSetup: Projects directory should now exist, cleaning up watcher")
					// Remove the current watcher
					_ = watcher.Remove(watchDir) // OK if fails; we're transitioning to project watcher

					// Start the normal project watcher
					slog.Info("Claude projects directory now exists, starting normal file watcher\n")
					// Try to get the project directory again
					slog.Info("WatchForClaudeSetup: Getting project directory name")
					if projectDir, err := GetClaudeCodeProjectDir(""); err == nil {
						slog.Info("WatchForClaudeSetup: Project directory will be", "directory", projectDir)
						if err := startProjectWatcher(projectDir); err != nil {
							log.UserWarn("Error starting project watcher: %v", err)
							slog.Error("WatchForClaudeSetup: Failed to start project watcher", "error", err)
						} else {
							slog.Info("WatchForClaudeSetup: Successfully started project watcher")
						}
					} else {
						log.UserWarn("Error getting project directory after Claude setup: %v", err)
						slog.Error("WatchForClaudeSetup: Failed to get project directory", "error", err)
					}
					return
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.UserWarn("Watcher error: %v", err)
				slog.Error("WatchForClaudeSetup: Watcher error", "error", err)
			}
		}
	}()

	return nil
}

// convertToAgentChatSession converts a Session to an AgentChatSession
// Returns nil if the session is warmup-only (no real messages)
func convertToAgentChatSession(session Session, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	// Skip empty sessions (no records)
	if len(session.Records) == 0 {
		return nil
	}

	// Write debug files first (even for warmup-only sessions)
	_ = make(map[int]int) // recordToFileNumber no longer needed
	if debugRaw {
		// Write debug files
		writeDebugRawFiles(session)
	}

	// Filter warmup messages
	filteredRecords := filterWarmupMessages(session.Records)

	// Skip if no real messages remain after filtering warmup
	if len(filteredRecords) == 0 {
		slog.Debug("Skipping warmup-only session", "sessionId", session.SessionUuid)
		return nil
	}

	// Create session with filtered records
	filteredSession := Session{
		SessionUuid: session.SessionUuid,
		Records:     filteredRecords,
	}

	// Get timestamp from root record
	rootRecord := filteredSession.Records[0]
	timestamp, ok := rootRecord.Data["timestamp"].(string)
	if !ok {
		slog.Warn("convertToAgentChatSession: No timestamp found in root record")
		return nil
	}

	// Get the slug
	slug := FileSlugFromRootRecord(filteredSession)

	// Generate SessionData from filtered session
	sessionData, err := GenerateAgentSession(filteredSession, workspaceRoot)
	if err != nil {
		slog.Error("Failed to generate SessionData", "sessionId", session.SessionUuid, "error", err)
		return nil
	}

	// Convert session records to JSONL format for raw data
	// Note: Uses unfiltered session.Records (not filteredSession.Records) to preserve
	// all records including warmup messages in the raw data for complete audit trail
	var rawDataBuilder strings.Builder
	for _, record := range session.Records {
		jsonBytes, _ := json.Marshal(record.Data)
		rawDataBuilder.Write(jsonBytes)
		rawDataBuilder.WriteString("\n")
	}

	return &spi.AgentChatSession{
		SessionID:   session.SessionUuid,
		CreatedAt:   timestamp, // ISO 8601 timestamp
		Slug:        slug,
		SessionData: sessionData,
		RawData:     rawDataBuilder.String(),
	}
}
