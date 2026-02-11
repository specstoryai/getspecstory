package provenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // SQLite driver (pure Go, same as Intent)
)

// Store handles SQLite persistence for file events and agent events.
// It uses WAL mode for safe concurrent access from multiple processes.
type Store struct {
	db *sql.DB
}

// OpenStore opens (or creates) a SQLite database at the given path.
// Applies WAL mode and performance pragmas matching Intent's configuration.
func OpenStore(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout=15000&_pragma=journal_mode(WAL)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Serialize writes to avoid deadlocks (same pattern as Intent)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Performance pragmas matching Intent's sqlite.go
	pragmas := []string{
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size = -64000",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA mmap_size = 268435456",
		"PRAGMA page_size = 8192",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("setting pragma %q: %w", p, err)
		}
	}

	s := &Store{db: db}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensuring schema: %w", err)
	}

	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ensureSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS events (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		file_path TEXT NOT NULL,
		timestamp INTEGER NOT NULL,
		matched_with TEXT,
		payload TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_events_unmatched
		ON events(type, timestamp) WHERE matched_with IS NULL;
	`
	_, err := s.db.Exec(schema)
	return err
}

// PushFileEvent stores a file event for later correlation.
func (s *Store) PushFileEvent(ctx context.Context, event FileEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid file event: %w", err)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling file event: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events (id, type, file_path, timestamp, payload) VALUES (?, ?, ?, ?, ?)`,
		event.ID, "file_event", event.Path, event.Timestamp.UnixNano(), string(payload),
	)
	if err != nil {
		return fmt.Errorf("inserting file event: %w", err)
	}

	return nil
}

// PushAgentEvent stores an agent event for later correlation.
// The file path is normalized before storage (backslashes → forward slashes, leading / added).
func (s *Store) PushAgentEvent(ctx context.Context, event AgentEvent) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid agent event: %w", err)
	}

	normalizedPath := NormalizePath(event.FilePath)

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling agent event: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO events (id, type, file_path, timestamp, payload) VALUES (?, ?, ?, ?, ?)`,
		event.ID, "agent_event", normalizedPath, event.Timestamp.UnixNano(), string(payload),
	)
	if err != nil {
		return fmt.Errorf("inserting agent event: %w", err)
	}

	return nil
}

// unmatchedEvent is an internal row representation used during correlation.
type unmatchedEvent struct {
	ID        string
	Type      string
	FilePath  string
	Timestamp time.Time
	Payload   string
}

// QueryUnmatchedFileEvents returns file events that haven't been correlated yet
// and fall within the given time range, ordered by timestamp ascending.
func (s *Store) QueryUnmatchedFileEvents(ctx context.Context, since, until time.Time) ([]unmatchedEvent, error) {
	return s.queryUnmatched(ctx, "file_event", since, until)
}

// QueryUnmatchedAgentEvents returns agent events that haven't been correlated yet
// and fall within the given time range, ordered by timestamp ascending.
func (s *Store) QueryUnmatchedAgentEvents(ctx context.Context, since, until time.Time) ([]unmatchedEvent, error) {
	return s.queryUnmatched(ctx, "agent_event", since, until)
}

func (s *Store) queryUnmatched(ctx context.Context, eventType string, since, until time.Time) ([]unmatchedEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, type, file_path, timestamp, payload
		 FROM events
		 WHERE type = ? AND matched_with IS NULL
		   AND timestamp >= ? AND timestamp <= ?
		 ORDER BY timestamp`,
		eventType, since.UnixNano(), until.UnixNano(),
	)
	if err != nil {
		return nil, fmt.Errorf("querying unmatched %s: %w", eventType, err)
	}
	defer func() { _ = rows.Close() }()

	var events []unmatchedEvent
	for rows.Next() {
		var e unmatchedEvent
		var nanos int64
		if err := rows.Scan(&e.ID, &e.Type, &e.FilePath, &nanos, &e.Payload); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		e.Timestamp = time.Unix(0, nanos).UTC()
		events = append(events, e)
	}

	return events, rows.Err()
}

// SetMatchedWith marks two events as correlated with each other in a single transaction.
func (s *Store) SetMatchedWith(ctx context.Context, eventID, matchedWithID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx,
		`UPDATE events SET matched_with = ? WHERE id = ?`,
		matchedWithID, eventID,
	)
	if err != nil {
		return fmt.Errorf("updating event %s: %w", eventID, err)
	}

	_, err = tx.ExecContext(ctx,
		`UPDATE events SET matched_with = ? WHERE id = ?`,
		eventID, matchedWithID,
	)
	if err != nil {
		return fmt.Errorf("updating event %s: %w", matchedWithID, err)
	}

	return tx.Commit()
}
