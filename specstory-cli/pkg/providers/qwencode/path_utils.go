package qwencode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
)

// QwenPathError describes actionable filesystem failures when locating Qwen Code data.
type QwenPathError struct {
	Kind      string   // projects_missing, project_missing
	Path      string   // offending path
	KnownDirs []string // project directories discovered on disk (optional)
	Message   string   // user-facing explanation
}

func (e *QwenPathError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// defaultProjectPath fills in an empty project path with the current working
// directory. Provider entry points call this once so the same resolved path
// is used both for locating the Qwen store and as the workspace root stamped
// into generated session data (stamping the raw empty string would fail
// schema validation and break path hint normalization).
func defaultProjectPath(projectPath string) (string, error) {
	if projectPath != "" {
		return spi.CanonicalizePathOrClean(projectPath), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}
	return spi.CanonicalizePathOrClean(cwd), nil
}

// SanitizeQwenCwd converts a working directory path into Qwen Code's project
// directory name: every character outside [a-zA-Z0-9] becomes '-'.
// Example: /Users/alice/my app -> -Users-alice-my-app
// This mirrors Qwen Code's sanitizeCwd: Windows lowercases first, then the
// JavaScript regexp replaces each non-alphanumeric UTF-16 code unit.
func SanitizeQwenCwd(path string) string {
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	var b strings.Builder
	b.Grow(len(path))
	// JavaScript's non-Unicode regexp replaces UTF-16 code units, including
	// both halves of an astral character such as an emoji.
	for _, r := range utf16.Encode([]rune(path)) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteByte(byte(r))
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// GetQwenProjectsDir returns the path to the Qwen Code projects directory (~/.qwen/projects)
func GetQwenProjectsDir() (string, error) {
	base := os.Getenv("QWEN_RUNTIME_DIR")
	if base == "" {
		base = os.Getenv("QWEN_HOME")
	}
	if base == "" {
		base = "~/.qwen"
	}
	base, err := resolveQwenStoragePath(base)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "projects"), nil
}

// resolveQwenStoragePath mirrors the vendor's tilde and relative-path
// expansion. Freeze overrides before cmd.Dir changes the child's cwd.
func resolveQwenStoragePath(base string) (string, error) {
	if base == "~" || strings.HasPrefix(base, "~/") || strings.HasPrefix(base, `~\`) {
		home, err := osUserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get Qwen home: %w", err)
		}
		if base == "~" {
			base = home
		} else {
			base = filepath.Join(home, filepath.FromSlash(strings.ReplaceAll(base[2:], `\`, "/")))
		}
	}
	path, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve Qwen storage root: %w", err)
	}
	return path, nil
}

// ResolveQwenProjectDir locates the Qwen Code project directory
// (~/.qwen/projects/<sanitized-cwd>) for the given project path.
func ResolveQwenProjectDir(projectPath string) (string, error) {
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return "", err
	}

	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(projectsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Debug("ResolveQwenProjectDir: Qwen projects directory not found", "projectsDir", projectsDir)
			return "", &QwenPathError{
				Kind:    "projects_missing",
				Path:    projectsDir,
				Message: fmt.Sprintf("Qwen Code projects directory %q not found. Run the Qwen Code CLI at least once or verify ~/.qwen exists.", projectsDir),
			}
		}
		return "", fmt.Errorf("failed to read Qwen projects directory %q: %w", projectsDir, err)
	}

	name := SanitizeQwenCwd(projectPath)
	dir := filepath.Join(projectsDir, name)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		slog.Debug("ResolveQwenProjectDir: Resolved Qwen project directory", "projectPath", projectPath, "dir", dir)
		return dir, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read Qwen project directory %q: %w", dir, err)
	}

	knownDirs, listErr := ListQwenProjectDirs()
	if listErr != nil {
		knownDirs = nil
	}

	var known string
	if len(knownDirs) > 0 {
		known = strings.Join(knownDirs, ", ")
	} else {
		known = "(none discovered)"
	}

	slog.Debug("ResolveQwenProjectDir: Qwen project directory not found",
		"expected", dir, "knownDirs", known)

	return "", &QwenPathError{
		Kind:      "project_missing",
		Path:      dir,
		KnownDirs: knownDirs,
		Message: fmt.Sprintf("No Qwen Code data found for this project (expected %q). Known project directories: %s. Start a Qwen Code session in your repo to create it.",
			dir, known),
	}
}

// ListQwenProjectDirs returns the sanitized project directory names currently on disk.
func ListQwenProjectDirs() ([]string, error) {
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read Qwen projects directory %q: %w", projectsDir, err)
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}

	sort.Strings(dirs)
	return dirs, nil
}

// sessionBelongsToProject checks the recorded cwd because the sanitized store
// key is lossy (/a-b and /a_b share a directory). Unknown origins remain
// readable in their project directory; never infer an origin from tool paths.
func sessionBelongsToProject(session *QwenSession, projectPath string) bool {
	if session.Cwd == "" {
		return true
	}
	cwd := session.Cwd
	// Qwen explicitly keeps worktree sessions associated with their parent
	// project. Match only its reserved .qwen/worktrees layout, not any subdir.
	normalized := strings.ReplaceAll(cwd, `\`, "/")
	if i := strings.LastIndex(normalized, "/.qwen/worktrees/"); i >= 0 {
		cwd = cwd[:i]
	}
	if filepath.Clean(cwd) == filepath.Clean(projectPath) {
		return true
	}
	// SameFile handles local symlinks and case aliases without rewriting a
	// recorded cwd, which may instead refer to a case-sensitive remote system.
	recorded, err := os.Stat(cwd)
	if err != nil {
		return false
	}
	project, err := os.Stat(projectPath)
	return err == nil && os.SameFile(recorded, project)
}

// validSessionFilename rejects separators from either OS before joining a
// caller-supplied id or reconstructed filename to the native store.
func validSessionFilename(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\:`) && !strings.ContainsRune(name, 0)
}

// isSessionFile excludes ledger sidecars that also end in .jsonl. Native
// transcripts have a single extension: <session-id>.jsonl.
func isSessionFile(name string) bool {
	return strings.HasSuffix(name, ".jsonl") && !strings.Contains(strings.TrimSuffix(name, ".jsonl"), ".")
}

// isProjectTranscriptPath reports whether path is a transcript at the one depth
// Qwen writes them, <projectsDir>/<sanitized-cwd>/chats/<session-id>.jsonl.
// Enforcing the exact shape keeps a store nested inside a project directory,
// such as a checked-out copy of someone else's ~/.qwen, out of enumeration.
func isProjectTranscriptPath(path string, projectsDir string) bool {
	if !isSessionFile(filepath.Base(path)) {
		return false
	}
	chatsDir := filepath.Dir(path)
	projectDir := filepath.Dir(chatsDir)
	return filepath.Base(chatsDir) == "chats" && filepath.Dir(projectDir) == projectsDir
}
