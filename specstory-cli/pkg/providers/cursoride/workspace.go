package cursoride

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
	ID     string // Workspace directory name
	Path   string // Full path to workspace directory
	DBPath string // Path to workspace database
	URI    string // Original workspace URI
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
			continue
		}

		// Get the workspace URI (prefer workspace over folder)
		workspaceURI := workspaceJSON.Workspace
		if workspaceURI == "" {
			workspaceURI = workspaceJSON.Folder
		}

		if workspaceURI == "" {
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
// For SSH remotes, matches are based on Git repository identity when available.
func FindAllWorkspacesForProject(projectPath string) ([]WorkspaceMatch, error) {
	// Normalize project path for comparison (handles Windows WSL paths, Unix paths on Windows, etc.)
	canonicalProjectPath, err := normalizePathForComparison(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize project path: %w", err)
	}

	// Get the project basename for SSH remote matching
	// SSH remotes have paths on different machines, so we match by repository name
	projectBasename := filepath.Base(canonicalProjectPath)

	// If the project path is itself a .code-workspace file, pre-collect its folders.
	// This lets us also match workspace entries opened directly from those folders
	// (Method 4 below), so both usage patterns are discoverable via the workspace file path.
	var workspaceFileFolders []string
	if strings.HasSuffix(canonicalProjectPath, ".code-workspace") {
		workspaceFileFolders = collectCodeWorkspaceFolders(canonicalProjectPath)
	}

	slog.Debug("Searching for all workspaces matching project",
		"projectPath", projectPath,
		"canonicalPath", canonicalProjectPath,
		"projectBasename", projectBasename)

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
				}
			}
		}

		// Method 3: Code workspace file matching.
		// When Cursor IDE is opened via a .code-workspace file, the workspace.json stores
		// the workspace file URI rather than the folder URI. Check whether that workspace
		// file lists our target folder as one of its folders.
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

// isSSHRemoteURI checks if a URI is a vscode-remote SSH URI
func isSSHRemoteURI(uri string) bool {
	return strings.HasPrefix(uri, "vscode-remote://ssh-remote")
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
// vscode-remote://wsl+distro/ URIs used by Cursor/VS Code in WSL,
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

// collectCodeWorkspaceFolders reads a .code-workspace JSON file and returns the
// normalized paths of all listed folders. Relative paths are resolved against the
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

		canonical, err := normalizePathForComparison(resolved)
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

// parseVSCodeRemoteURI extracts the filesystem path from a vscode-remote:// URI.
// Handles two types of remote URIs:
// 1. WSL: vscode-remote://wsl%2B{distro}/{path} - path is the WSL filesystem path
// 2. SSH: vscode-remote://ssh-remote%2B{config-hex}/{path} - path is the remote filesystem path
// Go's url.Parse rejects %2B in the host component, so we parse manually.
func parseVSCodeRemoteURI(uri string) (string, error) {
	// Strip scheme prefix: "vscode-remote://"
	remainder := strings.TrimPrefix(uri, "vscode-remote://")

	// Split host from path at the first unencoded slash after the host
	// The host is percent-encoded (e.g., "wsl%2Bubuntu" or "ssh-remote%2B{config-hex}")
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
		!strings.EqualFold(decodedHost, "ssh-remote") &&
		!strings.HasPrefix(hostLower, "dev-container+") &&
		!strings.EqualFold(decodedHost, "dev-container") {
		return "", fmt.Errorf("unsupported vscode-remote host %q: %s", decodedHost, uri)
	}

	// Decode the path portion
	path, err := url.PathUnescape(pathPart)
	if err != nil {
		return "", fmt.Errorf("failed to decode vscode-remote URI path: %w", err)
	}

	// Log the conversion with appropriate context
	switch {
	case strings.HasPrefix(hostLower, "ssh-remote"):
		slog.Debug("Converted vscode-remote SSH URI to path", "uri", uri, "path", path)
	case strings.HasPrefix(hostLower, "dev-container"):
		slog.Debug("Converted vscode-remote dev container URI to path", "uri", uri, "path", path)
	default:
		slog.Debug("Converted vscode-remote WSL URI to path", "uri", uri, "path", path)
	}

	return path, nil
}
