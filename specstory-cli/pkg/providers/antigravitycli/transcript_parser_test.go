package antigravitycli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHistoryIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Missing file → empty map, nil error.
	idx, err := loadHistoryIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(idx) != 0 {
		t.Errorf("expected empty index, got %d", len(idx))
	}

	writeHistory(t, home,
		`{"display":"first prompt","timestamp":1779831073907,"workspace":"/proj"}`, // no conversationId → skipped
		`{"display":"second","timestamp":1779831156198,"workspace":"/proj","conversationId":"conv-1"}`,
		`bad json`,
	)

	idx, err = loadHistoryIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("expected 1 mapped conversation, got %d", len(idx))
	}
	if idx["conv-1"].Workspace != "/proj" {
		t.Errorf("expected workspace /proj, got %q", idx["conv-1"].Workspace)
	}
}

func TestParseTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		``, // blank line tolerated
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-05-26T21:31:16Z","content":"hello"}`,
		`{bad json`, // malformed line skipped
	)

	history := map[string]historyEntry{"conv-1": {Workspace: "/proj"}}
	session, err := parseTranscript("conv-1", path, history, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(session.Steps) != 2 {
		t.Fatalf("expected 2 decoded steps, got %d", len(session.Steps))
	}
	if session.CreatedAt != "2026-05-26T21:31:13Z" || session.UpdatedAt != "2026-05-26T21:31:16Z" {
		t.Errorf("unexpected created/updated: %q / %q", session.CreatedAt, session.UpdatedAt)
	}
	if session.Workspace != "/proj" {
		t.Errorf("expected workspace from history, got %q", session.Workspace)
	}
	if session.RawData == "" {
		t.Errorf("expected raw data to be retained when wantRawData is true")
	}

	// wantRawData=false must not retain bytes.
	session2, err := parseTranscript("conv-1", path, history, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session2.RawData != "" {
		t.Errorf("expected no raw data when wantRawData is false")
	}
}

func TestNormalizeFallbackArgs(t *testing.T) {
	// transcript.jsonl double-encodes every value; normalization decodes once.
	in := map[string]any{
		"CommandLine":       `"git status"`,
		"WaitMsBeforeAsync": "5000",
		"Overwrite":         "true",
	}
	out := normalizeFallbackArgs(in)
	if out["CommandLine"] != "git status" {
		t.Errorf("CommandLine = %v, want git status", out["CommandLine"])
	}
	if out["Overwrite"] != true {
		t.Errorf("Overwrite = %v (%T), want bool true", out["Overwrite"], out["Overwrite"])
	}
	if n, ok := out["WaitMsBeforeAsync"].(float64); !ok || n != 5000 {
		t.Errorf("WaitMsBeforeAsync = %v (%T), want 5000", out["WaitMsBeforeAsync"], out["WaitMsBeforeAsync"])
	}
}

func TestLoadTaskOutputs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
	)
	tasksDir := filepath.Join(filepath.Dir(filepath.Dir(path)), "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "task-34.log"), []byte("async output\n"), 0o644); err != nil {
		t.Fatalf("write task log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "not-a-task.log"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored task log: %v", err)
	}

	session, err := parseTranscript("conv-1", path, nil, nil, false)
	if err != nil {
		t.Fatalf("parseTranscript: %v", err)
	}
	if got := session.TaskOutputs[34]; got != "async output" {
		t.Errorf("TaskOutputs[34] = %q, want async output", got)
	}
}

func TestSessionMetadata(t *testing.T) {
	session := &agSession{
		ConversationID: "conv-1",
		CreatedAt:      "2026-05-26T21:31:13Z",
		Steps: []transcriptStep{
			{Type: typeUserInput, Content: "<USER_REQUEST>\nFix the bug\n</USER_REQUEST>"},
		},
	}
	meta := sessionMetadata(session, map[string]historyEntry{})
	if meta == nil {
		t.Fatalf("expected metadata")
	}
	if meta.SessionID != "conv-1" || meta.CreatedAt != "2026-05-26T21:31:13Z" {
		t.Errorf("unexpected metadata: %+v", meta)
	}
	if meta.Slug == "" || meta.Name == "" {
		t.Errorf("expected slug and name to be derived, got %+v", meta)
	}

	// No user prompt → nil (skip).
	empty := &agSession{ConversationID: "c", Steps: []transcriptStep{{Type: typePlannerResponse, Content: "x"}}}
	if sessionMetadata(empty, map[string]historyEntry{}) != nil {
		t.Errorf("expected nil metadata for session without user prompt")
	}
}

func TestMsEpochToRFC3339(t *testing.T) {
	if got := msEpochToRFC3339(0); got != "" {
		t.Errorf("expected empty for 0, got %q", got)
	}
	if got := msEpochToRFC3339(1779831073907); got == "" {
		t.Errorf("expected a timestamp for valid epoch ms")
	}
}
