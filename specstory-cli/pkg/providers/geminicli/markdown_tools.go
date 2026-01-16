package geminicli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// formatToolAsMarkdown generates formatted markdown for a ToolInfo (input + output)
// Returns the inner content only (no <tool-use> tags - those are added by pkg/markdown)
// Also sets tool.Summary if a custom summary is needed
func formatToolAsMarkdown(tool *ToolInfo) string {
	if tool == nil {
		return ""
	}

	// Special case for referenced_files - content is already formatted in agent_session.go
	if tool.Name == "referenced_files" {
		if tool.FormattedMarkdown != nil {
			return *tool.FormattedMarkdown
		}
		return ""
	}

	// Build custom summary for certain tools (appending key parameters)
	var customSummary string
	switch tool.Name {
	case "read_file":
		if filePath := inputAsString(tool.Input, "file_path"); filePath != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, filePath)
		}
	case "search_file_content":
		pattern := inputAsString(tool.Input, "pattern")
		include := inputAsString(tool.Input, "include")
		if pattern != "" {
			if include != "" {
				customSummary = fmt.Sprintf("Tool use: **%s** `%s` in `%s`", tool.Name, pattern, include)
			} else {
				customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
			}
		}
	case "glob":
		if pattern := inputAsString(tool.Input, "pattern"); pattern != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, pattern)
		}
	case "list_directory":
		if dirPath := inputAsString(tool.Input, "dir_path"); dirPath != "" {
			customSummary = fmt.Sprintf("Tool use: **%s** `%s`", tool.Name, dirPath)
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
	case "replace", "smart_edit":
		return formatReplaceBodyFromInput(tool.Input)
	case "google_web_search":
		return formatSearchBodyFromInput(tool.Input)
	case "web_fetch":
		return formatWebFetchBodyFromInput(tool.Input)
	case "write_todos":
		return formatTodoBodyFromInput(tool.Input)
	case "read_file", "search_file_content", "glob", "list_directory":
		// Don't show input args - parameters are in the summary
		return ""
	default:
		return formatGenericBodyFromInput(tool.Input)
	}
}

// formatToolResultFromOutput formats the tool result/output section
func formatToolResultFromOutput(tool *ToolInfo) string {
	// Some tools display all their info in the body, so skip the result section
	switch tool.Name {
	case "write_todos":
		return ""
	case "read_file":
		return formatReadFileResultFromOutput(tool)
	case "search_file_content", "glob", "list_directory":
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
	return fmt.Sprintf("```%s\n%s\n```", lang, output)
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
		return fmt.Sprintf("```text\n%s\n```", output)
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
	dir := inputAsString(input, "dir_path")

	if command == "" && dir == "" {
		return ""
	}

	var builder strings.Builder
	if dir != "" {
		builder.WriteString(fmt.Sprintf("Directory: `%s`\n\n", dir))
	}

	if command != "" {
		builder.WriteString("```bash\n")
		builder.WriteString(command)
		builder.WriteString("\n```")
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
		builder.WriteString(fmt.Sprintf("Path: `%s`\n\n", path))
	}
	if content != "" {
		builder.WriteString("```")
		builder.WriteString(languageFromPath(path))
		builder.WriteString("\n")
		builder.WriteString(content)
		builder.WriteString("\n```")
	}
	return builder.String()
}

func formatReplaceBodyFromInput(input map[string]interface{}) string {
	path := inputAsString(input, "file_path")
	newString := inputAsString(input, "new_string")
	if path == "" && newString == "" {
		return ""
	}

	var builder strings.Builder
	if path != "" {
		builder.WriteString(fmt.Sprintf("Path: `%s`\n\n", path))
	}
	if newString != "" {
		builder.WriteString("```diff\n")
		builder.WriteString(truncate(newString, 2000))
		builder.WriteString("\n```")
	}
	return builder.String()
}

func formatSearchBodyFromInput(input map[string]interface{}) string {
	query := inputAsString(input, "query")
	if query == "" {
		return ""
	}
	return fmt.Sprintf("Query: %s", query)
}

func formatWebFetchBodyFromInput(input map[string]interface{}) string {
	prompt := inputAsString(input, "prompt")
	if prompt == "" {
		return ""
	}
	// Show the prompt directly without a "Query:" or "Prompt:" prefix
	return prompt
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
			desc, _ := todo["description"].(string)
			status, _ := todo["status"].(string)
			builder.WriteString(fmt.Sprintf("- [%s] %s\n", todoStatusSymbol(status), strings.TrimSpace(desc)))
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
	return fmt.Sprintf("```json\n%s\n```", string(bytes))
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

// outputAsString extracts the output string from tool output
func outputAsString(output map[string]interface{}) string {
	if output == nil {
		return ""
	}

	// Try "output" field first (common pattern)
	if out, ok := output["output"].(string); ok && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}

	// Try "content" field
	if content, ok := output["content"].(string); ok && strings.TrimSpace(content) != "" {
		return strings.TrimSpace(content)
	}

	// Try "error" field
	if errStr, ok := output["error"].(string); ok && strings.TrimSpace(errStr) != "" {
		return strings.TrimSpace(errStr)
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
