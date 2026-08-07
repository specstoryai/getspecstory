package qwencode

import (
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
		{"list_directory", "shell"},
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
