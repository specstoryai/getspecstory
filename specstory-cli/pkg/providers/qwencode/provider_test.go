package qwencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderName(t *testing.T) {
	p := NewProvider()
	if p.Name() != "Qwen Code" {
		t.Errorf("Name() = %q, want Qwen Code", p.Name())
	}
}

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

	// Point the package's cwd lookup at the project so an empty projectPath
	// resolves there.
	origGetwd := osGetwd
	osGetwd = func() (string, error) { return projectPath, nil }
	t.Cleanup(func() { osGetwd = origGetwd })

	p := NewProvider()
	sessions, err := p.GetAgentChatSessions("", false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1", len(sessions))
	}
	if got := sessions[0].SessionData.WorkspaceRoot; got != projectPath {
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
	if ref.OriginCwd != "/Users/dev/project" {
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
