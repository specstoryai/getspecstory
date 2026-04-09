package cursoride

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// isWSL checks if the current Linux environment is actually WSL
func isWSL() bool {
	// Check /proc/version for WSL indicators
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	version := string(data)
	versionLower := strings.ToLower(version)
	return strings.Contains(versionLower, "microsoft") || strings.Contains(versionLower, "wsl")
}

// getWindowsCursorGlobalStorageInWSL attempts to find Cursor's global database
// on the Windows filesystem when running in WSL
func getWindowsCursorGlobalStorageInWSL() string {
	usersDir := "/mnt/c/Users"
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		slog.Debug("Could not read Windows Users directory from WSL", "path", usersDir, "error", err)
		return ""
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		username := entry.Name()
		if username == "Public" || username == "Default" || username == "All Users" {
			continue
		}

		// Check for Cursor global database in Windows AppData
		dbPath := filepath.Join(usersDir, username, "AppData", "Roaming", "Cursor", "User", "globalStorage", "state.vscdb")
		if _, err := os.Stat(dbPath); err == nil {
			slog.Debug("Found Cursor global database on Windows filesystem from WSL",
				"path", dbPath,
				"windowsUser", username)
			return dbPath
		}
	}

	slog.Debug("No Cursor global database found on Windows filesystem from WSL")
	return ""
}

// getWindowsCursorWorkspaceStorageInWSL attempts to find Cursor's workspace storage
// on the Windows filesystem when running in WSL
func getWindowsCursorWorkspaceStorageInWSL() string {
	usersDir := "/mnt/c/Users"
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		slog.Debug("Could not read Windows Users directory from WSL", "path", usersDir, "error", err)
		return ""
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		username := entry.Name()
		if username == "Public" || username == "Default" || username == "All Users" {
			continue
		}

		// Check for Cursor workspace storage in Windows AppData
		storagePath := filepath.Join(usersDir, username, "AppData", "Roaming", "Cursor", "User", "workspaceStorage")
		if _, err := os.Stat(storagePath); err == nil {
			slog.Debug("Found Cursor workspace storage on Windows filesystem from WSL",
				"path", storagePath,
				"windowsUser", username)
			return storagePath
		}
	}

	slog.Debug("No Cursor workspace storage found on Windows filesystem from WSL")
	return ""
}

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
		// When running in WSL, Cursor stores data on the Windows side
		// Check Windows filesystem first via /mnt/c/
		if isWSL() {
			slog.Debug("Detected WSL environment, checking Windows filesystem for Cursor global database")
			windowsPath := getWindowsCursorGlobalStorageInWSL()
			if windowsPath != "" {
				return windowsPath, nil
			}
			slog.Debug("No global database found on Windows side, trying native Linux paths")
		}

		// Native Linux or WSL fallback: ~/.config/Cursor/User/globalStorage/state.vscdb (primary location)
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
		// When running in WSL, Cursor stores workspace data on the Windows side
		// Check Windows filesystem first via /mnt/c/
		if isWSL() {
			slog.Debug("Detected WSL environment, checking Windows filesystem for Cursor workspace storage")
			windowsPath := getWindowsCursorWorkspaceStorageInWSL()
			if windowsPath != "" {
				slog.Debug("Found Cursor workspace storage on Windows filesystem", "path", windowsPath)
				return windowsPath, nil
			}
			slog.Debug("No workspace storage found on Windows side, trying native Linux path")
		}

		// Native Linux or WSL fallback: ~/.config/Cursor/User/workspaceStorage/
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
