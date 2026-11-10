package cursoride

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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

// TestScanWorkspaceDir_ComposerPaths verifies that composer IDs are correctly mapped
// to their project paths by scanning a workspace storage directory.
func TestScanWorkspaceDir_ComposerPaths(t *testing.T) {
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

	index, err := scanWorkspaceDir(tmpDir)
	if err != nil {
		t.Fatalf("scanWorkspaceDir: %v", err)
	}
	result := index.ByComposer

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

// TestScanWorkspaceDir_DuplicateComposer verifies that when the same
// composer ID appears in multiple workspaces (e.g. WSL/SSH setups), the first-seen
// project path is used and no entry is duplicated.
func TestScanWorkspaceDir_DuplicateComposer(t *testing.T) {
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

	index, err := scanWorkspaceDir(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result := index.ByComposer
	if len(result) != 1 {
		t.Fatalf("expected 1 entry (deduped), got %d: %v", len(result), result)
	}
	if result["shared-composer"] == "" {
		t.Error("expected shared-composer to have a non-empty path")
	}
}

// TestScanWorkspaceDir_Empty verifies an empty storage directory
// returns an empty index without error.
func TestScanWorkspaceDir_Empty(t *testing.T) {
	index, err := scanWorkspaceDir(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(index.ByWorkspaceID) != 0 || len(index.ByComposer) != 0 {
		t.Errorf("expected an empty index, got %d workspaces and %d composers",
			len(index.ByWorkspaceID), len(index.ByComposer))
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

func TestCheckDatabaseFailures(t *testing.T) {
	resetUserDataDirOverride(t)
	SetUserDataDirOverride("")
	home := t.TempDir()
	testutil.SetHome(t, home)
	t.Setenv("APPDATA", home)
	// Use the production resolver to ensure missing-file errors survive wrapping.
	if !spi.IsWSL() {
		result := NewProvider().Check("")
		if result.Success || result.ErrorType != spi.CheckErrorNotFound {
			t.Fatalf("absent database = %+v", result)
		}
	}
	original := GetGlobalDatabasePath
	t.Cleanup(func() { GetGlobalDatabasePath = original })
	for _, tt := range []struct{ name, want string }{
		{"valid", ""}, {"corrupt", spi.CheckErrorUnknown},
		{"directory", spi.CheckErrorUnknown}, {"unreadable", spi.CheckErrorPermissionDenied},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "unreadable" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("requires Unix permissions enforced for a non-root user")
			}
			path := filepath.Join(t.TempDir(), "state.vscdb")
			GetGlobalDatabasePath = func() (string, error) { return path, nil }
			switch tt.name {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "valid":
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				_, createErr := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)")
				closeErr := db.Close()
				if createErr != nil {
					t.Fatal(createErr)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			default:
				mode := os.FileMode(0600)
				if tt.name == "unreadable" {
					mode = 0
				}
				if err := os.WriteFile(path, []byte("not a SQLite database"), mode); err != nil {
					t.Fatal(err)
				}
			}
			result := NewProvider().Check("")
			if result.ErrorType != tt.want || result.Success != (tt.want == "") {
				t.Fatalf("Check = %+v, want ErrorType %q", result, tt.want)
			}
		})
	}
}

// A multi-root entry (an untitled window's workspace.json or a saved .code-workspace)
// resolves to its workspace file, not a project directory. It must be indexed under its
// first listed folder, the project the extension and FindWorkspaces use: by workspace ID
// always, and by the composers its database references when it has one. An entry with
// no database yet is still indexed by ID, because a composer's embedded
// workspaceIdentifier can name it.
func TestScanWorkspaceDir_MultiRoot(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "monorepo")
	second := filepath.Join(base, "cli")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	canonicalFirst, err := vscode.NormalizePathForComparison(first)
	if err != nil {
		t.Fatal(err)
	}
	folders := []byte(`{"folders":[{"path":` + testutil.JSONString(first) + `},{"path":` + testutil.JSONString(second) + `}]}`)

	// Mirror Cursor's layout for an untitled window: <user data>/Workspaces/<timestamp>/workspace.json.
	untitledDir := filepath.Join(base, "Workspaces", "1793105102369")
	if err := os.MkdirAll(untitledDir, 0755); err != nil {
		t.Fatal(err)
	}
	untitledFile := filepath.Join(untitledDir, "workspace.json")
	savedFile := filepath.Join(base, "multi.code-workspace")
	for _, file := range []string{untitledFile, savedFile} {
		if err := os.WriteFile(file, folders, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// An SSH workspace whose remote path happens to name a real local multi-root file.
	// The remote host is dropped when the URI becomes a path, so reading that local file
	// would attribute the remote sessions to an unrelated local project; the entry must
	// keep its remote workspace-file path instead.
	remoteURI := "vscode-remote://ssh-remote%2Bdevbox" + filepath.ToSlash(savedFile)
	remotePath, err := vscode.URIToPath(remoteURI)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		workspaceURI string
		withDB       bool
		want         string
	}{
		{name: "untitled window with a database", workspaceURI: vscode.PathToFileURI(untitledFile), withDB: true, want: canonicalFirst},
		{name: "untitled window without a database", workspaceURI: vscode.PathToFileURI(untitledFile), withDB: false, want: canonicalFirst},
		{name: "saved .code-workspace with a database", workspaceURI: vscode.PathToFileURI(savedFile), withDB: true, want: canonicalFirst},
		{name: "saved .code-workspace without a database", workspaceURI: vscode.PathToFileURI(savedFile), withDB: false, want: canonicalFirst},
		{name: "SSH workspace is not read from the local filesystem", workspaceURI: remoteURI, withDB: true, want: remotePath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.HasPrefix(tt.workspaceURI, "vscode-remote://") {
				// A drive-letter path can't stand in for a remote POSIX path in the URI.
				t.Skip("remote workspace URIs carry POSIX paths")
			}
			storage := t.TempDir()
			writeEntry := func(id, workspaceJSON string, composerIDs []string) {
				dir := filepath.Join(storage, id)
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "workspace.json"), []byte(workspaceJSON), 0644); err != nil {
					t.Fatal(err)
				}
				if composerIDs != nil {
					createWorkspaceDB(t, filepath.Join(dir, "state.vscdb"), composerIDs)
				}
			}
			var multiComposers []string
			if tt.withDB {
				multiComposers = []string{"composer-multi"}
			}
			writeEntry("ws-multi", `{"workspace":"`+tt.workspaceURI+`"}`, multiComposers)
			// A single-folder neighbor shows multi-root resolution stays within its entry.
			writeEntry("ws-folder", `{"folder":"file:///project-a"}`, []string{"composer-aaa"})

			index, err := scanWorkspaceDir(storage)
			if err != nil {
				t.Fatalf("scanWorkspaceDir: %v", err)
			}
			if got := index.ByWorkspaceID["ws-multi"]; got != tt.want {
				t.Errorf("ws-multi path = %q, want %q", got, tt.want)
			}
			if got := index.ByWorkspaceID["ws-folder"]; got != "/project-a" {
				t.Errorf("ws-folder path = %q, want /project-a", got)
			}
			if got := index.ByComposer["composer-aaa"]; got != "/project-a" {
				t.Errorf("composer-aaa path = %q, want /project-a", got)
			}
			wantComposers := 1
			if tt.withDB {
				wantComposers = 2
				if got := index.ByComposer["composer-multi"]; got != tt.want {
					t.Errorf("composer-multi path = %q, want %q", got, tt.want)
				}
			}
			if len(index.ByComposer) != wantComposers {
				t.Errorf("ByComposer should hold only referenced composers, got %v", index.ByComposer)
			}
		})
	}
}

// resolveComposerProjectPath covers the association shapes seen in real databases:
// an ID-only identifier for a multi-root window, an identifier with a folder URI,
// an identifier whose entry is gone, and legacy composers known only to a
// workspace database.
func TestResolveComposerProjectPath(t *testing.T) {
	// Every source maps to a distinct path, so each case can only pass by resolving
	// through the source it names: a wrong precedence order returns another source's
	// path. The workspace-DB paths are what a lower-priority fallback would yield.
	index := &workspacePathIndex{
		ByWorkspaceID: map[string]string{
			"ws-multi":  "/code/monorepo", // first folder of a multi-root workspace
			"ws-single": "/code/single",
		},
		ByComposer: map[string]string{
			"in-db":  "/from-workspace-db",
			"legacy": "/code/legacy",
		},
	}
	identifier := func(id, fsPath string) *ComposerData {
		wi := &ComposerWorkspaceIdentifier{ID: id}
		if fsPath != "" {
			wi.URI = &ComposerWorkspaceURI{FsPath: fsPath}
		}
		return &ComposerData{WorkspaceIdentifier: wi}
	}

	tests := []struct {
		name       string
		composerID string
		composer   *ComposerData
		want       string
	}{
		{
			name:       "id-only identifier for a multi-root window resolves to the first folder",
			composerID: "in-db",
			composer:   identifier("ws-multi", ""),
			want:       "/code/monorepo",
		},
		{
			name:       "workspace entry wins over the identifier's workspace file URI",
			composerID: "in-db",
			composer:   identifier("ws-multi", "/code/multi.code-workspace"),
			want:       "/code/monorepo",
		},
		{
			name:       "single-folder identifier resolves through its entry",
			composerID: "in-db",
			composer:   identifier("ws-single", "/code/single"),
			want:       "/code/single",
		},
		{
			name:       "entry gone from workspace storage falls back to the identifier URI before the DB",
			composerID: "in-db",
			composer:   identifier("ws-deleted", "/code/moved"),
			want:       "/code/moved",
		},
		{
			name:       "id-only identifier with no entry falls back to the workspace-DB reference",
			composerID: "in-db",
			composer:   identifier("ws-deleted", ""),
			want:       "/from-workspace-db",
		},
		{
			name:       "id-only identifier with no entry and no DB reference has no path",
			composerID: "unknown",
			composer:   identifier("ws-deleted", ""),
			want:       "",
		},
		{
			name:       "legacy composer without identifier uses the workspace-DB reference",
			composerID: "legacy",
			composer:   &ComposerData{},
			want:       "/code/legacy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveComposerProjectPath(tt.composerID, tt.composer, index); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
