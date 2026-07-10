package piagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

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
// the header (session id, timestamp, cwd) and the first user message text.
// Returns (scan, nil) for a real session, (nil, nil) for a non-session file or
// a session with no user message, and (nil, err) for genuine read/parse errors
// so ScanSessionsInParallel logs them during reindex.
func scanPiSession(path string) (*piSessionScan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("pi: opening session %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	scan := &piSessionScan{}

	headerRead := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !headerRead {
			var h sessionHeader
			if err := json.Unmarshal([]byte(line), &h); err != nil {
				return nil, nil // not a pi session (bad first line)
			}
			if h.Type != entrySession || h.ID == "" {
				return nil, nil // not a pi session header
			}
			scan.sessionID = h.ID
			scan.timestamp = h.Timestamp
			scan.cwd = h.Cwd
			headerRead = true
			continue
		}
		var e rawEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Type != entryMessage {
			continue
		}
		if msg := firstUserText(e); msg != "" {
			scan.firstUserMessage = msg
			scan.foundUser = true
			break // got what we need; stop reading immediately
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("pi: reading session %s: %w", path, err)
	}
	if !scan.foundUser {
		return nil, nil // no user message → skip (warmup-only / empty)
	}
	return scan, nil
}

// firstUserText extracts the first user message text from a message entry, if
// its role is "user". Returns "" for non-user messages or empty content.
func firstUserText(e rawEntry) string {
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(e.Message, &m); err != nil {
		return ""
	}
	if m.Role != roleUser {
		return ""
	}
	return userContentString(m.Content)
}

// userContentString extracts a plain string from a pi user message content
// field (either a string or an array of {type:text} blocks). The result is
// trimmed so the scan path produces the same Slug/Name as the full-parse path
// (deriveSlug trims too).
func userContentString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &s); err == nil {
			return strings.TrimSpace(s)
		}
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			return strings.TrimSpace(b.Text)
		}
	}
	return ""
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
