package grokbuild

import (
	"encoding/json"
	"fmt"
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

func formatParameters(input map[string]any) string {
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		value := stringArg(input, key)
		label := map[string]string{"target_file": "Path", "target_directory": "Directory", "file_path": "Path", "offset": "Offset", "limit": "Limit", "pattern": "Pattern", "path": "Path", "glob": "Glob", "background": "Background", "timeout_ms": "Timeout (ms)", "persistent": "Persistent", "head_limit": "Result limit", "task_id": "Task ID", "task_ids": "Task IDs"}[key]
		if label == "" {
			label = key
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
	case "workflow":
		if source, ok := tool.Input["source"].(map[string]any); ok {
			return "Workflow source:\n" + formatParameters(source) + "\n" + spi.RenderGenericJSON(tool.Input, "source")
		}
		return spi.RenderGenericJSON(tool.Input)
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
		text := outputText(tool.Output)
		if text == "" {
			return "Error: the tool call failed"
		}
		if strings.Contains(text, "\n") {
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
		if text := outputText(tool.Output); text != "" {
			return fmt.Sprintf("Result:\n%s", spi.CodeFence("text", text))
		}
		return ""
	case "search_tool":
		if rendered := formatToolCatalog(outputText(tool.Output)); rendered != "" {
			return rendered
		}
		fallthrough
	case "list_dir", "grep":
		text := outputText(tool.Output)
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

	if text := outputText(tool.Output); text != "" {
		return addResultPrefix(fenceIfMultiline(text))
	}
	return ""
}

// formatToolCatalog gives the observed MCP discovery envelope a readable
// hierarchy while retaining schemas and unfamiliar metadata without loss.
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
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				return ""
			}
			fmt.Fprintf(&b, "\n**%s**\n\n%s\n\n", stringArg(tool, "tool_name"), stringArg(tool, "description"))
			b.WriteString(spi.RenderGenericJSON(tool, "tool_name", "description") + "\n")
		}
		b.WriteString(spi.RenderGenericJSON(group, "server", "tools") + "\n")
	}
	b.WriteString("\n" + spi.RenderGenericJSON(catalog, "results"))
	return strings.TrimSpace(b.String())
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
	if strings.Contains(text, "\n") {
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
	if text, ok := output["output"].(string); ok && strings.TrimSpace(text) != "" {
		return strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, ansi.Strip(text)))
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
