package piagent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// entryType constants for pi JSONL v3 session entries.
const (
	entrySession             = "session"
	entryMessage             = "message"
	entryModelChange         = "model_change"
	entryThinkingLevelChange = "thinking_level_change"
	entryCompaction          = "compaction"
	entryBranchSummary       = "branch_summary"
	entryCustom              = "custom"
	entryCustomMessage       = "custom_message"
	entryLabel               = "label"
	entrySessionInfo         = "session_info"
)

// messageRole constants for pi message entries.
const (
	roleUser          = "user"
	roleAssistant     = "assistant"
	roleToolResult    = "toolResult"
	roleBashExecution = "bashExecution"
	roleCustom        = "custom"
	roleBranchSummary = "branchSummary"
	roleCompaction    = "compactionSummary"
)

// rawEntry is the minimal envelope every pi entry shares. The message payload
// (for type=="message") is kept as raw json.RawMessage so we can decode it per
// role without fighting a single union struct. Compaction entries carry
// firstKeptEntryId as a top-level field (no message wrapper).
type rawEntry struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	ParentID         *string         `json:"parentId"` // null for the first entry
	Timestamp        string          `json:"timestamp"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"` // compaction entries only
	Message          json.RawMessage `json:"message,omitempty"`
}

// sessionHeader is the first line of a pi session file (no id/parentId).
type sessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

// contentBlock is one element of an assistant or user message content array.
type contentBlock struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Thinking string         `json:"thinking,omitempty"`
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Args     map[string]any `json:"arguments,omitempty"`
}

// userMessage is a pi user-role message: content is string OR []contentBlock.
type userMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
}

// assistantMessage is a pi assistant-role message.
type assistantMessage struct {
	Role         string         `json:"role"`
	Content      []contentBlock `json:"content"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	API          string         `json:"api"`
	StopReason   string         `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Usage        *piUsage       `json:"usage,omitempty"`
}

// piUsage is the token-usage shape on a pi assistant message.
type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
}

// toolResultMessage is a pi toolResult-role message (a top-level entry).
type toolResultMessage struct {
	Role       string          `json:"role"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Content    []contentBlock  `json:"content"`
	Details    json.RawMessage `json:"details,omitempty"` // tool-specific metadata (e.g. exitCode)
	IsError    bool            `json:"isError"`
}

// ParseSession reads a pi JSONL v3 session file and maps its current leaf-path
// branch into the unified schema.SessionData. It honors compaction entries:
// when a compaction entry is on the leaf path, entries before firstKeptEntryId
// are dropped from the conversation (matching pi's buildContextEntries).
func ParseSession(path string) (*schema.SessionData, error) {
	header, entries, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, fmt.Errorf("pi: no session header in %s", path)
	}
	if header.Type != entrySession {
		return nil, fmt.Errorf("pi: %s is not a pi session (header type %q)", path, header.Type)
	}
	if header.ID == "" {
		return nil, fmt.Errorf("pi: session header in %s has no id", path)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("pi: session %s has no entries", header.ID)
	}
	ordered := leafPathEntries(entries)
	return buildSessionData(header, ordered), nil
}

// piProviderVersion derives the provider version string recorded on SessionData
// from the pi session header's format version (e.g. v3). Always non-empty so
// schema.Validate() does not warn about a missing provider.version.
func piProviderVersion(header *sessionHeader) string {
	if header.Version > 0 {
		return fmt.Sprintf("v%d", header.Version)
	}
	return "v1"
}

// buildSessionData maps the ordered leaf-path entries into schema.SessionData.
func buildSessionData(header *sessionHeader, ordered []rawEntry) *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider: schema.ProviderInfo{
			ID:      providerID,
			Name:    providerName,
			Version: piProviderVersion(header),
		},
		SessionID:     header.ID,
		CreatedAt:     header.Timestamp,
		WorkspaceRoot: header.Cwd,
		Exchanges:     buildExchanges(ordered),
	}
}

// buildExchanges groups ordered entries into schema exchanges. A new user
// message starts a new exchange; assistant messages append to the current
// exchange; toolResults merge into the matching ToolInfo. Control entries
// (model_change, custom, etc.) are skipped from the conversation body.
func buildExchanges(ordered []rawEntry) []schema.Exchange {
	var exchanges []schema.Exchange
	var current *schema.Exchange
	commit := func() {
		if current != nil && len(current.Messages) > 0 {
			exchanges = append(exchanges, *current)
		}
	}
	for _, e := range ordered {
		if e.Type != entryMessage {
			continue
		}
		switch messageRole(e) {
		case roleUser:
			commit()
			current = &schema.Exchange{
				ExchangeID: e.ID,
				StartTime:  e.Timestamp,
				EndTime:    e.Timestamp,
				Messages:   []schema.Message{buildUserMessage(e)},
			}
		case roleAssistant:
			current = appendAssistant(current, e)
		case roleToolResult:
			mergeToolResult(current, e)
		}
	}
	commit()
	return exchanges
}

// appendAssistant appends an assistant message to the current exchange, creating
// one if none exists yet.
func appendAssistant(current *schema.Exchange, e rawEntry) *schema.Exchange {
	if current == nil {
		current = &schema.Exchange{ExchangeID: e.ID, StartTime: e.Timestamp}
	}
	current.Messages = append(current.Messages, buildAgentMessages(e)...)
	current.EndTime = e.Timestamp
	return current
}

// messageRole extracts .message.role from a message entry.
func messageRole(e rawEntry) string {
	var m struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(e.Message, &m)
	return m.Role
}

// mergeToolResult folds a toolResult entry into the matching agent ToolInfo in
// the current exchange, keyed by toolCallId == ToolInfo.UseID. It also advances
// the exchange EndTime to the toolResult's timestamp so downstream stats that
// read the last exchange's EndTime report the real final-event time.
func mergeToolResult(current *schema.Exchange, e rawEntry) {
	if current == nil {
		return
	}
	var tr toolResultMessage
	if err := json.Unmarshal(e.Message, &tr); err != nil {
		return
	}
	content := toolResultContent(tr)
	for i := range current.Messages {
		msg := &current.Messages[i]
		if msg.Tool != nil && msg.Tool.UseID == tr.ToolCallID {
			msg.Tool.Output = buildToolOutput(content, tr)
			msg.Timestamp = e.Timestamp
			current.EndTime = e.Timestamp
			return
		}
	}
}

// toolResultContent joins the text blocks of a toolResult message.
func toolResultContent(tr toolResultMessage) string {
	var parts []string
	for _, b := range tr.Content {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// buildUserMessage maps a pi user message entry to a schema user Message.
// pi user content is either a plain string or an array of {type:text|image}.
func buildUserMessage(e rawEntry) schema.Message {
	var um userMessage
	_ = json.Unmarshal(e.Message, &um)
	parts := userContentParts(um.Content)
	return schema.Message{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		Role:      schema.RoleUser,
		Content:   parts,
	}
}

// userContentParts decodes a pi user message's content (string or array) into
// schema ContentParts. Image blocks are dropped in v1 (recorded as a gap).
func userContentParts(raw json.RawMessage) []schema.ContentPart {
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return []schema.ContentPart{{Type: schema.ContentTypeText, Text: s}}
		}
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var parts []schema.ContentPart
	for _, b := range blocks {
		if b.Type == schema.ContentTypeText && b.Text != "" {
			parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: b.Text})
		}
	}
	return parts
}

// buildAgentMessages maps a pi assistant message entry to one or more schema
// Messages: one Message holds the text+thinking content parts; each toolCall
// block becomes its own agent Message carrying a ToolInfo.
func buildAgentMessages(e rawEntry) []schema.Message {
	var am assistantMessage
	if err := json.Unmarshal(e.Message, &am); err != nil {
		return nil
	}
	var parts []schema.ContentPart
	var messages []schema.Message
	for _, b := range am.Content {
		switch b.Type {
		case schema.ContentTypeText:
			if b.Text != "" {
				parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: b.Text})
			}
		case schema.ContentTypeThinking:
			if b.Thinking != "" {
				parts = append(parts, schema.ContentPart{Type: schema.ContentTypeThinking, Text: b.Thinking})
			}
		case "toolCall":
			messages = append(messages, buildToolMessage(e, b, am))
		}
	}
	if len(parts) > 0 {
		head := schema.Message{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			Role:      schema.RoleAgent,
			Model:     am.Model,
			Content:   parts,
			Usage:     mapUsage(am.Usage),
		}
		return append([]schema.Message{head}, messages...)
	}
	if len(messages) == 0 {
		return buildErrorMessage(e, am)
	}
	// Tool-call-only assistant message: carry model+usage once on the first
	// tool message so no metadata is lost and no schema-invalid empty message
	// is emitted.
	messages[0].Model = am.Model
	if messages[0].Usage == nil {
		messages[0].Usage = mapUsage(am.Usage)
	}
	return messages
}

// buildToolMessage builds an agent Message wrapping a ToolInfo from a toolCall.
// The parent assistant message's model is passed through so tool messages carry
// the same model metadata as text/thinking messages. The Message ID is derived
// from the parent entry id + the toolCall id, so multiple toolCalls in one
// assistant message get distinct Message IDs (downstream provenance keys use
// msg.ID as a deterministic component).
func buildToolMessage(e rawEntry, b contentBlock, am assistantMessage) schema.Message {
	return schema.Message{
		ID:        toolMessageID(e.ID, b.ID),
		Timestamp: e.Timestamp,
		Role:      schema.RoleAgent,
		Model:     am.Model,
		Tool: &schema.ToolInfo{
			Name:  b.Name,
			Type:  classifyToolType(b.Name),
			UseID: b.ID,
			Input: b.Args,
		},
	}
}

// toolMessageID builds a unique Message ID for a tool-call message from the
// parent entry id and the toolCall id. This avoids duplicate Message IDs when a
// single assistant entry contains multiple toolCall blocks.
func toolMessageID(entryID, callID string) string {
	if callID != "" {
		return entryID + ":" + callID
	}
	return entryID
}

// buildToolOutput constructs the ToolInfo.Output map from a toolResult's
// content, error flag, and optional details blob. details (e.g. exitCode) is
// decoded into a generic value so downstream consumers (markdown rendering,
// cloud) can surface structured tool metadata without re-parsing raw JSON.
func buildToolOutput(content string, tr toolResultMessage) map[string]any {
	out := map[string]any{"content": content, "is_error": tr.IsError}
	if len(tr.Details) > 0 {
		var details any
		if err := json.Unmarshal(tr.Details, &details); err == nil {
			out["details"] = details
		}
	}
	return out
}

// mapUsage converts a pi usage object to the schema Usage. pi's input/output map
// to InputTokens/OutputTokens; cacheRead/cacheWrite map to the Claude-style
// cache fields (pi uses the same semantics).
func mapUsage(u *piUsage) *schema.Usage {
	if u == nil {
		return nil
	}
	return &schema.Usage{
		InputTokens:              int(u.Input),
		OutputTokens:             int(u.Output),
		CacheReadInputTokens:     int(u.CacheRead),
		CacheCreationInputTokens: int(u.CacheWrite),
	}
}

// buildErrorMessage surfaces an assistant entry with stopReason=error and an
// empty content array (no text/thinking/toolCalls) as an agent text message so
// the error event is not dropped from the transcript (and compaction boundaries
// stay interpretable). Returns nil if there is no errorMessage to surface.
func buildErrorMessage(e rawEntry, am assistantMessage) []schema.Message {
	if strings.TrimSpace(am.ErrorMessage) == "" {
		return nil
	}
	return []schema.Message{{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		Role:      schema.RoleAgent,
		Model:     am.Model,
		Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: "[error] " + am.ErrorMessage}},
		Usage:     mapUsage(am.Usage),
	}}
}

// deriveSlug returns a filename-safe slug from the first user message text.
// The trimmed text is passed to GenerateFilenameFromUserMessage so slugs do not
// differ only by leading/trailing whitespace.
func deriveSlug(data *schema.SessionData) string {
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role != schema.RoleUser {
				continue
			}
			for _, part := range msg.Content {
				if t := strings.TrimSpace(part.Text); t != "" {
					return spi.GenerateFilenameFromUserMessage(t)
				}
			}
		}
	}
	return ""
}

// classifyToolType maps pi tool names to the schema tool-type taxonomy.
func classifyToolType(name string) string {
	switch strings.ToLower(name) {
	case "read":
		return schema.ToolTypeRead
	case "edit", "write":
		return schema.ToolTypeWrite
	case "bash", "ls":
		return schema.ToolTypeShell
	case "grep", "find", "web_search", "fetch_content":
		return schema.ToolTypeSearch
	case "until_done_set", "until_done_plan", "until_done_task_update",
		"until_done_progress", "until_done_complete", "until_done_block",
		"until_done_replan", "until_done_distill":
		return schema.ToolTypeTask
	default:
		return schema.ToolTypeUnknown
	}
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
