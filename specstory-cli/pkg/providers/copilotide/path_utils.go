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

// userDataDirOverrides holds override paths supplied via `--user-data-dir <provider-id>:<path>`,
// keyed by variant provider ID (e.g. "copilotide", "copilotide-insiders"). Each value points
// to that variant's user-data-dir (the parent of the "User" subdirectory). Overrides are set
// once by the CLI command before watchers/lookups run, so no locking is needed.
var userDataDirOverrides = map[string]string{}

// SetUserDataDirOverride sets the user-data-dir override for the given variant provider ID.
// Pass an empty path to clear. Intended to be called once at command startup
// from the `--user-data-dir <provider-id>:<path>` flag.
func SetUserDataDirOverride(variantID, path string) {
	if path == "" {
		delete(userDataDirOverrides, variantID)
		return
	}
	userDataDirOverrides[variantID] = path
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

// getWindowsWorkspaceStoragePathInWSL attempts to find the variant's workspace storage
// on the Windows filesystem when running in WSL. It searches through Windows user
// directories to find one that contains the variant's application data.
func getWindowsWorkspaceStoragePathInWSL(dataDirName string) string {
	// Try to find Windows users directory via /mnt/c/
	usersDir := "/mnt/c/Users"
	entries, err := os.ReadDir(usersDir)
	if err != nil {
		slog.Debug("Could not read Windows Users directory from WSL", "path", usersDir, "error", err)
		return ""
	}

	// Try each user directory to find the variant's workspace storage
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip system directories
		username := entry.Name()
		if username == "Public" || username == "Default" || username == "All Users" {
			continue
		}

		// Check for workspace storage in this user's AppData
		storagePath := filepath.Join(usersDir, username, "AppData", "Roaming", dataDirName, "User", "workspaceStorage")
		if _, err := os.Stat(storagePath); err == nil {
			slog.Debug("Found workspace storage on Windows filesystem from WSL",
				"path", storagePath,
				"windowsUser", username)
			return storagePath
		}
	}

	slog.Debug("No workspace storage found on Windows filesystem from WSL", "dataDirName", dataDirName)
	return ""
}

// workspaceStorageRoot returns where the variant's workspace storage lives, without
// requiring it to exist — workspace minting targets it before the app has ever
// created it. Resolution order is the explicit --user-data-dir override, then the
// Windows-side location when running under WSL, then the OS default. Only the first
// two are existence-checked: they identify an install that is already there, whereas
// the OS default is also the path a not-yet-created storage directory would take.
// Empty only when the platform is unsupported or the home directory is unknown.
func workspaceStorageRoot(variant Variant) string {
	// An explicit --user-data-dir <provider-id>:<path> override takes precedence over
	// OS-default discovery. If the derived workspaceStorage doesn't exist, warn and
	// fall through so a stale override doesn't silently kill the provider.
	if override := userDataDirOverrides[variant.ID]; override != "" {
		candidate := filepath.Join(override, "User", "workspaceStorage")
		if _, err := os.Stat(candidate); err == nil {
			slog.Debug("Using --user-data-dir override for workspace storage", "provider", variant.ID, "path", candidate)
			return candidate
		} else {
			slog.Warn("--user-data-dir override for workspaceStorage missing; falling back to OS default",
				"provider", variant.ID, "override", override, "candidate", candidate, "error", err)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/<dataDirName>/User/workspaceStorage/
		return filepath.Join(homeDir, "Library", "Application Support", variant.DataDirName, "User", "workspaceStorage")
	case "linux":
		// Under WSL, a VS Code installed on the Windows side keeps its workspace data
		// there, so check the Windows filesystem via /mnt/c/ before the native path.
		if isWSL() {
			slog.Debug("Detected WSL environment, checking Windows filesystem")
			if windowsPath := getWindowsWorkspaceStoragePathInWSL(variant.DataDirName); windowsPath != "" {
				return windowsPath
			}
			slog.Debug("No workspace storage found on Windows side, trying native Linux path")
		}

		// Native Linux or WSL fallback: ~/.config/<dataDirName>/User/workspaceStorage/
		return filepath.Join(homeDir, ".config", variant.DataDirName, "User", "workspaceStorage")
	case "windows":
		// Windows: %APPDATA%\<dataDirName>\User\workspaceStorage\
		appData := os.Getenv("APPDATA")
		if appData == "" {
			slog.Warn("APPDATA environment variable not set")
			return ""
		}
		return filepath.Join(appData, variant.DataDirName, "User", "workspaceStorage")
	default:
		return ""
	}
}

// GetWorkspaceStoragePath returns the workspace storage directory path for the given
// VS Code variant ("Code" for VS Code, "Code - Insiders" for Insiders, ...).
// Returns empty string if the directory doesn't exist.
func GetWorkspaceStoragePath(variant Variant) string {
	storagePath := workspaceStorageRoot(variant)
	if storagePath == "" {
		return ""
	}

	// Check if directory exists
	if _, err := os.Stat(storagePath); err != nil {
		// Escalate to a warning only when the user supplied an override that also missed —
		// otherwise this is just "VS Code isn't installed" and should stay quiet.
		if override := userDataDirOverrides[variant.ID]; override != "" {
			slog.Warn("Workspace storage missing at both override and OS-default paths; provider will be idle until restart",
				"provider", variant.ID, "override", override, "osDefault", storagePath)
		} else {
			slog.Debug("Workspace storage directory does not exist", "path", storagePath)
		}
		return ""
	}

	slog.Debug("Found workspace storage", "provider", variant.ID, "path", storagePath)
	return storagePath
}

// workspaceStoragePath returns the workspace storage directory for this provider's
// VS Code variant. Returns empty string if the directory doesn't exist.
func (p *Provider) workspaceStoragePath() string {
	return GetWorkspaceStoragePath(p.variant)
}

// HasAnyChatSessions reports whether any workspace in the given distribution's
// storage has ever had a Copilot chat session. The registry uses this to decide
// whether an alternative VS Code distribution (Insiders, VSCodium, ...) is worth
// registering: merely having opened the app creates workspace storage, but only
// an actual Copilot chat creates a chatSessions directory.
func HasAnyChatSessions(variant Variant) bool {
	storagePath := GetWorkspaceStoragePath(variant)
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

// EnsureDebugDirectory creates the debug directory for a session.
// It respects the --debug-dir flag override via spi.GetDebugDir.
func EnsureDebugDirectory(sessionID string) (string, error) {
	debugDir := spi.GetDebugDir(sessionID)
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create debug directory: %w", err)
	}
	return debugDir, nil
}
