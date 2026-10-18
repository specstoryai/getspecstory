package spi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Git origin helpers shared by project identity (pkg/utils) and by providers
// that match sessions to a project by repository rather than by directory. They
// live here because providers may not import pkg/utils, and a provider that
// normalized URLs its own way would attribute sessions to a different project
// than the git_id the rest of the CLI computes.

// originRemoteURLRegex captures the origin remote's url value from a git config file.
var originRemoteURLRegex = regexp.MustCompile(`\[remote "origin"\][^\[]*url\s*=\s*([^\n\r]+)`)

// NormalizeGitRemoteURL reduces a git remote URL to host/owner/repo so that the
// HTTPS, SSH and scheme-less spellings of one repository compare equal. It is
// the normalization the CLI hashes into a project's git_id.
func NormalizeGitRemoteURL(gitURL string) string {
	normalized := strings.TrimSuffix(gitURL, ".git")

	// scp-like SSH form (git@host:owner/repo or user@host:owner/repo) becomes host/path.
	if strings.Contains(normalized, "@") && strings.Contains(normalized, ":") {
		parts := strings.SplitN(normalized, "@", 2)
		if len(parts) == 2 {
			hostAndPath := strings.SplitN(parts[1], ":", 2)
			if len(hostAndPath) == 2 {
				return hostAndPath[0] + "/" + hostAndPath[1]
			}
		}
	}

	// Any scheme (https://, git://, ssh://) is dropped so only host/path remains.
	if _, rest, ok := strings.Cut(normalized, "://"); ok {
		return rest
	}

	// Already host/owner/repo.
	return normalized
}

// GitOriginURL returns the origin remote URL of the repository containing dir,
// walking up to the git root first so a subdirectory of a repository resolves
// to that repository. Returns "" when dir is not inside a repository or the
// repository has no origin remote.
func GitOriginURL(dir string) string {
	root, ok := FindGitRoot(dir)
	if !ok {
		return ""
	}
	configPath := gitConfigPath(root)
	if configPath == "" {
		return ""
	}
	return readOriginURL(configPath)
}

// FindGitRoot walks up from dir to the nearest ancestor containing a .git entry and
// returns it. The .git entry may be a directory (normal clone) or a file (git
// worktree or submodule); both satisfy the search. Returns false if no .git is
// found before the filesystem root.
func FindGitRoot(dir string) (string, bool) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// readOriginURL returns the origin remote URL from a git config file, or "" if the
// file is unreadable or has no origin remote.
func readOriginURL(gitConfigPath string) string {
	gitConfig, err := os.ReadFile(gitConfigPath)
	if err != nil {
		return ""
	}
	matches := originRemoteURLRegex.FindSubmatch(gitConfig)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(string(matches[1]))
}

// GitHeadBranch returns the branch checked out in the repository containing
// dir, or "" when HEAD is detached, unreadable, or dir is not in a repository.
func GitHeadBranch(dir string) string {
	root, ok := FindGitRoot(dir)
	if !ok {
		return ""
	}
	// HEAD is per worktree, so it lives in the private dir, not the common one.
	private, _ := gitDirs(root)
	if private == "" {
		return ""
	}
	head, err := os.ReadFile(filepath.Join(private, "HEAD"))
	if err != nil {
		return ""
	}
	// A detached HEAD holds a commit id rather than a branch ref.
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return branch
}

// gitConfigPath returns the path to the git config file that holds a root's
// remotes, or "" when it cannot be resolved. The config is shared by every
// worktree, so it lives in the common dir.
func gitConfigPath(gitRoot string) string {
	_, common := gitDirs(gitRoot)
	if common == "" {
		return ""
	}
	return filepath.Join(common, "config")
}

// gitDirs returns a root's private git dir, which holds per-worktree state such
// as HEAD, and its common dir, which holds the shared config. They are the same
// .git directory in a normal clone; a .git file (worktrees and submodules)
// points at a private gitdir whose commondir names the shared one. Both are ""
// when they cannot be resolved.
func gitDirs(gitRoot string) (private, common string) {
	gitPath := filepath.Join(gitRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", ""
	}
	if info.IsDir() {
		return gitPath, gitPath
	}

	// A .git file reads "gitdir: <path>" and points at the private git dir.
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", ""
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir:") {
		return "", ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(content, "gitdir:"))
	if gitDir == "" {
		return "", ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(gitRoot, gitDir)
	}
	commonDir := gitDir
	if cd, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(cd))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		commonDir = filepath.Clean(c)
	}
	return gitDir, commonDir
}
