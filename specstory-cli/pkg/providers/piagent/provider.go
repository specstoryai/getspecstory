package piagent

import (
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
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const (
	providerID    = "pi"
	providerName  = "Pi"
	defaultCmd    = "pi"
	versionFlag   = "--version"
	notYetSupport = "pi: %s not yet supported for the pi provider (v1 ships sync/list/search/reindex only)"
)

// Provider implements spi.Provider for the pi coding agent.
// pi stores sessions as JSONL v3 trees under ~/.pi/agent/sessions/--<encoded-cwd>--/.
type Provider struct{}

// NewProvider returns a new pi provider instance.
func NewProvider() *Provider { return &Provider{} }

// Name returns the human-readable provider name.
func (p *Provider) Name() string { return providerName }

// parsePiCommand splits a custom check/run command into binary + args,
// expanding a leading ~ like sibling providers (spi.SplitCommandLine handles
// quoting). Falls back to the default `pi` binary when no custom command is
// given.
func parsePiCommand(customCommand string) (string, []string) {
	if strings.TrimSpace(customCommand) != "" {
		parts := spi.SplitCommandLine(customCommand)
		if len(parts) > 0 {
			return expandTilde(parts[0]), parts[1:]
		}
	}
	return defaultCmd, nil
}

// classifyCheckError buckets a Check failure for messaging and analytics,
// matching the sibling providers' error taxonomy. errors.Is unwraps through
// exec.Error/os.PathError, covering both PATH lookups (exec.ErrNotFound) and
// explicit custom paths (os.ErrNotExist).
func classifyCheckError(err error) string {
	switch {
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist):
		return "not_found"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	default:
		return "version_failed"
	}
}

// buildCheckErrorMessage renders the user-facing Check failure text. The
// custom-command branch avoids the misleading "not found on PATH" wording when
// the user pointed at an explicit path, and version-probe failures surface the
// probe's stderr so the underlying diagnostic is not lost.
func buildCheckErrorMessage(errorType, command string, isCustom bool, stderr string) string {
	var b strings.Builder
	switch errorType {
	case "not_found":
		b.WriteString("The pi coding agent was not found.\n\n")
		if isCustom {
			b.WriteString("• Verify the custom command/path you provided exists and is executable.\n")
			fmt.Fprintf(&b, "• Provided command: %s\n", command)
		} else {
			b.WriteString("• Install pi (see https://pi.dev) and ensure `pi` is on your PATH.\n")
			b.WriteString("• Re-run `specstory check pi` after installation.\n")
		}
	case "permission_denied":
		b.WriteString("SpecStory cannot execute the pi binary due to permissions.\n\n")
		fmt.Fprintf(&b, "Try: chmod +x %s\n", command)
	default:
		b.WriteString("`pi --version` failed.\n\n")
		if stderr != "" {
			b.WriteString("Error output:\n")
			b.WriteString(stderr)
			b.WriteString("\n\n")
		}
		b.WriteString("Run `pi --version` manually to diagnose, then retry.")
	}
	return b.String()
}

// Check verifies the pi binary is available and reports its version.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	isCustom := strings.TrimSpace(customCommand) != ""
	cmdName, _ := parsePiCommand(customCommand)
	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		errorType := classifyCheckError(err)
		slog.Info("pi: Check binary not found", "command", cmdName, "error", err)
		trackCheckFailure(isCustom, cmdName, "", "", errorType, err.Error())
		return spi.CheckResult{
			Success:      false,
			ErrorMessage: buildCheckErrorMessage(errorType, cmdName, isCustom, ""),
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, versionFlag)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errorType := classifyCheckError(err)
		stderrOutput := strings.TrimSpace(stderr.String())
		slog.Info("pi: Check version probe failed", "resolved", resolved, "error", err, "stderr", stderrOutput)
		trackCheckFailure(isCustom, cmdName, resolved, stderrOutput, errorType, err.Error())
		return spi.CheckResult{
			Success:      false,
			Location:     resolved,
			ErrorMessage: buildCheckErrorMessage(errorType, cmdName, isCustom, stderrOutput),
		}
	}
	version := strings.TrimSpace(stdout.String())
	if version == "" {
		version = "unknown"
	}
	trackCheckSuccess(isCustom, cmdName, resolved, version)
	return spi.CheckResult{Success: true, Version: version, Location: resolved}
}

// DetectAgent reports whether pi has created sessions for the given project.
// When helpOutput is true and nothing is found, it prints guidance like the
// sibling providers do — the CLI callers (sync/list) rely on the provider to
// explain a negative result instead of exiting silently.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	files, err := SessionFilesInProject(projectPath)
	if err != nil {
		slog.Debug("pi: DetectAgent error", "error", err)
		return false
	}
	if len(files) > 0 {
		return true
	}
	if helpOutput {
		log.UserMessage("No pi sessions found for this project yet.\n")
		if dir, dirErr := ProjectSessionDir(projectPath); dirErr == nil {
			log.UserMessage("Expected session directory: %s\n", dir)
		}
		log.UserMessage("Run pi inside this project to create a session, then rerun `specstory sync pi`.\n")
	}
	return false
}

// ExecAgentAndWatch is the `specstory run pi` wrapper — out of v1 scope.
func (p *Provider) ExecAgentAndWatch(_ string, _ string, _ string, _ bool, _ func(*spi.AgentChatSession)) error {
	return fmt.Errorf(notYetSupport, "ExecAgentAndWatch (specstory run pi)")
}

// WatchAgent is live watch — out of v1 scope.
func (p *Provider) WatchAgent(_ context.Context, _ string, _ bool, _ func(*spi.AgentChatSession)) error {
	return fmt.Errorf(notYetSupport, "WatchAgent")
}

// ReconstructSession is out of v1 scope; pi has no native serializer yet.
func (p *Provider) ReconstructSession(_ *schema.SessionData, _ spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, errors.Join(spi.ErrReconstructionUnsupported, fmt.Errorf(notYetSupport, "ReconstructSession"))
}

// NativeSessionPath is out of v1 scope (no native serializer).
func (p *Provider) NativeSessionPath(_ string, _ string) (string, error) {
	return "", errors.Join(spi.ErrReconstructionUnsupported, fmt.Errorf(notYetSupport, "NativeSessionPath"))
}

// trackCheckSuccess emits the standard install-check success analytics event,
// matching the shape other providers use.
func trackCheckSuccess(custom bool, commandPath, resolvedPath, version string) {
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       providerID,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version":        version,
		"version_flag":   versionFlag,
	})
}

// trackCheckFailure emits the standard install-check failure analytics event,
// including the version probe's stderr when available (matching droidcli).
func trackCheckFailure(custom bool, commandPath, resolvedPath, stderrOutput, errorType, message string) {
	props := analytics.Properties{
		"provider":       providerID,
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

// sessionFile pairs a discovered pi session file with its header metadata.
type sessionFile struct {
	Path   string
	Header sessionHeader
}

// findProjectSession locates the session file with the given ID within the
// project's pi session directory. Returns "" if not found.
func findProjectSession(projectPath, sessionID string) (string, error) {
	files, err := SessionFilesInProject(projectPath)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		h, err := readHeader(f)
		if err != nil || h == nil {
			continue
		}
		if h.ID == sessionID {
			return f, nil
		}
	}
	return "", nil
}

// readHeader parses only the first JSON value of a session file and returns it
// only if it is a valid pi session header (type=="session" with a non-empty
// id). Non-session files return (nil, nil) so callers skip them. The read is
// capped at 1MB: a real pi header is a few hundred bytes, and the cap keeps a
// crafted file with a multi-GB first value from being buffered into memory.
func readHeader(path string) (*sessionHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(io.LimitReader(f, 1*MB))
	var h sessionHeader
	if err := dec.Decode(&h); err != nil {
		return nil, nil
	}
	if h.Type != entrySession || h.ID == "" {
		return nil, nil
	}
	return &h, nil
}

// listProjectSessions returns all session files in the project's pi directory
// with their headers. Non-session files (readHeader returns nil,nil) are
// skipped silently; only a genuine read error is logged.
func listProjectSessions(projectPath string) ([]sessionFile, error) {
	files, err := SessionFilesInProject(projectPath)
	if err != nil {
		return nil, err
	}
	var out []sessionFile
	for _, f := range files {
		h, err := readHeader(f)
		if err != nil {
			slog.Debug("pi: skipping unreadable session file", "path", f, "error", err)
			continue
		}
		if h == nil {
			continue // not a pi session file (bad header type/id)
		}
		out = append(out, sessionFile{Path: f, Header: *h})
	}
	return out, nil
}

// GetAgentChatSession returns a single pi session by ID for the project.
func (p *Provider) GetAgentChatSession(projectPath, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	path, err := findProjectSession(projectPath, sessionID)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	return parseToAgentSession(path, debugRaw)
}

// GetAgentChatSessions returns all pi sessions for the project.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	files, err := listProjectSessions(projectPath)
	if err != nil {
		return nil, err
	}
	total := len(files)
	var result []spi.AgentChatSession
	for i, sf := range files {
		chat, pErr := parseToAgentSession(sf.Path, debugRaw)
		if pErr != nil {
			slog.Debug("pi: skipping session", "path", sf.Path, "error", pErr)
		} else if chat != nil {
			result = append(result, *chat)
		}
		if progress != nil {
			progress(i+1, total)
		}
	}
	return result, nil
}

// parseToAgentSession parses one session file into an AgentChatSession,
// writing debug-raw artifacts when debugRaw is true.
func parseToAgentSession(path string, debugRaw bool) (*spi.AgentChatSession, error) {
	data, err := ParseSession(path)
	if err != nil {
		return nil, err
	}
	if debugRaw {
		if dErr := writeDebugRaw(path, data); dErr != nil {
			slog.Warn("pi: debug-raw write failed", "error", dErr)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pi: reading raw session: %w", err)
	}
	return &spi.AgentChatSession{
		SessionID:   data.SessionID,
		CreatedAt:   data.CreatedAt,
		Slug:        deriveSlug(data),
		SessionData: data,
		RawData:     string(raw),
	}, nil
}

// GetAgentChatSessionByPath parses a single pi session directly from its native
// file path, skipping the by-id discovery search. originCwd is the session's
// originating working directory (GlobalSessionRef.OriginCwd), passed through
// as the workspace root for path normalization — matching what
// GetAgentChatSession receives as projectPath. Implements spi.PathSessionReader
// so `specstory reindex` uses the O(N) path-keyed fast path instead of the
// O(N²) by-id lookup.
func (p *Provider) GetAgentChatSessionByPath(nativePath, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	data, err := ParseSession(nativePath)
	if err != nil {
		return nil, err
	}
	if data.WorkspaceRoot == "" && originCwd != "" {
		data.WorkspaceRoot = originCwd
	}
	if debugRaw {
		if dErr := writeDebugRaw(nativePath, data); dErr != nil {
			slog.Warn("pi: debug-raw write failed", "error", dErr)
		}
	}
	raw, err := os.ReadFile(nativePath)
	if err != nil {
		return nil, fmt.Errorf("pi: reading raw session: %w", err)
	}
	return &spi.AgentChatSession{
		SessionID:   data.SessionID,
		CreatedAt:   data.CreatedAt,
		Slug:        deriveSlug(data),
		SessionData: data,
		RawData:     string(raw),
	}, nil
}

// ListAgentChatSessions returns lightweight metadata for all project sessions,
// deriving Slug/Name from the leaf path's first user message (and the user-set
// session name when present) without decoding full message payloads.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	files, err := SessionFilesInProject(projectPath)
	if err != nil {
		return nil, err
	}
	result := make([]spi.SessionMetadata, 0, len(files))
	for _, f := range files {
		scan, scanErr := scanPiSession(f)
		if scanErr != nil {
			slog.Debug("pi: skipping unreadable session file", "path", f, "error", scanErr)
			continue
		}
		if scan == nil || !scan.foundUser {
			continue
		}
		result = append(result, spi.SessionMetadata{
			SessionID: scan.sessionID,
			CreatedAt: scan.timestamp,
			Slug:      spi.GenerateFilenameFromUserMessage(scan.firstUserMessage),
			Name:      scanName(scan),
		})
	}
	return result, nil
}

// ListAllAgentChatSessions enumerates every pi session across all projects,
// each ref carrying its originating cwd (read from the session header).
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	return p.ListAllAgentChatSessionsProgress(nil)
}

// ListAllAgentChatSessionsProgress enumerates all pi sessions with live scan
// progress, using the shared parallel scanner. Each ref carries Slug/Name
// derived from the first user message and the originating cwd from the header.
func (p *Provider) ListAllAgentChatSessionsProgress(r *spi.ScanReporter) ([]spi.GlobalSessionRef, error) {
	root, flat, err := piSessionsRoot()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(root); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, nil
		}
		return nil, statErr
	}
	// ScanSessionsInParallel walks recursively, but pi session files live at a
	// fixed depth: directly in the root for the flat override layout, else in
	// root/<encoded-cwd>/. Deeper *.jsonl files are extension/subagent
	// internals (observed: <proj>/<ts>_<uuid>/<hash>/run-0/session.jsonl) that
	// the project-scoped APIs (SessionFilesInProject) can never resolve by id;
	// indexing them would create rows sync/preview/resume cannot fetch.
	wantDepth := 1
	if flat {
		wantDepth = 0
	}
	scan := func(path string) (*spi.GlobalSessionRef, error) {
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil || strings.Count(rel, string(filepath.Separator)) != wantDepth {
			return nil, nil
		}
		s, sErr := scanPiSession(path)
		if sErr != nil {
			return nil, sErr
		}
		if s == nil {
			return nil, nil // non-session file
		}
		return scanToGlobalRef(s, path), nil
	}
	return spi.ScanSessionsInParallel(root, providerID, r, scan)
}

// piSessionScan holds the metadata fields read from a session file for
// listing/reindex: identity + first-user-message metadata + originating cwd +
// the latest user-set session name. The scan walks the SAME leaf path the full
// parse uses, so its Slug/Name always match what deriveSlug produces for the
// generated markdown (file order can differ from the active branch when the
// first prompt was re-edited). foundUser is false for sessions with no real
// user prompt; scanPiSession returns (nil, nil) for those so callers skip them.
type piSessionScan struct {
	sessionID        string
	timestamp        string
	firstUserMessage string
	sessionName      string
	cwd              string
	foundUser        bool
}

// scanPiSession reads a pi session file's metadata for listing: the header
// (session id, timestamp, cwd), the first user message text on the active leaf
// path, and the latest session_info display name. Returns (scan, nil) for a
// real session, (nil, nil) for a non-session file or a session with no user
// message, and (nil, err) for genuine read/parse errors so
// ScanSessionsInParallel logs them during reindex.
func scanPiSession(path string) (*piSessionScan, error) {
	h, err := readHeader(path)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, nil // not a pi session file
	}
	_, entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil // header-only session; nothing to list
	}
	scan := &piSessionScan{sessionID: h.ID, timestamp: h.Timestamp, cwd: h.Cwd}
	// pi resolves the display name from the LATEST session_info entry in file
	// order (not the leaf path), and an empty name explicitly clears the title
	// (session-manager.ts getSessionName) — mirror both rules.
	for _, e := range entries {
		if e.Type == entrySessionInfo {
			scan.sessionName = strings.TrimSpace(e.Name)
		}
	}
	for _, e := range leafPathEntries(entries) {
		if scan.foundUser || e.Type != entryMessage {
			continue
		}
		if msg := firstUserText(e); msg != "" {
			scan.firstUserMessage = msg
			scan.foundUser = true
		}
	}
	if !scan.foundUser {
		return nil, nil // no user prompt; nothing worth listing
	}
	return scan, nil
}

// scanName returns the display name for a scanned session: the user-set
// session_info name when present (pi lets users rename sessions), otherwise a
// readable name derived from the first user message like other providers.
func scanName(scan *piSessionScan) string {
	if scan.sessionName != "" {
		return scan.sessionName
	}
	return spi.GenerateReadableName(scan.firstUserMessage)
}

// scanToGlobalRef builds a GlobalSessionRef from a scan, deriving Slug/Name from
// the first user message. Returns nil for sessions with no user message.
func scanToGlobalRef(scan *piSessionScan, path string) *spi.GlobalSessionRef {
	if !scan.foundUser {
		return nil
	}
	return &spi.GlobalSessionRef{
		SessionID:  scan.sessionID,
		CreatedAt:  scan.timestamp,
		Slug:       spi.GenerateFilenameFromUserMessage(scan.firstUserMessage),
		Name:       scanName(scan),
		NativePath: path,
		OriginCwd:  scan.cwd,
	}
}
