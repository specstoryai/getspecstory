package deepseek

import (
	"strings"
	"testing"
)

// TestFormatToolCall_ReadFile_InputThenOutput exercises the two-phase render
// pattern: first a tool_use is rendered with input only (no Output map yet),
// then attachToolResults sets Output and the renderer adds an "Output:" block.
func TestFormatToolCall_ReadFile_InputThenOutput(t *testing.T) {
	tool := &ToolInfo{
		Name:  "read_file",
		Type:  "read",
		UseID: "id-1",
		Input: map[string]interface{}{"file_path": "pkg/main.go"},
	}

	// Phase 1: input only, no result yet.
	got := formatToolCall(tool)
	if !strings.Contains(got, "Path: `pkg/main.go`") {
		t.Errorf("phase 1 missing input section, got:\n%s", got)
	}
	if strings.Contains(got, "Output:") {
		t.Errorf("phase 1 should not have Output yet, got:\n%s", got)
	}

	// Phase 2: result attached.
	tool.Output = map[string]interface{}{"content": "package main\n\nfunc main() {}"}
	got = formatToolCall(tool)
	if !strings.Contains(got, "Path: `pkg/main.go`") {
		t.Errorf("phase 2 lost input section, got:\n%s", got)
	}
	if !strings.Contains(got, "Output:") {
		t.Errorf("phase 2 should now include Output block, got:\n%s", got)
	}
	if !strings.Contains(got, "package main") {
		t.Errorf("phase 2 should include actual content, got:\n%s", got)
	}
	// Multi-line output gets fenced with ```text.
	if !strings.Contains(got, "```text") {
		t.Errorf("multi-line output should use ```text fence, got:\n%s", got)
	}
}

func TestFormatToolCall_NilSafe(t *testing.T) {
	if got := formatToolCall(nil); got != "" {
		t.Errorf("expected empty string for nil tool, got %q", got)
	}
}

func TestFormatToolOutput(t *testing.T) {
	tests := []struct {
		name string
		out  map[string]interface{}
		want string
	}{
		{
			name: "nil output yields empty",
			out:  nil,
			want: "",
		},
		{
			name: "empty content yields empty",
			out:  map[string]interface{}{"content": ""},
			want: "",
		},
		{
			name: "whitespace content yields empty",
			out:  map[string]interface{}{"content": "   \n\t"},
			want: "",
		},
		{
			name: "single-line content uses inline form",
			out:  map[string]interface{}{"content": "ok"},
			want: "Output: ok",
		},
		{
			name: "multi-line content uses fenced form",
			out:  map[string]interface{}{"content": "line1\nline2"},
			want: "Output:\n```text\nline1\nline2\n```",
		},
		{
			name: "non-string content yields empty",
			out:  map[string]interface{}{"content": 42},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolInfo{Output: tt.out}
			if got := formatToolOutput(tool); got != tt.want {
				t.Errorf("formatToolOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatToolInput(t *testing.T) {
	tests := []struct {
		name           string
		toolName       string
		input          map[string]interface{}
		expectedSubstr []string
	}{
		{
			name:     "exec_shell with command and workdir",
			toolName: "exec_shell",
			input:    map[string]interface{}{"command": "ls -la", "workdir": "/repo"},
			expectedSubstr: []string{
				"Directory: `/repo`",
				"`ls -la`",
			},
		},
		{
			name:           "read_file with file_path",
			toolName:       "read_file",
			input:          map[string]interface{}{"file_path": "main.go"},
			expectedSubstr: []string{"Path: `main.go`"},
		},
		{
			name:     "read_file with offset and limit",
			toolName: "read_file",
			input:    map[string]interface{}{"file_path": "main.go", "offset": 10, "limit": 50},
			expectedSubstr: []string{
				"Path: `main.go`",
				"Lines: offset 10, limit 50",
			},
		},
		{
			name:           "list_dir with path",
			toolName:       "list_dir",
			input:          map[string]interface{}{"path": "/repo"},
			expectedSubstr: []string{"Path: `/repo`"},
		},
		{
			name:     "grep_files with pattern path glob",
			toolName: "grep_files",
			input: map[string]interface{}{
				"pattern": "TODO",
				"path":    "/repo",
				"include": "*.go",
			},
			expectedSubstr: []string{
				"Pattern: `TODO`",
				"Path: `/repo`",
				"Glob: `*.go`",
			},
		},
		{
			name:           "web_search with query",
			toolName:       "web_search",
			input:          map[string]interface{}{"query": "deepseek tui"},
			expectedSubstr: []string{"Query: `deepseek tui`"},
		},
		{
			name:     "fetch_url with url and prompt",
			toolName: "fetch_url",
			input: map[string]interface{}{
				"url":    "https://example.com",
				"prompt": "summarize",
			},
			expectedSubstr: []string{
				"URL: `https://example.com`",
				"summarize",
			},
		},
		{
			name:     "write_file with path and content",
			toolName: "write_file",
			input: map[string]interface{}{
				"file_path": "hello.go",
				"content":   "package main",
			},
			expectedSubstr: []string{
				"Path: `hello.go`",
				"```go",
				"package main",
			},
		},
		{
			name:     "edit_file produces diff block",
			toolName: "edit_file",
			input: map[string]interface{}{
				"file_path": "x.go",
				"old_str":   "foo",
				"new_str":   "bar",
			},
			expectedSubstr: []string{
				"Path: `x.go`",
				"```diff",
				"-foo",
				"+bar",
			},
		},
		{
			name:     "apply_patch fenced as diff",
			toolName: "apply_patch",
			input:    map[string]interface{}{"patch": "@@ -1 +1 @@\n-old\n+new"},
			expectedSubstr: []string{
				"```diff",
				"@@ -1 +1 @@",
			},
		},
		{
			name:     "todo_write renders todo list",
			toolName: "todo_write",
			input: map[string]interface{}{
				"todos": []interface{}{
					map[string]interface{}{"description": "Ship feature", "status": "completed"},
					map[string]interface{}{"description": "Write docs", "status": "in_progress"},
					map[string]interface{}{"description": "Tests", "status": "pending"},
				},
			},
			expectedSubstr: []string{
				"Todo List:",
				"[x] Ship feature",
				"Write docs",
				"Tests",
			},
		},
		{
			name:     "unknown tool falls back to JSON",
			toolName: "mystery_tool",
			input:    map[string]interface{}{"foo": "bar"},
			expectedSubstr: []string{
				"```json",
				"\"foo\"",
				"\"bar\"",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolInfo{Name: tt.toolName, Input: tt.input}
			got := formatToolInput(tool)
			for _, want := range tt.expectedSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, got)
				}
			}
		})
	}
}

func TestRenderGenericJSON_SortsKeys(t *testing.T) {
	got := renderGenericJSON(map[string]any{"z": 1, "a": "first"})
	if strings.Index(got, `"a"`) > strings.Index(got, `"z"`) {
		t.Errorf("renderGenericJSON should sort keys, got:\n%s", got)
	}
}

func TestExtractPathHints(t *testing.T) {
	tests := []struct {
		name          string
		input         map[string]interface{}
		workspaceRoot string
		wantContains  []string
		wantNot       []string
	}{
		{
			name:         "file_path field is extracted",
			input:        map[string]interface{}{"file_path": "main.go"},
			wantContains: []string{"main.go"},
		},
		{
			name:          "absolute path under workspace becomes relative",
			input:         map[string]interface{}{"file_path": "/repo/pkg/foo.go"},
			workspaceRoot: "/repo",
			wantContains:  []string{"pkg/foo.go"},
		},
		{
			name:         "nil input yields nil",
			input:        nil,
			wantContains: nil,
		},
		{
			name:         "no recognized fields yields nil",
			input:        map[string]interface{}{"unrelated": "value"},
			wantContains: nil,
		},
		{
			name:         "shell command redirect target is extracted",
			input:        map[string]interface{}{"command": "echo hello > out.txt"},
			wantContains: []string{"out.txt"},
		},
		{
			name: "duplicate hints are de-duplicated",
			input: map[string]interface{}{
				"file_path": "main.go",
				"path":      "main.go",
			},
			wantContains: []string{"main.go"},
		},
		{
			name: "array values are walked",
			input: map[string]interface{}{
				"path": []interface{}{"a.go", "b.go"},
			},
			wantContains: []string{"a.go", "b.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathHints(tt.input, tt.workspaceRoot)

			if len(tt.wantContains) == 0 {
				if len(got) != 0 {
					t.Errorf("expected no hints, got %v", got)
				}
				return
			}

			for _, want := range tt.wantContains {
				found := false
				for _, h := range got {
					if h == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected hints to contain %q, got %v", want, got)
				}
			}

			// Also assert no duplicates
			seen := make(map[string]int)
			for _, h := range got {
				seen[h]++
				if seen[h] > 1 {
					t.Errorf("duplicate hint %q in %v", h, got)
				}
			}

			for _, notWant := range tt.wantNot {
				for _, h := range got {
					if h == notWant {
						t.Errorf("hint %q should not be present, got %v", notWant, got)
					}
				}
			}
		})
	}
}
