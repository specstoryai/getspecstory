package qwencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// renderToolMarkdown fills in a tool's two rendering fields: Summary, when the
// tool has key arguments worth showing in the collapsed heading, and
// FormattedMarkdown, the inner content only (the <tool-use> tags are added by
// pkg/session). The rendered body is also returned, for callers that want it
// without reading it back off the tool.
func renderToolMarkdown(tool *ToolInfo) string {
	if tool == nil {
		return ""
	}

	// Build custom summary for certain tools (appending key parameters)
	var customSummary string
	switch canonicalQwenToolName(tool.Name) {
	case "read_file":
		if filePath := spi.StringValue(tool.Input, "file_path"); filePath != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, filePath)
		}
	case "grep_search":
		pattern := spi.StringValue(tool.Input, "pattern")
		path := spi.StringValue(tool.Input, "path")
		if pattern != "" {
			if path != "" {
				customSummary = fmt.Sprintf("Tool use: **%s** `%s` in `%s`", tool.Name, pattern, path)
			} else {
				customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
			}
		}
	case "glob":
		if pattern := spi.StringValue(tool.Input, "pattern"); pattern != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
		}
	case "list_directory":
		if dirPath := spi.StringValue(tool.Input, "path"); dirPath != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, dirPath)
		}
	case "skill":
		if skill := spi.StringValue(tool.Input, "skill"); skill != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, skill)
		}
	case "tool_search", "web_search":
		if query := spi.StringValue(tool.Input, "query"); query != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, query)
		}
	case "agent":
		if desc := spi.StringValue(tool.Input, "description"); desc != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** — %s", tool.Name, desc)
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
		builder.WriteString("\n\n")
		builder.WriteString(result)
	}
	if notifications := formatTaskNotifications(tool.Output); notifications != "" {
		builder.WriteString("\n\n")
		builder.WriteString(notifications)
	}
	if builder.Len() > 0 {
		builder.WriteString("\n")
	}

	rendered := builder.String()
	tool.FormattedMarkdown = &rendered
	return rendered
}

// formatToolBodyFromInput formats the tool input/body section
func formatToolBodyFromInput(tool *ToolInfo) string {
	switch canonicalQwenToolName(tool.Name) {
	case "run_shell_command", "monitor":
		// monitor streams a long-running command; its input is shaped like a
		// shell command (command + description), so it renders the same way.
		return formatShellBodyFromInput(tool.Input)
	case "ask_user_question":
		return formatQuestions(tool.Input)
	case "notebook_edit":
		var b strings.Builder
		fmt.Fprintf(&b, "Path: `%s`\n\nCell: %s (%s)\n\n", spi.StringValue(tool.Input, "notebook_path"), spi.StringValue(tool.Input, "cell_id"), spi.StringValue(tool.Input, "edit_mode"))
		lang := ""
		if spi.StringValue(tool.Input, "cell_type") == "markdown" {
			lang = "markdown"
		}
		if source := spi.StringValue(tool.Input, "new_source"); source != "" {
			b.WriteString(spi.CodeFence(lang, source))
		}
		if diff := spi.StringValue(tool.Output, "resultDisplay"); strings.Contains(diff, "@@") {
			fmt.Fprintf(&b, "\n\nChanges:\n%s", spi.CodeFence("diff", strings.TrimSpace(diff)))
		}
		return b.String()
	case "exec":
		return spi.CodeFence("javascript", spi.StringValue(tool.Input, "source"))
	case "write_file":
		return formatWriteFileBodyFromInput(tool.Input)
	case "edit":
		return formatEditBodyFromInput(tool)
	case "web_fetch":
		return formatWebFetchBodyFromInput(tool.Input)
	case "agent":
		return formatAgentBodyFromInput(tool.Input)
	case "todo_write":
		return formatTodoBodyFromInput(tool.Input)
	case "skill":
		// The skill name is in the summary; show the arguments it was invoked with.
		if args := spi.StringValue(tool.Input, "args"); args != "" {
			return fmt.Sprintf("Args: %s", args)
		}
		return ""
	case "read_file", "grep_search", "glob", "list_directory", "tool_search", "web_search":
		// Keep optional arguments (line ranges, filters, limits) visible too.
		return formatInputFields(tool.Input)
	default:
		if classifyQwenToolType(tool.Name) == "unknown" {
			return spi.RenderGenericJSON(tool.Input)
		}
		return formatInputFields(tool.Input)
	}
}

// formatToolResultFromOutput formats the tool result/output section
func formatToolResultFromOutput(tool *ToolInfo) string {
	// Errors take priority: render the error message for any tool.
	if errText := outputErrorString(tool.Output); errText != "" {
		return addResultPrefix(formatOutputText(errText))
	}

	switch canonicalQwenToolName(tool.Name) {
	case "todo_write":
		// The body already shows the checklist
		return ""
	case "read_file":
		return formatReadFileResultFromOutput(tool)
	case "run_shell_command":
		return formatShellResultFromOutput(tool.Output)
	case "monitor":
		// The monitor display is only a startup toast. Its model-facing output
		// also records event limits and lifecycle details worth retaining.
		return formatDefaultResultFromOutput(tool.Output)
	case "edit", "write_file":
		return formatDefaultResultFromOutput(tool.Output)
	case "grep_search", "glob", "list_directory":
		return formatSearchListResultFromOutput(tool.Output)
	}

	// Default behavior: show output content
	return formatDefaultResultFromOutput(tool.Output)
}

// formatReadFileResultFromOutput shows file content with syntax highlighting
func formatReadFileResultFromOutput(tool *ToolInfo) string {
	output := outputAsString(tool.Output)
	if output == "" {
		return spi.RenderGenericJSON(tool.Output)
	}

	filePath := spi.StringValue(tool.Input, "file_path")
	lang := spi.LanguageFromPath(filePath)
	return spi.CodeFence(lang, output)
}

// formatShellResultFromOutput shows shell output. The resultDisplay carries the
// raw stdout/stderr, which reads better than the functionResponse's structured
// "Command:/Directory:/Output:" envelope; prefer it when present.
func formatShellResultFromOutput(output map[string]any) string {
	content := ""
	if output != nil {
		if display, ok := output["resultDisplay"].(string); ok && strings.TrimSpace(display) != "" {
			content = strings.TrimRight(display, "\r\n")
		}
	}
	if content == "" {
		content = outputAsString(output)
	}
	if content == "" {
		return spi.RenderGenericJSON(output)
	}
	result := fmt.Sprintf("Result:\n%s", spi.CodeFence("text", content))
	if display := spi.StringValue(output, "resultDisplay"); strings.TrimSpace(display) != "" {
		// Use the last envelope markers: stdout itself can contain lines that
		// look like status fields. Never infer an exit code from arbitrary text.
		envelope := spi.StringValue(output, "output")
		if strings.HasPrefix(envelope, "Command: ") {
			if _, directory, found := strings.Cut(envelope, "\nDirectory: "); found {
				if value, _, found := strings.Cut(directory, "\nOutput: "); found {
					result += "\n\nDirectory: " + value
				}
			}
			if start := strings.LastIndex(envelope, "\nExit Code: "); start >= 0 {
				if errorStart := strings.LastIndex(envelope[:start], "\nError: "); errorStart >= 0 {
					if detail := envelope[errorStart+len("\nError: ") : start]; detail != "(none)" && detail != "" {
						result += "\n\nError:\n" + spi.CodeFence("text", detail)
					}
				}
				for _, line := range strings.Split(envelope[start+1:], "\n") {
					if strings.HasPrefix(line, "Exit Code: ") || (strings.HasPrefix(line, "Signal: ") && line != "Signal: (none)") {
						result += "\n\n" + line
					}
				}
			}
		}
	}
	return result
}

// formatSearchListResultFromOutput fences arbitrary file content safely.
func formatSearchListResultFromOutput(output map[string]any) string {
	content := outputAsString(output)
	if content == "" {
		return spi.RenderGenericJSON(output)
	}
	return spi.CodeFence("text", content)
}

// formatDefaultResultFromOutput builds result with "Result:" prefix
func formatDefaultResultFromOutput(output map[string]any) string {
	content := outputAsString(output)
	if content == "" {
		// Notifications have their own section, including when the immediate
		// response is absent from an interrupted/incomplete transcript.
		return spi.RenderGenericJSON(output, "notifications")
	}

	// Wrap multi-line output in code fence
	formatted := formatOutputText(content)

	// Add "Result:" prefix
	return addResultPrefix(formatted)
}

// formatOutputText wraps multi-line output in code fence, leaves single-line as-is
func formatOutputText(output string) string {
	// Indent the original JSON bytes rather than decoding through float64:
	// large integer IDs and counters must retain their exact values.
	trimmed := strings.TrimSpace(output)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, []byte(trimmed), "", "  "); err == nil {
			return spi.CodeFence("json", pretty.String())
		}
	}
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

func formatShellBodyFromInput(input map[string]any) string {
	command := spi.StringValue(input, "command")
	description := spi.StringValue(input, "description")
	directory := spi.StringValue(input, "directory")

	if command == "" && description == "" && directory == "" {
		return ""
	}

	var builder strings.Builder
	if description != "" {
		fmt.Fprintf(&builder, "%s\n\n", description)
	}
	if directory != "" {
		fmt.Fprintf(&builder, "Directory: `%s`\n\n", directory)
	}

	if command != "" {
		builder.WriteString(spi.CodeFence("bash", command))
	}

	return builder.String()
}

func formatWriteFileBodyFromInput(input map[string]any) string {
	path := spi.StringValue(input, "file_path")
	content := spi.StringValue(input, "content")
	if path == "" && content == "" {
		return ""
	}

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}
	if value, supplied := input["record_as_artifact"]; supplied {
		fmt.Fprintf(&builder, "record_as_artifact: %v\n\n", value)
	}
	if content != "" {
		builder.WriteString(spi.CodeFence(spi.LanguageFromPath(path), content))
	}
	return builder.String()
}

// formatEditBodyFromInput shows an edit as its unified diff when the tool
// outcome carries one (resultDisplay.fileDiff), falling back to the new_string
// input when it doesn't (e.g. the edit was denied or errored).
func formatEditBodyFromInput(tool *ToolInfo) string {
	path := spi.StringValue(tool.Input, "file_path")

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}

	if tool.Output != nil {
		// resultDisplay on an edit is the tool's own fileDiff. The hunk marker
		// distinguishes it from the other shapes that field can hold, such as a
		// plain confirmation string; it is not a sniff of the edited file.
		if diff, ok := tool.Output["resultDisplay"].(string); ok && strings.Contains(diff, "@@") {
			builder.WriteString(spi.CodeFence("diff", strings.TrimSpace(diff)))
			return builder.String()
		}
	}

	oldString := spi.StringValue(tool.Input, "old_string")
	newString := spi.StringValue(tool.Input, "new_string")
	if oldString != "" || newString != "" {
		builder.WriteString(spi.FormatDiffBlock(oldString, newString))
	}

	if builder.Len() == 0 {
		return ""
	}
	return builder.String()
}

func formatWebFetchBodyFromInput(input map[string]any) string {
	url := spi.StringValue(input, "url")
	prompt := spi.StringValue(input, "prompt")
	if url == "" && prompt == "" {
		return ""
	}

	var builder strings.Builder
	if url != "" {
		fmt.Fprintf(&builder, "URL: %s", url)
	}
	if prompt != "" {
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(prompt)
	}
	return builder.String()
}

// formatAgentBodyFromInput shows the delegation prompt. Immediate responses
// and later task notifications are rendered separately in the same tool block;
// the child's complete transcript remains in Qwen's separate subagent file.
func formatAgentBodyFromInput(input map[string]any) string {
	prompt := spi.StringValue(input, "prompt")
	subagent := spi.StringValue(input, "subagent_type")

	var builder strings.Builder
	if subagent != "" {
		fmt.Fprintf(&builder, "Subagent: `%s`\n\n", subagent)
	}
	if prompt != "" {
		builder.WriteString(prompt)
	}
	return builder.String()
}

func formatTodoBodyFromInput(input map[string]any) string {
	todos, ok := input["todos"].([]any)
	if !ok || len(todos) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Todo List:\n")
	for _, raw := range todos {
		if todo, ok := raw.(map[string]any); ok {
			// Qwen todos use "content"; keep "description" as a fallback for
			// forks/versions that use the Gemini field name.
			desc, _ := todo["content"].(string)
			if desc == "" {
				desc, _ = todo["description"].(string)
			}
			status, _ := todo["status"].(string)
			fmt.Fprintf(&builder, "- [%s] %s\n", spi.TodoSymbol(status), strings.TrimSpace(desc))
		}
	}
	return builder.String()
}

// outputAsString extracts the primary output string from tool output
func outputAsString(output map[string]any) string {
	if output == nil {
		return ""
	}

	// Try "output" field first (Qwen's functionResponse success payload)
	if out, ok := output["output"].(string); ok && strings.TrimSpace(out) != "" {
		return strings.TrimRight(out, "\r\n")
	}

	// Try "content" field
	if content, ok := output["content"].(string); ok && strings.TrimSpace(content) != "" {
		return strings.TrimRight(content, "\r\n")
	}

	// Try "resultDisplay" (envelope display form)
	if display, ok := output["resultDisplay"].(string); ok && strings.TrimSpace(display) != "" {
		return strings.TrimRight(display, "\r\n")
	}

	// Try "error" field
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
	}

	return ""
}

// outputErrorString returns the error text when the tool outcome failed, empty otherwise.
func outputErrorString(output map[string]any) string {
	if output == nil {
		return ""
	}
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
	}
	// Status "error" without an error string: fall back to the display form.
	if status, ok := output["status"].(string); ok && (status == "error" || status == "cancelled") {
		if display, ok := output["resultDisplay"].(string); ok && strings.TrimSpace(display) != "" {
			return strings.TrimRight(display, "\r\n")
		}
		if text := spi.StringValue(output, "errorType"); text != "" {
			return text
		}
		return "Tool call " + status
	}
	return ""
}

// formatInputFields gives known tools readable scalar arguments while keeping
// structured arguments intact, with deterministic ordering for stable syncs.
func formatInputFields(input map[string]any) string {
	var b strings.Builder
	for _, key := range slices.Sorted(maps.Keys(input)) {
		value := input[key]
		switch value.(type) {
		case map[string]any, []any:
			data, err := json.MarshalIndent(value, "", "  ")
			if err == nil {
				fmt.Fprintf(&b, "%s:\n%s\n", key, spi.CodeFence("json", string(data)))
			}
		default:
			if text := spi.StringValue(input, key); text != "" {
				fmt.Fprintf(&b, "%s: %s\n\n", key, text)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// formatTaskNotifications keeps lifecycle events distinct from the immediate
// tool response. Their timestamps are delivery times in the parent transcript.
func formatTaskNotifications(output map[string]any) string {
	notifications, _ := output["notifications"].([]any)
	var b strings.Builder
	for i, raw := range notifications {
		notification, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fields, ok := notification["fields"].(map[string]any)
		if !ok {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("Background events:\n\n")
		}
		fmt.Fprintf(&b, "**Event %d — %s**\n\n", i+1, spi.StringValue(notification, "timestamp"))
		metadata := maps.Clone(fields)
		delete(metadata, "result")
		b.WriteString(formatInputFields(metadata))
		if result, exists := fields["result"]; exists {
			if text, ok := result.(string); ok {
				fmt.Fprintf(&b, "\n\nResult:\n%s", spi.CodeFence("text", text))
			} else {
				b.WriteString("\n\n" + formatInputFields(map[string]any{"result": result}))
			}
		}
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

// formatQuestions renders the native question/option objects as a readable
// questionnaire; malformed elements are skipped without losing other rows.
func formatQuestions(input map[string]any) string {
	questions, _ := input["questions"].([]any)
	var b strings.Builder
	for _, raw := range questions {
		question, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := spi.StringValue(question, "question"); text != "" {
			fmt.Fprintf(&b, "%s\n\n", text)
		}
		options, _ := question["options"].([]any)
		for _, raw := range options {
			option, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			label := spi.StringValue(option, "label")
			if label == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s", label)
			if desc := spi.StringValue(option, "description"); desc != "" {
				fmt.Fprintf(&b, ": %s", desc)
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}
