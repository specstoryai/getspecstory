package copilotcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// fastWatcherTiming shortens the watcher's timers for the test.
func fastWatcherTiming(t *testing.T) {
	t.Helper()
	savedDebounce, savedBurst, savedReconcile := debounceDelay, maxBurstDelay, reconcileInterval
	t.Cleanup(func() { debounceDelay, maxBurstDelay, reconcileInterval = savedDebounce, savedBurst, savedReconcile })
	debounceDelay, maxBurstDelay, reconcileInterval = 20*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond
}

// deliveries records the sessions a watcher delivers, safe for the watcher's
// worker to write while the test reads.
type deliveries struct {
	mu       sync.Mutex
	sessions []*spi.AgentChatSession
}

func (d *deliveries) callback(session *spi.AgentChatSession) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessions = append(d.sessions, session)
}

func (d *deliveries) ids() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.sessions))
	for _, session := range d.sessions {
		ids = append(ids, session.SessionID)
	}
	return ids
}

// waitFor polls until cond holds or a deadline passes, rather than sleeping a
// fixed time that is either flaky or slow.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// quietPeriod lets several reconcile scans run, long enough for any wrongful
// delivery to happen.
func quietPeriod() { time.Sleep(4 * reconcileInterval) }

// appendRecord appends one record to a transcript, as Copilot does.
func appendRecord(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// writeOldSession writes a session whose transcript was last modified an hour
// ago, as a session from before the watcher started (or one copied into place
// with its timestamps preserved) would be.
func writeOldSession(t *testing.T, home, sessionID, project string) string {
	t.Helper()
	path := writeSession(t, home, sessionID,
		sessionStart(t, sessionID, project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "work in " + sessionID}),
	)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWatcherDropsWatchesThatAgeOutOfTheWindow(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	path := writeOldSession(t, home, "s1", project)
	fsWatcher, err := spi.NewFSWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsWatcher.Close() })
	w := &sessionWatcher{projectRoot: project, fsWatcher: fsWatcher, watched: map[string]bool{}, owned: map[string]bool{}}

	setModTimeAndUpdate := func(modTime time.Time) bool {
		t.Helper()
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		files, err := listSessionFiles()
		if err != nil {
			t.Fatal(err)
		}
		w.updateWatches(files)
		return w.watched[path] && slices.Contains(fsWatcher.WatchList(), path)
	}

	if !setModTimeAndUpdate(time.Now()) {
		t.Fatal("a recent transcript of the project is not watched")
	}
	if setModTimeAndUpdate(time.Now().AddDate(0, 0, -spi.WatchWindowDays-2)) {
		t.Error("a transcript that aged out of the watch window is still watched")
	}
	if !setModTimeAndUpdate(time.Now()) {
		t.Error("an aged-out transcript written again is not watched again")
	}
}

func TestWatcherDoesNotWarnAboutAFirstRecordStillBeingWritten(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	start := sessionStart(t, "s1", project)
	path := writeSession(t, home, "s1")
	if err := os.WriteFile(path, []byte(start[:len(start)/2]), 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	w := &sessionWatcher{projectRoot: project, owned: map[string]bool{}}
	if _, belongs := w.ownership(path); belongs {
		t.Error("a session whose header is still being written was matched to the project")
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("a record still being written was logged as a warning: %s", logs.String())
	}
}

func TestWatcherLeavesUnchangedSessionsAloneAtStartup(t *testing.T) {
	fastWatcherTiming(t)
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	writeOldSession(t, home, "existing", project)

	var got deliveries
	w, err := startWatcher(project, "", false, got.callback)
	if err != nil {
		t.Fatal(err)
	}
	quietPeriod()
	w.Stop()

	if ids := got.ids(); len(ids) != 0 {
		t.Errorf("delivered %v for a session written before startup", ids)
	}
}

func TestWatcherEmitsActivityDuringStartup(t *testing.T) {
	fastWatcherTiming(t)
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	existing := writeOldSession(t, home, "existing", project)

	// Activity lands after the baseline is taken but before the first check.
	duringStartup := func() {
		appendRecord(t, existing, record(t, eventAssistantMsg, "a1", map[string]any{"content": "during startup"}))
		writeSession(t, home, "fresh", sessionStart(t, "fresh", project),
			record(t, eventUserMessage, "u1", map[string]any{"content": "new work"}))
	}

	var got deliveries
	w, err := startWatcherWithHook(project, "", false, got.callback, duringStartup)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if !waitFor(t, func() bool { return len(got.ids()) >= 2 }) {
		t.Fatalf("delivered %v, want both sessions changed during startup", got.ids())
	}
}

func TestWatcherAdoptsSessionsArrivingAfterStartup(t *testing.T) {
	t.Run("into an existing store", func(t *testing.T) {
		fastWatcherTiming(t)
		home := useSessionStore(t)
		project := newProjectDir(t, "project")
		writeOldSession(t, home, "existing", project)

		var got deliveries
		w, err := startWatcher(project, "", false, got.callback)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Stop()

		// Copied or restored into place: old timestamps, but its arrival is
		// activity.
		writeOldSession(t, home, "restored", project)

		if !waitFor(t, func() bool { return slices.Equal(got.ids(), []string{"restored"}) }) {
			t.Fatalf("delivered %v, want only the session that arrived after startup", got.ids())
		}
	})

	t.Run("with the store created after startup", func(t *testing.T) {
		fastWatcherTiming(t)
		home := filepath.Join(t.TempDir(), "copilot-home")
		t.Setenv(homeEnvVar, home)
		project := newProjectDir(t, "project")

		var got deliveries
		w, err := startWatcher(project, "", false, got.callback)
		if err != nil {
			t.Fatalf("watcher must wait for a missing store, got %v", err)
		}
		defer w.Stop()

		writeOldSession(t, home, "first", project)

		if !waitFor(t, func() bool { return slices.Equal(got.ids(), []string{"first"}) }) {
			t.Fatalf("delivered %v, want the session in the late store", got.ids())
		}
	})
}

// TestWatcherBaselinesSessionsDiscoveredLate covers a session that existed at
// startup but could not be read then: once readable it is still history.
func TestWatcherBaselinesSessionsDiscoveredLate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions cannot hide a session on Windows")
	}
	fastWatcherTiming(t)
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	writeOldSession(t, home, "hidden", project)
	sessionDir := filepath.Join(home, sessionStateDirName, "hidden")
	if err := os.Chmod(sessionDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sessionDir, 0o755) })
	if _, err := os.Stat(filepath.Join(sessionDir, eventsFileName)); err == nil {
		t.Skip("permissions do not hide files from this user (running as root?)")
	}

	var got deliveries
	w, err := startWatcher(project, "", false, got.callback)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * reconcileInterval)
	if err := os.Chmod(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	quietPeriod()
	w.Stop()

	if ids := got.ids(); len(ids) != 0 {
		t.Errorf("delivered %v merely because a session became readable later", ids)
	}
}

func TestWatcherShutdown(t *testing.T) {
	t.Run("no activity delivers nothing", func(t *testing.T) {
		fastWatcherTiming(t)
		home := useSessionStore(t)
		project := newProjectDir(t, "project")
		writeOldSession(t, home, "existing", project)

		var got deliveries
		w, err := startWatcher(project, "", false, got.callback)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		w.Stop()
		if ids := got.ids(); len(ids) != 0 {
			t.Errorf("delivered %v on shutdown without activity", ids)
		}
	})

	t.Run("pending update is drained", func(t *testing.T) {
		fastWatcherTiming(t)
		// Long enough that only the shutdown check can see the update.
		debounceDelay, maxBurstDelay, reconcileInterval = time.Hour, time.Hour, time.Hour
		home := useSessionStore(t)
		project := newProjectDir(t, "project")
		existing := writeOldSession(t, home, "existing", project)

		var got deliveries
		w, err := startWatcher(project, "", false, got.callback)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		appendRecord(t, existing, record(t, eventAssistantMsg, "a1", map[string]any{"content": "last words"}))
		w.Stop()

		if !slices.Equal(got.ids(), []string{"existing"}) {
			t.Fatalf("delivered %v, want the pending update drained on shutdown", got.ids())
		}
		if !strings.Contains(got.sessions[0].RawData, "last words") {
			t.Error("drained delivery does not include the pending update")
		}
	})
}

// eventOnlyWatcher starts a watcher on an already-watched transcript with the
// reconcile scan pushed past any deadline, so only the fsnotify path can deliver.
func eventOnlyWatcher(t *testing.T, debounce, burst time.Duration) (string, *deliveries, *sessionWatcher) {
	t.Helper()
	savedDebounce, savedBurst, savedReconcile := debounceDelay, maxBurstDelay, reconcileInterval
	t.Cleanup(func() { debounceDelay, maxBurstDelay, reconcileInterval = savedDebounce, savedBurst, savedReconcile })
	debounceDelay, maxBurstDelay, reconcileInterval = debounce, burst, time.Hour

	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	path := writeOldSession(t, home, "s1", project)
	got := &deliveries{}
	watcher, err := startWatcher(project, "", false, got.callback)
	if err != nil {
		t.Fatalf("startWatcher: %v", err)
	}
	t.Cleanup(watcher.Stop)
	return path, got, watcher
}

// A quick run of appends, one streamed reply, is one delivery holding them all.
func TestWatcherCollapsesAQuietBurstIntoOneDelivery(t *testing.T) {
	const debounce = 200 * time.Millisecond
	path, got, _ := eventOnlyWatcher(t, debounce, time.Hour)

	for i := range 5 {
		appendRecord(t, path, record(t, eventAssistantMsg, fmt.Sprintf("a%d", i), map[string]any{"content": "chunk"}))
	}
	if !waitFor(t, func() bool { return len(got.ids()) > 0 }) {
		t.Fatal("no delivery after the burst went quiet")
	}
	time.Sleep(3 * debounce)

	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.sessions) != 1 {
		t.Fatalf("%d deliveries, want the burst collapsed into 1", len(got.sessions))
	}
	exchanges := got.sessions[0].SessionData.Exchanges
	if len(exchanges) != 1 || len(exchanges[0].Messages) != 6 {
		t.Errorf("delivered exchanges = %+v, want the prompt and all 5 chunks", exchanges)
	}
}

// The debounce never fires here, so only maxBurstDelay can deliver: a long agent
// turn must reach the user while it is still being written.
func TestWatcherDeliversDuringASustainedBurst(t *testing.T) {
	path, got, _ := eventOnlyWatcher(t, time.Hour, 100*time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for i := 0; len(got.ids()) == 0; i++ {
		if time.Now().After(deadline) {
			t.Fatal("no delivery while writes continued")
		}
		appendRecord(t, path, record(t, eventAssistantMsg, fmt.Sprintf("a%d", i), map[string]any{"content": "chunk"}))
		time.Sleep(10 * time.Millisecond)
	}
}

// A store that does not exist yet is normal, and adopted when it appears. One
// that exists but cannot be listed must fail at startup rather than wait forever.
func TestWatcherStartupDistinguishesMissingAndInvalidStores(t *testing.T) {
	fastWatcherTiming(t)
	project := newProjectDir(t, "project")
	callback := func(*spi.AgentChatSession) {}

	t.Run("missing store", func(t *testing.T) {
		useSessionStore(t)
		watcher, err := startWatcher(project, "", false, callback)
		if err != nil {
			t.Fatalf("startWatcher: %v", err)
		}
		watcher.Stop()
	})
	t.Run("store is a regular file", func(t *testing.T) {
		useSessionStore(t)
		root, err := sessionsRoot()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(root, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if watcher, err := startWatcher(project, "", false, callback); err == nil {
			watcher.Stop()
			t.Fatal("startWatcher accepted a store that cannot be listed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err = NewProvider().WatchAgent(ctx, project, false, callback)
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("WatchAgent = %v, want the startup failure", err)
		}
	})
}

func TestWatcherDeliversLiveUpdatesForProjectOnly(t *testing.T) {
	fastWatcherTiming(t)
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	other := newProjectDir(t, "other")

	existing := writeOldSession(t, home, "existing", project)

	delivered := make(chan string, 10)
	watcher, err := startWatcher(project, "", false, func(session *spi.AgentChatSession) {
		delivered <- session.SessionID
	})
	if err != nil {
		t.Fatalf("startWatcher: %v", err)
	}

	writeSession(t, home, "other", sessionStart(t, "other", other),
		record(t, eventUserMessage, "u1", map[string]any{"content": "elsewhere"}))
	writeSession(t, home, "fresh", sessionStart(t, "fresh", project),
		record(t, eventUserMessage, "u1", map[string]any{"content": "new work"}))

	// Appending to the baselined session is new activity too.
	appendRecord(t, existing, record(t, eventAssistantMsg, "a1", map[string]any{"content": "more"}))

	// Both sessions must arrive live, before Stop's final sweep could mask a
	// watcher that only ever delivers on shutdown.
	seen := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for !seen["existing"] || !seen["fresh"] {
		select {
		case id := <-delivered:
			seen[id] = true
		case <-deadline:
			t.Fatalf("live delivery timed out; delivered so far: %v", seen)
		}
	}
	watcher.Stop()
	close(delivered)

	for id := range delivered {
		seen[id] = true
	}
	if seen["other"] || len(seen) != 2 {
		t.Errorf("delivered sessions = %v, want only existing and fresh", seen)
	}

	// Only this project's transcripts are watched, file by file: never the
	// sessions root or another project's session (each watched directory
	// costs a descriptor per entry on macOS).
	var watched []string
	for path := range watcher.watched {
		watched = append(watched, filepath.Base(filepath.Dir(path))+"/"+filepath.Base(path))
	}
	slices.Sort(watched)
	if want := []string{"existing/" + eventsFileName, "fresh/" + eventsFileName}; !slices.Equal(watched, want) {
		t.Errorf("watched = %v, want %v", watched, want)
	}
}

// A session picked from another project and resumed here keeps that project's
// directory in its transcript. It must still be followed, live and through
// the shutdown sweep, while that project's other sessions stay excluded.
func TestWatcherFollowsResumedSessionFromAnotherProject(t *testing.T) {
	fastWatcherTiming(t)
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	other := newProjectDir(t, "other")

	resumed := writeOldSession(t, home, "resumed", other)
	unrelated := writeOldSession(t, home, "unrelated", other)

	var got deliveries
	w, err := startWatcher(project, "resumed", false, got.callback)
	if err != nil {
		t.Fatal(err)
	}

	appendRecord(t, resumed, record(t, eventAssistantMsg, "a1", map[string]any{"content": "continued here"}))
	appendRecord(t, unrelated, record(t, eventAssistantMsg, "a1", map[string]any{"content": "not this run"}))
	if !waitFor(t, func() bool { return slices.Contains(got.ids(), "resumed") }) {
		t.Fatalf("resumed session was not delivered live; delivered %v", got.ids())
	}

	// A last write just before Copilot exits is caught by the shutdown sweep.
	before := len(got.ids())
	appendRecord(t, resumed, record(t, eventAssistantMsg, "a2", map[string]any{"content": "final words"}))
	w.Stop()
	if len(got.ids()) == before {
		t.Errorf("shutdown sweep did not deliver the resumed session's last write")
	}

	if slices.Contains(got.ids(), "unrelated") {
		t.Errorf("delivered %v, want only the resumed session from the other project", got.ids())
	}
}
