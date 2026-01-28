package droidcli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionMentionsProject_UsesSessionCwd(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := fmt.Sprintf(`{"type":"session_start","id":"sess-1","cwd":%q}`, projectDir)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !sessionMentionsProject(path, projectDir) {
		t.Fatalf("expected true when cwd matches")
	}
}

func TestSessionMentionsProject_CwdMismatchIgnoresTextFallback(t *testing.T) {
	projectDir := t.TempDir()
	otherDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := fmt.Sprintf(`{"type":"session_start","id":"sess-1","cwd":%q}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, otherDir, projectDir)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if sessionMentionsProject(path, projectDir) {
		t.Fatalf("expected false when cwd mismatches even if text mentions project")
	}
}

func TestSessionMentionsProject_FallsBackToTextWhenNoCwd(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := fmt.Sprintf(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, projectDir)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !sessionMentionsProject(path, projectDir) {
		t.Fatalf("expected true when path present in text")
	}
}
