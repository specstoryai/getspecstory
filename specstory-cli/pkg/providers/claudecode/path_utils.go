package claudecode

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
)

// projectDirNameRegex matches any character that is not alphanumeric or a dash.
// Claude Code replaces each such character with a dash to map a working directory
// to a project folder name under ~/.claude/projects.
var projectDirNameRegex = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// encodeProjectDirName converts a real (symlink-resolved) path into Claude Code's
// project directory name: non-alphanumeric/dash characters become dashes.
// On Unix: "/Users/sean/app" -> "-Users-sean-app".
// On Windows: "C:\Users\Admin\code\app" -> "C--Users-Admin-code-app" (no leading
// dash — Claude Code does not add one on Windows).
func encodeProjectDirName(realPath string) string {
	return projectDirNameRegex.ReplaceAllString(realPath, "-")
}

// resolveClaudeProjectDir returns ~/.claude/projects/<encoded> for the given
// project path, resolving symlinks, WITHOUT requiring the directory to exist.
// Used when writing a reconstructed session into the store (the caller creates
// the directory). Distinct from GetClaudeCodeProjectDir, which requires the
// projects directory to already exist.
func resolveClaudeProjectDir(projectPath string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}

	cwd := projectPath
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %v", err)
		}
	}

	// Resolve symlinks to match Claude Code's behavior; fall back to the raw path
	// if resolution fails (e.g., a path component does not exist yet).
	realPath, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		realPath = cwd
	}

	return filepath.Join(homeDir, ".claude", "projects", encodeProjectDirName(realPath)), nil
}

// GetClaudeCodeProjectsDir returns the path to the Claude Code projects directory
func GetClaudeCodeProjectsDir() (string, error) {
	// Get user's home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Construct the path to .claude/projects
	projectsDir := filepath.Join(homeDir, ".claude", "projects")

	// Check if the directory exists
	if _, err := os.Stat(projectsDir); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("claude projects directory not found at %s", projectsDir)
		}
		return "", fmt.Errorf("error checking Claude projects directory: %v", err)
	}

	return projectsDir, nil
}

// GetClaudeCodeProjectDir returns the Claude Code project directory for the given path.
// If no path is provided (empty string), it uses the current working directory.
// It resolves any symlinks in the path to match Claude Code's behavior, which uses the real path
// when creating project directories. Claude Code requires a specific naming convention to map
// working directories to project folders: each non-alphanumeric character is replaced with a dash
// to create a unique, filesystem-safe identifier
func GetClaudeCodeProjectDir(projectPath string) (string, error) {
	// Use provided path or get current working directory
	var cwd string
	var err error
	if projectPath == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %v", err)
		}
	} else {
		cwd = projectPath
	}

	// Resolve any symlinks in the path to match Claude Code's behavior
	// Claude Code uses the real path when creating project directories, so
	// if the user's working directory contains symlinks, we need to resolve
	// them to find the correct project directory in ~/.claude/projects/
	realPath, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks in working directory: %v", err)
	}

	// Log if the path was changed by symlink resolution
	if realPath != cwd {
		slog.Debug("GetClaudeCodeProjectDir: Resolved symlinks in working directory",
			"original", cwd,
			"resolved", realPath)
	}

	// Convert path to project directory format (matching Claude Code's behavior).
	// On Unix: "/Users/sean/My Projects(1)/app" becomes "-Users-sean-My-Projects-1--app"
	// On Windows: "C:\Users\Admin\code\app" becomes "C--Users-Admin-code-app"
	projectDirName := encodeProjectDirName(realPath)

	// Log the transformation for debugging path issues
	slog.Debug("GetClaudeCodeProjectDir: Transformed working directory to project name",
		"cwd", cwd,
		"projectDirName", projectDirName)

	// Get Claude projects directory
	projectsDir, err := GetClaudeCodeProjectsDir()
	if err != nil {
		return "", fmt.Errorf("failed to get Claude projects directory: %v", err)
	}

	fullProjectPath := filepath.Join(projectsDir, projectDirName)
	slog.Debug("GetClaudeCodeProjectDir: Constructed full project path",
		"projectPath", fullProjectPath)

	return fullProjectPath, nil
}
