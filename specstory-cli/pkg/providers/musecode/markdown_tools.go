package musecode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// maxDiffRunes caps a rendered edit diff; find/replace payloads can be whole
// files and the diff is a preview, not the source of truth.
const maxDiffRunes = 2000

// formatToolAsMarkdown generates formatted markdown for a ToolInfo (input + output).
// Returns the inner content only (no <tool-use> tags - those are added by pkg/session).
// Also sets tool.Summary if a custom summary is needed.
func formatToolAsMarkdown(tool *ToolInfo) string {
	if tool == nil {
		return ""
	}

	// Build custom summary for certain tools (appending key parameters)
	var customSummary string
	switch tool.Name {
	case "read_file", "write_file", "edit_file":
		if path := inputAsString(tool.Input, "path"); path != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, path)
		}
	case "read_memory", "add_memory", "edit_memory":
		if location := memoryLocation(tool.Input); location != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, location)
		}
	case "search":
		if pattern := inputAsString(tool.Input, "pattern"); pattern != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
		}
	case "web_search":
		if query := inputAsString(tool.Input, "query"); query != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, query)
		}
	}

	// Set custom summary on tool if we built one
	if customSummary != "" {
		tool.Summary = &customSummary
	}

	// Build body content only (no wrapper tags)
	body := strings.TrimSpace(formatToolBodyFromInput(tool))
	result := strings.TrimSpace(formatToolResultFromOutput(tool))

	var builder strings.Builder
	if body != "" {
		builder.WriteString("\n")
		builder.WriteString(body)
	}
	if result != "" {
		// A single leading newline when there is no body: the wrapper already
		// ends the <summary> line, so doubling here would stack blank lines.
		if body != "" {
			builder.WriteString("\n\n")
		} else {
			builder.WriteString("\n")
		}
		builder.WriteString(result)
	}
	if builder.Len() > 0 {
		builder.WriteString("\n")
	}

	return builder.String()
}

// formatToolBodyFromInput formats the tool input/body section
func formatToolBodyFromInput(tool *ToolInfo) string {
	switch tool.Name {
	case "bash":
		return formatShellBodyFromInput(tool.Input)
	case "bash_input":
		return formatShellInputBodyFromInput(tool.Input)
	case "write_file":
		return formatWriteFileBodyFromInput(tool.Input)
	case "edit_file":
		return formatEditBodyFromInput(tool.Input)
	case "add_memory":
		return formatAddMemoryBodyFromInput(tool.Input)
	case "edit_memory":
		return formatEditMemoryBodyFromInput(tool.Input)
	case "cron_create":
		return formatCronCreateBodyFromInput(tool.Input)
	case "send_session_message":
		return formatSessionMessageBodyFromInput(tool.Input)
	case "subagent_send_message":
		return formatSubagentMessageBodyFromInput(tool.Input)
	case "request_user_input":
		return formatUserInputBodyFromInput(tool.Input)
	case "write_todos":
		return formatTodoBodyFromInput(tool.Input)
	case "subagent_spawn":
		return formatSubagentBodyFromInput(tool.Input)
	case "read_file", "search", "web_search", "read_memory":
		// Don't show input args - parameters are in the summary
		return ""
	default:
		return spi.RenderGenericJSON(tool.Input)
	}
}

// formatToolResultFromOutput formats the tool result/output section
func formatToolResultFromOutput(tool *ToolInfo) string {
	// Errors take priority: render the failure message for any tool.
	if errText := outputErrorString(tool.Output); errText != "" {
		return addResultPrefix(formatOutputText(errText))
	}

	switch tool.Name {
	case "write_todos":
		// The body already shows the checklist
		return ""
	case "edit_file":
		// The body already shows the diff
		return ""
	case "bash", "bash_input":
		return formatShellResultFromOutput(tool.Output)
	case "read_file":
		return formatFileReadResultFromOutput(tool.Output)
	case "web_search":
		return formatWebSearchResultFromOutput(tool.Output)
	case "add_memory", "edit_memory":
		return formatMemoryAckResultFromOutput(tool.Output)
	case "get_goal", "create_goal", "update_goal", "report_progress":
		return formatGoalResultFromOutput(tool.Output)
	case "list_peer_sessions":
		return formatPeerSessionsResultFromOutput(tool.Output)
	case "send_session_message":
		return formatSessionMessageResultFromOutput(tool.Output)
	case "subagent_spawn", "subagent_status", "subagent_send_message",
		"subagent_wait", "subagent_read_result", "subagent_cancel":
		return formatSubagentResultFromOutput(tool.Output)
	case "request_user_input":
		return formatUserInputResultFromOutput(tool.Output)
	default:
		// search results deliberately take this path too: hits are arbitrary
		// file content, so they must land inside a fence where embedded
		// markdown or HTML cannot break the enclosing tool block.
		return formatDefaultResultFromOutput(tool.Output)
	}
}

// formatShellBodyFromInput renders a bash invocation: its description and the
// command in a bash fence.
func formatShellBodyFromInput(input map[string]interface{}) string {
	command := inputAsString(input, "command")
	description := inputAsString(input, "description")

	if command == "" && description == "" {
		return ""
	}

	var builder strings.Builder
	if description != "" {
		fmt.Fprintf(&builder, "%s\n\n", description)
	}
	if command != "" {
		builder.WriteString(spi.CodeFence("bash", command))
	}
	return builder.String()
}

// formatShellInputBodyFromInput renders a bash_input call: keystrokes sent
// into the PTY that an earlier bash call started, or an order to terminate it.
// The session number is what ties the call back to that bash invocation, and
// the chars are typed input rather than a program, so they are fenced as text.
func formatShellInputBodyFromInput(input map[string]interface{}) string {
	sessionID := schema.GetIntFromMap(input, "session_id")
	terminate, _ := input["terminate"].(bool)
	chars := inputAsString(input, "chars")

	var builder strings.Builder
	switch {
	case terminate:
		fmt.Fprintf(&builder, "Terminate PTY session %d\n", sessionID)
	case sessionID > 0:
		fmt.Fprintf(&builder, "PTY session %d\n", sessionID)
	}
	if chars != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(spi.CodeFence("text", strings.TrimRight(chars, "\n")))
	}
	return builder.String()
}

// formatShellResultFromOutput renders a bash result. The result text is a JSON
// object describing the run; showing just its `output` is what the user saw in
// the terminal, with the exit code called out when the command failed.
func formatShellResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return addResultPrefix(formatOutputText(raw))
	}

	// A chunk with no output field at all is an unknown record shape; there is
	// nothing better to show than the record itself.
	text, ok := payload["output"].(string)
	if !ok {
		return addResultPrefix(formatOutputText(raw))
	}

	trimmed := strings.Trim(sanitizeTerminalOutput(text), "\n")

	var builder strings.Builder
	if exitCode := schema.GetIntFromMap(payload, "exit_code"); exitCode != 0 {
		fmt.Fprintf(&builder, "Exit code: %d\n\n", exitCode)
	}
	if status := shellStatusText(payload, trimmed != ""); status != "" {
		builder.WriteString(status)
		if trimmed != "" {
			builder.WriteString("\n\n")
		}
	}
	if trimmed != "" {
		builder.WriteString(spi.CodeFence("text", trimmed))
	}
	if builder.Len() == 0 {
		return ""
	}
	return addResultPrefix(builder.String())
}

// shellStatusText describes a shell run whose story is in its execution state
// rather than its output: a command handed to a background PTY has printed
// nothing yet, and a terminated one reports how it ended. Without this line,
// both render as no result at all and the reader cannot tell the session
// started or why it stopped.
func shellStatusText(payload map[string]interface{}, hasOutput bool) string {
	if status, _ := payload["terminal_status"].(string); status != "" && status != "completed" {
		if reason, _ := payload["terminal_reason"].(string); reason != "" {
			return fmt.Sprintf("Shell %s %s (%s)", shellSessionLabel(payload), status, reason)
		}
		return fmt.Sprintf("Shell %s %s", shellSessionLabel(payload), status)
	}
	// The background note matters only when there is no output to show; a
	// bash_input chunk that echoed something is self-evidently still running.
	if state, _ := payload["execution_state"].(string); state == "background_running" && !hasOutput {
		return fmt.Sprintf("Running in background (shell %s)", shellSessionLabel(payload))
	}
	return ""
}

// shellSessionLabel names the PTY session when the payload identifies one.
func shellSessionLabel(payload map[string]interface{}) string {
	if id := schema.GetIntFromMap(payload, "session_id"); id > 0 {
		return fmt.Sprintf("session %d", id)
	}
	return "session"
}

// sanitizeTerminalOutput normalizes PTY control bytes for markdown: CRLF and
// bare CR become newlines, a backspace erases the character before it the way
// the terminal display did, and other C0 control bytes are dropped so they
// cannot leak into the fence.
func sanitizeTerminalOutput(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	var cleaned []rune
	for _, r := range text {
		switch {
		case r == '\b':
			if len(cleaned) > 0 && cleaned[len(cleaned)-1] != '\n' {
				cleaned = cleaned[:len(cleaned)-1]
			}
		case r == '\n' || r == '\t':
			cleaned = append(cleaned, r)
		case r < 0x20 || r == 0x7f:
			// other control bytes have no markdown rendering; drop them
		default:
			cleaned = append(cleaned, r)
		}
	}
	return string(cleaned)
}

func formatWriteFileBodyFromInput(input map[string]interface{}) string {
	path := inputAsString(input, "path")
	content := inputAsString(input, "content")
	if path == "" && content == "" {
		return ""
	}

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}
	if content != "" {
		// The trailing newline is real file content, but rendering it leaves a
		// blank line inside the fence; the fence closes the block either way.
		builder.WriteString(spi.CodeFence(spi.LanguageFromPath(path), strings.TrimRight(content, "\n")))
	}
	return builder.String()
}

// memoryLocation names a memory entry the way the memory tools address it:
// the scope-qualified path. Either half can be absent on a malformed call.
func memoryLocation(input map[string]interface{}) string {
	scope := inputAsString(input, "scope")
	path := inputAsString(input, "path")
	switch {
	case scope != "" && path != "":
		return scope + "/" + path
	case path != "":
		return path
	default:
		return scope
	}
}

// formatAddMemoryBodyFromInput renders the memory note being written the way
// write_file renders content: the location is already in the summary, so the
// body is the description and the note itself rather than a JSON blob with
// the markdown buried in an escaped string.
func formatAddMemoryBodyFromInput(input map[string]interface{}) string {
	description := inputAsString(input, "description")
	content := inputAsString(input, "content")
	if description == "" && content == "" {
		return spi.RenderGenericJSON(input)
	}

	var builder strings.Builder
	if description != "" {
		fmt.Fprintf(&builder, "%s\n\n", description)
	}
	if content != "" {
		builder.WriteString(spi.CodeFence("markdown", strings.TrimRight(content, "\n")))
	}
	return builder.String()
}

// formatEditMemoryBodyFromInput renders a memory edit as a diff: old_str and
// new_str are the same find/replace pair edit_file carries, and deserve the
// same rendering.
func formatEditMemoryBodyFromInput(input map[string]interface{}) string {
	diff := diffFromFindReplace(inputAsString(input, "old_str"), inputAsString(input, "new_str"))
	if diff == "" {
		return spi.RenderGenericJSON(input)
	}
	return spi.CodeFence("diff", truncate(diff, maxDiffRunes))
}

// formatCronCreateBodyFromInput summarizes the schedule being created: the
// cron expression, whether it repeats, and the prompt it will fire.
func formatCronCreateBodyFromInput(input map[string]interface{}) string {
	cron := inputAsString(input, "cron")
	prompt := inputAsString(input, "prompt")
	if cron == "" && prompt == "" {
		return spi.RenderGenericJSON(input)
	}

	var builder strings.Builder
	if cron != "" {
		cadence := "once"
		if recurring, _ := input["recurring"].(bool); recurring {
			cadence = "recurring"
		}
		fmt.Fprintf(&builder, "Schedule: `%s` (%s)\n", cron, cadence)
	}
	if prompt != "" {
		fmt.Fprintf(&builder, "Prompt: %s\n", prompt)
	}
	return builder.String()
}

// formatMessageBody renders a message-sending tool's body: who the message
// goes to and the message itself, which the generic JSON block buries among
// transport bookkeeping (delivery policies, command ids, wake policies).
func formatMessageBody(target string, message string, input map[string]interface{}) string {
	if target == "" && message == "" {
		return spi.RenderGenericJSON(input)
	}

	var builder strings.Builder
	if target != "" {
		fmt.Fprintf(&builder, "To: %s\n", target)
	}
	if message != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(message)
	}
	return builder.String()
}

// formatSessionMessageBodyFromInput renders a peer-session message: the target
// session and the message body.
func formatSessionMessageBodyFromInput(input map[string]interface{}) string {
	target := ""
	if id := inputAsString(input, "target_session_id"); id != "" {
		target = fmt.Sprintf("session `%s`", id)
	}
	return formatMessageBody(target, inputAsString(input, "body"), input)
}

// formatSubagentMessageBodyFromInput renders a message queued for a child
// agent, addressed by its agent path when one is given (it names the child
// more readably than the subagent uuid).
func formatSubagentMessageBodyFromInput(input map[string]interface{}) string {
	target := ""
	switch {
	case inputAsString(input, "agent_path") != "":
		target = fmt.Sprintf("`%s`", inputAsString(input, "agent_path"))
	case inputAsString(input, "subagent_id") != "":
		target = fmt.Sprintf("subagent `%s`", inputAsString(input, "subagent_id"))
	}
	return formatMessageBody(target, inputAsString(input, "message"), input)
}

// formatUserInputBodyFromInput renders the question(s) put to the user as
// markdown — header, question text and the options offered — instead of the
// questions JSON. Anything not shaped like a question list falls back to the
// generic rendering so no input is ever lost.
func formatUserInputBodyFromInput(input map[string]interface{}) string {
	questions, ok := input["questions"].([]interface{})
	if !ok || len(questions) == 0 {
		return spi.RenderGenericJSON(input)
	}

	var builder strings.Builder
	for _, rawQuestion := range questions {
		question, ok := rawQuestion.(map[string]interface{})
		if !ok {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}

		header, _ := question["header"].(string)
		text, _ := question["question"].(string)
		switch {
		case header != "" && text != "":
			fmt.Fprintf(&builder, "**%s**: %s\n", header, text)
		case text != "":
			fmt.Fprintf(&builder, "%s\n", text)
		case header != "":
			fmt.Fprintf(&builder, "**%s**\n", header)
		}

		options, _ := question["options"].([]interface{})
		for _, rawOption := range options {
			option, ok := rawOption.(map[string]interface{})
			if !ok {
				continue
			}
			label, _ := option["label"].(string)
			description, _ := option["description"].(string)
			switch {
			case label != "" && description != "":
				fmt.Fprintf(&builder, "- %s — %s\n", label, description)
			case label != "":
				fmt.Fprintf(&builder, "- %s\n", label)
			}
		}
	}
	if builder.Len() == 0 {
		return spi.RenderGenericJSON(input)
	}
	return builder.String()
}

// formatEditBodyFromInput renders an edit as a diff of its find/replace pair.
// Muse's own result text carries a mini diff too, but the inputs are what the
// model asked for and are present even when the edit failed.
func formatEditBodyFromInput(input map[string]interface{}) string {
	path := inputAsString(input, "path")
	find := inputAsString(input, "find")
	replace := inputAsString(input, "replace")

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}
	if diff := diffFromFindReplace(find, replace); diff != "" {
		builder.WriteString(spi.CodeFence("diff", truncate(diff, maxDiffRunes)))
	}
	return builder.String()
}

// diffFromFindReplace renders a find/replace pair as diff-flavoured lines.
func diffFromFindReplace(find, replace string) string {
	var builder strings.Builder

	writePrefixed := func(prefix, text string) {
		if text == "" {
			return
		}
		for _, line := range strings.Split(text, "\n") {
			builder.WriteString(prefix)
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	writePrefixed("-", find)
	writePrefixed("+", replace)

	return strings.TrimSuffix(builder.String(), "\n")
}

// formatTodoBodyFromInput renders the todo list as a checklist. Muse todo items
// use "text" (Qwen uses "content", Gemini "description").
func formatTodoBodyFromInput(input map[string]interface{}) string {
	todos, ok := input["todos"].([]interface{})
	if !ok || len(todos) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Todo List:\n")
	for _, raw := range todos {
		todo, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		text, _ := todo["text"].(string)
		status, _ := todo["status"].(string)
		fmt.Fprintf(&builder, "- [%s] %s\n", spi.TodoSymbol(status), strings.TrimSpace(text))
	}
	return builder.String()
}

// formatSubagentBodyFromInput shows what a spawned subagent was asked to do.
// The subagent's own transcript lives in a separate file, so the objective plus
// the spawn result is all the parent session records about it.
func formatSubagentBodyFromInput(input map[string]interface{}) string {
	role := inputAsString(input, "role")
	taskName := inputAsString(input, "task_name")
	objective := inputAsString(input, "objective")

	var builder strings.Builder
	if role != "" {
		fmt.Fprintf(&builder, "Role: `%s`\n", role)
	}
	if taskName != "" {
		fmt.Fprintf(&builder, "Task: `%s`\n", taskName)
	}
	if objective != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(objective)
	}
	return builder.String()
}

// formatDefaultResultFromOutput builds result with "Result:" prefix. A result
// that is itself a JSON object or array is pretty-printed in a json fence:
// most Muse tools ack in single-line JSON, which is unreadable inline.
func formatDefaultResultFromOutput(output map[string]interface{}) string {
	content := outputAsString(output)
	if content == "" {
		return ""
	}
	if pretty := prettyJSON(content); pretty != "" {
		return addResultPrefix(spi.CodeFence("json", pretty))
	}
	return addResultPrefix(formatOutputText(content))
}

// prettyJSON re-indents a JSON object or array, returning "" for anything
// else. Scalars are excluded on purpose: a result that happens to be a bare
// number or quoted string reads better as the text it is.
func prettyJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return ""
	}

	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return ""
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(pretty)
}

// formatFileReadResultFromOutput renders a read_file window, dropping Muse's
// "Read text file `x`." preamble line: it repeats what the summary already
// says, and the numbered window below it is what the reader wants. File
// content cannot be mistaken for the preamble because content lines arrive
// numbered ("1|…").
func formatFileReadResultFromOutput(output map[string]interface{}) string {
	content := outputAsString(output)
	if content == "" {
		return ""
	}
	if first, rest, found := strings.Cut(content, "\n"); strings.HasPrefix(first, "Read text file ") {
		if !found {
			return "" // preamble only: an empty window has nothing to show
		}
		content = rest
	}
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return addResultPrefix(formatOutputText(content))
}

// markdownLinkTextEscaper neutralizes the characters that would end a link's
// text early.
var markdownLinkTextEscaper = strings.NewReplacer("[", `\[`, "]", `\]`)

// formatWebSearchResultFromOutput renders search hits as a linked list of
// titles. The raw result is one line of JSON whose snippets bury the part a
// reader scans — the titles and URLs; the count preserves how much came back.
// Anything not shaped like a hit list falls back to the generic rendering.
func formatWebSearchResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var payload struct {
		Results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload.Results) == 0 {
		return formatDefaultResultFromOutput(output)
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%d results:\n", len(payload.Results))
	for _, result := range payload.Results {
		// Titles arrive with stray newlines and runs of spaces; collapse them
		// so each hit stays on its own list line.
		title := markdownLinkTextEscaper.Replace(strings.Join(strings.Fields(result.Title), " "))
		if title == "" {
			title = result.URL
		}
		fmt.Fprintf(&builder, "- [%s](%s)\n", title, result.URL)
	}
	return addResultPrefix(builder.String())
}

// formatMemoryAckResultFromOutput reduces a memory write's JSON ack to its
// message ("memory note written"); the location is already in the summary and
// the body shows the note or diff.
func formatMemoryAckResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.Message == "" {
		return formatDefaultResultFromOutput(output)
	}
	return addResultPrefix(payload.Message)
}

// formatGoalResultFromOutput reduces a goal envelope to the goal's story:
// objective, status and percent complete. Every goal tool echoes the same
// envelope, so the repeated ids, budgets and millisecond clocks are noise.
func formatGoalResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return formatDefaultResultFromOutput(output)
	}
	goalRaw, ok := envelope["goal"]
	if !ok {
		return formatDefaultResultFromOutput(output)
	}

	var goal *struct {
		Objective       string `json:"objective"`
		Status          string `json:"status"`
		PercentComplete int    `json:"percent_complete"`
	}
	if err := json.Unmarshal(goalRaw, &goal); err != nil {
		return formatDefaultResultFromOutput(output)
	}
	if goal == nil {
		return addResultPrefix("No active goal")
	}
	if goal.Objective == "" && goal.Status == "" {
		return formatDefaultResultFromOutput(output)
	}

	var builder strings.Builder
	if goal.Objective != "" {
		fmt.Fprintf(&builder, "Goal: %s\n", goal.Objective)
	}
	if goal.Status != "" {
		fmt.Fprintf(&builder, "Status: %s (%d%% complete)\n", goal.Status, goal.PercentComplete)
	}
	return addResultPrefix(builder.String())
}

// formatSubagentResultFromOutput reduces a subagent envelope to its story:
// the status (with its reason), which child it concerns, and the child's
// summary when one came back. The task/attempt refs are runtime bookkeeping.
func formatSubagentResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var payload struct {
		Status     string `json:"status"`
		Reason     string `json:"reason"`
		SubagentID string `json:"subagent_id"`
		AgentPath  string `json:"agent_path"`
		Summary    string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.Status == "" {
		return formatDefaultResultFromOutput(output)
	}

	var builder strings.Builder
	builder.WriteString(payload.Status)
	if payload.Reason != "" {
		fmt.Fprintf(&builder, " (%s)", payload.Reason)
	}
	switch {
	case payload.AgentPath != "":
		fmt.Fprintf(&builder, " — `%s`", payload.AgentPath)
	case payload.SubagentID != "":
		fmt.Fprintf(&builder, " — subagent `%s`", payload.SubagentID)
	}
	if payload.Summary != "" {
		builder.WriteString("\n\n")
		// The summary is the child's own prose; a blockquote keeps its voice
		// visually distinct from the parent transcript.
		for _, line := range strings.Split(strings.TrimRight(payload.Summary, "\n"), "\n") {
			builder.WriteString("> ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	return addResultPrefix(builder.String())
}

// formatPeerSessionsResultFromOutput lists each peer session on its own line;
// the raw envelope wraps three fields per peer in schema bookkeeping. A
// populated diagnostic is a shape never observed, so it stays visible via the
// generic rendering rather than being guessed at.
func formatPeerSessionsResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return formatDefaultResultFromOutput(output)
	}
	if diagnostic, ok := envelope["diagnostic"]; ok && string(diagnostic) != "null" {
		return formatDefaultResultFromOutput(output)
	}
	peersRaw, ok := envelope["peer_sessions"]
	if !ok {
		return formatDefaultResultFromOutput(output)
	}

	var peers []struct {
		SessionID      string `json:"session_id"`
		WorkspaceLabel string `json:"workspace_label"`
		Reachable      *bool  `json:"reachable"`
	}
	if err := json.Unmarshal(peersRaw, &peers); err != nil {
		return formatDefaultResultFromOutput(output)
	}
	if len(peers) == 0 {
		return addResultPrefix("No peer sessions")
	}

	var builder strings.Builder
	if len(peers) == 1 {
		builder.WriteString("1 peer session:\n")
	} else {
		fmt.Fprintf(&builder, "%d peer sessions:\n", len(peers))
	}
	for _, peer := range peers {
		builder.WriteString("- ")
		if peer.WorkspaceLabel != "" {
			fmt.Fprintf(&builder, "%s (`%s`)", peer.WorkspaceLabel, peer.SessionID)
		} else {
			fmt.Fprintf(&builder, "`%s`", peer.SessionID)
		}
		if peer.Reachable != nil {
			if *peer.Reachable {
				builder.WriteString(" — reachable")
			} else {
				builder.WriteString(" — unreachable")
			}
		}
		builder.WriteString("\n")
	}
	return addResultPrefix(builder.String())
}

// formatSessionMessageResultFromOutput reduces a peer-message outcome envelope
// to its status; the rest is transport bookkeeping echoing the call's own ids.
func formatSessionMessageResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var payload struct {
		Outcome struct {
			Status string `json:"status"`
		} `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.Outcome.Status == "" {
		return formatDefaultResultFromOutput(output)
	}
	return addResultPrefix(payload.Outcome.Status)
}

// formatUserInputResultFromOutput renders what the user chose instead of the
// answer envelope. Multiple answers list per-question selections by id.
func formatUserInputResultFromOutput(output map[string]interface{}) string {
	raw := outputAsString(output)
	if raw == "" {
		return ""
	}

	var payload struct {
		Status  string `json:"status"`
		Answers []struct {
			ID            string `json:"id"`
			SelectedLabel string `json:"selected_label"`
		} `json:"answers"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.Status == "" {
		return formatDefaultResultFromOutput(output)
	}

	var builder strings.Builder
	builder.WriteString(payload.Status)
	if len(payload.Answers) == 1 && payload.Answers[0].SelectedLabel != "" {
		fmt.Fprintf(&builder, ": %s", payload.Answers[0].SelectedLabel)
	} else if len(payload.Answers) > 0 {
		builder.WriteString(":\n")
		for _, answer := range payload.Answers {
			fmt.Fprintf(&builder, "- %s: %s\n", answer.ID, answer.SelectedLabel)
		}
	}
	return addResultPrefix(builder.String())
}

// formatOutputText wraps multi-line output in a code fence, leaves single-line as-is
func formatOutputText(output string) string {
	if strings.Contains(output, "\n") {
		return spi.CodeFence("text", output)
	}
	return output
}

// addResultPrefix adds "Result:" or "Result:\n" depending on content format
func addResultPrefix(content string) string {
	if strings.Contains(content, "\n") {
		return fmt.Sprintf("Result:\n%s", content)
	}
	return fmt.Sprintf("Result: %s", content)
}

// inputAsString extracts a string value from tool input. Muse serialises
// unset optional arguments as JSON null, which must read as absent rather
// than as the literal "null".
func inputAsString(input map[string]interface{}, key string) string {
	if input == nil {
		return ""
	}
	val, ok := input[key]
	if !ok || val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case []byte:
		return string(v)
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(bytes)
	}
}

// outputAsString extracts the primary output string from tool output
func outputAsString(output map[string]interface{}) string {
	if output == nil {
		return ""
	}
	if out, ok := output["output"].(string); ok && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
	}
	return ""
}

// outputErrorString returns the failure text when the tool failed, empty otherwise.
func outputErrorString(output map[string]interface{}) string {
	if output == nil {
		return ""
	}
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
	}
	return ""
}

func truncate(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "\n... (truncated)"
}
