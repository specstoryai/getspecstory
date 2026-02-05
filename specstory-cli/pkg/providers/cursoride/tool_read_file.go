package cursoride

import (
	"encoding/json"
	"fmt"
)

// ReadFileHandler handles read_file and read_file_v2 tool invocations
type ReadFileHandler struct{}

// ReadFileParams represents the parameters for read_file tool
type ReadFileParams struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath,omitempty"`
	TargetFile            string `json:"targetFile,omitempty"`
}

// AdaptMessage formats the read_file tool invocation as markdown
func (h *ReadFileHandler) AdaptMessage(bubble *BubbleConversation) (string, error) {
	// Parse params to get the file path
	var params ReadFileParams
	if bubble.Params != "" {
		if err := json.Unmarshal([]byte(bubble.Params), &params); err != nil {
			return "", fmt.Errorf("failed to parse read_file params: %w", err)
		}
	}

	// Get the file path (prefer relativeWorkspacePath, fallback to targetFile)
	filePath := params.RelativeWorkspacePath
	if filePath == "" {
		filePath = params.TargetFile
	}

	// Format as a collapsed details block
	// This matches the TypeScript implementation:
	// `<details><summary>Tool use: **${toolName}** • Read file: ${path}</summary>\n\n</details>`
	return fmt.Sprintf(`<details><summary>Tool use: **%s** • Read file: %s</summary>

</details>`, bubble.Name, filePath), nil
}

// GetToolType returns the tool type category
func (h *ReadFileHandler) GetToolType() ToolType {
	return ToolTypeRead
}
