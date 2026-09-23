package opencode

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Compile-time assertions that Provider satisfies the full spi.Provider
// contract plus the optional progress-reporting enumeration. PathSessionReader
// is not implemented: every session shares one database file, so a path alone
// does not identify a session.
var (
	_ spi.Provider           = (*Provider)(nil)
	_ spi.ProgressEnumerator = (*Provider)(nil)
)

// versionFlag is the flag Check probes the binary with, reported alongside the
// result so analytics can tell a flag change from a genuine failure.
const versionFlag = "--version"

// Provider implements spi.Provider for OpenCode.
type Provider struct{}

// NewProvider creates an OpenCode provider.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the product name.
func (p *Provider) Name() string {
	return providerName
}

// Check verifies that the opencode binary resolves and reports a version.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, cmdArgs := parseOpenCodeCommand(customCommand)
	isCustom := customCommand != ""
	attempt := analytics.CheckAttempt{
		Provider:      providerID,
		CustomCommand: isCustom,
		CommandPath:   cmdName,
		VersionFlag:   versionFlag,
	}

	slog.Info("Check: verifying OpenCode installation", "command", cmdName, "customCommand", isCustom)

	resolvedPath, err := spi.LookPathForCheck(cmdName)
	if err != nil {
		errorType := spi.ClassifyCheckError(err)
		slog.Info("Check: binary lookup failed", "command", cmdName, "error", err)
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), "")
		return spi.CheckResult{
			Success:      false,
			ErrorType:    errorType,
			ErrorMessage: buildCheckErrorMessage(errorType, cmdName, isCustom, ""),
		}
	}
	attempt.ResolvedPath = resolvedPath

	// A custom command's arguments are part of how the user runs OpenCode (a
	// wrapper script may need them), so the probe keeps them.
	probeArgs := append(slices.Clone(cmdArgs), versionFlag)
	probeCommand := strings.Join(append([]string{cmdName}, probeArgs...), " ")
	if isCustom {
		probeCommand = customCommand + " " + versionFlag
	}
	cmd := exec.Command(resolvedPath, probeArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorType := spi.ClassifyCheckExecutionError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		slog.Info("Check: version probe failed",
			"resolved", resolvedPath,
			"errorType", errorType,
			"error", err,
			"stderr", stderrOutput)
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), stderrOutput)
		return spi.CheckResult{
			Success:      false,
			ErrorType:    errorType,
			Location:     resolvedPath,
			ErrorMessage: buildCheckErrorMessage(errorType, probeCommand, isCustom, stderrOutput),
		}
	}

	// Some wrappers print the version on stderr. A binary that prints nothing
	// still passes the check, reported as "unknown" so the result reads
	// unambiguously.
	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = strings.TrimSpace(stderr.String())
	}
	if version == "" {
		version = "unknown"
	}
	slog.Info("Check: succeeded", "resolved", resolvedPath, "version", version)
	analytics.TrackCheckSuccess(attempt, version)

	return spi.CheckResult{
		Success:  true,
		Version:  version,
		Location: resolvedPath,
	}
}

func buildCheckErrorMessage(errorType string, command string, isCustom bool, stderr string) string {
	var b strings.Builder

	switch errorType {
	case spi.CheckErrorNotFound:
		b.WriteString("OpenCode could not be found.\n\n")
		if isCustom {
			b.WriteString("• Verify the path you supplied actually points to the `opencode` executable.\n")
			fmt.Fprintf(&b, "• Provided command: %s\n", command)
		} else {
			b.WriteString("• Install OpenCode (https://opencode.ai) and ensure `opencode` is on your PATH.\n")
			b.WriteString("• Or pass a custom command via `specstory check opencode -c \"path/to/opencode\"`.\n")
		}
	case spi.CheckErrorPermissionDenied:
		b.WriteString("OpenCode exists but isn't executable.\n\n")
		fmt.Fprintf(&b, "• Fix permissions: `chmod +x %s`\n", command)
		b.WriteString("• Some package managers install the binary as root; run SpecStory with a path you can execute.\n")
	default:
		fmt.Fprintf(&b, "`%s` failed.\n\n", command)
		if stderr != "" {
			fmt.Fprintf(&b, "Error output:\n%s\n\n", stderr)
		}
		fmt.Fprintf(&b, "• Try running `%s` directly in your terminal.\n", command)
		b.WriteString("• If you upgraded recently, reinstall OpenCode to refresh its installation.\n")
	}

	return b.String()
}

// DetectAgent reports whether OpenCode has recorded a session started in this
// project directory.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	summaries, err := projectSessionSummaries(projectPath)
	switch {
	case errors.Is(err, errNoDatabase):
		if helpOutput {
			dbPath, _ := getDatabasePath()
			log.UserWarn("No OpenCode sessions were found for this directory.\n")
			log.UserMessage("OpenCode stores sessions in %s, which does not exist yet.\n\n", dbPath)
			log.UserMessage("Run `opencode` in this directory once, then run this command again.\n")
		}
		return false
	case err != nil:
		slog.Warn("DetectAgent: Failed to read OpenCode sessions", "projectPath", projectPath, "error", err)
		if helpOutput {
			log.UserWarn("Could not read OpenCode sessions: %v\n", err)
		}
		return false
	}

	for _, summary := range summaries {
		if summaryPrompt(summary) != "" {
			return true
		}
	}
	if helpOutput {
		log.UserWarn("No OpenCode sessions were found for this directory.\n")
		log.UserMessage("OpenCode records the directory each session starts in; none started here.\n")
		log.UserMessage("Start `opencode` from this directory so the provider can pick up its sessions.\n")
	}
	return false
}

// GetAgentChatSessions returns every session started in the project directory.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	var result []spi.AgentChatSession
	err := withDatabase(func(db *sql.DB) error {
		root, err := canonicalProjectPath(projectPath)
		if err != nil {
			return err
		}
		summaries, err := listSessionSummaries(db, root)
		if err != nil {
			return err
		}
		for i, summary := range summaries {
			snapshot, err := readSessionSnapshot(db, summary.ID)
			if err != nil {
				slog.Warn("GetAgentChatSessions: Failed to read OpenCode session, skipping",
					"sessionId", summary.ID, "error", err)
			} else if session := convertSnapshot(snapshot, root, debugRaw); session != nil {
				result = append(result, *session)
			}
			// Reported for every session, including skipped ones, so the
			// progress bar reaches its total.
			if progress != nil {
				progress(i+1, len(summaries))
			}
		}
		return nil
	})
	if errors.Is(err, errNoDatabase) {
		return nil, nil
	}
	return result, err
}

// GetAgentChatSession returns one session by id, or nil when it does not
// exist or was not started in this project. The database is global, so the
// directory check keeps a lookup from one project returning another's session.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	var result *spi.AgentChatSession
	err := withDatabase(func(db *sql.DB) error {
		root, err := canonicalProjectPath(projectPath)
		if err != nil {
			return err
		}
		snapshot, err := readSessionSnapshot(db, sessionID)
		if err != nil || snapshot == nil {
			return err
		}
		if snapshot.Session.ParentID != "" || snapshot.Session.Directory != root {
			slog.Debug("GetAgentChatSession: Session is not a top-level session of this project",
				"sessionId", sessionID, "directory", snapshot.Session.Directory, "projectPath", root)
			return nil
		}
		result = convertSnapshot(snapshot, root, debugRaw)
		return nil
	})
	if errors.Is(err, errNoDatabase) {
		return nil, nil
	}
	return result, err
}

// ListAgentChatSessions returns lightweight metadata for the project's
// sessions, read without decoding any message beyond the first prompt.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	summaries, err := projectSessionSummaries(projectPath)
	if errors.Is(err, errNoDatabase) {
		return []spi.SessionMetadata{}, nil
	}
	if err != nil {
		return nil, err
	}

	result := make([]spi.SessionMetadata, 0, len(summaries))
	for _, summary := range summaries {
		if metadata := summaryMetadata(summary); metadata != nil {
			result = append(result, *metadata)
		}
	}
	return result, nil
}

// ListAllAgentChatSessions enumerates every top-level session in the database,
// regardless of project. See docs/SESSIONS-DB.md.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates every top-level session while
// reporting scan progress into r (nil-safe). Used by `specstory reindex`.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	dbPath, err := getDatabasePath()
	if err != nil {
		return nil, err
	}

	refs := []spi.GlobalSessionRef{}
	err = withDatabase(func(db *sql.DB) error {
		summaries, err := listSessionSummaries(db, "")
		if err != nil {
			return err
		}
		for _, summary := range summaries {
			metadata := summaryMetadata(summary)
			if metadata == nil {
				continue
			}
			refs = append(refs, spi.GlobalSessionRef{
				SessionID:  metadata.SessionID,
				CreatedAt:  metadata.CreatedAt,
				Slug:       metadata.Slug,
				Name:       metadata.Name,
				NativePath: dbPath,
				// The directory OpenCode recorded is the session's origin;
				// an empty one stays empty so the CLI files it as unknown.
				OriginCwd: summary.Directory,
			})
			r.Add(1)
		}
		return nil
	})
	if errors.Is(err, errNoDatabase) {
		return []spi.GlobalSessionRef{}, nil
	}
	return refs, err
}

// ExecAgentAndWatch runs OpenCode interactively while watching the project's
// sessions, and returns OpenCode's exit status only after the watcher has
// delivered its final updates.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting OpenCode",
		"projectPath", projectPath,
		"resumeSessionId", resumeSessionID,
		"debugRaw", debugRaw)

	root, err := canonicalProjectPath(projectPath)
	if err != nil {
		return err
	}

	// A reconstructed session is staged as an export file; OpenCode must load
	// it before `opencode -s <id>` can open it.
	if err := importStagedSession(customCommand, root, resumeSessionID); err != nil {
		return err
	}

	watcher, err := startWatcher(root, debugRaw, sessionCallback)
	if err != nil {
		// run still launches OpenCode; the sessions are recovered by a later sync.
		slog.Error("ExecAgentAndWatch: Failed to start OpenCode session watcher", "error", err)
	}

	execErr := executeOpenCode(customCommand, root, resumeSessionID)

	if watcher != nil {
		watcher.Stop()
	}
	slog.Info("ExecAgentAndWatch: OpenCode session finished", "error", execErr)
	return execErr
}

// WatchAgent watches the project's sessions without launching OpenCode, until
// ctx is cancelled.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting OpenCode activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	root, err := canonicalProjectPath(projectPath)
	if err != nil {
		return err
	}
	watcher, err := startWatcher(root, debugRaw, sessionCallback)
	if err != nil {
		slog.Error("WatchAgent: Failed to start OpenCode session watcher", "error", err)
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	slog.Info("WatchAgent: Watcher started, blocking until context cancelled")
	<-ctx.Done()

	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	watcher.Stop()
	return ctx.Err()
}

// canonicalProjectPath returns the on-disk spelling of the project directory
// (symlinks resolved, case corrected), which is how OpenCode records it.
func canonicalProjectPath(projectPath string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
		projectPath = cwd
	}
	canonical, err := spi.GetCanonicalPath(projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project path %s: %w", projectPath, err)
	}
	return canonical, nil
}

// projectSessionSummaries lists the top-level sessions started in the project.
func projectSessionSummaries(projectPath string) ([]sessionSummary, error) {
	var summaries []sessionSummary
	err := withDatabase(func(db *sql.DB) error {
		root, err := canonicalProjectPath(projectPath)
		if err != nil {
			return err
		}
		summaries, err = listSessionSummaries(db, root)
		return err
	})
	return summaries, err
}

// summaryMetadata builds listing metadata from a summary, or nil when the
// session has no prompt yet. The slug is derived exactly as conversion derives
// it so listings name sessions the way their markdown files are named.
func summaryMetadata(summary sessionSummary) *spi.SessionMetadata {
	prompt := summaryPrompt(summary)
	if prompt == "" {
		return nil
	}
	slug := spi.GenerateFilenameFromUserMessage(prompt)
	if slug == "" {
		slug = defaultSlug
	}
	name := strings.TrimSpace(summary.Title)
	if name == "" {
		name = spi.GenerateReadableName(prompt)
	}
	return &spi.SessionMetadata{
		SessionID: summary.ID,
		CreatedAt: formatMillis(summary.TimeCreated),
		Slug:      slug,
		Name:      name,
	}
}

// summaryPrompt names a session the way conversion does: its first user
// prompt, else, for a session of shell commands only, its first command.
func summaryPrompt(summary sessionSummary) string {
	if prompt := firstPromptText(summary.FirstUserData); prompt != "" {
		return prompt
	}
	if summary.FirstShellData == "" {
		return ""
	}
	var shell struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(summary.FirstShellData), &shell); err != nil {
		slog.Debug("summaryPrompt: Unreadable OpenCode shell record", "error", err)
		return ""
	}
	return strings.TrimSpace(shell.Command)
}

// firstPromptText names a session from its first user record payload, the
// same way conversion does.
func firstPromptText(data string) string {
	if data == "" {
		return ""
	}
	var user nativeMessage
	if err := json.Unmarshal([]byte(data), &user); err != nil {
		slog.Debug("firstPromptText: Unreadable OpenCode user record", "error", err)
		return ""
	}
	return userPromptLabel(&user)
}
