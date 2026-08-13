package grokbuild

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// maxToolBodyRunes caps the inline size of very large tool bodies. Grok can hand
// back a whole file or a directory listing of thousands of lines.
const maxToolBodyRunes = 2000

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
	case "web_fetch", "open_page", "open_page_with_find", "web_search",
		"read_file", "list_dir", "grep", "search_tool",
		"x_user_search", "x_semantic_search", "x_keyword_search":
		// Everything identifying is already in the summary.
		return ""
	default:
		return formatGenericBody(tool.Input)
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
			return fmt.Sprintf("Error:\n%s", spi.CodeFence("text", truncate(text)))
		}
		return fmt.Sprintf("Error: %s", text)
	}

	switch tool.Name {
	case "todo_write":
		// The checklist in the body is the whole story.
		return ""
	case "read_file":
		if text := outputText(tool.Output); text != "" {
			return spi.CodeFence(languageFromPath(stringArg(tool.Input, "target_file")), truncate(text))
		}
		return ""
	case "run_terminal_command", "monitor":
		if text := outputText(tool.Output); text != "" {
			return fmt.Sprintf("Result:\n%s", spi.CodeFence("text", truncate(text)))
		}
		return ""
	case "list_dir", "grep", "search_tool":
		// Already line oriented, so a fence would only add noise.
		return truncate(outputText(tool.Output))
	}

	if text := outputText(tool.Output); text != "" {
		return addResultPrefix(fenceIfMultiline(truncate(text)))
	}
	return ""
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
		builder.WriteString(spi.CodeFence(languageFromPath(path), truncate(content)))
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
	for _, line := range strings.Split(truncate(oldString), "\n") {
		fmt.Fprintf(&diff, "-%s\n", line)
	}
	for _, line := range strings.Split(truncate(newString), "\n") {
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
		builder.WriteString(truncate(prompt))
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
			builder.WriteString(spi.CodeFence("json", truncate(string(encoded))))
		}
	}
	return builder.String()
}

func formatPromptBody(input map[string]any) string {
	prompt := stringArg(input, "prompt")
	if prompt == "" {
		return formatGenericBody(input)
	}
	return truncate(prompt)
}

func formatGenericBody(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	encoded, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return ""
	}
	return spi.CodeFence("json", truncate(string(encoded)))
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

func languageFromPath(path string) string {
	if path == "" {
		return "text"
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == "" {
		return "text"
	}
	return ext
}

// truncate caps runaway tool content, e.g. a whole-file read or a long listing.
func truncate(text string) string {
	runes := []rune(text)
	if len(runes) <= maxToolBodyRunes {
		return text
	}
	return string(runes[:maxToolBodyRunes]) + "\n... (truncated)"
}
