package qwencode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-level variables for mocking in tests
var (
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	osGetwd       = os.Getwd
)

// QwenPathError describes actionable filesystem failures when locating Qwen Code data.
type QwenPathError struct {
	Kind       string   // projects_missing, project_missing
	Path       string   // offending path
	ProjectDir string   // sanitized directory name when relevant
	KnownDirs  []string // project directories discovered on disk (optional)
	Message    string   // user-facing explanation
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
		return projectPath, nil
	}
	cwd, err := osGetwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %v", err)
	}
	return cwd, nil
}

// SanitizeQwenCwd converts a working directory path into Qwen Code's project
// directory name: every character outside [a-zA-Z0-9] becomes '-'.
// Example: /Users/alice/my app -> -Users-alice-my-app
// This mirrors Qwen Code's own sanitizeCwd, which applies the replacement to
// the process cwd verbatim (no symlink resolution).
func SanitizeQwenCwd(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// GetQwenProjectsDir returns the path to the Qwen Code projects directory (~/.qwen/projects)
func GetQwenProjectsDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}
	return filepath.Join(homeDir, ".qwen", "projects"), nil
}

// candidateProjectDirNames returns the sanitized directory names to try for a
// project path, most likely first. Qwen sanitizes the process cwd verbatim, so
// the canonical path (correct case, symlinks resolved) is the usual match; the
// raw absolute path is kept as a fallback in case the session was started from
// a differently-spelled path to the same directory.
func candidateProjectDirNames(projectPath string) []string {
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		absPath = projectPath
	}

	var names []string
	if canonical, err := spi.GetCanonicalPath(absPath); err == nil {
		names = append(names, SanitizeQwenCwd(canonical))
	}
	sanitizedAbs := SanitizeQwenCwd(absPath)
	if len(names) == 0 || names[0] != sanitizedAbs {
		names = append(names, sanitizedAbs)
	}
	return names
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

	if _, err := osStat(projectsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("ResolveQwenProjectDir: Qwen projects directory not found", "projectsDir", projectsDir)
			return "", &QwenPathError{
				Kind:    "projects_missing",
				Path:    projectsDir,
				Message: fmt.Sprintf("Qwen Code projects directory %q not found. Run the Qwen Code CLI at least once or verify ~/.qwen exists.", projectsDir),
			}
		}
		return "", fmt.Errorf("failed to read Qwen projects directory %q: %w", projectsDir, err)
	}

	candidates := candidateProjectDirNames(projectPath)
	for _, name := range candidates {
		dir := filepath.Join(projectsDir, name)
		if info, err := osStat(dir); err == nil && info.IsDir() {
			slog.Debug("ResolveQwenProjectDir: Resolved Qwen project directory",
				"projectPath", projectPath, "dir", dir)
			return dir, nil
		}
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

	expected := filepath.Join(projectsDir, candidates[0])
	slog.Warn("ResolveQwenProjectDir: Qwen project directory not found",
		"expected", expected, "knownDirs", known)

	return "", &QwenPathError{
		Kind:       "project_missing",
		Path:       expected,
		ProjectDir: candidates[0],
		KnownDirs:  knownDirs,
		Message: fmt.Sprintf("No Qwen Code data found for this project (expected %q). Known project directories: %s. Start a Qwen Code session in your repo to create it.",
			expected, known),
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
