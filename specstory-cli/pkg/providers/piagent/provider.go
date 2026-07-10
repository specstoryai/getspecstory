package piagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
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

// Check verifies the pi binary is on PATH and reports its version.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	cmdName := strings.TrimSpace(customCommand)
	isCustom := cmdName != ""
	if cmdName == "" {
		cmdName = defaultCmd
	}
	resolved, err := exec.LookPath(cmdName)
	if err != nil {
		slog.Info("pi: Check binary not found", "command", cmdName, "error", err)
		trackCheckFailure(isCustom, cmdName, "", "not_found", err.Error())
		return spi.CheckResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("pi binary '%s' not found on PATH", cmdName),
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(resolved, versionFlag)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		slog.Info("pi: Check version probe failed", "resolved", resolved, "error", err)
		trackCheckFailure(isCustom, cmdName, resolved, "version_probe_failed", err.Error())
		return spi.CheckResult{
			Success:      false,
			Location:     resolved,
			ErrorMessage: fmt.Sprintf("pi version probe failed: %v", err),
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
func (p *Provider) DetectAgent(projectPath string, _ bool) bool {
	files, err := SessionFilesInProject(projectPath)
	if err != nil {
		slog.Debug("pi: DetectAgent error", "error", err)
		return false
	}
	return len(files) > 0
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

// trackCheckFailure emits the standard install-check failure analytics event.
func trackCheckFailure(custom bool, commandPath, resolvedPath, errorType, message string) {
	analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
		"provider":       providerID,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version_flag":   versionFlag,
		"error_type":     errorType,
		"error_message":  message,
	})
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

// readHeader parses only the first line of a session file and returns it only
// if it is a valid pi session header (type=="session" with a non-empty id).
// Non-session files return (nil, nil) so callers skip them.
func readHeader(path string) (*sessionHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
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
// deriving Slug/Name from the first user message via a bounded single-pass scan
// (no full parse), matching how other providers populate metadata.
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
			Name:      spi.GenerateReadableName(scan.firstUserMessage),
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
	root, err := piSessionsRoot()
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(root); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, nil
		}
		return nil, statErr
	}
	scan := func(path string) (*spi.GlobalSessionRef, error) {
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

// piSessionScan holds the minimal fields read from a session file in one bounded
// pass: identity + first-user-message metadata + originating cwd. The scan
// stops as soon as it has the first user message, so it does NOT parse the whole
// session. foundUser is false for sessions with no real user prompt; scanPiSession
// returns (nil, nil) for those so callers skip them.
type piSessionScan struct {
	sessionID        string
	timestamp        string
	firstUserMessage string
	cwd              string
	foundUser        bool
}

// scanPiSession reads minimal data from a pi session file in one bounded pass:
// the header (session id, timestamp, cwd) and the first user message text, then
// stops. Returns (scan, nil) for a real session, (nil, nil) for a non-session
// file or a session with no user message, and (nil, err) for genuine read/parse
// errors so ScanSessionsInParallel logs them during reindex.
func scanPiSession(path string) (*piSessionScan, error) {
	scan := &piSessionScan{}
	headerRead := false
	err := readLines(path, func(line string) error {
		if !headerRead {
			var h sessionHeader
			if jErr := json.Unmarshal([]byte(line), &h); jErr != nil {
				return errStopRead // not a pi session (bad first line)
			}
			if h.Type != entrySession || h.ID == "" {
				return errStopRead // not a pi session header
			}
			scan.sessionID = h.ID
			scan.timestamp = h.Timestamp
			scan.cwd = h.Cwd
			headerRead = true
			return nil
		}
		var e rawEntry
		if jErr := json.Unmarshal([]byte(line), &e); jErr != nil {
			return nil // skip malformed line
		}
		if e.Type != entryMessage {
			return nil
		}
		if msg := firstUserText(e); msg != "" {
			scan.firstUserMessage = msg
			scan.foundUser = true
			return errStopRead // got what we need; stop reading
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !headerRead || !scan.foundUser {
		return nil, nil // non-session file or no user message
	}
	return scan, nil
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
		Name:       spi.GenerateReadableName(scan.firstUserMessage),
		NativePath: path,
		OriginCwd:  scan.cwd,
	}
}
