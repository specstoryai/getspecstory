package qwencode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Package-level variables for mocking in tests.
var (
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	osGetwd       = os.Getwd
)

// QwenPathError describes actionable filesystem failures when locating Qwen data.
type QwenPathError struct {
	Kind    string // qwen_dir_missing, projects_missing, project_missing
	Path    string // offending path
	Message string // user-facing explanation
}

func (e *QwenPathError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// projectDirNameRegex matches any character that is not alphanumeric.
// Qwen Code replaces each such character with a dash to map a working
// directory to a project folder name under ~/.qwen/projects
// (sanitizeCwd in Qwen Code's storage layer).
var projectDirNameRegex = regexp.MustCompile(`[^a-zA-Z0-9]`)

// encodeProjectDirName converts a real (symlink-resolved) path into Qwen
// Code's project directory name: every non-alphanumeric character becomes a
// dash. On Windows the path is lowercased first, matching Qwen Code.
// Example: "/Users/jy/app" -> "-Users-jy-app".
func encodeProjectDirName(realPath string) string {
	if runtime.GOOS == "windows" {
		realPath = strings.ToLower(realPath)
	}
	return projectDirNameRegex.ReplaceAllString(realPath, "-")
}

// GetQwenDir returns the path to the Qwen data directory (~/.qwen).
func GetQwenDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}
	return filepath.Join(homeDir, ".qwen"), nil
}

// GetQwenProjectsDir returns ~/.qwen/projects, erroring when it does not
// exist yet (Qwen Code creates it on first session save).
func GetQwenProjectsDir() (string, error) {
	qwenDir, err := GetQwenDir()
	if err != nil {
		return "", err
	}

	projectsDir := filepath.Join(qwenDir, "projects")
	if _, err := osStat(projectsDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &QwenPathError{
				Kind:    "projects_missing",
				Path:    projectsDir,
				Message: fmt.Sprintf("Qwen projects directory %q not found. Run Qwen Code at least once so it is created.", projectsDir),
			}
		}
		return "", fmt.Errorf("error checking Qwen projects directory: %v", err)
	}

	return projectsDir, nil
}

// resolveQwenProjectDir returns ~/.qwen/projects/<encoded> for the given
// project path, WITHOUT requiring the directory to exist. Used when writing
// into the store; distinct from ResolveQwenProjectDir, which requires the
// directory to already exist.
//
// Unlike Claude Code, Qwen Code sanitizes the working directory verbatim
// (sanitizeCwd) without resolving symlinks, so the path is encoded as-is.
func resolveQwenProjectDir(projectPath string) (string, error) {
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return "", err
	}

	cwd := projectPath
	if cwd == "" {
		cwd, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %v", err)
		}
	}

	return filepath.Join(projectsDir, encodeProjectDirName(cwd)), nil
}

// ResolveQwenProjectDir locates the Qwen Code project directory on disk for
// the given project path, requiring it to exist.
func ResolveQwenProjectDir(projectPath string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %v", err)
		}
	}

	qwenDir, err := GetQwenDir()
	if err != nil {
		return "", err
	}
	if _, err := osStat(qwenDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &QwenPathError{
				Kind:    "qwen_dir_missing",
				Path:    qwenDir,
				Message: fmt.Sprintf("Qwen directory %q not found. Run Qwen Code at least once or verify it is installed.", qwenDir),
			}
		}
		return "", fmt.Errorf("failed to read Qwen directory %q: %w", qwenDir, err)
	}

	projectDir, err := resolveQwenProjectDir(projectPath)
	if err != nil {
		return "", err
	}

	slog.Debug("ResolveQwenProjectDir: Checking for Qwen project directory",
		"projectPath", projectPath,
		"projectDir", projectDir)

	if _, err := osStat(projectDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &QwenPathError{
				Kind:    "project_missing",
				Path:    projectDir,
				Message: fmt.Sprintf("No Qwen data found for this project (expected %q). Start a Qwen Code session in your repo to create it.", projectDir),
			}
		}
		return "", fmt.Errorf("failed to read Qwen project directory %q: %w", projectDir, err)
	}

	return projectDir, nil
}

// FindSessions scans a resolved Qwen project directory for chat transcripts
// and parses each one. Sessions are returned sorted by start time.
func FindSessions(projectDir string) ([]*QwenSession, error) {
	chatsDir := filepath.Join(projectDir, "chats")
	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*QwenSession{}, nil
		}
		return nil, fmt.Errorf("failed to read chats directory %q: %w", chatsDir, err)
	}

	var sessions []*QwenSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(chatsDir, entry.Name())
		session, parseErr := ParseSessionFile(filePath)
		if parseErr != nil {
			slog.Warn("FindSessions: Failed to parse session file, skipping",
				"file", filePath,
				"error", parseErr)
			continue
		}
		sessions = append(sessions, session)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].StartTime < sessions[j].StartTime
	})

	return sessions, nil
}

// ListQwenProjectDirs returns the names of every project directory under
// ~/.qwen/projects. Returns an empty slice when the projects directory does
// not exist yet.
func ListQwenProjectDirs() ([]string, error) {
	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		var pathErr *QwenPathError
		if errors.As(err, &pathErr) && pathErr.Kind == "projects_missing" {
			return []string{}, nil
		}
		return nil, err
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read Qwen projects directory: %w", err)
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	return dirs, nil
}

// transcriptPath returns the native transcript path for a session id within a
// resolved project directory: <projectDir>/chats/<sessionID>.jsonl.
func transcriptPath(projectDir string, sessionID string) string {
	return filepath.Join(projectDir, "chats", sessionID+".jsonl")
}
