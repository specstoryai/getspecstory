package copilotide

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// workspaceStorageRoot returns where the given application data directory's
// workspace storage lives ("Code" for VS Code, "Code - Insiders" for
// Insiders), without requiring it to exist — workspace minting targets it
// before the app has ever created it. Empty only when the platform is
// unsupported or the home directory is unknown.
func workspaceStorageRoot(dataDirName string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/<dataDirName>/User/workspaceStorage/
		return filepath.Join(homeDir, "Library", "Application Support", dataDirName, "User", "workspaceStorage")
	case "linux":
		// Linux: ~/.config/<dataDirName>/User/workspaceStorage/
		return filepath.Join(homeDir, ".config", dataDirName, "User", "workspaceStorage")
	default:
		return ""
	}
}

// GetWorkspaceStoragePath returns the workspace storage directory path for the given
// application data directory name ("Code" for VS Code, "Code - Insiders" for Insiders).
// Returns empty string if the directory doesn't exist
func GetWorkspaceStoragePath(dataDirName string) string {
	storagePath := workspaceStorageRoot(dataDirName)
	if storagePath == "" {
		return ""
	}

	// Check if directory exists
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		return ""
	}

	return storagePath
}

// workspaceStoragePath returns the workspace storage directory for this provider's
// VS Code variant. Returns empty string if the directory doesn't exist.
func (p *Provider) workspaceStoragePath() string {
	return GetWorkspaceStoragePath(p.variant.DataDirName)
}

// HasAnyChatSessions reports whether any workspace in the given distribution's
// storage has ever had a Copilot chat session. The registry uses this to decide
// whether an alternative VS Code distribution (Insiders, VSCodium, ...) is worth
// registering: merely having opened the app creates workspace storage, but only
// an actual Copilot chat creates a chatSessions directory.
func HasAnyChatSessions(dataDirName string) bool {
	storagePath := GetWorkspaceStoragePath(dataDirName)
	if storagePath == "" {
		return false
	}
	entries, err := os.ReadDir(storagePath)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(GetChatSessionsPath(filepath.Join(storagePath, entry.Name()))); err == nil {
			return true
		}
	}
	return false
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
