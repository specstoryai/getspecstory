package cursoride

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
			message.WriteString(spi.CodeFence("diff", header+"\n"+chunk.DiffString))
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
				message.WriteString(spi.CodeFence(lang, content))
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
		message.WriteString(spi.CodeFence("diff", rawArgs.Patch))
		message.WriteString("\n")
	}

	return summary, message.String(), nil
}

// GetToolType returns the tool type category
func (h *ApplyPatchHandler) GetToolType() ToolType {
	return ToolTypeWrite
}
