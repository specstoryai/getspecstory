package spi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeGitRemoteURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"https", "https://github.com/specstoryai/cloud-agent-test", "github.com/specstoryai/cloud-agent-test"},
		{"https with .git", "https://github.com/specstoryai/cloud-agent-test.git", "github.com/specstoryai/cloud-agent-test"},
		{"scp-like ssh", "git@github.com:specstoryai/cloud-agent-test.git", "github.com/specstoryai/cloud-agent-test"},
		{"scp-like ssh without .git", "git@github.com:user/repo", "github.com/user/repo"},
		{"scp-like ssh with a custom user", "customuser@github.com:owner/repo.git", "github.com/owner/repo"},
		{"ssh scheme", "ssh://github.com/specstoryai/cloud-agent-test.git", "github.com/specstoryai/cloud-agent-test"},
		{"git scheme", "git://github.com/user/repo.git", "github.com/user/repo"},
		{"git+ssh scheme", "git+ssh://github.com/user/repo.git", "github.com/user/repo"},
		{"scheme-less as the Cursor v0 API records it", "github.com/specstoryai/cloud-agent-test", "github.com/specstoryai/cloud-agent-test"},
		{"GitLab https", "https://gitlab.com/patterns-ai-core/langchainrb.git", "gitlab.com/patterns-ai-core/langchainrb"},
		{"GitLab ssh", "git@gitlab.com:patterns-ai-core/langchainrb.git", "gitlab.com/patterns-ai-core/langchainrb"},
		{"Bitbucket https", "https://bitbucket.org/myteam/myrepo.git", "bitbucket.org/myteam/myrepo"},
		{"Bitbucket ssh", "git@bitbucket.org:myteam/myrepo.git", "bitbucket.org/myteam/myrepo"},
		{"custom server https", "https://git.company.com/project/repo.git", "git.company.com/project/repo"},
		{"custom server ssh", "git@git.company.com:project/repo.git", "git.company.com/project/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeGitRemoteURL(tt.url); got != tt.want {
				t.Errorf("NormalizeGitRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
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

func TestGitHeadBranch(t *testing.T) {
	tests := []struct {
		name string
		head string
		want string
	}{
		{name: "branch", head: "ref: refs/heads/main\n", want: "main"},
		{name: "branch with slashes", head: "ref: refs/heads/feature/cloud-run\n", want: "feature/cloud-run"},
		{name: "detached", head: "3f1c2b9e0d4a5f6e7d8c9b0a1f2e3d4c5b6a7f8e\n", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeGitConfig(t, t.TempDir(), "")
			if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte(tt.head), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := GitHeadBranch(root); got != tt.want {
				t.Errorf("GitHeadBranch = %q, want %q", got, tt.want)
			}
		})
	}

	// A worktree's HEAD is its own, in the private gitdir, not the main
	// checkout's HEAD in the common dir.
	t.Run("worktree reads its private HEAD", func(t *testing.T) {
		main := writeGitConfig(t, filepath.Join(t.TempDir(), "main"), "")
		if err := os.WriteFile(filepath.Join(main, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		private := filepath.Join(main, ".git", "worktrees", "wt")
		if err := os.MkdirAll(private, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(private, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(private, "HEAD"), []byte("ref: refs/heads/wt-branch\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		worktree := filepath.Join(t.TempDir(), "wt")
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+private+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := GitHeadBranch(worktree); got != "wt-branch" {
			t.Errorf("GitHeadBranch = %q, want %q", got, "wt-branch")
		}
	})

	t.Run("not a repository", func(t *testing.T) {
		if got := GitHeadBranch(t.TempDir()); got != "" {
			t.Errorf("GitHeadBranch = %q, want empty", got)
		}
	})
}
