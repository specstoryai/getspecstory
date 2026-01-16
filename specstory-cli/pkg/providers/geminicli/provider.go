package geminicli

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

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "Gemini CLI"
}

func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseGeminiCommand(customCommand)
	isCustom := customCommand != ""

	resolvedPath, err := exec.LookPath(cmdName)
	if err != nil {
		errorMessage := buildGeminiCheckErrorMessage("not_found", cmdName, isCustom, "")
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "gemini",
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

	cmd := exec.Command(cmdName, "--version")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorType := classifyGeminiCheckError(err)
		errorMessage := buildGeminiCheckErrorMessage(errorType, resolvedPath, isCustom, strings.TrimSpace(stderr.String()))
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "gemini",
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

	version := strings.TrimSpace(stdout.String())
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       "gemini",
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

func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	projectDir, err := ResolveGeminiProjectDir(projectPath)
	if err != nil {
		if helpOutput {
			printGeminiDetectionHelp(err)
		}
		return false
	}

	chatsDir := filepath.Join(projectDir, "chats")
	if _, err := os.Stat(chatsDir); err == nil {
		return true
	}

	if helpOutput {
		fmt.Printf("Gemini data found at %s but no chats/*.json files exist yet.\n", projectDir)
		fmt.Printf("Start a Gemini CLI session in this project so %s is created.\n", chatsDir)
		fmt.Printf("You can inspect %s for command history.\n", filepath.Join(projectDir, "logs.json"))
	}
	return false
}

func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	projectDir, err := ResolveGeminiProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	sessions, err := FindSessions(projectDir)
	if err != nil {
		return nil, err
	}

	var result []spi.AgentChatSession
	for _, s := range sessions {
		chatSession := convertToAgentChatSession(s, projectPath, debugRaw)
		if chatSession != nil {
			result = append(result, *chatSession)
		}
	}
	return result, nil
}

func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	projectDir, err := ResolveGeminiProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	// Ideally we would index them, but for now scan
	sessions, err := FindSessions(projectDir)
	if err != nil {
		return nil, err
	}

	for _, s := range sessions {
		if s.ID == sessionID {
			return convertToAgentChatSession(s, projectPath, debugRaw), nil
		}
	}

	return nil, nil
}

func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Gemini CLI", "project", projectPath)

	// Start watching
	SetWatcherDebugRaw(debugRaw)
	if err := WatchGeminiProject(projectPath, sessionCallback); err != nil {
		slog.Error("Failed to start watcher", "error", err)
	}
	defer StopWatcher()

	if resumeSessionID != "" {
		slog.Info("Attempting to resume Gemini CLI session", "sessionId", resumeSessionID)
	}

	return ExecuteGemini(customCommand, resumeSessionID)
}

// WatchAgent watches for Gemini CLI agent activity and calls the callback with AgentChatSession
// Does NOT execute the agent - only watches for existing activity
// Runs until error or context cancellation (blocks indefinitely)
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Gemini CLI activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// Set up debug mode
	SetWatcherDebugRaw(debugRaw)

	// Start watching for Gemini sessions
	if err := WatchGeminiProject(projectPath, sessionCallback); err != nil {
		slog.Error("WatchAgent: Failed to start Gemini session watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Block until context is cancelled
	slog.Info("WatchAgent: Watcher started, blocking until context cancelled")
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	StopWatcher()

	return ctx.Err()
}

func classifyGeminiCheckError(err error) string {
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

func buildGeminiCheckErrorMessage(errorType string, geminiCmd string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case "not_found":
		b.WriteString("Gemini CLI could not be found.\n\n")
		if isCustom {
			b.WriteString("• Verify the path you supplied actually points to the `gemini` executable.\n")
			b.WriteString(fmt.Sprintf("• Provided command: %s\n", geminiCmd))
		} else {
			b.WriteString("• Install Gemini CLI from https://ai.google.dev/gemini-cli/get-started\n")
			b.WriteString("• Ensure `gemini` is on your PATH or pass a custom command via `specstory check gemini -c \"path/to/gemini\"`.\n")
		}
	case "permission_denied":
		b.WriteString("Gemini CLI exists but isn't executable.\n\n")
		b.WriteString(fmt.Sprintf("• Fix permissions: `chmod +x %s`\n", geminiCmd))
		b.WriteString("• Some package managers install the binary as root; run SpecStory with a path you can execute.\n")
	default:
		b.WriteString("`gemini --version` failed.\n\n")
		if stderr != "" {
			b.WriteString(fmt.Sprintf("Error output:\n%s\n\n", stderr))
		}
		b.WriteString("• Try running `gemini --version` directly in your terminal.\n")
		b.WriteString("• If you upgraded recently, reinstall the CLI to refresh dependencies.\n")
	}

	return b.String()
}

func printGeminiDetectionHelp(err error) {
	var pathErr *GeminiPathError
	if errors.As(err, &pathErr) {
		switch pathErr.Kind {
		case "tmp_missing":
			log.UserWarn("Gemini tmp directory missing (%s).", pathErr.Path)
			log.UserMessage("Run the Gemini CLI once (e.g., `gemini`) so ~/.gemini/tmp is created, then rerun this command.\n")
		case "project_missing":
			log.UserWarn("No Gemini data found for this project (expected hash %s at %s).", pathErr.ProjectHash, pathErr.Path)
			if len(pathErr.KnownHashes) > 0 {
				log.UserMessage("Known Gemini project hashes: %s\n", strings.Join(pathErr.KnownHashes, ", "))
			} else {
				log.UserMessage("No Gemini projects detected yet under ~/.gemini/tmp.\n")
			}
			log.UserMessage("Start a Gemini CLI session from your repo so the provider can pick it up.\n")
		default:
			log.UserWarn("Gemini detection failed: %v", err)
		}
		return
	}

	log.UserWarn("Gemini detection failed: %v", err)
}

// convertToAgentChatSession converts a GeminiSession to the provider-agnostic AgentChatSession format.
// Used by both sync mode (GetAgentChatSession/GetAgentChatSessions) and watch mode.
func convertToAgentChatSession(session *GeminiSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	// Generate structured session data
	sessionData, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Error("convertToAgentChatSession: failed to generate session data",
			"sessionId", session.ID,
			"error", err)
		return nil
	}

	// Extract slug from first user message
	var slug string
	for _, msg := range session.Messages {
		if msg.Type == "user" {
			slug = spi.GenerateFilenameFromUserMessage(msgContent(msg))
			break
		}
	}
	if slug == "" {
		slug = "gemini-session"
	}

	// Raw data
	rawDataBytes, _ := json.Marshal(session)

	// Write provider-specific debug files if requested
	if debugRaw {
		if err := writeDebugRawFiles(session); err != nil {
			slog.Debug("convertToAgentChatSession: failed to write debug files",
				"sessionId", session.ID,
				"error", err)
		}
	}

	return &spi.AgentChatSession{
		SessionID:   session.ID,
		CreatedAt:   session.StartTime,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataBytes),
	}
}

// writeDebugRawFiles writes debug JSON files for a Gemini CLI session.
// Each message is written as a numbered JSON file in .specstory/debug/<session-id>/
func writeDebugRawFiles(session *GeminiSession) error {
	debugDir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return fmt.Errorf("failed to create debug dir: %w", err)
	}

	for idx, msg := range session.Messages {
		number := idx + 1
		entry := map[string]interface{}{
			"index":      number,
			"type":       msg.Type,
			"timestamp":  msg.Timestamp,
			"model":      msg.Model,
			"content":    msgContent(msg),
			"rawContent": msg.Content,
			"toolCalls":  msg.ToolCalls,
			"thoughts":   msg.Thoughts,
			"tokens":     msg.Tokens,
			"logs":       session.LogsForMessage(msgContent(msg)),
		}

		data, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			slog.Debug("writeDebugRawFiles: failed to marshal", "index", number, "error", err)
			continue
		}

		filename := filepath.Join(debugDir, fmt.Sprintf("%d.json", number))
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			slog.Debug("writeDebugRawFiles: failed to write", "index", number, "error", err)
			continue
		}
		slog.Debug("writeDebugRawFiles: wrote file", "path", filename, "index", number)
	}
	return nil
}
