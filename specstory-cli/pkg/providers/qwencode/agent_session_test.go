package qwencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadSession(t *testing.T, name string) *QwenSession {
	t.Helper()
	session, err := ParseSessionFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", name, err)
	}
	return session
}

func TestGenerateAgentSession_Basic(t *testing.T) {
	session := loadSession(t, "session-basic.jsonl")

	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	if data.SchemaVersion != "1.0" {
		t.Errorf("SchemaVersion = %q, want 1.0", data.SchemaVersion)
	}
	if data.Provider.ID != "qwen" || data.Provider.Name != "Qwen Code" {
		t.Errorf("Provider = %+v, want qwen/Qwen Code", data.Provider)
	}
	if data.Provider.Version != "0.21.7" {
		t.Errorf("Provider.Version = %q, want the Qwen version from the transcript", data.Provider.Version)
	}
	if data.SessionID != session.ID {
		t.Errorf("SessionID = %q, want %q", data.SessionID, session.ID)
	}
	if data.CreatedAt != "2026-08-07T16:52:35.916Z" {
		t.Errorf("CreatedAt = %q", data.CreatedAt)
	}

	// Two real user turns → two exchanges. The notification user record must
	// not start a third.
	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2", len(data.Exchanges))
	}

	if !data.Validate() {
		t.Error("SessionData failed validation")
	}

	// First exchange: user + thinking + text
	first := data.Exchanges[0]
	if len(first.Messages) != 3 {
		t.Fatalf("first exchange message count = %d, want 3", len(first.Messages))
	}
	if first.Messages[0].Role != "user" {
		t.Errorf("first message role = %q, want user", first.Messages[0].Role)
	}
	if first.Messages[1].Content[0].Type != "thinking" {
		t.Errorf("second message content type = %q, want thinking", first.Messages[1].Content[0].Type)
	}
	if first.Messages[2].Content[0].Type != "text" {
		t.Errorf("third message content type = %q, want text", first.Messages[2].Content[0].Type)
	}
	if first.Messages[2].Model != "qwen3-coder-plus" {
		t.Errorf("agent message model = %q", first.Messages[2].Model)
	}

	// Usage attached to the last message of the assistant record only
	if first.Messages[1].Usage != nil {
		t.Error("usage should not be attached to the thinking message")
	}
	usage := first.Messages[2].Usage
	if usage == nil {
		t.Fatal("usage missing from last agent message")
	}
	if usage.InputTokens != 37412 || usage.OutputTokens != 377 || usage.ThoughtTokens != 30 || usage.CachedTokens != 100 {
		t.Errorf("usage = %+v", usage)
	}

	// Second exchange: user + thinking + tool (read_file with folded output) + text
	second := data.Exchanges[1]
	var tool *ToolInfo
	for i := range second.Messages {
		if second.Messages[i].Tool != nil {
			tool = second.Messages[i].Tool
			break
		}
	}
	if tool == nil {
		t.Fatal("no tool message in second exchange")
	}
	if tool.Name != "read_file" || tool.Type != "read" || tool.UseID != "call_1" {
		t.Errorf("tool = %+v", tool)
	}
	if out, _ := tool.Output["output"].(string); !strings.Contains(out, "# Project") {
		t.Errorf("tool output not folded in: %v", tool.Output)
	}
	if status, _ := tool.Output["status"].(string); status != "success" {
		t.Errorf("tool output status = %q, want success", status)
	}
}

func TestGenerateAgentSession_ToolFoldingAndErrors(t *testing.T) {
	session := loadSession(t, "session-tools.jsonl")

	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if !data.Validate() {
		t.Error("SessionData failed validation")
	}

	if len(data.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(data.Exchanges))
	}

	tools := map[string]*ToolInfo{}
	for i := range data.Exchanges[0].Messages {
		if tool := data.Exchanges[0].Messages[i].Tool; tool != nil {
			tools[tool.UseID] = tool
		}
	}
	if len(tools) != 4 {
		t.Fatalf("tool message count = %d, want 4 (one per parallel functionCall)", len(tools))
	}

	edit := tools["call_edit"]
	if edit == nil {
		t.Fatal("edit tool message missing")
	}
	if edit.Type != "write" {
		t.Errorf("edit tool type = %q, want write", edit.Type)
	}
	if display, _ := edit.Output["resultDisplay"].(string); !strings.Contains(display, "@@ -3,3 +3,6 @@") {
		t.Errorf("edit output missing fileDiff display: %v", edit.Output["resultDisplay"])
	}

	shell := tools["call_shell"]
	if shell == nil {
		t.Fatal("shell tool message missing")
	}
	if display, _ := shell.Output["resultDisplay"].(string); display != "2 passed in 0.01s" {
		t.Errorf("shell resultDisplay = %q", display)
	}

	missing := tools["call_missing"]
	if missing == nil {
		t.Fatal("failed read tool message missing")
	}
	if errText, _ := missing.Output["error"].(string); !strings.Contains(errText, "File not found") {
		t.Errorf("error output not folded in: %v", missing.Output)
	}
	if status, _ := missing.Output["status"].(string); status != "error" {
		t.Errorf("status = %q, want error", status)
	}
	if errType, _ := missing.Output["errorType"].(string); errType != "file_not_found" {
		t.Errorf("errorType = %q, want file_not_found", errType)
	}

	// Path hints
	var editMsg *Message
	for i := range data.Exchanges[0].Messages {
		if data.Exchanges[0].Messages[i].Tool == edit {
			editMsg = &data.Exchanges[0].Messages[i]
		}
	}
	if editMsg == nil || len(editMsg.PathHints) == 0 {
		t.Error("edit tool message missing path hints")
	}
}

func TestGenerateAgentSession_CompressionAndSlashCommandsSkipped(t *testing.T) {
	session := loadSession(t, "session-compression.jsonl")

	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if !data.Validate() {
		t.Error("SessionData failed validation")
	}

	// Two real user turns → two exchanges; the chat_compression and
	// slash_command system records must not create exchanges or messages.
	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2", len(data.Exchanges))
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "state_snapshot") {
					t.Errorf("compression summary leaked into message content: %q", part.Text)
				}
				if strings.Contains(part.Text, "/compress") {
					t.Errorf("slash command leaked into message content: %q", part.Text)
				}
			}
		}
	}
}

func TestGenerateAgentSession_EmptySession(t *testing.T) {
	if _, err := GenerateAgentSession(&QwenSession{ID: "x"}, "/tmp"); err == nil {
		t.Error("expected error for session with no records")
	}
}

func TestGenerateAgentSession_SystemOnlySessionHasNoExchanges(t *testing.T) {
	session := loadSession(t, "session-system-only.jsonl")
	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if len(data.Exchanges) != 0 {
		t.Errorf("exchange count = %d, want 0", len(data.Exchanges))
	}
}

func TestClassifyQwenToolType(t *testing.T) {
	tests := []struct {
		tool string
		want string
	}{
		{"read_file", "read"},
		{"web_fetch", "read"},
		{"write_file", "write"},
		{"edit", "write"},
		{"grep_search", "search"},
		{"glob", "search"},
		{"run_shell_command", "shell"},
		{"list_directory", "search"},
		{"monitor", "shell"},
		{"todo_write", "task"},
		{"task", "task"},
		{"skill", "generic"},
		{"computer_use__get_screen_size", "generic"},
		{"some_new_tool", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			if got := classifyQwenToolType(tt.tool); got != tt.want {
				t.Errorf("classifyQwenToolType(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestAssistantPartOrderAndUniqueIDs(t *testing.T) {
	record := QwenRecord{UUID: "a", Type: "assistant", Timestamp: "2026-09-15T00:00:00Z", Message: &QwenMessage{Parts: []QwenPart{
		{Text: "before"}, {FunctionCall: &QwenFunctionCall{ID: "call", Name: "read_file"}}, {Text: "thinking", Thought: true}, {Text: "after"},
	}}, UsageMetadata: &QwenUsageMetadata{PromptTokenCount: 12}}
	msgs := buildAgentMessages(&record, nil, "/project")
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want each part in order", len(msgs))
	}
	if msgs[0].Content[0].Text != "before" || msgs[1].Tool == nil || msgs[2].Content[0].Type != "thinking" || msgs[3].Content[0].Text != "after" {
		t.Fatal("native part order lost")
	}
	ids := map[string]bool{}
	for i, msg := range msgs {
		if ids[msg.ID] {
			t.Fatal("duplicate message id")
		}
		ids[msg.ID] = true
		if i < 3 && msg.Usage != nil {
			t.Fatal("usage duplicated")
		}
	}
	if msgs[3].Usage == nil {
		t.Fatal("usage dropped")
	}
}

// Expectations come from the vendor declaration, not the classifier under test.
func TestDeclaredToolInventory(t *testing.T) {
	data, err := os.ReadFile("testdata/tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct{ Tools []struct{ Name, Type string } }
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Tools) == 0 {
		t.Fatal("empty tool inventory")
	}
	for _, tool := range inventory.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			if got := classifyQwenToolType(tool.Name); got != tool.Type {
				t.Errorf("got %s, want %s", got, tool.Type)
			}
		})
	}
}

func TestCurrentQwenRecorderFixture(t *testing.T) {
	session := loadSession(t, "session-current.jsonl")
	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatal(err)
	}
	if data.Provider.Version != "0.23.4" || !data.Validate() {
		t.Fatal("current recorder fixture failed conversion")
	}
	tools := 0
	for _, exchange := range data.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool != nil {
				tools++
				if msg.Tool.Name != "run_shell_command" || msg.Tool.FormattedMarkdown == nil || !strings.Contains(*msg.Tool.FormattedMarkdown, "tool-review-ok") {
					t.Fatalf("native shell result lost: %+v", msg.Tool)
				}
			}
		}
	}
	if tools != 1 {
		t.Fatalf("got %d tools, want native shell call exactly once", tools)
	}
}

func TestGenerateAgentSession_BackgroundNotifications(t *testing.T) {
	callRecord := QwenRecord{Type: "assistant", UUID: "calls", Message: &QwenMessage{Parts: []QwenPart{
		{FunctionCall: &QwenFunctionCall{ID: "agent-call", Name: "agent"}},
		{FunctionCall: &QwenFunctionCall{ID: "monitor-call", Name: "monitor"}},
		{FunctionCall: &QwenFunctionCall{ID: "other-call", Name: "read_file"}},
	}}}
	notifications := `<task-notification><tool-use-id>agent-call</tool-use-id><task-id>agent-task</task-id><status>completed</status><result>PONG &amp; &lt;done&gt;</result><usage><total_tokens>42</total_tokens></usage></task-notification>
<task-notification><tool-use-id>monitor-call</tool-use-id><status>running</status><event-count>1</event-count><result>tick 1</result></task-notification>
<task-notification><tool-use-id>agent-call</tool-use-id><status>cancelled</status><result>Request was aborted.</result><result>Error: stopped</result></task-notification>
<task-notification><tool-use-id>monitor-call</tool-use-id><status>running</status><event-count>2</event-count><result>tick 2</result></task-notification>
<task-notification><tool-use-id>monitor-call</tool-use-id><status>completed</status><result>Exited with code 0</result></task-notification>
<task-notification><tool-use-id>unknown-call</tool-use-id><result>orphan</result></task-notification>
<task-notification><result>no call id</result></task-notification>
<task-notification><tool-use-id>agent-call</tool-use-id><result>malformed</task-notification>`
	notice := QwenRecord{Type: "user", Provenance: "system", Subtype: "notification", Timestamp: "2026-09-15T21:30:30Z", Message: &QwenMessage{Parts: []QwenPart{{Text: notifications}}}}
	session := &QwenSession{ID: "background", Records: []QwenRecord{
		{Type: "user", Provenance: "real_user", Message: &QwenMessage{Parts: []QwenPart{{Text: "Try the tools"}}}},
		callRecord,
		notice,
		// Collecting later response records must not replace notifications.
		{Type: "tool_result", Message: &QwenMessage{Parts: []QwenPart{{FunctionResponse: &QwenFunctionResponse{ID: "agent-call", Response: map[string]any{"output": "Background agent launched."}}}}}, ToolCallResult: &QwenToolCallResult{Status: "success"}},
		{Type: "tool_result", Message: &QwenMessage{Parts: []QwenPart{{FunctionResponse: &QwenFunctionResponse{ID: "monitor-call", Response: map[string]any{"output": "Monitor started."}}}}}},
	}}
	data, err := GenerateAgentSession(session, "/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Exchanges) != 1 || len(data.Exchanges[0].Messages) != 4 {
		t.Fatalf("notifications must not create exchanges or phantom tools: %+v", data.Exchanges)
	}
	agent := data.Exchanges[0].Messages[1].Tool
	monitor := data.Exchanges[0].Messages[2].Tool
	other := data.Exchanges[0].Messages[3].Tool
	for _, tt := range []struct {
		tool *ToolInfo
		want []string
	}{
		{agent, []string{"Background agent launched.", "status: completed", "PONG & <done>", `"total_tokens": "42"`, "status: cancelled", "Request was aborted.", "Error: stopped"}},
		{monitor, []string{"Monitor started.", "event-count: 1", "tick 1", "event-count: 2", "tick 2", "Exited with code 0"}},
	} {
		md := *tt.tool.FormattedMarkdown
		for _, want := range tt.want {
			index := strings.Index(md, want)
			if index < 0 {
				t.Errorf("%s missing %q in %s", tt.tool.Name, want, md)
			}
		}
		if strings.Contains(md, "orphan") || strings.Contains(md, "malformed") {
			t.Errorf("unmatched/malformed notification attached to %s", tt.tool.Name)
		}
	}
	if strings.Index(*agent.FormattedMarkdown, "status: completed") >= strings.Index(*agent.FormattedMarkdown, "status: cancelled") {
		t.Error("agent lifecycle events reordered")
	}
	if strings.Index(*monitor.FormattedMarkdown, "tick 1") >= strings.Index(*monitor.FormattedMarkdown, "tick 2") {
		t.Error("monitor events reordered")
	}
	if agent.Output["status"] != "success" || len(agent.Output["notifications"].([]any)) != 2 || len(monitor.Output["notifications"].([]any)) != 3 {
		t.Error("launch status or individual background events were lost")
	}
	if strings.Contains(*other.FormattedMarkdown, "Background events") {
		t.Error("unrelated tool received notifications")
	}
	// A quoted notification in a human prompt must never become a tool result.
	session.Records[2].Provenance = "real_user"
	if got := collectToolOutcomes(session.Records)["agent-call"].Notifications; len(got) != 0 {
		t.Errorf("human text treated as system notifications: %v", got)
	}
}

func TestGenerateAgentSession_BackgroundShellNotifications(t *testing.T) {
	for _, tt := range []struct {
		name, taskID, extra, status, tail string
		want                              []string
		attached                          bool
	}{
		{"completed", "shell-a", "<exit-code>0</exit-code>", "completed", "<output-tail truncated=\"false\">  hello &amp; &lt;world&gt;\n```\n</output-tail>", []string{"exit-code: 0", "truncated: false", "````text\n  hello & <world>\n```\n"}, true},
		{"failed", "shell-a", "<exit-code>7</exit-code><result>Command failed</result>", "failed", "<output-tail truncated=\"true\">last output</output-tail>", []string{"exit-code: 7", "Command failed", "truncated: true", "last output"}, true},
		{"cancelled unreadable tail", "shell-a", "", "cancelled", "<output-tail error=\"unreadable\" />", []string{"error: unreadable"}, true},
		{"unmatched task", "unknown-shell", "", "completed", "", nil, false},
		{"non-shell response cannot establish ownership", "shell-fake", "", "completed", "", nil, false},
		{"explicit unknown call is not reassigned", "shell-a", "<tool-use-id>unknown-call</tool-use-id>", "completed", "", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			launch := func(callID, taskID string) QwenRecord {
				return QwenRecord{Type: "tool_result", Message: &QwenMessage{Parts: []QwenPart{{FunctionResponse: &QwenFunctionResponse{
					ID: callID, Response: map[string]any{"output": "Background shell started.\nid: " + taskID + "\npid: 123\noutput file: /tmp/" + taskID + ".output"},
				}}}}, ToolCallResult: &QwenToolCallResult{Status: "success", ResultDisplay: json.RawMessage(`"Background shell started"`)}}
			}
			notification := "<task-notification><task-id>" + tt.taskID + "</task-id><kind>shell</kind><status>" + tt.status + "</status>" + tt.extra + tt.tail + "<output-file>/tmp/shell-a.output</output-file></task-notification>"
			session := &QwenSession{ID: "background-shell", Records: []QwenRecord{
				{Type: "user", Provenance: "real_user", Message: &QwenMessage{Parts: []QwenPart{{Text: "Run background checks"}}}},
				{Type: "assistant", Message: &QwenMessage{Parts: []QwenPart{
					{FunctionCall: &QwenFunctionCall{ID: "call-a", Name: "run_shell_command", Args: map[string]any{"command": "check a", "is_background": true}}},
					{FunctionCall: &QwenFunctionCall{ID: "call-b", Name: "run_shell_command", Args: map[string]any{"command": "check b", "is_background": true}}},
					{FunctionCall: &QwenFunctionCall{ID: "call-stop", Name: "task_stop", Args: map[string]any{"task_id": "shell-a"}}},
					{FunctionCall: &QwenFunctionCall{ID: "call-fake", Name: "read_file"}},
				}}},
				// Results can arrive in a different order from the parallel calls.
				launch("call-b", "shell-b"), launch("call-a", "shell-a"), launch("call-fake", "shell-fake"),
				{Type: "user", Provenance: "system", Subtype: "notification", Timestamp: "2026-09-16T01:30:00Z", Message: &QwenMessage{Parts: []QwenPart{{Text: notification}}}},
			}}
			data, err := GenerateAgentSession(session, "/project")
			if err != nil {
				t.Fatal(err)
			}
			if len(data.Exchanges) != 1 || len(data.Exchanges[0].Messages) != 5 {
				t.Fatalf("notification created extra messages: %+v", data.Exchanges)
			}
			shell := data.Exchanges[0].Messages[1].Tool
			md := *shell.FormattedMarkdown
			for _, want := range []string{"is_background: true", "id: shell-a", "pid: 123", "output file: /tmp/shell-a.output"} {
				if !strings.Contains(md, want) {
					t.Errorf("launch information missing %q: %s", want, md)
				}
			}
			if strings.Contains(md, "Background events:") != tt.attached {
				t.Fatalf("notification association mismatch: %s", md)
			}
			if tt.attached {
				for _, want := range append(tt.want, "status: "+tt.status, "2026-09-16T01:30:00Z") {
					if !strings.Contains(md, want) {
						t.Errorf("background result missing %q: %s", want, md)
					}
				}
				if shell.Output["status"] != "success" {
					t.Error("completion must not replace the immediate launch status")
				}
			}
			for _, message := range data.Exchanges[0].Messages[2:] {
				if strings.Contains(*message.Tool.FormattedMarkdown, "Background events:") {
					t.Errorf("notification attached to unrelated call %s", message.Tool.UseID)
				}
			}
		})
	}
}
