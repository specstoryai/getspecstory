package cursoride

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GrepSearchHandler handles grep_search tool invocations
// This is different from regular grep - it uses a different data structure
type GrepSearchHandler struct{}

// GrepSearchRawArgs represents raw arguments for grep_search
type GrepSearchRawArgs struct {
	Query string `json:"query"`
}

// GrepSearchResult represents the result of a grep_search operation
type GrepSearchResult struct {
	Internal *GrepSearchInternal `json:"internal,omitempty"`
}

// GrepSearchInternal contains the internal grep_search results
type GrepSearchInternal struct {
	Results []GrepSearchFileResult `json:"results,omitempty"`
}

// GrepSearchFileResult represents results for a single file
type GrepSearchFileResult struct {
	Resource string                 `json:"resource"` // File path
	Results  []GrepSearchMatchEntry `json:"results,omitempty"`
}

// GrepSearchMatchEntry represents a single match entry
type GrepSearchMatchEntry struct {
	Match GrepSearchMatch `json:"match"`
}

// GrepSearchMatch contains the match details
type GrepSearchMatch struct {
	RangeLocations []GrepSearchRangeLocation `json:"rangeLocations,omitempty"`
	PreviewText    string                    `json:"previewText"`
}

// GrepSearchRangeLocation contains the location of the match
type GrepSearchRangeLocation struct {
	Source GrepSearchSource `json:"source"`
}

// GrepSearchSource contains the line number
type GrepSearchSource struct {
	StartLineNumber int `json:"startLineNumber"`
}

// AdaptMessage formats grep_search tool invocations as markdown
func (h *GrepSearchHandler) AdaptMessage(bubble *BubbleConversation) (string, error) {
	var rawArgs GrepSearchRawArgs
	if bubble.RawArgs != "" {
		if err := json.Unmarshal([]byte(bubble.RawArgs), &rawArgs); err != nil {
			return "", fmt.Errorf("failed to parse grep_search rawArgs: %w", err)
		}
	}

	var result GrepSearchResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", fmt.Errorf("failed to parse grep_search result: %w", err)
		}
	}

	// Count results (number of files with matches)
	resultsLength := 0
	if result.Internal != nil {
		resultsLength = len(result.Internal.Results)
	}

	var message strings.Builder
	message.WriteString(`<details>
            <summary>Tool use: **`)
	message.WriteString(bubble.Name)
	message.WriteString(`** • Grep search for "`)
	message.WriteString(rawArgs.Query)
	message.WriteString(`" • **`)
	message.WriteString(fmt.Sprintf("%d", resultsLength))
	message.WriteString(`** files</summary>
        `)

	if resultsLength == 0 {
		message.WriteString("\nNo results found")
	} else {
		// Add table header
		message.WriteString("\n| File | Line | Match |\n|------|------|-------|\n")

		// Add table rows
		for _, fileResult := range result.Internal.Results {
			for _, matchEntry := range fileResult.Results {
				// Get line number (add 1 since it's 0-based)
				lineNumber := 0
				if len(matchEntry.Match.RangeLocations) > 0 {
					lineNumber = matchEntry.Match.RangeLocations[0].Source.StartLineNumber + 1
				}

				// Add row
				message.WriteString(fmt.Sprintf("| `%s` | L%d | `%s` |\n",
					fileResult.Resource,
					lineNumber,
					escapeTableCellValue(matchEntry.Match.PreviewText)))
			}
		}
	}

	message.WriteString("\n</details>")
	return message.String(), nil
}

// GetToolType returns the tool type category
func (h *GrepSearchHandler) GetToolType() ToolType {
	return ToolTypeSearch
}
