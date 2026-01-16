package geminicli

import (
	"strings"
	"testing"
)

func TestFormatToolAsMarkdownShell(t *testing.T) {
	tool := &ToolInfo{
		Name:  "run_shell_command",
		Type:  "shell",
		UseID: "tool-1",
		Input: map[string]interface{}{"command": "ls -la"},
		Output: map[string]interface{}{
			"output": "ok",
		},
	}

	out := formatToolAsMarkdown(tool)

	// Should NOT have wrapper tags (those are added by pkg/markdown)
	if strings.Contains(out, "<tool-use") {
		t.Fatalf("should not contain <tool-use> wrapper tags: %s", out)
	}

	// Should have bash code fence with command
	if !strings.Contains(out, "```bash") {
		t.Fatalf("expected bash code fence: %s", out)
	}
	if !strings.Contains(out, "ls -la") {
		t.Fatalf("expected command in output: %s", out)
	}

	// Should have result
	if !strings.Contains(out, "Result:") {
		t.Fatalf("expected Result: prefix: %s", out)
	}
}

func TestFormatToolAsMarkdownWriteFile(t *testing.T) {
	tool := &ToolInfo{
		Name:  "write_file",
		Type:  "write",
		UseID: "tool-1",
		Input: map[string]interface{}{
			"file_path": "main.go",
			"content":   "package main",
		},
	}

	out := formatToolAsMarkdown(tool)
	if !strings.Contains(out, "Path: `main.go`") {
		t.Fatalf("expected path in output: %s", out)
	}
	if !strings.Contains(out, "```go") {
		t.Fatalf("expected go code fence: %s", out)
	}
}

func TestFormatToolAsMarkdownReadFile(t *testing.T) {
	tool := &ToolInfo{
		Name:  "read_file",
		Type:  "read",
		UseID: "tool-1",
		Input: map[string]interface{}{"file_path": "hello_world.py"},
		Output: map[string]interface{}{
			"output": "def hello_world():\n    return \"Hello, world!\"\n\nif __name__ == \"__main__\":\n    print(hello_world())",
		},
	}

	out := formatToolAsMarkdown(tool)

	// Should have custom summary set on tool (file path appended)
	if tool.Summary == nil {
		t.Errorf("expected tool.Summary to be set")
	} else if !strings.Contains(*tool.Summary, "`hello_world.py`") {
		t.Errorf("expected file path in summary, got: %s", *tool.Summary)
	}

	// Should NOT have JSON args
	if strings.Contains(out, "```json") {
		t.Errorf("should not contain JSON args: %s", out)
	}

	// Should NOT have "Result:" prefix for read_file
	if strings.Contains(out, "Result:") {
		t.Errorf("should not contain 'Result:' prefix: %s", out)
	}

	// Should have Python code fence with content
	if !strings.Contains(out, "```py") {
		t.Errorf("expected Python code fence, got: %s", out)
	}

	if !strings.Contains(out, "def hello_world()") {
		t.Errorf("expected file content in output: %s", out)
	}
}

func TestFormatToolAsMarkdownSearchFileContent(t *testing.T) {
	tool := &ToolInfo{
		Name:  "search_file_content",
		Type:  "search",
		UseID: "tool-1",
		Input: map[string]interface{}{
			"pattern": "hello",
			"include": "*.py",
		},
		Output: map[string]interface{}{
			"output": "Found 3 matches for pattern \"hello\" in the workspace directory (filter: \"*.py\"):\n---\nFile: hello_world.py\nL1: def hello_world():\nL2: return \"Hello, world!\"\nL5: print(hello_world())\n---",
		},
	}

	out := formatToolAsMarkdown(tool)

	// Should have custom summary with pattern and include
	if tool.Summary == nil {
		t.Errorf("expected tool.Summary to be set")
	} else if !strings.Contains(*tool.Summary, "`hello`") || !strings.Contains(*tool.Summary, "`*.py`") {
		t.Errorf("expected pattern and include in summary, got: %s", *tool.Summary)
	}

	// Should NOT have JSON args
	if strings.Contains(out, "```json") {
		t.Errorf("should not contain JSON args: %s", out)
	}

	// Should NOT have "Result:" prefix
	if strings.Contains(out, "Result:") {
		t.Errorf("should not contain 'Result:' prefix: %s", out)
	}

	// Should have the actual match output
	if !strings.Contains(out, "Found 3 matches") {
		t.Errorf("expected match output: %s", out)
	}

	if !strings.Contains(out, "File: hello_world.py") {
		t.Errorf("expected file details in output: %s", out)
	}
}

func TestFormatToolAsMarkdownGlob(t *testing.T) {
	tool := &ToolInfo{
		Name:  "glob",
		Type:  "search",
		UseID: "tool-1",
		Input: map[string]interface{}{"pattern": "*.py"},
		Output: map[string]interface{}{
			"output": "Found 1 file(s) matching \"*.py\" within /Users/sean/Source/SpecStory/compositions/cross-agent-3, sorted by modification time (newest first):\n/Users/sean/Source/SpecStory/compositions/cross-agent-3/hello_world.py",
		},
	}

	out := formatToolAsMarkdown(tool)

	// Should have custom summary with pattern
	if tool.Summary == nil {
		t.Errorf("expected tool.Summary to be set")
	} else if !strings.Contains(*tool.Summary, "`*.py`") {
		t.Errorf("expected pattern in summary, got: %s", *tool.Summary)
	}

	// Should NOT have JSON args
	if strings.Contains(out, "```json") {
		t.Errorf("should not contain JSON args: %s", out)
	}

	// Should NOT have "Result:" prefix
	if strings.Contains(out, "Result:") {
		t.Errorf("should not contain 'Result:' prefix: %s", out)
	}

	// Should have the actual file list output
	if !strings.Contains(out, "Found 1 file(s)") {
		t.Errorf("expected file count output: %s", out)
	}

	if !strings.Contains(out, "hello_world.py") {
		t.Errorf("expected file path in output: %s", out)
	}
}

func TestFormatToolAsMarkdownListDirectory(t *testing.T) {
	tool := &ToolInfo{
		Name:  "list_directory",
		Type:  "shell",
		UseID: "tool-1",
		Input: map[string]interface{}{"dir_path": "."},
		Output: map[string]interface{}{
			"output": "Directory listing for /Users/sean/Source/SpecStory/compositions/cross-agent-3:\n[DIR] .specstory\n[DIR] cursor-prolog\n.cursorindexingignore\nhello_world.py",
		},
	}

	out := formatToolAsMarkdown(tool)

	// Should have custom summary with directory path
	if tool.Summary == nil {
		t.Errorf("expected tool.Summary to be set")
	} else if !strings.Contains(*tool.Summary, "`.`") {
		t.Errorf("expected directory path in summary, got: %s", *tool.Summary)
	}

	// Should NOT have "Result:" prefix
	if strings.Contains(out, "Result:") {
		t.Errorf("should not contain 'Result:' prefix: %s", out)
	}

	// Should have the actual directory listing
	if !strings.Contains(out, "Directory listing for") {
		t.Errorf("expected directory listing output: %s", out)
	}

	if !strings.Contains(out, "[DIR] .specstory") {
		t.Errorf("expected directory entries in output: %s", out)
	}
}

func TestFormatToolAsMarkdownWebFetch(t *testing.T) {
	tool := &ToolInfo{
		Name:  "web_fetch",
		Type:  "read",
		UseID: "tool-1",
		Input: map[string]interface{}{"prompt": "Fetch the content of https://www.example.com"},
		Output: map[string]interface{}{
			"output": "The content of https://www.example.com is: \"Example Domain Example Domain This domain is for use in documentation examples without needing permission. Avoid use in operations. Learn more\"[1].\n\nSources:\n[1] Example Domain (https://www.example.com)",
		},
	}

	out := formatToolAsMarkdown(tool)

	// Should have the prompt in the body (without "Prompt:" or "Query:" prefix)
	if !strings.Contains(out, "Fetch the content of https://www.example.com") {
		t.Errorf("expected prompt in body, got: %s", out)
	}

	// Should NOT have "Prompt:" or "Query:" prefix
	if strings.Contains(out, "Prompt:") || strings.Contains(out, "Query:") {
		t.Errorf("should not contain 'Prompt:' or 'Query:' prefix: %s", out)
	}

	// Should have the actual fetched content
	if !strings.Contains(out, "Example Domain") {
		t.Errorf("expected fetched content in output: %s", out)
	}

	// Should have Result: prefix for this tool
	if !strings.Contains(out, "Result:") {
		t.Errorf("expected 'Result:' prefix for web_fetch: %s", out)
	}
}
