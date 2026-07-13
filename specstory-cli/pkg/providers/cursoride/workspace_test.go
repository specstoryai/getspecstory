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
	SetUserDataDirOverride(t.TempDir())

	_, err := GetWorkspaceStoragePath()
	if err == nil {
		// If a real Cursor install happens to exist on this machine, the OS-default
		// branch succeeded — also a valid fall-through outcome. Either way, the override
		// must not have been used (we'd have failed before reaching here otherwise).
		return
	}
	// The error must come from the OS-default path, not from the override candidate
	// (the override candidate is under t.TempDir()). The error message includes the
	// path being checked; assert it doesn't mention our tmp dir.
	if strings.Contains(err.Error(), t.TempDir()) {
		t.Errorf("expected fall-through to OS default after bad override, but error mentions override path: %v", err)
	}
}

// TestUserDataDirOverride_NoOverridePreservesExistingBehavior verifies that when the
// override is empty, the resolver behaves exactly as it did before this feature —
// no surprises for the common case.
func TestUserDataDirOverride_NoOverridePreservesExistingBehavior(t *testing.T) {
	resetUserDataDirOverride(t)
	SetUserDataDirOverride("") // explicit clear

	// On a CI box without Cursor installed, this will error; on a dev box with Cursor
	// installed, it will succeed. Both outcomes are valid — we only assert the call
	// completes without panicking and produces an unsurprising error shape if it fails.
	got, err := GetWorkspaceStoragePath()
	if err != nil {
		if !strings.Contains(err.Error(), "workspace storage") {
			t.Errorf("unexpected error from default discovery: %v", err)
		}
		return
	}
	if got == "" {
		t.Errorf("GetWorkspaceStoragePath() returned empty path with no error")
	}
}

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

func TestParseVSCodeRemoteURI(t *testing.T) {
	tests := []struct {
		name      string
		uri       string
		wantPath  string
		wantError string
	}{
		// Valid WSL URIs
		{
			name:     "percent-encoded wsl+ubuntu",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "unencoded wsl+ubuntu",
			uri:      "vscode-remote://wsl+ubuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "percent-encoded wsl+Debian",
			uri:      "vscode-remote://wsl%2BDebian/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "case insensitive WSL host",
			uri:      "vscode-remote://WSL%2BUbuntu/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "wsl host without distro name",
			uri:      "vscode-remote://wsl/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "deep path",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/code/specstory-monorepo",
			wantPath: "/home/user/code/specstory-monorepo",
		},
		{
			name:     "path with spaces encoded",
			uri:      "vscode-remote://wsl%2Bubuntu/home/user/my%20project",
			wantPath: "/home/user/my project",
		},
		{
			name:     "root path",
			uri:      "vscode-remote://wsl%2Bubuntu/",
			wantPath: "/",
		},

		// Valid SSH remote URIs
		{
			name:     "ssh-remote with simple config",
			uri:      "vscode-remote://ssh-remote+myserver/home/user/project",
			wantPath: "/home/user/project",
		},
		{
			name:     "ssh-remote with hex-encoded config",
			uri:      "vscode-remote://ssh-remote%2B7b22686f73744e616d65223a226d61632d6d696e69227d/Users/bago/code/getspecstory",
			wantPath: "/Users/bago/code/getspecstory",
		},
		{
			name:     "ssh-remote case insensitive",
			uri:      "vscode-remote://SSH-REMOTE+myserver/home/user/project",
			wantPath: "/home/user/project",
		},

		// Valid tunnel URIs
		{
			name:     "tunnel with simple host",
			uri:      "vscode-remote://tunnel+myhost/work/group/user/myproject",
			wantPath: "/work/group/user/myproject",
		},
		{
			name:     "tunnel with percent-encoded host",
			uri:      "vscode-remote://tunnel%2Bmyhost/work/group/user/myproject",
			wantPath: "/work/group/user/myproject",
		},
		{
			name:     "tunnel case insensitive",
			uri:      "vscode-remote://TUNNEL+myhost/home/user/project",
			wantPath: "/home/user/project",
		},

		// Dev container URIs - path returned as-is (container-internal path)
		{
			name:     "dev container URI with hex-encoded config",
			uri:      "vscode-remote://dev-container%2B7b2273657474696e6754797065223a22636f6e7461696e6572222c22636f6e7461696e65724964223a22656335613261653766636632227d/workspace",
			wantPath: "/workspace",
		},
		{
			name:     "dev container URI case insensitive",
			uri:      "vscode-remote://DEV-CONTAINER%2Babc123/home/user/project",
			wantPath: "/home/user/project",
		},

		// Error cases
		{
			name:      "no path component",
			uri:       "vscode-remote://wsl%2Bubuntu",
			wantError: "malformed vscode-remote URI (no path)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVSCodeRemoteURI(tt.uri)

			if tt.wantError != "" {
				if err == nil {
					t.Errorf("parseVSCodeRemoteURI(%q) expected error containing %q, got nil", tt.uri, tt.wantError)
					return
				}
				if got := err.Error(); !strings.Contains(got, tt.wantError) {
					t.Errorf("parseVSCodeRemoteURI(%q) error = %q, want error containing %q", tt.uri, got, tt.wantError)
				}
				return
			}

			if err != nil {
				t.Errorf("parseVSCodeRemoteURI(%q) unexpected error: %v", tt.uri, err)
				return
			}

			if got != tt.wantPath {
				t.Errorf("parseVSCodeRemoteURI(%q) = %q, want %q", tt.uri, got, tt.wantPath)
			}
		})
	}
}

func TestIsRemoteURIRequiringBasenameMatch(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{
			name: "ssh-remote URI matches",
			uri:  "vscode-remote://ssh-remote+myserver/home/user/project",
			want: true,
		},
		{
			name: "ssh-remote URI case insensitive",
			uri:  "vscode-remote://SSH-REMOTE+myserver/home/user/project",
			want: true,
		},
		{
			name: "tunnel URI matches",
			uri:  "vscode-remote://tunnel+myhost/work/group/user/myproject",
			want: true,
		},
		{
			name: "tunnel URI case insensitive",
			uri:  "vscode-remote://TUNNEL+myhost/work/group/user/myproject",
			want: true,
		},
		{
			name: "dev-container URI matches",
			uri:  "vscode-remote://dev-container%2Babc123/workspace",
			want: true,
		},
		{
			name: "dev-container URI case insensitive",
			uri:  "vscode-remote://DEV-CONTAINER%2Babc123/home/user/project",
			want: true,
		},
		{
			name: "wsl URI does not match",
			uri:  "vscode-remote://wsl%2Bubuntu/home/user/project",
			want: false,
		},
		{
			name: "local file URI does not match",
			uri:  "file:///Users/bago/code/getspecstory",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRemoteURIRequiringBasenameMatch(tt.uri); got != tt.want {
				t.Errorf("isRemoteURIRequiringBasenameMatch(%q) = %v, want %v", tt.uri, got, tt.want)
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
