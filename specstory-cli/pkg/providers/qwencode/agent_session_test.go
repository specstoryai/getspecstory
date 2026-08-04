package qwencode

import (
	"path/filepath"
	"testing"
)

func TestGenerateAgentSession(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-1.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile returned error: %v", err)
	}

	data, err := GenerateAgentSession(session, "/tmp/qwen-fixture-project")
	if err != nil {
		t.Fatalf("GenerateAgentSession returned error: %v", err)
	}

	if data.Provider.ID != "qwen" {
		t.Errorf("provider ID = %q, want %q", data.Provider.ID, "qwen")
	}
	if data.Provider.Name != "Qwen Code" {
		t.Errorf("provider name = %q, want %q", data.Provider.Name, "Qwen Code")
	}
	if data.Provider.Version != "0.21.4" {
		t.Errorf("provider version = %q, want %q", data.Provider.Version, "0.21.4")
	}
	if data.SessionID != session.ID {
		t.Errorf("session ID = %q, want %q", data.SessionID, session.ID)
	}
	if data.CreatedAt != "2026-08-01T09:00:00.000Z" {
		t.Errorf("createdAt = %q, want %q", data.CreatedAt, "2026-08-01T09:00:00.000Z")
	}
	if data.WorkspaceRoot != "/tmp/qwen-fixture-project" {
		t.Errorf("workspaceRoot = %q, want %q", data.WorkspaceRoot, "/tmp/qwen-fixture-project")
	}

	// Two real user messages -> two exchanges
	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2", len(data.Exchanges))
	}

	first := data.Exchanges[0]
	if first.ExchangeID != session.ID+":0" {
		t.Errorf("exchange id = %q, want %q", first.ExchangeID, session.ID+":0")
	}

	// Exchange 1: user text, thinking, write_file tool, agent text
	var sawUser, sawThinking, sawTool, sawText bool
	for _, msg := range first.Messages {
		if msg.Role == "user" {
			sawUser = true
			if len(msg.Content) != 1 || msg.Content[0].Text != "Write a hello world script" {
				t.Errorf("user message content = %+v, want the prompt text", msg.Content)
			}
			continue
		}

		for _, part := range msg.Content {
			switch part.Type {
			case "thinking":
				sawThinking = true
			case "text":
				sawText = true
			}
		}
		if msg.Tool != nil {
			sawTool = true
			if msg.Tool.Name != "write_file" {
				t.Errorf("tool name = %q, want %q", msg.Tool.Name, "write_file")
			}
			if msg.Tool.Type != "write" {
				t.Errorf("tool type = %q, want %q", msg.Tool.Type, "write")
			}
			// The tool_result record must have been folded into the tool output
			if msg.Tool.Output == nil {
				t.Error("tool output is nil, want the functionResponse payload")
			} else if got, _ := msg.Tool.Output["output"].(string); got != "Successfully wrote to /tmp/qwen-fixture-project/hello.sh" {
				t.Errorf("tool output = %q, want the write_file result", got)
			}
		}
	}
	if !sawUser || !sawThinking || !sawTool || !sawText {
		t.Errorf("exchange 1 missing messages: user=%v thinking=%v tool=%v text=%v",
			sawUser, sawThinking, sawTool, sawText)
	}

	// Exchange 2: user text, run_shell_command tool with error output
	second := data.Exchanges[1]
	var tool *ToolInfo
	for i := range second.Messages {
		if second.Messages[i].Tool != nil {
			tool = second.Messages[i].Tool
		}
	}
	if tool == nil {
		t.Fatal("exchange 2 has no tool message")
	}
	if tool.Type != "shell" {
		t.Errorf("tool type = %q, want %q", tool.Type, "shell")
	}
	if tool.Output == nil {
		t.Fatal("tool output is nil, want the error payload")
	}
	if got, _ := tool.Output["error"].(string); got != "bash: hello.sh: No such file or directory" {
		t.Errorf("tool error output = %q, want the shell error", got)
	}
}

func TestGenerateAgentSessionUsageAttachedOnce(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-1.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile returned error: %v", err)
	}

	data, err := GenerateAgentSession(session, "/tmp/qwen-fixture-project")
	if err != nil {
		t.Fatalf("GenerateAgentSession returned error: %v", err)
	}

	// Each assistant record that carries usageMetadata attaches it to exactly
	// one message (its last). The fixture has two such records, both in
	// exchange 1; the second assistant record (tool-only) has none.
	var inputs []int
	for _, exchange := range data.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Usage != nil {
				if msg.Role != "agent" {
					t.Errorf("usage attached to non-agent message (role %q)", msg.Role)
				}
				inputs = append(inputs, msg.Usage.InputTokens)
			}
		}
	}
	if len(inputs) != 2 {
		t.Fatalf("messages with usage = %d, want 2", len(inputs))
	}

	want := map[int]bool{1200: false, 1300: false}
	for _, input := range inputs {
		if _, ok := want[input]; !ok {
			t.Errorf("unexpected usage input tokens %d", input)
			continue
		}
		want[input] = true
	}
	for input, seen := range want {
		if !seen {
			t.Errorf("usage with input tokens %d not found", input)
		}
	}

	// Verify the full mapping of the second assistant record's usageMetadata
	last := data.Exchanges[0].Messages[len(data.Exchanges[0].Messages)-1]
	if last.Usage == nil {
		t.Fatal("last message of exchange 1 has no usage")
	}
	if last.Usage.InputTokens != 1300 || last.Usage.OutputTokens != 12 ||
		last.Usage.ThoughtTokens != 0 || last.Usage.CachedTokens != 500 {
		t.Errorf("usage = %+v, want input=1300 output=12 thoughts=0 cached=500", last.Usage)
	}
}

func TestGenerateAgentSessionRejectsSystemOnly(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-system-only.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile returned error: %v", err)
	}

	if _, err := GenerateAgentSession(session, "/tmp/qwen-fixture-project"); err == nil {
		t.Fatal("GenerateAgentSession should fail for a system-only transcript")
	}
}

func TestGenerateAgentSessionEmpty(t *testing.T) {
	session := &QwenSession{ID: "empty"}
	if _, err := GenerateAgentSession(session, "/tmp"); err == nil {
		t.Fatal("GenerateAgentSession should fail for an empty session")
	}
}

func TestClassifyQwenToolType(t *testing.T) {
	cases := map[string]string{
		"read_file":         "read",
		"web_fetch":         "read",
		"write_file":        "write",
		"edit":              "write",
		"run_shell_command": "shell",
		"glob":              "search",
		"grep_search":       "search",
		"web_search":        "search",
		"todo_write":        "task",
		"task":              "task",
		"skill":             "generic",
		"some_new_tool":     "unknown",
	}
	for tool, want := range cases {
		if got := classifyQwenToolType(tool); got != want {
			t.Errorf("classifyQwenToolType(%q) = %q, want %q", tool, got, want)
		}
	}
}
