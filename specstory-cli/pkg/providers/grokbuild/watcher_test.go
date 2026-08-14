package grokbuild

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
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
