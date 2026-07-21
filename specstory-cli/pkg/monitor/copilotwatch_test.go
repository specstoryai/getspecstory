//go:build copilotide_monitor

package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// copilotRoot returns the fixture's stock-VS-Code workspaceStorage root.
func (f *activityFixture) copilotRoot() string {
	return f.roots.CopilotIDE["copilotide"]
}

// writeCopilotWorkspace creates a fake VS Code workspace-storage entry:
// <root>/<id>/workspace.json (content given verbatim; "" means no file) and,
// optionally, an empty chatSessions directory. Returns the workspace dir.
func writeCopilotWorkspace(t *testing.T, root, id, workspaceJSON string, withChatSessions bool) string {
	t.Helper()
	wsDir := filepath.Join(root, id)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}
	if workspaceJSON != "" {
		if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(workspaceJSON), 0o644); err != nil {
			t.Fatalf("failed to write workspace.json: %v", err)
		}
	}
	if withChatSessions {
		if err := os.MkdirAll(filepath.Join(wsDir, "chatSessions"), 0o755); err != nil {
			t.Fatalf("failed to create chatSessions dir: %v", err)
		}
	}
	return wsDir
}

// folderJSON builds a workspace.json body whose "folder" URI points at path.
func folderJSON(path string) string {
	return fmt.Sprintf(`{"folder":"file://%s"}`, path)
}

// writeCodeWorkspaceFile writes a .code-workspace file listing the given
// folders (absolute paths) and returns a workspace.json body referencing it.
func writeCodeWorkspaceFile(t *testing.T, dir string, folders ...string) string {
	t.Helper()
	body := `{"folders":[`
	for i, f := range folders {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`{"path":%q}`, f)
	}
	body += `]}`
	wsFile := filepath.Join(dir, "multi.code-workspace")
	if err := os.WriteFile(wsFile, []byte(body), 0o644); err != nil {
		t.Fatalf("failed to write .code-workspace file: %v", err)
	}
	return fmt.Sprintf(`{"workspace":"file://%s"}`, wsFile)
}

func TestMapCopilotWorkspaces(t *testing.T) {
	tests := []struct {
		name string
		// workspaceJSON returns the workspace.json body ("" = no file).
		workspaceJSON    func(t *testing.T, f *activityFixture) string
		withChatSessions bool
		// wantRepo returns the repo the workspace must map to ("" = excluded).
		wantRepo func(f *activityFixture) string
	}{
		{
			name:             "folder is repo root",
			workspaceJSON:    func(t *testing.T, f *activityFixture) string { return folderJSON(f.repo1) },
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return f.repo1 },
		},
		{
			name:             "folder is repo subdirectory (longest-prefix mapping)",
			workspaceJSON:    func(t *testing.T, f *activityFixture) string { return folderJSON(f.repo2Sub) },
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return f.repo2 },
		},
		{
			name: "folder outside every discovered repo is excluded",
			workspaceJSON: func(t *testing.T, f *activityFixture) string {
				return folderJSON(t.TempDir())
			},
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return "" },
		},
		{
			name: "code-workspace with one folder inside a repo",
			workspaceJSON: func(t *testing.T, f *activityFixture) string {
				// First folder is outside the repo set; the second maps into
				// repo2 — the first matching folder wins.
				return writeCodeWorkspaceFile(t, t.TempDir(), t.TempDir(), f.repo2Sub)
			},
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return f.repo2 },
		},
		{
			name: "code-workspace with no folder in the repo set is excluded",
			workspaceJSON: func(t *testing.T, f *activityFixture) string {
				return writeCodeWorkspaceFile(t, t.TempDir(), t.TempDir(), t.TempDir())
			},
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return "" },
		},
		{
			name:             "missing workspace.json is skipped",
			workspaceJSON:    func(t *testing.T, f *activityFixture) string { return "" },
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return "" },
		},
		{
			name:             "invalid workspace.json is skipped",
			workspaceJSON:    func(t *testing.T, f *activityFixture) string { return "{not json" },
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return "" },
		},
		{
			name: "non-file URI is skipped",
			workspaceJSON: func(t *testing.T, f *activityFixture) string {
				return `{"folder":"vscode-remote://ssh-remote%2Bhost/home/user/repo"}`
			},
			withChatSessions: true,
			wantRepo:         func(f *activityFixture) string { return "" },
		},
		{
			name:             "matched workspace without chatSessions dir is skipped",
			workspaceJSON:    func(t *testing.T, f *activityFixture) string { return folderJSON(f.repo1) },
			withChatSessions: false,
			wantRepo:         func(f *activityFixture) string { return "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newActivityFixture(t)
			root := f.copilotRoot()
			wsDir := writeCopilotWorkspace(t, root, "ws-under-test", tt.workspaceJSON(t, f), tt.withChatSessions)

			got, err := mapCopilotWorkspaces(root, NewResolver(f.repos(), f.roots))
			if err != nil {
				t.Fatalf("mapCopilotWorkspaces() error = %v", err)
			}

			wantRepo := tt.wantRepo(f)
			chatDir := filepath.Join(wsDir, "chatSessions")
			if wantRepo == "" {
				if len(got) != 0 {
					t.Errorf("mapCopilotWorkspaces() = %v, want empty map", got)
				}
				return
			}
			if len(got) != 1 || got[chatDir] != wantRepo {
				t.Errorf("mapCopilotWorkspaces() = %v, want {%q: %q}", got, chatDir, wantRepo)
			}
		})
	}
}

// TestMapCopilotWorkspaces_ManyEntries pins that the enumeration keeps only
// repo-relevant workspaces out of a larger population — the property that
// bounds the fsnotify watch count regardless of workspaceStorage size.
func TestMapCopilotWorkspaces_ManyEntries(t *testing.T) {
	f := newActivityFixture(t)
	root := f.copilotRoot()

	outside := t.TempDir()
	for i := 0; i < 25; i++ {
		writeCopilotWorkspace(t, root, fmt.Sprintf("outside-%d", i), folderJSON(outside), true)
	}
	matched := writeCopilotWorkspace(t, root, "matched", folderJSON(f.repo1), true)

	got, err := mapCopilotWorkspaces(root, NewResolver(f.repos(), f.roots))
	if err != nil {
		t.Fatalf("mapCopilotWorkspaces() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("mapCopilotWorkspaces() kept %d workspaces, want 1: %v", len(got), got)
	}
	if got[filepath.Join(matched, "chatSessions")] != f.repo1 {
		t.Errorf("mapCopilotWorkspaces() = %v, want the matched workspace mapping to %q", got, f.repo1)
	}
}

// startCopilotWatch runs WatchCopilotWorkspaceStorage in a goroutine and gives
// the watcher a moment to establish before the test generates events (same
// pattern as startRootWatch).
func startCopilotWatch(t *testing.T, ctx context.Context, root string, r *Resolver, onActivity func(string)) chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- WatchCopilotWorkspaceStorage(ctx, root, r, onActivity)
	}()
	time.Sleep(250 * time.Millisecond)
	return errCh
}

// stopCopilotWatch cancels the watcher and asserts it returns cleanly.
func stopCopilotWatch(t *testing.T, cancel context.CancelFunc, errCh chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("WatchCopilotWorkspaceStorage() error = %v, want nil on cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("WatchCopilotWorkspaceStorage() did not return after context cancellation")
	}
}

func TestWatchCopilotWorkspaceStorage_ChatSessionEvent(t *testing.T) {
	f := newActivityFixture(t)
	root := f.copilotRoot()
	wsDir := writeCopilotWorkspace(t, root, "ws1", folderJSON(f.repo1), true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := &eventRecorder{}
	errCh := startCopilotWatch(t, ctx, root, NewResolver(f.repos(), f.roots), rec.record)

	// A new session file in the pre-mapped chatSessions dir is activity.
	sessionFile := filepath.Join(wsDir, "chatSessions", "0198c5c1-abcd.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return rec.contains(f.repo1) },
		"onActivity never observed "+f.repo1)

	stopCopilotWatch(t, cancel, errCh)
}

func TestWatchCopilotWorkspaceStorage_NewWorkspaceDirAppears(t *testing.T) {
	// Shrink the workspace.json retry delay so the async-write simulation
	// below doesn't slow the suite down.
	savedDelay := copilotWorkspaceJSONRetryDelay
	copilotWorkspaceJSONRetryDelay = 20 * time.Millisecond
	defer func() { copilotWorkspaceJSONRetryDelay = savedDelay }()

	f := newActivityFixture(t)
	root := f.copilotRoot()
	outside := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := &eventRecorder{}
	errCh := startCopilotWatch(t, ctx, root, NewResolver(f.repos(), f.roots), rec.record)

	// A workspace for a folder OUTSIDE the repo set appears first; it must
	// never produce a notification even once its chat starts.
	outsideWS := writeCopilotWorkspace(t, root, "ws-outside", folderJSON(outside), false)
	time.Sleep(300 * time.Millisecond)
	if err := os.MkdirAll(filepath.Join(outsideWS, "chatSessions"), 0o755); err != nil {
		t.Fatalf("failed to create outside chatSessions: %v", err)
	}

	// A repo-mapped workspace appears the way VS Code creates one: directory
	// first, workspace.json a moment later (exercising the retry loop), and
	// the chatSessions dir only when the first chat starts.
	wsDir := filepath.Join(root, "ws-new")
	if err := os.Mkdir(wsDir, 0o755); err != nil {
		t.Fatalf("failed to create new workspace dir: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // Let a first workspace.json read fail.
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(folderJSON(f.repo1)), 0o644); err != nil {
		t.Fatalf("failed to write workspace.json: %v", err)
	}
	// Give the resolver time to finish and register the pending-workspace
	// watch before the first chat "starts".
	time.Sleep(300 * time.Millisecond)
	if err := os.Mkdir(filepath.Join(wsDir, "chatSessions"), 0o755); err != nil {
		t.Fatalf("failed to create chatSessions: %v", err)
	}

	// The chatSessions creation itself must register as activity.
	waitFor(t, 5*time.Second, func() bool { return rec.contains(f.repo1) },
		"onActivity never observed "+f.repo1+" after chatSessions creation")

	// And subsequent session-file writes keep flowing through the promoted
	// chatSessions watch.
	before := rec.count(f.repo1)
	if err := os.WriteFile(filepath.Join(wsDir, "chatSessions", "s1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return rec.count(f.repo1) > before },
		"onActivity never observed session-file activity in the promoted chatSessions dir")

	// The outside workspace must not have produced any notification.
	if rec.contains(outside) {
		t.Errorf("onActivity observed %q, want no notifications for a workspace outside the repo set", outside)
	}

	stopCopilotWatch(t, cancel, errCh)
}

// TestWatchCopilotWorkspaceStorage_NewWorkspaceWithExistingChatSessions covers
// the runtime path where the new workspace directory already has chatSessions
// by the time workspace.json resolves (e.g. the monitor raced a fast setup).
func TestWatchCopilotWorkspaceStorage_NewWorkspaceWithExistingChatSessions(t *testing.T) {
	savedDelay := copilotWorkspaceJSONRetryDelay
	copilotWorkspaceJSONRetryDelay = 20 * time.Millisecond
	defer func() { copilotWorkspaceJSONRetryDelay = savedDelay }()

	f := newActivityFixture(t)
	root := f.copilotRoot()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := &eventRecorder{}
	errCh := startCopilotWatch(t, ctx, root, NewResolver(f.repos(), f.roots), rec.record)

	// Fully formed workspace appears in one go.
	wsDir := writeCopilotWorkspace(t, root, "ws-fast", folderJSON(f.repo2), true)
	time.Sleep(300 * time.Millisecond) // Let the watcher resolve and add the chatSessions watch.

	if err := os.WriteFile(filepath.Join(wsDir, "chatSessions", "s1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return rec.contains(f.repo2) },
		"onActivity never observed "+f.repo2)

	stopCopilotWatch(t, cancel, errCh)
}

func TestResolveNewCopilotWorkspace_RetriesUntilWorkspaceJSONAppears(t *testing.T) {
	savedDelay := copilotWorkspaceJSONRetryDelay
	copilotWorkspaceJSONRetryDelay = 20 * time.Millisecond
	defer func() { copilotWorkspaceJSONRetryDelay = savedDelay }()

	f := newActivityFixture(t)
	r := NewResolver(f.repos(), f.roots)

	tests := []struct {
		name string
		// delayedJSON is written after a delay ("" = never written).
		delayedJSON string
		wantRepo    string
		wantOK      bool
	}{
		{
			name:        "workspace.json arrives late and maps to a repo",
			delayedJSON: folderJSON(f.repo1),
			wantRepo:    f.repo1,
			wantOK:      true,
		},
		{
			name:        "workspace.json arrives late but maps outside the repo set",
			delayedJSON: folderJSON(t.TempDir()),
			wantOK:      false,
		},
		{
			name:   "workspace.json never arrives",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsDir := filepath.Join(f.copilotRoot(), "retry-"+tt.name)
			if err := os.Mkdir(wsDir, 0o755); err != nil {
				t.Fatalf("failed to create workspace dir: %v", err)
			}
			if tt.delayedJSON != "" {
				go func() {
					time.Sleep(3 * copilotWorkspaceJSONRetryDelay)
					_ = os.WriteFile(filepath.Join(wsDir, "workspace.json"), []byte(tt.delayedJSON), 0o644)
				}()
			}

			repo, ok := resolveNewCopilotWorkspace(context.Background(), wsDir, r)
			if ok != tt.wantOK {
				t.Fatalf("resolveNewCopilotWorkspace() ok = %v, want %v (repo=%q)", ok, tt.wantOK, repo)
			}
			if ok && repo != tt.wantRepo {
				t.Errorf("resolveNewCopilotWorkspace() = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}
