package droidcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFactorySession(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "session-1.jsonl")
	lines := []string{
		`{"type":"session_start","timestamp":"2025-01-01T12:00:00Z","session":{"id":"session-1","title":"Implement feature","created_at":"2025-01-01T11:59:00Z"}}`,
		`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","model":"","content":[{"type":"text","text":"Implement feature please"}]}}`,
		`{"type":"message","id":"m2","timestamp":"2025-01-01T12:00:02Z","message":{"role":"assistant","model":"factory-pro","content":[{"type":"text","text":"Working on it"},{"type":"tool_use","id":"call_1","name":"Execute","input":{"command":"ls"}},{"type":"tool_result","tool_use_id":"call_1","content":"main.go"}]}}`,
		`{"type":"todo_state","timestamp":"2025-01-01T12:00:03Z","todos":[{"description":"Add parser","status":"in_progress"}]}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	session, err := parseFactorySession(path)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if session.ID != "session-1" {
		t.Fatalf("expected session ID session-1, got %s", session.ID)
	}

	if session.Slug != "implement-feature-please" {
		t.Fatalf("unexpected slug %s", session.Slug)
	}

	if len(session.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(session.Blocks))
	}

	toolBlock := session.Blocks[2]
	if toolBlock.Kind != blockTool {
		t.Fatalf("expected tool block at index 2")
	}
	if toolBlock.Tool == nil || toolBlock.Tool.Result == nil {
		t.Fatalf("expected tool result")
	}
	if strings.TrimSpace(toolBlock.Tool.Result.Content) != "main.go" {
		t.Fatalf("unexpected tool output %s", toolBlock.Tool.Result.Content)
	}
}
