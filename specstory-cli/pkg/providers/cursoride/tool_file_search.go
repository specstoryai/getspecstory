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
func (h *FileSearchHandler) AdaptMessage(bubble *BubbleConversation) (summary string, body string, err error) {
	// Parse raw args to get the query
	var rawArgs FileSearchRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", "", fmt.Errorf("failed to parse file_search rawArgs: %w", err)
		}
	}

	// Parse result to get the file list
	var result FileSearchResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", "", fmt.Errorf("failed to parse file_search result: %w", err)
		}
	}

	resultsLength := len(result.Files)

	summary = fmt.Sprintf(`Tool use: **%s** • Searched codebase "%s" • **%d** results`, escapeSummaryText(bubble.Name), escapeSummaryText(rawArgs.Query), resultsLength)

	if resultsLength == 0 {
		body = "No results found"
	} else {
		// Add table header
		message := "| File |\n|------|\n"

		// Add table rows
		for _, file := range result.Files {
			// Use name if available, otherwise use URI
			displayName := file.Name
			if displayName == "" {
				displayName = file.URI
			}
			// Escape the DB-sourced name so pipes/newlines can't break the table
			message += fmt.Sprintf("| `%s` |\n", escapeTableCellValue(displayName))
		}
		body = message
	}

	return summary, body, nil
}

// GetToolType returns the tool type category
func (h *FileSearchHandler) GetToolType() ToolType {
	return ToolTypeSearch
}
