package copilotide

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Note: uriToPath is already covered by TestUriToPath in parser_test.go, so it is
// deliberately not re-tested here.

// setupWorkspaceStorage redirects HOME to a fresh temp dir and creates the VS Code
// ("Code" variant) workspace storage directory inside it, so findWorkspaceForProject
// resolves against a fully test-controlled filesystem. os.UserHomeDir reads $HOME on
// both macOS and Linux, so t.Setenv is enough to redirect GetWorkspaceStoragePath.
func setupWorkspaceStorage(t *testing.T) string {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	var storage string
	switch runtime.GOOS {
	case "darwin":
		storage = filepath.Join(home, "Library", "Application Support", "Code", "User", "workspaceStorage")
	case "linux":
		storage = filepath.Join(home, ".config", "Code", "User", "workspaceStorage")
	default:
		t.Skipf("workspace storage layout not defined for GOOS %s", runtime.GOOS)
	}

	if err := os.MkdirAll(storage, 0755); err != nil {
		t.Fatalf("failed to create workspace storage dir: %v", err)
	}
	return storage
}

// addWorkspaceEntry creates <storage>/<id>/workspace.json with the given content and,
// optionally, an empty chatSessions directory (the marker findWorkspaceForProject
// requires when requireChatSessions is true).
func addWorkspaceEntry(t *testing.T, storage, id, workspaceJSON string, withChatSessions bool) string {
	t.Helper()

	dir := filepath.Join(storage, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create workspace entry dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.json"), []byte(workspaceJSON), 0644); err != nil {
		t.Fatalf("failed to write workspace.json: %v", err)
	}
	if withChatSessions {
		if err := os.MkdirAll(filepath.Join(dir, "chatSessions"), 0755); err != nil {
			t.Fatalf("failed to create chatSessions dir: %v", err)
		}
	}
	return dir
}

// canonicalTempDir returns a fresh temp dir with symlinks resolved, so paths written
// into fixtures compare equal to what spi.GetCanonicalPath produces (e.g. /var vs
// /private/var on macOS).
func canonicalTempDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// writeStateDB creates a state.vscdb file in the workspace dir and pins its mtime,
// which selectNewestWorkspace uses to break ties between multiple matches.
func writeStateDB(t *testing.T, workspaceDir string, mtime time.Time) {
	t.Helper()

	path := filepath.Join(workspaceDir, "state.vscdb")
	if err := os.WriteFile(path, []byte("db"), 0644); err != nil {
		t.Fatalf("failed to write state.vscdb: %v", err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("failed to set state.vscdb mtime: %v", err)
	}
}

func TestFindWorkspaceForProject(t *testing.T) {
	tests := []struct {
		name                string
		requireChatSessions bool
		// setup builds the HOME/workspaceStorage fixtures and returns the
		// projectPath to search for.
		setup           func(t *testing.T) string
		wantID          string
		wantErrContains string
	}{
		{
			name:                "direct folder match among multiple entries",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				project := canonicalTempDir(t)
				other := canonicalTempDir(t)
				addWorkspaceEntry(t, storage, "ws-other", `{"folder":"file://`+other+`"}`, true)
				addWorkspaceEntry(t, storage, "ws-match", `{"folder":"file://`+project+`"}`, true)
				return project
			},
			wantID: "ws-match",
		},
		{
			name:                "no matching workspace",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				other := canonicalTempDir(t)
				addWorkspaceEntry(t, storage, "ws-other", `{"folder":"file://`+other+`"}`, true)
				return canonicalTempDir(t)
			},
			wantErrContains: "no workspace found",
		},
		{
			name:                "workspace storage directory missing",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				// HOME redirected but no workspaceStorage created underneath.
				t.Setenv("HOME", t.TempDir())
				return canonicalTempDir(t)
			},
			wantErrContains: "workspace storage directory not found",
		},
		{
			name:                "match without chatSessions filtered when required",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				project := canonicalTempDir(t)
				addWorkspaceEntry(t, storage, "ws-no-chat", `{"folder":"file://`+project+`"}`, false)
				return project
			},
			wantErrContains: "no workspace found",
		},
		{
			name:                "match without chatSessions kept for reconstruction targets",
			requireChatSessions: false,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				project := canonicalTempDir(t)
				addWorkspaceEntry(t, storage, "ws-no-chat", `{"folder":"file://`+project+`"}`, false)
				return project
			},
			wantID: "ws-no-chat",
		},
		{
			name:                "multiple matches select newest state.vscdb",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				project := canonicalTempDir(t)
				oldDir := addWorkspaceEntry(t, storage, "ws-old", `{"folder":"file://`+project+`"}`, true)
				newDir := addWorkspaceEntry(t, storage, "ws-new", `{"folder":"file://`+project+`"}`, true)
				writeStateDB(t, oldDir, time.Now().Add(-48*time.Hour))
				writeStateDB(t, newDir, time.Now().Add(-1*time.Hour))
				return project
			},
			wantID: "ws-new",
		},
		{
			name:                "method 3: workspace.json points at code-workspace listing the folder",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				base := canonicalTempDir(t)
				project := filepath.Join(base, "my-project")
				if err := os.Mkdir(project, 0755); err != nil {
					t.Fatalf("mkdir project: %v", err)
				}
				// The .code-workspace file lives in a sibling dir and lists the
				// project folder via a relative path.
				wsFileDir := filepath.Join(base, "workspaces")
				if err := os.Mkdir(wsFileDir, 0755); err != nil {
					t.Fatalf("mkdir workspaces: %v", err)
				}
				wsFile := filepath.Join(wsFileDir, "multi.code-workspace")
				if err := os.WriteFile(wsFile, []byte(`{"folders":[{"path":"../my-project"}]}`), 0644); err != nil {
					t.Fatalf("write code-workspace: %v", err)
				}
				addWorkspaceEntry(t, storage, "ws-multiroot", `{"workspace":"file://`+wsFile+`"}`, true)
				return project
			},
			wantID: "ws-multiroot",
		},
		{
			name:                "method 4: project path is a code-workspace file matching a folder entry",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				base := canonicalTempDir(t)
				project := filepath.Join(base, "my-project")
				if err := os.Mkdir(project, 0755); err != nil {
					t.Fatalf("mkdir project: %v", err)
				}
				wsFile := filepath.Join(base, "multi.code-workspace")
				if err := os.WriteFile(wsFile, []byte(`{"folders":[{"path":"my-project"}]}`), 0644); err != nil {
					t.Fatalf("write code-workspace: %v", err)
				}
				// The storage entry was created by opening the folder directly, but
				// the caller queries with the .code-workspace file path.
				addWorkspaceEntry(t, storage, "ws-from-folder", `{"folder":"file://`+project+`"}`, true)
				return wsFile
			},
			wantID: "ws-from-folder",
		},
		{
			name:                "malformed and non-file entries are skipped, valid match still found",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				project := canonicalTempDir(t)
				addWorkspaceEntry(t, storage, "ws-bad-json", `not json`, true)
				addWorkspaceEntry(t, storage, "ws-empty", `{}`, true)
				addWorkspaceEntry(t, storage, "ws-remote", `{"folder":"vscode-remote://wsl/home/user/proj"}`, true)
				addWorkspaceEntry(t, storage, "ws-good", `{"folder":"file://`+project+`"}`, true)
				return project
			},
			wantID: "ws-good",
		},
		{
			name:                "non-file URI alone cannot match",
			requireChatSessions: true,
			setup: func(t *testing.T) string {
				storage := setupWorkspaceStorage(t)
				project := canonicalTempDir(t)
				// URI scheme is unsupported, so even a path-identical entry is skipped.
				addWorkspaceEntry(t, storage, "ws-remote", `{"folder":"vscode-remote://wsl`+project+`"}`, true)
				return project
			},
			wantErrContains: "no workspace found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectPath := tt.setup(t)
			p := NewProvider(VSCode)

			// Exercise the exported wrapper for the requireChatSessions=true cases
			// so its delegation is covered too.
			var match *WorkspaceMatch
			var err error
			if tt.requireChatSessions {
				match, err = p.FindWorkspaceForProject(projectPath)
			} else {
				match, err = p.findWorkspaceForProject(projectPath, false)
			}

			if tt.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got match %+v", tt.wantErrContains, match)
				}
				if !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if match.ID != tt.wantID {
				t.Errorf("match.ID = %q, want %q", match.ID, tt.wantID)
			}
			if match.Dir == "" || match.URI == "" || match.Path == "" {
				t.Errorf("match has empty fields: %+v", match)
			}
		})
	}
}

func TestCollectCodeWorkspaceFolders(t *testing.T) {
	base := canonicalTempDir(t)
	relProject := filepath.Join(base, "rel-project")
	absProject := filepath.Join(base, "abs-project")
	for _, dir := range []string{relProject, absProject} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "relative and absolute folder paths resolved",
			content: `{"folders":[{"path":"rel-project"},{"path":"` + absProject + `"}]}`,
			want:    []string{relProject, absProject},
		},
		{
			name:    "empty path entries skipped",
			content: `{"folders":[{"path":""},{"path":"rel-project"}]}`,
			want:    []string{relProject},
		},
		{
			name:    "no folders key",
			content: `{"settings":{}}`,
			want:    nil,
		},
		{
			name:    "malformed JSON returns nil",
			content: `{"folders":[`,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsFile := filepath.Join(base, "test.code-workspace")
			if err := os.WriteFile(wsFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("write code-workspace: %v", err)
			}

			got := collectCodeWorkspaceFolders(wsFile)
			if len(got) != len(tt.want) {
				t.Fatalf("collectCodeWorkspaceFolders() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("folder[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}

	t.Run("missing file returns nil", func(t *testing.T) {
		if got := collectCodeWorkspaceFolders(filepath.Join(base, "does-not-exist.code-workspace")); got != nil {
			t.Errorf("expected nil for missing file, got %v", got)
		}
	})
}

func TestCodeWorkspaceContainsFolder(t *testing.T) {
	base := canonicalTempDir(t)
	project := filepath.Join(base, "my-project")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	wsFile := filepath.Join(base, "test.code-workspace")

	tests := []struct {
		name    string
		content string
		folder  string
		want    bool
	}{
		{
			name:    "relative path resolves to target folder",
			content: `{"folders":[{"path":"my-project"}]}`,
			folder:  project,
			want:    true,
		},
		{
			name:    "absolute path matches target folder",
			content: `{"folders":[{"path":"` + project + `"}]}`,
			folder:  project,
			want:    true,
		},
		{
			name:    "unrelated folder does not match",
			content: `{"folders":[{"path":"other-project"}]}`,
			folder:  project,
			want:    false,
		},
		{
			name:    "malformed JSON never matches",
			content: `not json`,
			folder:  project,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(wsFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("write code-workspace: %v", err)
			}
			if got := codeWorkspaceContainsFolder(wsFile, tt.folder); got != tt.want {
				t.Errorf("codeWorkspaceContainsFolder() = %v, want %v", got, tt.want)
			}
		})
	}
}
