package droidcli

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
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

type jsonlEnvelope struct {
	Type string `json:"type"`
}

type sessionStartEvent struct {
	Type          string `json:"type"`
	Timestamp     string `json:"timestamp"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	SessionTitle  string `json:"sessionTitle"`
	CreatedAt     string `json:"created_at"`
	CWD           string `json:"cwd"`
	WorkspaceRoot string `json:"workspace_root"`
	Session       struct {
		ID            string `json:"id"`
		Title         string `json:"title"`
		CreatedAt     string `json:"created_at"`
		CWD           string `json:"cwd"`
		WorkspaceRoot string `json:"workspace_root"`
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
	Thinking        string          `json:"thinking"`
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
			slog.Debug("droidcli: skipping malformed JSONL line", "error", err, "file", filePath)
			return nil
		}

		switch env.Type {
		case "session_start":
			var event sessionStartEvent
			if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
				slog.Debug("droidcli: skipping malformed session_start event", "error", err, "file", filePath)
				return nil
			}
			session.ID = fallback(session.ID, firstNonEmpty(event.ID, event.Session.ID))
			session.Title = fallback(session.Title, firstNonEmpty(event.Title, event.Session.Title, event.SessionTitle))
			session.CreatedAt = fallback(session.CreatedAt, firstNonEmpty(event.CreatedAt, event.Session.CreatedAt, event.Timestamp))
			session.WorkspaceRoot = fallback(session.WorkspaceRoot, firstNonEmpty(event.CWD, event.WorkspaceRoot, event.Session.CWD, event.Session.WorkspaceRoot))
		case "message":
			var event messageEvent
			if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
				slog.Debug("droidcli: skipping malformed message event", "error", err, "file", filePath)
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
				slog.Debug("droidcli: skipping malformed todo_state event", "error", err, "file", filePath)
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
				slog.Debug("droidcli: skipping malformed compaction_state event", "error", err, "file", filePath)
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
			appendTextBlock(session, role, model, event.Timestamp, block.Text, false, event.ID)
		case "reasoning", "thinking", "thought":
			// Thinking content may be in the "thinking" field or "text" field depending on the format
			thinkingText := block.Thinking
			if thinkingText == "" {
				thinkingText = block.Text
			}
			appendTextBlock(session, role, model, event.Timestamp, thinkingText, true, event.ID)
		case "tool_use":
			tool := &fdToolCall{
				ID:         block.ID,
				Name:       block.Name,
				Input:      block.Input,
				RiskLevel:  block.RiskLevel,
				RiskReason: block.RiskLevelReason,
				InvokedAt:  event.Timestamp,
			}
			session.Blocks = append(session.Blocks, fdBlock{
				Kind:      blockTool,
				Role:      role,
				MessageID: strings.TrimSpace(event.ID),
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
					MessageID: strings.TrimSpace(event.ID),
					Timestamp: event.Timestamp,
					Model:     model,
					Tool: &fdToolCall{
						ID:        block.ToolUseID,
						Name:      "tool_result",
						Result:    result,
						InvokedAt: event.Timestamp,
					},
				})
			}
		}
	}
}

func appendTextBlock(session *fdSession, role, model, timestamp, rawText string, reasoning bool, messageID string) {
	text := strings.TrimSpace(rawText)
	if text == "" {
		return
	}
	session.Blocks = append(session.Blocks, fdBlock{
		Kind:        blockText,
		Role:        role,
		MessageID:   strings.TrimSpace(messageID),
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeEventTimestamp(ts string) string {
	return strings.TrimSpace(ts)
}

// Scanner utilities for reading JSONL files

const (
	kb                    = 1024
	mb                    = 1024 * kb
	maxReasonableLineSize = 250 * mb
)

var errStopScan = errors.New("stop scan")

func scanLines(reader io.Reader, handle func(line string) error) error {
	buf := bufio.NewReader(reader)
	lineNumber := 0

	for {
		line, err := buf.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		if err == io.EOF && line == "" {
			break
		}

		lineNumber++
		if len(line) > maxReasonableLineSize {
			return fmt.Errorf("line %d exceeds reasonable size limit (%d MB)", lineNumber, maxReasonableLineSize/mb)
		}

		line = strings.TrimRight(line, "\n")
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}

		if handleErr := handle(line); handleErr != nil {
			return handleErr
		}

		if err == io.EOF {
			break
		}
	}

	return nil
}

// Text cleaning utilities

var systemReminderPattern = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

func cleanUserText(text string) string {
	clean := systemReminderPattern.ReplaceAllString(text, "")
	return strings.TrimSpace(clean)
}
