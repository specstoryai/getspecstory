package droidcli

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
	"regexp"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const defaultFactoryCommand = "droid"

type Provider struct{}

// NewProvider creates a new Factory Droid CLI provider instance.
func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "Factory Droid CLI"
}

func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseDroidCommand(customCommand)
	isCustom := strings.TrimSpace(customCommand) != ""
	versionFlag := "--version"

	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		msg := buildCheckErrorMessage("not_found", cmdName, isCustom, "")
		trackCheckFailure("droid", isCustom, cmdName, "", classifyDroidPath(cmdName, ""), versionFlag, "", "not_found", err.Error())
		return spi.CheckResult{Success: false, Location: "", ErrorMessage: msg}
	}
	pathType := classifyDroidPath(cmdName, resolved)

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, versionFlag)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorType := classifyCheckError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		msg := buildCheckErrorMessage(errorType, resolved, isCustom, stderrOutput)
		trackCheckFailure("droid", isCustom, cmdName, resolved, pathType, versionFlag, stderrOutput, errorType, err.Error())
		return spi.CheckResult{Success: false, Location: resolved, ErrorMessage: msg}
	}

	versionOutput := sanitizeDroidVersion(stdout.String())
	version := extractDroidVersion(versionOutput)
	if version == "" {
		version = strings.TrimSpace(versionOutput)
	}
	trackCheckSuccess("droid", isCustom, cmdName, resolved, pathType, version, versionFlag)
	return spi.CheckResult{Success: true, Version: version, Location: resolved}
}

func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	files, err := listSessionFiles()
	if err != nil {
		if helpOutput {
			log.UserWarn("Factory Droid detection failed: %v", err)
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

func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	files, err := listSessionFiles()
	if err != nil {
		return nil, err
	}
	result := make([]spi.AgentChatSession, 0, len(files))
	normalizedProject := strings.TrimSpace(projectPath)

	for _, file := range files {
		if normalizedProject != "" && !sessionMentionsProject(file.Path, normalizedProject) {
			continue
		}
		session, err := parseFactorySession(file.Path)
		if err != nil {
			slog.Debug("droidcli: skipping session", "path", file.Path, "error", err)
			continue
		}
		chat := convertToAgentSession(session, projectPath, debugRaw)
		if chat != nil {
			result = append(result, *chat)
		}
	}
	return result, nil
}

func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	path, err := findSessionFileByID(sessionID)
	if err != nil || path == "" {
		return nil, err
	}
	if strings.TrimSpace(projectPath) != "" && !sessionMentionsProject(path, projectPath) {
		return nil, nil
	}
	session, err := parseFactorySession(path)
	if err != nil {
		return nil, err
	}
	return convertToAgentSession(session, projectPath, debugRaw), nil
}

func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if sessionCallback == nil {
		return ExecuteDroid(customCommand, resumeSessionID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watchErr := make(chan error, 1)
	go func() {
		watchErr <- watchSessions(ctx, projectPath, debugRaw, sessionCallback)
	}()

	err := ExecuteDroid(customCommand, resumeSessionID)
	cancel()

	if werr := <-watchErr; werr != nil && !errors.Is(werr, context.Canceled) {
		slog.Warn("droidcli: watcher stopped with error", "error", werr)
	}

	if err != nil {
		return fmt.Errorf("factory droid CLI execution failed: %w", err)
	}
	return nil
}

func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if sessionCallback == nil {
		return fmt.Errorf("session callback is required")
	}
	return watchSessions(ctx, projectPath, debugRaw, sessionCallback)
}

func convertToAgentSession(session *fdSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	if session == nil {
		return nil
	}
	if len(session.Blocks) == 0 {
		return nil
	}
	sessionData, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Debug("droidcli: skipping session due to conversion error", "sessionId", session.ID, "error", err)
		return nil
	}
	created := strings.TrimSpace(sessionData.CreatedAt)
	chat := &spi.AgentChatSession{
		SessionID:   session.ID,
		CreatedAt:   created,
		Slug:        session.Slug,
		SessionData: sessionData,
		RawData:     session.RawData,
	}
	if debugRaw {
		if err := writeFactoryDebugRaw(session); err != nil {
			slog.Debug("droidcli: debug raw failed", "sessionId", session.ID, "error", err)
		}
	}
	return chat
}

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
		builder.WriteString("Factory Droid CLI was not found.\n\n")
		if isCustom {
			builder.WriteString("• Verify the custom path you provided is executable.\n")
			builder.WriteString(fmt.Sprintf("• Provided command: %s\n", command))
		} else {
			builder.WriteString("• Install the Factory CLI and ensure `droid` is on your PATH.\n")
			builder.WriteString("• Re-run `specstory check droid` after installation.\n")
		}
	case "permission_denied":
		builder.WriteString("SpecStory cannot execute the Factory CLI due to permissions.\n\n")
		builder.WriteString(fmt.Sprintf("Try: chmod +x %s\n", command))
	default:
		builder.WriteString("`droid --version` failed.\n\n")
		if stderr != "" {
			builder.WriteString("Error output:\n")
			builder.WriteString(stderr)
			builder.WriteString("\n\n")
		}
		builder.WriteString("Run `droid --version` manually to diagnose, then retry.")
	}
	return builder.String()
}

var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func sanitizeDroidVersion(raw string) string {
	clean := strings.ReplaceAll(raw, "\r", "")
	clean = ansiRegexp.ReplaceAllString(clean, "")
	return clean
}

func extractDroidVersion(raw string) string {
	lines := strings.Split(raw, "\n")
	var filtered []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) == 0 {
		return ""
	}
	return filtered[len(filtered)-1]
}

func trackCheckSuccess(provider string, custom bool, commandPath string, resolvedPath string, pathType string, version string, versionFlag string) {
	props := analytics.Properties{
		"provider":       provider,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"path_type":      pathType,
		"version":        version,
		"version_flag":   versionFlag,
	}
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, props)
}

func trackCheckFailure(provider string, custom bool, commandPath string, resolvedPath string, pathType string, versionFlag string, stderrOutput string, errorType string, message string) {
	props := analytics.Properties{
		"provider":       provider,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"path_type":      pathType,
		"version_flag":   versionFlag,
		"error_type":     errorType,
		"error_message":  message,
	}
	if stderrOutput != "" {
		props["stderr"] = stderrOutput
	}
	analytics.TrackEvent(analytics.EventCheckInstallFailed, props)
}

func classifyDroidPath(command string, resolvedPath string) string {
	if resolvedPath == "" {
		if filepath.IsAbs(command) {
			return "absolute_path"
		}
		return "unknown"
	}
	resolvedLower := strings.ToLower(resolvedPath)
	if strings.Contains(resolvedLower, "homebrew") || strings.Contains(resolvedLower, "/opt/homebrew/") {
		return "homebrew"
	}
	if strings.Contains(resolvedLower, "/.local/bin/") {
		return "user_local"
	}
	if strings.Contains(resolvedLower, "/.factory/") {
		return "factory_local"
	}
	if filepath.IsAbs(command) {
		return "absolute_path"
	}
	return "system_path"
}

func printDetectionHelp() {
	log.UserMessage("No Factory Droid sessions found under ~/.factory/sessions yet.\n")
	log.UserMessage("Run the Factory CLI inside this project to create a JSONL session, then rerun `specstory sync droid`.\n")
}

func sessionMentionsProject(filePath string, projectPath string) bool {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return false
	}

	canonicalProject := canonicalizePath(projectPath)
	if sessionRoot := extractSessionWorkspaceRoot(filePath); sessionRoot != "" {
		canonicalRoot := canonicalizePath(sessionRoot)
		if canonicalProject != "" && canonicalRoot != "" {
			return canonicalRoot == canonicalProject
		}
		return strings.TrimSpace(sessionRoot) == projectPath
	}

	return sessionMentionsProjectText(filePath, projectPath)
}

func extractSessionWorkspaceRoot(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer func() {
		_ = file.Close()
	}()

	var workspaceRoot string
	scanErr := scanLines(file, func(line string) error {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return nil
		}
		var env jsonlEnvelope
		if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
			return nil
		}
		if env.Type != "session_start" {
			return nil
		}
		var event sessionStartEvent
		if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
			return nil
		}
		workspaceRoot = firstNonEmpty(event.CWD, event.WorkspaceRoot, event.Session.CWD, event.Session.WorkspaceRoot)
		return errStopScan
	})
	if scanErr != nil && !errors.Is(scanErr, errStopScan) {
		return ""
	}

	return strings.TrimSpace(workspaceRoot)
}

func canonicalizePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	canonical, err := spi.GetCanonicalPath(trimmed)
	if err == nil {
		return canonical
	}
	if abs, absErr := filepath.Abs(trimmed); absErr == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

func sessionMentionsProjectText(filePath string, projectPath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()
	needle := strings.TrimSpace(projectPath)
	short := filepath.Base(projectPath)
	foundLines := 0
	err = scanLines(file, func(line string) error {
		if needle != "" && strings.Contains(line, needle) {
			return errStopScan
		}
		if short != "" && strings.Contains(line, short) {
			foundLines++
			if foundLines > 2 {
				return errStopScan
			}
		}
		return nil
	})

	if err != nil && !errors.Is(err, errStopScan) {
		slog.Warn("error scanning session file", "path", filePath, "err", err)
		return false
	}

	return errors.Is(err, errStopScan)
}

func writeFactoryDebugRaw(session *fdSession) error {
	if session == nil {
		return nil
	}
	dir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("droidcli: unable to create debug dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(session.RawData), 0o644); err != nil {
		return fmt.Errorf("droidcli: unable to write debug raw: %w", err)
	}
	meta := fmt.Sprintf("Session: %s\nTitle: %s\nCreated: %s\nBlocks: %d\n", session.ID, session.Title, session.CreatedAt, len(session.Blocks))
	return os.WriteFile(filepath.Join(dir, "summary.txt"), []byte(meta), 0o644)
}
