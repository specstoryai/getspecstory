package cursorcli

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func createWatcherDatabase(t *testing.T, root, id string) *sql.DB {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "store.db")+"?"+spi.BusyTimeoutPragma)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)",
		"CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)",
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	meta := hex.EncodeToString([]byte(`{"createdAt":1700000000000}`))
	if _, err := db.Exec("INSERT INTO meta VALUES ('0', ?)", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO blobs VALUES ('message', ?)", watcherMessage("initial")); err != nil {
		t.Fatal(err)
	}
	return db
}

func watcherMessage(text string) []byte {
	data, _ := json.Marshal(map[string]any{"role": "user", "content": []map[string]string{{"type": "text", "text": text}}})
	return data
}

func awaitCursorUpdate(t *testing.T, updates <-chan *spi.AgentChatSession, text string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case session := <-updates:
			if strings.Contains(session.RawData, text) {
				return
			}
		case <-timer.C:
			t.Fatalf("no session update containing %q", text)
		}
	}
}

func TestCursorWatcherExistingSessionUpdatesAndRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	db := createWatcherDatabase(t, root, "existing")
	updates := make(chan *spi.AgentChatSession, 100)
	w := &CursorWatcher{projectPath: t.TempDir(), hashDir: root, sessionCallback: func(s *spi.AgentChatSession) { updates <- s }}
	t.Cleanup(w.Stop)
	for n := 0; n < 2; n++ {
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		// Stop also performs a final scan; unchanged history must stay silent.
		w.Stop()
		select {
		case <-updates:
			t.Fatal("emitted existing history")
		default:
		}
		if err := w.Start(); err != nil {
			t.Fatal(err)
		}
		text := strings.Repeat("updated", n+1)
		// UPDATE keeps the row count fixed, exposing the old count-based polling.
		if _, err := db.Exec("UPDATE blobs SET data=? WHERE id='message'", watcherMessage(text)); err != nil {
			t.Fatal(err)
		}
		awaitCursorUpdate(t, updates, text)
		w.Stop()
		for len(updates) > 0 {
			<-updates
		}
	}
}

func TestCursorWatcherAdoptsLateDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-yet", "project")
	updates := make(chan *spi.AgentChatSession, 100)
	w := &CursorWatcher{projectPath: t.TempDir(), hashDir: root, sessionCallback: func(s *spi.AgentChatSession) { updates <- s }}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)
	createWatcherDatabase(t, root, "new")
	awaitCursorUpdate(t, updates, "initial")
}

func TestCursorWatcherFinalSaveAndPanicRecovery(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "save", true: "panic"}[panics], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			db := createWatcherDatabase(t, root, "existing")
			var saved bool
			w := &CursorWatcher{projectPath: t.TempDir(), hashDir: root, sessionCallback: func(s *spi.AgentChatSession) {
				if strings.Contains(s.RawData, "last turn") {
					saved = true
				}
				if panics {
					panic("consumer")
				}
			}}
			t.Cleanup(w.Stop)
			if err := w.Start(); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE blobs SET data=?", watcherMessage("last turn")); err != nil {
				t.Fatal(err)
			}
			w.Stop()
			if !saved {
				t.Fatal("Stop returned before saving the final write")
			}
		})
	}
}

// Drive reconciliation synchronously so transaction ordering, rather than
// scheduler timing or sleeps, determines what the watcher can observe.
func newSynchronousCursorWatcher(t *testing.T, root string, callback func(*spi.AgentChatSession)) *CursorWatcher {
	t.Helper()
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	w := &CursorWatcher{
		projectPath: t.TempDir(), hashDir: root, sessionCallback: callback,
		watcher: watcher, watched: make(map[string]bool), stamps: make(map[string]databaseStamp),
		walEnabled: make(map[string]bool), databases: make(map[string]*watchedCursorDatabase),
		tsCache: NewMessageTimestampCache(),
	}
	t.Cleanup(func() {
		w.closeDatabases()
		_ = watcher.Close()
	})
	if err := w.reconcile(false); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestCursorWatcherCommitAfterFileEvent(t *testing.T) {
	for _, check := range []string{"event", "poll", "shutdown"} {
		t.Run(check, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "project")
			db := createWatcherDatabase(t, root, "existing")
			// Reuse the allocated WAL so committing does not change its size.
			if _, err := db.Exec("PRAGMA wal_checkpoint(RESTART)"); err != nil {
				t.Fatal(err)
			}
			var updates []string
			w := newSynchronousCursorWatcher(t, root, func(s *spi.AgentChatSession) {
				updates = append(updates, s.RawData)
			})
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback() })
			if _, err := tx.Exec("UPDATE blobs SET data=?", watcherMessage("committed update")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "existing", "store.db")
			// Model the filesystem event arriving before SQLite publishes the
			// commit. The old snapshot is still the only one readers can see.
			eventTime := time.Now().Add(time.Second)
			if err := os.Chtimes(path+"-wal", eventTime, eventTime); err != nil {
				t.Fatal(err)
			}
			if err := w.reconcile(true); err != nil {
				t.Fatal(err)
			}
			beforeCommit := cursorDatabaseStamp(path)
			if len(updates) != 0 {
				t.Fatalf("exported unmodified history before commit: %v", updates)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			// A real slow fsync publishes the commit without another change to
			// the WAL metadata. Recreate that condition without delaying disk I/O.
			if err := os.Chtimes(path+"-wal", beforeCommit[1].modified, beforeCommit[1].modified); err != nil {
				t.Fatal(err)
			}
			if after := cursorDatabaseStamp(path); after != beforeCommit {
				t.Fatalf("fixture changed file metadata at commit: before=%v after=%v", beforeCommit, after)
			}
			switch check {
			case "event":
				if err := w.reconcile(true); err != nil {
					t.Fatal(err)
				}
			case "poll":
				w.checkDatabaseCommits()
			case "shutdown":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				w.watchLoop(ctx)
			}
			if len(updates) != 1 || !strings.Contains(updates[0], "committed update") {
				t.Fatalf("committed update was not exported exactly once: %v", updates)
			}
			if check != "shutdown" {
				w.checkDatabaseCommits()
				if len(updates) != 1 {
					t.Fatalf("exported unchanged commit again: %v", updates)
				}
			}
		})
	}
}

func TestCursorWatcherCommitDuringCallback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	db := createWatcherDatabase(t, root, "existing")
	var updates []string
	var callbackErr error
	w := newSynchronousCursorWatcher(t, root, func(s *spi.AgentChatSession) {
		updates = append(updates, s.RawData)
		if len(updates) == 1 {
			_, callbackErr = db.Exec("UPDATE blobs SET data=?", watcherMessage("second commit"))
		}
	})
	if _, err := db.Exec("UPDATE blobs SET data=?", watcherMessage("first commit")); err != nil {
		t.Fatal(err)
	}
	w.checkDatabaseCommits()
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	w.checkDatabaseCommits()
	if len(updates) != 2 || !strings.Contains(updates[0], "first commit") || !strings.Contains(updates[1], "second commit") {
		t.Fatalf("lost commit during callback: %v", updates)
	}
}

func TestCursorWatcherClosesCommitTrackers(t *testing.T) {
	for _, reason := range []string{"shutdown", "expired", "deleted"} {
		t.Run(reason, func(t *testing.T) {
			if reason == "deleted" && runtime.GOOS == "windows" {
				t.Skip("Windows does not allow moving an open SQLite database")
			}
			root := filepath.Join(t.TempDir(), "project")
			createWatcherDatabase(t, root, "existing")
			w := newSynchronousCursorWatcher(t, root, nil)
			tracker := w.databases["existing"]
			if tracker == nil {
				t.Fatal("missing commit tracker")
			}
			switch reason {
			case "shutdown":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				w.watchLoop(ctx)
			case "expired":
				old := spi.WatchWindowCutoff(time.Now()).Add(-time.Hour)
				path := filepath.Join(root, "existing", "store.db")
				for _, suffix := range []string{"", "-wal"} {
					if err := os.Chtimes(path+suffix, old, old); err != nil {
						t.Fatal(err)
					}
				}
				// The first reconciliation notices the file change; the next
				// prunes the now-idle session outside the watch window.
				for range 2 {
					if err := w.reconcile(true); err != nil {
						t.Fatal(err)
					}
				}
			case "deleted":
				// The old path disappears while its tracked inode is still open.
				if err := os.Rename(root, root+"-moved"); err != nil {
					t.Fatal(err)
				}
				w.checkDatabaseCommits()
			}
			if len(w.databases) != 0 {
				t.Fatal("retained idle commit tracker")
			}
			if err := tracker.conn.PingContext(context.Background()); err != sql.ErrConnDone {
				t.Fatalf("tracker connection was not closed: %v", err)
			}
		})
	}
}
