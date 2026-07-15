package copilotide

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// FindWorkspaceForProject finds the single best workspace directory that matches the
// given project path. Callers that need exactly one target (e.g. reconstruction writes)
// use this; read paths should use FindWorkspacesForProject instead, because a project
// can legitimately match several workspace entries (opened directly as a folder AND
// listed in one or more .code-workspace files) and sessions may live in any of them.
func (p *Provider) FindWorkspaceForProject(projectPath string) (*WorkspaceMatch, error) {
	return p.findWorkspaceForProject(projectPath, true)
}

// FindWorkspacesForProject finds ALL workspace directories that match the given project
// path, sorted newest-first by state.vscdb modification time (so the most-likely-active
// workspace is tried first and iteration order is deterministic).
func (p *Provider) FindWorkspacesForProject(projectPath string) ([]WorkspaceMatch, error) {
	return p.findWorkspacesForProject(projectPath, true)
}

// findWorkspaceForProject is "first of plural": it returns the newest matching workspace.
// Kept for callers that need exactly one target — the reconstruction write path — where
// newest ≈ the VS Code window the user is most likely to open next.
func (p *Provider) findWorkspaceForProject(projectPath string, requireChatSessions bool) (*WorkspaceMatch, error) {
	matches, err := p.findWorkspacesForProject(projectPath, requireChatSessions)
	if err != nil {
		return nil, err
	}

	if len(matches) > 1 {
		slog.Warn("Multiple workspaces match project path, selecting newest",
			"projectPath", projectPath,
			"matchCount", len(matches),
			"selectedWorkspaceID", matches[0].ID)
	}

	return &matches[0], nil
}

// findWorkspacesForProject matches projectPath against every workspace.json in the
// variant's storage directory and returns all matches, sorted newest-first by
// state.vscdb mtime. requireChatSessions filters out matches that have never had a
// chat session — wanted when reading sessions, not when picking a write target for
// reconstruction (the resume flow creates the chatSessions directory when writing).
func (p *Provider) findWorkspacesForProject(projectPath string, requireChatSessions bool) ([]WorkspaceMatch, error) {
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
	workspaceStoragePath := p.workspaceStoragePath()
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
			// Check if chatSessions directory exists (skipped for reconstruction targets,
			// where the directory is created on first write)
			chatSessionsPath := GetChatSessionsPath(workspaceDir)
			if _, err := os.Stat(chatSessionsPath); err != nil && requireChatSessions {
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
		return nil, fmt.Errorf("no workspace found for project path %s (searched VS Code workspace storage in %s; open the folder in VS Code once to create one)", projectPath, workspaceStoragePath)
	}

	sortWorkspacesNewestFirst(matches)
	return matches, nil
}

// sortWorkspacesNewestFirst orders matches by descending state.vscdb modification time,
// so the workspace the user most recently had open comes first. A workspace whose
// state.vscdb can't be stat'd gets the zero time and sorts last rather than erroring
// out the whole lookup. The sort is stable so ties keep directory-listing order,
// keeping iteration deterministic across calls.
func sortWorkspacesNewestFirst(matches []WorkspaceMatch) {
	modTimes := make(map[string]time.Time, len(matches))
	for _, match := range matches {
		stateDBPath := GetWorkspaceStateDBPath(match.Dir)
		info, err := os.Stat(stateDBPath)
		if err != nil {
			slog.Debug("Failed to stat state.vscdb", "path", stateDBPath, "error", err)
			continue
		}
		modTimes[match.ID] = info.ModTime()
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return modTimes[matches[i].ID].After(modTimes[matches[j].ID])
	})
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

	// url.Parse already percent-decodes into Path (e.g. %20 -> space), so no
	// extra PathUnescape is needed — unescaping again would corrupt paths
	// containing literal % sequences.
	path := parsedURI.Path

	// On Windows, URL paths have an extra leading slash (e.g., /C:/Users)
	// but we don't support Windows, so we can just use the path as-is

	return path, nil
}
