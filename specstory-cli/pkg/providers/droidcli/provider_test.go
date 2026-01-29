package droidcli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
