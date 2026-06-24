package antigravitycli

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConversation creates a fake brain transcript under home and returns the
// transcript path. It is the shared filesystem fixture for the package tests.
func writeConversation(t *testing.T, home, conversationID string, lines ...string) string {
	t.Helper()
	return writeConversationFile(t, home, conversationID, transcriptFileName, lines...)
}

func writeConversationFile(t *testing.T, home, conversationID, fileName string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, geminiRootDir, antigravityDirName, brainDirName, conversationID, systemGeneratedDir, logsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, fileName)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// writeHistory writes a history.jsonl with the given raw lines.
func writeHistory(t *testing.T, home string, lines ...string) {
	t.Helper()
	dir := filepath.Join(home, geminiRootDir, antigravityDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, historyFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write history: %v", err)
	}
}

func TestResolvePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := resolveAntigravityDir()
	if err != nil {
		t.Fatalf("resolveAntigravityDir: %v", err)
	}
	if want := filepath.Join(home, ".gemini", "antigravity-cli"); dir != want {
		t.Errorf("resolveAntigravityDir = %q, want %q", dir, want)
	}

	tp, err := transcriptDirFor("abc")
	if err != nil {
		t.Fatalf("transcriptDirFor: %v", err)
	}
	if want := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "abc", ".system_generated", "logs"); tp != want {
		t.Errorf("transcriptDirFor = %q, want %q", tp, want)
	}
}

func TestListConversationFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Missing brain dir → empty, no error.
	files, err := listConversationFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files for missing brain dir, got %d", len(files))
	}

	writeConversation(t, home, "conv-a", `{"step_index":0,"type":"USER_INPUT"}`)
	writeConversation(t, home, "conv-b", `{"step_index":0,"type":"USER_INPUT"}`)

	files, err = listConversationFiles()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(files))
	}
}

func TestFindTranscriptByID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if path, err := findTranscriptByID("missing"); err != nil || path != "" {
		t.Errorf("expected ('', nil) for missing id, got (%q, %v)", path, err)
	}

	writeConversation(t, home, "conv-x", `{"step_index":0,"type":"USER_INPUT"}`)
	path, err := findTranscriptByID("conv-x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Errorf("expected to find transcript for conv-x")
	}
}

func TestPathWithin(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if !pathWithin(sub, root) {
		t.Errorf("expected %q within %q", sub, root)
	}
	if !pathWithin(root, root) {
		t.Errorf("a path should be within itself")
	}
	if pathWithin(root, sub) {
		t.Errorf("parent should not be within child")
	}
	if pathWithin("", root) || pathWithin(root, "") {
		t.Errorf("empty paths should never match")
	}
}
