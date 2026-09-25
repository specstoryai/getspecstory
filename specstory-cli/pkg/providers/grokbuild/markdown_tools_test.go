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
		Name:   "future_tool",
		Type:   "unknown",
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
				for _, want := range []string{"Glob: `*.txt`", "-i: `true`", "-C: `1`", "Result limit: `20`"} {
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
						for _, text := range []string{"Discovered tools:", "Server:", "Parameters:", "Hidden tools:"} {
							if !strings.Contains(md, text) {
								t.Errorf("discovery dropped %q", text)
							}
						}
						if strings.Contains(md, "```json") || strings.Contains(md, "input_schema") {
							t.Errorf("discovery fell back to JSON: %s", md)
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

// TestNative140ToolRendering checks each renderer against payloads recorded by
// Grok Build 1.0.40, trimmed to the parts the renderer reads. None of these
// tools may fall back to a JSON block for its known arguments.
func TestNative140ToolRendering(t *testing.T) {
	const imagePath = "/Users/qa/.grok/sessions/%2Fqa/01a0c98a/images/1.jpg"
	const reminder = "\n\n<system-reminder>\nBackground subagent \"01a0\" completed successfully.\n</system-reminder>"
	tests := []struct {
		name        string
		tool        *ToolInfo
		wantSummary string
		want        []string
		notWant     []string
	}{
		{
			name: "search_tool lists parameters instead of schemas",
			tool: &ToolInfo{Name: "search_tool", Input: map[string]any{"limit": float64(3), "query": "tasks list"}, Output: map[string]any{"status": "success", "output": `{
  "results": [{"server": "tasks", "tools": [{
    "tool_name": "tasks__list_trigger_resources",
    "description": "List selectable resources.\n\nUse this when authoring an automation.",
    "score": 5.75,
    "input_schema": {"type": "object", "additionalProperties": false, "required": ["provider", "resource_type"], "properties": {
      "provider": {"type": "string", "description": "Trigger provider wire tag."},
      "resource_type": {"type": "string", "enum": ["repository", "branch"], "description": "Resource kind to list."},
      "repo_ids": {"type": "array", "items": {"type": "string"}, "description": "Repository ids."},
      "page_token": {"anyOf": [{"type": "string"}, {"type": "null"}], "default": null},
      "dimensions": {"type": "object", "properties": {"from": {"type": ["string", "array"]}}}
    }}
  }]}],
  "note": null, "status": "ready", "total_hidden_tools": 209
}`}},
			wantSummary: "Tool use: **search_tool** `tasks list`",
			want: []string{
				"Limit: `3`\nQuery: `tasks list`",
				"**tasks__list_trigger_resources**\n\nList selectable resources.\n\nUse this when authoring an automation.",
				"- `provider` (string, required): Trigger provider wire tag.\n- `resource_type` (string, required): Resource kind to list. One of: `repository`, `branch`.",
				"- `repo_ids` (array of string): Repository ids.",
				"- `page_token` (string | null); default: `null`",
				"- `dimensions` (object)\n  - `from` (string | array)",
				"additionalProperties: `false`",
				"Status: `ready`\nHidden tools: `209`",
			},
			notWant: []string{"```json", "input_schema", "score", "5.75", "Note:"},
		},
		{
			name:        "image_gen shows the prompt and the saved path",
			tool:        &ToolInfo{Name: "image_gen", Input: map[string]any{"aspect_ratio": "1:1", "prompt": "A small round blue ceramic teapot"}, Output: map[string]any{"status": "success", "output": `{"path":"` + imagePath + `","filename":"1.jpg","session_folder":"images","message":"Image generated and saved to ` + imagePath + `. Do not read or re-display it."}` + reminder}},
			wantSummary: "Tool use: **image_gen** — A small round blue ceramic teapot",
			want:        []string{"Prompt:\n```text\nA small round blue ceramic teapot\n```", "Aspect ratio: `1:1`", "Saved image: `" + imagePath + "`"},
			notWant:     []string{"```json", "system-reminder", "Do not read", "session_folder"},
		},
		{
			name:    "image_edit lists its source images as paths",
			tool:    &ToolInfo{Name: "image_edit", Input: map[string]any{"image": []any{imagePath}, "prompt": "Now deep green"}, Output: map[string]any{"status": "success", "output": `{"path":"/qa/2.jpg","filename":"2.jpg","session_folder":"images","message":"Image edited."}`}},
			want:    []string{"Image: `" + imagePath + "`", "Saved image: `/qa/2.jpg`"},
			notWant: []string{"```json", `["`},
		},
		{
			name:        "video failure keeps its arguments readable",
			tool:        &ToolInfo{Name: "reference_to_video", Input: map[string]any{"aspect_ratio": "1:1", "duration": float64(6), "first_frame": imagePath, "prompt": strings.Repeat("steam rises ", 10), "resolution_name": "480p"}, Output: map[string]any{"status": "error", "output": "Tool `reference_to_video` failed: unavailable under ZDR"}},
			wantSummary: "Tool use: **reference_to_video** — " + strings.Repeat("steam rises ", 6) + "steam ri…",
			want:        []string{"Duration: `6`", "First frame: `" + imagePath + "`", "Resolution: `480p`", "Error: Tool `reference_to_video` failed"},
			notWant:     []string{"```json"},
		},
		{
			name:        "inline workflow script",
			tool:        &ToolInfo{Name: "workflow", Input: map[string]any{"source": map[string]any{"type": "script", "script": "let meta = #{\n    name: \"tool-demo\",\n};\ncomplete(#{ ok: false });\n"}, "validate_only": true}, Output: map[string]any{"status": "success", "output": "Smoke check passed for workflow 'tool-demo'."}},
			wantSummary: "Tool use: **workflow** `tool-demo`",
			want:        []string{"Source: `script`\n\nScript:\n```rhai\nlet meta = #{\n    name: \"tool-demo\",\n};\ncomplete(#{ ok: false });\n```", "Validate only: `true`", "Result: Smoke check passed"},
			notWant:     []string{"```json"},
		},
		{
			name:        "saved workflow by name",
			tool:        &ToolInfo{Name: "workflow", Input: map[string]any{"source": map[string]any{"type": "name", "name": "SPECSTORY_QA_NONEXISTENT"}}},
			wantSummary: "Tool use: **workflow** `SPECSTORY_QA_NONEXISTENT`",
			want:        []string{"Source: `name`\nName: `SPECSTORY_QA_NONEXISTENT`"},
			notWant:     []string{"```json", "Script:"},
		},
		{
			name:        "scheduler_create",
			tool:        &ToolInfo{Name: "scheduler_create", Input: map[string]any{"fire_immediately": false, "interval": "1d", "prompt": "Reply with one word: demo."}, Output: map[string]any{"status": "success", "output": "Scheduled task created (ID: 01a0, every 1 day)."}},
			wantSummary: "Tool use: **scheduler_create** every `1d`",
			want:        []string{"Prompt:\n```text\nReply with one word: demo.\n```", "Fire immediately: `false`\nInterval: `1d`", "Result: Scheduled task created"},
			notWant:     []string{"```json"},
		},
		{
			name:        "scheduler_delete",
			tool:        &ToolInfo{Name: "scheduler_delete", Input: map[string]any{"id": "01a0ca8b"}, Output: map[string]any{"status": "success", "output": "Scheduled task 01a0ca8b cancelled."}},
			wantSummary: "Tool use: **scheduler_delete** `01a0ca8b`",
			want:        []string{"ID: `01a0ca8b`"},
			notWant:     []string{"```json"},
		},
		{
			name:        "send_feedback",
			tool:        &ToolInfo{Name: "send_feedback", Input: map[string]any{"details": "What happened:\nA demo.", "title": "Demo draft", "type": "idea"}, Output: map[string]any{"status": "success", "output": "Local feedback draft saved."}},
			wantSummary: "Tool use: **send_feedback** — Demo draft",
			want:        []string{"Title: `Demo draft`\nType: `idea`\n\nDetails:\n```text\nWhat happened:\nA demo.\n```"},
			notWant:     []string{"```json"},
		},
		{
			name:        "background command envelope and a stray reminder",
			tool:        &ToolInfo{Name: "run_terminal_command", Input: map[string]any{"background": true, "command": "sleep 45", "description": "Start a sleep"}, Output: map[string]any{"status": "success", "output": "<task-id>01a0</task-id>\n<task-type>bash</task-type>\n<output-file>/qa/call-16.log</output-file>\n<status>running</status>\nUse get_command_or_subagent_output when you need the output." + reminder}},
			wantSummary: "Tool use: **run_terminal_command** `sleep 45`",
			want:        []string{"Result:\nTask ID: `01a0`\nTask type: `bash`\nOutput file: `/qa/call-16.log`\nStatus: `running`\n\nUse get_command_or_subagent_output"},
			notWant:     []string{"<task-id>", "system-reminder", "Background subagent"},
		},
		{
			name:        "multi-line command summary uses the first line",
			tool:        &ToolInfo{Name: "monitor", Input: map[string]any{"command": "echo one\necho two"}},
			wantSummary: "Tool use: **monitor** `echo one`",
		},
		{
			name:        "waiting on several tasks",
			tool:        &ToolInfo{Name: "get_command_or_subagent_output", Input: map[string]any{"task_ids": []any{"01a0-a", "01a0-b"}, "timeout_ms": float64(0)}},
			wantSummary: "Tool use: **get_command_or_subagent_output** 2 tasks",
			want:        []string{"Task IDs: `01a0-a`, `01a0-b`"},
		},
		{
			name:        "waiting on one task",
			tool:        &ToolInfo{Name: "get_command_or_subagent_output", Input: map[string]any{"task_ids": []any{"01a0-a"}}},
			wantSummary: "Tool use: **get_command_or_subagent_output** `01a0-a`",
		},
		{
			name:    "grep drops the workspace wrapper",
			tool:    &ToolInfo{Name: "grep", Input: map[string]any{"pattern": "^write$"}, Output: map[string]any{"status": "success", "output": "<workspace_result workspace_path=\"/qa\">\nFound 1 matching lines\n/qa/tools.txt\n26:write\n</workspace_result>"}},
			want:    []string{"```text\nFound 1 matching lines\n/qa/tools.txt\n26:write\n```"},
			notWant: []string{"workspace_result"},
		},
		{
			name: "use_tool fences a JSON result as JSON",
			tool: &ToolInfo{Name: "use_tool", Input: map[string]any{"tool_input": map[string]any{}, "tool_name": "tasks__list"}, Output: map[string]any{"status": "success", "output": "{\n  \"automations\": []\n}"}},
			want: []string{"Result:\n```json\n{\n  \"automations\": []\n}\n```"},
		},
		{
			name: "web_fetch labels its URL",
			tool: &ToolInfo{Name: "web_fetch", Input: map[string]any{"url": "https://example.com"}},
			want: []string{"URL: `https://example.com`"},
		},
		{
			name: "spawn_subagent labels its description",
			tool: &ToolInfo{Name: "spawn_subagent", Input: map[string]any{"background": true, "description": "Reply with one word", "prompt": "Reply pong."}},
			want: []string{"Description: `Reply with one word`"},
		},
		{
			name: "file content keeps a trailing reminder tag",
			tool: &ToolInfo{Name: "read_file", Input: map[string]any{"target_file": "notes.md"}, Output: map[string]any{"status": "success", "output": "notes" + reminder}},
			want: []string{"<system-reminder>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := formatToolAsMarkdown(tt.tool)
			if tt.wantSummary != "" && (tt.tool.Summary == nil || *tt.tool.Summary != tt.wantSummary) {
				got := "<nil>"
				if tt.tool.Summary != nil {
					got = *tt.tool.Summary
				}
				t.Errorf("summary = %q, want %q", got, tt.wantSummary)
			}
			for _, want := range tt.want {
				if !strings.Contains(md, want) {
					t.Errorf("missing %q in:\n%s", want, md)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(md, notWant) {
					t.Errorf("unexpected %q in:\n%s", notWant, md)
				}
			}
		})
	}
}

func TestStripTrailingReminders(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no reminder", input: "  output\n\n", want: "  output\n\n"},
		{name: "one trailing reminder", input: "exit: 0\n\n<system-reminder>\nnotice\n</system-reminder>\n", want: "exit: 0"},
		{name: "two trailing reminders", input: "ok\n<system-reminder>a</system-reminder>\n<system-reminder>b</system-reminder>", want: "ok"},
		// Output that follows a reminder is real, so neither is removed.
		{name: "reminder mid-output", input: "a\n<system-reminder>x</system-reminder>\nb", want: "a\n<system-reminder>x</system-reminder>\nb"},
		// Real output between two reminders survives; only the last one goes.
		{name: "output between reminders", input: "<system-reminder>x</system-reminder>\nreal\n<system-reminder>y</system-reminder>", want: "<system-reminder>x</system-reminder>\nreal"},
		{name: "only a reminder", input: "<system-reminder>x</system-reminder>", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripTrailingReminders(tt.input); got != tt.want {
				t.Errorf("stripTrailingReminders(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
