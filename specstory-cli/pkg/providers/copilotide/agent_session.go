package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ConvertToSessionData converts VS Code raw format to CLI's unified schema
func ConvertToSessionData(composer VSCodeComposer, projectPath string, state *VSCodeStateFile) spi.AgentChatSession {
	// Format timestamps
	createdAt := FormatTimestamp(composer.CreationDate)
	updatedAt := FormatTimestamp(composer.LastMessageDate)

	// Generate slug
	slug := GenerateSlug(composer)

	// Handle editing-only sessions (no chat requests but has file operations)
	requests := composer.Requests
	if len(requests) == 0 && state != nil {
		syntheticRequests := createSyntheticRequestsFromEditingState(composer, state)
		if len(syntheticRequests) > 0 {
			requests = syntheticRequests
		}
	}

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
		Exchanges:     ConvertRequestsToExchanges(requests),
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
				Role:  schema.RoleAgent,
				Model: req.ModelID,
				Content: []schema.ContentPart{
					{Type: schema.ContentTypeThinking, Text: thinking},
				},
			}
			messages = append(messages, thinkingMsg)
		}
	}

	// 3. Parse responses and extract tool calls
	toolMessages := ParseResponsesForTools(req.Response, req.Result.Metadata, req.ModelID)
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
func ParseResponsesForTools(responses []json.RawMessage, metadata VSCodeResultMetadata, modelID string) []schema.Message {
	var toolMessages []schema.Message

	// Build ordered sequence of tool calls from metadata
	toolCallSequence := BuildToolCallSequence(metadata)

	// Track sequence index for matching
	sequenceIndex := 0

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

			// Skip hidden tools (don't increment sequence index for hidden tools)
			if invocation.Presentation == "hidden" {
				continue
			}

			// Match by sequence: get the next tool call from the ordered list
			if sequenceIndex >= len(toolCallSequence) {
				slog.Debug("Tool invocation has no matching tool call in sequence",
					"sequenceIndex", sequenceIndex,
					"totalToolCalls", len(toolCallSequence),
					"toolCallId", invocation.ToolCallID)
				continue
			}

			toolCall := toolCallSequence[sequenceIndex]
			sequenceIndex++

			slog.Debug("Matched tool by sequence",
				"sequenceIndex", sequenceIndex-1,
				"toolName", toolCall.Name,
				"invocationId", invocation.ToolCallID,
				"metadataId", toolCall.ID)

			// Build tool info using the matched tool call
			toolInfo := BuildToolInfoFromInvocation(invocation, toolCall, metadata.ToolCallResults)
			if toolInfo != nil {
				toolMsg := schema.Message{
					Role:  schema.RoleAgent,
					Model: modelID,
					Tool:  toolInfo,
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

// BuildToolInfoFromInvocation creates ToolInfo from VS Code invocation + tool call
// Uses sequence-based matching: toolCall is passed directly instead of looked up by ID
func BuildToolInfoFromInvocation(
	invocation VSCodeToolInvocationResponse,
	toolCall VSCodeToolCallInfo,
	toolResults map[string]VSCodeToolCallResult,
) *schema.ToolInfo {
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
	// Note: We still look up results by invocation.ToolCallID since that's the VS Code ID
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

// MapToolType maps VS Code Copilot tool names to schema.ToolType constants
func MapToolType(toolName string) string {
	// Handle MCP tools (any tool starting with "mcp_")
	// Note: MCP tools use generic type until schema.ToolTypeMCP is added
	if strings.HasPrefix(toolName, "mcp_") {
		return schema.ToolTypeGeneric
	}

	mapping := map[string]string{
		// VS Code Copilot tools (OpenAI API names)
		"grep_search":           schema.ToolTypeSearch,
		"apply_patch":           schema.ToolTypeWrite,
		"read_file":             schema.ToolTypeRead,
		"insert_edit_into_file": schema.ToolTypeWrite,
		"create_file":           schema.ToolTypeWrite,
		"file_search":           schema.ToolTypeSearch,
		"semantic_search":       schema.ToolTypeSearch,
		"list_dir":              schema.ToolTypeGeneric,
		"manage_todo_list":      schema.ToolTypeTask,
		"get_errors":            schema.ToolTypeGeneric,

		// Legacy tool names (kept for compatibility)
		"bash":               schema.ToolTypeShell,
		"search_files":       schema.ToolTypeSearch,
		"write_to_file":      schema.ToolTypeWrite,
		"str_replace_editor": schema.ToolTypeWrite,
		"list_files":         schema.ToolTypeSearch,
		"grep":               schema.ToolTypeSearch,
		"find":               schema.ToolTypeSearch,
	}

	if toolType, ok := mapping[toolName]; ok {
		return toolType
	}

	slog.Debug("Unknown tool type, mapping to generic", "toolName", toolName)
	return schema.ToolTypeGeneric
}

// createSyntheticRequestsFromEditingState creates synthetic request blocks from editing operations
// when there are no chat requests but file operations exist
func createSyntheticRequestsFromEditingState(composer VSCodeComposer, state *VSCodeStateFile) []VSCodeRequestBlock {
	if state == nil {
		return nil
	}

	// Detect state version
	version := state.Version
	if version == 0 {
		version = 1 // Default to version 1
	}

	slog.Debug("Processing editing state", "version", version, "sessionId", composer.SessionID)

	var fileOperationSummaries []string

	if version >= 2 {
		// Version 2: Extract from timeline.operations
		fileOperationSummaries = extractOperationsFromV2State(state)
		slog.Debug("Extracted operations from v2 state", "count", len(fileOperationSummaries))
	} else {
		// Version 1: Extract from recentSnapshot/pendingSnapshot
		fileOperationSummaries = extractOperationsFromV1State(state)
		slog.Debug("Extracted operations from v1 state", "count", len(fileOperationSummaries))
	}

	// Fallback: If we couldn't extract operations but state exists, show generic message
	if len(fileOperationSummaries) == 0 {
		// Check if there's any indication of editing activity
		hasRecentSnapshot := state.RecentSnapshot != nil
		hasPendingSnapshot := state.PendingSnapshot != nil
		hasTimeline := state.Timeline != nil

		if !hasRecentSnapshot && !hasPendingSnapshot && !hasTimeline {
			return nil // No editing activity detected
		}

		fileOperationSummaries = []string{"File editing session (details not available)"}
	}

	// Get user input text if available (from customTitle)
	userText := composer.CustomTitle
	if userText == "" {
		userText = "File editing session"
	}

	// Build synthetic text for assistant message
	var assistantText string
	if len(fileOperationSummaries) > 0 {
		plural := ""
		if len(fileOperationSummaries) > 1 {
			plural = "s"
		}
		assistantText = fmt.Sprintf("Performed %d file operation%s:\n\n%s",
			len(fileOperationSummaries),
			plural,
			strings.Join(fileOperationSummaries, "\n"))
	} else {
		assistantText = "Performed file editing operations"
	}

	// Create synthetic request block
	syntheticRequest := VSCodeRequestBlock{
		RequestID: composer.SessionID + "-synthetic",
		Timestamp: composer.CreationDate,
		Message: VSCodeMessage{
			Text: userText,
		},
		Response: []json.RawMessage{},
		Result: VSCodeResult{
			Metadata: VSCodeResultMetadata{
				Messages: []VSCodeMetadataMessage{
					{
						Role:    "assistant",
						Content: assistantText,
					},
				},
			},
		},
		ModelID: composer.ResponderUsername,
	}

	return []VSCodeRequestBlock{syntheticRequest}
}

// extractOperationsFromV2State extracts file operations from version 2 state format (timeline.operations)
func extractOperationsFromV2State(state *VSCodeStateFile) []string {
	if state.Timeline == nil || len(state.Timeline.Operations) == 0 {
		return nil
	}

	var summaries []string
	for _, op := range state.Timeline.Operations {
		var fileName string
		if op.URI != nil {
			// Extract file name from URI
			path := op.URI.FSPath
			if path == "" {
				path = op.URI.Path
			}
			if path != "" {
				parts := strings.Split(path, "/")
				fileName = parts[len(parts)-1]
			} else {
				fileName = "unknown file"
			}
		} else {
			fileName = "unknown file"
		}

		switch op.Type {
		case "create":
			summaries = append(summaries, fmt.Sprintf("Created file: `%s`", fileName))
		case "textEdit":
			editCount := len(op.Edits)
			if editCount == 0 {
				editCount = 1
			}
			plural := ""
			if editCount > 1 {
				plural = "s"
			}
			summaries = append(summaries, fmt.Sprintf("Edited `%s` (%d edit%s)", fileName, editCount, plural))
		case "delete":
			summaries = append(summaries, fmt.Sprintf("Deleted file: `%s`", fileName))
		default:
			summaries = append(summaries, fmt.Sprintf("%s: `%s`", op.Type, fileName))
		}
	}

	return summaries
}

// extractOperationsFromV1State extracts file operations from version 1 state format (recentSnapshot/pendingSnapshot)
func extractOperationsFromV1State(state *VSCodeStateFile) []string {
	filesSummary := make(map[string]bool)

	// Try recentSnapshot (can be array or object)
	if state.RecentSnapshot != nil {
		entries := extractEntriesFromSnapshot(state.RecentSnapshot)
		for _, entry := range entries {
			if fileName := extractFileNameFromEntry(entry); fileName != "" {
				filesSummary[fmt.Sprintf("Modified `%s`", fileName)] = true
			}
		}
	}

	// Try pendingSnapshot
	if state.PendingSnapshot != nil {
		entries := extractEntriesFromSnapshot(state.PendingSnapshot)
		for _, entry := range entries {
			if fileName := extractFileNameFromEntry(entry); fileName != "" {
				filesSummary[fmt.Sprintf("Modified `%s`", fileName)] = true
			}
		}
	}

	// Convert map to slice
	var summaries []string
	for summary := range filesSummary {
		summaries = append(summaries, summary)
	}

	return summaries
}

// extractEntriesFromSnapshot extracts entries from a snapshot (handles both array and object formats)
func extractEntriesFromSnapshot(snapshot any) []VSCodeStopEntry {
	if snapshot == nil {
		return nil
	}

	var entries []VSCodeStopEntry

	// Try to unmarshal as VSCodeStop object
	if stopMap, ok := snapshot.(map[string]any); ok {
		if entriesData, ok := stopMap["entries"].([]any); ok {
			for _, entryData := range entriesData {
				if entryMap, ok := entryData.(map[string]any); ok {
					if resource, ok := entryMap["resource"].(string); ok {
						entries = append(entries, VSCodeStopEntry{Resource: resource})
					}
				}
			}
			return entries
		}
	}

	// Try to unmarshal as array of VSCodeStop objects
	if stopsArray, ok := snapshot.([]any); ok {
		for _, stopData := range stopsArray {
			if stopMap, ok := stopData.(map[string]any); ok {
				if entriesData, ok := stopMap["entries"].([]any); ok {
					for _, entryData := range entriesData {
						if entryMap, ok := entryData.(map[string]any); ok {
							if resource, ok := entryMap["resource"].(string); ok {
								entries = append(entries, VSCodeStopEntry{Resource: resource})
							}
						}
					}
				}
			}
		}
	}

	return entries
}

// extractFileNameFromEntry extracts the file name from an entry object
func extractFileNameFromEntry(entry VSCodeStopEntry) string {
	if entry.Resource == "" {
		return ""
	}

	// Handle both URI strings and file paths
	resource := entry.Resource
	parts := strings.Split(resource, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}

	return resource
}
