package vscode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // Pure Go SQLite driver, for the minted state.vscdb

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// MintWorkspace creates a workspace storage entry for folderPath under
// storageRoot, for projects never opened in the IDE: without it, targeting the
// project (e.g. reconstructing a session into it) would fail with "open the
// folder once first". The entry uses the IDE's own workspace ID for the path
// as given (see WorkspaceID for the spelling policy), so when the user later
// opens the folder the IDE computes the same ID and adopts the minted entry
// instead of creating a duplicate.
func MintWorkspace(storageRoot, folderPath string) (*WorkspaceEntry, error) {
	id, err := WorkspaceID(folderPath)
	if err != nil {
		return nil, fmt.Errorf("cannot determine workspace ID for %q: %w", folderPath, err)
	}

	entry := &WorkspaceEntry{
		ID:           id,
		Dir:          filepath.Join(storageRoot, id),
		URI:          PathToFileURI(folderPath),
		ResolvedPath: folderPath,
	}

	if err := os.MkdirAll(entry.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create workspace storage directory: %w", err)
	}
	// workspace.json mirrors the format the IDE writes (2-space indented single field).
	workspaceJSON, err := json.MarshalIndent(WorkspaceJSON{Folder: entry.URI}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal workspace.json: %w", err)
	}
	if err := os.WriteFile(entry.MetadataPath(), workspaceJSON, 0644); err != nil {
		return nil, fmt.Errorf("failed to write workspace.json: %w", err)
	}
	if err := CreateEmptyStateDB(entry.StateDBPath()); err != nil {
		return nil, err
	}

	slog.Info("Minted workspace entry for project",
		"workspaceID", id, "folderPath", folderPath, "storageRoot", storageRoot)

	return entry, nil
}

// CreateEmptyStateDB creates a state.vscdb containing the IDE's exact
// ItemTable schema (key UNIQUE ON CONFLICT REPLACE, BLOB value), so the IDE
// adopts a minted workspace entry as its own on first open instead of treating
// it as corrupt — and so index writes during session reconstruction have a
// table to land in.
func CreateEmptyStateDB(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath+"?"+spi.BusyTimeoutPragma)
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
