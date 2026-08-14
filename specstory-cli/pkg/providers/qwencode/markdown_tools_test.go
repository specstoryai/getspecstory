package qwencode

import (
	"strings"
	"testing"
)

func TestFormatToolAsMarkdown_ReadFile(t *testing.T) {
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"file_path": "/Users/dev/project/main.go"},
		Output: map[string]any{"output": "package main\n\nfunc main() {}"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`/Users/dev/project/main.go`") {
		t.Errorf("read_file summary missing path: %v", tool.Summary)
	}
	if !strings.Contains(md, "```go\npackage main") {
		t.Errorf("read_file output not fenced with language:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ShellPrefersResultDisplay(t *testing.T) {
	tool := &ToolInfo{
		Name: "run_shell_command",
		Type: "shell",
		Input: map[string]any{
			"command":     "ls -la",
			"description": "List files",
		},
		Output: map[string]any{
			"output":        "Command: ls -la\nDirectory: /tmp\nOutput: file.txt\nError: (none)\nExit Code: 0",
			"resultDisplay": "file.txt",
			"status":        "success",
		},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "```bash\nls -la\n```") {
		t.Errorf("shell command not fenced:\n%s", md)
	}
	if !strings.Contains(md, "List files") {
		t.Errorf("shell description missing:\n%s", md)
	}
	// The raw stdout (resultDisplay), not the structured envelope, should be shown
	if !strings.Contains(md, "Result:\n```text\nfile.txt\n```") {
		t.Errorf("shell result should prefer resultDisplay:\n%s", md)
	}
	if strings.Contains(md, "Process Group") {
		t.Errorf("structured envelope leaked into output:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_EditShowsDiff(t *testing.T) {
	tool := &ToolInfo{
		Name: "edit",
		Type: "write",
		Input: map[string]any{
			"file_path":  "/Users/dev/project/calc.py",
			"old_string": "a",
			"new_string": "b",
		},
		Output: map[string]any{
			"output":        "The file has been updated.",
			"resultDisplay": "--- calc.py\n+++ calc.py\n@@ -1 +1 @@\n-a\n+b",
			"status":        "success",
		},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Path: `/Users/dev/project/calc.py`") {
		t.Errorf("edit path missing:\n%s", md)
	}
	if !strings.Contains(md, "```diff") || !strings.Contains(md, "@@ -1 +1 @@") {
		t.Errorf("edit diff missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_EditFallsBackToNewString(t *testing.T) {
	tool := &ToolInfo{
		Name: "edit",
		Type: "write",
		Input: map[string]any{
			"file_path":  "/Users/dev/project/calc.py",
			"new_string": "def div(a, b):",
		},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "def div(a, b):") {
		t.Errorf("edit fallback to new_string missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ErrorTakesPriority(t *testing.T) {
	tool := &ToolInfo{
		Name:  "read_file",
		Type:  "read",
		Input: map[string]any{"file_path": "/missing.txt"},
		Output: map[string]any{
			"error":     "File not found: /missing.txt",
			"status":    "error",
			"errorType": "file_not_found",
		},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Result: File not found: /missing.txt") {
		t.Errorf("error result missing:\n%s", md)
	}
	if strings.Contains(md, "```txt") {
		t.Errorf("error should not be rendered as file content:\n%s", md)
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
		t.Errorf("completed todo missing:\n%s", md)
	}
	if !strings.Contains(md, "- [⚡] Second step") {
		t.Errorf("in-progress todo missing:\n%s", md)
	}
	if !strings.Contains(md, "- [ ] Third step") {
		t.Errorf("pending todo missing:\n%s", md)
	}
	if strings.Contains(md, "Todos updated.") {
		t.Errorf("todo output should be suppressed (checklist is the body):\n%s", md)
	}
}

func TestFormatToolAsMarkdown_WebFetch(t *testing.T) {
	tool := &ToolInfo{
		Name: "web_fetch",
		Type: "read",
		Input: map[string]any{
			"url":    "https://example.com",
			"prompt": "Summarize the page",
		},
		Output: map[string]any{"output": "Example Domain is a placeholder site.", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "URL: https://example.com") {
		t.Errorf("web_fetch url missing:\n%s", md)
	}
	if !strings.Contains(md, "Summarize the page") {
		t.Errorf("web_fetch prompt missing:\n%s", md)
	}
	if !strings.Contains(md, "Result: Example Domain is a placeholder site.") {
		t.Errorf("web_fetch result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_GenericToolShowsJSONInput(t *testing.T) {
	tool := &ToolInfo{
		Name:   "record_artifact",
		Type:   "generic",
		Input:  map[string]any{"title": "Demo", "kind": "file"},
		Output: map[string]any{"output": "Recorded.", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "```json") || !strings.Contains(md, `"title": "Demo"`) {
		t.Errorf("generic tool input not shown as JSON:\n%s", md)
	}
	if !strings.Contains(md, "Result: Recorded.") {
		t.Errorf("generic tool result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_GrepSummary(t *testing.T) {
	tool := &ToolInfo{
		Name:   "grep_search",
		Type:   "search",
		Input:  map[string]any{"pattern": "func main", "path": "/Users/dev/project"},
		Output: map[string]any{"output": "Found 1 match", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`func main` in `/Users/dev/project`") {
		t.Errorf("grep summary = %v", tool.Summary)
	}
	if !strings.Contains(md, "Found 1 match") {
		t.Errorf("grep result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_AgentDelegation(t *testing.T) {
	tool := &ToolInfo{
		Name: "agent",
		Type: "task",
		Input: map[string]any{
			"description":   "Count files in directory",
			"prompt":        "Count the files in /tmp/project and report the total.",
			"subagent_type": "general-purpose",
		},
		Output: map[string]any{"output": "Total count: 3 files.", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "Count files in directory") {
		t.Errorf("agent summary should carry the description: %v", tool.Summary)
	}
	if !strings.Contains(md, "Subagent: `general-purpose`") {
		t.Errorf("agent body missing subagent type:\n%s", md)
	}
	if !strings.Contains(md, "Count the files in /tmp/project") {
		t.Errorf("agent body missing prompt:\n%s", md)
	}
	if !strings.Contains(md, "Result: Total count: 3 files.") {
		t.Errorf("agent result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_MonitorRendersLikeShell(t *testing.T) {
	tool := &ToolInfo{
		Name: "monitor",
		Type: "shell",
		Input: map[string]any{
			"command":     "tail -f build.log",
			"description": "Watch the build",
		},
		Output: map[string]any{"output": "line one\nline two", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "```bash\ntail -f build.log\n```") {
		t.Errorf("monitor command not fenced like shell:\n%s", md)
	}
	if !strings.Contains(md, "Watch the build") {
		t.Errorf("monitor description missing:\n%s", md)
	}
	if strings.Contains(md, "```json") {
		t.Errorf("monitor should not fall back to JSON dump:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_SkillShowsArgs(t *testing.T) {
	tool := &ToolInfo{
		Name:   "skill",
		Type:   "generic",
		Input:  map[string]any{"skill": "review", "args": "check the auth flow"},
		Output: map[string]any{"output": "Skill loaded.", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`review`") {
		t.Errorf("skill summary = %v", tool.Summary)
	}
	if !strings.Contains(md, "Args: check the auth flow") {
		t.Errorf("skill args missing from body:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ToolSearchSummary(t *testing.T) {
	tool := &ToolInfo{
		Name:   "tool_search",
		Type:   "search",
		Input:  map[string]any{"query": "deferred tools", "max_results": 3},
		Output: map[string]any{"output": "Found 2 tools.", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`deferred tools`") {
		t.Errorf("tool_search summary = %v", tool.Summary)
	}
	if strings.Contains(md, "```json") {
		t.Errorf("tool_search should not dump JSON input:\n%s", md)
	}
	if !strings.Contains(md, "Result: Found 2 tools.") {
		t.Errorf("tool_search result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_NilAndEmpty(t *testing.T) {
	if got := formatToolAsMarkdown(nil); got != "" {
		t.Errorf("nil tool should render empty, got %q", got)
	}

	tool := &ToolInfo{Name: "glob", Type: "search", Input: map[string]any{"pattern": "**/*.go"}}
	md := formatToolAsMarkdown(tool)
	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`**/*.go`") {
		t.Errorf("glob summary = %v", tool.Summary)
	}
	if strings.TrimSpace(md) != "" {
		t.Errorf("glob with no output should have empty body, got:\n%s", md)
	}
}
