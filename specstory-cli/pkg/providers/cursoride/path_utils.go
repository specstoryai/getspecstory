package cursoride

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GetGlobalDatabasePath finds the Cursor IDE global database.
// It is a var so tests can replace it with a function that returns a temp path.
var GetGlobalDatabasePath = getGlobalDatabasePath

// getGlobalDatabasePath is the real implementation; GetGlobalDatabasePath delegates to it.
func getGlobalDatabasePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	var possiblePaths []string
	switch runtime.GOOS {
	case "darwin":
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb"))
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, ".cursor", "extensions", "cursor-context-manager-*", "globalStorage", "cursor-context-manager", "state.vscdb"))
	case "linux":
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, ".config", "Cursor", "User", "globalStorage", "state.vscdb"))
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, ".cursor", "extensions", "cursor-context-manager-*", "globalStorage", "cursor-context-manager", "state.vscdb"))
	default:
		return "", fmt.Errorf("unsupported operating system: %s (only macOS and Linux are supported)", runtime.GOOS)
	}

	for _, path := range possiblePaths {
		if strings.Contains(path, "*") {
			matches, err := filepath.Glob(path)
			if err == nil && len(matches) > 0 {
				if _, err := os.Stat(matches[0]); err == nil {
					slog.Debug("Found Cursor IDE global database", "path", matches[0])
					return matches[0], nil
				}
			}
		} else {
			if _, err := os.Stat(path); err == nil {
				slog.Debug("Found Cursor IDE global database", "path", path)
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("global database not found in any of the expected locations")
}

// GetWorkspaceStoragePath returns the OS-specific workspace storage directory
func GetWorkspaceStoragePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	var workspaceStoragePath string
	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/Cursor/User/workspaceStorage/
		workspaceStoragePath = filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "workspaceStorage")
	case "linux":
		// Linux: ~/.config/Cursor/User/workspaceStorage/
		workspaceStoragePath = filepath.Join(homeDir, ".config", "Cursor", "User", "workspaceStorage")
	default:
		return "", fmt.Errorf("unsupported operating system: %s (only macOS and Linux are supported)", runtime.GOOS)
	}

	// Check if the workspace storage directory exists
	if _, err := os.Stat(workspaceStoragePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("workspace storage directory not found at %s (has Cursor IDE been used?)", workspaceStoragePath)
		}
		return "", fmt.Errorf("failed to access workspace storage directory: %w", err)
	}

	slog.Debug("Found Cursor IDE workspace storage", "path", workspaceStoragePath)
	return workspaceStoragePath, nil
}
