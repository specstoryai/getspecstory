package cursoride

import (
	"encoding/json"
	"fmt"
)

// ListDirectoryHandler handles list_directory tool invocations
type ListDirectoryHandler struct{}

// ListDirectoryRawArgs represents the raw arguments for list_directory tool
type ListDirectoryRawArgs struct {
	RelativeWorkspacePath string `json:"relative_workspace_path,omitempty"`
}

// ListDirectoryResult represents the result of list_directory tool
type ListDirectoryResult struct {
	Files []DirectoryFile `json:"files"`
}

// DirectoryFile represents a file or directory entry
type DirectoryFile struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
}

// AdaptMessage formats the list_directory tool invocation as markdown
func (h *ListDirectoryHandler) AdaptMessage(bubble *BubbleConversation) (string, error) {
	// Parse raw args to get the directory path
	var rawArgs ListDirectoryRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", fmt.Errorf("failed to parse list_directory rawArgs: %w", err)
		}
	}

	// Parse result to get the file list
	var result ListDirectoryResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", fmt.Errorf("failed to parse list_directory result: %w", err)
		}
	}

	filesLength := len(result.Files)

	// Format the workspace path display
	relativeWorkspacePath := rawArgs.RelativeWorkspacePath
	workspaceDisplay := "current directory"
	if relativeWorkspacePath != "" && relativeWorkspacePath != "." {
		workspaceDisplay = fmt.Sprintf("directory %s", relativeWorkspacePath)
	}

	// Build the markdown message
	pluralSuffix := ""
	if filesLength != 1 {
		pluralSuffix = "s"
	}

	message := fmt.Sprintf(`<details>
<summary>Tool use: **%s** • Listed %s, %d result%s</summary>
`, bubble.Name, workspaceDisplay, filesLength, pluralSuffix)

	if filesLength == 0 {
		message += "\nNo results found"
	} else {
		// Add table header
		message += "\n| Name |\n|-------|\n"

		// Add table rows
		for _, file := range result.Files {
			icon := "📄"
			if file.IsDirectory {
				icon = "📁"
			}
			message += fmt.Sprintf("| %s `%s` |\n", icon, file.Name)
		}
	}

	message += "\n</details>"

	return message, nil
}

// GetToolType returns the tool type category
func (h *ListDirectoryHandler) GetToolType() ToolType {
	return ToolTypeSearch
}
