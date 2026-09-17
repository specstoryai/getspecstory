package grokbuild

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

// GenerateAgentSession converts a parsed Grok session into the unified schema.
func GenerateAgentSession(session *GrokSession, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: starting", "sessionID", session.ID, "records", len(session.Records))

	if len(session.Records) == 0 {
		return nil, fmt.Errorf("session has no records")
	}

	if session.Cwd != "" {
		workspaceRoot = session.Cwd
	}
	if workspaceRoot == "" {
		var err error
		workspaceRoot, err = defaultProjectPath("")
		if err != nil {
			return nil, err
		}
	}
	createdAt := session.CreatedAt
	if createdAt == "" {
		createdAt = session.UpdatedAt
	}

	exchanges := buildExchanges(session, workspaceRoot)
	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", session.ID, i)
	}

	// Pre-render each tool so the markdown layer has nothing left to decide.
	for i := range exchanges {
		for j := range exchanges[i].Messages {
			msg := &exchanges[i].Messages[j]
			if msg.Tool != nil {
				formatted := formatToolAsMarkdown(msg.Tool)
				msg.Tool.FormattedMarkdown = &formatted
			}
		}
	}

	return &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "grok",
			Name:    "Grok Build",
			Version: "unknown",
		},
		SessionID:     session.ID,
		CreatedAt:     createdAt,
		UpdatedAt:     session.UpdatedAt,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}, nil
}

// buildExchanges walks the transcript and groups it into conversational turns.
// An exchange starts at a real user turn and runs until the next one.
func buildExchanges(session *GrokSession, workspaceRoot string) []Exchange {
	results := collectToolResults(session.Records)
	// Grok's incremental todo updates carry only an id and a status, so remember
	// each item's text from the call that introduced it.
	todoText := map[string]string{}

	var exchanges []Exchange
	var current *Exchange
	// The prompt id each exchange's agent messages carry, used to find that
	// turn's token totals.
	var exchangePrompts []string
	currentPrompt := ""
	userSeen := 0
	agentSeen := 0
	thoughtSeen := 0
	// Reasoning records carry no model of their own, and the summary's
	// current_model_id spells it differently from the assistant records
	// (grok-4.6 against grok-4.6-build), so borrow the assistant spelling.
	lastModel := assistantModel(session)

	flush := func() {
		if current != nil && len(current.Messages) > 0 {
			exchanges = append(exchanges, *current)
			exchangePrompts = append(exchangePrompts, currentPrompt)
		}
		current = nil
		currentPrompt = ""
	}

	for i := range session.Records {
		record := &session.Records[i]

		switch record.Type {
		case "user":
			query, ok := record.UserQuery()
			if !ok {
				// Injected context, not conversation.
				continue
			}
			flush()
			// Join on Grok's own prompt index. Grok injects synthetic prompt
			// turns (a finished subagent, for one), which take an index in
			// updates.jsonl but carry no <user_query>, so counting real turns
			// here would stamp every later prompt with the wrong time.
			var timestamp string
			if record.PromptIndex != nil {
				timestamp = session.Index.userTimeForPrompt(*record.PromptIndex)
			} else {
				timestamp = session.Index.userTimeAtOrdinal(userSeen)
			}
			if timestamp == "" {
				timestamp = session.CreatedAt
			}
			userSeen++
			current = &Exchange{
				StartTime: timestamp,
				Messages: []Message{{
					Timestamp: timestamp,
					Role:      schema.RoleUser,
					Content:   []ContentPart{{Type: "text", Text: query}},
				}},
			}

		case "reasoning":
			thought := strings.TrimSpace(record.ThoughtContent())
			if thought == "" {
				continue
			}
			current = ensureExchange(current, session.CreatedAt)
			current.Messages = append(current.Messages, Message{
				ID:        record.ID,
				Timestamp: session.Index.thoughtTimeAt(thoughtSeen),
				Role:      schema.RoleAgent,
				// A reasoning record carries no model of its own, so use the one
				// the surrounding assistant records report rather than the
				// session default, which spells the model differently.
				Model:   lastModel,
				Content: []ContentPart{{Type: "thinking", Text: thought}},
			})
			thoughtSeen++

		case "assistant":
			current = ensureExchange(current, session.CreatedAt)
			timestamp := session.Index.agentTimeAt(agentSeen)

			if text := strings.TrimSpace(record.TextContent()); text != "" {
				if currentPrompt == "" {
					currentPrompt = session.Index.agentPromptAt(agentSeen)
				}
				agentSeen++
				current.Messages = append(current.Messages, Message{
					Timestamp: timestamp,
					Role:      schema.RoleAgent,
					Model:     record.ModelID,
					Content:   []ContentPart{{Type: "text", Text: text}},
				})
				current.EndTime = timestamp
			}

			for k := range record.ToolCalls {
				call := &record.ToolCalls[k]
				msg := buildToolMessage(session, record, call, results, workspaceRoot)
				if msg.Tool != nil && msg.Tool.Name == "todo_write" {
					backfillTodoText(msg.Tool.Input, todoText)
					formatted := formatToolAsMarkdown(msg.Tool)
					msg.Tool.FormattedMarkdown = &formatted
				}
				current.Messages = append(current.Messages, msg)
				if msg.Timestamp != "" {
					current.EndTime = msg.Timestamp
				}
			}

		case "backend_tool_call":
			if record.Kind == nil {
				continue
			}
			current = ensureExchange(current, session.CreatedAt)
			msg := buildBackendToolMessage(session, record)
			current.Messages = append(current.Messages, msg)
			if msg.Timestamp != "" {
				current.EndTime = msg.Timestamp
			}
		case "system", "tool_result":
			// System instructions are runtime scaffolding; results are paired above.
		default:
			slog.Debug("Grok transcript: unknown record kind", "type", record.Type, "sessionID", session.ID)
		}
	}

	flush()
	attachUsage(exchanges, session.Index.usage, exchangePrompts)
	return exchanges
}

// ensureExchange starts an exchange for agent activity that arrives before any
// user turn, so nothing is silently dropped.
func ensureExchange(current *Exchange, fallbackTime string) *Exchange {
	if current != nil {
		return current
	}
	return &Exchange{StartTime: fallbackTime}
}

// assistantModel returns the model label the assistant records use, falling back
// to the session's own model when the transcript has none.
func assistantModel(session *GrokSession) string {
	for i := range session.Records {
		if session.Records[i].Type == "assistant" && session.Records[i].ModelID != "" {
			return session.Records[i].ModelID
		}
	}
	return session.Model
}

// backfillTodoText records the text of each todo item and fills it back in on
// the incremental updates that omit it, so a merge update still renders as a
// readable checklist instead of a row of empty bullets.
func backfillTodoText(input map[string]any, seen map[string]string) {
	todos, ok := input["todos"].([]any)
	if !ok {
		return
	}
	for _, raw := range todos {
		todo, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := todo["id"].(string)
		if id == "" {
			continue
		}
		if content, _ := todo["content"].(string); strings.TrimSpace(content) != "" {
			seen[id] = content
			continue
		}
		if content, ok := seen[id]; ok {
			todo["content"] = content
		}
	}
}

// collectToolResults indexes tool_result records by the call they answer.
func collectToolResults(records []GrokRecord) map[string]string {
	results := map[string]string{}
	for i := range records {
		record := &records[i]
		if record.Type != "tool_result" || record.ToolCallID == "" {
			continue
		}
		results[record.ToolCallID] = record.TextContent()
	}
	return results
}

// buildToolMessage renders one tool call with its result and outcome folded in.
func buildToolMessage(session *GrokSession, record *GrokRecord, call *GrokToolCall, results map[string]string, workspaceRoot string) Message {
	args := call.Args()

	output := map[string]any{}
	if result, ok := results[call.ID]; ok && result != "" {
		output["output"] = result
	}
	// Cancellations can have a failed update without a completion event. A
	// result string alone is not evidence that the native call succeeded.
	if session.Index.toolError[call.ID] {
		output["status"] = "error"
	} else if status := session.Index.toolStatus[call.ID]; status != "" {
		output["status"] = status
	}

	// spawn_subagent gets the sibling meta.json folded in, which is where the
	// subagent's description and outcome actually live.
	if call.Name == "spawn_subagent" {
		if meta := session.subagentFor(args); meta != nil {
			output["subagentStatus"] = meta.Status
			output["subagentType"] = meta.SubagentType
			if meta.DurationMs > 0 {
				output["durationMs"] = meta.DurationMs
			}
		}
	}

	return Message{
		ID:        call.ID,
		Timestamp: session.Index.toolTime[call.ID],
		Role:      schema.RoleAgent,
		Model:     record.ModelID,
		Tool: &ToolInfo{
			Name:   call.Name,
			Type:   classifyGrokTool(call.Name, session.Index.toolKind[call.ID]),
			UseID:  call.ID,
			Input:  args,
			Output: output,
		},
		PathHints: extractPathHints(call.Name, args, workspaceRoot),
	}
}

// buildBackendToolMessage renders a server-side web or X tool call. These never
// appear in tool_calls, so without this they would vanish from the transcript.
func buildBackendToolMessage(session *GrokSession, record *GrokRecord) Message {
	kind := record.Kind

	name := kind.Name
	input := map[string]any{}

	switch kind.ToolType {
	case "web_search":
		if kind.Action != nil {
			switch kind.Action.Type {
			case "open_page":
				name = "open_page"
				input["url"] = kind.Action.URL
			case "find_in_page":
				name = "open_page_with_find"
				input["url"] = kind.Action.URL
				input["pattern"] = kind.Action.Pattern
			default:
				name = "web_search"
				input["query"] = kind.Action.Query
			}
			// The pages a search returned live only here, so carry them through
			// rather than rendering a search with no results.
			if len(kind.Action.Sources) > 0 {
				urls := make([]string, 0, len(kind.Action.Sources))
				for _, source := range kind.Action.Sources {
					if source.URL != "" {
						urls = append(urls, source.URL)
					}
				}
				if len(urls) > 0 {
					input["sources"] = urls
				}
			}
		}
		if name == "" {
			name = "web_search"
		}
	case "x_search":
		// kind.Input is a JSON string of the real arguments.
		if kind.Input != "" {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(kind.Input), &parsed); err == nil {
				input = parsed
			} else {
				input["input"] = kind.Input
			}
		}
		if name == "" {
			name = "x_search"
		}
	default:
		if name == "" {
			name = kind.ToolType
		}
	}

	useID := kind.ID
	if useID == "" {
		useID = kind.CallID
	}

	output := map[string]any{}
	if kind.Status != "" {
		output["status"] = kind.Status
	}

	return Message{
		ID:        useID,
		Timestamp: session.Index.toolTime[useID],
		Role:      schema.RoleAgent,
		Model:     session.Model,
		Tool: &ToolInfo{
			Name:   name,
			Type:   classifyGrokTool(name, ""),
			UseID:  useID,
			Input:  input,
			Output: output,
		},
	}
}

// subagentFor matches a spawn_subagent call to its meta.json by description,
// which is the only field shared between the call arguments and the metadata.
func (s *GrokSession) subagentFor(args map[string]any) *GrokSubagentMeta {
	description, _ := args["description"].(string)
	if description == "" {
		return nil
	}
	for _, meta := range s.Subagents {
		if meta.Description == description {
			return meta
		}
	}
	return nil
}

// attachUsage puts a turn's token totals on the last message of the exchange
// that turn produced, so aggregation neither double counts nor misattributes.
//
// The link is the prompt id, taken from the agent messages in the exchange.
// Matching by position would break on any session that completes a turn without
// a matching user prompt, which happens whenever Grok injects one.
func attachUsage(exchanges []Exchange, usageByPrompt map[string]*GrokUsage, promptForExchange []string) {
	if len(usageByPrompt) == 0 {
		return
	}
	for i := range exchanges {
		if i >= len(promptForExchange) {
			continue
		}
		// An exchange whose agent messages carry no prompt id has nothing to
		// join on, so leave its tokens absent rather than guessing.
		if promptForExchange[i] == "" {
			continue
		}
		usage := usageByPrompt[promptForExchange[i]]
		if usage == nil {
			continue
		}
		messages := exchanges[i].Messages
		if len(messages) == 0 {
			continue
		}
		messages[len(messages)-1].Usage = &Usage{
			InputTokens:   usage.InputTokens,
			OutputTokens:  usage.OutputTokens,
			CachedTokens:  usage.CachedReadTokens,
			ThoughtTokens: usage.ReasoningTokens,
		}
	}
}

// classifyGrokTool maps a Grok tool to a schema tool type.
//
// Only observed native names are classified; future tools retain unknown
// rather than guessing semantics from an unrelated agent taxonomy.
func classifyGrokTool(name, _ string) string {
	switch name {
	case "read_file", "list_dir", "web_fetch":
		return "read"
	case "write", "search_replace":
		return "write"
	case "grep", "search_tool", "web_search":
		return "search"
	case "run_terminal_command", "monitor",
		"get_command_or_subagent_output", "kill_command_or_subagent":
		return "shell"
	case "todo_write":
		return "task"
	case "spawn_subagent", "workflow", "use_tool", "image_gen", "image_edit", "image_to_video", "reference_to_video",
		"scheduler_create", "scheduler_list", "scheduler_delete",
		"enter_plan_mode", "exit_plan_mode", "ask_user_question":
		return "generic"
	}

	return "unknown"
}

// extractPathHints pulls file paths out of a tool call's arguments.
func extractPathHints(name string, args map[string]any, workspaceRoot string) []string {
	if len(args) == 0 {
		return nil
	}

	var paths []string
	add := func(value string) {
		if value == "" {
			return
		}
		normalized := spi.NormalizePath(value, workspaceRoot)
		if !slices.Contains(paths, normalized) {
			paths = append(paths, normalized)
		}
	}

	// Grok's argument names differ per tool: target_file for reads, file_path for
	// writes and edits, target_directory for listings, and image for the video
	// tools that take a generated frame as input.
	for _, field := range []string{"target_file", "file_path", "target_directory", "path", "image"} {
		add(stringArg(args, field))
	}

	if command := stringArg(args, "command"); command != "" {
		cwd := stringArg(args, "cwd")
		if cwd == "" {
			cwd = workspaceRoot
		}
		for _, hint := range spi.ExtractShellPathHints(command, cwd, workspaceRoot) {
			if !slices.Contains(paths, hint) {
				paths = append(paths, hint)
			}
		}
	}

	return paths
}

// userTimeForPrompt returns the start time Grok recorded for a prompt index.
func (i *sessionIndex) userTimeForPrompt(promptIndex int) string {
	if i == nil {
		return ""
	}
	return i.userTime[promptIndex]
}

// userTimeAtOrdinal is a best-effort fallback for records missing a prompt index.
func (i *sessionIndex) userTimeAtOrdinal(n int) string {
	if i == nil || n < 0 || n >= len(i.userTimes) {
		return ""
	}
	return i.userTimes[n]
}

// agentTimeAt returns the recorded time of the nth agent message.
func (i *sessionIndex) agentTimeAt(n int) string {
	if i == nil || n < 0 || n >= len(i.agentTime) {
		return ""
	}
	return i.agentTime[n]
}

// agentPromptAt returns the prompt id of the nth agent message, which links an
// exchange to the turn whose token totals arrive separately.
func (i *sessionIndex) agentPromptAt(n int) string {
	if i == nil || n < 0 || n >= len(i.agentPrompt) {
		return ""
	}
	return i.agentPrompt[n]
}

// thoughtTimeAt returns the recorded time of the nth reasoning block.
func (i *sessionIndex) thoughtTimeAt(n int) string {
	if i == nil || n < 0 || n >= len(i.thoughtTime) {
		return ""
	}
	return i.thoughtTime[n]
}
