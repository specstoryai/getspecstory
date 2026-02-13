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

		// Compare paths
		if canonicalProjectPath == canonicalWorkspacePath {
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

	// Get the path from the URI and decode it
	// This converts %3A to : and other URL-encoded characters
	path, err := url.PathUnescape(parsedURI.Path)
	if err != nil {
		return "", fmt.Errorf("failed to decode URI path: %w", err)
	}

	// On Windows, URL paths have an extra leading slash (e.g., file:///c:/Users becomes /c:/Users)
	// We need to remove the leading slash and normalize the path
	if filepath.Separator == '\\' {
		// Remove leading slash on Windows
		if len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}
		// Normalize path separators to backslashes
		path = filepath.FromSlash(path)
	}

	return path, nil
}
