package monitor

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// mkGitRepo creates dir with a .git directory (gitFile=false) or a .git file
// (gitFile=true, the worktree/submodule shape).
func mkGitRepo(t *testing.T, dir string, gitFile bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create repo dir %s: %v", dir, err)
	}
	gitPath := filepath.Join(dir, ".git")
	if gitFile {
		if err := os.WriteFile(gitPath, []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o644); err != nil {
			t.Fatalf("failed to create .git file: %v", err)
		}
	} else {
		if err := os.MkdirAll(gitPath, 0o755); err != nil {
			t.Fatalf("failed to create .git dir: %v", err)
		}
	}
}

func TestDiscoverRepos(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, root string)
		maxDepth int
		excludes []string
		want     []string // relative to root; "." means the root itself
	}{
		{
			name: "repos at various depths",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "a"), false)
				mkGitRepo(t, filepath.Join(root, "group", "b"), false)
				mkGitRepo(t, filepath.Join(root, "x", "y", "z", "c"), false)
			},
			maxDepth: 4,
			want:     []string{"a", "group/b", "x/y/z/c"},
		},
		{
			name: "git file counts as repo (worktree)",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "worktree"), true)
			},
			maxDepth: 4,
			want:     []string{"worktree"},
		},
		{
			name: "root itself is a repo",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, root, false)
				// A nested repo must not be reported: discovery stops at the root repo.
				mkGitRepo(t, filepath.Join(root, "inner"), false)
			},
			maxDepth: 4,
			want:     []string{"."},
		},
		{
			name: "does not descend into repos",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "outer"), false)
				mkGitRepo(t, filepath.Join(root, "outer", "nested"), false)
			},
			maxDepth: 4,
			want:     []string{"outer"},
		},
		{
			name: "too deep is skipped",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "shallow"), false)
				mkGitRepo(t, filepath.Join(root, "l1", "l2", "deep"), false)
			},
			maxDepth: 2,
			want:     []string{"shallow"},
		},
		{
			name: "repo at exactly maxDepth is found",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "l1", "edge"), false)
			},
			maxDepth: 2,
			want:     []string{"l1/edge"},
		},
		{
			name: "node_modules and vendor are always skipped",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "node_modules", "dep"), false)
				mkGitRepo(t, filepath.Join(root, "vendor", "dep"), false)
				mkGitRepo(t, filepath.Join(root, "real"), false)
			},
			maxDepth: 4,
			want:     []string{"real"},
		},
		{
			name: "excluded glob is skipped",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "archive", "old"), false)
				mkGitRepo(t, filepath.Join(root, "scratch"), false)
				mkGitRepo(t, filepath.Join(root, "keep"), false)
			},
			maxDepth: 4,
			excludes: []string{"archive/*", "scratch"},
			want:     []string{"keep"},
		},
		{
			name: "exclude of parent skips everything under it",
			setup: func(t *testing.T, root string) {
				mkGitRepo(t, filepath.Join(root, "skipme", "a", "b"), false)
				mkGitRepo(t, filepath.Join(root, "keep"), false)
			},
			maxDepth: 4,
			excludes: []string{"skipme"},
			want:     []string{"keep"},
		},
		{
			name:     "empty tree finds nothing",
			setup:    func(t *testing.T, root string) {},
			maxDepth: 4,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)

			got, err := DiscoverRepos(root, tt.maxDepth, tt.excludes)
			if err != nil {
				t.Fatalf("DiscoverRepos() error = %v", err)
			}

			var want []string
			for _, rel := range tt.want {
				if rel == "." {
					want = append(want, filepath.Clean(root))
				} else {
					want = append(want, filepath.Join(root, rel))
				}
			}
			sort.Strings(want)

			if !reflect.DeepEqual(got, want) {
				t.Errorf("DiscoverRepos() = %v, want %v", got, want)
			}
		})
	}
}

func TestDiscoverRepos_MissingRoot(t *testing.T) {
	_, err := DiscoverRepos(filepath.Join(t.TempDir(), "does-not-exist"), 4, nil)
	if err == nil {
		t.Error("DiscoverRepos() on a missing root: expected error, got nil")
	}
}
