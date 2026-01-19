package droidcli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatToolCallExecute(t *testing.T) {
	input, _ := json.Marshal(map[string]any{"command": "ls", "workdir": "/repo"})
	tool := &fdToolCall{ID: "call_1", Name: "Execute", Input: input, Result: &fdToolResult{Content: "main.go"}}
	md := formatToolCall(tool)
	if !strings.Contains(md, "`ls`") {
		t.Fatalf("expected inline command: %s", md)
	}
	if !strings.Contains(md, "Output:") {
		t.Fatalf("missing result: %s", md)
	}
}

func TestFormatToolCallTodoWrite(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"todos": []map[string]any{{"description": "Task", "status": "completed"}},
	})
	tool := &fdToolCall{Name: "TodoWrite", Input: input}
	md := formatToolCall(tool)
	if !strings.Contains(md, "Todo List") {
		t.Fatalf("expected todo list output")
	}
}
