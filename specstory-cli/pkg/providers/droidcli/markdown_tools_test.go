package droidcli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatToolCall(t *testing.T) {
	tests := []struct {
		name           string
		toolName       string
		input          map[string]any
		result         *fdToolResult
		expectedSubstr []string
	}{
		{
			name:     "Execute with command and workdir",
			toolName: "Execute",
			input:    map[string]any{"command": "ls", "workdir": "/repo"},
			result:   &fdToolResult{Content: "main.go"},
			expectedSubstr: []string{
				"`ls`",
				"Output:",
			},
		},
		{
			name:     "TodoWrite with array format",
			toolName: "TodoWrite",
			input: map[string]any{
				"todos": []map[string]any{{"description": "Task", "status": "completed"}},
			},
			expectedSubstr: []string{
				"Todo List",
			},
		},
		{
			name:     "TodoWrite with string format",
			toolName: "TodoWrite",
			input: map[string]any{
				"todos": "1. [completed] Ship feature",
			},
			expectedSubstr: []string{
				"Todo List",
				"Ship feature",
			},
		},
		{
			name:     "WebSearch with query and options",
			toolName: "WebSearch",
			input: map[string]any{
				"query":      "Delhi weather",
				"category":   "news",
				"numResults": 5,
			},
			expectedSubstr: []string{
				"Query: `Delhi weather`",
				"Category: `news`",
				"Results: `5`",
			},
		},
		{
			name:     "Grep with pattern and glob",
			toolName: "Grep",
			input: map[string]any{
				"pattern":      "TODO",
				"path":         "/repo",
				"glob_pattern": "*.go",
			},
			expectedSubstr: []string{
				"Pattern: `TODO`",
				"Path: `/repo`",
				"Glob: `*.go`",
			},
		},
		{
			name:     "Write with file path and content",
			toolName: "Write",
			input: map[string]any{
				"file_path": "main.go",
				"content":   "package main",
			},
			expectedSubstr: []string{
				"Path: `main.go`",
				"```go",
				"package main",
			},
		},
		{
			name:     "Read with file path",
			toolName: "Read",
			input: map[string]any{
				"file_path": "/repo/main.go",
			},
			expectedSubstr: []string{
				"Path: `/repo/main.go`",
			},
		},
		{
			name:     "Edit with old and new text",
			toolName: "Edit",
			input: map[string]any{
				"file_path":  "main.go",
				"old_string": "foo",
				"new_string": "bar",
			},
			expectedSubstr: []string{
				"Path: `main.go`",
				"```diff",
				"-foo",
				"+bar",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatalf("failed to marshal input: %v", err)
			}

			tool := &fdToolCall{
				ID:     "call_1",
				Name:   tt.toolName,
				Input:  inputJSON,
				Result: tt.result,
			}

			md := formatToolCall(tool)

			for _, substr := range tt.expectedSubstr {
				if !strings.Contains(md, substr) {
					t.Errorf("expected output to contain %q, got:\n%s", substr, md)
				}
			}
		})
	}
}

func TestToolTypeMappings(t *testing.T) {
	tests := []struct {
		toolName     string
		expectedType string
	}{
		{"Execute", "shell"},
		{"run", "shell"},
		{"Bash", "shell"},
		{"Read", "read"},
		{"cat", "read"},
		{"WebSearch", "search"},
		{"Grep", "search"},
		{"glob", "search"},
		{"Write", "write"},
		{"Edit", "write"},
		{"MultiEdit", "write"},
		{"ApplyPatch", "write"},
		{"TodoWrite", "task"},
		{"UnknownTool", "generic"},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := toolType(tt.toolName)
			if got != tt.expectedType {
				t.Errorf("toolType(%q) = %q, want %q", tt.toolName, got, tt.expectedType)
			}
		})
	}
}
