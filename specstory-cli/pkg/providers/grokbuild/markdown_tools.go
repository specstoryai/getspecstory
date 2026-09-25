package grokbuild

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// formatToolAsMarkdown renders a tool call's body and result. It returns the
// inner content only; pkg/session adds the surrounding <tool-use> tags.
// It also sets tool.Summary when the tool has a parameter worth putting in the
// collapsed header.
func formatToolAsMarkdown(tool *ToolInfo) string {
	if tool == nil {
		return ""
	}

	if summary := buildToolSummary(tool); summary != "" {
		tool.Summary = &summary
	}

	body := strings.TrimSpace(formatToolBody(tool))
	result := strings.TrimSpace(formatToolResult(tool))

	var builder strings.Builder
	if body != "" {
		builder.WriteString("\n")
		builder.WriteString(body)
	}
	if result != "" {
		builder.WriteString("\n\n")
		builder.WriteString(result)
	}
	if builder.Len() > 0 {
		builder.WriteString("\n")
	}

	return builder.String()
}

// buildToolSummary puts the most identifying argument in the collapsed header,
// so a reader can scan a transcript without expanding every tool.
func buildToolSummary(tool *ToolInfo) string {
	switch tool.Name {
	case "read_file":
		if path := stringArg(tool.Input, "target_file"); path != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, path)
		}
	case "write", "search_replace":
		if path := stringArg(tool.Input, "file_path"); path != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, path)
		}
	case "list_dir":
		if path := stringArg(tool.Input, "target_directory"); path != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, path)
		}
	case "grep":
		pattern := stringArg(tool.Input, "pattern")
		path := stringArg(tool.Input, "path")
		if pattern != "" && path != "" {
			return fmt.Sprintf("Tool use: **%s** `%s` in `%s`", tool.Name, pattern, path)
		}
		if pattern != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
		}
	case "search_tool", "web_search":
		if query := stringArg(tool.Input, "query"); query != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, query)
		}
	case "web_fetch":
		if url := stringArg(tool.Input, "url"); url != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, url)
		}
	case "use_tool":
		// The real tool is nested inside; showing use_tool alone tells a reader nothing.
		if inner := stringArg(tool.Input, "tool_name"); inner != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, inner)
		}
	case "spawn_subagent":
		if description := stringArg(tool.Input, "description"); description != "" {
			return fmt.Sprintf("Tool use: **%s** — %s", tool.Name, description)
		}
	case "run_terminal_command", "monitor":
		// Only the first line fits in a collapsed header; the body keeps the
		// whole command.
		if command, _, _ := strings.Cut(strings.TrimSpace(stringArg(tool.Input, "command")), "\n"); command != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, command)
		}
	case "get_command_or_subagent_output":
		if ids, ok := stringList(tool.Input["task_ids"]); ok && len(ids) == 1 {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, ids[0])
		} else if ok && len(ids) > 1 {
			return fmt.Sprintf("Tool use: **%s** %d tasks", tool.Name, len(ids))
		}
	case "image_gen", "image_edit", "image_to_video", "reference_to_video":
		// The prompt is the only thing that tells one generation from another.
		if prompt := stringArg(tool.Input, "prompt"); prompt != "" {
			return fmt.Sprintf("Tool use: **%s** — %s", tool.Name, headerText(prompt, summaryPromptRunes))
		}
	case "workflow":
		if name := workflowName(tool.Input); name != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, name)
		}
	case "scheduler_create":
		if interval := stringArg(tool.Input, "interval"); interval != "" {
			return fmt.Sprintf("Tool use: **%s** every `%s`", tool.Name, interval)
		}
	case "scheduler_delete":
		if id := stringArg(tool.Input, "id"); id != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, id)
		}
	case "send_feedback":
		if title := stringArg(tool.Input, "title"); title != "" {
			return fmt.Sprintf("Tool use: **%s** — %s", tool.Name, title)
		}
	}
	return ""
}

// formatToolBody preserves new arguments even when a specialized formatter
// only understands the established fields. Native tools add options over time.
func formatToolBody(tool *ToolInfo) string {
	body := formatKnownToolBody(tool)
	var consumed []string
	switch tool.Name {
	case "run_terminal_command", "monitor":
		consumed = []string{"command", "description"}
	case "write":
		consumed = []string{"file_path", "content"}
	case "search_replace":
		consumed = []string{"file_path", "old_string", "new_string"}
	case "todo_write":
		consumed = []string{"todos", "merge"}
	case "spawn_subagent":
		consumed = []string{"subagent_type", "prompt"}
	case "use_tool":
		consumed = []string{"tool_name", "tool_input"}
	case "ask_user_question":
		consumed = []string{"questions"}
	case "web_search":
		consumed = []string{"sources"}
	case "image_gen", "image_edit", "image_to_video", "reference_to_video", "scheduler_create":
		consumed = []string{"prompt"}
	case "workflow":
		consumed = []string{"source"}
	case "send_feedback":
		consumed = []string{"title", "type", "details"}
	case "scheduler_delete":
		// Every argument renders as a labeled line through the extras below.
	default:
		return body
	}
	extra := map[string]any{}
	for key, value := range tool.Input {
		if !slices.Contains(consumed, key) {
			extra[key] = value
		}
	}
	if len(extra) > 0 {
		body += "\n\n" + formatParameters(extra)
	}
	if strings.TrimSpace(body) == "" && len(tool.Input) > 0 {
		return spi.RenderGenericJSON(tool.Input)
	}
	return body
}

// parameterLabels gives observed native argument and result keys a readable
// label. Keys without an entry keep their native spelling, so an argument Grok
// adds later is still identifiable.
var parameterLabels = map[string]string{
	"target_file": "Path", "target_directory": "Directory", "file_path": "Path", "path": "Path",
	"offset": "Offset", "limit": "Limit", "pattern": "Pattern", "glob": "Glob", "head_limit": "Result limit",
	"query": "Query", "url": "URL", "description": "Description",
	"background": "Background", "timeout_ms": "Timeout (ms)", "persistent": "Persistent",
	"task_id": "Task ID", "task_ids": "Task IDs",
	"aspect_ratio": "Aspect ratio", "image": "Image", "duration": "Duration", "resolution_name": "Resolution",
	"first_frame": "First frame", "interval": "Interval", "fire_immediately": "Fire immediately", "id": "ID",
	"validate_only": "Validate only", "name": "Name", "status": "Status", "note": "Note",
	"total_hidden_tools": "Hidden tools",
}

// summaryPromptRunes keeps a prompt in a collapsed header to one scannable line.
const summaryPromptRunes = 80

func formatParameters(input map[string]any) string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		label := parameterLabels[key]
		if label == "" {
			label = key
		}
		// A list of IDs or paths reads better as code spans than as a JSON array.
		if items, ok := stringList(input[key]); ok && len(items) > 0 && !slices.ContainsFunc(items, func(item string) bool {
			return item == "" || strings.ContainsAny(item, "\n`")
		}) {
			fmt.Fprintf(&b, "%s: `%s`\n", label, strings.Join(items, "`, `"))
			continue
		}
		value := stringArg(input, key)
		// Every key here is present: preserve explicit null separately from
		// the empty string used by optional-argument lookups.
		if input[key] == nil {
			value = "null"
		}
		if strings.ContainsAny(value, "\n`") {
			fmt.Fprintf(&b, "%s:\n%s\n", label, spi.CodeFence("text", value))
		} else {
			fmt.Fprintf(&b, "%s: `%s`\n", label, value)
		}
	}
	return strings.TrimSpace(b.String())
}

func formatKnownToolBody(tool *ToolInfo) string {
	switch tool.Name {
	case "run_terminal_command", "monitor":
		return formatShellBody(tool.Input)
	case "write":
		return formatWriteBody(tool.Input)
	case "search_replace":
		return formatSearchReplaceBody(tool.Input)
	case "todo_write":
		return formatTodoBody(tool.Input)
	case "spawn_subagent":
		return formatSubagentBody(tool.Input)
	case "use_tool":
		return formatUseToolBody(tool.Input)
	case "web_search":
		return formatWebSearchBody(tool.Input)
	case "ask_user_question":
		return formatQuestionBody(tool.Input)
	case "image_gen", "image_edit", "image_to_video", "reference_to_video", "scheduler_create":
		return formatTextBlock("Prompt", stringArg(tool.Input, "prompt"))
	case "workflow":
		return formatWorkflowBody(tool.Input)
	case "send_feedback":
		return formatFeedbackBody(tool.Input)
	case "scheduler_delete":
		// formatToolBody renders every argument as a labeled line.
		return ""
	case "web_fetch", "read_file", "list_dir", "grep", "search_tool", "get_command_or_subagent_output", "kill_command_or_subagent":
		return formatParameters(tool.Input)
	default:
		return spi.RenderGenericJSON(tool.Input)
	}
}

func formatToolResult(tool *ToolInfo) string {
	result := formatKnownToolResult(tool)
	extra := map[string]any{}
	for key, value := range tool.Output {
		if key != "output" && key != "status" {
			extra[key] = value
		}
	}
	if status, _ := tool.Output["status"].(string); status != "" && status != "success" && status != "error" {
		extra["Status"] = status
	}
	if len(extra) > 0 {
		result += "\n\n" + formatParameters(extra)
	}
	return strings.TrimSpace(result)
}

func formatKnownToolResult(tool *ToolInfo) string {
	// A failed call reads as an error first, whatever the tool was. Grok records
	// the outcome in sidecars rather than in the result text, so without this
	// label a failed call would be indistinguishable from a successful one.
	if isErrorOutput(tool.Output) {
		text := resultText(tool)
		if text == "" {
			return "Error: the tool call failed"
		}
		if strings.Contains(text, "\n") || strings.TrimSpace(text) != text {
			return fmt.Sprintf("Error:\n%s", spi.CodeFence("text", text))
		}
		return fmt.Sprintf("Error: %s", text)
	}

	switch tool.Name {
	case "todo_write":
		// The checklist in the body is the whole story.
		return ""
	case "read_file":
		if text := outputText(tool.Output); text != "" {
			return spi.CodeFence(spi.LanguageFromPath(stringArg(tool.Input, "target_file")), text)
		}
		return ""
	case "run_terminal_command", "monitor":
		text := resultText(tool)
		if text == "" {
			return ""
		}
		if envelope := formatTaskEnvelope(text); envelope != "" {
			return "Result:\n" + envelope
		}
		return fmt.Sprintf("Result:\n%s", spi.CodeFence("text", text))
	case "image_gen", "image_edit":
		if rendered := formatImageResult(resultText(tool)); rendered != "" {
			return rendered
		}
	case "use_tool":
		// An MCP tool's result is usually a JSON document; fencing it as JSON
		// keeps its structure readable and highlighted.
		if text := resultText(tool); looksLikeJSON(text) {
			return fmt.Sprintf("Result:\n%s", spi.CodeFence("json", text))
		}
	case "search_tool":
		if rendered := formatToolCatalog(resultText(tool)); rendered != "" {
			return rendered
		}
		fallthrough
	case "list_dir", "grep":
		text := stripWorkspaceWrapper(resultText(tool))
		if text == "" {
			return ""
		}
		// Fence native wrappers and catalogs so Markdown cannot interpret them
		// as HTML or collapse their layout.
		if looksLikeJSON(text) {
			return fmt.Sprintf("Result:\n%s", spi.CodeFence("json", text))
		}
		return spi.CodeFence("text", text)
	}

	if text := resultText(tool); text != "" {
		return addResultPrefix(fenceIfMultiline(text))
	}
	return ""
}

// formatToolCatalog gives the observed MCP discovery envelope a readable
// hierarchy. Each discovered tool's input schema becomes a parameter list; a
// discovery can return hundreds of tools, and a JSON schema per tool buried the
// rest of the transcript. Unfamiliar metadata still renders as labeled lines.
func formatToolCatalog(text string) string {
	var catalog map[string]any
	if json.Unmarshal([]byte(text), &catalog) != nil {
		return ""
	}
	groups, ok := catalog["results"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("Discovered tools:\n")
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			return ""
		}
		tools, ok := group["tools"].([]any)
		if !ok {
			return ""
		}
		fmt.Fprintf(&b, "\nServer: `%s`\n", stringArg(group, "server"))
		if extra := formatParameters(withoutKeys(group, "server", "tools")); extra != "" {
			b.WriteString("\n" + extra + "\n")
		}
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				return ""
			}
			fmt.Fprintf(&b, "\n**%s**\n\n", stringArg(tool, "tool_name"))
			if description := strings.TrimSpace(stringArg(tool, "description")); description != "" {
				b.WriteString(description + "\n\n")
			}
			schema, _ := tool["input_schema"].(map[string]any)
			b.WriteString(formatInputSchema(schema) + "\n")
			// score is the discovery search's relevance ranking, not a property
			// of the tool, so it is left out.
			rest := withoutKeys(tool, "tool_name", "description", "score")
			if schema != nil {
				rest = withoutKeys(rest, "input_schema")
			}
			if extra := formatParameters(rest); extra != "" {
				b.WriteString("\n" + extra + "\n")
			}
		}
	}
	// A null footer field (Grok writes "note": null) carries nothing to show.
	footer := map[string]any{}
	for key, value := range withoutKeys(catalog, "results") {
		if value != nil {
			footer[key] = value
		}
	}
	if extra := formatParameters(footer); extra != "" {
		b.WriteString("\n" + extra)
	}
	return strings.TrimSpace(b.String())
}

// formatInputSchema lists a tool's parameters, required ones first, with their
// types and descriptions.
func formatInputSchema(schema map[string]any) string {
	if schema == nil {
		return "Parameters: none"
	}
	var b strings.Builder
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		b.WriteString("Parameters: none")
	} else {
		b.WriteString("Parameters:\n")
		writeSchemaProperties(&b, schema, 0)
	}
	// Keys such as additionalProperties constrain the whole input; "type" is
	// always "object" for a tool input and says nothing.
	if extra := formatParameters(withoutKeys(schema, "type", "properties", "required")); extra != "" {
		b.WriteString("\n\n" + extra)
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeSchemaProperties writes one bullet per property of an object schema,
// nesting the properties of object and array-of-object parameters beneath it.
func writeSchemaProperties(b *strings.Builder, schema map[string]any, depth int) {
	properties, _ := schema["properties"].(map[string]any)
	required, _ := stringList(schema["required"])
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		iRequired, jRequired := slices.Contains(required, names[i]), slices.Contains(required, names[j])
		if iRequired != jRequired {
			return iRequired
		}
		return names[i] < names[j]
	})

	indent := strings.Repeat("  ", depth)
	for _, name := range names {
		property, ok := properties[name].(map[string]any)
		if !ok {
			fmt.Fprintf(b, "%s- %s: %s\n", indent, spi.InlineCode(name), spi.InlineCode(stringArg(properties, name)))
			continue
		}
		var qualifiers []string
		if schemaType := schemaTypeName(property); schemaType != "" {
			qualifiers = append(qualifiers, schemaType)
		}
		if slices.Contains(required, name) {
			qualifiers = append(qualifiers, "required")
		}
		line := indent + "- " + spi.InlineCode(name)
		if len(qualifiers) > 0 {
			line += " (" + strings.Join(qualifiers, ", ") + ")"
		}
		// A description can span paragraphs; one line keeps the list intact.
		if description := strings.Join(strings.Fields(stringArg(property, "description")), " "); description != "" {
			line += ": " + description
		}
		if values, ok := property["enum"].([]any); ok && len(values) > 0 {
			spans := make([]string, 0, len(values))
			for _, value := range values {
				spans = append(spans, spi.InlineCode(jsonText(value)))
			}
			line += " One of: " + strings.Join(spans, ", ") + "."
		}
		consumed := []string{"type", "anyOf", "oneOf", "description", "enum", "properties", "required"}
		items, _ := property["items"].(map[string]any)
		if schemaTypeName(property) != "" {
			consumed = append(consumed, "items")
		}
		for _, key := range sortedKeys(withoutKeys(property, consumed...)) {
			line += fmt.Sprintf("; %s: %s", key, spi.InlineCode(jsonText(property[key])))
		}
		b.WriteString(line + "\n")

		if _, nested := property["properties"].(map[string]any); nested {
			writeSchemaProperties(b, property, depth+1)
		} else if _, nested := items["properties"].(map[string]any); nested {
			writeSchemaProperties(b, items, depth+1)
		}
	}
}

// schemaTypeName describes a JSON schema's type the way a reader would say it:
// "string", "array of string", "string | null".
func schemaTypeName(schema map[string]any) string {
	switch typed := schema["type"].(type) {
	case string:
		if items, ok := schema["items"].(map[string]any); typed == "array" && ok {
			if itemType := schemaTypeName(items); itemType != "" {
				return "array of " + itemType
			}
		}
		return typed
	case []any:
		if names, ok := stringList(typed); ok {
			return strings.Join(names, " | ")
		}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, ok := schema[key].([]any)
		if !ok {
			continue
		}
		var names []string
		for _, raw := range branches {
			branch, ok := raw.(map[string]any)
			if !ok {
				return ""
			}
			name := schemaTypeName(branch)
			if name == "" {
				return ""
			}
			names = append(names, name)
		}
		return strings.Join(names, " | ")
	}
	return ""
}

// formatTaskEnvelope turns the tagged envelope Grok returns for a backgrounded
// command (<task-id>, <status>, <output-file>, ...) into labeled lines. It
// returns "" for any output that does not open with that envelope.
func formatTaskEnvelope(text string) string {
	if !strings.HasPrefix(text, "<task-id>") {
		return ""
	}
	var fields, rest []string
	for _, line := range strings.Split(text, "\n") {
		match := taskEnvelopeTagRe.FindStringSubmatch(line)
		if match == nil || match[1] != match[3] {
			rest = append(rest, line)
			continue
		}
		fields = append(fields, fmt.Sprintf("%s: %s", tagLabel(match[1]), spi.InlineCode(match[2])))
	}
	result := strings.Join(fields, "\n")
	if remaining := strings.TrimSpace(strings.Join(rest, "\n")); remaining != "" {
		result += "\n\n" + fenceIfMultiline(remaining)
	}
	return result
}

var taskEnvelopeTagRe = regexp.MustCompile(`^<([a-z][a-z-]*)>(.*)</([a-z][a-z-]*)>$`)

// tagLabel turns a native tag such as "output-file" into "Output file".
func tagLabel(tag string) string {
	words := strings.Split(tag, "-")
	for i, word := range words {
		if word == "id" {
			words[i] = "ID"
		}
	}
	label := strings.Join(words, " ")
	return strings.ToUpper(label[:1]) + label[1:]
}

// formatImageResult shows where a generated or edited image was saved. Grok
// returns a JSON envelope whose message repeats the path alongside an
// instruction to the model not to describe the image, and whose filename and
// folder are both parts of the path; the path alone says it all. It returns ""
// when the result is not that envelope.
func formatImageResult(text string) string {
	var envelope map[string]any
	if !looksLikeJSON(text) || json.Unmarshal([]byte(text), &envelope) != nil {
		return ""
	}
	path, _ := envelope["path"].(string)
	if path == "" {
		return ""
	}
	result := fmt.Sprintf("Saved image: %s", spi.InlineCode(path))
	if extra := formatParameters(withoutKeys(envelope, "path", "filename", "session_folder", "message")); extra != "" {
		result += "\n" + extra
	}
	return result
}

// stripWorkspaceWrapper removes the <workspace_result> tags Grok wraps search
// output in. They only restate the session's workspace, and left bare they
// read as markup in the middle of the results.
func stripWorkspaceWrapper(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<workspace_result") || !strings.HasSuffix(trimmed, "</workspace_result>") {
		return text
	}
	opening, body, found := strings.Cut(trimmed, "\n")
	if !found || !strings.HasSuffix(opening, ">") {
		return text
	}
	return strings.TrimSpace(strings.TrimSuffix(body, "</workspace_result>"))
}

// resultText is a tool's output with Grok's trailing <system-reminder> blocks
// removed. Grok appends these notices about unrelated background tasks to
// whichever tool result is returning when the task changes state, so they
// describe a different call. File content is left untouched: a file may
// legitimately end with such a tag.
func resultText(tool *ToolInfo) string {
	text := outputText(tool.Output)
	if tool.Name == "read_file" {
		return text
	}
	return stripTrailingReminders(text)
}

// stripTrailingReminders removes <system-reminder> blocks from the end of text.
// It works backward block by block rather than with one regular expression,
// which could match from the first reminder to the last and take the real
// output between them.
func stripTrailingReminders(text string) string {
	const openTag, closeTag = "<system-reminder>", "</system-reminder>"
	stripped := text
	for strings.HasSuffix(strings.TrimRight(stripped, " \t\r\n"), closeTag) {
		start := strings.LastIndex(stripped, openTag)
		if start < 0 {
			break
		}
		stripped = stripped[:start]
	}
	if stripped == text {
		return text
	}
	// Only the separator Grok puts before the reminder is removed.
	return strings.TrimRight(stripped, " \t\r\n")
}

// formatWebSearchBody lists the pages a search returned. Grok records them in
// the call itself and never emits a tool result for a backend search, so without
// this a web search renders as a query with nothing behind it.
func formatWebSearchBody(input map[string]any) string {
	sources, _ := input["sources"].([]string)
	if len(sources) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Sources:\n")
	for _, url := range sources {
		fmt.Fprintf(&builder, "- %s\n", url)
	}
	return builder.String()
}

func formatShellBody(input map[string]any) string {
	command := stringArg(input, "command")
	description := stringArg(input, "description")
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

func formatWriteBody(input map[string]any) string {
	path := stringArg(input, "file_path")
	content := stringArg(input, "content")
	if path == "" && content == "" {
		return ""
	}

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}
	if content != "" {
		builder.WriteString(spi.CodeFence(spi.LanguageFromPath(path), content))
	}
	return builder.String()
}

// formatSearchReplaceBody shows an edit as a diff built from the old and new
// strings, which reads far better than either half on its own.
func formatSearchReplaceBody(input map[string]any) string {
	path := stringArg(input, "file_path")
	oldString := stringArg(input, "old_string")
	newString := stringArg(input, "new_string")

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}

	if oldString == "" && newString == "" {
		if builder.Len() == 0 {
			return ""
		}
		return builder.String()
	}

	var diff strings.Builder
	for _, line := range strings.Split(oldString, "\n") {
		fmt.Fprintf(&diff, "-%s\n", line)
	}
	for _, line := range strings.Split(newString, "\n") {
		fmt.Fprintf(&diff, "+%s\n", line)
	}
	builder.WriteString(spi.CodeFence("diff", strings.TrimRight(diff.String(), "\n")))
	return builder.String()
}

// formatTodoBody renders a todo list. Grok also sends incremental updates with
// merge set, which carry only an id and a status; the parser backfills the text
// for those from the call that first introduced each item, so an update still
// reads as a checklist rather than a row of empty bullets.
func formatTodoBody(input map[string]any) string {
	todos, ok := input["todos"].([]any)
	if !ok || len(todos) == 0 {
		return ""
	}

	heading := "Todo List:"
	if merge, ok := input["merge"].(bool); ok && merge {
		heading = "Todo update:"
	}

	var builder strings.Builder
	builder.WriteString(heading + "\n")
	for _, raw := range todos {
		todo, ok := raw.(map[string]any)
		if !ok {
			builder.WriteString(spi.RenderGenericJSON(map[string]any{"todo": raw}) + "\n")
			continue
		}
		content, _ := todo["content"].(string)
		status, _ := todo["status"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			// The item's text was never seen, so name it by id rather than
			// rendering an empty bullet.
			if id, _ := todo["id"].(string); id != "" {
				content = fmt.Sprintf("(item %s)", id)
			}
		}
		fmt.Fprintf(&builder, "- [%s] %s\n", todoStatusSymbol(status), content)
		consumed := []string{}
		for _, key := range []string{"id", "content"} {
			if _, ok := todo[key].(string); ok {
				consumed = append(consumed, key)
			}
		}
		if status == "pending" || status == "in_progress" || status == "completed" {
			consumed = append(consumed, "status")
		}
		if extra := spi.RenderGenericJSON(todo, consumed...); extra != "" {
			builder.WriteString(extra + "\n")
		}
	}
	return builder.String()
}

func todoStatusSymbol(status string) string {
	switch status {
	case "completed":
		return "x"
	case "in_progress":
		return "⚡"
	default:
		return " "
	}
}

// formatSubagentBody shows what the subagent was asked to do. The subagent's own
// transcript is a separate session that the provider deliberately skips, so this
// prompt plus the folded-in result is the only record of the delegated work.
func formatSubagentBody(input map[string]any) string {
	var builder strings.Builder
	if subagentType := stringArg(input, "subagent_type"); subagentType != "" {
		fmt.Fprintf(&builder, "Subagent: `%s`\n\n", subagentType)
	}
	if prompt := stringArg(input, "prompt"); prompt != "" {
		builder.WriteString(prompt)
	}
	return builder.String()
}

// formatUseToolBody unwraps an MCP call. Grok dispatches every MCP tool through
// use_tool, so without unwrapping, the most common tool in a session renders as
// an opaque envelope.
func formatUseToolBody(input map[string]any) string {
	var builder strings.Builder
	if name := stringArg(input, "tool_name"); name != "" {
		fmt.Fprintf(&builder, "MCP tool: `%s`", name)
	}
	if toolInput, ok := input["tool_input"]; ok {
		if encoded, err := json.MarshalIndent(toolInput, "", "  "); err == nil && string(encoded) != "{}" {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(spi.CodeFence("json", string(encoded)))
		}
	}
	return builder.String()
}

func fenceIfMultiline(text string) string {
	if strings.Contains(text, "\n") || strings.TrimSpace(text) != text {
		return spi.CodeFence("text", text)
	}
	return text
}

func addResultPrefix(content string) string {
	if strings.Contains(content, "\n") {
		return fmt.Sprintf("Result:\n%s", content)
	}
	return fmt.Sprintf("Result: %s", content)
}

// isErrorOutput reports whether the tool call failed according to its sidecars.
func isErrorOutput(output map[string]any) bool {
	if output == nil {
		return false
	}
	status, _ := output["status"].(string)
	return status == "error"
}

// outputText pulls the renderable text out of a tool's output map.
func outputText(output map[string]any) string {
	if output == nil {
		return ""
	}
	if text, ok := output["output"].(string); ok && text != "" {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, ansi.Strip(text))
	}
	return ""
}

// stringArg reads a string argument, encoding non-string values rather than
// dropping them.
func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

// stringList reads a JSON array whose items are all strings. The bool is false
// for any other value, so a caller can fall back to rendering it as JSON.
func stringList(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return typed, true
	case []any:
		items := make([]string, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(string)
			if !ok {
				return nil, false
			}
			items = append(items, item)
		}
		return items, true
	}
	return nil, false
}

// withoutKeys returns a copy of m without the named keys, leaving m intact.
func withoutKeys(m map[string]any, keys ...string) map[string]any {
	kept := make(map[string]any, len(m))
	for key, value := range m {
		if !slices.Contains(keys, key) {
			kept[key] = value
		}
	}
	return kept
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jsonText renders a JSON value as its literal text: strings as-is, everything
// else encoded, including null.
func jsonText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// headerText fits text to one line of a collapsed summary, marking a cut.
func headerText(text string, maxRunes int) string {
	line := strings.Join(strings.Fields(text), " ")
	count := 0
	for i := range line {
		if count == maxRunes {
			return strings.TrimRight(line[:i], " ") + "…"
		}
		count++
	}
	return line
}

// formatTextBlock fences a free-text argument such as a prompt, which is
// often long and may span lines.
func formatTextBlock(label, text string) string {
	if text == "" {
		return ""
	}
	return fmt.Sprintf("%s:\n%s", label, spi.CodeFence("text", text))
}

// workflowMetaNameRe finds the name a workflow script declares in its meta map
// (let meta = #{ name: "tool-demo", ... }).
var workflowMetaNameRe = regexp.MustCompile(`(?s)\bmeta\s*=\s*#\{.*?\bname\s*:\s*"([^"]+)"`)

// workflowName identifies a workflow: by its saved name, or by the name its
// inline script declares.
func workflowName(input map[string]any) string {
	source, _ := input["source"].(map[string]any)
	if name := stringArg(source, "name"); name != "" {
		return name
	}
	if match := workflowMetaNameRe.FindStringSubmatch(stringArg(source, "script")); match != nil {
		return match[1]
	}
	return ""
}

// formatWorkflowBody shows a workflow's source: a saved workflow by name, or an
// inline Rhai script in full.
func formatWorkflowBody(input map[string]any) string {
	raw, present := input["source"]
	if !present {
		return ""
	}
	source, ok := raw.(map[string]any)
	if !ok {
		return formatParameters(map[string]any{"source": raw})
	}
	var parts []string
	if _, ok := source["type"]; ok {
		parts = append(parts, fmt.Sprintf("Source: %s", spi.InlineCode(stringArg(source, "type"))))
	}
	if extra := formatParameters(withoutKeys(source, "type", "script")); extra != "" {
		parts = append(parts, extra)
	}
	if script := stringArg(source, "script"); script != "" {
		// The blank line keeps the labels above from running into the fence's label.
		parts = append(parts, "\nScript:\n"+spi.CodeFence("rhai", strings.TrimRight(script, "\n")))
	}
	return strings.Join(parts, "\n")
}

// formatFeedbackBody shows a feedback draft the way it reads in Grok's drafts
// view: title and type first, then the details.
func formatFeedbackBody(input map[string]any) string {
	var parts []string
	for _, field := range []struct{ key, label string }{{"title", "Title"}, {"type", "Type"}} {
		if value := stringArg(input, field.key); value != "" {
			parts = append(parts, fmt.Sprintf("%s: %s", field.label, spi.InlineCode(value)))
		}
	}
	if details := formatTextBlock("Details", stringArg(input, "details")); details != "" {
		parts = append(parts, "\n"+details)
	}
	return strings.Join(parts, "\n")
}

// looksLikeJSON reports whether output should be fenced to stay readable. Some
// Grok tools return a JSON document as their text result, which collapses into
// an unreadable run-on if it is emitted bare.
func looksLikeJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// formatQuestionBody preserves the question, choices and any future fields.
func formatQuestionBody(input map[string]any) string {
	questions, ok := input["questions"].([]any)
	if !ok {
		return spi.RenderGenericJSON(input)
	}
	var b strings.Builder
	for i, raw := range questions {
		question, ok := raw.(map[string]any)
		if !ok {
			return spi.RenderGenericJSON(input)
		}
		fmt.Fprintf(&b, "Question %d: %s\n", i+1, stringArg(question, "question"))
		options, ok := question["options"].([]any)
		if !ok {
			return spi.RenderGenericJSON(input)
		}
		for _, raw := range options {
			option, ok := raw.(map[string]any)
			if !ok {
				return spi.RenderGenericJSON(input)
			}
			fmt.Fprintf(&b, "- %s: %s\n", stringArg(option, "label"), stringArg(option, "description"))
			if extra := spi.RenderGenericJSON(option, "label", "description"); extra != "" {
				b.WriteString(extra + "\n")
			}
		}
		if extra := spi.RenderGenericJSON(question, "question", "options"); extra != "" {
			b.WriteString(extra + "\n")
		}
	}
	return b.String()
}
