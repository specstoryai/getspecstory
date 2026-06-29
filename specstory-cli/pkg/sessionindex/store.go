// Package sessionindex implements sessions.db — the machine-level index of every coding-agent
// session SpecStory knows about, across all projects and providers. It backs the
// `specstory resume` selection UX and is (re)built by `specstory reindex`.
//
// sessions.db is a DERIVED CACHE over the native session stores: it can be deleted and
// fully rebuilt at any time. See docs/SESSIONS-DB.md for the design and schema.
package sessionindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // SQLite driver (pure Go), same as pkg/provenance
)

// Connection-pool sizing. Writes (reindex) must serialize on a single connection to
// avoid SQLITE_BUSY/deadlocks. The interactive browse path (resume/search) only reads,
// so it opens several connections — WAL permits many concurrent readers, which keeps a
// single slow full-text query (a broad prefix can take seconds) from starving the UI.
const (
	writerConns = 1
	readerConns = 4
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

	// IsNew is a write-time hint, not a persisted column: when true, the caller has
	// determined this (agent, session_id) has no existing index row, so upsertOne can skip
	// looking up and deleting a prior FTS row before insert. Leave false when unsure — the
	// lookup-and-delete is then kept, which is always correct (a missing prior row is a no-op).
	IsNew bool
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

// Open opens (or creates) sessions.db for writing — a single serialized connection,
// applying WAL + performance pragmas (matching pkg/provenance) and ensuring the schema
// exists. Use OpenReader for the read-only interactive browse path.
func Open(path string) (*Store, error) {
	return openWith(path, writerConns)
}

// OpenReader opens sessions.db for the interactive browse path (resume/search), which
// only reads. It allows several concurrent connections so one slow full-text query can't
// starve the UI (WAL permits many simultaneous readers). Never write through this handle.
func OpenReader(path string) (*Store, error) {
	return openWith(path, readerConns)
}

func openWith(path string, maxConns int) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout=15000&_pragma=journal_mode(WAL)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sessions.db: %w", err)
	}

	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

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
// scan order in scanSessions stays in lockstep with it.
const sessionColumns = `project_id, project_name, agent, session_id, created_at, updated_at,
	user_turns, total_turns, slug, name, native_path, origin_cwd, size, mtime, index_version, indexed_at`

// sessionInsertColumns is sessionColumns plus fts_rowid, the internal link to the session's
// FTS row. fts_rowid is write-only — set on insert, read only by SessionBody's join — so it is
// deliberately kept out of sessionColumns and the Session struct rather than threaded through
// every read path.
const sessionInsertColumns = sessionColumns + `, fts_rowid`

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
		fts_rowid     INTEGER,
		PRIMARY KEY (agent, session_id)
	);
	-- Composite over the project filter PLUS the picker's sort order. ListByProject
	-- (the resume picker's hot path) filters by project_id and orders by
	-- updated_at DESC, created_at DESC; this index serves both, so SQLite walks it
	-- backward instead of filtering then sorting in a temp b-tree. project_id is the
	-- left prefix, so ProjectCount/UnattributedCount still use it. Replaces the old
	-- single-column idx_sessions_project (dropped in the migration below).
	CREATE INDEX IF NOT EXISTS idx_sessions_project_recent ON sessions(project_id, updated_at, created_at);

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
		return fmt.Errorf("executing schema: %w", err)
	}
	// Migration for indexes created before index_version existed (the column is part of
	// the freshness fingerprint). Ignore the error when it already exists.
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN index_version INTEGER DEFAULT 0`)
	// fts_rowid links a session row to its sessions_fts row, so the body read and the
	// delete-before-insert are O(1) rowid lookups instead of whole-FTS scans (session_id/agent
	// are UNINDEXED). NULL on rows written before this column existed; a reindex (reindexVersion
	// bump) repopulates it. Ignore the error when the column already exists.
	_, _ = s.db.Exec(`ALTER TABLE sessions ADD COLUMN fts_rowid INTEGER`)
	// Drop the old single-column project index now superseded by the composite
	// idx_sessions_project_recent (project_id is its left prefix). Idempotent; a no-op
	// on fresh databases that never had it.
	_, _ = s.db.Exec(`DROP INDEX IF EXISTS idx_sessions_project`)
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

// sessionUpsertStmts are the per-row statements UpsertBatch prepares once and reuses across
// every row in a transaction, so the SQL is parsed once per batch instead of once per row.
type sessionUpsertStmts struct {
	insSession    *sql.Stmt
	selOldRowid   *sql.Stmt
	delFTSByRowid *sql.Stmt
	delFTSByKey   *sql.Stmt
	insFTS        *sql.Stmt
}

// upsertOne writes one session's row and full-text row using the batch's prepared statements.
// FTS5 standalone tables are not auto-synced, so the FTS row is maintained by hand. The new FTS
// row is inserted first so its rowid can be stored on the sessions row (fts_rowid) — that link
// is what makes later body reads and replace-deletes O(1) rowid lookups instead of whole-FTS
// scans (session_id/agent are UNINDEXED). For an existing session the prior FTS row is removed
// first; brand-new sessions (sess.IsNew) skip that lookup-and-delete entirely.
func upsertOne(st sessionUpsertStmts, sess Session) error {
	if !sess.IsNew {
		if err := deleteOldFTSRow(st, sess); err != nil {
			return err
		}
	}
	res, err := st.insFTS.Exec(sess.SessionID, sess.Agent, sess.Name, sess.Body)
	if err != nil {
		return fmt.Errorf("insert fts row: %w", err)
	}
	ftsRowid, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("read fts rowid: %w", err)
	}
	if _, err := st.insSession.Exec(
		sess.ProjectID, sess.ProjectName, sess.Agent, sess.SessionID, sess.CreatedAt, sess.UpdatedAt,
		sess.UserTurns, sess.TotalTurns, sess.Slug, sess.Name, sess.NativePath, sess.OriginCwd,
		sess.Size, sess.Mtime, sess.IndexVersion, sess.IndexedAt, ftsRowid); err != nil {
		return fmt.Errorf("upsert session row: %w", err)
	}
	return nil
}

// deleteOldFTSRow removes an existing session's current FTS row before the sessions row is
// replaced (INSERT OR REPLACE would otherwise drop the fts_rowid link and orphan the FTS row).
// It resolves the row by its stored fts_rowid (O(1)); for rows written before fts_rowid existed
// (NULL) it falls back to the by-key delete — a whole-FTS scan that a reindex retires by
// repopulating fts_rowid.
func deleteOldFTSRow(st sessionUpsertStmts, sess Session) error {
	var oldRowid sql.NullInt64
	switch err := st.selOldRowid.QueryRow(sess.Agent, sess.SessionID).Scan(&oldRowid); {
	case errors.Is(err, sql.ErrNoRows):
		return nil // no prior row to remove
	case err != nil:
		return fmt.Errorf("look up old fts rowid: %w", err)
	}
	if oldRowid.Valid {
		if _, err := st.delFTSByRowid.Exec(oldRowid.Int64); err != nil {
			return fmt.Errorf("clear fts row by rowid: %w", err)
		}
		return nil
	}
	if _, err := st.delFTSByKey.Exec(sess.Agent, sess.SessionID); err != nil {
		return fmt.Errorf("clear legacy fts row: %w", err)
	}
	return nil
}

// prepareUpsert prepares the per-row statements on tx and returns them with a closer.
func prepareUpsert(tx *sql.Tx) (sessionUpsertStmts, func(), error) {
	var prepared []*sql.Stmt
	closer := func() {
		for _, s := range prepared {
			_ = s.Close()
		}
	}
	prep := func(what, query string) (*sql.Stmt, error) {
		s, err := tx.Prepare(query)
		if err != nil {
			closer()
			return nil, fmt.Errorf("prepare %s: %w", what, err)
		}
		prepared = append(prepared, s)
		return s, nil
	}

	insSession, err := prep("session upsert", `INSERT OR REPLACE INTO sessions (`+sessionInsertColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	selOldRowid, err := prep("fts rowid lookup", `SELECT fts_rowid FROM sessions WHERE agent = ? AND session_id = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	delFTSByRowid, err := prep("fts delete by rowid", `DELETE FROM sessions_fts WHERE rowid = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	delFTSByKey, err := prep("fts delete by key", `DELETE FROM sessions_fts WHERE agent = ? AND session_id = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	insFTS, err := prep("fts insert", `INSERT INTO sessions_fts (session_id, agent, name, body) VALUES (?,?,?,?)`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	return sessionUpsertStmts{
		insSession:    insSession,
		selOldRowid:   selOldRowid,
		delFTSByRowid: delFTSByRowid,
		delFTSByKey:   delFTSByKey,
		insFTS:        insFTS,
	}, closer, nil
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

	st, closeStmts, err := prepareUpsert(tx)
	if err != nil {
		return err
	}
	defer closeStmts()

	for _, sess := range sessions {
		if err := upsertOne(st, sess); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert: %w", err)
	}
	committed = true
	return nil
}

// Exists reports whether a session row is already indexed, looked up by the primary key
// (agent, session_id) so it stays O(log n) instead of scanning. Because a session's sessions
// row and its sessions_fts row are always written together (upsertOne, one transaction), a
// missing sessions row guarantees a missing FTS row — which lets the live writer set
// Session.IsNew and skip the whole-table FTS delete for genuinely new sessions.
func (s *Store) Exists(agent, sessionID string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE agent = ? AND session_id = ?`, agent, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

// ProjectSummary is a rolled-up view of one project for the all-projects picker.
type ProjectSummary struct {
	ProjectID    string
	ProjectName  string
	Sessions     int            // total sessions in the project
	LastActivity string         // most recent updated_at across the project
	AgentCounts  map[string]int // sessions per agent (claude, codex, …)
}

// ListProjects returns one rolled-up summary per project, most recently active first.
// Used by the all-projects view (date-bucketed). The unknown-project bucket is included;
// the caller decides how to present it.
func (s *Store) ListProjects() ([]ProjectSummary, error) {
	rows, err := s.db.Query(`SELECT project_id, project_name, agent, COUNT(*), MAX(updated_at)
		FROM sessions GROUP BY project_id, agent`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byID := map[string]*ProjectSummary{}
	for rows.Next() {
		var pid, pname, agent, last string
		var n int
		if err := rows.Scan(&pid, &pname, &agent, &n, &last); err != nil {
			return nil, err
		}
		ps, ok := byID[pid]
		if !ok {
			ps = &ProjectSummary{ProjectID: pid, ProjectName: pname, AgentCounts: map[string]int{}}
			byID[pid] = ps
		}
		ps.Sessions += n
		ps.AgentCounts[agent] += n
		if ps.ProjectName == "" {
			ps.ProjectName = pname
		}
		if last > ps.LastActivity {
			ps.LastActivity = last
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ProjectSummary, 0, len(byID))
	for _, ps := range byID {
		out = append(out, *ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out, nil
}

// SessionBody returns the full-text conversation body for a session (for the preview
// pane), or "" if the session has no indexed body (e.g. Cursor, metadata-only).
func (s *Store) SessionBody(agent, sessionID string) (string, error) {
	var body string
	// Fast path: resolve the FTS row by the rowid stored on the sessions row (O(1)), instead of
	// scanning the whole FTS (session_id/agent are UNINDEXED).
	err := s.db.QueryRow(`SELECT f.body FROM sessions s
		JOIN sessions_fts f ON f.rowid = s.fts_rowid
		WHERE s.agent = ? AND s.session_id = ?`, agent, sessionID).Scan(&body)
	if err == nil {
		return body, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// No fts_rowid link (a row written before the column existed). Fall back to the by-key scan;
	// a reindex repopulates fts_rowid and retires this path.
	err = s.db.QueryRow(`SELECT body FROM sessions_fts WHERE agent = ? AND session_id = ?`,
		agent, sessionID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return body, err
}

// Search runs a full-text query and returns matching sessions, most recent first.
// projectID scopes the search to one project; "" searches across all projects.
func (s *Store) Search(query, projectID string) ([]Session, error) {
	return s.SearchContext(context.Background(), query, projectID)
}

// SearchContext is Search bound to a context. The interactive search cancels the context
// when a newer keystroke arrives, which aborts a slow in-flight query and frees its
// connection. Snippets are deliberately fetched separately for only the visible rows:
// FTS5 snippet generation over hundreds of full transcripts dominates broad searches.
func (s *Store) SearchContext(ctx context.Context, query, projectID string) ([]Session, error) {
	q := `SELECT ` + prefixed("s", sessionColumns) + `
		FROM sessions_fts
		JOIN sessions s ON s.agent = sessions_fts.agent AND s.session_id = sessions_fts.session_id
		WHERE sessions_fts MATCH ?`
	args := []any{query}
	if projectID != "" {
		q += ` AND s.project_id = ?`
		args = append(args, projectID)
	}
	// Order by recency, not FTS5's BM25 rank. For full-conversation transcripts BM25 is
	// noisy (length-normalization dominates) and a single-term query gets no IDF signal,
	// so relevance order looks arbitrary; newest-first is what's useful when browsing your
	// own history. It also makes LIMIT keep the 500 most RECENT matches (vs the 500 densest)
	// and skips BM25 scoring entirely. updated_at is ISO 8601, so TEXT sort = chronological.
	q += ` ORDER BY s.updated_at DESC LIMIT 500`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSessions(rows)
}

// Snippets returns highlighted match snippets for the provided sessions. The result is
// keyed by FingerprintKey(agent, session_id). Matched terms are wrapped in \x02 and \x03
// (STX/ETX) so callers can highlight without colliding with conversation text.
func (s *Store) Snippets(query string, sessions []Session) (map[string]string, error) {
	return s.SnippetsContext(context.Background(), query, sessions)
}

// SnippetsContext is Snippets bound to a context. Callers should pass only the currently
// visible rows; snippet() is intentionally lazy because it is the expensive part of FTS
// search on long transcripts.
func (s *Store) SnippetsContext(ctx context.Context, query string, sessions []Session) (map[string]string, error) {
	out := map[string]string{}
	if query == "" || len(sessions) == 0 {
		return out, nil
	}

	clauses := make([]string, 0, len(sessions))
	args := []any{query}
	seen := map[string]bool{}
	for _, sess := range sessions {
		key := FingerprintKey(sess.Agent, sess.SessionID)
		if seen[key] {
			continue
		}
		seen[key] = true
		clauses = append(clauses, `(agent = ? AND session_id = ?)`)
		args = append(args, sess.Agent, sess.SessionID)
	}
	if len(clauses) == 0 {
		return out, nil
	}

	q := `SELECT agent, session_id, snippet(sessions_fts, 3, char(2), char(3), '…', 12)
		FROM sessions_fts
		WHERE sessions_fts MATCH ? AND (` + strings.Join(clauses, ` OR `) + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var agent, sessionID, snippet string
		if err := rows.Scan(&agent, &sessionID, &snippet); err != nil {
			return nil, err
		}
		out[FingerprintKey(agent, sessionID)] = snippet
	}
	return out, rows.Err()
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
