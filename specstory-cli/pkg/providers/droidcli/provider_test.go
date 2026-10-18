package droidcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestDebugRawRefreshPreservesNativeRecordsAndUnownedFiles(t *testing.T) {
	testutil.IsolateDebugDir(t)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := `{"type":"session_start","id":"debug-refresh"}`
	future := `{"type":"future","z":9007199254740993,"a":1.234567890123456789}`
	if err := os.WriteFile(path, []byte(header+"\n{broken\n"+future+"\n"+future+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := parseFactorySession(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFactoryDebugRaw(session); err != nil {
		t.Fatal(err)
	}
	dir := spi.GetDebugDir(session.ID)
	testutil.AssertDebugRefresh(t, dir, []string{"3.json"},
		[]string{"session-data.json", "notes.md", "nested/notes.md"}, func() {
			session.RawData = header + "\n" + future + "\n"
			if err := writeFactoryDebugRaw(session); err != nil {
				t.Fatal(err)
			}
		})
	data, err := os.ReadFile(filepath.Join(dir, "2.json"))
	if err != nil || !strings.Contains(string(data), "\n  \"z\": 9007199254740993") {
		t.Fatalf("native record not pretty-printed: %s (%v)", data, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil || compact.String() != future {
		t.Fatalf("native order or precision changed: %s (%v)", data, err)
	}
}

func TestSessionMentionsProject(t *testing.T) {
	tests := []struct {
		name        string
		makeContent func(projectDir, otherDir string) string
		expected    bool
	}{
		{
			name: "returns true when session cwd matches project",
			makeContent: func(projectDir, _ string) string {
				return fmt.Sprintf(`{"type":"session_start","id":"sess-1","cwd":%q}`, projectDir)
			},
			expected: true,
		},
		{
			name: "returns false when cwd mismatches even if text mentions project",
			makeContent: func(projectDir, otherDir string) string {
				return fmt.Sprintf(`{"type":"session_start","id":"sess-1","cwd":%q}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, otherDir, projectDir)
			},
			expected: false,
		},
		{
			name: "falls back to text search when no cwd present",
			makeContent: func(projectDir, _ string) string {
				return fmt.Sprintf(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, projectDir)
			},
			expected: true,
		},
		{
			name: "returns false when no cwd and text does not mention project",
			makeContent: func(_, otherDir string) string {
				return fmt.Sprintf(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":%q}]}}`, otherDir)
			},
			expected: false,
		},
		{
			name: "returns true when workspace_root matches project",
			makeContent: func(projectDir, _ string) string {
				return fmt.Sprintf(`{"type":"session_start","id":"sess-1","workspace_root":%q}`, projectDir)
			},
			expected: true,
		},
		{
			name: "returns true when basename appears more than twice in text",
			makeContent: func(projectDir, _ string) string {
				// No cwd/workspace_root, so falls back to text search.
				// Basename threshold requires >2 occurrences.
				basename := filepath.Base(projectDir)
				return fmt.Sprintf(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"working on %s"}]}}
{"type":"message","message":{"role":"user","content":[{"type":"text","text":"still in %s"}]}}
{"type":"message","message":{"role":"agent","content":[{"type":"text","text":"found %s"}]}}`, basename, basename, basename)
			},
			expected: true,
		},
		{
			name: "returns false when basename appears only twice in text",
			makeContent: func(projectDir, _ string) string {
				// No cwd/workspace_root, so falls back to text search.
				// Basename threshold requires >2 occurrences, so 2 is not enough.
				basename := filepath.Base(projectDir)
				return fmt.Sprintf(`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"working on %s"}]}}
{"type":"message","message":{"role":"agent","content":[{"type":"text","text":"found %s"}]}}`, basename, basename)
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			otherDir := t.TempDir()
			sessionPath := filepath.Join(t.TempDir(), "session.jsonl")

			content := tt.makeContent(projectDir, otherDir)
			if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}

			got := sessionMentionsProject(sessionPath, projectDir)
			if got != tt.expected {
				t.Errorf("sessionMentionsProject() = %v, want %v", got, tt.expected)
			}
		})
	}
}
