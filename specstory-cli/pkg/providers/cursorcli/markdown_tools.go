package cursorcli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// getLanguageFromExtension returns the language identifier for syntax highlighting based on file extension
func getLanguageFromExtension(filePath string) string {
	if filePath == "" {
		return ""
	}

	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	if ext == "" {
		return ""
	}

	// Map common extensions to language identifiers
	switch ext {
	case "js":
		return "javascript"
	case "ts":
		return "typescript"
	case "py":
		return "python"
	case "rb":
		return "ruby"
	case "yml":
		return "yaml"
	case "md":
		return "markdown"
	case "jsx":
		return "javascript"
	case "tsx":
		return "typescript"
	case "h", "c":
		return "c"
	case "hpp", "cpp", "cc":
		return "cpp"
	case "cs":
		return "csharp"
	case "rs":
		return "rust"
	default:
		// Common extensions that don't need mapping: go, java, json, xml, sh, html, css, etc.
		return ext
	}
}

// formatWriteTool formats Write tool usage with file content in triple backticks
// Expected args: "path" (filename) and "contents" (file content)
// Returns just the formatted content (path is handled in the summary)
func formatWriteTool(args map[string]interface{}) string {
	// Extract file path and contents
	filePath, _ := args["path"].(string)
	contents, hasContents := args["contents"].(string)

	if !hasContents {
		return ""
	}

	// Get language for syntax highlighting
	lang := getLanguageFromExtension(filePath)

	// Escape triple backticks in content to prevent breaking the code block
	escapedContent := strings.ReplaceAll(contents, "```", "\\```")

	// Add content in triple backticks with language hint
	return fmt.Sprintf("```%s\n%s\n```", lang, escapedContent)
}

// formatStrReplaceTool formats StrReplace tool usage with git diff style output
// Expected args: "file_path" (filename), "old_string" (text to replace), and "new_string" (replacement text)
// Returns just the diff content (no summary - "StrReplace" alone isn't value-add)
func formatStrReplaceTool(args map[string]interface{}) string {
	var result strings.Builder

	// Extract arguments
	filePath, _ := args["file_path"].(string)
	oldString, hasOld := args["old_string"].(string)
	newString, hasNew := args["new_string"].(string)

	if hasOld && hasNew {
		// Split strings into lines for diff-style formatting
		oldLines := strings.Split(oldString, "\n")
		newLines := strings.Split(newString, "\n")

		// Format as a diff
		result.WriteString("```diff\n")

		// Add file path as a header if available
		if filePath != "" {
			result.WriteString(fmt.Sprintf("--- %s\n", filePath))
			result.WriteString(fmt.Sprintf("+++ %s\n", filePath))
		}

		// Add old lines with - prefix
		for _, line := range oldLines {
			result.WriteString(fmt.Sprintf("-%s\n", line))
		}

		// Add new lines with + prefix
		for _, line := range newLines {
			result.WriteString(fmt.Sprintf("+%s\n", line))
		}

		result.WriteString("```")
	} else {
		// Fallback if arguments are missing
		if filePath != "" {
			result.WriteString(fmt.Sprintf("File: %s", filePath))
		}
	}

	return result.String()
}

// formatDeleteTool formats Delete tool usage
// Expected args: "path" (file or directory path to delete)
// Returns custom summary content (just the path)
func formatDeleteTool(args map[string]interface{}) string {
	path, hasPath := args["path"].(string)

	if hasPath && path != "" {
		return fmt.Sprintf("`%s`", path)
	}

	// Fallback: empty string means use default summary
	return ""
}

// formatGrepTool formats Grep tool usage
// Expected args: "pattern" (search pattern), "path" (search location), "output_mode" (optional)
// Returns custom summary content (search details without "Tool use: Grep" prefix)
func formatGrepTool(args map[string]interface{}) string {
	pattern, hasPattern := args["pattern"].(string)
	path, hasPath := args["path"].(string)

	// Check for non-empty values
	hasPattern = hasPattern && pattern != ""
	hasPath = hasPath && path != ""

	// Build the formatted output
	if hasPattern && hasPath {
		return fmt.Sprintf("pattern `%s` path `%s`", pattern, path)
	} else if hasPattern {
		return fmt.Sprintf("pattern `%s`", pattern)
	} else if hasPath {
		return fmt.Sprintf("path `%s`", path)
	}

	// Fallback: empty string means use default summary
	return ""
}

// formatGlobTool formats Glob tool usage
// Expected args: "glob_pattern" (file pattern to search)
// Returns custom summary content (pattern without "Tool use: Glob" prefix)
func formatGlobTool(args map[string]interface{}) string {
	pattern, hasPattern := args["glob_pattern"].(string)

	if hasPattern && pattern != "" {
		return fmt.Sprintf("pattern `%s`", pattern)
	}

	// Fallback: empty string means use default summary
	return ""
}

// formatReadTool formats Read tool usage
// Expected args: "path" (file path to read)
// Returns custom summary content (just the path)
func formatReadTool(args map[string]interface{}) string {
	path, hasPath := args["path"].(string)

	if hasPath && path != "" {
		return fmt.Sprintf("`%s`", path)
	}

	// Fallback: empty string means use default summary
	return ""
}

// formatLSTool formats LS tool usage
// Expected args: "target_directory" (directory to list)
// Returns custom summary content (just the directory)
func formatLSTool(args map[string]interface{}) string {
	targetDir, hasTargetDir := args["target_directory"].(string)

	if hasTargetDir && targetDir != "" {
		return fmt.Sprintf("`%s`", targetDir)
	}

	// Fallback: empty string means use default summary
	return ""
}

// formatShellTool formats Shell tool usage
// Expected args: "command" (shell command to execute), "description" (optional description)
// Returns custom summary content (just the command)
func formatShellTool(args map[string]interface{}) string {
	command, hasCommand := args["command"].(string)

	if hasCommand && command != "" {
		return fmt.Sprintf("`%s`", command)
	}

	// Fallback: empty string means use default summary
	return ""
}

// formatMultiStrReplaceTool formats MultiStrReplace tool usage
// Expected args: "file_path" (file to edit) and "edits" (array of replacements)
// Returns custom summary content (file path and edits)
func formatMultiStrReplaceTool(args map[string]interface{}) string {
	var result strings.Builder

	// Get the file path
	filePath, hasPath := args["file_path"].(string)
	if hasPath && filePath != "" {
		result.WriteString(fmt.Sprintf("`%s`", filePath))
	}

	// Get the edits array
	editsRaw, hasEdits := args["edits"].([]interface{})
	if hasEdits && len(editsRaw) > 0 {
		result.WriteString("\n")
		for i, editRaw := range editsRaw {
			if edit, ok := editRaw.(map[string]interface{}); ok {
				oldStr, hasOld := edit["old_string"].(string)
				newStr, hasNew := edit["new_string"].(string)
				replaceAll, hasReplaceAll := edit["replace_all"].(bool)

				if hasOld && hasNew {
					result.WriteString(fmt.Sprintf("- Replace: `%s` → `%s`", oldStr, newStr))
					if hasReplaceAll && replaceAll {
						result.WriteString(" (all occurrences)")
					}
					if i < len(editsRaw)-1 {
						result.WriteString("\n")
					}
				}
			}
		}
	}

	return result.String()
}

// formatGrepResult formats Grep tool results, stripping workspace tags
// Returns just the cleaned result content (no "Result:" prefix)
func formatGrepResult(result string) string {
	// Regular expression to match workspace_result tags and extract content
	// Matches <workspace_result ...> content </workspace_result>
	re := regexp.MustCompile(`(?s)<workspace_result[^>]*>\n?(.*?)\n?</workspace_result>`)
	matches := re.FindStringSubmatch(result)

	var cleanResult string
	if len(matches) > 1 {
		// Extract the content between the tags
		cleanResult = strings.TrimSpace(matches[1])
	} else {
		// If no workspace tags found, use the original result
		cleanResult = strings.TrimSpace(result)
	}

	return cleanResult
}

// formatToolResult formats tool result output in triple backticks
// Returns just the code block (no "Result:" prefix)
func formatToolResult(result string) string {
	if result == "" {
		return ""
	}

	var output strings.Builder

	// Escape triple backticks in result to prevent breaking the code block
	escapedResult := strings.ReplaceAll(result, "```", "\\```")

	output.WriteString(fmt.Sprintf("```\n%s\n```", escapedResult))

	return output.String()
}

// Todo status constants
const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusCompleted  = "completed"
)

// formatTodoList formats a list of todos from TodoWrite tool-call as markdown checkboxes
// Expected input: []interface{} containing maps with keys: "id", "content", "status"
// status: "pending", "in_progress", "completed"
// When merge: true, todos might only have id and status (partial updates)
func formatTodoList(todos []interface{}) string {
	var result strings.Builder
	var hasValidTodos bool

	for _, todo := range todos {
		if todoMap, ok := todo.(map[string]interface{}); ok {
			// Skip todos without content (these are partial updates when merge: true)
			content, hasContent := todoMap["content"].(string)
			if !hasContent || content == "" {
				continue
			}

			// Only write header if we have at least one valid todo
			if !hasValidTodos {
				result.WriteString("**Agent task list:**\n")
				hasValidTodos = true
			}

			// Extract status, default to pending if missing
			status, ok := todoMap["status"].(string)
			if !ok {
				status = TodoStatusPending
			}

			// Determine checkbox state based on status
			var checkbox string
			switch status {
			case TodoStatusPending:
				checkbox = "- [ ]"
			case TodoStatusInProgress:
				checkbox = "- [⚡]"
			case TodoStatusCompleted:
				checkbox = "- [X]"
			default:
				checkbox = "- [ ]"
			}

			// Format the todo line
			result.WriteString(fmt.Sprintf("%s %s\n", checkbox, content))
		}
	}

	return result.String()
}

// cleanTodoResult removes the "(id: X)" suffixes from TodoWrite tool-result strings
// Input: Raw result string from TodoWrite tool with ID suffixes
// Output: Cleaned string without ID suffixes
func cleanTodoResult(result string) string {
	// Use regex to remove all occurrences of " (id: <anything>)" pattern
	re := regexp.MustCompile(` \(id: [^)]+\)`)
	return re.ReplaceAllString(result, "")
}
