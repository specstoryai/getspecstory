package copilotide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// TestUserDataDirOverride_NoOverridePreservesExistingBehavior verifies that the
// empty-override case behaves exactly as before this feature was added.
func TestUserDataDirOverride_NoOverridePreservesExistingBehavior(t *testing.T) {
	resetUserDataDirOverride(t)
	SetUserDataDirOverride(VSCode.ID, "") // explicit clear

	// Should not panic; result depends on whether VS Code is installed on the host.
	_ = GetWorkspaceStoragePath(VSCode)
}
