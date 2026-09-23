package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const (
	// dataDirName is OpenCode's directory under the XDG data home.
	dataDirName = "opencode"

	// defaultDBName is the database every released channel (latest, beta, ...)
	// shares, as chosen by OpenCode 2.0.14's database path logic.
	defaultDBName = "opencode.db"

	// dbEnvVar overrides the database file, resolved relative to the data
	// directory, exactly as OpenCode resolves it.
	dbEnvVar = "OPENCODE_DB"

	// inMemoryDB is the OPENCODE_DB value that tells OpenCode to keep no file.
	inMemoryDB = ":memory:"

	// sessionTable and messageTable are the 2.x schema's session tables. Their
	// absence means an OpenCode older than the supported baseline.
	sessionTable = "session_v2"
	messageTable = "session_message"
)

// errNoDatabase reports that OpenCode has not created its database yet, which
// callers treat as "no sessions" rather than a failure.
var errNoDatabase = errors.New("OpenCode database not found")

// sessionRecord is one session_v2 row. The typed fields are the ones the
// provider reasons about; Row keeps every column so RawData and the debug
// export preserve fields this code does not know about.
type sessionRecord struct {
	ID          string
	ParentID    string
	Directory   string
	Title       string
	Version     string
	TimeCreated int64
	TimeUpdated int64
	Row         map[string]any
}

// messageRecord is one session_message row, with the JSON payload kept raw so
// unfamiliar fields survive into RawData and the debug export.
type messageRecord struct {
	ID          string
	Type        string
	Seq         int64
	TimeCreated int64
	TimeUpdated int64
	Data        json.RawMessage
	Row         map[string]any
}

// sessionSnapshot is a session and its messages read in one transaction, so
// conversion, RawData and the debug export all describe the same moment even
// while OpenCode is streaming into the session.
type sessionSnapshot struct {
	Session  sessionRecord
	Messages []messageRecord
}

// sessionSummary is the lightweight view used for listing, project matching
// and change detection, read without decoding any message payload.
type sessionSummary struct {
	ID          string
	Directory   string
	Title       string
	TimeCreated int64
	// TimeUpdated advances on a title change, which touches no message row.
	TimeUpdated int64
	// MessageCount and LastMessageUpdate advance when a record is appended or
	// an assistant row is rewritten while it streams.
	MessageCount      int64
	LastMessageUpdate int64
	// FirstUserData is the payload of the earliest user record, empty when
	// the session has none yet.
	FirstUserData string
	// FirstShellData is the payload of the earliest user shell command, which
	// names a session that has no typed prompt.
	FirstShellData string
}

// signature is the change-detection key for a session: any write OpenCode
// makes to the session or its messages moves at least one of these fields.
type signature struct {
	TimeUpdated       int64
	MessageCount      int64
	LastMessageUpdate int64
}

func (s sessionSummary) signature() signature {
	return signature{
		TimeUpdated:       s.TimeUpdated,
		MessageCount:      s.MessageCount,
		LastMessageUpdate: s.LastMessageUpdate,
	}
}

// getDataDir returns OpenCode's data directory. OpenCode derives it the same
// way on every OS: $XDG_DATA_HOME, falling back to ~/.local/share (there is no
// %APPDATA% branch, even on Windows).
func getDataDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, dataDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", dataDirName), nil
}

// getDatabasePath returns the database file OpenCode uses, honoring the same
// OPENCODE_DB override OpenCode does. Pre-release channels that write
// opencode-<channel>.db are not discovered: their channel name is not
// observable from outside the binary.
func getDatabasePath() (string, error) {
	dataDir, err := getDataDir()
	if err != nil {
		return "", err
	}
	override := strings.TrimSpace(os.Getenv(dbEnvVar))
	switch {
	case override == "":
		return filepath.Join(dataDir, defaultDBName), nil
	case override == inMemoryDB:
		return "", fmt.Errorf("%s=%s keeps OpenCode sessions in memory only; there is no database to read", dbEnvVar, inMemoryDB)
	case filepath.IsAbs(override):
		return override, nil
	default:
		return filepath.Join(dataDir, override), nil
	}
}

// openDatabase opens OpenCode's database read-only. The file: scheme is
// required for mode=ro to take effect; the busy timeout rides out the moments
// when OpenCode's service holds a write lock.
func openDatabase(dbPath string) (*sql.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoDatabase
		}
		return nil, fmt.Errorf("failed to access OpenCode database %s: %w", dbPath, err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&"+spi.BusyTimeoutPragma)
	if err != nil {
		return nil, fmt.Errorf("failed to open OpenCode database %s: %w", dbPath, err)
	}
	// One connection is enough (SQLite serializes access anyway) and keeps a
	// long-running watch from accumulating file descriptors.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// withDatabase opens the database, verifies it has the 2.x session schema,
// runs fn, and closes it.
func withDatabase(fn func(db *sql.DB) error) error {
	dbPath, err := getDatabasePath()
	if err != nil {
		return err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("Failed to close OpenCode database", "path", dbPath, "error", closeErr)
		}
	}()
	if err := verifySchema(db); err != nil {
		return fmt.Errorf("%s: %w", dbPath, err)
	}
	return fn(db)
}

// verifySchema confirms the session tables exist. sql.Open is lazy, so this is
// also the first query that proves the file is a readable SQLite database.
func verifySchema(db *sql.DB) error {
	var count int
	err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN (?, ?)`,
		sessionTable, messageTable,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("failed to read OpenCode database schema: %w", err)
	}
	if count != 2 {
		return fmt.Errorf("unsupported OpenCode database: missing %s/%s tables (OpenCode 2.0.14 or newer is required)", sessionTable, messageTable)
	}
	return nil
}

// summaryQuery lists top-level sessions with the counters change detection
// needs. Subagent sessions (parent_id set) belong to their parent's
// conversation, so they are never listed on their own.
//
// The first-prompt subquery picks the first user record with text or an
// attachment, the same record conversion names the session from; a record
// with neither renders nothing. The first user shell command is read too, for
// a session that has only those. json_valid guards json_extract, which would
// otherwise abort the whole listing on one corrupt record.
const summaryQuery = `
SELECT s.id, s.directory, IFNULL(s.title, ''), s.time_created, s.time_updated,
       (SELECT count(*) FROM session_message m WHERE m.session_id = s.id),
       (SELECT IFNULL(max(m.time_updated), 0) FROM session_message m WHERE m.session_id = s.id),
       IFNULL((SELECT m.data FROM session_message m
               WHERE m.session_id = s.id AND m.type = 'user' AND json_valid(m.data)
                 AND (trim(IFNULL(json_extract(m.data, '$.text'), ''), ' ' || char(9, 10, 13)) != ''
                      OR IFNULL(json_array_length(m.data, '$.files'), 0) > 0
                      OR IFNULL(json_array_length(m.data, '$.agents'), 0) > 0)
               ORDER BY m.seq LIMIT 1), ''),
       IFNULL((SELECT m.data FROM session_message m
               WHERE m.session_id = s.id AND m.type = 'shell' AND json_valid(m.data)
                 AND trim(IFNULL(json_extract(m.data, '$.command'), ''), ' ' || char(9, 10, 13)) != ''
               ORDER BY m.seq LIMIT 1), '')
FROM session_v2 s
WHERE s.parent_id IS NULL`

// listSessionSummaries returns the top-level sessions recorded for directory,
// or every top-level session when directory is empty, oldest first.
func listSessionSummaries(db *sql.DB, directory string) ([]sessionSummary, error) {
	query := summaryQuery
	var args []any
	if directory != "" {
		query += " AND s.directory = ?"
		args = append(args, directory)
	}
	query += " ORDER BY s.time_created, s.id"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list OpenCode sessions: %w", err)
	}
	defer func() { _ = rows.Close() }() // read-only cursor; close errors are not actionable

	var summaries []sessionSummary
	for rows.Next() {
		var s sessionSummary
		if err := rows.Scan(&s.ID, &s.Directory, &s.Title, &s.TimeCreated, &s.TimeUpdated,
			&s.MessageCount, &s.LastMessageUpdate, &s.FirstUserData, &s.FirstShellData); err != nil {
			return nil, fmt.Errorf("failed to read OpenCode session row: %w", err)
		}
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to list OpenCode sessions: %w", err)
	}
	return summaries, nil
}

// readSessionSnapshot reads one session and all of its messages inside a
// single read transaction. It returns nil, nil when the session does not exist.
func readSessionSnapshot(db *sql.DB, sessionID string) (*sessionSnapshot, error) {
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("failed to begin OpenCode read transaction: %w", err)
	}
	// A read-only transaction has nothing to commit; rollback just releases it.
	defer func() { _ = tx.Rollback() }()

	sessionRows, err := queryRowMaps(tx, `SELECT * FROM session_v2 WHERE id = ?`, nil, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenCode session %s: %w", sessionID, err)
	}
	if len(sessionRows) == 0 {
		return nil, nil
	}

	// data is the only JSON payload column in session_message; embedding it
	// as raw JSON keeps the export readable instead of double-encoded.
	messageRows, err := queryRowMaps(tx,
		`SELECT * FROM session_message WHERE session_id = ? ORDER BY seq`,
		map[string]bool{"data": true}, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenCode messages for %s: %w", sessionID, err)
	}

	snapshot := &sessionSnapshot{Session: newSessionRecord(sessionRows[0])}
	for _, row := range messageRows {
		snapshot.Messages = append(snapshot.Messages, newMessageRecord(row))
	}
	return snapshot, nil
}

// queryRowMaps runs a query and returns each row as a column-name map, so
// every column (including ones added by a later OpenCode) is carried through.
// Columns named in jsonColumns are embedded as json.RawMessage when they hold
// valid JSON.
func queryRowMaps(tx *sql.Tx, query string, jsonColumns map[string]bool, args ...any) ([]map[string]any, error) {
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }() // read-only cursor; close errors are not actionable

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			value := values[i]
			// The driver returns TEXT as string but BLOB as []byte; either way
			// a string is what the JSON export should show.
			if b, ok := value.([]byte); ok {
				value = string(b)
			}
			if s, ok := value.(string); ok && jsonColumns[column] && json.Valid([]byte(s)) {
				value = json.RawMessage(s)
			}
			row[column] = value
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func newSessionRecord(row map[string]any) sessionRecord {
	return sessionRecord{
		ID:          columnString(row, "id"),
		ParentID:    columnString(row, "parent_id"),
		Directory:   columnString(row, "directory"),
		Title:       columnString(row, "title"),
		Version:     columnString(row, "version"),
		TimeCreated: columnInt(row, "time_created"),
		TimeUpdated: columnInt(row, "time_updated"),
		Row:         row,
	}
}

func newMessageRecord(row map[string]any) messageRecord {
	record := messageRecord{
		ID:          columnString(row, "id"),
		Type:        columnString(row, "type"),
		Seq:         columnInt(row, "seq"),
		TimeCreated: columnInt(row, "time_created"),
		TimeUpdated: columnInt(row, "time_updated"),
		Row:         row,
	}
	switch data := row["data"].(type) {
	case json.RawMessage:
		record.Data = data
	case string:
		// Not valid JSON (queryRowMaps only embeds valid payloads); keep the
		// text so the parser can report the corrupt record.
		record.Data = json.RawMessage(data)
	}
	return record
}

func columnString(row map[string]any, column string) string {
	switch v := row[column].(type) {
	case string:
		return v
	case json.RawMessage:
		return string(v)
	}
	return ""
}

func columnInt(row map[string]any, column string) int64 {
	switch v := row[column].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}
