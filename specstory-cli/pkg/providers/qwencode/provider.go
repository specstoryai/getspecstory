package qwencode

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

// Compile-time assertions that Provider satisfies the full spi.Provider
// contract plus the optional reindex capabilities.
var (
	_ spi.Provider           = (*Provider)(nil)
	_ spi.ProgressEnumerator = (*Provider)(nil)
	_ spi.PathSessionReader  = (*Provider)(nil)
)

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "Qwen Code"
}

func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseQwenCommand(customCommand)
	isCustom := customCommand != ""

	resolvedPath, err := exec.LookPath(cmdName)
	if err != nil {
		errorMessage := buildQwenCheckErrorMessage("not_found", cmdName, isCustom, "")
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "qwen",
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
		errorType := classifyQwenCheckError(err)
		errorMessage := buildQwenCheckErrorMessage(errorType, resolvedPath, isCustom, strings.TrimSpace(stderr.String()))
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":       "qwen",
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
		"provider":       "qwen",
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
	projectDir, err := ResolveQwenProjectDir(projectPath)
	if err != nil {
		if helpOutput {
			printQwenDetectionHelp(err)
		}
		return false
	}

	chatsDir := filepath.Join(projectDir, "chats")
	if _, err := os.Stat(chatsDir); err == nil {
		return true
	}

	if helpOutput {
		fmt.Printf("Qwen Code data found at %s but no chats/*.jsonl files exist yet.\n", projectDir)
		fmt.Printf("Start a Qwen Code session in this project so %s is created.\n", chatsDir)
	}
	return false
}

func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	projectDir, err := ResolveQwenProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	sessions, err := FindSessions(projectDir)
	if err != nil {
		return nil, err
	}

	totalSessions := len(sessions)
	var result []spi.AgentChatSession
	for i, s := range sessions {
		chatSession := convertToAgentChatSession(s, projectPath, debugRaw)
		if chatSession != nil {
			result = append(result, *chatSession)
		}

		// Report progress after each session
		if progress != nil {
			progress(i+1, totalSessions)
		}
	}
	return result, nil
}

func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	projectDir, err := ResolveQwenProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	// Sessions are stored one per file named <session-id>.jsonl, so resolve
	// directly by path before falling back to a scan (IDs are user-supplied
	// and may not match a filename exactly).
	directPath := filepath.Join(projectDir, "chats", sessionID+".jsonl")
	if _, err := os.Stat(directPath); err == nil {
		session, parseErr := ParseSessionFile(directPath)
		if parseErr == nil && len(session.Records) > 0 {
			return convertToAgentChatSession(session, projectPath, debugRaw), nil
		}
	}

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

// GetAgentChatSessionByPath parses a single session directly from its known
// native file path, skipping by-id discovery. Used by reindex (which already
// holds NativePath from enumeration) to keep resolving N sessions O(N).
func (p *Provider) GetAgentChatSessionByPath(nativePath string, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	session, err := ParseSessionFile(nativePath)
	if err != nil {
		return nil, err
	}
	if len(session.Records) == 0 {
		return nil, nil
	}
	return convertToAgentChatSession(session, originCwd, debugRaw), nil
}

func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Qwen Code", "project", projectPath)

	// Start watching
	SetWatcherDebugRaw(debugRaw)
	if err := WatchQwenProject(projectPath, sessionCallback); err != nil {
		slog.Error("Failed to start watcher", "error", err)
	}
	defer StopWatcher()

	if resumeSessionID != "" {
		slog.Info("Attempting to resume Qwen Code session", "sessionId", resumeSessionID)
	}

	return ExecuteQwen(customCommand, resumeSessionID)
}

// WatchAgent watches for Qwen Code agent activity and calls the callback with AgentChatSession
// Does NOT execute the agent - only watches for existing activity
// Runs until error or context cancellation (blocks indefinitely)
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Qwen Code activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	SetWatcherDebugRaw(debugRaw)

	if err := WatchQwenProject(projectPath, sessionCallback); err != nil {
		slog.Error("WatchAgent: Failed to start Qwen session watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Block until context is cancelled
	slog.Info("WatchAgent: Watcher started, blocking until context cancelled")
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	StopWatcher()

	return ctx.Err()
}

func classifyQwenCheckError(err error) string {
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

func buildQwenCheckErrorMessage(errorType string, qwenCmd string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case "not_found":
		b.WriteString("Qwen Code could not be found.\n\n")
		if isCustom {
			b.WriteString("• Verify the path you supplied actually points to the `qwen` executable.\n")
			fmt.Fprintf(&b, "• Provided command: %s\n", qwenCmd)
		} else {
			b.WriteString("• Install Qwen Code with `npm install -g @qwen-code/qwen-code` or see https://github.com/QwenLM/qwen-code\n")
			b.WriteString("• Ensure `qwen` is on your PATH or pass a custom command via `specstory check qwen -c \"path/to/qwen\"`.\n")
		}
	case "permission_denied":
		b.WriteString("Qwen Code exists but isn't executable.\n\n")
		fmt.Fprintf(&b, "• Fix permissions: `chmod +x %s`\n", qwenCmd)
		b.WriteString("• Some package managers install the binary as root; run SpecStory with a path you can execute.\n")
	default:
		b.WriteString("`qwen --version` failed.\n\n")
		if stderr != "" {
			fmt.Fprintf(&b, "Error output:\n%s\n\n", stderr)
		}
		b.WriteString("• Try running `qwen --version` directly in your terminal.\n")
		b.WriteString("• If you upgraded recently, reinstall the CLI to refresh dependencies.\n")
	}

	return b.String()
}

func printQwenDetectionHelp(err error) {
	var pathErr *QwenPathError
	if errors.As(err, &pathErr) {
		switch pathErr.Kind {
		case "projects_missing":
			log.UserWarn("Qwen projects directory missing (%s).", pathErr.Path)
			log.UserMessage("Run Qwen Code once (e.g., `qwen`) so ~/.qwen/projects is created, then rerun this command.\n")
		case "project_missing":
			log.UserWarn("No Qwen Code data found for this project (expected %s).", pathErr.Path)
			if len(pathErr.KnownDirs) > 0 {
				log.UserMessage("Known Qwen project directories: %s\n", strings.Join(pathErr.KnownDirs, ", "))
			} else {
				log.UserMessage("No Qwen projects detected yet under ~/.qwen/projects.\n")
			}
			log.UserMessage("Start a Qwen Code session from your repo so the provider can pick it up.\n")
		default:
			log.UserWarn("Qwen Code detection failed: %v", err)
		}
		return
	}

	log.UserWarn("Qwen Code detection failed: %v", err)
}

// convertToAgentChatSession converts a QwenSession to the provider-agnostic AgentChatSession format.
// Used by both sync mode (GetAgentChatSession/GetAgentChatSessions) and watch mode.
func convertToAgentChatSession(session *QwenSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	sessionData, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Error("convertToAgentChatSession: failed to generate session data",
			"sessionId", session.ID,
			"error", err)
		return nil
	}

	// Extract slug from first real user message
	slug := ""
	if text := session.FirstRealUserText(); text != "" {
		slug = spi.GenerateFilenameFromUserMessage(text)
	}
	if slug == "" {
		slug = "qwen-session"
	}
	sessionData.Slug = slug

	// Raw data: the original JSONL transcript
	rawData, err := os.ReadFile(session.FilePath)
	if err != nil {
		slog.Debug("convertToAgentChatSession: failed to read raw transcript",
			"path", session.FilePath, "error", err)
		rawData = nil
	}

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
		RawData:     string(rawData),
	}
}

// writeDebugRawFiles writes debug JSON files for a Qwen Code session.
// Each record is written as a numbered JSON file in .specstory/debug/<session-id>/
func writeDebugRawFiles(session *QwenSession) error {
	debugDir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return fmt.Errorf("failed to create debug dir: %w", err)
	}

	for idx, record := range session.Records {
		number := idx + 1
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			slog.Debug("writeDebugRawFiles: failed to marshal", "index", number, "error", err)
			continue
		}

		filename := filepath.Join(debugDir, fmt.Sprintf("%d.json", number))
		if err := os.WriteFile(filename, data, 0o644); err != nil {
			slog.Debug("writeDebugRawFiles: failed to write", "index", number, "error", err)
			continue
		}
	}
	return nil
}

// ListAgentChatSessions retrieves lightweight session metadata without full conversion
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	projectDir, err := ResolveQwenProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	sessions, err := FindSessions(projectDir)
	if err != nil {
		return nil, err
	}

	result := make([]spi.SessionMetadata, 0, len(sessions))
	for _, session := range sessions {
		metadata := extractQwenSessionMetadata(session)
		if metadata == nil {
			slog.Debug("Skipping empty session", "sessionID", session.ID)
			continue
		}
		result = append(result, *metadata)
	}

	return result, nil
}

// extractQwenSessionMetadata extracts lightweight metadata from a Qwen session.
// Returns nil if the session has no real user messages.
func extractQwenSessionMetadata(session *QwenSession) *spi.SessionMetadata {
	firstUserMessage := session.FirstRealUserText()
	if firstUserMessage == "" {
		return nil
	}

	slug := spi.GenerateFilenameFromUserMessage(firstUserMessage)
	if slug == "" {
		slug = "qwen-session"
	}

	return &spi.SessionMetadata{
		SessionID: session.ID,
		CreatedAt: session.StartTime,
		Slug:      slug,
		Name:      spi.GenerateReadableName(firstUserMessage),
	}
}

// ListAllAgentChatSessions enumerates every session in this provider's native
// store, regardless of project. See docs/SESSIONS-DB.md.
//
// Qwen Code stores each record's originating cwd inside the transcript, so the
// origin is read from the session itself rather than derived from the
// sanitized directory name (which is lossy: '-' replaced every separator).
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates all sessions while reporting
// scan progress into r (nil-safe). Used by `specstory reindex`.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(projectsDir); err != nil {
		if os.IsNotExist(err) {
			return []spi.GlobalSessionRef{}, nil
		}
		return nil, fmt.Errorf("failed to read Qwen projects directory: %w", err)
	}

	return spi.ScanSessionsInParallel(projectsDir, "qwen", r, func(path string) (*spi.GlobalSessionRef, error) {
		// Only chats/*.jsonl files are transcripts.
		if filepath.Base(filepath.Dir(path)) != "chats" {
			return nil, nil
		}

		session, err := ParseSessionFile(path)
		if err != nil {
			return nil, err
		}
		metadata := extractQwenSessionMetadata(session)
		if metadata == nil {
			return nil, nil // empty or warmup-only session
		}

		return &spi.GlobalSessionRef{
			SessionID:  metadata.SessionID,
			CreatedAt:  metadata.CreatedAt,
			Slug:       metadata.Slug,
			Name:       metadata.Name,
			NativePath: path,
			OriginCwd:  session.Cwd,
		}, nil
	})
}
