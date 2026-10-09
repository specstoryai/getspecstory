package copilotcli

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestProviderSessionsAreScopedToProject(t *testing.T) {
	// Glob syntax in the home path must not hide sessions from listing.
	home := filepath.Join(t.TempDir(), "Jane [Work]")
	t.Setenv(homeEnvVar, home)
	project := newProjectDir(t, "project")
	other := newProjectDir(t, "other")

	writeSession(t, home, "in-project",
		sessionStart(t, "in-project", project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "fix the login bug"}),
		record(t, eventAssistantMsg, "a1", map[string]any{"content": "Fixed."}),
	)
	writeSession(t, home, "other-project",
		sessionStart(t, "other-project", other),
		record(t, eventUserMessage, "u1", map[string]any{"content": "unrelated"}),
	)
	writeSession(t, home, "no-prompt-yet", sessionStart(t, "no-prompt-yet", project))
	// Created and driven by another session: its only input is a session
	// message, and it must still be exported, named after that task.
	writeSession(t, home, "orchestrated",
		sessionStart(t, "orchestrated", project),
		record(t, eventUserMessage, "x1", map[string]any{
			"content":            "Run the e2e suite",
			"transformedContent": "<cross_session_message>\nfrom_session_id: in-project\nRun the e2e suite\n</cross_session_message>",
		}),
		record(t, eventAssistantMsg, "a1", map[string]any{"content": "All green."}),
	)
	// Not a session transcript: a stray JSONL a session keeps in its workspace.
	if err := os.MkdirAll(filepath.Join(home, sessionStateDirName, "in-project", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, sessionStateDirName, "in-project", "files", "data.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := NewProvider()

	sessions, err := provider.GetAgentChatSessions(project, false, nil)
	if err != nil {
		t.Fatalf("GetAgentChatSessions: %v", err)
	}
	slugs := map[string]string{}
	for _, session := range sessions {
		if !session.SessionData.Validate() {
			t.Errorf("SessionData for %s failed schema validation", session.SessionID)
		}
		if session.SessionData.Provider.ID != providerID || session.SessionData.Provider.Version != "1.0.95-2" {
			t.Errorf("unexpected provider for %s: %+v", session.SessionID, session.SessionData.Provider)
		}
		slugs[session.SessionID] = session.Slug
	}
	wantSlugs := map[string]string{"in-project": "fix-the-login-bug", "orchestrated": "run-the-e2e-suite"}
	if !maps.Equal(slugs, wantSlugs) {
		t.Fatalf("GetAgentChatSessions slugs = %v, want %v", slugs, wantSlugs)
	}

	metadata, err := provider.ListAgentChatSessions(project)
	listed := map[string]string{}
	for _, m := range metadata {
		listed[m.SessionID] = m.Slug
	}
	if err != nil || !maps.Equal(listed, wantSlugs) {
		t.Errorf("ListAgentChatSessions = %v, %v; want %v", listed, err, wantSlugs)
	}
	if !provider.DetectAgent(project, false) {
		t.Errorf("DetectAgent(project) = false, want true")
	}

	if got, _ := provider.GetAgentChatSession(project, "other-project", false); got != nil {
		t.Errorf("GetAgentChatSession returned another project's session")
	}
	if got, _ := provider.GetAgentChatSession(project, "../escape", false); got != nil {
		t.Errorf("GetAgentChatSession accepted a path-traversal session ID")
	}

	refs, err := provider.ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions: %v", err)
	}
	var ids []string
	for _, ref := range refs {
		ids = append(ids, ref.SessionID)
	}
	slices.Sort(ids)
	if want := []string{"in-project", "orchestrated", "other-project"}; !slices.Equal(ids, want) {
		t.Errorf("ListAllAgentChatSessions IDs = %v, want %v", ids, want)
	}
}

func TestResumeArgs(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sessionID string
		want      []string
	}{
		{"no session leaves args alone", []string{"--model", "x"}, "", []string{"--model", "x"}},
		{"appends attached flag", []string{"--allow-all-tools"}, "abc", []string{"--allow-all-tools", "--resume=abc"}},
		{"overrides configured attached flag", []string{"--resume=old"}, "abc", []string{"--resume=abc"}},
		{"overrides configured short flag", []string{"-r", "old"}, "abc", []string{"-r", "abc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resumeArgs(tt.args, tt.sessionID); !slices.Equal(got, tt.want) {
				t.Errorf("resumeArgs(%v, %q) = %v, want %v", tt.args, tt.sessionID, got, tt.want)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{"release banner with update hint", "GitHub Copilot CLI 1.0.95-2.\nRun 'copilot update' to check for updates.\n", "1.0.95-2"},
		{"bare version", "1.2.3", "1.2.3"},
		{"empty output", "  \n", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseVersion(tt.output); got != tt.want {
				t.Errorf("parseVersion(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

// TestProjectReachedThroughOtherSpellings resolves the project through a
// symlink, a path with a space and an underscore, and a differently-cased
// spelling; Copilot records the directory as the operating system reports it.
func TestProjectReachedThroughOtherSpellings(t *testing.T) {
	home := useSessionStore(t)
	real := newProjectDir(t, "My Project_dir")
	writeSession(t, home, "s1",
		sessionStart(t, "s1", real),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
	)

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

	p := NewProvider()
	for name, spelling := range spellings {
		t.Run(name, func(t *testing.T) {
			sessions, err := p.GetAgentChatSessions(spelling, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			listed, err := p.ListAgentChatSessions(spelling)
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || len(listed) != 1 {
				t.Errorf("via %q: %d sessions, %d listed; want 1 each", spelling, len(sessions), len(listed))
			}
			if got, _ := p.GetAgentChatSession(spelling, "s1", false); got == nil {
				t.Errorf("GetAgentChatSession via %q found nothing", spelling)
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
		{"release banner", `echo "GitHub Copilot CLI 1.0.95-2."; echo "Run 'copilot update' to check for updates."`, "1.0.95-2", true},
		{"empty output", "exit 0", "unknown", true},
		{"failure", "echo boom >&2; exit 3", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "co pilot")
			if err := os.WriteFile(path, []byte("#!/bin/sh\n"+tt.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			result := NewProvider().Check(`"` + path + `" --flag`)
			if result.Success != tt.success || result.Version != tt.version {
				t.Fatalf("Check() = %+v", result)
			}
			if !tt.success && !strings.Contains(result.ErrorMessage, path+" --flag --version") {
				t.Errorf("failure message %q does not name the command run, arguments included", result.ErrorMessage)
			}
		})
	}
}
