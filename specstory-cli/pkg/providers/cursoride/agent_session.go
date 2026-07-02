package cursoride

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// ConvertToAgentChatSession converts Cursor composer data to AgentChatSession format.
// workspaceRoot is the project path the caller is scanning — threaded straight through
// to SessionData.WorkspaceRoot, matching every other provider's convention.
// This is a minimal implementation - markdown output will be improved later
func ConvertToAgentChatSession(composer *ComposerData, workspaceRoot string) (*spi.AgentChatSession, error) {
	// Use composer ID as session ID
	sessionID := composer.ComposerID

	// Convert timestamp (milliseconds to ISO 8601)
	var createdAt string
	if composer.CreatedAt > 0 {
		t := time.Unix(composer.CreatedAt/1000, (composer.CreatedAt%1000)*1000000)
		createdAt = t.Format(time.RFC3339)
	} else {
		createdAt = time.Now().Format(time.RFC3339)
	}

	// Generate slug from composer name or first user message
	slug := generateSlug(composer)

	// Build SessionData with provider info
	sessionData := &schema.SessionData{
		SchemaVersion: "1.0",
		Provider: schema.ProviderInfo{
			ID:      "cursoride",
			Name:    "Cursor IDE",
			Version: "unknown",
		},
		SessionID:     sessionID,
		CreatedAt:     createdAt,
		Slug:          slug,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     []schema.Exchange{},
	}

	// Get composer-level model name as fallback
	composerModelName := ""
	if composer.ModelConfig != nil && composer.ModelConfig.ModelName != "" {
		composerModelName = composer.ModelConfig.ModelName
	}

	// Parse capabilities to extract tool data (V1 format)
	capabilitiesMap := parseCapabilities(composer)

	// Create tool registry for formatting tool invocations
	toolRegistry := NewToolRegistry()

	// Convert conversation messages to exchanges
	// Each exchange groups one user turn with the agent response(s) that follow it.
	// ExchangeID is assigned after the loop (sessionID:index) rather than derived from
	// the triggering bubble's ID, matching every other provider — see the assignment
	// pass below.
	var currentMessages []schema.Message
	for i, bubble := range composer.Conversation {
		// Skip empty bubbles that have no content to display
		// BUT: Don't skip tool invocations (capabilityType=15) - they generate content from tool data
		if bubble.Text == "" && (bubble.Thinking == nil || bubble.Thinking.Text == "") && bubble.CapabilityType != 0 && bubble.CapabilityType != 15 {
			slog.Debug("Skipping empty capability bubble",
				"bubbleId", bubble.BubbleID,
				"capabilityType", bubble.CapabilityType)
			continue
		}
		// Resolve tool invocations into structured ToolInfo (with pre-formatted markdown)
		// up front, so buildMessageText knows whether to also embed the tool content in
		// Content — otherwise the tool use would render twice: once from Content, once
		// from Tool.
		var tool *schema.ToolInfo
		if bubble.CapabilityType == 15 {
			tool = resolveToolInfo(&bubble, capabilitiesMap, toolRegistry, composer.Version)
		}

		// Build the message text, including thinking blocks and tool invocations if present
		messageText := buildMessageText(&bubble, capabilitiesMap, toolRegistry, composer.Version, tool != nil)

		// Skip if we still have no content after processing (shouldn't happen, but safety check)
		if messageText == "" && tool == nil {
			slog.Debug("Skipping bubble with no content after processing",
				"bubbleId", bubble.BubbleID,
				"capabilityType", bubble.CapabilityType)
			continue
		}

		// Calculate timestamp from timing info
		timestamp := calculateTimestamp(bubble.TimingInfo)

		// If this is a user message without a timestamp, use the timestamp from the next assistant message
		// This matches the extension's behavior (see ts-extension/core/utils/conversation.ts:55-72)
		if bubble.Type == 1 && timestamp == "" {
			// Find next assistant message with timestamp
			for j := i + 1; j < len(composer.Conversation); j++ {
				nextBubble := composer.Conversation[j]
				if nextBubble.Type == 2 { // assistant message
					nextTimestamp := calculateTimestamp(nextBubble.TimingInfo)
					if nextTimestamp != "" {
						timestamp = nextTimestamp
						break
					}
				}
			}
		}

		// Get model name for agent messages only — user messages must not carry a model.
		modelName := ""
		if bubble.Type == 2 {
			if bubble.ModelInfo != nil && bubble.ModelInfo.ModelName != "" {
				modelName = bubble.ModelInfo.ModelName
			} else if composerModelName != "" {
				modelName = composerModelName
			}
		}

		// Build message with model and mode info
		message := schema.Message{
			Role:      getRoleFromType(bubble.Type),
			Timestamp: timestamp,
			Model:     modelName,
			Tool:      tool,
		}
		if messageText != "" {
			message.Content = []schema.ContentPart{
				{
					Type: schema.ContentTypeText,
					Text: messageText,
				},
			}
		}

		// Add mode to metadata for agent messages
		if bubble.Type == 2 && bubble.UnifiedMode != 0 {
			message.Metadata = map[string]interface{}{
				"mode": unifiedModeToString(bubble.UnifiedMode),
			}
		}

		// If this is a user message and we have pending messages, flush the current exchange
		// and start a new one.
		if bubble.Type == 1 && len(currentMessages) > 0 {
			sessionData.Exchanges = append(sessionData.Exchanges, schema.Exchange{Messages: currentMessages})
			currentMessages = nil
		}

		currentMessages = append(currentMessages, message)
	}

	// Create final exchange if there are remaining messages
	if len(currentMessages) > 0 {
		sessionData.Exchanges = append(sessionData.Exchanges, schema.Exchange{Messages: currentMessages})
	}

	// Assign exchangeId to each exchange (format: sessionId:index), matching every
	// other provider. Always non-empty, unlike deriving it from a triggering bubble ID.
	for i := range sessionData.Exchanges {
		sessionData.Exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", sessionID, i)
	}

	// Marshal to JSON for raw data
	rawDataJSON, err := json.Marshal(composer)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal composer to JSON: %w", err)
	}

	slog.Debug("Converted composer to AgentChatSession",
		"composerID", sessionID,
		"slug", slug,
		"exchanges", len(sessionData.Exchanges))

	return &spi.AgentChatSession{
		SessionID:   sessionID,
		CreatedAt:   createdAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataJSON),
	}, nil
}

// generateSlug creates a filesystem-safe slug from the composer name or first user message,
// matching the spi.GenerateFilenameFromUserMessage convention used by every other provider.
func generateSlug(composer *ComposerData) string {
	// Use composer name if available
	if composer.Name != "" {
		if slug := spi.GenerateFilenameFromUserMessage(composer.Name); slug != "" {
			return slug
		}
	}

	// Otherwise, use first user message
	for _, bubble := range composer.Conversation {
		if bubble.Type == 1 && bubble.Text != "" {
			if slug := spi.GenerateFilenameFromUserMessage(bubble.Text); slug != "" {
				return slug
			}
		}
	}

	// Fallback to composer ID
	return composer.ComposerID
}

// getRoleFromType converts Cursor's message type to schema role
func getRoleFromType(messageType int) string {
	if messageType == 1 {
		return schema.RoleUser
	}
	return schema.RoleAgent
}

// buildMessageText constructs the full message text including thinking blocks and,
// for tool bubbles that resolveToolInfo couldn't turn into structured ToolInfo, a
// plain-text tool fallback. toolHandledSeparately is true when the caller already
// resolved the tool invocation into message.Tool — in that case the formatted tool
// markdown lives in Tool.FormattedMarkdown and must not also be embedded here,
// otherwise the tool use renders twice.
func buildMessageText(bubble *ComposerConversation, capabilitiesMap map[int]*CapabilityData, toolRegistry *ToolRegistry, composerVersion int, toolHandledSeparately bool) string {
	var parts []string

	// Add thinking block if present
	if bubble.Thinking != nil && bubble.Thinking.Text != "" {
		thinkingBlock := fmt.Sprintf("<think><details><summary>Thought Process</summary>\n%s</details></think>", bubble.Thinking.Text)
		parts = append(parts, thinkingBlock)
	}

	// Process tool invocations if this is a tool capability (capabilityType = 15)
	if bubble.CapabilityType == 15 {
		if !toolHandledSeparately {
			toolText := processToolInvocation(bubble, capabilitiesMap, toolRegistry, composerVersion)
			if toolText != "" {
				parts = append(parts, toolText)
			}
		}
	} else if bubble.Text != "" {
		// Add main text if present (non-tool bubbles)
		parts = append(parts, bubble.Text)
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// resolveToolInfo resolves a tool-invocation bubble (capabilityType 15) whose handler
// completed normally into a schema.ToolInfo carrying pre-formatted markdown, so the
// tool use renders exactly once via the Tool field instead of also being embedded in
// Content. Returns nil when the bubble has no resolvable tool data, or ended in an
// error/cancelled/invalid state — those cases fall back to FormatToolInvocation's
// plain text via processToolInvocation/buildMessageText instead, matching their
// existing (unwrapped) rendering.
func resolveToolInfo(bubble *ComposerConversation, capabilitiesMap map[int]*CapabilityData, toolRegistry *ToolRegistry, composerVersion int) *schema.ToolInfo {
	toolData := resolveToolData(bubble, capabilitiesMap, composerVersion)
	if toolData == nil || toolData.Name == "" || toolData.Tool == 0 || toolData.Status == "error" || toolData.Status == "cancelled" {
		return nil
	}

	content, toolType := FormatToolContent(toolData, toolRegistry)
	return &schema.ToolInfo{
		Name:              toolData.Name,
		Type:              toSchemaToolType(toolType),
		FormattedMarkdown: &content,
	}
}

// resolveToolData finds the BubbleConversation for a tool bubble.
// V3+ stores it in bubble.ToolFormerData; V1 stores it in the capabilities map.
// Returns nil if the tool data cannot be found.
func resolveToolData(bubble *ComposerConversation, capabilitiesMap map[int]*CapabilityData, composerVersion int) *BubbleConversation {
	if composerVersion >= 3 && bubble.ToolFormerData != nil {
		return convertToolInvocationData(bubble.ToolFormerData)
	}
	if capData, ok := capabilitiesMap[bubble.CapabilityType]; ok {
		if capData.ParsedBubbleMap != nil {
			if bubbleData, found := capData.ParsedBubbleMap[bubble.BubbleID]; found {
				return bubbleData
			}
		}
	}
	return nil
}

// processToolInvocation processes a tool invocation bubble and returns formatted markdown
func processToolInvocation(bubble *ComposerConversation, capabilitiesMap map[int]*CapabilityData, toolRegistry *ToolRegistry, composerVersion int) string {
	toolData := resolveToolData(bubble, capabilitiesMap, composerVersion)
	if toolData == nil {
		slog.Warn("Tool invocation data not found",
			"bubbleId", bubble.BubbleID,
			"capabilityType", bubble.CapabilityType)
		return bubble.Text // Fallback to original text
	}

	// Format the tool invocation using the registry
	return FormatToolInvocation(toolData, toolRegistry)
}

// toSchemaToolType converts a cursoride ToolType to the schema tool type string.
// The only mapping difference is "mcp" which has no schema equivalent and maps to "generic".
func toSchemaToolType(t ToolType) string {
	if t == ToolTypeMCP {
		return schema.ToolTypeGeneric
	}
	return string(t)
}

// convertToolInvocationData converts ToolInvocationData to BubbleConversation
func convertToolInvocationData(data *ToolInvocationData) *BubbleConversation {
	return &BubbleConversation{
		Tool:           data.Tool,
		Name:           data.Name,
		RawArgs:        data.RawArgs,
		Params:         data.Params,
		Result:         data.Result,
		Status:         data.Status,
		Error:          data.Error,
		AdditionalData: data.AdditionalData,
		UserDecision:   data.UserDecision,
	}
}

// parseCapabilities parses the capabilities array and extracts tool data
// Returns a map of capabilityType -> CapabilityData
func parseCapabilities(composer *ComposerData) map[int]*CapabilityData {
	capabilitiesMap := make(map[int]*CapabilityData)

	for _, capability := range composer.Capabilities {
		capData := capability.Data

		// Parse bubbleDataMap if it's a string (JSON)
		if capData.BubbleDataMap != nil {
			if bubbleMapStr, ok := capData.BubbleDataMap.(string); ok {
				// Parse the JSON string into a map
				var bubbleMap map[string]*BubbleConversation
				if err := json.Unmarshal([]byte(bubbleMapStr), &bubbleMap); err != nil {
					slog.Warn("Failed to parse bubbleDataMap",
						"capabilityType", capability.Type,
						"error", err)
				} else {
					capData.ParsedBubbleMap = bubbleMap
				}
			}
		}

		capabilitiesMap[capability.Type] = &capData
	}

	return capabilitiesMap
}

// calculateTimestamp calculates the absolute timestamp from timing info
// Based on the TypeScript extension logic:
// Sometimes clientStartTime is relative (< 946684800000 = Jan 1, 2000)
// In that case, calculate absolute time as: clientEndTime - clientStartTime
func calculateTimestamp(timingInfo *TimingInfo) string {
	if timingInfo == nil {
		return ""
	}

	clientStartTime := timingInfo.ClientStartTime
	if clientStartTime == 0 {
		return ""
	}

	// Check if clientStartTime is relative (< 946684800000, which is year 2000)
	// e.g., full epoch: 1754311716540, relative: 1234.435
	if clientStartTime < 946684800000 {
		// Get clientEndTime (try fields in priority order)
		var clientEndTime float64
		if timingInfo.ClientRpcSendTime != 0 {
			clientEndTime = timingInfo.ClientRpcSendTime
		} else if timingInfo.ClientSettleTime != 0 {
			clientEndTime = timingInfo.ClientSettleTime
		} else if timingInfo.ClientEndTime != 0 {
			clientEndTime = timingInfo.ClientEndTime
		}

		// Calculate absolute time
		if clientEndTime != 0 {
			clientStartTime = clientEndTime - clientStartTime
		}
	}

	// Convert milliseconds to ISO 8601
	// Split into seconds and fractional part for precise conversion
	seconds := int64(clientStartTime / 1000)
	nanos := int64((clientStartTime - float64(seconds)*1000) * 1000000)
	t := time.Unix(seconds, nanos)
	return t.Format(time.RFC3339)
}

// unifiedModeToString converts Cursor's unifiedMode number to a readable string
// Based on the TypeScript extension logic (ts-extension/core/utils/composer.ts:118-132)
// 1 = Ask, 2 = Agent, 5 = Plan, other = Custom
func unifiedModeToString(unifiedMode int) string {
	switch unifiedMode {
	case 1:
		return "Ask"
	case 2:
		return "Agent"
	case 5:
		return "Plan"
	default:
		if unifiedMode != 0 {
			return "Custom"
		}
		return ""
	}
}
