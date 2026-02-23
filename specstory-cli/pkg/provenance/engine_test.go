package provenance

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// testEngine creates a temporary Engine for testing.
func testEngine(t *testing.T, opts ...Option) *Engine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	opts = append([]Option{WithDBPath(path)}, opts...)
	e, err := NewEngine(opts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func TestPathSuffixMatch(t *testing.T) {
	// Cases from the plan's matching algorithm table
	tests := []struct {
		name      string
		fsPath    string
		agentPath string
		want      bool
	}{
		{
			name:      "relative file matches as suffix",
			fsPath:    "/project/src/foo.go",
			agentPath: "/foo.go",
			want:      true,
		},
		{
			name:      "relative with directory matches",
			fsPath:    "/project/src/foo.go",
			agentPath: "/src/foo.go",
			want:      true,
		},
		{
			name:      "exact absolute match",
			fsPath:    "/project/src/foo.go",
			agentPath: "/project/src/foo.go",
			want:      true,
		},
		{
			name:      "different filename no match",
			fsPath:    "/project/src/foo.go",
			agentPath: "/bar.go",
			want:      false,
		},
		{
			name:      "partial filename no match",
			fsPath:    "/project/src/foobar.go",
			agentPath: "/bar.go",
			want:      false,
		},
		{
			name:      "agent path suffix of longer filename rejected",
			fsPath:    "/project/src/afoo.go",
			agentPath: "/foo.go",
			want:      false,
		},
		{
			name:      "agent directory suffix of longer dirname rejected",
			fsPath:    "/project/xsrc/foo.go",
			agentPath: "/src/foo.go",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathSuffixMatch(tt.fsPath, tt.agentPath)
			if got != tt.want {
				t.Errorf("pathSuffixMatch(%q, %q) = %v, want %v",
					tt.fsPath, tt.agentPath, got, tt.want)
			}
		})
	}
}

func TestPushFileEvent_MatchesExistingAgent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	e := testEngine(t)

	// Push agent event first
	_, err := e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-1",
		FilePath:   "src/main.go",
		ChangeType: "edit",
		Timestamp:  now,
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
		AgentModel: "claude-sonnet-4-20250514",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}

	// Push file event — should match the agent event
	record, err := e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-1",
		Path:       "/project/src/main.go",
		ChangeType: "modify",
		Timestamp:  now.Add(1 * time.Second),
	})
	if err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}
	if record == nil {
		t.Fatal("expected a ProvenanceRecord, got nil")
	}

	if record.FilePath != "/project/src/main.go" {
		t.Errorf("FilePath = %q, want %q", record.FilePath, "/project/src/main.go")
	}
	if record.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", record.SessionID, "sess-1")
	}
	if record.ExchangeID != "exch-1" {
		t.Errorf("ExchangeID = %q, want %q", record.ExchangeID, "exch-1")
	}
	if record.AgentType != "claude-code" {
		t.Errorf("AgentType = %q, want %q", record.AgentType, "claude-code")
	}

}

func TestPushAgentEvent_MatchesExistingFile(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	e := testEngine(t)

	// Push file event first
	_, err := e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-1",
		Path:       "/project/src/main.go",
		ChangeType: "modify",
		Timestamp:  now,
	})
	if err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}

	// Push agent event — should match the file event
	record, err := e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-1",
		FilePath:   "src/main.go",
		ChangeType: "edit",
		Timestamp:  now.Add(2 * time.Second),
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}
	if record == nil {
		t.Fatal("expected a ProvenanceRecord, got nil")
	}

	if record.FilePath != "/project/src/main.go" {
		t.Errorf("FilePath = %q, want %q", record.FilePath, "/project/src/main.go")
	}
	if record.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", record.SessionID, "sess-1")
	}
}

func TestPushFileEvent_NoMatch(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	// Push file event with no agent events at all
	record, err := e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-1",
		Path:       "/project/foo.go",
		ChangeType: "create",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}
	if record != nil {
		t.Errorf("expected nil record (no match), got %+v", record)
	}
}

func TestPushAgentEvent_NoMatch(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)

	// Push agent event with no file events at all
	record, err := e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-1",
		FilePath:   "foo.go",
		ChangeType: "edit",
		Timestamp:  time.Now(),
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}
	if record != nil {
		t.Errorf("expected nil record (no match), got %+v", record)
	}
}

func TestPushFileEvent_TimeWindowBoundary(t *testing.T) {
	// The match window boundary is exclusive: delta > window means no match,
	// so exactly 5s matches but 5s+1ns does not.
	tests := []struct {
		name      string
		delta     time.Duration
		wantMatch bool
	}{
		{name: "exactly at window boundary matches", delta: 5 * time.Second, wantMatch: true},
		{name: "1ns beyond window does not match", delta: 5*time.Second + 1*time.Nanosecond, wantMatch: false},
		{name: "well outside window does not match", delta: 10 * time.Second, wantMatch: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
			e := testEngine(t)

			_, err := e.PushAgentEvent(ctx, AgentEvent{
				ID:         "ae-1",
				FilePath:   "foo.go",
				ChangeType: "edit",
				Timestamp:  now,
				SessionID:  "sess-1",
				ExchangeID: "exch-1",
				AgentType:  "claude-code",
			})
			if err != nil {
				t.Fatalf("PushAgentEvent: %v", err)
			}

			record, err := e.PushFileEvent(ctx, FileEvent{
				ID:         "fe-1",
				Path:       "/project/foo.go",
				ChangeType: "modify",
				Timestamp:  now.Add(tt.delta),
			})
			if err != nil {
				t.Fatalf("PushFileEvent: %v", err)
			}

			gotMatch := record != nil
			if gotMatch != tt.wantMatch {
				t.Errorf("delta=%v: got match=%v, want match=%v", tt.delta, gotMatch, tt.wantMatch)
			}
		})
	}
}

func TestPushFileEvent_ClosestTimestampWins(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	e := testEngine(t)

	// Two agent events for the same path at different times
	_, err := e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-far",
		FilePath:   "src/foo.go",
		ChangeType: "edit",
		Timestamp:  now.Add(-4 * time.Second),
		SessionID:  "sess-far",
		ExchangeID: "exch-far",
		AgentType:  "claude-code",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent (far): %v", err)
	}

	_, err = e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-close",
		FilePath:   "src/foo.go",
		ChangeType: "edit",
		Timestamp:  now.Add(1 * time.Second),
		SessionID:  "sess-close",
		ExchangeID: "exch-close",
		AgentType:  "claude-code",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent (close): %v", err)
	}

	// File event at T=0 — closer to ae-close (1s) than ae-far (4s)
	record, err := e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-1",
		Path:       "/project/src/foo.go",
		ChangeType: "modify",
		Timestamp:  now,
	})
	if err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}
	if record == nil {
		t.Fatal("expected a record, got nil")
	}
	if record.SessionID != "sess-close" {
		t.Errorf("SessionID = %q, want %q (closest match)", record.SessionID, "sess-close")
	}
}

func TestPushFileEvent_MatchedAgentNotReused(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	e := testEngine(t)

	// One agent event
	_, err := e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-1",
		FilePath:   "foo.go",
		ChangeType: "edit",
		Timestamp:  now,
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}

	// First file event matches
	record, err := e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-1",
		Path:       "/project/foo.go",
		ChangeType: "modify",
		Timestamp:  now,
	})
	if err != nil {
		t.Fatalf("PushFileEvent 1: %v", err)
	}
	if record == nil {
		t.Fatal("expected first file event to match")
	}

	// Second file event for same path — agent already consumed
	record, err = e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-2",
		Path:       "/project/foo.go",
		ChangeType: "modify",
		Timestamp:  now.Add(1 * time.Second),
	})
	if err != nil {
		t.Fatalf("PushFileEvent 2: %v", err)
	}
	if record != nil {
		t.Errorf("expected nil (agent already matched), got %+v", record)
	}
}

func TestCustomMatchWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	// 2s window instead of default 5s
	e := testEngine(t, WithMatchWindow(2*time.Second))

	_, err := e.PushAgentEvent(ctx, AgentEvent{
		ID:         "ae-1",
		FilePath:   "foo.go",
		ChangeType: "edit",
		Timestamp:  now,
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
	})
	if err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}

	// 3s after agent event — inside default 5s but outside custom 2s
	record, err := e.PushFileEvent(ctx, FileEvent{
		ID:         "fe-1",
		Path:       "/project/foo.go",
		ChangeType: "modify",
		Timestamp:  now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}
	if record != nil {
		t.Errorf("expected nil (outside 2s window), got %+v", record)
	}
}
