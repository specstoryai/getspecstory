package copilotide

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// rawMessages converts JSON literals to the raw response array shape.
func rawMessages(items ...string) []json.RawMessage {
	raw := make([]json.RawMessage, len(items))
	for i, s := range items {
		raw[i] = json.RawMessage(s)
	}
	return raw
}

func TestExtractTextFromResponseArray(t *testing.T) {
	tests := []struct {
		name      string
		responses []json.RawMessage
		want      string
	}{
		{
			// The real-world truncation case: markdown fragments split around an
			// inlineReference file chip, with tool/progress items interleaved.
			name: "fragments around inline file reference",
			responses: rawMessages(
				`{"kind": "mcpServersStarting", "didStartServerIds": []}`,
				`{"kind": "toolInvocationSerialized", "toolId": "create_file"}`,
				`{"kind": "textEditGroup", "uri": {"path": "/proj/NEW_README.md"}}`,
				`{"value": "A new file named ", "supportThemeIcons": false}`,
				`{"kind": "inlineReference", "inlineReference": {"fsPath": "/proj/NEW_README.md", "path": "/proj/NEW_README.md", "scheme": "file"}}`,
				`{"value": " has been created with a fresh overview."}`,
			),
			want: "A new file named `NEW_README.md` has been created with a fresh overview.",
		},
		{
			name: "symbol reference uses its name",
			responses: rawMessages(
				`{"value": "See "}`,
				`{"kind": "inlineReference", "name": "ParseResponseKind", "inlineReference": {"fsPath": "/proj/parser.go"}}`,
				`{"value": " for details."}`,
			),
			want: "See `ParseResponseKind` for details.",
		},
		{
			name: "single markdown item",
			responses: rawMessages(
				`{"value": "Plain answer."}`,
			),
			want: "Plain answer.",
		},
		{
			name: "reference without name or path renders nothing",
			responses: rawMessages(
				`{"value": "Before"}`,
				`{"kind": "inlineReference", "inlineReference": {}}`,
				`{"value": " after"}`,
			),
			want: "Before after",
		},
		{
			// Separate progress notes streamed between tool batches must not
			// butt together mid-sentence ("...report.I'll create...").
			name: "adjacent plain fragments get a paragraph break",
			responses: rawMessages(
				`{"value": "First progress note."}`,
				`{"value": "Second progress note."}`,
			),
			want: "First progress note.\n\nSecond progress note.",
		},
		{
			// A fragment carrying its own boundary newlines keeps them as-is.
			name: "fragment with leading newlines is not double-separated",
			responses: rawMessages(
				`{"value": "First."}`,
				`{"value": "\n\nSecond."}`,
			),
			want: "First.\n\nSecond.",
		},
		{
			// VS Code brackets structured code-block items with bare-fence
			// fragments; with the items unrendered they'd leave empty fences.
			name: "bare fence delimiter fragments dropped",
			responses: rawMessages(
				`{"value": "Before the block."}`,
				`{"value": "\n`+"```"+`\n"}`,
				`{"kind": "codeblockUri", "uri": {"path": "/proj/x.go"}}`,
				`{"value": "\n`+"```"+`\n"}`,
				`{"value": "After the block."}`,
			),
			want: "Before the block.\n\nAfter the block.",
		},
		{
			// A real fenced code example is one fragment spanning several lines
			// and must survive intact.
			name: "complete fenced block within one fragment kept",
			responses: rawMessages(
				`{"value": "` + "```go\\nfmt.Println(1)\\n```" + `"}`,
			),
			want: "```go\nfmt.Println(1)\n```",
		},
		{
			name:      "no markdown items",
			responses: rawMessages(`{"kind": "mcpServersStarting"}`),
			want:      "",
		},
		{
			name:      "empty array",
			responses: nil,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTextFromResponseArray(tt.responses); got != tt.want {
				t.Errorf("ExtractTextFromResponseArray() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatToolMarkdown covers the pre-rendered tool body: input key-value pairs
// (multiline values fenced, internal keys skipped) and the fenced result.
func TestSanitizeInvocationMessage(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// The label is empty on disk — VS Code's own renderer fills it in,
			// plain markdown shows the raw "[](file://...)".
			name: "empty-label file link gets the file name",
			in:   "Viewed image [](file:///Users/me/tool_demos/tiny.png)",
			want: "Viewed image `tiny.png`",
		},
		{
			name: "notebook cell link drops the fragment",
			in:   "Ran [](vscode-notebook-cell:/Users/me/demo.ipynb#W2sZmlsZQ%3D%3D)",
			want: "Ran `demo.ipynb`",
		},
		{
			name: "labeled IDE-scheme link keeps its label, drops the dead URL",
			in:   "Opened [Browser](vscode-browser:/591ef302?vscodeLinkType=browser)",
			want: "Opened `Browser`",
		},
		{
			name: "message without links passes through",
			in:   "Updated todo list",
			want: "Updated todo list",
		},
		{
			name: "empty message stays empty",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeInvocationMessage(tt.in); got != tt.want {
				t.Errorf("sanitizeInvocationMessage(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatToolMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		tool     *schema.ToolInfo
		contains []string
		absent   []string
	}{
		{
			// The motivating case: a create_file whose input carries the whole
			// written file. The content must survive into FormattedMarkdown.
			name: "input with multiline and scalar values",
			tool: &schema.ToolInfo{
				Name: "create_file",
				Input: map[string]interface{}{
					"filePath": "/proj/NEW_README.md",
					"content":  "# Title\n\nBody line.",
					"_cwd":     "/proj", // internal field, must be skipped
				},
			},
			contains: []string{
				"**Input:**",
				"- filePath: `/proj/NEW_README.md`",
				"- content:\n\n```\n# Title\n\nBody line.\n```",
			},
			absent: []string{"_cwd", "**Result:**"},
		},
		{
			// A tool without a dedicated formatter renders the generic result fence.
			name: "result rendered fenced",
			tool: &schema.ToolInfo{
				Name:   "get_terminal_output",
				Input:  map[string]interface{}{"id": "term-1"},
				Output: map[string]interface{}{"result": "file-a\nfile-b"},
			},
			contains: []string{"- id: `term-1`", "**Result:**\n\n```\nfile-a\nfile-b\n```"},
		},
		{
			// Values containing backtick fences must be wrapped in a longer fence
			// so they cannot terminate the block early.
			name: "fence sizing adapts to embedded fences",
			tool: &schema.ToolInfo{
				Name:   "create_file",
				Input:  map[string]interface{}{"content": "# Readme\n\n```sh\nmake build\n```\n"},
				Output: map[string]interface{}{"result": "before\n````\nfour\n````\nafter"},
			},
			contains: []string{
				"- content:\n\n````\n# Readme\n\n```sh\nmake build\n```\n\n````",
				"**Result:**\n\n`````\nbefore\n````\nfour\n````\nafter\n`````",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatToolMarkdown(tt.tool)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			for _, ban := range tt.absent {
				if strings.Contains(got, ban) {
					t.Errorf("unexpected %q in:\n%s", ban, got)
				}
			}
		})
	}
}

// TestFormatToolMarkdown_Empty verifies a tool with no structured data pre-renders
// nothing, so ToolInfo.FormattedMarkdown stays nil and downstream fallbacks apply.
func TestFormatToolMarkdown_TerminalHandler(t *testing.T) {
	tool := &schema.ToolInfo{
		Name: "run_in_terminal",
		Input: map[string]any{
			"command":     "pwd && ls -la",
			"explanation": "Show current directory and list files",
			"mode":        "sync",
		},
		Output: map[string]any{"result": "/Users/me/proj\ntotal 16\n\n\n"},
	}
	got := FormatToolMarkdown(tool)
	for _, want := range []string{
		"Show current directory and list files",
		"```bash\npwd && ls -la\n```",
		"**Result:**",
		"/Users/me/proj",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**Input:**") || strings.Contains(got, "mode") {
		t.Errorf("terminal handler should replace the generic key/value input, got:\n%s", got)
	}

	// Without a command, the handler must defer to the generic rendering.
	noCmd := &schema.ToolInfo{Name: "run_in_terminal", Input: map[string]any{"mode": "sync"}}
	if got := FormatToolMarkdown(noCmd); !strings.Contains(got, "**Input:**") {
		t.Errorf("expected generic fallback without a command, got:\n%s", got)
	}
}

func TestFormatToolMarkdown_TodoHandler(t *testing.T) {
	tool := &schema.ToolInfo{
		Name: "manage_todo_list",
		Input: map[string]any{
			"operation": "write",
			"todoList": []any{
				map[string]any{"title": "Set up sandbox", "status": "completed"},
				map[string]any{"title": "Run browser tools", "status": "in-progress"},
				map[string]any{"title": "Write report", "status": "not-started"},
			},
		},
	}
	got := FormatToolMarkdown(tool)
	for _, want := range []string{
		"- [x] Set up sandbox",
		"- [ ] Run browser tools _(in progress)_",
		"- [ ] Write report",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// A read operation (no todoList) falls back to the generic rendering.
	read := &schema.ToolInfo{Name: "manage_todo_list", Input: map[string]any{"operation": "read"}}
	if got := FormatToolMarkdown(read); !strings.Contains(got, "**Input:**") {
		t.Errorf("expected generic fallback for read operation, got:\n%s", got)
	}
}

func TestFormatToolMarkdown_Empty(t *testing.T) {
	if got := FormatToolMarkdown(&schema.ToolInfo{Name: "MysteryTool"}); got != "" {
		t.Errorf("expected empty markdown for tool without data, got:\n%s", got)
	}
}

// TestFormatToolMarkdown_ResultCap verifies oversized results are truncated (inputs
// are deliberately uncapped).
func TestFormatToolMarkdown_ResultCap(t *testing.T) {
	tool := &schema.ToolInfo{
		Name:   "read_file",
		Output: map[string]interface{}{"result": strings.Repeat("x", toolResultCap+500)},
	}
	got := FormatToolMarkdown(tool)
	if !strings.Contains(got, "… (output truncated)") {
		t.Error("oversized result should be marked truncated")
	}
	if len(got) > toolResultCap+200 {
		t.Errorf("result not capped: len=%d", len(got))
	}

	small := &schema.ToolInfo{Name: "read_file", Output: map[string]interface{}{"result": "short"}}
	if s := FormatToolMarkdown(small); strings.Contains(s, "truncated") {
		t.Errorf("small result should not be truncated: %s", s)
	}
}

// TestCapRunes verifies rune-safe truncation without materializing []rune:
// multi-byte characters are never split, and strings whose byte length exceeds
// the cap but whose rune count doesn't are returned unchanged.
func TestCapRunes(t *testing.T) {
	const marker = "\n… (output truncated)"
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"short ascii unchanged", "hello", 10, "hello"},
		{"ascii truncated", "hello world", 5, "hello" + marker},
		{"multibyte truncated on rune boundary", strings.Repeat("é", 10), 5, strings.Repeat("é", 5) + marker},
		{"more bytes than max but fewer runes", "ééé", 4, "ééé"},
		{"exact rune count unchanged", "héllo", 5, "héllo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capRunes(tt.s, tt.max); got != tt.want {
				t.Errorf("capRunes(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
			}
		})
	}
}

func TestCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"no backticks", "plain text", "```"},
		{"inline code only", "uses `go build` here", "```"},
		{"double backticks", "``x``", "```"},
		{"triple backtick fence", "```sh\nls\n```", "````"},
		{"four backticks", "````\nnested\n````", "`````"},
		{"empty", "", "```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeFence(tt.value); got != tt.want {
				t.Errorf("codeFence(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// TestParseJSONL verifies the incremental update semantics: kind:1 replaces at a key
// path (creating intermediate maps), kind:2 appends array deltas (or replaces when the
// value isn't an array), unknown kinds and malformed lines are skipped, and the final
// typed composer reflects all accumulated updates.
func TestParseJSONL(t *testing.T) {
	lines := []string{
		`{"kind":0,"v":{"version":3,"sessionId":"s-1","requesterUsername":"user","requests":[{"requestId":"r-1","message":{"text":"first"}}]}}`,
		// kind:1 replace of a top-level field
		`{"kind":1,"k":["customTitle"],"v":"My Title"}`,
		// kind:1 creating an intermediate map that wasn't in the snapshot
		`{"kind":1,"k":["inputState","inputText"],"v":"draft"}`,
		// kind:2 array delta: appends to the existing requests
		`{"kind":2,"k":["requests"],"v":[{"requestId":"r-2","message":{"text":"second"}}]}`,
		// kind:2 with a non-array value degrades to replace
		`{"kind":2,"k":["responderUsername"],"v":"GitHub Copilot"}`,
		// unknown kind and malformed line must both be skipped without failing the parse
		`{"kind":9,"k":["requests"],"v":"bogus"}`,
		`not json at all`,
		// later kind:1 overwrites the earlier value
		`{"kind":1,"k":["customTitle"],"v":"Final Title"}`,
	}

	composer, err := parseJSONL([]byte(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("parseJSONL() error = %v", err)
	}

	if composer.SessionID != "s-1" {
		t.Errorf("SessionID = %q, want %q", composer.SessionID, "s-1")
	}
	if composer.CustomTitle != "Final Title" {
		t.Errorf("CustomTitle = %q, want %q", composer.CustomTitle, "Final Title")
	}
	if len(composer.Requests) != 2 {
		t.Fatalf("len(Requests) = %d, want 2", len(composer.Requests))
	}
	if got := composer.Requests[1].Message.Text; got != "second" {
		t.Errorf("Requests[1].Message.Text = %q, want %q", got, "second")
	}
	if composer.ResponderUsername != "GitHub Copilot" {
		t.Errorf("ResponderUsername = %q, want %q", composer.ResponderUsername, "GitHub Copilot")
	}
}

// TestParseJSONL_ArrayIndexPaths verifies key paths containing numeric array indices,
// which VS Code uses for the vast majority of updates on real sessions (observed shape:
// ["requests", <idx>, "<field>"] for kind:1 field updates and kind:2 response-part
// appends). These were previously dropped because the path was decoded as []string.
func TestParseJSONL_ArrayIndexPaths(t *testing.T) {
	lines := []string{
		`{"kind":0,"v":{"version":3,"sessionId":"s-2","requests":[` +
			`{"requestId":"r-1","message":{"text":"first"},"response":[{"value":"partial"}]},` +
			`{"requestId":"r-2","message":{"text":"second"}}]}}`,
		// kind:1 field update inside an indexed request
		`{"kind":1,"k":["requests",0,"modelId"],"v":"gpt-5"}`,
		// kind:1 full replace of an indexed element's response array
		`{"kind":1,"k":["requests",0,"response"],"v":[{"value":"replaced"}]}`,
		// kind:2 append of response parts inside an indexed request
		`{"kind":2,"k":["requests",1,"response"],"v":[{"value":"part one"}]}`,
		`{"kind":2,"k":["requests",1,"response"],"v":[{"value":"part two"}]}`,
		// index == length appends a new element
		`{"kind":1,"k":["requests",2],"v":{"requestId":"r-3","message":{"text":"third"}}}`,
		// index past the end is rejected and skipped without failing the parse
		`{"kind":1,"k":["requests",9,"modelId"],"v":"dropped"}`,
		// string key against an array is rejected and skipped
		`{"kind":1,"k":["requests","notAnIndex"],"v":"dropped"}`,
	}

	composer, err := parseJSONL([]byte(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("parseJSONL() error = %v", err)
	}

	if len(composer.Requests) != 3 {
		t.Fatalf("len(Requests) = %d, want 3", len(composer.Requests))
	}
	if got := composer.Requests[0].ModelID; got != "gpt-5" {
		t.Errorf("Requests[0].ModelID = %q, want %q", got, "gpt-5")
	}
	if got := len(composer.Requests[0].Response); got != 1 {
		t.Errorf("len(Requests[0].Response) = %d, want 1 (replaced, not appended)", got)
	}
	if got := len(composer.Requests[1].Response); got != 2 {
		t.Errorf("len(Requests[1].Response) = %d, want 2 (two appended parts)", got)
	}
	if got := composer.Requests[2].Message.Text; got != "third" {
		t.Errorf("Requests[2].Message.Text = %q, want %q", got, "third")
	}
}

// TestGenerateSlug verifies slugs follow the shared spi.GenerateFilenameFromUserMessage
// convention: punctuation acts as a word separator (not silently dropped), output is
// bounded regardless of title length, and empty candidates fall through to the next
// source (custom title -> name -> first request -> "untitled").
func TestGenerateSlug(t *testing.T) {
	longTitle := strings.Repeat("verylongword ", 40)
	tests := []struct {
		name     string
		composer VSCodeComposer
		want     string
	}{
		{
			name:     "punctuation separates words",
			composer: VSCodeComposer{CustomTitle: "Plan: Replace parser"},
			want:     "plan-replace-parser",
		},
		{
			name:     "long title is bounded",
			composer: VSCodeComposer{CustomTitle: longTitle},
			want:     "verylongword-verylongword-verylongword-verylongword",
		},
		{
			name:     "custom title preferred over name",
			composer: VSCodeComposer{CustomTitle: "Custom Title", Name: "Other Name"},
			want:     "custom-title",
		},
		{
			name:     "punctuation-only title falls through to name",
			composer: VSCodeComposer{CustomTitle: "!!!", Name: "My Session"},
			want:     "my-session",
		},
		{
			name: "falls back to first request message",
			composer: VSCodeComposer{Requests: []VSCodeRequestBlock{
				{Message: VSCodeMessage{Text: "Fix the flaky loader test please"}},
			}},
			want: "fix-the-flaky-loader",
		},
		{
			name:     "nothing available",
			composer: VSCodeComposer{},
			want:     "untitled",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GenerateSlug(tt.composer); got != tt.want {
				t.Errorf("GenerateSlug() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestConvertResponsesToMessages_HiddenToolsKeepSequenceAligned guards the
// order-based fallback matching (UUID-style invocation IDs that can't ID-match):
// metadata's toolCallRounds include hidden tools, so a hidden invocation must
// claim its metadata call (without emitting a message) or every later visible
// tool gets the wrong name/args/results.
func TestConvertResponsesToMessages_HiddenToolsKeepSequenceAligned(t *testing.T) {
	metadata := VSCodeResultMetadata{
		ToolCallRounds: []VSCodeToolCallRound{
			{ToolCalls: []VSCodeToolCallInfo{
				{ID: "call-1", Name: "read_file", Arguments: `{"filePath": "/proj/a.go"}`},
				{ID: "call-2", Name: "get_errors", Arguments: `{}`},
				{ID: "call-3", Name: "create_file", Arguments: `{"filePath": "/proj/b.go"}`},
			}},
		},
	}
	responses := rawMessages(
		`{"kind": "toolInvocationSerialized", "toolCallId": "vs-1"}`,
		`{"kind": "toolInvocationSerialized", "toolCallId": "vs-2", "presentation": "hidden"}`,
		`{"kind": "toolInvocationSerialized", "toolCallId": "vs-3"}`,
	)

	messages, _ := ConvertResponsesToMessages(responses, metadata, "model-x")

	if len(messages) != 2 {
		t.Fatalf("expected 2 visible tool messages, got %d", len(messages))
	}
	if got := messages[0].Tool.Name; got != "read_file" {
		t.Errorf("first visible tool = %q, want %q", got, "read_file")
	}
	// Without the hidden call-2 claiming its metadata call, this would misalign to get_errors.
	if got := messages[1].Tool.Name; got != "create_file" {
		t.Errorf("second visible tool = %q, want %q", got, "create_file")
	}
}

// TestConvertResponsesToMessages_UUIDInvocationCannotStealIDMatchedCall guards
// the two-pass resolution with real-session shape: a hidden UUID-style
// invocation with no metadata call at all (VS Code-internal todo updates)
// precedes ID-matched invocations. In a single in-order pass it would claim the
// first metadata call, mislabeling every tool after it.
func TestConvertResponsesToMessages_UUIDInvocationCannotStealIDMatchedCall(t *testing.T) {
	metadata := VSCodeResultMetadata{
		ToolCallRounds: []VSCodeToolCallRound{
			{ToolCalls: []VSCodeToolCallInfo{
				{ID: "call_LIST__vscode-1", Name: "list_dir", Arguments: `{"path": "/proj"}`},
				{ID: "call_CREATE__vscode-2", Name: "create_file", Arguments: `{"filePath": "/proj/a.txt"}`},
			}},
		},
	}
	responses := rawMessages(
		`{"kind": "toolInvocationSerialized", "toolCallId": "314b5962-uuid", "toolId": "manage_todo_list", "presentation": "hidden"}`,
		`{"kind": "toolInvocationSerialized", "toolCallId": "call_LIST", "toolId": "copilot_listDirectory"}`,
		`{"kind": "toolInvocationSerialized", "toolCallId": "call_CREATE", "toolId": "copilot_createFile"}`,
		`{"kind": "toolInvocationSerialized", "toolCallId": "a5e98c22-uuid", "toolId": "manage_todo_list", "pastTenseMessage": "Created 2 todos"}`,
	)

	messages, _ := ConvertResponsesToMessages(responses, metadata, "model-x")

	if len(messages) != 3 {
		t.Fatalf("expected 3 visible tool messages, got %d", len(messages))
	}
	if got := messages[0].Tool.Name; got != "list_dir" {
		t.Errorf("first tool = %q, want list_dir (exact ID match must win)", got)
	}
	if got := messages[1].Tool.Name; got != "create_file" {
		t.Errorf("second tool = %q, want create_file", got)
	}
	// The metadata-less todo update renders from its own invocation data.
	if got := messages[2].Tool.Name; got != "manage_todo_list" {
		t.Errorf("third tool = %q, want manage_todo_list (invocation-only fallback)", got)
	}
	if md := messages[2].Tool.FormattedMarkdown; md == nil || !strings.Contains(*md, "Created 2 todos") {
		t.Errorf("fallback tool should carry its invocation message, got %v", md)
	}
}

// TestConvertResponsesToMessages_IDMatchAndDedup guards the two behaviors the
// real session data demanded: a metadata ID is the invocation's toolCallId plus
// a "__vscode-<n>" suffix (exact pairing beats position), and VS Code appends a
// fresh serialization of the same invocation per state update (one rendered
// block per call, last serialization's data, first occurrence's position).
func TestConvertResponsesToMessages_IDMatchAndDedup(t *testing.T) {
	metadata := VSCodeResultMetadata{
		ToolCallRounds: []VSCodeToolCallRound{
			{ToolCalls: []VSCodeToolCallInfo{
				{ID: "call_AAA__vscode-100", Name: "read_file", Arguments: `{"filePath": "/proj/a.go"}`},
				{ID: "call_BBB__vscode-101", Name: "create_file", Arguments: `{"filePath": "/proj/b.go"}`},
			}},
		},
	}
	responses := rawMessages(
		// Out of metadata order on purpose: ID matching must pair correctly anyway.
		`{"kind": "toolInvocationSerialized", "toolCallId": "call_BBB"}`,
		`{"value": "Narration between the tools."}`,
		// Same call serialized twice (running, then completed with resultDetails).
		`{"kind": "toolInvocationSerialized", "toolCallId": "call_AAA"}`,
		`{"kind": "toolInvocationSerialized", "toolCallId": "call_AAA", "resultDetails": {"output": [{"type": "embed", "isText": true, "value": "file contents"}]}}`,
	)

	messages, fullText := ConvertResponsesToMessages(responses, metadata, "model-x")

	if len(messages) != 3 {
		t.Fatalf("expected tool + text + tool, got %d messages", len(messages))
	}
	if got := messages[0].Tool.Name; got != "create_file" {
		t.Errorf("first tool = %q, want create_file (ID match, not position)", got)
	}
	if messages[1].Tool != nil || messages[1].Content[0].Text != "Narration between the tools." {
		t.Errorf("middle message should be the narration, got %+v", messages[1])
	}
	if got := messages[2].Tool.Name; got != "read_file" {
		t.Errorf("second tool = %q, want read_file", got)
	}
	if fullText != "Narration between the tools." {
		t.Errorf("fullText = %q", fullText)
	}
}

// TestUriToPath verifies file URI conversion, in particular that percent-encoded
// characters come back decoded exactly once (url.Parse decodes; adding PathUnescape
// on top would corrupt paths with literal % sequences).
func TestUriToPath(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{"plain path", "file:///Users/me/proj", "/Users/me/proj", false},
		{"space decoded", "file:///Users/me/My%20Project", "/Users/me/My Project", false},
		{"unicode decoded", "file:///Users/me/caf%C3%A9", "/Users/me/café", false},
		{"literal percent preserved", "file:///Users/me/literal%2520pct", "/Users/me/literal%20pct", false},
		{"remote SSH URI yields remote path", "vscode-remote://ssh-remote%2Bmyhost/home/me/proj", "/home/me/proj", false},
		{"remote WSL URI yields in-distro path", "vscode-remote://wsl%2Bubuntu/home/me/proj", "/home/me/proj", false},
		{"dev container URI yields container path", "vscode-remote://dev-container%2Babc123/workspaces/proj", "/workspaces/proj", false},
		{"unsupported scheme rejected", "https://example.com/proj", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uriToPath(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("uriToPath(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

// TestBuildToolInfoFromInvocation_PreRenders verifies the forward pass populates
// Summary and FormattedMarkdown so tool payloads survive cross-agent resume.
func TestBuildToolInfoFromInvocation_PreRenders(t *testing.T) {
	// Real sessions: the invocation carries a VS Code UUID while toolCallResults
	// is keyed by the OpenAI-style ID from toolCallRounds — they never match.
	invocation := VSCodeToolInvocationResponse{Kind: "toolInvocationSerialized", ToolCallID: "b4fc9b8c-59db-473d-9f87-16bf9c1bb481"}
	toolCall := VSCodeToolCallInfo{
		ID:        "call_abc__vscode-1763114684819",
		Name:      "create_file",
		Arguments: `{"filePath": "/proj/a.md", "content": "line one\nline two"}`,
	}
	results := map[string]VSCodeToolCallResult{
		"call_abc__vscode-1763114684819": {Content: []VSCodeToolCallContent{{Value: "created"}}},
	}

	toolInfo := BuildToolInfoFromInvocation(invocation, toolCall, results)
	if toolInfo.Summary == nil || *toolInfo.Summary != "Tool use: **create_file**" {
		t.Errorf("summary = %v", toolInfo.Summary)
	}
	if toolInfo.FormattedMarkdown == nil {
		t.Fatal("expected formatted markdown")
	}
	for _, want := range []string{"**Input:**", "line one\nline two", "- filePath: `/proj/a.md`", "**Result:**", "created"} {
		if !strings.Contains(*toolInfo.FormattedMarkdown, want) {
			t.Errorf("missing %q in:\n%s", want, *toolInfo.FormattedMarkdown)
		}
	}
}
