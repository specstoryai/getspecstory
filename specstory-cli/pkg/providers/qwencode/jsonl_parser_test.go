package qwencode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSessionFile_Basic(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-basic.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	if session.ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("session ID = %q, want %q", session.ID, "11111111-2222-3333-4444-555555555555")
	}
	if len(session.Records) != 8 {
		t.Errorf("record count = %d, want 8", len(session.Records))
	}
	if session.StartTime != "2026-08-07T16:52:35.916Z" {
		t.Errorf("StartTime = %q, want first record timestamp", session.StartTime)
	}
	if session.LastUpdated != "2026-08-07T16:53:10.000Z" {
		t.Errorf("LastUpdated = %q, want last record timestamp", session.LastUpdated)
	}
	if session.Cwd != "/Users/dev/project" {
		t.Errorf("Cwd = %q, want /Users/dev/project", session.Cwd)
	}
	if session.Version != "0.21.7" {
		t.Errorf("Version = %q, want 0.21.7", session.Version)
	}
}

func TestParseSessionFile_MalformedLinesSkipped(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-malformed.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}

	if len(session.Records) != 2 {
		t.Errorf("record count = %d, want 2 (malformed line skipped)", len(session.Records))
	}
	if session.ID != "99999999-8888-7777-6666-555555555555" {
		t.Errorf("session ID = %q, want session id from valid records", session.ID)
	}
}

func TestParseSessionFile_IDFallsBackToFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deadbeef-0000-1111-2222-333333333333.jsonl")
	// Records without a sessionId field
	content := `{"uuid":"u-1","parentUuid":null,"timestamp":"2026-08-07T10:00:00.000Z","type":"user","provenance":"real_user","message":{"role":"user","parts":[{"text":"hi"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("ParseSessionFile failed: %v", err)
	}
	if session.ID != "deadbeef-0000-1111-2222-333333333333" {
		t.Errorf("session ID = %q, want filename stem", session.ID)
	}
}

func TestFindSessions(t *testing.T) {
	dir := t.TempDir()
	chatsDir := filepath.Join(dir, "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	older := `{"uuid":"u-1","parentUuid":null,"sessionId":"older","timestamp":"2026-08-01T10:00:00.000Z","type":"user","provenance":"real_user","message":{"role":"user","parts":[{"text":"old"}]}}` + "\n"
	newer := `{"uuid":"u-1","parentUuid":null,"sessionId":"newer","timestamp":"2026-08-07T10:00:00.000Z","type":"user","provenance":"real_user","message":{"role":"user","parts":[{"text":"new"}]}}` + "\n"

	if err := os.WriteFile(filepath.Join(chatsDir, "older.jsonl"), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatsDir, "newer.jsonl"), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-transcript files must be ignored
	if err := os.WriteFile(filepath.Join(chatsDir, "newer.runtime.json"), []byte(`{"pid":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty transcript must be skipped
	if err := os.WriteFile(filepath.Join(chatsDir, "empty.jsonl"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := FindSessions(dir)
	if err != nil {
		t.Fatalf("FindSessions failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(sessions))
	}
	if sessions[0].ID != "newer" || sessions[1].ID != "older" {
		t.Errorf("sessions not sorted most-recent-first: got [%s, %s]", sessions[0].ID, sessions[1].ID)
	}
}

func TestFindSessions_MissingChatsDir(t *testing.T) {
	sessions, err := FindSessions(t.TempDir())
	if err != nil {
		t.Fatalf("FindSessions failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("session count = %d, want 0", len(sessions))
	}
}

func TestFirstRealUserText_SkipsNotifications(t *testing.T) {
	session := &QwenSession{
		Records: []QwenRecord{
			{Type: "system", Provenance: "system"},
			{Type: "user", Subtype: "notification", Provenance: "system",
				Message: &QwenMessage{Role: "user", Parts: []QwenPart{{Text: "<task-notification>x</task-notification>"}}}},
			{Type: "user", Provenance: "real_user",
				Message: &QwenMessage{Role: "user", Parts: []QwenPart{{Text: "real question"}}}},
		},
	}
	if got := session.FirstRealUserText(); got != "real question" {
		t.Errorf("FirstRealUserText = %q, want %q", got, "real question")
	}
}

func TestRecordTextAndThoughtContent(t *testing.T) {
	record := QwenRecord{
		Message: &QwenMessage{
			Role: "model",
			Parts: []QwenPart{
				{Text: "thinking hard", Thought: true},
				{Text: "visible answer"},
				{Text: "more thinking", Thought: true},
			},
		},
	}
	if got := record.TextContent(); got != "visible answer" {
		t.Errorf("TextContent = %q, want %q", got, "visible answer")
	}
	if got := record.ThoughtContent(); got != "thinking hard\n\nmore thinking" {
		t.Errorf("ThoughtContent = %q", got)
	}
}

func TestResultDisplayString(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain string", raw: `"2 passed"`, want: "2 passed"},
		{name: "file diff object", raw: `{"fileDiff":"@@ -1 +1 @@","fileName":"a.py"}`, want: "@@ -1 +1 @@"},
		{name: "output object", raw: `{"output":"listing"}`, want: "listing"},
		{name: "null", raw: `null`, want: ""},
		{name: "empty", raw: ``, want: ""},
		{name: "unrecognized object", raw: `{"other":1}`, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resultDisplayString([]byte(tt.raw)); got != tt.want {
				t.Errorf("resultDisplayString(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
