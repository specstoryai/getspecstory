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

func TestFormatAskUserQuestionTool(t *testing.T) {
	tests := []struct {
		name        string
		input       map[string]interface{}
		description string
		contains    []string
		notContains []string
	}{
		{
			name: "Single select question with options",
			input: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":    "What type of project would you like to work on today?",
						"header":      "Project type",
						"multiSelect": false,
						"options": []interface{}{
							map[string]interface{}{
								"label":       "Web application",
								"description": "Build or modify a frontend web app",
							},
							map[string]interface{}{
								"label":       "CLI tool",
								"description": "Create a command-line interface tool",
							},
						},
					},
				},
			},
			description: "",
			contains: []string{
				"**What type of project would you like to work on today?**",
				"Options:",
				"- **Web application** - Build or modify a frontend web app",
				"- **CLI tool** - Create a command-line interface tool",
			},
			notContains: []string{
				"select multiple",
			},
		},
		{
			name: "Multi-select question",
			input: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":    "Which features would you like?",
						"header":      "Features",
						"multiSelect": true,
						"options": []interface{}{
							map[string]interface{}{
								"label":       "Authentication",
								"description": "User login and sessions",
							},
							map[string]interface{}{
								"label":       "Database",
								"description": "Database integration",
							},
						},
					},
				},
			},
			description: "",
			contains: []string{
				"**Which features would you like?** _(select multiple)_",
				"- **Authentication** - User login and sessions",
				"- **Database** - Database integration",
			},
			notContains: []string{},
		},
		{
			name: "Option without description",
			input: map[string]interface{}{
				"questions": []interface{}{
					map[string]interface{}{
						"question":    "Pick a color",
						"multiSelect": false,
						"options": []interface{}{
							map[string]interface{}{
								"label": "Red",
							},
							map[string]interface{}{
								"label": "Blue",
							},
						},
					},
				},
			},
			description: "",
			contains: []string{
				"**Pick a color**",
				"- **Red**",
				"- **Blue**",
			},
			notContains: []string{},
		},
		{
			name:        "Nil input returns empty string",
			input:       nil,
			description: "",
			contains:    []string{},
			notContains: []string{},
		},
		{
			name:        "Empty questions array returns empty string",
			input:       map[string]interface{}{"questions": []interface{}{}},
			description: "",
			contains:    []string{},
			notContains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatAskUserQuestionTool("AskUserQuestion", tt.input, tt.description)
			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("formatAskUserQuestionTool() result doesn't contain %q\nGot: %q", substr, result)
				}
			}
			for _, substr := range tt.notContains {
				if strings.Contains(result, substr) {
					t.Errorf("formatAskUserQuestionTool() result should not contain %q\nGot: %q", substr, result)
				}
			}
		})
	}
}

func TestParseAskUserQuestionAnswer(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Single answer",
			input:    `User has answered your questions: "What type of project would you like to work on today?"="API/Backend". You can now continue with the user's answers in mind.`,
			expected: "API/Backend",
		},
		{
			name:     "Multi-select answer",
			input:    `User has answered your questions: "Which features would you like to include in your API/Backend project?"="REST endpoints, Testing, And my own thing". You can now continue with the user's answers in mind.`,
			expected: "REST endpoints, Testing, And my own thing",
		},
		{
			name:     "No colon found",
			input:    "Some random text without the expected format",
			expected: "",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Truncated content without continuation sentence",
			input:    `User has answered your questions: "Pick a color"="Blue"`,
			expected: "Blue",
		},
		{
			name:     "Answer containing equals sign",
			input:    `User has answered your questions: "When configuring environment variables, which format do you prefer?"="KEY=value pairs". You can now continue with the user's answers in mind.`,
			expected: "KEY=value pairs",
		},
		{
			name:     "Answer containing commas",
			input:    `User has answered your questions: "Which format do you prefer?"="YAML, TOML, or similar". You can now continue with the user's answers in mind.`,
			expected: "YAML, TOML, or similar",
		},
		{
			name:     "Multi-select with equals and commas in answer",
			input:    `User has answered your questions: "Which formats do you prefer?"="KEY=value pairs, YAML, TOML, or similar, Also this". You can now continue with the user's answers in mind.`,
			expected: "KEY=value pairs, YAML, TOML, or similar, Also this",
		},
		{
			name:     "Multiple questions returns all answers",
			input:    `User has answered your questions: "Question 1"="Answer 1", "Question 2"="Answer 2". You can now continue with the user's answers in mind.`,
			expected: "Answer 1, Answer 2",
		},
		{
			name:     "Answer containing single quotes",
			input:    `User has answered your questions: "Which style?"="console.log('single')". You can now continue with the user's answers in mind.`,
			expected: "console.log('single')",
		},
		{
			name:     "Answer containing backticks",
			input:    "User has answered your questions: \"Which style?\"=\"Template `literals`\". You can now continue with the user's answers in mind.",
			expected: "Template `literals`",
		},
		{
			name:     "Answer containing single quotes and backticks together",
			input:    "User has answered your questions: \"Which style?\"=\"console.log('hello'), Template `literals`, It's working!\". You can now continue with the user's answers in mind.",
			expected: "console.log('hello'), Template `literals`, It's working!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAskUserQuestionAnswer(tt.input)
			if result != tt.expected {
				t.Errorf("parseAskUserQuestionAnswer() = %q, want %q", result, tt.expected)
			}
		})
	}
}
