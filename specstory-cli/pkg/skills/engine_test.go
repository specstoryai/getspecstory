package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
)

// newTestEngine builds an engine with stubbed cloud calls and an isolated HOME, so the full
// install/uninstall/list flow can be exercised on a temp filesystem without a network.
func newTestEngine(t *testing.T, library []cloud.SkillRow) (*Engine, *[]string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	project := filepath.Join(home, "proj")
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stateCalls []string
	e := &Engine{
		projectDir:      project,
		listLibrary:     func() ([]cloud.SkillRow, error) { return library, nil },
		setInstallState: func(name, state string) error { stateCalls = append(stateCalls, name+":"+state); return nil },
	}
	return e, &stateCalls
}

// TestEngineInstallUninstall exercises the full local install round-trip through the engine:
// files land on disk, a lock entry is recorded, the cloud install_state is flipped, and
// uninstall reverses all of it.
func TestEngineInstallUninstall(t *testing.T) {
	e, calls := newTestEngine(t, []cloud.SkillRow{
		{Name: "my-skill", State: cloud.SkillStateReady, SkillMd: "# body\n", ContentSha: "sha1"},
	})

	report, err := e.Install("my-skill", InstallOptions{Global: false, Agents: []string{"claude-code"}})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(report.Agents) != 1 || report.Agents[0] != "claude-code" {
		t.Errorf("report agents = %v, want [claude-code]", report.Agents)
	}
	if _, err := os.Stat(filepath.Join(report.CanonicalPath, "SKILL.md")); err != nil {
		t.Errorf("canonical SKILL.md missing: %v", err)
	}
	entry, ok, _ := getLockEntry("my-skill")
	if !ok || entry.Scope != "project" || entry.ContentSha != "sha1" {
		t.Errorf("lock entry not recorded correctly: %+v", entry)
	}
	if len(*calls) != 1 || (*calls)[0] != "my-skill:installed" {
		t.Errorf("expected install_state flip to installed, got %v", *calls)
	}

	// List should now report it as locally installed, no drift (sha matches).
	views, err := e.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || !views[0].LocallyInstalled || views[0].Drift {
		t.Errorf("expected installed, no-drift view, got %+v", views[0])
	}

	// Uninstall removes files, the lock entry, and flips cloud state back.
	if _, err := e.Uninstall("my-skill"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(report.CanonicalPath); !os.IsNotExist(err) {
		t.Errorf("canonical dir should be gone: %v", err)
	}
	if _, ok, _ := getLockEntry("my-skill"); ok {
		t.Error("lock entry should be removed after uninstall")
	}
	if len(*calls) != 2 || (*calls)[1] != "my-skill:available" {
		t.Errorf("expected install_state flip to available, got %v", *calls)
	}
}

// TestEngineDriftDetected verifies List flags drift when the cloud contentSha differs from
// the locally installed copy's.
func TestEngineDriftDetected(t *testing.T) {
	e, _ := newTestEngine(t, []cloud.SkillRow{
		{Name: "s", State: cloud.SkillStateReady, SkillMd: "# v1", ContentSha: "old"},
	})
	if _, err := e.Install("s", InstallOptions{Global: true, Agents: []string{"claude-code"}}); err != nil {
		t.Fatalf("install: %v", err)
	}
	// The cloud now serves a newer version.
	e.listLibrary = func() ([]cloud.SkillRow, error) {
		return []cloud.SkillRow{{Name: "s", State: cloud.SkillStateReady, SkillMd: "# v2", ContentSha: "new"}}, nil
	}
	views, err := e.List()
	if err != nil {
		t.Fatal(err)
	}
	if !views[0].Drift {
		t.Error("expected drift to be detected when contentSha changed")
	}
}

// TestEngineCanonicalOnlyInstall verifies that an explicit empty agent set installs to the
// canonical store only (no agents), rather than defaulting to all detected agents.
func TestEngineCanonicalOnlyInstall(t *testing.T) {
	e, _ := newTestEngine(t, []cloud.SkillRow{
		{Name: "s", State: cloud.SkillStateReady, SkillMd: "# body", ContentSha: "x"},
	})
	report, err := e.Install("s", InstallOptions{Global: true, Agents: []string{}})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(report.Agents) != 0 {
		t.Errorf("expected canonical-only install (no agents), got %v", report.Agents)
	}
	if _, err := os.Stat(filepath.Join(report.CanonicalPath, "SKILL.md")); err != nil {
		t.Errorf("canonical SKILL.md should still be written: %v", err)
	}
}

// TestEngineInstallRejectsReview ensures a review-state skill can't be installed.
func TestEngineInstallRejectsReview(t *testing.T) {
	e, _ := newTestEngine(t, []cloud.SkillRow{
		{Name: "r", State: cloud.SkillStateReview, SkillMd: "# body", DossierID: "d1"},
	})
	if _, err := e.Install("r", InstallOptions{Global: true}); err == nil {
		t.Error("expected install of a review-state skill to be rejected")
	}
}
