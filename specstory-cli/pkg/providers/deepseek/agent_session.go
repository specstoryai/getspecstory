package deepseek

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"log/slog"

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
	SchemaVersion int          `json:"schema_version"`
	Metadata      dsMetadata   `json:"metadata"`
	Messages      []dsMessage  `json:"messages"`
	SystemPrompt  string       `json:"system_prompt"`
	RawData       string       // not serialised — set after parse
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
	Role    string        `json:"role"`
	Content []dsContentPart `json:"content"`
}

type dsContentPart struct {
	Type       string                 `json:"type"`        // "text", "thinking", "tool_use", "tool_result"
	Text       string                 `json:"text"`        // used when Type == "text"
	Thinking   string                 `json:"thinking"`    // used when Type == "thinking"
	// tool_use fields are flat in the JSON (not nested)
	Name       string                 `json:"name"`
	ID         string                 `json:"id"`
	Input      map[string]interface{} `json:"input"`
	// tool_result fields
	ContentArr []dsContentPart `json:"content_arr"` // ignored — content is a raw string in actual JSON
	ToolResultContent string  `json:"content"` // tool_result content — can be a JSON string
	ToolUseID    string       `json:"tool_use_id"`
}



// parseSessionFile reads and parses a DeepSeek TUI session JSON file.
func parseSessionFile(path string) (*dsSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("deepseek: cannot read session file %s: %w", path, err)
	}

	var session dsSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("deepseek: cannot parse session file %s: %w", path, err)
	}

	session.RawData = string(data)
	return &session, nil
}

// extractSessionMetadata reads only the metadata fields from a session file
// without parsing the full messages array.
func extractSessionMetadata(path string) (*spi.SessionMetadata, error) {
	// Parse full file since we need to find the first user message for name/slug.
	session, err := parseSessionFile(path)
	if err != nil {
		return nil, err
	}

	if len(session.Messages) == 0 {
		return nil, nil
	}

	// Find first user message for slug and name
	var firstUserMsg string
	for _, msg := range session.Messages {
		if msg.Role == "user" {
			firstUserMsg = msgContentText(msg)
			if firstUserMsg != "" {
				break
			}
		}
	}

	if firstUserMsg == "" {
		return nil, nil
	}

	slug := spi.GenerateFilenameFromUserMessage(firstUserMsg)
	if slug == "" {
		slug = "deepseek-session"
	}

	name := spi.GenerateReadableName(firstUserMsg)

	return &spi.SessionMetadata{
		SessionID: session.Metadata.ID,
		CreatedAt: session.Metadata.CreatedAt,
		Slug:      slug,
		Name:      name,
	}, nil
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

	exchanges := buildExchanges(session)
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("session contains no conversational exchanges")
	}

	created := strings.TrimSpace(session.Metadata.CreatedAt)
	updated := strings.TrimSpace(session.Metadata.UpdatedAt)
	if updated == "" {
		updated = created
	}

	// Attach session-level token usage to the last message of the last exchange.
	if session.Metadata.TotalTokens > 0 {
		if lastEx := &exchanges[len(exchanges)-1]; len(lastEx.Messages) > 0 {
			usage := &Usage{InputTokens: session.Metadata.TotalTokens}
			lastEx.Messages[len(lastEx.Messages)-1].Usage = usage
		}
	}

	data := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "deepseek",
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
func buildExchanges(session *dsSession) []Exchange {
	var exchanges []Exchange
	var current *Exchange

	for _, msg := range session.Messages {
		if msg.Role == "user" {
			if isToolResultOnly(&msg) {
				// This is a tool result injected as user-role — skip it
				// but if there's a current exchange, attach tool result
				// info to the last assistant message.
				if current != nil && len(current.Messages) > 0 {
					lastIdx := len(current.Messages) - 1
					lastMsg := &current.Messages[lastIdx]
					attachToolResults(&msg, lastMsg)
				}
				continue
			}

			// Start a new exchange for user messages with real text content.
			if current != nil && len(current.Messages) > 0 {
				exchanges = append(exchanges, *current)
			}
			current = &Exchange{}
			ts := session.Metadata.CreatedAt

			spiMsg := convertUserMessage(msg, ts)
			if spiMsg.Content != nil && len(spiMsg.Content) > 0 {
				current.Messages = append(current.Messages, spiMsg)
				if current.StartTime == "" {
					current.StartTime = ts
				}
				current.EndTime = ts
			}
			continue
		}

		// Assistant message
		if current == nil {
			current = &Exchange{}
		}

		ts := time.Now().UTC().Format(time.RFC3339)
		spiMsgs := convertAssistantMessage(msg, session.Metadata.Model, ts)
		current.Messages = append(current.Messages, spiMsgs...)
		current.EndTime = ts
	}

	// Flush the last exchange.
	if current != nil && len(current.Messages) > 0 {
		exchanges = append(exchanges, *current)
	}

	// Assign exchange IDs.
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

// attachToolResults attaches tool result content to the last assistant Message's
// tool Output, if present. DeepSeek TUI stores tool_result content as a raw JSON
// string.
func attachToolResults(msg *dsMessage, lastMsg *Message) {
	if lastMsg.Tool == nil {
		return
	}
	for _, cp := range msg.Content {
		if cp.Type != "tool_result" {
			continue
		}
		// Tool result content is a JSON string in DeepSeek TUI.
		content := strings.TrimSpace(cp.ToolResultContent)
		if content == "" {
			continue
		}
		if lastMsg.Tool.Output == nil {
			lastMsg.Tool.Output = make(map[string]interface{})
		}
		if existing, ok := lastMsg.Tool.Output["content"].(string); ok {
			lastMsg.Tool.Output["content"] = existing + content
		} else {
			lastMsg.Tool.Output["content"] = content
		}
	}
}

// hasUserMessageContent returns true if the message has at least one text
// content part that isn't a system block (<turn_meta>).
func hasUserMessageContent(msg *dsMessage) bool {
	for _, cp := range msg.Content {
		if cp.Type == "text" {
			t := strings.TrimSpace(cp.Text)
			if t != "" && !strings.HasPrefix(t, "<turn_meta>") {
				return true
			}
		}
	}
	return false
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
func convertAssistantMessage(msg dsMessage, model string, timestamp string) []Message {
	var msgs []Message
	var textParts []ContentPart
	var toolParts []dsContentPart

	// Separate text/thinking from tool_use parts.
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

	// Emit a single message for text/thinking parts.
	if len(textParts) > 0 {
		msgs = append(msgs, Message{
			Timestamp: timestamp,
			Role:      "agent",
			Model:     model,
			Content:   textParts,
		})
	}

	// Each tool_use becomes its own message with ToolInfo.
	for _, cp := range toolParts {
		toolMsg := convertToolUseMessage(cp, model, timestamp)
		msgs = append(msgs, toolMsg)
	}

	return msgs
}

// convertToolUseMessage creates a Message for a tool_use content part.
func convertToolUseMessage(cp dsContentPart, model string, timestamp string) Message {
	name := cp.Name
	if name == "" {
		name = "unknown"
	}
	tool := &ToolInfo{
		Name:   name,
		Type:   classifyToolType(name),
		UseID:  cp.ID,
		Input:  cp.Input,
	}

	return Message{
		Timestamp: timestamp,
		Role:      "agent",
		Model:     model,
		Tool:      tool,
	}
}

// classifyToolType maps a tool name to a SpecStory tool type.
func classifyToolType(name string) string {
	writeTools := map[string]bool{
		"write_file": true, "edit_file": true, "apply_patch": true,
		"checklist_write": true, "task_create": true, "note": true,
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

	// Write the raw session JSON.
	rawPath := dir + "/raw-session.json"
	if err := os.WriteFile(rawPath, []byte(session.RawData), 0o644); err != nil {
		slog.Debug("deepseek: failed to write debug raw file", "path", rawPath, "error", err)
		return
	}

	slog.Debug("deepseek: wrote debug raw file", "sessionId", session.Metadata.ID, "path", rawPath)
}
