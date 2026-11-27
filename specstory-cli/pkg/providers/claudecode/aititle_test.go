package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAiTitleFromFile_LatestWins(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	lines := `{"type":"user","uuid":"u1","sessionId":"s1","message":{"role":"user","content":"hi"}}
{"type":"ai-title","aiTitle":"First Title","sessionId":"s1"}
{"type":"assistant","uuid":"a1","sessionId":"s1","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
{"type":"ai-title","aiTitle":"Latest Title","sessionId":"s1"}
`
	if err := os.WriteFile(p, []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}
	if got := aiTitleFromFile(p); got != "Latest Title" {
		t.Errorf("aiTitleFromFile = %q, want %q", got, "Latest Title")
	}
}

func TestAiTitleFromFile_BlankWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "n.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"user","uuid":"u1","sessionId":"s1"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := aiTitleFromFile(p); got != "" {
		t.Errorf("aiTitleFromFile = %q, want empty", got)
	}
	if got := aiTitleFromFile(filepath.Join(dir, "does-not-exist.jsonl")); got != "" {
		t.Errorf("missing file should yield empty, got %q", got)
	}
}
