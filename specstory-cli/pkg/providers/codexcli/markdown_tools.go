package codexcli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Todo status constants (used by formatUpdatePlan)
const (
	todoStatusPending    = "pending"
	todoStatusInProgress = "in_progress"
	todoStatusCompleted  = "completed"
)

// formatUpdatePlan formats the update_plan tool usage with plan items as markdown checkboxes
// Expected arguments: JSON string containing {"plan": [{"status": "pending|in_progress|completed", "step": "description"}]}
func formatUpdatePlan(toolName string, argumentsJSON string) string {
	var result strings.Builder

	// Parse the arguments JSON
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		// If parsing fails, return empty (CLI will use default summary)
		return ""
	}

	// Extract plan array
	planRaw, ok := args["plan"].([]interface{})
	if !ok || len(planRaw) == 0 {
		return ""
	}

	// Format as task list
	result.WriteString("**Agent task list:**\n")

	for _, item := range planRaw {
		if itemMap, ok := item.(map[string]interface{}); ok {
			status, _ := itemMap["status"].(string)
			step, _ := itemMap["step"].(string)

			if step == "" {
				continue
			}

			// Determine checkbox state based on status
			var checkbox string
			switch status {
			case todoStatusPending:
				checkbox = "- [ ]"
			case todoStatusInProgress:
				checkbox = "- [⚡]"
			case todoStatusCompleted:
				checkbox = "- [X]"
			default:
				checkbox = "- [ ]"
			}

			// Format the todo line (no priority emoji for Codex)
			result.WriteString(fmt.Sprintf("%s %s\n", checkbox, step))
		}
	}

	return result.String()
}

// formatShellWithSummary formats the shell tool usage with command display
// Returns (summary, body) where:
// - Single-line commands: summary = "`command`", body = ""
// - Multi-line commands: summary = "", body = "```bash\n...\n```"
// Expected arguments: JSON string containing {"command": "...", "workdir": "..."}
func formatShellWithSummary(argumentsJSON string) (string, string) {
	// Parse the arguments JSON
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", ""
	}

	// Extract command string
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return "", ""
	}

	// Multi-line commands go in body with bash code fence
	// Single-line commands go in summary with inline backticks
	if strings.Contains(command, "\n") {
		return "", fmt.Sprintf("```bash\n%s\n```", command)
	}
	return fmt.Sprintf("`%s`", command), ""
}

// formatViewImage formats the view_image tool usage with image path
// Expected arguments: JSON string containing {"path": "/path/to/image.jpg"}
func formatViewImage(toolName string, argumentsJSON string) string {
	// Parse the arguments JSON
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		// If parsing fails, return empty (CLI will use default summary)
		return ""
	}

	// Extract path
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ""
	}

	// Just return the path - CLI will generate default summary
	return fmt.Sprintf("%s\n", path)
}

// capitalizeFirst converts the first character of a string to uppercase.
// Used instead of deprecated strings.Title for simple operation names.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// writePatchSection writes a file operation section (Add/Modify) with its diff content.
// This is a helper to avoid code duplication in formatApplyPatch.
func writePatchSection(result *strings.Builder, operation, filename string, patchContent *strings.Builder) {
	if patchContent.Len() > 0 {
		fmt.Fprintf(result, "**%s: `%s`**\n\n", capitalizeFirst(operation), filename)
		result.WriteString("```diff\n")
		result.WriteString(patchContent.String())
		result.WriteString("```\n\n")
	}
}

// formatApplyPatch formats the apply_patch tool usage with file operations and patch content.
// The input parameter contains the patch in unified diff format with special markers:
// *** Begin Patch, *** Add File:, *** Modify File:, *** Update File:, *** Delete File:, *** End Patch
func formatApplyPatch(toolName string, input string) string {
	var result strings.Builder

	if input == "" {
		return ""
	}

	// Parse the patch to extract file operations
	lines := strings.Split(input, "\n")
	var currentFile string
	var currentOp string // "add", "modify", "delete"
	var patchContent strings.Builder
	inPatch := false

	for _, line := range lines {
		if strings.HasPrefix(line, "*** Begin Patch") {
			inPatch = true
			continue
		}
		if strings.HasPrefix(line, "*** End Patch") {
			// Write any remaining patch content
			if currentFile != "" {
				writePatchSection(&result, currentOp, currentFile, &patchContent)
			}
			break
		}

		if !inPatch {
			continue
		}

		// Check for file operation markers
		if strings.HasPrefix(line, "*** Add File: ") {
			// Write previous file's patch if any
			if currentFile != "" {
				writePatchSection(&result, currentOp, currentFile, &patchContent)
			}
			currentFile = strings.TrimPrefix(line, "*** Add File: ")
			currentOp = "add"
			patchContent.Reset()
		} else if strings.HasPrefix(line, "*** Modify File: ") {
			// Write previous file's patch if any
			if currentFile != "" {
				writePatchSection(&result, currentOp, currentFile, &patchContent)
			}
			currentFile = strings.TrimPrefix(line, "*** Modify File: ")
			currentOp = "modify"
			patchContent.Reset()
		} else if strings.HasPrefix(line, "*** Update File: ") {
			// Write previous file's patch if any
			if currentFile != "" {
				writePatchSection(&result, currentOp, currentFile, &patchContent)
			}
			currentFile = strings.TrimPrefix(line, "*** Update File: ")
			currentOp = "update"
			patchContent.Reset()
		} else if strings.HasPrefix(line, "*** Delete File: ") {
			// Write previous file's patch if any
			if currentFile != "" {
				writePatchSection(&result, currentOp, currentFile, &patchContent)
			}
			currentFile = strings.TrimPrefix(line, "*** Delete File: ")
			patchContent.Reset()
			// For delete operations, just show the header
			result.WriteString(fmt.Sprintf("**Delete: `%s`**\n\n", currentFile))
			currentFile = ""
			currentOp = ""
		} else if currentFile != "" {
			// This is patch content for the current file
			patchContent.WriteString(line)
			patchContent.WriteString("\n")
		}
	}

	return result.String()
}

// formatToolCall formats a function call for markdown output
// Note: shell_command is handled separately via formatShellWithSummary
func formatToolCall(toolName string, argumentsJSON string) string {
	// Check if we have a specific formatter for this tool
	switch toolName {
	case "update_plan":
		return formatUpdatePlan(toolName, argumentsJSON)
	case "view_image":
		return formatViewImage(toolName, argumentsJSON)
	default:
		// Return empty - CLI will use default summary
		return ""
	}
}

// formatCustomToolCall formats a custom tool call for markdown output.
// Custom tools use an input string instead of JSON arguments.
func formatCustomToolCall(toolName string, input string) string {
	// Check if we have a specific formatter for this custom tool
	switch toolName {
	case "apply_patch":
		return formatApplyPatch(toolName, input)
	default:
		// For unknown custom tools, show truncated input if too long
		if input != "" {
			// Show first 200 characters if input is long
			if len(input) > 200 {
				return fmt.Sprintf("\n\nInput (truncated): ```\n%s\n...\n```\n", input[:200])
			} else {
				return fmt.Sprintf("\n\nInput: ```\n%s\n```\n", input)
			}
		}
		return ""
	}
}
