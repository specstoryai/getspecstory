package geminicli

import (
	"fmt"
	"html"
	"log/slog"
	"path/filepath"
	"slices"
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

// GenerateAgentSession creates a SessionData from a GeminiSession
// The session should already be parsed and normalized (messages sorted, warmup trimmed)
func GenerateAgentSession(session *GeminiSession, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "sessionID", session.ID, "messageCount", len(session.Messages))

	if len(session.Messages) == 0 {
		return nil, fmt.Errorf("session has no messages")
	}

	// Use session's start time as createdAt
	createdAt := session.StartTime
	if createdAt == "" {
		createdAt = session.LastUpdated
	}

	// Build exchanges from messages
	exchanges, err := buildExchangesFromMessages(session.Messages, workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to build exchanges: %w", err)
	}

	// Assign exchangeId to each exchange (format: sessionId:index)
	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", session.ID, i)
	}

	slog.Info("GenerateAgentSession: Built exchanges", "count", len(exchanges))

	// Populate FormattedMarkdown for all tools
	for i := range exchanges {
		for j := range exchanges[i].Messages {
			msg := &exchanges[i].Messages[j]
			if msg.Tool != nil {
				formattedMd := formatToolAsMarkdown(msg.Tool)
				msg.Tool.FormattedMarkdown = &formattedMd
			}
		}
	}

	sessionData := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "gemini-cli",
			Name:    "Gemini CLI",
			Version: "unknown",
		},
		SessionID:     session.ID,
		CreatedAt:     createdAt,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}

	return sessionData, nil
}

// buildExchangesFromMessages groups GeminiMessages into exchanges
// An exchange starts with a user message and includes all subsequent agent messages until the next user message
func buildExchangesFromMessages(messages []GeminiMessage, workspaceRoot string) ([]Exchange, error) {
	var exchanges []Exchange
	var currentExchange *Exchange

	for _, msg := range messages {
		switch msg.Type {
		case "user":
			// Start a new exchange
			if currentExchange != nil && len(currentExchange.Messages) > 0 {
				exchanges = append(exchanges, *currentExchange)
			}

			currentExchange = &Exchange{
				StartTime: msg.Timestamp,
				Messages:  []Message{},
			}

			// Build user message and handle referenced files extraction
			userMsg, refMsgs := buildUserMessageWithReferences(msg)
			currentExchange.Messages = append(currentExchange.Messages, userMsg)

			// Add synthetic agent messages for referenced files
			currentExchange.Messages = append(currentExchange.Messages, refMsgs...)

		case "gemini":
			// Agent message - add to current exchange
			if currentExchange == nil {
				// Create exchange if we don't have one (shouldn't happen normally)
				currentExchange = &Exchange{
					StartTime: msg.Timestamp,
					Messages:  []Message{},
				}
			}

			// Add messages for agent response (may produce multiple messages for tool calls)
			agentMsgs := buildAgentMessages(msg, workspaceRoot)
			currentExchange.Messages = append(currentExchange.Messages, agentMsgs...)
			currentExchange.EndTime = msg.Timestamp
		}
	}

	// Add the last exchange if exists
	if currentExchange != nil && len(currentExchange.Messages) > 0 {
		exchanges = append(exchanges, *currentExchange)
	}

	return exchanges, nil
}

// buildUserMessageWithReferences creates a user Message and extracts referenced file content
// Returns the user message and any synthetic agent messages for referenced files
func buildUserMessageWithReferences(msg GeminiMessage) (Message, []Message) {
	content := strings.TrimSpace(msgContent(msg))

	// Extract referenced file sections from user message
	mainContent, refSections := extractReferencedSections(content)

	var contentParts []ContentPart
	if mainContent != "" {
		contentParts = append(contentParts, ContentPart{
			Type: "text",
			Text: mainContent,
		})
	}

	userMsg := Message{
		ID:        msg.ID,
		Timestamp: msg.Timestamp,
		Role:      "user",
		Content:   contentParts,
	}

	// Build synthetic agent messages for each referenced file section
	var refMsgs []Message
	for _, refContent := range refSections {
		refMsg := buildReferencedFilesMessage(refContent, msg.Timestamp)
		refMsgs = append(refMsgs, refMsg)
	}

	return userMsg, refMsgs
}

// buildReferencedFilesMessage creates a synthetic agent message for referenced file content
func buildReferencedFilesMessage(content string, timestamp string) Message {
	// Format the content with HTML escaping
	formattedContent := formatReferencedFilesMarkdown(content)

	tool := &ToolInfo{
		Name:  "referenced_files",
		Type:  "read",
		UseID: fmt.Sprintf("ref_%s", timestamp),
		Input: map[string]interface{}{
			"content": content,
		},
		FormattedMarkdown: &formattedContent,
	}

	return Message{
		Timestamp: timestamp,
		Role:      "agent",
		Tool:      tool,
	}
}

// formatReferencedFilesMarkdown formats referenced file content for markdown display
func formatReferencedFilesMarkdown(content string) string {
	if content == "" {
		return ""
	}
	return fmt.Sprintf("\n\n<pre><code>%s</code></pre>\n", html.EscapeString(content))
}

// buildAgentMessages creates Messages from a Gemini agent message
// Returns multiple messages: one for thoughts, one per tool call, and one for text content
func buildAgentMessages(msg GeminiMessage, workspaceRoot string) []Message {
	var messages []Message

	// Add thinking content from Thoughts
	if len(msg.Thoughts) > 0 {
		thinkingMsg := buildThinkingMessage(msg)
		messages = append(messages, thinkingMsg)
	}

	// Add a message for each tool call
	for _, toolCall := range msg.ToolCalls {
		toolMsg := buildToolMessage(msg, toolCall, workspaceRoot)
		messages = append(messages, toolMsg)
	}

	// Add text content message if present
	textContent := strings.TrimSpace(msgContent(msg))
	if textContent != "" {
		textMsg := Message{
			ID:        msg.ID,
			Timestamp: msg.Timestamp,
			Role:      "agent",
			Model:     msg.Model,
			Content: []ContentPart{
				{Type: "text", Text: textContent},
			},
		}
		messages = append(messages, textMsg)
	}

	return messages
}

// buildThinkingMessage creates a Message containing thinking content from GeminiThoughts
func buildThinkingMessage(msg GeminiMessage) Message {
	var thinkingText strings.Builder
	for _, thought := range msg.Thoughts {
		subject := strings.TrimSpace(thought.Subject)
		if subject == "" {
			subject = "Thought"
		}
		description := strings.TrimSpace(thought.Description)
		if description == "" {
			description = "(no description)"
		}
		thinkingText.WriteString(fmt.Sprintf("- **%s** — %s\n", subject, description))
	}

	// Trim trailing newline to avoid double-newline when markdown adds closing tag
	text := strings.TrimSuffix(thinkingText.String(), "\n")

	return Message{
		ID:        msg.ID,
		Timestamp: msg.Timestamp,
		Role:      "agent",
		Model:     msg.Model,
		Content: []ContentPart{
			{Type: "thinking", Text: text},
		},
	}
}

// buildToolMessage creates a Message for a single tool call
func buildToolMessage(msg GeminiMessage, toolCall GeminiToolCall, workspaceRoot string) Message {
	// Convert GeminiToolCall to ToolInfo
	toolInfo := convertToolCall(toolCall)

	// Extract path hints from tool input
	pathHints := extractPathHintsFromTool(toolCall, workspaceRoot)

	return Message{
		ID:        toolCall.ID,
		Timestamp: toolCall.Timestamp,
		Role:      "agent",
		Model:     msg.Model,
		Tool:      toolInfo,
		PathHints: pathHints,
	}
}

// convertToolCall converts a GeminiToolCall to a ToolInfo
func convertToolCall(toolCall GeminiToolCall) *ToolInfo {
	// Build output from tool results
	var output map[string]interface{}
	if len(toolCall.Result) > 0 && toolCall.Result[0].FunctionResponse != nil {
		output = toolCall.Result[0].FunctionResponse.Response
	}

	// If no structured output, try ResultDisplay
	if output == nil && toolCall.ResultDisplay != "" {
		output = map[string]interface{}{
			"output": toolCall.ResultDisplay,
		}
	}

	return &ToolInfo{
		Name:   toolCall.Name,
		Type:   classifyGeminiToolType(toolCall.Name),
		UseID:  toolCall.ID,
		Input:  toolCall.Args,
		Output: output,
	}
}

// classifyGeminiToolType maps Gemini tool names to standard tool types
// Valid types: write, read, search, shell, task, generic, unknown
func classifyGeminiToolType(toolName string) string {
	switch toolName {
	case "read_file", "read_many_files", "web_fetch":
		return "read"
	case "write_file", "replace", "smart_edit":
		return "write"
	case "run_shell_command", "list_directory":
		return "shell"
	case "search_file_content", "google_web_search", "glob":
		return "search"
	case "write_todos":
		return "task"
	case "save_memory", "codebase_investigator", "delegate_to_agent":
		return "generic"
	default:
		return "unknown"
	}
}

// extractPathHintsFromTool extracts file paths from Gemini tool call args
func extractPathHintsFromTool(toolCall GeminiToolCall, workspaceRoot string) []string {
	var paths []string

	// Common path field names for Gemini tools
	pathFields := []string{"file_path", "dir_path", "path"}

	for _, field := range pathFields {
		if value := inputAsString(toolCall.Args, field); value != "" {
			normalizedPath := normalizeGeminiPath(value, workspaceRoot)
			if !slices.Contains(paths, normalizedPath) {
				paths = append(paths, normalizedPath)
			}
		}
	}

	return paths
}

// normalizeGeminiPath converts absolute paths to workspace-relative paths when possible
func normalizeGeminiPath(path, workspaceRoot string) string {
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

// extractReferencedSections extracts "--- Content from referenced files ---" sections from a message
// Returns the main content (without markers) and the extracted reference sections
func extractReferencedSections(message string) (string, []string) {
	const marker = "--- Content from referenced files ---"
	var sections []string
	remaining := message

	for {
		idx := strings.Index(remaining, marker)
		if idx == -1 {
			break
		}
		before := strings.TrimRight(remaining[:idx], "\n ")
		after := remaining[idx+len(marker):]

		next := strings.Index(after, marker)
		var block string
		if next == -1 {
			block = strings.TrimSpace(after)
			remaining = before
		} else {
			block = strings.TrimSpace(after[:next])
			remaining = strings.TrimSpace(before + after[next:])
		}

		if block != "" {
			sections = append(sections, block)
		}
	}

	return strings.TrimSpace(remaining), sections
}
