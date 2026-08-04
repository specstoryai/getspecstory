package qwencode

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

// Provider implements the spi.Provider interface for Qwen Code, a Gemini CLI
// fork. Sessions are recorded as JSONL transcripts under
// ~/.qwen/projects/<sanitized-cwd>/chats/<session-id>.jsonl.
type Provider struct{}

// NewProvider creates a new Qwen Code provider instance
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
	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		if helpOutput {
			fmt.Printf("Qwen data found at %s but no chats directory exists yet.\n", projectDir)
			fmt.Printf("Start a Qwen Code session in this project so %s is created.\n", chatsDir)
		}
		return false
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			return true
		}
	}

	if helpOutput {
		fmt.Printf("Qwen data found at %s but no chats/*.jsonl transcripts exist yet.\n", projectDir)
		fmt.Printf("Start a Qwen Code session in this project so a transcript is recorded.\n")
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

	// Qwen Code names transcripts <session-id>.jsonl, so the common case is a
	// direct path lookup without scanning the whole chats directory.
	path := transcriptPath(projectDir, sessionID)
	if _, err := osStat(path); err == nil {
		session, parseErr := ParseSessionFile(path)
		if parseErr != nil {
			return nil, parseErr
		}
		return convertToAgentChatSession(session, projectPath, debugRaw), nil
	}

	// Fall back to scanning (e.g. transcripts renamed by hand).
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

// GetAgentChatSessionByPath parses a session directly from its native
// transcript path, skipping by-id discovery. Implements spi.PathSessionReader.
func (p *Provider) GetAgentChatSessionByPath(nativePath string, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	session, err := ParseSessionFile(nativePath)
	if err != nil {
		return nil, err
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

	// Set up debug mode
	SetWatcherDebugRaw(debugRaw)

	// Start watching for Qwen sessions
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
			b.WriteString("• Install Qwen Code: `npm install -g @qwen-code/qwen-code`\n")
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
		case "qwen_dir_missing":
			log.UserWarn("Qwen directory missing (%s).", pathErr.Path)
			log.UserMessage("Run Qwen Code once (e.g., `qwen`) so ~/.qwen is created, then rerun this command.\n")
		case "projects_missing":
			log.UserWarn("Qwen projects directory missing (%s).", pathErr.Path)
			log.UserMessage("Run Qwen Code once so it records a session, then rerun this command.\n")
		case "project_missing":
			log.UserWarn("No Qwen data found for this project (expected %s).", pathErr.Path)
			log.UserMessage("Start a Qwen Code session from your repo so the provider can pick it up.\n")
		default:
			log.UserWarn("Qwen detection failed: %v", err)
		}
		return
	}

	log.UserWarn("Qwen detection failed: %v", err)
}

// convertToAgentChatSession converts a QwenSession to the provider-agnostic AgentChatSession format.
// Used by both sync mode (GetAgentChatSession/GetAgentChatSessions) and watch mode.
func convertToAgentChatSession(session *QwenSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	// Generate structured session data
	sessionData, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Error("convertToAgentChatSession: failed to generate session data",
			"sessionId", session.ID,
			"error", err)
		return nil
	}

	// Extract slug from first user message
	slug := spi.GenerateFilenameFromUserMessage(session.FirstUserMessage())
	if slug == "" {
		slug = "qwen-session"
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

// writeDebugRawFiles writes debug JSON files for a Qwen Code session.
// Each entry is written as a numbered JSON file in .specstory/debug/<session-id>/
func writeDebugRawFiles(session *QwenSession) error {
	debugDir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return fmt.Errorf("failed to create debug dir: %w", err)
	}

	for idx, entry := range session.Entries {
		number := idx + 1

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

// ListAgentChatSessions retrieves lightweight session metadata without full parsing
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	projectDir, err := ResolveQwenProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	chatsDir := filepath.Join(projectDir, "chats")
	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []spi.SessionMetadata{}, nil
		}
		return nil, fmt.Errorf("failed to read chats directory %q: %w", chatsDir, err)
	}

	var result []spi.SessionMetadata
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		metadata, scanErr := extractQwenSessionMetadata(filepath.Join(chatsDir, entry.Name()))
		if scanErr != nil {
			slog.Debug("ListAgentChatSessions: failed to scan session",
				"file", entry.Name(), "error", scanErr)
			continue
		}
		if metadata == nil {
			slog.Debug("Skipping empty session", "file", entry.Name())
			continue
		}
		result = append(result, *metadata)
	}

	return result, nil
}

// qwenSessionScan accumulates the minimal fields a header scan needs.
type qwenSessionScan struct {
	sessionID        string
	timestamp        string
	cwd              string
	firstUserMessage string
	foundRealMessage bool
}

// scanQwenSession reads minimal data from a transcript: session id, first
// timestamp, first real user message, and originating cwd. Shared by the
// project-scoped metadata path and the global enumeration
// (ListAllAgentChatSessions).
func scanQwenSession(filePath string) (*qwenSessionScan, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open session file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	reader := bufio.NewReader(file)
	scan := &qwenSessionScan{}

	// Read records until we find everything we need.
	// Why: ReadString can return data AND io.EOF on the last line (no trailing
	// newline), so we always process the line first, then check for EOF once at
	// the bottom.
	lineNum := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, fmt.Errorf("failed to read line: %w", readErr)
		}

		lineNum++
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var record QwenSessionEntry
			if jsonErr := json.Unmarshal([]byte(trimmed), &record); jsonErr != nil {
				slog.Warn("Skipping malformed JSONL line",
					"file", filepath.Base(filePath),
					"line", lineNum,
					"error", jsonErr)
			} else {
				if scan.sessionID == "" && record.SessionID != "" {
					scan.sessionID = record.SessionID
				}

				// Capture the originating cwd (first record that carries one).
				// This is the input to project-identity resolution for the
				// restore index.
				if scan.cwd == "" && record.Cwd != "" {
					scan.cwd = record.Cwd
				}

				if record.Type != entryTypeSystem {
					scan.foundRealMessage = true
					if scan.timestamp == "" && record.Timestamp != "" {
						scan.timestamp = record.Timestamp
					}
					if scan.firstUserMessage == "" &&
						record.Type == entryTypeUser && record.Provenance == provenanceRealUser {
						scan.firstUserMessage = entryText(record)
					}
				}
			}
		}

		// Single exit: found everything we need, or reached end of file
		if (scan.sessionID != "" && scan.timestamp != "" && scan.firstUserMessage != "" && scan.cwd != "") || readErr == io.EOF {
			break
		}
	}

	return scan, nil
}

// extractQwenSessionMetadata reads minimal data from a transcript to extract
// metadata. Returns nil if the transcript has no real messages.
func extractQwenSessionMetadata(filePath string) (*spi.SessionMetadata, error) {
	scan, err := scanQwenSession(filePath)
	if err != nil {
		return nil, err
	}

	if !scan.foundRealMessage {
		return nil, nil
	}

	sessionID := scan.sessionID
	if sessionID == "" {
		base := filepath.Base(filePath)
		sessionID = strings.TrimSuffix(base, ".jsonl")
	}

	slug := spi.GenerateFilenameFromUserMessage(scan.firstUserMessage)
	if slug == "" {
		slug = "qwen-session"
	}

	return &spi.SessionMetadata{
		SessionID: sessionID,
		CreatedAt: scan.timestamp,
		Slug:      slug,
		Name:      spi.GenerateReadableName(scan.firstUserMessage),
	}, nil
}

// ListAllAgentChatSessions enumerates every Qwen Code session across all projects. It is the
// no-progress form of ListAllAgentChatSessionsProgress.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates every Qwen Code session across all projects by
// walking ~/.qwen/projects/*/chats/ for *.jsonl (the originating cwd comes from inside each
// transcript; the project directory name is a lossy, irreversible encoding of the path),
// reporting scan progress into r (nil-safe). Headers are scanned in parallel across CPUs;
// output order is irrelevant (reindex dedups and sorts later). Implements
// spi.ProgressEnumerator. See docs/SESSIONS-DB.md.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		// No projects directory yet → nothing to enumerate (not an error).
		return []spi.GlobalSessionRef{}, nil
	}

	return spi.ScanSessionsInParallel(projectsDir, "qwen", r, func(path string) (*spi.GlobalSessionRef, error) {
		// Skip the per-session process markers (<id>.runtime.json is not .jsonl,
		// but guard anyway in case Qwen Code adds sibling transcript formats).
		if strings.HasSuffix(path, ".runtime.json") {
			return nil, nil
		}

		scan, scanErr := scanQwenSession(path)
		if scanErr != nil {
			return nil, scanErr
		}
		if !scan.foundRealMessage {
			return nil, nil // empty transcript (not a session)
		}

		sessionID := scan.sessionID
		if sessionID == "" {
			sessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		}

		slug := spi.GenerateFilenameFromUserMessage(scan.firstUserMessage)
		if slug == "" {
			slug = "qwen-session"
		}

		return &spi.GlobalSessionRef{
			SessionID:  sessionID,
			CreatedAt:  scan.timestamp,
			Slug:       slug,
			Name:       spi.GenerateReadableName(scan.firstUserMessage),
			NativePath: path,
			OriginCwd:  scan.cwd,
		}, nil
	})
}
