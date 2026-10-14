package spi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeGitRemoteURL(t *testing.T) {
	const want = "github.com/specstoryai/cloud-agent-test"
	tests := []struct {
		name string
		url  string
	}{
		{"https", "https://github.com/specstoryai/cloud-agent-test"},
		{"https with .git", "https://github.com/specstoryai/cloud-agent-test.git"},
		{"scp-like ssh", "git@github.com:specstoryai/cloud-agent-test.git"},
		{"ssh scheme", "ssh://github.com/specstoryai/cloud-agent-test.git"},
		{"scheme-less as the Cursor v0 API records it", "github.com/specstoryai/cloud-agent-test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeGitRemoteURL(tt.url); got != want {
				t.Errorf("NormalizeGitRemoteURL(%q) = %q, want %q", tt.url, got, want)
			}
		})
	}
}

// writeGitConfig creates dir/.git/config naming origin, returning dir.
func writeGitConfig(t *testing.T, dir, origin string) string {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[core]\n\tbare = false\n"
	if origin != "" {
		config += "[remote \"origin\"]\n\turl = " + origin + "\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGitOriginURL(t *testing.T) {
	const origin = "git@github.com:specstoryai/cloud-agent-test.git"

	t.Run("repository root", func(t *testing.T) {
		root := writeGitConfig(t, t.TempDir(), origin)
		if got := GitOriginURL(root); got != origin {
			t.Errorf("GitOriginURL = %q, want %q", got, origin)
		}
	})

	t.Run("subdirectory walks up to the root", func(t *testing.T) {
		root := writeGitConfig(t, t.TempDir(), origin)
		sub := filepath.Join(root, "a", "b")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := GitOriginURL(sub); got != origin {
			t.Errorf("GitOriginURL = %q, want %q", got, origin)
		}
	})

	t.Run("worktree .git file follows commondir", func(t *testing.T) {
		main := writeGitConfig(t, filepath.Join(t.TempDir(), "main"), origin)
		private := filepath.Join(main, ".git", "worktrees", "wt")
		if err := os.MkdirAll(private, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(private, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		worktree := filepath.Join(t.TempDir(), "wt")
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+private+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := GitOriginURL(worktree); got != origin {
			t.Errorf("GitOriginURL = %q, want %q", got, origin)
		}
	})

	t.Run("no origin remote", func(t *testing.T) {
		root := writeGitConfig(t, t.TempDir(), "")
		if got := GitOriginURL(root); got != "" {
			t.Errorf("GitOriginURL = %q, want empty", got)
		}
	})
}
