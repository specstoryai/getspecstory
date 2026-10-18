package grokbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// makeSessionDir creates a session directory with a transcript whose mtime is
// set, so the watcher's recency ordering can be tested.
func makeSessionDir(t *testing.T, groupDir, id string, modTime time.Time) string {
	t.Helper()
	dir := filepath.Join(groupDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, chatHistoryFile)
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(transcript, modTime, modTime); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDesiredWatchDirs_BoundsTheWatchSet(t *testing.T) {
	groupDir := t.TempDir()
	base := time.Now()

	// More sessions than the watcher is willing to hold open at once.
	var dirs []string
	for i := 0; i < maxWatchedSessions+5; i++ {
		id := uuidForIndex(i)
		// Later indexes are more recent.
		dirs = append(dirs, makeSessionDir(t, groupDir, id, base.Add(time.Duration(i)*time.Minute)))
	}

	desired := desiredWatchDirs(groupDir)

	// Watching every session in a project's history would pin a file descriptor
	// per file, which is what exhausted the descriptor table for Codex.
	if len(desired) != maxWatchedSessions {
		t.Fatalf("watch set = %d, want %d", len(desired), maxWatchedSessions)
	}

	// The most recent must be watched and the oldest must not.
	newest := dirs[len(dirs)-1]
	oldest := dirs[0]
	if !desired[newest] {
		t.Error("the most recently modified session should be watched")
	}
	if desired[oldest] {
		t.Error("the oldest session should have aged out of the watch set")
	}
}

func TestDesiredWatchDirs_IgnoresNonSessionEntries(t *testing.T) {
	groupDir := t.TempDir()
	makeSessionDir(t, groupDir, "11111111-2222-7333-8444-555555555555", time.Now())

	// Grok keeps non-session entries beside the session directories.
	if err := os.MkdirAll(filepath.Join(groupDir, subagentsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupDir, "prompt_history.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	desired := desiredWatchDirs(groupDir)

	if len(desired) != 1 {
		t.Errorf("watch set = %v, want only the session directory", desired)
	}
}

// captureWatcherPublishes points the watcher's callback at a slice and returns
// it, restoring the previous callback when the test ends.
func captureWatcherPublishes(t *testing.T, workspaceRoot string) *[]string {
	t.Helper()

	var published []string
	SetWatcherCallback(func(session *spi.AgentChatSession) {
		published = append(published, session.SessionID)
	})
	SetWatcherWorkspaceRoot(workspaceRoot)
	t.Cleanup(func() {
		SetWatcherCallback(nil)
		SetWatcherWorkspaceRoot("")
	})
	return &published
}

func TestProcessSessionChange_PublishesARealSession(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "11111111-2222-7333-8444-555555555555")
	copyFixture(t, "session-basic", sessionDir)

	published := captureWatcherPublishes(t, "/Users/dev/project")
	processSessionChange(sessionDir)

	if len(*published) != 1 {
		t.Fatalf("published %d sessions, want 1", len(*published))
	}
}

func TestProcessSessionChange_SkipsSubagents(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "99999999-8888-7777-6666-555555555555")
	copyFixture(t, "session-subagent", sessionDir)

	published := captureWatcherPublishes(t, "/Users/dev/project")
	processSessionChange(sessionDir)

	if len(*published) != 0 {
		t.Errorf("a spawned subagent must not be published as its own session, got %v", *published)
	}
}

func TestProcessSessionChange_WaitsForMetadata(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "11111111-2222-7333-8444-555555555555")
	copyFixture(t, "session-basic", sessionDir)

	// A session directory exists for several seconds before summary.json lands,
	// and session_kind is the only thing marking a subagent. Publishing before
	// the metadata arrives is how a subagent would leak out as a real session.
	if err := os.Remove(filepath.Join(sessionDir, summaryFile)); err != nil {
		t.Fatal(err)
	}

	published := captureWatcherPublishes(t, "/Users/dev/project")
	processSessionChange(sessionDir)

	if len(*published) != 0 {
		t.Errorf("a session with no metadata yet must not be published, got %v", *published)
	}
}

func TestProcessSessionChange_IgnoresEmptyTranscript(t *testing.T) {
	testutil.IsolateDebugDir(t)
	SetWatcherDebugRaw(true)
	t.Cleanup(func() { SetWatcherDebugRaw(false) })
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "dddddddd-eeee-7fff-8000-111111111111")
	copyFixture(t, "session-noquery", sessionDir)

	published := captureWatcherPublishes(t, "/Users/dev/project")
	processSessionChange(sessionDir)

	// The transcript holds no conversation, and an empty markdown file is worse
	// than none.
	if len(*published) != 0 {
		t.Errorf("a session with no conversation must not be published, got %v", *published)
	}
	// Accepted context records still need native debug output when no Markdown
	// is emitted, including on the live conversion path.
	for _, name := range []string{"1.json", "2.json", "native-sidecars.json"} {
		if _, err := os.Stat(filepath.Join(spi.GetDebugDir(filepath.Base(sessionDir)), name)); err != nil {
			t.Errorf("non-rendered live record missing from debug export: %s (%v)", name, err)
		}
	}
}

func TestSessionDirFor(t *testing.T) {
	// Built with filepath.Join so the separators are native: sessionDirFor
	// compares filepath.Dir results, and a hardcoded forward-slash prefix would
	// never match them on Windows.
	groupDir := filepath.Join("store", "sessions", "%2Ftmp%2Fproject")
	sessionID := "11111111-2222-7333-8444-555555555555"
	sessionDir := filepath.Join(groupDir, sessionID)

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "transcript inside a session", path: filepath.Join(sessionDir, chatHistoryFile), want: sessionDir},
		{name: "summary inside a session", path: filepath.Join(sessionDir, summaryFile), want: sessionDir},
		{name: "the session directory itself", path: sessionDir, want: sessionDir},
		{name: "a file beside the sessions", path: filepath.Join(groupDir, "prompt_history.jsonl"), want: ""},
		{name: "a file nested deeper", path: filepath.Join(sessionDir, "terminal", "call-1.log"), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionDirFor(groupDir, tt.path); got != tt.want {
				t.Errorf("sessionDirFor(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsTranscriptChange(t *testing.T) {
	tests := []struct {
		name string
		file string
		op   fsnotify.Op
		want bool
	}{
		{name: "transcript write", file: chatHistoryFile, op: fsnotify.Write, want: true},
		{name: "updates write", file: updatesFile, op: fsnotify.Write, want: true},
		{name: "summary rename", file: summaryFile, op: fsnotify.Rename, want: true},
		{name: "events write", file: eventsFile, op: fsnotify.Write, want: true},
		// These churn constantly during a turn and carry nothing renderable.
		{name: "lock file", file: "chat_history.jsonl.lock", op: fsnotify.Write, want: false},
		{name: "rewind points", file: "rewind_points.jsonl", op: fsnotify.Write, want: false},
		{name: "signals", file: "signals.json", op: fsnotify.Write, want: false},
		{name: "transcript chmod", file: chatHistoryFile, op: fsnotify.Chmod, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := fsnotify.Event{Name: filepath.Join("/store/session", tt.file), Op: tt.op}
			if got := isTranscriptChange(event); got != tt.want {
				t.Errorf("isTranscriptChange(%s, %v) = %v, want %v", tt.file, tt.op, got, tt.want)
			}
		})
	}
}

// uuidForIndex builds a distinct UUID-shaped directory name for an index.
func uuidForIndex(i int) string {
	const hex = "0123456789abcdef"
	return "0000000" + string(hex[i%16]) + "-1111-7222-8333-44444444444" + string(hex[(i/16)%16])
}

// A panic in the consumer's callback must not escape delivery: it would unwind
// the fsnotify event goroutine and take the process down over one bad session.
func TestTriggerCallback_ContainsConsumerPanic(t *testing.T) {
	var called bool
	SetWatcherCallback(func(*spi.AgentChatSession) {
		called = true
		panic("consumer blew up")
	})
	t.Cleanup(func() { SetWatcherCallback(nil) })

	triggerCallback(&spi.AgentChatSession{SessionID: "s-1"})

	if !called {
		t.Fatal("callback was never invoked")
	}
}

// Delivery is skipped rather than panicking when either side is missing.
func TestTriggerCallback_NilSafe(t *testing.T) {
	SetWatcherCallback(nil)
	triggerCallback(&spi.AgentChatSession{SessionID: "s-1"})

	var called bool
	SetWatcherCallback(func(*spi.AgentChatSession) { called = true })
	t.Cleanup(func() { SetWatcherCallback(nil) })

	triggerCallback(nil)

	if called {
		t.Error("callback invoked for a nil session")
	}
}

func TestWatcherRestartAndShutdownDrain(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	dir := seedSession(t, home, project, "session-basic", id)
	t.Cleanup(StopWatcher)
	for cycle := range 2 {
		delivered := make(chan *spi.AgentChatSession, 10)
		if err := WatchGrokProject(project, func(s *spi.AgentChatSession) { delivered <- s }); err != nil {
			t.Fatal(err)
		}
		// Stopping an unchanged watcher must not manufacture session activity.
		StopWatcher()
		if len(delivered) != 0 {
			t.Fatal("startup or idle shutdown emitted an unchanged session")
		}
		if err := WatchGrokProject(project, func(s *spi.AgentChatSession) { delivered <- s }); err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(dir, chatHistoryFile), os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		text := fmt.Sprintf("final reply %d", cycle)
		_, err = fmt.Fprintf(file, "{\"type\":\"assistant\",\"content\":%q}\n", text)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		// No sleep: this specifically exercises cancellation before fsnotify delivery.
		StopWatcher()
		if len(delivered) == 0 {
			t.Fatal("shutdown lost the final native write")
		}
		var latest *spi.AgentChatSession
		for len(delivered) > 0 {
			latest = <-delivered
		}
		if !strings.Contains(latest.RawData, text) {
			t.Fatal("callback did not finish with the final reply")
		}
	}
}

func TestWatcherDebugRefreshesTranscriptAndSidecars(t *testing.T) {
	testutil.IsolateDebugDir(t)
	home := withFakeGrokHome(t)
	project := t.TempDir()
	id := "11111111-2222-7333-8444-555555555555"
	dir := seedSession(t, home, project, "session-basic", id)
	first := `{"type":"user","content":[{"type":"text","text":"<user_query>live debug</user_query>"}]}`
	second := `{"type":"assistant","content":"current reply"}`
	body := first + "\n" + second + "\n"
	if err := os.WriteFile(filepath.Join(dir, chatHistoryFile), []byte(body+`{"type":"assistant","content":"obsolete reply"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetWatcherDebugRaw(true)
	SetWatcherWorkspaceRoot(project)
	t.Cleanup(func() {
		StopWatcher()
		SetWatcherDebugRaw(false)
		SetWatcherWorkspaceRoot("")
	})
	delivered := make(chan *spi.AgentChatSession, 20)
	start := func() {
		t.Helper()
		if err := WatchGrokProject(project, func(s *spi.AgentChatSession) { delivered <- s }); err != nil {
			t.Fatal(err)
		}
	}
	start()
	// A sidecar-only filesystem event must export the full parsing snapshot.
	if err := os.WriteFile(filepath.Join(dir, eventsFile), []byte(`{"type":"future_event","state":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("sidecar change did not trigger a live debug export")
	}
	StopWatcher()
	debugDir := spi.GetDebugDir(id)
	data, err := os.ReadFile(filepath.Join(debugDir, "native-sidecars.json"))
	if err != nil || !strings.Contains(string(data), `"state": "old"`) {
		t.Fatalf("live sidecar snapshot missing: %s (%v)", data, err)
	}
	testutil.AssertDebugRefresh(t, debugDir, []string{"3.json"}, []string{"session-data.json", "03.json", "+3.json"}, func() {
		start()
		if err := os.WriteFile(filepath.Join(dir, chatHistoryFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, eventsFile), []byte(`{"type":"future_event","state":"new"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, updatesFile)); err != nil {
			t.Fatal(err)
		}
		// Drain pending writes before inspecting output, including removed sidecars.
		StopWatcher()
	})
	data, err = os.ReadFile(filepath.Join(debugDir, "2.json"))
	if err != nil || !strings.Contains(string(data), "current reply") {
		t.Fatalf("live transcript snapshot stale: %s (%v)", data, err)
	}
	data, err = os.ReadFile(filepath.Join(debugDir, "native-sidecars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sidecars map[string]json.RawMessage
	if err := json.Unmarshal(data, &sidecars); err != nil {
		t.Fatal(err)
	}
	if string(sidecars[updatesFile]) != "null" || !strings.Contains(string(sidecars[eventsFile]), `"state": "new"`) || strings.Contains(string(sidecars[eventsFile]), `"state": "old"`) {
		t.Fatalf("live sidecar refresh stale: %s", data)
	}
}

func TestWatcherAdoptsLateStoreWithOldFiles(t *testing.T) {
	home := withFakeGrokHome(t)
	// Multiple missing ancestors must not disable startup.
	home = filepath.Join(home, "new", "nested", "grok")
	t.Setenv("GROK_HOME", home)
	project := t.TempDir()
	delivered := make(chan *spi.AgentChatSession, 10)
	if err := WatchGrokProject(project, func(s *spi.AgentChatSession) { delivered <- s }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(StopWatcher)
	dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	old := time.Now().Add(-24 * time.Hour)
	for _, name := range []string{chatHistoryFile, summaryFile, updatesFile, eventsFile} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case session := <-delivered:
		if !strings.Contains(session.RawData, "read the README") {
			t.Fatal("late session content missing")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late store was not adopted through filesystem events")
	}
	StopWatcher()
}

func TestWatcherRejectsInvalidStore(t *testing.T) {
	home := withFakeGrokHome(t)
	if err := os.WriteFile(filepath.Join(home, "sessions"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WatchGrokProject(t.TempDir(), nil); err == nil {
		StopWatcher()
		t.Fatal("invalid store was silently accepted")
	}
}

func TestWatcherReconcilesStartupAndDormantSession(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
	watcher, err := spi.NewFSWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close() }()
	state := &grokWatchState{watcher: watcher, projectPath: project, sessionsDir: filepath.Join(home, "sessions"), watched: map[string]bool{}, signatures: map[string]sessionSignature{}, pending: map[string]bool{}}
	if err := state.refresh(true); err != nil {
		t.Fatal(err)
	}
	if len(state.pending) != 0 {
		t.Fatal("pre-existing session counted as activity")
	}
	// Model an edit between the baseline and completion of watch registration.
	if err := os.WriteFile(filepath.Join(dir, eventsFile), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.refresh(false); err != nil {
		t.Fatal(err)
	}
	if !state.pending[dir] {
		t.Fatal("initialization race lost the sidecar change")
	}
	delete(state.pending, dir)
	// Periodic reconciliation must also find a resumed old session outside the
	// bounded watch set, even when no directory entry is created.
	_ = watcher.Remove(dir)
	delete(state.watched, dir)
	if err := os.WriteFile(filepath.Join(dir, updatesFile), []byte("{}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.refresh(false); err != nil {
		t.Fatal(err)
	}
	if !state.pending[dir] {
		t.Fatal("unwatched sidecar change was lost")
	}
}

func TestWatcherSignatureIncludesSubagentMetadata(t *testing.T) {
	dir := t.TempDir()
	before := signatureFor(dir)
	metaDir := filepath.Join(dir, subagentsDir, "child")
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(metaDir, "meta.json")
	if err := os.WriteFile(path, []byte(`{"status":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	running := signatureFor(dir)
	if running == before {
		t.Fatal("new subagent metadata did not change signature")
	}
	if err := os.WriteFile(path, []byte(`{"status":"completed","duration_ms":1000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if signatureFor(dir) == running {
		t.Fatal("subagent completion did not change signature")
	}
	// Logs and child transcripts do not belong to the parent's renderable data.
	complete := signatureFor(dir)
	if err := os.WriteFile(filepath.Join(metaDir, chatHistoryFile), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if signatureFor(dir) != complete {
		t.Fatal("child transcript changed parent signature")
	}
}

func TestWatchSlotsExcludeTopLevelSubagents(t *testing.T) {
	group := t.TempDir()
	base := time.Now()
	parent := makeSessionDir(t, group, uuidForIndex(0), base)
	for i := 1; i <= maxWatchedSessions+1; i++ {
		child := makeSessionDir(t, group, uuidForIndex(i), base.Add(time.Duration(i)*time.Second))
		if err := os.WriteFile(filepath.Join(child, summaryFile), []byte(`{"session_kind":"subagent"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := desiredWatchDirs(group)
	if len(got) != 1 || !got[parent] {
		t.Fatalf("child sessions displaced parent watch: %v", got)
	}
}

func TestWatcherErrorExitDrainsFinalWrites(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending=%v", pending), func(t *testing.T) {
			home := withFakeGrokHome(t)
			project := t.TempDir()
			dir := seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")
			watcher, err := spi.NewFSWatcher()
			if err != nil {
				t.Fatal(err)
			}
			state := &grokWatchState{watcher: watcher, projectPath: project, sessionsDir: filepath.Join(home, "sessions"), watched: map[string]bool{}, signatures: map[string]sessionSignature{}, pending: map[string]bool{}}
			if err := state.refresh(true); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, chatHistoryFile)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString("\n" + `{"type":"assistant","content":"FINAL-ERROR-DRAIN"}` + "\n")
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				t.Fatalf("append: %v, %v", err, closeErr)
			}
			if pending {
				state.pending[dir] = true
			}
			var delivered *spi.AgentChatSession
			SetWatcherCallback(func(s *spi.AgentChatSession) { delivered = s })
			SetWatcherWorkspaceRoot(project)
			t.Cleanup(func() { SetWatcherCallback(nil); SetWatcherWorkspaceRoot("") })
			if err := watcher.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := state.run(ctx); err == nil || !strings.Contains(err.Error(), "stream closed") {
				t.Fatalf("missing watcher failure: %v", err)
			}
			if delivered == nil || !strings.Contains(delivered.RawData, "FINAL-ERROR-DRAIN") {
				t.Fatal("error exit lost final native write")
			}
			if len(state.pending) != 0 {
				t.Fatal("pending callbacks were not drained")
			}
		})
	}
}
