// Package sessionindex implements sessions.db — the machine-level index of every coding-agent
// session SpecStory knows about, across all projects and providers. It backs the
// `specstory resume` selection UX and is (re)built by `specstory reindex`.
//
// sessions.db is a DERIVED CACHE over the native session stores: it can be deleted and
// fully rebuilt at any time. See docs/SESSIONS-DB.md for the design and schema.
package sessionindex

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // SQLite driver (pure Go), same as pkg/provenance
)

// Session is one row of the restore index (the `sessions` table), plus Body, which is
// indexed into the FTS table rather than stored on the row. See docs/SESSIONS-DB.md.
type Session struct {
	ProjectID    string // resolved git_id (else workspace_id)
	ProjectName  string
	Agent        string // provider id: claude, codex, gemini, droid, deepseek, cursor
	SessionID    string // native session id (uuid)
	CreatedAt    string // ISO 8601, first turn
	UpdatedAt    string // ISO 8601, last activity (last turn, else file mtime)
	UserTurns    int    // count of user prompts
	TotalTurns   int    // count of all messages
	Slug         string
	Name         string
	NativePath   string // absolute path the provider opens to read this session
	OriginCwd    string // working directory the session was launched from
	Size         int64  // native file size, bytes — part of the freshness fingerprint
	Mtime        int64  // native file mtime, epoch ms — part of the freshness fingerprint
	IndexVersion int    // reindex logic version that wrote this row — part of the fingerprint
	IndexedAt    string // ISO 8601, when reindex last wrote this row

	// Body is the full conversation text, indexed into sessions_fts (not persisted on
	// the sessions row). Left empty on rows read back out of the index.
	Body string
}

// Fingerprint identifies an indexed session's freshness: the native file's size and
// mtime, plus the reindex logic version that produced the row. reindex skips a session
// whose fingerprint is unchanged. See docs/SESSIONS-DB.md.
type Fingerprint struct {
	Size    int64
	Mtime   int64
	Version int
}

// Store is a handle to sessions.db.
type Store struct {
	db *sql.DB
}

// DefaultPath returns ~/.specstory/sessions.db.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".specstory", "sessions.db"), nil
}

// Open opens (or creates) sessions.db at the given path, applying WAL + performance
// pragmas (matching pkg/provenance) and ensuring the schema exists.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout=15000&_pragma=journal_mode(WAL)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sessions.db: %w", err)
	}

	// Serialize writes to avoid deadlocks (same pattern as pkg/provenance).
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, p := range []string{
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size = -64000",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA mmap_size = 268435456",
		"PRAGMA page_size = 8192",
	} {
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

// sessionColumns is the canonical sessions column list, shared by every SELECT so the
// scan order in scanSession stays in lockstep with it.
const sessionColumns = `project_id, project_name, agent, session_id, created_at, updated_at,
	user_turns, total_turns, slug, name, native_path, origin_cwd, size, mtime, index_version, indexed_at`

func (s *Store) ensureSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		project_id   TEXT NOT NULL,
		project_name TEXT,
		agent        TEXT NOT NULL,
		session_id   TEXT NOT NULL,
		created_at   TEXT,
		updated_at   TEXT,
		user_turns   INTEGER,
		total_turns  INTEGER,
		slug         TEXT,
		name         TEXT,
		native_path  TEXT,
		origin_cwd   TEXT,
		size          INTEGER,
		mtime         INTEGER,
		index_version INTEGER,
		indexed_at    TEXT,
		PRIMARY KEY (agent, session_id)
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_id);

	-- Standalone FTS5 index over the conversation body + name. session_id/agent ride
	-- along UNINDEXED as join keys back to the sessions row. See docs/SESSIONS-DB.md.
	CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
		session_id UNINDEXED,
		agent UNINDEXED,
		name,
		body
	);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Migration for indexes created before index_version existed (the column is part of
	// the freshness fingerprint). Ignore the error when it already exists.
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN index_version INTEGER DEFAULT 0`)
	return nil
}

// Fingerprints returns the freshness fingerprint of every indexed session, keyed by
// fingerprintKey(agent, session_id). reindex loads this once and skips any session
// whose native file is unchanged (same size + mtime) and was indexed by the current
// logic version.
func (s *Store) Fingerprints() (map[string]Fingerprint, error) {
	rows, err := s.db.Query(`SELECT agent, session_id, size, mtime, index_version FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]Fingerprint)
	for rows.Next() {
		var agent, sessionID string
		var fp Fingerprint
		if err := rows.Scan(&agent, &sessionID, &fp.Size, &fp.Mtime, &fp.Version); err != nil {
			return nil, err
		}
		out[FingerprintKey(agent, sessionID)] = fp
	}
	return out, rows.Err()
}

// FingerprintKey is the map key for a session's fingerprint: agent + NUL + session_id.
func FingerprintKey(agent, sessionID string) string {
	return agent + "\x00" + sessionID
}

// upsertTx writes one session's row and full-text row within an open transaction.
// FTS5 standalone tables are not auto-synced, so the FTS row is replaced by hand.
func upsertTx(tx *sql.Tx, sess Session) error {
	if _, err := tx.Exec(`INSERT OR REPLACE INTO sessions (`+sessionColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sess.ProjectID, sess.ProjectName, sess.Agent, sess.SessionID, sess.CreatedAt, sess.UpdatedAt,
		sess.UserTurns, sess.TotalTurns, sess.Slug, sess.Name, sess.NativePath, sess.OriginCwd,
		sess.Size, sess.Mtime, sess.IndexVersion, sess.IndexedAt); err != nil {
		return fmt.Errorf("upsert session row: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sessions_fts WHERE agent = ? AND session_id = ?`,
		sess.Agent, sess.SessionID); err != nil {
		return fmt.Errorf("clear fts row: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO sessions_fts (session_id, agent, name, body) VALUES (?,?,?,?)`,
		sess.SessionID, sess.Agent, sess.Name, sess.Body); err != nil {
		return fmt.Errorf("insert fts row: %w", err)
	}
	return nil
}

// Upsert inserts or replaces a single session row and its full-text row, atomically. A
// re-index of the same session (same agent + session_id) replaces both in lockstep.
func (s *Store) Upsert(sess Session) error {
	return s.UpsertBatch([]Session{sess})
}

// UpsertBatch writes many sessions in one transaction — the write path for `reindex`,
// which batches to avoid thousands of tiny WAL commits.
func (s *Store) UpsertBatch(sessions []Session) error {
	if len(sessions) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin upsert: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, sess := range sessions {
		if err := upsertTx(tx, sess); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert: %w", err)
	}
	committed = true
	return nil
}

// Count returns the number of indexed sessions.
func (s *Store) Count() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ProjectCount returns the number of distinct attributed projects in the index
// (excluding the unknownID bucket).
func (s *Store) ProjectCount(unknownID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT project_id) FROM sessions WHERE project_id != ?`, unknownID).Scan(&n)
	return n, err
}

// UnattributedCount returns the number of sessions in the unknownID bucket.
func (s *Store) UnattributedCount(unknownID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project_id = ?`, unknownID).Scan(&n)
	return n, err
}

// ListByProject returns a project's sessions, newest activity first. Body is not
// populated (it lives only in the FTS index). Used by the `specstory resume` picker.
func (s *Store) ListByProject(projectID string) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionColumns+`
		FROM sessions WHERE project_id = ? ORDER BY updated_at DESC, created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSessions(rows)
}

// Search runs a full-text query over the conversation body + name and returns the
// matching sessions, best match first. Used by the `specstory resume` picker.
func (s *Store) Search(query string) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+prefixed("s", sessionColumns)+`
		FROM sessions_fts f
		JOIN sessions s ON s.agent = f.agent AND s.session_id = f.session_id
		WHERE sessions_fts MATCH ? ORDER BY rank`, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSessions(rows)
}

// prefixed qualifies each comma-separated column in cols with the given table alias,
// e.g. prefixed("s", "a, b") -> "s.a, s.b". Used to disambiguate the sessions columns
// in the FTS join (where session_id/agent also exist on the FTS table).
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// scanSessions scans rows selected with sessionColumns (in order) into Sessions.
func scanSessions(rows *sql.Rows) ([]Session, error) {
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(
			&s.ProjectID, &s.ProjectName, &s.Agent, &s.SessionID, &s.CreatedAt, &s.UpdatedAt,
			&s.UserTurns, &s.TotalTurns, &s.Slug, &s.Name, &s.NativePath, &s.OriginCwd,
			&s.Size, &s.Mtime, &s.IndexVersion, &s.IndexedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
