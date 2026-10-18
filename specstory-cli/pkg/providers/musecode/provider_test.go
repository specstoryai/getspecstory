package musecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestDebugRawPreservesAllNativeEnvelopesFromSnapshot(t *testing.T) {
	testutil.IsolateDebugDir(t)
	fixture, err := os.ReadFile(filepath.Join("testdata", "session-basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Foreign streams and new payload kinds do not render, but must remain
	// inspectable along with the fixture's metadata and diagnostic records.
	hidden := `{"id":"hidden","stream":{"kind":"task","id":"foreign"},"payload_type":"future","payload":{"unknown":true},"unknownNative":9007199254740993}`
	body := strings.ReplaceAll(strings.TrimSpace(string(fixture)), "\n", "\r\n") + "\r\n" + hidden
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{broken\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ParseSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	chat := convertToAgentChatSession(snapshot, project, false)
	if chat == nil || chat.RawData != body {
		t.Fatal("native snapshot lost accepted envelopes or retained malformed data")
	}
	dir := spi.GetDebugDir(snapshot.ID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("debug-disabled conversion created output: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	// Conversion must not read this newer, shorter transcript.
	if err := os.WriteFile(path, []byte(strings.Join(lines[:2], "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chat = convertToAgentChatSession(snapshot, project, true)
	if chat == nil || chat.RawData != body {
		t.Fatal("conversion reread a changed transcript")
	}
	for i, line := range lines {
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.json", i+1)))
		if err != nil || !bytes.Contains(data, []byte("\n  \"")) {
			t.Fatalf("record %d missing or not pretty printed: %v", i+1, err)
		}
		var want, got bytes.Buffer
		if err := json.Compact(&want, []byte(line)); err != nil {
			t.Fatal(err)
		}
		if err := json.Compact(&got, data); err != nil || want.String() != got.String() {
			t.Fatalf("record %d changed: %s (%v)", i+1, data, err)
		}
	}
	var stale []string
	for i := 3; i <= len(lines); i++ {
		stale = append(stale, fmt.Sprintf("%d.json", i))
	}
	testutil.AssertDebugRefresh(t, dir, stale, []string{"session-data.json", "3-notes.json"}, func() {
		if chat, err := NewProvider().GetAgentChatSessionByPath(path, project, true); err != nil || chat == nil {
			t.Fatalf("refresh failed: %v", err)
		}
	})
}

func TestProviderName(t *testing.T) {
	if got := NewProvider().Name(); got != "Muse Code" {
		t.Errorf("Name() = %q, want Muse Code", got)
	}
}

func TestDetectAgent(t *testing.T) {
	t.Run("finds a session recorded for this project", func(t *testing.T) {
		sessionsRoot := seedStore(t)
		project := t.TempDir()
		writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)

		if !NewProvider().DetectAgent(project, false) {
			t.Error("DetectAgent() = false, want true")
		}
	})

	t.Run("another project's sessions do not count", func(t *testing.T) {
		sessionsRoot := seedStore(t)
		writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", t.TempDir())

		if NewProvider().DetectAgent(t.TempDir(), false) {
			t.Error("DetectAgent() = true for a project with no sessions")
		}
	})

	t.Run("no store at all", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", t.TempDir())

		if NewProvider().DetectAgent(t.TempDir(), false) {
			t.Error("DetectAgent() = true with no Muse store")
		}
	})
}

func TestGetAgentChatSessions(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()
	writeSession(t, sessionsRoot, "2026/08/05", basicSessionID, "session-basic.jsonl", project)
	writeSession(t, sessionsRoot, "2026/08/07", multiturnSessionID, "session-multiturn.jsonl", project)
	writeSession(t, sessionsRoot, "2026/08/06", toolsSessionID, "session-tools.jsonl", t.TempDir())

	var progressCalls int
	sessions, err := NewProvider().GetAgentChatSessions(project, false, func(current, total int) {
		progressCalls++
		if total != 2 {
			t.Errorf("progress total = %d, want 2", total)
		}
	})
	if err != nil {
		t.Fatalf("GetAgentChatSessions failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2 (other projects excluded)", len(sessions))
	}
	if progressCalls != 2 {
		t.Errorf("progress callbacks = %d, want 2", progressCalls)
	}

	first := sessions[0]
	if first.SessionID != multiturnSessionID {
		t.Errorf("first session = %q, want the most recent (%q)", first.SessionID, multiturnSessionID)
	}
	if first.SessionData == nil || !first.SessionData.Validate() {
		t.Error("session data missing or invalid")
	}
	if first.SessionData.WorkspaceRoot != project {
		t.Errorf("workspace root = %q, want %q", first.SessionData.WorkspaceRoot, project)
	}
	if first.Slug == "" || first.Slug == "muse-session" {
		t.Errorf("slug = %q, want one derived from the first prompt", first.Slug)
	}
	if first.RawData == "" {
		t.Error("RawData is empty, want the original transcript")
	}
	if first.CreatedAt == "" {
		t.Error("CreatedAt is empty")
	}
}

func TestGetAgentChatSession(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()
	writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)

	provider := NewProvider()

	t.Run("resolves by id through the store layout", func(t *testing.T) {
		session, err := provider.GetAgentChatSession(project, basicSessionID, false)
		if err != nil {
			t.Fatalf("GetAgentChatSession failed: %v", err)
		}
		if session == nil {
			t.Fatal("session not found")
		}
		if session.SessionID != basicSessionID {
			t.Errorf("SessionID = %q, want %q", session.SessionID, basicSessionID)
		}
		if len(session.SessionData.Exchanges) != 1 {
			t.Errorf("exchange count = %d, want 1", len(session.SessionData.Exchanges))
		}
	})

	t.Run("unknown id returns nil without error", func(t *testing.T) {
		session, err := provider.GetAgentChatSession(project, "no-such-session", false)
		if err != nil {
			t.Fatalf("GetAgentChatSession failed: %v", err)
		}
		if session != nil {
			t.Errorf("session = %+v, want nil", session)
		}
	})
}

func TestGetAgentChatSessionByPath(t *testing.T) {
	project := t.TempDir()
	provider := NewProvider()

	session, err := provider.GetAgentChatSessionByPath(filepath.Join("testdata", "session-tools.jsonl"), project, false)
	if err != nil {
		t.Fatalf("GetAgentChatSessionByPath failed: %v", err)
	}
	if session == nil {
		t.Fatal("session is nil")
	}
	if session.SessionID != toolsSessionID {
		t.Errorf("SessionID = %q, want %q", session.SessionID, toolsSessionID)
	}
	if session.SessionData.WorkspaceRoot != project {
		t.Errorf("workspace root = %q, want the origin cwd %q", session.SessionData.WorkspaceRoot, project)
	}
}

func TestListAgentChatSessions(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()
	writeSession(t, sessionsRoot, "2026/08/07", multiturnSessionID, "session-multiturn.jsonl", project)
	writeSession(t, sessionsRoot, "2026/08/06", toolsSessionID, "session-tools.jsonl", t.TempDir())

	metadata, err := NewProvider().ListAgentChatSessions(project)
	if err != nil {
		t.Fatalf("ListAgentChatSessions failed: %v", err)
	}

	if len(metadata) != 1 {
		t.Fatalf("metadata count = %d, want 1", len(metadata))
	}
	entry := metadata[0]
	if entry.SessionID != multiturnSessionID {
		t.Errorf("SessionID = %q, want %q", entry.SessionID, multiturnSessionID)
	}
	if entry.CreatedAt == "" || entry.Slug == "" || entry.Name == "" {
		t.Errorf("incomplete metadata: %+v", entry)
	}
}

func TestListAllAgentChatSessions(t *testing.T) {
	sessionsRoot := seedStore(t)
	projectA := t.TempDir()
	projectB := t.TempDir()
	writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", projectA)
	parentPath := writeSession(t, sessionsRoot, "2026/08/07", subagentSessionID, "session-subagent-noise.jsonl", projectB)

	// A subagent transcript alongside its parent must not be enumerated.
	subagentDir := filepath.Join(filepath.Dir(parentPath), subagentDirName, "child-id")
	writeFileFrom(t, parentPath, filepath.Join(subagentDir, sessionFileName))

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions failed: %v", err)
	}

	if len(refs) != 2 {
		t.Fatalf("ref count = %d, want 2 (both projects, no subagent transcript)", len(refs))
	}

	byID := make(map[string]spi.GlobalSessionRef, len(refs))
	for _, ref := range refs {
		byID[ref.SessionID] = ref
	}
	// OriginCwd comes from inside the transcript: the store path says nothing
	// about which project a session belongs to.
	if got := byID[basicSessionID].OriginCwd; got != projectA {
		t.Errorf("basic session OriginCwd = %q, want %q", got, projectA)
	}
	if got := byID[subagentSessionID].OriginCwd; got != projectB {
		t.Errorf("subagent-noise session OriginCwd = %q, want %q", got, projectB)
	}
	if byID[basicSessionID].NativePath == "" {
		t.Error("NativePath is empty")
	}
}

func TestListAllAgentChatSessions_MissingStore(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions failed: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("ref count = %d, want 0", len(refs))
	}
}

func TestMuseSessionSlug(t *testing.T) {
	tests := []struct {
		name     string
		session  *MuseSession
		expected string
	}{
		{
			name:     "no prompt falls back",
			session:  &MuseSession{ID: "x"},
			expected: "muse-session",
		},
		{
			name: "prompt drives the slug",
			session: &MuseSession{ID: "x", Events: []MuseConversationEvent{
				{Event: museEvent{Kind: eventStarted, Prompt: "Add a sub function"}},
			}},
			expected: spi.GenerateFilenameFromUserMessage("Add a sub function"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := museSessionSlug(tt.session); got != tt.expected {
				t.Errorf("museSessionSlug() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// The Muse store is global and keyed by session id alone, so a by-id lookup can
// reach any project's transcript. Returning one would not merely answer the
// wrong question: conversion stamps the requesting project onto the result, so
// the session would be filed under a project that never ran it.
func TestGetAgentChatSession_RejectsAnotherProjectsSession(t *testing.T) {
	sessionsRoot := seedStore(t)
	projectA := t.TempDir()
	projectB := t.TempDir()

	writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", projectB)

	got, err := NewProvider().GetAgentChatSession(projectA, basicSessionID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("project A was handed project B's session %q", got.SessionID)
	}

	// The owning project must still resolve it, or the guard has simply broken
	// by-id lookup instead of scoping it.
	got, err = NewProvider().GetAgentChatSession(projectB, basicSessionID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("owning project could not resolve its own session")
	}
	if got.SessionID != basicSessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, basicSessionID)
	}
}
