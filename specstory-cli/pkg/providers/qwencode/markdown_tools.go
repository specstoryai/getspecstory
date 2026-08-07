package qwencode

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

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
	case "read_file":
		if filePath := inputAsString(tool.Input, "file_path"); filePath != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, filePath)
		}
	case "grep_search":
		pattern := inputAsString(tool.Input, "pattern")
		path := inputAsString(tool.Input, "path")
		if pattern != "" {
			if path != "" {
				customSummary = fmt.Sprintf("Tool use: **%s** `%s` in `%s`", tool.Name, pattern, path)
			} else {
				customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
			}
		}
	case "glob":
		if pattern := inputAsString(tool.Input, "pattern"); pattern != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
		}
	case "list_directory":
		if dirPath := inputAsString(tool.Input, "path"); dirPath != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, dirPath)
		}
	case "skill":
		if skill := inputAsString(tool.Input, "skill"); skill != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, skill)
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
	if builder.Len() > 0 {
		builder.WriteString("\n")
	}

	return builder.String()
}

// formatToolBodyFromInput formats the tool input/body section
func formatToolBodyFromInput(tool *ToolInfo) string {
	switch tool.Name {
	case "run_shell_command":
		return formatShellBodyFromInput(tool.Input)
	case "write_file":
		return formatWriteFileBodyFromInput(tool.Input)
	case "edit", "replace", "smart_edit":
		return formatEditBodyFromInput(tool)
	case "web_fetch":
		return formatWebFetchBodyFromInput(tool.Input)
	case "todo_write", "write_todos":
		return formatTodoBodyFromInput(tool.Input)
	case "read_file", "grep_search", "glob", "list_directory", "skill":
		// Don't show input args - parameters are in the summary
		return ""
	default:
		return formatGenericBodyFromInput(tool.Input)
	}
}

// formatToolResultFromOutput formats the tool result/output section
func formatToolResultFromOutput(tool *ToolInfo) string {
	// Errors take priority: render the error message for any tool.
	if errText := outputErrorString(tool.Output); errText != "" {
		return addResultPrefix(formatOutputText(errText))
	}

	switch tool.Name {
	case "todo_write", "write_todos":
		// The body already shows the checklist
		return ""
	case "read_file":
		return formatReadFileResultFromOutput(tool)
	case "run_shell_command":
		return formatShellResultFromOutput(tool.Output)
	case "edit", "replace", "smart_edit", "write_file":
		// The body already shows the diff/content
		return ""
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
		return ""
	}

	filePath := inputAsString(tool.Input, "file_path")
	lang := languageFromPath(filePath)
	return spi.CodeFence(lang, output)
}

// formatShellResultFromOutput shows shell output. The resultDisplay carries the
// raw stdout/stderr, which reads better than the functionResponse's structured
// "Command:/Directory:/Output:" envelope; prefer it when present.
func formatShellResultFromOutput(output map[string]interface{}) string {
	content := ""
	if output != nil {
		if display, ok := output["resultDisplay"].(string); ok && strings.TrimSpace(display) != "" {
			content = strings.TrimSpace(display)
		}
	}
	if content == "" {
		content = outputAsString(output)
	}
	if content == "" {
		return ""
	}
	return fmt.Sprintf("Result:\n%s", spi.CodeFence("text", content))
}

// formatSearchListResultFromOutput shows raw output without code fence
func formatSearchListResultFromOutput(output map[string]interface{}) string {
	content := outputAsString(output)
	if content == "" {
		return ""
	}
	return content
}

// formatDefaultResultFromOutput builds result with "Result:" prefix
func formatDefaultResultFromOutput(output map[string]interface{}) string {
	content := outputAsString(output)
	if content == "" {
		return ""
	}

	// Wrap multi-line output in code fence
	formatted := formatOutputText(content)

	// Add "Result:" prefix
	return addResultPrefix(formatted)
}

// formatOutputText wraps multi-line output in code fence, leaves single-line as-is
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

func formatWriteFileBodyFromInput(input map[string]interface{}) string {
	path := inputAsString(input, "file_path")
	content := inputAsString(input, "content")
	if path == "" && content == "" {
		return ""
	}

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}
	if content != "" {
		builder.WriteString(spi.CodeFence(languageFromPath(path), content))
	}
	return builder.String()
}

// formatEditBodyFromInput shows an edit as its unified diff when the tool
// outcome carries one (resultDisplay.fileDiff), falling back to the new_string
// input when it doesn't (e.g. the edit was denied or errored).
func formatEditBodyFromInput(tool *ToolInfo) string {
	path := inputAsString(tool.Input, "file_path")

	var builder strings.Builder
	if path != "" {
		fmt.Fprintf(&builder, "Path: `%s`\n\n", path)
	}

	if tool.Output != nil {
		if diff, ok := tool.Output["resultDisplay"].(string); ok && strings.Contains(diff, "@@") {
			builder.WriteString(spi.CodeFence("diff", truncate(strings.TrimSpace(diff), 2000)))
			return builder.String()
		}
	}

	if newString := inputAsString(tool.Input, "new_string"); newString != "" {
		builder.WriteString(spi.CodeFence("diff", truncate(newString, 2000)))
	}

	if builder.Len() == 0 {
		return ""
	}
	return builder.String()
}

func formatWebFetchBodyFromInput(input map[string]interface{}) string {
	url := inputAsString(input, "url")
	prompt := inputAsString(input, "prompt")
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

func formatTodoBodyFromInput(input map[string]interface{}) string {
	todos, ok := input["todos"].([]interface{})
	if !ok || len(todos) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("Todo List:\n")
	for _, raw := range todos {
		if todo, ok := raw.(map[string]interface{}); ok {
			// Qwen todos use "content"; keep "description" as a fallback for
			// forks/versions that use the Gemini field name.
			desc, _ := todo["content"].(string)
			if desc == "" {
				desc, _ = todo["description"].(string)
			}
			status, _ := todo["status"].(string)
			fmt.Fprintf(&builder, "- [%s] %s\n", todoStatusSymbol(status), strings.TrimSpace(desc))
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

func formatGenericBodyFromInput(input map[string]interface{}) string {
	if len(input) == 0 {
		return ""
	}
	bytes, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return ""
	}
	return spi.CodeFence("json", string(bytes))
}

// inputAsString extracts a string value from tool input
func inputAsString(input map[string]interface{}, key string) string {
	if input == nil {
		return ""
	}
	val, ok := input[key]
	if !ok {
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

	// Try "output" field first (Qwen's functionResponse success payload)
	if out, ok := output["output"].(string); ok && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}

	// Try "content" field
	if content, ok := output["content"].(string); ok && strings.TrimSpace(content) != "" {
		return strings.TrimSpace(content)
	}

	// Try "resultDisplay" (envelope display form)
	if display, ok := output["resultDisplay"].(string); ok && strings.TrimSpace(display) != "" {
		return strings.TrimSpace(display)
	}

	// Try "error" field
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
	}

	return ""
}

// outputErrorString returns the error text when the tool outcome failed, empty otherwise.
func outputErrorString(output map[string]interface{}) string {
	if output == nil {
		return ""
	}
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
	}
	// Status "error" without an error string: fall back to the display form.
	if status, ok := output["status"].(string); ok && status == "error" {
		if display, ok := output["resultDisplay"].(string); ok && strings.TrimSpace(display) != "" {
			return strings.TrimSpace(display)
		}
	}
	return ""
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
