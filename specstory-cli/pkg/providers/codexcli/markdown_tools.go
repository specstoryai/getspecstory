package codexcli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
			fmt.Fprintf(&result, "%s %s\n", checkbox, step)
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
		return "", spi.CodeFence("bash", command)
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

// formatRequestUserInput formats the request_user_input tool as each question with its
// options, followed by the user's answer once the output has been recorded. The answers
// are the whole point of this tool, so any answer that can't be matched to a question is
// still rendered rather than dropped.
// Expected input: {"questions": [{"id": "...", "header": "...", "question": "...", "options": [{"label": "...", "description": "..."}]}]}
// Expected output: {"answers": {"<question id>": {"answers": ["<option label>", "user_note: <free text>"]}}}
// Returns "" when the input has no questions, so the caller falls back to generic rendering.
func formatRequestUserInput(input map[string]interface{}, output map[string]interface{}) string {
	questions, _ := input["questions"].([]interface{})
	if len(questions) == 0 {
		return ""
	}

	answersByID := requestUserInputAnswers(output)
	var result strings.Builder
	answeredIDs := make(map[string]bool)

	for _, item := range questions {
		question, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := question["id"].(string)
		header, _ := question["header"].(string)
		text, _ := question["question"].(string)

		if result.Len() > 0 {
			result.WriteString("\n")
		}
		if header != "" {
			fmt.Fprintf(&result, "**%s:** %s\n", header, text)
		} else {
			fmt.Fprintf(&result, "**%s**\n", text)
		}

		options, _ := question["options"].([]interface{})
		if len(options) > 0 {
			result.WriteString("\n")
		}
		for _, optionItem := range options {
			option, ok := optionItem.(map[string]interface{})
			if !ok {
				continue
			}
			label, _ := option["label"].(string)
			description, _ := option["description"].(string)
			if description != "" {
				fmt.Fprintf(&result, "- %s: %s\n", label, description)
			} else {
				fmt.Fprintf(&result, "- %s\n", label)
			}
		}

		// A nil output means the call is still waiting on the user; there's no answer to
		// report yet, as opposed to a recorded output with nothing selected (cancelled).
		if output != nil {
			result.WriteString("\n")
			writeRequestUserInputAnswer(&result, "Answer", answersByID[id])
			answeredIDs[id] = true
		}
	}

	// Answers keyed by an id that none of the questions carry. Sorted for stable output.
	var orphanIDs []string
	for id := range answersByID {
		if !answeredIDs[id] {
			orphanIDs = append(orphanIDs, id)
		}
	}
	sort.Strings(orphanIDs)
	for _, id := range orphanIDs {
		result.WriteString("\n")
		writeRequestUserInputAnswer(&result, fmt.Sprintf("Answer (%s)", id), answersByID[id])
	}

	return result.String()
}

// requestUserInputAnswers extracts the answers from a request_user_input output, keyed by
// question id. Returns nil when the output holds no answers.
func requestUserInputAnswers(output map[string]interface{}) map[string][]string {
	answersMap, _ := output["answers"].(map[string]interface{})
	if len(answersMap) == 0 {
		return nil
	}

	answersByID := make(map[string][]string, len(answersMap))
	for id, entry := range answersMap {
		entryMap, _ := entry.(map[string]interface{})
		values, _ := entryMap["answers"].([]interface{})
		for _, value := range values {
			if s, ok := value.(string); ok && s != "" {
				answersByID[id] = append(answersByID[id], s)
			}
		}
	}
	return answersByID
}

// writeRequestUserInputAnswer writes one question's answers: inline when there's a single
// answer, as a list when the user picked an option and also left a note.
func writeRequestUserInputAnswer(result *strings.Builder, label string, answers []string) {
	switch len(answers) {
	case 0:
		fmt.Fprintf(result, "**%s:** _No answer_\n", label)
	case 1:
		fmt.Fprintf(result, "**%s:** %s\n", label, answers[0])
	default:
		fmt.Fprintf(result, "**%s:**\n", label)
		for _, answer := range answers {
			fmt.Fprintf(result, "- %s\n", answer)
		}
	}
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
		result.WriteString(spi.CodeFence("diff", strings.TrimRight(patchContent.String(), "\n")))
		result.WriteString("\n\n")
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
			fmt.Fprintf(&result, "**Delete: `%s`**\n\n", currentFile)
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
		// Preserve arbitrary custom input, including embedded Markdown fences.
		if input != "" {
			return "\n\nInput:\n" + spi.CodeFence("", input) + "\n"
		}
		return ""
	}
}
