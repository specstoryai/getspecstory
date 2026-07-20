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
func (h *ListDirectoryHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	// Parse raw args to get the directory path
	var rawArgs ListDirectoryRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", "", fmt.Errorf("failed to parse list_directory rawArgs: %w", err)
		}
	}

	// Parse result to get the file list
	var result ListDirectoryResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", "", fmt.Errorf("failed to parse list_directory result: %w", err)
		}
	}

	filesLength := len(result.Files)

	// Format the workspace path display
	relativeWorkspacePath := rawArgs.RelativeWorkspacePath
	workspaceDisplay := "current directory"
	if relativeWorkspacePath != "" && relativeWorkspacePath != "." {
		workspaceDisplay = fmt.Sprintf("directory %s", relativeWorkspacePath)
	}

	// Build the summary line
	pluralSuffix := ""
	if filesLength != 1 {
		pluralSuffix = "s"
	}
	summary = fmt.Sprintf("Tool use: **%s** • Listed %s, %d result%s", escapeSummaryText(bubble.Name), escapeSummaryText(workspaceDisplay), filesLength, pluralSuffix)

	if filesLength == 0 {
		body = "No results found"
	} else {
		// Add table header
		message := "| Name |\n|-------|\n"

		// Add table rows
		for _, file := range result.Files {
			icon := "📄"
			if file.IsDirectory {
				icon = "📁"
			}
			// Escape the DB-sourced name so pipes/newlines can't break the table
			message += fmt.Sprintf("| %s `%s` |\n", icon, escapeTableCellValue(file.Name))
		}
		body = message
	}

	return summary, body, nil
}

// GetToolType returns the tool type category
func (h *ListDirectoryHandler) GetToolType() ToolType {
	return ToolTypeSearch
}
