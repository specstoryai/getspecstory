package opencode

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// fastWatcherTiming shortens the watcher's intervals for the test.
func fastWatcherTiming(t *testing.T) {
	t.Helper()
	debounce, burst, reconcile := debounceDelay, maxBurstDelay, reconcileInterval
	debounceDelay, maxBurstDelay, reconcileInterval = 20*time.Millisecond, 100*time.Millisecond, 150*time.Millisecond
	t.Cleanup(func() {
		debounceDelay, maxBurstDelay, reconcileInterval = debounce, burst, reconcile
	})
}

// deliveries records what the watcher delivers.
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
	var ids []string
	for _, session := range d.sessions {
		ids = append(ids, session.SessionID)
	}
	return ids
}

func (d *deliveries) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sessions)
}

// waitFor polls until condition holds or the deadline passes.
func waitFor(t *testing.T, condition func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return condition()
}

// quietPeriod is long enough for several reconciliation passes.
func quietPeriod() { time.Sleep(4 * reconcileInterval) }

// writeSession writes a session with one prompt and one reply, stamped at ms.
func writeSession(t *testing.T, db *sql.DB, id, project string, ms int64) {
	t.Helper()
	insertSession(t, db, id, project, ms, ms)
	insertMessage(t, db, id, id+"_u", recordUser, 1, ms, userData(ms, "prompt for "+id))
	insertMessage(t, db, id, id+"_a", recordAssistant, 2, ms, assistantData(ms, "reply for "+id))
}

// appendReply adds a reply to an existing session, stamped now, in one
// transaction so the watcher sees it as a single change.
func appendReply(t *testing.T, db *sql.DB, id string, seq int64, text string) {
	t.Helper()
	now := time.Now().UnixMilli()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id+"_"+text, id, recordAssistant, seq, now, now, assistantData(now, text)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE session_v2 SET time_updated = ? WHERE id = ?`, now, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func hourAgo() int64 { return time.Now().Add(-time.Hour).UnixMilli() }

func TestWatcherLeavesUnchangedSessionsAloneAtStartup(t *testing.T) {
	fastWatcherTiming(t)
	project := newProjectDir(t, "project")
	db := createFixtureDB(t, useFixtureStore(t))
	writeSession(t, db, "ses_existing", project, hourAgo())

	var got deliveries
	w, err := startWatcher(project, false, got.callback)
	if err != nil {
		t.Fatal(err)
	}
	quietPeriod()
	w.Stop()

	if got.count() != 0 {
		t.Errorf("delivered %v for a session written before startup", got.ids())
	}
}

func TestWatcherEmitsActivityDuringStartup(t *testing.T) {
	fastWatcherTiming(t)
	project := newProjectDir(t, "project")
	db := createFixtureDB(t, useFixtureStore(t))
	writeSession(t, db, "ses_existing", project, hourAgo())

	// Activity lands after the watch exists but before the first read.
	duringStartup := func() {
		appendReply(t, db, "ses_existing", 3, "during-startup")
		writeSession(t, db, "ses_new", project, time.Now().UnixMilli())
	}

	var got deliveries
	w, err := startWatcherWithHook(project, false, got.callback, duringStartup)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if !waitFor(t, func() bool { return got.count() >= 2 }) {
		t.Fatalf("delivered %v, want both sessions changed during startup", got.ids())
	}
}

func TestWatcherAdoptsStoreCreatedAfterStartup(t *testing.T) {
	fastWatcherTiming(t)
	project := newProjectDir(t, "project")
	dbPath := useFixtureStore(t)
	if _, err := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(err) {
		t.Fatalf("data directory exists before startup: %v", err)
	}

	var got deliveries
	w, err := startWatcher(project, false, got.callback)
	if err != nil {
		t.Fatalf("watcher must wait for a missing store, got %v", err)
	}
	defer w.Stop()

	// The store arrives after startup carrying a session with old timestamps
	// (restored or copied into place); arrival is activity.
	db := createFixtureDB(t, dbPath)
	writeSession(t, db, "ses_restored", project, hourAgo())

	if !waitFor(t, func() bool { return got.count() == 1 }) {
		t.Fatalf("delivered %v, want the session in the late store", got.ids())
	}
}

// TestWatcherAdoptsStoreRestoredDuringStartup covers a store that appears
// after startup began but before the watcher's first read: it arrived after
// startup, so its sessions are adopted even though their timestamps are old.
func TestWatcherAdoptsStoreRestoredDuringStartup(t *testing.T) {
	fastWatcherTiming(t)
	project := newProjectDir(t, "project")
	dbPath := useFixtureStore(t)

	restoreStore := func() {
		db := createFixtureDB(t, dbPath)
		writeSession(t, db, "ses_restored", project, hourAgo())
	}

	var got deliveries
	w, err := startWatcherWithHook(project, false, got.callback, restoreStore)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	if !waitFor(t, func() bool { return got.count() == 1 }) {
		t.Fatalf("delivered %v, want the session in the store restored during startup", got.ids())
	}
}

// TestWatcherBaselinesSessionsDiscoveredLate covers a pre-existing store the
// first read cannot see: sessions that predate startup are still baseline
// when a later read discovers them.
func TestWatcherBaselinesSessionsDiscoveredLate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions cannot hide the database on Windows")
	}
	fastWatcherTiming(t)
	project := newProjectDir(t, "project")
	dbPath := useFixtureStore(t)
	db := createFixtureDB(t, dbPath)
	writeSession(t, db, "ses_existing", project, hourAgo())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Hide the store from the first read, then reveal it.
	if err := os.Chmod(dbPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o644) })

	var got deliveries
	w, err := startWatcher(project, false, got.callback)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * reconcileInterval)
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatal(err)
	}
	quietPeriod()
	w.Stop()

	if got.count() != 0 {
		t.Errorf("delivered %v merely because the store became readable later", got.ids())
	}
}

func TestWatcherShutdown(t *testing.T) {
	t.Run("no activity delivers nothing", func(t *testing.T) {
		fastWatcherTiming(t)
		project := newProjectDir(t, "project")
		db := createFixtureDB(t, useFixtureStore(t))
		writeSession(t, db, "ses_existing", project, hourAgo())

		var got deliveries
		w, err := startWatcher(project, false, got.callback)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		w.Stop()
		if got.count() != 0 {
			t.Errorf("delivered %v on shutdown without activity", got.ids())
		}
	})

	t.Run("pending update is drained", func(t *testing.T) {
		fastWatcherTiming(t)
		// Long enough that only the shutdown read can see the update.
		debounceDelay, maxBurstDelay, reconcileInterval = time.Hour, time.Hour, time.Hour
		project := newProjectDir(t, "project")
		db := createFixtureDB(t, useFixtureStore(t))
		writeSession(t, db, "ses_existing", project, hourAgo())

		var got deliveries
		w, err := startWatcher(project, false, got.callback)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		appendReply(t, db, "ses_existing", 3, "last-words")
		w.Stop()

		if got.count() != 1 {
			t.Fatalf("delivered %v, want the pending update drained on shutdown", got.ids())
		}
		if !strings.Contains(got.sessions[0].RawData, "last-words") {
			t.Error("drained delivery does not include the pending update")
		}
	})
}

func TestWatcherDeliversLiveUpdatesOnce(t *testing.T) {
	fastWatcherTiming(t)
	debugDir := testutil.IsolateDebugDir(t)
	project := newProjectDir(t, "project")
	db := createFixtureDB(t, useFixtureStore(t))
	writeSession(t, db, "ses_live", project, hourAgo())

	var got deliveries
	w, err := startWatcher(project, true, got.callback)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	appendReply(t, db, "ses_live", 3, "update-one")
	if !waitFor(t, func() bool { return got.count() == 1 }) {
		t.Fatalf("delivered %v, want the update", got.ids())
	}
	// Reconciliation must not re-deliver an unchanged session.
	quietPeriod()
	if got.count() != 1 {
		t.Errorf("delivered %d times for one update", got.count())
	}
	// Live updates refresh the native debug export.
	if _, err := os.Stat(filepath.Join(debugDir, "ses_live", "4.json")); err != nil {
		t.Errorf("live update did not write debug records: %v", err)
	}
}

func TestWatcherFailsWithoutAStore(t *testing.T) {
	useFixtureStore(t)
	t.Setenv(dbEnvVar, inMemoryDB)
	if _, err := startWatcher(newProjectDir(t, "project"), false, func(*spi.AgentChatSession) {}); err == nil {
		t.Error("startWatcher() succeeded for an in-memory OpenCode store")
	}
}
