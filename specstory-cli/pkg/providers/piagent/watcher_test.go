package piagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// --- test helpers -----------------------------------------------------------

func piHeaderLine(id, cwd string) string {
	return fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-09-03T10:00:00.000Z","cwd":%q}`, id, cwd)
}

// parentIDField renders the parentId JSON field: null for the first entry.
func parentIDField(parentID string) string {
	if parentID == "" {
		return `"parentId":null`
	}
	return fmt.Sprintf(`"parentId":%q`, parentID)
}

func piUserLine(entryID, parentID, text string) string {
	return fmt.Sprintf(`{"type":"message","id":%q,%s,"timestamp":"2026-09-03T10:00:01.000Z","message":{"role":"user","content":%q,"timestamp":1788450646425}}`,
		entryID, parentIDField(parentID), text)
}

func piAssistantLine(entryID, parentID, text string) string {
	return fmt.Sprintf(`{"type":"message","id":%q,%s,"timestamp":"2026-09-03T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":%q}],"provider":"openai","model":"gpt-5.5","api":"","stopReason":"stop"}}`,
		entryID, parentIDField(parentID), text)
}

func piSessionInfoLine(entryID, parentID, name string) string {
	return fmt.Sprintf(`{"type":"session_info","id":%q,%s,"timestamp":"2026-09-03T10:00:03.000Z","name":%q}`,
		entryID, parentIDField(parentID), name)
}

// validSession joins a header + user + assistant into a complete pi session.
func validSession(id, cwd, userText string) string {
	return piHeaderLine(id, cwd) + "\n" +
		piUserLine("m1", "", userText) + "\n" +
		piAssistantLine("m2", "m1", "hi there") + "\n"
}

// startWatch wires a buffered callback channel and starts the watcher, returning
// the channel and a stop func. Callers pre-create the target directory so the
// watcher adds a directory watch immediately (the bootstrap path is exercised
// separately by production code, not needed for these emit assertions).
func startWatch(t *testing.T, projectPath string) (<-chan *spi.AgentChatSession, func()) {
	t.Helper()
	ch := make(chan *spi.AgentChatSession, 16)
	SetWatcherCallback(func(s *spi.AgentChatSession) { ch <- s })
	if err := WatchForProjectDir(projectPath); err != nil {
		t.Fatalf("WatchForProjectDir: %v", err)
	}
	stop := func() {
		StopWatcher()
		ClearWatcherCallback()
	}
	return ch, stop
}

func waitForSession(t *testing.T, ch <-chan *spi.AgentChatSession) *spi.AgentChatSession {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a session emit")
		return nil
	}
}

func assertNoSession(t *testing.T, ch <-chan *spi.AgentChatSession, within time.Duration) {
	t.Helper()
	select {
	case s := <-ch:
		t.Fatalf("unexpected session emit: %+v", s)
	case <-time.After(within):
	}
}

// --- tests ------------------------------------------------------------------

// TestWatch_EmitsOnNewSession writes a valid pi session into the watched
// encoded-cwd directory and asserts the callback fires with the right id/slug.
func TestWatch_EmitsOnNewSession(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	// Project paths are real absolute directories from t.TempDir. A rootless
	// path such as /pi-watch-proj becomes D:\pi-watch-proj under filepath.Abs
	// on Windows, and since the default layout checks the header cwd against
	// the project, a header written with the rootless form would be filtered
	// out there. Pi records the full path its process saw.
	projectPath := filepath.Join(t.TempDir(), "pi-watch-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	ch, stop := startWatch(t, projectPath)
	defer stop()

	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-emit.jsonl")
	if wErr := os.WriteFile(path, []byte(validSession("sess-emit", projectPath, "please summarize the readme")), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}

	s := waitForSession(t, ch)
	if s.SessionID != "sess-emit" {
		t.Errorf("SessionID = %q, want sess-emit", s.SessionID)
	}
	if s.Slug == "" {
		t.Error("expected a non-empty slug derived from the first user message")
	}
}

// TestWatch_PartialLineThenComplete writes a truncated trailing JSON line (which
// must not panic or emit), then completes the file and asserts an emit follows.
// Exercises readLines fragment handling under a live write.
func TestWatch_PartialLineThenComplete(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-partial-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	ch, stop := startWatch(t, projectPath)
	defer stop()

	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-partial.jsonl")

	// Header + a truncated (unclosed) user line: no complete entry yet.
	partial := piHeaderLine("sess-partial", projectPath) + "\n" +
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-09-03T10:00:01.000Z","message":{"role":"user","content":"hel`
	if wErr := os.WriteFile(path, []byte(partial), 0o600); wErr != nil {
		t.Fatalf("WriteFile partial: %v", wErr)
	}
	assertNoSession(t, ch, 700*time.Millisecond)

	// Complete the file with a valid session.
	if wErr := os.WriteFile(path, []byte(validSession("sess-partial", projectPath, "hello there full line")), 0o600); wErr != nil {
		t.Fatalf("WriteFile complete: %v", wErr)
	}
	s := waitForSession(t, ch)
	if s.SessionID != "sess-partial" {
		t.Errorf("SessionID = %q, want sess-partial", s.SessionID)
	}
}

// TestWatch_SessionInfoRename appends a session_info rename mid-session and
// asserts the whole-file reparse re-emits with the new name present. Note:
// spi.AgentChatSession carries no display-name field (the session_info name
// surfaces through the metadata scan path, not the emit path), so the watcher's
// observable rename signal is the re-emitted RawData reflecting the appended
// entry.
func TestWatch_SessionInfoRename(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-rename-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	ch, stop := startWatch(t, projectPath)
	defer stop()

	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-rename.jsonl")
	base := validSession("sess-rename", projectPath, "initial prompt here")
	if wErr := os.WriteFile(path, []byte(base), 0o600); wErr != nil {
		t.Fatalf("WriteFile base: %v", wErr)
	}
	_ = waitForSession(t, ch)

	// Append a session_info rename and re-write the file.
	renamed := base + piSessionInfoLine("m3", "m2", "My Renamed Session") + "\n"
	if wErr := os.WriteFile(path, []byte(renamed), 0o600); wErr != nil {
		t.Fatalf("WriteFile rename: %v", wErr)
	}

	// Wait for a re-emit whose RawData includes the appended rename.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-ch:
			if s.SessionID == "sess-rename" && strings.Contains(s.RawData, "My Renamed Session") {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the re-emit reflecting the rename")
		}
	}
}

// TestWatch_FlatLayoutFiltersByCwd verifies the flat PI_CODING_AGENT_SESSION_DIR
// layout emits only sessions whose header cwd matches the project.
func TestWatch_FlatLayoutFiltersByCwd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envSessionDir, tmp) // flat layout: files live directly in tmp
	// The flat filter compares the header's cwd with filepath.Abs of the
	// project path. On Windows Abs prepends the drive letter to a rootless
	// path such as \pi-flat-proj, so a header written with the rootless form
	// never matches and the test timed out there. Pi records the full path
	// its process saw, so the fixture uses real absolute paths too.
	projectPath := filepath.Join(t.TempDir(), "pi-flat-proj")
	otherPath := filepath.Join(t.TempDir(), "some-other-proj")

	ch, stop := startWatch(t, projectPath)
	defer stop()

	// A session for a different project must be filtered out.
	otherFile := filepath.Join(tmp, "2026-09-03T10-00-00-000Z_other.jsonl")
	if wErr := os.WriteFile(otherFile, []byte(validSession("sess-other", otherPath, "other project prompt")), 0o600); wErr != nil {
		t.Fatalf("WriteFile other: %v", wErr)
	}
	// A session for our project must be emitted.
	mineFile := filepath.Join(tmp, "2026-09-03T10-00-05-000Z_mine.jsonl")
	if wErr := os.WriteFile(mineFile, []byte(validSession("sess-mine", projectPath, "my project prompt")), 0o600); wErr != nil {
		t.Fatalf("WriteFile mine: %v", wErr)
	}

	// The first matching emit must be ours; the other-project file is never emitted.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-ch:
			if s.SessionID == "sess-other" {
				t.Fatalf("emitted a session from another project: %q", s.SessionID)
			}
			if s.SessionID == "sess-mine" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the matching-project emit")
		}
	}
}

// TestWatch_DefaultLayoutCollidingDirFiltersByCwd verifies the default per-cwd
// layout emits only sessions whose header cwd matches the project when two
// projects share an encoded directory (<base>/a/b and <base>/a-b both encode
// to the same name). The paths are real absolute temp paths for the same
// reason as in TestWatch_FlatLayoutFiltersByCwd.
func TestWatch_DefaultLayoutCollidingDirFiltersByCwd(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	base := t.TempDir()
	projectPath := filepath.Join(base, "a", "b")
	otherPath := filepath.Join(base, "a-b")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	otherDir, err := ProjectSessionDir(otherPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir(other): %v", err)
	}
	if otherDir != targetDir {
		t.Fatalf("expected the two projects to share an encoded dir, got %q and %q", targetDir, otherDir)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	ch, stop := startWatch(t, projectPath)
	defer stop()

	// A session for the colliding project must be filtered out.
	otherFile := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_other.jsonl")
	if wErr := os.WriteFile(otherFile, []byte(validSession("sess-other", otherPath, "other project prompt")), 0o600); wErr != nil {
		t.Fatalf("WriteFile other: %v", wErr)
	}
	// A session for our project must be emitted.
	mineFile := filepath.Join(targetDir, "2026-09-03T10-00-05-000Z_mine.jsonl")
	if wErr := os.WriteFile(mineFile, []byte(validSession("sess-mine", projectPath, "my project prompt")), 0o600); wErr != nil {
		t.Fatalf("WriteFile mine: %v", wErr)
	}

	// The first matching emit must be ours; the other-project file is never emitted.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-ch:
			if s.SessionID == "sess-other" {
				t.Fatalf("emitted a session from a colliding project: %q", s.SessionID)
			}
			if s.SessionID == "sess-mine" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the matching-project emit")
		}
	}
}

// TestWatch_IgnoresNonJSONLAndHeaderOnly asserts a .txt file and a header-only
// .jsonl produce no emit.
func TestWatch_IgnoresNonJSONLAndHeaderOnly(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-ignore-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	ch, stop := startWatch(t, projectPath)
	defer stop()

	// Non-JSONL file: ignored by extension.
	if wErr := os.WriteFile(filepath.Join(targetDir, "notes.txt"), []byte("hello"), 0o600); wErr != nil {
		t.Fatalf("WriteFile txt: %v", wErr)
	}
	// Header-only session: ParseSession errors "no entries" → nothing to emit.
	headerOnly := piHeaderLine("sess-header-only", projectPath) + "\n"
	if wErr := os.WriteFile(filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_header.jsonl"), []byte(headerOnly), 0o600); wErr != nil {
		t.Fatalf("WriteFile header-only: %v", wErr)
	}

	assertNoSession(t, ch, 1*time.Second)
}

// TestStopWatcher_JoinsInFlightSave models the `run pi` exit: pi writes its
// session file and exits, and StopWatcher runs right after. The callback here
// stands in for the markdown save; it blocks on a gate the test controls so
// the save is provably still in flight when StopWatcher is called. The
// assertions are that StopWatcher does not return while the save is in flight
// and that the markdown exists once it does return.
func TestStopWatcher_JoinsInFlightSave(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-join-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	markdown := filepath.Join(tmp, "saved-session.md")
	started := make(chan struct{})
	gate := make(chan struct{})
	SetWatcherCallback(func(s *spi.AgentChatSession) {
		close(started)
		<-gate
		if wErr := os.WriteFile(markdown, []byte("# "+s.SessionID+"\n"), 0o600); wErr != nil {
			t.Errorf("WriteFile markdown: %v", wErr)
		}
	})
	t.Cleanup(ClearWatcherCallback)
	if wErr := WatchForProjectDir(projectPath); wErr != nil {
		t.Fatalf("WatchForProjectDir: %v", wErr)
	}

	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-join.jsonl")
	if wErr := os.WriteFile(path, []byte(validSession("sess-join", projectPath, "final prompt before exit")), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the callback to start")
	}

	// pi has "exited": stop the watcher while the save is still in flight.
	stopped := make(chan struct{})
	go func() {
		StopWatcher()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("StopWatcher returned while the session save was still in flight")
	case <-time.After(300 * time.Millisecond):
	}

	close(gate)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("StopWatcher did not return after the save finished")
	}
	if _, statErr := os.Stat(markdown); statErr != nil {
		t.Fatalf("markdown missing after StopWatcher returned: %v", statErr)
	}
}

// TestWatch_DirectoryAppearsWithFileEmitsOnce covers the bootstrap gap: the
// session directory does not exist when the watch starts, and it appears with a
// complete session file already inside it (an agent that creates the directory
// and writes the whole file at once). The directory watch added after the
// bootstrap never sees an event for that file, so the sweep right after
// watcher.Add must emit it, and the sweep in StopWatcher must not emit it a
// second time. The directory is built elsewhere and renamed into place so the
// file is present at the instant the directory exists.
func TestWatch_DirectoryAppearsWithFileEmitsOnce(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-bootstrap-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	// The sessions root exists; the project's encoded directory does not.
	if mkErr := os.MkdirAll(filepath.Dir(targetDir), 0o755); mkErr != nil {
		t.Fatalf("MkdirAll root: %v", mkErr)
	}

	staging := filepath.Join(tmp, "staging", filepath.Base(targetDir))
	if mkErr := os.MkdirAll(staging, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll staging: %v", mkErr)
	}
	stagedFile := filepath.Join(staging, "2026-09-03T10-00-00-000Z_sess-boot.jsonl")
	if wErr := os.WriteFile(stagedFile, []byte(validSession("sess-boot", projectPath, "prompt written with the directory")), 0o600); wErr != nil {
		t.Fatalf("WriteFile staged: %v", wErr)
	}

	// Adoption follows arrival, not mtime: copying or renaming a directory
	// can preserve timestamps from long before this watcher started.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stagedFile, old, old); err != nil {
		t.Fatal(err)
	}
	ch, stop := startWatch(t, projectPath)
	defer stop()

	if rErr := os.Rename(staging, targetDir); rErr != nil {
		t.Fatalf("Rename staging dir into place: %v", rErr)
	}

	s := waitForSession(t, ch)
	if s.SessionID != "sess-boot" {
		t.Errorf("SessionID = %q, want sess-boot", s.SessionID)
	}

	// StopWatcher joins every dispatched callback, so once it returns anything
	// the stop sweep emitted is already in the channel.
	stop()
	if extra := len(ch); extra != 0 {
		t.Fatalf("got %d extra emit(s) after stop; the file must be emitted exactly once", extra)
	}
}

// TestStopWatcher_SweepSkipsSessionAlreadyEmitted asserts a session delivered
// through an fsnotify event is not delivered again by the sweep in StopWatcher.
// The file is renamed into the watched directory so it produces exactly one
// event (a single Create) and exactly one event-driven emit; the callback count
// must still be one after StopWatcher has swept the directory.
func TestStopWatcher_SweepSkipsSessionAlreadyEmitted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-sweep-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	var calls atomic.Int32
	emitted := make(chan struct{}, 16)
	SetWatcherCallback(func(*spi.AgentChatSession) {
		calls.Add(1)
		emitted <- struct{}{}
	})
	t.Cleanup(ClearWatcherCallback)
	if wErr := WatchForProjectDir(projectPath); wErr != nil {
		t.Fatalf("WatchForProjectDir: %v", wErr)
	}

	staged := filepath.Join(tmp, "staged.jsonl")
	if wErr := os.WriteFile(staged, []byte(validSession("sess-sweep", projectPath, "prompt delivered by event")), 0o600); wErr != nil {
		t.Fatalf("WriteFile staged: %v", wErr)
	}
	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-sweep.jsonl")
	if rErr := os.Rename(staged, path); rErr != nil {
		t.Fatalf("Rename into watched dir: %v", rErr)
	}
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the event-driven emit")
	}

	StopWatcher()
	if got := calls.Load(); got != 1 {
		t.Fatalf("callback ran %d times; want 1 (the stop sweep must skip a version already emitted)", got)
	}
}

// TestStopWatcher_SweepLeavesOlderFileAlone asserts a session file whose mtime
// predates the watch start is not emitted by either sweep: watch reports new
// activity only, and older sessions belong to `sync pi`. The file is written
// before the watch starts and its mtime is pushed an hour back so the grace
// window for coarse filesystem clocks cannot admit it.
func TestStopWatcher_SweepLeavesOlderFileAlone(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.Join(t.TempDir(), "pi-older-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-older.jsonl")
	if wErr := os.WriteFile(path, []byte(validSession("sess-older", projectPath, "prompt from before the watch")), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}
	old := time.Now().Add(-time.Hour)
	if cErr := os.Chtimes(path, old, old); cErr != nil {
		t.Fatalf("Chtimes: %v", cErr)
	}

	ch, stop := startWatch(t, projectPath)
	// A second StopWatcher after the explicit one below has nothing left to do.
	defer stop()
	assertNoSession(t, ch, 500*time.Millisecond) // the sweep after watcher.Add must skip it

	stop()
	if extra := len(ch); extra != 0 {
		t.Fatalf("the stop sweep emitted %d session(s) older than the watch start", extra)
	}
}

func TestWatch_LeavesFreshExistingSessionAlone(t *testing.T) {
	project := t.TempDir()
	dir := t.TempDir()
	t.Setenv(envSessionDir, dir)
	path := filepath.Join(dir, "existing.jsonl")
	if err := os.WriteFile(path, []byte(validSession("existing", project, "already saved")), 0o600); err != nil {
		t.Fatal(err)
	}
	ch, stop := startWatch(t, project)
	defer stop()
	assertNoSession(t, ch, 300*time.Millisecond)
	// The baseline must suppress only the old version, not subsequent activity.
	if err := os.WriteFile(path, []byte(validSession("existing", project, "updated after watch started")), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := waitForSession(t, ch); !strings.Contains(s.RawData, "updated after watch started") {
		t.Fatal("did not deliver the changed session")
	}
}

func TestWatch_CallbacksFinishInOrder(t *testing.T) {
	project := t.TempDir()
	dir := t.TempDir()
	t.Setenv(envSessionDir, dir)
	started := make(chan struct{})
	release := make(chan struct{})
	second := make(chan *spi.AgentChatSession, 16)
	var calls atomic.Int32
	SetWatcherCallback(func(s *spi.AgentChatSession) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		} else {
			second <- s
		}
	})
	if err := WatchForProjectDir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { StopWatcher(); ClearWatcherCallback() })
	// Always release a blocked callback, including on a failed assertion.
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	path := filepath.Join(dir, "ordered.jsonl")
	if err := os.WriteFile(path, []byte(validSession("ordered", project, "first")), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first callback did not start")
	}
	if err := os.WriteFile(path, []byte(validSession("ordered", project, "second updated prompt")), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second:
		t.Fatal("a newer callback ran before the first callback finished")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	released = true
	StopWatcher()
	if calls.Load() != 2 {
		t.Fatalf("got %d callbacks; want both versions delivered before shutdown", calls.Load())
	}
	if got := waitForSession(t, second); !strings.Contains(got.RawData, "second updated prompt") {
		t.Fatal("shutdown did not save the latest version")
	}
	// A new run must not inherit cancellation or callback work from the old run.
	if err := WatchForProjectDir(project); err != nil {
		t.Fatal(err)
	}
	StopWatcher()
}

func TestStopWatcher_WaitsForSlowSave(t *testing.T) {
	project, dir := t.TempDir(), t.TempDir()
	t.Setenv(envSessionDir, dir)
	started, release := make(chan struct{}), make(chan struct{})
	SetWatcherCallback(func(*spi.AgentChatSession) { close(started); <-release })
	if err := WatchForProjectDir(project); err != nil {
		t.Fatal(err)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		StopWatcher()
		ClearWatcherCallback()
	})
	if err := os.WriteFile(filepath.Join(dir, "slow.jsonl"), []byte(validSession("slow", project, "slow save")), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not start")
	}
	stopped := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() { StopWatcher(); close(stopped) })
	// A cloud save can exceed ten seconds. Elapsed time alone cannot authorize
	// dropping it; shutdown must still wait for the consumer to finish.
	select {
	case <-stopped:
		t.Fatal("shutdown abandoned an in-flight save")
	case <-time.After(11 * time.Second):
	}
	close(release)
	released = true
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish after save")
	}
	wg.Wait()
}

func TestWatch_ReconcilesWithoutFileEvent(t *testing.T) {
	project, dir := t.TempDir(), t.TempDir()
	t.Setenv(envSessionDir, dir)
	ch := make(chan *spi.AgentChatSession, 16)
	SetWatcherCallback(func(s *spi.AgentChatSession) { ch <- s })
	w, err := startProjectWatcher(project)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopWatcher(w); ClearWatcherCallback() })
	// Observe one completed delivery before dropping the watch so the missing
	// update cannot be rescued by the initial startup scan.
	if err := os.WriteFile(filepath.Join(dir, "seed.jsonl"), []byte(validSession("seed", project, "startup completed")), 0600); err != nil {
		t.Fatal(err)
	}
	if got := waitForSession(t, ch); got.SessionID != "seed" {
		t.Fatalf("wrong seed session %q", got.SessionID)
	}
	// Remove the real OS watch so the write cannot be delivered by fsnotify.
	// The periodic reconcile must re-arm the watch and recover the missing update.
	if err := w.fs.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "missed.jsonl"), []byte(validSession("missed", project, "missed event")), 0600); err != nil {
		t.Fatal(err)
	}
	if s := waitForSession(t, ch); s.SessionID != "missed" {
		t.Fatalf("wrong session %q", s.SessionID)
	}
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, path := range w.fs.WatchList() {
			if path == dir {
				return
			}
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("reconciliation did not restore the filesystem watch")
		}
	}

}

func TestWatchAgent_ReportsTerminalWatcherFailure(t *testing.T) {
	project, dir := t.TempDir(), t.TempDir()
	t.Setenv(envSessionDir, dir)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() { result <- NewProvider().WatchAgent(ctx, project, false, func(*spi.AgentChatSession) {}) })
	t.Cleanup(func() { cancel(); StopWatcher(); wg.Wait() })
	// Wait on the production state rather than assuming registration takes a
	// fixed amount of time. Closing the OS watcher reproduces a terminal stream failure.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(5 * time.Second)
	var w *piWatcher
	for w == nil {
		watcherMutex.Lock()
		w = activeWatcher
		watcherMutex.Unlock()
		if w != nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("watcher did not start")
		}
	}
	if err := w.fs.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("want watcher failure, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WatchAgent hid terminal watcher failure")
	}
}

func TestWatch_CallbackPanicDoesNotStopUpdates(t *testing.T) {
	project, dir := t.TempDir(), t.TempDir()
	t.Setenv(envSessionDir, dir)
	first := make(chan struct{})
	next := make(chan *spi.AgentChatSession, 16)
	var calls atomic.Int32
	SetWatcherCallback(func(s *spi.AgentChatSession) {
		if calls.Add(1) == 1 {
			close(first)
			panic("consumer failed")
		}
		next <- s
	})
	if err := WatchForProjectDir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { StopWatcher(); ClearWatcherCallback() })
	path := filepath.Join(dir, "panic.jsonl")
	if err := os.WriteFile(path, []byte(validSession("panic", project, "first")), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("first callback did not start")
	}
	if err := os.WriteFile(path, []byte(validSession("panic", project, "after consumer panic")), 0600); err != nil {
		t.Fatal(err)
	}
	if s := waitForSession(t, next); !strings.Contains(s.RawData, "after consumer panic") {
		t.Fatal("missing update after callback panic")
	}
}

func TestWatch_WriteDuringBaselineStillEmits(t *testing.T) {
	project, dir := t.TempDir(), t.TempDir()
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	ch := make(chan *spi.AgentChatSession, 16)
	w := &piWatcher{fs: fs, dir: dir, flat: true, candidates: []string{project},
		stamps: make(map[string]fileStamp), baseline: make(map[string]bool), pending: make(map[string]time.Time),
		callback: func(s *spi.AgentChatSession) { ch <- s }}
	if err := w.ensureWatch(); err != nil {
		t.Fatal(err)
	}
	// This write happens after registration but before the baseline can stat it.
	// Its event must win over the already-updated baseline signature.
	if err := os.WriteFile(filepath.Join(dir, "racing.jsonl"), []byte(validSession("racing", project, "written during startup")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.recordBaseline(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := w.run(ctx); err != nil {
			t.Errorf("watch failed: %v", err)
		}
	})
	t.Cleanup(func() { cancel(); wg.Wait() })
	if s := waitForSession(t, ch); s.SessionID != "racing" {
		t.Fatalf("wrong session %q", s.SessionID)
	}
}

func TestWatch_DebugExportStaysWithinProject(t *testing.T) {
	project, dir := t.TempDir(), t.TempDir()
	t.Setenv(envSessionDir, dir)
	spi.SetDebugBaseDir(t.TempDir())
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	SetWatcherDebugRaw(true)
	t.Cleanup(func() { SetWatcherDebugRaw(false) })
	ch, stop := startWatch(t, project)
	defer stop()
	if err := os.WriteFile(filepath.Join(dir, "a-other.jsonl"), []byte(validSession("other-project", t.TempDir(), "another project")), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "z-mine.jsonl"), []byte(validSession("my-project", project, "my prompt")), 0600); err != nil {
		t.Fatal(err)
	}
	if s := waitForSession(t, ch); s.SessionID != "my-project" {
		t.Fatalf("unexpected session %q", s.SessionID)
	}
	stop()
	if _, err := os.Stat(spi.GetDebugDir("other-project")); !os.IsNotExist(err) {
		t.Fatalf("exported another project's debug data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spi.GetDebugDir("my-project"), "1.json")); err != nil {
		t.Fatalf("missing matching session debug export: %v", err)
	}
}
