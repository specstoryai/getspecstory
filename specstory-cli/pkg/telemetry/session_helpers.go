// Package telemetry provides session statistics helpers for computing and recording
// telemetry data from agent chat sessions.
package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// --- Session Statistics ---

// SessionStats holds computed statistics for a session, used for telemetry and span attributes.
type SessionStats struct {
	AgentName     string
	SessionID     string
	ProjectPath   string
	ExchangeCount int
	MessageCount  int
	ToolCount     int
	ToolTypeCount int
	TokenUsage    TokenUsage
}

// ComputeSessionStats computes all statistics for a session from its SessionData.
func ComputeSessionStats(agentName string, session *spi.AgentChatSession) SessionStats {
	stats := SessionStats{
		AgentName: agentName,
		SessionID: session.SessionID,
	}

	if session.SessionData != nil {
		stats.ProjectPath = session.SessionData.WorkspaceRoot
		stats.ExchangeCount = len(session.SessionData.Exchanges)
		stats.MessageCount = countSessionMessages(session.SessionData.Exchanges)
		stats.ToolCount, stats.ToolTypeCount = countSessionTools(session.SessionData.Exchanges)
		stats.TokenUsage = countSessionTokens(session.SessionData.Exchanges)
	}

	return stats
}

// SetSessionSpanAttributes sets all standard session attributes on a span.
func SetSessionSpanAttributes(span trace.Span, stats SessionStats, exchanges []schema.Exchange) {
	span.SetAttributes(
		attribute.String("specstory.agent", stats.AgentName),
		attribute.String("specstory.session.id", stats.SessionID),
		attribute.Int("specstory.session.exchange_count", stats.ExchangeCount),
		attribute.Int("specstory.session.message_count", stats.MessageCount),
		attribute.Int("specstory.session.tool_count", stats.ToolCount),
		attribute.Int("specstory.session.tool_type_count", stats.ToolTypeCount),
		attribute.String("specstory.project.path", stats.ProjectPath),
		// Token usage attributes
		attribute.Int("specstory.session.input_tokens", stats.TokenUsage.InputTokens),
		attribute.Int("specstory.session.output_tokens", stats.TokenUsage.OutputTokens),
		attribute.Int("specstory.session.cache_creation_tokens", stats.TokenUsage.CacheCreationInputTokens),
		attribute.Int("specstory.session.cache_read_tokens", stats.TokenUsage.CacheReadInputTokens),
	)

	// Add flattened exchange attributes (dot-notation keys)
	if len(exchanges) > 0 {
		span.SetAttributes(BuildExchangeAttributes(exchanges)...)
	}
}

// RecordSessionMetrics records all telemetry metrics for a session.
// This is a no-op when telemetry is disabled.
func RecordSessionMetrics(ctx context.Context, stats SessionStats, duration time.Duration) {
	if !metricsEnabled {
		return
	}
	recordSessionProcessed(ctx, stats.AgentName, stats.SessionID)
	recordExchanges(ctx, stats.AgentName, stats.SessionID, int64(stats.ExchangeCount))
	recordMessages(ctx, stats.AgentName, stats.SessionID, int64(stats.MessageCount))
	recordToolUsage(ctx, stats.AgentName, stats.SessionID, int64(stats.ToolCount))
	recordProcessingDuration(ctx, stats.AgentName, stats.SessionID, duration)
	recordTokenUsage(ctx, stats.AgentName, stats.SessionID, stats.TokenUsage)
}

// --- Exchange Attributes ---

// BuildExchangeAttributes creates span attributes for all exchanges in a session.
func BuildExchangeAttributes(exchanges []schema.Exchange) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(exchanges)*12)

	for i, exchange := range exchanges {
		prefix := fmt.Sprintf("specstory.exchanges.%s", exchange.ExchangeID)

		// Extract tool usage information
		toolNames, toolTypes, toolCount := extractToolInfo(exchange)

		// Extract token usage for this exchange
		tokenUsage := CountExchangeTokens(exchange)

		attrs = append(attrs,
			attribute.String(prefix+".prompt_text", extractUserPromptText(exchange)),
			attribute.Int(prefix+".prompt_index", i),
			attribute.String(prefix+".start_time", exchange.StartTime),
			attribute.String(prefix+".end_time", exchange.EndTime),
			attribute.Int(prefix+".message_count", len(exchange.Messages)),
			attribute.String(prefix+".tools_used", toolNames),
			attribute.String(prefix+".tool_types", toolTypes),
			attribute.Int(prefix+".tool_count", toolCount),
			// Token usage attributes
			attribute.Int(prefix+".input_tokens", tokenUsage.InputTokens),
			attribute.Int(prefix+".output_tokens", tokenUsage.OutputTokens),
			attribute.Int(prefix+".cache_creation_tokens", tokenUsage.CacheCreationInputTokens),
			attribute.Int(prefix+".cache_read_tokens", tokenUsage.CacheReadInputTokens),
		)
	}

	return attrs
}

// extractToolInfo extracts tool usage information from an exchange.
// Returns: comma-separated unique tool names, comma-separated unique tool types, and total tool count.
func extractToolInfo(exchange schema.Exchange) (toolNames string, toolTypes string, toolCount int) {
	nameSet := make(map[string]bool)
	typeSet := make(map[string]bool)

	for _, msg := range exchange.Messages {
		if msg.Tool != nil && msg.Tool.Name != "" {
			toolCount++
			nameSet[msg.Tool.Name] = true
			if msg.Tool.Type != "" {
				typeSet[msg.Tool.Type] = true
			}
		}
	}

	// Build unique tool names list
	var names []string
	for n := range nameSet {
		names = append(names, n)
	}

	// Build unique tool types list
	var types []string
	for t := range typeSet {
		types = append(types, t)
	}

	return strings.Join(names, ","), strings.Join(types, ","), toolCount
}

// extractUserPromptText finds the first user message in an exchange and returns its text content.
func extractUserPromptText(exchange schema.Exchange) string {
	for _, msg := range exchange.Messages {
		if msg.Role == schema.RoleUser {
			// Concatenate all text content parts
			var text string
			for _, part := range msg.Content {
				if part.Type == schema.ContentTypeText {
					if text != "" {
						text += "\n"
					}
					text += part.Text
				}
			}
			return text
		}
	}
	return ""
}

// --- Counting Helpers ---

// countSessionMessages counts total messages across all exchanges.
func countSessionMessages(exchanges []schema.Exchange) int {
	count := 0
	for _, exchange := range exchanges {
		count += len(exchange.Messages)
	}
	return count
}

// countSessionTools counts total tools and unique tool types across all exchanges.
// Returns: total tool count, unique tool type count.
func countSessionTools(exchanges []schema.Exchange) (toolCount int, toolTypeCount int) {
	typeSet := make(map[string]bool)

	for _, exchange := range exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool != nil && msg.Tool.Name != "" {
				toolCount++
				if msg.Tool.Type != "" {
					typeSet[msg.Tool.Type] = true
				}
			}
		}
	}

	return toolCount, len(typeSet)
}

// CountExchangeTokens aggregates token usage for a single exchange.
func CountExchangeTokens(exchange schema.Exchange) TokenUsage {
	var usage TokenUsage
	for _, msg := range exchange.Messages {
		if msg.Usage != nil {
			usage.InputTokens += msg.Usage.InputTokens
			usage.OutputTokens += msg.Usage.OutputTokens
			usage.CacheCreationInputTokens += msg.Usage.CacheCreationInputTokens
			usage.CacheReadInputTokens += msg.Usage.CacheReadInputTokens
		}
	}
	return usage
}

// countSessionTokens aggregates token usage across all exchanges in a session.
func countSessionTokens(exchanges []schema.Exchange) TokenUsage {
	var total TokenUsage
	for _, exchange := range exchanges {
		exchangeUsage := CountExchangeTokens(exchange)
		total.InputTokens += exchangeUsage.InputTokens
		total.OutputTokens += exchangeUsage.OutputTokens
		total.CacheCreationInputTokens += exchangeUsage.CacheCreationInputTokens
		total.CacheReadInputTokens += exchangeUsage.CacheReadInputTokens
	}
	return total
}
