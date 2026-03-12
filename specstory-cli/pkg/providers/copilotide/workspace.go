package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

// isUnixStylePathOnWindows detects if a path represents a remote (WSL/SSH) filesystem
// location while running on Windows. VS Code's fsPath returns these paths in two forms:
//   - "/home/user/project"  — forward-slash form (the raw fsPath value)
//   - "\home\user\project"  — backslash form (Windows filepath.Clean applied to the above)
//
// Both forms have no drive/volume name and must not be passed through filepath.Abs,
// which would corrupt them by prepending the current drive letter (e.g. C:\).
func isUnixStylePathOnWindows(path string) bool {
	if runtime.GOOS != "windows" || filepath.VolumeName(path) != "" {
		return false
	}
	// Forward-slash prefix: /home/user/project
	if strings.HasPrefix(path, "/") {
		return true
	}
	// Backslash prefix without UNC double-backslash: \home\user\project
	// (VS Code converts the forward-slash form to this on Windows)
	if strings.HasPrefix(path, `\`) && !strings.HasPrefix(path, `\\`) {
		return true
	}
	return false
}

// isWindowsWSLUNCPath reports whether path is a Windows UNC path pointing into WSL,
// i.e. \\wsl.localhost\<distro>\... or \\wsl$\<distro>\...
func isWindowsWSLUNCPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, `\\wsl.localhost\`) || strings.HasPrefix(lower, `\\wsl$\`)
}

// normalizeWindowsWSLPath converts Windows UNC WSL paths to Unix format.
// Handles paths like:
//   - \\wsl.localhost\Ubuntu\home\user\project -> /home/user/project
//   - \\wsl$\Ubuntu\home\user\project -> /home/user/project
func normalizeWindowsWSLPath(path string) string {
	if !strings.Contains(path, "\\") {
		return path
	}

	// Convert backslashes to forward slashes
	normalized := strings.ReplaceAll(path, "\\", "/")

	// Strip Windows UNC WSL prefixes and the distro name
	lower := strings.ToLower(normalized)
	for _, prefix := range []string{"//wsl.localhost/", "//wsl$/"} {
		if strings.HasPrefix(lower, prefix) {
			remainder := normalized[len(prefix):]
			// Skip the distro name (e.g., "Ubuntu/home/user" -> "/home/user")
			if slashIdx := strings.Index(remainder, "/"); slashIdx >= 0 {
				return remainder[slashIdx:]
			}
			return "/"
		}
	}

	return normalized
}

// normalizePathForComparison normalizes a path for workspace matching.
// Handles three cases:
// 1. Windows UNC WSL paths: \\wsl.localhost\Ubuntu\... -> /home/user/...
// 2. Unix-style paths on Windows (WSL/SSH remotes): preserved as-is
// 3. Normal paths: resolved to canonical form with symlinks and case normalization
func normalizePathForComparison(path string) (string, error) {
	originalPath := path

	// Step 1: Normalize Windows UNC WSL paths (\\wsl.localhost\... or \\wsl$\...) to Unix format.
	// We only trigger this for actual WSL UNC paths, not for ordinary Windows paths like C:\Users\...
	if runtime.GOOS == "windows" && isWindowsWSLUNCPath(path) {
		path = normalizeWindowsWSLPath(path)
		if path != originalPath {
			slog.Debug("Normalized Windows UNC WSL path",
				"original", originalPath,
				"normalized", path)
		}
	}

	// Step 2: Check if it's a Unix-style path on Windows (WSL/SSH remote)
	if isUnixStylePathOnWindows(path) {
		// Don't use filepath.Abs or GetCanonicalPath - they would corrupt the path
		// on Windows by treating "/home/user/project" as a relative path
		cleaned := filepath.Clean(path)
		slog.Debug("Preserved Unix-style path on Windows",
			"path", cleaned)
		return cleaned, nil
	}

	// Step 3: Normal path handling - resolve to canonical form
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	canonicalPath, err := spi.GetCanonicalPath(absPath)
	if err != nil {
		slog.Warn("Failed to get canonical path, using absolute path",
			"path", path,
			"error", err)
		return absPath, nil
	}

	return canonicalPath, nil
}

// FindWorkspaceForProject finds the workspace directory that matches the given project path
// Returns the workspace match or an error if not found
func FindWorkspaceForProject(projectPath string) (*WorkspaceMatch, error) {
	// Normalize project path for comparison (handles Windows WSL paths, Unix paths on Windows, etc.)
	canonicalProjectPath, err := normalizePathForComparison(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize project path: %w", err)
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

		// Normalize workspace path for comparison (handles Unix paths on Windows, etc.)
		canonicalWorkspacePath, err := normalizePathForComparison(workspaceFilePath)
		if err != nil {
			slog.Debug("Failed to normalize workspace path",
				"workspaceID", workspaceID,
				"path", workspaceFilePath,
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
		slog.Info("Multiple workspaces match project path, selecting newest",
			"projectPath", projectPath,
			"matchCount", len(matches))
		return selectNewestWorkspace(matches)
	}

	return &matches[0], nil
}

// FindAllWorkspacesForProject finds all workspace directories that match the given project path.
// In WSL, the same project may have multiple workspaces with different URI formats
// (e.g., file://wsl.localhost/... and vscode-remote://wsl+...).
// For SSH remotes, matches are based on repository basename when available.
func FindAllWorkspacesForProject(projectPath string) ([]WorkspaceMatch, error) {
	// Normalize project path for comparison (handles Windows WSL paths, Unix paths on Windows, etc.)
	canonicalProjectPath, err := normalizePathForComparison(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize project path: %w", err)
	}

	// Get the project basename for SSH remote matching
	// SSH remotes have paths on different machines, so we match by repository name
	projectBasename := filepath.Base(canonicalProjectPath)

	slog.Debug("Searching for all workspaces matching project",
		"projectPath", projectPath,
		"canonicalPath", canonicalProjectPath,
		"projectBasename", projectBasename)

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

	var matches []WorkspaceMatch

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

		// Normalize workspace path for comparison (handles Unix paths on Windows, etc.)
		canonicalWorkspacePath, err := normalizePathForComparison(workspaceFilePath)
		if err != nil {
			slog.Debug("Failed to normalize workspace path",
				"workspaceID", workspaceID,
				"path", workspaceFilePath,
				"error", err)
			canonicalWorkspacePath = workspaceFilePath
		}

		// Method 1: Direct path matching (works for local and WSL workspaces)
		isMatch := canonicalProjectPath == canonicalWorkspacePath

		// Method 2: Basename matching (for SSH remotes)
		// When direct path comparison fails, fall back to basename matching.
		// This handles SSH remotes where the workspace path is on a different machine.
		if !isMatch {
			workspaceBasename := filepath.Base(canonicalWorkspacePath)

			if projectBasename == workspaceBasename {
				isMatch = true
				if isSSHRemoteURI(workspaceURI) {
					slog.Info("Matched SSH remote workspace by repository name",
						"workspaceID", workspaceID,
						"workspaceURI", workspaceURI,
						"localPath", canonicalProjectPath,
						"remotePath", canonicalWorkspacePath,
						"repoName", projectBasename)
				} else {
					slog.Debug("Matched workspace by folder basename",
						"workspaceID", workspaceID,
						"workspaceURI", workspaceURI,
						"projectPath", canonicalProjectPath,
						"workspacePath", canonicalWorkspacePath,
						"baseName", projectBasename)
				}
			}
		}

		if isMatch {
			// Check if chatSessions directory exists
			chatSessionsPath := GetChatSessionsPath(workspaceDir)
			if _, err := os.Stat(chatSessionsPath); err != nil {
				continue
			}

			matches = append(matches, WorkspaceMatch{
				ID:   workspaceID,
				Dir:  workspaceDir,
				URI:  workspaceURI,
				Path: workspaceFilePath,
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

// uriToPath converts a workspace URI to a local file path.
// Handles standard file:// URIs, WSL file://wsl.localhost/ URIs,
// vscode-remote://wsl+distro/ URIs used by VS Code in WSL,
// and vscode-remote://ssh-remote+config/path URIs for SSH remotes.
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
// Handles two types of remote URIs:
// 1. WSL: vscode-remote://wsl%2B{distro}/{path} - path is the WSL filesystem path
// 2. SSH: vscode-remote://ssh-remote%2B{config-hex}/{path} - path is the remote filesystem path
// Go's url.Parse rejects %2B in the host component, so we parse manually.
func parseVSCodeRemoteURI(uri string) (string, error) {
	// Strip scheme prefix: "vscode-remote://"
	remainder := strings.TrimPrefix(uri, "vscode-remote://")

	// Split host from path at the first unencoded slash after the host
	slashIdx := strings.Index(remainder, "/")
	if slashIdx < 0 {
		return "", fmt.Errorf("malformed vscode-remote URI (no path): %s", uri)
	}

	host := remainder[:slashIdx]
	pathPart := remainder[slashIdx:]

	// Decode the host to determine remote type
	decodedHost, err := url.PathUnescape(host)
	if err != nil {
		decodedHost = host
	}

	hostLower := strings.ToLower(decodedHost)

	// Check if it's a supported remote type
	if !strings.HasPrefix(hostLower, "wsl+") &&
		!strings.EqualFold(decodedHost, "wsl") &&
		!strings.HasPrefix(hostLower, "ssh-remote+") &&
		!strings.EqualFold(decodedHost, "ssh-remote") {
		return "", fmt.Errorf("unsupported vscode-remote host %q: %s", decodedHost, uri)
	}

	// Decode the path portion
	path, err := url.PathUnescape(pathPart)
	if err != nil {
		return "", fmt.Errorf("failed to decode vscode-remote URI path: %w", err)
	}

	// Log the conversion with appropriate context
	if strings.HasPrefix(hostLower, "ssh-remote") {
		slog.Debug("Converted vscode-remote SSH URI to path", "uri", uri, "path", path)
	} else {
		slog.Debug("Converted vscode-remote WSL URI to path", "uri", uri, "path", path)
	}

	return path, nil
}

// isSSHRemoteURI checks if a URI is a vscode-remote SSH URI
func isSSHRemoteURI(uri string) bool {
	return strings.HasPrefix(uri, "vscode-remote://ssh-remote")
}
