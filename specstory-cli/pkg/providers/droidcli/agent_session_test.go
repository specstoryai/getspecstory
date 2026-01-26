package droidcli

import (
	"encoding/json"
	"testing"
)

func TestGenerateAgentSession_BasicFlow(t *testing.T) {
	session := &fdSession{
		ID:        "session-1",
		CreatedAt: "2025-11-25T10:00:00Z",
		Slug:      "hello-world",
		Blocks: []fdBlock{
			{Kind: blockText, Role: "user", Timestamp: "2025-11-25T10:00:00Z", Text: "Say hi"},
			{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T10:00:05Z", Text: "Hello there"},
			{
				Kind:      blockTool,
				Role:      "agent",
				Timestamp: "2025-11-25T10:00:10Z",
				Model:     "gpt-5-test",
				Tool: &fdToolCall{
					ID:     "tool-1",
					Name:   "read",
					Input:  json.RawMessage(`{"path":"/repo/main.go"}`),
					Result: &fdToolResult{Content: "package main"},
				},
			},
			{
				Kind:      blockTodo,
				Role:      "agent",
				Timestamp: "2025-11-25T10:00:20Z",
				Todo: &fdTodoState{Items: []fdTodoItem{
					{Description: "Ship feature", Status: "completed"},
				}},
			},
		},
	}

	data, err := GenerateAgentSession(session, "/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.SessionID != "session-1" {
		t.Fatalf("expected session id to be preserved")
	}
	if data.WorkspaceRoot != "/repo" {
		t.Fatalf("workspace root should be set")
	}
	if len(data.Exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(data.Exchanges))
	}
	ex := data.Exchanges[0]
	if len(ex.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(ex.Messages))
	}
	if ex.Messages[0].Role != "user" || len(ex.Messages[0].Content) == 0 {
		t.Fatalf("first message should be populated user message")
	}
	toolMsg := ex.Messages[2]
	if toolMsg.Tool == nil {
		t.Fatalf("expected tool message")
	}
	if len(toolMsg.PathHints) != 1 || toolMsg.PathHints[0] != "main.go" {
		t.Fatalf("expected relative path hint, got %#v", toolMsg.PathHints)
	}
	if toolMsg.Tool.FormattedMarkdown == nil || *toolMsg.Tool.FormattedMarkdown == "" {
		t.Fatalf("expected formatted markdown for tool message")
	}
	if ex.Messages[3].Tool == nil || ex.Messages[3].Tool.Type != "task" {
		t.Fatalf("todo block should be represented as task tool")
	}
}

func TestGenerateAgentSession_IncludesReasoningAndSummary(t *testing.T) {
	session := &fdSession{
		ID:        "session-2",
		CreatedAt: "2025-11-25T11:00:00Z",
		Blocks: []fdBlock{
			{Kind: blockText, Role: "user", Timestamp: "2025-11-25T11:00:00Z", Text: "do work"},
			{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T11:00:05Z", Text: "thinking...", IsReasoning: true},
			{Kind: blockSummary, Role: "agent", Timestamp: "2025-11-25T11:00:06Z", Summary: &fdSummary{Title: "Plan", Body: "All done"}},
		},
	}

	data, err := GenerateAgentSession(session, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.Exchanges) != 1 {
		t.Fatalf("expected single exchange")
	}
	msgs := data.Exchanges[0].Messages
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[1].Content[0].Type != "thinking" {
		t.Fatalf("reasoning block should map to thinking content")
	}
	if msgs[2].Content[0].Type != "thinking" {
		t.Fatalf("summary should map to thinking content")
	}
}

func TestGenerateAgentSession_UsesSessionWorkspaceRoot(t *testing.T) {
	session := &fdSession{
		ID:            "session-root",
		CreatedAt:     "2025-11-25T13:00:00Z",
		WorkspaceRoot: "/workspace",
		Blocks: []fdBlock{
			{Kind: blockText, Role: "user", Timestamp: "2025-11-25T13:00:00Z", Text: "hello"},
			{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T13:00:01Z", Text: "hi"},
		},
	}

	data, err := GenerateAgentSession(session, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.WorkspaceRoot != "/workspace" {
		t.Fatalf("expected workspace root from session metadata")
	}
}

func TestGenerateAgentSession_ReturnsErrorWhenNoMessages(t *testing.T) {
	session := &fdSession{ID: "empty"}
	if _, err := GenerateAgentSession(session, ""); err == nil {
		t.Fatalf("expected error for empty session")
	}
}

func TestGenerateAgentSession_PropagatesMessageIDs(t *testing.T) {
	session := &fdSession{
		ID:        "session-3",
		CreatedAt: "2025-11-25T12:00:00Z",
		Blocks: []fdBlock{
			{Kind: blockText, Role: "user", MessageID: "msg-user", Timestamp: "2025-11-25T12:00:00Z", Text: "hello"},
			{Kind: blockText, Role: "agent", MessageID: "msg-agent", Timestamp: "2025-11-25T12:00:02Z", Text: "hi"},
			{
				Kind:      blockTool,
				Role:      "agent",
				MessageID: "msg-tool",
				Timestamp: "2025-11-25T12:00:05Z",
				Tool: &fdToolCall{
					ID:    "tool-1",
					Name:  "read",
					Input: json.RawMessage(`{"path":"/repo/main.go"}`),
				},
			},
		},
	}

	data, err := GenerateAgentSession(session, "/repo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.Exchanges) != 1 {
		t.Fatalf("expected 1 exchange")
	}
	msgs := data.Exchanges[0].Messages
	if msgs[0].ID != "msg-user" {
		t.Fatalf("expected user message id to propagate")
	}
	if msgs[1].ID != "msg-agent" {
		t.Fatalf("expected agent message id to propagate")
	}
	if msgs[2].ID != "msg-tool" {
		t.Fatalf("expected tool message id to propagate")
	}
	if msgs[2].Tool == nil || msgs[2].Tool.UseID != "tool-1" {
		t.Fatalf("expected tool use id to remain")
	}
}
