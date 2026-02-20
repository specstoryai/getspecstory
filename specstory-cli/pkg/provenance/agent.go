package provenance

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// fileModifyingTools are tool types that can modify files and should generate agent events.
var fileModifyingTools = map[string]bool{
	schema.ToolTypeWrite:   true,
	schema.ToolTypeShell:   true,
	schema.ToolTypeGeneric: true,
}

// deterministicID generates a stable ID from the given components so that
// re-processing the same session produces the same events (deduped via INSERT OR IGNORE).
func deterministicID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// parseTimestamp attempts to parse an RFC3339 timestamp string.
// Returns the parsed time or the zero value if parsing fails.
func parseTimestamp(ts string) time.Time {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// Try RFC3339Nano as a fallback
		t, _ = time.Parse(time.RFC3339Nano, ts)
	}
	return t
}

// ExtractAgentEvents walks a SessionData's exchanges and messages, producing
// one AgentEvent per PathHint on file-modifying tool uses (write, shell, generic).
// Non-modifying tool types (read, search, task, unknown) are skipped.
func ExtractAgentEvents(sessionData *schema.SessionData) []AgentEvent {
	if sessionData == nil {
		return nil
	}

	var events []AgentEvent

	for _, exchange := range sessionData.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool == nil || !fileModifyingTools[msg.Tool.Type] {
				continue
			}
			// Only emit events for completed tool results — the tool_use timestamp
			// is when the agent requested the write, which may be much earlier than
			// the actual file change (e.g., waiting for user permission).
			if msg.Tool.Output == nil {
				continue
			}
			if len(msg.PathHints) == 0 {
				continue
			}

			ts := parseTimestamp(msg.Timestamp)
			// Fall back to exchange start time if message has no timestamp
			if ts.IsZero() {
				ts = parseTimestamp(exchange.StartTime)
			}

			for _, path := range msg.PathHints {
				events = append(events, AgentEvent{
					ID:         deterministicID(sessionData.SessionID, exchange.ExchangeID, msg.ID, path),
					FilePath:   path,
					ChangeType: "write",
					Timestamp:  ts,
					SessionID:  sessionData.SessionID,
					ExchangeID: exchange.ExchangeID,
					MessageID:  msg.ID,
					AgentType:  sessionData.Provider.ID,
					AgentModel: msg.Model,
				})
			}
		}
	}

	return events
}

// PushAgentEvents pushes a slice of agent events to the engine and returns
// any ProvenanceRecords produced by matches. Errors on individual events
// are logged but do not stop processing.
func PushAgentEvents(ctx context.Context, engine *Engine, events []AgentEvent) []*ProvenanceRecord {
	var records []*ProvenanceRecord

	for _, event := range events {
		record, err := engine.PushAgentEvent(ctx, event)
		if err != nil {
			slog.Warn("Failed to push agent event",
				"eventID", event.ID,
				"filePath", event.FilePath,
				"error", err)
			continue
		}
		if record != nil {
			records = append(records, record)
		}
	}

	return records
}

// ProcessSessionEvents extracts agent events from session data, pushes them to
// the engine, and returns any resulting provenance records.
// Safe to call with a nil engine — returns nil immediately.
func ProcessSessionEvents(ctx context.Context, engine *Engine, sessionData *schema.SessionData) []*ProvenanceRecord {
	if engine == nil || sessionData == nil {
		return nil
	}

	events := ExtractAgentEvents(sessionData)
	if len(events) == 0 {
		return nil
	}

	slog.Debug("Extracted agent events from session",
		"sessionID", sessionData.SessionID,
		"eventCount", len(events))

	return PushAgentEvents(ctx, engine, events)
}
