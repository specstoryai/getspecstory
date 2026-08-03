package qwencode

import (
	"strings"
	"testing"
)

func TestFormatToolAsMarkdownShell(t *testing.T) {
	tool := &ToolInfo{
		Name: "run_shell_command",
		Type: "shell",
		Input: map[string]interface{}{
			"command":   "ls -la",
			"directory": "/tmp/project",
		},
		Output: map[string]interface{}{
			"output": "file1\nfile2",
		},
	}

	md := formatToolAsMarkdown(tool)
	if !strings.Contains(md, "```bash\nls -la\n```") {
		t.Errorf("markdown missing command fence:\n%s", md)
	}
	if !strings.Contains(md, "Directory: `/tmp/project`") {
		t.Errorf("markdown missing directory:\n%s", md)
	}
	if !strings.Contains(md, "Result:\n```text\nfile1\nfile2\n```") {
		t.Errorf("markdown missing multi-line result:\n%s", md)
	}
}

func TestFormatToolAsMarkdownReadFile(t *testing.T) {
	tool := &ToolInfo{
		Name: "read_file",
		Type: "read",
		Input: map[string]interface{}{
			"file_path": "/tmp/project/main.go",
		},
		Output: map[string]interface{}{
			"output": "package main",
		},
	}

	md := formatToolAsMarkdown(tool)
	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`/tmp/project/main.go`") {
		t.Errorf("summary missing file path: %+v", tool.Summary)
	}
	if !strings.Contains(md, "```go\npackage main\n```") {
		t.Errorf("markdown missing highlighted content:\n%s", md)
	}
}

func TestFormatToolAsMarkdownEdit(t *testing.T) {
	tool := &ToolInfo{
		Name: "edit",
		Type: "write",
		Input: map[string]interface{}{
			"file_path":  "/tmp/project/main.go",
			"new_string": "fmt.Println(\"hello\")",
		},
	}

	md := formatToolAsMarkdown(tool)
	if !strings.Contains(md, "Path: `/tmp/project/main.go`") {
		t.Errorf("markdown missing path:\n%s", md)
	}
	if !strings.Contains(md, "```diff") {
		t.Errorf("markdown missing diff fence:\n%s", md)
	}
}

func TestFormatToolAsMarkdownTodoWrite(t *testing.T) {
	tool := &ToolInfo{
		Name: "todo_write",
		Type: "task",
		Input: map[string]interface{}{
			"todos": []interface{}{
				map[string]interface{}{"content": "do the thing", "status": "in_progress"},
				map[string]interface{}{"content": "done thing", "status": "completed"},
			},
		},
	}

	md := formatToolAsMarkdown(tool)
	if !strings.Contains(md, "- [⚡] do the thing") {
		t.Errorf("markdown missing in-progress todo:\n%s", md)
	}
	if !strings.Contains(md, "- [x] done thing") {
		t.Errorf("markdown missing completed todo:\n%s", md)
	}
}

func TestFormatToolAsMarkdownGeneric(t *testing.T) {
	tool := &ToolInfo{
		Name: "brand_new_tool",
		Type: "unknown",
		Input: map[string]interface{}{
			"foo": "bar",
		},
		Output: map[string]interface{}{
			"output": "ok",
		},
	}

	md := formatToolAsMarkdown(tool)
	if !strings.Contains(md, "```json") {
		t.Errorf("markdown missing generic json body:\n%s", md)
	}
	if !strings.Contains(md, "Result: ok") {
		t.Errorf("markdown missing single-line result:\n%s", md)
	}
}

func TestFormatToolAsMarkdownNil(t *testing.T) {
	if got := formatToolAsMarkdown(nil); got != "" {
		t.Errorf("formatToolAsMarkdown(nil) = %q, want empty", got)
	}
}
