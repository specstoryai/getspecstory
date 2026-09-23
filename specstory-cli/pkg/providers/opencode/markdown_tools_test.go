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
			name:   "read of a directory captions the listing",
			tool:   findTool(t, optional, "read", inputEquals("path", ".")),
			want:   []string{"Read directory ., entries 1-15\n\n```text\n.git/"},
			reject: []string{"Result:"},
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
			want:    []string{"Directory: `/builtin`", "```markdown\n# Skill: OpenCode"},
			reject:  []string{"<skill_content", "<skill_files", "Base directory for this skill", "file list is sampled"},
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
			want: []string{"```javascript\nconst s = search(", "**Error:**", "[browser.disconnected]", "Tool calls:", "- `opencode.models` — completed", "- `browser.tabs.list` — error"},
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
	if question.summary != "Tool use: **question** `Favorite color`" {
		t.Errorf("summary = %q, want the question's header", question.summary)
	}
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
// testdata/tools.txt) has a deliberate classification, so a tool dropped from
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

	file, err := os.Open(filepath.Join("testdata", "tools.txt"))
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
		name    string
		tool    schema.ToolInfo
		summary string
		want    []string
		reject  []string
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
			name:   "a file's final newline does not become a blank line in the fence",
			tool:   schema.ToolInfo{Name: "write", Input: map[string]any{"path": "a.txt", "content": "one\ntwo\n"}},
			want:   []string{"```txt\none\ntwo\n```"},
			reject: []string{"two\n\n```"},
		},
		{
			name:    "backticks in a summary argument stay inside the code span",
			tool:    schema.ToolInfo{Name: "grep", Input: map[string]any{"pattern": "a`b"}},
			summary: "Tool use: **grep** `` a`b ``",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tt.tool
			markdown := formatToolAsMarkdown(&tool)
			if tt.summary != "" && (tool.Summary == nil || *tool.Summary != tt.summary) {
				t.Errorf("summary = %v, want %q", tool.Summary, tt.summary)
			}
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
}

// TestRenderWebSearchResults covers the search document OpenCode 2.0.14's
// search provider returns: one "## [Title](url)" heading per hit followed by a
// page excerpt, where the excerpt can carry the page's own anchor headings.
func TestRenderWebSearchResults(t *testing.T) {
	const twoHits = "## [OpenCode](https://opencode.ai/)\n\n# The open source AI coding agent\n```\ncurl -fsSL https://opencode.ai/v2/install | bash\n```\n\n### What is OpenCode?\nOpenCode is an open source agent.\n\n" +
		"## [OpenCode - Overview - Z.AI](https://docs.z.ai/devpack/tool/opencode)\n\n## [\u200b](https://docs.z.ai/devpack/tool/opencode#step-1-installing-opencode)  Step 1: Installing OpenCode\nInstall it.\n"
	tests := []struct {
		name   string
		output map[string]any
		want   []string
		reject []string
	}{
		{
			name:   "hits become a linked list with their excerpts",
			output: map[string]any{"status": "completed", "texts": []string{twoHits}, "metadata": map[string]any{"provider": "firecrawl"}},
			want: []string{
				"Provider: `firecrawl`",
				"2 results:\n\n- [OpenCode](https://opencode.ai/)\n\n  ````markdown\n  # The open source AI coding agent\n  ```\n  curl -fsSL",
				"- [OpenCode - Overview - Z.AI](https://docs.z.ai/devpack/tool/opencode)\n\n  ```markdown\n  ## [\u200b](https://docs.z.ai/devpack/tool/opencode#step-1-installing-opencode)  Step 1: Installing OpenCode\n  Install it.\n  ```",
			},
			reject: []string{"3 results", "Result:\n"},
		},
		{
			name:   "a placeholder link text is named by its URL",
			output: map[string]any{"status": "completed", "texts": []string{"## [\u200b](https://example.com/a)\n\nexcerpt"}},
			want:   []string{"1 results:\n\n- [https://example.com/a](https://example.com/a)\n\n  ```markdown\n  excerpt\n  ```"},
		},
		{
			name:   "a hit without an excerpt is just its link",
			output: map[string]any{"status": "completed", "texts": []string{"## [A](https://a.example)\n\n## [B](https://b.example)\n\nonly B has text"}},
			want:   []string{"- [A](https://a.example)\n\n- [B](https://b.example)\n\n  ```markdown\n  only B has text\n  ```"},
		},
		{
			name:   "text without hit headings falls back to a fence",
			output: map[string]any{"status": "completed", "texts": []string{"No results found for the query."}},
			want:   []string{"Result:\n\n```text\nNo results found for the query.\n```"},
			reject: []string{"results:"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := schema.ToolInfo{Name: "websearch", Input: map[string]any{"query": "OpenCode"}, Output: tt.output}
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
}

// TestStripSkillFooter covers the footer OpenCode appends to a skill document,
// which is removed only when both its opening line and closing tag are there.
func TestStripSkillFooter(t *testing.T) {
	const footer = "\n\nBase directory for this skill: /builtin\nRelative paths in this skill are relative to this base directory.\nNote: file list is sampled.\n\n<skill_files>\n</skill_files>"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "footer removed", in: "# Skill\n\nBody." + footer, want: "# Skill\n\nBody."},
		{name: "footer with trailing newline removed", in: "# Skill" + footer + "\n", want: "# Skill"},
		{name: "no footer", in: "# Skill\n\nBody.\n", want: "# Skill\n\nBody.\n"},
		// A skill that talks about its base directory keeps that text.
		{name: "opening line without the closing tag is content", in: "# Skill\nBase directory for this skill: /x\nMore.", want: "# Skill\nBase directory for this skill: /x\nMore."},
		{name: "closing tag without the opening line is content", in: "# Skill\n<skill_files>\n</skill_files>", want: "# Skill\n<skill_files>\n</skill_files>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripSkillFooter(tt.in); got != tt.want {
				t.Errorf("stripSkillFooter(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestRenderMalformedMetadata covers the metadata walks that silently skip
// what they cannot read: a question whose answers are missing or misshapen
// falls back to the model-facing text, and an execute run lists only the
// inner calls that name a tool.
func TestRenderMalformedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		tool   schema.ToolInfo
		want   []string
		reject []string
	}{
		{
			name: "question without metadata shows the text result",
			tool: schema.ToolInfo{Name: "question",
				Output: map[string]any{"status": "completed", "texts": []string{"User picked Green"}}},
			want:   []string{"Result:\n\n```text\nUser picked Green\n```"},
			reject: []string{"Answer"},
		},
		{
			name: "question whose answers are not a list shows the text result",
			tool: schema.ToolInfo{Name: "question",
				Output: map[string]any{"status": "completed", "texts": []string{"User picked Green"},
					"metadata": map[string]any{"answers": "Green"}}},
			want:   []string{"User picked Green"},
			reject: []string{"Answer"},
		},
		{
			name: "question whose answers hold no labels shows the text result",
			tool: schema.ToolInfo{Name: "question",
				Output: map[string]any{"status": "completed", "texts": []string{"User picked Green"},
					"metadata": map[string]any{"answers": []any{[]any{"", "  "}, "not-a-list", 7}}}},
			want:   []string{"User picked Green"},
			reject: []string{"Answer"},
		},
		{
			name: "question keeps the readable answers and drops the malformed ones",
			tool: schema.ToolInfo{Name: "question",
				Output: map[string]any{"status": "completed", "texts": []string{"User picked Green and Large"},
					"metadata": map[string]any{"answers": []any{[]any{"Green"}, "not-a-list", []any{"Large", 3}}}}},
			want:   []string{"Answers:\n- Green\n- Large"},
			reject: []string{"User picked"},
		},
		{
			name: "execute that threw a one-line error is labeled inline",
			tool: schema.ToolInfo{Name: "execute", Input: map[string]any{"code": "x"},
				Output: map[string]any{"status": "completed", "texts": []string{"ReferenceError: Unknown identifier 'x'. (line 1, col 1)"},
					"metadata": map[string]any{"error": true}}},
			want:   []string{"**Error:** ReferenceError: Unknown identifier 'x'. (line 1, col 1)"},
			reject: []string{"Result:", "```text"},
		},
		{
			name: "execute that threw a multi-line error is labeled above a fence",
			tool: schema.ToolInfo{Name: "execute", Input: map[string]any{"code": "x"},
				Output: map[string]any{"status": "completed", "texts": []string{"TypeError: boom\n  at line 2"},
					"metadata": map[string]any{"error": true, "toolCalls": []any{map[string]any{"tool": "browser.tabs.open", "status": "error"}}}}},
			want:   []string{"**Error:**\n\n```text\nTypeError: boom\n  at line 2\n```", "Tool calls:\n\n- `browser.tabs.open` — error"},
			reject: []string{"Result:"},
		},
		{
			name: "execute without inner calls lists none",
			tool: schema.ToolInfo{Name: "execute", Input: map[string]any{"code": "1"},
				Output: map[string]any{"status": "completed", "texts": []string{"1"}}},
			want:   []string{"```javascript\n1\n```", "Result:\n\n```json\n1\n```"},
			reject: []string{"Tool calls:"},
		},
		{
			name: "execute whose inner calls are not a list lists none",
			tool: schema.ToolInfo{Name: "execute", Input: map[string]any{"code": "1"},
				Output: map[string]any{"status": "completed", "texts": []string{"1"},
					"metadata": map[string]any{"toolCalls": map[string]any{"tool": "opencode.models"}}}},
			reject: []string{"Tool calls:"},
		},
		{
			name: "execute lists only inner calls that name a tool",
			tool: schema.ToolInfo{Name: "execute", Input: map[string]any{"code": "1"},
				Output: map[string]any{"status": "completed", "texts": []string{"1"},
					"metadata": map[string]any{"toolCalls": []any{
						map[string]any{"tool": "opencode.models", "status": "completed"},
						map[string]any{"status": "error"},
						"not-a-call",
						map[string]any{"tool": "browser.tabs.list"},
					}}}},
			want:   []string{"Tool calls:\n\n- `opencode.models` — completed\n- `browser.tabs.list`"},
			reject: []string{"- ` `", "— error"},
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
}
