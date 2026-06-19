package copilotide

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// userDataDirOverride holds an override path supplied via `--user-data-dir copilotide:<path>`.
// When non-empty, it points to VS Code's user-data-dir (the parent of the "User" subdirectory).
// The override is set once by the CLI command before watchers/lookups run, so no locking is needed.
var userDataDirOverride string

// SetUserDataDirOverride sets the copilotide user-data-dir override.
// Pass an empty string to clear. Intended to be called once at command startup
// from the `--user-data-dir copilotide:<path>` flag.
func SetUserDataDirOverride(p string) {
	userDataDirOverride = p
}

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

// getWindowsWorkspaceStoragePathInWSL attempts to find VS Code workspace storage
// on the Windows filesystem when running in WSL. It searches through Windows user
// directories to find one that contains VS Code data.
func getWindowsWorkspaceStoragePathInWSL() string {
	// Try to find Windows users directory via /mnt/c/
	usersDir := "/mnt/c/Users"
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		slog.Debug("Could not read Windows Users directory from WSL", "path", usersDir, "error", err)
		return ""
	}

	// Try each user directory to find VS Code workspace storage
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip system directories
		username := entry.Name()
		if username == "Public" || username == "Default" || username == "All Users" {
			continue
		}

		// Check for VS Code workspace storage in this user's AppData
		storagePath := filepath.Join(usersDir, username, "AppData", "Roaming", "Code", "User", "workspaceStorage")
		if _, err := os.Stat(storagePath); err == nil {
			slog.Debug("Found VS Code workspace storage on Windows filesystem from WSL",
				"path", storagePath,
				"windowsUser", username)
			return storagePath
		}
	}

	slog.Debug("No VS Code workspace storage found on Windows filesystem from WSL")
	return ""
}

// GetWorkspaceStoragePath returns the VS Code workspace storage directory path
// Returns empty string if the directory doesn't exist
func GetWorkspaceStoragePath() string {
	// An explicit --user-data-dir copilotide:<path> override takes precedence over
	// OS-default discovery. If the derived workspaceStorage doesn't exist, warn and
	// fall through so a stale override doesn't silently kill the provider.
	if userDataDirOverride != "" {
		candidate := filepath.Join(userDataDirOverride, "User", "workspaceStorage")
		if _, err := os.Stat(candidate); err == nil {
			slog.Debug("Using --user-data-dir override for VS Code workspace storage", "path", candidate)
			return candidate
		} else {
			slog.Warn("--user-data-dir override for VS Code workspaceStorage missing; falling back to OS default",
				"override", userDataDirOverride, "candidate", candidate, "error", err)
		}
	}

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
		// When running in WSL, VS Code stores workspace data on the Windows side
		// Check Windows filesystem first via /mnt/c/, then fall back to native Linux
		if isWSL() {
			slog.Debug("Detected WSL environment, checking Windows filesystem")
			storagePath = getWindowsWorkspaceStoragePathInWSL()
			if storagePath != "" {
				return storagePath
			}
			slog.Debug("No workspace storage found on Windows side, trying native Linux path")
		}

		// Native Linux or WSL fallback: ~/.config/Code/User/workspaceStorage/
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
		// Escalate to a warning only when the user supplied an override that also missed —
		// otherwise this is just "VS Code isn't installed" and should stay quiet.
		if userDataDirOverride != "" {
			slog.Warn("VS Code workspace storage missing at both override and OS-default paths; provider will be idle until restart",
				"override", userDataDirOverride, "osDefault", storagePath)
		} else {
			slog.Debug("Workspace storage directory does not exist", "path", storagePath)
		}
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

// EnsureDebugDirectory creates the debug directory for a session.
// It respects the --debug-dir flag override via spi.GetDebugDir.
func EnsureDebugDirectory(sessionID string) (string, error) {
	debugDir := spi.GetDebugDir(sessionID)
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create debug directory: %w", err)
	}
	return debugDir, nil
}
