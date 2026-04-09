package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// WorkspaceMatch represents a matched workspace directory
type WorkspaceMatch struct {
	ID   string // Workspace directory name
	Dir  string // Full path to workspace directory
	URI  string // Original workspace URI
	Path string // Resolved workspace path
}

// WorkspaceJSON represents the structure of workspace.json
type WorkspaceJSON struct {
	Workspace string `json:"workspace,omitempty"` // For multi-root workspaces
	Folder    string `json:"folder,omitempty"`    // For single folder workspaces
}

// FindWorkspaceForProject finds the workspace directory that matches the given project path
// Returns the workspace match or an error if not found
func FindWorkspaceForProject(projectPath string) (*WorkspaceMatch, error) {
	// Get canonical project path (resolve symlinks, normalize case)
	absProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	canonicalProjectPath, err := spi.GetCanonicalPath(absProjectPath)
	if err != nil {
		slog.Warn("Failed to get canonical path, using absolute path",
			"projectPath", projectPath,
			"error", err)
		canonicalProjectPath = absProjectPath
	}

	// If the project path is itself a .code-workspace file, pre-collect its folders.
	// This lets us also match workspace entries opened directly from those folders
	// (Method 4 below), so both usage patterns are discoverable via the workspace file path.
	var workspaceFileFolders []string
	if strings.HasSuffix(canonicalProjectPath, ".code-workspace") {
		workspaceFileFolders = collectCodeWorkspaceFolders(canonicalProjectPath)
	}

	slog.Debug("Searching for workspace matching project",
		"projectPath", projectPath,
		"canonicalPath", canonicalProjectPath)

	// Get workspace storage directory
	workspaceStoragePath := GetWorkspaceStoragePath()
	if workspaceStoragePath == "" {
		return nil, fmt.Errorf("workspace storage directory not found")
	}

	// Read all workspace directories
	entries, err := os.ReadDir(workspaceStoragePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace storage directory: %w", err)
	}

	// Track all workspace directories for potential matches
	var matches []WorkspaceMatch

	// Check each workspace directory
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspaceID := entry.Name()
		workspaceDir := filepath.Join(workspaceStoragePath, workspaceID)

		// Read workspace.json
		workspaceJSONPath := GetWorkspaceMetadataPath(workspaceDir)
		workspaceJSON, err := readWorkspaceJSON(workspaceJSONPath)
		if err != nil {
			slog.Debug("Skipping workspace directory (no valid workspace.json)",
				"workspaceID", workspaceID,
				"error", err)
			continue
		}

		// Get the workspace URI (prefer workspace over folder)
		workspaceURI := workspaceJSON.Workspace
		if workspaceURI == "" {
			workspaceURI = workspaceJSON.Folder
		}

		if workspaceURI == "" {
			slog.Debug("Skipping workspace directory (no workspace or folder URI)",
				"workspaceID", workspaceID)
			continue
		}

		// Convert URI to file path
		workspaceFilePath, err := uriToPath(workspaceURI)
		if err != nil {
			slog.Debug("Skipping workspace directory (invalid URI)",
				"workspaceID", workspaceID,
				"uri", workspaceURI,
				"error", err)
			continue
		}

		// Get canonical workspace path
		canonicalWorkspacePath, err := spi.GetCanonicalPath(workspaceFilePath)
		if err != nil {
			slog.Debug("Failed to get canonical workspace path",
				"workspacePath", workspaceFilePath,
				"error", err)
			canonicalWorkspacePath = workspaceFilePath
		}

		// Direct path match (folder opened directly).
		isMatch := canonicalProjectPath == canonicalWorkspacePath

		// Method 3: Code workspace file matching.
		// When VS Code is opened via a .code-workspace file, workspace.json stores
		// the workspace file URI rather than the folder URI. Check whether that
		// workspace file lists our target folder as one of its folders.
		if !isMatch && strings.HasSuffix(canonicalWorkspacePath, ".code-workspace") {
			if codeWorkspaceContainsFolder(canonicalWorkspacePath, canonicalProjectPath) {
				isMatch = true
				slog.Debug("Matched workspace by .code-workspace folder reference",
					"workspaceID", workspaceID,
					"workspaceFile", canonicalWorkspacePath,
					"targetFolder", canonicalProjectPath)
			}
		}

		// Method 4: project path is a .code-workspace file — also match workspace entries
		// opened directly from a folder listed in that workspace file.
		if !isMatch {
			for _, folderPath := range workspaceFileFolders {
				if canonicalWorkspacePath == folderPath {
					isMatch = true
					slog.Debug("Matched workspace entry by folder listed in .code-workspace",
						"workspaceID", workspaceID,
						"folder", folderPath,
						"workspaceFile", canonicalProjectPath)
					break
				}
			}
		}

		if isMatch {
			// Check if chatSessions directory exists
			chatSessionsPath := GetChatSessionsPath(workspaceDir)
			if _, err := os.Stat(chatSessionsPath); err != nil {
				slog.Debug("Workspace match found but chatSessions directory missing",
					"workspaceID", workspaceID,
					"chatSessionsPath", chatSessionsPath)
				continue
			}

			matches = append(matches, WorkspaceMatch{
				ID:   workspaceID,
				Dir:  workspaceDir,
				URI:  workspaceURI,
				Path: workspaceFilePath,
			})

			slog.Info("Found matching workspace",
				"workspaceID", workspaceID,
				"projectPath", canonicalProjectPath,
				"workspaceURI", workspaceURI)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no workspace found for project path: %s", projectPath)
	}

	// If multiple matches, return the newest one (based on state.vscdb modification time)
	if len(matches) > 1 {
		slog.Warn("Multiple workspaces match project path, selecting newest",
			"projectPath", projectPath,
			"matchCount", len(matches))
		return selectNewestWorkspace(matches)
	}

	return &matches[0], nil
}

// selectNewestWorkspace returns the workspace with the newest state.vscdb modification time
func selectNewestWorkspace(matches []WorkspaceMatch) (*WorkspaceMatch, error) {
	var newest *WorkspaceMatch
	var newestTime int64

	for i := range matches {
		match := &matches[i]
		stateDBPath := GetWorkspaceStateDBPath(match.Dir)

		info, err := os.Stat(stateDBPath)
		if err != nil {
			slog.Debug("Failed to stat state.vscdb", "path", stateDBPath, "error", err)
			continue
		}

		modTime := info.ModTime().Unix()
		if newest == nil || modTime > newestTime {
			newest = match
			newestTime = modTime
		}
	}

	if newest == nil {
		return &matches[0], nil // Fall back to first match
	}

	slog.Debug("Selected newest workspace", "workspaceID", newest.ID, "modTime", newestTime)
	return newest, nil
}

// collectCodeWorkspaceFolders reads a .code-workspace JSON file and returns the
// canonical paths of all listed folders. Relative paths are resolved against the
// workspace file's directory.
func collectCodeWorkspaceFolders(workspaceFilePath string) []string {
	data, err := os.ReadFile(workspaceFilePath)
	if err != nil {
		slog.Debug("collectCodeWorkspaceFolders: failed to read workspace file",
			"path", workspaceFilePath, "error", err)
		return nil
	}

	var workspace struct {
		Folders []struct {
			Path string `json:"path"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &workspace); err != nil {
		slog.Debug("collectCodeWorkspaceFolders: failed to parse workspace file",
			"path", workspaceFilePath, "error", err)
		return nil
	}

	workspaceDir := filepath.Dir(workspaceFilePath)
	var folders []string
	for _, folder := range workspace.Folders {
		if folder.Path == "" {
			continue
		}

		// Resolve relative paths against the workspace file's directory.
		var resolved string
		if filepath.IsAbs(folder.Path) {
			resolved = folder.Path
		} else {
			resolved = filepath.Join(workspaceDir, folder.Path)
		}

		canonical, err := spi.GetCanonicalPath(resolved)
		if err != nil {
			canonical = filepath.Clean(resolved)
		}
		folders = append(folders, canonical)
	}

	return folders
}

// codeWorkspaceContainsFolder reports whether canonicalFolder is listed in the
// .code-workspace file at workspaceFilePath.
func codeWorkspaceContainsFolder(workspaceFilePath, canonicalFolder string) bool {
	for _, f := range collectCodeWorkspaceFolders(workspaceFilePath) {
		if f == canonicalFolder {
			return true
		}
	}
	return false
}

// readWorkspaceJSON reads and parses a workspace.json file
func readWorkspaceJSON(path string) (*WorkspaceJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace.json: %w", err)
	}

	var workspace WorkspaceJSON
	if err := json.Unmarshal(data, &workspace); err != nil {
		return nil, fmt.Errorf("failed to parse workspace.json: %w", err)
	}

	return &workspace, nil
}

// uriToPath converts a file:// URI to a local file path
func uriToPath(uri string) (string, error) {
	// Handle file:// URIs
	if !strings.HasPrefix(uri, "file://") {
		return "", fmt.Errorf("URI must start with file://: %s", uri)
	}

	// Parse the URI
	parsedURI, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	// Get the path from the URI
	path := parsedURI.Path

	// On Windows, URL paths have an extra leading slash (e.g., /C:/Users)
	// but we don't support Windows, so we can just use the path as-is

	return path, nil
}
