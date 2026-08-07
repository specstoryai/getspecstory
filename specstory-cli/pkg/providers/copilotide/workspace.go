package copilotide

import (
	"crypto/md5" // #nosec G501 -- not used for security; mirrors VS Code's own workspace-ID scheme
	"database/sql"
	"encoding/hex"
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
func (p *Provider) FindWorkspaceForProject(projectPath string) (*WorkspaceMatch, error) {
	return p.findWorkspaceForProject(projectPath, true)
}

// findWorkspaceForProject matches projectPath against every workspace.json in the
// variant's storage directory. requireChatSessions filters out matches that have never
// had a chat session — wanted when reading sessions, not when picking a write target
// for reconstruction (the resume flow creates the chatSessions directory when writing).
func (p *Provider) findWorkspaceForProject(projectPath string, requireChatSessions bool) (*WorkspaceMatch, error) {
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

		// Method 1: Direct path match (folder opened directly).
		isMatch := canonicalProjectPath == canonicalWorkspacePath

		// Method 2: Basename matching (SSH remotes, tunnels, and dev containers only).
		// These workspace paths live on a different machine or inside a container, so
		// direct path comparison can never succeed and the folder basename is the only
		// usable signal. The fallback must not apply to local workspaces: two unrelated
		// projects sharing a directory name (e.g. two different "backend" folders)
		// would otherwise match and export each other's sessions.
		if !isMatch && spi.IsRemoteURIRequiringBasenameMatch(workspaceURI) {
			workspaceBasename := filepath.Base(canonicalWorkspacePath)
			if filepath.Base(canonicalProjectPath) == workspaceBasename {
				isMatch = true
				slog.Info("Matched remote workspace by folder basename",
					"workspaceID", workspaceID,
					"workspaceURI", workspaceURI,
					"localPath", canonicalProjectPath,
					"remotePath", canonicalWorkspacePath,
					"repoName", workspaceBasename)
			}
		}

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

	// Prefer entries whose stored path equals the canonical path byte-for-byte.
	// macOS's case-insensitive filesystem lets the same folder be opened under
	// several case spellings (~/source vs ~/Source), each getting its own
	// workspace entry — but when VS Code resolves the folder itself it uses the
	// on-disk case, i.e. the canonical spelling. Reading or (worse) writing a
	// differently-cased entry targets a workspace the user's VS Code window
	// will never show.
	if len(matches) > 1 {
		var exact []WorkspaceMatch
		for _, match := range matches {
			if match.Path == canonicalProjectPath {
				exact = append(exact, match)
			}
		}
		if len(exact) > 0 && len(exact) < len(matches) {
			slog.Debug("Preferring case-exact workspace entries",
				"projectPath", canonicalProjectPath,
				"exactCount", len(exact),
				"totalCount", len(matches))
			matches = exact
		}
	}

	// If multiple matches remain, return the newest one (based on state.vscdb modification time)
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

// ensureWorkspaceForReconstruction returns the workspace entry for the project,
// minting one when the folder has never been opened in this VS Code variant —
// the case where a resume targets a brand-new project, which would otherwise
// fail with "open the folder in VS Code once first". Minting reproduces VS
// Code's own single-folder workspace ID — md5(folder path + a platform stat
// salt; see workspaceIDStatSalt) — verified byte-for-byte against entries
// current VS Code created, so when the user opens the folder VS Code computes
// the same ID and adopts the minted entry instead of creating a duplicate.
//
// The path is canonicalized before hashing (unlike Cursor, which mints per
// literal spelling): VS Code's own launcher resolves the on-disk case when
// opening a folder, so the canonical spelling is the ID it will compute.
func (p *Provider) ensureWorkspaceForReconstruction(projectPath string) (*WorkspaceMatch, error) {
	if ws, err := p.findWorkspaceForProject(projectPath, false); err == nil {
		return ws, nil
	}

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	canonical, err := spi.GetCanonicalPath(absPath)
	if err != nil {
		canonical = absPath
	}

	id, err := vscodeWorkspaceID(canonical)
	if err != nil {
		return nil, fmt.Errorf("cannot determine %s workspace ID for %q: %w", p.variant.AppName, canonical, err)
	}
	storageRoot := workspaceStorageRoot(p.variant.DataDirName)
	if storageRoot == "" {
		return nil, fmt.Errorf("cannot locate %s workspace storage on this machine", p.variant.AppName)
	}
	if _, err := os.Stat(storageRoot); err != nil {
		// No storage root at all means the app has never run here — a minted
		// entry would be orphaned rather than adopted.
		return nil, fmt.Errorf("%s does not appear to have been used on this machine (no workspace storage at %s)", p.variant.AppName, storageRoot)
	}

	wsPath := filepath.Join(storageRoot, id)
	folderURI := pathToFileURI(canonical)

	if err := os.MkdirAll(wsPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace storage directory: %w", err)
	}
	// workspace.json mirrors the format VS Code writes (2-space indented single field).
	workspaceJSON, err := json.MarshalIndent(WorkspaceJSON{Folder: folderURI}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workspace.json: %w", err)
	}
	if err := os.WriteFile(GetWorkspaceMetadataPath(wsPath), workspaceJSON, 0644); err != nil {
		return nil, fmt.Errorf("failed to write workspace.json: %w", err)
	}
	if err := createEmptyWorkspaceDB(GetWorkspaceStateDBPath(wsPath)); err != nil {
		return nil, err
	}

	slog.Info("Minted VS Code workspace entry for project",
		"app", p.variant.AppName, "workspaceID", id, "projectPath", canonical)

	return &WorkspaceMatch{ID: id, Dir: wsPath, URI: folderURI, Path: canonical}, nil
}

// vscodeWorkspaceID computes the workspace storage directory name VS Code
// derives for a local single-folder workspace: md5(folder path + platform stat
// salt). Mirrors getSingleFolderWorkspaceIdentifier in VS Code's main.js.
func vscodeWorkspaceID(projectPath string) (string, error) {
	info, err := os.Stat(projectPath)
	if err != nil {
		return "", err
	}
	sum := md5.Sum([]byte(projectPath + workspaceIDStatSalt(info))) // #nosec G401 -- not used for security; mirrors VS Code's own workspace-ID scheme
	return hex.EncodeToString(sum[:]), nil
}

// pathToFileURI builds the file:// URI VS Code stores in workspace.json, with
// url.URL handling percent-encoding (e.g. spaces) the way VS Code writes it.
func pathToFileURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}

// createEmptyWorkspaceDB creates a state.vscdb containing VS Code's exact
// ItemTable schema (key UNIQUE ON CONFLICT REPLACE, BLOB value), so VS Code
// adopts a minted workspace entry as its own on first open instead of treating
// it as corrupt — and so the session-index write during reconstruction has a
// table to land in.
func createEmptyWorkspaceDB(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("failed to create workspace database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close new workspace database", "error", closeErr)
		}
	}()
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)"); err != nil {
		return fmt.Errorf("failed to create ItemTable: %w", err)
	}
	return nil
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

// uriToPath converts a workspace URI to a file path. Both the vscode-remote://
// forms (wsl+distro, ssh-remote+config, tunnel+host, dev-container+config) and
// plain file:// URIs are converted by shared spi helpers so every provider
// translates them identically. Remote URIs yield the path on the remote
// machine / inside the container — matching against a local project path is
// handled by the basename fallback in findWorkspaceForProject.
func uriToPath(uri string) (string, error) {
	// Handle vscode-remote:// URIs before url.Parse because Go's URL parser
	// rejects percent-encoded characters like %2B in the host component
	// (e.g., vscode-remote://ssh-remote%2Bmyhost/home/user/project)
	if strings.HasPrefix(uri, "vscode-remote://") {
		return spi.ParseVSCodeRemoteURI(uri)
	}
	return spi.FileURIToPath(uri)
}
