package cursoride

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
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

	var dbPath string
	switch runtime.GOOS {
	case "darwin":
		dbPath = filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb")
	case "linux":
		dbPath = filepath.Join(homeDir, ".config", "Cursor", "User", "globalStorage", "state.vscdb")
	default:
		return "", fmt.Errorf("unsupported operating system: %s (only macOS and Linux are supported)", runtime.GOOS)
	}

	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("global database not found at %s (has Cursor IDE been used?)", dbPath)
	}

	slog.Debug("Found Cursor IDE global database", "path", dbPath)
	return dbPath, nil
}

// GetWorkspaceStoragePath returns the OS-specific workspace storage directory.
// It is a var so tests can point it at a temp directory (e.g. when exercising
// EnsureWorkspaceForProject, which creates entries under it).
var GetWorkspaceStoragePath = getWorkspaceStoragePath

// getWorkspaceStoragePath is the real implementation; GetWorkspaceStoragePath delegates to it.
func getWorkspaceStoragePath() (string, error) {
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
