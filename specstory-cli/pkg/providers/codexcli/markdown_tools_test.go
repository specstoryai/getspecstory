package codexcli

import (
	"strings"
	"testing"
)

func TestFormatShellWithSummary(t *testing.T) {
	tests := []struct {
		name          string
		argumentsJSON string
		wantSummary   string
		wantBody      string
	}{
		{
			name:          "simple one-line command",
			argumentsJSON: `{"command":"ls -la"}`,
			wantSummary:   "`ls -la`",
			wantBody:      "",
		},
		{
			name:          "multi-line command",
			argumentsJSON: `{"command":"cat <<'EOF' > hello.c\n#include <stdio.h>\nEOF"}`,
			wantSummary:   "",
			wantBody:      "```bash\ncat <<'EOF' > hello.c\n#include <stdio.h>\nEOF\n```",
		},
		{
			name:          "command with workdir",
			argumentsJSON: `{"command":"pwd","workdir":"/tmp"}`,
			wantSummary:   "`pwd`",
			wantBody:      "",
		},
		{
			name:          "empty command string",
			argumentsJSON: `{"command":""}`,
			wantSummary:   "",
			wantBody:      "",
		},
		{
			name:          "malformed JSON",
			argumentsJSON: `{invalid json`,
			wantSummary:   "",
			wantBody:      "",
		},
		{
			name:          "missing command field",
			argumentsJSON: `{"workdir":"/tmp"}`,
			wantSummary:   "",
			wantBody:      "",
		},
		{
			name:          "complex multiline heredoc",
			argumentsJSON: `{"command":"bash -lc 'line1\nline2\nline3'"}`,
			wantSummary:   "",
			wantBody:      "```bash\nbash -lc 'line1\nline2\nline3'\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSummary, gotBody := formatShellWithSummary(tt.argumentsJSON)
			if gotSummary != tt.wantSummary {
				t.Errorf("formatShellWithSummary() summary = %q, want %q", gotSummary, tt.wantSummary)
			}
			if gotBody != tt.wantBody {
				t.Errorf("formatShellWithSummary() body = %q, want %q", gotBody, tt.wantBody)
			}
		})
	}
}

func TestFormatUpdatePlan(t *testing.T) {
	tests := []struct {
		name          string
		argumentsJSON string
		wantContains  []string
		wantNotEmpty  bool
	}{
		{
			name:          "pending tasks",
			argumentsJSON: `{"plan":[{"status":"pending","step":"First task"},{"status":"pending","step":"Second task"}]}`,
			wantContains:  []string{"- [ ] First task", "- [ ] Second task", "**Agent task list:**"},
			wantNotEmpty:  true,
		},
		{
			name:          "in-progress task",
			argumentsJSON: `{"plan":[{"status":"in_progress","step":"Working on this"}]}`,
			wantContains:  []string{"- [⚡] Working on this"},
			wantNotEmpty:  true,
		},
		{
			name:          "completed task",
			argumentsJSON: `{"plan":[{"status":"completed","step":"Done with this"}]}`,
			wantContains:  []string{"- [X] Done with this"},
			wantNotEmpty:  true,
		},
		{
			name:          "mixed statuses",
			argumentsJSON: `{"plan":[{"status":"completed","step":"Done"},{"status":"in_progress","step":"Working"},{"status":"pending","step":"Todo"}]}`,
			wantContains:  []string{"- [X] Done", "- [⚡] Working", "- [ ] Todo"},
			wantNotEmpty:  true,
		},
		{
			name:          "empty plan",
			argumentsJSON: `{"plan":[]}`,
			wantContains:  []string{},
			wantNotEmpty:  false,
		},
		{
			name:          "malformed JSON",
			argumentsJSON: `{invalid`,
			wantContains:  []string{},
			wantNotEmpty:  false,
		},
		{
			name:          "missing plan field",
			argumentsJSON: `{"other":"field"}`,
			wantContains:  []string{},
			wantNotEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUpdatePlan("update_plan", tt.argumentsJSON)

			// Check if result is empty/just header when expected
			if !tt.wantNotEmpty {
				lines := strings.Split(strings.TrimSpace(got), "\n")
				if len(lines) > 1 {
					t.Errorf("formatUpdatePlan() expected only header, got %d lines: %q", len(lines), got)
				}
			}

			// Check for expected substrings
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("formatUpdatePlan() result does not contain %q, got: %q", want, got)
				}
			}
		})
	}
}

func TestFormatViewImage(t *testing.T) {
	tests := []struct {
		name          string
		argumentsJSON string
		want          string
	}{
		{
			name:          "valid image path",
			argumentsJSON: `{"path":"/tmp/image.png"}`,
			want:          "/tmp/image.png\n",
		},
		{
			name:          "relative path",
			argumentsJSON: `{"path":"./local/image.jpg"}`,
			want:          "./local/image.jpg\n",
		},
		{
			name:          "empty path",
			argumentsJSON: `{"path":""}`,
			want:          "",
		},
		{
			name:          "missing path field",
			argumentsJSON: `{"other":"field"}`,
			want:          "",
		},
		{
			name:          "malformed JSON",
			argumentsJSON: `{invalid`,
			want:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatViewImage("view_image", tt.argumentsJSON)
			if got != tt.want {
				t.Errorf("formatViewImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatToolCall(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		argumentsJSON string
		wantContains  string
	}{
		{
			name:          "update_plan tool",
			toolName:      "update_plan",
			argumentsJSON: `{"plan":[]}`,
			wantContains:  "",
		},
		{
			name:          "view_image tool",
			toolName:      "view_image",
			argumentsJSON: `{"path":"/tmp/img.png"}`,
			wantContains:  "/tmp/img.png",
		},
		{
			name:          "unknown tool uses default formatter",
			toolName:      "unknown_tool",
			argumentsJSON: `{}`,
			wantContains:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolCall(tt.toolName, tt.argumentsJSON)
			// Skip check if we expect empty string
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("formatToolCall() does not contain %q, got: %q", tt.wantContains, got)
			}
		})
	}
}

func TestFormatApplyPatch(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains []string
	}{
		{
			name: "add file with content",
			input: `*** Begin Patch
*** Add File: codex/index.html
+<!DOCTYPE html>
+<html>
+<body>Hello World</body>
+</html>
*** End Patch`,
			wantContains: []string{
				"Add:",
				"**Add: `codex/index.html`**",
				"```diff",
				"+<!DOCTYPE html>",
				"+<html>",
			},
		},
		{
			name: "modify file",
			input: `*** Begin Patch
*** Modify File: src/main.go
-func old() {
+func new() {
*** End Patch`,
			wantContains: []string{
				"Modify:",
				"**Modify: `src/main.go`**",
				"```diff",
				"-func old() {",
				"+func new() {",
			},
		},
		{
			name: "delete file",
			input: `*** Begin Patch
*** Delete File: old/file.txt
*** End Patch`,
			wantContains: []string{
				"Delete:",
				"**Delete: `old/file.txt`**",
			},
		},
		{
			name: "multiple files",
			input: `*** Begin Patch
*** Add File: new.txt
+content1
*** Modify File: existing.txt
-old
+new
*** Delete File: removed.txt
*** End Patch`,
			wantContains: []string{
				"**Add: `new.txt`**",
				"**Modify: `existing.txt`**",
				"**Delete: `removed.txt`**",
			},
		},
		{
			name:         "empty input",
			input:        "",
			wantContains: []string{},
		},
		{
			name:         "no patch markers",
			input:        "just some random text",
			wantContains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatApplyPatch("apply_patch", tt.input)

			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("formatApplyPatch() result does not contain %q\nGot: %q", want, got)
				}
			}
		})
	}
}

func TestFormatCustomToolCall(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		input        string
		wantContains []string
	}{
		{
			name:     "apply_patch tool",
			toolName: "apply_patch",
			input: `*** Begin Patch
*** Add File: test.txt
+content
*** End Patch`,
			wantContains: []string{
				"Add:",
				"**Add: `test.txt`**",
			},
		},
		{
			name:         "unknown tool with short input",
			toolName:     "custom_tool",
			input:        "short input text",
			wantContains: []string{"Input:", "Input: ```", "short input text"},
		},
		{
			name:     "unknown tool with long input (truncated)",
			toolName: "another_tool",
			input:    strings.Repeat("x", 300),
			wantContains: []string{
				"Input",
				"Input (truncated):",
				"...",
			},
		},
		{
			name:         "unknown tool with empty input",
			toolName:     "empty_tool",
			input:        "",
			wantContains: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCustomToolCall(tt.toolName, tt.input)

			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("formatCustomToolCall() result does not contain %q\nGot: %q", want, got)
				}
			}
		})
	}
}
