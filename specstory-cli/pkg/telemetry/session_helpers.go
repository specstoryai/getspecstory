// Package telemetry provides session statistics helpers for computing and recording
// telemetry data from agent chat sessions.
package telemetry

import (
	"context"
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

// ExchangeStats holds computed statistics for a single exchange, used for child span attributes.
type ExchangeStats struct {
	ExchangeID   string
	ExchangeIdx  int
	PromptText   string
	StartTime    string
	EndTime      string
	MessageCount int
	ToolNames    string
	ToolTypes    string
	ToolCount    int
	TokenUsage   TokenUsage
	Model        string // The model used for this exchange (from agent messages)
}

// ComputeExchangeStats computes statistics for a single exchange.
func ComputeExchangeStats(exchange schema.Exchange, idx int) ExchangeStats {
	toolNames, toolTypes, toolCount := extractToolInfo(exchange)
	tokenUsage := CountExchangeTokens(exchange)

	return ExchangeStats{
		ExchangeID:   exchange.ExchangeID,
		ExchangeIdx:  idx,
		PromptText:   extractUserPromptText(exchange),
		StartTime:    exchange.StartTime,
		EndTime:      exchange.EndTime,
		MessageCount: len(exchange.Messages),
		ToolNames:    toolNames,
		ToolTypes:    toolTypes,
		ToolCount:    toolCount,
		TokenUsage:   tokenUsage,
		Model:        extractModel(exchange),
	}
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
// This sets session-level summary attributes only; exchange details are in child spans.
// Token usage attributes are provider-specific and only non-zero values are meaningful.
func SetSessionSpanAttributes(span trace.Span, stats SessionStats) {
	span.SetAttributes(
		attribute.String("specstory.agent", stats.AgentName),
		attribute.String("specstory.session.id", stats.SessionID),
		attribute.Int("specstory.session.exchange_count", stats.ExchangeCount),
		attribute.Int("specstory.session.message_count", stats.MessageCount),
		attribute.Int("specstory.session.tool_count", stats.ToolCount),
		attribute.Int("specstory.session.tool_type_count", stats.ToolTypeCount),
		attribute.String("specstory.project.path", stats.ProjectPath),
		// Token usage attributes - common (all providers)
		attribute.Int("specstory.session.tokens.input", stats.TokenUsage.InputTokens),
		attribute.Int("specstory.session.tokens.output", stats.TokenUsage.OutputTokens),
		// Token usage attributes - Claude Code specific (also used by Droid CLI)
		attribute.Int("specstory.session.tokens.cache_creation", stats.TokenUsage.CacheCreationInputTokens),
		attribute.Int("specstory.session.tokens.cache_read", stats.TokenUsage.CacheReadInputTokens),
		// Token usage attributes - Codex CLI specific
		attribute.Int("specstory.session.tokens.cached_input", stats.TokenUsage.CachedInputTokens),
		attribute.Int("specstory.session.tokens.reasoning_output", stats.TokenUsage.ReasoningOutputTokens),
		// Token usage attributes - Gemini CLI specific
		attribute.Int("specstory.session.tokens.cached", stats.TokenUsage.CachedTokens),
		attribute.Int("specstory.session.tokens.thought", stats.TokenUsage.ThoughtTokens),
		attribute.Int("specstory.session.tokens.tool", stats.TokenUsage.ToolTokens),
		// Token usage attributes - Droid CLI specific
		attribute.Int("specstory.session.tokens.thinking", stats.TokenUsage.ThinkingTokens),
	)
}

// StartExchangeSpan creates a child span for processing a single exchange.
// The returned span should be ended by the caller when exchange processing is complete.
func StartExchangeSpan(ctx context.Context, sessionID string, exchangeID string, idx int) (context.Context, trace.Span) {
	return Tracer("specstory").Start(ctx, "process_exchange",
		trace.WithAttributes(
			attribute.String("specstory.session.id", sessionID),
			attribute.String("specstory.exchange.id", exchangeID),
			attribute.Int("specstory.exchange.index", idx),
		),
	)
}

// SetExchangeSpanAttributes sets all attributes on an exchange span.
// Token usage attributes are provider-specific and only non-zero values are meaningful.
func SetExchangeSpanAttributes(span trace.Span, stats ExchangeStats) {
	span.SetAttributes(
		attribute.String("specstory.exchange.id", stats.ExchangeID),
		attribute.Int("specstory.exchange.index", stats.ExchangeIdx),
		attribute.String("specstory.exchange.model", stats.Model),
		attribute.String("specstory.exchange.prompt_text", stats.PromptText),
		attribute.String("specstory.exchange.start_time", stats.StartTime),
		attribute.String("specstory.exchange.end_time", stats.EndTime),
		attribute.Int("specstory.exchange.message_count", stats.MessageCount),
		attribute.String("specstory.exchange.tools_used", stats.ToolNames),
		attribute.String("specstory.exchange.tool_types", stats.ToolTypes),
		attribute.Int("specstory.exchange.tool_count", stats.ToolCount),
		// Token usage attributes - common (all providers)
		attribute.Int("specstory.exchange.tokens.input", stats.TokenUsage.InputTokens),
		attribute.Int("specstory.exchange.tokens.output", stats.TokenUsage.OutputTokens),
		// Token usage attributes - Claude Code specific (also used by Droid CLI)
		attribute.Int("specstory.exchange.tokens.cache_creation", stats.TokenUsage.CacheCreationInputTokens),
		attribute.Int("specstory.exchange.tokens.cache_read", stats.TokenUsage.CacheReadInputTokens),
		// Token usage attributes - Codex CLI specific
		attribute.Int("specstory.exchange.tokens.cached_input", stats.TokenUsage.CachedInputTokens),
		attribute.Int("specstory.exchange.tokens.reasoning_output", stats.TokenUsage.ReasoningOutputTokens),
		// Token usage attributes - Gemini CLI specific
		attribute.Int("specstory.exchange.tokens.cached", stats.TokenUsage.CachedTokens),
		attribute.Int("specstory.exchange.tokens.thought", stats.TokenUsage.ThoughtTokens),
		attribute.Int("specstory.exchange.tokens.tool", stats.TokenUsage.ToolTokens),
		// Token usage attributes - Droid CLI specific
		attribute.Int("specstory.exchange.tokens.thinking", stats.TokenUsage.ThinkingTokens),
	)
}

// ProcessExchangeSpans creates child spans for all exchanges in a session.
// This is a helper that creates spans, sets attributes, and ends them immediately
// since exchange processing is retrospective (data already exists).
// When noPrompts is true, the prompt_text attribute will be empty for privacy.
func ProcessExchangeSpans(ctx context.Context, stats SessionStats, exchanges []schema.Exchange, noPrompts bool) {
	for i, exchange := range exchanges {
		_, exchangeSpan := StartExchangeSpan(ctx, stats.SessionID, exchange.ExchangeID, i)
		exchangeStats := ComputeExchangeStats(exchange, i)
		if noPrompts {
			exchangeStats.PromptText = ""
		}
		SetExchangeSpanAttributes(exchangeSpan, exchangeStats)
		exchangeSpan.End()
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

// --- Exchange Helpers ---

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

// extractModel finds the model used in an exchange by looking at agent messages.
// Returns the model from the first agent message that has one set.
func extractModel(exchange schema.Exchange) string {
	for _, msg := range exchange.Messages {
		if msg.Role == schema.RoleAgent && msg.Model != "" {
			return msg.Model
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
// Handles Claude Code, Codex CLI, Gemini CLI, and Droid CLI specific token fields.
func CountExchangeTokens(exchange schema.Exchange) TokenUsage {
	var usage TokenUsage
	for _, msg := range exchange.Messages {
		if msg.Usage != nil {
			// Common fields (all providers)
			usage.InputTokens += msg.Usage.InputTokens
			usage.OutputTokens += msg.Usage.OutputTokens
			// Claude Code specific (also used by Droid CLI)
			usage.CacheCreationInputTokens += msg.Usage.CacheCreationInputTokens
			usage.CacheReadInputTokens += msg.Usage.CacheReadInputTokens
			// Codex CLI specific
			usage.CachedInputTokens += msg.Usage.CachedInputTokens
			usage.ReasoningOutputTokens += msg.Usage.ReasoningOutputTokens
			// Gemini CLI specific
			usage.CachedTokens += msg.Usage.CachedTokens
			usage.ThoughtTokens += msg.Usage.ThoughtTokens
			usage.ToolTokens += msg.Usage.ToolTokens
			// Droid CLI specific
			usage.ThinkingTokens += msg.Usage.ThinkingTokens
		}
	}
	return usage
}

// countSessionTokens aggregates token usage across all exchanges in a session.
// Handles Claude Code, Codex CLI, Gemini CLI, and Droid CLI specific token fields.
func countSessionTokens(exchanges []schema.Exchange) TokenUsage {
	var total TokenUsage
	for _, exchange := range exchanges {
		exchangeUsage := CountExchangeTokens(exchange)
		// Common fields (all providers)
		total.InputTokens += exchangeUsage.InputTokens
		total.OutputTokens += exchangeUsage.OutputTokens
		// Claude Code specific (also used by Droid CLI)
		total.CacheCreationInputTokens += exchangeUsage.CacheCreationInputTokens
		total.CacheReadInputTokens += exchangeUsage.CacheReadInputTokens
		// Codex CLI specific
		total.CachedInputTokens += exchangeUsage.CachedInputTokens
		total.ReasoningOutputTokens += exchangeUsage.ReasoningOutputTokens
		// Gemini CLI specific
		total.CachedTokens += exchangeUsage.CachedTokens
		total.ThoughtTokens += exchangeUsage.ThoughtTokens
		total.ToolTokens += exchangeUsage.ToolTokens
		// Droid CLI specific
		total.ThinkingTokens += exchangeUsage.ThinkingTokens
	}
	return total
}
