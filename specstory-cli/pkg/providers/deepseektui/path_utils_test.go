package deepseektui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractWorkspaceFast covers the streaming workspace extraction. The
// function must:
//  1. Return the workspace value when it appears in metadata before "messages".
//  2. NOT pick up "workspace" strings that appear inside tool_result content
//     after the "messages" key has been seen — those are unrelated noise.
//  3. Return "" when no workspace is present at all.
//  4. Handle very large lines (the buffer was raised to 1MB for tool blobs).
func TestExtractWorkspaceFast(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "workspace before messages key",
			content: `{
  "schema_version": 1,
  "metadata": {
    "id": "sess-1",
    "workspace": "/Users/me/proj"
  },
  "messages": []
}`,
			want: "/Users/me/proj",
		},
		{
			name: "workspace key only inside tool_result after messages is ignored",
			content: `{
  "metadata": {"id": "sess-1"},
  "messages": [
    {"role": "user", "content": [{"type": "tool_result", "content": "this string mentions \"workspace\": \"/fake\""}]}
  ]
}`,
			want: "",
		},
		{
			name: "no workspace key at all",
			content: `{
  "metadata": {"id": "sess-2"},
  "messages": []
}`,
			want: "",
		},
		{
			name: "workspace on same line as messages key still found if it appears first",
			// matchWorkspace runs before the messages bail-out, so a single-line
			// JSON with workspace before messages should still resolve.
			content: `{"metadata":{"workspace":"/single/line/path"},"messages":[]}`,
			want:    "/single/line/path",
		},
		{
			name:    "workspace-like text after messages on same line is ignored",
			content: `{"metadata":{"id":"sess-1"},"messages":[{"role":"user","content":[{"type":"tool_result","content":"this string mentions \"workspace\": \"/fake\""}]}]}`,
			want:    "",
		},
		{
			name: "empty workspace value yields empty result",
			content: `{
  "metadata": {"workspace": ""},
  "messages": []
}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			got := extractWorkspaceFast(path)
			if got != tt.want {
				t.Errorf("extractWorkspaceFast() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExtractWorkspaceFast_LargeLine confirms the scanner's 1MB buffer handles
// tool_result blobs that exceed bufio.Scanner's default 64KB line cap. Without
// the raise, the scanner would return an error and the workspace (which sits
// before the giant line) would have been read fine — but if the workspace
// itself were on a long metadata line we'd lose it. Construct a case where the
// workspace lives on a long line to exercise the buffer.
func TestExtractWorkspaceFast_LargeLine(t *testing.T) {
	// Build a metadata line that's ~200KB long but still valid JSON parseable
	// by our quick matcher (we only scan textually).
	bigComment := strings.Repeat("x", 200*1024)
	content := `{"metadata":{"note":"` + bigComment + `","workspace":"/big/line/path"},"messages":[]}`

	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	got := extractWorkspaceFast(path)
	if got != "/big/line/path" {
		t.Errorf("extractWorkspaceFast() = %q, want /big/line/path", got)
	}
}

// TestExtractWorkspaceFast_MissingFile returns empty string instead of erroring.
func TestExtractWorkspaceFast_MissingFile(t *testing.T) {
	got := extractWorkspaceFast(filepath.Join(t.TempDir(), "no-such-file.json"))
	if got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

func TestSessionMentionsProject(t *testing.T) {
	tests := []struct {
		name        string
		makeContent func(projectDir, otherDir string) string
		projectDir  func(projectDir, otherDir string) string
		want        bool
	}{
		{
			name: "matches when workspace equals project",
			makeContent: func(p, _ string) string {
				return `{"metadata":{"workspace":"` + p + `"},"messages":[]}`
			},
			projectDir: func(p, _ string) string { return p },
			want:       true,
		},
		{
			name: "no match when workspace differs",
			makeContent: func(_, o string) string {
				return `{"metadata":{"workspace":"` + o + `"},"messages":[]}`
			},
			projectDir: func(p, _ string) string { return p },
			want:       false,
		},
		{
			name: "no match when no workspace key at all",
			makeContent: func(_, _ string) string {
				return `{"metadata":{"id":"x"},"messages":[]}`
			},
			projectDir: func(p, _ string) string { return p },
			want:       false,
		},
		{
			name: "empty project path returns false",
			makeContent: func(p, _ string) string {
				return `{"metadata":{"workspace":"` + p + `"},"messages":[]}`
			},
			projectDir: func(_, _ string) string { return "   " },
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			otherDir := t.TempDir()
			path := filepath.Join(t.TempDir(), "session.json")
			if err := os.WriteFile(path, []byte(tt.makeContent(projectDir, otherDir)), 0o644); err != nil {
				t.Fatalf("write file: %v", err)
			}
			got := sessionMentionsProject(path, tt.projectDir(projectDir, otherDir))
			if got != tt.want {
				t.Errorf("sessionMentionsProject() = %v, want %v", got, tt.want)
			}
		})
	}
}
