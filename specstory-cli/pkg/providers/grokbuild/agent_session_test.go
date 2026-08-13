package grokbuild

import (
	"strings"
	"testing"
)

func TestGenerateAgentSession_Basic(t *testing.T) {
	session := loadFixture(t, "session-basic")

	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	if !data.Validate() {
		t.Error("SessionData failed schema validation")
	}
	if data.Provider.ID != "grok-build" || data.Provider.Name != "Grok Build" {
		t.Errorf("provider = %+v", data.Provider)
	}
	if data.Provider.Version != "grok-4.6" {
		t.Errorf("provider version = %q, want the model id", data.Provider.Version)
	}

	// Two <user_query> records, so two exchanges. The injected context records
	// must not open exchanges of their own.
	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2", len(data.Exchanges))
	}

	first := data.Exchanges[0]
	if first.Messages[0].Role != "user" {
		t.Errorf("first message role = %q", first.Messages[0].Role)
	}
	if got := first.Messages[0].Content[0].Text; got != "read the README and tell me what it says" {
		t.Errorf("user text = %q", got)
	}
	// Timestamps come from updates.jsonl because chat_history has none.
	if first.Messages[0].Timestamp == "" {
		t.Error("user message should carry a timestamp from updates.jsonl")
	}

	var sawThinking, sawTool bool
	for _, msg := range first.Messages {
		for _, part := range msg.Content {
			if part.Type == "thinking" {
				sawThinking = true
				if strings.Contains(part.Text, "OPAQUE") {
					t.Error("encrypted reasoning leaked into content")
				}
			}
		}
		if msg.Tool != nil {
			sawTool = true
			if msg.Tool.Name != "read_file" || msg.Tool.Type != "read" {
				t.Errorf("tool = %+v", msg.Tool)
			}
			if out, _ := msg.Tool.Output["output"].(string); !strings.Contains(out, "# Project") {
				t.Errorf("tool result not folded in: %v", msg.Tool.Output)
			}
		}
	}
	if !sawThinking {
		t.Error("expected a thinking message")
	}
	if !sawTool {
		t.Error("expected a tool message")
	}

	// Usage lands on the last message of the exchange so totals do not double count.
	last := first.Messages[len(first.Messages)-1]
	if last.Usage == nil {
		t.Fatal("usage missing from the last message of the first exchange")
	}
	if last.Usage.InputTokens != 1200 || last.Usage.OutputTokens != 80 || last.Usage.CachedTokens != 900 {
		t.Errorf("usage = %+v", last.Usage)
	}
}

func TestGenerateAgentSession_ToolsAndErrors(t *testing.T) {
	session := loadFixture(t, "session-tools")

	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if !data.Validate() {
		t.Error("SessionData failed schema validation")
	}

	tools := map[string]*ToolInfo{}
	for _, exchange := range data.Exchanges {
		for i := range exchange.Messages {
			if tool := exchange.Messages[i].Tool; tool != nil {
				tools[tool.Name] = tool
			}
		}
	}

	// Five tool_calls plus two backend_tool_call records.
	if len(tools) != 7 {
		t.Fatalf("distinct tools = %d, want 7: %v", len(tools), keysOf(tools))
	}

	if got := tools["search_replace"].Type; got != "write" {
		t.Errorf("search_replace type = %q, want write", got)
	}
	if got := tools["run_terminal_command"].Type; got != "shell" {
		t.Errorf("run_terminal_command type = %q, want shell", got)
	}
	if got := tools["todo_write"].Type; got != "task" {
		t.Errorf("todo_write type = %q, want task", got)
	}

	// The failure is only recorded in events.jsonl, never in the result text.
	readFile := tools["read_file"]
	if status, _ := readFile.Output["status"].(string); status != "error" {
		t.Errorf("failed read_file status = %q, want error", status)
	}
	if status, _ := tools["run_terminal_command"].Output["status"].(string); status != "success" {
		t.Errorf("successful call status = %q, want success", status)
	}

	// Web and X tools arrive as backend_tool_call records, never in tool_calls.
	if web := tools["web_search"]; web == nil {
		t.Error("backend web_search was dropped")
	} else if web.Input["query"] != "grok 4.6 release notes" {
		t.Errorf("web_search input = %v", web.Input)
	}
	if x := tools["x_user_search"]; x == nil {
		t.Error("backend x_user_search was dropped")
	} else if x.Input["query"] != "xAI" {
		t.Errorf("x_user_search input = %v", x.Input)
	}

	// Path hints come from Grok's own argument names.
	for _, exchange := range data.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool != nil && msg.Tool.Name == "search_replace" && len(msg.PathHints) == 0 {
				t.Error("search_replace should produce a path hint from file_path")
			}
		}
	}
}

func TestGenerateAgentSession_NoUserQueryYieldsNoExchanges(t *testing.T) {
	session := loadFixture(t, "session-noquery")

	data, err := GenerateAgentSession(session, "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if len(data.Exchanges) != 0 {
		t.Errorf("exchange count = %d, want 0 for a session with no real prompt", len(data.Exchanges))
	}
}

func TestGenerateAgentSession_EmptySession(t *testing.T) {
	if _, err := GenerateAgentSession(&GrokSession{ID: "x", Index: newSessionIndex()}, "/tmp"); err == nil {
		t.Error("expected an error for a session with no records")
	}
}

func TestClassifyGrokTool(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		grokKind string
		want     string
	}{
		{name: "read by name", tool: "read_file", want: "read"},
		{name: "write by name", tool: "write", want: "write"},
		{name: "edit by name", tool: "search_replace", want: "write"},
		{name: "shell by name", tool: "run_terminal_command", want: "shell"},
		{name: "list is shell", tool: "list_dir", want: "shell"},
		{name: "grep is search", tool: "grep", want: "search"},
		{name: "todo is task", tool: "todo_write", want: "task"},
		{name: "subagent is task", tool: "spawn_subagent", want: "task"},
		{name: "mcp envelope is generic", tool: "use_tool", want: "generic"},
		{name: "web search is search", tool: "web_search", want: "search"},
		{name: "x thread fetch is search", tool: "x_thread_fetch", want: "search"},
		// An unknown tool falls back to Grok's own taxonomy, which is what keeps
		// MCP and future tools out of the unknown bucket.
		{name: "unknown name with kind", tool: "some_future_tool", grokKind: "execute", want: "shell"},
		{name: "unknown name with edit kind", tool: "another_tool", grokKind: "edit", want: "write"},
		{name: "unknown everything", tool: "mystery", want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyGrokTool(tt.tool, tt.grokKind); got != tt.want {
				t.Errorf("classifyGrokTool(%q, %q) = %q, want %q", tt.tool, tt.grokKind, got, tt.want)
			}
		})
	}
}

func keysOf(m map[string]*ToolInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
