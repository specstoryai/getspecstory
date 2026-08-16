package musecode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestDesiredWatchDirs(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()
	now := time.Now()

	today := now.Format("2006/01/02")
	stale := now.AddDate(0, 0, -30).Format("2006/01/02")

	todayPath := writeSession(t, sessionsRoot, today, basicSessionID, "session-basic.jsonl", project)
	stalePath := writeSession(t, sessionsRoot, stale, toolsSessionID, "session-tools.jsonl", project)

	// A subagent transcript nested under today's session.
	subagentDir := filepath.Join(filepath.Dir(todayPath), subagentDirName, "child-id")
	writeFileFrom(t, todayPath, filepath.Join(subagentDir, sessionFileName))

	desired := desiredWatchDirs(sessionsRoot, now)

	mustWatch := []string{
		sessionsRoot,
		filepath.Join(sessionsRoot, fmt.Sprintf("%04d", now.Year())),
		filepath.Join(sessionsRoot, filepath.FromSlash(today)),
		filepath.Dir(todayPath),
	}
	for _, dir := range mustWatch {
		if !desired[dir] {
			t.Errorf("directory not watched: %s", dir)
		}
	}

	// The fd-exhaustion lesson: history outside the trailing window is scanned
	// on demand but never watched.
	if desired[filepath.Dir(stalePath)] {
		t.Errorf("stale session directory is watched: %s", filepath.Dir(stalePath))
	}
	if desired[filepath.Join(sessionsRoot, filepath.FromSlash(stale))] {
		t.Errorf("stale day directory is watched: %s", stale)
	}
	if desired[filepath.Join(filepath.Dir(todayPath), subagentDirName)] {
		t.Error("subagent directory is watched")
	}
}

func TestDesiredWatchDirs_EmptyStore(t *testing.T) {
	sessionsRoot := seedStore(t)

	desired := desiredWatchDirs(sessionsRoot, time.Now())

	// Nothing exists yet, so only the root is asked for; the caller skips even
	// that until the root itself appears.
	if len(desired) != 1 || !desired[sessionsRoot] {
		t.Errorf("desired = %v, want just the sessions root", desired)
	}
}

func TestProcessSessionChange_OnlyPublishesThisProject(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()
	other := t.TempDir()

	minePath := writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)
	theirsPath := writeSession(t, sessionsRoot, "2026/08/07", toolsSessionID, "session-tools.jsonl", other)

	var published []*spi.AgentChatSession
	SetWatcherCallback(func(session *spi.AgentChatSession) {
		published = append(published, session)
	})
	SetWatcherWorkspaceRoot(project)
	t.Cleanup(func() {
		SetWatcherCallback(nil)
		SetWatcherWorkspaceRoot("")
	})

	// The store is global, so a transcript for another project can land in the
	// same day directory the watcher is holding.
	processSessionChange(theirsPath)
	if len(published) != 0 {
		t.Fatalf("published %d sessions for another project, want 0", len(published))
	}

	processSessionChange(minePath)
	if len(published) != 1 {
		t.Fatalf("published %d sessions, want 1", len(published))
	}
	if published[0].SessionID != basicSessionID {
		t.Errorf("published session = %q, want %q", published[0].SessionID, basicSessionID)
	}
	if published[0].SessionData.WorkspaceRoot != project {
		t.Errorf("workspace root = %q, want %q", published[0].SessionData.WorkspaceRoot, project)
	}
}

func TestProcessSessionChange_IgnoresEmptyTranscript(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()

	// Metadata only: the session exists but has no conversation yet.
	path := writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	metadataLine := strings.SplitN(string(full), "\n", 2)[0] + "\n"
	if err := os.WriteFile(path, []byte(metadataLine), 0o644); err != nil {
		t.Fatal(err)
	}

	var published int
	SetWatcherCallback(func(*spi.AgentChatSession) { published++ })
	SetWatcherWorkspaceRoot(project)
	t.Cleanup(func() {
		SetWatcherCallback(nil)
		SetWatcherWorkspaceRoot("")
	})

	processSessionChange(path)

	if published != 0 {
		t.Errorf("published %d sessions for a conversation-less transcript, want 0", published)
	}
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

// The watcher must be restartable: each start owns its own context, so a stop
// must only end the current run, never poison a later start (a context created
// once at package load would leave every watcher after the first stillborn).
func TestWatcherRestart(t *testing.T) {
	seedStore(t)
	project := t.TempDir()
	t.Cleanup(func() {
		SetWatcherCallback(nil)
		SetWatcherWorkspaceRoot("")
	})

	StopWatcher() // no watcher running: must be a safe no-op

	for i := range 2 {
		if err := WatchMuseProject(project, func(*spi.AgentChatSession) {}); err != nil {
			t.Fatalf("start %d failed: %v", i+1, err)
		}
		watcherMutex.RLock()
		running := watcherCancel != nil
		watcherMutex.RUnlock()
		if !running {
			t.Fatalf("start %d did not register a running watcher", i+1)
		}
		StopWatcher() // must end this run and leave the next start usable
	}
}

// Muse creates a day directory, a session directory and the transcript in
// immediate succession, so by the time the watcher adds a watch on the new day
// directory the whole session can already be on disk. fsnotify reports nothing
// that happened before the watch existed, so the directory must be walked on
// arrival or the first session of every day is lost until the next refresh.
func TestAdoptExisting_PublishesASessionThatLandedBeforeTheWatch(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()

	writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)
	// A subagent transcript sits one level deeper and is never a session.
	subagentDir := filepath.Join(sessionsRoot, "2026", "08", "07", basicSessionID, subagentDirName, "child-id")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatalf("failed to create subagent dir: %v", err)
	}
	writeFileFrom(t,
		filepath.Join(sessionsRoot, "2026", "08", "07", basicSessionID, sessionFileName),
		filepath.Join(subagentDir, sessionFileName))

	var published []string
	SetWatcherCallback(func(s *spi.AgentChatSession) { published = append(published, s.SessionID) })
	SetWatcherWorkspaceRoot(project)
	t.Cleanup(func() {
		SetWatcherCallback(nil)
		SetWatcherWorkspaceRoot("")
	})

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	t.Cleanup(func() { _ = watcher.Close() })

	set := &watchSet{watcher: watcher, sessionsRoot: sessionsRoot, watched: map[string]bool{}}
	set.adoptExisting(filepath.Join(sessionsRoot, "2026", "08", "07"))

	if len(published) != 1 || published[0] != basicSessionID {
		t.Errorf("adopting a pre-populated day directory published %v, want exactly [%s]", published, basicSessionID)
	}
}

// Adoption walks a new directory to find sessions, but must stop at the session
// directory: fsnotify's kqueue backend holds an open file descriptor per file in
// every watched directory, so watching a session's own spill directories buys
// nothing and costs fds that the trailing watch window exists to conserve.
func TestAdoptExisting_StopsAtTheSessionDirectory(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()

	transcript := writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)
	sessionDir := filepath.Dir(transcript)

	// The two directories Muse creates inside a session directory.
	toolOutputsDir := filepath.Join(sessionDir, "tool-outputs")
	if err := os.MkdirAll(toolOutputsDir, 0o755); err != nil {
		t.Fatalf("failed to create tool-outputs dir: %v", err)
	}
	subagentChildDir := filepath.Join(sessionDir, subagentDirName, "child-id")
	writeFileFrom(t, transcript, filepath.Join(subagentChildDir, sessionFileName))

	SetWatcherCallback(func(*spi.AgentChatSession) {})
	SetWatcherWorkspaceRoot(project)
	t.Cleanup(func() {
		SetWatcherCallback(nil)
		SetWatcherWorkspaceRoot("")
	})

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}
	t.Cleanup(func() { _ = watcher.Close() })

	set := &watchSet{watcher: watcher, sessionsRoot: sessionsRoot, watched: map[string]bool{}}
	set.adoptExisting(filepath.Join(sessionsRoot, "2026", "08", "07"))

	// The session directory itself is where the transcript lives, so it is the
	// deepest directory that ever earns a watch.
	if !set.watched[sessionDir] {
		t.Errorf("session directory not watched: %s", sessionDir)
	}
	for _, dir := range []string{
		toolOutputsDir,
		filepath.Join(sessionDir, subagentDirName),
		subagentChildDir,
	} {
		if set.watched[dir] {
			t.Errorf("directory inside a session is watched: %s", dir)
		}
	}
}

func TestWithinStoreLayout(t *testing.T) {
	sessionsRoot := filepath.Join(t.TempDir(), "muse", "sessions")
	set := &watchSet{sessionsRoot: sessionsRoot}

	tests := []struct {
		name     string
		relative string
		expected bool
	}{
		{"year directory", "2026", true},
		{"month directory", "2026/08", true},
		{"day directory", "2026/08/07", true},
		{"session directory", "2026/08/07/" + basicSessionID, true},
		{"spill directory inside a session", "2026/08/07/" + basicSessionID + "/tool-outputs", false},
		{"subagent directory inside a session", "2026/08/07/" + basicSessionID + "/" + subagentDirName, false},
		{"subagent session directory", "2026/08/07/" + basicSessionID + "/" + subagentDirName + "/child-id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(sessionsRoot, filepath.FromSlash(tt.relative))
			if got := set.withinStoreLayout(path); got != tt.expected {
				t.Errorf("withinStoreLayout(%q) = %v, want %v", tt.relative, got, tt.expected)
			}
		})
	}

	// The sessions root is not a directory the walk descends into, and neither is
	// anything outside it.
	if set.withinStoreLayout(sessionsRoot) {
		t.Error("the sessions root itself reported as a descendable store directory")
	}
	if set.withinStoreLayout(filepath.Join(sessionsRoot, "..", "elsewhere")) {
		t.Error("a directory outside the sessions root reported as inside the store layout")
	}
}
