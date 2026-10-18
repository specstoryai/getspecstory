package musecode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
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

// GenerateAgentSession creates a SessionData from a parsed MuseSession.
func GenerateAgentSession(session *MuseSession, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "sessionID", session.ID, "eventCount", len(session.Events))

	if len(session.Events) == 0 {
		return nil, fmt.Errorf("session has no conversation events")
	}

	createdAt := session.StartTime
	if createdAt == "" {
		createdAt = session.LastUpdated
	}

	exchanges := buildExchanges(session.Events, session.ModelID, workspaceRoot)

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
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "muse",
			Name:    "Muse Code",
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

// collectToolOutcomes gathers every tool result text keyed by tool call id, so
// tool messages can be built with their outputs folded in: results arrive in a
// batch after the assistant record that requested the calls.
func collectToolOutcomes(events []MuseConversationEvent) map[string]string {
	outcomes := make(map[string]string)
	for i := range events {
		event := &events[i]
		if event.Kind() != eventToolResultBatchCommitted {
			continue
		}
		for _, result := range event.Event.Results {
			if result.ToolCallID == "" {
				continue
			}
			outcomes[result.ToolCallID] = result.Text
		}
	}
	return outcomes
}

// buildExchanges groups Muse run events into exchanges: one exchange per run,
// opened by `started` and closed by `terminal`.
func buildExchanges(events []MuseConversationEvent, defaultModel, workspaceRoot string) []Exchange {
	outcomes := collectToolOutcomes(events)

	var exchanges []Exchange
	var current *Exchange
	var pendingUsage *Usage
	model := defaultModel

	flush := func() {
		if current != nil && len(current.Messages) > 0 {
			exchanges = append(exchanges, *current)
		}
		current = nil
		pendingUsage = nil
	}

	// A transcript can begin mid-run (log rotation, truncation), leaving agent
	// events with no `started` ahead of them; keep the turn rather than drop it.
	ensureExchange := func(timestamp string) {
		if current == nil {
			current = &Exchange{StartTime: timestamp}
		}
	}

	// appendMessage carries over usage from a model step that reported before
	// its output was committed (see attachUsage).
	appendMessage := func(msg Message) {
		if pendingUsage != nil {
			msg.Usage = pendingUsage
			pendingUsage = nil
		}
		current.Messages = append(current.Messages, msg)
		current.EndTime = msg.Timestamp
	}

	// attachUsage places one model step's usage on that step's own message.
	// The runtime reports usage BEFORE committing tool calls but AFTER
	// committing a final assistant message, so "the last message if it can
	// still take usage, otherwise the next one" covers both orderings.
	attachUsage := func(usage *Usage) {
		if usage == nil || current == nil {
			return
		}
		if len(current.Messages) > 0 {
			last := &current.Messages[len(current.Messages)-1]
			if last.Role == schema.RoleAgent && last.Usage == nil {
				last.Usage = usage
				return
			}
		}
		pendingUsage = usage
	}

	for i := range events {
		event := &events[i]

		switch event.Kind() {
		case eventStarted:
			prompt := strings.TrimSpace(event.Event.Prompt)
			if prompt == "" {
				continue
			}
			flush()
			current = &Exchange{
				StartTime: event.Timestamp,
				Messages: []Message{{
					ID:        messageID(event),
					Timestamp: event.Timestamp,
					Role:      schema.RoleUser,
					Content:   []ContentPart{{Type: schema.ContentTypeText, Text: prompt}},
				}},
			}

		case eventReasoningCommitted:
			// Reasoning is encrypted for the Meta provider, so text is usually
			// empty; an empty thinking message would render as a blank block.
			thinking := strings.TrimSpace(event.Event.Text)
			if thinking == "" {
				continue
			}
			ensureExchange(event.Timestamp)
			appendMessage(Message{
				ID:        messageID(event),
				Timestamp: event.Timestamp,
				Role:      schema.RoleAgent,
				Model:     model,
				Content:   []ContentPart{{Type: schema.ContentTypeThinking, Text: thinking}},
			})

		case eventAssistantMessageCommitted:
			text := strings.TrimSpace(event.Event.Text)
			if text == "" {
				continue
			}
			ensureExchange(event.Timestamp)
			appendMessage(Message{
				ID:        messageID(event),
				Timestamp: event.Timestamp,
				Role:      schema.RoleAgent,
				Model:     model,
				Content:   []ContentPart{{Type: schema.ContentTypeText, Text: text}},
			})

		case eventAssistantToolCallsCommitted:
			ensureExchange(event.Timestamp)
			for j := range event.Event.ToolCalls {
				appendMessage(buildToolMessage(event, &event.Event.ToolCalls[j], outcomes, model, workspaceRoot))
			}

		case eventModelCompleted:
			if event.Event.Model != "" {
				model = event.Event.Model
			}
			attachUsage(convertUsage(&event.Event.Usage))

		case eventTerminal:
			if current != nil {
				current.EndTime = event.Timestamp
			}
		}
	}

	flush()
	return exchanges
}

// messageID prefers the runtime's own message id, falling back to the record
// id for events that carry no message (tool result batches, terminals).
func messageID(event *MuseConversationEvent) string {
	if event.Event.MessageID != "" {
		return event.Event.MessageID
	}
	return event.RecordID
}

// buildToolMessage creates a Message for a single tool call, folding in the
// matching tool result when one exists.
func buildToolMessage(event *MuseConversationEvent, call *MuseToolCall, outcomes map[string]string, model, workspaceRoot string) Message {
	input := decodeToolArgs(call.Args)

	// A malformed record can carry an empty tool name; fall back to a
	// placeholder so the markdown never renders "Tool use: ****" with an
	// empty data-tool-name (matches the sibling providers' behavior).
	name := call.Name
	if name == "" {
		name = "unknown"
	}

	toolInfo := &ToolInfo{
		Name:   name,
		Type:   classifyMuseToolType(name),
		UseID:  call.CallID,
		Input:  input,
		Output: buildToolOutput(outcomes[call.CallID]),
	}

	return Message{
		ID:        messageID(event),
		Timestamp: event.Timestamp,
		Role:      schema.RoleAgent,
		Model:     model,
		Tool:      toolInfo,
		PathHints: extractPathHints(input, workspaceRoot),
	}
}

// decodeToolArgs turns a tool call's args - a JSON *string* on the wire - into
// the input map the schema expects. Anything that will not decode as an object
// is preserved verbatim so the markdown still shows what was requested.
func decodeToolArgs(args string) map[string]any {
	if strings.TrimSpace(args) == "" {
		return nil
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		slog.Warn("decodeToolArgs: Tool args are not a JSON object, preserving raw", "error", err)
		return map[string]any{"raw": args}
	}
	return input
}

// buildToolOutput wraps a tool result text as the schema's output map. Muse
// reports failures as a "tool failed: <reason>" text rather than a status
// field, so the prefix is what separates an error from output.
func buildToolOutput(text string) map[string]any {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if isToolFailure(text) {
		return map[string]any{"error": text}
	}
	return map[string]any{"output": text}
}

// isToolFailure reports whether a tool result text is Muse's failure form.
func isToolFailure(text string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "tool failed")
}

// convertUsage converts one model step's token accounting to the shared Usage
// type. Muse splits cached input across cached_tokens and cache_read_tokens;
// both are input the model did not have to re-read, so they sum into
// CachedTokens. Returns nil when the step reported nothing.
func convertUsage(usage *MuseUsage) *Usage {
	if usage == nil {
		return nil
	}

	converted := Usage{
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		CachedTokens:  usage.CachedTokens + usage.CacheReadTokens,
		ThoughtTokens: usage.ReasoningTokens,
	}
	if converted == (Usage{}) {
		return nil
	}
	return &converted
}

// classifyMuseToolType maps Muse Code tool names to standard tool types.
// Valid types: write, read, search, shell, task, generic, unknown
func classifyMuseToolType(toolName string) string {
	switch toolName {
	case "read_file":
		return schema.ToolTypeRead
	case "write_file", "edit_file":
		return schema.ToolTypeWrite
	case "search", "web_search":
		return schema.ToolTypeSearch
	case "bash", "bash_input":
		return schema.ToolTypeShell
	case "write_todos", "subagent_spawn", "subagent_status", "subagent_wait",
		"subagent_read_result", "subagent_send_message", "subagent_cancel":
		return schema.ToolTypeTask
	// The memory tools operate on Muse's own memory store, not workspace
	// documents, so they group with the other agent-state tools rather than
	// with file read/write.
	case "read_memory", "add_memory", "edit_memory",
		"get_goal", "create_goal", "update_goal", "report_progress",
		"cron_create", "cron_list", "cron_delete", "read_skill",
		"request_user_input", "snooze_reminder",
		"list_peer_sessions", "send_session_message":
		return schema.ToolTypeGeneric
	default:
		return schema.ToolTypeUnknown
	}
}

// extractPathHints extracts file paths from Muse tool call args. Every
// file-touching native tool names its target `path`; shell commands get the
// shared extractor, resolved against the call's own workdir.
func extractPathHints(input map[string]any, workspaceRoot string) []string {
	var paths []string

	if value := inputAsString(input, "path"); value != "" {
		paths = append(paths, spi.NormalizePath(value, workspaceRoot))
	}

	if command := inputAsString(input, "command"); command != "" {
		cwd := inputAsString(input, "workdir")
		if cwd == "" {
			cwd = workspaceRoot
		}
		for _, shellPath := range spi.ExtractShellPathHints(command, cwd, workspaceRoot) {
			if !slices.Contains(paths, shellPath) {
				paths = append(paths, shellPath)
			}
		}
	}

	return paths
}
