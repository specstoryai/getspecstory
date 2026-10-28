// Package monitor implements the `specstory monitor` supervisor: it discovers
// git repositories under a root directory, watches the agent-side session
// storage roots (Claude Code, Codex CLI, Cursor CLI) for new activity, maps
// that activity back to a discovered repository, and spawns/reaps child
// `specstory watch` processes per active repository.
package monitor

import (
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// alwaysSkipDirs are directory names never worth descending into: they either
// can't contain the user's own repos (dependency trees) or are repo internals.
var alwaysSkipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	".git":         true,
}

// DiscoverRepos walks root (depth 0) up to maxDepth levels deep and returns
// the git repository roots it finds, sorted. A directory counts as a repo root
// when it contains a .git entry — directory OR file, because worktrees and
// submodules use a .git file pointing at the real git dir. Discovery does not
// descend into a repo (nested repos inside another repo are the outer repo's
// concern, handled by its own watch child). excludes are path globs matched
// with path.Match against the slash-separated path relative to root; matching
// directories and everything under them are skipped.
func DiscoverRepos(root string, maxDepth int, excludes []string) ([]string, error) {
	root = filepath.Clean(root)

	var repos []string
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable/missing root means there is nothing to discover at
			// all — that must surface. Deeper unreadable entries (permissions,
			// races) shouldn't abort discovery of everything else; log and move on.
			if p == root {
				return err
			}
			slog.Warn("DiscoverRepos: skipping unreadable entry", "path", p, "error", err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p == root {
			// The root itself may be a repo; check it like any other directory
			// but never exclude/skip it (depth 0 is always in range).
			if isRepoRoot(p) {
				repos = append(repos, p)
				return filepath.SkipDir
			}
			return nil
		}

		if alwaysSkipDirs[d.Name()] {
			return filepath.SkipDir
		}

		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			// Should be impossible since p is under root; skip defensively.
			return filepath.SkipDir
		}
		rel = filepath.ToSlash(rel)

		for _, glob := range excludes {
			// path.Match errors only on malformed patterns; a bad pattern
			// simply never matches (the CLI can't do anything smarter mid-walk).
			if matched, _ := path.Match(glob, rel); matched {
				return filepath.SkipDir
			}
		}

		// Depth of root is 0, so depth == number of separators in rel + 1.
		depth := strings.Count(rel, "/") + 1
		if depth > maxDepth {
			return filepath.SkipDir
		}

		if isRepoRoot(p) {
			repos = append(repos, p)
			// Don't descend into a discovered repo: its own watch child covers
			// everything inside it, including nested repos/worktrees.
			return filepath.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Strings(repos)
	return repos, nil
}

// isRepoRoot reports whether dir contains a .git entry. Both a .git directory
// (normal clone) and a .git file (worktree/submodule pointer) count.
func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
