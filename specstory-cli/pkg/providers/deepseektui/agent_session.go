package deepseektui

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Type aliases for convenience - use the shared schema types.
type (
	SessionData  = schema.SessionData
	ProviderInfo = schema.ProviderInfo
	Exchange     = schema.Exchange
	Message      = schema.Message
	ContentPart  = schema.ContentPart
	ToolInfo     = schema.ToolInfo
	Usage        = schema.Usage
)

// dsSession represents the on-disk format of a DeepSeek TUI session file.
type dsSession struct {
	SchemaVersion int         `json:"schema_version"`
	Metadata      dsMetadata  `json:"metadata"`
	Messages      []dsMessage `json:"messages"`
	SystemPrompt  string      `json:"system_prompt"`
	RawData       string      `json:"-"` // populated only when parseSessionFile is called with wantRawData=true
}

type dsMetadata struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	TotalTokens  int    `json:"total_tokens"`
	Model        string `json:"model"`
	Workspace    string `json:"workspace"`
	Mode         string `json:"mode"`
}

type dsMessage struct {
	Role    string          `json:"role"`
	Content []dsContentPart `json:"content"`
}

type dsContentPart struct {
	Type     string `json:"type"`     // "text", "thinking", "tool_use", "tool_result"
	Text     string `json:"text"`     // used when Type == "text"
	Thinking string `json:"thinking"` // used when Type == "thinking"
	// tool_use fields are flat in the JSON (not nested)
	Name  string         `json:"name"`
	ID    string         `json:"id"`
	Input map[string]any `json:"input"`
	// tool_result fields
	ToolResultContent string `json:"content"`     // tool_result content (raw string)
	ToolUseID         string `json:"tool_use_id"` // matches the tool_use.id this result is for
}

// parseSessionFile reads and parses a DeepSeek TUI session JSON file. When
// wantRawData is true the file's full byte body is retained on the returned
// session for downstream cloud sync and debug-raw output; callers that only
// need the parsed structure (e.g. metadata extraction) pass false to avoid
// keeping a copy of the entire JSON in memory.
func parseSessionFile(path string, wantRawData bool) (*dsSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("deepseek: cannot read session file %s: %w", path, err)
	}

	var session dsSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("deepseek: cannot parse session file %s: %w", path, err)
	}

	if wantRawData {
		session.RawData = string(data)
	}
	return &session, nil
}

// extractSessionMetadata parses a session file and returns a SessionMetadata
// summary derived from the first user message. Returns (nil, nil) when the
// session has no usable user content — callers treat that as "skip this file".
func extractSessionMetadata(path string) (*spi.SessionMetadata, error) {
	var result *spi.SessionMetadata

	session, err := parseSessionFile(path, false)
	if err != nil {
		return nil, err
	}

	firstUserMsg := firstUserMessageText(session)
	if firstUserMsg != "" {
		slug := spi.GenerateFilenameFromUserMessage(firstUserMsg)
		if slug == "" {
			slug = "deepseek-session"
		}
		result = &spi.SessionMetadata{
			SessionID: session.Metadata.ID,
			CreatedAt: session.Metadata.CreatedAt,
			Slug:      slug,
			Name:      spi.GenerateReadableName(firstUserMsg),
		}
	}

	return result, nil
}

// firstUserMessageText returns the text of the first user message that carries
// visible content, or "" if no such message exists.
func firstUserMessageText(session *dsSession) string {
	for _, msg := range session.Messages {
		if msg.Role != "user" {
			continue
		}
		if text := msgContentText(msg); text != "" {
			return text
		}
	}
	return ""
}

// convertToAgentSession converts a parsed dsSession to the unified AgentChatSession format.
func convertToAgentSession(session *dsSession, workspaceRoot string, debugRaw bool) *spi.AgentChatSession {
	if session == nil {
		return nil
	}
	if len(session.Messages) == 0 {
		return nil
	}

	sessionData, err := generateAgentSession(session, workspaceRoot)
	if err != nil {
		slog.Debug("deepseek: skipping session due to conversion error",
			"sessionId", session.Metadata.ID, "error", err)
		return nil
	}

	// Derive slug from first user message
	slug := deriveSlug(session)
	if slug == "" {
		slug = "deepseek-session"
	}

	if debugRaw {
		writeDebugRaw(session)
	}

	return &spi.AgentChatSession{
		SessionID:   session.Metadata.ID,
		CreatedAt:   sessionData.CreatedAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     session.RawData,
	}
}

// generateAgentSession converts a dsSession into the shared SessionData schema.
func generateAgentSession(session *dsSession, workspaceRoot string) (*SessionData, error) {
	workspace := strings.TrimSpace(workspaceRoot)
	if ws := strings.TrimSpace(session.Metadata.Workspace); ws != "" {
		workspace = ws
	}

	exchanges := buildExchanges(session, workspace)
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("session contains no conversational exchanges")
	}

	created := strings.TrimSpace(session.Metadata.CreatedAt)
	updated := strings.TrimSpace(session.Metadata.UpdatedAt)
	if updated == "" {
		updated = created
	}

	// DeepSeek TUI reports only a session-level total_tokens with no breakdown
	// between input/output/cached, so we intentionally don't synthesize a Usage
	// value — attributing the sum to InputTokens would mislead downstream stats.

	data := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "deepseek-tui",
			Name:    "DeepSeek TUI",
			Version: session.Metadata.Model,
		},
		SessionID:     session.Metadata.ID,
		CreatedAt:     created,
		UpdatedAt:     updated,
		Slug:          deriveSlug(session),
		WorkspaceRoot: workspace,
		Exchanges:     exchanges,
	}

	return data, nil
}

// buildExchanges groups messages into exchanges.
// Each user message with real text content starts a new exchange.
// User messages that only contain tool_result content are treated as
// part of the current assistant turn (tool response), not as new exchanges.
func buildExchanges(session *dsSession, workspaceRoot string) []Exchange {
	var exchanges []Exchange
	var current *Exchange

	// DeepSeek TUI does not stamp per-message timestamps. Use session-level
	// CreatedAt for user turns (best signal of when the conversation began)
	// and UpdatedAt for assistant turns so re-parsing the same file produces
	// stable, deterministic output. time.Now() would change every parse.
	userTs := strings.TrimSpace(session.Metadata.CreatedAt)
	assistantTs := strings.TrimSpace(session.Metadata.UpdatedAt)
	if assistantTs == "" {
		assistantTs = userTs
	}

	for _, msg := range session.Messages {
		if msg.Role == "user" {
			if isToolResultOnly(&msg) {
				// Tool results live in user-role messages; route each tool_result
				// to the assistant Tool message it belongs to via tool_use_id.
				if current != nil {
					attachToolResults(&msg, current)
				}
				continue
			}

			if current != nil && len(current.Messages) > 0 {
				exchanges = append(exchanges, *current)
			}
			current = &Exchange{}

			spiMsg := convertUserMessage(msg, userTs)
			if len(spiMsg.Content) > 0 {
				current.Messages = append(current.Messages, spiMsg)
				if current.StartTime == "" {
					current.StartTime = userTs
				}
				current.EndTime = userTs
			}
			continue
		}

		if current == nil {
			current = &Exchange{}
		}

		spiMsgs := convertAssistantMessage(msg, session.Metadata.Model, assistantTs, workspaceRoot)
		current.Messages = append(current.Messages, spiMsgs...)
		current.EndTime = assistantTs
	}

	if current != nil && len(current.Messages) > 0 {
		exchanges = append(exchanges, *current)
	}

	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", session.Metadata.ID, i)
		if exchanges[i].StartTime == "" {
			exchanges[i].StartTime = session.Metadata.CreatedAt
		}
		if exchanges[i].EndTime == "" {
			exchanges[i].EndTime = exchanges[i].StartTime
		}
	}

	return exchanges
}

// isToolResultOnly returns true if the user message only contains tool_result
// content parts (no real text from the user).
func isToolResultOnly(msg *dsMessage) bool {
	hasToolResult := false
	for _, cp := range msg.Content {
		switch cp.Type {
		case "tool_result":
			hasToolResult = true
		case "text":
			// Check if the text content is just <turn_meta> or system noise.
			t := strings.TrimSpace(cp.Text)
			if t != "" && !strings.HasPrefix(t, "<turn_meta>") {
				return false // has real user text
			}
		}
	}
	return hasToolResult
}

// attachToolResults routes each tool_result content part in a user-role message
// to the assistant Tool message it belongs to, matched by tool_use_id.
//
// DeepSeek TUI emits tool results in the user message that follows the
// assistant's tool calls. When the assistant fires N parallel tool calls, the
// next user message holds N tool_result parts — each with its own tool_use_id.
// Earlier code attached every result to whichever was the *last* tool message
// and string-concatenated multiple results together; that produced wrong
// markdown for any session with parallel calls. We fix this by indexing
// assistant tool messages by UseID and attaching each result to its match.
//
// After attaching, we re-render FormattedMarkdown so the rendered tool block
// includes the result body.
func attachToolResults(msg *dsMessage, current *Exchange) {
	if current == nil {
		return
	}
	// Build an index of assistant Tool messages in this exchange by UseID.
	byUseID := make(map[string]*Message, len(current.Messages))
	for i := range current.Messages {
		m := &current.Messages[i]
		if m.Tool != nil && m.Tool.UseID != "" {
			byUseID[m.Tool.UseID] = m
		}
	}

	for _, cp := range msg.Content {
		if cp.Type != "tool_result" {
			continue
		}
		target, ok := byUseID[cp.ToolUseID]
		if !ok || target == nil || target.Tool == nil {
			// Result with no matching assistant call — drop rather than misattribute.
			slog.Debug("deepseek: tool_result has no matching tool_use",
				"toolUseId", cp.ToolUseID)
			continue
		}
		content := strings.TrimSpace(cp.ToolResultContent)
		if content == "" {
			continue
		}
		target.Tool.Output = map[string]any{"content": content}
		// Re-render the formatted markdown now that the result is in place.
		formatted := formatToolCall(target.Tool)
		if formatted != "" {
			target.Tool.FormattedMarkdown = &formatted
		}
	}
}

// convertUserMessage converts a DeepSeek user message to the schema Message format.
// System-injected metadata blocks like <turn_meta> are filtered out so only the
// user's actual question appears in the saved session.
func convertUserMessage(msg dsMessage, timestamp string) Message {
	parts := userContentParts(msg)
	return Message{
		Timestamp: timestamp,
		Role:      "user",
		Content:   parts,
	}
}

// userContentParts returns the user-visible text content parts from a message,
// skipping system-injected blocks such as <turn_meta>.
func userContentParts(msg dsMessage) []ContentPart {
	var parts []ContentPart
	for _, cp := range msg.Content {
		if cp.Type != "text" || cp.Text == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(cp.Text), "<turn_meta>") {
			continue
		}
		parts = append(parts, ContentPart{Type: "text", Text: cp.Text})
	}
	return parts
}

// convertAssistantMessage converts a DeepSeek assistant message to the schema Message format.
// Each tool_use content part produces a separate Message (one tool = one message),
// while text and thinking parts are combined into a single message.
func convertAssistantMessage(msg dsMessage, model string, timestamp string, workspaceRoot string) []Message {
	var msgs []Message
	var textParts []ContentPart
	var toolParts []dsContentPart

	for _, cp := range msg.Content {
		switch cp.Type {
		case "tool_use":
			toolParts = append(toolParts, cp)
		case "thinking":
			thinking := strings.TrimSpace(cp.Thinking)
			if thinking == "" {
				thinking = strings.TrimSpace(cp.Text)
			}
			if thinking != "" {
				textParts = append(textParts, ContentPart{Type: "thinking", Text: thinking})
			}
		case "text":
			text := strings.TrimSpace(cp.Text)
			if text == "" {
				text = strings.TrimSpace(cp.Thinking)
			}
			if text != "" {
				textParts = append(textParts, ContentPart{Type: "text", Text: text})
			}
		}
	}

	if len(textParts) > 0 {
		msgs = append(msgs, Message{
			Timestamp: timestamp,
			Role:      "agent",
			Model:     model,
			Content:   textParts,
		})
	}

	for _, cp := range toolParts {
		msgs = append(msgs, convertToolUseMessage(cp, model, timestamp, workspaceRoot))
	}

	return msgs
}

// convertToolUseMessage creates a Message for a tool_use content part. The
// FormattedMarkdown is set from the tool's input only here; attachToolResults
// re-renders it once the matching tool_result is attached.
func convertToolUseMessage(cp dsContentPart, model string, timestamp string, workspaceRoot string) Message {
	name := cp.Name
	if name == "" {
		name = "unknown"
	}
	tool := &ToolInfo{
		Name:  name,
		Type:  classifyToolType(name),
		UseID: cp.ID,
		Input: cp.Input,
	}
	if formatted := formatToolCall(tool); formatted != "" {
		tool.FormattedMarkdown = &formatted
	}
	return Message{
		Timestamp: timestamp,
		Role:      "agent",
		Model:     model,
		Tool:      tool,
		PathHints: extractPathHints(cp.Input, workspaceRoot),
	}
}

// classifyToolType maps a tool name to a SpecStory tool type.
func classifyToolType(name string) string {
	writeTools := map[string]bool{
		"write_file": true, "edit_file": true, "apply_patch": true,
	}
	readTools := map[string]bool{
		"read_file": true, "list_dir": true, "retrieve_tool_result": true,
	}
	searchTools := map[string]bool{
		"grep_files": true, "file_search": true, "web_search": true,
		"fetch_url": true,
	}
	shellTools := map[string]bool{
		"exec_shell": true, "task_shell_start": true, "task_shell_wait": true,
		"exec_shell_wait": true, "exec_interact": true, "exec_shell_interact": true,
	}
	taskTools := map[string]bool{
		"update_plan": true, "todo_write": true, "checklist_write": true,
		"task_create": true, "task_read": true, "task_list": true,
	}

	if writeTools[name] {
		return schema.ToolTypeWrite
	}
	if readTools[name] {
		return schema.ToolTypeRead
	}
	if searchTools[name] {
		return schema.ToolTypeSearch
	}
	if shellTools[name] {
		return schema.ToolTypeShell
	}
	if taskTools[name] {
		return schema.ToolTypeTask
	}

	return schema.ToolTypeGeneric
}

// msgContentText extracts the first user-visible text content from a dsMessage,
// skipping system-injected blocks such as <turn_meta>.
func msgContentText(msg dsMessage) string {
	for _, cp := range msg.Content {
		if cp.Type == "text" && cp.Text != "" {
			if strings.HasPrefix(strings.TrimSpace(cp.Text), "<turn_meta>") {
				continue
			}
			return cp.Text
		}
	}
	// Fallback: try thinking content or first non-empty.
	for _, cp := range msg.Content {
		t := strings.TrimSpace(cp.Text)
		if t == "" {
			t = strings.TrimSpace(cp.Thinking)
		}
		if t != "" {
			return t
		}
	}
	return ""
}

// deriveSlug creates a filename-safe slug from the first user message.
func deriveSlug(session *dsSession) string {
	for _, msg := range session.Messages {
		if msg.Role == "user" {
			text := msgContentText(msg)
			if text != "" {
				return spi.GenerateFilenameFromUserMessage(text)
			}
		}
	}
	return "deepseek-session"
}

// writeDebugRaw writes the raw session JSON to the debug directory.
func writeDebugRaw(session *dsSession) {
	if session == nil {
		return
	}

	dir := spi.GetDebugDir(session.Metadata.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Debug("deepseek: unable to create debug dir", "error", err)
		return
	}

	rawPath := filepath.Join(dir, "raw-session.json")
	if err := os.WriteFile(rawPath, []byte(session.RawData), 0o644); err != nil {
		slog.Debug("deepseek: failed to write debug raw file", "path", rawPath, "error", err)
		return
	}

	slog.Debug("deepseek: wrote debug raw file", "sessionId", session.Metadata.ID, "path", rawPath)
}
