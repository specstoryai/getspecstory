package qwencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestCheck_InvalidCommand(t *testing.T) {
	p := NewProvider()
	result := p.Check("definitely-not-a-real-binary-xyz")
	if result.Success {
		t.Error("Check should fail for nonexistent command")
	}
	if !strings.Contains(result.ErrorMessage, "could not be found") {
		t.Errorf("unexpected error message: %q", result.ErrorMessage)
	}
}

func TestDetectAgent_NoData(t *testing.T) {
	withFakeHome(t)
	p := NewProvider()
	if p.DetectAgent(t.TempDir(), false) {
		t.Error("DetectAgent should return false when no Qwen data exists")
	}
}

// seedFakeSession copies a testdata fixture into a fake Qwen store for
// projectPath and returns the transcript's path.
func seedFakeSession(t *testing.T, home, projectPath, fixture, sessionID string) string {
	t.Helper()

	canonical, err := filepath.EvalSymlinks(projectPath)
	if err != nil {
		canonical = projectPath
	}

	chatsDir := filepath.Join(home, ".qwen", "projects", SanitizeQwenCwd(canonical), "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	cwdJSON, _ := json.Marshal(canonical)
	data = []byte(strings.ReplaceAll(string(data), `"/Users/dev/project"`, string(cwdJSON)))
	dest := filepath.Join(chatsDir, sessionID+".jsonl")
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dest
}

func TestGetAgentChatSessions(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()
	seedFakeSession(t, home, projectPath, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")

	p := NewProvider()

	if !p.DetectAgent(projectPath, false) {
		t.Error("DetectAgent should return true when chats exist")
	}

	sessions, err := p.GetAgentChatSessions(projectPath, false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}

	s := sessions[0]
	if s.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("SessionID = %q", s.SessionID)
	}
	if s.Slug == "" || s.Slug == "qwen-session" {
		t.Errorf("Slug should derive from first user message, got %q", s.Slug)
	}
	if s.SessionData == nil || len(s.SessionData.Exchanges) != 2 {
		t.Errorf("SessionData exchanges wrong: %+v", s.SessionData)
	}
	if !strings.Contains(s.RawData, "hey what tools do you have") {
		t.Error("RawData should carry the original JSONL transcript")
	}
}

func TestGetAgentChatSessions_EmptyProjectPathUsesCwd(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()
	seedFakeSession(t, home, projectPath, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")

	t.Chdir(projectPath)

	p := NewProvider()
	sessions, err := p.GetAgentChatSessions("", false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if got := sessions[0].SessionData.WorkspaceRoot; got != spi.CanonicalizePathOrClean(projectPath) {
		t.Errorf("WorkspaceRoot = %q, want the defaulted cwd %q (never empty)", got, projectPath)
	}
}

func TestGetAgentChatSession_ByID(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()
	seedFakeSession(t, home, projectPath, "session-tools.jsonl", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

	p := NewProvider()

	session, err := p.GetAgentChatSession(projectPath, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession failed: %v", err)
	}
	if session == nil {
		t.Fatal("session not found")
	}

	missing, err := p.GetAgentChatSession(projectPath, "00000000-0000-0000-0000-000000000000", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession for missing ID errored: %v", err)
	}
	if missing != nil {
		t.Error("expected nil for unknown session ID")
	}
}

func TestGetAgentChatSessionByPath(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()
	path := seedFakeSession(t, home, projectPath, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")

	p := NewProvider()
	session, err := p.GetAgentChatSessionByPath(path, projectPath, false)
	if err != nil {
		t.Fatalf("GetAgentChatSessionByPath failed: %v", err)
	}
	if session == nil || session.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("unexpected session: %+v", session)
	}
	if session.SessionData.WorkspaceRoot != projectPath {
		t.Errorf("WorkspaceRoot = %q, want origin cwd %q", session.SessionData.WorkspaceRoot, projectPath)
	}
}

func TestReindexRejectsSymlinkedTranscripts(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	path := seedFakeSession(t, home, project, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.jsonl")
	if err := os.WriteFile(external, data, 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "linked.jsonl")
	if err := os.Symlink(external, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlinks requires Windows developer mode or privileges: %v", err)
		}
		t.Fatal(err)
	}

	p := NewProvider()
	var progress spi.ScanReporter
	refs, err := p.ListAllAgentChatSessionsProgress(&progress)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].NativePath != path || progress.Found() != 1 {
		t.Errorf("enumeration must include only the regular transcript: refs=%+v found=%d", refs, progress.Found())
	}
	if session, err := p.GetAgentChatSessionByPath(link, project, false); err != nil || session != nil {
		t.Errorf("path lookup followed symlink: session=%v error=%v", session, err)
	}
	if session, err := p.GetAgentChatSessionByPath(path, project, false); err != nil || session == nil {
		t.Fatalf("regular transcript must still load: session=%v error=%v", session, err)
	}

	// A previously enumerated path must also be rejected if replaced before full loading.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	if session, err := p.GetAgentChatSessionByPath(path, project, false); err != nil || session != nil {
		t.Errorf("replaced transcript followed symlink: session=%v error=%v", session, err)
	}
}

func TestListAgentChatSessions(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()
	seedFakeSession(t, home, projectPath, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")
	// A system-only session must be filtered from listings
	seedFakeSession(t, home, projectPath, "session-system-only.jsonl", "00000000-1111-2222-3333-444444444444")

	p := NewProvider()
	metadata, err := p.ListAgentChatSessions(projectPath)
	if err != nil {
		t.Fatalf("ListAgentChatSessions failed: %v", err)
	}
	if len(metadata) != 1 {
		t.Fatalf("metadata count = %d, want 1 (system-only session skipped)", len(metadata))
	}
	if metadata[0].SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("SessionID = %q", metadata[0].SessionID)
	}
	if metadata[0].Name == "" {
		t.Error("Name should be derived from first user message")
	}
}

func TestListAllAgentChatSessions(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()
	path := seedFakeSession(t, home, projectPath, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")

	p := NewProvider()
	refs, err := p.ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions failed: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("ref count = %d, want 1", len(refs))
	}
	ref := refs[0]
	if ref.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("SessionID = %q", ref.SessionID)
	}
	if ref.NativePath != path {
		t.Errorf("NativePath = %q, want %q", ref.NativePath, path)
	}
	// OriginCwd comes from inside the transcript, not the directory name
	if ref.OriginCwd != spi.CanonicalizePathOrClean(projectPath) {
		t.Errorf("OriginCwd = %q, want /Users/dev/project", ref.OriginCwd)
	}
}

func TestListAllAgentChatSessions_NoStore(t *testing.T) {
	withFakeHome(t)
	p := NewProvider()
	refs, err := p.ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions failed: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("ref count = %d, want 0", len(refs))
	}
}

func TestSessionProjectBoundary(t *testing.T) {
	home := withFakeHome(t)
	project := filepath.Join(t.TempDir(), "a-b")
	other := strings.TrimSuffix(project, "a-b") + "a_b"
	for _, dir := range []string{project, other} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	const id = "11111111-2222-3333-4444-555555555555"
	path := seedFakeSession(t, home, other, "session-basic.jsonl", id)
	p := NewProvider()
	if session, err := p.GetAgentChatSession(project, id, false); err != nil || session != nil {
		t.Fatalf("cross-project lookup: %v, %v", session, err)
	}
	if sessions, err := p.GetAgentChatSessions(project, false, nil); err != nil || len(sessions) != 0 {
		t.Fatalf("cross-project sync: %d, %v", len(sessions), err)
	}
	if sessions, err := p.ListAgentChatSessions(project); err != nil || len(sessions) != 0 {
		t.Fatalf("cross-project list: %d, %v", len(sessions), err)
	}
	if session, err := p.GetAgentChatSession(other, id, false); err != nil || session == nil {
		t.Fatalf("legitimate lookup failed: %v", err)
	}
	external := filepath.Join(home, "external.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, data, 0644); err != nil {
		t.Fatal(err)
	}
	traversal, err := filepath.Rel(filepath.Dir(path), strings.TrimSuffix(external, ".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if session, _ := p.GetAgentChatSession(other, traversal, false); session != nil {
		t.Fatal("traversal returned external transcript")
	}
	if session, _ := p.GetAgentChatSession(other, strings.ReplaceAll(traversal, "/", `\`), false); session != nil {
		t.Fatal("Windows traversal returned transcript")
	}
}

func TestSyncSkipsEmptyAndReportsEveryFile(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	seedFakeSession(t, home, project, "session-system-only.jsonl", "empty")
	path := seedFakeSession(t, home, project, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "broken.jsonl"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "session.ledger.jsonl"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	sessions, err := NewProvider().GetAgentChatSessions(project, false, func(current, total int) {
		calls++
		if current != calls || total != 3 {
			t.Errorf("progress %d/%d, call %d", current, total, calls)
		}
	})
	if err != nil || len(sessions) != 1 || calls != 3 {
		t.Fatalf("sessions=%d progress=%d error=%v", len(sessions), calls, err)
	}
}

func TestDebugRawPreservesUnknownFieldsAndClearsStaleRecords(t *testing.T) {
	spi.SetDebugBaseDir(t.TempDir())
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	path := filepath.Join(t.TempDir(), "debug.jsonl")
	raw := `{"sessionId":"debug","type":"user","timestamp":"2026-09-15T00:00:00Z","message":{"role":"user","parts":[{"text":"hello"}]},"unknown":{"preserve":true}}`
	if err := os.WriteFile(path, []byte(raw+"\n"+raw+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDebugRawFiles(session); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	session, err = ParseSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDebugRawFiles(session); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(spi.GetDebugDir("debug"), "1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"preserve": true`) {
		t.Fatal("unknown native fields lost")
	}
	if _, err := os.Stat(filepath.Join(spi.GetDebugDir("debug"), "2.json")); !os.IsNotExist(err) {
		t.Fatal("stale debug record remains")
	}
}

func TestProjectOwnershipAllowsQwenWorktreeOnly(t *testing.T) {
	project := spi.CanonicalizePathOrClean(t.TempDir())
	worktree := filepath.Join(project, ".qwen", "worktrees", "feature")
	if !sessionBelongsToProject(&QwenSession{Cwd: worktree}, project) {
		t.Fatal("native worktree attribution rejected")
	}
	if sessionBelongsToProject(&QwenSession{Cwd: filepath.Join(project, "other-child")}, project) {
		t.Fatal("arbitrary child attributed to project")
	}
}

func TestEnumerationOmitsNestedStoresAndKeepsUnknownOrigin(t *testing.T) {
	home := withFakeHome(t)
	project := t.TempDir()
	path := seedFakeSession(t, home, project, "session-basic.jsonl", "11111111-2222-3333-4444-555555555555")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		delete(record, "cwd")
		record["sessionId"] = "unknown-origin"
		encoded, _ := json.Marshal(record)
		lines = append(lines, string(encoded))
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "unknown-origin.jsonl"), []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(filepath.Dir(path), "nested", "chats")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "nested.jsonl"), data, 0644); err != nil {
		t.Fatal(err)
	}
	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("enumeration included nested sessions: %d", len(refs))
	}
	for _, ref := range refs {
		if ref.SessionID == "unknown-origin" && ref.OriginCwd != "" {
			t.Fatal("invented unknown origin")
		}
	}
}
