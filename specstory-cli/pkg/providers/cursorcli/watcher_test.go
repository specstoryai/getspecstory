package cursorcli

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
