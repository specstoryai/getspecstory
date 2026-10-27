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

// WorkspacePathIndex is the result of walking the workspace storage directory:
// every workspace entry resolved to a project directory, keyed both by the
// workspace storage ID (what a composer's embedded workspaceIdentifier carries)
// and by the composer IDs the workspace database references.
type WorkspacePathIndex struct {
	// ByWorkspaceID maps a workspace storage directory name to its project path.
	ByWorkspaceID map[string]string
	// ByComposer maps a composer ID referenced by a workspace database to that
	// workspace's project path. First-seen workspace wins for composers that
	// appear in several (WSL/SSH setups).
	ByComposer map[string]string
}

// ScanAllWorkspacePaths walks every workspace storage entry and resolves each one
// to a project directory, indexed by workspace ID and by referenced composer ID.
// Multi-root workspaces resolve to their first listed folder (see
// scanWorkspaceDir). An empty index, not an error, when Cursor has no workspace
// storage yet.
func ScanAllWorkspacePaths() (*WorkspacePathIndex, error) {
	workspaceStoragePath, err := GetWorkspaceStoragePath()
	if err != nil {
		slog.Debug("Workspace storage not found, no Cursor IDE sessions", "error", err)
		return &WorkspacePathIndex{ByWorkspaceID: map[string]string{}, ByComposer: map[string]string{}}, nil
	}
	return scanWorkspaceDir(workspaceStoragePath)
}

// scanWorkspaceDirForComposerPaths is the composer-keyed view of scanWorkspaceDir,
// kept for callers and tests that only need the composer mapping.
func scanWorkspaceDirForComposerPaths(workspaceStoragePath string) (map[string]string, error) {
	index, err := scanWorkspaceDir(workspaceStoragePath)
	if err != nil {
		return nil, err
	}
	return index.ByComposer, nil
}

// scanWorkspaceDir does the actual directory walk for ScanAllWorkspacePaths. It is
// a separate function so tests can call it directly with a temp directory rather
// than relying on the live Cursor installation.
func scanWorkspaceDir(workspaceStoragePath string) (*WorkspacePathIndex, error) {
	entries, err := os.ReadDir(workspaceStoragePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace storage: %w", err)
	}

	index := &WorkspacePathIndex{
		ByWorkspaceID: make(map[string]string),
		ByComposer:    make(map[string]string),
	}
	composerToPath := index.ByComposer
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspacePath := filepath.Join(workspaceStoragePath, entry.Name())
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

		// A multi-root workspace resolves to its workspace file, not a project directory.
		// Attribute its composers to the first listed folder instead: that is the folder
		// the extension runs the CLI in, and the one FindWorkspaces matches back to this
		// entry, so the path is one a sync can actually run from. Unreadable files
		// (e.g. remote workspaces) keep the file path as before.
		if workspaceJSON.Workspace != "" {
			if firstFolder := vscode.FirstWorkspaceFolder(projectPath); firstFolder != "" {
				projectPath = firstFolder
			}
		}
		index.ByWorkspaceID[entry.Name()] = projectPath

		dbPath := filepath.Join(workspacePath, "state.vscdb")
		if _, statErr := os.Stat(dbPath); statErr != nil {
			continue // workspace directory has no database yet, so no composer references
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

	slog.Debug("Scanned all workspaces for project paths",
		"workspaceCount", len(index.ByWorkspaceID), "composerCount", len(composerToPath))
	return index, nil
}

// resolveComposerProjectPath picks the project directory a composer belongs to for
// the global session listing, or "" when none is known. The embedded
// workspaceIdentifier is the live association in Cursor >= 3.12, so its workspace
// entry is consulted first: that resolves multi-root workspaces to their first
// folder, and covers identifiers that carry only an ID (multi-root windows have no
// single folder URI). The identifier's folder URI is next, for an entry that no
// longer exists in workspace storage, and the workspace-DB reference last, for
// composers that predate the embedded field.
func resolveComposerProjectPath(composerID string, composer *ComposerData, index *WorkspacePathIndex) string {
	if wi := composer.WorkspaceIdentifier; wi != nil {
		if path := index.ByWorkspaceID[wi.ID]; path != "" {
			return path
		}
		if wi.URI != nil && wi.URI.FsPath != "" {
			return wi.URI.FsPath
		}
	}
	return index.ByComposer[composerID]
}
