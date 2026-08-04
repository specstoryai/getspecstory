package qwencode

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSessionFile(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-1.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile returned error: %v", err)
	}

	if session.ID != "3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b" {
		t.Errorf("session ID = %q, want %q", session.ID, "3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b")
	}
	if session.Cwd != "/tmp/qwen-fixture-project" {
		t.Errorf("session Cwd = %q, want %q", session.Cwd, "/tmp/qwen-fixture-project")
	}
	if session.Version != "0.21.4" {
		t.Errorf("session Version = %q, want %q", session.Version, "0.21.4")
	}
	if session.Model != "qwen3-coder" {
		t.Errorf("session Model = %q, want %q", session.Model, "qwen3-coder")
	}
	if session.StartTime != "2026-08-01T09:00:00.000Z" {
		t.Errorf("session StartTime = %q, want %q", session.StartTime, "2026-08-01T09:00:00.000Z")
	}
	if session.LastUpdated != "2026-08-01T09:01:03.500Z" {
		t.Errorf("session LastUpdated = %q, want %q", session.LastUpdated, "2026-08-01T09:01:03.500Z")
	}
	if len(session.Entries) != 8 {
		t.Errorf("entry count = %d, want 8", len(session.Entries))
	}

	if got := session.FirstUserMessage(); got != "Write a hello world script" {
		t.Errorf("FirstUserMessage = %q, want %q", got, "Write a hello world script")
	}
}

func TestParseSessionFileSkipsMalformedLines(t *testing.T) {
	session, err := ParseSessionFile(filepath.Join("testdata", "session-malformed.jsonl"))
	if err != nil {
		t.Fatalf("ParseSessionFile returned error: %v", err)
	}

	if len(session.Entries) != 2 {
		t.Fatalf("entry count = %d, want 2 (malformed line skipped)", len(session.Entries))
	}
	if session.ID != "9a8b7c6d-1111-2222-3333-444455556666" {
		t.Errorf("session ID = %q, want %q", session.ID, "9a8b7c6d-1111-2222-3333-444455556666")
	}
}

func TestParseSessionFileFallsBackToFilenameID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abcd1234.jsonl")
	content := `{"uuid":"u1","timestamp":"2026-08-02T12:00:00.000Z","type":"user","provenance":"real_user","message":{"role":"user","parts":[{"text":"hi"}]}}` + "\n"
	if err := writeFileForTest(t, path, content); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("ParseSessionFile returned error: %v", err)
	}
	if session.ID != "abcd1234" {
		t.Errorf("session ID = %q, want fallback %q", session.ID, "abcd1234")
	}
}

func TestParseSessionFileMissing(t *testing.T) {
	if _, err := ParseSessionFile(filepath.Join("testdata", "does-not-exist.jsonl")); err == nil {
		t.Fatal("ParseSessionFile should fail for a missing file")
	}
}

func TestEntryTextIgnoresThoughts(t *testing.T) {
	text := "visible text"
	thought := "hidden reasoning"
	entry := QwenSessionEntry{
		Type:       entryTypeUser,
		Provenance: provenanceRealUser,
		Message: &QwenMessage{
			Role: "user",
			Parts: []QwenPart{
				{Text: &thought, Thought: true},
				{Text: &text},
			},
		},
	}

	if got := entryText(entry); got != "visible text" {
		t.Errorf("entryText = %q, want %q", got, "visible text")
	}
}

func TestFindSessionsSortsByStartTime(t *testing.T) {
	projectDir := t.TempDir()
	chatsDir := filepath.Join(projectDir, "chats")
	if err := mkdirAllForTest(t, chatsDir); err != nil {
		t.Fatalf("failed to create chats dir: %v", err)
	}

	// Two single-entry transcripts with different start times; also drop a
	// .runtime.json marker that must be ignored.
	early := `{"uuid":"u1","sessionId":"11111111-0000-0000-0000-000000000001","timestamp":"2026-08-01T08:00:00.000Z","type":"user","provenance":"real_user","message":{"role":"user","parts":[{"text":"early"}]}}` + "\n"
	late := `{"uuid":"u2","sessionId":"22222222-0000-0000-0000-000000000002","timestamp":"2026-08-02T08:00:00.000Z","type":"user","provenance":"real_user","message":{"role":"user","parts":[{"text":"late"}]}}` + "\n"

	if err := writeFileForTest(t, filepath.Join(chatsDir, "22222222-0000-0000-0000-000000000002.jsonl"), late); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	if err := writeFileForTest(t, filepath.Join(chatsDir, "11111111-0000-0000-0000-000000000001.jsonl"), early); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	if err := writeFileForTest(t, filepath.Join(chatsDir, "22222222-0000-0000-0000-000000000002.runtime.json"), "{}"); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	sessions, err := FindSessions(projectDir)
	if err != nil {
		t.Fatalf("FindSessions returned error: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(sessions))
	}
	if !strings.HasPrefix(sessions[0].ID, "11111111") {
		t.Errorf("first session = %q, want the earlier session", sessions[0].ID)
	}
	if !strings.HasPrefix(sessions[1].ID, "22222222") {
		t.Errorf("second session = %q, want the later session", sessions[1].ID)
	}
}

func TestFindSessionsMissingChatsDir(t *testing.T) {
	sessions, err := FindSessions(t.TempDir())
	if err != nil {
		t.Fatalf("FindSessions returned error for missing chats dir: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("session count = %d, want 0", len(sessions))
	}
}
