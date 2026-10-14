package cursorcloud

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

const testOrigin = "git@github.com:specstoryai/cloud-agent-test.git"

// makeProject creates a git checkout at dir whose origin is origin.
func makeProject(t *testing.T, dir, origin string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "[remote \"origin\"]\n\turl = " + origin + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheck(t *testing.T) {
	t.Run("no key is not found with guidance", func(t *testing.T) {
		useSettings(t, "")
		res := NewProvider().Check("")
		if res.Success || res.ErrorType != spi.CheckErrorNotFound || !strings.Contains(res.ErrorMessage, apiKeyPage) {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("accepted key reports the key name", func(t *testing.T) {
		useSettings(t, "test-key")
		newFakeAPI(t)
		res := NewProvider().Check("")
		if !res.Success || res.Version != "" || !strings.Contains(res.Location, `"Cloude Agent API key for SpecStory"`) || res.ErrorType != "" {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("config key used when env is unset", func(t *testing.T) {
		useSettings(t, "")
		SetAPIKey("test-key")
		newFakeAPI(t)
		if res := NewProvider().Check(""); !res.Success {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("rejected key is permission denied", func(t *testing.T) {
		useSettings(t, "test-key")
		f := newFakeAPI(t)
		f.meStatus = http.StatusUnauthorized
		res := NewProvider().Check("")
		if res.Success || res.ErrorType != spi.CheckErrorPermissionDenied {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("bad custom opener names the path", func(t *testing.T) {
		useSettings(t, "test-key")
		bad := filepath.Join(t.TempDir(), "no-such-opener")
		res := NewProvider().Check(bad)
		// The message names the opener as the command splitter parsed it, which
		// drops Windows separators (backslash is its escape character), so the
		// assertion checks the basename.
		if res.Success || res.ErrorType != spi.CheckErrorNotFound || !strings.Contains(res.ErrorMessage, "no-such-opener") {
			t.Errorf("result = %+v", res)
		}
	})
}

func TestResolveAPIKeyEnvironmentWins(t *testing.T) {
	useSettings(t, "from-env")
	SetAPIKey("from-config")
	if key, source := resolveAPIKey(); key != "from-env" || source != apiKeyEnvVar {
		t.Errorf("got %q from %q", key, source)
	}
	t.Setenv(apiKeyEnvVar, "")
	if key, source := resolveAPIKey(); key != "from-config" || source != "cursor_api_key" {
		t.Errorf("got %q from %q", key, source)
	}
}

func TestAgentMatchesOrigin(t *testing.T) {
	agent := apiAgent{Repos: []apiRepo{{URL: "https://github.com/specstoryai/cloud-agent-test"}}}
	tests := []struct {
		origin string
		want   bool
	}{
		{"https://github.com/specstoryai/cloud-agent-test.git", true},
		{"git@github.com:specstoryai/cloud-agent-test.git", true},
		{"ssh://git@github.com/specstoryai/cloud-agent-test", false}, // user@ in an ssh:// URL keeps the user
		{"git@github.com:specstoryai/other-repo.git", false},
		{"", false},
	}
	for _, tt := range tests {
		origin := ""
		if tt.origin != "" {
			origin = spi.NormalizeGitRemoteURL(tt.origin)
		}
		if got := agentMatchesOrigin(agent, origin); got != tt.want {
			t.Errorf("origin %q: match = %v, want %v", tt.origin, got, tt.want)
		}
	}
}

// syncProject runs GetAgentChatSessions against the fake API.
func syncProject(t *testing.T, project string, debugRaw bool) []spi.AgentChatSession {
	t.Helper()
	calls := 0
	sessions, err := NewProvider().GetAgentChatSessions(project, debugRaw, func(current, total int) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if calls != len(sessions) {
		t.Errorf("progress called %d times for %d sessions", calls, len(sessions))
	}
	return sessions
}

func TestGetAgentChatSessionsSyncsAndMirrors(t *testing.T) {
	useSettings(t, "test-key")
	f := newFakeAPI(t)
	f.addFixture(loadFixture(t, "fixture-run"))
	f.addFixture(loadFixture(t, "failure-run"))
	// An agent on another repository must not reach this project.
	other := loadFixture(t, "tool-enumeration")
	var otherAgent map[string]any
	_ = json.Unmarshal(other.agent.raw, &otherAgent)
	otherAgent["repos"] = []any{map[string]any{"url": "https://github.com/specstoryai/elsewhere"}}
	other.agent.raw, _ = json.Marshal(otherAgent)
	f.addFixture(other)

	project := makeProject(t, t.TempDir(), testOrigin)
	sessions := syncProject(t, project, false)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want the 2 agents for this repository", len(sessions))
	}
	for _, s := range sessions {
		if s.RawData == "" || s.SessionData == nil || s.SessionData.WorkspaceRoot != project {
			t.Errorf("session %s incomplete", s.SessionID)
		}
	}
	mirrorRoot := filepath.Join(project, ".specstory", "cursorcloud")
	if data, err := os.ReadFile(filepath.Join(mirrorRoot, ".gitignore")); err != nil || !strings.Contains(string(data), "*") {
		t.Errorf("project-local mirror has no .gitignore: %v", err)
	}
	streamsBefore := f.count("/v1/agents/bc-a2a25972-6529-47c0-9691-8f135d0a6527/runs/")
	runsBefore := f.count("/v1/agents/bc-a2a25972-6529-47c0-9691-8f135d0a6527/runs")

	// A second sync with no agent activity re-lists but fetches nothing else
	// and changes no mirror bytes.
	before := snapshotTree(t, mirrorRoot)
	again := syncProject(t, project, false)
	if len(again) != 2 {
		t.Fatalf("second sync got %d sessions", len(again))
	}
	if f.count("/v1/agents/bc-a2a25972-6529-47c0-9691-8f135d0a6527/runs") != runsBefore || f.count("/v1/agents/bc-a2a25972-6529-47c0-9691-8f135d0a6527/runs/") != streamsBefore {
		t.Error("an unchanged agent was re-fetched")
	}
	if after := snapshotTree(t, mirrorRoot); after != before {
		t.Error("a sync with no activity changed the mirror")
	}

	// Once updatedAt advances the agent is re-fetched, but its whole streams are not.
	fx := loadFixture(t, "fixture-run")
	var agent map[string]any
	_ = json.Unmarshal(fx.agent.raw, &agent)
	agent["updatedAt"] = "2026-10-13T15:09:00.000Z"
	raw, _ := json.Marshal(agent)
	f.setAgent(raw)
	syncProject(t, project, false)
	if f.count("/v1/agents/bc-a2a25972-6529-47c0-9691-8f135d0a6527/runs") != runsBefore+1 {
		t.Error("an advanced updatedAt did not refresh the runs")
	}
	if f.count("/v1/agents/bc-a2a25972-6529-47c0-9691-8f135d0a6527/runs/") != streamsBefore {
		t.Error("a whole stream capture was fetched again")
	}
}

// snapshotTree fingerprints every file's bytes and mtime under root.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, _ := d.Info()
		data, _ := os.ReadFile(path)
		b.WriteString(path + "|" + info.ModTime().String() + "|" + string(data) + "\n")
		return nil
	})
	return b.String()
}

func TestPartialCaptureReplacedWhileRetainedKeptAfterExpiry(t *testing.T) {
	fx := loadFixture(t, "fixture-run")
	runID := fx.runs[0].run.ID
	agentID := fx.agent.agent.ID
	full := parseSSEBytes(t, fx.streams[runID])

	for _, tt := range []struct {
		name      string
		expired   bool
		wantWhole bool
	}{
		{"retained stream replaces the partial capture", false, true},
		{"expired stream keeps the partial capture", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useSettings(t, "test-key")
			f := newFakeAPI(t)
			f.addFixture(fx)
			f.expired[runID] = tt.expired
			m := mirror{root: t.TempDir()}
			writeFixtureMirror(t, m, fx)
			if err := m.writeEvents(agentID, runID, full[:50]); err != nil {
				t.Fatal(err)
			}
			// The run ended an hour before "now", inside the 24h retention.
			ended, _ := time.Parse(time.RFC3339, fx.runs[0].run.UpdatedAt)
			if err := m.refreshAgent(t.Context(), newAPIClient("test-key"), fx.agent, refreshOptions{now: ended.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			events, _ := readEventsFile(m.eventsFile(agentID, runID))
			if eventsAreWhole(events) != tt.wantWhole {
				t.Errorf("whole = %v, want %v (%d events)", eventsAreWhole(events), tt.wantWhole, len(events))
			}
		})
	}

	t.Run("a stream past retention is not requested", func(t *testing.T) {
		useSettings(t, "test-key")
		f := newFakeAPI(t)
		f.addFixture(fx)
		m := mirror{root: t.TempDir()}
		writeFixtureMirror(t, m, fx)
		if err := m.writeEvents(agentID, runID, full[:50]); err != nil {
			t.Fatal(err)
		}
		ended, _ := time.Parse(time.RFC3339, fx.runs[0].run.UpdatedAt)
		if err := m.refreshAgent(t.Context(), newAPIClient("test-key"), fx.agent, refreshOptions{now: ended.Add(25 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if n := f.count("/v1/agents/" + agentID + "/runs/" + runID + "/stream"); n != 0 {
			t.Errorf("requested an expired stream %d times", n)
		}
	})
}

func TestGetAgentChatSessionChecksMembership(t *testing.T) {
	useSettings(t, "test-key")
	f := newFakeAPI(t)
	id := f.addFixture(loadFixture(t, "fixture-run"))

	member := makeProject(t, t.TempDir(), testOrigin)
	s, err := NewProvider().GetAgentChatSession(member, id, false)
	if err != nil || s == nil || s.SessionID != id {
		t.Fatalf("member project: session %v err %v", s, err)
	}

	stranger := makeProject(t, t.TempDir(), "git@github.com:specstoryai/another.git")
	if s, err := NewProvider().GetAgentChatSession(stranger, id, false); err != nil || s != nil {
		t.Errorf("another repository's project got %v, %v; want nil, nil", s, err)
	}
	if s, err := NewProvider().GetAgentChatSession(member, "bc-00000000-0000-0000-0000-000000000000", false); err != nil || s != nil {
		t.Errorf("unknown id got %v, %v; want nil, nil", s, err)
	}
}

func TestListAgentChatSessionsUsesOnlyTheMirror(t *testing.T) {
	useSettings(t, "test-key")
	f := newFakeAPI(t)
	project := makeProject(t, t.TempDir(), testOrigin)
	m, _ := mirrorFor(project)
	writeFixtureMirror(t, m, loadFixture(t, "failure-run"))

	list, err := NewProvider().ListAgentChatSessions(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Slug != "verification-task-for-a" || list[0].Name != "specstory-failure-capture" {
		t.Errorf("list = %+v", list)
	}
	if n := f.count("/"); n != 0 {
		t.Errorf("listing made %d API requests", n)
	}
}

func TestListAllAndByPath(t *testing.T) {
	useSettings(t, "")
	shared := t.TempDir()
	SetMirrorDir(shared)
	m := mirror{root: shared}
	writeFixtureMirror(t, m, loadFixture(t, "fixture-run"))
	writeFixtureMirror(t, m, loadFixture(t, "failure-run"))

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d refs", len(refs))
	}
	for _, ref := range refs {
		if ref.OriginCwd != "" || filepath.Base(ref.NativePath) != "agent.json" || ref.Fingerprint == nil || ref.Fingerprint.Size == 0 {
			t.Errorf("ref = %+v", ref)
		}
		s, err := NewProvider().GetAgentChatSessionByPath(ref.NativePath, "/elsewhere", false)
		if err != nil || s == nil || s.SessionID != ref.SessionID || s.Slug != ref.Slug {
			t.Errorf("by path %s: %v %v", ref.NativePath, s, err)
		}
	}

	// A new run's events land outside agent.json; the fingerprint must move.
	before := *refs[0].Fingerprint
	dir := filepath.Dir(refs[0].NativePath)
	if err := os.WriteFile(filepath.Join(dir, "runs", "run-00000000-0000-0000-0000-000000000000.events.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := agentDirFingerprint(dir); after.Size == before.Size {
		t.Error("fingerprint did not change when a run file was added")
	}
}

func TestMirrorLocationPrecedence(t *testing.T) {
	useSettings(t, "")
	project := t.TempDir()
	if m, local := mirrorFor(project); !local || m.root != filepath.Join(project, ".specstory", "cursorcloud") {
		t.Errorf("default mirror = %s local %v", m.root, local)
	}
	configDir := t.TempDir()
	SetMirrorDir(configDir)
	if m, local := mirrorFor(project); local || m.root != configDir {
		t.Errorf("config mirror = %s local %v", m.root, local)
	}
	flagDir := t.TempDir()
	SetUserDataDirOverride(flagDir)
	if m, _ := mirrorFor(project); m.root != flagDir {
		t.Errorf("--user-data-dir mirror = %s, want it to win over config", m.root)
	}

	home, _ := os.UserHomeDir()
	SetMirrorDir("~/.specstory/cursorcloud")
	if configMirrorDir != filepath.Join(home, ".specstory", "cursorcloud") {
		t.Errorf("tilde not expanded: %s", configMirrorDir)
	}
}

func TestProjectPathShapes(t *testing.T) {
	useSettings(t, "")
	base := t.TempDir()
	tests := []struct {
		name string
		path func(real string) string
		skip func() string
	}{
		{"space and underscore in the path", func(real string) string { return real }, nil},
		{"reached through a symlink", func(real string) string {
			link := filepath.Join(base, "link")
			if err := os.Symlink(real, link); err != nil {
				return ""
			}
			return link
		}, nil},
		{"differently cased spelling", func(real string) string {
			return filepath.Join(filepath.Dir(real), strings.ToUpper(filepath.Base(real)))
		}, func() string {
			if runtime.GOOS == "linux" {
				return "case-sensitive filesystem"
			}
			return ""
		}},
	}
	real := makeProject(t, filepath.Join(base, "my project_dir"), testOrigin)
	m, _ := mirrorFor(real)
	writeFixtureMirror(t, m, loadFixture(t, "fixture-run"))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != nil {
				if reason := tt.skip(); reason != "" {
					t.Skip(reason)
				}
			}
			path := tt.path(real)
			if path == "" {
				t.Skip("symlinks unavailable")
			}
			if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
				t.Skip("path does not resolve on this filesystem")
			}
			list, err := NewProvider().ListAgentChatSessions(path)
			if err != nil || len(list) != 1 {
				t.Errorf("found %d sessions (err %v), want the same one as the canonical path", len(list), err)
			}
		})
	}
}

func TestReconstructionUnsupportedConsistently(t *testing.T) {
	p := NewProvider()
	if p.SupportsReconstruction() {
		t.Error("SupportsReconstruction = true")
	}
	if _, err := p.ReconstructSession(&schema.SessionData{}, spi.ReconstructOptions{}); !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("ReconstructSession err = %v", err)
	}
	if _, err := p.NativeSessionPath(t.TempDir(), "x"); !errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("NativeSessionPath err = %v", err)
	}
}

func TestDebugOutput(t *testing.T) {
	debugBase := t.TempDir()
	spi.SetDebugBaseDir(debugBase)
	t.Cleanup(func() { spi.SetDebugBaseDir("") })

	snap := snapshotFromFixture(t, "failure-run")
	dir := spi.GetDebugDir(snap.agent.ID)
	if !strings.HasPrefix(dir, debugBase) {
		t.Fatalf("debug dir %s ignores --debug-dir", dir)
	}

	t.Run("flag off writes nothing", func(t *testing.T) {
		if convertSnapshot(snap, testWorkspace, false) == nil {
			t.Fatal("no session")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("debug dir exists without the flag: %v", err)
		}
	})

	// Seed a CLI-owned file and a stale run file from a longer earlier export.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "run-3-run-00000000-0000-0000-0000-000000000000.json")
	cliOwned := filepath.Join(dir, "session-data.json")
	for _, path := range []string{stale, cliOwned} {
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if convertSnapshot(snap, testWorkspace, true) == nil {
		t.Fatal("no session")
	}
	for _, name := range []string{"agent.json", "conversation.json", "usage.json",
		"run-1-run-ec41805a-07c4-4c80-874f-f077b9877582.json", "run-2-run-4fd81a06-3d70-4d12-89e6-44ef450485d1.json", "session-data.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale run-3 debug file survived the refresh")
	}
	agentJSON, _ := os.ReadFile(filepath.Join(dir, "agent.json"))
	// workOnCurrentBranch is not read by the provider; the native export keeps it.
	if !strings.Contains(string(agentJSON), `"workOnCurrentBranch": false`) {
		t.Errorf("unknown field missing or not pretty-printed:\n%s", agentJSON)
	}
	run1, _ := os.ReadFile(filepath.Join(dir, "run-1-run-ec41805a-07c4-4c80-874f-f077b9877582.json"))
	if !strings.Contains(string(run1), `"event": "heartbeat"`) {
		t.Error("run debug file lacks its events")
	}
}

func TestListAllCarriesRepositoryNotDirectory(t *testing.T) {
	useSettings(t, "")
	project := makeProject(t, t.TempDir(), testOrigin)
	m, _ := mirrorFor(project)
	writeFixtureMirror(t, m, loadFixture(t, "fixture-run"))
	t.Chdir(project)

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs from the project-local mirror", len(refs))
	}
	if refs[0].OriginCwd != "" || refs[0].OriginRepoURL != "https://github.com/specstoryai/cloud-agent-test" {
		t.Errorf("ref = OriginCwd %q OriginRepoURL %q; want no directory and the agent's repository", refs[0].OriginCwd, refs[0].OriginRepoURL)
	}
}

// TestEnsureRootRepairsGitignore checks that an existing .gitignore without the
// "*" rule is rewritten, since trusting its presence would let git track the
// unredacted mirror, while one that already excludes everything is kept as is.
func TestEnsureRootRepairsGitignore(t *testing.T) {
	for _, tt := range []struct {
		name     string
		existing string
		wantKept bool
	}{
		{"missing file is written", "", false},
		{"empty file is rewritten", "# nothing ignored\n", false},
		{"file with the rule is left alone", "*\n# keep my note\n", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := mirror{root: filepath.Join(t.TempDir(), "mirror")}
			path := filepath.Join(m.root, ".gitignore")
			if tt.existing != "" {
				if err := os.MkdirAll(m.root, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.ensureRoot(true); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantKept && string(got) != tt.existing {
				t.Errorf("rewrote a sufficient .gitignore: %q", got)
			}
			if !gitignoreExcludesAll(got) {
				t.Errorf(".gitignore = %q, want the * rule", got)
			}
		})
	}
}

// TestRefreshAgentLeavesMarkerStaleOnTransientFailure checks that a 5xx from
// the conversation endpoint fails the refresh before agent.json is written, so
// the next sync retries, while a permanent 404 mirrors the agent without it.
func TestRefreshAgentLeavesMarkerStaleOnTransientFailure(t *testing.T) {
	useSettings(t, "test-key")
	prev := initialBackoff
	initialBackoff = time.Millisecond
	t.Cleanup(func() { initialBackoff = prev })
	f := newFakeAPI(t)
	fx := loadFixture(t, "fixture-run")
	f.addFixture(fx)
	agentID := fx.agent.agent.ID
	m := mirror{root: t.TempDir()}
	now, _ := time.Parse(time.RFC3339, fx.runs[0].run.UpdatedAt)

	f.convStatus = http.StatusServiceUnavailable
	if err := m.refreshAgent(t.Context(), newAPIClient("test-key"), fx.agent, refreshOptions{now: now}); err == nil {
		t.Fatal("refresh succeeded despite a failing conversation fetch")
	}
	if m.readStoredAgent(agentID) != nil {
		t.Error("agent.json was written after a transient failure, so the next sync would not retry")
	}

	f.mu.Lock()
	f.convStatus = http.StatusNotFound
	f.mu.Unlock()
	if err := m.refreshAgent(t.Context(), newAPIClient("test-key"), fx.agent, refreshOptions{now: now}); err != nil {
		t.Fatalf("refresh failed on a permanent 404: %v", err)
	}
	if m.readStoredAgent(agentID) == nil {
		t.Error("agent.json missing after a permanent refusal; the agent would never be mirrored")
	}
	if _, err := os.Stat(filepath.Join(m.agentDir(agentID), "conversation.json")); err == nil {
		t.Error("conversation.json written although the endpoint refused")
	}
}
