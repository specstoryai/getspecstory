package claudecode

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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

	// Convert path to project directory format using regex
	// Replace anything that's not alphanumeric or dash with a dash (matching Claude Code's behavior)
	// Example: "/Users/sean/My Projects(1)/app" becomes "-Users-sean-My-Projects-1--app"
	reg := regexp.MustCompile(`[^a-zA-Z0-9-]`)
	projectDirName := reg.ReplaceAllString(realPath, "-")

	// Add leading dash if not already there
	if !strings.HasPrefix(projectDirName, "-") {
		projectDirName = "-" + projectDirName
	}

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
