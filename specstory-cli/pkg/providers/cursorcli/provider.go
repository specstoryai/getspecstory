package cursorcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/specstoryai/SpecStoryCLI/pkg/analytics"
	"github.com/specstoryai/SpecStoryCLI/pkg/log"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// Provider implements the SPI Provider interface for Cursor CLI
type Provider struct{}

// NewProvider creates a new Cursor CLI provider instance
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the human-readable name of this provider
func (p *Provider) Name() string {
	return "Cursor CLI"
}

// buildCheckErrorMessage creates a user-facing error message tailored to the failure type.
func buildCheckErrorMessage(errorType string, cursorCmd string, isCustom bool, stderrOutput string) string {
	var errorMsg strings.Builder

	switch errorType {
	case "not_found":
		errorMsg.WriteString(fmt.Sprintf("  🔍 Could not find Cursor CLI at: %s\n", cursorCmd))
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Here's how to fix this:\n")
		errorMsg.WriteString("\n")
		if isCustom {
			errorMsg.WriteString("     The specified path doesn't exist. Please check:\n")
			errorMsg.WriteString(fmt.Sprintf("     • Is cursor-agent installed at %s?\n", cursorCmd))
			errorMsg.WriteString("     • Did you type the path correctly?")
		} else {
			errorMsg.WriteString("     1. Make sure the Cursor CLI is installed:\n")
			errorMsg.WriteString("        • Visit https://cursor.com/cli to download and install the Cursor CLI\n")
			errorMsg.WriteString("\n")
			errorMsg.WriteString("     2. If it's already installed, try:\n")
			errorMsg.WriteString("        • Check if 'cursor-agent' is in your PATH\n")
			errorMsg.WriteString("        • Use -c flag to specify the full command to execute\n")
			errorMsg.WriteString("        • Example: specstory check cursor -c \"~/.cursor/bin/cursor-agent\"")
		}
	case "permission_denied":
		errorMsg.WriteString(fmt.Sprintf("  🔒 Permission denied when trying to run: %s\n", cursorCmd))
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Here's how to fix this:\n")
		errorMsg.WriteString(fmt.Sprintf("     • Check file permissions: chmod +x %s\n", cursorCmd))
		errorMsg.WriteString("     • Try running with elevated permissions if needed")
	case "no_output":
		errorMsg.WriteString("  ⚠️  No version information from cursor-agent\n")
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  🤔 The command ran but produced no output\n")
		errorMsg.WriteString("  ❓ Expected: Version information from cursor-agent\n")
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 This might not be the correct cursor-agent. Please check:\n")
		errorMsg.WriteString("     • Is this the Cursor CLI agent?\n")
		errorMsg.WriteString("     • Try running 'cursor-agent --version' manually\n")
		errorMsg.WriteString("     • Visit https://cursor.com/cli for installation help")
	default:
		errorMsg.WriteString("  ⚠️  Error running 'cursor-agent --version'\n")
		if stderrOutput != "" {
			errorMsg.WriteString(fmt.Sprintf("  📋 Error details: %s\n", stderrOutput))
		}
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Troubleshooting tips:\n")
		errorMsg.WriteString("     • Make sure the Cursor CLI is properly installed\n")
		errorMsg.WriteString("     • Try running 'cursor-agent --version' directly in your terminal\n")
		errorMsg.WriteString("     • Check https://cursor.com/cli for installation help")
	}

	return errorMsg.String()
}

// Check verifies Cursor CLI installation and returns version info
func (p *Provider) Check(customCommand string) spi.CheckResult {
	// Parse the command (no custom args for version check)
	cursorCmd, _ := parseCursorCommand(customCommand)

	// Determine if custom command was used
	isCustomCommand := customCommand != ""

	// Resolve the actual path of the command
	resolvedPath := cursorCmd
	if !filepath.IsAbs(cursorCmd) {
		// Try to find the command in PATH
		if path, err := exec.LookPath(cursorCmd); err == nil {
			resolvedPath = path
		}
	}

	// Run cursor-agent --version to check version
	cmd := exec.Command(cursorCmd, "--version")
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		// Track installation check failure
		errorType := "unknown"

		var execErr *exec.Error
		var pathErr *os.PathError

		// Check error types in order of specificity
		switch {
		case errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound:
			errorType = "not_found"
		case errors.As(err, &pathErr):
			if errors.Is(pathErr.Err, os.ErrNotExist) {
				errorType = "not_found"
			} else if errors.Is(pathErr.Err, os.ErrPermission) {
				errorType = "permission_denied"
			}
		case errors.Is(err, os.ErrPermission):
			errorType = "permission_denied"
		}

		stderrOutput := strings.TrimSpace(errOut.String())
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "cursor",
			"custom_command": isCustomCommand,
			"command_path":   cursorCmd,
			"error_type":     errorType,
			"error_message":  err.Error(),
		})

		errorMessage := buildCheckErrorMessage(errorType, cursorCmd, isCustomCommand, stderrOutput)

		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	// Check if we got any output
	output := strings.TrimSpace(out.String())
	if output == "" {
		// Track unexpected output error
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "cursor",
			"custom_command": isCustomCommand,
			"command_path":   cursorCmd,
			"error_type":     "no_output",
			"output":         "",
		})

		errorMessage := buildCheckErrorMessage("no_output", cursorCmd, isCustomCommand, "")

		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	// Success! Track it
	pathType := getPathType(cursorCmd, resolvedPath)
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       "cursor",
		"custom_command": isCustomCommand,
		"command_path":   resolvedPath,
		"path_type":      pathType,
		"version":        output,
	})

	slog.Debug("Cursor CLI check successful", "version", output, "location", resolvedPath)

	return spi.CheckResult{
		Success:      true,
		Version:      output,
		Location:     resolvedPath,
		ErrorMessage: "",
	}
}

// DetectAgent checks if Cursor CLI has been used in the given project path
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	agentIsPresent := false
	var expectedHashDir string

	// Get the cursor chats directory
	chatsDir, err := GetCursorChatsDir()
	if err != nil {
		slog.Debug("DetectAgent: Failed to get cursor chats directory", "error", err)
	} else {
		// Check if the cursor chats directory exists
		if _, err := os.Stat(chatsDir); err != nil {
			slog.Debug("DetectAgent: Cursor chats directory doesn't exist", "path", chatsDir)
		} else {
			// Get the project hash directory
			hashDir, err := GetProjectHashDir(projectPath)
			if err != nil {
				slog.Debug("DetectAgent: Failed to get project hash directory", "error", err)
			} else {
				expectedHashDir = hashDir

				// Get session directories
				sessionDirs, err := GetCursorSessionDirs(hashDir)
				if err != nil {
					slog.Debug("DetectAgent: No Cursor project found", "expected_path", hashDir, "error", err)
				} else {
					// Look for store.db in session subdirectories
					for _, sessionID := range sessionDirs {
						if HasStoreDB(hashDir, sessionID) {
							slog.Debug("DetectAgent: Cursor CLI project found", "hashDir", hashDir, "sessionID", sessionID)
							agentIsPresent = true
							break // Exit early once we find the first store.db
						}
					}

					if !agentIsPresent {
						slog.Debug("DetectAgent: No valid Cursor sessions found", "hashDir", hashDir)
					}
				}
			}
		}
	}

	// If helpOutput is requested and agent not found, provide helpful guidance
	if helpOutput && !agentIsPresent {
		fmt.Println() // Add visual separation
		log.UserWarn("No Cursor CLI project was found for this directory.\n")
		log.UserMessage("Cursor CLI hasn't created a project folder for your current directory yet.\n")
		log.UserMessage("This happens when Cursor CLI hasn't been run in this directory.\n\n")
		log.UserMessage("To fix this:\n")
		log.UserMessage("  1. Run `specstory run cursor` to start Cursor CLI in this directory\n")
		log.UserMessage("  2. Or run Cursor CLI directly with `cursor-agent`, then try syncing again\n\n")
		if expectedHashDir != "" {
			log.UserMessage("Expected project folder: %s\n", expectedHashDir)
		}
		fmt.Println() // Add trailing newline
	}

	return agentIsPresent
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	// Get the project hash directory
	hashDir, err := GetProjectHashDir(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get project hash directory: %w", err)
	}

	// Get all session directories
	sessionIDs, err := GetCursorSessionDirs(hashDir)
	if err != nil {
		// No project directory exists, return empty list
		return []spi.AgentChatSession{}, nil
	}

	// Collect sessions
	var sessions []spi.AgentChatSession
	for _, sessionID := range sessionIDs {
		// Check if this session has a store.db file
		if !HasStoreDB(hashDir, sessionID) {
			slog.Debug("Skipping session without store.db", "sessionID", sessionID)
			continue
		}

		// Get the session data
		session, err := p.GetAgentChatSession(projectPath, sessionID, debugRaw)
		if err != nil {
			slog.Debug("Failed to get session", "sessionID", sessionID, "error", err)
			continue // Skip sessions we can't read
		}
		if session != nil {
			sessions = append(sessions, *session)
		}
	}

	return sessions, nil
}

// GetAgentChatSession retrieves a single chat session by ID for the given project path
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	// Get the project hash directory
	hashDir, err := GetProjectHashDir(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get project hash directory: %w", err)
	}

	// Check if the session exists and has store.db
	if !HasStoreDB(hashDir, sessionID) {
		return nil, nil // Session not found or no store.db
	}

	// Build the session path
	sessionPath := filepath.Join(hashDir, sessionID)

	slog.Debug("Reading Cursor CLI session",
		"sessionID", sessionID,
		"sessionPath", sessionPath)

	// Read the session data from SQLite
	createdAt, slug, blobRecords, orphanRecords, err := ReadSessionData(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session data: %w", err)
	}

	// Generate SessionData from blob records
	sessionData, err := GenerateAgentSession(blobRecords, projectPath, sessionID, createdAt, slug)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SessionData: %w", err)
	}

	// Marshal blob records to JSON for raw data
	rawDataJSON, err := json.Marshal(blobRecords)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal blob records: %w", err)
	}
	rawData := string(rawDataJSON)

	// Write provider-specific debug output if requested
	if debugRaw {
		if err := writeDebugOutput(sessionID, rawData, orphanRecords); err != nil {
			slog.Debug("Failed to write debug output", "sessionID", sessionID, "error", err)
			// Don't fail the operation if debug output fails
		}
	}

	return &spi.AgentChatSession{
		SessionID:   sessionID,
		CreatedAt:   createdAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     rawData,
	}, nil
}

// ExecAgentAndWatch executes Cursor CLI in interactive mode and watches for session updates
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Cursor CLI execution and monitoring",
		"projectPath", projectPath,
		"customCommand", customCommand,
		"resumeSessionID", resumeSessionID,
		"debugRaw", debugRaw)

	// Validate resume session ID if provided (Cursor uses different format than Claude)
	if resumeSessionID != "" {
		resumeSessionID = strings.TrimSpace(resumeSessionID)
		slog.Info("Resuming Cursor session", "sessionId", resumeSessionID)
	}

	// Process any existing sessions first before starting the watcher
	slog.Info("Processing existing sessions...")
	existingSessionIDs := make(map[string]bool)
	existingSessions, err := p.GetAgentChatSessions(projectPath, debugRaw)
	if err != nil {
		slog.Error("Failed to get existing sessions", "error", err)
	} else {
		// Use a worker pool to limit concurrent session processing
		const maxWorkers = 10
		var initialWg sync.WaitGroup
		sessionChan := make(chan spi.AgentChatSession, len(existingSessions))

		// Queue all sessions
		for _, session := range existingSessions {
			existingSessionIDs[session.SessionID] = true
			sessionChan <- session
		}
		close(sessionChan)

		// Start worker goroutines (up to maxWorkers or number of sessions, whichever is less)
		numWorkers := maxWorkers
		if len(existingSessions) < maxWorkers {
			numWorkers = len(existingSessions)
		}

		if sessionCallback != nil && numWorkers > 0 {
			initialWg.Add(numWorkers)
			for i := 0; i < numWorkers; i++ {
				go func() {
					defer initialWg.Done()
					for session := range sessionChan {
						func(s spi.AgentChatSession) {
							defer func() {
								if r := recover(); r != nil {
									slog.Error("Session callback panicked", "panic", r, "sessionId", s.SessionID)
								}
							}()
							sessionCallback(&s)
						}(session)
					}
				}()
			}
		}

		// Wait for all workers to complete
		initialWg.Wait()
		slog.Info("Processed existing sessions", "count", len(existingSessions), "workers", numWorkers)
	}

	// Create and configure the watcher before starting it
	slog.Info("Initializing database monitoring...")
	watcher, err := NewCursorWatcher(projectPath, debugRaw, sessionCallback)
	if err != nil {
		// Log the error but don't fail - watcher might work later
		slog.Error("Failed to create database watcher", "error", err)
		watcher = nil
	} else if watcher != nil {
		// Tell the watcher about existing sessions and resumed session BEFORE starting
		watcher.SetInitialState(existingSessionIDs, resumeSessionID)

		// Now start the watcher
		if err := watcher.Start(); err != nil {
			slog.Error("Failed to start database watcher", "error", err)
			watcher = nil
		}
	}

	// Execute Cursor CLI - this blocks until Cursor exits
	slog.Info("Executing Cursor CLI", "command", customCommand)
	err = ExecuteCursorCLI(customCommand, resumeSessionID)

	// Stop the watcher if it was started
	if watcher != nil {
		slog.Info("Cursor CLI has exited, stopping watcher")
		watcher.Stop()
	}

	// Return any execution error
	if err != nil {
		return fmt.Errorf("cursor CLI execution failed: %w", err)
	}

	return nil
}

// WatchAgent watches for Cursor CLI agent activity and calls the callback with AgentChatSession
// Does NOT execute the agent - only watches for existing activity
// Runs until error or context cancellation
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Cursor CLI activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// The watcher callback directly passes AgentChatSession (which now contains SessionData)
	wrappedCallback := func(agentChatSession *spi.AgentChatSession) {
		slog.Debug("WatchAgent: Received AgentChatSession",
			"sessionID", agentChatSession.SessionID,
			"hasSessionData", agentChatSession.SessionData != nil)

		// Call the user's callback with the AgentChatSession
		sessionCallback(agentChatSession)
	}

	// Create a new Cursor watcher with the wrapped callback
	watcher, err := NewCursorWatcher(projectPath, debugRaw, wrappedCallback)
	if err != nil {
		slog.Error("WatchAgent: Failed to create Cursor watcher", "error", err)
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	// Get existing sessions to avoid processing pre-existing ones
	// (unless they're being resumed, but that's handled by the watcher)
	sessions, err := p.GetAgentChatSessions(projectPath, debugRaw)
	if err != nil {
		slog.Warn("WatchAgent: Failed to get existing sessions", "error", err)
		// Continue anyway - not fatal
	}

	// Build map of existing session IDs
	existingSessionIDs := make(map[string]bool)
	for _, session := range sessions {
		existingSessionIDs[session.SessionID] = true
	}

	// Set initial state (no resumed session for watch-only mode)
	watcher.SetInitialState(existingSessionIDs, "")

	// Start the watcher
	if err := watcher.Start(); err != nil {
		slog.Error("WatchAgent: Failed to start Cursor watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	slog.Info("WatchAgent: Watcher started, blocking until context cancelled")

	// Block until context is cancelled
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	watcher.Stop()

	return ctx.Err()
}

// writeDebugOutput writes debug JSON files for a Cursor CLI session
func writeDebugOutput(sessionID string, rawData string, orphanRecords []BlobRecord) error {
	// Parse the JSON array
	var blobs []BlobRecord
	if err := json.Unmarshal([]byte(rawData), &blobs); err != nil {
		return fmt.Errorf("failed to parse raw data: %w", err)
	}

	// Get the debug directory path
	debugDir := spi.GetDebugDir(sessionID)

	// Create the debug directory
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	// Write each blob as a pretty-printed JSON file
	for index, blob := range blobs {
		// Create filename with DAG index and rowid (1-based index for readability)
		filename := fmt.Sprintf("%d-%d.json", index+1, blob.RowID)
		filepath := filepath.Join(debugDir, filename)

		// Pretty print the blob
		prettyJSON, err := json.MarshalIndent(blob, "", "  ")
		if err != nil {
			slog.Debug("Failed to marshal blob to JSON", "rowid", blob.RowID, "error", err)
			continue
		}

		// Write the file
		if err := os.WriteFile(filepath, prettyJSON, 0644); err != nil {
			slog.Debug("Failed to write debug file", "path", filepath, "error", err)
			continue
		}

		slog.Debug("Wrote debug file", "path", filepath, "rowid", blob.RowID)
	}

	// Write orphaned blobs as well
	for _, blob := range orphanRecords {
		// Create filename with orphan prefix and rowid
		filename := fmt.Sprintf("orphan-%d.json", blob.RowID)
		filepath := filepath.Join(debugDir, filename)

		// Pretty print the blob
		prettyJSON, err := json.MarshalIndent(blob, "", "  ")
		if err != nil {
			slog.Debug("Failed to marshal orphan blob to JSON", "rowid", blob.RowID, "error", err)
			continue
		}

		// Write the file
		if err := os.WriteFile(filepath, prettyJSON, 0644); err != nil {
			slog.Debug("Failed to write orphan debug file", "path", filepath, "error", err)
			continue
		}

		slog.Debug("Wrote orphan debug file", "path", filepath, "rowid", blob.RowID)
	}

	return nil
}
