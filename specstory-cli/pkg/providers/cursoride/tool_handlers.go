package cursoride

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ToolType represents the category of a tool (read, write, search, etc.)
type ToolType string

const (
	ToolTypeRead    ToolType = "read"
	ToolTypeWrite   ToolType = "write"
	ToolTypeSearch  ToolType = "search"
	ToolTypeShell   ToolType = "shell"
	ToolTypeTask    ToolType = "task"
	ToolTypeMCP     ToolType = "mcp"
	ToolTypeGeneric ToolType = "generic"
	ToolTypeUnknown ToolType = "unknown"
)

// ToolHandler is the interface for all tool handlers
// Each handler knows how to format the markdown output for a specific tool
type ToolHandler interface {
	// AdaptMessage formats the tool invocation as markdown
	// Returns the formatted markdown text
	AdaptMessage(bubble *BubbleConversation) (string, error)

	// GetToolType returns the tool type category
	GetToolType() ToolType
}

// ToolRegistry maps tool names to their handlers
// Multiple tool names can map to the same handler (e.g., read_file and read_file_v2)
type ToolRegistry struct {
	handlers map[string]ToolHandler
}

// NewToolRegistry creates a new tool registry with all handlers registered
func NewToolRegistry() *ToolRegistry {
	registry := &ToolRegistry{
		handlers: make(map[string]ToolHandler),
	}

	// Register read file handlers
	readFileHandler := &ReadFileHandler{}
	registry.Register("read_file", readFileHandler)
	registry.Register("read_file_v2", readFileHandler)

	// Register code edit handlers
	codeEditHandler := &CodeEditHandler{}
	registry.Register("edit_file", codeEditHandler)
	registry.Register("MultiEdit", codeEditHandler)
	registry.Register("edit_notebook", codeEditHandler)
	registry.Register("reapply", codeEditHandler)
	registry.Register("search_replace", codeEditHandler)
	registry.Register("write", codeEditHandler)
	registry.Register("edit_file_v2", codeEditHandler)

	// Register delete file handler
	deleteFileHandler := &DeleteFileHandler{}
	registry.Register("delete_file", deleteFileHandler)

	// Register apply patch handler
	applyPatchHandler := &ApplyPatchHandler{}
	registry.Register("apply_patch", applyPatchHandler)

	// Register copilot handlers
	copilotApplyPatchHandler := &CopilotApplyPatchHandler{}
	registry.Register("copilot_applyPatch", copilotApplyPatchHandler)
	registry.Register("copilot_insertEdit", copilotApplyPatchHandler)

	// Register shell/terminal command handlers
	shellCommandHandler := &ShellCommandHandler{}
	registry.Register("run_terminal_cmd", shellCommandHandler)
	registry.Register("run_terminal_command", shellCommandHandler)
	registry.Register("run_terminal_command_v2", shellCommandHandler)

	// Register grep/search handlers
	grepHandler := &GrepHandler{}
	registry.Register("grep", grepHandler)
	registry.Register("ripgrep", grepHandler)

	// Register grep_search handler (different data structure)
	grepSearchHandler := &GrepSearchHandler{}
	registry.Register("grep_search", grepSearchHandler)

	return registry
}

// Register adds a handler for a specific tool name
func (r *ToolRegistry) Register(toolName string, handler ToolHandler) {
	r.handlers[toolName] = handler
}

// GetHandler returns the handler for a tool name, or nil if not found
func (r *ToolRegistry) GetHandler(toolName string) ToolHandler {
	return r.handlers[toolName]
}

// FormatToolInvocation formats a tool invocation as markdown
// This is the main entry point for processing tool invocations
func FormatToolInvocation(bubble *BubbleConversation, registry *ToolRegistry) string {
	// Handle invalid tool (tool = 0)
	if bubble.Tool == 0 {
		return formatToolError(bubble)
	}

	// Handle error status
	if bubble.Status == "error" {
		return formatToolError(bubble)
	}

	// Handle cancelled status
	if bubble.Status == "cancelled" {
		return "Cancelled"
	}

	// Get the handler for this tool name
	handler := registry.GetHandler(bubble.Name)
	var toolType ToolType
	var content string

	if handler != nil {
		// Use the registered handler
		toolType = handler.GetToolType()
		var err error
		content, err = handler.AdaptMessage(bubble)
		if err != nil {
			slog.Warn("Error adapting tool message, using fallback",
				"toolName", bubble.Name,
				"error", err)
			// Fallback to catch-all handler
			toolType = ToolTypeUnknown
			content = formatCatchAll(bubble)
		}
	} else {
		// Unknown tool - use catch-all handler
		slog.Debug("Unknown tool, using catch-all handler",
			"toolName", bubble.Name)
		toolType = ToolTypeUnknown
		content = formatCatchAll(bubble)
	}

	// Wrap in tool-use HTML tag
	// Format: <tool-use data-tool-type="read" data-tool-name="read_file">
	return fmt.Sprintf(`<tool-use data-tool-type="%s" data-tool-name="%s">
%s
</tool-use>`, toolType, bubble.Name, content)
}

// formatToolError formats a tool error message
func formatToolError(bubble *BubbleConversation) string {
	if bubble.Error != "" {
		// Parse the error JSON
		var errorData struct {
			ClientVisibleErrorMessage string `json:"clientVisibleErrorMessage"`
		}
		if err := json.Unmarshal([]byte(bubble.Error), &errorData); err == nil {
			return errorData.ClientVisibleErrorMessage
		}
		// If parsing fails, return the raw error
		return bubble.Error
	}
	return "An unknown error occurred"
}

// formatCatchAll is the fallback formatter for unknown tools
// Matches the TypeScript CatchAllBubbleHandler format
func formatCatchAll(bubble *BubbleConversation) string {
	var message strings.Builder

	// Start with summary line (just tool name, no params)
	message.WriteString(fmt.Sprintf(`<details>
<summary>Tool use: **%s**</summary>

`, bubble.Name))

	// Parse params
	var params map[string]interface{}
	if bubble.Params != "" {
		if err := json.Unmarshal([]byte(bubble.Params), &params); err != nil {
			slog.Warn("Failed to parse tool params",
				"toolName", bubble.Name,
				"error", err)
		}
	}

	// Add parameters section (outside summary, inside details)
	if len(params) > 0 {
		message.WriteString("\nParameters:\n\n")
		message.WriteString(formatEscapeJSONBlock(params))
	}

	// Add additional data section
	if len(bubble.AdditionalData) > 0 {
		message.WriteString("Additional data:\n\n")
		message.WriteString(formatEscapeJSONBlock(bubble.AdditionalData))
	}

	// Parse and add result section
	var result map[string]interface{}
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			slog.Warn("Failed to parse tool result",
				"toolName", bubble.Name,
				"error", err)
		}
		if len(result) > 0 {
			message.WriteString("Result:\n\n")
			message.WriteString(formatEscapeJSONBlock(result))
		}
	}

	// Add user decision
	if bubble.UserDecision != "" {
		message.WriteString(fmt.Sprintf("User decision: **%s**\n\n", bubble.UserDecision))
	}

	// Add status
	if bubble.Status != "" {
		message.WriteString(fmt.Sprintf("Status: **%s**\n\n", bubble.Status))
	}

	// Add error section
	if bubble.Error != "" {
		message.WriteString("Error:\n\n")
		errorData := map[string]interface{}{"error": bubble.Error}
		message.WriteString(formatEscapeJSONBlock(errorData))
	}

	message.WriteString("\n</details>")

	return message.String()
}

// formatEscapeJSONBlock formats JSON with escaped special characters
// Matches the TypeScript formatEscapeJsonBlock function
func formatEscapeJSONBlock(data map[string]interface{}) string {
	// Marshal JSON with indentation
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf("```json\n%v\n```\n", data)
	}

	// Escape special characters (backticks, <, >, &)
	jsonStr := string(jsonBytes)
	jsonStr = strings.ReplaceAll(jsonStr, "&", "&amp;")
	jsonStr = strings.ReplaceAll(jsonStr, "`", "&#96;")
	jsonStr = strings.ReplaceAll(jsonStr, "<", "&lt;")
	jsonStr = strings.ReplaceAll(jsonStr, ">", "&gt;")

	return fmt.Sprintf("```json\n%s\n```\n", jsonStr)
}
