package qwencode

import (
	"fmt"
	"log/slog"
	"maps"
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

// toolOutcome pairs the two places a tool call's result lives on a tool_result
// record: the functionResponse payload (what the model saw) and the envelope's
// toolCallResult (status plus display-oriented output like fileDiff).
type toolOutcome struct {
	Response *QwenFunctionResponse
	Result   *QwenToolCallResult
}

// convertUsage converts Qwen's Gemini-style usage metadata to the shared Usage type.
// Returns nil if usage is nil.
func convertUsage(usage *QwenUsageMetadata) *Usage {
	if usage == nil {
		return nil
	}
	return &Usage{
		InputTokens:   usage.PromptTokenCount,
		OutputTokens:  usage.CandidatesTokenCount,
		CachedTokens:  usage.CachedContentTokenCount,
		ThoughtTokens: usage.ThoughtsTokenCount,
	}
}

// GenerateAgentSession creates a SessionData from a parsed QwenSession.
func GenerateAgentSession(session *QwenSession, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "sessionID", session.ID, "recordCount", len(session.Records))

	if len(session.Records) == 0 {
		return nil, fmt.Errorf("session has no records")
	}

	createdAt := session.StartTime
	if createdAt == "" {
		createdAt = session.LastUpdated
	}

	exchanges := buildExchangesFromRecords(session.Records, workspaceRoot)

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
			ID:      "qwen",
			Name:    "Qwen Code",
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

// collectToolOutcomes gathers every tool_result record's payload keyed by call
// ID, so tool messages can be built with their outputs folded in regardless of
// where the result record lands in the transcript (results for parallel calls
// arrive as a batch after the assistant record).
func collectToolOutcomes(records []QwenRecord) map[string]toolOutcome {
	outcomes := make(map[string]toolOutcome)
	for i := range records {
		record := &records[i]
		if record.Type != "tool_result" || record.Message == nil {
			continue
		}
		for _, part := range record.Message.Parts {
			if part.FunctionResponse == nil {
				continue
			}
			callID := part.FunctionResponse.ID
			if callID == "" && record.ToolCallResult != nil {
				callID = record.ToolCallResult.CallID
			}
			if callID == "" {
				continue
			}
			outcomes[callID] = toolOutcome{
				Response: part.FunctionResponse,
				Result:   record.ToolCallResult,
			}
		}
	}
	return outcomes
}

// buildExchangesFromRecords groups Qwen records into exchanges. An exchange
// starts with a real user turn and includes all subsequent agent activity
// until the next user turn. System records and system-injected user records
// (task notifications) are skipped; tool_result records only contribute their
// end-time since their payloads are folded into the tool messages.
func buildExchangesFromRecords(records []QwenRecord, workspaceRoot string) []Exchange {
	outcomes := collectToolOutcomes(records)

	var exchanges []Exchange
	var currentExchange *Exchange

	flush := func() {
		if currentExchange != nil && len(currentExchange.Messages) > 0 {
			exchanges = append(exchanges, *currentExchange)
		}
		currentExchange = nil
	}

	for i := range records {
		record := &records[i]
		switch record.Type {
		case "user":
			if !record.IsRealUserTurn() {
				continue
			}
			content := strings.TrimSpace(record.TextContent())
			if content == "" {
				continue
			}

			flush()
			currentExchange = &Exchange{
				StartTime: record.Timestamp,
				Messages: []Message{{
					ID:        record.UUID,
					Timestamp: record.Timestamp,
					Role:      schema.RoleUser,
					Content:   []ContentPart{{Type: "text", Text: content}},
				}},
			}

		case "assistant":
			if currentExchange == nil {
				// Shouldn't happen normally; keep the turn rather than drop it.
				currentExchange = &Exchange{StartTime: record.Timestamp}
			}
			agentMsgs := buildAgentMessages(record, outcomes, workspaceRoot)
			currentExchange.Messages = append(currentExchange.Messages, agentMsgs...)
			currentExchange.EndTime = record.Timestamp

		case "tool_result":
			if currentExchange != nil {
				currentExchange.EndTime = record.Timestamp
			}
		}
	}

	flush()
	return exchanges
}

// buildAgentMessages creates Messages from one assistant record: one for
// thinking, one per functionCall (with its outcome folded in), and one for
// text content. Token usage is attached to the last message only to avoid
// double-counting in aggregation.
func buildAgentMessages(record *QwenRecord, outcomes map[string]toolOutcome, workspaceRoot string) []Message {
	var messages []Message

	if thinking := strings.TrimSpace(record.ThoughtContent()); thinking != "" {
		messages = append(messages, Message{
			ID:        record.UUID,
			Timestamp: record.Timestamp,
			Role:      schema.RoleAgent,
			Model:     record.Model,
			Content:   []ContentPart{{Type: "thinking", Text: thinking}},
		})
	}

	if record.Message != nil {
		for _, part := range record.Message.Parts {
			if part.FunctionCall == nil {
				continue
			}
			messages = append(messages, buildToolMessage(record, part.FunctionCall, outcomes, workspaceRoot))
		}
	}

	if text := strings.TrimSpace(record.TextContent()); text != "" {
		messages = append(messages, Message{
			ID:        record.UUID,
			Timestamp: record.Timestamp,
			Role:      schema.RoleAgent,
			Model:     record.Model,
			Content:   []ContentPart{{Type: "text", Text: text}},
		})
	}

	if usage := convertUsage(record.UsageMetadata); usage != nil && len(messages) > 0 {
		messages[len(messages)-1].Usage = usage
	}

	return messages
}

// buildToolMessage creates a Message for a single tool call, folding in the
// tool's outcome when a matching tool_result record exists.
func buildToolMessage(record *QwenRecord, call *QwenFunctionCall, outcomes map[string]toolOutcome, workspaceRoot string) Message {
	outcome := outcomes[call.ID]

	toolInfo := &ToolInfo{
		Name:   call.Name,
		Type:   classifyQwenToolType(call.Name),
		UseID:  call.ID,
		Input:  call.Args,
		Output: buildToolOutput(outcome),
	}

	return Message{
		ID:        record.UUID,
		Timestamp: record.Timestamp,
		Role:      schema.RoleAgent,
		Model:     record.Model,
		Tool:      toolInfo,
		PathHints: extractPathHints(call, workspaceRoot),
	}
}

// buildToolOutput assembles the tool Output map from an outcome. The
// functionResponse payload ({"output": ...} or {"error": ...}) is the base;
// the envelope's status/errorType and display form (fileDiff for edits, raw
// stdout for shell) are layered in for rendering.
func buildToolOutput(outcome toolOutcome) map[string]any {
	var output map[string]any

	if outcome.Response != nil && len(outcome.Response.Response) > 0 {
		output = make(map[string]any, len(outcome.Response.Response)+3)
		maps.Copy(output, outcome.Response.Response)
	}

	if outcome.Result != nil {
		if output == nil {
			output = make(map[string]any, 3)
		}
		if outcome.Result.Status != "" {
			output["status"] = outcome.Result.Status
		}
		if outcome.Result.ErrorType != "" {
			output["errorType"] = outcome.Result.ErrorType
		}
		if display := resultDisplayString(outcome.Result.ResultDisplay); display != "" {
			output["resultDisplay"] = display
		}
	}

	return output
}

// classifyQwenToolType maps Qwen Code tool names to standard tool types.
// Valid types: write, read, search, shell, task, generic, unknown
func classifyQwenToolType(toolName string) string {
	// Computer-use tools arrive as computer_use__<action>; classify the family.
	if strings.HasPrefix(toolName, "computer_use") {
		return "generic"
	}

	switch toolName {
	case "read_file", "read_many_files", "web_fetch":
		return "read"
	case "write_file", "edit", "replace", "smart_edit", "notebook_edit":
		return "write"
	case "grep_search", "search_file_content", "glob", "google_web_search", "web_search", "tool_search":
		return "search"
	case "run_shell_command", "list_directory", "monitor":
		return "shell"
	case "todo_write", "write_todos", "task", "agent", "delegate_to_agent":
		return "task"
	case "skill", "ask_user_question", "save_memory", "record_artifact",
		"list_agents", "cron_list", "get_goal", "send_message", "exit_plan_mode":
		return "generic"
	default:
		return "unknown"
	}
}

// extractPathHints extracts file paths from Qwen tool call args.
func extractPathHints(call *QwenFunctionCall, workspaceRoot string) []string {
	var paths []string

	// Common path field names for Qwen tools (read_file/write_file/edit use
	// file_path; glob/grep_search/list_directory use path).
	pathFields := []string{"file_path", "path", "dir_path"}

	for _, field := range pathFields {
		if value := inputAsString(call.Args, field); value != "" {
			normalizedPath := spi.NormalizePath(value, workspaceRoot)
			if !slices.Contains(paths, normalizedPath) {
				paths = append(paths, normalizedPath)
			}
		}
	}

	// Extract paths from shell commands (redirect targets, file-creating commands)
	if command := inputAsString(call.Args, "command"); command != "" {
		cwd := inputAsString(call.Args, "directory")
		if cwd == "" {
			cwd = workspaceRoot
		}
		shellPaths := spi.ExtractShellPathHints(command, cwd, workspaceRoot)
		for _, sp := range shellPaths {
			if !slices.Contains(paths, sp) {
				paths = append(paths, sp)
			}
		}
	}

	return paths
}
