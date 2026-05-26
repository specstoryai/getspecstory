package antigravitycli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// providerID is the analytics/registry tag for this provider.
const providerID = "antigravity"

// Provider implements the spi.Provider interface for the Antigravity CLI.
type Provider struct{}

// NewProvider creates a new Antigravity CLI provider instance.
func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return providerName
}

// Check verifies that the Antigravity CLI is available and returns its resolved
// location and version (`agy --version` prints a bare semver such as "1.0.2").
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseCommand(customCommand)
	isCustom := strings.TrimSpace(customCommand) != ""
	slog.Info("Check: verifying Antigravity CLI installation",
		"command", cmdName, "customCommand", isCustom)

	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		slog.Info("Check: binary not found on PATH", "command", cmdName, "error", err)
		msg := buildCheckErrorMessage("not_found", cmdName, isCustom, "")
		trackCheckFailure(providerID, isCustom, cmdName, "", versionFlag, "", "not_found", err.Error())
		return spi.CheckResult{Success: false, Location: "", ErrorMessage: msg}
	}
	slog.Info("Check: binary resolved", "command", cmdName, "resolved", resolved)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, versionFlag)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorType := classifyCheckError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		slog.Info("Check: version probe failed",
			"resolved", resolved, "errorType", errorType, "stderr", stderrOutput, "error", err)
		msg := buildCheckErrorMessage(errorType, resolved, isCustom, stderrOutput)
		trackCheckFailure(providerID, isCustom, cmdName, resolved, versionFlag, stderrOutput, errorType, err.Error())
		return spi.CheckResult{Success: false, Location: resolved, ErrorMessage: msg}
	}

	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = "unknown"
	}
	slog.Info("Check: succeeded", "resolved", resolved, "version", version)
	trackCheckSuccess(providerID, isCustom, cmdName, resolved, version, versionFlag)
	return spi.CheckResult{Success: true, Version: version, Location: resolved}
}

// DetectAgent reports whether the Antigravity CLI has been used in the given
// project, based on transcripts under ~/.gemini/antigravity-cli/brain/.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	files, err := listConversationFiles()
	if err != nil {
		if helpOutput {
			log.UserWarn("Antigravity CLI detection failed: %v", err)
		}
		return false
	}
	if len(files) == 0 {
		if helpOutput {
			printDetectionHelp()
		}
		return false
	}
	if strings.TrimSpace(projectPath) == "" {
		return true
	}

	history, _ := loadHistoryIndex()
	for _, file := range files {
		session, err := parseTranscript(file.ConversationID, file.Path, history, false)
		if err != nil {
			continue
		}
		if sessionMatchesProject(session, projectPath) {
			return true
		}
	}

	if helpOutput {
		printDetectionHelp()
	}
	return false
}

// GetAgentChatSessions returns all Antigravity CLI sessions, optionally filtered
// to those associated with projectPath.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	files, err := listConversationFiles()
	if err != nil {
		return nil, err
	}
	history, _ := loadHistoryIndex()

	total := len(files)
	result := make([]spi.AgentChatSession, 0, total)
	for i, file := range files {
		session, err := parseTranscript(file.ConversationID, file.Path, history, true)
		if err != nil {
			slog.Debug("antigravity: skipping session", "conversationId", file.ConversationID, "error", err)
			if progress != nil {
				progress(i+1, total)
			}
			continue
		}
		if !sessionMatchesProject(session, projectPath) {
			if progress != nil {
				progress(i+1, total)
			}
			continue
		}
		if chat := convertToAgentSession(session, projectPath, debugRaw); chat != nil {
			result = append(result, *chat)
		}
		if progress != nil {
			progress(i+1, total)
		}
	}
	return result, nil
}

// GetAgentChatSession returns the session with the given conversation ID.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	path, err := findTranscriptByID(sessionID)
	if err != nil || path == "" {
		return nil, err
	}
	history, _ := loadHistoryIndex()
	session, err := parseTranscript(sessionID, path, history, true)
	if err != nil {
		return nil, err
	}
	// When a session is explicitly requested by id, exclude it only if it has a
	// known workspace that mismatches the project. Unscoped sessions (text-only
	// print-mode, no recoverable workspace) are returned as requested.
	if strings.TrimSpace(projectPath) != "" && sessionWorkspaceKnown(session) && !sessionMatchesProject(session, projectPath) {
		return nil, nil
	}
	return convertToAgentSession(session, projectPath, debugRaw), nil
}

// ListAgentChatSessions returns lightweight metadata for all sessions without
// retaining full session content.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	files, err := listConversationFiles()
	if err != nil {
		return nil, err
	}
	history, _ := loadHistoryIndex()

	var result []spi.SessionMetadata
	for _, file := range files {
		session, err := parseTranscript(file.ConversationID, file.Path, history, false)
		if err != nil {
			slog.Debug("antigravity: failed to parse session", "conversationId", file.ConversationID, "error", err)
			continue
		}
		if !sessionMatchesProject(session, projectPath) {
			continue
		}
		meta := sessionMetadata(session, history)
		if meta == nil {
			slog.Debug("antigravity: skipping session with no user prompt", "conversationId", file.ConversationID)
			continue
		}
		result = append(result, *meta)
	}

	return result, nil
}

// ExecAgentAndWatch executes the Antigravity CLI for the given project and
// watches for transcript updates.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: starting Antigravity CLI execution and monitoring",
		"projectPath", projectPath,
		"customCommand", customCommand,
		"resumeSessionID", resumeSessionID,
		"debugRaw", debugRaw,
		"hasCallback", sessionCallback != nil)

	if sessionCallback == nil {
		slog.Info("ExecAgentAndWatch: no callback provided, running without watcher")
		return ExecuteAntigravity(customCommand, resumeSessionID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("ExecAgentAndWatch: launching session watcher in background")
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- watchSessions(ctx, projectPath, debugRaw, sessionCallback)
	}()

	slog.Info("ExecAgentAndWatch: executing Antigravity CLI", "command", customCommand)
	err := ExecuteAntigravity(customCommand, resumeSessionID)
	slog.Info("ExecAgentAndWatch: Antigravity CLI exited, stopping watcher", "execError", err)
	cancel()

	if werr := <-watchErr; werr != nil && !errors.Is(werr, context.Canceled) {
		slog.Warn("ExecAgentAndWatch: watcher stopped with error", "error", werr)
	}

	if err != nil {
		return fmt.Errorf("antigravity execution failed: %w", err)
	}
	slog.Info("ExecAgentAndWatch: complete")
	return nil
}

// WatchAgent monitors Antigravity CLI sessions for the given project and invokes
// sessionCallback for each new or updated session until ctx is canceled.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: starting Antigravity CLI activity monitoring",
		"projectPath", projectPath, "debugRaw", debugRaw)
	if sessionCallback == nil {
		return fmt.Errorf("session callback is required")
	}
	err := watchSessions(ctx, projectPath, debugRaw, sessionCallback)
	slog.Info("WatchAgent: watcher exited", "error", err)
	return err
}

// --- helpers shared across the package ---

func classifyCheckError(err error) string {
	var execErr *exec.Error
	var pathErr *os.PathError
	switch {
	case errors.As(err, &execErr) && execErr.Err == exec.ErrNotFound:
		return "not_found"
	case errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	default:
		return "version_failed"
	}
}

func buildCheckErrorMessage(errorType string, command string, isCustom bool, stderr string) string {
	var builder strings.Builder
	switch errorType {
	case "not_found":
		builder.WriteString("Antigravity CLI was not found.\n\n")
		if isCustom {
			builder.WriteString("• Verify the custom path you provided is executable.\n")
			fmt.Fprintf(&builder, "• Provided command: %s\n", command)
		} else {
			builder.WriteString("• Install the Antigravity CLI and ensure `agy` is on your PATH.\n")
			builder.WriteString("• Re-run `specstory check antigravity` after installation.\n")
		}
	case "permission_denied":
		builder.WriteString("SpecStory cannot execute the Antigravity CLI due to permissions.\n\n")
		fmt.Fprintf(&builder, "Try: chmod +x %s\n", command)
	default:
		builder.WriteString("`agy --version` failed.\n\n")
		if stderr != "" {
			builder.WriteString("Error output:\n")
			builder.WriteString(stderr)
			builder.WriteString("\n\n")
		}
		builder.WriteString("Run `agy --version` manually to diagnose, then retry.")
	}
	return builder.String()
}

func trackCheckSuccess(provider string, custom bool, commandPath string, resolvedPath string, version string, versionFlag string) {
	props := analytics.Properties{
		"provider":       provider,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version":        version,
		"version_flag":   versionFlag,
	}
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, props)
}

func trackCheckFailure(provider string, custom bool, commandPath string, resolvedPath string, versionFlag string, stderrOutput string, errorType string, message string) {
	props := analytics.Properties{
		"provider":       provider,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version_flag":   versionFlag,
		"error_type":     errorType,
		"error_message":  message,
	}
	if stderrOutput != "" {
		props["stderr"] = stderrOutput
	}
	analytics.TrackEvent(analytics.EventCheckInstallFailed, props)
}

func printDetectionHelp() {
	log.UserMessage("No Antigravity CLI sessions found under ~/.gemini/antigravity-cli/brain yet.\n")
	log.UserMessage("Run the Antigravity CLI (`agy`) inside this project to create a session, then rerun `specstory sync antigravity`.\n")
}
