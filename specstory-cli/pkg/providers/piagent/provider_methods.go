package piagent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

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
