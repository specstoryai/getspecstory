package claudecode

import (
	"strings"
	"testing"
)

// TestFormatToolAsMarkdown_OutputWithCodeFences reproduces the real-world fold:
// a tool whose output contains its own ``` fences (e.g. `cat README.md`),
// truncated so the inner fences are unbalanced. The wrapper must size up so the
// embedded fences cannot break out and swallow the rest of the document.
func TestFormatToolAsMarkdown_OutputWithCodeFences(t *testing.T) {
	tool := &ToolInfo{
		Name:  "Bash",
		Input: map[string]interface{}{"command": "cat README.md"},
		Output: map[string]interface{}{
			"content": "```sql\nCREATE TABLE x\n```\nmid text\n```bash\necho hi\n```\ntrailing open:\n```",
		},
	}

	result := formatToolAsMarkdown(tool, "/workspace")

	// Output is wrapped in a >=4 backtick fence so embedded ``` can't break out.
	if !strings.Contains(result, "````text\n") {
		t.Fatalf("expected a 4-backtick text fence wrapping the output; got:\n%s", result)
	}
	// The 4-backtick wrapper must be balanced: exactly one open and one close.
	if n := strings.Count(result, "````"); n != 2 {
		t.Errorf("expected exactly 2 four-backtick fences (open+close), got %d:\n%s", n, result)
	}
	// Embedded fenced content is preserved verbatim — no backslash artifact.
	if !strings.Contains(result, "```sql\nCREATE TABLE x\n```") {
		t.Errorf("embedded fenced content not preserved verbatim:\n%s", result)
	}
	if strings.Contains(result, "\\```") {
		t.Errorf("output should not contain backslash-escaped fences:\n%s", result)
	}
}

// TestFormatToolAsMarkdown_ErrorOutputWithCodeFences covers the is_error branch,
// which shares the same wrapping path.
func TestFormatToolAsMarkdown_ErrorOutputWithCodeFences(t *testing.T) {
	tool := &ToolInfo{
		Name:  "Bash",
		Input: map[string]interface{}{"command": "false"},
		Output: map[string]interface{}{
			"is_error": true,
			"content":  "traceback:\n```\nboom\n```",
		},
	}

	result := formatToolAsMarkdown(tool, "/workspace")
	if !strings.Contains(result, "````text\n") {
		t.Fatalf("expected a 4-backtick text fence for error output; got:\n%s", result)
	}
	if n := strings.Count(result, "````"); n != 2 {
		t.Errorf("expected balanced 4-backtick fences, got %d:\n%s", n, result)
	}
}
