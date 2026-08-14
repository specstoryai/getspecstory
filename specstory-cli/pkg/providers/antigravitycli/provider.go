package antigravitycli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// location and version (`agy --version` prints a bare semver such as "1.1.3").
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, _ := parseCommand(customCommand)
	isCustom := strings.TrimSpace(customCommand) != ""
	slog.Info("Check: verifying Antigravity CLI installation",
		"command", cmdName, "customCommand", isCustom)
	attempt := analytics.CheckAttempt{
		Provider:      providerID,
		CustomCommand: isCustom,
		CommandPath:   cmdName,
		VersionFlag:   versionFlag,
	}

	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		slog.Info("Check: binary not found on PATH", "command", cmdName, "error", err)
		msg := buildCheckErrorMessage(spi.CheckErrorNotFound, cmdName, isCustom, "")
		analytics.TrackCheckFailure(attempt, spi.CheckErrorNotFound, err.Error(), "")
		return spi.CheckResult{Success: false, Location: "", ErrorMessage: msg}
	}
	slog.Info("Check: binary resolved", "command", cmdName, "resolved", resolved)
	attempt.ResolvedPath = resolved

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, versionFlag)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorType := spi.ClassifyCheckError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		slog.Info("Check: version probe failed",
			"resolved", resolved, "errorType", errorType, "stderr", stderrOutput, "error", err)
		msg := buildCheckErrorMessage(errorType, resolved, isCustom, stderrOutput)
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), stderrOutput)
		return spi.CheckResult{Success: false, Location: resolved, ErrorMessage: msg}
	}

	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = "unknown"
	}
	slog.Info("Check: succeeded", "resolved", resolved, "version", version)
	analytics.TrackCheckSuccess(attempt, version)
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

	history, projectWorkspaces := loadWorkspaceIndexes()

	// Answer from the indexes first: a conversation whose workspace is already
	// stated needs no transcript read at all, which for interactive users
	// short-circuits before any file is opened.
	for _, file := range files {
		workspace := resolveSessionWorkspace(file.ConversationID, history, projectWorkspaces)
		if workspace != "" && workspacesOverlap(workspace, projectPath) {
			return true
		}
	}

	// Sessions with no stated workspace (print mode) can only be placed by the
	// paths their tools touched, which means parsing the transcript.
	for _, file := range files {
		session, err := parseTranscript(file.ConversationID, file.Path, history, projectWorkspaces, false)
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
	history, projectWorkspaces := loadWorkspaceIndexes()

	total := len(files)
	result := make([]spi.AgentChatSession, 0, total)
	for i, file := range files {
		if chat := convertConversation(file, projectPath, debugRaw, history, projectWorkspaces); chat != nil {
			result = append(result, *chat)
		}
		if progress != nil {
			progress(i+1, total)
		}
	}
	return result, nil
}

// convertConversation parses one conversation and converts it to the unified
// format, returning nil when it cannot be read or does not belong to
// projectPath. Keeping the per-conversation work here lets the callers report
// progress exactly once per file regardless of why a file was skipped.
func convertConversation(file conversationFile, projectPath string, debugRaw bool,
	history map[string]historyEntry, projectWorkspaces map[string]string) *spi.AgentChatSession {

	session, err := parseTranscript(file.ConversationID, file.Path, history, projectWorkspaces, true)
	if err != nil {
		slog.Debug("antigravity: skipping session", "conversationId", file.ConversationID, "error", err)
		return nil
	}
	if !sessionMatchesProject(session, projectPath) {
		return nil
	}
	return convertToAgentSession(session, projectPath, debugRaw)
}

// GetAgentChatSession returns the session with the given conversation ID.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	path, err := resolveTranscriptPath(sessionID)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil // no transcript on disk — not found, not an error
	}
	history, projectWorkspaces := loadWorkspaceIndexes()
	session, err := parseTranscript(sessionID, path, history, projectWorkspaces, true)
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
	history, projectWorkspaces := loadWorkspaceIndexes()
	summaries := loadSummaries()

	var result []spi.SessionMetadata
	for _, file := range files {
		session, err := parseTranscript(file.ConversationID, file.Path, history, projectWorkspaces, false)
		if err != nil {
			slog.Debug("antigravity: failed to parse session", "conversationId", file.ConversationID, "error", err)
			continue
		}
		if !sessionMatchesProject(session, projectPath) {
			continue
		}
		meta := sessionMetadata(session, history, summaries)
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

// ListAllAgentChatSessions enumerates every Antigravity session across all
// projects, one ref per conversation that has a parseable transcript. Unlike the
// project-scoped listing, the project is discovered rather than supplied: each
// ref carries the session's resolved workspace as OriginCwd so `specstory
// reindex` can map it to a project; sessions whose workspace Antigravity never
// stated carry an empty OriginCwd and stay unmapped rather than guessing.
// Enumeration is keyed off the brain/ dirs (the transcript-backed
// conversations) rather than conversations/*.db, since only the former yield
// conversation content this provider can read. See
// docs/ANTIGRAVITY-FORMAT.md §1.1.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	files, err := listConversationFiles()
	if err != nil {
		return nil, err
	}
	history, projectWorkspaces := loadWorkspaceIndexes()
	summaries := loadSummaries()

	var refs []spi.GlobalSessionRef
	for _, file := range files {
		session, err := parseTranscript(file.ConversationID, file.Path, history, projectWorkspaces, false)
		if err != nil {
			slog.Debug("antigravity: skipping session during global enumeration",
				"conversationId", file.ConversationID, "error", err)
			continue
		}
		meta := sessionMetadata(session, history, summaries)
		if meta == nil {
			// No user prompt — nothing resumable/indexable.
			continue
		}
		refs = append(refs, spi.GlobalSessionRef{
			SessionID:  meta.SessionID,
			CreatedAt:  meta.CreatedAt,
			Slug:       meta.Slug,
			Name:       meta.Name,
			NativePath: file.Path,
			OriginCwd:  session.Workspace,
		})
	}
	return refs, nil
}

// --- helpers shared across the package ---

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
		// Name the command that actually failed — with a custom antigravity_cmd
		// (wrapper script, non-agy binary) a hardcoded `agy` would point the user
		// at the wrong program.
		fmt.Fprintf(&builder, "`%s %s` failed.\n\n", command, versionFlag)
		if stderr != "" {
			builder.WriteString("Error output:\n")
			builder.WriteString(stderr)
			builder.WriteString("\n\n")
		}
		fmt.Fprintf(&builder, "Run `%s %s` manually to diagnose, then retry.", command, versionFlag)
	}
	return builder.String()
}

func printDetectionHelp() {
	log.UserMessage("No Antigravity CLI sessions found under ~/.gemini/antigravity-cli/brain yet.\n")
	log.UserMessage("Run the Antigravity CLI (`agy`) inside this project to create a session, then rerun `specstory sync antigravity`.\n")
}
