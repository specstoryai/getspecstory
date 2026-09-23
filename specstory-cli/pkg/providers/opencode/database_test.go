package opencode

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
)

// Recorded directories in the captured fixtures (paths were rewritten to
// neutral ones when the sessions were captured).
const (
	fixtureToolsDir = "/Users/dev/oc-tools"
	fixtureOC1Dir   = "/Users/dev/oc-1"
)

// Captured session ids used across tests.
const (
	toolSessionID     = "ses_f34a354dbffe8waMGOZgKvK6cK"
	subagentSessionID = "ses_f34a2e95bffethLCcvMT6BjIMA"
	tuiSessionID      = "ses_f34a1dc63ffe0cCnMWOTpjumbx"
	optionalSessionID = "ses_f34859f9affeumHNfv8TfefDkV"
	modelErrSessionID = "ses_f34addb6fffeca2UK7PpveGIW3"
	questionSessionID = "ses_f34687c4effe4c1OUSq8A7KEeN"
)

// useFixtureStore points OpenCode's data directory at a fresh temp directory
// and returns the database path the provider will read.
func useFixtureStore(t *testing.T) string {
	t.Helper()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv(dbEnvVar, "")
	return filepath.Join(dataHome, dataDirName, defaultDBName)
}

// createFixtureDB creates dbPath with OpenCode 2.0.14's session tables (DDL
// captured from a real database) and returns a read-write handle for tests
// that write further records.
func createFixtureDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// readFixture loads captured records, rewriting each session's recorded
// directory through dirs (fixture directory -> test directory).
func readFixture(t *testing.T, name string, dirs map[string]string) []rawRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var records []rawRecord
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		var record rawRecord
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if record.Table == sessionTable {
			if dir, ok := record.Row["directory"].(string); ok {
				if mapped, ok := dirs[dir]; ok {
					record.Row["directory"] = mapped
				}
			}
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

// insertRecords writes captured rows into the fixture database, column for
// column, with message payloads stored as JSON text the way OpenCode stores
// them.
func insertRecords(t *testing.T, db *sql.DB, records []rawRecord) {
	t.Helper()
	for _, record := range records {
		columns := make([]string, 0, len(record.Row))
		for column := range record.Row {
			columns = append(columns, column)
		}
		slices.Sort(columns)
		values := make([]any, len(columns))
		for i, column := range columns {
			values[i] = sqlValue(t, column, record.Row[column])
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", record.Table,
			strings.Join(columns, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", "))
		if _, err := db.Exec(query, values...); err != nil {
			t.Fatalf("insert into %s: %v", record.Table, err)
		}
	}
}

func sqlValue(t *testing.T, column string, value any) any {
	t.Helper()
	switch v := value.(type) {
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		f, _ := v.Float64()
		return f
	case map[string]any, []any:
		// Only session_message.data holds a JSON document.
		encoded, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("encode %s: %v", column, err)
		}
		return string(encoded)
	default:
		return v
	}
}

// loadFixtures creates the store and loads the named fixtures into it.
func loadFixtures(t *testing.T, dirs map[string]string, names ...string) *sql.DB {
	t.Helper()
	db := createFixtureDB(t, useFixtureStore(t))
	for _, name := range names {
		insertRecords(t, db, readFixture(t, name, dirs))
	}
	return db
}

// newProjectDir creates a real project directory and returns its canonical
// path, which is how OpenCode records it.
func newProjectDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestGetDatabasePath(t *testing.T) {
	home := t.TempDir()
	dataHome := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "elsewhere.db")

	tests := []struct {
		name     string
		xdg      string
		override string
		want     string
		wantErr  string
	}{
		{name: "default data home", want: filepath.Join(home, ".local", "share", "opencode", "opencode.db")},
		{name: "XDG_DATA_HOME", xdg: dataHome, want: filepath.Join(dataHome, "opencode", "opencode.db")},
		{name: "relative OPENCODE_DB resolves in data dir", xdg: dataHome, override: "custom.db", want: filepath.Join(dataHome, "opencode", "custom.db")},
		{name: "absolute OPENCODE_DB", xdg: dataHome, override: absolute, want: absolute},
		{name: "in-memory OPENCODE_DB has no file", override: ":memory:", wantErr: "in memory only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.SetHome(t, home)
			t.Setenv("XDG_DATA_HOME", tt.xdg)
			t.Setenv(dbEnvVar, tt.override)
			got, err := getDatabasePath()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("getDatabasePath() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("getDatabasePath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("getDatabasePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingDatabaseMeansNoSessions(t *testing.T) {
	useFixtureStore(t)
	project := newProjectDir(t, "project")
	p := NewProvider()

	sessions, err := p.GetAgentChatSessions(project, false, nil)
	if err != nil || len(sessions) != 0 {
		t.Errorf("GetAgentChatSessions() = %d sessions, %v; want none, nil", len(sessions), err)
	}
	listed, err := p.ListAgentChatSessions(project)
	if err != nil || len(listed) != 0 {
		t.Errorf("ListAgentChatSessions() = %d, %v; want none, nil", len(listed), err)
	}
	all, err := p.ListAllAgentChatSessions()
	if err != nil || len(all) != 0 {
		t.Errorf("ListAllAgentChatSessions() = %d, %v; want none, nil", len(all), err)
	}
	session, err := p.GetAgentChatSession(project, toolSessionID, false)
	if err != nil || session != nil {
		t.Errorf("GetAgentChatSession() = %v, %v; want nil, nil", session, err)
	}
}

func TestUnsupportedSchemaIsReported(t *testing.T) {
	dbPath := useFixtureStore(t)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// A database from an OpenCode that predates the session_v2 schema.
	if _, err := db.Exec("CREATE TABLE session (id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = NewProvider().GetAgentChatSessions(newProjectDir(t, "project"), false, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported OpenCode database") {
		t.Fatalf("GetAgentChatSessions() error = %v, want unsupported-schema error", err)
	}
	if errors.Is(err, errNoDatabase) {
		t.Error("an unsupported database must not read as a missing one")
	}
}

// insertSession writes a minimal top-level session_v2 row.
func insertSession(t *testing.T, db *sql.DB, id, directory string, created, updated int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO session_v2 (id, project_id, slug, directory, version, time_created, time_updated)
		VALUES (?, 'test-project', 'test-slug', ?, '2.0.14', ?, ?)`, id, directory, created, updated)
	if err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
}

// insertMessage writes one session_message row with data stored verbatim.
func insertMessage(t *testing.T, db *sql.DB, sessionID, id, recordType string, seq, created int64, data string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, sessionID, recordType, seq, created, created, data)
	if err != nil {
		t.Fatalf("insert message %s: %v", id, err)
	}
}

// userData is a minimal user record payload.
func userData(created int64, text string) string {
	encoded, _ := json.Marshal(map[string]any{"time": map[string]any{"created": created}, "text": text})
	return string(encoded)
}

// assistantData is a minimal assistant record payload with one text part.
func assistantData(created int64, text string) string {
	encoded, _ := json.Marshal(map[string]any{
		"time":    map[string]any{"created": created, "completed": created},
		"agent":   "build",
		"model":   map[string]any{"id": "big-pickle", "providerID": "opencode"},
		"content": []any{map[string]any{"type": "text", "text": text}},
		"finish":  "stop",
	})
	return string(encoded)
}
