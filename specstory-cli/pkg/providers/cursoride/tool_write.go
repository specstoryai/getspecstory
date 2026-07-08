package cursoride

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// CodeEditHandler is a handler for code editing tools
// Handles: edit_file, MultiEdit, edit_notebook, reapply, search_replace, write, edit_file_v2
type CodeEditHandler struct{}

// CodeEditParams represents parameters for code edit tools
type CodeEditParams struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath,omitempty"`
	Instructions          string `json:"instructions,omitempty"`
}

// CodeEditResult represents the result of a code edit operation
type CodeEditResult struct {
	ApplyFailed bool `json:"applyFailed,omitempty"`
	Diff        *struct {
		Chunks []struct {
			DiffString   string `json:"diffString"`
			OldStart     int    `json:"oldStart"`
			NewStart     int    `json:"newStart"`
			OldLines     int    `json:"oldLines"`
			NewLines     int    `json:"newLines"`
			LinesAdded   int    `json:"linesAdded"`
			LinesRemoved int    `json:"linesRemoved"`
		} `json:"chunks"`
	} `json:"diff,omitempty"`
}

// AdaptMessage formats code edit tool invocations as markdown
func (h *CodeEditHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	var params CodeEditParams
	if bubble.Params != "" {
		if err := json.Unmarshal([]byte(bubble.Params), &params); err != nil {
			return "", "", fmt.Errorf("failed to parse code edit params: %w", err)
		}
	}

	var result CodeEditResult
	if bubble.Result != "" {
		// Parse result, but ignore errors (non-fatal)
		_ = json.Unmarshal([]byte(bubble.Result), &result)
	}

	// Build summary line
	if params.RelativeWorkspacePath != "" {
		summary = fmt.Sprintf("Tool use: **%s** • Edit file: %s", escapeSummaryText(bubble.Name), escapeSummaryText(params.RelativeWorkspacePath))
	} else {
		summary = fmt.Sprintf("Tool use: **%s**", escapeSummaryText(bubble.Name))
	}

	var message strings.Builder

	// Add instructions if present
	if params.Instructions != "" {
		fmt.Fprintf(&message, "%s\n\n", params.Instructions)
	}

	// Add status if not completed
	if bubble.Status != "" && bubble.Status != "completed" {
		fmt.Fprintf(&message, "Status: **%s**\n\n", bubble.Status)
	}

	// Add apply failed message
	if result.ApplyFailed {
		message.WriteString("**Apply failed**\n\n")
	}

	// Add diff chunks if present. Diffs carry the edit the agent made, so they are
	// rendered verbatim and uncapped like other inputs.
	if result.Diff != nil && len(result.Diff.Chunks) > 0 {
		for i, chunk := range result.Diff.Chunks {
			fmt.Fprintf(&message, "**Chunk %d**\n", i+1)
			fmt.Fprintf(&message, "Lines added: %d, lines removed: %d\n\n", chunk.LinesAdded, chunk.LinesRemoved)
			header := fmt.Sprintf("@@ -%d,%d +%d,%d @@", chunk.OldStart, chunk.OldLines, chunk.NewStart, chunk.NewLines)
			message.WriteString(fencedBlock("diff", header+"\n"+chunk.DiffString))
			message.WriteString("\n\n")
		}
	} else if len(bubble.AdditionalData) > 0 {
		// Check for codeblock in additionalData (edit_file_v2 format)
		if codeblockData, ok := bubble.AdditionalData["codeblock"].(map[string]interface{}); ok {
			if content, hasContent := codeblockData["content"].(string); hasContent && content != "" {
				lang := ""
				if languageId, hasLang := codeblockData["languageId"].(string); hasLang {
					lang = languageId
				}
				message.WriteString(fencedBlock(lang, content))
				message.WriteString("\n\n")
			}
		}
	}

	return summary, message.String(), nil
}

// GetToolType returns the tool type category
func (h *CodeEditHandler) GetToolType() ToolType {
	return ToolTypeWrite
}

// DeleteFileHandler handles delete_file tool invocations
type DeleteFileHandler struct{}

// DeleteFileRawArgs represents raw arguments for delete_file
type DeleteFileRawArgs struct {
	Explanation string `json:"explanation"`
}

// AdaptMessage formats delete_file tool invocations as markdown
func (h *DeleteFileHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	var rawArgs DeleteFileRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", "", fmt.Errorf("failed to parse delete_file rawArgs: %w", err)
		}
	}

	summary = fmt.Sprintf("Tool use: **%s**", escapeSummaryText(bubble.Name))

	var message strings.Builder
	if rawArgs.Explanation != "" {
		fmt.Fprintf(&message, "Explanation: %s\n\n", rawArgs.Explanation)
	}

	return summary, message.String(), nil
}

// GetToolType returns the tool type category
func (h *DeleteFileHandler) GetToolType() ToolType {
	return ToolTypeWrite
}

// ApplyPatchHandler handles apply_patch tool invocations
type ApplyPatchHandler struct{}

// ApplyPatchRawArgs represents raw arguments for apply_patch
type ApplyPatchRawArgs struct {
	FilePath string `json:"file_path"`
	Patch    string `json:"patch"`
}

// AdaptMessage formats apply_patch tool invocations as markdown
func (h *ApplyPatchHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	var rawArgs ApplyPatchRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", "", fmt.Errorf("failed to parse apply_patch rawArgs: %w", err)
		}
	}

	summary = fmt.Sprintf("Tool use: **%s** • Apply patch for %s", escapeSummaryText(bubble.Name), escapeSummaryText(rawArgs.FilePath))

	var message strings.Builder
	if rawArgs.Patch != "" {
		message.WriteString(fencedBlock("diff", rawArgs.Patch))
		message.WriteString("\n")
	}

	return summary, message.String(), nil
}

// GetToolType returns the tool type category
func (h *ApplyPatchHandler) GetToolType() ToolType {
	return ToolTypeWrite
}

// CopilotApplyPatchHandler handles copilot_applyPatch and copilot_insertEdit tool invocations
type CopilotApplyPatchHandler struct{}

// CopilotApplyPatchRawArgs represents raw arguments for copilot apply patch tools
type CopilotApplyPatchRawArgs struct {
	ToolID       string `json:"toolId,omitempty"`
	VscodeToolID string `json:"vscodeToolId,omitempty"`
}

// CopilotApplyPatchParams represents parameters for copilot apply patch tools
type CopilotApplyPatchParams struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath,omitempty"`
	FilePath              string `json:"file_path,omitempty"`
}

// CopilotApplyPatchResult represents the result of a copilot apply patch operation
type CopilotApplyPatchResult struct {
	InvocationMessage string `json:"invocationMessage,omitempty"`
	Content           string `json:"content,omitempty"`
	TextEditContent   string `json:"textEditContent,omitempty"`
	PastTenseMessage  string `json:"pastTenseMessage,omitempty"`
}

// AdaptMessage formats copilot apply patch tool invocations as markdown
func (h *CopilotApplyPatchHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	var rawArgs CopilotApplyPatchRawArgs
	if bubble.RawArgs != "" {
		// Parse rawArgs, but ignore errors (non-fatal, use defaults)
		_ = json.Unmarshal([]byte(bubble.RawArgs), &rawArgs)
	}

	var params CopilotApplyPatchParams
	if bubble.Params != "" {
		// Parse params, but ignore errors (non-fatal)
		_ = json.Unmarshal([]byte(bubble.Params), &params)
	}

	var result CopilotApplyPatchResult
	if bubble.Result != "" {
		// Parse result, but ignore errors (non-fatal)
		_ = json.Unmarshal([]byte(bubble.Result), &result)
	}

	// Determine tool ID
	toolID := rawArgs.ToolID
	if toolID == "" {
		toolID = rawArgs.VscodeToolID
	}
	if toolID == "" {
		toolID = "copilot_applyPatch"
	}

	// Extract invocation message
	invocationMsg := result.InvocationMessage
	if invocationMsg == "" {
		invocationMsg = "Using \"Apply Patch\""
	}

	// For insertEdit, include file path in summary if available
	filePath := params.RelativeWorkspacePath
	if filePath == "" {
		filePath = params.FilePath
	}
	if filePath != "" && toolID == "copilot_insertEdit" {
		invocationMsg = fmt.Sprintf("Edit file: %s", filePath)
	}

	summary = fmt.Sprintf("Tool use: **%s** • %s", escapeSummaryText(bubble.Name), escapeSummaryText(invocationMsg))

	var message strings.Builder

	// Check if operation failed
	if bubble.Status == "error" && bubble.Error != "" {
		// Prefer the parsed message, but fall back to the raw error text when
		// bubble.Error isn't valid JSON or doesn't carry a message — otherwise the
		// failure is silently hidden with an empty body.
		errMsg := bubble.Error
		var errorData struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(bubble.Error), &errorData); err == nil && errorData.Message != "" {
			errMsg = errorData.Message
		}
		message.WriteString("**❌ Patch Failed**\n\n")
		fmt.Fprintf(&message, "%s\n", errMsg)
	} else {
		// Add the collected content
		if result.Content != "" && strings.TrimSpace(result.Content) != "" {
			message.WriteString(result.Content)
		} else if result.TextEditContent != "" && strings.TrimSpace(result.TextEditContent) != "" {
			// For insertEdit, use the direct textEditGroup content
			extension := ""
			if filePath != "" {
				extension = filepath.Ext(filePath)
				if len(extension) > 0 {
					extension = extension[1:] // Remove leading dot
				}
			}
			language := extensionToLanguage(extension)
			// The edit content is what the agent chose to write, so it is not capped.
			message.WriteString(fencedBlock(language, result.TextEditContent))
			message.WriteString("\n\n")
		} else {
			message.WriteString("_No content to show_\n")
		}

		// Add past tense message if available
		if result.PastTenseMessage != "" {
			fmt.Fprintf(&message, "\n**Status:** %s\n", result.PastTenseMessage)
		}
	}

	return summary, message.String(), nil
}

// GetToolType returns the tool type category
func (h *CopilotApplyPatchHandler) GetToolType() ToolType {
	return ToolTypeWrite
}

// extensionToLanguage maps file extensions to language identifiers
// This is a simplified version - the full mapping is in ts-extension/core/utils/extension2language.ts
func extensionToLanguage(extension string) string {
	// Common mappings
	langMap := map[string]string{
		"js":    "javascript",
		"jsx":   "javascript",
		"ts":    "typescript",
		"tsx":   "typescript",
		"py":    "python",
		"go":    "go",
		"rs":    "rust",
		"java":  "java",
		"c":     "c",
		"cpp":   "cpp",
		"cc":    "cpp",
		"cxx":   "cpp",
		"h":     "c",
		"hpp":   "cpp",
		"cs":    "csharp",
		"php":   "php",
		"rb":    "ruby",
		"swift": "swift",
		"kt":    "kotlin",
		"scala": "scala",
		"sh":    "bash",
		"zsh":   "bash",
		"bash":  "bash",
		"md":    "markdown",
		"json":  "json",
		"xml":   "xml",
		"yaml":  "yaml",
		"yml":   "yaml",
		"toml":  "toml",
		"html":  "html",
		"css":   "css",
		"scss":  "scss",
		"sass":  "sass",
		"less":  "less",
		"sql":   "sql",
	}

	if lang, ok := langMap[extension]; ok {
		return lang
	}

	// Default to the extension itself
	if extension != "" {
		return extension
	}
	return "text"
}
