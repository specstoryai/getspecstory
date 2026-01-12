package codexcli

import (
	"bufio"
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

	"github.com/specstoryai/SpecStoryCLI/pkg/analytics"
	"github.com/specstoryai/SpecStoryCLI/pkg/log"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

const (
	KB                    = 1024
	MB                    = 1024 * 1024
	maxReasonableLineSize = 250 * MB // 250MB sanity limit to prevent OOM from malformed or malicious files
)

// codexSessionMetaPayload captures the payload embedded in the Codex CLI session metadata record.
type codexSessionMetaPayload struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

// codexSessionMeta represents the metadata stored as the first line of a Codex CLI session JSONL file.
type codexSessionMeta struct {
	Type      string                  `json:"type"`
	Timestamp string                  `json:"timestamp"`
	Payload   codexSessionMetaPayload `json:"payload"`
}

// codexSessionInfo holds information about a discovered Codex CLI session.
type codexSessionInfo struct {
	SessionID   string
	SessionPath string
	Meta        *codexSessionMeta
}

// Provider implements the SPI Provider interface for the OpenAI Codex CLI.
type Provider struct{}

// NewProvider creates a new Codex CLI provider instance.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the human-readable name of this provider.
func (p *Provider) Name() string {
	return "Codex CLI"
}

// buildCheckErrorMessage creates a user-facing error message tailored to the failure type.
func buildCheckErrorMessage(errorType string, codexCmd string, isCustom bool, stderrOutput string) string {
	var errorMsg strings.Builder

	switch errorType {
	case "not_found":
		errorMsg.WriteString(fmt.Sprintf("  🔍 Could not find Codex CLI at: %s\n\n", codexCmd))
		errorMsg.WriteString("  💡 Here's how to fix this:\n\n")
		if isCustom {
			errorMsg.WriteString("     • Double-check the custom command/path you provided\n")
			errorMsg.WriteString("     • Ensure the file exists and is executable\n")
			errorMsg.WriteString("     • If the binary lives elsewhere, point to it with an absolute path\n")
		} else {
			errorMsg.WriteString("     1. Check common installation locations:\n")
			errorMsg.WriteString("        • Homebrew: /opt/homebrew/bin/codex (installed via `brew install codex`)\n")
			errorMsg.WriteString("        • npm: $(npm bin -g)/codex (installed via `npm install -g codex`)\n")
			errorMsg.WriteString("\n")
			errorMsg.WriteString("     2. If it's already installed, try:\n")
			errorMsg.WriteString("        • Check if 'codex' is in your PATH\n")
			errorMsg.WriteString("        • Use -c flag to specify the full path\n")
			errorMsg.WriteString("        • Example: specstory check codex -c \"/opt/local/bin/codex\"")
		}
	case "permission_denied":
		errorMsg.WriteString(fmt.Sprintf("  🔒 Permission denied when trying to run: %s\n\n", codexCmd))
		errorMsg.WriteString("  💡 Try the following:\n")
		errorMsg.WriteString(fmt.Sprintf("     • Ensure the binary is executable: chmod +x %s\n", codexCmd))
		errorMsg.WriteString("     • Run the command manually to confirm it works outside SpecStory\n")
	case "no_output":
		errorMsg.WriteString("  ⚠️  No version information from codex\n\n")
		errorMsg.WriteString("  🤔 The command ran but produced no output\n")
		errorMsg.WriteString("  ❓ Expected: Version information from codex\n\n")
		errorMsg.WriteString("  💡 Please verify you're pointing at the Codex CLI binary:\n")
		errorMsg.WriteString("     • Try running '" + codexCmd + " --version' directly\n")
		errorMsg.WriteString("     • If you're using a wrapper script, pass the real codex binary with -c\n")
	default:
		errorMsg.WriteString(fmt.Sprintf("  ⚠️  Error running '%s --version'\n", codexCmd))
		if stderrOutput != "" {
			errorMsg.WriteString(fmt.Sprintf("  📋 Error details: %s\n", stderrOutput))
		}
		errorMsg.WriteString("\n")
		errorMsg.WriteString("  💡 Troubleshooting tips:\n")
		errorMsg.WriteString("     • Make sure the Codex CLI is correctly installed\n")
		errorMsg.WriteString("     • Try running 'codex --version' directly in your terminal\n")
		errorMsg.WriteString("     • Check https://developers.openai.com/codex/quickstart for installation help\n")
	}

	return errorMsg.String()
}

// Check verifies OpenAI Codex CLI installation and returns version info.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	codexCmd, _ := parseCodexCommand(customCommand)
	isCustomCommand := customCommand != ""

	resolvedPath := codexCmd
	if !filepath.IsAbs(codexCmd) {
		if path, err := exec.LookPath(codexCmd); err == nil {
			resolvedPath = path
		}
	}

	versionOutput, versionFlag, stderrOutput, err := runCodexVersionCommand(codexCmd)
	if err != nil {
		errorType := classifyCheckError(err)
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":            "codex",
			"custom_command":      isCustomCommand,
			"command_path":        codexCmd,
			"resolved_path":       resolvedPath,
			"error_type":          errorType,
			"version_flag":        versionFlag,
			"stderr":              stderrOutput,
			"error_message":       err.Error(),
			"path_classification": classifyCodexPath(codexCmd, resolvedPath),
		})

		errorMessage := buildCheckErrorMessage(errorType, codexCmd, isCustomCommand, stderrOutput)

		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	if versionOutput == "" {
		errorType := "no_output"
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":            "codex",
			"custom_command":      isCustomCommand,
			"command_path":        codexCmd,
			"resolved_path":       resolvedPath,
			"error_type":          errorType,
			"version_flag":        versionFlag,
			"stderr":              stderrOutput,
			"path_classification": classifyCodexPath(codexCmd, resolvedPath),
		})

		errorMessage := buildCheckErrorMessage(errorType, codexCmd, isCustomCommand, stderrOutput)

		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	pathType := classifyCodexPath(codexCmd, resolvedPath)
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       "codex",
		"custom_command": isCustomCommand,
		"command_path":   resolvedPath,
		"path_type":      pathType,
		"version":        versionOutput,
		"version_flag":   versionFlag,
	})

	slog.Debug("Codex CLI check successful", "version", versionOutput, "location", resolvedPath, "flag", versionFlag)

	return spi.CheckResult{
		Success:      true,
		Version:      versionOutput,
		Location:     resolvedPath,
		ErrorMessage: "",
	}
}

// DetectAgent checks if OpenAI Codex CLI has been used in the given project path.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	// Try to find sessions using the shared helper (stop on first match)
	sessions, err := findCodexSessions(projectPath, "", true)

	// Handle different error cases with helpful output
	if err != nil {
		// Check if it's a home directory error
		if strings.Contains(err.Error(), "home directory") {
			slog.Debug("DetectAgent: Failed to resolve user home directory", "error", err)
			if helpOutput {
				fmt.Println()
				log.UserWarn("Could not scan Codex CLI sessions for this directory.\n")
				log.UserMessage("Reason: failed to determine your home directory.\n")
				fmt.Println()
			}
			return false
		}

		// Check if it's a sessions directory error
		if strings.Contains(err.Error(), "sessions directory not accessible") || strings.Contains(err.Error(), "sessions root") {
			homeDir, _ := osUserHomeDir()
			sessionsRoot := codexSessionsRoot(homeDir)

			slog.Debug("DetectAgent: Codex sessions directory not accessible", "error", err)
			if helpOutput {
				fmt.Println()
				log.UserWarn("No Codex CLI sessions were found for this directory.\n")
				log.UserMessage("Codex CLI stores activity under ~/.codex/sessions/YYYY/MM/DD/. We couldn't find that directory.\n\n")
				log.UserMessage("To fix this:\n")
				log.UserMessage("  1. Run `specstory run codex` to launch Codex CLI in this project\n")
				log.UserMessage("  2. Or start Codex CLI manually, then run `specstory sync codex` afterward\n\n")
				log.UserMessage("Expected sessions directory: %s\n", sessionsRoot)
				fmt.Println()
			}
			return false
		}

		// Unknown error
		slog.Debug("DetectAgent: Error finding sessions", "error", err)
		return false
	}

	// Check if we found any sessions
	if len(sessions) > 0 {
		session := sessions[0]
		slog.Debug("DetectAgent: Codex CLI activity detected",
			"sessionID", session.SessionID,
			"sessionPath", session.SessionPath)
		return true
	}

	// No sessions found - provide helpful output
	if helpOutput {
		homeDir, _ := osUserHomeDir()
		sessionsRoot := codexSessionsRoot(homeDir)

		fmt.Println()
		log.UserWarn("No Codex CLI sessions were found for this directory.\n")
		log.UserMessage("Codex CLI hasn't saved a session with this working directory yet.\n")
		log.UserMessage("Codex stores sessions in ~/.codex/sessions/YYYY/MM/DD/ as JSONL files.\n\n")
		log.UserMessage("To fix this:\n")
		log.UserMessage("  1. Run `specstory run codex` to launch Codex CLI here\n")
		log.UserMessage("  2. Or open Codex CLI manually in this project, then try syncing again\n\n")
		log.UserMessage("Checked sessions directory: %s\n", sessionsRoot)
		fmt.Println()
	}

	return false
}

// GetAgentChatSessions retrieves chat sessions for the given project path.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	// Find all sessions for this project (don't stop on first)
	sessions, err := findCodexSessions(projectPath, "", false)
	if err != nil {
		// If sessions directory doesn't exist, return empty list (not an error)
		if strings.Contains(err.Error(), "sessions directory not accessible") ||
			strings.Contains(err.Error(), "sessions root") ||
			strings.Contains(err.Error(), "home directory") {
			return []spi.AgentChatSession{}, nil
		}
		return nil, fmt.Errorf("failed to find codex sessions: %w", err)
	}

	// Convert to AgentChatSession structs
	var result []spi.AgentChatSession
	for _, sessionInfo := range sessions {
		session, err := processSessionToAgentChat(&sessionInfo, projectPath, debugRaw)
		if err != nil {
			slog.Debug("GetAgentChatSessions: Failed to process session",
				"sessionID", sessionInfo.SessionID,
				"error", err)
			continue // Skip sessions we can't process
		}

		// Skip empty sessions (processSessionToAgentChat returns nil for empty sessions)
		if session == nil {
			slog.Debug("GetAgentChatSessions: Skipping empty session",
				"sessionID", sessionInfo.SessionID)
			continue
		}

		result = append(result, *session)
	}

	return result, nil
}

// GetAgentChatSession retrieves a chat session by ID.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	// Find the specific session (will short-circuit when found)
	sessions, err := findCodexSessions(projectPath, sessionID, false)
	if err != nil {
		// If sessions directory doesn't exist, return nil (not an error)
		if strings.Contains(err.Error(), "sessions directory not accessible") ||
			strings.Contains(err.Error(), "sessions root") ||
			strings.Contains(err.Error(), "home directory") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find codex sessions: %w", err)
	}

	// Session not found (empty result)
	if len(sessions) == 0 {
		return nil, nil
	}

	// Process the first (and only) session using shared helper
	return processSessionToAgentChat(&sessions[0], projectPath, debugRaw)
}

// ExecAgentAndWatch executes the Codex CLI in interactive mode and monitors for session updates.
// The function blocks until the Codex CLI exits. During execution, it watches for JSONL file changes
// and invokes sessionCallback for each update, enabling real-time markdown generation and cloud sync.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Codex CLI execution and monitoring",
		"projectPath", projectPath,
		"customCommand", customCommand,
		"resumeSessionID", resumeSessionID,
		"debugRaw", debugRaw)

	// Log if resuming a specific session
	if resumeSessionID != "" {
		slog.Info("ExecAgentAndWatch: Will resume specific Codex session", "sessionID", resumeSessionID)
	}

	// Set up the callback which enables real-time markdown generation and cloud sync during
	// interactive sessions. As Codex CLI writes JSONL updates, the watcher detects changes
	// and invokes this callback, allowing immediate processing without blocking the agent's
	// execution. The defer ensures cleanup when the agent exits.
	SetWatcherCallback(sessionCallback)
	defer ClearWatcherCallback()

	// Set debug raw mode for the watcher
	SetWatcherDebugRaw(debugRaw)

	// Start watching for Codex sessions in the background
	slog.Info("Initializing Codex session monitoring...")

	if err := WatchForCodexSessions(projectPath, resumeSessionID); err != nil {
		// Log the error but don't fail - watcher might work later
		slog.Error("Failed to start Codex session watcher", "error", err)
	}

	// Execute Codex CLI - this blocks until Codex exits
	slog.Info("Executing Codex CLI", "command", customCommand, "resumeSessionID", resumeSessionID)
	err := ExecuteCodex(customCommand, resumeSessionID)

	// Stop the watcher goroutine and wait for it to finish before returning
	slog.Info("Codex CLI has exited, stopping watcher")
	StopWatcher()

	// Return any execution error
	if err != nil {
		return fmt.Errorf("codex CLI execution failed: %w", err)
	}

	return nil
}

// WatchAgent watches for Codex CLI agent activity and calls the callback with AgentChatSession
// Does NOT execute the agent - only watches for existing activity
// Runs until error or context cancellation (blocks indefinitely)
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Codex CLI activity monitoring",
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

	// Start watching for Codex sessions in the background
	slog.Info("WatchAgent: Initializing Codex session monitoring...")

	if err := WatchForCodexSessions(projectPath, ""); err != nil {
		slog.Error("WatchAgent: Failed to start Codex session watcher", "error", err)
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

// findCodexSessions traverses the Codex CLI sessions directory structure and finds
// sessions that match the given project path.
//
// Short-circuit behavior:
//   - If targetSessionID is provided: stops when that specific session is found (ignores stopOnFirst)
//   - If targetSessionID is empty and stopOnFirst is true: stops after finding first matching session
//   - If targetSessionID is empty and stopOnFirst is false: returns all matching sessions
func findCodexSessions(projectPath string, targetSessionID string, stopOnFirst bool) ([]codexSessionInfo, error) {
	normalizedProjectPath := normalizeCodexPath(projectPath)
	if normalizedProjectPath == "" {
		slog.Debug("findCodexSessions: Unable to normalize project path", "projectPath", projectPath)
	}

	homeDir, err := osUserHomeDir()
	if err != nil {
		slog.Debug("findCodexSessions: Failed to resolve user home directory", "error", err)
		return nil, fmt.Errorf("failed to determine home directory: %w", err)
	}

	sessionsRoot := codexSessionsRoot(homeDir)
	rootInfo, err := os.Stat(sessionsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("findCodexSessions: Codex sessions directory not found", "path", sessionsRoot)
		} else {
			slog.Debug("findCodexSessions: Failed to stat Codex sessions directory", "path", sessionsRoot, "error", err)
		}
		return nil, fmt.Errorf("sessions directory not accessible: %w", err)
	}

	if !rootInfo.IsDir() {
		slog.Debug("findCodexSessions: Codex sessions root is not a directory", "path", sessionsRoot)
		return nil, fmt.Errorf("sessions root is not a directory: %s", sessionsRoot)
	}

	yearEntries, err := readDirSortedDesc(sessionsRoot)
	if err != nil {
		slog.Debug("findCodexSessions: Failed to read Codex sessions root", "path", sessionsRoot, "error", err)
		return nil, fmt.Errorf("failed to read sessions root: %w", err)
	}

	var sessions []codexSessionInfo

	// Traverse year/month/day directory structure
	for _, yearEntry := range yearEntries {
		if !yearEntry.IsDir() {
			continue
		}

		yearPath := filepath.Join(sessionsRoot, yearEntry.Name())
		monthEntries, err := readDirSortedDesc(yearPath)
		if err != nil {
			slog.Debug("findCodexSessions: Failed to read Codex year directory", "path", yearPath, "error", err)
			continue
		}

		for _, monthEntry := range monthEntries {
			if !monthEntry.IsDir() {
				continue
			}

			monthPath := filepath.Join(yearPath, monthEntry.Name())
			dayEntries, err := readDirSortedDesc(monthPath)
			if err != nil {
				slog.Debug("findCodexSessions: Failed to read Codex month directory", "path", monthPath, "error", err)
				continue
			}

			for _, dayEntry := range dayEntries {
				if !dayEntry.IsDir() {
					continue
				}

				dayPath := filepath.Join(monthPath, dayEntry.Name())
				sessionEntries, err := readDirSortedDesc(dayPath)
				if err != nil {
					slog.Debug("findCodexSessions: Failed to read Codex day directory", "path", dayPath, "error", err)
					continue
				}

				for _, sessionEntry := range sessionEntries {
					if sessionEntry.IsDir() {
						continue
					}
					if filepath.Ext(sessionEntry.Name()) != ".jsonl" {
						continue
					}

					sessionPath := filepath.Join(dayPath, sessionEntry.Name())
					meta, err := loadCodexSessionMeta(sessionPath)
					if err != nil {
						slog.Debug("findCodexSessions: Failed to load Codex session meta", "path", sessionPath, "error", err)
						continue
					}

					sessionID := strings.TrimSpace(meta.Payload.ID)
					normalizedCWD := normalizeCodexPath(meta.Payload.CWD)
					if normalizedCWD == "" {
						slog.Debug("findCodexSessions: Session meta missing cwd", "sessionID", sessionID, "path", sessionPath)
						continue
					}

					// Check if this session matches the project path
					matched := false
					if normalizedProjectPath != "" {
						if normalizedCWD == normalizedProjectPath || strings.EqualFold(normalizedCWD, normalizedProjectPath) {
							matched = true
						}
					} else if normalizedCWD == projectPath || strings.EqualFold(normalizedCWD, projectPath) {
						matched = true
					}

					if matched {
						// If we're searching for a specific session, skip sessions that don't match
						if targetSessionID != "" && sessionID != targetSessionID {
							continue
						}

						slog.Debug("findCodexSessions: Codex CLI session matched project",
							"sessionID", sessionID,
							"sessionPath", sessionPath)

						sessions = append(sessions, codexSessionInfo{
							SessionID:   sessionID,
							SessionPath: sessionPath,
							Meta:        meta,
						})

						// Short-circuit logic:
						// 1. If we have a target sessionID and this matches, we're done
						if targetSessionID != "" && sessionID == targetSessionID {
							slog.Debug("findCodexSessions: Found target session, stopping search", "sessionID", targetSessionID)
							return sessions, nil
						}

						// 2. If stopOnFirst is true (and no target sessionID), return first match
						if targetSessionID == "" && stopOnFirst {
							return sessions, nil
						}

						// 3. Otherwise, continue collecting all matching sessions
					}
				}
			}
		}
	}

	return sessions, nil
}

// readSessionRawData reads all JSONL lines from a Codex CLI session file and returns
// both the parsed records and the raw JSONL content.
func readSessionRawData(sessionPath string) ([]map[string]interface{}, string, error) {
	file, err := os.Open(sessionPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open session file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	var records []map[string]interface{}
	var rawBuilder strings.Builder

	// Use bufio.Reader instead of Scanner to handle arbitrarily large lines
	// Scanner has a token size limit (even with custom buffer), but Reader does not
	reader := bufio.NewReader(file)

	lineNumber := 0
	for {
		// Read line using ReadString which has no size limit
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")

		// EOF is expected at end of file, other errors are genuine failures
		if err != nil && err != io.EOF {
			return nil, "", fmt.Errorf("error reading line %d: %w", lineNumber+1, err)
		}

		// Determine if we're at end of file and if we have content to process
		atEOF := err == io.EOF
		hasContent := len(line) > 0

		// Increment line number for every line read (including empty lines) to match text editor line numbers
		if hasContent || !atEOF {
			lineNumber++
		}

		// If no content, either skip empty line or exit at EOF
		if !hasContent {
			if atEOF {
				break // Reached end of file with no content
			}
			continue // Empty line in middle of file, skip it
		}

		// Sanity check to prevent OOM from pathological files
		if len(line) > maxReasonableLineSize {
			slog.Warn("line exceeds reasonable size limit",
				"lineNumber", lineNumber,
				"sizeMB", len(line)/MB,
				"limitMB", maxReasonableLineSize/MB,
				"file", filepath.Base(sessionPath))
			return nil, "", fmt.Errorf("line %d exceeds reasonable size limit (%d MB): refusing to process potentially malformed file",
				lineNumber, maxReasonableLineSize/MB)
		}

		// Log when processing unusually large lines (helps debug performance issues)
		if len(line) > 10*MB {
			slog.Debug("processing large JSONL line",
				"lineNumber", lineNumber,
				"sizeMB", len(line)/MB,
				"file", filepath.Base(sessionPath))
		}

		// Trim whitespace and skip empty lines
		line = strings.TrimSpace(line)
		if line == "" {
			if atEOF {
				break
			}
			continue
		}

		// Add to raw data
		rawBuilder.WriteString(line)
		rawBuilder.WriteString("\n")

		// Parse JSON
		var record map[string]interface{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			slog.Debug("readSessionRawData: Failed to parse JSON line",
				"error", err,
				"lineNumber", lineNumber,
				"line", line[:min(len(line), 100)])
			// After processing record, check if we're done
			if atEOF {
				break
			}
			continue
		}

		records = append(records, record)

		// After processing record, check if we're done
		if atEOF {
			break
		}
	}

	return records, rawBuilder.String(), nil
}

// loadCodexSessionMeta reads the first JSON line from a session file and parses the session metadata.
func loadCodexSessionMeta(sessionPath string) (*codexSessionMeta, error) {
	file, err := os.Open(sessionPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		if scanErr := scanner.Err(); scanErr != nil {
			return nil, scanErr
		}
		return nil, errors.New("codex session meta not found")
	}

	line := strings.TrimSpace(scanner.Text())
	if line == "" {
		return nil, errors.New("codex session meta is empty")
	}

	var meta codexSessionMeta
	if err := json.Unmarshal([]byte(line), &meta); err != nil {
		return nil, fmt.Errorf("failed to parse codex session meta: %w", err)
	}

	if meta.Type != "session_meta" {
		return nil, fmt.Errorf("unexpected codex session record type: %s", meta.Type)
	}

	return &meta, nil
}

// processSessionToAgentChat converts a codexSessionInfo into an AgentChatSession by reading
// the session data, generating SessionData, and optionally writing debug files. Returns nil if
// the session is empty or cannot be read.
func processSessionToAgentChat(sessionInfo *codexSessionInfo, workspaceRoot string, debugRaw bool) (*spi.AgentChatSession, error) {
	// Read the session data
	records, rawData, err := readSessionRawData(sessionInfo.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session data: %w", err)
	}

	// Skip empty sessions
	if len(records) == 0 {
		return nil, nil
	}

	// Get timestamp from metadata
	timestamp := sessionInfo.Meta.Timestamp

	// Generate slug from first user message
	firstUserMessage := findFirstUserMessage(records)
	slug := spi.GenerateFilenameFromUserMessage(firstUserMessage)
	if slug == "" {
		slog.Debug("processSessionToAgentChat: No user message for slug yet",
			"sessionID", sessionInfo.SessionID)
	} else {
		slog.Debug("processSessionToAgentChat: Generated slug from user message",
			"sessionID", sessionInfo.SessionID,
			"slug", slug)
	}

	// Generate SessionData from records
	sessionData, err := GenerateAgentSession(records, workspaceRoot)
	if err != nil {
		slog.Error("Failed to generate SessionData", "sessionId", sessionInfo.SessionID, "error", err)
		return nil, fmt.Errorf("failed to generate SessionData: %w", err)
	}

	// Write provider-specific debug files if requested
	if debugRaw {
		if err := writeDebugRawFiles(sessionInfo.SessionID, records); err != nil {
			slog.Debug("processSessionToAgentChat: Failed to write debug files",
				"sessionID", sessionInfo.SessionID,
				"error", err)
			// Don't fail the operation if debug output fails
		}
	}

	return &spi.AgentChatSession{
		SessionID:   sessionInfo.SessionID,
		CreatedAt:   timestamp,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     rawData,
	}, nil
}

// writeDebugRawFiles writes debug JSON files for a Codex CLI session.
// Each record is written as a numbered JSON file in .specstory/debug/<session-id>/
func writeDebugRawFiles(sessionID string, records []map[string]interface{}) error {
	// Get the debug directory path
	debugDir := spi.GetDebugDir(sessionID)

	// Create the debug directory
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	// Write each record as a pretty-printed JSON file
	for index, record := range records {
		// Create filename with 1-based index for readability
		filename := fmt.Sprintf("%d.json", index+1)
		debugPath := filepath.Join(debugDir, filename)

		// Pretty print the record
		prettyJSON, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			slog.Debug("writeDebugRawFiles: Failed to marshal record to JSON",
				"index", index,
				"error", err)
			continue
		}

		// Write the file
		if err := os.WriteFile(debugPath, prettyJSON, 0644); err != nil {
			slog.Debug("writeDebugRawFiles: Failed to write debug file",
				"path", debugPath,
				"error", err)
			continue
		}

		slog.Debug("writeDebugRawFiles: Wrote debug file",
			"path", debugPath,
			"index", index)
	}

	return nil
}

// findFirstUserMessage extracts the first user message from Codex CLI session records.
// Returns empty string if no user message is found.
func findFirstUserMessage(records []map[string]interface{}) string {
	for _, record := range records {
		// Get record type
		recordType, ok := record["type"].(string)
		if !ok || recordType != "event_msg" {
			continue
		}

		// Get payload
		payload, ok := record["payload"].(map[string]interface{})
		if !ok {
			continue
		}

		// Check if this is a user message
		payloadType, ok := payload["type"].(string)
		if !ok || payloadType != "user_message" {
			continue
		}

		// Extract the message content
		message, ok := payload["message"].(string)
		if !ok || message == "" {
			continue
		}

		// Found the first user message
		slog.Debug("findFirstUserMessage: Found first user message",
			"message", message[:min(len(message), 100)])
		return message
	}

	slog.Debug("findFirstUserMessage: No user message found in session")
	return ""
}
