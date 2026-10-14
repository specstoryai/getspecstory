package cursorcloud

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// expectedToolTypes is the classification of every name recorded in
// tool_call events (testdata/tools.txt section 1).
var expectedToolTypes = map[string]string{
	"await":            schema.ToolTypeGeneric,
	"delete_file":      schema.ToolTypeWrite,
	"edit_file":        schema.ToolTypeWrite,
	"file_search":      schema.ToolTypeSearch,
	"generate_image":   schema.ToolTypeGeneric,
	"get_mcp_tools":    schema.ToolTypeGeneric,
	"grep_search":      schema.ToolTypeSearch,
	"mcp":              schema.ToolTypeGeneric,
	"read_canvas":      schema.ToolTypeGeneric,
	"read_file":        schema.ToolTypeRead,
	"run_terminal_cmd": schema.ToolTypeShell,
	"todo_write":       schema.ToolTypeTask,
	"web_fetch":        schema.ToolTypeRead,
	"web_search":       schema.ToolTypeSearch,
	"write_canvas":     schema.ToolTypeGeneric,
}

// recordedToolNames reads section 1 of testdata/tools.txt.
func recordedToolNames(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "tools.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var names []string
	inSection := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "## 1."):
			inSection = true
		case strings.HasPrefix(line, "## "):
			inSection = false
		case inSection && line != "" && !strings.HasPrefix(line, "#"):
			names = append(names, line)
		}
	}
	return names
}

func TestToolTypeSweep(t *testing.T) {
	names := recordedToolNames(t)
	if len(names) == 0 {
		t.Fatal("no tool names read from testdata/tools.txt")
	}
	for _, name := range names {
		want, ok := expectedToolTypes[name]
		if !ok {
			t.Errorf("%s is in tools.txt but has no expected type here", name)
			continue
		}
		if got := toolType(name); got != want {
			t.Errorf("toolType(%q) = %q, want %q", name, got, want)
		}
	}
	if got := toolType("never_seen_tool"); got != schema.ToolTypeUnknown {
		t.Errorf("an unenumerated tool is %q, want unknown", got)
	}
}

// renderedCalls converts every tool call in the captured fixtures through the
// production dispatch path.
func renderedCalls(t *testing.T) []schema.Message {
	t.Helper()
	var msgs []schema.Message
	for _, name := range []string{"fixture-run", "tool-enumeration", "failure-run"} {
		fx := loadFixture(t, name)
		runIDs := make([]string, 0, len(fx.streams))
		for id := range fx.streams {
			runIDs = append(runIDs, id)
		}
		sort.Strings(runIDs)
		for _, id := range runIDs {
			for _, seg := range parseRunEvents(parseSSEBytes(t, fx.streams[id]), name).segments {
				if seg.call != nil {
					msgs = append(msgs, toolMessage(seg.call, testWorkspace))
				}
			}
		}
	}
	return msgs
}

func TestCapturedToolCallsRender(t *testing.T) {
	// What every captured call of a tool must show, success or failure.
	expect := map[string][]string{
		"run_terminal_cmd": {"```bash", "Result"},
		"edit_file":        {"```diff"},
		"todo_write":       {"Todo List:\n- ["},
		"mcp":              {"Server: `cursor-", "Result:\n```text"},
		"get_mcp_tools":    {"Server: `cursor"},
		"await":            {"Result: completed after 1000 ms"},
		"generate_image":   {"Description: ", "Result: saved to `/opt/cursor/artifacts/assets/tool_demo_icon.jpg`"},
		"write_canvas":     {"```jsx", "Result: [Tool Demo Canvas](https://cursor.com/canvas/"},
		"read_canvas":      {"Canvas: ", "```jsx"},
		"file_search":      {"Directory: `.`", "Result: 1 file\n- `tools.txt`"},
		"web_search":       {"Result:\n\nLinks:\n1. [Cursor Agent](https://cursor.com/docs/agent/overview)", "Highlights:\n```text\n<result id=\"1\">"},
	}
	seen := map[string]bool{}
	for _, msg := range renderedCalls(t) {
		tool := msg.Tool
		seen[tool.Name] = true
		if tool.Type == schema.ToolTypeUnknown {
			t.Errorf("%s classified unknown", tool.Name)
		}
		if tool.FormattedMarkdown == nil || strings.TrimSpace(*tool.FormattedMarkdown) == "" {
			t.Errorf("%s (%s) rendered nothing", tool.Name, tool.UseID)
			continue
		}
		md := *tool.FormattedMarkdown
		for _, want := range expect[tool.Name] {
			if !strings.Contains(md, want) {
				t.Errorf("%s (%s) markdown lacks %q:\n%s", tool.Name, tool.UseID, want, md)
			}
		}
		for _, key := range plumbingInputKeys {
			if _, ok := tool.Input[key]; ok {
				t.Errorf("%s input still carries %s", tool.Name, key)
			}
		}
		// Known tools never fall back to dumping their raw result envelope.
		if strings.Contains(md, `"success": {`) {
			t.Errorf("%s rendered its raw result envelope:\n%s", tool.Name, md)
		}
	}
	for _, name := range recordedToolNames(t) {
		if !seen[name] {
			t.Errorf("no captured call of %s was rendered", name)
		}
	}
}

func TestCapturedFailuresRenderTheError(t *testing.T) {
	// The failure-run capture's deliberate failures, keyed by call id.
	want := map[string]string{
		"read_file":   "Error: File not found",
		"delete_file": "Error: File not found: /workspace/missing-file.txt",
		"grep_search": "Error: Path does not exist: /nonexistent-path",
		"web_fetch":   "Error: Error fetching URL, status code: 500 Internal Server Error",
	}
	found := map[string]bool{}
	for _, msg := range renderedCalls(t) {
		md := ""
		if msg.Tool.FormattedMarkdown != nil {
			md = *msg.Tool.FormattedMarkdown
		}
		if text, ok := want[msg.Tool.Name]; ok && strings.Contains(md, text) {
			found[msg.Tool.Name] = true
		}
		if msg.Tool.Name == "run_terminal_cmd" && strings.Contains(md, "cat /nonexistent-file") {
			found["shell failure"] = true
			if !strings.Contains(md, "Exit code: 1") || !strings.Contains(md, "No such file or directory") {
				t.Errorf("shell failure lacks exit code or stderr:\n%s", md)
			}
		}
	}
	for name := range want {
		if !found[name] {
			t.Errorf("no %s call rendered %q", name, want[name])
		}
	}
	if !found["shell failure"] {
		t.Error("the failing shell command was not rendered")
	}
}

func TestFormatToolFallbacks(t *testing.T) {
	tests := []struct {
		name string
		tool schema.ToolInfo
		want []string
	}{
		{
			name: "unknown tool renders input and result as JSON",
			tool: schema.ToolInfo{Name: "brand_new", Input: map[string]any{"a": "b"}, Output: map[string]any{"success": map[string]any{"x": 1.0}}},
			want: []string{"```json\n{\n  \"a\": \"b\"\n}", "Result:\n```json"},
		},
		{
			name: "error without a known message key",
			tool: schema.ToolInfo{Name: "read_file", Input: map[string]any{"path": "/workspace/a"}, Output: map[string]any{"error": map[string]any{"code": 7.0}}},
			want: []string{`Error: {"code":7}`},
		},
		{
			name: "unfamiliar result shape on a known tool",
			tool: schema.ToolInfo{Name: "read_file", Input: map[string]any{"path": "/workspace/a"}, Output: map[string]any{"pending": true}},
			want: []string{"Result:\n```json"},
		},
		{
			name: "edit still streaming shows its content",
			tool: schema.ToolInfo{Name: "edit_file", Input: map[string]any{"path": "/workspace/a.go", "streamContent": "package a"}},
			want: []string{"```go\npackage a\n```"},
		},
		{
			name: "malformed todo item is skipped",
			tool: schema.ToolInfo{Name: "todo_write", Input: map[string]any{"todos": []any{"bad", map[string]any{"content": "ok", "status": "TODO_STATUS_COMPLETED"}}}},
			want: []string{"Todo List:\n- [x] ok\n"},
		},
		{
			name: "background shell command",
			tool: schema.ToolInfo{Name: "run_terminal_cmd", Input: map[string]any{"command": "sleep 9"}, Output: map[string]any{"success": map[string]any{}, "isBackground": true}},
			want: []string{"Running in the background.", "Result: (no output)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tt.tool
			md := formatTool(&tool)
			for _, want := range tt.want {
				if !strings.Contains(md, want) {
					t.Errorf("markdown lacks %q:\n%s", want, md)
				}
			}
		})
	}
}
