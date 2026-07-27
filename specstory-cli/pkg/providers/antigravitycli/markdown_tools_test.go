package antigravitycli

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Every one of Antigravity's 19 real tools, plus an unknown one. The list is the
// agent's own enumeration of its tool set (docs/ANTIGRAVITY-FORMAT.md
// §3.5), so this doubles as the record of which names actually exist: a name not
// in this table should not be special-cased anywhere in the provider.
func TestClassifyToolType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"write_to_file", schema.ToolTypeWrite},
		{"replace_file_content", schema.ToolTypeWrite},
		{"multi_replace_file_content", schema.ToolTypeWrite},
		{"view_file", schema.ToolTypeRead},
		{"list_dir", schema.ToolTypeRead},
		{"grep_search", schema.ToolTypeSearch},
		{"search_web", schema.ToolTypeSearch},
		{"read_url_content", schema.ToolTypeSearch},
		{"run_command", schema.ToolTypeShell},
		{"manage_task", schema.ToolTypeTask},
		{"schedule", schema.ToolTypeTask},
		{"ask_permission", schema.ToolTypeGeneric},
		{"ask_question", schema.ToolTypeGeneric},
		{"define_subagent", schema.ToolTypeGeneric},
		{"generate_image", schema.ToolTypeGeneric},
		{"invoke_subagent", schema.ToolTypeGeneric},
		{"list_permissions", schema.ToolTypeGeneric},
		{"manage_subagents", schema.ToolTypeGeneric},
		{"send_message", schema.ToolTypeGeneric},
		{"some_future_tool", schema.ToolTypeGeneric},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyToolType(tt.name); got != tt.want {
				t.Errorf("classifyToolType(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestFormatToolCall_RunCommandTwoPhase(t *testing.T) {
	tool := &ToolInfo{
		Name:  "run_command",
		Type:  schema.ToolTypeShell,
		Input: map[string]any{"CommandLine": "git status", "Cwd": "/proj"},
	}

	// Phase 1: input only, no output section.
	phase1 := formatToolCall(tool)
	if !strings.Contains(phase1, "git status") || !strings.Contains(phase1, "/proj") {
		t.Errorf("phase 1 missing command/dir: %q", phase1)
	}
	if strings.Contains(phase1, "Output:") {
		t.Errorf("phase 1 should not contain output: %q", phase1)
	}

	// Phase 2: after output attached.
	tool.Output = map[string]any{"content": "On branch dev\nnothing to commit"}
	phase2 := formatToolCall(tool)
	if !strings.Contains(phase2, "Output:") || !strings.Contains(phase2, "nothing to commit") {
		t.Errorf("phase 2 missing output: %q", phase2)
	}
}

func TestRenderInputs(t *testing.T) {
	if got := renderReadInput(map[string]any{"AbsolutePath": "file:///proj/main.go"}); !strings.Contains(got, "/proj/main.go") || strings.Contains(got, "file://") {
		t.Errorf("renderReadInput stripped file:// and shows path: got %q", got)
	}
	if got := renderGrepInput(map[string]any{"Query": "TODO", "SearchPath": "/proj"}); !strings.Contains(got, "TODO") || !strings.Contains(got, "/proj") {
		t.Errorf("renderGrepInput = %q", got)
	}
	if got := renderWriteInput(map[string]any{"TargetFile": "/proj/x.go", "CodeContent": "package main"}); !strings.Contains(got, "/proj/x.go") || !strings.Contains(got, "package main") {
		t.Errorf("renderWriteInput = %q", got)
	}
	edit := renderEditInput(map[string]any{"TargetFile": "/proj/x.go", "TargetContent": "old", "ReplacementContent": "new"})
	if !strings.Contains(edit, "```diff") || !strings.Contains(edit, "-old") || !strings.Contains(edit, "+new") {
		t.Errorf("renderEditInput should produce a diff: %q", edit)
	}
}

func TestRenderHighValueInputs(t *testing.T) {
	// search_web / read_url_content dispatch by the REAL tool names (previously
	// keyed to websearch/readurl and silently falling to generic JSON).
	if got := formatToolInput(&ToolInfo{Name: "search_web", Input: map[string]any{"query": "golang generics"}}); !strings.Contains(got, "Query: `golang generics`") {
		t.Errorf("search_web input = %q", got)
	}
	if got := formatToolInput(&ToolInfo{Name: "read_url_content", Input: map[string]any{"Url": "https://example.com"}}); !strings.Contains(got, "URL: `https://example.com`") {
		t.Errorf("read_url_content input = %q", got)
	}

	// generate_image: name, aspect ratio, and prompt.
	img := renderGenerateImageInput(map[string]any{"ImageName": "logo", "AspectRatio": "1:1", "Prompt": "a neon icon"})
	if !strings.Contains(img, "`logo`") || !strings.Contains(img, "1:1") || !strings.Contains(img, "a neon icon") {
		t.Errorf("generate_image input = %q", img)
	}

	// multi_replace_file_content: a diff block per chunk.
	multi := renderMultiEditInput(map[string]any{
		"TargetFile": "/x/demo.txt",
		"ReplacementChunks": []any{
			map[string]any{"TargetContent": "Line 1", "ReplacementContent": "Line 1 Mod"},
			map[string]any{"TargetContent": "Line 4", "ReplacementContent": "Line 4 Mod"},
		},
	})
	if strings.Count(multi, "```diff") != 2 || !strings.Contains(multi, "-Line 1") || !strings.Contains(multi, "+Line 4 Mod") {
		t.Errorf("multi_replace input = %q", multi)
	}

	// invoke_subagent: role, type, model, prompt on one bullet.
	inv := renderInvokeSubagentInput(map[string]any{"Subagents": []any{
		map[string]any{"Role": "Demo Assistant", "TypeName": "demo_helper", "Model": "flash", "Prompt": "hi"},
	}})
	if !strings.Contains(inv, "**Demo Assistant**") || !strings.Contains(inv, "`demo_helper`") || !strings.Contains(inv, "model `flash`") {
		t.Errorf("invoke_subagent input = %q", inv)
	}

	// manage_subagents kill: action + quoted conversation ids from a list.
	mgr := renderManageInput(map[string]any{"Action": "kill", "ConversationIds": []any{"abc", "def"}}, "Conversations", "ConversationIds")
	if !strings.Contains(mgr, "Action: `kill`") || !strings.Contains(mgr, "`abc`, `def`") {
		t.Errorf("manage_subagents input = %q", mgr)
	}

	// ask_question: bold question + option bullets.
	q := renderAskQuestionInput(map[string]any{"questions": []any{
		map[string]any{"question": "OK?", "options": []any{"Yes", "No"}},
	}})
	if !strings.Contains(q, "**OK?**") || !strings.Contains(q, "- Yes") || !strings.Contains(q, "- No") {
		t.Errorf("ask_question input = %q", q)
	}

	// list_permissions has no meaningful args → empty input (output stands alone).
	if got := formatToolInput(&ToolInfo{Name: "list_permissions", Input: map[string]any{"toolAction": "x", "toolSummary": "y"}}); got != "" {
		t.Errorf("list_permissions input should be empty, got %q", got)
	}
}

func TestRenderGenericJSON_DropsMetaKeys(t *testing.T) {
	// toolAction/toolSummary are agy-injected labels, not args; the generic
	// fallback must omit them (and render nothing when they are all that's left).
	if got := renderGenericJSON(map[string]any{"toolAction": "x", "toolSummary": "y"}); got != "" {
		t.Errorf("expected empty for meta-only args, got %q", got)
	}
	got := renderGenericJSON(map[string]any{"toolAction": "x", "Real": "keep"})
	if strings.Contains(got, "toolAction") || !strings.Contains(got, "Real") {
		t.Errorf("expected meta dropped and real key kept, got %q", got)
	}
}

func TestCleanResultContent_StripsHeaderAndBoilerplate(t *testing.T) {
	raw := "Created At: 2026-07-24T11:42:45-04:00\n" +
		"Completed At: 2026-07-24T11:42:51-04:00\n" +
		"The following changes were made by the replace_file_content tool to: /x/demo.txt. If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.\n" +
		"[diff_block_start]\n@@ -1,1 +1,1 @@\n-a\n+b\n[diff_block_end]\n" +
		"Please note that the above snippet only shows the MODIFIED lines from the last change. It shows up to 3 lines of unchanged lines before and after the modified lines. The actual file contents may have many more lines not shown."
	got := cleanResultContent(transcriptStep{Content: raw})
	if strings.Contains(got, "Created At:") || strings.Contains(got, "Completed At:") {
		t.Errorf("timing header not stripped: %q", got)
	}
	if strings.Contains(got, "If relevant, proactively") || strings.Contains(got, "Please note that the above snippet") {
		t.Errorf("boilerplate not stripped: %q", got)
	}
	if !strings.Contains(got, "[diff_block_start]") || !strings.Contains(got, "+b") {
		t.Errorf("real content lost: %q", got)
	}
}

func TestCleanResultContent_TabHandling(t *testing.T) {
	// RUN_COMMAND: agy uniformly tab-indents the output block (spec §3.9). Only
	// that shared indent may be stripped — the deeper tab here is the command's
	// real output (a printed Makefile rule) and must survive.
	run := "Created At: 2026-07-24T11:00:00Z\n\n" +
		"\t\t\tThe command completed successfully.\n" +
		"\t\t\tOutput:\n" +
		"\t\t\tbuild:\n" +
		"\t\t\t\tgo build ./..."
	got := cleanResultContent(transcriptStep{Type: typeRunCommand, Content: run})
	if strings.Contains(got, "\t\t") {
		t.Errorf("shared indent not stripped: %q", got)
	}
	if !strings.Contains(got, "\n\tgo build ./...") {
		t.Errorf("tab belonging to command output was lost: %q", got)
	}

	// Non-RUN_COMMAND results are not indented by agy, so leading tabs are file
	// content and must pass through untouched.
	view := "build:\n\tgo build ./..."
	if got := cleanResultContent(transcriptStep{Type: typeViewFile, Content: view}); got != view {
		t.Errorf("view_file content altered: got %q, want %q", got, view)
	}
}

func TestCodeFence_OutrunsEmbeddedBackticks(t *testing.T) {
	// A written markdown file containing its own ``` fence: the wrapper fence
	// must be longer so the block doesn't terminate early, and the body must not
	// be altered (backslash-escaping backticks inside a fence does nothing).
	got := codeFence("markdown", "# Doc\n```go\nx := 1\n```")
	if !strings.HasPrefix(got, "````markdown\n") || !strings.HasSuffix(got, "\n````") {
		t.Errorf("fence should be one backtick longer than embedded runs: %q", got)
	}
	if !strings.Contains(got, "```go") {
		t.Errorf("body must be unaltered: %q", got)
	}

	// No backticks in the body → the standard three-backtick fence.
	if got := codeFence("diff", "-a\n+b"); !strings.HasPrefix(got, "```diff\n") || !strings.HasSuffix(got, "\n```") {
		t.Errorf("plain body should use the standard fence: %q", got)
	}
}

func TestFormatSubagentOutputs(t *testing.T) {
	// invoke_subagent: summarize to the conversation id, drop log URI / workspace.
	invoke := &ToolInfo{Name: "invoke_subagent", Output: map[string]any{"content": "Created the following subagents:\n{\n  \"conversationId\":  \"abc-123\",\n  \"logAbsoluteUri\":  \"file:///x/transcript.jsonl\",\n  \"workspaceUris\":  [\"file:///x\"]\n}"}}
	got := formatToolOutput(invoke)
	if !strings.Contains(got, "conversation `abc-123`") {
		t.Errorf("invoke_subagent output = %q", got)
	}
	if strings.Contains(got, "logAbsoluteUri") || strings.Contains(got, "workspaceUris") {
		t.Errorf("invoke_subagent output should drop internal fields: %q", got)
	}

	// manage_subagents list: role + type + conversation id, drop model/tier/prompt.
	list := &ToolInfo{Name: "manage_subagents", Output: map[string]any{"content": "You have 1 active subagent(s):\n{\n  \"spec\":  {\n    \"typeName\":  \"demo_helper\",\n    \"role\":  \"Demo Assistant\",\n    \"model\":  \"MODEL_PLACEHOLDER_M196\",\n    \"modelTier\":  \"MODEL_TIER_FLASH\"\n  },\n  \"result\":  {\n    \"conversationId\":  \"abc-123\"\n  }\n}"}}
	got = formatToolOutput(list)
	if !strings.Contains(got, "**Demo Assistant**") || !strings.Contains(got, "`demo_helper`") || !strings.Contains(got, "conversation `abc-123`") {
		t.Errorf("manage_subagents list output = %q", got)
	}
	if strings.Contains(got, "MODEL_PLACEHOLDER") || strings.Contains(got, "modelTier") {
		t.Errorf("manage_subagents output should drop model internals: %q", got)
	}

	// manage_subagents kill: plain text (no JSON) falls back to the raw output.
	kill := &ToolInfo{Name: "manage_subagents", Output: map[string]any{"content": "Successfully killed 1 subagent(s) and their descendants.\nKilled roles: Demo Assistant"}}
	got = formatToolOutput(kill)
	if !strings.Contains(got, "Successfully killed") {
		t.Errorf("manage_subagents kill output = %q", got)
	}
}

func TestFormatDiffBlockOutput(t *testing.T) {
	// An edit-tool result: lead sentence + [diff_block_start]…[diff_block_end].
	tool := &ToolInfo{Name: "replace_file_content", Output: map[string]any{"content": "The following changes were made by the replace_file_content tool to: /x/demo.txt.\n[diff_block_start]\n@@ -1,2 +1,2 @@\n-Line 2: Beta\n+Line 2: Beta Updated\n[diff_block_end]"}}
	got := formatToolOutput(tool)
	if !strings.Contains(got, "```diff") {
		t.Errorf("expected a diff fence, got %q", got)
	}
	if strings.Contains(got, "[diff_block_start]") || strings.Contains(got, "[diff_block_end]") {
		t.Errorf("native diff markers should be removed, got %q", got)
	}
	if strings.Contains(got, "The following changes were made") {
		t.Errorf("redundant lead line should be dropped, got %q", got)
	}
	if !strings.Contains(got, "@@ -1,2 +1,2 @@") || !strings.Contains(got, "+Line 2: Beta Updated") {
		t.Errorf("diff body/header should be preserved, got %q", got)
	}

	// No diff markers → falls back to the raw output block.
	plain := &ToolInfo{Name: "run_command", Output: map[string]any{"content": "line1\nline2"}}
	if got := formatToolOutput(plain); !strings.Contains(got, "```text") {
		t.Errorf("non-diff output should use a text block, got %q", got)
	}
}

func TestExtractPathHints(t *testing.T) {
	hints := extractPathHints(map[string]any{"AbsolutePath": "file:///proj/main.go"}, "/proj")
	if len(hints) == 0 {
		t.Fatalf("expected a path hint")
	}
	for _, h := range hints {
		if strings.Contains(h, "file://") {
			t.Errorf("path hint should not contain file:// scheme: %q", h)
		}
	}

	// Shell command path hints come from the command line.
	shellHints := extractPathHints(map[string]any{"CommandLine": "cat /proj/notes.txt", "Cwd": "/proj"}, "/proj")
	if len(shellHints) == 0 {
		t.Errorf("expected shell command path hints")
	}
}
