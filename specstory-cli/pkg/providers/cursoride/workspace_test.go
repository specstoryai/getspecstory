package cursoride

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
				if got := err.Error(); !contains(got, tt.wantError) {
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
				if got := err.Error(); !contains(got, tt.wantError) {
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

// contains checks if s contains substr (simple helper to avoid importing strings in tests)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
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
