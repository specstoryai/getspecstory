package grokbuild

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFormatToolAsMarkdown_ReadFile(t *testing.T) {
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"target_file": "/Users/dev/project/main.go"},
		Output: map[string]any{"output": "package main\n\nfunc main() {}", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`/Users/dev/project/main.go`") {
		t.Errorf("summary should carry the path: %v", tool.Summary)
	}
	if !strings.Contains(md, "```go\npackage main") {
		t.Errorf("file content should be fenced with the file's language:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ShellCommand(t *testing.T) {
	tool := &ToolInfo{
		Name: "run_terminal_command",
		Type: "shell",
		Input: map[string]any{
			"command":     "python3 -m pytest",
			"description": "Run the tests",
		},
		Output: map[string]any{"output": "2 passed\n1 skipped", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Run the tests") {
		t.Errorf("description missing:\n%s", md)
	}
	if !strings.Contains(md, "```bash\npython3 -m pytest\n```") {
		t.Errorf("command should be fenced as bash:\n%s", md)
	}
	if !strings.Contains(md, "Result:\n```text\n2 passed") {
		t.Errorf("output should be fenced:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_SearchReplaceRendersDiff(t *testing.T) {
	tool := &ToolInfo{
		Name: "search_replace",
		Type: "write",
		Input: map[string]any{
			"file_path":  "/Users/dev/project/calc.py",
			"old_string": "def sub(a, b):",
			"new_string": "def sub(a, b):\n    return a - b",
		},
		Output: map[string]any{"output": "updated", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Path: `/Users/dev/project/calc.py`") {
		t.Errorf("path missing:\n%s", md)
	}
	if !strings.Contains(md, "```diff") {
		t.Errorf("edit should render as a diff:\n%s", md)
	}
	if !strings.Contains(md, "-def sub(a, b):") || !strings.Contains(md, "+    return a - b") {
		t.Errorf("diff should show both sides:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_ErrorTakesPriority(t *testing.T) {
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"target_file": "/missing.txt"},
		Output: map[string]any{"output": "File not found: /missing.txt", "status": "error"},
	}

	md := formatToolAsMarkdown(tool)

	// A failed call must be visibly different from a successful one, because
	// Grok records the failure only in events.jsonl.
	if !strings.Contains(md, "Error: File not found: /missing.txt") {
		t.Errorf("failure should be labelled as an error:\n%s", md)
	}
	if strings.Contains(md, "Result:") {
		t.Errorf("a failed call should not be labelled as a result:\n%s", md)
	}
	// A failed read must not be dressed up as file content.
	if strings.Contains(md, "```txt") {
		t.Errorf("failed read should not render as source:\n%s", md)
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
		t.Errorf("completed item missing:\n%s", md)
	}
	if !strings.Contains(md, "- [⚡] Second step") {
		t.Errorf("in-progress item missing:\n%s", md)
	}
	if !strings.Contains(md, "- [ ] Third step") {
		t.Errorf("pending item missing:\n%s", md)
	}
	if strings.Contains(md, "Todos updated.") {
		t.Errorf("the checklist is the whole story, so the result should be suppressed:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_TodoMergeUpdate(t *testing.T) {
	// Grok sends incremental updates carrying only an id and a status. The
	// parser backfills the text, but an item it never saw must still render as
	// something a reader can identify.
	tool := &ToolInfo{
		Name: "todo_write",
		Type: "task",
		Input: map[string]any{
			"merge": true,
			"todos": []any{
				map[string]any{"id": "3", "content": "Run the tests", "status": "completed"},
				map[string]any{"id": "4", "status": "in_progress"},
			},
		},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "Todo update:") {
		t.Errorf("a merge update should be labelled as an update:\n%s", md)
	}
	if !strings.Contains(md, "- [x] Run the tests") {
		t.Errorf("backfilled item text missing:\n%s", md)
	}
	if !strings.Contains(md, "- [⚡] (item 4)") {
		t.Errorf("an unknown item should be named by id, not left blank:\n%s", md)
	}
}

func TestBackfillTodoText(t *testing.T) {
	seen := map[string]string{}

	first := map[string]any{"todos": []any{
		map[string]any{"id": "1", "content": "First step", "status": "in_progress"},
		map[string]any{"id": "2", "content": "Second step", "status": "pending"},
	}}
	backfillTodoText(first, seen)

	update := map[string]any{"merge": true, "todos": []any{
		map[string]any{"id": "1", "status": "completed"},
		map[string]any{"id": "2", "status": "in_progress"},
	}}
	backfillTodoText(update, seen)

	todos := update["todos"].([]any)
	if got := todos[0].(map[string]any)["content"]; got != "First step" {
		t.Errorf("item 1 content = %v, want the text from the first call", got)
	}
	if got := todos[1].(map[string]any)["content"]; got != "Second step" {
		t.Errorf("item 2 content = %v, want the text from the first call", got)
	}
}

func TestFormatToolAsMarkdown_ListDirUsesTargetDirectory(t *testing.T) {
	// Grok names this argument target_directory, not path.
	tool := &ToolInfo{
		Name:   "list_dir",
		Type:   "shell",
		Input:  map[string]any{"target_directory": "src"},
		Output: map[string]any{"output": "- src/\n  - calc.py", "status": "success"},
	}

	formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`src`") {
		t.Errorf("summary should carry the listed directory: %v", tool.Summary)
	}
}

func TestFormatToolAsMarkdown_UseToolUnwrapsMCP(t *testing.T) {
	tool := &ToolInfo{
		Name: "use_tool",
		Type: "generic",
		Input: map[string]any{
			"tool_name":  "voice__list_voices",
			"tool_input": map[string]any{"limit": float64(3)},
		},
		Output: map[string]any{"output": "voices: alpha, bravo", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	// Grok dispatches every MCP tool through use_tool, so the inner name is the
	// only thing that tells a reader what actually ran.
	if tool.Summary == nil || !strings.Contains(*tool.Summary, "`voice__list_voices`") {
		t.Errorf("summary should name the inner MCP tool: %v", tool.Summary)
	}
	if !strings.Contains(md, "MCP tool: `voice__list_voices`") {
		t.Errorf("body should name the inner tool:\n%s", md)
	}
	if !strings.Contains(md, "\"limit\": 3") {
		t.Errorf("inner tool input missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_SubagentShowsPrompt(t *testing.T) {
	tool := &ToolInfo{
		Name: "spawn_subagent",
		Type: "task",
		Input: map[string]any{
			"description":   "Count files",
			"prompt":        "Count the files in /tmp/project and report the total.",
			"subagent_type": "general-purpose",
		},
		Output: map[string]any{"output": "There are 3 files.", "status": "success", "subagentStatus": "completed"},
	}

	md := formatToolAsMarkdown(tool)

	if tool.Summary == nil || !strings.Contains(*tool.Summary, "Count files") {
		t.Errorf("summary should carry the description: %v", tool.Summary)
	}
	if !strings.Contains(md, "Subagent: `general-purpose`") {
		t.Errorf("subagent type missing:\n%s", md)
	}
	// The subagent's own transcript is a session we deliberately skip, so this
	// prompt is the only record of what it was asked to do.
	if !strings.Contains(md, "Count the files in /tmp/project") {
		t.Errorf("subagent prompt missing:\n%s", md)
	}
	if !strings.Contains(md, "Result: There are 3 files.") {
		t.Errorf("subagent result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_WebAndSearchSummaries(t *testing.T) {
	tests := []struct {
		name string
		tool *ToolInfo
		want string
	}{
		{
			name: "web_search",
			tool: &ToolInfo{Name: "web_search", Type: "search", Input: map[string]any{"query": "grok release"}},
			want: "`grok release`",
		},
		{
			name: "web_fetch",
			tool: &ToolInfo{Name: "web_fetch", Type: "read", Input: map[string]any{"url": "https://x.ai/news"}},
			want: "`https://x.ai/news`",
		},
		{
			name: "grep",
			tool: &ToolInfo{Name: "grep", Type: "search", Input: map[string]any{"pattern": "func main", "path": "/src"}},
			want: "`func main` in `/src`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatToolAsMarkdown(tt.tool)
			if tt.tool.Summary == nil || !strings.Contains(*tt.tool.Summary, tt.want) {
				t.Errorf("summary = %v, want it to contain %q", tt.tool.Summary, tt.want)
			}
		})
	}
}

func TestFormatToolAsMarkdown_UnknownToolShowsJSON(t *testing.T) {
	tool := &ToolInfo{
		Name:   "scheduler_create",
		Type:   "generic",
		Input:  map[string]any{"cron": "0 9 * * *", "task": "daily report"},
		Output: map[string]any{"output": "created", "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if !strings.Contains(md, "```json") || !strings.Contains(md, `"cron": "0 9 * * *"`) {
		t.Errorf("unrecognized tool should show its input as JSON:\n%s", md)
	}
	if !strings.Contains(md, "Result: created") {
		t.Errorf("result missing:\n%s", md)
	}
}

func TestFormatToolAsMarkdown_KeepsLargeOutputWhole(t *testing.T) {
	// Tool output is the record the user came for, so it is not truncated.
	huge := strings.Repeat("x", 2500)
	tool := &ToolInfo{
		Name:   "read_file",
		Type:   "read",
		Input:  map[string]any{"target_file": "/big.txt"},
		Output: map[string]any{"output": huge, "status": "success"},
	}

	md := formatToolAsMarkdown(tool)

	if strings.Contains(md, "(truncated)") {
		t.Error("file content should be kept whole")
	}
	if !strings.Contains(md, huge) {
		t.Error("the full output should appear in the markdown")
	}
}

func TestFormatToolAsMarkdown_PreservesFullEditInputs(t *testing.T) {
	huge := strings.Repeat("y", 2500)
	tool := &ToolInfo{
		Name: "search_replace",
		Type: "write",
		Input: map[string]any{
			"file_path":  "/big.py",
			"old_string": huge,
			"new_string": huge,
		},
	}

	md := formatToolAsMarkdown(tool)

	if strings.Contains(md, "(truncated)") || !strings.Contains(md, "-"+huge) || !strings.Contains(md, "+"+huge) {
		t.Error("edit input was truncated")
	}
}

func TestFormatToolAsMarkdown_Nil(t *testing.T) {
	if got := formatToolAsMarkdown(nil); got != "" {
		t.Errorf("nil tool should render empty, got %q", got)
	}
}

func TestNative134ToolInputsAndOrder(t *testing.T) {
	session := loadFixture(t, "session-1.0.34")
	data, err := GenerateAgentSession(session, "/qa/project space_under")
	if err != nil {
		t.Fatal(err)
	}
	var nativeIDs []string
	for _, record := range session.Records {
		for _, call := range record.ToolCalls {
			nativeIDs = append(nativeIDs, call.ID)
		}
		if record.Kind != nil {
			nativeIDs = append(nativeIDs, record.Kind.ID)
		}
	}
	var renderedIDs []string
	for _, exchange := range data.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool == nil {
				continue
			}
			renderedIDs = append(renderedIDs, msg.Tool.UseID)
			md := *msg.Tool.FormattedMarkdown
			switch msg.Tool.Name {
			case "read_file":
				if _, ok := msg.Tool.Input["offset"]; ok {
					for _, want := range []string{"Offset: `1`", "Limit: `15`"} {
						if !strings.Contains(md, want) {
							t.Errorf("read omitted %q: %s", want, md)
						}
					}
				}
			case "grep":
				for _, want := range []string{"Glob: `*.txt`", "-i: `true`", "-C: `1`", "Result limit: `20`", "<workspace_result"} {
					if !strings.Contains(md, want) {
						t.Errorf("grep omitted %q: %s", want, md)
					}
				}
			case "run_terminal_command":
				if msg.Tool.Input["background"] == true && !strings.Contains(md, "Background: `true`") {
					t.Error("background setting omitted")
				}
			case "monitor":
				if !strings.Contains(md, "Timeout (ms): `15000`") || !strings.Contains(md, "Persistent: `false`") {
					t.Error("monitor options omitted")
				}
			}
		}
	}
	if !reflect.DeepEqual(renderedIDs, nativeIDs) {
		t.Fatalf("native order/identity mismatch: %v, want %v", renderedIDs, nativeIDs)
	}
}

func TestSpecializedToolKeepsUnknownArguments(t *testing.T) {
	for _, name := range []string{"read_file", "write", "search_replace", "run_terminal_command", "monitor", "todo_write", "spawn_subagent", "use_tool", "image_gen", "web_search"} {
		t.Run(name, func(t *testing.T) {
			tool := &ToolInfo{Name: name, Input: map[string]any{"new_option": "KEEP-THIS"}}
			if md := formatToolAsMarkdown(tool); !strings.Contains(md, "KEEP-THIS") {
				t.Fatalf("unknown input lost: %s", md)
			}
		})
	}
}

func TestToolParametersPreserveJSONValues(t *testing.T) {
	for _, name := range []string{"read_file", "run_terminal_command"} {
		for _, tc := range []struct {
			name  string
			value any
			want  string
		}{
			{"null", nil, "null"},
			{"empty string", "", ""},
			{"false", false, "false"},
			{"zero", float64(0), "0"},
			{"array", []any{nil, false}, "[null,false]"},
			{"object", map[string]any{"nested": nil}, `{"nested":null}`},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				tool := &ToolInfo{
					Name:   name,
					Input:  map[string]any{"new_option": tc.value},
					Output: map[string]any{"new_result": tc.value},
				}
				md := formatToolAsMarkdown(tool)
				for _, key := range []string{"new_option", "new_result"} {
					if want := key + ": `" + tc.want + "`"; !strings.Contains(md, want) {
						t.Errorf("missing %q in rendered tool:\n%s", want, md)
					}
				}
			})
		}
	}
}

func TestDeclaredToolInventoryAlwaysRenders(t *testing.T) {
	inventory, err := os.ReadFile(filepath.Join("testdata", "session-1.0.34", "tools.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Fields(string(inventory)) {
		t.Run(name, func(t *testing.T) {
			tool := &ToolInfo{Name: name, Input: map[string]any{"unfamiliar_parameter": "preserve me"}, Output: map[string]any{"output": "native failure detail", "status": "error"}}
			rendered := formatToolAsMarkdown(tool)
			if !strings.Contains(rendered, "preserve me") || !strings.Contains(rendered, "Error: native failure detail") {
				t.Fatalf("inventory tool drops unknown parameters or errors: %s", rendered)
			}
		})
	}
}

func TestResultMetadataAndControlBytes(t *testing.T) {
	tool := &ToolInfo{Name: "spawn_subagent", Output: map[string]any{"output": "\x1b[31mfinished\x1b[0m\x00\n✓", "subagentStatus": "completed", "durationMs": 1000}}
	md := formatToolAsMarkdown(tool)
	for _, want := range []string{"finished", "✓", "completed", "1000"} {
		if !strings.Contains(md, want) {
			t.Errorf("result omitted %q: %s", want, md)
		}
	}
	if strings.ContainsAny(md, "\x1b\x00") {
		t.Fatal("terminal controls leaked into markdown")
	}
	tool = &ToolInfo{Name: "web_search", Output: map[string]any{"status": "in_progress"}}
	if !strings.Contains(formatToolAsMarkdown(tool), "in_progress") {
		t.Fatal("pending backend tool status lost")
	}
}

func TestAdditionalNative134Tools(t *testing.T) {
	for _, name := range []string{"discovery", "headless", "scheduler"} {
		t.Run(name, func(t *testing.T) {
			session := loadFixture(t, "session-1.0.34/"+name)
			data, err := GenerateAgentSession(session, session.Cwd)
			if err != nil {
				t.Fatal(err)
			}
			var want, got []string
			for _, r := range session.Records {
				for _, call := range r.ToolCalls {
					want = append(want, call.ID)
				}
			}
			for _, e := range data.Exchanges {
				for _, m := range e.Messages {
					if m.Tool == nil {
						continue
					}
					got = append(got, m.Tool.UseID)
					md := *m.Tool.FormattedMarkdown
					switch m.Tool.Name {
					case "search_tool":
						for _, text := range []string{"Discovered tools:", "Server:", "input_schema", "total_hidden_tools"} {
							if !strings.Contains(md, text) {
								t.Errorf("discovery dropped %q", text)
							}
						}
					case "ask_user_question":
						for _, text := range []string{"Question 1: Is this a headless QA session?", "- Yes:", "- No:", "No user is available"} {
							if !strings.Contains(md, text) {
								t.Errorf("question dropped %q", text)
							}
						}
					case "workflow":
						if m.Tool.Output["status"] != "error" || !strings.Contains(md, "Error: User cancelled") || !strings.Contains(md, "SPECSTORY_QA_NONEXISTENT") {
							t.Errorf("cancellation misrepresented: %s", md)
						}
					case "scheduler_list":
						if !strings.Contains(md, "No scheduled tasks.") {
							t.Error("empty scheduler result missing")
						}
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("native order changed: %v, want %v", got, want)
			}
		})
	}
}

func TestTodoItemsRetainUnfamiliarData(t *testing.T) {
	tool := &ToolInfo{Name: "todo_write", Input: map[string]any{"todos": []any{
		map[string]any{"id": "one", "content": "Known task", "status": "pending", "priority": "KEEP-PRIORITY"},
		map[string]any{"id": "two", "content": "Blocked task", "status": "awaiting-external-review"},
		"UNFAMILIAR-ITEM",
	}}}
	rendered := formatToolAsMarkdown(tool)
	for _, want := range []string{"- [ ] Known task", "KEEP-PRIORITY", "awaiting-external-review", "UNFAMILIAR-ITEM"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("lost %q in %s", want, rendered)
		}
	}
}

func TestToolResultsPreserveWhitespace(t *testing.T) {
	for _, name := range []string{"read_file", "run_terminal_command", "unfamiliar_tool"} {
		for _, status := range []string{"success", "error"} {
			for _, native := range []string{"  indented\ntrailing  \n\n", "\t  "} {
				tool := &ToolInfo{Name: name, Input: map[string]any{"target_file": "snippet.txt"}, Output: map[string]any{"status": status, "output": "\x1b[31m" + native + "\x1b[0m"}}
				md := formatToolAsMarkdown(tool)
				if !strings.Contains(md, "\n"+native+"\n") || strings.ContainsRune(md, '\x1b') {
					t.Errorf("%s/%s changed native whitespace %q: %q", name, status, native, md)
				}
			}
		}
	}
}
