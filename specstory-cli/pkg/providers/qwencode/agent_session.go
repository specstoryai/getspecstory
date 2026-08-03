package qwencode

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Type aliases for convenience - use the shared schema types
type (
	SessionData  = schema.SessionData
	ProviderInfo = schema.ProviderInfo
	Exchange     = schema.Exchange
	Message      = schema.Message
	ContentPart  = schema.ContentPart
	ToolInfo     = schema.ToolInfo
	Usage        = schema.Usage
)

// convertUsageMetadata converts Qwen's token accounting (Gemini CLI field
// names) to the shared Usage type. Returns nil when usage is nil.
func convertUsageMetadata(usage *QwenUsageMetadata) *Usage {
	if usage == nil {
		return nil
	}
	return &Usage{
		InputTokens:   usage.PromptTokenCount,
		OutputTokens:  usage.CandidatesTokenCount,
		CachedTokens:  usage.CachedContentTokenCount,
		ThoughtTokens: usage.ThoughtsTokenCount,
		ToolTokens:    usage.ToolsTokenCount,
	}
}

// GenerateAgentSession creates a SessionData from a parsed QwenSession.
func GenerateAgentSession(session *QwenSession, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "sessionID", session.ID, "entryCount", len(session.Entries))

	if len(session.Entries) == 0 {
		return nil, fmt.Errorf("session has no entries")
	}

	createdAt := session.StartTime
	if createdAt == "" {
		createdAt = session.LastUpdated
	}

	exchanges := buildExchangesFromEntries(session.Entries, workspaceRoot)
	if len(exchanges) == 0 {
		return nil, fmt.Errorf("session has no conversation exchanges")
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

	version := session.Version
	if version == "" {
		version = "unknown"
	}

	sessionData := &SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		Provider: ProviderInfo{
			ID:      "qwen",
			Name:    "Qwen Code",
			Version: version,
		},
		SessionID:     session.ID,
		CreatedAt:     createdAt,
		UpdatedAt:     session.LastUpdated,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}

	return sessionData, nil
}

// buildExchangesFromEntries groups transcript records into exchanges. An
// exchange starts with a real user message and includes all subsequent agent
// messages until the next real user message. Tool results are folded into the
// tool messages they answer.
func buildExchangesFromEntries(entries []QwenSessionEntry, workspaceRoot string) []Exchange {
	var exchanges []Exchange
	var currentExchange *Exchange

	// Pending tool calls awaiting their result, keyed by call id. Values point
	// at the shared ToolInfo (Message copies in the exchange all reference the
	// same pointer), so results land regardless of slice reallocation.
	pendingTools := make(map[string]*ToolInfo)

	for _, entry := range entries {
		switch {
		case entry.Type == entryTypeUser && entry.Provenance == provenanceRealUser:
			// Start a new exchange
			if currentExchange != nil && len(currentExchange.Messages) > 0 {
				exchanges = append(exchanges, *currentExchange)
			}

			currentExchange = &Exchange{
				StartTime: entry.Timestamp,
				EndTime:   entry.Timestamp,
				Messages:  []Message{},
			}
			pendingTools = make(map[string]*ToolInfo)

			userMsg := buildUserMessage(entry)
			if userMsg != nil {
				currentExchange.Messages = append(currentExchange.Messages, *userMsg)
			}

		case entry.Type == entryTypeAssistant:
			if currentExchange == nil {
				// Agent output before any user message (shouldn't happen normally)
				currentExchange = &Exchange{
					StartTime: entry.Timestamp,
					Messages:  []Message{},
				}
			}

			agentMsgs := buildAgentMessages(entry, workspaceRoot, pendingTools)
			currentExchange.Messages = append(currentExchange.Messages, agentMsgs...)
			currentExchange.EndTime = entry.Timestamp

		case entry.Type == entryTypeToolResult:
			if currentExchange == nil {
				continue
			}
			applyToolResults(entry, pendingTools)
			currentExchange.EndTime = entry.Timestamp
		}
	}

	if currentExchange != nil && len(currentExchange.Messages) > 0 {
		exchanges = append(exchanges, *currentExchange)
	}

	return exchanges
}

// buildUserMessage creates a user Message from a real-user record. Returns nil
// when the record carries no text.
func buildUserMessage(entry QwenSessionEntry) *Message {
	content := strings.TrimSpace(entryText(entry))
	if content == "" {
		return nil
	}

	return &Message{
		ID:        entry.UUID,
		Timestamp: entry.Timestamp,
		Role:      schema.RoleUser,
		Content: []ContentPart{
			{Type: schema.ContentTypeText, Text: content},
		},
	}
}

// buildAgentMessages creates Messages from an assistant record: one thinking
// message, one message per tool call, and one text message. Token usage is
// attached to the last message only to avoid double-counting in aggregation.
func buildAgentMessages(entry QwenSessionEntry, workspaceRoot string, pendingTools map[string]*ToolInfo) []Message {
	if entry.Message == nil {
		return nil
	}

	var messages []Message
	usage := convertUsageMetadata(entry.UsageMetadata)

	var thinkingParts []string
	var textParts []string

	for _, part := range entry.Message.Parts {
		switch {
		case part.FunctionCall != nil:
			toolMsg := buildToolMessage(entry, *part.FunctionCall, workspaceRoot)
			pendingTools[part.FunctionCall.ID] = toolMsg.Tool
			messages = append(messages, toolMsg)

		case part.Text != nil && part.Thought:
			if trimmed := strings.TrimSpace(*part.Text); trimmed != "" {
				thinkingParts = append(thinkingParts, trimmed)
			}

		case part.Text != nil:
			if trimmed := strings.TrimSpace(*part.Text); trimmed != "" {
				textParts = append(textParts, trimmed)
			}
		}
	}

	// Thinking first (it precedes the visible response)
	if len(thinkingParts) > 0 {
		messages = append([]Message{{
			ID:        entry.UUID,
			Timestamp: entry.Timestamp,
			Role:      schema.RoleAgent,
			Model:     entry.Model,
			Content: []ContentPart{
				{Type: schema.ContentTypeThinking, Text: strings.Join(thinkingParts, "\n\n")},
			},
		}}, messages...)
	}

	// Visible text last
	if len(textParts) > 0 {
		messages = append(messages, Message{
			ID:        entry.UUID,
			Timestamp: entry.Timestamp,
			Role:      schema.RoleAgent,
			Model:     entry.Model,
			Content: []ContentPart{
				{Type: schema.ContentTypeText, Text: strings.Join(textParts, "\n\n")},
			},
		})
	}

	// Attach usage to the last message only to avoid double-counting
	if len(messages) > 0 && usage != nil {
		messages[len(messages)-1].Usage = usage
	}

	return messages
}

// buildToolMessage creates a Message for a single tool call.
func buildToolMessage(entry QwenSessionEntry, call QwenFunctionCall, workspaceRoot string) Message {
	toolInfo := &ToolInfo{
		Name:  call.Name,
		Type:  classifyQwenToolType(call.Name),
		UseID: call.ID,
		Input: call.Args,
	}

	pathHints := extractPathHintsFromTool(call, workspaceRoot)

	return Message{
		ID:        call.ID,
		Timestamp: entry.Timestamp,
		Role:      schema.RoleAgent,
		Model:     entry.Model,
		Tool:      toolInfo,
		PathHints: pathHints,
	}
}

// applyToolResults folds a tool_result record into the pending tool messages:
// each functionResponse part answers the tool call with the same id. Results
// for calls not present in the transcript (e.g. it started mid-session) are
// dropped, since a response without its request cannot be rendered.
func applyToolResults(entry QwenSessionEntry, pendingTools map[string]*ToolInfo) {
	output := toolResultOutput(entry)

	if entry.Message != nil {
		for _, part := range entry.Message.Parts {
			if part.FunctionResponse == nil {
				continue
			}
			if toolInfo, ok := pendingTools[part.FunctionResponse.ID]; ok {
				toolInfo.Output = toolOutputMap(part.FunctionResponse.Response, output)
				delete(pendingTools, part.FunctionResponse.ID)
			}
		}
	}
}

// toolResultOutput extracts the best human-readable output text from a
// tool_result record, preferring the structured resultDisplay.
func toolResultOutput(entry QwenSessionEntry) string {
	if entry.ToolCallResult != nil {
		if display := strings.TrimSpace(entry.ToolCallResult.ResultDisplay); display != "" {
			return display
		}
	}

	if entry.Message != nil {
		for _, part := range entry.Message.Parts {
			if part.FunctionResponse == nil || part.FunctionResponse.Response == nil {
				continue
			}
			if out, ok := part.FunctionResponse.Response["output"].(string); ok && strings.TrimSpace(out) != "" {
				return out
			}
			if errStr, ok := part.FunctionResponse.Response["error"].(string); ok && strings.TrimSpace(errStr) != "" {
				return errStr
			}
		}
	}
	return ""
}

// toolOutputMap builds the Output map for a tool, preferring the structured
// functionResponse payload and falling back to the result display text.
func toolOutputMap(response map[string]any, display string) map[string]interface{} {
	if len(response) > 0 {
		output := make(map[string]interface{}, len(response))
		for key, value := range response {
			output[key] = value
		}
		return output
	}
	if display != "" {
		return map[string]interface{}{"output": display}
	}
	return nil
}

// classifyQwenToolType maps Qwen Code tool names to standard tool types.
// Valid types: write, read, search, shell, task, generic, unknown
func classifyQwenToolType(toolName string) string {
	switch toolName {
	case "read_file", "web_fetch", "read_many_files":
		return "read"
	case "write_file", "edit", "notebook_edit":
		return "write"
	case "run_shell_command", "list_directory":
		return "shell"
	case "glob", "grep_search", "search_file_content", "web_search":
		return "search"
	case "todo_write", "write_todos", "task":
		return "task"
	case "save_memory", "skill", "ask_user_question":
		return "generic"
	default:
		return "unknown"
	}
}

// extractPathHintsFromTool extracts file paths from tool call args.
func extractPathHintsFromTool(call QwenFunctionCall, workspaceRoot string) []string {
	var paths []string

	// Common path field names for Qwen Code tools
	pathFields := []string{"file_path", "dir_path", "path", "notebook_path"}

	for _, field := range pathFields {
		if value := argAsString(call.Args, field); value != "" {
			normalizedPath := spi.NormalizePath(value, workspaceRoot)
			if !containsString(paths, normalizedPath) {
				paths = append(paths, normalizedPath)
			}
		}
	}

	// Extract paths from shell commands (redirect targets, file-creating commands)
	if command := argAsString(call.Args, "command"); command != "" {
		cwd := argAsString(call.Args, "directory")
		if cwd == "" {
			cwd = workspaceRoot
		}
		shellPaths := spi.ExtractShellPathHints(command, cwd, workspaceRoot)
		for _, sp := range shellPaths {
			if !containsString(paths, sp) {
				paths = append(paths, sp)
			}
		}
	}

	return paths
}

// argAsString extracts a string value from tool call args.
func argAsString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return str
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
