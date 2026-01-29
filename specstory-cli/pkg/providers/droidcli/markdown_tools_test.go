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
		{
			name:     "AskUser with questionnaire and answer",
			toolName: "AskUser",
			input: map[string]any{
				"questionnaire": "1. [question] Pick one?\n[option] A\n[option] B",
			},
			result: &fdToolResult{Content: "1. [question] Pick one?\n[answer] A"},
			expectedSubstr: []string{
				"**Pick one?**",
				"Options:",
				"- A",
				"- B",
				"**Answer:** A",
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

func TestParseQuestionnaire(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantFirst string
		wantOpts  []string
	}{
		{
			name: "single question with options",
			input: `1. [question] Which language do you prefer?
[topic] Language
[option] Python
[option] JavaScript
[option] TypeScript`,
			wantCount: 1,
			wantFirst: "Which language do you prefer?",
			wantOpts:  []string{"Python", "JavaScript", "TypeScript"},
		},
		{
			name: "multiple questions",
			input: `1. [question] First question?
[option] A
[option] B

2. [question] Second question?
[option] X
[option] Y`,
			wantCount: 2,
			wantFirst: "First question?",
			wantOpts:  []string{"A", "B"},
		},
		{
			name:      "empty input",
			input:     "",
			wantCount: 0,
		},
		{
			name:      "no questions",
			input:     "just some random text",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseQuestionnaire(tt.input)
			if len(got) != tt.wantCount {
				t.Errorf("parseQuestionnaire() returned %d questions, want %d", len(got), tt.wantCount)
				return
			}
			if tt.wantCount > 0 {
				if got[0].question != tt.wantFirst {
					t.Errorf("first question = %q, want %q", got[0].question, tt.wantFirst)
				}
				if len(got[0].options) != len(tt.wantOpts) {
					t.Errorf("first question has %d options, want %d", len(got[0].options), len(tt.wantOpts))
				}
				for i, opt := range tt.wantOpts {
					if i < len(got[0].options) && got[0].options[i] != opt {
						t.Errorf("option[%d] = %q, want %q", i, got[0].options[i], opt)
					}
				}
			}
		})
	}
}

func TestParseAskUserAnswers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "single answer",
			input: `1. [question] Which language?
[answer] Python`,
			want: []string{"Python"},
		},
		{
			name: "multiple answers",
			input: `1. [question] First?
[answer] A

2. [question] Second?
[answer] B`,
			want: []string{"A", "B"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "no answers",
			input: "just some text without answers",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAskUserAnswers(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("parseAskUserAnswers() returned %d answers, want %d", len(got), len(tt.want))
				return
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("answer[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestRenderAskUserInput(t *testing.T) {
	tests := []struct {
		name           string
		questionnaire  string
		answer         string
		expectedSubstr []string
		notExpected    []string
	}{
		{
			name: "full questionnaire with answer",
			questionnaire: `1. [question] Which language?
[topic] Lang
[option] Python
[option] Go`,
			answer: `1. [question] Which language?
[answer] Python`,
			expectedSubstr: []string{
				"**Which language?**",
				"Options:",
				"- Python",
				"- Go",
				"**Answer:** Python",
			},
			notExpected: []string{
				"[topic]",
				"[question]",
				"[option]",
				"```json",
			},
		},
		{
			name: "multiple questions with answers",
			questionnaire: `1. [question] First?
[option] A
[option] B

2. [question] Second?
[option] X
[option] Y`,
			answer: `1. [question] First?
[answer] A

2. [question] Second?
[answer] Y`,
			expectedSubstr: []string{
				"**First?**",
				"**Second?**",
				"**Answer:** A, Y",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"questionnaire": tt.questionnaire}
			var result *fdToolResult
			if tt.answer != "" {
				result = &fdToolResult{Content: tt.answer}
			}

			got := renderAskUserInput(args, result)

			for _, substr := range tt.expectedSubstr {
				if !strings.Contains(got, substr) {
					t.Errorf("expected output to contain %q, got:\n%s", substr, got)
				}
			}
			for _, substr := range tt.notExpected {
				if strings.Contains(got, substr) {
					t.Errorf("expected output NOT to contain %q, got:\n%s", substr, got)
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
