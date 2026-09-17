package grokbuild

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestExecReportsWatcherFailureAfterChildExit(t *testing.T) {
	if os.Getenv("GROK_QA_CHILD") != "" {
		// The watcher starts before this child does. Replace its empty store
		// with a file to cause a genuine reconciliation error during the run.
		store := filepath.Join(os.Getenv("GROK_HOME"), "sessions")
		if err := os.Remove(store); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(store, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("GROK_QA_CHILD") == "failure" {
			os.Exit(7)
		}
		return
	}
	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			home := withFakeGrokHome(t)
			if err := os.Mkdir(filepath.Join(home, "sessions"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GROK_QA_CHILD", outcome)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			err = NewProvider().ExecAgentAndWatch(t.TempDir(), fmt.Sprintf("%q -test.run=^TestExecReportsWatcherFailureAfterChildExit$", exe), "", false, nil)
			if outcome == "success" {
				if err == nil || !strings.Contains(err.Error(), "directory") {
					t.Fatalf("watcher failure was swallowed: %v", err)
				}
			} else {
				var child *spi.AgentExitError
				if !errors.As(err, &child) || child.Code != 7 {
					t.Fatalf("child exit code was lost: %v", err)
				}
			}
		})
	}
}

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

	groupDir := filepath.Join(home, "sessions", EncodeCwdDirname(spi.CanonicalizePathOrClean(projectPath)))
	sessionDir := filepath.Join(groupDir, sessionID)
	copyFixture(t, fixture, sessionDir)

	// Rewrite the fixture's identity so it matches where it now lives. Decode
	// and re-encode rather than splicing the path into the raw text: a Windows
	// path's backslashes would otherwise become invalid JSON escapes and
	// silently corrupt the file, taking session_kind and the title with it.
	summaryPath := filepath.Join(sessionDir, summaryFile)
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	info, ok := summary["info"].(map[string]any)
	if !ok {
		t.Fatalf("fixture %s has no info object", fixture)
	}
	info["cwd"] = projectPath
	updated, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summaryPath, updated, 0o644); err != nil {
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

func TestRawSnapshotAndDebugRefresh(t *testing.T) {
	spi.SetDebugBaseDir(t.TempDir())
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	dir := t.TempDir()
	copyFixture(t, "session-basic", dir)
	path := filepath.Join(dir, chatHistoryFile)
	first := `{"type":"user","content":[{"type":"text","text":"<user_query>snapshot</user_query>"}],"future_field":{"keep":true}}`
	second := `{"type":"assistant","content":"original reply"}`
	if err := os.WriteFile(path, []byte(first+"\nmalformed\n"+second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A later native write cannot change the raw export of an already parsed session.
	if err := os.WriteFile(path, []byte(first+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chat := convertToAgentChatSession(parsed, dir, true)
	if chat == nil || chat.RawData != first+"\n"+second+"\n" {
		t.Fatalf("inconsistent raw snapshot: %+v", chat)
	}
	debugDir := spi.GetDebugDir(parsed.ID)
	debug, err := os.ReadFile(filepath.Join(debugDir, "1.json"))
	if err != nil || !strings.Contains(string(debug), "future_field") {
		t.Fatalf("native field missing: %s, %v", debug, err)
	}
	cliFile := filepath.Join(debugDir, "session-data.json")
	if err := os.WriteFile(cliFile, []byte("CLI owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	shorter, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	convertToAgentChatSession(shorter, dir, false)
	if _, err := os.Stat(filepath.Join(debugDir, "2.json")); err != nil {
		t.Fatal("debug=false changed exports", err)
	}
	convertToAgentChatSession(shorter, dir, true)
	if _, err := os.Stat(filepath.Join(debugDir, "2.json")); !os.IsNotExist(err) {
		t.Fatalf("stale record remains: %v", err)
	}
	if data, err := os.ReadFile(cliFile); err != nil || string(data) != "CLI owned" {
		t.Fatalf("CLI file changed: %s, %v", data, err)
	}
}

func TestGetSessionNotFoundAndTraversal(t *testing.T) {
	withFakeGrokHome(t)
	for _, id := range []string{"11111111-2222-7333-8444-555555555555", "../other-project/session", ""} {
		session, err := NewProvider().GetAgentChatSession(t.TempDir(), id, false)
		if err != nil || session != nil {
			t.Errorf("missing %q = %v, %v; want nil, nil", id, session, err)
		}
	}
}

func TestGetSessionDoesNotFollowCrossProjectSymlink(t *testing.T) {
	home := withFakeGrokHome(t)
	project, other := t.TempDir(), t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	target := seedSession(t, home, other, "session-basic", id)
	group := filepath.Join(home, "sessions", EncodeCwdDirname(spi.CanonicalizePathOrClean(project)))
	if err := os.MkdirAll(group, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(group, id)); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	got, err := NewProvider().GetAgentChatSession(project, id, false)
	if err != nil || got != nil {
		t.Fatalf("cross-project symlink returned %v, %v", got, err)
	}
}

func TestDebugSidecarsPreserveTheParsingSnapshot(t *testing.T) {
	spi.SetDebugBaseDir(t.TempDir())
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	dir := t.TempDir()
	copyFixture(t, "session-basic", dir)
	path := filepath.Join(dir, eventsFile)
	event := `{"type":"future_event","future_field":"original-sidecar"}`
	if err := os.WriteFile(path, []byte(event+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDebugRawFiles(session); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(spi.GetDebugDir(session.ID), "native-sidecars.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"original-sidecar", summaryFile, updatesFile, eventsFile} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("sidecar debug missing %q", want)
		}
	}
}
