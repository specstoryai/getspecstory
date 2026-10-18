package spi

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// BusyTimeoutPragma is appended to SQLite DSNs so each new connection waits for a
// briefly-held lock instead of failing immediately with SQLITE_BUSY. The agent that
// owns the database may be running and writing to it (e.g. during a checkpoint), so
// without a busy timeout a read or a journal-mode change can fail on a transient lock.
const BusyTimeoutPragma = "_pragma=busy_timeout(5000)"

// EnsureWALMode ensures the database is running in WAL journal mode. WAL lets our
// read-only connections and the owning agent's writer proceed concurrently without
// blocking each other, and guarantees the -wal file exists for watchers that rely
// on fsnotify events. This must open a read-write connection because PRAGMA
// journal_mode cannot change the mode on a read-only connection. It is intended to
// be called once per database, not on every read.
func EnsureWALMode(dbPath string) error {
	// Open read-write to be able to change journal mode. mode=rw (not the
	// default create-if-missing) so a wrong path errors instead of leaving a
	// stray empty database behind; the file: scheme is required because the
	// driver only honors mode= on URI-form DSNs.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=rw&"+BusyTimeoutPragma)
	if err != nil {
		return fmt.Errorf("failed to open database for WAL check: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close database after WAL check", "error", closeErr)
		}
	}()

	// Check current journal mode
	var currentMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&currentMode); err != nil {
		return fmt.Errorf("failed to query journal mode: %w", err)
	}

	if strings.EqualFold(currentMode, "wal") {
		slog.Debug("Database already in WAL mode", "path", dbPath)
		return nil
	}

	// Switch to WAL mode. QueryRow rather than Exec because the pragma returns
	// the resulting mode as a row; checking it catches a silent failure to switch.
	var newMode string
	if err := db.QueryRow("PRAGMA journal_mode=WAL").Scan(&newMode); err != nil {
		return fmt.Errorf("failed to set WAL mode: %w", err)
	}

	if !strings.EqualFold(newMode, "wal") {
		return fmt.Errorf("failed to enable WAL mode: got %q instead", newMode)
	}

	slog.Info("Enabled WAL mode on database", "path", dbPath)
	return nil
}
