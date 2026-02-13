package cursoride

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GetGlobalDatabasePath finds the Cursor IDE global database
// Returns the path to state.vscdb in Cursor's globalStorage
func GetGlobalDatabasePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	// Try multiple possible locations for the global database
	var possiblePaths []string

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/Cursor/User/globalStorage/state.vscdb (primary location)
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "globalStorage", "state.vscdb"))
		// Also try extension location (legacy/fallback)
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, ".cursor", "extensions", "cursor-context-manager-*", "globalStorage", "cursor-context-manager", "state.vscdb"))
	case "linux":
		// Linux: ~/.config/Cursor/User/globalStorage/state.vscdb (primary location)
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, ".config", "Cursor", "User", "globalStorage", "state.vscdb"))
		// Also try extension location (legacy/fallback)
		possiblePaths = append(possiblePaths,
			filepath.Join(homeDir, ".cursor", "extensions", "cursor-context-manager-*", "globalStorage", "cursor-context-manager", "state.vscdb"))
	case "windows":
		// Windows: %APPDATA%\Cursor\User\globalStorage\state.vscdb
		// Get AppData\Roaming directory
		appData := os.Getenv("APPDATA")
		slog.Debug("Windows APPDATA environment variable", "appData", appData)
		if appData == "" {
			return "", fmt.Errorf("APPDATA environment variable not set")
		}
		primaryPath := filepath.Join(appData, "Cursor", "User", "globalStorage", "state.vscdb")
		slog.Debug("Checking Windows primary path", "path", primaryPath)
		possiblePaths = append(possiblePaths, primaryPath)
		// Also try extension location (legacy/fallback)
		fallbackPath := filepath.Join(homeDir, ".cursor", "extensions", "cursor-context-manager-*", "globalStorage", "cursor-context-manager", "state.vscdb")
		slog.Debug("Checking Windows fallback path", "path", fallbackPath)
		possiblePaths = append(possiblePaths, fallbackPath)
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	// Try each possible path
	for _, path := range possiblePaths {
		// If path contains glob pattern, expand it
		if strings.Contains(path, "*") {
			matches, err := filepath.Glob(path)
			if err == nil && len(matches) > 0 {
				slog.Debug("Glob pattern matched", "pattern", path, "matches", len(matches))
				// Use first match
				if _, err := os.Stat(matches[0]); err == nil {
					slog.Debug("Found Cursor IDE global database", "path", matches[0])
					return matches[0], nil
				} else {
					slog.Debug("Matched path does not exist", "path", matches[0], "error", err)
				}
			} else {
				slog.Debug("Glob pattern did not match", "pattern", path, "error", err)
			}
		} else {
			// Direct path, check if it exists
			if _, err := os.Stat(path); err == nil {
				slog.Debug("Found Cursor IDE global database", "path", path)
				return path, nil
			} else {
				slog.Debug("Path does not exist", "path", path, "error", err)
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
	case "windows":
		// Windows: %APPDATA%\Cursor\User\workspaceStorage\
		// Get AppData\Roaming directory
		appData := os.Getenv("APPDATA")
		slog.Debug("Windows APPDATA environment variable", "appData", appData)
		if appData == "" {
			return "", fmt.Errorf("APPDATA environment variable not set")
		}
		workspaceStoragePath = filepath.Join(appData, "Cursor", "User", "workspaceStorage")
		slog.Debug("Checking Windows workspace storage path", "path", workspaceStoragePath)
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	// Check if the workspace storage directory exists
	slog.Debug("Checking if workspace storage directory exists", "path", workspaceStoragePath)
	if _, err := os.Stat(workspaceStoragePath); err != nil {
		if os.IsNotExist(err) {
			slog.Debug("Workspace storage directory does not exist", "path", workspaceStoragePath)
			return "", fmt.Errorf("workspace storage directory not found at %s (has Cursor IDE been used?)", workspaceStoragePath)
		}
		slog.Debug("Failed to access workspace storage directory", "path", workspaceStoragePath, "error", err)
		return "", fmt.Errorf("failed to access workspace storage directory: %w", err)
	}

	slog.Debug("Found Cursor IDE workspace storage", "path", workspaceStoragePath)
	return workspaceStoragePath, nil
}
