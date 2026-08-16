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
