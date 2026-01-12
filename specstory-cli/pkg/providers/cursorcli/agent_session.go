package cursorcli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
)

// Type aliases for convenience - use the shared schema types
type (
	SessionData  = schema.SessionData
	ProviderInfo = schema.ProviderInfo
	Exchange     = schema.Exchange
	Message      = schema.Message
	ContentPart  = schema.ContentPart
	ToolInfo     = schema.ToolInfo
)

// GenerateAgentSession creates a SessionData from Cursor CLI blob records
// The blob records should already be DAG-sorted from ReadSessionData
func GenerateAgentSession(blobRecords []BlobRecord, workspaceRoot, sessionID, createdAt, slug string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "sessionID", sessionID, "blobCount", len(blobRecords))

	if len(blobRecords) == 0 {
		return nil, fmt.Errorf("no blob records to process")
	}

	// Group records into exchanges
	exchanges, err := buildExchangesFromBlobs(blobRecords, workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to build exchanges: %w", err)
	}

	// Assign exchangeId to each exchange (format: sessionId:index)
	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", sessionID, i)
	}

	slog.Info("GenerateAgentSession: Built exchanges", "count", len(exchanges))

	// Populate Summary and FormattedMarkdown for all tools using existing Cursor formatters
	for i := range exchanges {
		for j := range exchanges[i].Messages {
			msg := &exchanges[i].Messages[j]
			if msg.Tool != nil {
				summary, formattedMd := formatCursorToolWithSummary(msg.Tool)
				if summary != "" {
					msg.Tool.Summary = &summary
				}
				if formattedMd != "" {
					msg.Tool.FormattedMarkdown = &formattedMd
				}
			}
		}
	}

	sessionData := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "cursor",
			Name:    "Cursor CLI",
			Version: "unknown", // Cursor doesn't expose version in SQLite
		},
		SessionID:     sessionID,
		CreatedAt:     createdAt,
		Slug:          slug,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}

	return sessionData, nil
}

// buildExchangesFromBlobs groups blob records into exchanges
// An exchange is a user prompt followed by agent responses and tool results
// Blob records are already in DAG-sorted order from ReadSessionData
func buildExchangesFromBlobs(blobRecords []BlobRecord, workspaceRoot string) ([]Exchange, error) {
	var exchanges []Exchange
	var currentExchange *Exchange

	// Track pending tool calls that need results merged
	type pendingTool struct {
		exchangeIdx int
		messageIdx  int
		toolCallID  string
	}
	pendingTools := make(map[string]pendingTool)

	for _, record := range blobRecords {
		// Parse the blob data
		var data struct {
			Role    string            `json:"role"`
			ID      string            `json:"id"`
			Content []json.RawMessage `json:"content"`
		}

		if err := json.Unmarshal(record.Data, &data); err != nil {
			slog.Debug("Failed to parse blob record", "rowid", record.RowID, "error", err)
			continue
		}

		// Skip if no content
		if len(data.Content) == 0 {
			continue
		}

		switch data.Role {
		case "user":
			// Check if this is a real user message or just metadata
			firstContent, skipMsg := parseFirstContent(data.Content[0])
			if skipMsg || (firstContent.Type == "text" && strings.HasPrefix(strings.TrimSpace(firstContent.Text), "<user_info>")) {
				continue
			}

			// This is a new user prompt, start a new exchange
			if currentExchange != nil && len(currentExchange.Messages) > 0 {
				exchanges = append(exchanges, *currentExchange)
			}

			currentExchange = &Exchange{
				Messages: []Message{},
			}

			msg := buildUserMessageFromBlob(data.Content, record.RowID)
			currentExchange.Messages = append(currentExchange.Messages, msg)
			if msg.Timestamp != "" {
				currentExchange.StartTime = msg.Timestamp
			}

		case "assistant":
			// Agent message - add to current exchange
			if currentExchange == nil {
				// Create exchange if we don't have one (shouldn't happen normally)
				currentExchange = &Exchange{
					Messages: []Message{},
				}
			}

			// Build messages from this assistant blob (may return multiple if there are multiple tool calls)
			msgs := buildAgentMessagesFromBlob(data.Content, record.RowID, workspaceRoot)

			// Add all messages and track tool calls
			for _, msg := range msgs {
				// Track tool calls for later result merging
				if msg.Tool != nil && msg.Tool.UseID != "" {
					pendingTools[msg.Tool.UseID] = pendingTool{
						exchangeIdx: len(exchanges),
						messageIdx:  len(currentExchange.Messages),
						toolCallID:  msg.Tool.UseID,
					}
				}

				currentExchange.Messages = append(currentExchange.Messages, msg)
				if msg.Timestamp != "" {
					currentExchange.EndTime = msg.Timestamp
				}
			}

		case "tool":
			// Tool result - merge into the corresponding tool use
			if currentExchange == nil {
				continue
			}

			// Extract tool result content
			for _, contentRaw := range data.Content {
				var contentItem struct {
					Type       string `json:"type"`
					ToolCallID string `json:"toolCallId"`
					ToolName   string `json:"toolName"`
					Result     string `json:"result"`
				}

				if err := json.Unmarshal(contentRaw, &contentItem); err != nil {
					continue
				}

				if contentItem.Type == "tool-result" && contentItem.ToolCallID != "" {
					// Find the pending tool and merge the result
					if pending, ok := pendingTools[contentItem.ToolCallID]; ok {
						// Merge result into the tool message
						mergeToolResultIntoMessage(currentExchange, pending.messageIdx, contentItem.Result, contentItem.ToolName)
						delete(pendingTools, contentItem.ToolCallID)
					}
				}
			}
		}
	}

	// Add the last exchange if it exists
	if currentExchange != nil && len(currentExchange.Messages) > 0 {
		exchanges = append(exchanges, *currentExchange)
	}

	// Blob records are already in DAG-sorted order, so exchanges are too
	return exchanges, nil
}

// parseFirstContent parses the first content item to check if message should be skipped
func parseFirstContent(contentRaw json.RawMessage) (ContentPart, bool) {
	var contentItem struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	if err := json.Unmarshal(contentRaw, &contentItem); err != nil {
		return ContentPart{}, false
	}

	// Skip if it's user_info
	if contentItem.Type == "text" && strings.HasPrefix(strings.TrimSpace(contentItem.Text), "<user_info>") {
		return ContentPart{Type: "text", Text: contentItem.Text}, true
	}

	return ContentPart{Type: contentItem.Type, Text: contentItem.Text}, false
}

// buildUserMessageFromBlob creates a Message from a user blob
func buildUserMessageFromBlob(content []json.RawMessage, rowID int) Message {
	var contentParts []ContentPart

	for _, contentRaw := range content {
		var contentItem struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}

		if err := json.Unmarshal(contentRaw, &contentItem); err != nil {
			continue
		}

		if contentItem.Type == "text" && contentItem.Text != "" {
			// Strip user_query tags if present
			cleanedText := stripUserQueryTags(contentItem.Text)
			if cleanedText != "" {
				contentParts = append(contentParts, ContentPart{
					Type: "text",
					Text: cleanedText,
				})
			}
		}
	}

	return Message{
		ID:      fmt.Sprintf("row-%d", rowID),
		Role:    "user",
		Content: contentParts,
	}
}

// buildAgentMessagesFromBlob creates Messages from an assistant blob
// When there are multiple tool-calls in a single assistant message, this creates
// one text message followed by separate messages for each tool call
func buildAgentMessagesFromBlob(content []json.RawMessage, rowID int, workspaceRoot string) []Message {
	var messages []Message
	var contentParts []ContentPart
	toolCallIndex := 0

	for _, contentRaw := range content {
		var contentItem struct {
			Type       string          `json:"type"`
			Text       string          `json:"text"`
			ToolName   string          `json:"toolName"`
			ToolCallID string          `json:"toolCallId"`
			Args       json.RawMessage `json:"args"`
		}

		if err := json.Unmarshal(contentRaw, &contentItem); err != nil {
			continue
		}

		switch contentItem.Type {
		case "text":
			if contentItem.Text != "" {
				contentParts = append(contentParts, ContentPart{
					Type: "text",
					Text: contentItem.Text,
				})
			}

		case "reasoning":
			// Preserve reasoning content as thinking type (schema canonical name)
			if contentItem.Text != "" {
				contentParts = append(contentParts, ContentPart{
					Type: "thinking",
					Text: contentItem.Text,
				})
			}

		case "tool-call":
			// Parse tool arguments
			var toolInput map[string]interface{}
			if err := json.Unmarshal(contentItem.Args, &toolInput); err != nil {
				continue
			}

			// Create a separate message for this tool call
			toolMsg := Message{
				ID:   fmt.Sprintf("row-%d-tool-%d", rowID, toolCallIndex),
				Role: "agent",
				Tool: &ToolInfo{
					Name:  contentItem.ToolName,
					Type:  classifyCursorToolType(contentItem.ToolName),
					UseID: contentItem.ToolCallID,
					Input: toolInput,
				},
				PathHints: extractCursorPathHints(toolInput, workspaceRoot),
			}

			messages = append(messages, toolMsg)
			toolCallIndex++
		}
	}

	// If there's text content, create a text message first
	if len(contentParts) > 0 {
		textMsg := Message{
			ID:      fmt.Sprintf("row-%d", rowID),
			Role:    "agent",
			Content: contentParts,
		}
		// Insert text message at the beginning
		messages = append([]Message{textMsg}, messages...)
	}

	// If no messages were created (shouldn't happen), return an empty text message
	if len(messages) == 0 {
		messages = append(messages, Message{
			ID:      fmt.Sprintf("row-%d", rowID),
			Role:    "agent",
			Content: []ContentPart{},
		})
	}

	return messages
}

// mergeToolResultIntoMessage merges a tool result into a tool use message
func mergeToolResultIntoMessage(exchange *Exchange, messageIdx int, result, toolName string) {
	if messageIdx >= len(exchange.Messages) {
		return
	}

	msg := &exchange.Messages[messageIdx]
	if msg.Tool == nil {
		return
	}

	// Skip TodoWrite errors
	if toolName == "TodoWrite" && strings.HasPrefix(result, "Invalid arguments:") {
		return
	}

	// Clean TodoWrite results
	if toolName == "TodoWrite" {
		result = cleanTodoResult(result)
	}

	// Add the output to the tool
	msg.Tool.Output = map[string]interface{}{
		"content": result,
	}
}

// classifyCursorToolType maps Cursor tool names to standard tool types
func classifyCursorToolType(toolName string) string {
	switch toolName {
	case "Write", "StrReplace", "MultiStrReplace":
		return "write"
	case "Read":
		return "read"
	case "Grep", "Glob":
		return "search"
	case "Shell", "LS":
		return "shell"
	case "Delete":
		return "write" // Delete is a write operation
	case "TodoWrite":
		return "task"
	default:
		return "unknown"
	}
}

// extractCursorPathHints extracts file paths from Cursor tool input
func extractCursorPathHints(input map[string]interface{}, workspaceRoot string) []string {
	var paths []string

	// Common path field names in Cursor tools
	pathFields := []string{"path", "file_path"}

	for _, field := range pathFields {
		if value, ok := input[field].(string); ok && value != "" {
			// Normalize path if possible
			normalizedPath := normalizeCursorPath(value, workspaceRoot)
			if !containsPath(paths, normalizedPath) {
				paths = append(paths, normalizedPath)
			}
		}
	}

	// Handle MultiStrReplace paths array
	if pathsArray, ok := input["paths"].([]interface{}); ok {
		for _, item := range pathsArray {
			if pathObj, ok := item.(map[string]interface{}); ok {
				if filePath, ok := pathObj["file_path"].(string); ok && filePath != "" {
					normalizedPath := normalizeCursorPath(filePath, workspaceRoot)
					if !containsPath(paths, normalizedPath) {
						paths = append(paths, normalizedPath)
					}
				}
			}
		}
	}

	return paths
}

// normalizeCursorPath converts absolute paths to workspace-relative paths when possible
func normalizeCursorPath(path, workspaceRoot string) string {
	if workspaceRoot == "" {
		return path
	}

	// If path is absolute and starts with workspace root, make it relative
	if filepath.IsAbs(path) && strings.HasPrefix(path, workspaceRoot) {
		relPath, err := filepath.Rel(workspaceRoot, path)
		if err == nil {
			return relPath
		}
	}

	return path
}

// containsPath checks if a string slice contains a path
func containsPath(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

// formatCursorToolWithSummary generates custom summary and formatted markdown for a Cursor tool
// Returns (summary, formattedMarkdown) where summary is the custom content for <summary> tag
// and formattedMarkdown is additional content to display in the tool-use block
func formatCursorToolWithSummary(tool *ToolInfo) (string, string) {
	var summary string
	var formattedMd strings.Builder

	// Format tool use based on tool name using existing Cursor formatters
	// These formatters return custom summary content (without "Tool use: **Name**" prefix)
	if tool.Input != nil {
		switch tool.Name {
		case "Write":
			// Write tool: summary has file path, formatted content has code block
			if path, ok := tool.Input["path"].(string); ok && path != "" {
				summary = fmt.Sprintf("`%s`", path)
			}
			formattedMd.WriteString(formatWriteTool(tool.Input))
		case "StrReplace":
			// StrReplace tool: summary is empty, formatted content has diff
			formattedMd.WriteString(formatStrReplaceTool(tool.Input))
		case "Delete":
			// Delete tool: summary has path, no formatted content
			summary = formatDeleteTool(tool.Input)
		case "Grep":
			// Grep tool: summary has search details, no formatted content
			summary = formatGrepTool(tool.Input)
		case "Glob":
			// Glob tool: summary has pattern, no formatted content
			summary = formatGlobTool(tool.Input)
		case "Read":
			// Read tool: summary has file path, no formatted content
			summary = formatReadTool(tool.Input)
		case "LS":
			// LS tool: summary has directory, no formatted content
			summary = formatLSTool(tool.Input)
		case "Shell":
			// Shell tool: summary has command, no formatted content
			summary = formatShellTool(tool.Input)
		case "MultiStrReplace":
			// MultiStrReplace tool: summary has file and edits, no formatted content
			summary = formatMultiStrReplaceTool(tool.Input)
		case "TodoWrite":
			// Handle TodoWrite specially
			if todos, ok := tool.Input["todos"].([]interface{}); ok && len(todos) > 0 {
				formattedMd.WriteString(formatTodoList(todos))
			}
		}
	}

	// Add tool result/output if present
	if tool.Output != nil {
		if content, ok := tool.Output["content"].(string); ok && content != "" {
			if formattedMd.Len() > 0 {
				formattedMd.WriteString("\n\n")
			}
			// Format based on tool type
			switch tool.Name {
			case "TodoWrite":
				// TodoWrite results are already formatted
				formattedMd.WriteString(content)
			case "Grep":
				// Grep results have special formatting
				formattedMd.WriteString(formatGrepResult(content))
			case "Glob":
				// Glob results are inline
				formattedMd.WriteString(strings.TrimSpace(content))
			case "Delete":
				// Delete results are inline
				formattedMd.WriteString(content)
			default:
				// Default: wrap in code block
				formattedMd.WriteString(formatToolResult(content))
			}
		}
	}

	// If we have a custom summary, prepend "Tool use: **ToolName**" prefix
	if summary != "" {
		summary = fmt.Sprintf("Tool use: **%s** %s", tool.Name, summary)
	}

	return summary, strings.TrimSpace(formattedMd.String())
}

// stripUserQueryTags removes <user_query> tags from text content
// These tags are added by Cursor CLI to wrap user input, but we don't want them in the markdown output
func stripUserQueryTags(text string) string {
	trimmed := strings.TrimSpace(text)

	// Check if text is wrapped with user_query tags
	if strings.HasPrefix(trimmed, "<user_query>") && strings.HasSuffix(trimmed, "</user_query>") {
		// Remove opening tag and any trailing newline
		content := strings.TrimPrefix(trimmed, "<user_query>")
		content = strings.TrimPrefix(content, "\n")

		// Remove closing tag and any preceding newline
		content = strings.TrimSuffix(content, "</user_query>")
		content = strings.TrimSuffix(content, "\n")

		return content
	}

	// Return original text if tags aren't present
	return text
}
