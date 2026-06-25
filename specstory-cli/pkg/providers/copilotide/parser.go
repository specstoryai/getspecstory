package copilotide

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// ParseResponseKind identifies the response type without fully parsing
func ParseResponseKind(rawResponse json.RawMessage) (string, error) {
	var kindOnly struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(rawResponse, &kindOnly); err != nil {
		return "", fmt.Errorf("failed to parse response kind: %w", err)
	}
	return kindOnly.Kind, nil
}

// IsHiddenTool checks if a tool invocation response is marked as hidden
func IsHiddenTool(rawResponse json.RawMessage) bool {
	var invocation VSCodeToolInvocationResponse
	if err := json.Unmarshal(rawResponse, &invocation); err != nil {
		return false
	}
	return invocation.Presentation == "hidden"
}

// BuildToolCallMap creates a lookup map from toolCallId to tool call info
// NOTE: This is kept for backward compatibility but sequence-based matching is preferred
func BuildToolCallMap(metadata VSCodeResultMetadata) map[string]VSCodeToolCallInfo {
	toolCalls := make(map[string]VSCodeToolCallInfo)

	for _, round := range metadata.ToolCallRounds {
		for _, call := range round.ToolCalls {
			toolCalls[call.ID] = call
		}
	}

	return toolCalls
}

// BuildToolCallSequence creates an ordered list of tool calls from metadata
// This is used for sequence-based matching since VS Code IDs don't match OpenAI IDs
func BuildToolCallSequence(metadata VSCodeResultMetadata) []VSCodeToolCallInfo {
	var sequence []VSCodeToolCallInfo

	for _, round := range metadata.ToolCallRounds {
		sequence = append(sequence, round.ToolCalls...)
	}

	return sequence
}

// HasToolCalls checks if there are any tool calls in the metadata
func HasToolCalls(metadata VSCodeResultMetadata) bool {
	for _, round := range metadata.ToolCallRounds {
		if len(round.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// ExtractThinkingFromMetadata extracts thinking text from tool call rounds
// Should only be called when there are actual tool calls
func ExtractThinkingFromMetadata(metadata VSCodeResultMetadata) string {
	var thinking strings.Builder

	for _, round := range metadata.ToolCallRounds {
		// Only include response if there are tool calls in this round
		if len(round.ToolCalls) > 0 && round.Response != "" {
			thinking.WriteString(round.Response)
			thinking.WriteString("\n\n")
		}
	}

	result := strings.TrimSpace(thinking.String())
	if result == "" {
		return ""
	}

	return result
}

// ExtractResponseFromToolCallRounds gets the response from tool call rounds when no tool calls present
// This is used when toolCallRounds exists but toolCalls is empty - the response is the final output
func ExtractResponseFromToolCallRounds(metadata VSCodeResultMetadata) string {
	for _, round := range metadata.ToolCallRounds {
		if round.Response != "" {
			return round.Response
		}
	}
	return ""
}

// ExtractTextFromResponseArray extracts text from the response array
// Handles response items that have a "value" field but no "kind" field
func ExtractTextFromResponseArray(responses []json.RawMessage) string {
	for _, rawResp := range responses {
		// Try to parse as a value response (no kind field)
		var valueResp struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(rawResp, &valueResp); err == nil && valueResp.Value != "" {
			return valueResp.Value
		}
	}
	return ""
}

// ExtractFinalAgentMessage gets the final text response from metadata
func ExtractFinalAgentMessage(metadata VSCodeResultMetadata) string {
	// VS Code stores final messages in metadata.messages
	for _, msg := range metadata.Messages {
		if msg.Role == "assistant" {
			return msg.Content
		}
	}
	return ""
}

// GenerateSlug creates a URL-safe slug from composer name or first message
func GenerateSlug(composer VSCodeComposer) string {
	// Use custom title if available
	if composer.CustomTitle != "" {
		return slugify(composer.CustomTitle)
	}
	if composer.Name != "" {
		return slugify(composer.Name)
	}

	// Fall back to first request message
	if len(composer.Requests) > 0 {
		firstMsg := composer.Requests[0].Message.Text
		// Take first 50 chars
		if len(firstMsg) > 50 {
			firstMsg = firstMsg[:50]
		}
		return slugify(firstMsg)
	}

	return "untitled"
}

// FormatTimestamp converts Unix timestamp (ms) to ISO 8601
func FormatTimestamp(unixMs int64) string {
	t := time.Unix(0, unixMs*int64(time.Millisecond))
	return t.Format(time.RFC3339)
}

// slugify converts text to URL-safe slug
func slugify(text string) string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Replace non-alphanumeric characters with hyphens
	var builder strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		} else if unicode.IsSpace(r) {
			builder.WriteRune('-')
		}
	}

	slug := builder.String()

	// Remove consecutive hyphens
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}

	// Trim hyphens from start and end
	slug = strings.Trim(slug, "-")

	// Ensure we have something
	if slug == "" {
		return "untitled"
	}

	return slug
}
