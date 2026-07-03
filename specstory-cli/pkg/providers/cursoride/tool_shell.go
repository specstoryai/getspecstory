package cursoride

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ShellCommandHandler handles run_terminal_cmd, run_terminal_command, and run_terminal_command_v2 tool invocations
type ShellCommandHandler struct{}

// ShellCommandRawArgs represents raw arguments for terminal command tools
type ShellCommandRawArgs struct {
	Command string `json:"command,omitempty"`
}

// ShellCommandParams represents parameters for terminal command tools
type ShellCommandParams struct {
	Command string `json:"command,omitempty"`
}

// ShellCommandResult represents the result of a terminal command execution
type ShellCommandResult struct {
	Output string `json:"output,omitempty"`
}

// AdaptMessage formats terminal command tool invocations as markdown
func (h *ShellCommandHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	var rawArgs ShellCommandRawArgs
	if bubble.RawArgs != "" {
		// Parse rawArgs, but ignore errors (non-fatal)
		_ = json.Unmarshal([]byte(bubble.RawArgs), &rawArgs)
	}

	var params ShellCommandParams
	if bubble.Params != "" {
		// Parse params, but ignore errors (non-fatal)
		_ = json.Unmarshal([]byte(bubble.Params), &params)
	}

	var result ShellCommandResult
	if bubble.Result != "" {
		// Parse result, but ignore errors (non-fatal)
		_ = json.Unmarshal([]byte(bubble.Result), &result)
	}

	// Get command from params or rawArgs (prefer params)
	command := params.Command
	if command == "" {
		command = rawArgs.Command
	}

	// If we have a command, show it in the summary and as a bash block
	var message strings.Builder
	if command != "" {
		summary = fmt.Sprintf("Tool use: **%s** • Run command: %s", escapeSummaryText(bubble.Name), escapeCodeBlock(command))
		fmt.Fprintf(&message, "```bash\n%s\n```", escapeCodeBlock(command))
	} else {
		summary = fmt.Sprintf("Tool use: **%s**", escapeSummaryText(bubble.Name))
	}

	// If we have output, show it in a code block
	if result.Output != "" {
		fmt.Fprintf(&message, "\n\n```\n%s\n```", escapeCodeBlock(result.Output))
	}

	return summary, message.String(), nil
}

// GetToolType returns the tool type category
func (h *ShellCommandHandler) GetToolType() ToolType {
	return ToolTypeShell
}
