package cursoride

import (
	"encoding/json"
	"fmt"
)

// GlobFileSearchHandler handles glob_file_search tool invocations
type GlobFileSearchHandler struct{}

// GlobFileSearchRawArgs represents the raw arguments for glob_file_search tool
type GlobFileSearchRawArgs struct {
	GlobPattern string `json:"glob_pattern"`
}

// GlobFileSearchResult represents the result of glob_file_search tool
type GlobFileSearchResult struct {
	Directories []GlobSearchDirectory `json:"directories"`
}

// GlobSearchDirectory represents a directory with matching files
type GlobSearchDirectory struct {
	AbsPath    string           `json:"absPath"`
	Files      []GlobSearchFile `json:"files"`
	TotalFiles int              `json:"totalFiles"`
}

// GlobSearchFile represents a file found by glob search
type GlobSearchFile struct {
	RelPath string `json:"relPath,omitempty"`
	Name    string `json:"name,omitempty"`
	URI     string `json:"uri,omitempty"`
}

// AdaptMessage formats the glob_file_search tool invocation as markdown
func (h *GlobFileSearchHandler) AdaptMessage(bubble *BubbleConversation) (string, error) {
	// Parse raw args to get the glob pattern
	var rawArgs GlobFileSearchRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", fmt.Errorf("failed to parse glob_file_search rawArgs: %w", err)
		}
	}

	// Parse result to get the directories and files
	var result GlobFileSearchResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", fmt.Errorf("failed to parse glob_file_search result: %w", err)
		}
	}

	// Calculate total results and directories
	resultsLength := 0
	for _, directory := range result.Directories {
		resultsLength += directory.TotalFiles
	}
	directoriesCount := len(result.Directories)

	// Build the details message
	var messageDetails string
	if directoriesCount == 0 {
		messageDetails += "\nNo results found"
	} else {
		for _, directory := range result.Directories {
			filesCount := directory.TotalFiles
			pluralSuffix := ""
			if filesCount != 1 {
				pluralSuffix = "s"
			}

			// Add directory name
			messageDetails += fmt.Sprintf("\nDirectory: **%s** (%d file%s)\n", directory.AbsPath, filesCount, pluralSuffix)

			if len(directory.Files) > 0 {
				// Add table header
				messageDetails += "\n| File |\n|------|\n"

				// Add table rows
				for _, file := range directory.Files {
					// Use relPath first, then name, then uri, then fallback
					displayName := file.RelPath
					if displayName == "" {
						displayName = file.Name
					}
					if displayName == "" {
						displayName = file.URI
					}
					if displayName == "" {
						displayName = "Unknown file"
					}
					messageDetails += fmt.Sprintf("| `%s` |\n", displayName)
				}
			}
		}
	}

	// Build the summary line
	resultsPluralSuffix := ""
	if resultsLength != 1 {
		resultsPluralSuffix = "s"
	}
	directoriesPluralSuffix := "directory"
	if directoriesCount != 1 {
		directoriesPluralSuffix = "directories"
	}

	message := fmt.Sprintf(`<details>
<summary>Tool use: **%s** • Searched codebase "%s" • **%d** result%s in **%d** %s</summary>
%s
</details>`, bubble.Name, rawArgs.GlobPattern, resultsLength, resultsPluralSuffix, directoriesCount, directoriesPluralSuffix, messageDetails)

	return message, nil
}

// GetToolType returns the tool type category
func (h *GlobFileSearchHandler) GetToolType() ToolType {
	return ToolTypeSearch
}
