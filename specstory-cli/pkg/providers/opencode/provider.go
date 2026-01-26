package opencode

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

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Provider implements the SPI Provider interface for OpenCode.
// OpenCode is a terminal-based AI coding assistant by SST that stores
// session data in JSON files at ~/.local/share/opencode/storage/
type Provider struct{}

// NewProvider creates a new OpenCode provider instance.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the human-readable name of this provider.
func (p *Provider) Name() string {
	return "OpenCode"
}

// Check verifies OpenCode installation and returns version info.
// Runs `opencode --version` and parses the output to verify installation.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseOpenCodeCommand(customCommand)
	isCustom := customCommand != ""

	// Try to find the command in PATH
	resolvedPath, err := exec.LookPath(cmdName)
	if err != nil {
		errorMessage := buildCheckErrorMessage("not_found", cmdName, isCustom, "")
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "opencode",
			"custom_command": isCustom,
			"command_path":   cmdName,
			"error_type":     "not_found",
			"error_message":  err.Error(),
		})
		return spi.CheckResult{
			Success:      false,
			Location:     "",
			ErrorMessage: errorMessage,
		}
	}

	// Run opencode --version to get version info
	cmd := exec.Command(cmdName, "--version")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorType := classifyCheckError(err)
		errorMessage := buildCheckErrorMessage(errorType, resolvedPath, isCustom, strings.TrimSpace(stderr.String()))
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "opencode",
			"custom_command": isCustom,
			"command_path":   resolvedPath,
			"error_type":     errorType,
			"error_message":  err.Error(),
		})

		return spi.CheckResult{
			Success:      false,
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	// Parse version from output
	// Note: Version validation is minimal - we accept whatever opencode --version returns.
	// OpenCode may have varying version output formats across releases.
	version := strings.TrimSpace(stdout.String())
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       "opencode",
		"custom_command": isCustom,
		"command_path":   resolvedPath,
		"version":        version,
	})

	return spi.CheckResult{
		Success:  true,
		Version:  version,
		Location: resolvedPath,
	}
}

// DetectAgent checks if OpenCode has been used in the given project path.
// Returns true if OpenCode session data exists for the project.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	projectDir, err := ResolveProjectDir(projectPath)
	if err != nil {
		if helpOutput {
			printDetectionHelp(err)
		}
		return false
	}

	// Check if the project directory contains any session files
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		slog.Debug("DetectAgent: Failed to read project directory",
			"path", projectDir,
			"error", err)
		if helpOutput {
			log.UserWarn("Failed to read OpenCode project directory: %v", err)
		}
		return false
	}

	// Look for session files (ses_*.json)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "ses_") && strings.HasSuffix(entry.Name(), ".json") {
			slog.Debug("DetectAgent: Found OpenCode session", "path", projectDir)
			return true
		}
	}

	if helpOutput {
		log.UserWarn("OpenCode data found at %s but no session files exist yet.", projectDir)
		log.UserMessage("Start an OpenCode session in this project to create session data.\n")
	}
	return false
}

// GetAgentChatSession retrieves a single chat session by ID.
// Loads the session, its messages, and parts, then converts to the unified schema.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSession: Loading session",
		"projectPath", projectPath,
		"sessionID", sessionID)

	// Compute project hash from path
	projectHash, err := ComputeProjectHash(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute project hash: %w", err)
	}

	// Load and assemble the session
	fullSession, err := LoadAndAssembleSession(projectHash, sessionID)
	if err != nil {
		// Check if it's a "not found" error
		if strings.Contains(err.Error(), "not found") {
			slog.Debug("GetAgentChatSession: Session not found",
				"sessionID", sessionID,
				"projectHash", projectHash)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	// Convert to AgentChatSession
	chatSession := convertToAgentChatSession(fullSession, projectPath, debugRaw)
	return chatSession, nil
}

// GetAgentChatSessions retrieves all chat sessions for the given project path.
// Loads all sessions, assembles them with messages and parts, and converts to unified schema.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSessions: Loading all sessions", "projectPath", projectPath)

	// Compute project hash from path
	projectHash, err := ComputeProjectHash(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute project hash: %w", err)
	}

	// Check if project directory exists
	projectDir, err := GetSessionsDir(projectHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions directory: %w", err)
	}

	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		slog.Debug("GetAgentChatSessions: No sessions directory found", "path", projectDir)
		return []spi.AgentChatSession{}, nil
	}

	// Load all sessions for the project
	fullSessions, err := LoadAllSessionsForProject(projectHash)
	if err != nil {
		return nil, fmt.Errorf("failed to load sessions: %w", err)
	}

	// Convert each session to AgentChatSession
	var result []spi.AgentChatSession
	for _, fullSession := range fullSessions {
		chatSession := convertToAgentChatSession(fullSession, projectPath, debugRaw)
		if chatSession != nil {
			result = append(result, *chatSession)
		}
	}

	slog.Info("GetAgentChatSessions: Loaded sessions",
		"projectPath", projectPath,
		"count", len(result))

	return result, nil
}

// ExecAgentAndWatch executes OpenCode in interactive mode and watches for session updates.
// This is a stub implementation that only executes OpenCode without file watching.
// Full watching functionality will be implemented in Step 7 (watcher.go).
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting OpenCode execution and monitoring",
		"projectPath", projectPath,
		"customCommand", customCommand,
		"resumeSessionID", resumeSessionID,
		"debugRaw", debugRaw)

	// Validate resume session ID if provided
	if resumeSessionID != "" {
		resumeSessionID = strings.TrimSpace(resumeSessionID)
		// OpenCode session IDs start with "ses_"
		if !strings.HasPrefix(resumeSessionID, "ses_") {
			slog.Warn("ExecAgentAndWatch: Resume session ID doesn't have expected prefix",
				"sessionID", resumeSessionID)
		}
		slog.Info("Resuming OpenCode session", "sessionId", resumeSessionID)
	}

	// TODO: Step 7 - Set up file watcher before executing OpenCode
	// Important: Process existing sessions BEFORE starting the watcher to ensure
	// we capture any sessions that were created before this run started.
	// This follows the same pattern as the Cursor CLI provider.
	// SetWatcherCallback(sessionCallback)
	// defer ClearWatcherCallback()
	// SetWatcherDebugRaw(debugRaw)
	// if err := WatchOpenCodeProject(projectPath, sessionCallback); err != nil {
	//     slog.Error("Failed to start watcher", "error", err)
	// }
	// defer StopWatcher()

	// Execute OpenCode
	slog.Info("Executing OpenCode", "command", customCommand)
	err := executeOpenCode(customCommand, resumeSessionID)

	if err != nil {
		return fmt.Errorf("OpenCode execution failed: %w", err)
	}

	return nil
}

// WatchAgent watches for OpenCode agent activity without executing OpenCode itself.
// This is a stub implementation that blocks until context cancellation.
// Full watching functionality will be implemented in Step 7 (watcher.go).
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting OpenCode activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// TODO: Step 7 - Implement file watching
	// 1. Set up debug mode: SetWatcherDebugRaw(debugRaw)
	// 2. Start watching: WatchOpenCodeProject(projectPath, sessionCallback)
	// 3. Block until context is cancelled
	// 4. Stop watcher on exit

	// For now, just wait for context cancellation
	slog.Info("WatchAgent: Watcher not yet implemented, blocking until context cancelled")
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled")
	return ctx.Err()
}

// parseOpenCodeCommand parses the custom command string into command and arguments.
// Returns the command name (or "opencode" if empty) and any additional arguments.
func parseOpenCodeCommand(customCommand string) (string, []string) {
	if customCommand == "" {
		return "opencode", nil
	}

	args := spi.SplitCommandLine(customCommand)
	if len(args) == 0 {
		return "opencode", nil
	}

	return args[0], args[1:]
}

// classifyCheckError determines the type of error from running opencode --version.
func classifyCheckError(err error) string {
	var execErr *exec.Error
	var pathErr *os.PathError

	switch {
	case errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound:
		return "not_found"
	case errors.As(err, &pathErr):
		if errors.Is(pathErr.Err, os.ErrPermission) {
			return "permission_denied"
		}
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	}
	return "version_failed"
}

// buildCheckErrorMessage creates a user-facing error message for Check failures.
func buildCheckErrorMessage(errorType string, opencodeCmd string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case "not_found":
		b.WriteString("OpenCode could not be found.\n\n")
		if isCustom {
			b.WriteString("  Verify the path you supplied points to the `opencode` executable.\n")
			b.WriteString(fmt.Sprintf("  Provided command: %s\n", opencodeCmd))
		} else {
			b.WriteString("  Install OpenCode from https://opencode.ai\n")
			b.WriteString("  Ensure `opencode` is on your PATH or pass a custom command via:\n")
			b.WriteString("    specstory check opencode -c \"path/to/opencode\"\n")
		}
	case "permission_denied":
		b.WriteString("OpenCode exists but isn't executable.\n\n")
		b.WriteString(fmt.Sprintf("  Fix permissions: chmod +x %s\n", opencodeCmd))
	default:
		b.WriteString("`opencode --version` failed.\n\n")
		if stderr != "" {
			b.WriteString(fmt.Sprintf("Error output:\n%s\n\n", stderr))
		}
		b.WriteString("  Try running `opencode --version` directly in your terminal.\n")
		b.WriteString("  If you upgraded recently, reinstall OpenCode to refresh dependencies.\n")
	}

	return b.String()
}

// printDetectionHelp prints helpful guidance when OpenCode detection fails.
func printDetectionHelp(err error) {
	var pathErr *OpenCodePathError
	if errors.As(err, &pathErr) {
		switch pathErr.Kind {
		case "storage_missing":
			log.UserWarn("OpenCode storage directory missing (%s).", pathErr.Path)
			log.UserMessage("Run OpenCode once (e.g., `opencode`) so ~/.local/share/opencode is created, then rerun this command.\n")
		case "project_missing":
			log.UserWarn("No OpenCode data found for this project (expected hash %s at %s).", pathErr.ProjectHash, pathErr.Path)
			if len(pathErr.KnownHashes) > 0 {
				log.UserMessage("Known OpenCode project hashes: %s\n", strings.Join(pathErr.KnownHashes, ", "))
			} else {
				log.UserMessage("No OpenCode projects detected yet under ~/.local/share/opencode/storage.\n")
			}
			log.UserMessage("Start an OpenCode session from your repo so the provider can pick it up.\n")
		case "global_session":
			log.UserWarn("Global OpenCode sessions are not supported.")
			log.UserMessage("SpecStory requires a project-specific context. Run OpenCode from a project directory.\n")
		default:
			log.UserWarn("OpenCode detection failed: %v", err)
		}
		return
	}

	log.UserWarn("OpenCode detection failed: %v", err)
}

// convertToAgentChatSession converts a FullSession to the provider-agnostic AgentChatSession format.
// Used by both sync mode (GetAgentChatSession/GetAgentChatSessions) and watch mode.
func convertToAgentChatSession(fullSession *FullSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	if fullSession == nil || fullSession.Session == nil {
		return nil
	}

	session := fullSession.Session

	// Skip sessions with no messages
	if len(fullSession.Messages) == 0 {
		slog.Debug("convertToAgentChatSession: Skipping empty session", "sessionId", session.ID)
		return nil
	}

	// Get provider version from Check (empty string if not checked)
	providerVersion := ""

	// Convert to SessionData using the schema conversion
	sessionData, err := ConvertToSessionData(fullSession, providerVersion)
	if err != nil {
		slog.Error("convertToAgentChatSession: Failed to convert session data",
			"sessionId", session.ID,
			"error", err)
		return nil
	}

	// Generate slug from first user message if not already set
	slug := sessionData.Slug
	if slug == "" {
		slug = "opencode-session"
	}

	// Build raw data as JSON
	rawDataBytes, err := json.Marshal(fullSession)
	if err != nil {
		slog.Debug("convertToAgentChatSession: Failed to marshal raw data",
			"sessionId", session.ID,
			"error", err)
		rawDataBytes = []byte("{}")
	}

	// Write provider-specific debug files if requested
	if debugRaw {
		if err := writeDebugRawFiles(fullSession); err != nil {
			slog.Debug("convertToAgentChatSession: Failed to write debug files",
				"sessionId", session.ID,
				"error", err)
		}
	}

	return &spi.AgentChatSession{
		SessionID:   session.ID,
		CreatedAt:   session.Time.Created,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataBytes),
	}
}

// writeDebugRawFiles writes debug JSON files for an OpenCode session.
// Each message is written as a numbered JSON file in .specstory/debug/<session-id>/
func writeDebugRawFiles(fullSession *FullSession) error {
	if fullSession == nil || fullSession.Session == nil {
		return fmt.Errorf("fullSession or session is nil")
	}

	debugDir := spi.GetDebugDir(fullSession.Session.ID)
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return fmt.Errorf("failed to create debug dir: %w", err)
	}

	// Write each message with its parts as a numbered JSON file
	for idx, fullMsg := range fullSession.Messages {
		number := idx + 1
		entry := map[string]interface{}{
			"index":     number,
			"messageId": fullMsg.Message.ID,
			"role":      fullMsg.Message.Role,
			"time":      fullMsg.Message.Time,
			"modelId":   fullMsg.Message.ModelID,
			"parts":     fullMsg.Parts,
		}

		data, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			slog.Debug("writeDebugRawFiles: Failed to marshal message",
				"index", number,
				"error", err)
			continue
		}

		filename := filepath.Join(debugDir, fmt.Sprintf("%d.json", number))
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			slog.Debug("writeDebugRawFiles: Failed to write file",
				"index", number,
				"error", err)
			continue
		}
		slog.Debug("writeDebugRawFiles: Wrote file", "path", filename, "index", number)
	}

	return nil
}

// executeOpenCode runs the opencode command with optional resume session.
func executeOpenCode(customCommand string, resumeSessionID string) error {
	cmdName, extraArgs := parseOpenCodeCommand(customCommand)

	// Build command arguments
	var args []string
	args = append(args, extraArgs...)

	// Add resume session if specified
	if resumeSessionID != "" {
		// OpenCode uses --resume or -r flag for resuming sessions
		args = append(args, "--resume", resumeSessionID)
	}

	slog.Debug("executeOpenCode: Running command",
		"command", cmdName,
		"args", args)

	cmd := exec.Command(cmdName, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
