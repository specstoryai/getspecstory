package copilotide

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// FindWorkspaceForProject finds the workspace directory that matches the given project path
// Returns the workspace match or an error if not found
func (p *Provider) FindWorkspaceForProject(projectPath string) (*vscode.WorkspaceEntry, error) {
	return p.findWorkspaceForProject(projectPath, true)
}

// findWorkspaceForProject matches projectPath against the variant's workspace
// storage via the shared VS Code-lineage engine. requireChatSessions filters
// out matches that have never had a chat session — wanted when reading
// sessions, not when picking a write target for reconstruction (the resume
// flow creates the chatSessions directory when writing).
func (p *Provider) findWorkspaceForProject(projectPath string, requireChatSessions bool) (*vscode.WorkspaceEntry, error) {
	storageRoot := p.workspaceStoragePath()
	if storageRoot == "" {
		return nil, fmt.Errorf("workspace storage directory not found")
	}

	opts := vscode.MatchOptions{}
	if requireChatSessions {
		opts.RequireFile = "chatSessions"
	}
	entries, err := vscode.FindWorkspaces(storageRoot, projectPath, opts)
	if err != nil {
		return nil, err
	}

	primary := vscode.SelectPrimary(entries, projectPath)
	if primary == nil {
		return nil, fmt.Errorf("no workspace found for project path %s (searched VS Code workspace storage in %s; open the folder in VS Code once to create one)", projectPath, storageRoot)
	}

	slog.Debug("Found matching workspace",
		"workspaceID", primary.ID,
		"projectPath", projectPath,
		"workspaceURI", primary.URI)
	return primary, nil
}

// ensureWorkspaceForReconstruction returns the workspace entry for the project,
// minting one when the folder has never been opened in this VS Code variant —
// the case where a resume targets a brand-new project, which would otherwise
// fail with "open the folder in VS Code once first". When the user later opens
// the folder, VS Code computes the same workspace ID and adopts the minted
// entry instead of creating a duplicate.
//
// The path is canonicalized before minting (unlike Cursor, which mints per
// literal spelling): VS Code's own launcher resolves the on-disk case when
// opening a folder, so the canonical spelling is the ID it will compute.
func (p *Provider) ensureWorkspaceForReconstruction(projectPath string) (*vscode.WorkspaceEntry, error) {
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

	storageRoot := workspaceStorageRoot(p.variant.DataDirName)
	if storageRoot == "" {
		return nil, fmt.Errorf("cannot locate %s workspace storage on this machine", p.variant.AppName)
	}
	if _, err := os.Stat(storageRoot); err != nil {
		// No storage root at all means the app has never run here — a minted
		// entry would be orphaned rather than adopted.
		return nil, fmt.Errorf("%s does not appear to have been used on this machine (no workspace storage at %s)", p.variant.AppName, storageRoot)
	}

	return vscode.MintWorkspace(storageRoot, canonical)
}
