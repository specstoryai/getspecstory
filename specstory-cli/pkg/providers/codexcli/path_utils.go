package codexcli

import (
	"os"
	slashpath "path"
	"path/filepath"
	"sort"
	"strings"
)

// codexSessionsRoot returns the root directory where Codex stores session files.
func codexSessionsRoot(homeDir string) string {
	// Codex CLI honors CODEX_HOME as its base directory (default ~/.codex), so
	// read sessions from the same place rather than assuming the default.
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		return filepath.Join(codexHome, "sessions")
	}
	return filepath.Join(homeDir, ".codex", "sessions")
}

// normalizeCodexPath resolves a path to an absolute, cleaned representation suitable for comparisons.
func normalizeCodexPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}

	// A rooted path that is not absolute by this host's rules is a foreign
	// recorded path (a Unix cwd read on a Windows host): normalize it as a
	// string — filepath.Abs would staple the current drive letter on and
	// EvalSymlinks would consult the wrong filesystem.
	if !filepath.IsAbs(path) && strings.HasPrefix(path, "/") {
		return slashpath.Clean(path)
	}

	cleaned := filepath.Clean(path)

	absPath, err := filepath.Abs(cleaned)
	if err == nil {
		cleaned = absPath
	}

	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = resolved
	}

	return filepath.Clean(cleaned)
}

// readDirSortedDesc reads directory entries and sorts them descending by name.
func readDirSortedDesc(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() > entries[j].Name()
	})

	return entries, nil
}
