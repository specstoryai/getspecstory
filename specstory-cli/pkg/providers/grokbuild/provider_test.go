package grokbuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
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
	testutil.IsolateDebugDir(t)
	dir := filepath.Join(t.TempDir(), "11111111-2222-7333-8444-555555555555")
	copyFixture(t, "session-basic", dir)
	path := filepath.Join(dir, chatHistoryFile)
	first := `{"type":"user","content":[{"type":"text","text":"<user_query>snapshot</user_query>"}],"future_field":{"keep":true}}`
	second := `{"type":"assistant","content":"original reply"}`
	third := `{"type":"future_record","z":9007199254740993,"a":1.234567890123456789}`
	if err := os.WriteFile(path, []byte(first+"\nmalformed\n"+second+"\n"+third+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	debugDir := spi.GetDebugDir(parsed.ID)
	convertToAgentChatSession(parsed, dir, false)
	if _, err := os.Stat(debugDir); !os.IsNotExist(err) {
		t.Fatalf("debug=false created exports: %v", err)
	}
	// A later native write cannot change the raw export of an already parsed session.
	if err := os.WriteFile(path, []byte(first+"\n"+second+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chat := convertToAgentChatSession(parsed, dir, true)
	if chat == nil || chat.RawData != first+"\n"+second+"\n"+third+"\n" {
		t.Fatalf("inconsistent raw snapshot: %+v", chat)
	}
	debug, err := os.ReadFile(filepath.Join(debugDir, "1.json"))
	if err != nil || !strings.Contains(string(debug), "future_field") {
		t.Fatalf("native field missing: %s, %v", debug, err)
	}
	debug, err = os.ReadFile(filepath.Join(debugDir, "3.json"))
	if err != nil || !bytes.Contains(debug, []byte("\n  \"z\": ")) {
		t.Fatalf("native record not pretty-printed: %s (%v)", debug, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, debug); err != nil || compact.String() != third {
		t.Fatalf("native precision or key order lost: %s (%v)", debug, err)
	}
	shorter, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	convertToAgentChatSession(shorter, dir, false)
	if _, err := os.Stat(filepath.Join(debugDir, "3.json")); err != nil {
		t.Fatal("debug=false changed exports", err)
	}
	testutil.AssertDebugRefresh(t, debugDir, []string{"3.json"},
		[]string{"session-data.json", "03.json", "+3.json", "notes.json", "nested/notes.md"}, func() {
			if convertToAgentChatSession(shorter, dir, true) == nil {
				t.Fatal("refresh prevented conversion")
			}
		})
}

func TestDebugRefreshWithoutRenderedConversation(t *testing.T) {
	for _, mode := range []string{"bulk", "single", "path"} {
		t.Run(mode, func(t *testing.T) {
			testutil.IsolateDebugDir(t)
			home := withFakeGrokHome(t)
			project := t.TempDir()
			id := "11111111-2222-7333-8444-555555555555"
			dir := seedSession(t, home, project, "session-basic", id)
			provider := NewProvider()
			read := func(debug bool) int {
				t.Helper()
				if mode == "bulk" {
					sessions, err := provider.GetAgentChatSessions(project, debug, nil)
					if err != nil {
						t.Fatal(err)
					}
					return len(sessions)
				}
				var session *spi.AgentChatSession
				var err error
				if mode == "single" {
					session, err = provider.GetAgentChatSession(project, id, debug)
				} else {
					session, err = provider.GetAgentChatSessionByPath(filepath.Join(dir, chatHistoryFile), project, debug)
				}
				if err != nil {
					t.Fatal(err)
				}
				if session != nil {
					return 1
				}
				return 0
			}
			if read(true) != 1 {
				t.Fatal("initial conversation missing")
			}
			native := `{"type":"system","content":"context only"}`
			if err := os.WriteFile(filepath.Join(dir, chatHistoryFile), []byte(native+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			debugDir := spi.GetDebugDir(id)
			if read(false) != 0 {
				t.Fatal("context-only session produced a conversation")
			}
			// The helper verifies the stale files still exist after debug=false.
			testutil.AssertDebugRefresh(t, debugDir, []string{"2.json", "3.json"}, []string{"session-data.json"}, func() {
				if read(true) != 0 {
					t.Fatal("debug export produced an empty conversation")
				}
			})
			data, err := os.ReadFile(filepath.Join(debugDir, "1.json"))
			if err != nil || !bytes.Contains(data, []byte("context only")) {
				t.Fatalf("non-rendered record not refreshed: %s (%v)", data, err)
			}
		})
	}
}

func TestDebugExportFailureWarnsWithoutPreventingConversion(t *testing.T) {
	for _, filename := range []string{"1.json", "native-sidecars.json"} {
		t.Run(filename, func(t *testing.T) {
			testutil.IsolateDebugDir(t)
			dir := filepath.Join(t.TempDir(), "11111111-2222-7333-8444-555555555555")
			copyFixture(t, "session-basic", dir)
			session, err := ParseSessionDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			debugDir := spi.GetDebugDir(session.ID)
			path := filepath.Join(debugDir, filename)
			// A directory at the output filename fails writes on every platform.
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeDebugRawFiles(session); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("expected error with output path, got %v", err)
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if chat := convertToAgentChatSession(session, dir, true); chat == nil || chat.RawData == "" {
				t.Fatal("debug failure prevented conversion")
			}
			var warning map[string]any
			if err := json.Unmarshal(logs.Bytes(), &warning); err != nil {
				t.Fatalf("expected one warning, got %s (%v)", logs.Bytes(), err)
			}
			if warning["level"] != "WARN" || warning["sessionID"] != session.ID || warning["path"] != debugDir {
				t.Fatalf("warning lacks export context: %v", warning)
			}
		})
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
	testutil.IsolateDebugDir(t)
	dir := filepath.Join(t.TempDir(), "11111111-2222-7333-8444-555555555555")
	copyFixture(t, "session-basic", dir)
	path := filepath.Join(dir, eventsFile)
	event := `{"type":"future_event","future_field":"original-sidecar","z":9007199254740993,"a":1.234567890123456789}`
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
	var sidecars map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sidecars); err != nil {
		t.Fatal(err)
	}
	var events []json.RawMessage
	if err := json.Unmarshal(sidecars[eventsFile], &events); err != nil || len(events) != 1 {
		t.Fatalf("expected original sidecar event: %s (%v)", sidecars[eventsFile], err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, events[0]); err != nil || compact.String() != event {
		t.Fatalf("sidecar precision or key order lost: %s (%v)", events[0], err)
	}
}

func TestGetSessionReturnsFilesystemFailure(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	dir := seedSession(t, home, project, "session-basic", id)
	summary := filepath.Join(dir, summaryFile)
	if err := os.Remove(summary); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(summary, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := NewProvider().GetAgentChatSession(project, id, false)
	if err == nil || got != nil {
		t.Fatalf("filesystem failure hidden: %v, %v", got, err)
	}
}

func TestDiscoveryRejectsSymlinkedTranscripts(t *testing.T) {
	home := withFakeGrokHome(t)
	project, other := t.TempDir(), t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	dir := seedSession(t, home, project, "session-basic", id)
	target := seedSession(t, home, other, "session-basic", id)
	transcript := filepath.Join(dir, chatHistoryFile)
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, chatHistoryFile), transcript); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	p := NewProvider()
	if sessions, err := p.GetAgentChatSessions(project, false, nil); err != nil || len(sessions) != 0 {
		t.Fatalf("project discovery followed transcript link: %v, %v", sessions, err)
	}
	if sessions, err := p.ListAgentChatSessions(project); err != nil || len(sessions) != 0 {
		t.Fatalf("metadata discovery followed transcript link: %v, %v", sessions, err)
	}
	if session, err := p.GetAgentChatSession(project, id, false); err == nil || session != nil {
		t.Fatalf("by-id lookup followed transcript link: %v, %v", session, err)
	}
	if session, err := p.GetAgentChatSessionByPath(transcript, project, false); err == nil || session != nil {
		t.Fatalf("by-path lookup followed transcript link: %v, %v", session, err)
	}
	refs, err := p.ListAllAgentChatSessions()
	if err != nil || len(refs) != 1 || refs[0].OriginCwd != other {
		t.Fatalf("global discovery imported linked transcript: %v, %v", refs, err)
	}
}

func TestInvalidSummaryIDCannotChooseDebugPath(t *testing.T) {
	for _, bad := range []string{"../../outside", `..\..\outside`, "/absolute/outside", "not-a-uuid", "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"} {
		t.Run(bad, func(t *testing.T) {
			home := withFakeGrokHome(t)
			project := t.TempDir()
			id := "11111111-2222-7333-8444-555555555555"
			dir := seedSession(t, home, project, "session-basic", id)
			path := filepath.Join(dir, summaryFile)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var summary map[string]any
			if err := json.Unmarshal(raw, &summary); err != nil {
				t.Fatal(err)
			}
			summary["info"].(map[string]any)["id"] = bad
			raw, err = json.Marshal(summary)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			debug := testutil.IsolateDebugDir(t)
			session, err := NewProvider().GetAgentChatSession(project, id, true)
			if err != nil || session == nil || session.SessionID != id {
				t.Fatalf("unsafe summary identity accepted: %v, %v", session, err)
			}
			if _, err := os.Stat(filepath.Join(debug, id, "1.json")); err != nil {
				t.Fatalf("debug not written under native UUID: %v", err)
			}
			parsed, err := parseSessionDir(dir, true)
			if err != nil || parsed.ID != id {
				t.Fatalf("metadata identity disagrees: %v, %v", parsed, err)
			}
			refs, err := NewProvider().ListAllAgentChatSessions()
			if err != nil || len(refs) != 1 || refs[0].SessionID != id {
				t.Fatalf("global identity disagrees: %v, %v", refs, err)
			}
		})
	}
}

func TestGlobalDiscoveryRequiresNativeStoreLayout(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	native := seedSession(t, home, project, "session-basic", id)
	group := filepath.Dir(native)
	// All copies have valid native contents and UUID summary IDs. Only their
	// position in the store distinguishes them from the real session.
	for _, dir := range []string{group, filepath.Join(group, "not-a-session"), filepath.Join(native, "archive", id), filepath.Join(home, "sessions", "extra", filepath.Base(group), id)} {
		copyFixture(t, "session-basic", dir)
	}
	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil || len(refs) != 1 || refs[0].NativePath != filepath.Join(native, chatHistoryFile) {
		t.Fatalf("non-native store paths became sessions: %v, %v", refs, err)
	}
}

func TestByPathRejectsReplacedSessionDirectory(t *testing.T) {
	for _, component := range []string{"session", "group"} {
		t.Run(component, func(t *testing.T) {
			home := withFakeGrokHome(t)
			project, other := t.TempDir(), t.TempDir()
			id := "11111111-2222-7333-8444-555555555555"
			dir := seedSession(t, home, project, "session-basic", id)
			target := seedSession(t, home, other, "session-basic", id)
			transcript := filepath.Join(dir, chatHistoryFile)
			p := NewProvider()
			if session, err := p.GetAgentChatSessionByPath(transcript, project, false); err != nil || session == nil {
				t.Fatalf("genuine session not readable: %v, %v", session, err)
			}
			// A previously enumerated reference must not follow a replaced UUID directory.
			replaced, destination := dir, target
			if component == "group" {
				replaced, destination = filepath.Dir(dir), filepath.Dir(target)
			}
			if err := os.RemoveAll(replaced); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(destination, replaced); err != nil {
				t.Skipf("directory symlinks unavailable: %v", err)
			}
			if session, err := p.GetAgentChatSessionByPath(transcript, project, false); err == nil || session != nil {
				t.Fatalf("path lookup imported linked session: %v, %v", session, err)
			}
			if session, err := parseSessionDir(dir, true); err == nil || session != nil {
				t.Fatalf("metadata parser imported linked session: %v, %v", session, err)
			}
		})
	}
}

func TestGetSessionAcceptsUppercaseUUID(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	id := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	seedSession(t, home, project, "session-basic", id)
	for _, lookup := range []string{id, strings.ToUpper(id)} {
		session, err := NewProvider().GetAgentChatSession(project, lookup, false)
		if err != nil || session == nil || session.SessionID != id {
			t.Fatalf("lookup %q: %v, %v", lookup, session, err)
		}
	}
}

func TestMalformedSummaryRetainedInDebugSnapshot(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	dir := seedSession(t, home, project, "session-basic", id)
	native := `{"info":{"id":"partial`
	if err := os.WriteFile(filepath.Join(dir, summaryFile), []byte(native), 0600); err != nil {
		t.Fatal(err)
	}
	session, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Export the parsing snapshot even if the agent has since repaired its file.
	if err := os.WriteFile(filepath.Join(dir, summaryFile), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	debug := testutil.IsolateDebugDir(t)
	if err := writeDebugRawFiles(session); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(debug, id, "native-sidecars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sidecars map[string]any
	if err := json.Unmarshal(raw, &sidecars); err != nil {
		t.Fatal(err)
	}
	if sidecars[summaryFile] != native {
		t.Fatalf("malformed native summary lost: %v", sidecars[summaryFile])
	}
}

func TestUnindexedPromptSkipsSyntheticIndexedUpdates(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	transcript := `{"type":"user","prompt_index":0,"content":[{"type":"text","text":"<user_query>first</user_query>"}]}` + "\n" + `{"type":"user","content":[{"type":"text","text":"<user_query>second</user_query>"}]}` + "\n"
	updates := `{"timestamp":1700000000,"params":{"update":{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":0}}}}` + "\n" + `{"timestamp":1700000001,"params":{"update":{"sessionUpdate":"user_message_chunk","_meta":{"promptIndex":1}}}}` + "\n" + `{"timestamp":1700000002,"params":{"update":{"sessionUpdate":"user_message_chunk"}}}` + "\n"
	for name, content := range map[string]string{chatHistoryFile: transcript, updatesFile: updates} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	session, err := ParseSessionDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := GenerateAgentSession(session, project)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Exchanges) != 2 {
		t.Fatalf("exchanges: %v", data.Exchanges)
	}
	if got, want := data.Exchanges[1].StartTime, isoFromMillis(0, 1700000002); got != want {
		t.Fatalf("unindexed real prompt time %q, want %q", got, want)
	}
}

func TestMalformedSummaryTimesUseTranscriptTime(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	path := filepath.Join(dir, summaryFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	summary["created_at"] = "not-a-timestamp"
	summary["updated_at"] = "2026-99-99T99:99:99Z"
	raw, err = json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, chatHistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	want := info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, metadata := range []bool{false, true} {
		session, err := parseSessionDir(dir, metadata)
		if err != nil {
			t.Fatal(err)
		}
		if session.CreatedAt != want || session.UpdatedAt != want {
			t.Fatalf("metadata=%v times %q/%q, want %q", metadata, session.CreatedAt, session.UpdatedAt, want)
		}
		if string(session.RawSummary) != string(raw) {
			t.Fatal("native malformed timestamps lost from raw summary")
		}
		data, err := GenerateAgentSession(session, project)
		if err != nil {
			t.Fatal(err)
		}
		if !data.Validate() {
			t.Fatal("invalid normalized session after timestamp fallback")
		}
	}
}
