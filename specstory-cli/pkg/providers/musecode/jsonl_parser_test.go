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

// A transcript truncated mid-subagent begins with task-stream records. The
// parser must still identify the real session stream from the metadata record
// rather than adopting the first task stream it sees, which would filter out
// the entire real conversation.
func TestParseSessionFile_TruncatedTaskStreamFirst(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-truncated-task-first.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	if session.ID != "99999999-aaaa-bbbb-cccc-dddddddddddd" {
		t.Errorf("session ID = %q, want the session stream id from the metadata record", session.ID)
	}
	if session.WorkspaceRoot != "/Users/dev/project" {
		t.Errorf("WorkspaceRoot = %q, want the metadata workspace root", session.WorkspaceRoot)
	}

	data, err := GenerateAgentSession(session, session.WorkspaceRoot)
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if len(data.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1 (the real turn survives)", len(data.Exchanges))
	}
	for _, msg := range data.Exchanges[0].Messages {
		for _, part := range msg.Content {
			if strings.Contains(part.Text, "Subagent prose") || strings.Contains(part.Text, "demo-worker") {
				t.Errorf("subagent task-stream content leaked into the conversation: %q", part.Text)
			}
		}
	}
	if got := session.FirstUserPrompt(); got != "the real user turn" {
		t.Errorf("FirstUserPrompt = %q, want the real user turn", got)
	}
}

// A transcript inside the store layout takes its id from the directory name,
// so record order cannot change it.
func TestParseSessionFile_StorePathSettlesSessionID(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "12345678-1234-1234-1234-123456789abc")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("testdata", "session-truncated-task-first.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "session.jsonl")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}
	if session.ID != "12345678-1234-1234-1234-123456789abc" {
		t.Errorf("session ID = %q, want the directory name", session.ID)
	}
	// Records for a different stream are foreign under this id, so no
	// conversation survives; the point is that the directory name wins.
	if len(session.Events) != 0 {
		t.Errorf("event count = %d, want 0 (all records belong to other streams)", len(session.Events))
	}
}

func TestIsSessionID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "canonical id", input: "a6eea682-47ef-400a-914e-b310b0136c52", want: true},
		{name: "uppercase hex", input: "A6EEA682-47EF-400A-914E-B310B0136C52", want: true},
		{name: "testdata dir", input: "testdata", want: false},
		{name: "too short", input: "a6eea682", want: false},
		{name: "wrong separators", input: "a6eea68247ef400a914eb310b0136c52aaaa", want: false},
		{name: "non-hex", input: "z6eea682-47ef-400a-914e-b310b0136c52", want: false},
		{name: "empty", input: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSessionID(tt.input); got != tt.want {
				t.Errorf("isSessionID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// A single oversized line must be refused by the reader's own bound, not read
// into memory and measured afterwards: the check exists to prevent exactly the
// allocation that reading-then-checking would already have made.
func TestParseSessionFile_RefusesOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), sessionFileName)

	var line strings.Builder
	line.WriteString(`{"payload_type":"runtime.session","payload":"`)
	line.WriteString(strings.Repeat("x", maxReasonableLineSize))
	line.WriteString("\"}\n")
	if err := os.WriteFile(path, []byte(line.String()), 0o644); err != nil {
		t.Fatalf("failed to write oversized transcript: %v", err)
	}

	if _, err := ParseSessionFile(path); err == nil {
		t.Fatal("expected an error for a line past the size limit")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should name the size limit, got: %v", err)
	}
}
