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

	// Build minimal SessionData
	// For now, we just need something that works - markdown can be improved later
	sessionData := &schema.SessionData{
		SessionID: sessionID,
		CreatedAt: createdAt,
		Exchanges: []schema.Exchange{},
	}

	// Convert conversation messages to exchanges
	// For now, create a simple exchange for each user+assistant pair
	var currentMessages []schema.Message
	for _, bubble := range composer.Conversation {
		message := schema.Message{
			Role: getRoleFromType(bubble.Type),
			Content: []schema.ContentPart{
				{
					Type: schema.ContentTypeText,
					Text: bubble.Text,
				},
			},
		}
		currentMessages = append(currentMessages, message)

		// Create an exchange when we have both user and agent messages
		if len(currentMessages) >= 2 {
			exchange := schema.Exchange{
				Messages: currentMessages,
			}
			sessionData.Exchanges = append(sessionData.Exchanges, exchange)
			currentMessages = nil
		}
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

// slugify converts a string to a slug
func slugify(s string) string {
	// Simple slugification - just replace spaces and lowercase
	// Can be improved later
	s = strings.ToLower(s)
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
