package droidcli

import (
	"encoding/json"
	"testing"
)

// TestGenerateAgentSession_BasicFlow is an integration test that verifies
// the complete session conversion pipeline with all block types.
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
		t.Errorf("expected session id to be preserved")
	}
	if data.WorkspaceRoot != "/repo" {
		t.Errorf("workspace root should be set")
	}
	if len(data.Exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(data.Exchanges))
	}
	ex := data.Exchanges[0]
	if len(ex.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(ex.Messages))
	}
	if ex.Messages[0].Role != "user" || len(ex.Messages[0].Content) == 0 {
		t.Errorf("first message should be populated user message")
	}
	toolMsg := ex.Messages[2]
	if toolMsg.Tool == nil {
		t.Fatalf("expected tool message")
	}
	if len(toolMsg.PathHints) != 1 || toolMsg.PathHints[0] != "main.go" {
		t.Errorf("expected relative path hint, got %#v", toolMsg.PathHints)
	}
	if toolMsg.Tool.FormattedMarkdown == nil || *toolMsg.Tool.FormattedMarkdown == "" {
		t.Errorf("expected formatted markdown for tool message")
	}
	if ex.Messages[3].Tool == nil || ex.Messages[3].Tool.Type != "task" {
		t.Errorf("todo block should be represented as task tool")
	}
}

func TestGenerateAgentSession(t *testing.T) {
	tests := []struct {
		name          string
		session       *fdSession
		workspaceRoot string
		expectError   bool
		validate      func(t *testing.T, data *SessionData)
	}{
		{
			name: "maps reasoning blocks to thinking content type",
			session: &fdSession{
				ID:        "reasoning-test",
				CreatedAt: "2025-11-25T11:00:00Z",
				Blocks: []fdBlock{
					{Kind: blockText, Role: "user", Timestamp: "2025-11-25T11:00:00Z", Text: "do work"},
					{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T11:00:05Z", Text: "thinking...", IsReasoning: true},
				},
			},
			validate: func(t *testing.T, data *SessionData) {
				if len(data.Exchanges) != 1 || len(data.Exchanges[0].Messages) != 2 {
					t.Fatalf("expected 1 exchange with 2 messages")
				}
				msg := data.Exchanges[0].Messages[1]
				if len(msg.Content) == 0 || msg.Content[0].Type != "thinking" {
					t.Errorf("reasoning block should map to thinking content, got %v", msg.Content)
				}
			},
		},
		{
			name: "maps summary blocks to thinking content type",
			session: &fdSession{
				ID:        "summary-test",
				CreatedAt: "2025-11-25T11:00:00Z",
				Blocks: []fdBlock{
					{Kind: blockText, Role: "user", Timestamp: "2025-11-25T11:00:00Z", Text: "do work"},
					{Kind: blockSummary, Role: "agent", Timestamp: "2025-11-25T11:00:06Z", Summary: &fdSummary{Title: "Plan", Body: "All done"}},
				},
			},
			validate: func(t *testing.T, data *SessionData) {
				if len(data.Exchanges) != 1 || len(data.Exchanges[0].Messages) != 2 {
					t.Fatalf("expected 1 exchange with 2 messages")
				}
				msg := data.Exchanges[0].Messages[1]
				if len(msg.Content) == 0 || msg.Content[0].Type != "thinking" {
					t.Errorf("summary should map to thinking content, got %v", msg.Content)
				}
			},
		},
		{
			name: "uses session workspace root over parameter",
			session: &fdSession{
				ID:            "workspace-test",
				CreatedAt:     "2025-11-25T13:00:00Z",
				WorkspaceRoot: "/from-session",
				Blocks: []fdBlock{
					{Kind: blockText, Role: "user", Timestamp: "2025-11-25T13:00:00Z", Text: "hello"},
					{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T13:00:01Z", Text: "hi"},
				},
			},
			workspaceRoot: "/from-parameter",
			validate: func(t *testing.T, data *SessionData) {
				if data.WorkspaceRoot != "/from-session" {
					t.Errorf("expected workspace root from session metadata, got %q", data.WorkspaceRoot)
				}
			},
		},
		{
			name: "uses parameter workspace root when session has none",
			session: &fdSession{
				ID:        "workspace-param-test",
				CreatedAt: "2025-11-25T13:00:00Z",
				Blocks: []fdBlock{
					{Kind: blockText, Role: "user", Timestamp: "2025-11-25T13:00:00Z", Text: "hello"},
					{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T13:00:01Z", Text: "hi"},
				},
			},
			workspaceRoot: "/from-parameter",
			validate: func(t *testing.T, data *SessionData) {
				if data.WorkspaceRoot != "/from-parameter" {
					t.Errorf("expected workspace root from parameter, got %q", data.WorkspaceRoot)
				}
			},
		},
		{
			name:        "returns error for empty session",
			session:     &fdSession{ID: "empty"},
			expectError: true,
		},
		{
			name:        "returns error for nil session",
			session:     nil,
			expectError: true,
		},
		{
			name: "propagates message IDs to output",
			session: &fdSession{
				ID:        "id-propagation-test",
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
			},
			workspaceRoot: "/repo",
			validate: func(t *testing.T, data *SessionData) {
				if len(data.Exchanges) != 1 {
					t.Fatalf("expected 1 exchange")
				}
				msgs := data.Exchanges[0].Messages
				if len(msgs) != 3 {
					t.Fatalf("expected 3 messages, got %d", len(msgs))
				}
				if msgs[0].ID != "msg-user" {
					t.Errorf("expected user message id 'msg-user', got %q", msgs[0].ID)
				}
				if msgs[1].ID != "msg-agent" {
					t.Errorf("expected agent message id 'msg-agent', got %q", msgs[1].ID)
				}
				if msgs[2].ID != "msg-tool" {
					t.Errorf("expected tool message id 'msg-tool', got %q", msgs[2].ID)
				}
				if msgs[2].Tool == nil || msgs[2].Tool.UseID != "tool-1" {
					t.Errorf("expected tool use id 'tool-1'")
				}
			},
		},
		{
			name: "groups consecutive user messages in same exchange",
			session: &fdSession{
				ID:        "multi-user-test",
				CreatedAt: "2025-11-25T14:00:00Z",
				Blocks: []fdBlock{
					{Kind: blockText, Role: "user", Timestamp: "2025-11-25T14:00:00Z", Text: "first"},
					{Kind: blockText, Role: "user", Timestamp: "2025-11-25T14:00:01Z", Text: "second"},
					{Kind: blockText, Role: "agent", Timestamp: "2025-11-25T14:00:02Z", Text: "response"},
				},
			},
			validate: func(t *testing.T, data *SessionData) {
				if len(data.Exchanges) != 1 {
					t.Errorf("expected 1 exchange for consecutive user messages, got %d", len(data.Exchanges))
				}
				if len(data.Exchanges[0].Messages) != 3 {
					t.Errorf("expected 3 messages in exchange")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := GenerateAgentSession(tt.session, tt.workspaceRoot)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, data)
			}
		})
	}
}
