package spi

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// createTestDB creates a SQLite database at dbPath with the given journal mode
// and a small table so the file is a real, non-empty database.
func createTestDB(t *testing.T, dbPath string, journalMode string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Failed to close test database: %v", err)
		}
	}()

	// journal_mode pragmas return the resulting mode as a row, so QueryRow it
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode=" + journalMode).Scan(&mode); err != nil {
		t.Fatalf("Failed to set journal mode %s: %v", journalMode, err)
	}

	if _, err := db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("Failed to create test table: %v", err)
	}
}

// journalMode reads the current journal mode of the database at dbPath over a
// fresh read-only connection, so the assertion sees the persisted mode rather
// than any per-connection state.
func journalMode(t *testing.T, dbPath string) string {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("Failed to open database read-only: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("Failed to close database: %v", err)
		}
	}()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("Failed to query journal mode: %v", err)
	}
	return mode
}

func TestEnsureWALMode(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, dbPath string)
		wantErr bool
	}{
		{
			name: "converts delete mode database to WAL",
			setup: func(t *testing.T, dbPath string) {
				createTestDB(t, dbPath, "DELETE")
			},
			wantErr: false,
		},
		{
			name: "already WAL database succeeds unchanged",
			setup: func(t *testing.T, dbPath string) {
				createTestDB(t, dbPath, "WAL")
			},
			wantErr: false,
		},
		{
			name: "non-database file returns error",
			setup: func(t *testing.T, dbPath string) {
				// Long enough that SQLite reads a header and rejects it rather
				// than treating the file as a valid empty database
				garbage := []byte("this is not a sqlite database, just some text padding out the header")
				if err := os.WriteFile(dbPath, garbage, 0644); err != nil {
					t.Fatalf("Failed to write garbage file: %v", err)
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "store.db")
			tt.setup(t, dbPath)

			err := EnsureWALMode(dbPath)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("EnsureWALMode failed: %v", err)
			}

			if mode := journalMode(t, dbPath); mode != "wal" {
				t.Errorf("Expected journal mode wal, got %q", mode)
			}
		})
	}
}
