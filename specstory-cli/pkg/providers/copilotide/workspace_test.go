package copilotide

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
)

// resetUserDataDirOverride restores the package-level override map after a test mutates it.
// Tests must defer this — leaking state into sibling tests would silently change their behavior.
func resetUserDataDirOverride(t *testing.T) {
	t.Helper()
	prev, had := userDataDirOverrides[VSCode.ID]
	t.Cleanup(func() {
		if had {
			userDataDirOverrides[VSCode.ID] = prev
		} else {
			delete(userDataDirOverrides, VSCode.ID)
		}
	})
}

// TestUserDataDirOverride_WorkspaceStorage verifies that an override pointing to a
// valid directory wins over OS-default discovery, and that a missing override path
// falls through to OS defaults (warn-and-fall-back, not hard failure).
func TestUserDataDirOverride_WorkspaceStorage(t *testing.T) {
	resetUserDataDirOverride(t)

	tmp := t.TempDir()
	wantPath := filepath.Join(tmp, "User", "workspaceStorage")
	if err := os.MkdirAll(wantPath, 0755); err != nil {
		t.Fatalf("Failed to create fake workspaceStorage: %v", err)
	}

	SetUserDataDirOverride(VSCode.ID, tmp)
	got := GetWorkspaceStoragePath(VSCode)
	if got != wantPath {
		t.Errorf("GetWorkspaceStoragePath() = %q, want %q", got, wantPath)
	}
}

// TestUserDataDirOverride_MissingPathFallsThrough verifies that a bad override falls
// through to the OS-default branch instead of being treated as authoritative-and-empty.
// The path we set as the override does not contain User/workspaceStorage, so the
// resolver must move on and consult the OS default. If the OS default also doesn't
// exist (CI without VS Code), we get "" — but critically, the returned path must
// not be derived from our override dir.
func TestUserDataDirOverride_MissingPathFallsThrough(t *testing.T) {
	resetUserDataDirOverride(t)

	override := t.TempDir()
	SetUserDataDirOverride(VSCode.ID, override)

	got := GetWorkspaceStoragePath(VSCode)
	// Either the OS default exists (got != "" and not under override) or doesn't
	// (got == ""). Either way, the override-derived path must not be returned.
	if got != "" && strings.HasPrefix(got, override) {
		t.Errorf("expected fall-through to OS default after bad override, but got override-derived path: %q", got)
	}
}

// TestForEachUniqueSession_EmptyCopyDoesNotSuppressPopulated guards the dedup
// ordering: under WSL the same project is recorded under multiple workspace
// entries, enumerated by workspace hash rather than recency. An empty copy of a
// session encountered first must not claim the session ID and suppress the
// populated copy from another workspace entry — only content-bearing copies
// mark the ID as seen (and among those, first wins).
func TestForEachUniqueSession_EmptyCopyDoesNotSuppressPopulated(t *testing.T) {
	// writeSession creates {dir}/chatSessions/{name}.json with the given request count.
	writeSession := func(t *testing.T, dir, name, sessionID string, requestCount int) {
		t.Helper()
		chatDir := filepath.Join(dir, "chatSessions")
		if err := os.MkdirAll(chatDir, 0755); err != nil {
			t.Fatal(err)
		}
		requests := make([]map[string]any, requestCount)
		for i := range requests {
			requests[i] = map[string]any{}
		}
		content, err := json.Marshal(map[string]any{"sessionId": sessionID, "requests": requests})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(chatDir, name+".json"), content, 0644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name          string
		firstRequests int // request count of the copy in the first-enumerated workspace
		wantHandled   int // sessions reaching handle
		wantRequests  int // request count of the handled copy
	}{
		{
			name:          "empty copy first, populated copy survives",
			firstRequests: 0,
			wantHandled:   1,
			wantRequests:  2, // the second workspace's copy
		},
		{
			name:          "populated copy first wins over later copy",
			firstRequests: 1,
			wantHandled:   1,
			wantRequests:  1, // the first workspace's copy
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsA, wsB := t.TempDir(), t.TempDir()
			writeSession(t, wsA, "session", "dup-id", tt.firstRequests)
			writeSession(t, wsB, "session", "dup-id", 2)

			sources := collectSessionSources([]vscode.WorkspaceEntry{{Dir: wsA}, {Dir: wsB}})
			var handled []*VSCodeComposer
			forEachUniqueSession(sources, func(composer *VSCodeComposer, _ *VSCodeStateFile) {
				handled = append(handled, composer)
			})

			if len(handled) != tt.wantHandled {
				t.Fatalf("handled %d sessions, want %d", len(handled), tt.wantHandled)
			}
			if got := len(handled[0].Requests); got != tt.wantRequests {
				t.Errorf("handled copy has %d requests, want %d", got, tt.wantRequests)
			}
		})
	}
}
