package deepseektui

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

const (
	defaultCommand = "deepseek"
	versionFlag    = "--version"
)

// Provider implements the spi.Provider interface for DeepSeek TUI.
type Provider struct{}

// NewProvider creates a new DeepSeek TUI provider instance.
func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "DeepSeek TUI"
}

// Check verifies that DeepSeek TUI is available and returns its resolved
// location and version information.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseCommand(customCommand)
	isCustom := strings.TrimSpace(customCommand) != ""
	slog.Info("Check: verifying DeepSeek TUI installation",
		"command", cmdName, "customCommand", isCustom)

	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		slog.Info("Check: binary not found on PATH", "command", cmdName, "error", err)
		msg := buildCheckErrorMessage("not_found", cmdName, isCustom, "")
		trackCheckFailure("deepseek", isCustom, cmdName, "", versionFlag, "", "not_found", err.Error())
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
		trackCheckFailure("deepseek", isCustom, cmdName, resolved, versionFlag, stderrOutput, errorType, err.Error())
		return spi.CheckResult{Success: false, Location: resolved, ErrorMessage: msg}
	}

	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = "unknown"
	}
	slog.Info("Check: succeeded", "resolved", resolved, "version", version)
	trackCheckSuccess("deepseek", isCustom, cmdName, resolved, version, versionFlag)
	return spi.CheckResult{Success: true, Version: version, Location: resolved}
}

// DetectAgent reports whether DeepSeek TUI has been used in the given project
// based on existing session files in ~/.deepseek/sessions/.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	files, err := listSessionFiles()
	if err != nil {
		if helpOutput {
			log.UserWarn("DeepSeek TUI detection failed: %v", err)
		}
		return false
	}
	if len(files) == 0 {
		if helpOutput {
			printDetectionHelp()
		}
		return false
	}
	if projectPath == "" {
		return true
	}
	for _, file := range files {
		if sessionMentionsProject(file.Path, projectPath) {
			return true
		}
	}
	if helpOutput {
		printDetectionHelp()
	}
	return false
}

// GetAgentChatSessions returns all available agent chat sessions from DeepSeek TUI,
// optionally filtered to sessions associated with the given projectPath.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	files, err := listSessionFiles()
	if err != nil {
		return nil, err
	}

	normalizedProject := strings.TrimSpace(projectPath)

	var matchingFiles []sessionFile
	for _, file := range files {
		if normalizedProject != "" && !sessionMentionsProject(file.Path, normalizedProject) {
			continue
		}
		matchingFiles = append(matchingFiles, file)
	}

	totalFiles := len(matchingFiles)
	result := make([]spi.AgentChatSession, 0, len(matchingFiles))

	for i, file := range matchingFiles {
		session, err := parseSessionFile(file.Path, true)
		if err != nil {
			slog.Debug("deepseek: skipping session", "path", file.Path, "error", err)
			if progress != nil {
				progress(i+1, totalFiles)
			}
			continue
		}
		chat := convertToAgentSession(session, projectPath, debugRaw)
		if chat != nil {
			result = append(result, *chat)
		}
		if progress != nil {
			progress(i+1, totalFiles)
		}
	}
	return result, nil
}

// GetAgentChatSession returns the agent chat session with the given ID.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	path, err := findSessionFileByID(sessionID)
	if err != nil || path == "" {
		return nil, err
	}
	if strings.TrimSpace(projectPath) != "" && !sessionMentionsProject(path, projectPath) {
		return nil, nil
	}
	session, err := parseSessionFile(path, true)
	if err != nil {
		return nil, err
	}
	return convertToAgentSession(session, projectPath, debugRaw), nil
}

// ExecAgentAndWatch executes DeepSeek TUI for the given project and watches
// for session updates.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: starting DeepSeek TUI execution and monitoring",
		"projectPath", projectPath,
		"customCommand", customCommand,
		"resumeSessionID", resumeSessionID,
		"debugRaw", debugRaw,
		"hasCallback", sessionCallback != nil)

	if sessionCallback == nil {
		slog.Info("ExecAgentAndWatch: no callback provided, running without watcher")
		return ExecuteDeepSeek(customCommand, resumeSessionID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("ExecAgentAndWatch: launching session watcher in background")
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- watchSessions(ctx, projectPath, debugRaw, sessionCallback)
	}()

	slog.Info("ExecAgentAndWatch: executing DeepSeek TUI", "command", customCommand)
	err := ExecuteDeepSeek(customCommand, resumeSessionID)
	slog.Info("ExecAgentAndWatch: DeepSeek TUI exited, stopping watcher", "execError", err)
	cancel()

	if werr := <-watchErr; werr != nil && !errors.Is(werr, context.Canceled) {
		slog.Warn("ExecAgentAndWatch: watcher stopped with error", "error", werr)
	}

	if err != nil {
		return fmt.Errorf("deepseek execution failed: %w", err)
	}
	slog.Info("ExecAgentAndWatch: complete")
	return nil
}

// WatchAgent monitors DeepSeek TUI sessions for the given project and invokes
// sessionCallback for each new or updated session until ctx is canceled.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: starting DeepSeek TUI activity monitoring",
		"projectPath", projectPath, "debugRaw", debugRaw)
	if sessionCallback == nil {
		return fmt.Errorf("session callback is required")
	}
	err := watchSessions(ctx, projectPath, debugRaw, sessionCallback)
	slog.Info("WatchAgent: watcher exited", "error", err)
	return err
}

// ListAgentChatSessions retrieves lightweight session metadata without full parsing.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	files, err := listSessionFiles()
	if err != nil {
		return nil, err
	}

	normalizedProject := strings.TrimSpace(projectPath)

	var result []spi.SessionMetadata
	for _, file := range files {
		if normalizedProject != "" && !sessionMentionsProject(file.Path, normalizedProject) {
			continue
		}

		metadata, err := extractSessionMetadata(file.Path)
		if err != nil {
			slog.Debug("deepseek: failed to extract session metadata", "path", file.Path, "error", err)
			continue
		}
		if metadata == nil {
			slog.Debug("deepseek: skipping empty session", "path", file.Path)
			continue
		}
		result = append(result, *metadata)
	}

	return result, nil
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
		builder.WriteString("DeepSeek TUI was not found.\n\n")
		if isCustom {
			builder.WriteString("• Verify the custom path you provided is executable.\n")
			fmt.Fprintf(&builder, "• Provided command: %s\n", command)
		} else {
			builder.WriteString("• Install DeepSeek TUI and ensure `deepseek` is on your PATH.\n")
			builder.WriteString("• Re-run `specstory check deepseek` after installation.\n")
		}
	case "permission_denied":
		builder.WriteString("SpecStory cannot execute DeepSeek TUI due to permissions.\n\n")
		fmt.Fprintf(&builder, "Try: chmod +x %s\n", command)
	default:
		builder.WriteString("`deepseek --version` failed.\n\n")
		if stderr != "" {
			builder.WriteString("Error output:\n")
			builder.WriteString(stderr)
			builder.WriteString("\n\n")
		}
		builder.WriteString("Run `deepseek --version` manually to diagnose, then retry.")
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
	log.UserMessage("No DeepSeek TUI sessions found under ~/.deepseek/sessions yet.\n")
	log.UserMessage("Run DeepSeek TUI inside this project to create a session, then rerun `specstory sync deepseek`.\n")
}
