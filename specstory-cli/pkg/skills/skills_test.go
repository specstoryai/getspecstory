package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"already kebab", "my-skill", "my-skill"},
		{"spaces and caps", "My Cool Skill", "my-cool-skill"},
		{"path traversal", "../../etc/passwd", "etc-passwd"},
		{"keeps dots and underscores", "a.b_c", "a.b_c"},
		{"trims leading/trailing punctuation", "--weird--", "weird"},
		{"empty falls back", "///", "unnamed-skill"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeName(tt.in); got != tt.want {
				t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestInstallAndRemove_ProjectScope verifies the full disk round-trip for a project install:
// the canonical SKILL.md is written and a symlink is created into a detected agent's dir,
// and removeSkillFromDisk cleans both up.
func TestInstallAndRemove_ProjectScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "") // force ~/.agents/.skill-lock.json path

	project := filepath.Join(home, "proj")
	// claude-code is non-universal (.claude/skills); its config root must exist for a
	// project install to materialize the symlink.
	if err := os.MkdirAll(filepath.Join(project, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	claude, ok := FindAgent("claude-code")
	if !ok {
		t.Fatal("claude-code agent not in registry")
	}

	res, err := installSkillToDisk("My Skill", "# body\n", false, project, []Agent{claude})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	canonical := filepath.Join(project, ".agents", "skills", "my-skill", "SKILL.md")
	if _, err := os.Stat(canonical); err != nil {
		t.Errorf("canonical SKILL.md missing: %v", err)
	}
	link := filepath.Join(project, ".claude", "skills", "my-skill")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("agent symlink missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected a symlink at %s", link)
	}
	// The symlink must resolve to the canonical SKILL.md.
	if _, err := os.Stat(filepath.Join(link, "SKILL.md")); err != nil {
		t.Errorf("symlink does not resolve to skill content: %v", err)
	}
	if len(res.agents) != 1 || res.agents[0] != "claude-code" {
		t.Errorf("agents = %v, want [claude-code]", res.agents)
	}

	// Now remove and confirm both locations are gone.
	entry := LockEntry{Scope: "project", ProjectPath: project, Agents: []string{"claude-code"}, CanonicalPath: res.canonicalDir}
	if err := removeSkillFromDisk("My Skill", entry); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("symlink not removed: %v", err)
	}
	if _, err := os.Stat(res.canonicalDir); !os.IsNotExist(err) {
		t.Errorf("canonical dir not removed: %v", err)
	}
}

// TestInstall_ProjectSkipsUndetectedAgent verifies a project install does NOT create an
// agent's config tree when the user isn't using that agent in the project.
func TestInstall_ProjectSkipsUndetectedAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	windsurf, _ := FindAgent("windsurf") // .windsurf/skills, not present in project
	res, err := installSkillToDisk("skill", "# body", false, project, []Agent{windsurf})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.skipped) != 1 || res.skipped[0] != "windsurf" {
		t.Errorf("skipped = %v, want [windsurf]", res.skipped)
	}
	if _, err := os.Stat(filepath.Join(project, ".windsurf")); !os.IsNotExist(err) {
		t.Errorf(".windsurf should not have been created")
	}
}

// TestLockRoundTripPreservesForeignEntries ensures we never clobber entries or top-level
// keys written by the npx skills CLI when we add a SpecStory entry.
func TestLockRoundTripPreservesForeignEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	// Seed a lock file as npx skills would write it: a foreign skill + a "dismissed" key.
	seed := `{
  "version": 3,
  "skills": {
    "their-skill": {"source": "owner/repo", "sourceType": "github", "sourceUrl": "https://x", "skillFolderHash": "abc", "installedAt": "t", "updatedAt": "t"}
  },
  "dismissed": {"findSkillsPrompt": true},
  "lastSelectedAgents": ["claude-code"]
}`
	lockDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, ".skill-lock.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add our entry.
	if err := upsertLockEntry("ours", LockEntry{Source: sourceTypeSpecStory, SourceType: sourceTypeSpecStory, Scope: "global"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(lockDir, ".skill-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	// Foreign top-level keys preserved.
	if _, ok := doc["dismissed"]; !ok {
		t.Error("dismissed key was dropped")
	}
	if _, ok := doc["lastSelectedAgents"]; !ok {
		t.Error("lastSelectedAgents key was dropped")
	}

	// Both skills present; only ours is SpecStory-owned.
	ours, err := specStoryLockEntries()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ours["ours"]; !ok {
		t.Error("our entry missing")
	}
	if _, ok := ours["their-skill"]; ok {
		t.Error("foreign entry wrongly classified as SpecStory-owned")
	}

	entry, ok, _ := getLockEntry("their-skill")
	if !ok || entry.SourceType != "github" {
		t.Errorf("foreign entry corrupted: %+v", entry)
	}
}

// TestLockWipesOldVersion mirrors npx skills' behavior: a pre-v3 lock is treated as empty.
func TestLockWipesOldVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	lockDir := filepath.Join(home, ".agents")
	_ = os.MkdirAll(lockDir, 0o755)
	_ = os.WriteFile(filepath.Join(lockDir, ".skill-lock.json"),
		[]byte(`{"version": 2, "skills": {"old": {"sourceType":"github"}}}`), 0o644)

	lf, err := readLock()
	if err != nil {
		t.Fatal(err)
	}
	if len(lf.Skills) != 0 {
		t.Errorf("expected old-version lock to be wiped, got %d skills", len(lf.Skills))
	}
}

func TestResolveTargets(t *testing.T) {
	e := NewEngine(t.TempDir())

	got, err := e.resolveTargets([]string{"claude-code", "codex"})
	if err != nil {
		t.Fatalf("resolveTargets: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d targets, want 2", len(got))
	}

	if _, err := e.resolveTargets([]string{"not-an-agent"}); err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestUniversalAgentDetection(t *testing.T) {
	codex, _ := FindAgent("codex")
	if !codex.Universal() {
		t.Error("codex should be a universal agent (.agents/skills)")
	}
	claude, _ := FindAgent("claude-code")
	if claude.Universal() {
		t.Error("claude-code should not be universal (.claude/skills)")
	}
}
