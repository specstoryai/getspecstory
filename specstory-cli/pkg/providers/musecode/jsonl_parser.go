package musecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	mb                    = 1024 * 1024
	maxReasonableLineSize = 250 * mb // 250MB sanity limit to prevent OOM from malformed or malicious files
)

// Payload types carried by the record envelope. Everything not listed here is
// telemetry, subagent control or cron/reminder machinery and is skipped.
const (
	payloadTypeMetadata = "runtime.session.metadata"
	payloadTypeSession  = "runtime.session"
)

// Run event kinds that carry conversation content. The runtime emits many more
// (diagnostics, traces, reminder bookkeeping); those are skipped.
const (
	eventStarted                     = "started"
	eventReasoningCommitted          = "reasoning_committed"
	eventAssistantMessageCommitted   = "assistant_message_committed"
	eventAssistantToolCallsCommitted = "assistant_tool_calls_committed"
	eventToolResultBatchCommitted    = "tool_result_batch_committed"
	eventModelCompleted              = "model_completed"
	eventTerminal                    = "terminal"
)

// MuseRecord is the envelope wrapping every line of a Muse Code transcript.
// The transcript is an append-only event log: the envelope is uniform and the
// payload is decoded per PayloadType.
//
// Stream is the critical field. Records belonging to the session itself carry
// the session's own id; subagent task-stream runs are recorded in the SAME
// file under a different stream id. Without filtering on it, every subagent
// objective renders as a fake user turn.
type MuseRecord struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Stream        MuseStream      `json:"stream"`
	Sequence      int64           `json:"sequence"`
	RecordedAt    int64           `json:"recorded_at"` // microseconds since the Unix epoch
	RecordType    string          `json:"record_type"`
	PayloadType   string          `json:"payload_type"`
	Payload       json.RawMessage `json:"payload"`
}

// MuseStream identifies the event stream a record belongs to ("session" for the
// session itself, "task" for subagent execution streams).
type MuseStream struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// museMetadataPayload is the payload of a runtime.session.metadata record.
type museMetadataPayload struct {
	Kind   string `json:"kind"`
	Record struct {
		WorkspaceRoot string `json:"workspace_root"`
		ProviderID    string `json:"provider_id"`
		ModelID       string `json:"model_id"`
		Build         struct {
			SHA    string `json:"sha"`
			Semver string `json:"semver"`
		} `json:"build"`
		ToolSurfaceVersion string `json:"tool_surface_version"`
	} `json:"record"`
}

// museRunPayload is the payload of a runtime.session record. Kind is "run"
// (model turns) or "task" (execution bookkeeping, skipped).
type museRunPayload struct {
	Kind  string          `json:"kind"`
	RunID string          `json:"run_id"`
	Event json.RawMessage `json:"event"`
}

// MuseToolCall is one tool invocation requested by the model. Args is a JSON
// *string*, not an object, so callers must unmarshal it themselves.
type MuseToolCall struct {
	ID     string `json:"id"`
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Args   string `json:"args"`
}

// MuseToolResult is one tool outcome, matched to its call by ToolCallID. Text
// is a plain string; for bash it is a JSON object string, and failures start
// with "tool failed: ".
type MuseToolResult struct {
	ToolCallIndex int    `json:"tool_call_index"`
	ToolCallID    string `json:"tool_call_id"`
	Text          string `json:"text"`
}

// MuseUsage is the token accounting reported by one model step.
type MuseUsage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
}

// museEvent is the union of the run event shapes that carry conversation
// content. Only the fields for the decoded Kind are populated.
type museEvent struct {
	Kind string `json:"kind"`

	// started
	Prompt string `json:"prompt"`

	// reasoning_committed / assistant_message_committed
	MessageID string `json:"message_id"`
	Text      string `json:"text"`

	// assistant_tool_calls_committed
	ToolCalls []MuseToolCall `json:"tool_calls"`

	// tool_result_batch_committed
	BatchID string           `json:"batch_id"`
	Results []MuseToolResult `json:"results"`

	// model_completed
	Usage        MuseUsage `json:"usage"`
	DurationMs   int64     `json:"duration_ms"`
	FinishReason string    `json:"finish_reason"`
	Model        string    `json:"model"`

	// terminal
	Terminal        string `json:"terminal"`
	TurnDurationMs  int64  `json:"turn_duration_ms"`
	TimeToFirstToke int64  `json:"time_to_first_token_ms"`
}

// MuseConversationEvent is one conversation-relevant run event in file order,
// with the envelope fields the conversion needs already resolved.
type MuseConversationEvent struct {
	RecordID  string
	RunID     string
	Timestamp string // ISO 8601, converted from the envelope's microsecond clock
	Event     museEvent
}

// Kind returns the run event kind (started, model_completed, ...).
func (e *MuseConversationEvent) Kind() string {
	return e.Event.Kind
}

// MuseSession is one parsed Muse Code transcript. Muse stores each session as
// a directory containing session.jsonl, so the session id is the directory
// name; the records' stream id says the same thing and is what the subagent
// filter matches against.
type MuseSession struct {
	ID       string
	FilePath string

	// From the runtime.session.metadata record (first non-empty value wins:
	// the runtime writes metadata more than once and later copies may blank
	// out fields such as model_id).
	WorkspaceRoot string
	ProviderID    string
	ModelID       string
	Version       string

	StartTime   string // ISO 8601 of the first record kept
	LastUpdated string // ISO 8601 of the last record kept

	Events []MuseConversationEvent
}

// museTimeToISO converts Muse's microsecond epoch clock to an ISO 8601 UTC
// timestamp with millisecond precision, the form every other provider emits.
func museTimeToISO(microseconds int64) string {
	if microseconds <= 0 {
		return ""
	}
	return time.UnixMicro(microseconds).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// ParseSessionFile parses a single Muse Code session.jsonl transcript. Records
// are kept in file order: the log is append-only and append order is the true
// conversation order (`muse resume` appends more runs to the same file).
//
// Records whose stream id differs from the session's own are dropped. The
// session id is established by the first record carrying a stream id (the
// metadata record written at session open) and falls back to the directory
// name, which is the session id in Muse's store layout.
func ParseSessionFile(filePath string) (*MuseSession, error) {
	slog.Debug("ParseSessionFile: Reading Muse session file", "path", filePath)

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only file; close errors not actionable

	// Use bufio.Reader instead of Scanner to handle arbitrarily large lines
	reader := bufio.NewReader(file)

	session := &MuseSession{FilePath: filePath}
	lineNumber := 0
	foreignStreamRecords := 0

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")

		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("error reading line %d: %w", lineNumber+1, err)
		}

		atEOF := err == io.EOF
		hasContent := len(strings.TrimSpace(line)) > 0

		if hasContent || !atEOF {
			lineNumber++
		}

		if !hasContent {
			if atEOF {
				break
			}
			continue
		}

		if len(line) > maxReasonableLineSize {
			return nil, fmt.Errorf("line %d exceeds reasonable size limit (%d MB): refusing to process potentially malformed file",
				lineNumber, maxReasonableLineSize/mb)
		}

		var record MuseRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// Log and skip corrupted lines rather than failing the entire parse:
			// the file may be mid-write when the watcher fires.
			slog.Warn("ParseSessionFile: Skipping corrupted JSONL line",
				"file", filepath.Base(filePath),
				"line", lineNumber,
				"error", err)
			if atEOF {
				break
			}
			continue
		}

		if session.ID == "" && record.Stream.ID != "" {
			session.ID = record.Stream.ID
		}
		if record.Stream.ID != session.ID {
			foreignStreamRecords++
			if atEOF {
				break
			}
			continue
		}

		accumulateRecord(session, &record)

		if atEOF {
			break
		}
	}

	if session.ID == "" {
		session.ID = sessionIDFromPath(filePath)
	}

	slog.Debug("ParseSessionFile: Parsed Muse session",
		"path", filePath,
		"sessionId", session.ID,
		"eventCount", len(session.Events),
		"foreignStreamRecords", foreignStreamRecords)

	return session, nil
}

// sessionIDFromPath derives the session id from the store layout
// (<session-id>/session.jsonl), falling back to the filename stem for
// transcripts kept outside that layout.
func sessionIDFromPath(filePath string) string {
	dir := filepath.Base(filepath.Dir(filePath))
	if dir != "" && dir != "." && dir != string(filepath.Separator) {
		return dir
	}
	return strings.TrimSuffix(filepath.Base(filePath), ".jsonl")
}

// accumulateRecord folds one in-stream record into the session: envelope
// timestamps always, metadata and conversation events by payload type.
func accumulateRecord(session *MuseSession, record *MuseRecord) {
	timestamp := museTimeToISO(record.RecordedAt)
	if timestamp != "" {
		if session.StartTime == "" {
			session.StartTime = timestamp
		}
		if timestamp > session.LastUpdated {
			session.LastUpdated = timestamp
		}
	}

	switch record.PayloadType {
	case payloadTypeMetadata:
		accumulateMetadata(session, record)
	case payloadTypeSession:
		accumulateRunEvent(session, record, timestamp)
	}
}

// accumulateMetadata folds a runtime.session.metadata record into the session.
// Fields are first-write-wins because the runtime emits metadata more than once
// per session and the later copies can carry blanked-out values.
func accumulateMetadata(session *MuseSession, record *MuseRecord) {
	var payload museMetadataPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		slog.Warn("accumulateMetadata: Skipping unreadable metadata payload",
			"file", filepath.Base(session.FilePath), "error", err)
		return
	}

	if session.WorkspaceRoot == "" {
		session.WorkspaceRoot = payload.Record.WorkspaceRoot
	}
	if session.ProviderID == "" {
		session.ProviderID = payload.Record.ProviderID
	}
	if session.ModelID == "" {
		session.ModelID = payload.Record.ModelID
	}
	if session.Version == "" {
		session.Version = payload.Record.Build.Semver
	}
}

// accumulateRunEvent appends a conversation-carrying run event. Task-kind
// payloads are execution bookkeeping ("opening meta model stream") and every
// other event kind is diagnostics, so both are dropped here rather than in the
// conversion.
func accumulateRunEvent(session *MuseSession, record *MuseRecord, timestamp string) {
	var payload museRunPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		slog.Warn("accumulateRunEvent: Skipping unreadable run payload",
			"file", filepath.Base(session.FilePath), "error", err)
		return
	}
	if payload.Kind != "run" {
		return
	}

	var event museEvent
	if err := json.Unmarshal(payload.Event, &event); err != nil {
		slog.Warn("accumulateRunEvent: Skipping unreadable run event",
			"file", filepath.Base(session.FilePath), "runId", payload.RunID, "error", err)
		return
	}

	switch event.Kind {
	case eventStarted, eventReasoningCommitted, eventAssistantMessageCommitted,
		eventAssistantToolCallsCommitted, eventToolResultBatchCommitted,
		eventModelCompleted, eventTerminal:
		session.Events = append(session.Events, MuseConversationEvent{
			RecordID:  record.ID,
			RunID:     payload.RunID,
			Timestamp: timestamp,
			Event:     event,
		})
	}
}

// FirstUserPrompt returns the prompt that opened the first run, used for slugs
// and readable names.
func (s *MuseSession) FirstUserPrompt() string {
	for i := range s.Events {
		event := &s.Events[i]
		if event.Kind() != eventStarted {
			continue
		}
		if prompt := strings.TrimSpace(event.Event.Prompt); prompt != "" {
			return prompt
		}
	}
	return ""
}
