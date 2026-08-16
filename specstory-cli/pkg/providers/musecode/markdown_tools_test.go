package musecode

import (
	"strings"
	"testing"
)

// toolMarkdown renders a tool and returns the markdown plus the summary the
// renderer chose to set.
func toolMarkdown(tool *ToolInfo) (markdown, summary string) {
	markdown = formatToolAsMarkdown(tool)
	if tool.Summary != nil {
		summary = *tool.Summary
	}
	return markdown, summary
}

func TestFormatToolAsMarkdown_Summaries(t *testing.T) {
	tests := []struct {
		name     string
		tool     *ToolInfo
		expected string
	}{
		{
			name:     "read_file uses path",
			tool:     &ToolInfo{Name: "read_file", Input: map[string]any{"path": "notes.txt"}},
			expected: "Tool use: **read_file** `notes.txt`",
		},
		{
			name:     "write_file uses path",
			tool:     &ToolInfo{Name: "write_file", Input: map[string]any{"path": "SUMMARY.md"}},
			expected: "Tool use: **write_file** `SUMMARY.md`",
		},
		{
			name:     "edit_file uses path",
			tool:     &ToolInfo{Name: "edit_file", Input: map[string]any{"path": "src/calc.py"}},
			expected: "Tool use: **edit_file** `src/calc.py`",
		},
		{
			name:     "search uses pattern",
			tool:     &ToolInfo{Name: "search", Input: map[string]any{"pattern": "beta", "mode": "regex"}},
			expected: "Tool use: **search** `beta`",
		},
		{
			name:     "web_search uses query",
			tool:     &ToolInfo{Name: "web_search", Input: map[string]any{"query": "Muse Spark model"}},
			expected: "Tool use: **web_search** `Muse Spark model`",
		},
		{
			name:     "bash gets no custom summary",
			tool:     &ToolInfo{Name: "bash", Input: map[string]any{"command": "ls"}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, summary := toolMarkdown(tt.tool)
			if summary != tt.expected {
				t.Errorf("summary = %q, want %q", summary, tt.expected)
			}
		})
	}
}

func TestFormatToolAsMarkdown_BashRendersParsedOutput(t *testing.T) {
	tool := &ToolInfo{
		Name: "bash",
		Type: "shell",
		Input: map[string]any{
			"command":     `python3 -c "print(42)"`,
			"description": "Print the answer",
		},
		Output: map[string]any{"output": `{"chunk_id":"exec-1-1","command":"python3","exit_code":0,"output":"42\n","truncated":false}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Print the answer") {
		t.Errorf("description missing from body:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```bash\npython3 -c \"print(42)\"\n```") {
		t.Errorf("command not rendered in a bash fence:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```text\n42\n```") {
		t.Errorf("parsed output not rendered:\n%s", markdown)
	}
	// The JSON envelope is machine bookkeeping; it must not reach the reader.
	if strings.Contains(markdown, "chunk_id") {
		t.Errorf("raw result JSON leaked into the markdown:\n%s", markdown)
	}
	if strings.Contains(markdown, "Exit code") {
		t.Errorf("exit code shown for a successful command:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_BashShowsNonZeroExitCode(t *testing.T) {
	tool := &ToolInfo{
		Name:   "bash",
		Type:   "shell",
		Input:  map[string]any{"command": "exit 5", "description": "Run a failing command"},
		Output: map[string]any{"output": `{"command":"exit 5","exit_code":5,"terminal_status":"failed","output":"boom\n"}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Exit code: 5") {
		t.Errorf("exit code missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```text\nboom\n```") {
		t.Errorf("command output missing:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_BashFallsBackToRawOnUnparseableResult(t *testing.T) {
	tests := []struct {
		name   string
		output map[string]any
		expect string
	}{
		{
			name:   "not JSON",
			output: map[string]any{"output": "plain text result\nsecond line"},
			expect: "plain text result",
		},
		{
			name:   "JSON without an output field",
			output: map[string]any{"output": `{"execution_state":"background_running","session_id":3}`},
			expect: "background_running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolInfo{Name: "bash", Input: map[string]any{"command": "sleep 1"}, Output: tt.output}
			markdown, _ := toolMarkdown(tool)
			if !strings.Contains(markdown, tt.expect) {
				t.Errorf("raw result not preserved (want %q):\n%s", tt.expect, markdown)
			}
		})
	}
}

// A bash call that opens a background PTY reports execution state instead of
// output; without a status line the block renders no result at all and the
// reader cannot tell a session started. A terminated PTY must say how it ended,
// and its output must shed the terminal control bytes (CR, backspace) the raw
// record carries.
func TestFormatToolAsMarkdown_BashBackgroundAndTerminatedPTY(t *testing.T) {
	t.Run("background start with no output", func(t *testing.T) {
		tool := &ToolInfo{
			Name:   "bash",
			Input:  map[string]any{"command": "cat", "tty": true},
			Output: map[string]any{"output": `{"chunk_id":"exec-3-1","command":"cat","execution_state":"background_running","session_id":3,"output":""}`},
		}
		markdown, _ := toolMarkdown(tool)
		if !strings.Contains(markdown, "Running in background (shell session 3)") {
			t.Errorf("background PTY start rendered no status:\n%s", markdown)
		}
	})

	t.Run("terminated session sanitizes control bytes", func(t *testing.T) {
		tool := &ToolInfo{
			Name:   "bash_input",
			Input:  map[string]any{"session_id": float64(3), "chars": "", "terminate": true},
			Output: map[string]any{"output": "{\"chunk_id\":\"exec-3-3\",\"command\":\"cat\",\"terminal_status\":\"cancelled\",\"terminal_reason\":\"terminated by bash_input\",\"session_id\":3,\"output\":\"\\r\\n^D\\b\\b\\r\\ncat finished\\r\\n\"}"},
		}
		markdown, _ := toolMarkdown(tool)
		if !strings.Contains(markdown, "Shell session 3 cancelled (terminated by bash_input)") {
			t.Errorf("terminal status missing:\n%s", markdown)
		}
		if !strings.Contains(markdown, "cat finished") {
			t.Errorf("output missing:\n%s", markdown)
		}
		// The backspaces erased the echoed ^D on the terminal; the markdown
		// must show what the terminal showed, with no raw control bytes.
		for _, forbidden := range []string{"\r", "\b", "^D"} {
			if strings.Contains(markdown, forbidden) {
				t.Errorf("control sequence %q leaked into the markdown:\n%s", forbidden, markdown)
			}
		}
	})
}

func TestFormatToolAsMarkdown_BashInputBody(t *testing.T) {
	t.Run("keystrokes name their PTY session", func(t *testing.T) {
		tool := &ToolInfo{
			Name:  "bash_input",
			Input: map[string]any{"session_id": float64(3), "chars": "hello\n", "terminate": false},
		}
		markdown, _ := toolMarkdown(tool)
		if !strings.Contains(markdown, "PTY session 3") {
			t.Errorf("session number missing from body:\n%s", markdown)
		}
		// Keystrokes are typed input, not a bash program.
		if !strings.Contains(markdown, "```text\nhello\n```") {
			t.Errorf("chars not fenced as text:\n%s", markdown)
		}
	})

	t.Run("terminate call says so instead of rendering nothing", func(t *testing.T) {
		tool := &ToolInfo{
			Name:  "bash_input",
			Input: map[string]any{"session_id": float64(3), "chars": "", "terminate": true},
		}
		markdown, _ := toolMarkdown(tool)
		if !strings.Contains(markdown, "Terminate PTY session 3") {
			t.Errorf("terminate call rendered no body:\n%s", markdown)
		}
	})
}

func TestFormatToolAsMarkdown_EditRendersDiffFence(t *testing.T) {
	tool := &ToolInfo{
		Name: "edit_file",
		Type: "write",
		Input: map[string]any{
			"path":    "src/calc.py",
			"find":    "def add(a, b):\n    return a + b",
			"replace": "def add(a, b):\n    return a + b\n\ndef sub(a, b):\n    return a - b",
		},
		Output: map[string]any{"output": "edited\nchanged lines: lines 1-2"},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Path: `src/calc.py`") {
		t.Errorf("path missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "```diff\n") {
		t.Errorf("diff fence missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "-def add(a, b):\n-    return a + b\n") {
		t.Errorf("find lines not rendered as removals:\n%s", markdown)
	}
	if !strings.Contains(markdown, "+def sub(a, b):\n+    return a - b") {
		t.Errorf("replace lines not rendered as additions:\n%s", markdown)
	}
	// The body already shows the change; repeating the result adds noise.
	if strings.Contains(markdown, "changed lines") {
		t.Errorf("edit result duplicated below the diff:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_TodosChecklist(t *testing.T) {
	tool := &ToolInfo{
		Name: "write_todos",
		Type: "task",
		Input: map[string]any{"todos": []any{
			map[string]any{"text": "Run the tests", "status": "completed"},
			map[string]any{"text": "Add sub()", "status": "in_progress"},
			map[string]any{"text": "Write SUMMARY.md", "status": "pending"},
			// Qwen's key must not be read: Muse items carry "text".
			map[string]any{"content": "ignored", "status": "pending"},
		}},
		Output: map[string]any{"output": `{"ok":true,"revision":1,"items":3}`},
	}

	markdown, _ := toolMarkdown(tool)

	for _, want := range []string{
		"- [x] Run the tests",
		"- [⚡] Add sub()",
		"- [ ] Write SUMMARY.md",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("checklist missing %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "ignored") {
		t.Errorf("item text read from the wrong key:\n%s", markdown)
	}
	// The checklist is the record of the call; the JSON ack adds nothing.
	if strings.Contains(markdown, "revision") {
		t.Errorf("todo result duplicated below the checklist:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_ErrorTakesPriority(t *testing.T) {
	tests := []struct {
		name string
		tool *ToolInfo
	}{
		{
			name: "read_file failure",
			tool: &ToolInfo{
				Name:   "read_file",
				Input:  map[string]any{"path": "missing.txt"},
				Output: map[string]any{"error": "tool failed: No such file or directory (os error 2)"},
			},
		},
		{
			name: "write_todos failure still renders",
			tool: &ToolInfo{
				Name:   "write_todos",
				Input:  map[string]any{"todos": []any{map[string]any{"text": "x", "status": "pending"}}},
				Output: map[string]any{"error": "tool failed: budget exceeded"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown, _ := toolMarkdown(tt.tool)
			if !strings.Contains(markdown, "Result: tool failed") {
				t.Errorf("failure not rendered as the result:\n%s", markdown)
			}
		})
	}
}

// A tool result containing its own fence must not be able to close the fence
// wrapping it; spi.CodeFence sizes the wrapper to outrun the longest inner run.
func TestFormatToolAsMarkdown_UsesCodeFenceForEmbeddedBackticks(t *testing.T) {
	tests := []struct {
		name   string
		tool   *ToolInfo
		expect string
	}{
		{
			name: "bash output with a fence",
			tool: &ToolInfo{
				Name:   "bash",
				Input:  map[string]any{"command": "cat README.md"},
				Output: map[string]any{"output": "{\"exit_code\":0,\"output\":\"```go\\nfmt.Println()\\n```\\n\"}"},
			},
			expect: "````text\n",
		},
		{
			name: "write_file content with a fence",
			tool: &ToolInfo{
				Name: "write_file",
				Input: map[string]any{
					"path":    "README.md",
					"content": "intro\n```zsh\nspecstory sync\n```\n",
				},
			},
			expect: "````markdown\n",
		},
		{
			name: "generic result with a long backtick run",
			tool: &ToolInfo{
				Name:   "get_goal",
				Output: map[string]any{"output": "line\n````\nnested\n````"},
			},
			expect: "`````text\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown, _ := toolMarkdown(tt.tool)
			if !strings.Contains(markdown, tt.expect) {
				t.Errorf("fence not widened past the embedded backticks (want %q):\n%s", tt.expect, markdown)
			}
		})
	}
}

func TestFormatToolAsMarkdown_SubagentSpawnBody(t *testing.T) {
	tool := &ToolInfo{
		Name: "subagent_spawn",
		Type: "task",
		Input: map[string]any{
			"role":      "demo-worker",
			"objective": "Write tool-demo/subagent-output.txt",
		},
		Output: map[string]any{"output": `{"status":"accepted","agent_path":"main/subagent-demo/1"}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Role: `demo-worker`") {
		t.Errorf("role missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Write tool-demo/subagent-output.txt") {
		t.Errorf("objective missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Result: ") || !strings.Contains(markdown, "accepted") {
		t.Errorf("spawn result missing:\n%s", markdown)
	}
}

// Search hits are arbitrary file content: markdown or HTML inside a matched
// line must render as text inside a fence, not as markup that can break the
// enclosing tool block.
func TestFormatToolAsMarkdown_SearchResultsFenced(t *testing.T) {
	tool := &ToolInfo{
		Name:   "search",
		Type:   "search",
		Input:  map[string]any{"pattern": "beta"},
		Output: map[string]any{"output": "notes.txt:2:beta\nREADME.md:9:</details> beta"},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "```text\n") {
		t.Errorf("search hits not fenced:\n%s", markdown)
	}
	if !strings.Contains(markdown, "notes.txt:2:beta") || !strings.Contains(markdown, "</details> beta") {
		t.Errorf("search hits missing:\n%s", markdown)
	}
}

// A JSON result must render pretty-printed in a json fence; plain text and
// bare scalars must not be mistaken for JSON.
func TestFormatToolAsMarkdown_JSONResultsPrettyPrinted(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantFence  bool
		wantInline string
	}{
		{
			name:      "single-line object becomes an indented fence",
			output:    `{"jobs":[{"id":"eb5498db","cron":"0 9 * * 1"}]}`,
			wantFence: true,
		},
		{
			name:      "array becomes an indented fence",
			output:    `[{"id":1},{"id":2}]`,
			wantFence: true,
		},
		{
			name:       "plain text stays text",
			output:     "Removed scheduled job eb5498db.",
			wantInline: "Result: Removed scheduled job eb5498db.",
		},
		{
			name:       "bare scalar stays text",
			output:     "42",
			wantInline: "Result: 42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// cron_list has no dedicated renderer, so it exercises the default path.
			markdown, _ := toolMarkdown(&ToolInfo{Name: "cron_list", Output: map[string]any{"output": tt.output}})
			if tt.wantFence {
				if !strings.Contains(markdown, "```json\n{") && !strings.Contains(markdown, "```json\n[") {
					t.Errorf("JSON result not fenced and indented:\n%s", markdown)
				}
				if strings.Contains(markdown, tt.output) {
					t.Errorf("single-line JSON survived un-indented:\n%s", markdown)
				}
			}
			if tt.wantInline != "" && !strings.Contains(markdown, tt.wantInline) {
				t.Errorf("want %q in:\n%s", tt.wantInline, markdown)
			}
		})
	}
}

func TestFormatToolAsMarkdown_WebSearchResultsAsLinks(t *testing.T) {
	tool := &ToolInfo{
		Name:  "web_search",
		Input: map[string]any{"query": "Muse Spark"},
		Output: map[string]any{"output": `{"query":"Muse Spark","results":[` +
			`{"url":"https://a.example/1","title":"Meta debuts [Muse] Spark","snippet":"\n \n noisy snippet text"},` +
			`{"url":"https://b.example/2","title":"Line\nbroken   title"}]}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "2 results:") {
		t.Errorf("result count missing:\n%s", markdown)
	}
	// Brackets in a title must not end the link text early, and embedded
	// newlines must not break the list line.
	if !strings.Contains(markdown, `- [Meta debuts \[Muse\] Spark](https://a.example/1)`) {
		t.Errorf("first hit not a clean link:\n%s", markdown)
	}
	if !strings.Contains(markdown, "- [Line broken title](https://b.example/2)") {
		t.Errorf("title whitespace not collapsed:\n%s", markdown)
	}
	if strings.Contains(markdown, "snippet") {
		t.Errorf("snippet noise leaked:\n%s", markdown)
	}

	t.Run("unexpected shape falls back", func(t *testing.T) {
		markdown, _ := toolMarkdown(&ToolInfo{Name: "web_search", Output: map[string]any{"output": "no structured hits"}})
		if !strings.Contains(markdown, "no structured hits") {
			t.Errorf("fallback lost the raw result:\n%s", markdown)
		}
	})
}

func TestFormatToolAsMarkdown_MemoryTools(t *testing.T) {
	t.Run("summaries carry the scope-qualified path", func(t *testing.T) {
		for _, name := range []string{"read_memory", "add_memory", "edit_memory"} {
			tool := &ToolInfo{Name: name, Input: map[string]any{"scope": "personal_project", "path": "memory.md"}}
			_, summary := toolMarkdown(tool)
			want := "Tool use: **" + name + "** `personal_project/memory.md`"
			if summary != want {
				t.Errorf("%s summary = %q, want %q", name, summary, want)
			}
		}
	})

	t.Run("read_memory shows no JSON input block", func(t *testing.T) {
		tool := &ToolInfo{Name: "read_memory", Input: map[string]any{"scope": "s", "path": "m.md", "offset": float64(1)}}
		markdown, _ := toolMarkdown(tool)
		if strings.Contains(markdown, "```json") {
			t.Errorf("params should live in the summary, not a JSON block:\n%s", markdown)
		}
	})

	t.Run("add_memory renders the note like write_file", func(t *testing.T) {
		tool := &ToolInfo{
			Name: "add_memory",
			Input: map[string]any{
				"scope": "personal_project", "path": "memory.md",
				"description": "demo for add_memory",
				"content":     "# Demo memory entry\n- a note\n",
			},
			Output: map[string]any{"output": `{"success":true,"operation":"add","message":"memory note written"}`},
		}
		markdown, _ := toolMarkdown(tool)
		if !strings.Contains(markdown, "demo for add_memory") {
			t.Errorf("description missing:\n%s", markdown)
		}
		if !strings.Contains(markdown, "```markdown\n# Demo memory entry\n- a note\n```") {
			t.Errorf("note not fenced (or trailing newline kept):\n%s", markdown)
		}
		if !strings.Contains(markdown, "Result: memory note written") {
			t.Errorf("ack not reduced to its message:\n%s", markdown)
		}
		if strings.Contains(markdown, `"success"`) {
			t.Errorf("raw ack JSON leaked:\n%s", markdown)
		}
	})

	t.Run("edit_memory renders a diff like edit_file", func(t *testing.T) {
		tool := &ToolInfo{
			Name: "edit_memory",
			Input: map[string]any{
				"scope": "personal_project", "path": "memory.md",
				"old_str": "- a note",
				"new_str": "- an updated note",
			},
			Output: map[string]any{"output": `{"success":true,"operation":"edit","message":"memory note edited"}`},
		}
		markdown, _ := toolMarkdown(tool)
		// The leading "-"/"+" are the diff markers; the second "-" of the
		// removal line is the note's own bullet.
		if !strings.Contains(markdown, "```diff\n-- a note\n+- an updated note") {
			t.Errorf("old/new not rendered as a diff:\n%s", markdown)
		}
		if !strings.Contains(markdown, "Result: memory note edited") {
			t.Errorf("ack not reduced to its message:\n%s", markdown)
		}
	})
}

func TestFormatToolAsMarkdown_GoalResults(t *testing.T) {
	t.Run("no active goal", func(t *testing.T) {
		markdown, _ := toolMarkdown(&ToolInfo{Name: "get_goal", Output: map[string]any{"output": `{"goal":null}`}})
		if !strings.Contains(markdown, "Result: No active goal") {
			t.Errorf("null goal not reduced:\n%s", markdown)
		}
	})

	t.Run("goal envelope reduces to its story", func(t *testing.T) {
		envelope := `{"goal":{"session_id":"s","goal_id":"goal-1","objective":"Demo goal","status":"active","percent_complete":50,"token_budget":1000,"tokens_used":0,"created_at_ms":1786885696184}}`
		markdown, _ := toolMarkdown(&ToolInfo{Name: "report_progress", Output: map[string]any{"output": envelope}})
		if !strings.Contains(markdown, "Goal: Demo goal") {
			t.Errorf("objective missing:\n%s", markdown)
		}
		if !strings.Contains(markdown, "Status: active (50% complete)") {
			t.Errorf("status/percent missing:\n%s", markdown)
		}
		if strings.Contains(markdown, "created_at_ms") {
			t.Errorf("envelope bookkeeping leaked:\n%s", markdown)
		}
	})
}

func TestFormatToolAsMarkdown_SubagentResults(t *testing.T) {
	t.Run("rejection shows status, reason and child", func(t *testing.T) {
		markdown, _ := toolMarkdown(&ToolInfo{
			Name:   "subagent_send_message",
			Output: map[string]any{"output": `{"status":"rejected","reason":"terminal","subagent_id":"01a00ab0"}`},
		})
		if !strings.Contains(markdown, "Result: rejected (terminal) — subagent `01a00ab0`") {
			t.Errorf("envelope not reduced:\n%s", markdown)
		}
	})

	t.Run("wait surfaces the child summary as a blockquote", func(t *testing.T) {
		markdown, _ := toolMarkdown(&ToolInfo{
			Name:   "subagent_wait",
			Output: map[string]any{"output": `{"status":"ready","subagent_id":"01a00ab0","task_ref":"task/1#5","summary":"Hello! I am a subagent.","evidence_refs":["x"]}`},
		})
		if !strings.Contains(markdown, "> Hello! I am a subagent.") {
			t.Errorf("summary not quoted:\n%s", markdown)
		}
		if strings.Contains(markdown, "task_ref") || strings.Contains(markdown, "evidence_refs") {
			t.Errorf("bookkeeping refs leaked:\n%s", markdown)
		}
	})
}

func TestFormatToolAsMarkdown_RequestUserInput(t *testing.T) {
	tool := &ToolInfo{
		Name: "request_user_input",
		Input: map[string]any{
			"auto_resolution_ms": float64(60000),
			"questions": []any{map[string]any{
				"id": "demo_choice", "header": "Demo",
				"question": "Pick an option?",
				"options": []any{
					map[string]any{"label": "Option A (Recommended)", "description": "Confirms it works."},
					map[string]any{"label": "Option B"},
				},
			}},
		},
		Output: map[string]any{"output": `{"status":"answered","answers":[{"id":"demo_choice","selected_label":"Option A (Recommended)"}]}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "**Demo**: Pick an option?") {
		t.Errorf("question missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "- Option A (Recommended) — Confirms it works.") {
		t.Errorf("described option missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "- Option B") {
		t.Errorf("bare option missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Result: answered: Option A (Recommended)") {
		t.Errorf("answer not reduced:\n%s", markdown)
	}
	if strings.Contains(markdown, "```json") {
		t.Errorf("questions or answers leaked as JSON:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_CronCreateBody(t *testing.T) {
	tests := []struct {
		name      string
		recurring bool
		want      string
	}{
		{name: "one-shot", recurring: false, want: "Schedule: `0 9 * * 1` (once)"},
		{name: "recurring", recurring: true, want: "Schedule: `0 9 * * 1` (recurring)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolInfo{
				Name:  "cron_create",
				Input: map[string]any{"cron": "0 9 * * 1", "prompt": "weekly hello", "recurring": tt.recurring},
			}
			markdown, _ := toolMarkdown(tool)
			if !strings.Contains(markdown, tt.want) {
				t.Errorf("schedule line missing (%q):\n%s", tt.want, markdown)
			}
			if !strings.Contains(markdown, "Prompt: weekly hello") {
				t.Errorf("prompt missing:\n%s", markdown)
			}
			if strings.Contains(markdown, "```json") {
				t.Errorf("input leaked as JSON:\n%s", markdown)
			}
		})
	}
}

// Muse's read_file preamble repeats what the summary says; the numbered
// window below it is the content the reader wants.
func TestFormatToolAsMarkdown_ReadFilePreambleStripped(t *testing.T) {
	tool := &ToolInfo{
		Name:   "read_file",
		Input:  map[string]any{"path": "tools.txt"},
		Output: map[string]any{"output": "Read text file `tools.txt`.\n1|muse.read_file\n2|muse.search"},
	}

	markdown, _ := toolMarkdown(tool)

	if strings.Contains(markdown, "Read text file") {
		t.Errorf("preamble kept:\n%s", markdown)
	}
	if !strings.Contains(markdown, "1|muse.read_file\n2|muse.search") {
		t.Errorf("numbered window missing:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_SubagentSpawnIncludesTaskName(t *testing.T) {
	tool := &ToolInfo{
		Name: "subagent_spawn",
		Input: map[string]any{
			"role": "demo helper", "task_name": "demo subagent",
			"objective": "Say hello and exit.",
		},
		Output: map[string]any{"output": `{"status":"accepted","subagent_id":"01a00ab0","agent_path":"main/demo subagent/1"}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "Task: `demo subagent`") {
		t.Errorf("task name missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Result: accepted — `main/demo subagent/1`") {
		t.Errorf("spawn result not reduced:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_SessionMessage(t *testing.T) {
	tool := &ToolInfo{
		Name: "send_session_message",
		Input: map[string]any{
			"target_session_id": "a8806e39-7b88",
			"body":              "Hello from session 98bb97c9.",
			"authority":         "runtime_context",
			"delivery_policy":   "queue_next_turn",
			"command_id":        "demo-send-001",
		},
		Output: map[string]any{"output": `{"outcome":{"command_id":"demo-send-001","status":"not_attempted","duplicate_of":null}}`},
	}

	markdown, _ := toolMarkdown(tool)

	if !strings.Contains(markdown, "To: session `a8806e39-7b88`") {
		t.Errorf("target missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Hello from session 98bb97c9.") {
		t.Errorf("message body missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Result: not_attempted") {
		t.Errorf("outcome not reduced to its status:\n%s", markdown)
	}
	if strings.Contains(markdown, "delivery_policy") || strings.Contains(markdown, "```json") {
		t.Errorf("transport bookkeeping leaked:\n%s", markdown)
	}

	t.Run("outcome-less result falls back", func(t *testing.T) {
		markdown, _ := toolMarkdown(&ToolInfo{
			Name:   "send_session_message",
			Output: map[string]any{"output": `{"unexpected":"shape"}`},
		})
		if !strings.Contains(markdown, "unexpected") {
			t.Errorf("fallback lost the raw result:\n%s", markdown)
		}
	})
}

func TestFormatToolAsMarkdown_SubagentMessageBody(t *testing.T) {
	tool := &ToolInfo{
		Name: "subagent_send_message",
		Input: map[string]any{
			"agent_path":  "main/demo subagent/1",
			"subagent_id": "01a00ab0",
			"message":     "Hello subagent — please continue.",
			"mode":        "queue",
			"interrupt":   false,
		},
	}

	markdown, _ := toolMarkdown(tool)

	// The agent path names the child more readably than the uuid.
	if !strings.Contains(markdown, "To: `main/demo subagent/1`") {
		t.Errorf("target missing:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Hello subagent — please continue.") {
		t.Errorf("message missing:\n%s", markdown)
	}
	if strings.Contains(markdown, "```json") {
		t.Errorf("input leaked as JSON:\n%s", markdown)
	}
}

func TestFormatToolAsMarkdown_PeerSessionsResult(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    []string
		wantNot []string
	}{
		{
			name:    "single reachable peer",
			output:  `{"schema_version":1,"peer_sessions":[{"session_id":"a8806e39","workspace_label":"muse-2","reachable":true}],"diagnostic":null}`,
			want:    []string{"1 peer session:", "- muse-2 (`a8806e39`) — reachable"},
			wantNot: []string{"schema_version", "```json"},
		},
		{
			name:   "unreachable peer without a label",
			output: `{"peer_sessions":[{"session_id":"b1","reachable":false}]}`,
			want:   []string{"- `b1` — unreachable"},
		},
		{
			name:   "no peers",
			output: `{"schema_version":1,"peer_sessions":[],"diagnostic":null}`,
			want:   []string{"Result: No peer sessions"},
		},
		{
			// A populated diagnostic is a shape never observed; keep it visible.
			name:   "diagnostic present falls back to JSON",
			output: `{"peer_sessions":[],"diagnostic":{"reason":"transport down"}}`,
			want:   []string{"```json", "transport down"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markdown, _ := toolMarkdown(&ToolInfo{Name: "list_peer_sessions", Output: map[string]any{"output": tt.output}})
			for _, want := range tt.want {
				if !strings.Contains(markdown, want) {
					t.Errorf("missing %q in:\n%s", want, markdown)
				}
			}
			for _, forbidden := range tt.wantNot {
				if strings.Contains(markdown, forbidden) {
					t.Errorf("unexpected %q in:\n%s", forbidden, markdown)
				}
			}
		})
	}
}

func TestInputAsString(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		key      string
		expected string
	}{
		{name: "nil map", input: nil, key: "path", expected: ""},
		{name: "missing key", input: map[string]any{"other": "x"}, key: "path", expected: ""},
		// Muse serialises unset optional args as JSON null; "null" is not a path.
		{name: "json null reads as absent", input: map[string]any{"workdir": nil}, key: "workdir", expected: ""},
		{name: "string value", input: map[string]any{"path": "notes.txt"}, key: "path", expected: "notes.txt"},
		{name: "non-string value marshals", input: map[string]any{"limit": float64(500)}, key: "limit", expected: "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inputAsString(tt.input, tt.key); got != tt.expected {
				t.Errorf("inputAsString() = %q, want %q", got, tt.expected)
			}
		})
	}
}
