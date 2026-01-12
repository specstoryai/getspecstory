package cursorcli

import (
	"strings"
	"testing"
)

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
			filePath: "app.ts",
			expected: "typescript",
		},
		{
			name:     "Python file",
			filePath: "main.py",
			expected: "python",
		},
		{
			name:     "Go file",
			filePath: "server.go",
			expected: "go",
		},
		{
			name:     "HTML file",
			filePath: "index.html",
			expected: "html",
		},
		{
			name:     "CSS file",
			filePath: "styles.css",
			expected: "css",
		},
		{
			name:     "JSON file",
			filePath: "config.json",
			expected: "json",
		},
		{
			name:     "YAML file",
			filePath: "config.yml",
			expected: "yaml",
		},
		{
			name:     "Markdown file",
			filePath: "README.md",
			expected: "markdown",
		},
		{
			name:     "JSX file",
			filePath: "component.jsx",
			expected: "javascript",
		},
		{
			name:     "TSX file",
			filePath: "component.tsx",
			expected: "typescript",
		},
		{
			name:     "Ruby file",
			filePath: "script.rb",
			expected: "ruby",
		},
		{
			name:     "C file",
			filePath: "main.c",
			expected: "c",
		},
		{
			name:     "C++ file",
			filePath: "main.cpp",
			expected: "cpp",
		},
		{
			name:     "C# file",
			filePath: "Program.cs",
			expected: "csharp",
		},
		{
			name:     "Rust file",
			filePath: "main.rs",
			expected: "rust",
		},
		{
			name:     "Full path",
			filePath: "/Users/test/project/index.html",
			expected: "html",
		},
		{
			name:     "No extension",
			filePath: "Makefile",
			expected: "",
		},
		{
			name:     "Empty path",
			filePath: "",
			expected: "",
		},
		{
			name:     "Unknown extension",
			filePath: "file.xyz",
			expected: "xyz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLanguageFromExtension(tt.filePath)
			if result != tt.expected {
				t.Errorf("getLanguageFromExtension(%s) = %q, want %q", tt.filePath, result, tt.expected)
			}
		})
	}
}

func TestFormatWriteTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "HTML file with content",
			args: map[string]interface{}{
				"path":     "index.html",
				"contents": "<!DOCTYPE html>\n<html>\n<body>\n<h1>Hello</h1>\n</body>\n</html>",
			},
			expected: "```html\n<!DOCTYPE html>\n<html>\n<body>\n<h1>Hello</h1>\n</body>\n</html>\n```",
		},
		{
			name: "JavaScript file with content",
			args: map[string]interface{}{
				"path":     "script.js",
				"contents": "function hello() {\n  console.log('Hello');\n}",
			},
			expected: "```javascript\nfunction hello() {\n  console.log('Hello');\n}\n```",
		},
		{
			name: "Content with triple backticks (needs escaping)",
			args: map[string]interface{}{
				"path":     "README.md",
				"contents": "# Example\n\n```js\ncode here\n```\n\nMore text",
			},
			expected: "```markdown\n# Example\n\n\\```js\ncode here\n\\```\n\nMore text\n```",
		},
		{
			name: "File path only, no contents",
			args: map[string]interface{}{
				"path": "test.txt",
			},
			expected: "",
		},
		{
			name:     "Empty args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "Python file",
			args: map[string]interface{}{
				"path":     "main.py",
				"contents": "def main():\n    print('Hello')",
			},
			expected: "```python\ndef main():\n    print('Hello')\n```",
		},
		{
			name: "File with no extension",
			args: map[string]interface{}{
				"path":     "Makefile",
				"contents": "all:\n\techo 'Building'",
			},
			expected: "```\nall:\n\techo 'Building'\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatWriteTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatWriteTool() mismatch\nGot:\n%q\n\nWant:\n%q", result, tt.expected)
				// Show line-by-line comparison for better debugging
				gotLines := strings.Split(result, "\n")
				wantLines := strings.Split(tt.expected, "\n")
				for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
					var got, want string
					if i < len(gotLines) {
						got = gotLines[i]
					}
					if i < len(wantLines) {
						want = wantLines[i]
					}
					if got != want {
						t.Errorf("Line %d differs:\n  Got:  %q\n  Want: %q", i+1, got, want)
					}
				}
			}
		})
	}
}

func TestFormatStrReplaceTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "simple_replace",
			args: map[string]interface{}{
				"file_path":  "example.js",
				"old_string": "console.log('old');",
				"new_string": "console.log('new');",
			},
			expected: "```diff\n--- example.js\n+++ example.js\n-console.log('old');\n+console.log('new');\n```",
		},
		{
			name: "multiline_replace",
			args: map[string]interface{}{
				"file_path":  "test.py",
				"old_string": "def old_function():\n    pass",
				"new_string": "def new_function():\n    print('updated')\n    return True",
			},
			expected: "```diff\n--- test.py\n+++ test.py\n-def old_function():\n-    pass\n+def new_function():\n+    print('updated')\n+    return True\n```",
		},
		{
			name: "no_filepath",
			args: map[string]interface{}{
				"old_string": "old text",
				"new_string": "new text",
			},
			expected: "```diff\n-old text\n+new text\n```",
		},
		{
			name: "empty_old_string",
			args: map[string]interface{}{
				"file_path":  "new_file.txt",
				"old_string": "",
				"new_string": "New content",
			},
			expected: "```diff\n--- new_file.txt\n+++ new_file.txt\n-\n+New content\n```",
		},
		{
			name: "missing_args",
			args: map[string]interface{}{
				"file_path": "test.txt",
			},
			expected: "File: test.txt",
		},
		{
			name: "large_multiline_replace_from_readme",
			args: map[string]interface{}{
				"file_path":  "README.md",
				"old_string": "## Enjoy the Game! 🎉\n\nChallenge a friend or practice your strategy. Have fun playing Checkers!",
				"new_string": "## Future Features\n\nHere are some exciting enhancements planned for future versions:\n\n### Gameplay Enhancements\n- 🤖 **AI Opponent**: Add computer player with multiple difficulty levels\n\n## Enjoy the Game! 🎉\n\nChallenge a friend or practice your strategy. Have fun playing Checkers!",
			},
			expected: "```diff\n--- README.md\n+++ README.md\n-## Enjoy the Game! 🎉\n-\n-Challenge a friend or practice your strategy. Have fun playing Checkers!\n+## Future Features\n+\n+Here are some exciting enhancements planned for future versions:\n+\n+### Gameplay Enhancements\n+- 🤖 **AI Opponent**: Add computer player with multiple difficulty levels\n+\n+## Enjoy the Game! 🎉\n+\n+Challenge a friend or practice your strategy. Have fun playing Checkers!\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatStrReplaceTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatStrReplaceTool() mismatch\nGot:\n%q\n\nWant:\n%q", result, tt.expected)
				// Show line-by-line comparison for better debugging
				gotLines := strings.Split(result, "\n")
				wantLines := strings.Split(tt.expected, "\n")
				for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
					var got, want string
					if i < len(gotLines) {
						got = gotLines[i]
					}
					if i < len(wantLines) {
						want = wantLines[i]
					}
					if got != want {
						t.Errorf("Line %d differs:\n  Got:  %q\n  Want: %q", i+1, got, want)
					}
				}
			}
		})
	}
}

func TestFormatGrepTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "Grep with pattern and path",
			args: map[string]interface{}{
				"pattern":     "1-800-FERRETS",
				"path":        "phone",
				"output_mode": "files_with_matches",
			},
			expected: "pattern `1-800-FERRETS` path `phone`",
		},
		{
			name: "Grep with pattern only",
			args: map[string]interface{}{
				"pattern": "TODO",
			},
			expected: "pattern `TODO`",
		},
		{
			name: "Grep with path only",
			args: map[string]interface{}{
				"path": "src/",
			},
			expected: "path `src/`",
		},
		{
			name: "Grep with full path",
			args: map[string]interface{}{
				"pattern": "error",
				"path":    "/Users/test/project",
			},
			expected: "pattern `error` path `/Users/test/project`",
		},
		{
			name:     "Grep with no args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "Grep with empty strings",
			args: map[string]interface{}{
				"pattern": "",
				"path":    "",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGrepTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatGrepTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatGlobTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "Glob with pattern",
			args: map[string]interface{}{
				"glob_pattern": "**/phone/*.md",
			},
			expected: "pattern `**/phone/*.md`",
		},
		{
			name: "Glob with simple pattern",
			args: map[string]interface{}{
				"glob_pattern": "*.txt",
			},
			expected: "pattern `*.txt`",
		},
		{
			name: "Glob with complex pattern",
			args: map[string]interface{}{
				"glob_pattern": "src/**/*.{js,ts}",
			},
			expected: "pattern `src/**/*.{js,ts}`",
		},
		{
			name: "Glob with empty pattern",
			args: map[string]interface{}{
				"glob_pattern": "",
			},
			expected: "",
		},
		{
			name:     "Glob with no args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "Glob with null pattern",
			args: map[string]interface{}{
				"glob_pattern": nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGlobTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatGlobTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatLSTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "LS with current directory",
			args: map[string]interface{}{
				"target_directory": ".",
			},
			expected: "`.`",
		},
		{
			name: "LS with subdirectory",
			args: map[string]interface{}{
				"target_directory": "phone",
			},
			expected: "`phone`",
		},
		{
			name: "LS with full path",
			args: map[string]interface{}{
				"target_directory": "/Users/test/project",
			},
			expected: "`/Users/test/project`",
		},
		{
			name: "LS with parent directory",
			args: map[string]interface{}{
				"target_directory": "..",
			},
			expected: "`..`",
		},
		{
			name: "LS with empty directory",
			args: map[string]interface{}{
				"target_directory": "",
			},
			expected: "",
		},
		{
			name:     "LS with no args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "LS with null directory",
			args: map[string]interface{}{
				"target_directory": nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatLSTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatLSTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatMultiStrReplaceTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "Single replacement with replace_all",
			args: map[string]interface{}{
				"file_path": "hello.txt",
				"edits": []interface{}{
					map[string]interface{}{
						"old_string":  "1-800-FERRETS",
						"new_string":  "1-888-FERRETS",
						"replace_all": true,
					},
				},
			},
			expected: "`hello.txt`\n- Replace: `1-800-FERRETS` → `1-888-FERRETS` (all occurrences)",
		},
		{
			name: "Single replacement without replace_all",
			args: map[string]interface{}{
				"file_path": "test.js",
				"edits": []interface{}{
					map[string]interface{}{
						"old_string": "foo",
						"new_string": "bar",
					},
				},
			},
			expected: "`test.js`\n- Replace: `foo` → `bar`",
		},
		{
			name: "Multiple replacements",
			args: map[string]interface{}{
				"file_path": "config.json",
				"edits": []interface{}{
					map[string]interface{}{
						"old_string":  "localhost",
						"new_string":  "example.com",
						"replace_all": true,
					},
					map[string]interface{}{
						"old_string":  "8080",
						"new_string":  "443",
						"replace_all": true,
					},
					map[string]interface{}{
						"old_string": "http",
						"new_string": "https",
					},
				},
			},
			expected: "`config.json`\n- Replace: `localhost` → `example.com` (all occurrences)\n- Replace: `8080` → `443` (all occurrences)\n- Replace: `http` → `https`",
		},
		{
			name: "No file path",
			args: map[string]interface{}{
				"edits": []interface{}{
					map[string]interface{}{
						"old_string": "test",
						"new_string": "demo",
					},
				},
			},
			expected: "\n- Replace: `test` → `demo`",
		},
		{
			name: "Empty edits array",
			args: map[string]interface{}{
				"file_path": "file.txt",
				"edits":     []interface{}{},
			},
			expected: "`file.txt`",
		},
		{
			name:     "No args",
			args:     map[string]interface{}{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMultiStrReplaceTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatMultiStrReplaceTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatShellTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "Shell with simple command",
			args: map[string]interface{}{
				"command":     "echo \"2+2\" | bc",
				"description": "Calculate 2+2 using bc calculator",
			},
			expected: "`echo \"2+2\" | bc`",
		},
		{
			name: "Shell with ls command",
			args: map[string]interface{}{
				"command": "ls -la",
			},
			expected: "`ls -la`",
		},
		{
			name: "Shell with complex command",
			args: map[string]interface{}{
				"command":     "find . -name '*.txt' | wc -l",
				"description": "Count text files",
			},
			expected: "`find . -name '*.txt' | wc -l`",
		},
		{
			name: "Shell with empty command",
			args: map[string]interface{}{
				"command": "",
			},
			expected: "",
		},
		{
			name:     "Shell with no args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "Shell with null command",
			args: map[string]interface{}{
				"command": nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatShellTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatShellTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatReadTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "Read with file path",
			args: map[string]interface{}{
				"path": "hello.txt",
			},
			expected: "`hello.txt`",
		},
		{
			name: "Read with full path",
			args: map[string]interface{}{
				"path": "/Users/test/project/file.md",
			},
			expected: "`/Users/test/project/file.md`",
		},
		{
			name: "Read with relative path",
			args: map[string]interface{}{
				"path": "../config/settings.json",
			},
			expected: "`../config/settings.json`",
		},
		{
			name: "Read with empty path",
			args: map[string]interface{}{
				"path": "",
			},
			expected: "",
		},
		{
			name:     "Read with no args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "Read with null path",
			args: map[string]interface{}{
				"path": nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatReadTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatReadTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatGrepResult(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		expected string
	}{
		{
			name:     "Grep result with workspace tags",
			result:   "<workspace_result workspace_path=\"/Users/sean/Source/SpecStory/compositions/game-7\">\nFound 1 file\nphone/1.md\n</workspace_result>",
			expected: "Found 1 file\nphone/1.md",
		},
		{
			name:     "Grep result with workspace tags and extra whitespace",
			result:   "<workspace_result workspace_path=\"/test/path\">\n\nFound 3 matches\nfile1.txt\nfile2.txt\nfile3.txt\n\n</workspace_result>",
			expected: "Found 3 matches\nfile1.txt\nfile2.txt\nfile3.txt",
		},
		{
			name:     "Grep result without workspace tags",
			result:   "Found 2 files\ntest1.md\ntest2.md",
			expected: "Found 2 files\ntest1.md\ntest2.md",
		},
		{
			name:     "Empty result",
			result:   "",
			expected: "",
		},
		{
			name:     "Grep result with no matches",
			result:   "<workspace_result workspace_path=\"/path\">\nNo matches found\n</workspace_result>",
			expected: "No matches found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGrepResult(tt.result)
			if result != tt.expected {
				t.Errorf("formatGrepResult() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatDeleteTool(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]interface{}
		expected string
	}{
		{
			name: "Delete with file path",
			args: map[string]interface{}{
				"path": "phone/1.md",
			},
			expected: "`phone/1.md`",
		},
		{
			name: "Delete with full path",
			args: map[string]interface{}{
				"path": "/Users/test/project/temp.txt",
			},
			expected: "`/Users/test/project/temp.txt`",
		},
		{
			name: "Delete with empty path",
			args: map[string]interface{}{
				"path": "",
			},
			expected: "",
		},
		{
			name:     "Delete with no args",
			args:     map[string]interface{}{},
			expected: "",
		},
		{
			name: "Delete with null path",
			args: map[string]interface{}{
				"path": nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDeleteTool(tt.args)
			if result != tt.expected {
				t.Errorf("formatDeleteTool() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatToolResult(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		expected string
	}{
		{
			name:     "Simple result message",
			result:   "Wrote contents to index.html",
			expected: "```\nWrote contents to index.html\n```",
		},
		{
			name:     "Multi-line result",
			result:   "Created file: test.js\nWrote 50 lines\nOperation successful",
			expected: "```\nCreated file: test.js\nWrote 50 lines\nOperation successful\n```",
		},
		{
			name:     "Result with triple backticks (needs escaping)",
			result:   "Error in code:\n```\nerror here\n```",
			expected: "```\nError in code:\n\\```\nerror here\n\\```\n```",
		},
		{
			name:     "Empty result",
			result:   "",
			expected: "",
		},
		{
			name:     "Result with special characters",
			result:   "File saved: ~/project/src/*.js",
			expected: "```\nFile saved: ~/project/src/*.js\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolResult(tt.result)
			if result != tt.expected {
				t.Errorf("formatToolResult() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatTodoList(t *testing.T) {
	tests := []struct {
		name     string
		todos    []interface{}
		expected string
	}{
		{
			name:     "Empty todo list",
			todos:    []interface{}{},
			expected: "", // No header when no valid todos
		},
		{
			name: "Single pending todo",
			todos: []interface{}{
				map[string]interface{}{
					"id":      "1",
					"status":  TodoStatusPending,
					"content": "Create HTML structure",
				},
			},
			expected: "**Agent task list:**\n- [ ] Create HTML structure\n",
		},
		{
			name: "All status combinations",
			todos: []interface{}{
				map[string]interface{}{
					"id":      "1",
					"status":  TodoStatusPending,
					"content": "Pending task",
				},
				map[string]interface{}{
					"id":      "2",
					"status":  TodoStatusInProgress,
					"content": "In progress task",
				},
				map[string]interface{}{
					"id":      "3",
					"status":  TodoStatusCompleted,
					"content": "Completed task",
				},
			},
			expected: "**Agent task list:**\n- [ ] Pending task\n- [⚡] In progress task\n- [X] Completed task\n",
		},
		{
			name: "Invalid status defaults to pending",
			todos: []interface{}{
				map[string]interface{}{
					"id":      "1",
					"status":  "invalid_status",
					"content": "Task with invalid status",
				},
			},
			expected: "**Agent task list:**\n- [ ] Task with invalid status\n",
		},
		{
			name: "Missing status field defaults to pending",
			todos: []interface{}{
				map[string]interface{}{
					"id":      "1",
					"content": "Task with missing status",
				},
			},
			expected: "**Agent task list:**\n- [ ] Task with missing status\n",
		},
		{
			name: "Missing content field is skipped",
			todos: []interface{}{
				map[string]interface{}{
					"id":     "1",
					"status": TodoStatusInProgress,
				},
			},
			expected: "", // Skipped because no content
		},
		{
			name: "Non-string field types are skipped",
			todos: []interface{}{
				map[string]interface{}{
					"id":      "1",
					"status":  123,                            // non-string status
					"content": []string{"not", "a", "string"}, // non-string content
				},
			},
			expected: "", // Skipped because content is not a string
		},
		{
			name: "Non-map todo item is skipped",
			todos: []interface{}{
				"not a map",
				map[string]interface{}{
					"id":      "1",
					"status":  TodoStatusPending,
					"content": "Valid task",
				},
				123, // not a map
			},
			expected: "**Agent task list:**\n- [ ] Valid task\n",
		},
		{
			name: "Partial update with merge true (no content)",
			todos: []interface{}{
				map[string]interface{}{
					"id":     "1",
					"status": "completed",
				},
				map[string]interface{}{
					"id":     "2",
					"status": "in_progress",
				},
			},
			expected: "", // No output because these are partial updates without content
		},
		{
			name: "Mixed partial and full todos",
			todos: []interface{}{
				map[string]interface{}{
					"id":     "1",
					"status": "completed",
					// No content - partial update
				},
				map[string]interface{}{
					"id":      "2",
					"content": "Real task with content",
					"status":  "pending",
				},
				map[string]interface{}{
					"id":     "3",
					"status": "in_progress",
					// No content - partial update
				},
			},
			expected: "**Agent task list:**\n- [ ] Real task with content\n",
		},
		{
			name: "Real-world example from Cursor CLI",
			todos: []interface{}{
				map[string]interface{}{
					"id":      "1",
					"content": "Create HTML structure for the checkers game",
					"status":  "in_progress",
				},
				map[string]interface{}{
					"id":      "2",
					"content": "Create CSS styles for beautiful modern UI",
					"status":  "pending",
				},
				map[string]interface{}{
					"id":      "3",
					"content": "Implement game logic (board, pieces, moves)",
					"status":  "pending",
				},
			},
			expected: "**Agent task list:**\n- [⚡] Create HTML structure for the checkers game\n- [ ] Create CSS styles for beautiful modern UI\n- [ ] Implement game logic (board, pieces, moves)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTodoList(tt.todos)
			if result != tt.expected {
				t.Errorf("formatTodoList() = %q, want %q", result, tt.expected)
				// Show differences more clearly
				t.Errorf("Got lines: %v", strings.Split(result, "\n"))
				t.Errorf("Want lines: %v", strings.Split(tt.expected, "\n"))
			}
		})
	}
}

func TestCleanTodoResult(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "No IDs to remove",
			input:    "### ✅ Todos Updated\n\n- **PENDING**: Create HTML structure",
			expected: "### ✅ Todos Updated\n\n- **PENDING**: Create HTML structure",
		},
		{
			name:     "Single ID removal",
			input:    "### ✅ Todos Updated\n\n- **IN_PROGRESS**: Create HTML structure (id: 1)",
			expected: "### ✅ Todos Updated\n\n- **IN_PROGRESS**: Create HTML structure",
		},
		{
			name: "Multiple ID removals",
			input: `### ✅ Todos Updated

- **IN_PROGRESS**: Create HTML structure for the checkers game (id: 1)
- **PENDING**: Create CSS styles for beautiful modern UI (id: 2)
- **PENDING**: Implement game logic (board, pieces, moves) (id: 3)`,
			expected: `### ✅ Todos Updated

- **IN_PROGRESS**: Create HTML structure for the checkers game
- **PENDING**: Create CSS styles for beautiful modern UI
- **PENDING**: Implement game logic (board, pieces, moves)`,
		},
		{
			name: "IDs with different formats",
			input: `### ✅ Todos Updated

- **COMPLETED**: Task one (id: 1)
- **COMPLETED**: Task two (id: abc-123)
- **IN_PROGRESS**: Task three (id: task_3_uuid)`,
			expected: `### ✅ Todos Updated

- **COMPLETED**: Task one
- **COMPLETED**: Task two
- **IN_PROGRESS**: Task three`,
		},
		{
			name: "Real-world example from tool-result",
			input: `### ✅ Todos Updated

- **COMPLETED**: Implement game logic (board, pieces, moves) (id: 3)
- **COMPLETED**: Add move validation and game rules (id: 4)
- **COMPLETED**: Implement piece capturing and king promotion (id: 5)
- **COMPLETED**: Add win condition checking (id: 6)
- **IN_PROGRESS**: Create README with instructions (id: 7)`,
			expected: `### ✅ Todos Updated

- **COMPLETED**: Implement game logic (board, pieces, moves)
- **COMPLETED**: Add move validation and game rules
- **COMPLETED**: Implement piece capturing and king promotion
- **COMPLETED**: Add win condition checking
- **IN_PROGRESS**: Create README with instructions`,
		},
		{
			name: "Preserve parentheses not related to IDs",
			input: `### ✅ Todos Updated

- **PENDING**: Implement function (with parameters) (id: 1)
- **PENDING**: Fix bug (see issue #123) (id: 2)`,
			expected: `### ✅ Todos Updated

- **PENDING**: Implement function (with parameters)
- **PENDING**: Fix bug (see issue #123)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanTodoResult(tt.input)
			if result != tt.expected {
				t.Errorf("cleanTodoResult() = %q, want %q", result, tt.expected)
			}
		})
	}
}
