package cursorcli

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// data_version is only comparable on the same SQLite connection. Keep one
// pinned read-only connection per actively watched database, without holding a
// read transaction (which would prevent WAL checkpoints from progressing).
type watchedCursorDatabase struct {
	db      *sql.DB
	conn    *sql.Conn
	file    os.FileInfo
	version int64
	pending bool
}

func (d *watchedCursorDatabase) close() {
	_ = d.conn.Close()
	_ = d.db.Close()
}

func (w *CursorWatcher) closeDatabases() {
	for id, db := range w.databases {
		db.close()
		delete(w.databases, id)
	}
}

func (w *CursorWatcher) checkDatabaseCommits() {
	for id := range w.databases {
		w.checkDatabaseCommit(id, filepath.Join(w.hashDir, id, "store.db"), true)
	}
}

func (w *CursorWatcher) checkDatabaseCommit(id, path string, emit bool) {
	info, err := os.Stat(path)
	d := w.databases[id]
	// A deleted/replaced database must not leave us querying the old inode.
	if d != nil && (err != nil || !os.SameFile(d.file, info)) {
		d.close()
		delete(w.databases, id)
		delete(w.walEnabled, id)
		d = nil
	}
	if err != nil {
		return
	}
	if d == nil {
		db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&"+spi.BusyTimeoutPragma)
		if err != nil {
			slog.Warn("Cursor watcher could not open commit tracker", "path", path, "error", err)
			return
		}
		conn, err := db.Conn(context.Background())
		if err != nil {
			_ = db.Close()
			slog.Warn("Cursor watcher could not connect commit tracker", "path", path, "error", err)
			return
		}
		d = &watchedCursorDatabase{db: db, conn: conn, file: info, pending: emit}
		w.databases[id] = d
	}
	var version int64
	if err := d.conn.QueryRowContext(context.Background(), "PRAGMA data_version").Scan(&version); err != nil {
		slog.Warn("Cursor watcher could not read committed version", "path", path, "error", err)
		return
	}
	if !emit {
		d.version = version
		return
	}
	if !d.pending && version == d.version {
		return
	}
	if w.processSessionChanges(id, path) {
		// Record the version from BEFORE the read. A commit racing the read or
		// callback must remain visible to the next check, even if file metadata
		// was already updated while the writer was still syncing its WAL.
		d.version = version
		d.pending = false
	}
}
