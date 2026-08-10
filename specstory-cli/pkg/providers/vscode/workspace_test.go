package vscode

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeWorkspaceEntry creates a workspace storage entry with the given
// workspace.json content, returning its directory.
func writeWorkspaceEntry(t *testing.T, storageRoot, id, workspaceJSON string) string {
	t.Helper()
	dir := filepath.Join(storageRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.json"), []byte(workspaceJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestURIToPath(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{"plain path", "file:///Users/me/proj", "/Users/me/proj", false},
		{"space decoded", "file:///Users/me/My%20Project", "/Users/me/My Project", false},
		{"unicode decoded", "file:///Users/me/caf%C3%A9", "/Users/me/café", false},
		{"literal percent decoded exactly once", "file:///Users/me/literal%2520pct", "/Users/me/literal%20pct", false},
		{"remote SSH URI yields remote path", "vscode-remote://ssh-remote%2Bmyhost/home/me/proj", "/home/me/proj", false},
		{"remote WSL URI yields in-distro path", "vscode-remote://wsl%2Bubuntu/home/me/proj", "/home/me/proj", false},
		{"dev container URI yields container path", "vscode-remote://dev-container%2Babc123/workspaces/proj", "/workspaces/proj", false},
		{"unsupported scheme rejected", "https://example.com/proj", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := URIToPath(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("URIToPath(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("URIToPath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestPathToFileURI(t *testing.T) {
	if got := PathToFileURI("/Users/me/My Project"); got != "file:///Users/me/My%20Project" {
		t.Errorf("PathToFileURI = %q, want percent-encoded file URI", got)
	}
}

func TestFindWorkspaces(t *testing.T) {
	storageRoot := t.TempDir()
	projectDir := t.TempDir()
	canonicalProject, err := NormalizePathForComparison(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	// Entry matching the project directly.
	direct := writeWorkspaceEntry(t, storageRoot, "direct",
		`{"folder": "`+PathToFileURI(canonicalProject)+`"}`)
	// Entry for an unrelated folder.
	writeWorkspaceEntry(t, storageRoot, "other",
		`{"folder": "file:///somewhere/else"}`)
	// Remote entry whose basename matches the project's.
	writeWorkspaceEntry(t, storageRoot, "remote",
		`{"folder": "vscode-remote://ssh-remote%2Bbox/home/user/`+filepath.Base(canonicalProject)+`"}`)
	// Local entry with the same basename must NOT basename-match.
	writeWorkspaceEntry(t, storageRoot, "local-samename",
		`{"folder": "file:///elsewhere/`+filepath.Base(canonicalProject)+`"}`)
	// Malformed entry is skipped, not fatal.
	writeWorkspaceEntry(t, storageRoot, "broken", `{not json`)

	entries, err := FindWorkspaces(storageRoot, projectDir, MatchOptions{})
	if err != nil {
		t.Fatalf("FindWorkspaces: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.ID] = true
	}
	if !ids["direct"] || !ids["remote"] {
		t.Errorf("expected direct+remote matches, got %v", ids)
	}
	if ids["other"] || ids["local-samename"] || ids["broken"] {
		t.Errorf("unexpected matches present: %v", ids)
	}

	// RequireFile filters entries lacking the artifact.
	if err := os.WriteFile(filepath.Join(direct, "state.vscdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	filtered, err := FindWorkspaces(storageRoot, projectDir, MatchOptions{RequireFile: "state.vscdb"})
	if err != nil {
		t.Fatalf("FindWorkspaces filtered: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "direct" {
		t.Errorf("RequireFile filter kept %v, want only direct", filtered)
	}

	// No matches is an empty slice, not an error.
	none, err := FindWorkspaces(storageRoot, t.TempDir(), MatchOptions{})
	if err != nil || len(none) != 0 {
		t.Errorf("no-match case = (%v, %v), want empty and nil", none, err)
	}
}

func TestFindWorkspaces_CodeWorkspaceMethods(t *testing.T) {
	storageRoot := t.TempDir()
	base := t.TempDir()
	projectDir := filepath.Join(base, "my-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalProject, err := NormalizePathForComparison(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	// A .code-workspace file listing the project folder (relative path).
	wsFile := filepath.Join(base, "multi.code-workspace")
	if err := os.WriteFile(wsFile, []byte(`{"folders": [{"path": "my-project"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalWsFile, err := NormalizePathForComparison(wsFile)
	if err != nil {
		t.Fatal(err)
	}

	// Method 3: entry stores the .code-workspace file; project is a listed folder.
	writeWorkspaceEntry(t, storageRoot, "via-wsfile",
		`{"workspace": "`+PathToFileURI(canonicalWsFile)+`"}`)
	// Method 4: project path IS the .code-workspace file; entry is a listed folder.
	writeWorkspaceEntry(t, storageRoot, "via-folder",
		`{"folder": "`+PathToFileURI(canonicalProject)+`"}`)

	byFolder, err := FindWorkspaces(storageRoot, projectDir, MatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, e := range byFolder {
		found[e.ID] = true
	}
	if !found["via-wsfile"] || !found["via-folder"] {
		t.Errorf("folder lookup should match both methods, got %v", found)
	}

	byWsFile, err := FindWorkspaces(storageRoot, wsFile, MatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found = map[string]bool{}
	for _, e := range byWsFile {
		found[e.ID] = true
	}
	if !found["via-wsfile"] || !found["via-folder"] {
		t.Errorf(".code-workspace lookup should match both methods, got %v", found)
	}
}

func TestSelectPrimary(t *testing.T) {
	storageRoot := t.TempDir()
	projectDir := t.TempDir()
	canonicalProject, err := NormalizePathForComparison(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	// Case-variant entry (stale) vs case-exact entry: exact must win even when
	// the variant's state.vscdb is newer.
	variantDir := writeWorkspaceEntry(t, storageRoot, "variant", `{}`)
	exactDir := writeWorkspaceEntry(t, storageRoot, "exact", `{}`)
	if err := os.WriteFile(filepath.Join(exactDir, "state.vscdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(variantDir, "state.vscdb"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(variantDir, "state.vscdb"), future, future); err != nil {
		t.Fatal(err)
	}

	entries := []WorkspaceEntry{
		{ID: "variant", Dir: variantDir, ResolvedPath: strings.ToLower(canonicalProject) + "-x"},
		{ID: "exact", Dir: exactDir, ResolvedPath: canonicalProject},
	}
	if got := SelectPrimary(entries, projectDir); got == nil || got.ID != "exact" {
		t.Errorf("SelectPrimary = %v, want case-exact entry", got)
	}

	// With no exact entry, the newest state.vscdb wins.
	entries[1].ResolvedPath = canonicalProject + "-y"
	if got := SelectPrimary(entries, projectDir); got == nil || got.ID != "variant" {
		t.Errorf("SelectPrimary mtime tie-break = %v, want newest (variant)", got)
	}

	if got := SelectPrimary(nil, projectDir); got != nil {
		t.Errorf("SelectPrimary(nil) = %v, want nil", got)
	}
}

func TestMintWorkspace(t *testing.T) {
	storageRoot := t.TempDir()
	projectDir := t.TempDir()

	entry, err := MintWorkspace(storageRoot, projectDir)
	if err != nil {
		t.Fatalf("MintWorkspace: %v", err)
	}

	wantID, err := WorkspaceID(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != wantID {
		t.Errorf("minted ID = %q, want the IDE's own %q", entry.ID, wantID)
	}

	wsJSON, err := ReadWorkspaceJSON(entry.MetadataPath())
	if err != nil {
		t.Fatalf("minted workspace.json unreadable: %v", err)
	}
	if got, err := URIToPath(wsJSON.Folder); err != nil || got != projectDir {
		t.Errorf("workspace.json folder = %q (%v), want %q", got, err, projectDir)
	}

	db, err := sql.Open("sqlite", entry.StateDBPath())
	if err != nil {
		t.Fatalf("minted state.vscdb unopenable: %v", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM ItemTable").Scan(&n); err != nil {
		t.Errorf("minted state.vscdb lacks ItemTable: %v", err)
	}
}

func TestOpenApp_MissingLaunchers(t *testing.T) {
	// Default launcher missing → the sentinel, so callers print install guidance.
	err := OpenApp("VS Test", "definitely-not-a-real-launcher-xyz", "", t.TempDir())
	if !errors.Is(err, ErrCLIMissing) {
		t.Errorf("missing default launcher error = %v, want ErrCLIMissing", err)
	}
	// Missing custom launcher → a plain error (install guidance doesn't apply).
	err = OpenApp("VS Test", "code", "also-not-real-xyz --flag", t.TempDir())
	if err == nil || errors.Is(err, ErrCLIMissing) {
		t.Errorf("missing custom launcher error = %v, want plain error", err)
	}
}
