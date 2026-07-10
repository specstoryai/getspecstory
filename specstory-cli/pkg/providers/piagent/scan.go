package piagent

import (
	"encoding/json"
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
// the header (session id, timestamp, cwd) and the first user message text, then
// stops. Uses bufio.Reader (via readLines) so arbitrarily large lines parse
// without the 16MB bufio.Scanner cap. Returns (scan, nil) for a real session,
// (nil, nil) for a non-session file or a session with no user message, and
// (nil, err) for genuine read/parse errors so ScanSessionsInParallel logs them.
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
