package musecode

import (
	"strings"
	"testing"
)

// toolMarkdown renders a tool and returns the markdown plus the summary the
// renderer chose to set.
func toolMarkdown(tool *ToolInfo) (markdown, summary string) {
	markdown = formatToolAsMarkdown(tool)
	if tool.Summary != nil {
		summary = *tool.Summary
	}
	return markdown, summary
}

func TestFormatToolAsMarkdown_Summaries(t *testing.T) {
	tests := []struct {
		name     string
		tool     *ToolInfo
		expected string
	}{
		{
			name:     "read_file uses path",
			tool:     &ToolInfo{Name: "read_file", Input: map[string]any{"path": "notes.txt"}},
			expected: "Tool use: **read_file** `notes.txt`",
		},
		{
			name:     "write_file uses path",
			tool:     &ToolInfo{Name: "write_file", Input: map[string]any{"path": "SUMMARY.md"}},
			expected: "Tool use: **write_file** `SUMMARY.md`",
		},
		{
			name:     "edit_file uses path",
			tool:     &ToolInfo{Name: "edit_file", Input: map[string]any{"path": "src/calc.py"}},
			expected: "Tool use: **edit_file** `src/calc.py`",
		},
		{
			name:     "search uses pattern",
			tool:     &ToolInfo{Name: "search", Input: map[string]any{"pattern": "beta", "mode": "regex"}},
			expected: "Tool use: **search** `beta`",
		},
		{
			name:     "web_search uses query",
			tool:     &ToolInfo{Name: "web_search", Input: map[string]any{"query": "Muse Spark model"}},
			expected: "Tool use: **web_search** `Muse Spark model`",
		},
		{
			name:     "bash gets no custom summary",
			tool:     &ToolInfo{Name: "bash", Input: map[string]any{"command": "ls"}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, summary := toolMarkdown(tt.tool)
			if summary != tt.expected {
				t.Errorf("summary = %q, want %q", summary, tt.expected)
			}
		})
	}
}

func TestFormatToolAsMarkdown_BashRendersParsedOutput(t *testing.T) {
	tool := &ToolInfo{
		Name: "bash",
		Type: "shell",
		Input: map[string]any{
			"command":     `python3 -c "print(42)"`,
			"description": "Print the answer",
		},
		Output: map[string]any{"output": `{"chunk_id":"exec-1-1","command":"python3","exit_code":0,"output":"42\n","truncated":false}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Print the answer") {
		t.Errorf("description missing from body:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```bash\npython3 -c \"print(42)\"\n```") {
		t.Errorf("command not rendered in a bash fence:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```text\n42\n```") {
		t.Errorf("parsed output not rendered:\n%s", markdown)
	}
	// The JSON envelope is machine bookkeeping; it must not reach the reader.
	if strings.Contains(markdown, "chunk_id") {
		t.Errorf("raw result JSON leaked into the markdown:\n%s", markdown)
	}
	if strings.Contains(markdown, "Exit code") {
		t.Errorf("exit code shown for a successful command:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_BashShowsNonZeroExitCode(t *testing.T) {
	tool := &ToolInfo{
		Name:   "bash",
		Type:   "shell",
		Input:  map[string]any{"command": "exit 5", "description": "Run a failing command"},
		Output: map[string]any{"output": `{"command":"exit 5","exit_code":5,"terminal_status":"failed","output":"boom\n"}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Exit code: 5") {
		t.Errorf("exit code missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```text\nboom\n```") {
		t.Errorf("command output missing:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_BashFallsBackToRawOnUnparseableResult(t *testing.T) {
	tests := []struct {
		name   string
		output map[string]any
		expect string
	}{
		{
			name:   "not JSON",
			output: map[string]any{"output": "plain text result\nsecond line"},
			expect: "plain text result",
		},
		{
			name:   "JSON without an output field",
			output: map[string]any{"output": `{"execution_state":"background_running","session_id":3}`},
			expect: "background_running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolInfo{Name: "bash", Input: map[string]any{"command": "sleep 1"}, Output: tt.output}
			markdown, _ := toolMarkdown(tool)
			if !strings.Contains(markdown, tt.expect) {
				t.Errorf("raw result not preserved (want %q):\n%s", tt.expect, markdown)
			}
		})
	}
}

func TestFormatToolAsMarkdown_EditRendersDiffFence(t *testing.T) {
	tool := &ToolInfo{
		Name: "edit_file",
		Type: "write",
		Input: map[string]any{
			"path":    "src/calc.py",
			"find":    "def add(a, b):\n    return a + b",
			"replace": "def add(a, b):\n    return a + b\n\ndef sub(a, b):\n    return a - b",
		},
		Output: map[string]any{"output": "edited\nchanged lines: lines 1-2"},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Path: `src/calc.py`") {
		t.Errorf("path missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```diff\n") {
		t.Errorf("diff fence missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "-def add(a, b):\n-    return a + b\n") {
		t.Errorf("find lines not rendered as removals:\n%s", markdown)
	}
	if !strings.Contains(markdown, "+def sub(a, b):\n+    return a - b") {
		t.Errorf("replace lines not rendered as additions:\n%s", markdown)
	}
	// The body already shows the change; repeating the result adds noise.
	if strings.Contains(markdown, "changed lines") {
		t.Errorf("edit result duplicated below the diff:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_TodosChecklist(t *testing.T) {
	tool := &ToolInfo{
		Name: "write_todos",
		Type: "task",
		Input: map[string]any{"todos": []any{
			map[string]any{"text": "Run the tests", "status": "completed"},
			map[string]any{"text": "Add sub()", "status": "in_progress"},
			map[string]any{"text": "Write SUMMARY.md", "status": "pending"},
			// Qwen's key must not be read: Muse items carry "text".
			map[string]any{"content": "ignored", "status": "pending"},
		}},
		Output: map[string]any{"output": `{"ok":true,"revision":1,"items":3}`},
	}

	markdown, _ := toolMarkdown(tool)

	for _, want := range []string{
		"- [x] Run the tests",
		"- [⚡] Add sub()",
		"- [ ] Write SUMMARY.md",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("checklist missing %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "ignored") {
		t.Errorf("item text read from the wrong key:\n%s", markdown)
	}
	// The checklist is the record of the call; the JSON ack adds nothing.
	if strings.Contains(markdown, "revision") {
		t.Errorf("todo result duplicated below the checklist:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_ErrorTakesPriority(t *testing.T) {
	tests := []struct {
		name string
		tool *ToolInfo
	}{
		{
			name: "read_file failure",
			tool: &ToolInfo{
				Name:   "read_file",
				Input:  map[string]any{"path": "missing.txt"},
				Output: map[string]any{"error": "tool failed: No such file or directory (os error 2)"},
			},
		},
		{
			name: "write_todos failure still renders",
			tool: &ToolInfo{
				Name:   "write_todos",
				Input:  map[string]any{"todos": []any{map[string]any{"text": "x", "status": "pending"}}},
				Output: map[string]any{"error": "tool failed: budget exceeded"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown, _ := toolMarkdown(tt.tool)
			if !strings.Contains(markdown, "Result: tool failed") {
				t.Errorf("failure not rendered as the result:\n%s", markdown)
			}
		})
	}
}

// A tool result containing its own fence must not be able to close the fence
// wrapping it; spi.CodeFence sizes the wrapper to outrun the longest inner run.
func TestFormatToolAsMarkdown_UsesCodeFenceForEmbeddedBackticks(t *testing.T) {
	tests := []struct {
		name   string
		tool   *ToolInfo
		expect string
	}{
		{
			name: "bash output with a fence",
			tool: &ToolInfo{
				Name:   "bash",
				Input:  map[string]any{"command": "cat README.md"},
				Output: map[string]any{"output": "{\"exit_code\":0,\"output\":\"```go\\nfmt.Println()\\n```\\n\"}"},
			},
			expect: "````text\n",
		},
		{
			name: "write_file content with a fence",
			tool: &ToolInfo{
				Name: "write_file",
				Input: map[string]any{
					"path":    "README.md",
					"content": "intro\n```zsh\nspecstory sync\n```\n",
				},
			},
			expect: "````markdown\n",
		},
		{
			name: "generic result with a long backtick run",
			tool: &ToolInfo{
				Name:   "get_goal",
				Output: map[string]any{"output": "line\n````\nnested\n````"},
			},
			expect: "`````text\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown, _ := toolMarkdown(tt.tool)
			if !strings.Contains(markdown, tt.expect) {
				t.Errorf("fence not widened past the embedded backticks (want %q):\n%s", tt.expect, markdown)
			}
		})
	}
}

func TestFormatToolAsMarkdown_SubagentSpawnBody(t *testing.T) {
	tool := &ToolInfo{
		Name: "subagent_spawn",
		Type: "task",
		Input: map[string]any{
			"role":      "demo-worker",
			"objective": "Write tool-demo/subagent-output.txt",
		},
		Output: map[string]any{"output": `{"status":"accepted","agent_path":"main/subagent-demo/1"}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Role: `demo-worker`") {
		t.Errorf("role missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Write tool-demo/subagent-output.txt") {
		t.Errorf("objective missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Result: ") || !strings.Contains(markdown, "accepted") {
		t.Errorf("spawn result missing:\n%s", markdown)
	}
}

// Search hits are arbitrary file content: markdown or HTML inside a matched
// line must render as text inside a fence, not as markup that can break the
// enclosing tool block.
func TestFormatToolAsMarkdown_SearchResultsFenced(t *testing.T) {
	tool := &ToolInfo{
		Name:   "search",
		Type:   "search",
		Input:  map[string]any{"pattern": "beta"},
		Output: map[string]any{"output": "notes.txt:2:beta\nREADME.md:9:</details> beta"},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "```text\n") {
		t.Errorf("search hits not fenced:\n%s", markdown)
	}
	if !strings.Contains(markdown, "notes.txt:2:beta") || !strings.Contains(markdown, "</details> beta") {
		t.Errorf("search hits missing:\n%s", markdown)
	}
}

func TestInputAsString(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		key      string
		expected string
	}{
		{name: "nil map", input: nil, key: "path", expected: ""},
		{name: "missing key", input: map[string]any{"other": "x"}, key: "path", expected: ""},
		// Muse serialises unset optional args as JSON null; "null" is not a path.
		{name: "json null reads as absent", input: map[string]any{"workdir": nil}, key: "workdir", expected: ""},
		{name: "string value", input: map[string]any{"path": "notes.txt"}, key: "path", expected: "notes.txt"},
		{name: "non-string value marshals", input: map[string]any{"limit": float64(500)}, key: "limit", expected: "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inputAsString(tt.input, tt.key); got != tt.expected {
				t.Errorf("inputAsString() = %q, want %q", got, tt.expected)
			}
		})
	}
}
