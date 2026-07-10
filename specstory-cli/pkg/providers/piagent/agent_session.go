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
// role without fighting a single union struct. Compaction entries carry their
// summary as a top-level field (no message wrapper), and session_info entries
// carry the user-visible session name the same way.
type rawEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"` // null for the first entry
	Timestamp string          `json:"timestamp"`
	Summary   string          `json:"summary,omitempty"` // compaction entries only
	Name      string          `json:"name,omitempty"`    // session_info entries only
	Message   json.RawMessage `json:"message,omitempty"`
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
	Reasoning   int64 `json:"reasoning"` // thinking-model reasoning tokens
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

// ParseSession reads a pi JSONL session file (v3 tree or unmigrated v1 linear)
// and maps its current leaf-path branch into the unified schema.SessionData.
// The FULL leaf path is kept: compaction only trims pi's LLM context window,
// not the transcript, so pre-compaction history is preserved and the
// compaction summary is rendered as a marker message.
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
	exchanges := buildExchanges(ordered)
	enrichToolMessages(exchanges, header.Cwd)
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
		Exchanges:     exchanges,
	}
}

// enrichToolMessages populates PathHints and Summary/FormattedMarkdown on tool
// messages after tool results are merged, matching the sibling providers: path
// hints feed provenance extraction, and the formatted markdown lifts tool
// rendering out of the generic key/value fallback.
func enrichToolMessages(exchanges []schema.Exchange, workspaceRoot string) {
	for i := range exchanges {
		for j := range exchanges[i].Messages {
			msg := &exchanges[i].Messages[j]
			if msg.Tool == nil {
				continue
			}
			msg.PathHints = extractPathHints(msg.Tool.Input, workspaceRoot)
			summary, markdown := formatToolMarkdown(msg.Tool)
			if summary != "" {
				msg.Tool.Summary = &summary
			}
			if markdown != "" {
				msg.Tool.FormattedMarkdown = &markdown
			}
		}
	}
}

// extractPathHints collects file paths from a pi tool call's arguments (pi
// tools use "path"; the extra keys cover extension tools) plus paths mentioned
// in shell commands, normalized and deduped like the sibling providers.
func extractPathHints(input map[string]any, workspaceRoot string) []string {
	if input == nil {
		return nil
	}
	var hints []string
	add := func(p string) {
		if p == "" {
			return
		}
		n := spi.NormalizePath(p, workspaceRoot)
		for _, h := range hints {
			if h == n {
				return
			}
		}
		hints = append(hints, n)
	}
	for _, field := range []string{"path", "file_path", "filePath", "dir", "directory", "target"} {
		switch v := input[field].(type) {
		case string:
			add(v)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					add(s)
				}
			}
		}
	}
	if command, _ := input["command"].(string); command != "" {
		for _, sp := range spi.ExtractShellPathHints(command, workspaceRoot, workspaceRoot) {
			add(sp)
		}
	}
	return hints
}

// formatToolMarkdown builds the pre-formatted markdown (and optional summary)
// for the pi tools with a natural rendering: single-line bash commands become
// an inline-code summary, multi-line commands and file writes become fenced
// blocks, file tools show their path. Tools without a specific format return
// empty so the CLI's generic fallback (which renders input and output) is
// used instead. When a specific format is produced, the tool output is
// appended so the markdown stays self-contained.
func formatToolMarkdown(tool *schema.ToolInfo) (string, string) {
	var summary string
	var b strings.Builder
	in := tool.Input
	switch strings.ToLower(tool.Name) {
	case "bash":
		cmd, _ := in["command"].(string)
		if cmd == "" {
			return "", ""
		}
		if strings.Contains(cmd, "\n") {
			fmt.Fprintf(&b, "```bash\n%s\n```", strings.ReplaceAll(cmd, "```", "\\```"))
		} else {
			summary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, cmd)
		}
	case "read", "edit", "ls":
		p, _ := in["path"].(string)
		if p == "" {
			return "", ""
		}
		fmt.Fprintf(&b, "`%s`", p)
	case "write":
		p, _ := in["path"].(string)
		if p == "" {
			return "", ""
		}
		fmt.Fprintf(&b, "`%s`\n", p)
		if content, _ := in["content"].(string); content != "" {
			fmt.Fprintf(&b, "\n```\n%s\n```", strings.ReplaceAll(content, "```", "\\```"))
		}
	case "grep", "find":
		pattern, _ := in["pattern"].(string)
		if pattern == "" {
			return "", ""
		}
		fmt.Fprintf(&b, "`%s`", pattern)
	default:
		return "", ""
	}
	appendToolOutput(&b, tool)
	return summary, strings.TrimSpace(b.String())
}

// appendToolOutput appends the merged toolResult content as a fenced block
// (truncated like codexcli) so formatted tools still show their result.
func appendToolOutput(b *strings.Builder, tool *schema.ToolInfo) {
	if tool.Output == nil {
		return
	}
	content, _ := tool.Output["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if len(content) > 5000 {
		content = content[:5000] + "\n... (truncated)"
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	if isErr, _ := tool.Output["is_error"].(bool); isErr {
		b.WriteString("Error:\n")
	}
	b.WriteString("```\n" + content + "\n```")
}

// buildExchanges groups ordered entries into schema exchanges. A new user
// message starts a new exchange; assistant messages append to the current
// exchange; toolResults merge into the matching ToolInfo; compaction entries
// become marker messages so trimmed-context sessions stay self-explanatory.
// Other control entries (model_change, custom, etc.) are skipped from the
// conversation body.
func buildExchanges(ordered []rawEntry) []schema.Exchange {
	var exchanges []schema.Exchange
	var current *schema.Exchange
	commit := func() {
		if current != nil && len(current.Messages) > 0 {
			exchanges = append(exchanges, *current)
		}
	}
	for _, e := range ordered {
		if e.Type == entryCompaction {
			current = appendCompaction(current, e)
			continue
		}
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

// appendCompaction appends a compaction entry's summary to the current exchange
// as a marker agent message (creating an exchange if none exists yet). The
// summary is what pi replaced the pre-compaction context with; rendering it
// keeps the transcript self-explanatory without dropping any history.
func appendCompaction(current *schema.Exchange, e rawEntry) *schema.Exchange {
	if strings.TrimSpace(e.Summary) == "" {
		return current
	}
	if current == nil {
		current = &schema.Exchange{ExchangeID: e.ID, StartTime: e.Timestamp}
	}
	current.Messages = append(current.Messages, schema.Message{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		Role:      schema.RoleAgent,
		Content: []schema.ContentPart{{
			Type: schema.ContentTypeText,
			Text: "[Conversation compacted — summary of the earlier context]\n\n" + e.Summary,
		}},
	})
	current.EndTime = e.Timestamp
	return current
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
// cache fields (pi uses the same semantics); reasoning maps to the same field
// codexcli uses so telemetry aggregates pi thinking tokens like other providers.
func mapUsage(u *piUsage) *schema.Usage {
	if u == nil {
		return nil
	}
	return &schema.Usage{
		InputTokens:              int(u.Input),
		OutputTokens:             int(u.Output),
		ReasoningOutputTokens:    int(u.Reasoning),
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
