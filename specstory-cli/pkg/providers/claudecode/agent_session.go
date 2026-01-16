package claudecode

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

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

// GenerateAgentSession creates a SessionData from a Claude Code Session
// The session should already be parsed from JSONL with records in chronological order
func GenerateAgentSession(session Session, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "sessionID", session.SessionUuid, "recordCount", len(session.Records))

	if len(session.Records) == 0 {
		return nil, fmt.Errorf("session has no records")
	}

	// Extract metadata from records
	createdAt := extractCreatedAtFromRecords(session.Records)
	if workspaceRoot == "" {
		workspaceRoot = extractWorkspaceRootFromRecords(session.Records)
	}

	// Group records into exchanges
	exchanges, err := buildExchangesFromRecords(session.Records, workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to build exchanges: %w", err)
	}

	// Assign exchangeId to each exchange (format: sessionId:index)
	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", session.SessionUuid, i)
	}

	slog.Info("GenerateAgentSession: Built exchanges", "count", len(exchanges))

	// Populate FormattedMarkdown for all tools
	for i := range exchanges {
		for j := range exchanges[i].Messages {
			msg := &exchanges[i].Messages[j]
			if msg.Tool != nil {
				formattedMd := formatToolAsMarkdown(msg.Tool, workspaceRoot)
				msg.Tool.FormattedMarkdown = &formattedMd
			}
		}
	}

	sessionData := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "claude-code",
			Name:    "Claude Code",
			Version: "unknown",
		},
		SessionID:     session.SessionUuid,
		CreatedAt:     createdAt,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}

	return sessionData, nil
}

// extractCreatedAtFromRecords extracts the earliest timestamp from JSONL records
func extractCreatedAtFromRecords(records []JSONLRecord) string {
	if len(records) == 0 {
		return time.Now().Format(time.RFC3339)
	}

	// Get timestamp from first record
	if timestamp, ok := records[0].Data["timestamp"].(string); ok && timestamp != "" {
		return timestamp
	}

	return time.Now().Format(time.RFC3339)
}

// extractWorkspaceRootFromRecords extracts the workspace root from JSONL records
func extractWorkspaceRootFromRecords(records []JSONLRecord) string {
	for _, record := range records {
		if cwd, ok := record.Data["cwd"].(string); ok && cwd != "" {
			return cwd
		}
	}
	return ""
}

// buildExchangesFromRecords groups JSONL records into exchanges
// An exchange is a user prompt followed by agent responses and tool results
// Records are assumed to be in chronological order from JSONL parsing
func buildExchangesFromRecords(records []JSONLRecord, workspaceRoot string) ([]Exchange, error) {
	var exchanges []Exchange
	var currentExchange *Exchange
	var exchangeStartTime string

	for _, record := range records {
		recordType, _ := record.Data["type"].(string)
		timestamp, _ := record.Data["timestamp"].(string)

		// Check if this is a sidechain message (subagent conversation)
		// Note: warmup messages are filtered earlier by filterWarmupMessages
		isSidechain, _ := record.Data["isSidechain"].(bool)

		switch recordType {
		case "user":
			// Check if this is a tool result or a new user message
			message, _ := record.Data["message"].(map[string]interface{})
			messageContent, _ := message["content"].([]interface{})

			isToolResult := false
			if len(messageContent) > 0 {
				if firstContent, ok := messageContent[0].(map[string]interface{}); ok {
					if contentType, ok := firstContent["type"].(string); ok && contentType == "tool_result" {
						isToolResult = true
					}
				}
			}

			if isToolResult {
				// This is a tool result - merge it into the previous tool use
				if currentExchange != nil {
					mergeToolResultIntoExchange(currentExchange, messageContent)
					currentExchange.EndTime = timestamp
				}
			} else {
				// This is a new user prompt

				// Sidechain messages append to current exchange (parent agent talking to subagent)
				// rather than starting a new exchange like regular user prompts do
				if isSidechain {
					if currentExchange == nil {
						slog.Warn("Sidechain user message with no current exchange", "uuid", record.Data["uuid"])
						continue
					}
					msg := buildUserMessage(record, isSidechain)
					currentExchange.Messages = append(currentExchange.Messages, msg)
					currentExchange.EndTime = timestamp
					continue
				}

				// Non-sidechain: start a new exchange
				if currentExchange != nil {
					exchanges = append(exchanges, *currentExchange)
				}

				currentExchange = &Exchange{
					StartTime: timestamp,
					Messages:  []Message{buildUserMessage(record, isSidechain)},
				}
			}

		case "assistant":
			// Agent message - add to current exchange
			if currentExchange == nil {
				// Create exchange if we don't have one (shouldn't happen normally)
				exchangeStartTime = timestamp
				currentExchange = &Exchange{
					StartTime: exchangeStartTime,
					Messages:  []Message{},
				}
			}

			msg := buildAgentMessage(record, workspaceRoot, isSidechain)
			currentExchange.Messages = append(currentExchange.Messages, msg)
			currentExchange.EndTime = timestamp
		}
	}

	// Add the last exchange if exists
	if currentExchange != nil && len(currentExchange.Messages) > 0 {
		exchanges = append(exchanges, *currentExchange)
	}

	// Records are already in chronological order from JSONL parsing,
	// so exchanges are also in order. No sorting needed.

	return exchanges, nil
}

// mergeToolResultIntoExchange finds the tool use in the exchange and adds the result to it
func mergeToolResultIntoExchange(exchange *Exchange, messageContent []interface{}) {
	// Extract tool_result data from message content
	if len(messageContent) == 0 {
		return
	}

	toolResultMap, ok := messageContent[0].(map[string]interface{})
	if !ok {
		return
	}

	// Get the tool_use_id that this result corresponds to
	toolUseID, ok := toolResultMap["tool_use_id"].(string)
	if !ok || toolUseID == "" {
		return
	}

	// Extract result content and error status
	// Content can be either a string or an array of text objects (for Task tool results)
	var content string
	if contentStr, ok := toolResultMap["content"].(string); ok {
		content = contentStr
	} else if contentArray, ok := toolResultMap["content"].([]interface{}); ok {
		// Array of text objects like [{"type":"text","text":"..."}]
		var parts []string
		for _, item := range contentArray {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if text, ok := itemMap["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		content = strings.Join(parts, "\n")
	}
	isError, _ := toolResultMap["is_error"].(bool)

	// Find the message with matching tool use ID and add the output
	for i := range exchange.Messages {
		msg := &exchange.Messages[i]
		if msg.Tool != nil && msg.Tool.UseID == toolUseID {
			// Add the output to this tool
			msg.Tool.Output = map[string]interface{}{
				"content":  content,
				"is_error": isError,
			}
			return
		}
	}
}

// buildUserMessage creates a Message from a user JSONL record
func buildUserMessage(record JSONLRecord, isSidechain bool) Message {
	uuid, _ := record.Data["uuid"].(string)
	timestamp, _ := record.Data["timestamp"].(string)

	message, _ := record.Data["message"].(map[string]interface{})
	var contentParts []ContentPart

	// Handle different content formats
	if content, ok := message["content"].(string); ok {
		// Simple string content
		contentParts = append(contentParts, ContentPart{
			Type: "text",
			Text: content,
		})
	} else if contentArray, ok := message["content"].([]interface{}); ok {
		// Array of content parts
		for _, item := range contentArray {
			if contentMap, ok := item.(map[string]interface{}); ok {
				contentType, _ := contentMap["type"].(string)

				switch contentType {
				case "text":
					text, _ := contentMap["text"].(string)
					contentParts = append(contentParts, ContentPart{
						Type: "text",
						Text: text,
					})
				case "thinking":
					thinking, _ := contentMap["thinking"].(string)
					contentParts = append(contentParts, ContentPart{
						Type: "thinking",
						Text: thinking,
					})
					// tool_result is now merged into tool use, not handled here
				}
			}
		}
	}

	msg := Message{
		ID:        uuid,
		Timestamp: timestamp,
		Role:      "user",
		Content:   contentParts,
	}

	// For sidechain messages, treat as agent (parent agent talking to subagent, not actual user input)
	if isSidechain {
		msg.Role = "agent"
		msg.Metadata = map[string]interface{}{
			"isSidechain": true,
		}
	}

	return msg
}

// buildAgentMessage creates a Message from an assistant JSONL record
func buildAgentMessage(record JSONLRecord, workspaceRoot string, isSidechain bool) Message {
	uuid, _ := record.Data["uuid"].(string)
	timestamp, _ := record.Data["timestamp"].(string)

	message, _ := record.Data["message"].(map[string]interface{})
	model, _ := message["model"].(string)

	var contentParts []ContentPart
	var toolInfo *ToolInfo
	var pathHints []string

	// Parse message content
	if contentArray, ok := message["content"].([]interface{}); ok {
		for _, item := range contentArray {
			if contentMap, ok := item.(map[string]interface{}); ok {
				contentType, _ := contentMap["type"].(string)

				switch contentType {
				case "text":
					text, _ := contentMap["text"].(string)
					if text != "" {
						contentParts = append(contentParts, ContentPart{
							Type: "text",
							Text: text,
						})
					}

				case "thinking":
					// Preserve thinking content with its type
					thinking, _ := contentMap["thinking"].(string)
					if thinking != "" {
						contentParts = append(contentParts, ContentPart{
							Type: "thinking",
							Text: thinking,
						})
					}

				case "tool_use":
					// Extract tool information
					toolName, _ := contentMap["name"].(string)
					toolUseID, _ := contentMap["id"].(string)
					toolInput, _ := contentMap["input"].(map[string]interface{})

					toolInfo = &ToolInfo{
						Name:  toolName,
						Type:  classifyToolType(toolName),
						UseID: toolUseID,
						Input: toolInput,
					}

					// Extract path hints from tool input
					pathHints = extractPathHints(toolInput, workspaceRoot)
				}
			}
		}
	}

	// Also check for tool use result in the record
	if toolUseResult, ok := record.Data["toolUseResult"].(map[string]interface{}); ok {
		if toolInfo != nil {
			// Add output to existing tool info
			toolInfo.Output = map[string]interface{}{
				"type":     toolUseResult["type"],
				"filePath": toolUseResult["filePath"],
			}
		}
	}

	msg := Message{
		ID:        uuid,
		Timestamp: timestamp,
		Role:      "agent",
		Model:     model,
		Content:   contentParts,
		Tool:      toolInfo,
		PathHints: pathHints,
	}

	// Add sidechain flag to metadata if true
	if isSidechain {
		msg.Metadata = map[string]interface{}{
			"isSidechain": true,
		}
	}

	return msg
}

// classifyToolType maps tool names to standard tool types
func classifyToolType(toolName string) string {
	toolName = strings.ToLower(toolName)

	switch toolName {
	// "str_replace_editor", "write_file", "edit_file", "create_file", "append_to_file" are legacy tools, no longer in the agent
	case "write", "edit", "multiedit", "str_replace_editor", "write_file", "edit_file", "create_file", "append_to_file":
		return "write"
	case "read", "webfetch", "read_file", "view_file", "cat": // "read_file", "view_file", "cat" are legacy tools, no longer in the agent
		return "read"
	case "grep", "glob", "websearch":
		return "search"
	case "bash", "taskoutput", "killshell":
		return "shell"
	case "notebookedit", "todowrite":
		return "task"
	case "enterplanmode", "exitplanmode", "askuserquestion", "skill", "lsp", "task":
		return "generic"
	default:
		return "unknown"
	}
}

// extractPathHints extracts file paths from tool input
func extractPathHints(input map[string]interface{}, workspaceRoot string) []string {
	var paths []string

	// Common path field names
	pathFields := []string{"file_path", "path", "filename", "old_path", "new_path"}
	arrayFields := []string{"paths", "files", "targets"}

	// Extract single path fields
	for _, field := range pathFields {
		if value, ok := input[field].(string); ok && value != "" {
			// Normalize path if possible
			normalizedPath := normalizePath(value, workspaceRoot)
			if !contains(paths, normalizedPath) {
				paths = append(paths, normalizedPath)
			}
		}
	}

	// Extract array path fields
	for _, field := range arrayFields {
		if arr, ok := input[field].([]interface{}); ok {
			for _, item := range arr {
				if path, ok := item.(string); ok && path != "" {
					normalizedPath := normalizePath(path, workspaceRoot)
					if !contains(paths, normalizedPath) {
						paths = append(paths, normalizedPath)
					}
				}
			}
		}
	}

	return paths
}

// normalizePath converts absolute paths to workspace-relative paths when possible
func normalizePath(path, workspaceRoot string) string {
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

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

// formatToolAsMarkdown generates formatted markdown for a tool (input + output)
// Returns the inner content only (no <tool-use> tags)
func formatToolAsMarkdown(tool *ToolInfo, workspaceRoot string) string {
	var markdown strings.Builder

	// Add _cwd to input for path normalization in formatters
	if tool.Input == nil {
		tool.Input = make(map[string]interface{})
	}
	tool.Input["_cwd"] = workspaceRoot

	// Extract description if present
	description := ""
	if desc, ok := tool.Input["description"].(string); ok {
		description = desc
	}

	// Format tool use based on tool name using existing formatters
	var toolContent string
	if formatter, ok := toolFormatters[tool.Name]; ok {
		// Use tool-specific formatter
		toolContent = formatter(tool.Name, tool.Input, description)
	} else {
		// Use default formatter
		toolContent = formatDefaultTool(tool.Name, tool.Input, description)
	}

	// Add formatted tool content
	if toolContent != "" {
		markdown.WriteString(toolContent)
		markdown.WriteString("\n")
	}

	// Add tool result/output if present
	if tool.Output != nil {
		// Check for error first
		if isError, ok := tool.Output["is_error"].(bool); ok && isError {
			markdown.WriteString("```\n")
			if content, ok := tool.Output["content"].(string); ok {
				cleaned := stripSystemReminders(content)
				cleaned = stripANSIEscapeSequences(cleaned)
				markdown.WriteString(cleaned)
			}
			markdown.WriteString("\n```\n")
		} else {
			// Regular result
			if content, ok := tool.Output["content"].(string); ok {
				cleaned := stripSystemReminders(content)
				cleaned = stripANSIEscapeSequences(cleaned)
				// Only trim trailing spaces/tabs, preserve all newlines
				cleaned = strings.TrimRight(cleaned, " \t")

				// Special handling for AskUserQuestion - parse and format the answer nicely
				if tool.Name == "AskUserQuestion" {
					answer := parseAskUserQuestionAnswer(cleaned)
					if answer != "" {
						markdown.WriteString(fmt.Sprintf("\n**Answer:** %s\n", answer))
					} else if cleaned != "" {
						// Fallback to code block if parsing fails
						markdown.WriteString("```\n")
						markdown.WriteString(cleaned)
						markdown.WriteString("\n```\n")
					}
				} else if strings.TrimSpace(cleaned) != TodoWriteSuccessMessage && cleaned != "" {
					// Skip TodoWrite success messages
					markdown.WriteString("```\n")
					markdown.WriteString(cleaned)
					markdown.WriteString("\n```\n")
				}
			}
		}
	}

	return markdown.String()
}
