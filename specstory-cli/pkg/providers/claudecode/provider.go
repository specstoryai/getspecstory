package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
		fmt.Fprintf(&errorMsg, "  🔍 Could not find Claude Code at: %s\n", claudeCmd)
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Here's how to fix this:\n")
		errorMsg.WriteString("\n")
		if isCustom {
			errorMsg.WriteString("     The specified path doesn't exist. Please check:\n")
			fmt.Fprintf(&errorMsg, "     • Is Claude Code installed at %s?\n", claudeCmd)
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
		fmt.Fprintf(&errorMsg, "  🔒 Permission denied when trying to run: %s\n", claudeCmd)
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Here's how to fix this:\n")
		fmt.Fprintf(&errorMsg, "     • Check file permissions: chmod +x %s\n", claudeCmd)
		errorMsg.WriteString("     • Try running with elevated permissions if needed")
	case "unexpected_output":
		fmt.Fprintf(&errorMsg, "  ⚠️  Unexpected output from 'claude -v': %s\n", strings.TrimSpace(stderrOutput))
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 This might not be Claude Code. Expected output containing '(Claude Code)'.\n")
		errorMsg.WriteString("     • Make sure you have Claude Code installed, not a different 'claude' command\n")
		errorMsg.WriteString("     • Visit https://docs.anthropic.com/en/docs/claude-code/quickstart for installation")
	default:
		errorMsg.WriteString("  ⚠️  Error running 'claude -v'\n")
		if stderrOutput != "" {
			fmt.Fprintf(&errorMsg, "  📋 Error details: %s\n", stderrOutput)
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
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       "claude",
		"custom_command": isCustomCommand,
		"command_path":   resolvedPath,
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
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	// Get the Claude Code project directory
	claudeProjectDir, err := GetClaudeCodeProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	// Check if project directory exists
	if _, err := os.Stat(claudeProjectDir); os.IsNotExist(err) {
		return []spi.AgentChatSession{}, nil // No sessions if no project
	}

	// Parse JSONL files with progress callback
	parser := NewJSONLParser()
	err = parser.ParseProjectSessionsWithProgress(claudeProjectDir, progress)
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

// isSyntheticMessage checks if a message is synthetic/internal and should be skipped
// when looking for the first real user message (e.g. for the session title/slug).
// Filters warmup prompts, title-generation prompts, and slash-command noise (the
// command invocation, its local stdout/stderr, and the local-command caveat) — these
// are conversation scaffolding, not the user's actual prompt. Without this, a session
// that opens with a slash command gets an empty title and falls back to its UUID.
func isSyntheticMessage(content string) bool {
	// warmup and the <TEXTBLOCK> title-generation wrapper can be embedded anywhere
	// in the record, so match them with Contains.
	if strings.Contains(strings.ToLower(content), "warmup") {
		return true
	}
	if strings.Contains(content, "<TEXTBLOCK>") {
		return true
	}
	// The slash-command scaffolding tags (shared with reconstruction via
	// spi.SyntheticCommandTags) and the interrupt marker REPLACE the prompt, so they
	// appear at the very START of the record. Match by prefix so a real prompt that
	// merely mentions a tag mid-sentence is still treated as real. The interrupt
	// marker's "…for tool use" variant is caught by the same prefix.
	trimmed := strings.TrimSpace(content)
	for _, marker := range spi.SyntheticCommandTags {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return strings.HasPrefix(trimmed, "[Request interrupted by user")
}

// cleanSyntheticPrefixes strips known boilerplate prefixes that Claude Code
// prepends to user messages (e.g. plan mode). The real user content follows
// the prefix, so we strip rather than skip the message entirely.
func cleanSyntheticPrefixes(content string) string {
	prefixes := []string{
		"Implement the following plan:",
	}
	trimmed := strings.TrimSpace(content)
	lower := strings.ToLower(trimmed)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, strings.ToLower(prefix)) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return content
}

// extractContentText extracts plain text from a message content field.
// The content field may be either a plain string or a JSON array of typed content
// blocks (e.g. [{type:"text",text:"..."},{type:"tool_use",...}]).
// When it's an array, all "text"-type blocks are joined with newlines.
// This handles the Claude Code JSONL format where IDE context tags (e.g.
// <ide_opened_file>) are injected as separate text blocks before the real question.
func extractContentText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, block := range v {
			if m, ok := block.(map[string]interface{}); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, ok := m["text"].(string); ok && text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
		slog.Debug("extractContentText: extracted text from content block array",
			"blockCount", len(v),
			"textParts", len(parts))
		return strings.Join(parts, "\n")
	default:
		return ""
	}
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

		// Extract content from message - content may be a string or a list of blocks
		if message, ok := record.Data["message"].(map[string]interface{}); ok {
			content := extractContentText(message["content"])
			if content != "" {
				// Skip synthetic messages (warmup, title generation prompts, etc.)
				if isSyntheticMessage(content) {
					continue
				}
				return cleanSyntheticPrefixes(content)
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

// ListAgentChatSessions retrieves lightweight session metadata without full parsing
// This is much faster than GetAgentChatSessions as it only reads minimal data from each session
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	// Get the Claude Code project directory
	claudeProjectDir, err := GetClaudeCodeProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	// Check if project directory exists
	if _, err := os.Stat(claudeProjectDir); os.IsNotExist(err) {
		return []spi.SessionMetadata{}, nil // No sessions if no project
	}

	// Collect all JSONL files in the project directory
	var sessionFiles []string
	err = filepath.Walk(claudeProjectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		sessionFiles = append(sessionFiles, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan project directory: %w", err)
	}

	// Extract metadata from each session file
	sessions := make([]spi.SessionMetadata, 0, len(sessionFiles))
	for _, filePath := range sessionFiles {
		metadata, err := extractSessionMetadata(filePath)
		if err != nil {
			slog.Warn("Failed to extract session metadata",
				"file", filePath,
				"error", err)
			continue
		}

		// Skip warmup-only sessions (no metadata means warmup-only)
		if metadata == nil {
			slog.Debug("Skipping warmup-only session", "file", filePath)
			continue
		}

		sessions = append(sessions, *metadata)
	}

	return sessions, nil
}

// claudeSessionScan holds the minimal fields read from a session file in one pass:
// identity + first-message metadata, plus the originating cwd (Claude Code stamps a
// top-level "cwd" on every conversational record). foundRealMessage is false for
// warmup-only sessions (no real messages).
type claudeSessionScan struct {
	sessionID        string
	timestamp        string
	firstUserMessage string
	commandFallback  string // slash command title when there is no free-text prompt
	cwd              string
	foundRealMessage bool
}

// scanClaudeSession reads minimal data from a session file: session id, first-real
// timestamp, first user message, and originating cwd. Shared by the project-scoped
// metadata path and the global enumeration (ListAllAgentChatSessions).
func scanClaudeSession(filePath string) (*claudeSessionScan, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open session file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	reader := bufio.NewReader(file)
	scan := &claudeSessionScan{}

	// Read records until we find everything we need.
	// Why: ReadString can return data AND io.EOF on the last line (no trailing newline),
	// so we always process the line first, then check for EOF once at the bottom.
	lineNum := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("failed to read line: %w", readErr)
		}

		lineNum++
		line = strings.TrimSpace(line)

		if line != "" {
			// Parse JSON record
			var record map[string]interface{}
			if jsonErr := json.Unmarshal([]byte(line), &record); jsonErr != nil {
				slog.Warn("Skipping malformed JSONL line",
					"file", filepath.Base(filePath),
					"line", lineNum,
					"error", jsonErr)
			} else {
				// Extract session ID (from any record)
				if scan.sessionID == "" {
					if sid, ok := record["sessionId"].(string); ok {
						scan.sessionID = sid
					}
				}

				// Capture the originating cwd (first record that carries one). This is
				// the input to project-identity resolution for the restore index.
				if scan.cwd == "" {
					if cwd, ok := record["cwd"].(string); ok && cwd != "" {
						scan.cwd = cwd
					}
				}

				// Only process non-sidechain, non-system records for message extraction
				isSidechain, _ := record["isSidechain"].(bool)
				recordType, hasType := record["type"].(string)
				isSystemRecord := hasType && (recordType == "file-history-snapshot" || recordType == "file-change")

				if !isSidechain && !isSystemRecord {
					scan.foundRealMessage = true
					// created_at = the first real record that actually carries a timestamp.
					// (The leading {mode,sessionId,type} record has none, so we can't gate
					// timestamp capture on "first real record" — it would stay empty.)
					if scan.timestamp == "" {
						if ts, ok := record["timestamp"].(string); ok && ts != "" {
							scan.timestamp = ts
						}
					}

					// Extract first user message for slug (if this is a user message)
					// Content may be a string or a list of typed blocks (see extractContentText)
					if scan.firstUserMessage == "" && hasType && recordType == "user" {
						isMeta, _ := record["isMeta"].(bool)
						if !isMeta {
							if message, ok := record["message"].(map[string]interface{}); ok {
								content := extractContentText(message["content"])
								if content != "" {
								if isSyntheticMessage(content) {
										// A slash-command session may have no free-text prompt;
										// remember the command name as a fallback title.
										if scan.commandFallback == "" {
											scan.commandFallback = extractCommandName(content)
										}
									} else {
										scan.firstUserMessage = content
									}
								}
							}
						}
					}
				}
			}
		}

		// Single exit: found everything we need, or reached end of file
		if (scan.sessionID != "" && scan.timestamp != "" && scan.firstUserMessage != "" && scan.cwd != "") || readErr == io.EOF {
			break
		}
	}

	// Fall back to the slash command when there was no real user prompt (so a
	// command-only session shows e.g. "/code-review" instead of its UUID).
	if scan.firstUserMessage == "" {
		scan.firstUserMessage = scan.commandFallback
	}
	return scan, nil
}

// extractCommandName pulls the slash command from a Claude Code command record, e.g.
// "<command-name>/code-review</command-name>…" -> "/code-review". Falls back to the
// bare command-message name (prefixed with "/"). Returns "" when none is present.
func extractCommandName(content string) string {
	if c := between(content, "<command-name>", "</command-name>"); c != "" {
		return c
	}
	if c := between(content, "<command-message>", "</command-message>"); c != "" {
		return "/" + c
	}
	return ""
}

// between returns the text between the first open/close markers, trimmed, or "".
func between(s, openTag, closeTag string) string {
	_, after, ok := strings.Cut(s, openTag)
	if !ok {
		return ""
	}
	inner, _, ok := strings.Cut(after, closeTag)
	if !ok {
		return ""
	}
	return strings.TrimSpace(inner)
}

// extractSessionMetadata reads minimal data from a session file to extract metadata
// Returns nil if the session is warmup-only (no real messages)
func extractSessionMetadata(filePath string) (*spi.SessionMetadata, error) {
	scan, err := scanClaudeSession(filePath)
	if err != nil {
		return nil, err
	}

	// If no real message was found, this is a warmup-only session
	if !scan.foundRealMessage {
		return nil, nil
	}

	return &spi.SessionMetadata{
		SessionID: scan.sessionID,
		CreatedAt: scan.timestamp,
		Slug:      spi.GenerateFilenameFromUserMessage(scan.firstUserMessage),
		Name:      spi.GenerateReadableName(scan.firstUserMessage),
	}, nil
}

// ListAllAgentChatSessions enumerates every Claude Code session across all projects. It is the
// no-progress form of ListAllAgentChatSessionsProgress.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates every Claude Code session across all projects by
// walking ~/.claude/projects/*/ for *.jsonl (the originating cwd comes from inside each session;
// the project directory name is a lossy, irreversible encoding of the path), reporting scan
// progress into r (nil-safe). Headers are scanned in parallel across CPUs; output order is
// irrelevant (reindex dedups and sorts later). Implements spi.ProgressEnumerator. See
// docs/SESSIONS-DB.md.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	projectsDir, err := GetClaudeCodeProjectsDir()
	if err != nil {
		// No projects directory yet → nothing to enumerate (not an error).
		return []spi.GlobalSessionRef{}, nil
	}

	return spi.ScanSessionsInParallel(projectsDir, "claude", r, func(path string) (*spi.GlobalSessionRef, error) {
		scan, scanErr := scanClaudeSession(path)
		if scanErr != nil {
			return nil, scanErr
		}
		if !scan.foundRealMessage {
			return nil, nil // warmup-only or sidechain-only transcript (not a session)
		}
		return &spi.GlobalSessionRef{
			SessionID:  scan.sessionID,
			CreatedAt:  scan.timestamp,
			Slug:       spi.GenerateFilenameFromUserMessage(scan.firstUserMessage),
			Name:       spi.GenerateReadableName(scan.firstUserMessage),
			NativePath: path,
			OriginCwd:  scan.cwd,
		}, nil
	})
}
