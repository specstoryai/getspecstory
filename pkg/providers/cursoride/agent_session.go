package cursoride

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
)

// ConvertToAgentChatSession converts Cursor composer data to AgentChatSession format
// This is a minimal implementation - markdown output will be improved later
func ConvertToAgentChatSession(composer *ComposerData) (*spi.AgentChatSession, error) {
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
			Name:    "cursoride",
			Version: "unknown",
		},
		SessionID: sessionID,
		CreatedAt: createdAt,
		Slug:      slug,
		Exchanges: []schema.Exchange{},
	}

	// Get composer-level model name as fallback
	composerModelName := ""
	if composer.ModelConfig != nil && composer.ModelConfig.ModelName != "" {
		composerModelName = composer.ModelConfig.ModelName
	}

	// Convert conversation messages to exchanges
	// For now, create a simple exchange for each user+assistant pair
	var currentMessages []schema.Message
	for i, bubble := range composer.Conversation {
		// Skip empty bubbles that have no content to display
		// These are typically capability/tool placeholders with no text or thinking
		if bubble.Text == "" && (bubble.Thinking == nil || bubble.Thinking.Text == "") && bubble.CapabilityType != 0 {
			slog.Debug("Skipping empty capability bubble",
				"bubbleId", bubble.BubbleID,
				"capabilityType", bubble.CapabilityType)
			continue
		}
		// Build the message text, including thinking blocks if present
		messageText := buildMessageText(&bubble)

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

		// Get model name: bubble-level modelInfo > composer-level modelConfig
		modelName := ""
		if bubble.ModelInfo != nil && bubble.ModelInfo.ModelName != "" {
			modelName = bubble.ModelInfo.ModelName
		} else if composerModelName != "" {
			modelName = composerModelName
		}

		// Build message with model and mode info
		message := schema.Message{
			Role:      getRoleFromType(bubble.Type),
			Timestamp: timestamp,
			Model:     modelName,
			Content: []schema.ContentPart{
				{
					Type: schema.ContentTypeText,
					Text: messageText,
				},
			},
		}

		// Add mode to metadata for agent messages
		if bubble.Type == 2 && bubble.UnifiedMode != 0 {
			message.Metadata = map[string]interface{}{
				"mode": unifiedModeToString(bubble.UnifiedMode),
			}
		}

		// If this is a user message and we have pending messages, create an exchange first
		if bubble.Type == 1 && len(currentMessages) > 0 {
			exchange := schema.Exchange{
				Messages: currentMessages,
			}
			sessionData.Exchanges = append(sessionData.Exchanges, exchange)
			currentMessages = nil
		}

		currentMessages = append(currentMessages, message)
	}

	// Create final exchange if there are remaining messages
	if len(currentMessages) > 0 {
		exchange := schema.Exchange{
			Messages: currentMessages,
		}
		sessionData.Exchanges = append(sessionData.Exchanges, exchange)
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

// generateSlug creates a slug from the composer name or first user message
func generateSlug(composer *ComposerData) string {
	// Use composer name if available
	if composer.Name != "" {
		return slugify(composer.Name)
	}

	// Otherwise, use first user message
	for _, bubble := range composer.Conversation {
		if bubble.Type == 1 && bubble.Text != "" {
			// Take first 4 words
			return slugifyText(bubble.Text, 4)
		}
	}

	// Fallback to composer ID
	return composer.ComposerID
}

// slugify converts a string to a slug while preserving casing
// The casing is preserved for use in titles, and will be lowercased for filenames by the caller
func slugify(s string) string {
	// Replace spaces with hyphens, keep original casing
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

// slugifyText converts text to a slug using the first N words
func slugifyText(text string, wordCount int) string {
	words := strings.Fields(text)
	if len(words) > wordCount {
		words = words[:wordCount]
	}
	return slugify(strings.Join(words, " "))
}

// getRoleFromType converts Cursor's message type to schema role
func getRoleFromType(messageType int) string {
	if messageType == 1 {
		return schema.RoleUser
	}
	return schema.RoleAgent
}

// buildMessageText constructs the full message text including thinking blocks
func buildMessageText(bubble *ComposerConversation) string {
	var parts []string

	// Add thinking block if present
	if bubble.Thinking != nil && bubble.Thinking.Text != "" {
		thinkingBlock := fmt.Sprintf("<think><details><summary>Thought Process</summary>\n%s</details></think>", bubble.Thinking.Text)
		parts = append(parts, thinkingBlock)
	}

	// Add main text if present
	if bubble.Text != "" {
		parts = append(parts, bubble.Text)
	}

	return strings.Join(parts, "\n\n---\n\n")
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
