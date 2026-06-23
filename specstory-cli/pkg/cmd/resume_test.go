package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShortID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{"1234567890abc", "1234567890abc"}, // <= 13 chars: unchanged
		{"1234567890abcdef", "12345...bcdef"},
	}
	for _, c := range cases {
		if got := shortID(c.in); got != c.want {
			t.Errorf("shortID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteReconstructedSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "session.jsonl")
	content := []byte(`{"type":"user"}` + "\n")

	if err := writeReconstructedSession(path, content); err != nil {
		t.Fatalf("writeReconstructedSession: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	// The atomic-write temp file must not be left behind in the target directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.jsonl" {
			t.Errorf("unexpected leftover file in target dir: %q", e.Name())
		}
	}
}

func TestSessionFileReadable(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.jsonl")
	if ok, _ := sessionFileReadable(missing); ok {
		t.Error("missing file reported readable")
	}

	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := sessionFileReadable(empty); ok {
		t.Error("empty file reported readable")
	}

	full := filepath.Join(dir, "full.jsonl")
	if err := os.WriteFile(full, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := sessionFileReadable(full); !ok {
		t.Errorf("file with content reported not readable: %v", err)
	}
}

func TestWaitForSessionFileVisible(t *testing.T) {
	dir := t.TempDir()

	present := filepath.Join(dir, "present.jsonl")
	if err := os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := waitForSessionFileVisible(present, time.Second); err != nil {
		t.Errorf("expected an already-present file to be visible: %v", err)
	}

	// A file that never appears must time out with a diagnostic error rather than block forever.
	if err := waitForSessionFileVisible(filepath.Join(dir, "never.jsonl"), 100*time.Millisecond); err == nil {
		t.Error("expected timeout error for a file that never becomes visible")
	}
}
