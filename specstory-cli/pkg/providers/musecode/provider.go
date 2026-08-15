package musecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Compile-time assertions that Provider satisfies the full spi.Provider
// contract plus the optional reindex capabilities.
var (
	_ spi.Provider           = (*Provider)(nil)
	_ spi.ProgressEnumerator = (*Provider)(nil)
	_ spi.PathSessionReader  = (*Provider)(nil)
)

// versionFlag is the flag Check probes the binary with, reported alongside the
// result so analytics can tell a flag change from a genuine failure.
const versionFlag = "--version"

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return "Muse Code"
}

func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseMuseCommand(customCommand)
	isCustom := customCommand != ""
	attempt := analytics.CheckAttempt{
		Provider:      "muse",
		CustomCommand: isCustom,
		CommandPath:   cmdName,
		VersionFlag:   versionFlag,
	}

	resolvedPath, err := exec.LookPath(cmdName)
	if err != nil {
		errorMessage := buildMuseCheckErrorMessage(spi.CheckErrorNotFound, cmdName, isCustom, "")
		analytics.TrackCheckFailure(attempt, spi.CheckErrorNotFound, err.Error(), "")
		return spi.CheckResult{
			Success:      false,
			Location:     "",
			ErrorMessage: errorMessage,
		}
	}
	attempt.ResolvedPath = resolvedPath

	cmd := exec.Command(cmdName, versionFlag)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorType := spi.ClassifyCheckError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		errorMessage := buildMuseCheckErrorMessage(errorType, resolvedPath, isCustom, stderrOutput)
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), stderrOutput)

		return spi.CheckResult{
			Success:      false,
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	// A binary that runs but prints nothing still passes the check; report a
	// placeholder rather than an empty version so the result reads unambiguously.
	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = "unknown"
	}
	analytics.TrackCheckSuccess(attempt, version)

	return spi.CheckResult{
		Success:  true,
		Version:  version,
		Location: resolvedPath,
	}
}

func buildMuseCheckErrorMessage(errorType string, museCmd string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case spi.CheckErrorNotFound:
		b.WriteString("Muse Code could not be found.\n\n")
		if isCustom {
			b.WriteString("• Verify the path you supplied actually points to the `muse` executable.\n")
			fmt.Fprintf(&b, "• Provided command: %s\n", museCmd)
		} else {
			b.WriteString("• Install Muse Code and ensure `muse` is on your PATH.\n")
			b.WriteString("• Or pass a custom command via `specstory check muse -c \"path/to/muse\"`.\n")
		}
	case spi.CheckErrorPermissionDenied:
		b.WriteString("Muse Code exists but isn't executable.\n\n")
		fmt.Fprintf(&b, "• Fix permissions: `chmod +x %s`\n", museCmd)
		b.WriteString("• Some package managers install the binary as root; run SpecStory with a path you can execute.\n")
	default:
		b.WriteString("`muse --version` failed.\n\n")
		if stderr != "" {
			fmt.Fprintf(&b, "Error output:\n%s\n\n", stderr)
		}
		b.WriteString("• Try running `muse --version` directly in your terminal.\n")
		b.WriteString("• If you upgraded recently, reinstall the CLI to refresh dependencies.\n")
	}

	return b.String()
}

// DetectAgent reports whether Muse Code has recorded any session for this
// project. The store is global, so this is a content question (does any
// transcript name this workspace root?) rather than a "does a directory exist"
// question.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	sessionsRoot, rootErr := GetMuseSessionsDir()

	sessions, err := findMuseSessions(projectPath)
	if err != nil {
		if helpOutput {
			fmt.Printf("Muse Code detection failed: %v\n", err)
		}
		return false
	}
	if len(sessions) > 0 {
		return true
	}

	if helpOutput {
		if rootErr != nil || sessionsRoot == "" {
			fmt.Println("No Muse Code sessions found.")
			fmt.Println("Start a Muse Code session in this project (e.g. `muse`) so a transcript is created.")
			return false
		}
		if _, statErr := os.Stat(sessionsRoot); statErr != nil {
			fmt.Printf("Muse Code sessions directory not found (%s).\n", sessionsRoot)
			fmt.Println("Run Muse Code once (e.g. `muse`) so the session store is created, then rerun this command.")
			return false
		}
		fmt.Printf("Muse Code sessions exist under %s but none record this project as their workspace root.\n", sessionsRoot)
		fmt.Println("Start a Muse Code session from this directory so the provider can pick it up.")
	}
	return false
}

func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	files, err := findMuseSessions(projectPath)
	if err != nil {
		return nil, err
	}

	var result []spi.AgentChatSession
	for i, file := range files {
		if chatSession := parseAndConvert(file.Path, projectPath, debugRaw); chatSession != nil {
			result = append(result, *chatSession)
		}

		// Report progress after each session
		if progress != nil {
			progress(i+1, len(files))
		}
	}
	return result, nil
}

func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	// The store is keyed by session id (the id IS the directory name), so a
	// glob resolves the path without reading any transcript.
	//
	// The id alone does not establish ownership, though: the store is global, so
	// the glob will just as happily find another project's session. Confirm the
	// transcript names this project before converting it — conversion stamps the
	// requested project onto the result, so returning an unchecked match would
	// file one project's conversation under another. The scan below applies the
	// same rule via findMuseSessions.
	directPath, err := findMuseSessionPathByID(sessionID)
	if err != nil {
		return nil, err
	}
	if directPath != "" && sessionFileBelongsToProject(directPath, projectPath) {
		if chatSession := parseAndConvert(directPath, projectPath, debugRaw); chatSession != nil {
			return chatSession, nil
		}
	}

	// Fall back to a scan: the id may name a session recorded under a layout
	// the glob does not cover.
	files, err := findMuseSessions(projectPath)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if file.SessionID == sessionID {
			return parseAndConvert(file.Path, projectPath, debugRaw), nil
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
	if len(session.Events) == 0 {
		return nil, nil
	}
	return convertToAgentChatSession(session, originCwd, debugRaw), nil
}

// parseAndConvert parses one transcript and converts it, logging and returning
// nil for files that are unreadable or hold no conversation yet.
func parseAndConvert(filePath string, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	session, err := ParseSessionFile(filePath)
	if err != nil {
		slog.Warn("parseAndConvert: Failed to parse Muse session file, skipping",
			"file", filePath, "error", err)
		return nil
	}
	if len(session.Events) == 0 {
		slog.Debug("parseAndConvert: Session file has no conversation events", "file", filePath)
		return nil
	}
	return convertToAgentChatSession(session, workspaceRoot, debugRaw)
}

// convertToAgentChatSession converts a MuseSession to the provider-agnostic
// AgentChatSession format. Used by both sync mode and watch mode.
func convertToAgentChatSession(session *MuseSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	sessionData, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Error("convertToAgentChatSession: failed to generate session data",
			"sessionId", session.ID,
			"error", err)
		return nil
	}

	slug := museSessionSlug(session)
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

// museSessionSlug derives the filename-safe slug from the prompt that opened
// the session.
func museSessionSlug(session *MuseSession) string {
	if prompt := session.FirstUserPrompt(); prompt != "" {
		if slug := spi.GenerateFilenameFromUserMessage(prompt); slug != "" {
			return slug
		}
	}
	return "muse-session"
}

// writeDebugRawFiles writes debug JSON files for a Muse Code session.
// Each conversation event is written as a numbered JSON file in
// .specstory/debug/<session-id>/. Only conversation events are written: the
// telemetry and diagnostics records the parser drops are noise for debugging
// markdown generation.
func writeDebugRawFiles(session *MuseSession) error {
	debugDir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return fmt.Errorf("failed to create debug dir: %w", err)
	}

	for idx := range session.Events {
		number := idx + 1
		data, err := json.MarshalIndent(session.Events[idx], "", "  ")
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

// ListAgentChatSessions retrieves lightweight session metadata without full conversion.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	files, err := findMuseSessions(projectPath)
	if err != nil {
		return nil, err
	}

	result := make([]spi.SessionMetadata, 0, len(files))
	for _, file := range files {
		session, err := ParseSessionFile(file.Path)
		if err != nil {
			slog.Warn("ListAgentChatSessions: Failed to parse session file, skipping",
				"file", file.Path, "error", err)
			continue
		}
		metadata := extractMuseSessionMetadata(session)
		if metadata == nil {
			slog.Debug("ListAgentChatSessions: Skipping session with no user prompt", "sessionID", session.ID)
			continue
		}
		result = append(result, *metadata)
	}

	return result, nil
}

// extractMuseSessionMetadata extracts lightweight metadata from a Muse session.
// Returns nil if the session never received a user prompt (a session that
// crashed during startup, or a transcript holding only telemetry).
func extractMuseSessionMetadata(session *MuseSession) *spi.SessionMetadata {
	firstPrompt := session.FirstUserPrompt()
	if firstPrompt == "" {
		return nil
	}

	slug := spi.GenerateFilenameFromUserMessage(firstPrompt)
	if slug == "" {
		slug = "muse-session"
	}

	return &spi.SessionMetadata{
		SessionID: session.ID,
		CreatedAt: session.StartTime,
		Slug:      slug,
		Name:      spi.GenerateReadableName(firstPrompt),
	}
}

// ListAllAgentChatSessions enumerates every session in this provider's native
// store, regardless of project. See docs/SESSIONS-DB.md.
//
// Muse's store is global and date-sharded, so the project association comes
// from the workspace_root recorded inside each transcript rather than from the
// path.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates all sessions while reporting
// scan progress into r (nil-safe). Used by `specstory reindex`.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	sessionsRoot, err := GetMuseSessionsDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(sessionsRoot); err != nil {
		if os.IsNotExist(err) {
			return []spi.GlobalSessionRef{}, nil
		}
		return nil, fmt.Errorf("failed to read Muse sessions directory: %w", err)
	}

	return spi.ScanSessionsInParallel(sessionsRoot, "muse", r, func(path string) (*spi.GlobalSessionRef, error) {
		// Only top-level <session-id>/session.jsonl files are sessions;
		// subagent transcripts belong to their parent.
		if filepath.Base(path) != sessionFileName || IsSubagentTranscript(path) {
			return nil, nil
		}

		session, err := ParseSessionFile(path)
		if err != nil {
			return nil, err
		}
		metadata := extractMuseSessionMetadata(session)
		if metadata == nil {
			return nil, nil // telemetry-only or prompt-less session
		}

		return &spi.GlobalSessionRef{
			SessionID:  metadata.SessionID,
			CreatedAt:  metadata.CreatedAt,
			Slug:       metadata.Slug,
			Name:       metadata.Name,
			NativePath: path,
			OriginCwd:  session.WorkspaceRoot,
		}, nil
	})
}

func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Muse Code", "project", projectPath)

	SetWatcherDebugRaw(debugRaw)
	if err := WatchMuseProject(projectPath, sessionCallback); err != nil {
		slog.Error("Failed to start watcher", "error", err)
	}
	defer StopWatcher()

	if resumeSessionID != "" {
		slog.Info("Attempting to resume Muse Code session", "sessionId", resumeSessionID)
	}

	return ExecuteMuse(customCommand, resumeSessionID)
}

// WatchAgent watches for Muse Code agent activity and calls the callback with
// an AgentChatSession. Does NOT execute the agent - only watches for activity.
// Runs until error or context cancellation (blocks indefinitely).
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Muse Code activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	SetWatcherDebugRaw(debugRaw)

	if err := WatchMuseProject(projectPath, sessionCallback); err != nil {
		slog.Error("WatchAgent: Failed to start Muse session watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	slog.Info("WatchAgent: Watcher started, blocking until context cancelled")
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	StopWatcher()

	return ctx.Err()
}
