package cursoride

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
	ID     string // Workspace directory name
	Path   string // Full path to workspace directory
	DBPath string // Path to workspace database
	URI    string // Original workspace URI
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
	workspaceStoragePath, err := GetWorkspaceStoragePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace storage path: %w", err)
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
		workspacePath := filepath.Join(workspaceStoragePath, workspaceID)

		// Read workspace.json
		workspaceJSONPath := filepath.Join(workspacePath, "workspace.json")
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
			// Check if workspace database exists
			dbPath := filepath.Join(workspacePath, "state.vscdb")
			if _, err := os.Stat(dbPath); err != nil {
				slog.Debug("Workspace match found but database missing",
					"workspaceID", workspaceID,
					"dbPath", dbPath)
				continue
			}

			matches = append(matches, WorkspaceMatch{
				ID:     workspaceID,
				Path:   workspacePath,
				DBPath: dbPath,
				URI:    workspaceURI,
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

	if len(matches) > 1 {
		slog.Info("Multiple workspaces match project path",
			"projectPath", projectPath,
			"matchCount", len(matches))
	}

	return &matches[0], nil
}

// FindAllWorkspacesForProject finds all workspace directories that match the given project path.
// In WSL, the same project may have multiple workspaces with different URI formats
// (e.g., file://wsl.localhost/... and vscode-remote://wsl+...).
func FindAllWorkspacesForProject(projectPath string) ([]WorkspaceMatch, error) {
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

	slog.Debug("Searching for all workspaces matching project",
		"projectPath", projectPath,
		"canonicalPath", canonicalProjectPath)

	// Get workspace storage directory
	workspaceStoragePath, err := GetWorkspaceStoragePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace storage path: %w", err)
	}

	// Read all workspace directories
	entries, err := os.ReadDir(workspaceStoragePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace storage directory: %w", err)
	}

	var matches []WorkspaceMatch

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspaceID := entry.Name()
		workspacePath := filepath.Join(workspaceStoragePath, workspaceID)

		// Read workspace.json
		workspaceJSONPath := filepath.Join(workspacePath, "workspace.json")
		workspaceJSON, err := readWorkspaceJSON(workspaceJSONPath)
		if err != nil {
			continue
		}

		workspaceURI := workspaceJSON.Workspace
		if workspaceURI == "" {
			workspaceURI = workspaceJSON.Folder
		}
		if workspaceURI == "" {
			continue
		}

		workspaceFilePath, err := uriToPath(workspaceURI)
		if err != nil {
			continue
		}

		canonicalWorkspacePath, err := spi.GetCanonicalPath(workspaceFilePath)
		if err != nil {
			canonicalWorkspacePath = workspaceFilePath
		}

		if canonicalProjectPath == canonicalWorkspacePath {
			dbPath := filepath.Join(workspacePath, "state.vscdb")
			if _, err := os.Stat(dbPath); err != nil {
				continue
			}

			matches = append(matches, WorkspaceMatch{
				ID:     workspaceID,
				Path:   workspacePath,
				DBPath: dbPath,
				URI:    workspaceURI,
			})

			slog.Debug("Found matching workspace",
				"workspaceID", workspaceID,
				"workspaceURI", workspaceURI)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no workspace found for project path: %s", projectPath)
	}

	slog.Debug("Found all matching workspaces",
		"projectPath", projectPath,
		"matchCount", len(matches))

	return matches, nil
}

// LoadComposerIDsFromAllWorkspaces loads and deduplicates composer IDs from all matching workspaces.
// This handles WSL environments where the same project may have multiple workspace entries.
func LoadComposerIDsFromAllWorkspaces(workspaces []WorkspaceMatch) ([]string, error) {
	seen := make(map[string]bool)
	var allIDs []string

	for _, ws := range workspaces {
		ids, err := LoadWorkspaceComposerIDs(ws.DBPath)
		if err != nil {
			slog.Warn("Failed to load composer IDs from workspace",
				"workspaceID", ws.ID,
				"error", err)
			continue
		}

		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				allIDs = append(allIDs, id)
			}
		}
	}

	return allIDs, nil
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

// uriToPath converts a workspace URI to a local file path.
// Handles standard file:// URIs, WSL file://wsl.localhost/ URIs,
// and vscode-remote://wsl+distro/ URIs used by Cursor/VS Code in WSL.
func uriToPath(uri string) (string, error) {
	// Handle vscode-remote:// URIs before url.Parse because Go's URL parser
	// rejects percent-encoded characters like %2B in the host component
	// (e.g., vscode-remote://wsl%2Bubuntu/home/user/project)
	if strings.HasPrefix(uri, "vscode-remote://") {
		return parseVSCodeRemoteURI(uri)
	}

	// Parse the URI
	parsedURI, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("failed to parse URI: %w", err)
	}

	// Reject non-file schemes
	if parsedURI.Scheme != "file" && parsedURI.Scheme != "" {
		return "", fmt.Errorf("unsupported URI scheme %q: %s", parsedURI.Scheme, uri)
	}

	// Get the path from the URI and decode it
	// This converts %3A to : and other URL-encoded characters
	path, err := url.PathUnescape(parsedURI.Path)
	if err != nil {
		return "", fmt.Errorf("failed to decode URI path: %w", err)
	}

	// Handle WSL file:// URIs (e.g., file://wsl.localhost/Ubuntu/home/user/project)
	// Host is "wsl.localhost" and path starts with /{DistroName}/...
	// Strip the distro name to get the actual WSL filesystem path
	if strings.EqualFold(parsedURI.Host, "wsl.localhost") || strings.HasPrefix(strings.ToLower(parsedURI.Host), "wsl$") {
		// path = /Ubuntu/home/user/project → strip /Ubuntu → /home/user/project
		if len(path) > 1 {
			if idx := strings.Index(path[1:], "/"); idx >= 0 {
				path = path[idx+1:]
				slog.Debug("Converted WSL file URI to path", "uri", uri, "path", path)
				return path, nil
			}
		}
		return "", fmt.Errorf("malformed WSL URI path: %s", uri)
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

// parseVSCodeRemoteURI extracts the filesystem path from a vscode-remote:// URI.
// These URIs use the format vscode-remote://wsl%2B{distro}/{path} where the host
// identifies a WSL distro and the path is the WSL filesystem path.
// Go's url.Parse rejects %2B in the host component, so we parse manually.
func parseVSCodeRemoteURI(uri string) (string, error) {
	// Strip scheme prefix: "vscode-remote://"
	remainder := strings.TrimPrefix(uri, "vscode-remote://")

	// Split host from path at the first unencoded slash after the host
	// The host is percent-encoded (e.g., "wsl%2Bubuntu")
	slashIdx := strings.Index(remainder, "/")
	if slashIdx < 0 {
		return "", fmt.Errorf("malformed vscode-remote URI (no path): %s", uri)
	}

	host := remainder[:slashIdx]
	pathPart := remainder[slashIdx:]

	// Decode the host to check if it's a WSL remote
	decodedHost, err := url.PathUnescape(host)
	if err != nil {
		decodedHost = host
	}

	if !strings.HasPrefix(strings.ToLower(decodedHost), "wsl+") && !strings.EqualFold(decodedHost, "wsl") {
		return "", fmt.Errorf("unsupported vscode-remote host %q: %s", decodedHost, uri)
	}

	// Decode the path portion
	path, err := url.PathUnescape(pathPart)
	if err != nil {
		return "", fmt.Errorf("failed to decode vscode-remote URI path: %w", err)
	}

	slog.Debug("Converted vscode-remote WSL URI to path", "uri", uri, "path", path)
	return path, nil
}
