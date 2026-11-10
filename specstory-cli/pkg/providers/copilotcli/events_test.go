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
		"copilotVersion": "1.0.105",
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
			name: "a typed prompt that only starts with the notification tag is the user's own prompt",
			events: []rawEvent{
				event("user.message", "u1", `{"content":"<system_notification> why does this tag show up in my logs?"}`),
				event("assistant.message", "a1", `{"content":"Because..."}`),
			},
			wantLines: []string{
				"0:user:text=<system_notification> why does this tag show up in my logs?",
				"0:agent:text=Because...",
			},
			wantName: "<system_notification> why does this tag show up in my logs?",
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
			wantBody: "\nList files\n\n```bash\nls -a\n```\n\n**Result:**\n\n```text\na.go\nb.go\n```\n",
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
			// A command that exits non-zero is a successful call, not an
			// error: Copilot appends the exit line to the output, so the exit
			// code must survive rendering alongside the command's own output.
			name:     "shell command exiting non-zero shows its output and exit code",
			request:  `{"toolCallId":"c6","name":"bash","arguments":{"command":"ls /missing"}}`,
			result:   `{"toolCallId":"c6","success":true,"result":{"content":"ls: /missing: No such file or directory\n<shellId: 0 completed with exit code 1>"},"shellExecution":{"exitCode":1}}`,
			wantBody: "\n```bash\nls /missing\n```\n\n**Result:**\n\n```text\nls: /missing: No such file or directory\n<shellId: 0 completed with exit code 1>\n```\n\nExit code: 1\n",
		},
		{
			name:     "shell output loses its terminal control bytes",
			request:  `{"toolCallId":"c7","name":"bash","arguments":{"command":"make"}}`,
			result:   `{"toolCallId":"c7","success":true,"result":{"content":"\u001b[32mok\u001b[0m\n"}}`,
			wantBody: "\n```bash\nmake\n```\n\n**Result:**\n\n```text\nok\n```\n",
		},
		{
			name:     "a shell tool with no command still shows its output as terminal text",
			request:  `{"toolCallId":"c8","name":"read_bash","arguments":{"shellId":"0"}}`,
			result:   `{"toolCallId":"c8","success":true,"result":{"content":"done"}}`,
			wantBody: "\n**Input:**\n\n- shellId: `0`\n\n**Result:**\n\n```text\ndone\n```\n",
		},
		{
			name:     "a powershell command is fenced as powershell",
			request:  `{"toolCallId":"c9","name":"powershell","arguments":{"command":"Get-ChildItem"}}`,
			wantBody: "\n```powershell\nGet-ChildItem\n```\n",
		},
		{
			name:     "edit: path and diff, then its confirmation",
			request:  `{"toolCallId":"c10","name":"edit","intentionSummary":"edit a.go","arguments":{"path":"/w/a.go","old_str":"one\ntwo","new_str":"one\n2"}}`,
			result:   `{"toolCallId":"c10","success":true,"result":{"content":"File updated."}}`,
			wantBody: "\nedit a.go\n\nPath: `/w/a.go`\n\n```diff\n-one\n-two\n+one\n+2\n```\n\n**Result:**\n\n```\nFile updated.\n```\n",
		},
		{
			name:     "edit that deletes text has no added side",
			request:  `{"toolCallId":"c11","name":"edit","arguments":{"path":"a.go","old_str":"gone","new_str":""}}`,
			wantBody: "\nPath: `a.go`\n\n```diff\n-gone\n```\n",
		},
		{
			name:     "failed edit keeps its diff and shows the error",
			request:  `{"toolCallId":"c12","name":"edit","arguments":{"path":"a.go","old_str":"x","new_str":"y"}}`,
			result:   `{"toolCallId":"c12","success":false,"error":{"message":"no match"}}`,
			wantBody: "\nPath: `a.go`\n\n```diff\n-x\n+y\n```\n\n**Result:**\n\n```\nno match\n```\n",
		},
		{
			name:     "create: path and a fence tagged by extension",
			request:  `{"toolCallId":"c13","name":"create","arguments":{"path":"/w/main.go","file_text":"package main\n"}}`,
			wantBody: "\nPath: `/w/main.go`\n\n```go\npackage main\n```\n",
		},
		{
			name:     "create with an unknown extension is fenced untagged",
			request:  `{"toolCallId":"c14","name":"create","arguments":{"path":"notes","file_text":"hello"}}`,
			wantBody: "\nPath: `notes`\n\n```\nhello\n```\n",
		},
		{
			name:     "an argument the renderer does not show stays listed",
			request:  `{"toolCallId":"c15","name":"edit","arguments":{"path":"a.go","old_str":"x","new_str":"y","replace_all":true}}`,
			wantBody: "\nPath: `a.go`\n\n```diff\n-x\n+y\n```\n\n**Input:**\n\n- replace_all: `true`\n",
		},
		{
			name:     "a JSON object result is indented in a json fence, keeping its key order",
			request:  `{"toolCallId":"c17","name":"github-mcp-server-search_code"}`,
			result:   `{"toolCallId":"c17","success":true,"result":{"content":"{\"zebra\":1,\"apple\":[2]}"}}`,
			wantBody: "\n**Result:**\n\n```json\n{\n  \"zebra\": 1,\n  \"apple\": [\n    2\n  ]\n}\n```\n",
		},
		{
			name:     "a JSON array result is indented too",
			request:  `{"toolCallId":"c18","name":"github-mcp-server-get_file_contents"}`,
			result:   `{"toolCallId":"c18","success":true,"result":{"content":"[{\"name\":\"a\"}]"}}`,
			wantBody: "\n**Result:**\n\n```json\n[\n  {\n    \"name\": \"a\"\n  }\n]\n```\n",
		},
		{
			name:     "a bare JSON value stays plain text",
			request:  `{"toolCallId":"c19","name":"task"}`,
			result:   `{"toolCallId":"c19","success":true,"result":{"content":"27"}}`,
			wantBody: "\n**Result:**\n\n```\n27\n```\n",
		},
		{
			name:     "text that only looks like JSON stays plain",
			request:  `{"toolCallId":"c20","name":"fetch_copilot_cli_documentation"}`,
			result:   `{"toolCallId":"c20","success":true,"result":{"content":"[Could not load README]"}}`,
			wantBody: "\n**Result:**\n\n```\n[Could not load README]\n```\n",
		},
		{
			name:     "a viewed JSON file keeps its own bytes",
			request:  `{"toolCallId":"c21","name":"view"}`,
			result:   `{"toolCallId":"c21","success":true,"result":{"content":"{\"a\":1}"}}`,
			wantBody: "\n**Result:**\n\n```\n{\"a\":1}\n```\n",
		},
		{
			name:     "a file tool without its content degrades to the Input list",
			request:  `{"toolCallId":"c16","name":"create","arguments":{"path":"a.go"}}`,
			wantBody: "\n**Input:**\n\n- path: `a.go`\n",
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

func TestRawDataKeepsAcceptedRecordsExceptPrivateOnes(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	lines := nativeSession(t, project)
	path := writeSession(t, home, "s1", lines...)

	session, err := NewProvider().GetAgentChatSessionByPath(path, project, false)
	if err != nil || session == nil {
		t.Fatalf("GetAgentChatSessionByPath = %v, %v", session, err)
	}
	var want strings.Builder
	for _, line := range lines {
		if !strings.Contains(line, `"type":"system.message"`) && !strings.Contains(line, `"type":"model.`) {
			want.WriteString(line + "\n")
		}
	}
	if session.RawData != want.String() {
		t.Errorf("RawData = %q, want every record but the system prompt and model calls, verbatim: %q", session.RawData, want.String())
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

	t.Run("an unsafe header ID cannot steer the debug directory", func(t *testing.T) {
		debugRoot := testutil.IsolateDebugDir(t)
		hostile := writeSession(t, home, "s2",
			sessionStart(t, "../../escaped", project),
			record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
		)
		session, err := p.GetAgentChatSessionByPath(hostile, project, true)
		if err != nil || session == nil {
			t.Fatalf("GetAgentChatSessionByPath = %v, %v", session, err)
		}
		if session.SessionID != "s2" {
			t.Errorf("SessionID = %q, want the directory's name", session.SessionID)
		}
		if _, err := os.Stat(filepath.Join(debugRoot, "s2", "1.json")); err != nil {
			t.Errorf("debug records not under the directory's name: %v", err)
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
		"semantic_code_search":           schema.ToolTypeSearch,
		"powershell":                     schema.ToolTypeShell,
		"write_powershell":               schema.ToolTypeShell,
		"write_bash":                     schema.ToolTypeShell,
		"local_shell":                    schema.ToolTypeShell,
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
// captured from Copilot CLI 1.0.105 exercising its tools, with the system prompt,
// model-call records and an attached image removed, and account data and home
// paths anonymized) and checks the
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
	if sessionData.Provider.Version != "1.0.105" || sessionData.WorkspaceRoot != "/private/tmp/copilot-v3" {
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
	if prompts != 9 {
		t.Errorf("user turns = %d, want the 8 typed prompts and 1 `!` command (injections excluded)", prompts)
	}
	if sidechain == 0 {
		t.Error("the explore sub-agent's work is missing from the transcript")
	}
	if failed != 4 {
		t.Errorf("failed calls = %d, want the 4 the capture holds (view of a missing file, 2 workflow calls with extensions off, an unknown workflow)", failed)
	}
	for _, name := range []string{
		"bash", "view", "create", "edit", "glob", "grep", "web_fetch", "task", "sql", "skill",
		"ask_user", "semantic_code_search", "run_dynamic_workflow", "read_agent", "write_agent",
	} {
		if !seenTools[name] {
			t.Errorf("exercised tool %q missing from the transcript", name)
		}
	}
}

// A failing command's output can exceed the result cap, which cuts off the exit
// line Copilot appends; the exit code is rendered outside the cap so the
// failure survives in the markdown and in cross-agent resume.
func TestLongFailingShellOutputKeepsExitCode(t *testing.T) {
	output := strings.Repeat("x", spi.ToolResultCap+500) + "\n<shellId: 0 completed with exit code 2>"
	result, err := json.Marshal(map[string]any{
		"toolCallId":     "c1",
		"success":        true,
		"result":         map[string]any{"content": output},
		"shellExecution": map[string]any{"exitCode": 2},
	})
	if err != nil {
		t.Fatal(err)
	}

	builder := newTranscriptBuilder("/w")
	builder.add(event("user.message", "u", `{"content":"go"}`))
	builder.add(event("assistant.message", "a", `{"toolRequests":[{"toolCallId":"c1","name":"bash","arguments":{"command":"make"}}]}`))
	builder.add(event("tool.execution_complete", "r", string(result)))

	body := *builder.finish()[0].Messages[1].Tool.FormattedMarkdown
	if strings.Contains(body, "completed with exit code 2") {
		t.Fatalf("test setup: the exit line should have been cut by the result cap")
	}
	if !strings.HasSuffix(body, "\nExit code: 2\n") {
		t.Errorf("body does not end with the exit code:\n%s", body[max(0, len(body)-200):])
	}
}

// A tool call is stamped with when it finished, not when the agent asked for
// it, so a write held by a permission prompt still lines up with its file
// change in provenance.
func TestToolMessageTakesCompletionTime(t *testing.T) {
	const (
		requested  = "2026-10-09T07:35:38.000Z"
		nextPrompt = "2026-10-09T07:35:50.000Z"
		completed  = "2026-10-09T07:36:20.000Z" // well past provenance's 5s window
	)
	builder := newTranscriptBuilder("/w")
	builder.add(event("user.message", "u", `{"content":"go"}`))
	request := event("assistant.message", "a", `{"toolRequests":[{"toolCallId":"c1","name":"edit","arguments":{"path":"/w/a.go"}}]}`)
	request.Timestamp = requested
	builder.add(request)
	// The result can land after the exchange has closed.
	next := event("user.message", "u2", `{"content":"next"}`)
	next.Timestamp = nextPrompt
	builder.add(next)
	result := event("tool.execution_complete", "r", `{"toolCallId":"c1","success":true,"result":{"content":"ok"}}`)
	result.Timestamp = completed
	builder.add(result)

	exchanges := builder.finish()
	if got := exchanges[0].Messages[1].Timestamp; got != completed {
		t.Errorf("tool message timestamp = %q, want completion time %q", got, completed)
	}
	// The late result extends the exchange that made the call, not the one
	// that happened to be open when it arrived.
	if exchanges[0].EndTime != completed {
		t.Errorf("first exchange EndTime = %q, want its tool's completion time %q", exchanges[0].EndTime, completed)
	}
	if exchanges[1].EndTime != nextPrompt {
		t.Errorf("second exchange EndTime = %q, want its own prompt's time %q", exchanges[1].EndTime, nextPrompt)
	}
}

// Parallel calls can finish out of order, so an exchange ends at its latest
// message rather than its last one.
func TestExchangeEndsAtItsLatestMessage(t *testing.T) {
	const (
		slowDone = "2026-10-09T07:36:20.000Z"
		fastDone = "2026-10-09T07:35:40.000Z"
	)
	builder := newTranscriptBuilder("/w")
	builder.add(event("user.message", "u", `{"content":"go"}`))
	builder.add(event("assistant.message", "a", `{"toolRequests":[{"toolCallId":"slow","name":"bash","arguments":{"command":"make"}},{"toolCallId":"fast","name":"bash","arguments":{"command":"ls"}}]}`))
	fast := event("tool.execution_complete", "r1", `{"toolCallId":"fast","success":true,"result":{"content":"ok"}}`)
	fast.Timestamp = fastDone
	builder.add(fast)
	slow := event("tool.execution_complete", "r2", `{"toolCallId":"slow","success":true,"result":{"content":"ok"}}`)
	slow.Timestamp = slowDone
	builder.add(slow)

	if got := builder.finish()[0].EndTime; got != slowDone {
		t.Errorf("EndTime = %q, want the later completion %q", got, slowDone)
	}
}

// outputTokens covers a whole native message, which can become several schema
// messages or have no text at all. Stats sum per message, so it must land
// exactly once, on the first.
func TestOutputTokensCountedOnce(t *testing.T) {
	tests := []struct {
		name string
		data string
		want []int
	}{
		{
			name: "text and tool calls",
			data: `{"content":"hi","outputTokens":40,"toolRequests":[{"toolCallId":"c1","name":"view","arguments":{}},{"toolCallId":"c2","name":"view","arguments":{}}]}`,
			want: []int{40, 0, 0},
		},
		{
			name: "tool calls only",
			data: `{"outputTokens":30,"toolRequests":[{"toolCallId":"c1","name":"view","arguments":{}},{"toolCallId":"c2","name":"view","arguments":{}}]}`,
			want: []int{30, 0},
		},
		{
			name: "task_complete summary only",
			data: `{"outputTokens":20,"toolRequests":[{"toolCallId":"c1","name":"task_complete","arguments":{"summary":"done"}}]}`,
			want: []int{20},
		},
		{
			name: "model that records no count",
			data: `{"toolRequests":[{"toolCallId":"c1","name":"view","arguments":{}}]}`,
			want: []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTranscriptBuilder("/w")
			builder.add(event("user.message", "u", `{"content":"go"}`))
			builder.add(event("assistant.message", "a", tt.data))

			var got []int
			for _, message := range builder.finish()[0].Messages {
				if message.Role != schema.RoleAgent {
					continue
				}
				tokens := 0
				if message.Usage != nil {
					tokens = message.Usage.OutputTokens
				}
				got = append(got, tokens)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("output tokens per message = %v, want %v", got, tt.want)
			}
		})
	}
}

// apply_patch carries no path argument, so the files it writes come from its
// patch headers, including a rename's destination.
func TestApplyPatchPathHints(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: /w/new.go\n+package main\n*** Update File: /w/old.go\n*** Move to: /w/moved.go\n@@\n-a\n+b\n*** End Patch"
	arguments, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	builder := newTranscriptBuilder("/w")
	builder.add(event("user.message", "u", `{"content":"go"}`))
	builder.add(event("assistant.message", "a", `{"toolRequests":[{"toolCallId":"c1","name":"apply_patch","arguments":`+string(arguments)+`}]}`))

	got := builder.finish()[0].Messages[1].PathHints
	if want := []string{"new.go", "old.go", "moved.go"}; !slices.Equal(got, want) {
		t.Errorf("PathHints = %v, want %v", got, want)
	}
}

// A `!command` is the user's own activity: it opens an exchange, labeled so it
// cannot be mistaken for an instruction, and its result joins that message
// rather than being dropped as a result with no request.
func TestUserShellCommand(t *testing.T) {
	const label = "User ran a shell command:\n\n```bash\nmake test\n```"
	tests := []struct {
		name      string
		toolName  string
		arguments string
		result    string
		wantLines []string
	}{
		{
			name:      "output joins the labeled command",
			toolName:  "local_shell",
			arguments: `{"command":"make test"}`,
			result:    `{"toolCallId":"c1","success":true,"result":{"content":"ok\n\n"}}`,
			wantLines: []string{"1:user:text=" + label + ",text=Output:\n\n```text\nok\n```"},
		},
		{
			name:      "a non-zero exit is shown with the output",
			toolName:  "local_shell",
			arguments: `{"command":"make test"}`,
			result:    `{"toolCallId":"c1","success":true,"result":{"content":"boom"},"shellExecution":{"exitCode":2}}`,
			wantLines: []string{"1:user:text=" + label + ",text=Output:\n\n```text\nboom\n```\n\nExit code: 2"},
		},
		{
			name:      "a failed call shows its error",
			toolName:  "local_shell",
			arguments: `{"command":"make test"}`,
			result:    `{"toolCallId":"c1","success":false,"error":{"message":"denied"}}`,
			wantLines: []string{"1:user:text=" + label + ",text=Output:\n\n```text\ndenied\n```"},
		},
		{
			name:      "a command with no result yet still appears",
			toolName:  "local_shell",
			arguments: `{"command":"make test"}`,
			wantLines: []string{"1:user:text=" + label},
		},
		{
			name:      "a user-started tool that is not a shell is left to the raw data",
			toolName:  "view",
			arguments: `{"path":"a.go"}`,
			result:    `{"toolCallId":"c1","success":true,"result":{"content":"package a"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTranscriptBuilder("/w")
			builder.add(event("user.message", "u", `{"content":"hi"}`))
			builder.add(event(eventToolUserRequested, "q", `{"toolCallId":"c1","toolName":"`+tt.toolName+`","arguments":`+tt.arguments+`}`))
			if tt.result != "" {
				builder.add(event("tool.execution_complete", "r", tt.result))
			}
			got := renderExchanges(builder.finish())[1:]
			if !slices.Equal(got, tt.wantLines) {
				t.Errorf("exchanges mismatch\n got: %q\nwant: %q", got, tt.wantLines)
			}
			if builder.name != "hi" {
				t.Errorf("name = %q, want it left on the first prompt", builder.name)
			}
		})
	}
}

// Copilot writes a call's usage record just before its message, joined by
// apiCallId. Models that never write outputTokens depend on this record.
func TestUsageRecordsReachTheirMessages(t *testing.T) {
	const record = `{"usage":{"apiCallId":"call-1","inputTokens":100,"outputTokens":7,"cacheReadTokens":60,"cacheWriteTokens":30,"reasoningTokens":3}}`
	full := schema.Usage{InputTokens: 100, OutputTokens: 7, CachedInputTokens: 60, CacheCreationInputTokens: 30, ReasoningOutputTokens: 3}
	tests := []struct {
		name    string
		records []string
		message string
		want    []schema.Usage
	}{
		{
			name:    "a message with no count of its own",
			records: []string{record},
			message: `{"apiCallId":"call-1","content":"hi"}`,
			want:    []schema.Usage{full},
		},
		{
			name:    "the record wins over the message's output count",
			records: []string{record},
			message: `{"apiCallId":"call-1","content":"hi","outputTokens":7}`,
			want:    []schema.Usage{full},
		},
		{
			name:    "several schema messages from one call count once",
			records: []string{record},
			message: `{"apiCallId":"call-1","toolRequests":[{"toolCallId":"c1","name":"view","arguments":{}},{"toolCallId":"c2","name":"view","arguments":{}}]}`,
			want:    []schema.Usage{full, {}},
		},
		{
			name:    "another call's record is not borrowed",
			records: []string{record},
			message: `{"apiCallId":"call-2","content":"hi"}`,
			want:    []schema.Usage{{}},
		},
		{
			name:    "no record falls back on the message's output count",
			message: `{"apiCallId":"call-1","content":"hi","outputTokens":40}`,
			want:    []schema.Usage{{OutputTokens: 40}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := newTranscriptBuilder("/w")
			builder.add(event("user.message", "u", `{"content":"go"}`))
			for _, data := range tt.records {
				builder.add(event(eventUsageRecord, "usage", data))
			}
			builder.add(event("assistant.message", "a", tt.message))

			var got []schema.Usage
			for _, message := range builder.finish()[0].Messages[1:] {
				usage := schema.Usage{}
				if message.Usage != nil {
					usage = *message.Usage
				}
				got = append(got, usage)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("usage per message = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A usage record is a counter, not conversation: it must not move the session's
// update time, or a session would look active without a new message.
func TestUsageRecordDoesNotAdvanceTheTranscript(t *testing.T) {
	builder := newTranscriptBuilder("/w")
	builder.add(event("user.message", "u", `{"content":"go"}`))
	before := builder.lastTimestamp
	record := event(eventUsageRecord, "usage", `{"usage":{"apiCallId":"call-1","outputTokens":7}}`)
	record.Timestamp = "2030-01-01T00:00:00.000Z"
	builder.add(record)
	if builder.lastTimestamp != before {
		t.Errorf("lastTimestamp = %q, want %q", builder.lastTimestamp, before)
	}
}

// The captured session writes a usage record for every model call, so every
// agent turn in it must end up with its tokens.
func TestCapturedSessionCarriesUsage(t *testing.T) {
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
	var withUsage, input int
	for _, exchange := range session.SessionData.Exchanges {
		for _, message := range exchange.Messages {
			if message.Usage != nil {
				withUsage++
				input += message.Usage.InputTokens
			}
		}
	}
	if want := strings.Count(string(data), `"type":"session.usage_record"`); withUsage != want || input == 0 {
		t.Errorf("%d messages with usage (%d input tokens), want one per usage record (%d)", withUsage, input, want)
	}
}

// On Windows the shell tool is powershell. It must be treated as bash is, or its
// writes get no path hints and provenance skips them.
func TestPowerShellCallIsAShellCall(t *testing.T) {
	builder := newTranscriptBuilder("/w")
	builder.add(event("user.message", "u", `{"content":"go"}`))
	builder.add(event("assistant.message", "a", `{"toolRequests":[{"toolCallId":"c1","name":"powershell","arguments":{"command":"echo hi > /w/out.txt"}}]}`))

	message := builder.finish()[0].Messages[1]
	if message.Tool.Type != schema.ToolTypeShell {
		t.Errorf("type = %q, want shell", message.Tool.Type)
	}
	if !strings.Contains(*message.Tool.FormattedMarkdown, "```powershell\necho hi > /w/out.txt\n```") {
		t.Errorf("command not fenced: %q", *message.Tool.FormattedMarkdown)
	}
	if want := []string{"out.txt"}; !slices.Equal(message.PathHints, want) {
		t.Errorf("PathHints = %v, want %v", message.PathHints, want)
	}
}

// A session with no recorded directory still needs a root to normalize paths
// against, but the fallback must not read as the session's own origin.
func TestWorkspaceRootFallback(t *testing.T) {
	launchDir := newProjectDir(t, "launch")
	start := func(t *testing.T, cwd string) string {
		data := map[string]any{"sessionId": "s1", "startTime": "2026-10-09T07:35:38.043Z"}
		if cwd != "" {
			data["context"] = map[string]any{"cwd": cwd}
		}
		return record(t, eventSessionStart, "start", data)
	}
	tests := []struct {
		name      string
		recorded  string
		originCwd string
		wantRoot  string
		editPath  string
		wantHints []string
	}{
		{"the recorded directory wins", "/recorded", "/caller", "/recorded", "/recorded/a.go", []string{"a.go"}},
		{"no record uses the caller's path", "", "/caller", "/caller", "/caller/a.go", []string{"a.go"}},
		{"no record or caller uses the process directory", "", "", launchDir, "a.go", []string{"a.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := useSessionStore(t)
			t.Chdir(launchDir)
			path := writeSession(t, home, "s1",
				start(t, tt.recorded),
				record(t, eventUserMessage, "u1", map[string]any{"content": "go"}),
				record(t, eventAssistantMsg, "a1", map[string]any{"toolRequests": []any{map[string]any{
					"toolCallId": "c1", "name": "edit", "arguments": map[string]any{"path": tt.editPath, "old_str": "x", "new_str": "y"},
				}}}),
			)
			session, err := NewProvider().GetAgentChatSessionByPath(path, tt.originCwd, false)
			if err != nil || session == nil {
				t.Fatalf("GetAgentChatSessionByPath = %v, %v", session, err)
			}
			if session.SessionData.WorkspaceRoot != tt.wantRoot {
				t.Errorf("WorkspaceRoot = %q, want %q", session.SessionData.WorkspaceRoot, tt.wantRoot)
			}
			if !session.SessionData.Validate() {
				t.Error("session data fails schema validation")
			}
			if hints := session.SessionData.Exchanges[0].Messages[1].PathHints; !slices.Equal(hints, tt.wantHints) {
				t.Errorf("PathHints = %v, want %v normalized against the root", hints, tt.wantHints)
			}
		})
	}
}

// The fallback root must stay out of enumeration: reindex attributes a session
// by OriginCwd, and a guessed origin would file it under the wrong project.
func TestSessionWithoutADirectoryHasNoOrigin(t *testing.T) {
	home := useSessionStore(t)
	t.Chdir(newProjectDir(t, "launch"))
	writeSession(t, home, "s1",
		record(t, eventSessionStart, "start", map[string]any{"sessionId": "s1"}),
		record(t, eventUserMessage, "u1", map[string]any{"content": "go"}),
	)
	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].OriginCwd != "" {
		t.Errorf("refs = %+v, want one session with no OriginCwd", refs)
	}
}

// An unfamiliar source could be Copilot tagging typed prompts in a new way, which
// would drop them, so it warns. It warns once, because a watch re-reads a session
// on every change, and known injections stay quiet because they arrive every turn.
func TestUnfamiliarInputSourceWarnsOnce(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantWarns int
	}{
		{"unfamiliar source", "brand-new-host", 1},
		{"known injection", "autopilot", 0},
		{"skill injection", "skill-pdf", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnedSources.Clear()
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			message := userMessageData{Content: "hello", Source: tt.source}
			for range 3 {
				if kind := classifyUserMessage(message); kind != machinery {
					t.Fatalf("classifyUserMessage = %v, want machinery", kind)
				}
			}
			if got := strings.Count(logs.String(), "level=WARN"); got != tt.wantWarns {
				t.Errorf("%d warnings, want %d:\n%s", got, tt.wantWarns, logs.String())
			}
		})
	}
}

// TestCapturedUserShellCommands converts the records of a real interactive session
// (testdata/user-shell.jsonl, from Copilot CLI 1.0.105, reduced to its session,
// tool and message records): a `!` command that succeeds, one that fails, and a
// prompt. A slash command was also run; Copilot does not record it.
func TestCapturedUserShellCommands(t *testing.T) {
	home := useSessionStore(t)
	data, err := os.ReadFile(filepath.Join("testdata", "user-shell.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeSession(t, home, "capture", strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")...)
	session, err := NewProvider().GetAgentChatSessionByPath(path, "", false)
	if err != nil || session == nil {
		t.Fatalf("GetAgentChatSessionByPath = %v, %v", session, err)
	}
	want := []string{
		"0:user:text=User ran a shell command:\n\n```bash\necho marker-77\n```,text=Output:\n\n```text\nmarker-77\n```",
		"1:user:text=User ran a shell command:\n\n```bash\nls /no-such-dir\n```,text=Output:\n\n```text\nls: /no-such-dir: No such file or directory\n```\n\nExit code: 1",
		"2:user:text=Reply with the word done",
		"2:agent:text=done",
	}
	if got := renderExchanges(session.SessionData.Exchanges); !slices.Equal(got, want) {
		t.Errorf("exchanges mismatch\n got: %q\nwant: %q", got, want)
	}
	if session.Slug != "reply-with-the-word" {
		t.Errorf("slug = %q, want it named by the prompt, not a command", session.Slug)
	}
	if !session.SessionData.Validate() {
		t.Error("captured session fails schema validation")
	}
}
