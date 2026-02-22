package provenance

import (
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// completedOutput is a minimal tool result indicating a completed tool use.
// Only messages with Output set produce agent events (tool_result, not tool_use).
var completedOutput = map[string]interface{}{"content": "ok", "is_error": false}

func TestExtractAgentEvents(t *testing.T) {
	baseTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	baseTimeStr := baseTime.Format(time.RFC3339)

	tests := []struct {
		name          string
		sessionData   *schema.SessionData
		wantCount     int
		wantPaths     []string
		wantAgentType string
	}{
		{
			name:        "nil session data",
			sessionData: nil,
			wantCount:   0,
		},
		{
			name: "empty exchanges",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{},
			},
			wantCount: 0,
		},
		{
			name: "write tool with path hints produces events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Model:     "claude-sonnet-4-20250514",
								Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, Output: completedOutput},
								PathHints: []string{"src/main.go"},
							},
						},
					},
				},
			},
			wantCount:     1,
			wantPaths:     []string{"src/main.go"},
			wantAgentType: "claude-code",
		},
		{
			name: "read tool produces no events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "Read", Type: schema.ToolTypeRead},
								PathHints: []string{"src/main.go"},
							},
						},
					},
				},
			},
			wantCount: 0,
		},
		{
			name: "search tool produces no events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "Grep", Type: schema.ToolTypeSearch},
								PathHints: []string{"src/main.go"},
							},
						},
					},
				},
			},
			wantCount: 0,
		},
		{
			name: "shell tool with path hints produces events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "Bash", Type: schema.ToolTypeShell, Output: completedOutput},
								PathHints: []string{"build/output.bin"},
							},
						},
					},
				},
			},
			wantCount: 1,
			wantPaths: []string{"build/output.bin"},
		},
		{
			name: "generic tool with path hints produces events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "cursor"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "custom_tool", Type: schema.ToolTypeGeneric, Output: completedOutput},
								PathHints: []string{"foo.txt"},
							},
						},
					},
				},
			},
			wantCount:     1,
			wantPaths:     []string{"foo.txt"},
			wantAgentType: "cursor",
		},
		{
			name: "write tool without path hints produces no events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, Output: completedOutput},
								PathHints: []string{},
							},
						},
					},
				},
			},
			wantCount: 0,
		},
		{
			name: "tool_use without result produces no events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite},
								PathHints: []string{"src/main.go"},
							},
						},
					},
				},
			},
			wantCount: 0,
		},
		{
			name: "multiple path hints produce multiple events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, Output: completedOutput},
								PathHints: []string{"a.go", "b.go", "c.go"},
							},
						},
					},
				},
			},
			wantCount: 3,
			wantPaths: []string{"a.go", "b.go", "c.go"},
		},
		{
			name: "message without tool produces no events",
			sessionData: &schema.SessionData{
				SessionID: "sess-1",
				Provider:  schema.ProviderInfo{ID: "claude-code"},
				Exchanges: []schema.Exchange{
					{
						ExchangeID: "ex-1",
						Messages: []schema.Message{
							{
								ID:        "msg-1",
								Timestamp: baseTimeStr,
								Role:      schema.RoleAgent,
								PathHints: []string{"src/main.go"},
							},
						},
					},
				},
			},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := ExtractAgentEvents(tt.sessionData)
			if len(events) != tt.wantCount {
				t.Errorf("got %d events, want %d", len(events), tt.wantCount)
				return
			}
			for i, wantPath := range tt.wantPaths {
				if events[i].FilePath != wantPath {
					t.Errorf("event[%d].FilePath = %q, want %q", i, events[i].FilePath, wantPath)
				}
			}
			if tt.wantAgentType != "" && len(events) > 0 {
				if events[0].AgentType != tt.wantAgentType {
					t.Errorf("event[0].AgentType = %q, want %q", events[0].AgentType, tt.wantAgentType)
				}
			}
		})
	}
}

func TestExtractAgentEvents_DeterministicIDs(t *testing.T) {
	sessionData := &schema.SessionData{
		SessionID: "sess-1",
		Provider:  schema.ProviderInfo{ID: "claude-code"},
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "ex-1",
				Messages: []schema.Message{
					{
						ID:        "msg-1",
						Timestamp: time.Now().Format(time.RFC3339),
						Role:      schema.RoleAgent,
						Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, Output: completedOutput},
						PathHints: []string{"src/main.go"},
					},
				},
			},
		},
	}

	events1 := ExtractAgentEvents(sessionData)
	events2 := ExtractAgentEvents(sessionData)

	if len(events1) != 1 || len(events2) != 1 {
		t.Fatalf("expected 1 event each, got %d and %d", len(events1), len(events2))
	}

	if events1[0].ID != events2[0].ID {
		t.Errorf("IDs not deterministic: %q != %q", events1[0].ID, events2[0].ID)
	}
}

func TestExtractAgentEvents_TimestampFallback(t *testing.T) {
	exchangeTime := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	sessionData := &schema.SessionData{
		SessionID: "sess-1",
		Provider:  schema.ProviderInfo{ID: "claude-code"},
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "ex-1",
				StartTime:  exchangeTime.Format(time.RFC3339),
				Messages: []schema.Message{
					{
						ID:   "msg-1",
						Role: schema.RoleAgent,
						// No Timestamp on message — should fall back to exchange StartTime
						Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, Output: completedOutput},
						PathHints: []string{"src/main.go"},
					},
				},
			},
		},
	}

	events := ExtractAgentEvents(sessionData)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if !events[0].Timestamp.Equal(exchangeTime) {
		t.Errorf("timestamp = %v, want %v (exchange fallback)", events[0].Timestamp, exchangeTime)
	}
}

func TestExtractAgentEvents_FieldMapping(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	sessionData := &schema.SessionData{
		SessionID: "sess-42",
		Provider:  schema.ProviderInfo{ID: "claude-code", Name: "Claude Code"},
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "ex-7",
				Messages: []schema.Message{
					{
						ID:        "msg-99",
						Timestamp: ts.Format(time.RFC3339),
						Role:      schema.RoleAgent,
						Model:     "claude-sonnet-4-20250514",
						Tool:      &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, Output: completedOutput},
						PathHints: []string{"pkg/foo.go"},
					},
				},
			},
		},
	}

	events := ExtractAgentEvents(sessionData)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.SessionID != "sess-42" {
		t.Errorf("SessionID = %q, want %q", e.SessionID, "sess-42")
	}
	if e.ExchangeID != "ex-7" {
		t.Errorf("ExchangeID = %q, want %q", e.ExchangeID, "ex-7")
	}
	if e.MessageID != "msg-99" {
		t.Errorf("MessageID = %q, want %q", e.MessageID, "msg-99")
	}
	if e.AgentType != "claude-code" {
		t.Errorf("AgentType = %q, want %q", e.AgentType, "claude-code")
	}
	if e.AgentModel != "claude-sonnet-4-20250514" {
		t.Errorf("AgentModel = %q, want %q", e.AgentModel, "claude-sonnet-4-20250514")
	}
	if e.FilePath != "pkg/foo.go" {
		t.Errorf("FilePath = %q, want %q", e.FilePath, "pkg/foo.go")
	}
	if e.ChangeType != schema.ToolTypeWrite {
		t.Errorf("ChangeType = %q, want %q", e.ChangeType, schema.ToolTypeWrite)
	}
	if !e.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, ts)
	}
}
