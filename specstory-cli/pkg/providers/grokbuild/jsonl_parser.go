package grokbuild

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const (
	chatHistoryFile = "chat_history.jsonl"
	updatesFile     = "updates.jsonl"
	eventsFile      = "events.jsonl"
	summaryFile     = "summary.json"
	subagentsDir    = "subagents"
)

// userQueryRe captures the human's actual prompt. Grok wraps every real user turn
// in <user_query> tags; user records without the tags are injected context
// (<user_info>, <git_status>, <rules>, and <system-reminder> blocks) and are not
// conversation. One of those injected records is a ~29KB skills dump.
var userQueryRe = regexp.MustCompile(`(?s)^\s*<user_query>(.*)</user_query>\s*$`)

// GrokRecord is one line of chat_history.jsonl. The discriminator is Type; there
// is no role field, and no record carries a timestamp.
//
// Content is deliberately json.RawMessage because its shape varies by record
// type: user records hold an array of parts, while system, assistant and
// tool_result records hold a plain string.
type GrokRecord struct {
	// Raw preserves unknown native fields from the same accepted parsing snapshot.
	Raw     json.RawMessage `json:"-"`
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content,omitempty"`

	// user
	SyntheticReason *string `json:"synthetic_reason,omitempty"`
	PromptIndex     *int    `json:"prompt_index,omitempty"`

	// assistant
	ModelID          string         `json:"model_id,omitempty"`
	ModelFingerprint string         `json:"model_fingerprint,omitempty"`
	ReasoningEffort  string         `json:"reasoning_effort,omitempty"`
	ToolCalls        []GrokToolCall `json:"tool_calls,omitempty"`

	// tool_result
	ToolCallID string `json:"tool_call_id,omitempty"`

	// reasoning
	ID               string            `json:"id,omitempty"`
	Summary          []GrokSummaryPart `json:"summary,omitempty"`
	EncryptedContent string            `json:"encrypted_content,omitempty"`
	Status           string            `json:"status,omitempty"`

	// backend_tool_call
	Kind *GrokBackendKind `json:"kind,omitempty"`
}

// GrokToolCall is one tool invocation. Arguments is a JSON string, not an object,
// so it needs a second decode before the parameters can be read.
type GrokToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// GrokSummaryPart is one human-readable chunk of a reasoning record. The sibling
// encrypted_content field is opaque and must never be rendered.
type GrokSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// GrokBackendKind describes a server-side tool call. Web and X tools never appear
// in tool_calls; they arrive as backend_tool_call records instead.
type GrokBackendKind struct {
	ToolType string          `json:"tool_type"`        // web_search or x_search
	Action   *GrokBackendAct `json:"action,omitempty"` // web_search detail
	Name     string          `json:"name,omitempty"`   // x_search tool name
	Input    string          `json:"input,omitempty"`  // x_search args, JSON string
	CallID   string          `json:"call_id,omitempty"`
	ID       string          `json:"id,omitempty"`
	Status   string          `json:"status,omitempty"`
}

// GrokBackendAct is the web_search action detail. Sources carries the pages the
// search actually returned, which is the only record of a web search's results.
type GrokBackendAct struct {
	Type    string              `json:"type"` // search, open_page, find_in_page
	Query   string              `json:"query,omitempty"`
	URL     string              `json:"url,omitempty"`
	Pattern string              `json:"pattern,omitempty"`
	Sources []GrokBackendSource `json:"sources,omitempty"`
}

// GrokBackendSource is one page a web search returned.
type GrokBackendSource struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// GrokSummary is summary.json: the session's identity and metadata.
type GrokSummary struct {
	Raw  json.RawMessage `json:"-"`
	Info struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"info"`
	SessionSummary    string `json:"session_summary"`
	GeneratedTitle    string `json:"generated_title"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	CurrentModelID    string `json:"current_model_id"`
	NumMessages       int    `json:"num_messages"`
	NumChatMessages   int    `json:"num_chat_messages"`
	SessionKind       string `json:"session_kind"`
	AgentName         string `json:"agent_name"`
	HeadBranch        string `json:"head_branch"`
	ChatFormatVersion int    `json:"chat_format_version"`
}

// GrokUsage is the token accounting reported when a turn completes.
type GrokUsage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CachedReadTokens int `json:"cachedReadTokens"`
	ReasoningTokens  int `json:"reasoningTokens"`
}

// sessionIndex holds everything the sidecar files contribute: timing, Grok's own
// tool classification, tool outcomes, and per-turn usage. chat_history.jsonl has
// none of this.
type sessionIndex struct {
	rawUpdates       []json.RawMessage
	rawEvents        []json.RawMessage
	userTimes        []string              // first timestamp of each prompt in arrival order
	unindexedPrompts map[string]bool       // chunk deduplication when promptIndex is absent
	toolTime         map[string]string     // tool_call_id -> ISO 8601
	toolKind         map[string]string     // tool_call_id -> Grok's own tool kind
	toolError        map[string]bool       // tool_call_id -> the call failed
	toolStatus       map[string]string     // tool_call_id -> recorded completion or pending status
	userTime         map[int]string        // prompt index -> ISO 8601
	agentTime        []string              // assistant text, in order of appearance
	agentPrompt      []string              // prompt id per assistant text, same order
	thoughtTime      []string              // reasoning, in order of appearance
	usage            map[string]*GrokUsage // token totals by prompt id
}

// GrokSession is one parsed session directory.
type GrokSession struct {
	RawSummary json.RawMessage
	ID         string
	Dir        string
	Cwd        string
	CreatedAt  string
	UpdatedAt  string
	Title      string
	Model      string
	Kind       string // "subagent" marks a spawned subagent session
	Records    []GrokRecord
	Index      *sessionIndex
	Subagents  map[string]*GrokSubagentMeta // by subagent id
}

// GrokSubagentMeta is subagents/<id>/meta.json under the parent session. It
// enriches the spawn_subagent rendering with what the subagent was asked to do.
type GrokSubagentMeta struct {
	Raw          json.RawMessage `json:"-"`
	SubagentID   string          `json:"subagent_id"`
	ChildID      string          `json:"child_session_id"`
	SubagentType string          `json:"subagent_type"`
	Description  string          `json:"description"`
	Prompt       string          `json:"prompt"`
	Status       string          `json:"status"`
	DurationMs   int             `json:"duration_ms"`
	ToolCalls    int             `json:"tool_calls"`
	Turns        int             `json:"turns"`
}

// IsSubagent reports whether this session is a spawned subagent rather than a
// conversation a human started. Subagent sessions sit at the top level next to
// real ones, so they would otherwise sync as separate sessions.
func (s *GrokSession) IsSubagent() bool {
	return s.Kind == "subagent"
}

// ParseSessionDir reads one session directory: the transcript plus the sidecars
// that carry timing, tool outcomes, and metadata.
func ParseSessionDir(dir string) (*GrokSession, error) {
	return parseSessionDir(dir, false)
}

// Metadata scans stop at the first real prompt and never open sidecars.
func parseSessionDir(dir string, metadataOnly bool) (*GrokSession, error) {
	// Grok writes regular transcripts. Following a link here could associate
	// another project's conversation with this directory's summary; opening a
	// special file could also block discovery indefinitely.
	transcript := filepath.Join(dir, chatHistoryFile)
	info, err := os.Lstat(transcript)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect Grok transcript: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("grok transcript %q is not a regular file", transcript)
	}
	session := &GrokSession{
		Dir:       dir,
		ID:        filepath.Base(dir),
		Index:     newSessionIndex(),
		Subagents: map[string]*GrokSubagentMeta{},
	}

	// The native directory names the session. Copied or stale summary metadata
	// must not re-key lookup, index, debug, or cloud output to another UUID.
	summary, err := readSummary(filepath.Join(dir, summaryFile))
	if err != nil {
		return nil, err
	}
	if summary != nil {
		session.RawSummary = summary.Raw
		if summary.Info.ID != "" && summary.Info.ID != session.ID {
			slog.Warn("Grok summary ID disagrees with directory identity; using directory identity", "dir", dir)
		}
		session.Cwd = summary.Info.Cwd
		session.CreatedAt = normalizeTime(summary.CreatedAt)
		session.UpdatedAt = normalizeTime(summary.UpdatedAt)
		session.Model = summary.CurrentModelID
		session.Kind = summary.SessionKind
		session.Title = summary.GeneratedTitle
		if session.Title == "" {
			session.Title = summary.SessionSummary
		}
	}
	// Session IDs also name debug-output directories.
	if !uuidLike.MatchString(session.ID) {
		return nil, fmt.Errorf("grok session directory %q has no valid UUID identity", dir)
	}

	if metadataOnly {
		if session.IsSubagent() {
			return session, nil
		}
		err = visitJSONLines(filepath.Join(dir, chatHistoryFile), spi.MaxRecordLineSize, func(raw []byte) bool {
			var record GrokRecord
			if json.Unmarshal(raw, &record) != nil {
				return true
			}
			if _, ok := record.UserQuery(); ok {
				session.Records = []GrokRecord{record}
				return false
			}
			return true
		})
	} else {
		session.Records, err = readChatHistory(filepath.Join(dir, chatHistoryFile))
		session.Index = buildSessionIndex(dir)
		session.Subagents = readSubagentMeta(filepath.Join(dir, subagentsDir))
	}
	if err != nil {
		return nil, err
	}

	// summary.json is where the times live, so a missing or corrupt one would
	// otherwise leave createdAt empty, which the schema rejects. The transcript's
	// own mtime is a truthful stand-in.
	if session.CreatedAt == "" || session.UpdatedAt == "" {
		if info, err := os.Stat(filepath.Join(dir, chatHistoryFile)); err == nil {
			stamp := info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
			if session.CreatedAt == "" {
				session.CreatedAt = stamp
			}
			if session.UpdatedAt == "" {
				session.UpdatedAt = stamp
			}
		}
	}

	slog.Debug("ParseSessionDir: parsed Grok session",
		"sessionID", session.ID, "records", len(session.Records), "subagentMeta", len(session.Subagents))

	return session, nil
}

func readSummary(path string) (*GrokSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", summaryFile, err)
	}
	var summary GrokSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		// A half-written summary should not sink the whole session.
		slog.Warn("readSummary: failed to parse summary.json", "path", path, "error", err)
		return nil, nil
	}
	summary.Raw = append(json.RawMessage(nil), data...)
	return &summary, nil
}

// readChatHistory parses the transcript, skipping lines that fail to decode. A
// live session can be mid-write when the watcher fires, so a corrupt trailing
// line is expected rather than fatal.
func readChatHistory(path string) ([]GrokRecord, error) {
	return readChatHistoryCapped(path, spi.MaxRecordLineSize)
}

// readChatHistoryCapped accepts records on either side of a corrupt or oversized line.
func readChatHistoryCapped(path string, maxLine int) ([]GrokRecord, error) {
	var records []GrokRecord
	err := visitJSONLines(path, maxLine, func(raw []byte) bool {
		var record GrokRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			slog.Warn("readChatHistory: skipping invalid record", "path", path, "error", err)
			return true
		}
		record.Raw = append(json.RawMessage(nil), raw...)
		records = append(records, record)
		return true
	})
	return records, err
}

// visitJSONLines bounds each allocation and can stop after a metadata query.
// Missing files are normal while a new session is being initialized.
func visitJSONLines(path string, maxLine int, fn func([]byte) bool) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	for number := 1; ; number++ {
		raw, oversized, readErr := spi.ReadRecordLine(reader, maxLine)
		if oversized {
			slog.Warn("Skipping oversized Grok record", "path", path, "line", number, "limit", maxLine)
		} else if len(strings.TrimSpace(string(raw))) > 0 {
			if !json.Valid(raw) {
				slog.Warn("Skipping malformed Grok record", "path", path, "line", number)
			} else if !fn(raw) {
				return nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read %s line %d: %w", path, number, readErr)
		}
	}
}

func newSessionIndex() *sessionIndex {
	return &sessionIndex{
		toolTime:         map[string]string{},
		unindexedPrompts: map[string]bool{},
		toolKind:         map[string]string{},
		toolError:        map[string]bool{},
		toolStatus:       map[string]string{},
		userTime:         map[int]string{},
		usage:            map[string]*GrokUsage{},
	}
}

// buildSessionIndex reads updates.jsonl and events.jsonl. updates.jsonl is the
// Agent Client Protocol event stream and the only source of per-message timing,
// Grok's own tool classification, and token usage. events.jsonl is the only
// place a failed tool call is marked as failed.
func buildSessionIndex(dir string) *sessionIndex {
	idx := newSessionIndex()

	forEachJSONLine(filepath.Join(dir, updatesFile), func(raw []byte) {
		idx.rawUpdates = append(idx.rawUpdates, append(json.RawMessage(nil), raw...))
		var envelope struct {
			Timestamp int64 `json:"timestamp"`
			Params    struct {
				Update map[string]any `json:"update"`
				Meta   struct {
					AgentTimestampMs int64  `json:"agentTimestampMs"`
					PromptID         string `json:"promptId"`
				} `json:"_meta"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return
		}
		update := envelope.Params.Update
		if update == nil {
			return
		}

		ts := isoFromMillis(envelope.Params.Meta.AgentTimestampMs, envelope.Timestamp)
		kind, _ := update["sessionUpdate"].(string)

		switch kind {
		case "tool_call":
			id, _ := update["toolCallId"].(string)
			if id == "" {
				return
			}
			if _, seen := idx.toolTime[id]; !seen && ts != "" {
				idx.toolTime[id] = ts
			}
			if meta, ok := update["_meta"].(map[string]any); ok {
				if tool, ok := meta["x.ai/tool"].(map[string]any); ok {
					if k, ok := tool["kind"].(string); ok && k != "" {
						idx.toolKind[id] = k
					}
				}
			}
		case "tool_call_update":
			id, _ := update["toolCallId"].(string)
			status, _ := update["status"].(string)
			if id != "" && status != "" {
				switch status {
				case "failed":
					idx.toolStatus[id] = "error"
				case "completed":
					idx.toolStatus[id] = "success"
				default:
					idx.toolStatus[id] = status
				}
			}
		case "user_message_chunk":
			// A prompt can arrive as several chunks; the first one is its start.
			if ts == "" {
				return
			}
			if meta, ok := update["_meta"].(map[string]any); ok {
				if v, ok := meta["promptIndex"].(float64); ok && v >= 0 && v == float64(int(v)) {
					index := int(v)
					if _, seen := idx.userTime[index]; !seen {
						idx.userTime[index] = ts
						idx.userTimes = append(idx.userTimes, ts)
					}
					return
				}
			}
			// Missing indices must never overwrite valid index zero. A prompt id can
			// still identify repeated chunks; without either key preserve arrival order.
			id := envelope.Params.Meta.PromptID
			if id != "" && idx.unindexedPrompts[id] {
				return
			}
			if id != "" {
				idx.unindexedPrompts[id] = true
			}
			idx.userTimes = append(idx.userTimes, ts)
		case "agent_message_chunk":
			if ts != "" {
				idx.agentTime = append(idx.agentTime, ts)
				// The prompt id ties this message to the turn whose token totals
				// arrive later, in turn_completed. It sits on the envelope's own
				// _meta, next to agentTimestampMs, not on the update's.
				idx.agentPrompt = append(idx.agentPrompt, envelope.Params.Meta.PromptID)
			}
		case "agent_thought_chunk":
			// Reasoning is timed separately from assistant text; sharing one
			// counter would give every thought the time of the message after it.
			if ts != "" {
				idx.thoughtTime = append(idx.thoughtTime, ts)
			}
		case "turn_completed":
			usage, ok := update["usage"].(map[string]any)
			if !ok {
				return
			}
			// Key by prompt id rather than by position: a session can complete
			// more turns than it has user prompts, so counting would attribute
			// one turn's tokens to another turn's conversation.
			//
			// Every turn_completed in the real store carries the id, but a turn
			// without one must be dropped rather than stored under an empty key:
			// a second such turn would overwrite the first, and any exchange
			// whose own prompt id is unknown would then match it by accident.
			promptID, _ := update["prompt_id"].(string)
			if promptID == "" {
				return
			}
			idx.usage[promptID] = &GrokUsage{
				InputTokens:      intFrom(usage["inputTokens"]),
				OutputTokens:     intFrom(usage["outputTokens"]),
				CachedReadTokens: intFrom(usage["cachedReadTokens"]),
				ReasoningTokens:  intFrom(usage["reasoningTokens"]),
			}
		}
	})

	forEachJSONLine(filepath.Join(dir, eventsFile), func(raw []byte) {
		idx.rawEvents = append(idx.rawEvents, append(json.RawMessage(nil), raw...))
		var event struct {
			TS         string `json:"ts"`
			Type       string `json:"type"`
			ToolCallID string `json:"tool_call_id"`
			Outcome    string `json:"outcome"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return
		}
		if event.ToolCallID == "" {
			return
		}
		switch event.Type {
		case "tool_completed", "mcp_tool_call_completed":
			if event.Outcome != "" {
				idx.toolStatus[event.ToolCallID] = event.Outcome
			}
			if event.Outcome == "error" {
				idx.toolError[event.ToolCallID] = true
			}
			if _, seen := idx.toolTime[event.ToolCallID]; !seen && event.TS != "" {
				idx.toolTime[event.ToolCallID] = normalizeTime(event.TS)
			}
		}
	})

	return idx
}

// forEachJSONLine streams a JSONL file, handing each non-empty line to fn.
// A missing file is not an error: every sidecar is optional.
func forEachJSONLine(path string, fn func(raw []byte)) {
	forEachJSONLineCapped(path, spi.MaxRecordLineSize, fn)
}

// forEachJSONLineCapped keeps valid sidecar records after oversized lines.
func forEachJSONLineCapped(path string, maxLine int, fn func(raw []byte)) {
	if err := visitJSONLines(path, maxLine, func(raw []byte) bool { fn(raw); return true }); err != nil {
		slog.Warn("Failed to read Grok sidecar", "path", path, "error", err)
	}
}

// readSubagentMeta loads the per-subagent meta.json files under a session.
func readSubagentMeta(dir string) map[string]*GrokSubagentMeta {
	result := map[string]*GrokSubagentMeta{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "meta.json"))
		if err != nil {
			continue
		}
		var meta GrokSubagentMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		meta.Raw = append(json.RawMessage(nil), data...)
		id := meta.SubagentID
		if id == "" {
			id = entry.Name()
		}
		result[id] = &meta
	}
	return result
}

// FindSessions parses every session directory in a project group, skipping
// subagent sessions. Returns them most recently updated first.
func FindSessions(groupDir string) ([]*GrokSession, error) {
	return findSessions(groupDir, false)
}

func findSessions(groupDir string, metadataOnly bool) ([]*GrokSession, error) {
	entries, err := os.ReadDir(groupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*GrokSession{}, nil
		}
		return nil, fmt.Errorf("failed to read Grok project directory: %w", err)
	}

	var sessions []*GrokSession
	for _, entry := range entries {
		if !entry.IsDir() || !uuidLike.MatchString(entry.Name()) {
			continue
		}
		session, err := parseSessionDir(filepath.Join(groupDir, entry.Name()), metadataOnly)
		if err != nil {
			slog.Warn("FindSessions: failed to parse session, skipping", "dir", entry.Name(), "error", err)
			continue
		}
		if session.IsSubagent() {
			slog.Debug("FindSessions: skipping subagent session", "sessionID", session.ID)
			continue
		}
		if len(session.Records) == 0 {
			continue
		}
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})

	return sessions, nil
}

// UserQuery returns the human's prompt from a user record, and whether the
// record is a real user turn at all.
func (r *GrokRecord) UserQuery() (string, bool) {
	if r.Type != "user" || r.SyntheticReason != nil {
		return "", false
	}
	text := r.TextContent()
	match := userQueryRe.FindStringSubmatch(text)
	if match == nil {
		return "", false
	}
	query := strings.TrimSpace(match[1])
	if query == "" {
		return "", false
	}
	return query, true
}

// TextContent flattens a record's content to plain text, whichever shape it uses.
func (r *GrokRecord) TextContent() string {
	if len(r.Content) == 0 {
		return ""
	}

	// system, assistant and tool_result hold a plain string.
	var s string
	if err := json.Unmarshal(r.Content, &s); err == nil {
		return s
	}

	if r.Type == "tool_result" {
		var value any
		if json.Unmarshal(r.Content, &value) == nil {
			encoded, _ := json.MarshalIndent(value, "", "  ")
			return string(encoded)
		}
	}

	// user holds an array of typed parts.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(r.Content, &parts); err == nil {
		var b strings.Builder
		for _, part := range parts {
			if part.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(part.Text)
		}
		return b.String()
	}

	return ""
}

// ThoughtContent joins the readable half of a reasoning record. The sibling
// encrypted_content is opaque and is never rendered.
func (r *GrokRecord) ThoughtContent() string {
	var b strings.Builder
	for _, part := range r.Summary {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

// Args decodes a tool call's arguments, which Grok stores as a JSON string.
func (t *GrokToolCall) Args() map[string]any {
	if strings.TrimSpace(t.Arguments) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(t.Arguments), &args); err != nil {
		slog.Warn("GrokToolCall.Args: preserving invalid arguments", "tool", t.Name, "error", err)
		return map[string]any{"arguments": t.Arguments}
	}
	return args
}

// FirstUserQuery returns the first real user prompt, used for slugs and names.
func (s *GrokSession) FirstUserQuery() string {
	for i := range s.Records {
		if query, ok := s.Records[i].UserQuery(); ok {
			return query
		}
	}
	return ""
}

// isoFromMillis prefers the millisecond stamp and falls back to whole seconds.
func isoFromMillis(millis, seconds int64) string {
	switch {
	case millis > 0:
		return time.UnixMilli(millis).UTC().Format("2006-01-02T15:04:05.000Z")
	case seconds > 0:
		return time.Unix(seconds, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	default:
		return ""
	}
}

// normalizeTime rewrites Grok's microsecond timestamps into the millisecond
// ISO 8601 form the schema uses. Unparseable values pass through untouched.
func normalizeTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("2006-01-02T15:04:05.000Z")
}

func intFrom(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}
