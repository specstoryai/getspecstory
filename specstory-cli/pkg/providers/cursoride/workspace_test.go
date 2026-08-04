package cursoride

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUriToPath(t *testing.T) {
	tests := []struct {
		name      string
		uri       string
		wantPath  string
		wantError string
	}{
		// Standard file:// URIs (Linux/macOS)
		{
			name:     "standard Linux file URI",
			uri:      "file:///home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "standard Linux file URI with spaces",
			uri:      "file:///home/user/my%20project",
			wantPath: "/home/user/my project",
		},

		// WSL file://wsl.localhost URIs
		{
			name:     "WSL wsl.localhost URI with Ubuntu",
			uri:      "file://wsl.localhost/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl.localhost URI with different distro",
			uri:      "file://wsl.localhost/Debian/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl.localhost URI case insensitive host",
			uri:      "file://WSL.LOCALHOST/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl.localhost URI with deep path",
			uri:      "file://wsl.localhost/Ubuntu/home/user/code/specstory-monorepo",
			wantPath: "/home/user/code/specstory-monorepo",
		},
		{
			name:      "WSL wsl.localhost URI with only distro (no path)",
			uri:       "file://wsl.localhost/Ubuntu",
			wantError: "malformed WSL URI path",
		},

		// WSL wsl$ URIs
		{
			name:     "WSL wsl$ URI",
			uri:      "file://wsl$/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "WSL wsl$ URI case insensitive",
			uri:      "file://WSL$/Ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},

		// vscode-remote:// URIs (delegated to parseVSCodeRemoteURI)
		{
			name:     "vscode-remote URI with percent-encoded host",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "vscode-remote URI with plus in host",
			uri:      "vscode-remote://wsl+ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "vscode-remote SSH URI with hex-encoded config",
			uri:      "vscode-remote://ssh-remote%2B7b22686f73744e616d65223a226d61632d6d696e69227d/Users/bago/code/getspecstory",
			wantPath: "/Users/bago/code/getspecstory",
		},
		{
			name:     "vscode-remote tunnel URI with percent-encoded host",
			uri:      "vscode-remote://tunnel%2Bmyhost/work/group/user/myproject",
			wantPath: "/work/group/user/myproject",
		},

		// Unsupported schemes
		{
			name:      "unsupported http scheme",
			uri:       "http://example.com/path",
			wantError: "unsupported URI scheme",
		},
		{
			name:      "unsupported https scheme",
			uri:       "https://example.com/path",
			wantError: "unsupported URI scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uriToPath(tt.uri)

			if tt.wantError != "" {
				if err == nil {
					t.Errorf("uriToPath(%q) expected error containing %q, got nil", tt.uri, tt.wantError)
					return
				}
				if got := err.Error(); !strings.Contains(got, tt.wantError) {
					t.Errorf("uriToPath(%q) error = %q, want error containing %q", tt.uri, got, tt.wantError)
				}
				return
			}

			if err != nil {
				t.Errorf("uriToPath(%q) unexpected error: %v", tt.uri, err)
				return
			}

			if got != tt.wantPath {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.wantPath)
			}
		})
	}
}

func TestUriToPath_WindowsPaths(t *testing.T) {
	// Windows-specific path handling only runs on Windows
	if runtime.GOOS != "windows" {
		t.Skip("Windows path tests only run on Windows")
	}

	tests := []struct {
		name     string
		uri      string
		wantPath string
	}{
		{
			name:     "Windows file URI",
			uri:      "file:///c%3A/Users/Admin/project",
			wantPath: "c:\\Users\\Admin\\project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := uriToPath(tt.uri)
			if err != nil {
				t.Errorf("uriToPath(%q) unexpected error: %v", tt.uri, err)
				return
			}
			if got != tt.wantPath {
				t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.wantPath)
			}
		})
	}
}

func TestCodeWorkspaceContainsFolder(t *testing.T) {
	// Create a temporary directory structure.
	tmpDir, err := os.MkdirTemp("", "workspace-contains-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

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

	tests := []struct {
		name             string
		workspaceContent string
		targetFolder     string
		expected         bool
	}{
		{
			name:             "relative path that resolves to target folder",
			workspaceContent: `{"folders": [{"path": "../my-project"}]}`,
			targetFolder:     canonicalProjectDir,
			expected:         true,
		},
		{
			name:             "absolute path matching target folder",
			workspaceContent: `{"folders": [{"path": "` + projectDir + `"}]}`,
			targetFolder:     canonicalProjectDir,
			expected:         true,
		},
		{
			name:             "no folders entry matching target",
			workspaceContent: `{"folders": [{"path": "../other-project"}]}`,
			targetFolder:     canonicalProjectDir,
			expected:         false,
		},
		{
			name:             "empty folders array",
			workspaceContent: `{"folders": []}`,
			targetFolder:     canonicalProjectDir,
			expected:         false,
		},
		{
			name:             "malformed JSON",
			workspaceContent: `not json`,
			targetFolder:     canonicalProjectDir,
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeWorkspaceFile(tt.workspaceContent)
			result := codeWorkspaceContainsFolder(workspaceFile, tt.targetFolder)
			if result != tt.expected {
				t.Errorf("codeWorkspaceContainsFolder() = %v, want %v", result, tt.expected)
			}
		})
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
	workspaces := []WorkspaceMatch{{ID: "ws-hash-1", DBPath: wsDBPath}}

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
	wantID, err := cursorWorkspaceID(projectDir)
	if err != nil {
		t.Fatalf("cursorWorkspaceID: %v", err)
	}
	if ws.ID != wantID {
		t.Errorf("workspace ID = %q, want %q", ws.ID, wantID)
	}

	// workspace.json must parse and carry the folder URI.
	wj, err := readWorkspaceJSON(filepath.Join(ws.Path, "workspace.json"))
	if err != nil {
		t.Fatalf("minted workspace.json unreadable: %v", err)
	}
	if wj.Folder != pathToFileURI(projectDir) {
		t.Errorf("workspace.json folder = %q, want %q", wj.Folder, pathToFileURI(projectDir))
	}

	// The state.vscdb must exist with a usable ItemTable (our own readers query it).
	if _, err := LoadWorkspaceComposerIDs(ws.DBPath); err != nil {
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
