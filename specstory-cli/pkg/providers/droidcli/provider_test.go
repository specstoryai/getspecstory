package droidcli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionMentionsProject(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "session.jsonl")
	content := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"/Users/test/project"}]}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if !sessionMentionsProject(path, "/Users/test/project") {
		t.Fatalf("expected true when path present")
	}
}
