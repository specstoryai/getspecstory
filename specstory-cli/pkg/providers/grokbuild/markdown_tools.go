package grokbuild

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// maxDiffRunes caps a rendered edit diff, which is built from both halves of a
// find and replace and can otherwise reach the size of two whole files.
//
// Only the diff is capped. Tool output and written file content are the record
// the user came for, so they are kept whole, matching the sibling providers.
const maxDiffRunes = 2000

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
	case "web_fetch", "open_page", "open_page_with_find":
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
	case "x_user_search", "x_semantic_search", "x_keyword_search":
		if query := stringArg(tool.Input, "query"); query != "" {
			return fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, query)
		}
	}
	return ""
}

func formatToolBody(tool *ToolInfo) string {
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
	case "image_gen", "image_edit", "image_to_video", "reference_to_video":
		return formatPromptBody(tool.Input)
	case "web_search":
		return formatWebSearchBody(tool.Input)
	case "x_user_search", "x_semantic_search", "x_keyword_search", "x_thread_fetch":
		return formatXSearchBody(tool.Input)
	case "open_page", "open_page_with_find":
		return formatOpenPageBody(tool.Input)
	case "web_fetch", "read_file", "list_dir", "grep", "search_tool":
		// Everything identifying is already in the summary.
		return ""
	default:
		return spi.RenderGenericJSON(tool.Input)
	}
}

func formatToolResult(tool *ToolInfo) string {
	// A failed call reads as an error first, whatever the tool was. Grok records
	// the failure in events.jsonl rather than in the result text, so without this
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
	case "list_dir", "grep", "search_tool":
		text := outputText(tool.Output)
		if text == "" {
			return ""
		}
		// These are usually line oriented, where a fence would only add noise.
		// search_tool returns a JSON document though, which collapses into an
		// unreadable run-on unless it is fenced.
		if looksLikeJSON(text) {
			return fmt.Sprintf("Result:\n%s", spi.CodeFence("json", text))
		}
		return text
	}

	if text := outputText(tool.Output); text != "" {
		return addResultPrefix(fenceIfMultiline(text))
	}
	return ""
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

// formatOpenPageBody names the page that was opened. Grok records no result for
// these backend calls, so without a body the tool would render as nothing at all
// and the shared renderer would fall back to an empty Result heading.
func formatOpenPageBody(input map[string]any) string {
	url := stringArg(input, "url")
	if url == "" {
		return ""
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "- url: %s\n", url)
	if pattern := stringArg(input, "pattern"); pattern != "" {
		fmt.Fprintf(&builder, "- find: `%s`\n", pattern)
	}
	return builder.String()
}

// formatXSearchBody shows the arguments an X search ran with. They vary by tool
// (query, post_id, count, mode), so render whatever is present.
func formatXSearchBody(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}

	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		value := stringArg(input, key)
		if value == "" {
			continue
		}
		fmt.Fprintf(&builder, "- %s: `%s`\n", key, value)
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
	for _, line := range strings.Split(truncate(oldString, maxDiffRunes), "\n") {
		fmt.Fprintf(&diff, "-%s\n", line)
	}
	for _, line := range strings.Split(truncate(newString, maxDiffRunes), "\n") {
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

func formatPromptBody(input map[string]any) string {
	prompt := stringArg(input, "prompt")
	if prompt == "" {
		return spi.RenderGenericJSON(input)
	}
	return prompt
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

// isErrorOutput reports whether the tool call failed. Grok records the failure in
// events.jsonl rather than in the result text, and the parser folds it in here.
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
		return strings.TrimSpace(text)
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

// truncate caps text at limit runes.
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

// looksLikeJSON reports whether output should be fenced to stay readable. Some
// Grok tools return a JSON document as their text result, which collapses into
// an unreadable run-on if it is emitted bare.
func looksLikeJSON(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}
