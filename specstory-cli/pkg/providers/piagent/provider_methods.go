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

// ListAgentChatSessions returns lightweight metadata for all project sessions
// without a full parse (header fields only).
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	files, err := listProjectSessions(projectPath)
	if err != nil {
		return nil, err
	}
	result := make([]spi.SessionMetadata, 0, len(files))
	for _, sf := range files {
		result = append(result, spi.SessionMetadata{
			SessionID: sf.Header.ID,
			CreatedAt: sf.Header.Timestamp,
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
// progress, using the shared parallel scanner.
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
	return spi.ScanSessionsInParallel(root, providerID, r, scanPiSession)
}

// scanPiSession reads one .jsonl file's header and returns a GlobalSessionRef
// carrying the originating cwd, or (nil, nil) for non-session files.
func scanPiSession(path string) (*spi.GlobalSessionRef, error) {
	h, err := readHeader(path)
	if err != nil || h == nil {
		return nil, nil
	}
	return &spi.GlobalSessionRef{SessionID: h.ID, CreatedAt: h.Timestamp, NativePath: path, OriginCwd: h.Cwd}, nil
}
