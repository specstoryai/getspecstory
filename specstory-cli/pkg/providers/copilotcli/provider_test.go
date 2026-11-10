package copilotcli

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
		if session.SessionData.Provider.ID != providerID || session.SessionData.Provider.Version != "1.0.105" {
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
		{"inserts before the option separator", []string{"--model", "x", "--", "prompt"}, "abc", []string{"--model", "x", "--resume=abc", "--", "prompt"}},
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
		{"release banner with update hint", "GitHub Copilot CLI 1.0.105.\nRun 'copilot update' to check for updates.\n", "1.0.105"},
		{"prerelease banner", "GitHub Copilot CLI 1.0.106-9.\n", "1.0.106-9"},
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

// TestSessionRecordedInAnotherCaseOnWindows puts the case difference on the
// recorded side. A Windows process can hold a current directory spelled
// differently from the disk, and Copilot records what the process reports (Go
// and Node both read it from GetCurrentDirectory), so that spelling must still
// match the canonical project root. Other platforms report on-disk spelling.
func TestSessionRecordedInAnotherCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows keeps the caller's spelling of the current directory")
	}
	home := useSessionStore(t)
	real := newProjectDir(t, "MixedCase")
	t.Chdir(filepath.Join(filepath.Dir(real), strings.ToUpper(filepath.Base(real))))
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("project %q, process reports %q", real, cwd)
	writeSession(t, home, "s1",
		sessionStart(t, "s1", cwd),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
	)

	sessions, err := NewProvider().GetAgentChatSessions(real, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("session recorded in %q: found %d sessions for project %q, want 1", cwd, len(sessions), real)
	}
}

// A remote workspace's root reaches Windows without a drive and must match the
// Unix path Copilot recorded, not a path on the current drive.
func TestDrivelessRemoteRootMatchesRecordedPathOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows has rooted paths without a drive")
	}
	home := useSessionStore(t)
	writeSession(t, home, "s1",
		sessionStart(t, "s1", "/home/user/project"),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
	)

	sessions, err := NewProvider().GetAgentChatSessions(`\home\user\project`, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Errorf("found %d sessions, want the one recorded at /home/user/project", len(sessions))
	}
}

// A transcript copied into another session's directory is that directory's
// session: listings, reads and `copilot --resume` must all agree on its ID.
func TestSessionIdentityComesFromItsDirectory(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	path := writeSession(t, home, "directory-id",
		sessionStart(t, "header-id", project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
	)

	p := NewProvider()
	listed, err := p.ListAgentChatSessions(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].SessionID != "directory-id" {
		t.Fatalf("listed = %+v, want one session named directory-id", listed)
	}
	bulk, err := p.GetAgentChatSessions(project, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bulk) != 1 {
		t.Fatalf("bulk read found %d sessions, want 1", len(bulk))
	}
	byID, err := p.GetAgentChatSession(project, "directory-id", false)
	if err != nil || byID == nil {
		t.Fatalf("by-ID read = %v, %v", byID, err)
	}
	byPath, err := p.GetAgentChatSessionByPath(path, "", false)
	if err != nil || byPath == nil {
		t.Fatalf("by-path read = %v, %v", byPath, err)
	}
	for name, session := range map[string]*spi.AgentChatSession{"bulk": &bulk[0], "by ID": byID, "by path": byPath} {
		if session.SessionID != "directory-id" || session.SessionData.SessionID != "directory-id" {
			t.Errorf("%s read: SessionID = %q, SessionData.SessionID = %q, want directory-id",
				name, session.SessionID, session.SessionData.SessionID)
		}
	}
}

// A session that cannot be examined is not a session that does not exist.
func TestDirectLookupsReportFilesystemFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating the symlink loop needs privileges Windows runners may lack")
	}
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	path := writeSession(t, home, "looped",
		sessionStart(t, "looped", project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
	)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, path); err != nil {
		t.Fatal(err)
	}

	p := NewProvider()
	if session, err := p.GetAgentChatSession(project, "looped", false); err == nil {
		t.Errorf("GetAgentChatSession = %v, nil; want the stat error", session)
	}
	if session, err := p.GetAgentChatSessionByPath(path, "", false); err == nil {
		t.Errorf("GetAgentChatSessionByPath = %v, nil; want the stat error", session)
	}
	// Discovery skips what it cannot read, and absence stays absence.
	if sessions, err := p.GetAgentChatSessions(project, false, nil); err != nil || len(sessions) != 0 {
		t.Errorf("bulk read = %d sessions, %v; want 0, nil", len(sessions), err)
	}
	if session, err := p.GetAgentChatSession(project, "missing", false); session != nil || err != nil {
		t.Errorf("missing session = %v, %v; want nil, nil", session, err)
	}
	if session, err := p.GetAgentChatSessionByPath(path+"-gone", "", false); session != nil || err != nil {
		t.Errorf("missing path = %v, %v; want nil, nil", session, err)
	}
}

// A Unix root keeps exact matching: on a case-sensitive disk these are two
// projects, so folding case everywhere would merge their sessions.
func TestBelongsToProjectKeepsCaseDistinctUnixRoots(t *testing.T) {
	if belongsToProject(&sessionHeader{Cwd: "/work/Project"}, "/work/project") {
		t.Error("a session from /work/Project was claimed by /work/project")
	}
	if !belongsToProject(&sessionHeader{Cwd: "/work/project"}, "/work/project") {
		t.Error("a session from /work/project was not claimed by /work/project")
	}
}

// reindex persists NativePath and reads it back from any directory later, so a
// relative COPILOT_HOME must not reach it.
func TestRelativeHomeGivesAbsoluteTranscriptPaths(t *testing.T) {
	launchDir := t.TempDir()
	t.Chdir(launchDir)
	t.Setenv(homeEnvVar, "state")
	project := newProjectDir(t, "project")
	writeSession(t, filepath.Join(launchDir, "state"), "s1",
		sessionStart(t, "s1", project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "hello"}),
	)

	p := NewProvider()
	refs, err := p.ListAllAgentChatSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("listed %d sessions, want 1", len(refs))
	}
	if !filepath.IsAbs(refs[0].NativePath) {
		t.Fatalf("NativePath = %q, want an absolute path", refs[0].NativePath)
	}

	t.Chdir(t.TempDir())
	session, err := p.GetAgentChatSessionByPath(refs[0].NativePath, "", false)
	if err != nil || session == nil {
		t.Errorf("by-path read from another directory = %v, %v", session, err)
	}
}

// A flag after "--" is positional to Copilot, so a custom command holding one
// must not make a working install fail its check.
func TestCheckKeepsVersionFlagBeforeSeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	path := filepath.Join(t.TempDir(), "copilot")
	script := `#!/bin/sh
for arg in "$@"; do
  [ "$arg" = "--" ] && break
  if [ "$arg" = "--version" ]; then echo "GitHub Copilot CLI 1.0.105."; exit 0; fi
done
echo "error: unrecognized subcommand" >&2
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result := NewProvider().Check(`"` + path + `" --model x -- prompt`)
	if !result.Success || result.Version != "1.0.105" {
		t.Errorf("Check() = %+v, want success at 1.0.105", result)
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
		{"release banner", `echo "GitHub Copilot CLI 1.0.105."; echo "Run 'copilot update' to check for updates."`, "1.0.105", true},
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
