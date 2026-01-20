package droidcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

type jsonlEnvelope struct {
	Type string `json:"type"`
}

type sessionStartEvent struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Session   struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		CreatedAt string `json:"created_at"`
	} `json:"session"`
}

type messageEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Message   messagePayload  `json:"message"`
	Metadata  json.RawMessage `json:"metadata"`
}

type messagePayload struct {
	Role    string           `json:"role"`
	Model   string           `json:"model"`
	Content []contentPayload `json:"content"`
}

type contentPayload struct {
	Type            string          `json:"type"`
	Text            string          `json:"text"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Input           json.RawMessage `json:"input"`
	ToolUseID       string          `json:"tool_use_id"`
	Content         json.RawMessage `json:"content"`
	IsError         bool            `json:"is_error"`
	RiskLevel       string          `json:"riskLevel"`
	RiskLevelReason string          `json:"riskLevelReason"`
}

type todoEvent struct {
	Type      string       `json:"type"`
	Timestamp string       `json:"timestamp"`
	Todos     []fdTodoItem `json:"todos"`
}

type compactionEvent struct {
	Type      string    `json:"type"`
	Timestamp string    `json:"timestamp"`
	Summary   fdSummary `json:"summary"`
}

// parseFactorySession converts a Factory Droid JSONL transcript into an fdSession.
func parseFactorySession(filePath string) (*fdSession, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("droidcli: unable to open session %s: %w", filePath, err)
	}
	defer func() {
		_ = file.Close()
	}()

	session := &fdSession{Blocks: make([]fdBlock, 0, 64)}
	toolIndex := make(map[string]*fdToolCall)
	var rawBuilder strings.Builder
	var firstUserText string
	var firstTimestamp string

	scanErr := scanLines(file, func(line string) error {
		rawBuilder.WriteString(line)
		rawBuilder.WriteByte('\n')

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return nil
		}

		var env jsonlEnvelope
		if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
			// Skip malformed lines but keep raw data for diagnostics.
			return nil
		}

		switch env.Type {
		case "session_start":
			var event sessionStartEvent
			if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
				return nil
			}
			session.ID = fallback(session.ID, event.Session.ID)
			session.Title = fallback(session.Title, strings.TrimSpace(event.Session.Title))
			session.CreatedAt = fallback(session.CreatedAt, fallback(event.Session.CreatedAt, event.Timestamp))
		case "message":
			var event messageEvent
			if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
				return nil
			}
			if firstTimestamp == "" {
				firstTimestamp = normalizeEventTimestamp(event.Timestamp)
			}
			handleMessageEvent(session, &event, toolIndex)
			if strings.EqualFold(event.Message.Role, "user") {
				if firstUserText == "" {
					firstUserText = firstUserContent(event.Message.Content)
				}
			}
		case "todo_state":
			var event todoEvent
			if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
				return nil
			}
			if firstTimestamp == "" {
				firstTimestamp = normalizeEventTimestamp(event.Timestamp)
			}
			if len(event.Todos) == 0 {
				return nil
			}
			session.Blocks = append(session.Blocks, fdBlock{
				Kind:      blockTodo,
				Role:      "agent",
				Timestamp: event.Timestamp,
				Todo: &fdTodoState{
					Items: append([]fdTodoItem(nil), event.Todos...),
				},
			})
		case "compaction_state":
			var event compactionEvent
			if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
				return nil
			}
			if firstTimestamp == "" {
				firstTimestamp = normalizeEventTimestamp(event.Timestamp)
			}
			if strings.TrimSpace(event.Summary.Body) == "" {
				return nil
			}
			session.Blocks = append(session.Blocks, fdBlock{
				Kind:      blockSummary,
				Role:      "agent",
				Timestamp: event.Timestamp,
				Summary:   &event.Summary,
			})
		}

		return nil
	})
	if scanErr != nil {
		return nil, fmt.Errorf("droidcli: failed scanning %s: %w", filePath, scanErr)
	}

	if session.ID == "" {
		session.ID = strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	}

	if session.CreatedAt == "" {
		session.CreatedAt = fallback(firstTimestamp, deriveTimestampFromFilename(filePath))
	}

	session.Slug = slugFromContent(firstUserText, session.Title, session.CreatedAt)
	session.RawData = rawBuilder.String()

	return session, nil
}

func handleMessageEvent(session *fdSession, event *messageEvent, toolIndex map[string]*fdToolCall) {
	role := strings.ToLower(strings.TrimSpace(event.Message.Role))
	if role == "" {
		role = "agent"
	}
	model := strings.TrimSpace(event.Message.Model)

	for _, block := range event.Message.Content {
		switch block.Type {
		case "text":
			appendTextBlock(session, role, model, event.Timestamp, block.Text, false)
		case "reasoning", "thinking", "thought":
			appendTextBlock(session, role, model, event.Timestamp, block.Text, true)
		case "tool_use":
			tool := &fdToolCall{
				ID:           block.ID,
				Name:         block.Name,
				Input:        block.Input,
				RiskLevel:    block.RiskLevel,
				RiskReason:   block.RiskLevelReason,
				InvocationAt: event.Timestamp,
			}
			session.Blocks = append(session.Blocks, fdBlock{
				Kind:      blockTool,
				Role:      role,
				Timestamp: event.Timestamp,
				Model:     model,
				Tool:      tool,
			})
			if tool.ID != "" {
				toolIndex[tool.ID] = tool
			}
		case "tool_result":
			result := &fdToolResult{
				Content: decodeContent(block.Content),
				IsError: block.IsError,
			}
			if ref := toolIndex[block.ToolUseID]; ref != nil {
				ref.Result = result
			} else {
				session.Blocks = append(session.Blocks, fdBlock{
					Kind:      blockTool,
					Role:      role,
					Timestamp: event.Timestamp,
					Model:     model,
					Tool: &fdToolCall{
						ID:           block.ToolUseID,
						Name:         "tool_result",
						Result:       result,
						InvocationAt: event.Timestamp,
					},
				})
			}
		}
	}
}

func appendTextBlock(session *fdSession, role, model, timestamp, rawText string, reasoning bool) {
	text := strings.TrimSpace(rawText)
	if text == "" {
		return
	}
	session.Blocks = append(session.Blocks, fdBlock{
		Kind:        blockText,
		Role:        role,
		Timestamp:   timestamp,
		Model:       model,
		Text:        text,
		IsReasoning: reasoning,
	})
}

func firstUserContent(blocks []contentPayload) string {
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		text := strings.TrimSpace(block.Text)
		text = cleanUserText(text)
		if text != "" {
			return text
		}
	}
	return ""
}

func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		var builder strings.Builder
		for _, item := range arr {
			if text, ok := item["text"].(string); ok && strings.TrimSpace(text) != "" {
				if builder.Len() > 0 {
					builder.WriteString("\n")
				}
				builder.WriteString(text)
			}
		}
		return builder.String()
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if text, ok := obj["text"].(string); ok {
			return text
		}
		bytes, err := json.Marshal(obj)
		if err == nil {
			return string(bytes)
		}
	}
	return string(raw)
}

func slugFromContent(text string, title string, created string) string {
	if slug := spi.GenerateFilenameFromUserMessage(text); slug != "" {
		return slug
	}
	if slug := spi.GenerateFilenameFromUserMessage(title); slug != "" {
		return slug
	}
	if created != "" {
		return strings.ToLower(strings.ReplaceAll(created, ":", "-"))
	}
	return "factory-session"
}

func deriveTimestampFromFilename(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return base
}

func fallback(current, candidate string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(candidate)
}

func normalizeEventTimestamp(ts string) string {
	return strings.TrimSpace(ts)
}
