package copilotcli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// record builds one events.jsonl line.
func record(t *testing.T, eventType, id string, data map[string]any) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"type":      eventType,
		"id":        id,
		"timestamp": "2026-10-09T07:35:38.000Z",
		"data":      data,
	})
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return string(line)
}

func sessionStart(t *testing.T, sessionID, cwd string) string {
	return record(t, eventSessionStart, "start", map[string]any{
		"sessionId":      sessionID,
		"copilotVersion": "1.0.95-2",
		"startTime":      "2026-10-09T07:35:38.043Z",
		"context":        map[string]any{"cwd": cwd},
	})
}

// writeSession creates <home>/session-state/<id>/events.jsonl.
func writeSession(t *testing.T, home, sessionID string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, sessionStateDirName, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, eventsFileName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}

// newProjectDir creates a project directory and returns its canonical path,
// the spelling Copilot records as a session's working directory (the operating
// system's: symlinks resolved, on-disk case).
func newProjectDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := spi.GetCanonicalPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// useSessionStore points COPILOT_HOME at a fresh directory and returns it.
func useSessionStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(homeEnvVar, home)
	return home
}

// renderExchanges flattens exchanges to "exchange:role:text|tool(name)=output"
// lines so tests can assert on structure without depending on IDs. A
// sub-agent's message shows as role "agent-sidechain", mirroring the
// "Agent - sidechain" header the markdown generator writes for it.
func renderExchanges(exchanges []schema.Exchange) []string {
	var lines []string
	for i, exchange := range exchanges {
		for _, message := range exchange.Messages {
			var parts []string
			for _, content := range message.Content {
				parts = append(parts, content.Type+"="+content.Text)
			}
			if message.Tool != nil {
				output, _ := message.Tool.Output["result"].(string)
				if isError, _ := message.Tool.Output["is_error"].(bool); isError {
					output = "ERROR:" + output
				}
				parts = append(parts, "tool("+message.Tool.Name+"/"+message.Tool.Type+")="+output)
			}
			role := message.Role
			if isSidechain, _ := message.Metadata[sidechainKey].(bool); isSidechain {
				role += "-sidechain"
			}
			lines = append(lines, string(rune('0'+i))+":"+role+":"+strings.Join(parts, ","))
		}
	}
	return lines
}

func TestTranscriptBuilder(t *testing.T) {
	tests := []struct {
		name      string
		events    []rawEvent
		wantLines []string
		wantName  string
	}{
		{
			name: "prompt, reply with thinking, tool call paired with its result",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"list files","source":"user"}`),
				event("assistant.message", "a1", `{"model":"m","reasoningText":"think","content":"Listing.","toolRequests":[{"toolCallId":"c1","name":"bash","arguments":{"command":"ls"}}]}`),
				event("tool.execution_complete", "r1", `{"toolCallId":"c1","success":true,"result":{"content":"a.go"}}`),
				event("assistant.message", "a2", `{"content":"Done."}`),
			},
			wantLines: []string{
				"0:user:text=list files",
				"0:agent:thinking=think,text=Listing.",
				"0:agent:tool(bash/shell)=a.go",
				"0:agent:text=Done.",
			},
			wantName: "list files",
		},
		{
			name: "only the user's own messages are prompts; injections do not split exchanges",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"first"}`),
				event("assistant.message", "a1", `{"content":"one"}`),
				event("user.message", "u2", `{"content":"keep going","source":"autopilot"}`),
				event("user.message", "u3", `{"content":"nudge","source":"system"}`),
				event("user.message", "u4", `{"content":"<skill-context>","source":"skill-x"}`),
				event("user.message", "u5", `{"content":"   "}`),
				event("user.message", "u7", `{"content":"<system_notification>\nAgent finished</system_notification>"}`),
				event("user.message", "u10", `{"content":"<system_notification>\nSession archived</system_notification>","source":"agent-workspace-1"}`),
				event("user.message", "u8", `{"content":"<system_reminder>","source":"instruction-discovery"}`),
				event("assistant.message", "a2", `{"content":"two"}`),
				event("user.message", "u6", `{"content":"second","source":"user"}`),
			},
			wantLines: []string{
				"0:user:text=first",
				"0:agent:text=one",
				"0:agent:text=two",
				"1:user:text=second",
			},
			wantName: "first",
		},
		{
			name: "a sub-agent's prompt and work are kept as a sidechain inside the parent's exchange",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"delegate","parentAgentTaskId":"p1"}`),
				event("assistant.message", "a1", `{"toolRequests":[{"toolCallId":"t1","name":"task","arguments":{"prompt":"go"}}]}`),
				// Quotes the session-message tag: the agent- source still makes it a sub-agent prompt.
				event("user.message", "s0", `{"content":"Audit <cross_session_message>","source":"agent-123","parentAgentTaskId":"p2","transformedContent":"<cross_session_message>\nfrom_session_id: s9"}`),
				event("assistant.message", "s1", `{"model":"sub","content":"inner","parentToolCallId":"t1","toolRequests":[{"toolCallId":"i1","name":"view"}]}`),
				event("tool.execution_complete", "s2", `{"toolCallId":"i1","success":true,"parentToolCallId":"t1","result":{"content":"x"}}`),
				event("tool.execution_complete", "r1", `{"toolCallId":"t1","success":true,"result":{"content":"report"}}`),
				event("assistant.message", "a2", `{"content":"Here is the summary."}`),
			},
			wantLines: []string{
				"0:user:text=delegate",
				"0:agent:tool(task/generic)=report",
				"0:agent-sidechain:text=Audit <cross_session_message>",
				"0:agent-sidechain:text=inner",
				"0:agent-sidechain:tool(view/read)=x",
				"0:agent:text=Here is the summary.",
			},
			wantName: "delegate",
		},
		{
			name: "failed tool keeps its error, unmatched result and empty chunk are ignored",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"edit it"}`),
				event("assistant.message", "a1", `{"toolRequests":[{"toolCallId":"c1","name":"edit","arguments":{"path":"a.go"}}]}`),
				event("assistant.message", "a2", `{"content":"","chunkIndex":0}`),
				event("tool.execution_complete", "r1", `{"toolCallId":"c1","success":false,"error":{"message":"no match","code":"failure"}}`),
				event("tool.execution_complete", "r2", `{"toolCallId":"unknown","success":true,"result":{"content":"stray"}}`),
			},
			wantLines: []string{
				"0:user:text=edit it",
				"0:agent:tool(edit/write)=ERROR:no match",
			},
			wantName: "edit it",
		},
		{
			name: "agent output before any prompt opens its own exchange",
			events: []rawEvent{
				event("assistant.message", "a1", `{"content":"resumed"}`),
				event("user.message", "u1", `{"content":"next"}`),
			},
			wantLines: []string{
				"0:agent:text=resumed",
				"1:user:text=next",
			},
			wantName: "next",
		},
		{
			name: "task_complete's summary is the agent's closing answer, not a tool call",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"ship it"}`),
				event("assistant.message", "a1", `{"toolRequests":[{"toolCallId":"c1","name":"task_complete","arguments":{"summary":"Shipped."}}]}`),
				event("tool.execution_complete", "r1", `{"toolCallId":"c1","success":true,"result":{"content":"Task marked complete"}}`),
			},
			wantLines: []string{
				"0:user:text=ship it",
				"0:agent:text=Shipped.",
			},
			wantName: "ship it",
		},
		{
			name: "a message from another session is labeled input and does not name a session the user prompted",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"orchestrate"}`),
				event("assistant.message", "a1", `{"content":"started a worker"}`),
				event("user.message", "x1", `{"content":"Audit finished.","transformedContent":"<current_datetime>x</current_datetime>\n<cross_session_message>\nfrom_project_session_id: p1\nfrom_session_id: s2\nAudit finished.\n</cross_session_message>"}`),
				event("assistant.message", "a2", `{"content":"merging results"}`),
			},
			wantLines: []string{
				"0:user:text=orchestrate",
				"0:agent:text=started a worker",
				"1:user:text=_Message from Copilot session `s2`:_\n\nAudit finished.",
				"1:agent:text=merging results",
			},
			wantName: "orchestrate",
		},
		{
			name: "a typed prompt that quotes the envelope tag is the user's own prompt",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"why look for <cross_session_message>?","transformedContent":"<current_datetime>x</current_datetime>\n\nwhy look for <cross_session_message>?"}`),
				event("assistant.message", "a1", `{"content":"to label messages"}`),
				event("user.message", "u2", `{"content":"ok"}`),
				event("assistant.message", "bad", `{"content":7}`),
			},
			wantLines: []string{
				"0:user:text=why look for <cross_session_message>?",
				"0:agent:text=to label messages",
				"1:user:text=ok",
			},
			wantName: "why look for <cross_session_message>?",
		},
		{
			name: "a session another session drives is named by its first message",
			events: []rawEvent{
				event("user.message", "x1", `{"content":"Fix the login bug","transformedContent":"<cross_session_message>\nfrom_session_id: parent\nFix the login bug\n</cross_session_message>"}`),
				event("assistant.message", "a1", `{"content":"Fixed."}`),
			},
			wantLines: []string{
				"0:user:text=_Message from Copilot session `parent`:_\n\nFix the login bug",
				"0:agent:text=Fixed.",
			},
			wantName: "Fix the login bug",
		},
		{
			name: "the name never changes once set, even when the user later types into an orchestrated session",
			events: []rawEvent{
				event("user.message", "x1", `{"content":"Fix the login bug","transformedContent":"<cross_session_message>\nfrom_session_id: parent\nFix the login bug\n</cross_session_message>"}`),
				event("assistant.message", "a1", `{"content":"Fixed."}`),
				event("user.message", "u1", `{"content":"also add a test"}`),
			},
			wantLines: []string{
				"0:user:text=_Message from Copilot session `parent`:_\n\nFix the login bug",
				"0:agent:text=Fixed.",
				"1:user:text=also add a test",
			},
			wantName: "Fix the login bug",
		},
		{
			name: "a sub-agent prompt never names the session, even when it comes first",
			events: []rawEvent{
				event("user.message", "s0", `{"content":"Audit the repo","source":"agent-123"}`),
				event("user.message", "u1", `{"content":"what did it find?"}`),
			},
			wantLines: []string{
				"0:agent-sidechain:text=Audit the repo",
				"1:user:text=what did it find?",
			},
			wantName: "what did it find?",
		},
		{
			name: "session without a prompt has no transcript",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"continue","source":"autopilot"}`),
			},
			wantLines: nil,
			wantName:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTranscriptBuilder("/work")
			for _, e := range tt.events {
				builder.add(e)
			}
			got := renderExchanges(builder.finish())
			if !slices.Equal(got, tt.wantLines) {
				t.Errorf("exchanges mismatch\n got: %q\nwant: %q", got, tt.wantLines)
			}
			if builder.name != tt.wantName {
				t.Errorf("name = %q, want %q", builder.name, tt.wantName)
			}
		})
	}
}

func event(eventType, id, data string) rawEvent {
	return rawEvent{Type: eventType, ID: id, Timestamp: "2026-10-09T07:35:38.000Z", Data: json.RawMessage(data)}
}

// TestToolMarkdown pins the rendered tool bodies: Copilot's intent line, the
// shared Input list and capped Result, shell commands in a bash fence, and a
// failed call's error as its result.
func TestToolMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		request  string
		result   string
		wantBody string
	}{
		{
			name:     "shell call: intent, bash fence, capped result",
			request:  `{"toolCallId":"c1","name":"bash","intentionSummary":"List  files","arguments":{"command":"ls -a","description":"List files"}}`,
			result:   `{"toolCallId":"c1","success":true,"result":{"content":"a.go\nb.go\n\n\n"}}`,
			wantBody: "\nList files\n\n```bash\nls -a\n```\n\n**Result:**\n\n```\na.go\nb.go\n```\n",
		},
		{
			name:     "other tool: intent line, sorted input list, result",
			request:  `{"toolCallId":"c2","name":"view","intentionSummary":"Read main.go","arguments":{"path":"/w/main.go","view_range":[1,2]}}`,
			result:   `{"toolCallId":"c2","success":true,"result":{"content":"1. package main"}}`,
			wantBody: "\nRead main.go\n\n**Input:**\n\n- path: `/w/main.go`\n- view_range: `[1,2]`\n\n**Result:**\n\n```\n1. package main\n```\n",
		},
		{
			name:     "failed call shows its error as the result",
			request:  `{"toolCallId":"c3","name":"edit","arguments":{"path":"a.go"}}`,
			result:   `{"toolCallId":"c3","success":false,"error":{"message":"no match"}}`,
			wantBody: "\n**Input:**\n\n- path: `a.go`\n\n**Result:**\n\n```\nno match\n```\n",
		},
		{
			name:     "call with no result yet still renders its input",
			request:  `{"toolCallId":"c4","name":"bash","arguments":{"command":"make"}}`,
			wantBody: "\n```bash\nmake\n```\n",
		},
		{
			name:     "apply_patch's raw patch text is shown as its input argument",
			request:  `{"toolCallId":"c5","name":"apply_patch","arguments":"*** Begin Patch\n*** End Patch"}`,
			wantBody: "\n**Input:**\n\n- input:\n\n```\n*** Begin Patch\n*** End Patch\n```\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTranscriptBuilder("/w")
			builder.add(event("user.message", "u", `{"content":"go"}`))
			builder.add(event("assistant.message", "a", `{"toolRequests":[`+tt.request+`]}`))
			if tt.result != "" {
				builder.add(event("tool.execution_complete", "r", tt.result))
			}
			tool := builder.finish()[0].Messages[1].Tool
			if tool.Summary == nil || *tool.Summary != "Tool use: **"+tool.Name+"**" {
				t.Errorf("summary = %v, want plain tool-use line", tool.Summary)
			}
			if tool.FormattedMarkdown == nil || *tool.FormattedMarkdown != tt.wantBody {
				got := "<nil>"
				if tool.FormattedMarkdown != nil {
					got = *tool.FormattedMarkdown
				}
				t.Errorf("body mismatch\n got: %q\nwant: %q", got, tt.wantBody)
			}
		})
	}
}

// nativeSession is a small transcript holding one record of each class the
// provider distinguishes: conversation, unrendered lifecycle, harness
// machinery, and a kind no Copilot version has written yet. The user message
// carries a field the provider does not decode.
func nativeSession(t *testing.T, project string) []string {
	return []string{
		sessionStart(t, "s1", project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello", "futureField": "kept"}),
		record(t, "hook.start", "h1", map[string]any{"hookName": "preToolUse"}),
		record(t, "system.message", "sys", map[string]any{"content": "system prompt"}),
		record(t, "model.messages_snapshot", "m1", map[string]any{"messages": []any{"context"}}),
		record(t, "session.mode_changed", "mc", map[string]any{"newMode": "autopilot"}),
		record(t, "brand.new_kind", "n1", map[string]any{"x": 1}),
		record(t, eventAssistantMsg, "a1", map[string]any{"content": "hi"}),
	}
}

func TestRawDataKeepsEveryAcceptedRecord(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	lines := nativeSession(t, project)
	path := writeSession(t, home, "s1", lines...)

	session, err := NewProvider().GetAgentChatSessionByPath(path, project, false)
	if err != nil || session == nil {
		t.Fatalf("GetAgentChatSessionByPath = %v, %v", session, err)
	}
	if want := strings.Join(lines, "\n") + "\n"; session.RawData != want {
		t.Errorf("RawData = %q, want every native record verbatim %q", session.RawData, want)
	}
}

func TestDebugRawExport(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	lines := nativeSession(t, project)
	path := writeSession(t, home, "s1", lines...)
	p := NewProvider()

	t.Run("no flag writes nothing", func(t *testing.T) {
		debugRoot := testutil.IsolateDebugDir(t)
		if _, err := p.GetAgentChatSessionByPath(path, project, false); err != nil {
			t.Fatal(err)
		}
		if entries, _ := os.ReadDir(debugRoot); len(entries) != 0 {
			t.Errorf("debug files written without --debug-raw: %v", entries)
		}
	})

	t.Run("every accepted record with its full envelope, refreshed on change", func(t *testing.T) {
		debugRoot := testutil.IsolateDebugDir(t)
		if _, err := p.GetAgentChatSessionByPath(path, project, true); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(debugRoot, "s1")
		for i, line := range lines {
			data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(i+1)+".json"))
			if err != nil {
				t.Fatalf("record %d: %v", i+1, err)
			}
			var got, want any
			_ = json.Unmarshal(data, &got)
			_ = json.Unmarshal([]byte(line), &want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%d.json = %s, want the native record %s", i+1, data, line)
			}
		}

		// The session shrinks (a rewrite); stale numbered files must go while
		// the CLI-owned session-data.json stays.
		short := lines[:len(lines)-2]
		testutil.AssertDebugRefresh(t, dir,
			[]string{strconv.Itoa(len(lines)) + ".json", strconv.Itoa(len(lines)-1) + ".json"},
			[]string{"session-data.json"},
			func() {
				writeSession(t, home, "s1", short...)
				if _, err := p.GetAgentChatSessionByPath(path, project, true); err != nil {
					t.Fatal(err)
				}
			})
	})
}

func TestReadEventsSkipsBadRecordsAndKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), eventsFileName)
	content := `{"type":"a","id":"1"}` + "\n" +
		`{not json` + "\n" +
		"\n" +
		`{"type":"b","id":"2"}` + "\n" +
		`{"type":"c","id":"3","data":{"partial":` // still being written
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name      string
		live      bool
		wantLevel string
	}{
		{"a cut-off final line warns when read whole", false, "WARN"},
		{"a final line still being written is debug noise during a watch", true, "DEBUG"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(previous) })

			var ids []string
			if err := readEvents(path, tt.live, func(event rawEvent, _ []byte) bool {
				ids = append(ids, event.ID)
				return true
			}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids, []string{"1", "2"}) {
				t.Errorf("visited %v, want the two complete, valid records", ids)
			}
			levels := map[float64]string{}
			for line := range strings.Lines(logs.String()) {
				var entry map[string]any
				if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == "Skipping corrupted JSONL line" {
					levels[entry["line"].(float64)] = entry["level"].(string)
				}
			}
			if levels[2] != "WARN" || levels[5] != tt.wantLevel {
				t.Errorf("skip log levels by line = %v, want line 2 WARN and line 5 %s", levels, tt.wantLevel)
			}
		})
	}
}

// readInventory returns the tool names in testdata/tools.txt.
func readInventory(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "tools.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			names = append(names, line)
		}
	}
	return names
}

// TestToolInventorySweep checks the classifier against Copilot's real tool
// inventory in both directions: every inventoried tool has a deliberate type
// (so a tool dropped from the table surfaces here rather than as unknown), and
// every classifier entry is an inventoried tool (so no case is untestable).
func TestToolInventorySweep(t *testing.T) {
	inventory := readInventory(t)
	for _, name := range inventory {
		if got := toolType(name); got == schema.ToolTypeUnknown {
			t.Errorf("inventoried tool %q is unclassified", name)
		}
	}
	for name := range toolTypes {
		if !slices.Contains(inventory, name) {
			t.Errorf("classifier entry %q is not in testdata/tools.txt", name)
		}
	}

	spotChecks := map[string]string{
		"bash":                           schema.ToolTypeShell,
		"view":                           schema.ToolTypeRead,
		"web_fetch":                      schema.ToolTypeRead,
		"apply_patch":                    schema.ToolTypeWrite,
		"rg":                             schema.ToolTypeSearch,
		"github-mcp-server-search_code":  schema.ToolTypeSearch,
		"update_todo":                    schema.ToolTypeTask,
		"task":                           schema.ToolTypeGeneric,
		"sql":                            schema.ToolTypeGeneric,
		"captain-get_jira_issue":         schema.ToolTypeUnknown,
		"tool_from_a_future_copilot_rel": schema.ToolTypeUnknown,
	}
	for name, want := range spotChecks {
		if got := toolType(name); got != want {
			t.Errorf("toolType(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestCapturedToolExercise converts a real session (testdata/tool-exercise.jsonl,
// captured from Copilot CLI 1.0.95-2 exercising its tools, with harness hook
// and system-prompt records removed and home paths anonymized) and checks the
// properties the rendering depends on.
func TestCapturedToolExercise(t *testing.T) {
	home := useSessionStore(t)
	data, err := os.ReadFile(filepath.Join("testdata", "tool-exercise.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeSession(t, home, "capture", strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")...)
	session, err := NewProvider().GetAgentChatSessionByPath(path, "", false)
	if err != nil || session == nil {
		t.Fatalf("GetAgentChatSessionByPath = %v, %v", session, err)
	}
	sessionData := session.SessionData
	if !sessionData.Validate() {
		t.Error("captured session fails schema validation")
	}
	if sessionData.Provider.Version != "1.0.95-2" || sessionData.WorkspaceRoot != "/private/tmp/copilot-tools-exercise" {
		t.Errorf("provider/workspace = %+v / %q", sessionData.Provider, sessionData.WorkspaceRoot)
	}

	var prompts, sidechain, failed int
	seenTools := map[string]bool{}
	for _, exchange := range sessionData.Exchanges {
		for _, message := range exchange.Messages {
			if message.Role == schema.RoleUser {
				prompts++
			}
			if isSidechain, _ := message.Metadata[sidechainKey].(bool); isSidechain {
				sidechain++
			}
			if message.Tool == nil {
				continue
			}
			seenTools[message.Tool.Name] = true
			if message.Tool.Type == schema.ToolTypeUnknown {
				t.Errorf("built-in tool %q rendered as unknown", message.Tool.Name)
			}
			if message.Tool.Output == nil {
				t.Errorf("tool %s (%s) has no result paired", message.Tool.Name, message.Tool.UseID)
			}
			if isError, _ := message.Tool.Output["is_error"].(bool); isError {
				failed++
				if result, _ := message.Tool.Output["result"].(string); result == "" ||
					message.Tool.FormattedMarkdown == nil || !strings.Contains(*message.Tool.FormattedMarkdown, result) {
					t.Errorf("failed %s call does not render its error text", message.Tool.Name)
				}
			}
		}
	}
	if prompts != 2 {
		t.Errorf("user prompts = %d, want the 2 typed prompts (injections excluded)", prompts)
	}
	if sidechain == 0 {
		t.Error("the explore sub-agent's work is missing from the transcript")
	}
	if failed != 2 {
		t.Errorf("failed calls = %d, want the 2 the capture holds (view of a missing file, Copilot Spaces)", failed)
	}
	for _, name := range []string{"bash", "view", "create", "edit", "glob", "grep", "web_fetch", "task", "sql", "skill"} {
		if !seenTools[name] {
			t.Errorf("exercised tool %q missing from the transcript", name)
		}
	}
}
