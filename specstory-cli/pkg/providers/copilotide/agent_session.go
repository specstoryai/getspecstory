package copilotide

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ConvertToSessionData converts VS Code raw format to CLI's unified schema
func ConvertToSessionData(composer VSCodeComposer, projectPath string) spi.AgentChatSession {
	// Format timestamps
	createdAt := FormatTimestamp(composer.CreationDate)
	updatedAt := FormatTimestamp(composer.LastMessageDate)

	// Generate slug
	slug := GenerateSlug(composer)

	// Build SessionData
	sessionData := &schema.SessionData{
		SchemaVersion: "1.0",
		Provider: schema.ProviderInfo{
			ID:      "copilotide",
			Name:    "copilotide",
			Version: "1.0",
		},
		SessionID:     composer.SessionID,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		Slug:          slug,
		WorkspaceRoot: projectPath,
		Exchanges:     ConvertRequestsToExchanges(composer.Requests),
	}

	// Marshal to JSON for raw data
	rawDataJSON, err := json.Marshal(composer)
	if err != nil {
		slog.Warn("Failed to marshal raw data", "sessionId", composer.SessionID, "error", err)
		rawDataJSON = []byte("{}")
	}

	return spi.AgentChatSession{
		SessionID:   composer.SessionID,
		CreatedAt:   createdAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataJSON),
	}
}

// ConvertRequestsToExchanges converts VS Code request blocks to exchanges
func ConvertRequestsToExchanges(requests []VSCodeRequestBlock) []schema.Exchange {
	var exchanges []schema.Exchange

	for _, req := range requests {
		exchange := schema.Exchange{
			ExchangeID: req.RequestID,
			StartTime:  FormatTimestamp(req.Timestamp),
			Messages:   ConvertRequestToMessages(req),
		}
		exchanges = append(exchanges, exchange)
	}

	return exchanges
}

// ConvertRequestToMessages converts one request block to message array
// Returns: [user message, thinking message (if present), tool message(s), agent message]
func ConvertRequestToMessages(req VSCodeRequestBlock) []schema.Message {
	var messages []schema.Message

	// 1. User message with timestamp
	userMsg := schema.Message{
		Role:      schema.RoleUser,
		Timestamp: FormatTimestamp(req.Timestamp),
		Content: []schema.ContentPart{
			{Type: schema.ContentTypeText, Text: req.Message.Text},
		},
	}
	messages = append(messages, userMsg)

	// Check if there are tool calls
	hasToolCalls := HasToolCalls(req.Result.Metadata)

	// 2. Extract thinking from tool call rounds (only if there are actual tool calls)
	if hasToolCalls {
		thinking := ExtractThinkingFromMetadata(req.Result.Metadata)
		if thinking != "" {
			thinkingMsg := schema.Message{
				Role: schema.RoleAgent,
				Content: []schema.ContentPart{
					{Type: schema.ContentTypeThinking, Text: thinking},
				},
			}
			messages = append(messages, thinkingMsg)
		}
	}

	// 3. Parse responses and extract tool calls
	toolMessages := ParseResponsesForTools(req.Response, req.Result.Metadata)
	messages = append(messages, toolMessages...)

	// 4. Final agent text message
	// Try multiple sources in order of preference
	var finalText string

	// First try: metadata.messages (if present)
	finalText = ExtractFinalAgentMessage(req.Result.Metadata)

	// Second try: toolCallRounds response when no tool calls (this is the actual response)
	if finalText == "" && !hasToolCalls {
		finalText = ExtractResponseFromToolCallRounds(req.Result.Metadata)
	}

	// Third try: response array value field
	if finalText == "" {
		finalText = ExtractTextFromResponseArray(req.Response)
	}

	if finalText != "" {
		agentMsg := schema.Message{
			Role: schema.RoleAgent,
			Content: []schema.ContentPart{
				{Type: schema.ContentTypeText, Text: finalText},
			},
			Model: req.ModelID,
		}
		messages = append(messages, agentMsg)
	}

	return messages
}

// ParseResponsesForTools extracts tool invocations from response array
func ParseResponsesForTools(responses []json.RawMessage, metadata VSCodeResultMetadata) []schema.Message {
	var toolMessages []schema.Message

	// Build tool call map from metadata
	toolCalls := BuildToolCallMap(metadata)

	// Process each response
	for _, rawResp := range responses {
		// Parse "kind" field first
		kind, err := ParseResponseKind(rawResp)
		if err != nil {
			slog.Debug("Failed to parse response kind", "error", err)
			continue
		}

		switch kind {
		case "toolInvocationSerialized":
			var invocation VSCodeToolInvocationResponse
			if err := json.Unmarshal(rawResp, &invocation); err != nil {
				slog.Debug("Failed to parse tool invocation", "error", err)
				continue
			}

			// Skip hidden tools
			if invocation.Presentation == "hidden" {
				continue
			}

			// Find matching tool call and result
			toolInfo := BuildToolInfoFromInvocation(invocation, toolCalls, metadata.ToolCallResults)
			if toolInfo != nil {
				toolMsg := schema.Message{
					Role: schema.RoleAgent,
					Tool: toolInfo,
				}
				toolMessages = append(toolMessages, toolMsg)
			}

		// Handle other response types (textEditGroup, codeblockUri, etc.)
		// Can defer detailed handling to phase 2
		case "textEditGroup":
			slog.Debug("Skipping textEditGroup response (phase 2)", "kind", kind)
		case "codeblockUri":
			slog.Debug("Skipping codeblockUri response (phase 2)", "kind", kind)
		case "confirmation":
			slog.Debug("Skipping confirmation response (phase 2)", "kind", kind)
		case "inlineReference":
			slog.Debug("Skipping inlineReference response (phase 2)", "kind", kind)
		default:
			slog.Debug("Unknown response kind", "kind", kind)
		}
	}

	return toolMessages
}

// BuildToolInfoFromInvocation creates ToolInfo from VS Code invocation + metadata
func BuildToolInfoFromInvocation(
	invocation VSCodeToolInvocationResponse,
	toolCalls map[string]VSCodeToolCallInfo,
	toolResults map[string]VSCodeToolCallResult,
) *schema.ToolInfo {
	// Find tool call details from metadata
	toolCall, ok := toolCalls[invocation.ToolCallID]
	if !ok {
		slog.Debug("Tool call not found in metadata", "toolCallId", invocation.ToolCallID)
		return nil
	}

	toolInfo := &schema.ToolInfo{
		Name:  toolCall.Name,
		Type:  MapToolType(toolCall.Name),
		UseID: invocation.ToolCallID,
	}

	// Parse arguments if present
	if toolCall.Arguments != "" {
		var args map[string]any
		if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err == nil {
			toolInfo.Input = args
		}
	}

	// Add output from results map
	if result, ok := toolResults[invocation.ToolCallID]; ok {
		output := make(map[string]any)
		if len(result.Content) > 0 {
			var contentParts []string
			for _, content := range result.Content {
				// Value can be string or object - convert to string
				valueStr := valueToString(content.Value)
				if valueStr != "" {
					contentParts = append(contentParts, valueStr)
				}
			}
			if len(contentParts) > 0 {
				output["result"] = strings.Join(contentParts, "\n")
			}
		}
		if len(output) > 0 {
			toolInfo.Output = output
		}
	}

	return toolInfo
}

// valueToString converts a tool result value (which can be string or object) to string
func valueToString(value any) string {
	if value == nil {
		return ""
	}

	// Try string first
	if str, ok := value.(string); ok {
		return str
	}

	// If it's an object, marshal to JSON
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(jsonBytes)
}

// MapToolType maps VS Code tool names to schema.ToolType constants
func MapToolType(toolName string) string {
	mapping := map[string]string{
		"bash":               schema.ToolTypeShell,
		"search_files":       schema.ToolTypeSearch,
		"read_file":          schema.ToolTypeRead,
		"write_to_file":      schema.ToolTypeWrite,
		"str_replace_editor": schema.ToolTypeWrite,
		"list_files":         schema.ToolTypeSearch,
		"grep":               schema.ToolTypeSearch,
		"find":               schema.ToolTypeSearch,
		// Add more as needed
	}

	if toolType, ok := mapping[toolName]; ok {
		return toolType
	}

	slog.Debug("Unknown tool type, mapping to unknown", "toolName", toolName)
	return schema.ToolTypeUnknown
}
