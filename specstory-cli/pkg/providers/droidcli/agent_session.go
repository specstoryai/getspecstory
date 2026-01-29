package droidcli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

type (
	SessionData  = schema.SessionData
	ProviderInfo = schema.ProviderInfo
	Exchange     = schema.Exchange
	Message      = schema.Message
	ContentPart  = schema.ContentPart
	ToolInfo     = schema.ToolInfo
)

// GenerateAgentSession converts an fdSession into the shared SessionData schema.
func GenerateAgentSession(session *fdSession, workspaceRoot string) (*SessionData, error) {
	if session == nil {
		return nil, fmt.Errorf("session is nil")
	}

	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if sessionRoot := strings.TrimSpace(session.WorkspaceRoot); sessionRoot != "" {
		workspaceRoot = sessionRoot
	}

	exchanges, lastTimestamp := buildExchanges(session, workspaceRoot)
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("session contains no conversational exchanges")
	}

	for idx := range exchanges {
		exchanges[idx].ExchangeID = fmt.Sprintf("%s:%d", session.ID, idx)
		ensureExchangeTimestamps(&exchanges[idx])
	}

	created := strings.TrimSpace(session.CreatedAt)
	if created == "" {
		created = exchanges[0].StartTime
	}
	if created == "" {
		created = lastTimestamp
	}

	updated := strings.TrimSpace(lastTimestamp)
	if updated == "" {
		updated = created
	}

	data := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "droid-cli",
			Name:    "Factory Droid CLI",
			Version: "unknown",
		},
		SessionID:     session.ID,
		CreatedAt:     created,
		UpdatedAt:     updated,
		Slug:          session.Slug,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}

	return data, nil
}

func buildExchanges(session *fdSession, workspaceRoot string) ([]Exchange, string) {
	var exchanges []Exchange
	var current *Exchange
	var lastTimestamp string
	lastBlockWasUser := false

	flush := func() {
		if current == nil {
			return
		}
		if len(current.Messages) > 0 {
			if current.StartTime == "" {
				current.StartTime = current.Messages[0].Timestamp
			}
			if current.EndTime == "" {
				current.EndTime = current.Messages[len(current.Messages)-1].Timestamp
			}
			exchanges = append(exchanges, *current)
		}
		current = nil
		lastBlockWasUser = false
	}

	appendMessage := func(msg Message, timestamp string) {
		if current == nil {
			current = &Exchange{StartTime: timestamp}
		}
		current.Messages = append(current.Messages, msg)
		if timestamp != "" {
			current.EndTime = timestamp
		}
	}

	for _, block := range session.Blocks {
		ts := strings.TrimSpace(block.Timestamp)
		if ts != "" {
			lastTimestamp = ts
		}

		if isUserBlock(block) {
			text := cleanUserText(block.Text)
			if text == "" {
				continue
			}
			if !lastBlockWasUser {
				flush()
				current = &Exchange{StartTime: ts}
			}
			msg := Message{
				ID:        strings.TrimSpace(block.MessageID),
				Timestamp: ts,
				Role:      "user",
				Content: []ContentPart{
					{Type: "text", Text: text},
				},
			}
			appendMessage(msg, ts)
			lastBlockWasUser = true
			continue
		}

		if current == nil {
			current = &Exchange{StartTime: ts}
		}
		lastBlockWasUser = false

		switch block.Kind {
		case blockText:
			if msg := buildAgentTextMessage(block); msg != nil {
				appendMessage(*msg, ts)
			}
		case blockTool:
			if msg := buildToolMessage(block, workspaceRoot); msg != nil {
				appendMessage(*msg, ts)
			}
		case blockTodo:
			if msg := buildTodoMessage(block); msg != nil {
				appendMessage(*msg, ts)
			}
		case blockSummary:
			if msg := buildSummaryMessage(block); msg != nil {
				appendMessage(*msg, ts)
			}
		}
	}

	flush()
	return exchanges, lastTimestamp
}

func ensureExchangeTimestamps(ex *Exchange) {
	if ex.StartTime == "" && len(ex.Messages) > 0 {
		ex.StartTime = ex.Messages[0].Timestamp
	}
	if ex.EndTime == "" && len(ex.Messages) > 0 {
		ex.EndTime = ex.Messages[len(ex.Messages)-1].Timestamp
	}
}

func isUserBlock(block fdBlock) bool {
	return block.Kind == blockText && strings.EqualFold(strings.TrimSpace(block.Role), "user")
}

func buildAgentTextMessage(block fdBlock) *Message {
	text := strings.TrimSpace(block.Text)
	if text == "" {
		return nil
	}
	partType := "text"
	if block.IsReasoning {
		partType = "thinking"
	}
	return &Message{
		ID:        strings.TrimSpace(block.MessageID),
		Timestamp: strings.TrimSpace(block.Timestamp),
		Role:      "agent",
		Model:     strings.TrimSpace(block.Model),
		Content: []ContentPart{
			{Type: partType, Text: text},
		},
	}
}

func buildToolMessage(block fdBlock, workspaceRoot string) *Message {
	if block.Tool == nil {
		return nil
	}
	toolInfo := convertToolCallToInfo(block.Tool)
	if toolInfo == nil {
		return nil
	}
	formatted := formatToolCall(block.Tool)
	if formatted != "" {
		toolInfo.FormattedMarkdown = &formatted
	}
	toolInfo.Type = toolType(toolInfo.Name)
	if toolInfo.Output != nil {
		if content, ok := toolInfo.Output["content"].(string); ok {
			toolInfo.Output["content"] = strings.TrimSpace(content)
		}
	}
	pathHints := extractPathHints(toolInfo.Input, workspaceRoot)
	return &Message{
		ID:        strings.TrimSpace(block.MessageID),
		Timestamp: strings.TrimSpace(block.Timestamp),
		Role:      "agent",
		Model:     strings.TrimSpace(block.Model),
		Tool:      toolInfo,
		PathHints: pathHints,
	}
}

func buildTodoMessage(block fdBlock) *Message {
	if block.Todo == nil || len(block.Todo.Items) == 0 {
		return nil
	}
	formatted := formatTodoUpdate(block.Todo)
	input := map[string]interface{}{
		"items": todoItemsToMap(block.Todo.Items),
	}
	tool := &ToolInfo{
		Name:  "todo_state",
		Type:  "task",
		UseID: fmt.Sprintf("todo_%s", strings.ReplaceAll(block.Timestamp, ":", "")),
		Input: input,
	}
	if formatted != "" {
		tool.FormattedMarkdown = &formatted
	}
	return &Message{
		ID:        strings.TrimSpace(block.MessageID),
		Timestamp: strings.TrimSpace(block.Timestamp),
		Role:      "agent",
		Tool:      tool,
	}
}

func buildSummaryMessage(block fdBlock) *Message {
	if block.Summary == nil {
		return nil
	}
	body := strings.TrimSpace(block.Summary.Body)
	if body == "" {
		return nil
	}
	title := strings.TrimSpace(block.Summary.Title)
	if title == "" {
		title = "Summary"
	}
	text := fmt.Sprintf("**%s**\n\n%s", title, body)
	return &Message{
		ID:        strings.TrimSpace(block.MessageID),
		Timestamp: strings.TrimSpace(block.Timestamp),
		Role:      "agent",
		Content: []ContentPart{
			{Type: "thinking", Text: text},
		},
	}
}

func convertToolCallToInfo(call *fdToolCall) *ToolInfo {
	if call == nil {
		return nil
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "tool"
	}
	useID := strings.TrimSpace(call.ID)
	if useID == "" {
		useID = fmt.Sprintf("tool-%s", strings.ReplaceAll(call.InvokedAt, ":", ""))
	}
	args := decodeInput(call.Input)
	var output map[string]interface{}
	if call.Result != nil {
		content := strings.TrimSpace(call.Result.Content)
		if content != "" || call.Result.IsError {
			output = map[string]interface{}{
				"content": content,
				"isError": call.Result.IsError,
			}
		}
	}
	tool := &ToolInfo{
		Name:   name,
		Type:   toolType(name),
		UseID:  useID,
		Input:  args,
		Output: output,
	}
	if call.RiskLevel != "" {
		summary := fmt.Sprintf("Risk level: %s", call.RiskLevel)
		if strings.TrimSpace(call.RiskReason) != "" {
			summary = fmt.Sprintf("%s — %s", summary, strings.TrimSpace(call.RiskReason))
		}
		tool.Summary = &summary
	}
	return tool
}

func todoItemsToMap(items []fdTodoItem) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]interface{}{
			"description": strings.TrimSpace(item.Description),
			"status":      strings.TrimSpace(item.Status),
		})
	}
	return result
}

func extractPathHints(input map[string]interface{}, workspaceRoot string) []string {
	fields := []string{"path", "file_path", "filePath", "dir", "directory",
		"workdir", "cwd", "target"}
	var hints []string
	if input == nil {
		return hints
	}
	for _, field := range fields {
		val, ok := input[field]
		if !ok {
			continue
		}
		switch v := val.(type) {
		case string:
			addPathHint(&hints, v, workspaceRoot)
		case []interface{}:
			for _, entry := range v {
				if s, ok := entry.(string); ok {
					addPathHint(&hints, s, workspaceRoot)
				}
			}
		case json.Number:
			addPathHint(&hints, v.String(), workspaceRoot)
		}
	}
	return hints
}

func addPathHint(hints *[]string, value string, workspaceRoot string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	normalized := normalizeWorkspacePath(value, workspaceRoot)
	for _, existing := range *hints {
		if existing == normalized {
			return
		}
	}
	*hints = append(*hints, normalized)
}

func normalizeWorkspacePath(path string, workspaceRoot string) string {
	if workspaceRoot == "" {
		return path
	}
	if filepath.IsAbs(path) {
		if rel, err := filepath.Rel(workspaceRoot, path); err == nil {
			rel = filepath.Clean(rel)
			if rel == "." {
				return "."
			}
			if !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
	}
	return path
}
