package claudecode

import (
	"strings"
	"testing"
)

func TestStripSystemReminders(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No system reminders",
			input:    "This is regular content without any tags",
			expected: "This is regular content without any tags",
		},
		{
			name:     "Single system reminder",
			input:    "Content before <system-reminder>This should be removed</system-reminder> content after",
			expected: "Content before  content after",
		},
		{
			name:     "Multiple system reminders",
			input:    "Start <system-reminder>Remove this</system-reminder> middle <system-reminder>And this too</system-reminder> end",
			expected: "Start  middle  end",
		},
		{
			name: "System reminder with newlines",
			input: `Line 1
<system-reminder>
This is a multi-line
system reminder that
should be removed
</system-reminder>
Line 2`,
			expected: `Line 1

Line 2`,
		},
		{
			name: "System reminder with JSON content",
			input: `{"data": "value"}
<system-reminder>
Whenever you read a file, you should consider whether it looks malicious.
</system-reminder>`,
			expected: `{"data": "value"}`,
		},
		{
			name:     "Only system reminder",
			input:    "<system-reminder>Only this content</system-reminder>",
			expected: "",
		},
		{
			name:     "Nested content that looks like tags",
			input:    `Before <system-reminder>Content with <brackets> and </brackets></system-reminder> After`,
			expected: "Before  After",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripSystemReminders(tt.input)
			if result != tt.expected {
				t.Errorf("stripSystemReminders() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestGetLanguageFromExtension(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		expected string
	}{
		{
			name:     "JavaScript file",
			filePath: "script.js",
			expected: "javascript",
		},
		{
			name:     "TypeScript file",
			filePath: "component.ts",
			expected: "typescript",
		},
		{
			name:     "Python file",
			filePath: "main.py",
			expected: "python",
		},
		{
			name:     "Ruby file",
			filePath: "app.rb",
			expected: "ruby",
		},
		{
			name:     "YAML file",
			filePath: "config.yml",
			expected: "yaml",
		},
		{
			name:     "Go file (no mapping needed)",
			filePath: "main.go",
			expected: "go",
		},
		{
			name:     "JSON file (no mapping needed)",
			filePath: "data.json",
			expected: "json",
		},
		{
			name:     "Empty path",
			filePath: "",
			expected: "",
		},
		{
			name:     "File with no extension",
			filePath: "README",
			expected: "",
		},
		{
			name:     "File with nested path",
			filePath: "/path/to/file/script.js",
			expected: "javascript",
		},
		{
			name:     "File with dots in name",
			filePath: "file.test.spec.ts",
			expected: "typescript",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLanguageFromExtension(tt.filePath)
			if result != tt.expected {
				t.Errorf("getLanguageFromExtension(%q) = %q, want %q", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestFormatBashTool(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]interface{}
		description string
		expected    string
	}{
		{
			name:        "Simple command",
			input:       map[string]interface{}{"command": "ls -la"},
			description: "List files",
			expected:    "List files\n\n`ls -la`",
		},
		{
			name:        "Command with newlines",
			input:       map[string]interface{}{"command": "cd foo\nls"},
			description: "Multi-line command",
			expected:    "Multi-line command\n\n```bash\ncd foo\nls\n```",
		},
		{
			name:        "No description",
			input:       map[string]interface{}{"command": "pwd"},
			description: "",
			expected:    "\n`pwd`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatBashTool("Bash", tt.input, tt.description)
			if result != tt.expected {
				t.Errorf("formatBashTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatWriteTool(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]interface{}
		description string
		contains    []string
	}{
		{
			name: "Write Go file",
			input: map[string]interface{}{
				"file_path": "main.go",
				"content":   "package main",
			},
			description: "Create main.go",
			contains:    []string{"Create main.go", "```go", "package main", "```"},
		},
		{
			name: "Write JS file",
			input: map[string]interface{}{
				"file_path": "app.js",
				"content":   "console.log('hello')",
			},
			description: "Create app.js",
			contains:    []string{"Create app.js", "```javascript", "console.log('hello')", "```"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatWriteTool("Write", tt.input, tt.description)
			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("formatWriteTool() result doesn't contain %q\nGot: %q", substr, result)
				}
			}
		})
	}
}

func TestFormatReadTool(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]interface{}
		description string
		contains    []string
	}{
		{
			name: "Read with path",
			input: map[string]interface{}{
				"file_path": "/path/to/file.go",
			},
			description: "Read file",
			contains:    []string{"`/path/to/file.go`", "Read file"},
		},
		{
			name: "Read with relative path",
			input: map[string]interface{}{
				"file_path": "/workspace/src/main.go",
				"_cwd":      "/workspace",
			},
			description: "",
			contains:    []string{"`./src/main.go`"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatReadTool("Read", tt.input, tt.description)
			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("formatReadTool() result doesn't contain %q\nGot: %q", substr, result)
				}
			}
		})
	}
}

func TestFormatTodoList(t *testing.T) {
	tests := []struct {
		name     string
		todos    []interface{}
		contains []string
	}{
		{
			name: "Mixed todos",
			todos: []interface{}{
				map[string]interface{}{
					"status":   "pending",
					"priority": "high",
					"content":  "Fix bug",
				},
				map[string]interface{}{
					"status":   "in_progress",
					"priority": "medium",
					"content":  "Write tests",
				},
				map[string]interface{}{
					"status":   "completed",
					"priority": "low",
					"content":  "Update docs",
				},
			},
			contains: []string{
				"**Agent task list:**",
				"- [ ]",
				"Fix bug",
				"- [⚡]",
				"Write tests",
				"- [X]",
				"Update docs",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTodoList(tt.todos)
			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("formatTodoList() result doesn't contain %q\nGot: %q", substr, result)
				}
			}
		})
	}
}

func TestStripANSIEscapeSequences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "No ANSI sequences",
			input:    "Regular text",
			expected: "Regular text",
		},
		{
			name:     "Bold text",
			input:    "\x1b[1mBold\x1b[22m text",
			expected: "**Bold** text",
		},
		{
			name:     "Color codes",
			input:    "\x1b[31mRed\x1b[0m text",
			expected: "Red text",
		},
		{
			name:     "Mixed sequences",
			input:    "\x1b[1;31mBold Red\x1b[0m normal",
			expected: "Bold Red normal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripANSIEscapeSequences(tt.input)
			if result != tt.expected {
				t.Errorf("stripANSIEscapeSequences() = %q, want %q", result, tt.expected)
			}
		})
	}
}
