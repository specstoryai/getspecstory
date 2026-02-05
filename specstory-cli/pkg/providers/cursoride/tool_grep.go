package cursoride

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GrepHandler handles grep and ripgrep tool invocations
type GrepHandler struct{}

// GrepParams represents parameters for grep tools
type GrepParams struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	OutputMode      string `json:"outputMode,omitempty"`
	CaseInsensitive bool   `json:"caseInsensitive,omitempty"`
}

// GrepResult represents the result of a grep operation
type GrepResult struct {
	Success *GrepSuccess `json:"success,omitempty"`
}

// GrepSuccess contains the successful grep results
type GrepSuccess struct {
	Pattern          string                          `json:"pattern"`
	Path             string                          `json:"path,omitempty"`
	OutputMode       string                          `json:"outputMode,omitempty"`
	WorkspaceResults map[string]*GrepWorkspaceResult `json:"workspaceResults,omitempty"`
}

// GrepWorkspaceResult contains results for a specific workspace
type GrepWorkspaceResult struct {
	Content *GrepContentResult `json:"content,omitempty"`
	Files   *GrepFilesResult   `json:"files,omitempty"`
}

// GrepContentResult contains content matches with line numbers
type GrepContentResult struct {
	Matches           []GrepFileMatch `json:"matches,omitempty"`
	TotalLines        int             `json:"totalLines,omitempty"`
	TotalMatchedLines int             `json:"totalMatchedLines,omitempty"`
}

// GrepFileMatch represents matches in a single file
type GrepFileMatch struct {
	File    string      `json:"file,omitempty"`
	Matches []GrepMatch `json:"matches,omitempty"`
}

// GrepMatch represents a single match with line number
type GrepMatch struct {
	LineNumber    int    `json:"lineNumber"`
	Content       string `json:"content,omitempty"`
	IsContextLine bool   `json:"isContextLine,omitempty"`
}

// GrepFilesResult contains just file names (for files_with_matches mode)
type GrepFilesResult struct {
	Files      []string `json:"files,omitempty"`
	TotalFiles int      `json:"totalFiles,omitempty"`
}

// AdaptMessage formats grep tool invocations as markdown
func (h *GrepHandler) AdaptMessage(bubble *BubbleConversation) (string, error) {
	var params GrepParams
	if bubble.Params != "" {
		if err := json.Unmarshal([]byte(bubble.Params), &params); err != nil {
			return "", fmt.Errorf("failed to parse grep params: %w", err)
		}
	}

	var result GrepResult
	if bubble.Result != "" {
		if err := json.Unmarshal([]byte(bubble.Result), &result); err != nil {
			return "", fmt.Errorf("failed to parse grep result: %w", err)
		}
	}

	// Extract results
	var resultsLength int
	var messageDetails string

	if result.Success != nil && result.Success.WorkspaceResults != nil {
		// Get the first (and typically only) workspace result
		for _, workspaceResult := range result.Success.WorkspaceResults {
			if workspaceResult.Content != nil {
				// Content mode: show matches in a table
				resultsLength = workspaceResult.Content.TotalMatchedLines
				if resultsLength == 0 {
					messageDetails = "\n_No matches found_"
				} else {
					messageDetails = h.formatContentResults(workspaceResult.Content)
				}
			} else if workspaceResult.Files != nil {
				// Files mode: show file names in a table
				resultsLength = workspaceResult.Files.TotalFiles
				if resultsLength == 0 {
					messageDetails = "\n_No matches found_"
				} else {
					messageDetails = h.formatFilesResults(workspaceResult.Files)
				}
			}
			break // Only process the first workspace
		}
	} else {
		messageDetails = "\n_No matches found_"
	}

	// Build the message
	var message strings.Builder
	message.WriteString("<details>\n")

	// Build summary line
	inString := ""
	if params.Path != "" {
		inString = fmt.Sprintf(` in "%s"`, params.Path)
	}
	matchWord := "match"
	if resultsLength != 1 {
		matchWord = "matches"
	}
	message.WriteString(fmt.Sprintf(`<summary>Tool use: **%s** • Grep for "%s"%s • %d %s</summary>

`, bubble.Name, params.Pattern, inString, resultsLength, matchWord))

	// Add output mode
	outputMode := params.OutputMode
	if outputMode == "" {
		outputMode = "content"
	}
	message.WriteString(fmt.Sprintf("Output mode: %s\n", outputMode))

	// Add details
	message.WriteString(fmt.Sprintf("\n%s\n", messageDetails))

	message.WriteString("\n</details>")
	return message.String(), nil
}

// formatContentResults formats content matches as a markdown table
func (h *GrepHandler) formatContentResults(content *GrepContentResult) string {
	if len(content.Matches) == 0 {
		return "\n_No matches found_"
	}

	// Check if we have file names in the results
	hasFiles := false
	for _, fileMatch := range content.Matches {
		if fileMatch.File != "" {
			hasFiles = true
			break
		}
	}

	var result strings.Builder

	// Build table header
	if hasFiles {
		result.WriteString("\n| File | Content | Line |\n|------|------|------|\n")
	} else {
		result.WriteString("\n| Content | Line |\n|------|------|\n")
	}

	// Build table rows
	for _, fileMatch := range content.Matches {
		for _, match := range fileMatch.Matches {
			// Skip context lines
			if match.IsContextLine {
				continue
			}
			// Skip matches without content (empty lines)
			if match.Content == "" {
				continue
			}

			// Add file column if needed
			if hasFiles {
				result.WriteString(fmt.Sprintf("| `%s` ", escapeTableCellValue(fileMatch.File)))
			}

			// Add content and line number
			result.WriteString(fmt.Sprintf("| `%s` | L%d |\n",
				escapeTableCellValue(match.Content),
				match.LineNumber))
		}
	}

	return result.String()
}

// formatFilesResults formats file matches as a markdown table
func (h *GrepHandler) formatFilesResults(files *GrepFilesResult) string {
	if len(files.Files) == 0 {
		return "\n_No matches found_"
	}

	var result strings.Builder
	result.WriteString("\n| File |\n|------|\n")

	for _, file := range files.Files {
		result.WriteString(fmt.Sprintf("| `%s` |\n", file))
	}

	return result.String()
}

// GetToolType returns the tool type category
func (h *GrepHandler) GetToolType() ToolType {
	return ToolTypeSearch
}

// escapeTableCellValue escapes special characters for markdown table cells
// Matches the TypeScript escapeTableCellValue function
func escapeTableCellValue(value string) string {
	// Escape pipes
	value = strings.ReplaceAll(value, "|", "\\|")
	// Convert newlines to HTML breaks
	value = strings.ReplaceAll(value, "\n", "<br/>")
	// Escape braces (for some markdown parsers)
	value = escapeBraces(value)
	// Normalize tabs to spaces
	value = strings.ReplaceAll(value, "\t", "    ")
	// Remove carriage returns
	value = strings.ReplaceAll(value, "\r", "")
	// Trim whitespace
	value = strings.TrimSpace(value)

	return value
}

// escapeBraces escapes curly braces in markdown
func escapeBraces(s string) string {
	var result strings.Builder
	prevChar := rune(0)

	for _, char := range s {
		if (char == '{' || char == '}') && prevChar != '\\' {
			result.WriteRune('\\')
		}
		result.WriteRune(char)
		prevChar = char
	}

	return result.String()
}
