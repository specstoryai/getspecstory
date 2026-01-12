package codexcli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// codexSessionsRoot returns the root directory where Codex stores session files.
func codexSessionsRoot(homeDir string) string {
	return filepath.Join(homeDir, ".codex", "sessions")
}

// normalizeCodexPath resolves a path to an absolute, cleaned representation suitable for comparisons.
func normalizeCodexPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
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
