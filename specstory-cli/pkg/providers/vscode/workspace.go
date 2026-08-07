// Package vscode is shared support for VS Code-lineage IDE providers (VS Code
// Copilot, Cursor). It is not a provider and is never registered; it owns the
// storage conventions the lineage shares: per-workspace storage directories
// keyed by md5(path+salt), workspace.json metadata, and the state.vscdb
// ItemTable database — plus the app-launcher behavior both IDEs need.
package vscode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// WorkspaceEntry is one workspace storage directory matched to a project.
type WorkspaceEntry struct {
	ID           string // storage dir name (the md5 hash)
	Dir          string // absolute path of the storage dir
	URI          string // raw workspace/folder URI from workspace.json
	ResolvedPath string // URI resolved to a filesystem path
}

// StateDBPath returns the entry's state.vscdb path.
func (e *WorkspaceEntry) StateDBPath() string {
	return filepath.Join(e.Dir, "state.vscdb")
}

// MetadataPath returns the entry's workspace.json path.
func (e *WorkspaceEntry) MetadataPath() string {
	return filepath.Join(e.Dir, "workspace.json")
}

// WorkspaceJSON is the structure of a workspace storage entry's workspace.json.
type WorkspaceJSON struct {
	Workspace string `json:"workspace,omitempty"` // multi-root workspaces (.code-workspace file URI)
	Folder    string `json:"folder,omitempty"`    // single-folder workspaces
}

// PrimaryURI returns the URI identifying the workspace, preferring the
// multi-root workspace URI over the single folder URI — matching what the IDE
// itself treats as the workspace identity.
func (w *WorkspaceJSON) PrimaryURI() string {
	if w.Workspace != "" {
		return w.Workspace
	}
	return w.Folder
}

// ReadWorkspaceJSON reads and parses a workspace.json file.
func ReadWorkspaceJSON(path string) (*WorkspaceJSON, error) {
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

// MatchOptions controls which workspace entries FindWorkspaces keeps.
type MatchOptions struct {
	// RequireFile, when non-empty, keeps only entries whose storage dir
	// contains this file or directory — e.g. "state.vscdb" for Cursor (every
	// live workspace has one) or "chatSessions" for Copilot session reads
	// (only workspaces that ever had a chat). Empty keeps every match, which
	// write targets need: the required artifacts are created on first write.
	RequireFile string
}

// FindWorkspaces returns every workspace entry under storageRoot that matches
// projectPath, using the lineage's four match methods:
//
//  1. Direct canonical path equality (folder opened directly).
//  2. Folder-basename equality, for SSH remote / tunnel / dev-container
//     workspaces only — those paths live on a different machine or inside a
//     container, so direct comparison can never succeed and the repository
//     name is the only usable signal. Local workspaces are excluded: two
//     unrelated projects sharing a directory name (e.g. two different
//     "backend" folders) would otherwise match and export each other's
//     sessions.
//  3. The entry is a .code-workspace file that lists projectPath as a folder.
//  4. projectPath is itself a .code-workspace file and the entry is one of the
//     folders it lists.
//
// Returns an empty slice (not an error) when nothing matches; callers own the
// error message because the right guidance is provider-specific.
func FindWorkspaces(storageRoot, projectPath string, opts MatchOptions) ([]WorkspaceEntry, error) {
	canonicalProjectPath, err := NormalizePathForComparison(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize project path: %w", err)
	}
	projectBasename := filepath.Base(canonicalProjectPath)

	// If the project path is itself a .code-workspace file, pre-collect its
	// folders so entries opened directly from those folders match too (method 4).
	var workspaceFileFolders []string
	if strings.HasSuffix(canonicalProjectPath, ".code-workspace") {
		workspaceFileFolders = collectCodeWorkspaceFolders(canonicalProjectPath)
	}

	dirEntries, err := os.ReadDir(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace storage directory: %w", err)
	}

	var matches []WorkspaceEntry
	for _, dirEntry := range dirEntries {
		if !dirEntry.IsDir() {
			continue
		}

		workspaceID := dirEntry.Name()
		workspaceDir := filepath.Join(storageRoot, workspaceID)

		workspaceJSON, err := ReadWorkspaceJSON(filepath.Join(workspaceDir, "workspace.json"))
		if err != nil {
			slog.Debug("Skipping workspace directory (no valid workspace.json)",
				"workspaceID", workspaceID, "error", err)
			continue
		}

		workspaceURI := workspaceJSON.PrimaryURI()
		if workspaceURI == "" {
			slog.Debug("Skipping workspace directory (no workspace or folder URI)",
				"workspaceID", workspaceID)
			continue
		}

		workspacePath, err := URIToPath(workspaceURI)
		if err != nil {
			slog.Debug("Skipping workspace directory (invalid URI)",
				"workspaceID", workspaceID, "uri", workspaceURI, "error", err)
			continue
		}

		canonicalWorkspacePath, err := NormalizePathForComparison(workspacePath)
		if err != nil {
			slog.Debug("Failed to normalize workspace path",
				"workspaceID", workspaceID, "path", workspacePath, "error", err)
			canonicalWorkspacePath = workspacePath
		}

		// Method 1: direct path match.
		isMatch := canonicalProjectPath == canonicalWorkspacePath

		// Method 2: remote-only basename match.
		if !isMatch && spi.IsRemoteURIRequiringBasenameMatch(workspaceURI) {
			if projectBasename == filepath.Base(canonicalWorkspacePath) {
				isMatch = true
				slog.Info("Matched remote workspace by folder basename",
					"workspaceID", workspaceID,
					"workspaceURI", workspaceURI,
					"localPath", canonicalProjectPath,
					"remotePath", canonicalWorkspacePath)
			}
		}

		// Method 3: the entry's .code-workspace file lists our folder.
		if !isMatch && strings.HasSuffix(canonicalWorkspacePath, ".code-workspace") {
			if codeWorkspaceContainsFolder(canonicalWorkspacePath, canonicalProjectPath) {
				isMatch = true
				slog.Debug("Matched workspace by .code-workspace folder reference",
					"workspaceID", workspaceID,
					"workspaceFile", canonicalWorkspacePath,
					"targetFolder", canonicalProjectPath)
			}
		}

		// Method 4: our .code-workspace file lists the entry's folder.
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

		if !isMatch {
			continue
		}

		if opts.RequireFile != "" {
			if _, err := os.Stat(filepath.Join(workspaceDir, opts.RequireFile)); err != nil {
				slog.Debug("Workspace match skipped (required artifact missing)",
					"workspaceID", workspaceID, "require", opts.RequireFile)
				continue
			}
		}

		matches = append(matches, WorkspaceEntry{
			ID:           workspaceID,
			Dir:          workspaceDir,
			URI:          workspaceURI,
			ResolvedPath: workspacePath,
		})
	}

	return matches, nil
}

// SelectPrimary picks the entry the IDE itself would use for projectPath.
// Entries whose stored path equals the canonical path byte-for-byte win first:
// a case-insensitive filesystem lets the same folder be opened under several
// case spellings (~/source vs ~/Source), each getting its own entry, but the
// IDE resolves the on-disk case when it opens the folder itself. Remaining
// ties break to the most recently used entry (state.vscdb modification time).
// Returns nil for an empty slice.
func SelectPrimary(entries []WorkspaceEntry, projectPath string) *WorkspaceEntry {
	if len(entries) == 0 {
		return nil
	}
	candidates := entries
	if canonical, err := NormalizePathForComparison(projectPath); err == nil {
		var exact []WorkspaceEntry
		for _, entry := range entries {
			if entry.ResolvedPath == canonical {
				exact = append(exact, entry)
			}
		}
		if len(exact) > 0 && len(exact) < len(entries) {
			slog.Debug("Preferring case-exact workspace entries",
				"projectPath", canonical,
				"exactCount", len(exact),
				"totalCount", len(entries))
			candidates = exact
		}
	}

	best := candidates[0]
	var bestMod int64
	if info, err := os.Stat(best.StateDBPath()); err == nil {
		bestMod = info.ModTime().UnixNano()
	}
	for _, entry := range candidates[1:] {
		info, err := os.Stat(entry.StateDBPath())
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); mod > bestMod {
			best = entry
			bestMod = mod
		}
	}
	return &best
}

// collectCodeWorkspaceFolders reads a .code-workspace JSON file and returns the
// canonical paths of all listed folders. Relative paths are resolved against
// the workspace file's directory.
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
		resolved := folder.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(workspaceDir, resolved)
		}

		canonical, err := NormalizePathForComparison(resolved)
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
	for _, folder := range collectCodeWorkspaceFolders(workspaceFilePath) {
		if folder == canonicalFolder {
			return true
		}
	}
	return false
}
