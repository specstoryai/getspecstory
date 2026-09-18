package grokbuild

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
	"strconv"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Compile-time assertions that Provider satisfies the full SPI contract plus the
// optional reindex capabilities. Interface conformance is otherwise only checked
// at registration, which would report the gap in the factory package instead of
// here.
var (
	_ spi.Provider           = (*Provider)(nil)
	_ spi.ProgressEnumerator = (*Provider)(nil)
	_ spi.PathSessionReader  = (*Provider)(nil)
)

// versionFlag is the flag Check probes the binary with, reported alongside the
// result so analytics can tell a flag change from a genuine failure.
const versionFlag = "--version"

// Provider reads and watches Grok Build sessions.
type Provider struct{}

// NewProvider constructs the Grok Build provider.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the display name of the agent.
func (p *Provider) Name() string {
	return "Grok Build"
}

// Check probes the executable and reports one analytics outcome.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseGrokCommand(customCommand)
	isCustom := customCommand != ""
	attempt := analytics.CheckAttempt{
		Provider:      "grok",
		CustomCommand: isCustom,
		CommandPath:   cmdName,
		VersionFlag:   versionFlag,
	}

	slog.Info("Check: verifying Grok Build installation", "command", cmdName, "customCommand", isCustom)
	resolvedPath, err := spi.LookPathForCheck(cmdName)
	if err != nil {
		slog.Info("Check: binary lookup failed", "command", cmdName, "error", err)
		errorType := spi.ClassifyCheckError(err)
		errorMessage := buildGrokCheckErrorMessage(errorType, cmdName, isCustom, "")
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), "")
		return spi.CheckResult{
			Success:      false,
			ErrorType:    errorType,
			Location:     "",
			ErrorMessage: errorMessage,
		}
	}
	attempt.ResolvedPath = resolvedPath

	cmd := exec.Command(resolvedPath, versionFlag)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		slog.Info("Check: version probe failed", "command", resolvedPath, "error", err)
		errorType := spi.ClassifyCheckExecutionError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		errorMessage := buildGrokCheckErrorMessage(errorType, resolvedPath, isCustom, stderrOutput)
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), stderrOutput)
		return spi.CheckResult{
			Success:      false,
			ErrorType:    errorType,
			Location:     resolvedPath,
			ErrorMessage: errorMessage,
		}
	}

	version := strings.TrimSpace(stdout.String())
	slog.Info("Check: succeeded", "resolved", resolvedPath, "version", version)
	analytics.TrackCheckSuccess(attempt, version)

	return spi.CheckResult{
		Success:  true,
		Version:  version,
		Location: resolvedPath,
	}
}

// DetectAgent reports whether this project has a human Grok conversation.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	groupDir, err := ResolveGrokProjectDir(projectPath)
	if err != nil {
		if helpOutput {
			printGrokDetectionHelp(err)
		}
		return false
	}

	sessions, err := findSessions(groupDir, true)
	if err != nil || len(sessions) == 0 {
		if helpOutput {
			fmt.Printf("Grok Build data found at %s but no sessions have been recorded yet.\n", groupDir)
			fmt.Println("Start a Grok Build session in this project so a transcript is created.")
		}
		return false
	}

	return true
}

// GetAgentChatSessions converts every session in this project.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	groupDir, err := ResolveGrokProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	sessions, err := FindSessions(groupDir)
	if err != nil {
		return nil, err
	}

	total := len(sessions)
	var result []spi.AgentChatSession
	for i, session := range sessions {
		if chatSession := convertToAgentChatSession(session, projectPath, debugRaw); chatSession != nil {
			result = append(result, *chatSession)
		}
		if progress != nil {
			progress(i+1, total)
		}
	}
	return result, nil
}

// GetAgentChatSession resolves a project-scoped native session ID.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	if !uuidLike.MatchString(sessionID) {
		return nil, nil
	}
	// Native UUID directory names use lowercase; callers may use either hex case.
	sessionID = strings.ToLower(sessionID)
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	groupDir, err := ResolveGrokProjectDir(projectPath)
	if err != nil {
		var missing *GrokPathError
		if errors.As(err, &missing) {
			return nil, nil
		}
		return nil, err
	}

	// Sessions are directories named by their id, so resolve directly before
	// falling back to a scan.
	directDir := filepath.Join(groupDir, sessionID)
	info, err := os.Lstat(directDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to inspect Grok session directory: %w", err)
	}
	if err == nil && info.IsDir() {
		session, parseErr := ParseSessionDir(directDir)
		if parseErr != nil {
			return nil, parseErr
		}
		if len(session.Records) > 0 && !session.IsSubagent() {
			return convertToAgentChatSession(session, projectPath, debugRaw), nil
		}
	}

	sessions, err := FindSessions(groupDir)
	if err != nil {
		return nil, err
	}
	for _, session := range sessions {
		if strings.EqualFold(session.ID, sessionID) {
			return convertToAgentChatSession(session, projectPath, debugRaw), nil
		}
	}

	return nil, nil
}

// GetAgentChatSessionByPath parses a session straight from its known directory,
// skipping the by-id search. reindex already holds the path from enumeration, so
// this keeps resolving N sessions O(N).
func (p *Provider) GetAgentChatSessionByPath(nativePath string, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	// Enumeration reports the transcript file; the session is its directory.
	dir := nativePath
	if strings.HasSuffix(nativePath, ".jsonl") {
		dir = filepath.Dir(nativePath)
	}

	session, err := ParseSessionDir(dir)
	if err != nil {
		return nil, err
	}
	if len(session.Records) == 0 || session.IsSubagent() {
		return nil, nil
	}
	return convertToAgentChatSession(session, originCwd, debugRaw), nil
}

// ExecAgentAndWatch launches Grok and drains live saves before returning.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: starting Grok Build", "project", projectPath)

	SetWatcherDebugRaw(debugRaw)
	if err := WatchGrokProject(projectPath, sessionCallback); err != nil {
		return fmt.Errorf("failed to start Grok watcher: %w", err)
	}
	defer StopWatcher()

	if resumeSessionID != "" {
		slog.Info("ExecAgentAndWatch: resuming Grok Build session", "sessionId", resumeSessionID)
	}

	agentErr := ExecuteGrok(projectPath, customCommand, resumeSessionID)
	StopWatcher()
	watcherLifecycle.Lock()
	failures := watcherErrors
	watcherLifecycle.Unlock()
	select {
	case watchErr := <-failures:
		if agentErr == nil {
			return watchErr
		}
	default:
	}
	return agentErr
}

// WatchAgent observes the project until cancellation or watcher failure.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: starting Grok Build activity monitoring",
		"projectPath", projectPath, "debugRaw", debugRaw)

	SetWatcherDebugRaw(debugRaw)

	if err := WatchGrokProject(projectPath, sessionCallback); err != nil {
		slog.Error("WatchAgent: failed to start watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	watcherLifecycle.Lock()
	failures := watcherErrors
	watcherLifecycle.Unlock()
	defer StopWatcher()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-failures:
		return err
	}
}

// ListAgentChatSessions reads only the metadata needed by the session picker.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return nil, err
	}

	groupDir, err := ResolveGrokProjectDir(projectPath)
	if err != nil {
		return nil, err
	}

	sessions, err := findSessions(groupDir, true)
	if err != nil {
		return nil, err
	}

	result := make([]spi.SessionMetadata, 0, len(sessions))
	for _, session := range sessions {
		metadata := extractSessionMetadata(session)
		if metadata == nil {
			continue
		}
		result = append(result, *metadata)
	}
	return result, nil
}

// ListAllAgentChatSessions enumerates every session in the store, regardless of
// project. See docs/SESSIONS-DB.md.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates all sessions while reporting scan
// progress. Used by `specstory reindex`.
//
// The store is grouped by project, and each session carries its own cwd in
// summary.json, so the originating directory is read from inside the session
// rather than recovered from the encoded directory name.
func (p *Provider) ListAllAgentChatSessionsProgress(reporter *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	sessionsDir, err := GetGrokSessionsDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(sessionsDir); err != nil {
		if os.IsNotExist(err) {
			return []spi.GlobalSessionRef{}, nil
		}
		return nil, fmt.Errorf("failed to read Grok sessions directory: %w", err)
	}

	return spi.ScanSessionsInParallel(sessionsDir, "grok", reporter, func(path string) (*spi.GlobalSessionRef, error) {
		// The scan yields every .jsonl in the tree; only chat_history.jsonl at
		// the top level of a session directory is a transcript.
		if filepath.Base(path) != chatHistoryFile {
			return nil, nil
		}
		relative, err := filepath.Rel(sessionsDir, path)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(relative, string(os.PathSeparator))
		// A matching filename in an archive, nested child directory, or at
		// group level is not a native top-level session.
		if len(parts) != 3 || parts[0] == ".." || !uuidLike.MatchString(parts[1]) {
			return nil, nil
		}
		dir := filepath.Dir(path)

		session, err := parseSessionDir(dir, true)
		if err != nil {
			return nil, err
		}
		if session.IsSubagent() {
			return nil, nil
		}

		metadata := extractSessionMetadata(session)
		if metadata == nil {
			return nil, nil
		}

		cwd := session.Cwd
		if cwd == "" {
			// Fall back to the group directory name when summary.json is missing.
			if decoded, ok := DecodeCwdDirname(filepath.Dir(dir)); ok {
				cwd = decoded
			}
		}

		return &spi.GlobalSessionRef{
			SessionID:  metadata.SessionID,
			CreatedAt:  metadata.CreatedAt,
			Slug:       metadata.Slug,
			Name:       metadata.Name,
			NativePath: path,
			OriginCwd:  cwd,
		}, nil
	})
}

// extractSessionMetadata builds the lightweight listing entry for a session.
// Returns nil for a session with no real user turn, which is what an aborted or
// metadata-only session looks like.
func extractSessionMetadata(session *GrokSession) *spi.SessionMetadata {
	query := session.FirstUserQuery()
	if query == "" {
		return nil
	}

	slug := spi.GenerateFilenameFromUserMessage(query)
	if slug == "" {
		slug = "grok-session"
	}

	// Grok titles its own sessions, which reads better than a slug of the prompt.
	name := session.Title
	if name == "" {
		name = spi.GenerateReadableName(query)
	}

	return &spi.SessionMetadata{
		SessionID: session.ID,
		CreatedAt: session.CreatedAt,
		Slug:      slug,
		Name:      name,
	}
}

// convertToAgentChatSession converts a parsed session into the provider-agnostic
// form. Returns nil for sessions that hold no conversation, so an aborted
// session never produces an empty markdown file.
func convertToAgentChatSession(session *GrokSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	sessionData, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Error("convertToAgentChatSession: failed to generate session data",
			"sessionID", session.ID, "error", err)
		return nil
	}
	if len(sessionData.Exchanges) == 0 {
		slog.Debug("convertToAgentChatSession: skipping session with no conversation", "sessionID", session.ID)
		return nil
	}

	query := session.FirstUserQuery()
	slug := spi.GenerateFilenameFromUserMessage(query)
	if slug == "" {
		slug = "grok-session"
	}
	sessionData.Slug = slug

	var rawData bytes.Buffer
	for _, record := range session.Records {
		rawData.Write(record.Raw)
		if len(record.Raw) > 0 && record.Raw[len(record.Raw)-1] != '\n' {
			rawData.WriteByte('\n')
		}
	}

	if debugRaw {
		if err := writeDebugRawFiles(session); err != nil {
			slog.Debug("convertToAgentChatSession: failed to write debug files",
				"sessionID", session.ID, "error", err)
		}
	}

	return &spi.AgentChatSession{
		SessionID:   session.ID,
		CreatedAt:   session.CreatedAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     rawData.String(),
	}
}

// writeDebugRawFiles writes one numbered JSON file per transcript record into
// .specstory/debug/<session-id>/.
func writeDebugRawFiles(session *GrokSession) error {
	debugDir := spi.GetDebugDir(session.ID)
	if err := os.MkdirAll(debugDir, 0o755); err != nil {
		return fmt.Errorf("failed to create debug dir: %w", err)
	}

	// Remove only provider-owned numbered records; the CLI owns session-data.json.
	entries, err := os.ReadDir(debugDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".json") {
			if n, err := strconv.Atoi(strings.TrimSuffix(name, ".json")); err == nil && n > len(session.Records) {
				if err := os.Remove(filepath.Join(debugDir, name)); err != nil {
					return err
				}
			}
		}
	}
	for idx, record := range session.Records {
		var data bytes.Buffer
		if err := json.Indent(&data, record.Raw, "", "  "); err != nil {
			return err
		}
		filename := filepath.Join(debugDir, fmt.Sprintf("%d.json", idx+1))
		if err := os.WriteFile(filename, data.Bytes(), 0o644); err != nil {
			return err
		}
	}
	sidecars := map[string]any{summaryFile: session.RawSummary}
	if session.Index != nil {
		sidecars[updatesFile] = session.Index.rawUpdates
		sidecars[eventsFile] = session.Index.rawEvents
	}
	if len(session.Subagents) > 0 {
		metas := map[string]json.RawMessage{}
		for id, meta := range session.Subagents {
			metas[id] = meta.Raw
		}
		sidecars["subagents"] = metas
	}
	data, err := json.MarshalIndent(sidecars, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(debugDir, "native-sidecars.json"), data, 0o644); err != nil {
		return err
	}

	return nil
}

func buildGrokCheckErrorMessage(errorType string, grokCmd string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case spi.CheckErrorNotFound:
		b.WriteString("Grok Build could not be found.\n\n")
		if isCustom {
			b.WriteString("• Verify the path you supplied actually points to the `grok` executable.\n")
			fmt.Fprintf(&b, "• Provided command: %s\n", grokCmd)
		} else {
			b.WriteString("• Install Grok Build with `curl -fsSL https://x.ai/cli/install.sh | bash` or see https://x.ai/cli\n")
			b.WriteString("• Ensure `grok` is on your PATH or pass a custom command via `specstory check grok -c \"path/to/grok\"`.\n")
		}
	case spi.CheckErrorPermissionDenied:
		b.WriteString("Grok Build exists but isn't executable.\n\n")
		fmt.Fprintf(&b, "• Fix permissions: `chmod +x %s`\n", grokCmd)
		b.WriteString("• Some installers place the binary as root; run SpecStory with a path you can execute.\n")
	default:
		b.WriteString("`grok --version` failed.\n\n")
		if stderr != "" {
			fmt.Fprintf(&b, "Error output:\n%s\n\n", stderr)
		}
		b.WriteString("• Try running `grok --version` directly in your terminal.\n")
		b.WriteString("• If you upgraded recently, reinstall the CLI to refresh its dependencies.\n")
	}

	return b.String()
}

func printGrokDetectionHelp(err error) {
	var pathErr *GrokPathError
	if errors.As(err, &pathErr) {
		switch pathErr.Kind {
		case "sessions_missing":
			log.UserWarn("Grok sessions directory missing (%s).", pathErr.Path)
			log.UserMessage("Run Grok Build once (e.g. `grok`) so the session store is created, then rerun this command.\n")
		case "project_missing":
			log.UserWarn("No Grok Build data found for this project.")
			if len(pathErr.KnownCwds) > 0 {
				log.UserMessage("Projects with Grok sessions: %s\n", strings.Join(pathErr.KnownCwds, ", "))
			} else {
				log.UserMessage("No Grok projects detected yet under the session store.\n")
			}
			log.UserMessage("Start a Grok Build session from your repo so the provider can pick it up.\n")
		default:
			log.UserWarn("Grok Build detection failed: %v", err)
		}
		return
	}

	log.UserWarn("Grok Build detection failed: %v", err)
}
