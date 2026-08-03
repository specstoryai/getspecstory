package qwencode

import (
	"os"
	"path/filepath"
	"testing"
)

// setupFakeQwenHome points the package's home-directory lookup at a temp dir
// containing ~/.qwen/projects/<encoded>/chats/<file> transcripts copied from
// testdata. Returns the fake home and the project path the transcripts claim.
func setupFakeQwenHome(t *testing.T) (fakeHome string, projectPath string) {
	t.Helper()

	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome = t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectPath = "/tmp/qwen-fixture-project"
	chatsDir := filepath.Join(fakeHome, ".qwen", "projects", "-tmp-qwen-fixture-project", "chats")
	if err := os.MkdirAll(chatsDir, 0o755); err != nil {
		t.Fatalf("failed to create chats dir: %v", err)
	}

	for _, name := range []string{
		"3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b.jsonl",
		"9a8b7c6d-1111-2222-3333-444455556666.jsonl",
		"0f0e0d0c-aaaa-bbbb-cccc-ddddeeeeffff.jsonl",
	} {
		src := map[string]string{
			"3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b.jsonl": "session-1.jsonl",
			"9a8b7c6d-1111-2222-3333-444455556666.jsonl": "session-malformed.jsonl",
			"0f0e0d0c-aaaa-bbbb-cccc-ddddeeeeffff.jsonl": "session-system-only.jsonl",
		}[name]
		data, err := os.ReadFile(filepath.Join("testdata", src))
		if err != nil {
			t.Fatalf("failed to read testdata %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(chatsDir, name), data, 0o644); err != nil {
			t.Fatalf("failed to write fixture %s: %v", name, err)
		}
	}

	return fakeHome, projectPath
}

func TestName(t *testing.T) {
	p := NewProvider()
	if got := p.Name(); got != "Qwen Code" {
		t.Errorf("Name() = %q, want %q", got, "Qwen Code")
	}
}

func TestDetectAgent(t *testing.T) {
	p := NewProvider()
	_, projectPath := setupFakeQwenHome(t)

	if !p.DetectAgent(projectPath, false) {
		t.Error("DetectAgent = false, want true for a project with transcripts")
	}
	if !p.DetectAgent(projectPath, true) {
		t.Error("DetectAgent(helpOutput) = false, want true")
	}
}

func TestDetectAgentNoQwenDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})
	osUserHomeDir = func() (string, error) {
		return t.TempDir(), nil
	}

	p := NewProvider()
	if p.DetectAgent("/tmp/never-used", false) {
		t.Error("DetectAgent = true, want false when ~/.qwen is missing")
	}
}

func TestDetectAgentEmptyChats(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})
	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectDir := filepath.Join(fakeHome, ".qwen", "projects", "-tmp-empty-project", "chats")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create chats dir: %v", err)
	}

	p := NewProvider()
	if p.DetectAgent("/tmp/empty-project", false) {
		t.Error("DetectAgent = true, want false for an empty chats directory")
	}
}

func TestGetAgentChatSessions(t *testing.T) {
	p := NewProvider()
	_, projectPath := setupFakeQwenHome(t)

	sessions, err := p.GetAgentChatSessions(projectPath, false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions returned error: %v", err)
	}

	// session-1 and session-malformed both contain real messages;
	// session-system-only has none and must be dropped.
	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2", len(sessions))
	}

	byID := make(map[string]bool)
	for _, s := range sessions {
		byID[s.SessionID] = true
		if s.Slug == "" {
			t.Errorf("session %s has empty slug", s.SessionID)
		}
		if s.SessionData == nil {
			t.Errorf("session %s has nil SessionData", s.SessionID)
		}
		if s.RawData == "" {
			t.Errorf("session %s has empty RawData", s.SessionID)
		}
	}
	if !byID["3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b"] {
		t.Error("missing session 3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b")
	}
	if !byID["9a8b7c6d-1111-2222-3333-444455556666"] {
		t.Error("missing session 9a8b7c6d-1111-2222-3333-444455556666")
	}
}

func TestGetAgentChatSessionDirectPath(t *testing.T) {
	p := NewProvider()
	_, projectPath := setupFakeQwenHome(t)

	session, err := p.GetAgentChatSession(projectPath, "3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession returned error: %v", err)
	}
	if session == nil {
		t.Fatal("GetAgentChatSession returned nil session")
	}
	if session.SessionData.SessionID != "3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b" {
		t.Errorf("session ID = %q, want the requested id", session.SessionData.SessionID)
	}

	// Unknown session id -> nil, no error
	missing, err := p.GetAgentChatSession(projectPath, "no-such-session", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession returned error for unknown id: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil session for unknown id, got %+v", missing)
	}
}

func TestGetAgentChatSessionByPath(t *testing.T) {
	p := NewProvider()
	fakeHome, projectPath := setupFakeQwenHome(t)

	nativePath := filepath.Join(fakeHome, ".qwen", "projects", "-tmp-qwen-fixture-project",
		"chats", "3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b.jsonl")
	session, err := p.GetAgentChatSessionByPath(nativePath, projectPath, false)
	if err != nil {
		t.Fatalf("GetAgentChatSessionByPath returned error: %v", err)
	}
	if session == nil || session.SessionData.SessionID != "3f2c1a9e-8b4d-4e5f-9a6b-1c2d3e4f5a6b" {
		t.Errorf("unexpected session from native path: %+v", session)
	}
}

func TestListAgentChatSessions(t *testing.T) {
	p := NewProvider()
	_, projectPath := setupFakeQwenHome(t)

	metadata, err := p.ListAgentChatSessions(projectPath)
	if err != nil {
		t.Fatalf("ListAgentChatSessions returned error: %v", err)
	}

	// The system-only transcript must be excluded.
	if len(metadata) != 2 {
		t.Fatalf("metadata count = %d, want 2", len(metadata))
	}

	for _, m := range metadata {
		if m.SessionID == "" || m.CreatedAt == "" || m.Slug == "" {
			t.Errorf("incomplete metadata: %+v", m)
		}
	}
}

func TestListAllAgentChatSessions(t *testing.T) {
	p := NewProvider()
	_, _ = setupFakeQwenHome(t)

	refs, err := p.ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions returned error: %v", err)
	}

	if len(refs) != 2 {
		t.Fatalf("ref count = %d, want 2", len(refs))
	}

	for _, ref := range refs {
		if ref.OriginCwd != "/tmp/qwen-fixture-project" {
			t.Errorf("ref %s OriginCwd = %q, want %q", ref.SessionID, ref.OriginCwd, "/tmp/qwen-fixture-project")
		}
		if ref.NativePath == "" {
			t.Errorf("ref %s has empty NativePath", ref.SessionID)
		}
		if ref.Slug == "" {
			t.Errorf("ref %s has empty Slug", ref.SessionID)
		}
	}
}

func TestListAllAgentChatSessionsNoProjectsDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})
	osUserHomeDir = func() (string, error) {
		return t.TempDir(), nil
	}

	p := NewProvider()
	refs, err := p.ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions returned error: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("ref count = %d, want 0", len(refs))
	}
}

func TestSupportsReconstruction(t *testing.T) {
	p := NewProvider()
	if p.SupportsReconstruction() {
		t.Error("SupportsReconstruction = true, want false until a serializer exists")
	}
}
