package claudecode

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

	"github.com/specstoryai/SpecStoryCLI/pkg/analytics"
	"github.com/specstoryai/SpecStoryCLI/pkg/log"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// Provider implements the SPI Provider interface for Claude Code
type Provider struct{}

// NewProvider creates a new Claude Code provider instance
func NewProvider() *Provider {
	return &Provider{}
}

// filterWarmupMessages removes warmup messages (sidechain messages before first real message)
func filterWarmupMessages(records []JSONLRecord) []JSONLRecord {
	// Find first non-sidechain message
	firstRealMessageIndex := -1
	for i, record := range records {
		if isSidechain, ok := record.Data["isSidechain"].(bool); !ok || !isSidechain {
			firstRealMessageIndex = i
			break
		}
	}

	// No real messages found
	if firstRealMessageIndex == -1 {
		return []JSONLRecord{}
	}

	// Return records starting from first real message
	return records[firstRealMessageIndex:]
}

// processSession filters warmup messages and converts a Session to an AgentChatSession
// Returns nil if the session is warmup-only (no real messages)
func processSession(session Session, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	// Write debug files first (even for warmup-only sessions)
	if debugRaw {
		// Write debug files and get the record-to-file mapping
		_ = writeDebugRawFiles(session) // Unused but needed for side effect
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
	timestamp := rootRecord.Data["timestamp"].(string)

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

// Name returns the human-readable name of this provider
func (p *Provider) Name() string {
	return "Claude Code"
}

// buildCheckErrorMessage creates a user-facing error message tailored to the failure type.
func buildCheckErrorMessage(errorType string, claudeCmd string, isCustom bool, stderrOutput string) string {
	var errorMsg strings.Builder

	switch errorType {
	case "not_found":
		errorMsg.WriteString(fmt.Sprintf("  🔍 Could not find Claude Code at: %s\n", claudeCmd))
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Here's how to fix this:\n")
		errorMsg.WriteString("\n")
		if isCustom {
			errorMsg.WriteString("     The specified path doesn't exist. Please check:\n")
			errorMsg.WriteString(fmt.Sprintf("     • Is Claude Code installed at %s?\n", claudeCmd))
			errorMsg.WriteString("     • Did you type the path correctly?")
		} else {
			errorMsg.WriteString("     1. Make sure Claude Code is installed:\n")
			errorMsg.WriteString("        • Visit https://docs.claude.com/en/docs/claude-code/quickstart\n")
			errorMsg.WriteString("\n")
			errorMsg.WriteString("     2. If it's already installed, try:\n")
			errorMsg.WriteString("        • Check if 'claude' is in your PATH\n")
			errorMsg.WriteString("        • Use -c flag to specify the full path\n")
			errorMsg.WriteString("        • Example: specstory check claude -c \"~/.claude/local/claude\"")
		}
	case "permission_denied":
		errorMsg.WriteString(fmt.Sprintf("  🔒 Permission denied when trying to run: %s\n", claudeCmd))
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Here's how to fix this:\n")
		errorMsg.WriteString(fmt.Sprintf("     • Check file permissions: chmod +x %s\n", claudeCmd))
		errorMsg.WriteString("     • Try running with elevated permissions if needed")
	case "unexpected_output":
		errorMsg.WriteString(fmt.Sprintf("  ⚠️  Unexpected output from 'claude -v': %s\n", strings.TrimSpace(stderrOutput)))
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 This might not be Claude Code. Expected output containing '(Claude Code)'.\n")
		errorMsg.WriteString("     • Make sure you have Claude Code installed, not a different 'claude' command\n")
		errorMsg.WriteString("     • Visit https://docs.anthropic.com/en/docs/claude-code/quickstart for installation")
	default:
		errorMsg.WriteString("  ⚠️  Error running 'claude -v'\n")
		if stderrOutput != "" {
			errorMsg.WriteString(fmt.Sprintf("  📋 Error details: %s\n", stderrOutput))
		}
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Troubleshooting tips:\n")
		errorMsg.WriteString("     • Make sure Claude Code is properly installed\n")
		errorMsg.WriteString("     • Try running 'claude -v' directly in your terminal\n")
		errorMsg.WriteString("     • Check https://docs.claude.com/en/docs/claude-code/quickstart for installation help")
	}

	return errorMsg.String()
}

// Check verifies Claude Code installation and returns version info
func (p *Provider) Check(customCommand string) spi.CheckResult {
	// Parse the command (no resume session for version check)
	claudeCmd, _ := parseClaudeCommand(customCommand, "")

	// Determine if custom command was used
	isCustomCommand := customCommand != ""

	// Resolve the actual path of the command
	resolvedPath := claudeCmd
	if !filepath.IsAbs(claudeCmd) {
		// Try to find the command in PATH
		if path, err := exec.LookPath(claudeCmd); err == nil {
			resolvedPath = path
		}
	}

	// Run claude -v to check version (ignore custom args for version check)
	cmd := exec.Command(claudeCmd, "-v")
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
			"provider":       "claude",
			"custom_command": isCustomCommand,
			"command_path":   claudeCmd,
			"error_type":     errorType,
			"error_message":  err.Error(),
		})

		errorMessage := buildCheckErrorMessage(errorType, claudeCmd, isCustomCommand, stderrOutput)

		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	// Check if output contains "Claude Code"
	output := out.String()
	if !strings.Contains(output, "(Claude Code)") {
		// Track unexpected output error
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "claude",
			"custom_command": isCustomCommand,
			"command_path":   claudeCmd,
			"error_type":     "unexpected_output",
			"output":         strings.TrimSpace(output),
		})

		errorMessage := buildCheckErrorMessage("unexpected_output", claudeCmd, isCustomCommand, output)

		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	// Success! Track it
	pathType := getPathType(claudeCmd, resolvedPath)
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       "claude",
		"custom_command": isCustomCommand,
		"command_path":   resolvedPath,
		"path_type":      pathType,
		"version":        strings.TrimSpace(output),
	})

	return spi.CheckResult{
		Success:      true,
		Version:      strings.TrimSpace(output),
		Location:     resolvedPath,
		ErrorMessage: "",
	}
}

// DetectAgent checks if Claude Code has been used in the given project directory
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	// Get the Claude Code project directory for the given path
	claudeProjectPath, err := GetClaudeCodeProjectDir(projectPath)
	if err != nil {
		slog.Debug("DetectAgent: Failed to get Claude Code project directory", "error", err)
		return false
	}

	// Check if the Claude Code project directory exists
	if info, err := os.Stat(claudeProjectPath); err == nil && info.IsDir() {
		slog.Debug("DetectAgent: Claude Code project found", "path", claudeProjectPath)
		return true
	}

	slog.Debug("DetectAgent: No Claude Code project found", "expected_path", claudeProjectPath)

	// If helpOutput is requested, provide helpful guidance
	if helpOutput {
		fmt.Println() // Add visual separation
		log.UserWarn("No Claude Code project found for this directory.\n")
		log.UserMessage("Claude Code hasn't created a project folder for your current directory yet.\n")
		log.UserMessage("This happens when Claude Code hasn't been run in this directory.\n\n")
		log.UserMessage("To fix this:\n")
		log.UserMessage("  1. Run 'specstory run' to start Claude Code in this directory\n")
		log.UserMessage("  2. Or run Claude Code directly with `claude`, then try syncing again\n\n")
		log.UserMessage("Expected project folder: %s\n", claudeProjectPath)
		fmt.Println() // Add trailing newline
	}

	return false
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	// Get the Claude Code project directory
	claudeProjectDir, err := GetClaudeCodeProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	// Check if project directory exists
	if _, err := os.Stat(claudeProjectDir); os.IsNotExist(err) {
		return []spi.AgentChatSession{}, nil // No sessions if no project
	}

	// Parse JSONL files
	parser := NewJSONLParser()
	err = parser.ParseProjectSessions(claudeProjectDir, true) // silent = true
	if err != nil {
		return nil, err
	}

	// Convert sessions to AgentChatSession structs
	result := make([]spi.AgentChatSession, 0, len(parser.Sessions))
	for _, session := range parser.Sessions {
		// Skip empty sessions (no records)
		if len(session.Records) == 0 {
			continue
		}

		// Process session (filter warmup, generate SessionData)
		chatSession := processSession(session, projectPath, debugRaw)
		if chatSession != nil {
			result = append(result, *chatSession)
		}
	}

	return result, nil
}

// GetAgentChatSession retrieves a single chat session by ID for the given project path
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	// Get the Claude Code project directory
	claudeProjectDir, err := GetClaudeCodeProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	// Check if project directory exists
	if _, err := os.Stat(claudeProjectDir); os.IsNotExist(err) {
		return nil, nil // No sessions if no project
	}

	// Parse only the session we're looking for
	parser := NewJSONLParser()
	err = parser.ParseSingleSession(claudeProjectDir, sessionID)
	if err != nil {
		// If session not found, return nil (not an error)
		if strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}

	// Find the session
	sessionPointer := parser.FindSession(sessionID)
	if sessionPointer == nil {
		return nil, nil
	}
	session := *sessionPointer

	// Skip empty sessions (no records)
	if len(session.Records) == 0 {
		return nil, nil
	}

	// Process session (filter warmup, generate SessionData)
	return processSession(session, projectPath, debugRaw), nil
}

// ExecAgentAndWatch executes Claude Code in interactive mode and watches for session updates
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Claude Code execution and monitoring",
		"projectPath", projectPath,
		"customCommand", customCommand,
		"resumeSessionID", resumeSessionID,
		"debugRaw", debugRaw)

	// Validate resume session ID if provided (Claude Code uses UUID format)
	if resumeSessionID != "" {
		resumeSessionID = strings.TrimSpace(resumeSessionID)
		// Simple UUID validation for Claude Code sessions
		if len(resumeSessionID) != 36 || !strings.Contains(resumeSessionID, "-") {
			return fmt.Errorf("invalid session ID format for Claude Code: %s (must be a UUID)", resumeSessionID)
		}
		slog.Info("Resuming Claude Code session", "sessionId", resumeSessionID)
	}

	// Set up the callback which enables real-time markdown generation and cloud sync during
	// interactive sessions. As Claude Code writes JSONL updates, the watcher detects
	// changes and invokes this callback, allowing immediate processing without blocking
	// the agent's execution. The defer ensures cleanup when the agent exits.
	SetWatcherCallback(sessionCallback)
	defer ClearWatcherCallback()

	// Set debug raw mode for the watcher
	SetWatcherDebugRaw(debugRaw)

	// Start watching for project directory in the background
	slog.Info("Initializing project directory monitoring...")

	if err := WatchForProjectDir(); err != nil {
		// Log the error but don't fail - watcher might work later
		slog.Error("Failed to start project directory watcher", "error", err)
	}

	// Execute Claude Code - this blocks until Claude exits
	slog.Info("Executing Claude Code", "command", customCommand)
	err := ExecuteClaude(customCommand, resumeSessionID)

	// Stop the watcher goroutine before returning
	slog.Info("Claude Code has exited, stopping watcher")
	StopWatcher()

	// Return any execution error
	if err != nil {
		return fmt.Errorf("claude Code execution failed: %w", err)
	}

	return nil
}

// WatchAgent watches for Claude Code agent activity and calls the callback with AgentChatSession
// Does NOT execute the agent - only watches for existing activity
// Runs until error or context cancellation
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Claude Code activity monitoring",
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

	// Set up the callback for the watcher
	SetWatcherCallback(wrappedCallback)
	defer ClearWatcherCallback()

	// Set debug raw mode for the watcher
	SetWatcherDebugRaw(debugRaw)

	// Get the Claude Code project directory for the specified project path
	slog.Info("WatchAgent: Initializing project directory monitoring...")

	claudeProjectDir, err := GetClaudeCodeProjectDir(projectPath)
	if err != nil {
		slog.Error("WatchAgent: Failed to get Claude Code project directory", "error", err)
		return fmt.Errorf("failed to get project directory: %w", err)
	}

	slog.Info("WatchAgent: Project directory found", "directory", claudeProjectDir)

	if err := startProjectWatcher(claudeProjectDir); err != nil {
		slog.Error("WatchAgent: Failed to start project watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Block until context is cancelled
	// The watcher runs in a background goroutine and will continue
	// until context is cancelled or the process exits
	slog.Info("WatchAgent: Watcher started, blocking until context cancelled")

	// Wait for context cancellation
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	StopWatcher()

	return ctx.Err()
}

// findFirstUserMessage finds the first user message in a session for slug generation
// Returns empty string if no suitable user message is found
func findFirstUserMessage(session Session) string {
	for _, record := range session.Records {
		// Check if this is a user message
		if recordType, ok := record.Data["type"].(string); !ok || recordType != "user" {
			continue
		}

		// Skip meta user messages
		if isMeta, ok := record.Data["isMeta"].(bool); ok && isMeta {
			continue
		}

		// Extract content from message
		if message, ok := record.Data["message"].(map[string]interface{}); ok {
			if content, ok := message["content"].(string); ok && content != "" {
				// Skip messages containing "warmup" (case-insensitive)
				if strings.Contains(strings.ToLower(content), "warmup") {
					continue
				}
				return content
			}
		}
	}
	return ""
}

// FileSlugFromRootRecord generates the slug part of the filename from the session
// Returns the human-readable slug derived from the first user message, or empty string if none
func FileSlugFromRootRecord(session Session) string {
	// Find the first user message and generate slug from it
	firstUserMessage := findFirstUserMessage(session)
	slog.Debug("FileSlugFromRootRecord: First user message",
		"sessionId", session.SessionUuid,
		"message", firstUserMessage)

	slug := spi.GenerateFilenameFromUserMessage(firstUserMessage)
	slog.Debug("FileSlugFromRootRecord: Generated slug",
		"sessionId", session.SessionUuid,
		"slug", slug)

	return slug
}
