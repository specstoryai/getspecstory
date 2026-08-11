package cursoride

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
)

// resetUserDataDirOverride restores the package-level override after a test mutates it.
// Tests must defer this — leaking state into sibling tests would silently change their behavior.
func resetUserDataDirOverride(t *testing.T) {
	t.Helper()
	prev := userDataDirOverride
	t.Cleanup(func() { userDataDirOverride = prev })
}

// TestUserDataDirOverride_WorkspaceStorage verifies that an override pointing to a
// valid directory wins over OS-default discovery, and that a missing override path
// falls through (warn-and-fall-back, not hard error).
func TestUserDataDirOverride_WorkspaceStorage(t *testing.T) {
	resetUserDataDirOverride(t)

	// Build a fake user-data-dir: <tmp>/User/workspaceStorage
	tmp := t.TempDir()
	wantPath := filepath.Join(tmp, "User", "workspaceStorage")
	if err := os.MkdirAll(wantPath, 0755); err != nil {
		t.Fatalf("Failed to create fake workspaceStorage: %v", err)
	}

	SetUserDataDirOverride(tmp)
	got, err := GetWorkspaceStoragePath()
	if err != nil {
		t.Fatalf("GetWorkspaceStoragePath() with valid override returned error: %v", err)
	}
	if got != wantPath {
		t.Errorf("GetWorkspaceStoragePath() = %q, want %q", got, wantPath)
	}
}

// TestUserDataDirOverride_GlobalDatabase verifies override resolution for state.vscdb.
func TestUserDataDirOverride_GlobalDatabase(t *testing.T) {
	resetUserDataDirOverride(t)

	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "User", "globalStorage")
	if err := os.MkdirAll(globalDir, 0755); err != nil {
		t.Fatalf("Failed to create fake globalStorage: %v", err)
	}
	wantPath := filepath.Join(globalDir, "state.vscdb")
	if err := os.WriteFile(wantPath, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to write fake state.vscdb: %v", err)
	}

	SetUserDataDirOverride(tmp)
	got, err := GetGlobalDatabasePath()
	if err != nil {
		t.Fatalf("GetGlobalDatabasePath() with valid override returned error: %v", err)
	}
	if got != wantPath {
		t.Errorf("GetGlobalDatabasePath() = %q, want %q", got, wantPath)
	}
}

// TestUserDataDirOverride_MissingPathFallsThrough verifies that when the override is
// set but the derived path does not exist, the resolver falls through to OS-default
// discovery instead of failing fast. On systems without a real Cursor install, this
// surfaces as the normal "not found" error from the OS-default path — proving we did
// fall through and didn't get stuck on the bad override.
func TestUserDataDirOverride_MissingPathFallsThrough(t *testing.T) {
	resetUserDataDirOverride(t)

	// Override points at a directory that exists but has no User/workspaceStorage inside.
	override := t.TempDir()
	SetUserDataDirOverride(override)

	_, err := GetWorkspaceStoragePath()
	if err == nil {
		// If a real Cursor install happens to exist on this machine, the OS-default
		// branch succeeded — also a valid fall-through outcome. Either way, the override
		// must not have been used (we'd have failed before reaching here otherwise).
		return
	}
	// The error must come from the OS-default path, not from the override candidate.
	// The error message includes the path being checked; assert it doesn't mention
	// the override dir.
	if strings.Contains(err.Error(), override) {
		t.Errorf("expected fall-through to OS default after bad override, but error mentions override path: %v", err)
	}
}

// createWorkspaceDB builds a minimal workspace state.vscdb with the given composer IDs
// stored in the allComposers list under the composer.composerData key.
func createWorkspaceDB(t *testing.T, path string, composerIDs []string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("createWorkspaceDB: open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		_ = db.Close()
		t.Fatalf("createWorkspaceDB: create table: %v", err)
	}
	refs := WorkspaceComposerRefs{AllComposers: make([]ComposerRef, len(composerIDs))}
	for i, id := range composerIDs {
		refs.AllComposers[i] = ComposerRef{ComposerID: id}
	}
	value, _ := json.Marshal(refs)
	if _, err := db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", "composer.composerData", string(value)); err != nil {
		_ = db.Close()
		t.Fatalf("createWorkspaceDB: insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("createWorkspaceDB: close: %v", err)
	}
}

// TestScanWorkspaceDirForComposerPaths verifies that composer IDs are correctly mapped
// to their project paths by scanning a workspace storage directory.
func TestScanWorkspaceDirForComposerPaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Workspace 1: one composer under /project-a
	ws1 := filepath.Join(tmpDir, "ws1")
	if err := os.Mkdir(ws1, 0755); err != nil {
		t.Fatalf("mkdir ws1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws1, "workspace.json"), []byte(`{"folder":"file:///project-a"}`), 0644); err != nil {
		t.Fatalf("write workspace.json: %v", err)
	}
	createWorkspaceDB(t, filepath.Join(ws1, "state.vscdb"), []string{"composer-aaa"})

	// Workspace 2: two composers under /project-b
	ws2 := filepath.Join(tmpDir, "ws2")
	if err := os.Mkdir(ws2, 0755); err != nil {
		t.Fatalf("mkdir ws2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws2, "workspace.json"), []byte(`{"folder":"file:///project-b"}`), 0644); err != nil {
		t.Fatalf("write workspace.json: %v", err)
	}
	createWorkspaceDB(t, filepath.Join(ws2, "state.vscdb"), []string{"composer-bbb", "composer-ccc"})

	// Workspace 3: no workspace.json — should be silently skipped
	ws3 := filepath.Join(tmpDir, "ws3")
	if err := os.Mkdir(ws3, 0755); err != nil {
		t.Fatalf("mkdir ws3: %v", err)
	}
	createWorkspaceDB(t, filepath.Join(ws3, "state.vscdb"), []string{"composer-zzz"})

	// A plain file in the storage dir — must be ignored (not a directory)
	if err := os.WriteFile(filepath.Join(tmpDir, "not-a-dir.txt"), []byte(""), 0644); err != nil {
		t.Fatalf("write not-a-dir.txt: %v", err)
	}

	result, err := scanWorkspaceDirForComposerPaths(tmpDir)
	if err != nil {
		t.Fatalf("scanWorkspaceDirForComposerPaths: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 composer mappings, got %d: %v", len(result), result)
	}
	if result["composer-aaa"] != "/project-a" {
		t.Errorf("composer-aaa path = %q, want /project-a", result["composer-aaa"])
	}
	if result["composer-bbb"] != "/project-b" {
		t.Errorf("composer-bbb path = %q, want /project-b", result["composer-bbb"])
	}
	if result["composer-ccc"] != "/project-b" {
		t.Errorf("composer-ccc path = %q, want /project-b", result["composer-ccc"])
	}
	if _, ok := result["composer-zzz"]; ok {
		t.Error("composer-zzz from workspace without workspace.json should not appear")
	}
}

// TestScanWorkspaceDirForComposerPaths_DuplicateComposer verifies that when the same
// composer ID appears in multiple workspaces (e.g. WSL/SSH setups), the first-seen
// project path is used and no entry is duplicated.
func TestScanWorkspaceDirForComposerPaths_DuplicateComposer(t *testing.T) {
	tmpDir := t.TempDir()

	ws1 := filepath.Join(tmpDir, "ws1")
	if err := os.Mkdir(ws1, 0755); err != nil {
		t.Fatalf("mkdir ws1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws1, "workspace.json"), []byte(`{"folder":"file:///first-project"}`), 0644); err != nil {
		t.Fatalf("write workspace.json: %v", err)
	}
	createWorkspaceDB(t, filepath.Join(ws1, "state.vscdb"), []string{"shared-composer"})

	ws2 := filepath.Join(tmpDir, "ws2")
	if err := os.Mkdir(ws2, 0755); err != nil {
		t.Fatalf("mkdir ws2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws2, "workspace.json"), []byte(`{"folder":"file:///second-project"}`), 0644); err != nil {
		t.Fatalf("write workspace.json: %v", err)
	}
	createWorkspaceDB(t, filepath.Join(ws2, "state.vscdb"), []string{"shared-composer"})

	result, err := scanWorkspaceDirForComposerPaths(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entry (deduped), got %d: %v", len(result), result)
	}
	if result["shared-composer"] == "" {
		t.Error("expected shared-composer to have a non-empty path")
	}
}

// TestScanWorkspaceDirForComposerPaths_Empty verifies an empty storage directory
// returns an empty map without error.
func TestScanWorkspaceDirForComposerPaths_Empty(t *testing.T) {
	result, err := scanWorkspaceDirForComposerPaths(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d entries", len(result))
	}
}

// TestFindProjectComposerIDs covers the union of the two project-association sources:
// workspace-DB references (Cursor 2 / early 3) and the workspaceIdentifier embedded in
// global composerData rows (the only live source in Cursor >= 3.12), including
// deduplication and both embedded match modes (workspace storage ID and fsPath).
func TestFindProjectComposerIDs(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "myproj")
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	// Workspace DB carrying one old-style reference, which the global DB also lists
	// (with no workspaceIdentifier) to prove IDs are deduplicated across sources.
	wsDBPath := filepath.Join(tmp, "state.vscdb")
	createWorkspaceDB(t, wsDBPath, []string{"ws-composer"})
	workspaces := []vscode.WorkspaceEntry{{ID: "ws-hash-1", Dir: tmp}}

	marshal := func(c ComposerData) string {
		t.Helper()
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal composer: %v", err)
		}
		return string(b)
	}
	globalDbPath := createTestGlobalDB(t, map[string]string{
		"composerData:ws-composer": marshal(ComposerData{ComposerID: "ws-composer"}),
		"composerData:by-wsid": marshal(ComposerData{
			ComposerID:          "by-wsid",
			WorkspaceIdentifier: &ComposerWorkspaceIdentifier{ID: "ws-hash-1"},
		}),
		"composerData:by-fspath": marshal(ComposerData{
			ComposerID: "by-fspath",
			WorkspaceIdentifier: &ComposerWorkspaceIdentifier{
				ID:  "some-other-workspace-hash",
				URI: &ComposerWorkspaceURI{FsPath: projectDir},
			},
		}),
		"composerData:unrelated": marshal(ComposerData{
			ComposerID: "unrelated",
			WorkspaceIdentifier: &ComposerWorkspaceIdentifier{
				ID:  "nope",
				URI: &ComposerWorkspaceURI{FsPath: filepath.Join(tmp, "otherproj")},
			},
		}),
	})

	ids, err := FindProjectComposerIDs(globalDbPath, projectDir, workspaces)
	if err != nil {
		t.Fatalf("FindProjectComposerIDs: %v", err)
	}

	got := make(map[string]bool, len(ids))
	for _, id := range ids {
		if got[id] {
			t.Errorf("duplicate composer ID %q in result", id)
		}
		got[id] = true
	}
	for _, want := range []string{"ws-composer", "by-wsid", "by-fspath"} {
		if !got[want] {
			t.Errorf("expected composer %q in result, got %v", want, ids)
		}
	}
	if got["unrelated"] {
		t.Errorf("composer %q from another project must not match, got %v", "unrelated", ids)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 composer IDs, got %d: %v", len(ids), ids)
	}
}

// TestEnsureWorkspaceForProject_MintsEntry verifies that a project never opened in
// Cursor gets a workspace entry minted with Cursor's own ID scheme, and that the
// minted entry round-trips through this provider's own workspace discovery.
func TestEnsureWorkspaceForProject_MintsEntry(t *testing.T) {
	storage := t.TempDir()
	origStorage := GetWorkspaceStoragePath
	GetWorkspaceStoragePath = func() (string, error) { return storage, nil }
	t.Cleanup(func() { GetWorkspaceStoragePath = origStorage })

	projectDir := filepath.Join(t.TempDir(), "fresh-project")
	if err := os.Mkdir(projectDir, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	ws, err := EnsureWorkspaceForProject(projectDir)
	if err != nil {
		t.Fatalf("EnsureWorkspaceForProject: %v", err)
	}

	// The ID must be Cursor's scheme: md5(path + platform stat salt).
	wantID, err := vscode.WorkspaceID(projectDir)
	if err != nil {
		t.Fatalf("cursorWorkspaceID: %v", err)
	}
	if ws.ID != wantID {
		t.Errorf("workspace ID = %q, want %q", ws.ID, wantID)
	}

	// workspace.json must parse and carry the folder URI.
	wj, err := vscode.ReadWorkspaceJSON(filepath.Join(ws.Dir, "workspace.json"))
	if err != nil {
		t.Fatalf("minted workspace.json unreadable: %v", err)
	}
	if wj.Folder != vscode.PathToFileURI(projectDir) {
		t.Errorf("workspace.json folder = %q, want %q", wj.Folder, vscode.PathToFileURI(projectDir))
	}

	// The state.vscdb must exist with a usable ItemTable (our own readers query it).
	if _, err := LoadWorkspaceComposerIDs(ws.StateDBPath()); err != nil {
		t.Errorf("minted state.vscdb not readable: %v", err)
	}

	// Round-trip: the provider's own workspace discovery must now find the entry.
	matches, err := FindAllWorkspacesForProject(projectDir)
	if err != nil {
		t.Fatalf("FindAllWorkspacesForProject after minting: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != wantID {
		t.Errorf("expected minted workspace to be discovered, got %+v", matches)
	}

	// A second call must reuse the entry, not mint a duplicate.
	ws2, err := EnsureWorkspaceForProject(projectDir)
	if err != nil {
		t.Fatalf("second EnsureWorkspaceForProject: %v", err)
	}
	if ws2.ID != wantID {
		t.Errorf("second call returned ID %q, want %q", ws2.ID, wantID)
	}
	entries, err := os.ReadDir(storage)
	if err != nil {
		t.Fatalf("read storage dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 workspace entry after two calls, got %d", len(entries))
	}
}
