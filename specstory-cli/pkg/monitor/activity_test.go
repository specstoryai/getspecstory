package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/claudecode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/cursorcli"
)

// activityFixture builds a full resolver fixture: discovered repos (with
// subdirectories) plus injectable storage roots, all under t.TempDir().
type activityFixture struct {
	roots StorageRoots
	// repo1 and repo2 are the discovered repos; repo2 lives nested one level
	// down and repo2Sub is a subdirectory inside it (session cwds are often
	// subdirectories of the repo).
	repo1, repo1Sub, repo2, repo2Sub string
}

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	base := t.TempDir()
	f := &activityFixture{
		repo1: filepath.Join(base, "repo1"),
		repo2: filepath.Join(base, "nested", "repo2"),
		roots: StorageRoots{
			Claude: filepath.Join(base, "storage", "claude", "projects"),
			Codex:  filepath.Join(base, "storage", "codex", "sessions"),
			Cursor: filepath.Join(base, "storage", "cursor", "chats"),
			CopilotIDE: map[string]string{
				"copilotide": filepath.Join(base, "storage", "copilot", "workspaceStorage"),
			},
		},
	}
	f.repo1Sub = filepath.Join(f.repo1, "pkg", "deep")
	f.repo2Sub = filepath.Join(f.repo2, "sub")
	for _, dir := range []string{f.repo1Sub, f.repo2Sub, f.roots.Claude, f.roots.Codex, f.roots.Cursor, f.roots.CopilotIDE["copilotide"]} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("failed to create fixture dir %s: %v", dir, err)
		}
	}
	return f
}

func (f *activityFixture) repos() []string {
	return []string{f.repo1, f.repo2}
}

// writeCodexSession writes a minimal real-format Codex session file (the
// session_meta first line is all the resolver reads) and returns its path.
func writeCodexSession(t *testing.T, codexRoot, name, cwd string) string {
	t.Helper()
	dateDir := filepath.Join(codexRoot, "2026", "07", "15")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatalf("failed to create codex date dir: %v", err)
	}
	p := filepath.Join(dateDir, name)
	line := fmt.Sprintf(`{"type":"session_meta","timestamp":"2026-07-15T12:00:00.000Z","payload":{"id":"0198c5c1-aaaa-bbbb-cccc-000000000001","timestamp":"2026-07-15T12:00:00.000Z","cwd":%q}}`+"\n", cwd)
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatalf("failed to write codex session file: %v", err)
	}
	return p
}

func TestResolver_Resolve(t *testing.T) {
	f := newActivityFixture(t)
	r := NewResolver(f.repos(), f.roots)

	// Fixture names are built with the providers' REAL encoding/hash functions
	// so the resolver is tested against exactly what the agents produce.
	encRepo1 := claudecode.EncodeProjectDirName(f.repo1)
	encRepo1Sub := claudecode.EncodeProjectDirName(f.repo1Sub)
	hashRepo2 := cursorcli.ProjectHash(f.repo2)

	codexInRepo2Sub := writeCodexSession(t, f.roots.Codex, "rollout-repo2sub.jsonl", f.repo2Sub)
	codexOutside := writeCodexSession(t, f.roots.Codex, "rollout-outside.jsonl", t.TempDir())
	codexEmpty := filepath.Join(f.roots.Codex, "2026", "07", "15", "rollout-empty.jsonl")
	if err := os.WriteFile(codexEmpty, nil, 0o644); err != nil {
		t.Fatalf("failed to write empty codex file: %v", err)
	}

	tests := []struct {
		name      string
		eventPath string
		wantRepo  string
		wantOK    bool
	}{
		{
			name:      "claude project dir created for repo root",
			eventPath: filepath.Join(f.roots.Claude, encRepo1),
			wantRepo:  f.repo1,
			wantOK:    true,
		},
		{
			name:      "claude jsonl inside project dir",
			eventPath: filepath.Join(f.roots.Claude, encRepo1, "0198c5c1-dead-beef.jsonl"),
			wantRepo:  f.repo1,
			wantOK:    true,
		},
		{
			name:      "claude session cwd in repo subdirectory",
			eventPath: filepath.Join(f.roots.Claude, encRepo1Sub, "session.jsonl"),
			wantRepo:  f.repo1,
			wantOK:    true,
		},
		{
			name:      "claude unknown project",
			eventPath: filepath.Join(f.roots.Claude, "-Users-somebody-else-project"),
			wantOK:    false,
		},
		{
			name:      "codex session cwd in repo subdirectory",
			eventPath: codexInRepo2Sub,
			wantRepo:  f.repo2,
			wantOK:    true,
		},
		{
			name:      "codex session cwd outside all repos",
			eventPath: codexOutside,
			wantOK:    false,
		},
		{
			name: "codex file with no metadata yet",
			// Codex creates the .jsonl before writing session_meta; the Create
			// event must resolve to nothing (a later Write retries).
			eventPath: codexEmpty,
			wantOK:    false,
		},
		{
			name:      "codex non-jsonl entry (new date directory)",
			eventPath: filepath.Join(f.roots.Codex, "2026", "07", "16"),
			wantOK:    false,
		},
		{
			name:      "cursor new session dir under known hash",
			eventPath: filepath.Join(f.roots.Cursor, hashRepo2, "0198c5c1-1111-2222-3333-444444444444"),
			wantRepo:  f.repo2,
			wantOK:    true,
		},
		{
			name:      "cursor unknown hash",
			eventPath: filepath.Join(f.roots.Cursor, "00000000000000000000000000000000", "session"),
			wantOK:    false,
		},
		{
			name:      "event outside every storage root",
			eventPath: filepath.Join(f.repo1, "README.md"),
			wantOK:    false,
		},
		{
			name:      "storage root itself resolves to nothing",
			eventPath: f.roots.Claude,
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, ok := r.Resolve(tt.eventPath)
			if ok != tt.wantOK {
				t.Fatalf("Resolve(%q) ok = %v, want %v (repo=%q)", tt.eventPath, ok, tt.wantOK, repo)
			}
			if ok && repo != tt.wantRepo {
				t.Errorf("Resolve(%q) = %q, want %q", tt.eventPath, repo, tt.wantRepo)
			}
		})
	}
}

// TestResolver_LongestPrefixMapping pins the longest-prefix rule: when one
// discovered repo contains another, a session cwd inside the inner repo maps
// to the inner repo, not the enclosing one.
func TestResolver_LongestPrefixMapping(t *testing.T) {
	f := newActivityFixture(t)
	outer := filepath.Dir(f.repo2) // .../nested contains .../nested/repo2
	r := NewResolver([]string{outer, f.repo2}, f.roots)

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{name: "cwd inside inner repo maps to inner", cwd: f.repo2Sub, want: f.repo2},
		{name: "cwd at inner repo root maps to inner", cwd: f.repo2, want: f.repo2},
		{name: "cwd in outer but not inner maps to outer", cwd: outer, want: outer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := writeCodexSession(t, f.roots.Codex, "rollout-"+tt.name+".jsonl", tt.cwd)
			repo, ok := r.Resolve(p)
			if !ok {
				t.Fatalf("Resolve(%q) ok = false, want true", p)
			}
			if repo != tt.want {
				t.Errorf("Resolve(cwd=%q) = %q, want %q", tt.cwd, repo, tt.want)
			}
		})
	}
}

// TestResolver_ClaudeSiblingNamePrefix guards the encoded-prefix boundary: a
// project dir for /path/repo2x must not match discovered repo /path/repo2 even
// though the raw string shares a prefix.
func TestResolver_ClaudeSiblingNamePrefix(t *testing.T) {
	f := newActivityFixture(t)
	sibling := f.repo1 + "x" // e.g. .../repo1x next to .../repo1
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("failed to create sibling dir: %v", err)
	}

	r := NewResolver(f.repos(), f.roots)
	event := filepath.Join(f.roots.Claude, claudecode.EncodeProjectDirName(sibling), "s.jsonl")
	if repo, ok := r.Resolve(event); ok {
		t.Errorf("Resolve(%q) = %q, want no match for sibling-named project", event, repo)
	}
}
