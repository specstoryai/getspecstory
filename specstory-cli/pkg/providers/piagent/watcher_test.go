package piagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// Give the goroutine a moment to register the directory watch before writes.
	time.Sleep(200 * time.Millisecond)
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
	projectPath := filepath.FromSlash("/pi-watch-proj")

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
	projectPath := filepath.FromSlash("/pi-partial-proj")

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
	projectPath := filepath.FromSlash("/pi-rename-proj")

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
	projectPath := filepath.FromSlash("/pi-ignore-proj")

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
	projectPath := filepath.FromSlash("/pi-join-proj")

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
	time.Sleep(200 * time.Millisecond) // let the goroutine register the directory watch

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

// TestStopWatcher_BoundsWaitOnStuckCallback asserts a callback that never
// finishes cannot hang StopWatcher past callbackDrainTimeout.
func TestStopWatcher_BoundsWaitOnStuckCallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(envAgentDir, tmp)
	projectPath := filepath.FromSlash("/pi-stuck-proj")

	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if mkErr := os.MkdirAll(targetDir, 0o755); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}

	prevTimeout := callbackDrainTimeout
	callbackDrainTimeout = 200 * time.Millisecond
	t.Cleanup(func() { callbackDrainTimeout = prevTimeout })

	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	SetWatcherCallback(func(*spi.AgentChatSession) {
		close(started)
		<-release
		close(finished)
	})
	t.Cleanup(ClearWatcherCallback)
	// Let the stuck callback go at the end so callbackWg is back to zero for
	// the next test in the package.
	t.Cleanup(func() {
		close(release)
		<-finished
	})
	if wErr := WatchForProjectDir(projectPath); wErr != nil {
		t.Fatalf("WatchForProjectDir: %v", wErr)
	}
	time.Sleep(200 * time.Millisecond)

	path := filepath.Join(targetDir, "2026-09-03T10-00-00-000Z_sess-stuck.jsonl")
	if wErr := os.WriteFile(path, []byte(validSession("sess-stuck", projectPath, "prompt for a stuck save")), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the callback to start")
	}

	begin := time.Now()
	StopWatcher()
	if elapsed := time.Since(begin); elapsed > 3*time.Second {
		t.Fatalf("StopWatcher took %v with a stuck callback; want about %v", elapsed, callbackDrainTimeout)
	}
}
