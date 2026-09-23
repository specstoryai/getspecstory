package opencode

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// renderedTool is one tool call as the provider renders it.
type renderedTool struct {
	name     string
	input    map[string]any
	summary  string
	markdown string
}

// renderedTools converts a captured session through the real conversion path
// and collects every rendered tool call.
func renderedTools(t *testing.T, fixture, sessionID string) []renderedTool {
	t.Helper()
	session, _ := loadSession(t, sessionID, fixture)
	var tools []renderedTool
	for _, message := range allMessages(session.SessionData) {
		if message.Tool == nil {
			continue
		}
		tool := renderedTool{name: message.Tool.Name, input: message.Tool.Input}
		if message.Tool.Summary != nil {
			tool.summary = *message.Tool.Summary
		}
		if message.Tool.FormattedMarkdown != nil {
			tool.markdown = *message.Tool.FormattedMarkdown
		}
		tools = append(tools, tool)
	}
	return tools
}

// findTool returns the first rendered call to name whose input satisfies match.
func findTool(t *testing.T, tools []renderedTool, name string, match func(map[string]any) bool) renderedTool {
	t.Helper()
	for _, tool := range tools {
		if tool.name == name && (match == nil || match(tool.input)) {
			return tool
		}
	}
	t.Fatalf("no rendered %s call matched", name)
	return renderedTool{}
}

func inputEquals(key, value string) func(map[string]any) bool {
	return func(input map[string]any) bool {
		v, _ := input[key].(string)
		return v == value
	}
}

func hasInput(key string) func(map[string]any) bool {
	return func(input map[string]any) bool {
		_, ok := input[key]
		return ok
	}
}

// TestRenderCapturedToolCalls renders every observed tool shape from real
// sessions through the actual dispatch path.
func TestRenderCapturedToolCalls(t *testing.T) {
	exercise := renderedTools(t, "tool-exercise.jsonl", toolSessionID)
	optional := renderedTools(t, "optional-params.jsonl", optionalSessionID)

	tests := []struct {
		name    string
		tool    renderedTool
		summary string
		want    []string
		reject  []string
	}{
		{
			name:    "read shows the file in a language fence under OpenCode's caption",
			tool:    findTool(t, exercise, "read", inputEquals("path", "hello.py")),
			summary: "Tool use: **read** `hello.py`",
			want:    []string{"Read file hello.py, lines 1-5", "```python\n1: def greet(name):"},
		},
		{
			name: "read with a line window",
			tool: findTool(t, optional, "read", hasInput("offset")),
			want: []string{"Offset: `2`", "Limit: `2`"},
		},
		{
			name:   "read of a missing file renders the error",
			tool:   findTool(t, exercise, "read", inputEquals("path", "missing.txt")),
			want:   []string{"**Error:** File not found: missing.txt"},
			reject: []string{"Result:"},
		},
		{
			name:    "write shows the content and the acknowledgement",
			tool:    findTool(t, exercise, "write", nil),
			summary: "Tool use: **write** `farewell.py`",
			want:    []string{"```python\ndef farewell(name):", "Result: Created file successfully: farewell.py"},
		},
		{
			name: "edit shows OpenCode's unified diff",
			tool: findTool(t, exercise, "edit", nil),
			want: []string{"```diff\nIndex: hello.py", "+def farewell(name):", "Result: Edited hello.py (1 replacement)"},
		},
		{
			name: "edit with replace-all",
			tool: findTool(t, optional, "edit", hasInput("replaceAll")),
			want: []string{"Replace all: `true`", "-    return \"Goodbye, \" + name"},
		},
		{
			name:    "glob",
			tool:    findTool(t, exercise, "glob", nil),
			summary: "Tool use: **glob** `*.py`",
			want:    []string{"Result:\n\n```text\n/Users/dev/oc-tools/hello.py\n```"},
		},
		{
			name: "glob restricted to a directory",
			tool: findTool(t, optional, "glob", hasInput("path")),
			want: []string{"Path: `sub`"},
		},
		{
			name: "grep with include and path",
			tool: findTool(t, optional, "grep", hasInput("include")),
			want: []string{"Path: `sub`", "Include: `*.txt`", "Found 1 matches"},
		},
		{
			name:   "shell output with the exit code from metadata",
			tool:   findTool(t, exercise, "shell", inputEquals("command", "ls -la")),
			want:   []string{"```bash\nls -la\n```", "hello.py", "Exit code: 0"},
			reject: []string{"Command exited with code"},
		},
		{
			name:   "failing shell command",
			tool:   findTool(t, exercise, "shell", inputEquals("command", "cat does-not-exist.txt")),
			want:   []string{"cat: does-not-exist.txt: No such file or directory", "Exit code: 1"},
			reject: []string{"Command exited with code"},
		},
		{
			name: "shell with working directory and timeout",
			tool: findTool(t, optional, "shell", hasInput("workdir")),
			want: []string{"Working directory: `sub`", "Timeout: `10000`", "/Users/dev/oc-tools/sub"},
		},
		{
			name:    "webfetch renders the converted page",
			tool:    findTool(t, exercise, "webfetch", nil),
			summary: "Tool use: **webfetch** `https://example.com`",
			want:    []string{"```markdown\n# Example Domain"},
		},
		{
			name: "webfetch in html format",
			tool: findTool(t, optional, "webfetch", hasInput("format")),
			want: []string{"Format: `html`", "```html\n"},
		},
		{
			name:    "failed websearch",
			tool:    findTool(t, exercise, "websearch", nil),
			summary: "Tool use: **websearch** `OpenCode AI coding agent`",
			want:    []string{"**Error:** Web search cancelled"},
		},
		{
			name:    "skill content without the model-facing wrapper",
			tool:    findTool(t, exercise, "skill", nil),
			summary: "Tool use: **skill** `opencode`",
			want:    []string{"```markdown\n# Skill: OpenCode"},
			reject:  []string{"<skill_content"},
		},
		{
			name:    "subagent prompt and answer",
			tool:    findTool(t, exercise, "subagent", nil),
			summary: "Tool use: **subagent** `Summarize notes.md`",
			want:    []string{"Agent: `explore`", "Prompt:", "Subagent session: `" + subagentSessionID + "` (completed)", "The file `/Users/dev/oc-tools/notes.md` is a minimal notes file"},
			reject:  []string{"<subagent"},
		},
		{
			name: "execute that failed inside its script",
			tool: findTool(t, exercise, "execute", func(input map[string]any) bool {
				code, _ := input["code"].(string)
				return !strings.Contains(code, "try")
			}),
			want: []string{"```javascript\nconst s = search(", "[browser.disconnected]", "Tool calls:", "- `opencode.models` — completed", "- `browser.tabs.list` — error"},
		},
		{
			name: "execute returning an object",
			tool: findTool(t, exercise, "execute", func(input map[string]any) bool {
				code, _ := input["code"].(string)
				return strings.Contains(code, "try")
			}),
			want: []string{"```json\n{\n  \"searchHitPaths\""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.summary != "" && tt.tool.summary != tt.summary {
				t.Errorf("summary = %q, want %q", tt.tool.summary, tt.summary)
			}
			for _, want := range tt.want {
				if !strings.Contains(tt.tool.markdown, want) {
					t.Errorf("markdown missing %q:\n%s", want, tt.tool.markdown)
				}
			}
			for _, reject := range tt.reject {
				if strings.Contains(tt.tool.markdown, reject) {
					t.Errorf("markdown contains %q:\n%s", reject, tt.tool.markdown)
				}
			}
		})
	}
}

func TestRenderQuestion(t *testing.T) {
	tools := renderedTools(t, "question.jsonl", questionSessionID)
	question := findTool(t, tools, "question", nil)
	for _, want := range []string{"**Which color do you prefer?**", "- Red — A warm, bold color.", "- Blue — A calm, cool color.", "Answer: Green"} {
		if !strings.Contains(question.markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, question.markdown)
		}
	}
	// The model-facing restatement of the answer is not repeated.
	if strings.Contains(question.markdown, "User has answered your questions") {
		t.Errorf("model-facing answer text rendered:\n%s", question.markdown)
	}
}

// TestToolInventorySweep checks every tool OpenCode 2.0.14 declares (see
// examples/tools.txt) has a deliberate classification, so a tool dropped from
// the switch surfaces as a failure rather than as "unknown".
func TestToolInventorySweep(t *testing.T) {
	want := map[string]string{
		"edit":      schema.ToolTypeWrite,
		"execute":   schema.ToolTypeGeneric,
		"question":  schema.ToolTypeGeneric,
		"glob":      schema.ToolTypeSearch,
		"grep":      schema.ToolTypeSearch,
		"read":      schema.ToolTypeRead,
		"shell":     schema.ToolTypeShell,
		"skill":     schema.ToolTypeGeneric,
		"subagent":  schema.ToolTypeGeneric,
		"webfetch":  schema.ToolTypeRead,
		"websearch": schema.ToolTypeSearch,
		"write":     schema.ToolTypeWrite,
	}

	file, err := os.Open(filepath.Join("examples", "tools.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()

	// tools.txt lists direct tools one per line until a blank line; the Code
	// Mode catalog after it is only reachable through execute.
	var declared []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		declared = append(declared, line)
	}
	if len(declared) != len(want) {
		t.Errorf("tools.txt declares %d direct tools %v, test expects %d", len(declared), declared, len(want))
	}
	for _, name := range declared {
		expected, ok := want[name]
		if !ok {
			t.Errorf("tools.txt tool %q has no expected type", name)
			continue
		}
		if got := toolType(name); got != expected {
			t.Errorf("toolType(%q) = %q, want %q", name, got, expected)
		}
	}
	if got := toolType("todowrite"); got != schema.ToolTypeUnknown {
		t.Errorf("an undeclared tool must stay unknown, got %q", got)
	}
}

func TestRenderEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		tool   schema.ToolInfo
		want   []string
		reject []string
	}{
		{
			name: "unknown tool falls back to generic JSON and its text result",
			tool: schema.ToolInfo{Name: "todowrite", Input: map[string]any{"questions": []any{"Pick one"}},
				Output: map[string]any{"status": "completed", "texts": []string{"answered"}}},
			want: []string{"```json\n{\n  \"questions\": [", "Result:\n\n```text\nanswered\n```"},
		},
		{
			name: "streaming call shows its partial arguments",
			tool: schema.ToolInfo{Name: "shell", Input: map[string]any{"partialInput": `{"command": "ls`},
				Output: map[string]any{"status": "streaming"}},
			want: []string{"Arguments (still streaming):", `{"command": "ls`, "_Still running._"},
		},
		{
			name: "error takes priority over the success renderer and keeps any text",
			tool: schema.ToolInfo{Name: "shell", Input: map[string]any{"command": "false"},
				Output: map[string]any{"status": "error", "error": "Command timed out", "texts": []string{"partial output"}}},
			want:   []string{"**Error:** Command timed out", "partial output"},
			reject: []string{"Exit code"},
		},
		{
			name: "backticks in a summary argument stay inside the code span",
			tool: schema.ToolInfo{Name: "grep", Input: map[string]any{"pattern": "a`b"}},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tt.tool
			markdown := formatToolAsMarkdown(&tool)
			for _, want := range tt.want {
				if !strings.Contains(markdown, want) {
					t.Errorf("markdown missing %q:\n%s", want, markdown)
				}
			}
			for _, reject := range tt.reject {
				if strings.Contains(markdown, reject) {
					t.Errorf("markdown contains %q:\n%s", reject, markdown)
				}
			}
		})
	}

	grep := schema.ToolInfo{Name: "grep", Input: map[string]any{"pattern": "a`b"}}
	formatToolAsMarkdown(&grep)
	if grep.Summary == nil || *grep.Summary != "Tool use: **grep** ``a`b``" {
		t.Errorf("summary with a backtick = %v", grep.Summary)
	}
}
