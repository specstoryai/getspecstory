package piagent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
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
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	ParentID   *string         `json:"parentId"` // null for the first entry
	Timestamp  string          `json:"timestamp"`
	Summary    string          `json:"summary,omitempty"` // compaction entries only
	Name       string          `json:"name,omitempty"`    // session_info entries only
	Message    json.RawMessage `json:"message,omitempty"`
	sourcePath string
	lineNumber int
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
	snapshot, err := readEntries(path)
	if err != nil {
		return nil, err
	}
	return snapshot.sessionData(path, "")
}

func (snapshot *sessionSnapshot) sessionData(path, projectPath string) (*schema.SessionData, error) {
	header, entries := snapshot.header, snapshot.entries
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
	// A missing native cwd only affects normalized rendering. Discovery must
	// continue using the original header so this fallback never invents an origin.
	workspaceRoot := header.Cwd
	if workspaceRoot == "" {
		workspaceRoot = projectPath
	}
	if workspaceRoot == "" {
		var err error
		workspaceRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("pi: resolving workspace fallback: %w", err)
		}
		workspaceRoot, err = spi.GetCanonicalPath(workspaceRoot)
		if err != nil {
			return nil, fmt.Errorf("pi: canonicalizing workspace fallback: %w", err)
		}
	}
	return buildSessionData(header, ordered, workspaceRoot), nil
}

// buildSessionData maps the ordered leaf-path entries into schema.SessionData.
func buildSessionData(header *sessionHeader, ordered []rawEntry, workspaceRoot string) *schema.SessionData {
	exchanges := buildExchanges(ordered)
	enrichToolMessages(exchanges, workspaceRoot)
	return &schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		Provider: schema.ProviderInfo{
			ID:      providerID,
			Name:    providerName,
			Version: "unknown", // Pi records its session format version, not its application release.
		},
		SessionID:     header.ID,
		CreatedAt:     header.Timestamp,
		WorkspaceRoot: workspaceRoot,
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
			msg.PathHints = extractPathHints(msg.Tool.Name, msg.Tool.Input, workspaceRoot)
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

// extractPathHints uses only the argument shapes declared by Pi's built-ins.
// Extension tools remain generic until their path semantics are known.
func extractPathHints(name string, input map[string]any, workspaceRoot string) []string {
	switch strings.ToLower(name) {
	case "read", "write", "edit", "grep", "find", "ls":
		if path, ok := input["path"].(string); ok && path != "" {
			return []string{spi.NormalizePath(path, workspaceRoot)}
		}
	case "bash":
		// The shared extractor understands shell syntax, not PowerShell syntax.
		if command, ok := input["command"].(string); ok {
			return spi.ExtractShellPathHints(command, workspaceRoot, workspaceRoot)
		}
	}
	return nil
}

// formatToolMarkdown preserves unrecognized fields even when a built-in has a
// bespoke rendering. Native inputs are never truncated; only displayed results
// are capped, leaving the complete structured output and RawData available.
func formatToolMarkdown(tool *schema.ToolInfo) (string, string) {
	var blocks []string
	in := tool.Input
	name := strings.ToLower(tool.Name)
	if failed, _ := tool.Output["is_error"].(bool); failed {
		// Error details can contain diagnostic diffs, not successfully applied edits.
		blocks = append(blocks, "**Error:**", renderToolOutput(tool, false))
		if args := spi.RenderGenericJSON(in); args != "" {
			blocks = append(blocks, "**Input:**\n"+args)
		}
		return "", strings.Join(blocks, "\n\n")
	}
	var consumed []string
	field := func(key, label, lang string) {
		value, exists := in[key]
		if !exists {
			return
		}
		switch value.(type) {
		case string, float64, bool, int, json.Number:
			blocks = append(blocks, "**"+label+":**\n"+spi.CodeFence(lang, spi.StringValue(in, key)))
			consumed = append(consumed, key)
		}
	}
	var summary string
	switch name {
	case "bash", "powershell":
		field("command", "Command", name)
		field("timeout", "Timeout (seconds)", "text")
		if cmd, ok := in["command"].(string); ok && cmd != "" && !strings.ContainsAny(cmd, "\n\r`") {
			summary = fmt.Sprintf("Tool use: **%s** `%s`", name, cmd)
		}
	case "read", "write", "edit", "grep", "find", "ls":
		field("path", "Path", "text")
		switch name {
		case "read":
			field("offset", "Offset (line)", "text")
			field("limit", "Limit", "text")
		case "write":
			field("content", "Content", spi.LanguageFromPath(spi.StringValue(in, "path")))
		case "edit":
			if old, ok := in["oldText"].(string); ok {
				if newText, ok := in["newText"].(string); ok {
					blocks = append(blocks, "**Edit:**\n"+spi.FormatDiffBlock(old, newText))
					consumed = append(consumed, "oldText", "newText")
				}
			}
			if edits, ok := in["edits"].([]any); ok {
				for i, edit := range edits {
					args, ok := edit.(map[string]any)
					if !ok {
						blocks = append(blocks, spi.RenderGenericJSON(map[string]any{"edit": edit}))
						continue
					}
					old, oldOK := args["oldText"].(string)
					newText, newOK := args["newText"].(string)
					var drop []string
					if oldOK && newOK {
						blocks = append(blocks, fmt.Sprintf("**Edit %d:**\n%s", i+1, spi.FormatDiffBlock(old, newText)))
						drop = []string{"oldText", "newText"}
					} else {
						blocks = append(blocks, spi.RenderGenericJSON(map[string]any{"edit": args}))
						continue
					}
					if extra := spi.RenderGenericJSON(args, drop...); extra != "" {
						blocks = append(blocks, extra)
					}
				}
				// An empty edits array is meaningful too: keep it in the generic fallback.
				if len(edits) > 0 {
					consumed = append(consumed, "edits")
				}
			}
		case "grep", "find":
			field("pattern", "Pattern", "text")
			field("limit", "Limit", "text")
			if name == "grep" {
				field("glob", "Glob", "text")
				field("ignoreCase", "Ignore case", "text")
				field("literal", "Literal", "text")
				field("context", "Context (lines)", "text")
			}
		case "ls":
			field("limit", "Limit", "text")
		}
	}
	if extra := spi.RenderGenericJSON(in, consumed...); extra != "" {
		blocks = append(blocks, "**Input:**\n"+extra)
	}
	if output := renderToolOutput(tool, true); output != "" {
		blocks = append(blocks, output)
	}
	return summary, strings.Join(blocks, "\n\n")
}

func renderToolOutput(tool *schema.ToolInfo, success bool) string {
	var blocks []string
	var consumed []string
	name := strings.ToLower(tool.Name)
	if content, ok := tool.Output["content"].(string); ok {
		lang := "text"
		if success && name == "read" {
			lang = spi.LanguageFromPath(spi.StringValue(tool.Input, "path"))
		}
		if name == "bash" || name == "powershell" {
			content = sanitizeShellOutput(content)
		}
		if content != "" {
			blocks = append(blocks, spi.CodeFence(lang, spi.CapRunes(content, 5000)))
		}
		consumed = append(consumed, "content")
	}
	if success && name == "edit" {
		if details, ok := tool.Output["details"].(map[string]any); ok {
			var rendered []string
			for _, key := range []string{"diff", "patch"} {
				if diff, ok := details[key].(string); ok {
					blocks = append(blocks, "**"+key+":**\n"+spi.CodeFence("diff", spi.CapRunes(diff, 5000)))
					rendered = append(rendered, key)
				}
			}
			if extra := cappedToolJSON(details, rendered...); extra != "" {
				blocks = append(blocks, "**Details:**\n"+extra)
			}
			consumed = append(consumed, "details")
		}
	}
	if _, ok := tool.Output["is_error"].(bool); ok {
		consumed = append(consumed, "is_error")
	}
	if extra := cappedToolJSON(tool.Output, consumed...); extra != "" {
		blocks = append(blocks, extra)
	}
	return strings.Join(blocks, "\n\n")
}

// Cap JSON before fencing it so truncation cannot remove the closing fence.
func cappedToolJSON(values map[string]any, drop ...string) string {
	kept := make(map[string]any, len(values))
	for key, value := range values {
		kept[key] = value
	}
	for _, key := range drop {
		delete(kept, key)
	}
	if len(kept) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return ""
	}
	return spi.CodeFence("json", spi.CapRunes(string(data), 5000))
}

func sanitizeShellOutput(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(content))
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
			msg := buildUserMessage(e)
			if msg == nil {
				continue
			}
			commit()
			current = &schema.Exchange{
				ExchangeID: e.ID,
				StartTime:  e.Timestamp,
				EndTime:    e.Timestamp,
				Messages:   []schema.Message{*msg},
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
		slog.Warn("pi: skipping corrupted tool result", "file", e.sourcePath, "lineNumber", e.lineNumber, "error", err)
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
func buildUserMessage(e rawEntry) *schema.Message {
	var um userMessage
	if err := json.Unmarshal(e.Message, &um); err != nil {
		slog.Warn("pi: skipping corrupted user message", "file", e.sourcePath, "lineNumber", e.lineNumber, "error", err)
		return nil
	}
	parts, err := userContentParts(um.Content)
	if err != nil {
		slog.Warn("pi: skipping corrupted user content", "file", e.sourcePath, "lineNumber", e.lineNumber, "error", err)
		return nil
	}
	return &schema.Message{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		Role:      schema.RoleUser,
		Content:   parts,
	}
}

// userContentParts decodes a pi user message's content (string or array) into
// schema ContentParts. Image blocks are dropped in v1 (recorded as a gap).
func userContentParts(raw json.RawMessage) ([]schema.ContentPart, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return []schema.ContentPart{{Type: schema.ContentTypeText, Text: s}}, nil
		}
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	var parts []schema.ContentPart
	for _, b := range blocks {
		if b.Type == schema.ContentTypeText && b.Text != "" {
			parts = append(parts, schema.ContentPart{Type: schema.ContentTypeText, Text: b.Text})
		}
	}
	return parts, nil
}

// buildAgentMessages flushes narration at each tool call so native block order
// survives normalization. Usage belongs to the native entry, hence only the
// first emitted message receives it.
func buildAgentMessages(e rawEntry) []schema.Message {
	var am assistantMessage
	if err := json.Unmarshal(e.Message, &am); err != nil {
		slog.Warn("pi: skipping corrupted assistant message", "file", e.sourcePath, "lineNumber", e.lineNumber, "error", err)
		return nil
	}
	var parts []schema.ContentPart
	var messages []schema.Message
	segmentStart := 0
	flush := func() {
		if len(parts) == 0 {
			return
		}
		id := e.ID
		if segmentStart > 0 {
			id = fmt.Sprintf("%s:content:%d", e.ID, segmentStart)
		}
		messages = append(messages, schema.Message{ID: id, Timestamp: e.Timestamp, Role: schema.RoleAgent, Model: am.Model, Content: parts})
		parts = nil
	}
	for i, b := range am.Content {
		if len(parts) == 0 {
			segmentStart = i
		}
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
			flush()
			msg := buildToolMessage(e, b, am)
			if b.ID == "" {
				msg.ID = fmt.Sprintf("%s:tool:%d", e.ID, i)
			}
			messages = append(messages, msg)
		default:
			slog.Warn("pi: unknown assistant content kind", "file", e.sourcePath, "lineNumber", e.lineNumber, "kind", b.Type)
		}
	}
	flush()
	if len(messages) == 0 {
		return buildErrorMessage(e, am)
	}
	messages[0].Usage = mapUsage(am.Usage)
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
	case "read", "ls":
		return schema.ToolTypeRead
	case "edit", "write":
		return schema.ToolTypeWrite
	case "bash", "powershell":
		return schema.ToolTypeShell
	case "grep", "find":
		return schema.ToolTypeSearch
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
// field (either a string or an array of {type:text} blocks). The result is the
// first text block that is non-empty after trimming, so the scan path (list,
// reindex) picks the same first user text as the full-parse path (sync), where
// deriveSlug trims each part and skips the blank ones. A block that is only
// whitespace does not count as text: it is skipped, not returned as "".
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
		if b.Type != "text" {
			continue
		}
		if t := strings.TrimSpace(b.Text); t != "" {
			return t
		}
	}
	return ""
}
