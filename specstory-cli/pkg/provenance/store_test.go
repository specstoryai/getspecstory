package provenance

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Wide time range that captures any event — store tests verify storage,
// not time-window filtering (which is the engine's responsibility).
var (
	distantPast   = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	distantFuture = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// testStore creates a temporary SQLite store for testing.
func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenStore(t *testing.T) {
	s := testStore(t)

	// Verify we can query the events table (schema was created)
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("querying events table: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows, got %d", count)
	}
}

func TestPushFileEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Nanosecond)

	event := FileEvent{
		ID:         "fe-1",
		Path:       "/project/src/main.go",
		ChangeType: "modify",
		Timestamp:  now,
	}

	if err := s.PushFileEvent(ctx, event); err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}

	// Verify it shows up as unmatched
	events, err := s.QueryUnmatchedFileEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedFileEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 unmatched file event, got %d", len(events))
	}
	if events[0].ID != "fe-1" {
		t.Errorf("expected ID %q, got %q", "fe-1", events[0].ID)
	}
	if events[0].FilePath != "/project/src/main.go" {
		t.Errorf("expected path %q, got %q", "/project/src/main.go", events[0].FilePath)
	}
}

func TestPushFileEvent_Validation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	tests := []struct {
		name  string
		event FileEvent
	}{
		{
			name:  "missing ID",
			event: FileEvent{Path: "/foo.go", ChangeType: "modify", Timestamp: time.Now()},
		},
		{
			name:  "relative path",
			event: FileEvent{ID: "x", Path: "foo.go", ChangeType: "modify", Timestamp: time.Now()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.PushFileEvent(ctx, tt.event)
			if err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

func TestPushAgentEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().Truncate(time.Nanosecond)

	event := AgentEvent{
		ID:         "ae-1",
		FilePath:   "src/foo.go",
		ChangeType: "edit",
		Timestamp:  now,
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
	}

	if err := s.PushAgentEvent(ctx, event); err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}

	// Verify path was normalized (leading / added)
	events, err := s.QueryUnmatchedAgentEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedAgentEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 unmatched agent event, got %d", len(events))
	}
	if events[0].FilePath != "/src/foo.go" {
		t.Errorf("expected normalized path %q, got %q", "/src/foo.go", events[0].FilePath)
	}
}

func TestPushAgentEvent_Validation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	err := s.PushAgentEvent(ctx, AgentEvent{})
	if err == nil {
		t.Error("expected validation error for empty agent event, got nil")
	}
}

func TestDuplicateIDIgnored(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	event := FileEvent{
		ID:         "fe-dup",
		Path:       "/project/foo.go",
		ChangeType: "create",
		Timestamp:  now,
	}

	// Insert twice — second should be silently ignored
	if err := s.PushFileEvent(ctx, event); err != nil {
		t.Fatalf("first PushFileEvent: %v", err)
	}
	if err := s.PushFileEvent(ctx, event); err != nil {
		t.Fatalf("second PushFileEvent: %v", err)
	}

	events, err := s.QueryUnmatchedFileEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedFileEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event after duplicate insert, got %d", len(events))
	}
}

func TestSetMatchedWith(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now()

	// Push one file event and one agent event
	fe := FileEvent{
		ID:         "fe-match",
		Path:       "/project/src/foo.go",
		ChangeType: "modify",
		Timestamp:  now,
	}
	ae := AgentEvent{
		ID:         "ae-match",
		FilePath:   "src/foo.go",
		ChangeType: "edit",
		Timestamp:  now,
		SessionID:  "sess-1",
		ExchangeID: "exch-1",
		AgentType:  "claude-code",
	}

	if err := s.PushFileEvent(ctx, fe); err != nil {
		t.Fatalf("PushFileEvent: %v", err)
	}
	if err := s.PushAgentEvent(ctx, ae); err != nil {
		t.Fatalf("PushAgentEvent: %v", err)
	}

	// Verify both are unmatched
	fileEvents, err := s.QueryUnmatchedFileEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedFileEvents: %v", err)
	}
	agentEvents, err := s.QueryUnmatchedAgentEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedAgentEvents: %v", err)
	}
	if len(fileEvents) != 1 || len(agentEvents) != 1 {
		t.Fatalf("expected 1 unmatched of each, got %d file, %d agent", len(fileEvents), len(agentEvents))
	}

	// Match them
	if err := s.SetMatchedWith(ctx, "fe-match", "ae-match"); err != nil {
		t.Fatalf("SetMatchedWith: %v", err)
	}

	// Both should now be absent from unmatched queries
	fileEvents, err = s.QueryUnmatchedFileEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedFileEvents after match: %v", err)
	}
	agentEvents, err = s.QueryUnmatchedAgentEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedAgentEvents after match: %v", err)
	}
	if len(fileEvents) != 0 {
		t.Errorf("expected 0 unmatched file events after match, got %d", len(fileEvents))
	}
	if len(agentEvents) != 0 {
		t.Errorf("expected 0 unmatched agent events after match, got %d", len(agentEvents))
	}
}

func TestQueryUnmatched_OrderByTimestamp(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// Insert events out of order — query should return them sorted
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	events := []FileEvent{
		{ID: "fe-3", Path: "/c.go", ChangeType: "create", Timestamp: base.Add(2 * time.Second)},
		{ID: "fe-1", Path: "/a.go", ChangeType: "create", Timestamp: base},
		{ID: "fe-2", Path: "/b.go", ChangeType: "create", Timestamp: base.Add(1 * time.Second)},
	}

	for _, e := range events {
		if err := s.PushFileEvent(ctx, e); err != nil {
			t.Fatalf("PushFileEvent(%s): %v", e.ID, err)
		}
	}

	result, err := s.QueryUnmatchedFileEvents(ctx, distantPast, distantFuture)
	if err != nil {
		t.Fatalf("QueryUnmatchedFileEvents: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 events, got %d", len(result))
	}

	// Verify ascending timestamp order
	expectedOrder := []string{"fe-1", "fe-2", "fe-3"}
	for i, want := range expectedOrder {
		if result[i].ID != want {
			t.Errorf("result[%d].ID = %q, want %q", i, result[i].ID, want)
		}
	}
}
