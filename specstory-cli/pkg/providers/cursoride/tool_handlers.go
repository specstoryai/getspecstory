package cursoride

import (
	"encoding/json"
	"fmt"
	"html"
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
	// AdaptMessage formats the tool invocation, returning a one-line summary
	// (rendered inside <summary>...</summary> by the caller) and a body-only
	// markdown fragment (rendered inside <details>...</details> by the caller).
	// Handlers must not include the <details>/<summary> wrapper themselves —
	// callers own it, so it's only added once (see FormatToolContent).
	AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error)

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

	// Register list directory handler
	listDirectoryHandler := &ListDirectoryHandler{}
	registry.Register("list_directory", listDirectoryHandler)

	// Register file search handler
	fileSearchHandler := &FileSearchHandler{}
	registry.Register("file_search", fileSearchHandler)

	// Register glob file search handler
	globFileSearchHandler := &GlobFileSearchHandler{}
	registry.Register("glob_file_search", globFileSearchHandler)

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

// FormatToolContent resolves the handler for a tool invocation (or falls back to the
// catch-all formatter for unregistered tools) and returns its one-line summary and
// body-only markdown, without any <details>/<summary> or <tool-use> wrapper. Split out
// from FormatToolInvocation so callers that embed the content elsewhere (e.g.
// schema.ToolInfo, which the shared markdown renderer wraps itself) don't end up with
// a nested <details> block and a duplicated "Tool use" heading.
// Callers are responsible for the error/cancelled/invalid-tool special cases handled by
// FormatToolInvocation — this only covers the "normal" handler-resolution path.
func FormatToolContent(bubble *BubbleConversation, registry *ToolRegistry) (summary string, body string, toolType ToolType) {
	handler := registry.GetHandler(bubble.Name)
	if handler != nil {
		// Use the registered handler
		toolType = handler.GetToolType()
		var err error
		summary, body, err = handler.AdaptMessage(bubble)
		if err != nil {
			slog.Warn("Error adapting tool message, using fallback",
				"toolName", bubble.Name,
				"error", err)
			// Fallback to catch-all handler
			toolType = ToolTypeUnknown
			summary, body = formatCatchAll(bubble)
		}
	} else {
		// Unknown tool - use catch-all handler
		slog.Debug("Unknown tool, using catch-all handler",
			"toolName", bubble.Name)
		toolType = ToolTypeUnknown
		summary, body = formatCatchAll(bubble)
	}
	return summary, body, toolType
}

// FormatToolInvocation formats a tool invocation as a complete markdown block,
// including the outer <tool-use> and <details>/<summary> wrapper. This is the fallback
// path used when a tool invocation can't be resolved into a structured schema.ToolInfo
// (see resolveToolInfo in agent_session.go) and needs to be embedded directly in Content.
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

	summary, body, toolType := FormatToolContent(bubble, registry)

	// Wrap in tool-use HTML tag
	// Format: <tool-use data-tool-type="read" data-tool-name="read_file">
	// bubble.Name comes from the Cursor DB, so escape it to keep a malformed or
	// malicious name (quotes, angle brackets) from breaking out of the attribute.
	return fmt.Sprintf(`<tool-use data-tool-type="%s" data-tool-name="%s">
<details>
<summary>%s</summary>

%s
</details>
</tool-use>`, toolType, escapeSummaryText(bubble.Name), summary, body)
}

// escapeSummaryText escapes DB-sourced strings (tool names, queries, file paths)
// before they are interpolated into HTML contexts like <summary> lines or tag
// attributes. These values come from Cursor's database, so a malformed string
// containing <, >, & or quotes would otherwise break the generated markup.
func escapeSummaryText(s string) string {
	return html.EscapeString(s)
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
func formatCatchAll(bubble *BubbleConversation) (summary string, body string) {
	// Summary is just the tool name, no params. Escape the DB-sourced name so it
	// can't inject markup into the <summary> element.
	summary = fmt.Sprintf("Tool use: **%s**", escapeSummaryText(bubble.Name))

	var message strings.Builder

	// Parse params
	var params map[string]interface{}
	if bubble.Params != "" {
		if err := json.Unmarshal([]byte(bubble.Params), &params); err != nil {
			slog.Warn("Failed to parse tool params",
				"toolName", bubble.Name,
				"error", err)
		}
	}

	// Add parameters section (outside summary, inside details).
	// Parameters and additional data are inputs, so they are not capped.
	if len(params) > 0 {
		message.WriteString("\nParameters:\n\n")
		message.WriteString(formatJSONBlock(params, 0))
	}

	// Add additional data section
	if len(bubble.AdditionalData) > 0 {
		message.WriteString("Additional data:\n\n")
		message.WriteString(formatJSONBlock(bubble.AdditionalData, 0))
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
			message.WriteString(formatJSONBlock(result, toolResultCap))
		}
	}

	// Add user decision
	if bubble.UserDecision != "" {
		fmt.Fprintf(&message, "User decision: **%s**\n\n", bubble.UserDecision)
	}

	// Add status
	if bubble.Status != "" {
		fmt.Fprintf(&message, "Status: **%s**\n\n", bubble.Status)
	}

	// Add error section
	if bubble.Error != "" {
		message.WriteString("Error:\n\n")
		errorData := map[string]interface{}{"error": bubble.Error}
		message.WriteString(formatJSONBlock(errorData, 0))
	}

	return summary, message.String()
}

// formatJSONBlock renders data as an indented JSON code block, truncated to at most
// maxRunes when positive (<= 0 means no cap). Content is kept verbatim (fencedBlock
// sizes the fence around any backticks) so resumed sessions carry real JSON instead
// of HTML entities.
func formatJSONBlock(data map[string]interface{}, maxRunes int) string {
	var jsonStr string
	if jsonBytes, err := json.MarshalIndent(data, "", "  "); err != nil {
		jsonStr = fmt.Sprintf("%v", data)
	} else {
		jsonStr = string(jsonBytes)
	}
	if maxRunes > 0 {
		jsonStr = capRunes(jsonStr, maxRunes)
	}
	return fencedBlock("json", jsonStr) + "\n"
}

// toolResultCap bounds how much tool output is rendered into the markdown body.
// Inputs are not capped — they carry what the agent chose to do (e.g. a patch or
// edit content) — but results (command output, tool result payloads) can be
// arbitrarily large and matter less once the agent has already responded to them.
const toolResultCap = 2000

// fencedBlock wraps content in a code fence with an optional language tag, keeping
// the content verbatim. codeFence sizes the fence so embedded backtick runs can't
// terminate the block early.
func fencedBlock(lang, content string) string {
	fence := codeFence(content)
	return fmt.Sprintf("%s%s\n%s\n%s", fence, lang, content, fence)
}

// codeFence returns a backtick fence long enough to safely wrap s: one backtick more
// than the longest backtick run inside it (a value containing ``` would otherwise
// terminate a plain three-backtick fence early), never shorter than the standard three.
func codeFence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	size := longest + 1
	if size < 3 {
		size = 3
	}
	return strings.Repeat("`", size)
}

// capRunes truncates s to at most max runes, marking the cut. Rune-based so a cap
// never splits a multi-byte character. Scans rune boundaries instead of converting
// to []rune, which would allocate O(len(s)) for exactly the oversized tool results
// this cap protects against.
func capRunes(s string, max int) string {
	if len(s) <= max {
		return s // fast path: byte length bounds rune length
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + "\n… (output truncated)"
		}
		count++
	}
	return s // more bytes than max but fewer runes (multi-byte characters)
}
