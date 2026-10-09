package copilotcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Compile-time assertions that Provider satisfies the spi.Provider contract
// plus the optional path-based read and progress-reporting enumeration.
var (
	_ spi.Provider           = (*Provider)(nil)
	_ spi.PathSessionReader  = (*Provider)(nil)
	_ spi.ProgressEnumerator = (*Provider)(nil)
)

const (
	defaultCommand = "copilot"
	versionFlag    = "--version"
	// resumeFlag takes an optional value, so the session ID is attached with
	// "=" rather than passed as the next argument.
	resumeFlag      = "--resume"
	resumeShortFlag = "-r"
)

// Provider implements spi.Provider for GitHub Copilot CLI.
type Provider struct{}

// NewProvider creates a Copilot CLI provider.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the product name.
func (p *Provider) Name() string {
	return providerName
}

// Check verifies that the copilot binary resolves and reports a version.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName, cmdArgs := parseCommand(customCommand)
	isCustom := strings.TrimSpace(customCommand) != ""
	attempt := analytics.CheckAttempt{
		Provider:      providerID,
		CustomCommand: isCustom,
		CommandPath:   cmdName,
		VersionFlag:   versionFlag,
	}
	slog.Info("Check: verifying Copilot CLI installation", "command", cmdName, "customCommand", isCustom)

	resolved, err := spi.LookPathForCheck(cmdName)
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
	attempt.ResolvedPath = resolved

	// A custom command's arguments are part of how the user runs Copilot (a
	// wrapper script may need them), so the probe keeps them.
	probeArgs := append(slices.Clone(cmdArgs), versionFlag)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, probeArgs...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorType := spi.ClassifyCheckExecutionError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		slog.Info("Check: version probe failed",
			"resolved", resolved, "errorType", errorType, "stderr", stderrOutput, "error", err)
		analytics.TrackCheckFailure(attempt, errorType, err.Error(), stderrOutput)
		// Name the command actually run, a custom command's arguments
		// included; only a permission fix targets the executable alone.
		command := resolved
		if errorType != spi.CheckErrorPermissionDenied {
			command = strings.Join(append([]string{resolved}, cmdArgs...), " ")
		}
		return spi.CheckResult{
			Success:      false,
			ErrorType:    errorType,
			Location:     resolved,
			ErrorMessage: buildCheckErrorMessage(errorType, command, isCustom, stderrOutput),
		}
	}

	version := parseVersion(stdout.String())
	slog.Info("Check: succeeded", "resolved", resolved, "version", version)
	analytics.TrackCheckSuccess(attempt, version)
	return spi.CheckResult{Success: true, Version: version, Location: resolved}
}

// parseVersion extracts the version from `copilot --version`, whose first line
// reads "GitHub Copilot CLI 1.0.95-2." followed by an update hint.
func parseVersion(output string) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	fields := strings.Fields(firstLine)
	if len(fields) == 0 {
		return "unknown"
	}
	return strings.TrimSuffix(fields[len(fields)-1], ".")
}

func buildCheckErrorMessage(errorType string, command string, isCustom bool, stderr string) string {
	var b strings.Builder
	switch errorType {
	case spi.CheckErrorNotFound:
		b.WriteString("Copilot CLI could not be found.\n\n")
		if isCustom {
			b.WriteString("• Verify the path you supplied points to the `copilot` executable.\n")
			fmt.Fprintf(&b, "• Provided command: %s\n", command)
		} else {
			b.WriteString("• Install GitHub Copilot CLI and ensure `copilot` is on your PATH.\n")
			b.WriteString("• Or pass a custom command via `specstory check copilot -c \"path/to/copilot\"`.\n")
		}
	case spi.CheckErrorPermissionDenied:
		b.WriteString("Copilot CLI exists but isn't executable.\n\n")
		fmt.Fprintf(&b, "• Fix permissions: `chmod +x %s`\n", command)
	default:
		fmt.Fprintf(&b, "`%s %s` failed.\n\n", command, versionFlag)
		if stderr != "" {
			fmt.Fprintf(&b, "Error output:\n%s\n\n", stderr)
		}
		fmt.Fprintf(&b, "• Try running `%s %s` directly in your terminal.\n", command, versionFlag)
	}
	return b.String()
}

// DetectAgent reports whether Copilot CLI has a session started in this
// project directory.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	metadata, err := p.ListAgentChatSessions(projectPath)
	if err != nil {
		slog.Warn("DetectAgent: Failed to read Copilot CLI sessions", "projectPath", projectPath, "error", err)
		if helpOutput {
			log.UserWarn("Could not read Copilot CLI sessions: %v\n", err)
		}
		return false
	}
	if len(metadata) > 0 {
		return true
	}
	if helpOutput {
		root, _ := sessionsRoot()
		log.UserWarn("No Copilot CLI sessions were found for this directory.\n")
		log.UserMessage("Copilot CLI stores sessions in %s and records the directory each one starts in.\n", root)
		log.UserMessage("Start `copilot` from this directory, then run this command again.\n")
	}
	return false
}

// projectSession is a session file known to belong to the project.
type projectSession struct {
	File   sessionFile
	Header *sessionHeader
}

// projectSessions returns the sessions started in the project directory. The
// session store is global, so each session's recorded cwd decides ownership.
func projectSessions(projectPath string) ([]projectSession, error) {
	root, err := canonicalProjectPath(projectPath)
	if err != nil {
		return nil, err
	}
	files, err := listSessionFiles()
	if err != nil {
		return nil, err
	}
	var result []projectSession
	for _, file := range files {
		header, err := readSessionHeader(file.Path, false)
		if err != nil {
			slog.Debug("Skipping unreadable Copilot CLI session", "path", file.Path, "error", err)
			continue
		}
		if belongsToProject(header, root) {
			result = append(result, projectSession{File: file, Header: header})
		}
	}
	return result, nil
}

// belongsToProject reports whether a session started in the project directory.
// Copilot records its working directory as the operating system reports it:
// symlinks resolved and in on-disk case, the same spelling GetCanonicalPath
// gives the local project root, so the two compare exactly. The recorded path
// is not canonicalized itself: it may name a directory on another machine.
func belongsToProject(header *sessionHeader, canonicalRoot string) bool {
	return header.Cwd != "" && header.Cwd == canonicalRoot
}

// GetAgentChatSessions returns every session started in the project directory.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	sessions, err := projectSessions(projectPath)
	if err != nil {
		return nil, err
	}
	result := make([]spi.AgentChatSession, 0, len(sessions))
	for i, session := range sessions {
		chat, err := convertSession(session.File, session.Header, debugRaw, false)
		if err != nil {
			slog.Warn("GetAgentChatSessions: Failed to read Copilot CLI session, skipping",
				"sessionId", session.File.SessionID, "error", err)
		} else if chat != nil {
			result = append(result, *chat)
		}
		// Reported for skipped sessions too, so the progress bar reaches its total.
		if progress != nil {
			progress(i+1, len(sessions))
		}
	}
	return result, nil
}

// GetAgentChatSession returns one session by ID, or nil when it does not exist
// or was not started in this project. The store is global, so the project
// check keeps a lookup from one project returning another's session.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	if !isValidSessionID(sessionID) {
		return nil, nil
	}
	root, err := canonicalProjectPath(projectPath)
	if err != nil {
		return nil, err
	}
	sessionsDir, err := sessionsRoot()
	if err != nil {
		return nil, err
	}
	file, ok := statSessionFile(filepath.Join(sessionsDir, sessionID, eventsFileName))
	if !ok {
		return nil, nil
	}
	header, err := readSessionHeader(file.Path, false)
	if err != nil {
		return nil, err
	}
	if !belongsToProject(header, root) {
		slog.Debug("GetAgentChatSession: Session was not started in this project",
			"sessionId", sessionID, "cwd", header.Cwd, "projectPath", root)
		return nil, nil
	}
	return convertSession(file, header, debugRaw, false)
}

// GetAgentChatSessionByPath reads a session straight from its transcript path,
// as recorded by reindex. originCwd is unused: the transcript records its own.
func (p *Provider) GetAgentChatSessionByPath(nativePath string, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	file, ok := statSessionFile(nativePath)
	if !ok {
		return nil, nil
	}
	header, err := readSessionHeader(file.Path, false)
	if err != nil {
		return nil, err
	}
	return convertSession(file, header, debugRaw, false)
}

// statSessionFile describes the transcript at path, or reports false when it is
// missing.
func statSessionFile(path string) (sessionFile, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return sessionFile{}, false
	}
	return sessionFile{
		SessionID: filepath.Base(filepath.Dir(path)),
		Path:      path,
		Size:      info.Size(),
		ModTime:   info.ModTime(),
	}, true
}

// ListAgentChatSessions returns lightweight metadata for the project's
// sessions, reading each transcript only up to its first prompt.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	sessions, err := projectSessions(projectPath)
	if err != nil {
		return nil, err
	}
	result := make([]spi.SessionMetadata, 0, len(sessions))
	for _, session := range sessions {
		summary, err := readSessionSummary(session.File.Path)
		if err != nil {
			slog.Debug("ListAgentChatSessions: Skipping unreadable session", "path", session.File.Path, "error", err)
			continue
		}
		if metadata := summary.metadata(session.File.SessionID); metadata != nil {
			result = append(result, *metadata)
		}
	}
	return result, nil
}

// ListAllAgentChatSessions enumerates every session regardless of project.
// See docs/SESSIONS-DB.md.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates every session while reporting
// scan progress into r (nil-safe). Used by `specstory reindex`.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	root, err := sessionsRoot()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return []spi.GlobalSessionRef{}, nil
	}
	return spi.ScanSessionsInParallel(root, providerName, r, func(path string) (*spi.GlobalSessionRef, error) {
		// The walk also reaches JSONL files a session keeps in its own
		// workspace folders; only a session's top-level transcript counts.
		if filepath.Base(path) != eventsFileName || filepath.Dir(filepath.Dir(path)) != root {
			return nil, nil
		}
		summary, err := readSessionSummary(path)
		if errors.Is(err, errNotSession) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		sessionID := filepath.Base(filepath.Dir(path))
		metadata := summary.metadata(sessionID)
		if metadata == nil {
			return nil, nil
		}
		return &spi.GlobalSessionRef{
			SessionID:  metadata.SessionID,
			CreatedAt:  metadata.CreatedAt,
			Slug:       metadata.Slug,
			Name:       metadata.Name,
			NativePath: path,
			OriginCwd:  summary.Header.Cwd,
		}, nil
	})
}

// ExecAgentAndWatch runs Copilot CLI interactively while watching the
// project's sessions, and returns Copilot's exit status only after the
// watcher has delivered its final updates.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: Starting Copilot CLI",
		"projectPath", projectPath, "resumeSessionId", resumeSessionID, "debugRaw", debugRaw)

	root, err := canonicalProjectPath(projectPath)
	if err != nil {
		return err
	}

	var watcher *sessionWatcher
	if sessionCallback != nil {
		watcher, err = startWatcher(root, debugRaw, sessionCallback)
		if err != nil {
			// Copilot still launches; its sessions are recovered by a later sync.
			slog.Error("ExecAgentAndWatch: Failed to start Copilot CLI session watcher", "error", err)
		}
	}

	execErr := ExecuteCopilot(customCommand, resumeSessionID)

	if watcher != nil {
		watcher.Stop()
	}
	slog.Info("ExecAgentAndWatch: Copilot CLI session finished", "error", execErr)
	return execErr
}

// WatchAgent watches the project's sessions without launching Copilot CLI,
// until ctx is cancelled.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Copilot CLI activity monitoring", "projectPath", projectPath, "debugRaw", debugRaw)

	root, err := canonicalProjectPath(projectPath)
	if err != nil {
		return err
	}
	watcher, err := startWatcher(root, debugRaw, sessionCallback)
	if err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	<-ctx.Done()
	slog.Info("WatchAgent: Context cancelled, stopping watcher")
	watcher.Stop()
	return ctx.Err()
}

// canonicalProjectPath returns the on-disk spelling of the project directory
// (symlinks resolved, case corrected), defaulting to the working directory.
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

// parseCommand splits a custom command string into the executable and its
// arguments, falling back to `copilot` on PATH.
func parseCommand(customCommand string) (string, []string) {
	if parts := spi.SplitCommandLine(customCommand); len(parts) > 0 {
		return expandTilde(parts[0]), parts[1:]
	}
	return defaultCommand, nil
}

// expandTilde lets users configure commands like "~/bin/copilot".
func expandTilde(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

// resumeArgs makes the requested session override any configured resume flag.
// A fresh flag is attached with "=" because --resume's value is optional and
// a separate argument could be read as a prompt or another option.
func resumeArgs(args []string, sessionID string) []string {
	if sessionID == "" {
		return args
	}
	for _, arg := range args {
		if arg == "--" {
			break
		}
		flag, _, _ := strings.Cut(arg, "=")
		if flag == resumeFlag || flag == resumeShortFlag {
			return spi.EnsureResumeFlagArgs(args, sessionID, resumeFlag, resumeShortFlag)
		}
	}
	return append(slices.Clone(args), resumeFlag+"="+sessionID)
}

// ExecuteCopilot launches Copilot CLI attached to the terminal and blocks until
// it exits, returning an *spi.AgentExitError when Copilot exits non-zero so the
// CLI can report Copilot's own status after saving the last turn.
func ExecuteCopilot(customCommand string, resumeSessionID string) error {
	cmdName, args := parseCommand(customCommand)
	args = resumeArgs(args, resumeSessionID)
	slog.Info("ExecuteCopilot: Launching Copilot CLI", "command", cmdName, "args", args)

	command := exec.Command(cmdName, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &spi.AgentExitError{Agent: providerName, Code: exitErr.ExitCode()}
	}
	return err
}
