package copilotide

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

// GetWorkspaceStoragePath returns the VS Code workspace storage directory path
// Returns empty string if the directory doesn't exist
func GetWorkspaceStoragePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	var storagePath string
	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/Code/User/workspaceStorage/
		storagePath = filepath.Join(homeDir, "Library", "Application Support", "Code", "User", "workspaceStorage")
	case "linux":
		// Linux: ~/.config/Code/User/workspaceStorage/
		storagePath = filepath.Join(homeDir, ".config", "Code", "User", "workspaceStorage")
	case "windows":
		// Windows: %APPDATA%\Code\User\workspaceStorage\
		appData := os.Getenv("APPDATA")
		slog.Debug("Windows APPDATA environment variable", "appData", appData)
		if appData == "" {
			slog.Warn("APPDATA environment variable not set")
			return ""
		}
		storagePath = filepath.Join(appData, "Code", "User", "workspaceStorage")
		slog.Debug("Checking Windows workspace storage path", "path", storagePath)
	default:
		return ""
	}

	// Check if directory exists
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		slog.Debug("Workspace storage directory does not exist", "path", storagePath)
		return ""
	}

	slog.Debug("Found VS Code workspace storage", "path", storagePath)
	return storagePath
}

// GetChatSessionsPath returns the chatSessions directory for a workspace
func GetChatSessionsPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, "chatSessions")
}

// GetChatEditingSessionsPath returns the chatEditingSessions directory for a workspace
func GetChatEditingSessionsPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, "chatEditingSessions")
}

// GetWorkspaceStateDBPath returns the path to the workspace state database
func GetWorkspaceStateDBPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, "state.vscdb")
}

// GetWorkspaceMetadataPath returns the path to workspace.json
func GetWorkspaceMetadataPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, "workspace.json")
}

// GetStateFilePath returns the path to a session's state file (if it exists)
func GetStateFilePath(workspaceDir, sessionID string) string {
	return filepath.Join(GetChatEditingSessionsPath(workspaceDir), sessionID, "state.json")
}

// EnsureDebugDirectory creates the debug directory for a session
func EnsureDebugDirectory(sessionID string) (string, error) {
	debugDir := filepath.Join(".specstory", "debug", sessionID)
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create debug directory: %w", err)
	}
	return debugDir, nil
}
