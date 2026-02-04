package droidcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFactorySession(t *testing.T) {
	tests := []struct {
		name           string
		lines          []string
		expectedID     string
		expectedSlug   string
		expectedBlocks int
		validate       func(t *testing.T, session *fdSession)
	}{
		{
			name: "parses complete session with all block types",
			lines: []string{
				`{"type":"session_start","timestamp":"2025-01-01T12:00:00Z","session":{"id":"session-1","title":"Implement feature","created_at":"2025-01-01T11:59:00Z"}}`,
				`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","model":"","content":[{"type":"text","text":"Implement feature please"}]}}`,
				`{"type":"message","id":"m2","timestamp":"2025-01-01T12:00:02Z","message":{"role":"assistant","model":"factory-pro","content":[{"type":"text","text":"Working on it"},{"type":"tool_use","id":"call_1","name":"Execute","input":{"command":"ls"}},{"type":"tool_result","tool_use_id":"call_1","content":"main.go"}]}}`,
				`{"type":"todo_state","timestamp":"2025-01-01T12:00:03Z","todos":[{"description":"Add parser","status":"in_progress"}]}`,
			},
			expectedID:     "session-1",
			expectedSlug:   "implement-feature-please",
			expectedBlocks: 4,
			validate: func(t *testing.T, session *fdSession) {
				toolBlock := session.Blocks[2]
				if toolBlock.Kind != blockTool {
					t.Errorf("expected tool block at index 2, got kind %v", toolBlock.Kind)
				}
				if toolBlock.Tool == nil || toolBlock.Tool.Result == nil {
					t.Errorf("expected tool result to be populated")
					return
				}
				if strings.TrimSpace(toolBlock.Tool.Result.Content) != "main.go" {
					t.Errorf("unexpected tool output %q", toolBlock.Tool.Result.Content)
				}
			},
		},
		{
			name: "extracts session ID from filename when not in content",
			lines: []string{
				`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Hello world"}]}}`,
			},
			expectedID:     "test-session",
			expectedSlug:   "hello-world",
			expectedBlocks: 1,
		},
		{
			name: "handles reasoning blocks",
			lines: []string{
				`{"type":"session_start","session":{"id":"reasoning-test"}}`,
				`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Think about this"}]}}`,
				`{"type":"message","id":"m2","timestamp":"2025-01-01T12:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","text":"Let me reason..."},{"type":"text","text":"Here is my answer"}]}}`,
			},
			expectedID:     "reasoning-test",
			expectedSlug:   "think-about-this",
			expectedBlocks: 3,
			validate: func(t *testing.T, session *fdSession) {
				reasoningBlock := session.Blocks[1]
				if !reasoningBlock.IsReasoning {
					t.Errorf("expected reasoning block to have IsReasoning=true")
				}
			},
		},
		{
			name: "handles compaction/summary events",
			lines: []string{
				`{"type":"session_start","session":{"id":"summary-test"}}`,
				`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Do something"}]}}`,
				`{"type":"compaction_state","timestamp":"2025-01-01T12:00:05Z","summary":{"title":"Progress","body":"Completed the task"}}`,
			},
			expectedID:     "summary-test",
			expectedSlug:   "do-something",
			expectedBlocks: 2,
			validate: func(t *testing.T, session *fdSession) {
				summaryBlock := session.Blocks[1]
				if summaryBlock.Kind != blockSummary {
					t.Errorf("expected summary block, got kind %v", summaryBlock.Kind)
				}
				if summaryBlock.Summary == nil || summaryBlock.Summary.Body != "Completed the task" {
					t.Errorf("unexpected summary content")
				}
			},
		},
		{
			name: "skips malformed JSON lines gracefully",
			lines: []string{
				`{"type":"session_start","session":{"id":"malformed-test"}}`,
				`this is not valid json`,
				`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Valid message"}]}}`,
			},
			expectedID:     "malformed-test",
			expectedSlug:   "valid-message",
			expectedBlocks: 1,
		},
		{
			name: "handles empty todo list gracefully",
			lines: []string{
				`{"type":"session_start","session":{"id":"empty-todo-test"}}`,
				`{"type":"message","id":"m1","timestamp":"2025-01-01T12:00:01Z","message":{"role":"user","content":[{"type":"text","text":"Test"}]}}`,
				`{"type":"todo_state","timestamp":"2025-01-01T12:00:02Z","todos":[]}`,
			},
			expectedID:     "empty-todo-test",
			expectedSlug:   "test",
			expectedBlocks: 1, // Empty todo_state should be skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			// Use expectedID for filename if it's a fallback test
			filename := "test-session.jsonl"
			if tt.expectedID != "test-session" {
				filename = tt.expectedID + ".jsonl"
			}
			path := filepath.Join(tmp, filename)

			if err := os.WriteFile(path, []byte(strings.Join(tt.lines, "\n")), 0o644); err != nil {
				t.Fatalf("write sample: %v", err)
			}

			session, err := parseFactorySession(path)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}

			if session.ID != tt.expectedID {
				t.Errorf("session.ID = %q, want %q", session.ID, tt.expectedID)
			}

			if session.Slug != tt.expectedSlug {
				t.Errorf("session.Slug = %q, want %q", session.Slug, tt.expectedSlug)
			}

			if len(session.Blocks) != tt.expectedBlocks {
				t.Errorf("len(session.Blocks) = %d, want %d", len(session.Blocks), tt.expectedBlocks)
			}

			if tt.validate != nil {
				tt.validate(t, session)
			}
		})
	}
}
