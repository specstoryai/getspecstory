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
	case "bash", "bash_input":
		return formatShellBodyFromInput(tool.Input)
	case "write_file":
		return formatWriteFileBodyFromInput(tool.Input)
	case "edit_file":
		return formatEditBodyFromInput(tool.Input)
	case "write_todos":
		return formatTodoBodyFromInput(tool.Input)
	case "subagent_spawn":
		return formatSubagentBodyFromInput(tool.Input)
	case "read_file", "search", "web_search":
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
	case "search":
		// ripgrep output is already file:line:text; fencing it adds nothing
		return outputAsString(tool.Output)
	default:
		return formatDefaultResultFromOutput(tool.Output)
	}
}

// formatShellBodyFromInput renders a shell invocation. bash carries a command,
// bash_input carries the characters typed into an already-running PTY.
func formatShellBodyFromInput(input map[string]interface{}) string {
	command := inputAsString(input, "command")
	if command == "" {
		command = inputAsString(input, "chars")
	}
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

	// Backgrounded and PTY chunks report execution state instead of output;
	// there is nothing better to show than the record itself.
	text, ok := payload["output"].(string)
	if !ok {
		return addResultPrefix(formatOutputText(raw))
	}

	var builder strings.Builder
	if exitCode := schema.GetIntFromMap(payload, "exit_code"); exitCode != 0 {
		fmt.Fprintf(&builder, "Exit code: %d\n\n", exitCode)
	}
	if trimmed := strings.TrimRight(text, "\n"); trimmed != "" {
		builder.WriteString(spi.CodeFence("text", trimmed))
	}
	if builder.Len() == 0 {
		return ""
	}
	return addResultPrefix(builder.String())
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
		builder.WriteString(spi.CodeFence(spi.LanguageFromPath(path), content))
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
		fmt.Fprintf(&builder, "- [%s] %s\n", todoStatusSymbol(status), strings.TrimSpace(text))
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

// formatSubagentBodyFromInput shows what a spawned subagent was asked to do.
// The subagent's own transcript lives in a separate file, so the objective plus
// the spawn result is all the parent session records about it.
func formatSubagentBodyFromInput(input map[string]interface{}) string {
	role := inputAsString(input, "role")
	objective := inputAsString(input, "objective")

	var builder strings.Builder
	if role != "" {
		fmt.Fprintf(&builder, "Role: `%s`\n\n", role)
	}
	if objective != "" {
		builder.WriteString(objective)
	}
	return builder.String()
}

// formatDefaultResultFromOutput builds result with "Result:" prefix
func formatDefaultResultFromOutput(output map[string]interface{}) string {
	content := outputAsString(output)
	if content == "" {
		return ""
	}
	return addResultPrefix(formatOutputText(content))
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
