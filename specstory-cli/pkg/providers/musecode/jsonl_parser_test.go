package musecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMuseTimeToISO(t *testing.T) {
	tests := []struct {
		name         string
		microseconds int64
		expected     string
	}{
		{
			name:         "whole second",
			microseconds: 1786140000000000,
			expected:     "2026-08-07T22:00:00.000Z",
		},
		{
			name:         "sub-millisecond precision truncates to milliseconds",
			microseconds: 1786140035565459,
			expected:     "2026-08-07T22:00:35.565Z",
		},
		{
			name:         "zero means absent",
			microseconds: 0,
			expected:     "",
		},
		{
			name:         "negative means absent",
			microseconds: -1,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := museTimeToISO(tt.microseconds)
			if got != tt.expected {
				t.Errorf("museTimeToISO(%d) = %q, want %q", tt.microseconds, got, tt.expected)
			}
		})
	}
}

func TestParseSessionFile_Basic(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-basic.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	if session.ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("session ID = %q, want the stream id", session.ID)
	}
	// The task-kind payload and the context_block_diagnostic run event are
	// bookkeeping and must not survive the parse.
	if len(session.Events) != 8 {
		t.Errorf("event count = %d, want 8 (non-conversation records dropped)", len(session.Events))
	}
	if session.StartTime != "2026-08-07T22:00:00.000Z" {
		t.Errorf("StartTime = %q, want the first record's converted timestamp", session.StartTime)
	}
	if session.LastUpdated != "2026-08-07T22:00:08.000Z" {
		t.Errorf("LastUpdated = %q, want the last record's converted timestamp", session.LastUpdated)
	}
	if session.WorkspaceRoot != "/Users/dev/project" {
		t.Errorf("WorkspaceRoot = %q, want /Users/dev/project", session.WorkspaceRoot)
	}
	if session.ProviderID != "meta" {
		t.Errorf("ProviderID = %q, want meta", session.ProviderID)
	}
	if session.ModelID != "muse-spark-1.2" {
		t.Errorf("ModelID = %q, want muse-spark-1.2", session.ModelID)
	}
	if session.Version != "0.1.0" {
		t.Errorf("Version = %q, want the build semver", session.Version)
	}
}

func TestParseSessionFile_EventKinds(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-basic.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	expected := []string{
		eventStarted,
		eventReasoningCommitted,
		eventModelCompleted,
		eventAssistantToolCallsCommitted,
		eventToolResultBatchCommitted,
		eventAssistantMessageCommitted,
		eventModelCompleted,
		eventTerminal,
	}
	if len(session.Events) != len(expected) {
		t.Fatalf("event count = %d, want %d", len(session.Events), len(expected))
	}
	for i, want := range expected {
		if got := session.Events[i].Kind(); got != want {
			t.Errorf("event %d kind = %q, want %q", i, got, want)
		}
	}
}

func TestParseSessionFile_StreamFilterExcludesSubagentRecords(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-subagent-noise.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	// started, tool calls, tool results, assistant message, terminal.
	if len(session.Events) != 5 {
		t.Errorf("event count = %d, want 5 (task-stream records excluded)", len(session.Events))
	}
	for i := range session.Events {
		if strings.Contains(session.Events[i].Event.Prompt, "demo-worker") {
			t.Errorf("event %d carries a subagent objective as a prompt: %q", i, session.Events[i].Event.Prompt)
		}
	}
	// The task stream's metadata record has no workspace_root; letting it
	// through would blank out the session's project association.
	if session.WorkspaceRoot != "/Users/dev/project" {
		t.Errorf("WorkspaceRoot = %q, want /Users/dev/project", session.WorkspaceRoot)
	}
}

func TestParseSessionFile_MalformedLinesSkipped(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-malformed.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	if len(session.Events) != 3 {
		t.Errorf("event count = %d, want 3 (malformed line skipped)", len(session.Events))
	}
	if session.ID != "55555555-6666-7777-8888-999999999999" {
		t.Errorf("session ID = %q, want the id from the valid records", session.ID)
	}
}

func TestParseSessionFile_IDFallsBackToDirectoryName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deadbeef-0000-1111-2222-333333333333")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")

	// Records with no stream envelope at all: the store layout is the only
	// remaining source of the session id.
	content := `{"schema_version":1,"id":"rec-1","recorded_at":1786140000000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"r1","event":{"kind":"started","prompt":"hi"}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}
	if session.ID != "deadbeef-0000-1111-2222-333333333333" {
		t.Errorf("session ID = %q, want the directory name", session.ID)
	}
	if len(session.Events) != 1 {
		t.Errorf("event count = %d, want 1", len(session.Events))
	}
}

func TestFirstUserPrompt(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		expected string
	}{
		{
			name:     "first run's prompt",
			file:     "session-multiturn.jsonl",
			expected: "what tools do you have access to",
		},
		{
			name:     "subagent objectives never win",
			file:     "session-subagent-noise.jsonl",
			expected: "Spawn a worker to write the report.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, err := ParseSessionFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("ParseSessionFile failed: %v", err)
			}
			if got := session.FirstUserPrompt(); got != tt.expected {
				t.Errorf("FirstUserPrompt() = %q, want %q", got, tt.expected)
			}
		})
	}
}
