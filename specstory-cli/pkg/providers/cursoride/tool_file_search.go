package cursoride

import (
	"encoding/json"
	"fmt"
)

// FileSearchHandler handles file_search tool invocations
type FileSearchHandler struct{}

// FileSearchRawArgs represents the raw arguments for file_search tool
type FileSearchRawArgs struct {
	Query       string `json:"query"`
	Explanation string `json:"explanation,omitempty"`
}

// FileSearchResult represents the result of file_search tool
type FileSearchResult struct {
	Files      []FileSearchFile `json:"files"`
	LimitHit   bool             `json:"limitHit,omitempty"`
	NumResults int              `json:"numResults,omitempty"`
}

// FileSearchFile represents a file found by file_search
type FileSearchFile struct {
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
}

// AdaptMessage formats the file_search tool invocation as markdown
func (h *FileSearchHandler) AdaptMessage(bubble *BubbleConversation) (string, error) {
	// Parse raw args to get the query
	var rawArgs FileSearchRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", fmt.Errorf("failed to parse file_search rawArgs: %w", err)
		}
	}

	// Parse result to get the file list
	var result FileSearchResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", fmt.Errorf("failed to parse file_search result: %w", err)
		}
	}

	resultsLength := len(result.Files)

	// Build the markdown message
	message := fmt.Sprintf(`<details>
<summary>Tool use: **%s** • Searched codebase "%s" • **%d** results</summary>
`, bubble.Name, rawArgs.Query, resultsLength)

	if resultsLength == 0 {
		message += "\nNo results found"
	} else {
		// Add table header
		message += "\n| File |\n|------|\n"

		// Add table rows
		for _, file := range result.Files {
			// Use name if available, otherwise use URI
			displayName := file.Name
			if displayName == "" {
				displayName = file.URI
			}
			message += fmt.Sprintf("| `%s` |\n", displayName)
		}
	}

	message += "\n</details>"

	return message, nil
}

// GetToolType returns the tool type category
func (h *FileSearchHandler) GetToolType() ToolType {
	return ToolTypeSearch
}
