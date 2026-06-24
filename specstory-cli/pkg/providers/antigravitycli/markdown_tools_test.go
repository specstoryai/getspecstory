package antigravitycli

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestClassifyToolType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"run_command", schema.ToolTypeShell},
		{"view_file", schema.ToolTypeRead},
		{"list_dir", schema.ToolTypeRead},
		{"write_to_file", schema.ToolTypeWrite},
		{"replace_file_content", schema.ToolTypeWrite},
		{"grep_search", schema.ToolTypeSearch},
		{"codebase_search", schema.ToolTypeSearch},
		{"update_plan", schema.ToolTypeTask},
		{"list_permissions", schema.ToolTypeGeneric},
		{"some_future_tool", schema.ToolTypeGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyToolType(tt.name); got != tt.want {
				t.Errorf("classifyToolType(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestFormatToolCall_RunCommandTwoPhase(t *testing.T) {
	tool := &ToolInfo{
		Name:  "run_command",
		Type:  schema.ToolTypeShell,
		Input: map[string]any{"CommandLine": "git status", "Cwd": "/proj"},
	}

	// Phase 1: input only, no output section.
	phase1 := formatToolCall(tool)
	if !strings.Contains(phase1, "git status") || !strings.Contains(phase1, "/proj") {
		t.Errorf("phase 1 missing command/dir: %q", phase1)
	}
	if strings.Contains(phase1, "Output:") {
		t.Errorf("phase 1 should not contain output: %q", phase1)
	}

	// Phase 2: after output attached.
	tool.Output = map[string]any{"content": "On branch dev\nnothing to commit"}
	phase2 := formatToolCall(tool)
	if !strings.Contains(phase2, "Output:") || !strings.Contains(phase2, "nothing to commit") {
		t.Errorf("phase 2 missing output: %q", phase2)
	}
}

func TestRenderInputs(t *testing.T) {
	if got := renderReadInput(map[string]any{"AbsolutePath": "file:///proj/main.go"}); !strings.Contains(got, "/proj/main.go") || strings.Contains(got, "file://") {
		t.Errorf("renderReadInput stripped file:// and shows path: got %q", got)
	}
	if got := renderGrepInput(map[string]any{"Query": "TODO", "SearchPath": "/proj"}); !strings.Contains(got, "TODO") || !strings.Contains(got, "/proj") {
		t.Errorf("renderGrepInput = %q", got)
	}
	if got := renderWriteInput(map[string]any{"TargetFile": "/proj/x.go", "CodeContent": "package main"}); !strings.Contains(got, "/proj/x.go") || !strings.Contains(got, "package main") {
		t.Errorf("renderWriteInput = %q", got)
	}
	edit := renderEditInput(map[string]any{"TargetFile": "/proj/x.go", "TargetContent": "old", "ReplacementContent": "new"})
	if !strings.Contains(edit, "```diff") || !strings.Contains(edit, "-old") || !strings.Contains(edit, "+new") {
		t.Errorf("renderEditInput should produce a diff: %q", edit)
	}
}

func TestExtractPathHints(t *testing.T) {
	hints := extractPathHints(map[string]any{"AbsolutePath": "file:///proj/main.go"}, "/proj")
	if len(hints) == 0 {
		t.Fatalf("expected a path hint")
	}
	for _, h := range hints {
		if strings.Contains(h, "file://") {
			t.Errorf("path hint should not contain file:// scheme: %q", h)
		}
	}

	// Shell command path hints come from the command line.
	shellHints := extractPathHints(map[string]any{"CommandLine": "cat /proj/notes.txt", "Cwd": "/proj"}, "/proj")
	if len(shellHints) == 0 {
		t.Errorf("expected shell command path hints")
	}
}
