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

func TestFormatToolCallTodoWriteString(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"todos": "1. [completed] Ship feature",
	})
	tool := &fdToolCall{Name: "TodoWrite", Input: input}
	md := formatToolCall(tool)
	if !strings.Contains(md, "Todo List") {
		t.Fatalf("expected todo list heading")
	}
	if !strings.Contains(md, "Ship feature") {
		t.Fatalf("expected todo content")
	}
}

func TestFormatToolCallWebSearch(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"query":      "Delhi weather",
		"category":   "news",
		"numResults": 5,
	})
	tool := &fdToolCall{Name: "WebSearch", Input: input}
	md := formatToolCall(tool)
	if !strings.Contains(md, "Query: `Delhi weather`") {
		t.Fatalf("expected query summary")
	}
	if !strings.Contains(md, "Category: `news`") {
		t.Fatalf("expected category summary")
	}
	if !strings.Contains(md, "Results: `5`") {
		t.Fatalf("expected numResults summary")
	}
}

func TestFormatToolCallGrep(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"pattern":      "TODO",
		"path":         "/repo",
		"glob_pattern": "*.go",
	})
	tool := &fdToolCall{Name: "Grep", Input: input}
	md := formatToolCall(tool)
	if !strings.Contains(md, "Pattern: `TODO`") {
		t.Fatalf("expected grep pattern")
	}
	if !strings.Contains(md, "Path: `/repo`") {
		t.Fatalf("expected grep path")
	}
	if !strings.Contains(md, "Glob: `*.go`") {
		t.Fatalf("expected grep glob")
	}
}

func TestFormatToolCallWrite(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"file_path": "main.go",
		"content":   "package main",
	})
	tool := &fdToolCall{Name: "Write", Input: input}
	md := formatToolCall(tool)
	if !strings.Contains(md, "Path: `main.go`") {
		t.Fatalf("expected path in write output")
	}
	if !strings.Contains(md, "```go") {
		t.Fatalf("expected language code fence")
	}
	if !strings.Contains(md, "package main") {
		t.Fatalf("expected content in write output")
	}
}

func TestToolTypeMappings(t *testing.T) {
	if got := toolType("WebSearch"); got != "search" {
		t.Fatalf("expected search type for WebSearch, got %s", got)
	}
	if got := toolType("MultiEdit"); got != "write" {
		t.Fatalf("expected write type for MultiEdit, got %s", got)
	}
}
