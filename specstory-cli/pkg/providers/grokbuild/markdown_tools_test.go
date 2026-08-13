package grokbuild

import (
	"strings"
	"testing"
)

func TestFormatToolAsMarkdown_ReadFile(t *testing.T) {
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"target_file": "/Users/dev/project/main.go"},
		Output: map[string]any{"output": "package main\n\nfunc main() {}", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`/Users/dev/project/main.go`") {
		t.Errorf("summary should carry the path: %v", tool.Summary)
	}
	if !strings.Contains(md, "```go\npackage main") {
		t.Errorf("file content should be fenced with the file's language:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ShellCommand(t *testing.T) {
	tool := &ToolInfo{
		Name: "run_terminal_command",
		Type: "shell",
		Input: map[string]any{
			"command":     "python3 -m pytest",
			"description": "Run the tests",
		},
		Output: map[string]any{"output": "2 passed\n1 skipped", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Run the tests") {
		t.Errorf("description missing:\n%s", md)
	}
	if !strings.Contains(md, "```bash\npython3 -m pytest\n```") {
		t.Errorf("command should be fenced as bash:\n%s", md)
	}
	if !strings.Contains(md, "Result:\n```text\n2 passed") {
		t.Errorf("output should be fenced:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_SearchReplaceRendersDiff(t *testing.T) {
	tool := &ToolInfo{
		Name: "search_replace",
		Type: "write",
		Input: map[string]any{
			"file_path":  "/Users/dev/project/calc.py",
			"old_string": "def sub(a, b):",
			"new_string": "def sub(a, b):\n    return a - b",
		},
		Output: map[string]any{"output": "updated", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Path: `/Users/dev/project/calc.py`") {
		t.Errorf("path missing:\n%s", md)
	}
	if !strings.Contains(md, "```diff") {
		t.Errorf("edit should render as a diff:\n%s", md)
	}
	if !strings.Contains(md, "-def sub(a, b):") || !strings.Contains(md, "+    return a - b") {
		t.Errorf("diff should show both sides:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ErrorTakesPriority(t *testing.T) {
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"target_file": "/missing.txt"},
		Output: map[string]any{"output": "File not found: /missing.txt", "status": "error"},
	}

	md := formatToolAsMarkdown(tool)

	// A failed call must be visibly different from a successful one, because
	// Grok records the failure only in events.jsonl.
	if !strings.Contains(md, "Error: File not found: /missing.txt") {
		t.Errorf("failure should be labelled as an error:\n%s", md)
	}
	if strings.Contains(md, "Result:") {
		t.Errorf("a failed call should not be labelled as a result:\n%s", md)
	}
	// A failed read must not be dressed up as file content.
	if strings.Contains(md, "```txt") {
		t.Errorf("failed read should not render as source:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_TodoChecklist(t *testing.T) {
	tool := &ToolInfo{
		Name: "todo_write",
		Type: "task",
		Input: map[string]any{
			"todos": []any{
				map[string]any{"id": "1", "content": "First step", "status": "completed"},
				map[string]any{"id": "2", "content": "Second step", "status": "in_progress"},
				map[string]any{"id": "3", "content": "Third step", "status": "pending"},
			},
		},
		Output: map[string]any{"output": "Todos updated.", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "- [x] First step") {
		t.Errorf("completed item missing:\n%s", md)
	}
	if !strings.Contains(md, "- [⚡] Second step") {
		t.Errorf("in-progress item missing:\n%s", md)
	}
	if !strings.Contains(md, "- [ ] Third step") {
		t.Errorf("pending item missing:\n%s", md)
	}
	if strings.Contains(md, "Todos updated.") {
		t.Errorf("the checklist is the whole story, so the result should be suppressed:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_TodoMergeUpdate(t *testing.T) {
	// Grok sends incremental updates carrying only an id and a status. The
	// parser backfills the text, but an item it never saw must still render as
	// something a reader can identify.
	tool := &ToolInfo{
		Name: "todo_write",
		Type: "task",
		Input: map[string]any{
			"merge": true,
			"todos": []any{
				map[string]any{"id": "3", "content": "Run the tests", "status": "completed"},
				map[string]any{"id": "4", "status": "in_progress"},
			},
		},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Todo update:") {
		t.Errorf("a merge update should be labelled as an update:\n%s", md)
	}
	if !strings.Contains(md, "- [x] Run the tests") {
		t.Errorf("backfilled item text missing:\n%s", md)
	}
	if !strings.Contains(md, "- [⚡] (item 4)") {
		t.Errorf("an unknown item should be named by id, not left blank:\n%s", md)
	}
}

func TestBackfillTodoText(t *testing.T) {
	seen := map[string]string{}

	first := map[string]any{"todos": []any{
		map[string]any{"id": "1", "content": "First step", "status": "in_progress"},
		map[string]any{"id": "2", "content": "Second step", "status": "pending"},
	}}
	backfillTodoText(first, seen)

	update := map[string]any{"merge": true, "todos": []any{
		map[string]any{"id": "1", "status": "completed"},
		map[string]any{"id": "2", "status": "in_progress"},
	}}
	backfillTodoText(update, seen)

	todos := update["todos"].([]any)
	if got := todos[0].(map[string]any)["content"]; got != "First step" {
		t.Errorf("item 1 content = %v, want the text from the first call", got)
	}
	if got := todos[1].(map[string]any)["content"]; got != "Second step" {
		t.Errorf("item 2 content = %v, want the text from the first call", got)
	}
}

func TestFormatToolAsMarkdown_ListDirUsesTargetDirectory(t *testing.T) {
	// Grok names this argument target_directory, not path.
	tool := &ToolInfo{
		Name:   "list_dir",
		Type:   "shell",
		Input:  map[string]any{"target_directory": "src"},
		Output: map[string]any{"output": "- src/\n  - calc.py", "status": "success"},
	}

	formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`src`") {
		t.Errorf("summary should carry the listed directory: %v", tool.Summary)
	}
}

func TestFormatToolAsMarkdown_UseToolUnwrapsMCP(t *testing.T) {
	tool := &ToolInfo{
		Name: "use_tool",
		Type: "generic",
		Input: map[string]any{
			"tool_name":  "voice__list_voices",
			"tool_input": map[string]any{"limit": float64(3)},
		},
		Output: map[string]any{"output": "voices: alpha, bravo", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	// Grok dispatches every MCP tool through use_tool, so the inner name is the
	// only thing that tells a reader what actually ran.
	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`voice__list_voices`") {
		t.Errorf("summary should name the inner MCP tool: %v", tool.Summary)
	}
	if !strings.Contains(md, "MCP tool: `voice__list_voices`") {
		t.Errorf("body should name the inner tool:\n%s", md)
	}
	if !strings.Contains(md, "\"limit\": 3") {
		t.Errorf("inner tool input missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_SubagentShowsPrompt(t *testing.T) {
	tool := &ToolInfo{
		Name: "spawn_subagent",
		Type: "task",
		Input: map[string]any{
			"description":   "Count files",
			"prompt":        "Count the files in /tmp/project and report the total.",
			"subagent_type": "general-purpose",
		},
		Output: map[string]any{"output": "There are 3 files.", "status": "success", "subagentStatus": "completed"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "Count files") {
		t.Errorf("summary should carry the description: %v", tool.Summary)
	}
	if !strings.Contains(md, "Subagent: `general-purpose`") {
		t.Errorf("subagent type missing:\n%s", md)
	}
	// The subagent's own transcript is a session we deliberately skip, so this
	// prompt is the only record of what it was asked to do.
	if !strings.Contains(md, "Count the files in /tmp/project") {
		t.Errorf("subagent prompt missing:\n%s", md)
	}
	if !strings.Contains(md, "Result: There are 3 files.") {
		t.Errorf("subagent result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_WebAndSearchSummaries(t *testing.T) {
	tests := []struct {
		name string
		tool *ToolInfo
		want string
	}{
		{
			name: "web_search",
			tool: &ToolInfo{Name: "web_search", Type: "search", Input: map[string]any{"query": "grok release"}},
			want: "`grok release`",
		},
		{
			name: "open_page",
			tool: &ToolInfo{Name: "open_page", Type: "read", Input: map[string]any{"url": "https://x.ai/news"}},
			want: "`https://x.ai/news`",
		},
		{
			name: "grep",
			tool: &ToolInfo{Name: "grep", Type: "search", Input: map[string]any{"pattern": "func main", "path": "/src"}},
			want: "`func main` in `/src`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatToolAsMarkdown(tt.tool)
			if tt.tool.Summary == nil || !strings.Contains(*tt.tool.Summary, tt.want) {
				t.Errorf("summary = %v, want it to contain %q", tt.tool.Summary, tt.want)
			}
		})
	}
}

func TestFormatToolAsMarkdown_UnknownToolShowsJSON(t *testing.T) {
	tool := &ToolInfo{
		Name:   "scheduler_create",
		Type:   "generic",
		Input:  map[string]any{"cron": "0 9 * * *", "task": "daily report"},
		Output: map[string]any{"output": "created", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "```json") || !strings.Contains(md, `"cron": "0 9 * * *"`) {
		t.Errorf("unrecognized tool should show its input as JSON:\n%s", md)
	}
	if !strings.Contains(md, "Result: created") {
		t.Errorf("result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_TruncatesRunawayOutput(t *testing.T) {
	huge := strings.Repeat("x", maxToolBodyRunes+500)
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"target_file": "/big.txt"},
		Output: map[string]any{"output": huge, "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "(truncated)") {
		t.Error("runaway output should be truncated")
	}
	if len([]rune(md)) > maxToolBodyRunes+200 {
		t.Errorf("truncated output is still too long: %d runes", len([]rune(md)))
	}
}

func TestFormatToolAsMarkdown_Nil(t *testing.T) {
	if got := formatToolAsMarkdown(nil); got != "" {
		t.Errorf("nil tool should render empty, got %q", got)
	}
}
