package codexcli

import (
	"os"
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

// SessionsRoot returns the directory Codex CLI stores session files under,
// honoring CODEX_HOME the same way session enumeration does. Exposed so the
// monitor command can watch the same storage root this provider reads from.
func SessionsRoot() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return codexSessionsRoot(homeDir), nil
}

// SessionOriginCwd returns the originating working directory recorded in a
// Codex session file's session_meta header (its first JSONL line). Exposed so
// the monitor's activity resolver can map a new session file back to a project
// directory without re-implementing Codex's session-meta parsing.
func SessionOriginCwd(sessionPath string) (string, error) {
	meta, err := loadCodexSessionMeta(sessionPath)
	if err != nil {
		return "", err
	}
	return meta.Payload.CWD, nil
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
