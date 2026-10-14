package cursorcloud

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// apiMe is the subset of GET /v1/me that Check reports. KeyLabel is the
// display name the user typed when creating the key on the Cursor dashboard,
// shown so they can tell which key is in use; the key itself never leaves the
// Authorization header.
type apiMe struct {
	KeyLabel string `json:"apiKeyName"`
}

// apiAgent is the subset of a v1 agent object the provider reads. The raw
// bytes travel alongside it (agentRecord) so unknown fields reach the mirror.
type apiAgent struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Repos       []apiRepo `json:"repos"`
	URL         string    `json:"url"`
	CreatedAt   string    `json:"createdAt"`
	UpdatedAt   string    `json:"updatedAt"`
	LatestRunID string    `json:"latestRunId"`
}

type apiRepo struct {
	URL string `json:"url"`
}

// apiRun is the subset of a v1 run object the provider reads.
type apiRun struct {
	ID         string `json:"id"`
	AgentID    string `json:"agentId"`
	Status     string `json:"status"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	DurationMs int64  `json:"durationMs"`
	Git        struct {
		Branches []struct {
			RepoURL string `json:"repoUrl"`
			Branch  string `json:"branch"`
			PrURL   string `json:"prUrl"`
		} `json:"branches"`
	} `json:"git"`
}

// isTerminal reports whether a run status is final. The non-terminal statuses
// observed are CREATING and RUNNING; anything else is treated as terminal so an
// unfamiliar final status never keeps a live stream attached forever.
func (r apiRun) isTerminal() bool {
	return r.Status != "CREATING" && r.Status != "RUNNING"
}

// v0Conversation is GET /v0/agents/{id}/conversation: text-only messages for
// every run of the agent, in order.
type v0Conversation struct {
	ID       string      `json:"id"`
	Messages []v0Message `json:"messages"`
}

type v0Message struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

// agentRecord pairs a decoded agent with its native bytes.
type agentRecord struct {
	raw   json.RawMessage
	agent apiAgent
}

// runRecord pairs a decoded run with its native bytes.
type runRecord struct {
	raw json.RawMessage
	run apiRun
}

func decodeAgent(raw json.RawMessage) (agentRecord, error) {
	var agent apiAgent
	if err := json.Unmarshal(raw, &agent); err != nil {
		return agentRecord{}, fmt.Errorf("decode agent: %w", err)
	}
	if !validAgentID(agent.ID) {
		return agentRecord{}, fmt.Errorf("decode agent: unexpected id %q", agent.ID)
	}
	return agentRecord{raw: raw, agent: agent}, nil
}

func decodeRun(raw json.RawMessage) (runRecord, error) {
	var run apiRun
	if err := json.Unmarshal(raw, &run); err != nil {
		return runRecord{}, fmt.Errorf("decode run: %w", err)
	}
	if !validRunID(run.ID) {
		return runRecord{}, fmt.Errorf("decode run: unexpected id %q", run.ID)
	}
	return runRecord{raw: raw, run: run}, nil
}

// sseEvent is one SSE block as the mirror stores it: one JSON object per line
// of a run's events file.
type sseEvent struct {
	ID    string          `json:"id,omitempty"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// maxSSELineSize caps one SSE line, and so one block. A variable so tests can
// exercise the oversized path without allocating 64MB.
var maxSSELineSize = spi.MaxRecordLineSize

// readSSE parses an SSE byte stream, calling emit for each complete block in
// order. A block holding an oversized line is skipped with a warning rather
// than ending the stream. Comment lines (": keepalive") are ignored. emit
// returning false stops the read early (used once the done event arrives).
func readSSE(r io.Reader, source string, emit func(sseEvent) bool) error {
	reader := bufio.NewReader(r)
	var id, event string
	var data strings.Builder
	hasData, skipBlock := false, false
	lineNo := 0

	dispatch := func() bool {
		defer func() {
			id, event = "", ""
			data.Reset()
			hasData, skipBlock = false, false
		}()
		if skipBlock {
			slog.Warn("Skipping oversized SSE block", "source", source, "line", lineNo)
			return true
		}
		if event == "" && !hasData {
			return true
		}
		if event == "" {
			// The SSE default event type; never observed from this API.
			event = "message"
		}
		return emit(sseEvent{ID: id, Event: event, Data: sseData(data.String(), hasData)})
	}

	for {
		line, oversized, err := spi.ReadRecordLine(reader, maxSSELineSize)
		if len(line) > 0 || oversized {
			lineNo++
		}
		if oversized {
			skipBlock = true
		} else if len(line) > 0 || err == nil {
			text := strings.TrimRight(string(line), "\r\n")
			if text == "" {
				if !dispatch() {
					return nil
				}
			} else if !strings.HasPrefix(text, ":") {
				field, value, _ := strings.Cut(text, ":")
				value = strings.TrimPrefix(value, " ")
				switch field {
				case "id":
					id = value
				case "event":
					event = value
				case "data":
					// SSE joins every data line of a block into one payload, so
					// the cap applies to their total, not to each line.
					if data.Len()+1+len(value) > maxSSELineSize {
						skipBlock = true
					} else {
						if hasData {
							data.WriteString("\n")
						}
						data.WriteString(value)
						hasData = true
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				dispatch()
				return nil
			}
			return err
		}
	}
}

// sseData keeps a data payload as JSON. Every payload observed is a JSON
// object; anything else is preserved as a JSON string so no content is lost.
func sseData(text string, hasData bool) json.RawMessage {
	if !hasData {
		return nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text)
	}
	quoted, _ := json.Marshal(text)
	return quoted
}

// eventsAreWhole reports whether a run's recorded events end with the done
// event, which the stream sends only after the run's final result.
func eventsAreWhole(events []sseEvent) bool {
	return len(events) > 0 && events[len(events)-1].Event == "done"
}

// encodeEvents renders events as JSONL for the mirror. HTML escaping is off
// so the stored data keeps the stream's bytes (json.Marshal would rewrite
// "&&" in a shell command as "&&").
func encodeEvents(events []sseEvent) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, event := range events {
		// Encode terminates each record with the newline JSONL needs.
		if err := enc.Encode(event); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// readEventsFile reads a run's mirrored events. A missing file is no events.
// Malformed or oversized lines are skipped with a warning so one bad record
// costs one event, not the run.
func readEventsFile(path string) ([]sseEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var events []sseEvent
	reader := bufio.NewReader(f)
	for lineNo := 1; ; lineNo++ {
		line, oversized, err := spi.ReadRecordLine(reader, spi.MaxRecordLineSize)
		if oversized {
			slog.Warn("Skipping oversized JSONL line", "file", path, "line", lineNo)
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var event sseEvent
			if jsonErr := json.Unmarshal(trimmed, &event); jsonErr != nil || event.Event == "" {
				slog.Warn("Skipping corrupted JSONL line", "file", path, "line", lineNo, "error", jsonErr)
			} else {
				events = append(events, event)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return events, nil
			}
			return events, err
		}
	}
}

// eventTime converts an SSE id ("<epochMillis>-<seq>") to an RFC 3339 UTC
// timestamp. The id is the only timestamp the stream carries. Returns "" when
// the id is absent (status events carry none) or malformed.
func eventTime(id string) string {
	t, ok := eventInstant(id)
	if !ok {
		return ""
	}
	return spi.RFC3339Millis(t)
}

// eventInstant parses the epoch-millisecond prefix of an SSE id.
func eventInstant(id string) (time.Time, bool) {
	millis, _, _ := strings.Cut(id, "-")
	ms, err := strconv.ParseInt(millis, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms).UTC(), true
}

// validAgentID and validRunID guard ids before they become mirror path
// components; ids come from the network and are untrusted. Observed shapes are
// bc-<uuid> and run-<uuid>.
func validAgentID(id string) bool { return validPrefixedID(id, "bc-") }
func validRunID(id string) bool   { return validPrefixedID(id, "run-") }

func validPrefixedID(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" || len(rest) > 64 {
		return false
	}
	for _, r := range rest {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex && r != '-' {
			return false
		}
	}
	return true
}
