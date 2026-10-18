package opencode

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// loadTwoProjects loads sessions recorded in two different directories.
func loadTwoProjects(t *testing.T) (toolsProject, otherProject string) {
	t.Helper()
	toolsProject = newProjectDir(t, "oc-tools")
	otherProject = newProjectDir(t, "oc-1")
	loadFixtures(t, map[string]string{fixtureToolsDir: toolsProject, fixtureOC1Dir: otherProject},
		"tool-exercise.jsonl", "model-error.jsonl")
	return toolsProject, otherProject
}

func TestSessionsAreScopedToTheirProject(t *testing.T) {
	toolsProject, otherProject := loadTwoProjects(t)
	p := NewProvider()

	var progressCalls [][2]int
	sessions, err := p.GetAgentChatSessions(toolsProject, false, func(current, total int) {
		progressCalls = append(progressCalls, [2]int{current, total})
	})
	if err != nil {
		t.Fatal(err)
	}
	// The subagent's child session is recorded in the same directory but is
	// part of its parent's conversation.
	if len(sessions) != 1 || sessions[0].SessionID != toolSessionID {
		t.Fatalf("sessions = %v, want only %s", sessionIDs(sessions), toolSessionID)
	}
	if !slices.Equal(progressCalls, [][2]int{{1, 1}}) {
		t.Errorf("progress calls = %v", progressCalls)
	}

	for _, tt := range []struct {
		name, project, sessionID string
		found                    bool
	}{
		{"own session", toolsProject, toolSessionID, true},
		{"another project's session", otherProject, toolSessionID, false},
		{"subagent child session", toolsProject, subagentSessionID, false},
		{"unknown id", toolsProject, "ses_missing", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			session, err := p.GetAgentChatSession(tt.project, tt.sessionID, false)
			if err != nil {
				t.Fatal(err)
			}
			if (session != nil) != tt.found {
				t.Errorf("GetAgentChatSession() found = %v, want %v", session != nil, tt.found)
			}
		})
	}

	if !p.DetectAgent(toolsProject, false) {
		t.Error("DetectAgent() = false for a project with sessions")
	}
	if p.DetectAgent(newProjectDir(t, "empty"), false) {
		t.Error("DetectAgent() = true for a project without sessions")
	}
}

func sessionIDs(sessions []spi.AgentChatSession) []string {
	var ids []string
	for _, session := range sessions {
		ids = append(ids, session.SessionID)
	}
	return ids
}

func TestListingMatchesConversion(t *testing.T) {
	toolsProject, _ := loadTwoProjects(t)
	p := NewProvider()

	listed, err := p.ListAgentChatSessions(toolsProject)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := p.GetAgentChatSessions(toolsProject, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || len(sessions) != 1 {
		t.Fatalf("listed %d, converted %d; want 1 each", len(listed), len(sessions))
	}
	// A listing must name the session the way its markdown file is named.
	if listed[0].Slug != sessions[0].Slug || listed[0].CreatedAt != sessions[0].CreatedAt {
		t.Errorf("listing %+v disagrees with conversion (slug %q, created %q)", listed[0], sessions[0].Slug, sessions[0].CreatedAt)
	}
	// OpenCode's own generated title names the session.
	if listed[0].Name != "Tool exercise" {
		t.Errorf("Name = %q, want OpenCode's title", listed[0].Name)
	}
}

func TestListAllAgentChatSessions(t *testing.T) {
	toolsProject, otherProject := loadTwoProjects(t)
	dbPath, err := getDatabasePath()
	if err != nil {
		t.Fatal(err)
	}

	var reporter spi.ScanReporter
	refs, err := NewProvider().ListAllAgentChatSessionsProgress(&reporter)
	if err != nil {
		t.Fatal(err)
	}
	origins := map[string]string{}
	for _, ref := range refs {
		origins[ref.SessionID] = ref.OriginCwd
		if ref.NativePath != dbPath {
			t.Errorf("NativePath = %q, want %q", ref.NativePath, dbPath)
		}
	}
	want := map[string]string{toolSessionID: toolsProject, modelErrSessionID: otherProject}
	if len(origins) != len(want) {
		t.Errorf("refs = %v, want %v", origins, want)
	}
	for id, origin := range want {
		if origins[id] != origin {
			t.Errorf("OriginCwd[%s] = %q, want %q", id, origins[id], origin)
		}
	}
	if reporter.Found() != int64(len(want)) {
		t.Errorf("reporter found %d, want %d", reporter.Found(), len(want))
	}
}

// TestListAllFingerprintsFollowTheirOwnSession covers the shared database: a write to
// one session changes that session's fingerprint and no other's, even though the one
// file every ref names has changed.
func TestListAllFingerprintsFollowTheirOwnSession(t *testing.T) {
	db := createFixtureDB(t, useFixtureStore(t))
	project := newProjectDir(t, "project")
	for _, id := range []string{"ses_a", "ses_b"} {
		insertSession(t, db, id, project, 1000, 1000)
		insertMessage(t, db, id, id+"_u", recordUser, 1, 1000, userData(1000, "prompt"))
	}
	p := NewProvider()

	fingerprints := func() map[string]spi.SessionFingerprint {
		t.Helper()
		refs, err := p.ListAllAgentChatSessions()
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]spi.SessionFingerprint{}
		for _, ref := range refs {
			if ref.Fingerprint == nil {
				t.Fatalf("ref %s has no fingerprint; the shared database cannot stand in for one", ref.SessionID)
			}
			got[ref.SessionID] = *ref.Fingerprint
		}
		return got
	}
	before := fingerprints()

	insertMessage(t, db, "ses_a", "ses_a_r", recordAssistant, 2, 5000, assistantData(5000, "reply"))
	after := fingerprints()

	if after["ses_a"] == before["ses_a"] {
		t.Errorf("ses_a fingerprint %+v did not change after a new message", after["ses_a"])
	}
	if after["ses_b"] != before["ses_b"] {
		t.Errorf("ses_b fingerprint changed from %+v to %+v without a write to it", before["ses_b"], after["ses_b"])
	}
}

// TestProjectReachedThroughOtherSpellings resolves the project through a
// symlink, a path with a space and an underscore, and a differently-cased
// spelling; OpenCode records the directory's real path.
func TestProjectReachedThroughOtherSpellings(t *testing.T) {
	real := newProjectDir(t, "My Project_dir")
	loadFixtures(t, map[string]string{fixtureToolsDir: real}, "tool-exercise.jsonl")
	p := NewProvider()

	spellings := map[string]string{"real path with space and underscore": real}

	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
	} else {
		spellings["symlink"] = link
	}

	upper := filepath.Join(filepath.Dir(real), strings.ToUpper(filepath.Base(real)))
	if info, err := os.Stat(upper); err == nil && info.IsDir() {
		spellings["different case"] = upper
	} else {
		t.Log("case-sensitive filesystem: skipping the differently-cased spelling")
	}

	for name, spelling := range spellings {
		t.Run(name, func(t *testing.T) {
			sessions, err := p.GetAgentChatSessions(spelling, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 {
				t.Errorf("sessions via %q = %d, want 1", spelling, len(sessions))
			}
		})
	}
}

func TestCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	for _, tt := range []struct {
		name, script, version string
		success               bool
	}{
		{"version on stdout", `echo "opencode v2.0.14"`, "opencode v2.0.14", true},
		{"version on stderr", `echo "opencode v2.0.14" >&2`, "opencode v2.0.14", true},
		{"empty output", "exit 0", "unknown", true},
		{"failure", "echo boom >&2; exit 3", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "open code")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tt.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			command := `"` + path + `" --flag`
			result := NewProvider().Check(command)
			if result.Success != tt.success || result.Version != tt.version {
				t.Fatalf("Check() = %+v", result)
			}
			if tt.success && result.ErrorType != "" {
				t.Errorf("ErrorType on success = %q", result.ErrorType)
			}
			if !tt.success {
				if result.ErrorType == "" || !strings.Contains(result.ErrorMessage, command+" --version") || !strings.Contains(result.ErrorMessage, "boom") {
					t.Errorf("failure lost its type, command or stderr: %+v", result)
				}
			}
		})
	}

	missing := NewProvider().Check(filepath.Join(t.TempDir(), "no-such-opencode"))
	if missing.Success || missing.ErrorType != spi.CheckErrorNotFound || !strings.Contains(missing.ErrorMessage, "no-such-opencode") {
		t.Errorf("missing custom binary = %+v", missing)
	}
}

// TestAttachmentOnlyPromptNamesTheSession covers a prompt with no text, only
// an attached file (possible from the TUI, e.g. a pasted image): the session
// must still be synced, detected, and listed under the slug it syncs with.
func TestAttachmentOnlyPromptNamesTheSession(t *testing.T) {
	db := createFixtureDB(t, useFixtureStore(t))
	project := newProjectDir(t, "project")
	insertSession(t, db, "ses_attach", project, 1000, 3000)
	// Shape per OpenCode 2.0.14's Prompt.FileAttachment schema.
	insertMessage(t, db, "ses_attach", "msg_1", recordUser, 1, 1000,
		`{"time":{"created":1000},"text":"","files":[{"name":"screenshot.png","mime":"image/png","source":{"type":"file","path":"screenshot.png"},"data":"iVBORw0KGgo="}],"agents":[]}`)
	insertMessage(t, db, "ses_attach", "msg_2", recordAssistant, 2, 2000, assistantData(2000, "That is a screenshot."))
	p := NewProvider()

	sessions, err := p.GetAgentChatSessions(project, false, nil)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("GetAgentChatSessions() = %d sessions, %v; want 1", len(sessions), err)
	}
	if !strings.Contains(messageText(allMessages(sessions[0].SessionData)[0]), "Attached file: `screenshot.png` (image/png)") {
		t.Errorf("attachment not rendered: %+v", allMessages(sessions[0].SessionData)[0])
	}
	listed, err := p.ListAgentChatSessions(project)
	if err != nil || len(listed) != 1 || listed[0].Slug != sessions[0].Slug {
		t.Errorf("listing = %+v, %v; want slug %q", listed, err, sessions[0].Slug)
	}
	if !p.DetectAgent(project, false) {
		t.Error("DetectAgent() = false for an attachment-only session")
	}
	// Enumeration without a progress reporter is safe: ScanReporter is nil-safe.
	if refs, err := p.ListAllAgentChatSessions(); err != nil || len(refs) != 1 {
		t.Errorf("ListAllAgentChatSessions() = %d, %v; want 1", len(refs), err)
	}
}

// TestShellOnlySessionIsNamedByItsCommand covers a session whose only user
// activity is a `!` shell command: it is synced, detected and listed, named
// by the command in both listing and conversion.
func TestShellOnlySessionIsNamedByItsCommand(t *testing.T) {
	db := createFixtureDB(t, useFixtureStore(t))
	project := newProjectDir(t, "project")
	insertSession(t, db, "ses_shell", project, 1000, 2000)
	// Shape as captured in testdata/tui-session.jsonl.
	insertMessage(t, db, "ses_shell", "msg_1", recordShell, 1, 1000,
		`{"time":{"created":1000,"completed":1100},"shellID":"sh_1","command":"make build-release","status":"exited","exit":0,"output":{"output":"ok\n","cursor":3,"size":3,"truncated":false}}`)
	p := NewProvider()

	sessions, err := p.GetAgentChatSessions(project, false, nil)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("GetAgentChatSessions() = %d sessions, %v; want 1", len(sessions), err)
	}
	listed, err := p.ListAgentChatSessions(project)
	if err != nil || len(listed) != 1 || listed[0].Slug != sessions[0].Slug {
		t.Errorf("listing = %+v, %v; want slug %q", listed, err, sessions[0].Slug)
	}
	if !strings.Contains(sessions[0].Slug, "make") {
		t.Errorf("slug %q is not named by the command", sessions[0].Slug)
	}
	if !p.DetectAgent(project, false) {
		t.Error("DetectAgent() = false for a shell-only session")
	}
}
