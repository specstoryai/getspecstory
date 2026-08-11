package cursoride

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
)

// FindWorkspaceForProject finds the workspace directory that matches the given project path.
// It is a var so tests can replace it with a stub that returns a temporary workspace.
var FindWorkspaceForProject = findWorkspaceForProject

// findWorkspaceForProject is the real implementation; FindWorkspaceForProject delegates to it.
// It picks a single workspace for callers that need exactly one write/watch target (e.g.
// reconstructing a session into a specific state.vscdb). It delegates to
// FindAllWorkspacesForProject so it benefits from the same matching methods (direct path,
// remote basename fallback, .code-workspace folder membership), and to the shared
// SelectPrimary for the case-exact preference and most-recently-used tie-break —
// so a reconstructed session lands in the workspace instance Cursor will actually open.
func findWorkspaceForProject(projectPath string) (*vscode.WorkspaceEntry, error) {
	matches, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return nil, err
	}
	return vscode.SelectPrimary(matches, projectPath), nil
}

// FindAllWorkspacesForProject finds all workspace directories that match the given
// project path via the shared VS Code-lineage engine. In WSL, the same project may
// have multiple workspaces with different URI formats; for SSH remotes and dev
// containers matching falls back to the workspace folder basename. Entries without
// a state.vscdb are skipped — every live Cursor workspace has one.
func FindAllWorkspacesForProject(projectPath string) ([]vscode.WorkspaceEntry, error) {
	storagePath, err := GetWorkspaceStoragePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace storage path: %w", err)
	}

	matches, err := vscode.FindWorkspaces(storagePath, projectPath, vscode.MatchOptions{RequireFile: "state.vscdb"})
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no workspace found for project path: %s", projectPath)
	}

	slog.Debug("Found all matching workspaces",
		"projectPath", projectPath,
		"matchCount", len(matches))
	return matches, nil
}

// EnsureWorkspaceForProject returns the workspace entry for the project, minting one in
// Cursor's workspaceStorage when the folder has never been opened in Cursor (the case
// where FindWorkspaceForProject has nothing to find, which would otherwise make the
// project unusable as a resume target). Minting reproduces Cursor's own single-folder
// workspace ID, so when the user later opens the folder, Cursor computes the same ID
// and adopts the minted entry instead of creating a duplicate.
//
// The path is deliberately minted exactly as given (no case or symlink
// normalization) because that is what Cursor does — the same folder opened via
// differently-spelled paths genuinely gets distinct workspace entries.
func EnsureWorkspaceForProject(projectPath string) (*vscode.WorkspaceEntry, error) {
	if ws, err := FindWorkspaceForProject(projectPath); err == nil {
		return ws, nil
	}

	storagePath, err := GetWorkspaceStoragePath()
	if err != nil {
		return nil, err
	}
	return vscode.MintWorkspace(storagePath, projectPath)
}

// FindProjectComposerIDs returns the composer IDs associated with the project, merging
// the two association sources that exist across Cursor versions:
//
//  1. Workspace-DB references (composer.composerData / workbench.panel keys) — Cursor 2
//     and early Cursor 3 record every conversation there.
//  2. The workspaceIdentifier embedded in each global composerData row — in Cursor
//     >= 3.12 the workspace DB no longer records conversations at all, and the global
//     composer.composerHeaders key is flushed lazily (often not until Cursor exits),
//     so this embedded field is the only association that updates live.
//
// The global scan reads every composerData row (headers only, no bubbles), so the
// cost of parsing the composer rows is acceptable. Callers that already hold the
// scanned map should use projectComposerIDs directly instead of scanning again.
func FindProjectComposerIDs(globalDbPath, projectPath string, workspaces []vscode.WorkspaceEntry) ([]string, error) {
	composers, err := LoadAllComposerDataLightweight(globalDbPath)
	if err != nil {
		// The workspace-DB IDs may still be usable (older Cursor versions), so degrade
		// to source 1 rather than failing the whole lookup.
		slog.Warn("Failed to load global composer data for workspaceIdentifier matching", "error", err)
		composers = nil
	}
	return projectComposerIDs(composers, projectPath, workspaces)
}

// projectComposerIDs is the load-free core of FindProjectComposerIDs: it matches an
// already-scanned composer map against the project. Split out so callers that need the
// scanned map for their own purposes too (e.g. the watcher's seeding, which also reads
// the timestamps) don't scan the global database twice.
func projectComposerIDs(composers map[string]*ComposerData, projectPath string, workspaces []vscode.WorkspaceEntry) ([]string, error) {
	ids, err := LoadComposerIDsFromAllWorkspaces(workspaces)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
	}

	canonicalProject, err := vscode.NormalizePathForComparison(projectPath)
	if err != nil {
		canonicalProject = projectPath
	}
	workspaceIDs := make(map[string]bool, len(workspaces))
	for _, ws := range workspaces {
		workspaceIDs[ws.ID] = true
	}

	for composerID, composer := range composers {
		if seen[composerID] || !composerBelongsToProject(composer, workspaceIDs, canonicalProject) {
			continue
		}
		seen[composerID] = true
		ids = append(ids, composerID)
	}

	slog.Debug("Resolved project composer IDs", "count", len(ids), "projectPath", projectPath)
	return ids, nil
}

// composerBelongsToProject implements the embedded-workspaceIdentifier match of
// FindProjectComposerIDs (source 2 in its doc comment).
func composerBelongsToProject(composer *ComposerData, workspaceIDs map[string]bool, canonicalProject string) bool {
	wi := composer.WorkspaceIdentifier
	if wi == nil {
		return false
	}
	if workspaceIDs[wi.ID] {
		return true
	}
	if wi.URI == nil || wi.URI.FsPath == "" {
		return false
	}
	canonicalComposer, err := vscode.NormalizePathForComparison(wi.URI.FsPath)
	if err != nil {
		canonicalComposer = wi.URI.FsPath
	}
	return canonicalComposer == canonicalProject
}

// LoadComposerIDsFromAllWorkspaces loads and deduplicates composer IDs from all matching workspaces.
// This handles WSL environments where the same project may have multiple workspace entries.
// NOTE: this only covers Cursor 2 / early Cursor 3 — newer versions stopped recording
// conversations in the workspace DB entirely; use FindProjectComposerIDs for full coverage.
func LoadComposerIDsFromAllWorkspaces(workspaces []vscode.WorkspaceEntry) ([]string, error) {
	seen := make(map[string]bool)
	var allIDs []string

	for _, ws := range workspaces {
		ids, err := LoadWorkspaceComposerIDs(ws.StateDBPath())
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

// ScanAllWorkspaceComposerPaths enumerates every Cursor IDE workspace directory and
// returns a map of composerID → project path (the OriginCwd for each session).
// When a composer appears in multiple workspaces (e.g. WSL/SSH setups), the first-seen
// workspace path is used. Workspaces that cannot be read are silently skipped so a
// single bad entry does not abort the whole enumeration.
func ScanAllWorkspaceComposerPaths() (map[string]string, error) {
	workspaceStoragePath, err := GetWorkspaceStoragePath()
	if err != nil {
		// No workspace storage directory means Cursor IDE has never been opened.
		slog.Debug("Workspace storage not found, no Cursor IDE sessions", "error", err)
		return map[string]string{}, nil
	}
	return scanWorkspaceDirForComposerPaths(workspaceStoragePath)
}

// scanWorkspaceDirForComposerPaths does the actual directory walk for
// ScanAllWorkspaceComposerPaths. It is a separate function so tests can call it
// directly with a temp directory rather than relying on the live Cursor installation.
func scanWorkspaceDirForComposerPaths(workspaceStoragePath string) (map[string]string, error) {
	entries, err := os.ReadDir(workspaceStoragePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace storage: %w", err)
	}

	composerToPath := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspacePath := filepath.Join(workspaceStoragePath, entry.Name())
		dbPath := filepath.Join(workspacePath, "state.vscdb")
		if _, statErr := os.Stat(dbPath); statErr != nil {
			continue // workspace directory has no database yet
		}

		workspaceJSON, jsonErr := vscode.ReadWorkspaceJSON(filepath.Join(workspacePath, "workspace.json"))
		if jsonErr != nil {
			slog.Debug("Skipping workspace (no valid workspace.json)",
				"workspace", entry.Name(), "error", jsonErr)
			continue
		}

		workspaceURI := workspaceJSON.PrimaryURI()
		if workspaceURI == "" {
			continue
		}

		projectPath, uriErr := vscode.URIToPath(workspaceURI)
		if uriErr != nil {
			slog.Debug("Skipping workspace (unresolvable URI)",
				"workspace", entry.Name(), "uri", workspaceURI, "error", uriErr)
			continue
		}

		composerIDs, idsErr := LoadWorkspaceComposerIDs(dbPath)
		if idsErr != nil {
			slog.Warn("Failed to load composer IDs from workspace",
				"workspace", entry.Name(), "error", idsErr)
			continue
		}

		for _, id := range composerIDs {
			if _, exists := composerToPath[id]; !exists {
				// First-seen workspace path wins; a composer scoped to multiple workspaces
				// (WSL/SSH setups) is attributed to whichever workspace is encountered first.
				composerToPath[id] = projectPath
			}
		}
	}

	slog.Debug("Scanned all workspaces for composer paths", "composerCount", len(composerToPath))
	return composerToPath, nil
}
