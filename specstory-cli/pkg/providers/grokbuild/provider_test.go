package grokbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderName(t *testing.T) {
	if got := NewProvider().Name(); got != "Grok Build" {
		t.Errorf("Name() = %q, want Grok Build", got)
	}
}

func TestCheck_InvalidCommand(t *testing.T) {
	result := NewProvider().Check("definitely-not-a-real-binary-xyz")
	if result.Success {
		t.Error("Check should fail for a command that does not exist")
	}
	if !strings.Contains(result.ErrorMessage, "could not be found") {
		t.Errorf("unexpected error message: %q", result.ErrorMessage)
	}
}

// withFakeGrokHome points the provider at a temporary session store.
func withFakeGrokHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GROK_HOME", home)
	return home
}

// seedSession copies a fixture into a store-shaped location for projectPath.
func seedSession(t *testing.T, home, projectPath, fixture, sessionID string) string {
	t.Helper()

	groupDir := filepath.Join(home, "sessions", EncodeCwdDirname(canonical(projectPath)))
	sessionDir := filepath.Join(groupDir, sessionID)
	copyFixture(t, fixture, sessionDir)

	// Rewrite the fixture's identity so it matches where it now lives.
	summaryPath := filepath.Join(sessionDir, summaryFile)
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.ReplaceAll(string(data), "/Users/dev/project", projectPath)
	if err := os.WriteFile(summaryPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionDir
}

func TestDetectAgent(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()

	provider := NewProvider()
	if provider.DetectAgent(project, false) {
		t.Error("DetectAgent should be false before any session exists")
	}

	seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")

	if !provider.DetectAgent(project, false) {
		t.Error("DetectAgent should be true once a session exists")
	}
}

func TestGetAgentChatSessions(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	// A subagent session sits beside real ones and must not be returned.
	seedSession(t, home, project, "session-subagent", "99999999-8888-7777-6666-555555555555")

	sessions, err := NewProvider().GetAgentChatSessions(project, false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1 (the subagent must be excluded)", len(sessions))
	}
	session := sessions[0]
	if session.SessionID != "11111111-2222-7333-8444-555555555555" {
		t.Errorf("SessionID = %q", session.SessionID)
	}
	if session.Slug == "" || session.Slug == "grok-session" {
		t.Errorf("slug should come from the first prompt, got %q", session.Slug)
	}
	if !strings.Contains(session.RawData, "user_query") {
		t.Error("RawData should carry the original transcript")
	}
	if session.SessionData.WorkspaceRoot != project {
		t.Errorf("WorkspaceRoot = %q, want %q", session.SessionData.WorkspaceRoot, project)
	}
}

func TestGetAgentChatSession_ByID(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	seedSession(t, home, project, "session-tools", "aaaaaaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee")

	provider := NewProvider()

	session, err := provider.GetAgentChatSession(project, "aaaaaaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession failed: %v", err)
	}
	if session == nil {
		t.Fatal("session not found")
	}

	missing, err := provider.GetAgentChatSession(project, "00000000-0000-0000-0000-000000000000", false)
	if err != nil {
		t.Fatalf("lookup of a missing session errored: %v", err)
	}
	if missing != nil {
		t.Error("expected nil for an unknown session id")
	}
}

func TestGetAgentChatSession_SubagentNotReturnedByID(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	seedSession(t, home, project, "session-subagent", "99999999-8888-7777-6666-555555555555")

	session, err := NewProvider().GetAgentChatSession(project, "99999999-8888-7777-6666-555555555555", false)
	if err != nil {
		t.Fatalf("GetAgentChatSession failed: %v", err)
	}
	if session != nil {
		t.Error("a subagent session should not be returned even when asked for by id")
	}
}

func TestConvertSkipsSessionWithoutConversation(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	seedSession(t, home, project, "session-noquery", "dddddddd-eeee-7fff-8000-111111111111")

	sessions, err := NewProvider().GetAgentChatSessions(project, false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}
	// An aborted session holds no conversation, and an empty markdown file is
	// worse than none.
	if len(sessions) != 0 {
		t.Errorf("session count = %d, want 0", len(sessions))
	}
}

func TestListAgentChatSessions(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	seedSession(t, home, project, "session-noquery", "dddddddd-eeee-7fff-8000-111111111111")

	metadata, err := NewProvider().ListAgentChatSessions(project)
	if err != nil {
		t.Fatalf("ListAgentChatSessions failed: %v", err)
	}

	if len(metadata) != 1 {
		t.Fatalf("metadata count = %d, want 1", len(metadata))
	}
	// Grok titles its own sessions, which reads better than a slug.
	if metadata[0].Name != "Read the README" {
		t.Errorf("Name = %q, want the Grok title", metadata[0].Name)
	}
}

func TestListAllAgentChatSessions(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	seedSession(t, home, project, "session-subagent", "99999999-8888-7777-6666-555555555555")

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions failed: %v", err)
	}

	if len(refs) != 1 {
		t.Fatalf("ref count = %d, want 1 (the subagent must be excluded)", len(refs))
	}
	ref := refs[0]
	if ref.NativePath != filepath.Join(dir, chatHistoryFile) {
		t.Errorf("NativePath = %q", ref.NativePath)
	}
	// The originating directory is read from inside the session rather than from
	// the encoded directory name.
	if ref.OriginCwd != project {
		t.Errorf("OriginCwd = %q, want %q", ref.OriginCwd, project)
	}
}

func TestListAllAgentChatSessions_NoStore(t *testing.T) {
	withFakeGrokHome(t)

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions failed: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("ref count = %d, want 0", len(refs))
	}
}

func TestGetAgentChatSessionByPath(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")

	session, err := NewProvider().GetAgentChatSessionByPath(filepath.Join(dir, chatHistoryFile), project, false)
	if err != nil {
		t.Fatalf("GetAgentChatSessionByPath failed: %v", err)
	}
	if session == nil || session.SessionID != "11111111-2222-7333-8444-555555555555" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if session.SessionData.WorkspaceRoot != project {
		t.Errorf("WorkspaceRoot = %q, want the origin cwd", session.SessionData.WorkspaceRoot)
	}
}
