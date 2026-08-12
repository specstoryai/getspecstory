package vscode

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
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
	// The URI round-trip lowercases the drive letter on Windows (fileURIParts
	// matches the IDE's own serialization), so compare with the platform's rules.
	if got, err := URIToPath(wsJSON.Folder); err != nil || !testutil.EqualPaths(got, projectDir) {
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

// TestFileURIParts verifies the VS Code-style URI serialization used for
// workspaceIdentifier.uri, in particular the Windows drive-letter normalization
// (lowercase drive, forward-slash URI path, %3A-encoded colon in external).
func TestFileURIParts(t *testing.T) {
	tests := []struct {
		name         string
		osPath       string
		wantFSPath   string
		wantURIPath  string
		wantExternal string
	}{
		{
			name:         "unix path",
			osPath:       "/home/user/proj",
			wantFSPath:   "/home/user/proj",
			wantURIPath:  "/home/user/proj",
			wantExternal: "file:///home/user/proj",
		},
		{
			name:         "unix path with space",
			osPath:       "/home/user/my proj",
			wantFSPath:   "/home/user/my proj",
			wantURIPath:  "/home/user/my proj",
			wantExternal: "file:///home/user/my%20proj",
		},
		{
			name:         "windows path uppercase drive",
			osPath:       `C:\Users\Admin\proj`,
			wantFSPath:   `c:\Users\Admin\proj`,
			wantURIPath:  "/c:/Users/Admin/proj",
			wantExternal: "file:///c%3A/Users/Admin/proj",
		},
		{
			name:         "windows path lowercase drive",
			osPath:       `c:\Users\Admin\proj`,
			wantFSPath:   `c:\Users\Admin\proj`,
			wantURIPath:  "/c:/Users/Admin/proj",
			wantExternal: "file:///c%3A/Users/Admin/proj",
		},
		{
			name:         "windows path with space",
			osPath:       `C:\Users\Admin\my proj`,
			wantFSPath:   `c:\Users\Admin\my proj`,
			wantURIPath:  "/c:/Users/Admin/my proj",
			wantExternal: "file:///c%3A/Users/Admin/my%20proj",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsPath, uriPath, external := fileURIParts(tt.osPath)
			if fsPath != tt.wantFSPath {
				t.Errorf("fsPath = %q, want %q", fsPath, tt.wantFSPath)
			}
			if uriPath != tt.wantURIPath {
				t.Errorf("uriPath = %q, want %q", uriPath, tt.wantURIPath)
			}
			if external != tt.wantExternal {
				t.Errorf("external = %q, want %q", external, tt.wantExternal)
			}
		})
	}
}

// TestWorkspaceURIMap_SepMarker verifies the "_sep" marker is emitted for Windows paths and
// omitted for Unix ones, matching VS Code's _pathSepMarker (1 on Windows, undefined elsewhere).
// URI.revive() drops the cached fsPath when the marker is absent, so native Windows rows
// always carry it.
func TestWorkspaceURIMap_SepMarker(t *testing.T) {
	tests := []struct {
		name    string
		osPath  string
		wantSep bool
	}{
		{name: "windows path gets marker", osPath: `C:\Users\Admin\proj`, wantSep: true},
		{name: "windows forward slash path gets marker", osPath: "c:/Users/Admin/proj", wantSep: true},
		{name: "unix path has no marker", osPath: "/home/user/proj", wantSep: false},
		{name: "macos path has no marker", osPath: "/Users/bago/code/proj", wantSep: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri := WorkspaceURIMap(tt.osPath)
			sep, ok := uri["_sep"]
			if tt.wantSep {
				if !ok {
					t.Fatalf("_sep missing for %q, want 1", tt.osPath)
				}
				if sep != 1 {
					t.Errorf("_sep = %v, want 1", sep)
				}
			} else if ok {
				t.Errorf("_sep = %v for %q, want it absent", sep, tt.osPath)
			}
		})
	}
}

func TestCodeWorkspaceContainsFolder(t *testing.T) {
	// Create a temporary directory structure.
	tmpDir := t.TempDir()

	// Create the target project folder.
	projectDir := filepath.Join(tmpDir, "my-project")
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatalf("Failed to create project dir: %v", err)
	}
	// Resolve symlinks so the canonical path matches what normalizePathForComparison returns
	// (e.g. /var → /private/var on macOS).
	canonicalProjectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		canonicalProjectDir = projectDir
	}

	// Create a workspace file in a sibling directory (common real-world pattern).
	workspacesDir := filepath.Join(tmpDir, "workspaces")
	if err := os.Mkdir(workspacesDir, 0755); err != nil {
		t.Fatalf("Failed to create workspaces dir: %v", err)
	}
	workspaceFile := filepath.Join(workspacesDir, "my-project.code-workspace")

	writeWorkspaceFile := func(content string) {
		if err := os.WriteFile(workspaceFile, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to write workspace file: %v", err)
		}
	}

	// nonExistentFile is a path that does not exist locally, simulating a remote-SSH workspace file.
	nonExistentFile := filepath.Join(tmpDir, "remote-host", "my-project", "my-project.code-workspace")
	// parentOfNonExistent is the directory that contains the non-existent file.
	parentOfNonExistent := filepath.Dir(nonExistentFile)

	tests := []struct {
		name             string
		workspaceContent string // empty means use nonExistentFile (skip writeWorkspaceFile)
		workspaceFile    string
		targetFolder     string
		isRemote         bool
		expected         bool
	}{
		{
			name:             "relative path that resolves to target folder",
			workspaceContent: `{"folders": [{"path": "../my-project"}]}`,
			workspaceFile:    workspaceFile,
			targetFolder:     canonicalProjectDir,
			isRemote:         false,
			expected:         true,
		},
		{
			name:             "absolute path matching target folder",
			workspaceContent: `{"folders": [{"path": "` + projectDir + `"}]}`,
			workspaceFile:    workspaceFile,
			targetFolder:     canonicalProjectDir,
			isRemote:         false,
			expected:         true,
		},
		{
			name:             "no folders entry matching target",
			workspaceContent: `{"folders": [{"path": "../other-project"}]}`,
			workspaceFile:    workspaceFile,
			targetFolder:     canonicalProjectDir,
			isRemote:         false,
			expected:         false,
		},
		{
			name:             "empty folders array",
			workspaceContent: `{"folders": []}`,
			workspaceFile:    workspaceFile,
			targetFolder:     canonicalProjectDir,
			isRemote:         false,
			expected:         false,
		},
		{
			name:             "malformed JSON",
			workspaceContent: `not json`,
			workspaceFile:    workspaceFile,
			targetFolder:     canonicalProjectDir,
			isRemote:         false,
			expected:         false,
		},
		// Remote-SSH fallback: file doesn't exist locally but isRemote=true and the target
		// folder matches the workspace file's parent directory.
		{
			name:          "remote SSH workspace file unreadable, parent dir matches project path",
			workspaceFile: nonExistentFile,
			targetFolder:  parentOfNonExistent,
			isRemote:      true,
			expected:      true,
		},
		// Remote-SSH fallback: file doesn't exist but the target folder is NOT the parent dir.
		{
			name:          "remote SSH workspace file unreadable, parent dir does not match",
			workspaceFile: nonExistentFile,
			targetFolder:  canonicalProjectDir,
			isRemote:      true,
			expected:      false,
		},
		// Non-remote: deleted local file should not trigger the parent-dir fallback.
		{
			name:          "local workspace file missing, isRemote false, no fallback",
			workspaceFile: nonExistentFile,
			targetFolder:  parentOfNonExistent,
			isRemote:      false,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.workspaceContent != "" {
				writeWorkspaceFile(tt.workspaceContent)
			}
			result := codeWorkspaceContainsFolder(tt.workspaceFile, tt.targetFolder, tt.isRemote)
			if result != tt.expected {
				t.Errorf("codeWorkspaceContainsFolder() = %v, want %v", result, tt.expected)
			}
		})
	}
}
