package cursoride

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// createTestGlobalDB builds a minimal global state.vscdb in a temp directory with
// both cursorDiskKV and ItemTable tables, matching the structure of the real Cursor
// global database. kvPairs seeds the cursorDiskKV table. The driver is registered by
// database.go's blank import, so no re-import is needed here.
func createTestGlobalDB(t *testing.T, kvPairs map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("createTestGlobalDB: open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		_ = db.Close()
		t.Fatalf("createTestGlobalDB: create cursorDiskKV: %v", err)
	}
	// ItemTable is the VSCode/Cursor key-value store for non-bubble data, including
	// composer.composerHeaders which WriteGlobalComposerHeader reads and writes.
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		_ = db.Close()
		t.Fatalf("createTestGlobalDB: create ItemTable: %v", err)
	}
	// composerHeaders mirrors the dedicated header table Cursor >= 3.12 creates
	// (composer.composerHeaders.tableGateEnabled) and reads the sidebar from.
	if _, err := db.Exec(`CREATE TABLE composerHeaders (composerId TEXT PRIMARY KEY, workspaceId TEXT,
		createdAt INTEGER, lastUpdatedAt INTEGER, isArchived INTEGER, isSubagent INTEGER,
		recency INTEGER, checkpointAt INTEGER, value TEXT)`); err != nil {
		_ = db.Close()
		t.Fatalf("createTestGlobalDB: create composerHeaders: %v", err)
	}
	for k, v := range kvPairs {
		if _, err := db.Exec("INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)", k, v); err != nil {
			_ = db.Close()
			t.Fatalf("createTestGlobalDB: insert %q: %v", k, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("createTestGlobalDB: close: %v", err)
	}
	return path
}

func TestLoadAllComposerDataLightweight(t *testing.T) {
	comp1, _ := json.Marshal(ComposerData{
		ComposerID: "aaa",
		Name:       "Fix the login bug",
		CreatedAt:  1700000000000,
		FullConversationHeadersOnly: []ComposerConversationHeader{
			{BubbleID: "b1"},
		},
	})
	comp2, _ := json.Marshal(ComposerData{
		ComposerID: "bbb",
		Name:       "Refactor auth module",
		CreatedAt:  1700000001000,
		FullConversationHeadersOnly: []ComposerConversationHeader{
			{BubbleID: "b2"},
		},
	})

	dbPath := createTestGlobalDB(t, map[string]string{
		"composerData:aaa": string(comp1),
		"composerData:bbb": string(comp2),
		// bubble and unrelated keys must be ignored
		"bubbleId:aaa:b1": `{"bubbleId":"b1","type":1,"text":"hello"}`,
		"settings:theme":  `{"dark":true}`,
	})

	composers, err := LoadAllComposerDataLightweight(dbPath)
	if err != nil {
		t.Fatalf("LoadAllComposerDataLightweight: %v", err)
	}
	if len(composers) != 2 {
		t.Fatalf("expected 2 composers, got %d", len(composers))
	}

	c1, ok := composers["aaa"]
	if !ok {
		t.Fatal("expected composer 'aaa'")
	}
	if c1.Name != "Fix the login bug" {
		t.Errorf("composer aaa name = %q, want %q", c1.Name, "Fix the login bug")
	}
	if c1.CreatedAt != 1700000000000 {
		t.Errorf("composer aaa createdAt = %d, want 1700000000000", c1.CreatedAt)
	}
	if len(c1.FullConversationHeadersOnly) != 1 || c1.FullConversationHeadersOnly[0].BubbleID != "b1" {
		t.Errorf("composer aaa headers = %v, want [{BubbleID:b1}]", c1.FullConversationHeadersOnly)
	}

	if _, ok := composers["bbb"]; !ok {
		t.Error("expected composer 'bbb'")
	}
}

func TestLoadAllComposerDataLightweight_EmptyDB(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	composers, err := LoadAllComposerDataLightweight(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(composers) != 0 {
		t.Errorf("expected 0 composers, got %d", len(composers))
	}
}

func TestLoadAllComposerDataLightweight_MissingDB(t *testing.T) {
	_, err := LoadAllComposerDataLightweight(filepath.Join(t.TempDir(), "nonexistent.db"))
	if err == nil {
		t.Error("expected error for nonexistent database")
	}
}

// TestLoadAllComposerDataLightweight_MalformedJSON verifies that a single malformed row
// is skipped without aborting the whole load, so one corrupt entry doesn't hide all others.
func TestLoadAllComposerDataLightweight_MalformedJSON(t *testing.T) {
	comp, _ := json.Marshal(ComposerData{
		ComposerID: "valid",
		Name:       "Valid composer",
		FullConversationHeadersOnly: []ComposerConversationHeader{
			{BubbleID: "b1"},
		},
	})
	dbPath := createTestGlobalDB(t, map[string]string{
		"composerData:bad":   `{ not valid json`,
		"composerData:valid": string(comp),
	})

	composers, err := LoadAllComposerDataLightweight(dbPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(composers) != 1 {
		t.Fatalf("expected 1 composer (bad row skipped), got %d", len(composers))
	}
	if _, ok := composers["valid"]; !ok {
		t.Error("expected 'valid' composer to be present")
	}
}

// readComposerHeaders reads the allComposers slice from composer.composerHeaders in ItemTable.
func readComposerHeaders(t *testing.T, dbPath string) []map[string]interface{} {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("readComposerHeaders: open: %v", err)
	}
	defer func() { _ = db.Close() }()
	var raw string
	if err := db.QueryRow("SELECT value FROM ItemTable WHERE key='composer.composerHeaders'").Scan(&raw); err != nil {
		t.Fatalf("readComposerHeaders: query: %v", err)
	}
	var blob map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		t.Fatalf("readComposerHeaders: unmarshal blob: %v", err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(blob["allComposers"], &entries); err != nil {
		t.Fatalf("readComposerHeaders: unmarshal allComposers: %v", err)
	}
	return entries
}

// TestWriteGlobalComposerHeader_WritesEntry verifies the function adds a properly-formed
// head entry to composer.composerHeaders in the global ItemTable.
func TestWriteGlobalComposerHeader_WritesEntry(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	nowMs := time.Now().UnixMilli()
	meta := ComposerHeadMeta{
		ComposerID:    "test-composer-abc",
		Name:          "Fix the login bug",
		CreatedAt:     nowMs,
		LastUpdatedAt: nowMs + 1000,
		WorkspaceID:   "ws-hash-123",
	}
	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/proj"); err != nil {
		t.Fatalf("WriteGlobalComposerHeader: %v", err)
	}

	entries := readComposerHeaders(t, dbPath)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in allComposers, got %d", len(entries))
	}
	e := entries[0]

	// Required fields for sidebar visibility.
	if e["type"] != "head" {
		t.Errorf("type = %v, want %q", e["type"], "head")
	}
	if e["composerId"] != "test-composer-abc" {
		t.Errorf("composerId = %v, want %q", e["composerId"], "test-composer-abc")
	}
	if e["name"] != "Fix the login bug" {
		t.Errorf("name = %v, want %q", e["name"], "Fix the login bug")
	}
	if e["isDraft"] != false {
		t.Errorf("isDraft = %v, want false", e["isDraft"])
	}
	if e["isArchived"] != false {
		t.Errorf("isArchived = %v, want false", e["isArchived"])
	}
	if e["isSpec"] != false {
		t.Errorf("isSpec = %v, want false", e["isSpec"])
	}
	if e["unifiedMode"] != "agent" {
		t.Errorf("unifiedMode = %v, want %q", e["unifiedMode"], "agent")
	}
	// workspaceIdentifier must carry the id and uri so Cursor can route to the right project.
	wi, ok := e["workspaceIdentifier"].(map[string]interface{})
	if !ok {
		t.Fatalf("workspaceIdentifier missing or wrong type: %T %v", e["workspaceIdentifier"], e["workspaceIdentifier"])
	}
	if wi["id"] != "ws-hash-123" {
		t.Errorf("workspaceIdentifier.id = %v, want %q", wi["id"], "ws-hash-123")
	}
	uri, _ := wi["uri"].(map[string]interface{})
	if uri["fsPath"] != "/tmp/proj" {
		t.Errorf("workspaceIdentifier.uri.fsPath = %v, want %q", uri["fsPath"], "/tmp/proj")
	}

	// The composerHeaders TABLE row must exist too — Cursor >= 3.12 builds the sidebar
	// from the table, not the JSON blob.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open for table check: %v", err)
	}
	defer func() { _ = db.Close() }()
	var wsID string
	var isArchived, isSubagent int
	var value string
	row := db.QueryRow("SELECT workspaceId, isArchived, isSubagent, value FROM composerHeaders WHERE composerId=?", "test-composer-abc")
	if err := row.Scan(&wsID, &isArchived, &isSubagent, &value); err != nil {
		t.Fatalf("composerHeaders table row missing: %v", err)
	}
	if wsID != "ws-hash-123" || isArchived != 0 || isSubagent != 0 {
		t.Errorf("composerHeaders row = (ws %q, archived %d, subagent %d), want (ws-hash-123, 0, 0)", wsID, isArchived, isSubagent)
	}
	if value == "" {
		t.Error("composerHeaders row has empty head JSON value")
	}
}

// TestWriteGlobalComposerHeader_NoTable verifies pre-3.12 databases (no composerHeaders
// table) are tolerated: the JSON blob is written and the table insert is skipped.
func TestWriteGlobalComposerHeader_NoTable(t *testing.T) {
	// Minimal DB with only ItemTable — mirrors a pre-3.12 global database.
	path := filepath.Join(t.TempDir(), "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE ItemTable (key TEXT PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create ItemTable: %v", err)
	}
	_ = db.Close()

	meta := ComposerHeadMeta{ComposerID: "c1", Name: "S", CreatedAt: 1, LastUpdatedAt: 2, WorkspaceID: "ws"}
	if err := WriteGlobalComposerHeader(path, meta, "/tmp/p"); err != nil {
		t.Fatalf("WriteGlobalComposerHeader without table: %v", err)
	}
	if entries := readComposerHeaders(t, path); len(entries) != 1 {
		t.Errorf("expected 1 blob entry, got %d", len(entries))
	}
}

// composerHeaderRow is a row of the composerHeaders SQL index, the source the Agent
// sidebar lists sessions from.
type composerHeaderRow struct {
	workspaceID   string
	createdAt     int64
	lastUpdatedAt int64
	recency       int64
	isArchived    int64
	isSubagent    int64
	value         string
}

// readComposerHeaderRows reads the composerHeaders SQL table keyed by composerId.
func readComposerHeaderRows(t *testing.T, dbPath string) map[string]composerHeaderRow {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("readComposerHeaderRows: open: %v", err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT composerId, workspaceId, createdAt, lastUpdatedAt,
		isArchived, isSubagent, recency, value FROM composerHeaders`)
	if err != nil {
		t.Fatalf("readComposerHeaderRows: query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]composerHeaderRow)
	for rows.Next() {
		var id string
		var r composerHeaderRow
		if err := rows.Scan(&id, &r.workspaceID, &r.createdAt, &r.lastUpdatedAt,
			&r.isArchived, &r.isSubagent, &r.recency, &r.value); err != nil {
			t.Fatalf("readComposerHeaderRows: scan: %v", err)
		}
		out[id] = r
	}
	return out
}

// TestWriteGlobalComposerHeader_WritesSQLRow verifies the session is registered in the
// composerHeaders SQL table. Without this row the Agent sidebar cannot list the session,
// even though it loads correctly when opened directly by ID.
func TestWriteGlobalComposerHeader_WritesSQLRow(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	nowMs := time.Now().UnixMilli()
	meta := ComposerHeadMeta{
		ComposerID:    "sql-row-abc",
		Name:          "Fix the login bug",
		CreatedAt:     nowMs,
		LastUpdatedAt: nowMs + 1000,
		WorkspaceID:   "ws-hash-123",
	}
	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/proj"); err != nil {
		t.Fatalf("WriteGlobalComposerHeader: %v", err)
	}

	rows := readComposerHeaderRows(t, dbPath)
	row, ok := rows["sql-row-abc"]
	if !ok {
		t.Fatalf("no composerHeaders row for the session; got rows for %v", rows)
	}
	// workspaceId is the column the sidebar filters on, so a wrong value hides the session.
	if row.workspaceID != "ws-hash-123" {
		t.Errorf("workspaceId = %q, want %q", row.workspaceID, "ws-hash-123")
	}
	if row.createdAt != nowMs {
		t.Errorf("createdAt = %d, want %d", row.createdAt, nowMs)
	}
	if row.lastUpdatedAt != nowMs+1000 {
		t.Errorf("lastUpdatedAt = %d, want %d", row.lastUpdatedAt, nowMs+1000)
	}
	// recency drives sidebar ordering; Cursor sets it to lastUpdatedAt for an unopened session.
	if row.recency != nowMs+1000 {
		t.Errorf("recency = %d, want %d", row.recency, nowMs+1000)
	}
	if row.isArchived != 0 || row.isSubagent != 0 {
		t.Errorf("isArchived = %d, isSubagent = %d, want 0, 0", row.isArchived, row.isSubagent)
	}
	// The row's value must be the same head entry stored in the JSON index.
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(row.value), &v); err != nil {
		t.Fatalf("value is not valid JSON: %v", err)
	}
	if v["name"] != "Fix the login bug" {
		t.Errorf("value.name = %v, want %q", v["name"], "Fix the login bug")
	}
	if v["composerId"] != "sql-row-abc" {
		t.Errorf("value.composerId = %v, want %q", v["composerId"], "sql-row-abc")
	}
}

// TestWriteGlobalComposerHeader_BackfillsSQLRow guards the regression that made an imported
// session invisible: when the JSON entry already exists, the write must still upsert the SQL
// row rather than returning early on the duplicate.
func TestWriteGlobalComposerHeader_BackfillsSQLRow(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	nowMs := time.Now().UnixMilli()
	meta := ComposerHeadMeta{
		ComposerID:    "backfill-id",
		Name:          "Resumed session",
		CreatedAt:     nowMs,
		LastUpdatedAt: nowMs,
		WorkspaceID:   "ws-backfill",
	}

	// Seed only the JSON index, mimicking a session written before the SQL row was written.
	existing, _ := json.Marshal(map[string]interface{}{
		"type": "head", "composerId": "backfill-id", "name": "Resumed session",
	})
	initial, _ := json.Marshal(map[string]interface{}{
		"allComposers": []interface{}{json.RawMessage(existing)},
	})
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("INSERT INTO ItemTable (key, value) VALUES ('composer.composerHeaders', ?)",
		string(initial)); err != nil {
		t.Fatalf("seed ItemTable: %v", err)
	}
	_ = db.Close()

	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/p"); err != nil {
		t.Fatalf("WriteGlobalComposerHeader: %v", err)
	}

	if _, ok := readComposerHeaderRows(t, dbPath)["backfill-id"]; !ok {
		t.Error("SQL row missing: a duplicate JSON entry must not skip the composerHeaders upsert")
	}
	// The JSON entry is rewritten in place, not duplicated.
	if entries := readComposerHeaders(t, dbPath); len(entries) != 1 {
		t.Errorf("expected 1 JSON entry, got %d", len(entries))
	}
}

// TestWriteGlobalComposerHeader_NoComposerHeadersTable verifies that a global database
// without the SQL index (a Cursor version that uses the JSON index alone) still gets its
// JSON entry and does not fail the resume.
func TestWriteGlobalComposerHeader_NoComposerHeadersTable(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("DROP TABLE composerHeaders"); err != nil {
		t.Fatalf("drop composerHeaders: %v", err)
	}
	_ = db.Close()

	meta := ComposerHeadMeta{
		ComposerID:    "no-table-id",
		Name:          "Session",
		CreatedAt:     1700000000000,
		LastUpdatedAt: 1700000000000,
		WorkspaceID:   "ws-x",
	}
	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/p"); err != nil {
		t.Fatalf("want success when composerHeaders table is absent, got: %v", err)
	}
	if entries := readComposerHeaders(t, dbPath); len(entries) != 1 {
		t.Errorf("expected the JSON entry to still be written, got %d entries", len(entries))
	}
}

// TestWriteGlobalComposerHeader_Idempotent verifies a second call with the same composerId
// is a no-op (the entry is not duplicated).
func TestWriteGlobalComposerHeader_Idempotent(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	nowMs := time.Now().UnixMilli()
	meta := ComposerHeadMeta{
		ComposerID:    "test-composer-dup",
		Name:          "Session",
		CreatedAt:     nowMs,
		LastUpdatedAt: nowMs,
		WorkspaceID:   "ws-abc",
	}

	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/p"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/p"); err != nil {
		t.Fatalf("second write: %v", err)
	}

	entries := readComposerHeaders(t, dbPath)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after two writes of same ID, got %d", len(entries))
	}
}

// TestWriteGlobalComposerHeader_PreservesExisting verifies that writing a new session
// prepends it to allComposers without dropping existing entries.
func TestWriteGlobalComposerHeader_PreservesExisting(t *testing.T) {
	existing, _ := json.Marshal(map[string]interface{}{
		"type": "head", "composerId": "existing-id", "name": "Old session",
	})
	initial, _ := json.Marshal(map[string]interface{}{
		"allComposers": []interface{}{json.RawMessage(existing)},
	})
	dbPath := createTestGlobalDB(t, map[string]string{})

	// Seed ItemTable with an existing header.
	db, _ := sql.Open("sqlite", dbPath)
	_, _ = db.Exec("INSERT INTO ItemTable (key, value) VALUES ('composer.composerHeaders', ?)", string(initial))
	_ = db.Close()

	nowMs := time.Now().UnixMilli()
	meta := ComposerHeadMeta{
		ComposerID:    "new-id",
		Name:          "New session",
		CreatedAt:     nowMs,
		LastUpdatedAt: nowMs,
		WorkspaceID:   "ws-x",
	}
	if err := WriteGlobalComposerHeader(dbPath, meta, "/tmp/q"); err != nil {
		t.Fatalf("WriteGlobalComposerHeader: %v", err)
	}

	entries := readComposerHeaders(t, dbPath)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// New entry should be first (prepended for recency ordering).
	if entries[0]["composerId"] != "new-id" {
		t.Errorf("first entry composerId = %v, want %q", entries[0]["composerId"], "new-id")
	}
	if entries[1]["composerId"] != "existing-id" {
		t.Errorf("second entry composerId = %v, want %q", entries[1]["composerId"], "existing-id")
	}
}

// readGlassState returns the parsed glass projects list and membership map from ItemTable.
func readGlassState(t *testing.T, dbPath string) ([]map[string]interface{}, map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("readGlassState: open: %v", err)
	}
	defer func() { _ = db.Close() }()
	read := func(key string) string {
		var v string
		err := db.QueryRow("SELECT value FROM ItemTable WHERE key=?", key).Scan(&v)
		if err == sql.ErrNoRows {
			return ""
		}
		if err != nil {
			t.Fatalf("readGlassState: query %s: %v", key, err)
		}
		return v
	}
	var projects []map[string]interface{}
	if raw := read(glassProjectsKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &projects); err != nil {
			t.Fatalf("readGlassState: unmarshal projects: %v", err)
		}
	}
	membership := map[string]string{}
	if raw := read(glassMembershipKey); raw != "" {
		if err := json.Unmarshal([]byte(raw), &membership); err != nil {
			t.Fatalf("readGlassState: unmarshal membership: %v", err)
		}
	}
	return projects, membership
}

// TestRegisterGlassProjectMembership_CreatesProject verifies that registering a composer
// in a database with no glass state creates both the project entry (with the workspace
// association Cursor needs) and the membership mapping.
func TestRegisterGlassProjectMembership_CreatesProject(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})

	if err := RegisterGlassProjectMembership(dbPath, "comp-1", "ws-hash-1", "/tmp/proj"); err != nil {
		t.Fatalf("RegisterGlassProjectMembership: %v", err)
	}

	projects, membership := readGlassState(t, dbPath)
	if len(projects) != 1 {
		t.Fatalf("expected 1 glass project, got %d", len(projects))
	}
	ws, _ := projects[0]["workspace"].(map[string]interface{})
	if ws["id"] != "ws-hash-1" {
		t.Errorf("project workspace id = %v, want ws-hash-1", ws["id"])
	}
	projectID, _ := projects[0]["id"].(string)
	if projectID == "" {
		t.Fatal("created project has empty id")
	}
	if membership["comp-1"] != projectID {
		t.Errorf("membership[comp-1] = %q, want %q", membership["comp-1"], projectID)
	}
}

// TestRegisterGlassProjectMembership_ReusesProject verifies that an existing project
// entry for the workspace is reused (no duplicate project) and that existing membership
// entries survive the rewrite.
func TestRegisterGlassProjectMembership_ReusesProject(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})

	// First composer creates the project; second must reuse it.
	if err := RegisterGlassProjectMembership(dbPath, "comp-1", "ws-hash-1", "/tmp/proj"); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := RegisterGlassProjectMembership(dbPath, "comp-2", "ws-hash-1", "/tmp/proj"); err != nil {
		t.Fatalf("second registration: %v", err)
	}

	projects, membership := readGlassState(t, dbPath)
	if len(projects) != 1 {
		t.Fatalf("expected project to be reused (1 entry), got %d", len(projects))
	}
	projectID, _ := projects[0]["id"].(string)
	if membership["comp-1"] != projectID || membership["comp-2"] != projectID {
		t.Errorf("both composers should map to project %q, got comp-1=%q comp-2=%q",
			projectID, membership["comp-1"], membership["comp-2"])
	}
}

// pagedComposerJSON builds a Cursor >= 3.27.14 composerData row: _v 18 with an empty
// inline header list, the shape that moved the ordered headers into composerConvHeaders.
func pagedComposerJSON(t *testing.T, id, name string) string {
	t.Helper()
	raw, err := json.Marshal(ComposerData{
		ComposerID:                  id,
		Name:                        name,
		Version:                     18,
		CreatedAt:                   1793105192455,
		LastUpdatedAt:               1793105239300,
		FullConversationHeadersOnly: []ComposerConversationHeader{},
	})
	if err != nil {
		t.Fatalf("marshal composer: %v", err)
	}
	return string(raw)
}

func bubbleJSON(t *testing.T, id string, bubbleType int, text string) string {
	t.Helper()
	raw, err := json.Marshal(ComposerConversation{BubbleID: id, Type: bubbleType, Text: text})
	if err != nil {
		t.Fatalf("marshal bubble: %v", err)
	}
	return string(raw)
}

func conversationIDs(conv []ComposerConversation) []string {
	ids := make([]string, len(conv))
	for i, c := range conv {
		ids[i] = c.BubbleID
	}
	return ids
}

func TestParseConvHeadersKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantID     string
		wantSuffix string
		wantParsed bool
	}{
		{"first page", "composerConvHeaders:abc:000000", "abc", "000000", true},
		{"later page", "composerConvHeaders:abc:000012", "abc", "000012", true},
		{"missing suffix separator", "composerConvHeaders:abc", "", "", false},
		{"empty composer ID", "composerConvHeaders::000000", "", "", false},
		{"empty suffix", "composerConvHeaders:abc:", "", "", false},
		{"other key type", "bubbleId:abc:b1", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, suffix, ok := parseConvHeadersKey(tt.key)
			if ok != tt.wantParsed || id != tt.wantID || suffix != tt.wantSuffix {
				t.Errorf("parseConvHeadersKey(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.key, id, suffix, ok, tt.wantID, tt.wantSuffix, tt.wantParsed)
			}
		})
	}
}

// Cursor 3.27.14: headers are paged into composerConvHeaders rows and the inline list is
// empty. The loader must concatenate the pages in key order (even when the rows are
// inserted out of order) and resolve suffixed bubble IDs such as "<uuid>/s:0" verbatim.
func TestLoadComposerDataBatch_PagedHeaders(t *testing.T) {
	const id = "396cc8c0-346d-4098-8e57-d15758197093"
	dbPath := createTestGlobalDB(t, map[string]string{
		"composerData:" + id: pagedComposerJSON(t, id, "CLI integration in monorepo"),
		// Listed out of order, but map iteration and SQLite row order are both
		// unspecified, so this alone cannot pin the ordering requirement; see
		// TestMergeHeaderPages for the deterministic check.
		"composerConvHeaders:" + id + ":000001": `[{"bubbleId":"u2","type":1,"createdAt":"2026-10-27T12:48:00.000Z"},{"bubbleId":"u2/s:0","type":2}]`,
		"composerConvHeaders:" + id + ":000000": `[{"bubbleId":"u1","type":1,"grouping":{"isRenderable":true,"hasText":true}},{"bubbleId":"u1/s:0","type":2},{"bubbleId":"u1/s:1","type":2}]`,
		"bubbleId:" + id + ":u1":                bubbleJSON(t, "u1", 1, "first question"),
		"bubbleId:" + id + ":u1/s:0":            bubbleJSON(t, "u1/s:0", 2, "thinking"),
		"bubbleId:" + id + ":u1/s:1":            bubbleJSON(t, "u1/s:1", 2, "first answer"),
		"bubbleId:" + id + ":u2":                bubbleJSON(t, "u2", 1, "second question"),
		"bubbleId:" + id + ":u2/s:0":            bubbleJSON(t, "u2/s:0", 2, "second answer"),
		// Another composer's page must not leak in.
		"composerConvHeaders:other:000000": `[{"bubbleId":"x","type":1}]`,
	})

	composers, err := LoadComposerDataBatch(dbPath, []string{id})
	if err != nil {
		t.Fatalf("LoadComposerDataBatch: %v", err)
	}
	composer, ok := composers[id]
	if !ok {
		t.Fatalf("composer %s not loaded", id)
	}
	if got := len(composer.FullConversationHeadersOnly); got != 5 {
		t.Fatalf("FullConversationHeadersOnly: got %d headers, want 5", got)
	}
	if composer.FullConversationHeadersOnly[0].Grouping == nil || !composer.FullConversationHeadersOnly[0].Grouping.IsRenderable {
		t.Errorf("header grouping hints should survive the page parse")
	}
	want := []string{"u1", "u1/s:0", "u1/s:1", "u2", "u2/s:0"}
	if got := conversationIDs(composer.Conversation); !slices.Equal(got, want) {
		t.Fatalf("Conversation order: got %v, want %v", got, want)
	}
	if composer.Conversation[4].Text != "second answer" {
		t.Errorf("bubble content not merged: got %q", composer.Conversation[4].Text)
	}
}

// The paging fallback must only kick in when the inline header list is empty, and must
// not disturb the other header sources or hide gaps the watcher relies on.
func TestLoadComposerDataBatch_HeaderSources(t *testing.T) {
	inlineRaw, err := json.Marshal(ComposerData{
		ComposerID: "inline",
		Version:    17,
		FullConversationHeadersOnly: []ComposerConversationHeader{
			{BubbleID: "b1", Type: 1},
		},
	})
	if err != nil {
		t.Fatalf("marshal composer: %v", err)
	}

	tests := []struct {
		name             string
		id               string
		rows             map[string]string
		wantHeaders      int
		wantConversation []string
		wantLastText     string
	}{
		{
			// Pre-3.27.14 composers still carry the inline list; it must win over any
			// page so existing exports are unchanged.
			name: "inline headers win over pages",
			id:   "inline",
			rows: map[string]string{
				"composerData:inline":               string(inlineRaw),
				"composerConvHeaders:inline:000000": `[{"bubbleId":"b1","type":1},{"bubbleId":"b2","type":2}]`,
				"bubbleId:inline:b1":                bubbleJSON(t, "b1", 1, "hi"),
				"bubbleId:inline:b2":                bubbleJSON(t, "b2", 2, "hello"),
			},
			wantHeaders:      1,
			wantConversation: []string{"b1"},
			wantLastText:     "hi",
		},
		{
			// A page that references a bubble Cursor hasn't committed yet must leave the
			// gap visible (headers > loaded bubbles) so the watcher's incomplete-data
			// guard retries.
			name: "missing bubble leaves the gap visible",
			id:   "racing",
			rows: map[string]string{
				"composerData:racing":               pagedComposerJSON(t, "racing", "mid-stream"),
				"composerConvHeaders:racing:000000": `[{"bubbleId":"u1","type":1},{"bubbleId":"u1/s:0","type":2},{"bubbleId":"u1/s:1","type":2}]`,
				"bubbleId:racing:u1":                bubbleJSON(t, "u1", 1, "question"),
				"bubbleId:racing:u1/s:0":            bubbleJSON(t, "u1/s:0", 2, "partial"),
			},
			wantHeaders:      3,
			wantConversation: []string{"u1", "u1/s:0"},
			wantLastText:     "partial",
		},
		{
			// Cursor 2 rows embed the conversation inline with no header list and no
			// pages; the inline-merge branch must be untouched by the paging fallback.
			name: "legacy inline conversation unchanged",
			id:   "legacy",
			rows: map[string]string{
				"composerData:legacy": `{"composerId":"legacy","_v":1,"conversation":[{"bubbleId":"b1","type":1,"text":""},{"bubbleId":"b2","type":2,"text":""}]}`,
				"bubbleId:legacy:b1":  bubbleJSON(t, "b1", 1, "full user text"),
				"bubbleId:legacy:b2":  bubbleJSON(t, "b2", 2, "full assistant text"),
			},
			wantHeaders:      0,
			wantConversation: []string{"b1", "b2"},
			wantLastText:     "full assistant text",
		},
		{
			// No inline headers, no pages and no bubbles is a genuinely empty composer.
			name: "empty composer stays empty",
			id:   "draft",
			rows: map[string]string{
				"composerData:draft": pagedComposerJSON(t, "draft", ""),
			},
			wantHeaders:      0,
			wantConversation: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := createTestGlobalDB(t, tt.rows)
			composers, err := LoadComposerDataBatch(dbPath, []string{tt.id})
			if err != nil {
				t.Fatalf("LoadComposerDataBatch: %v", err)
			}
			c, ok := composers[tt.id]
			if !ok {
				t.Fatalf("composer %s not loaded", tt.id)
			}
			if got := len(c.FullConversationHeadersOnly); got != tt.wantHeaders {
				t.Errorf("headers: got %d, want %d", got, tt.wantHeaders)
			}
			got := conversationIDs(c.Conversation)
			if !slices.Equal(got, tt.wantConversation) {
				t.Fatalf("conversation: got %v, want %v", got, tt.wantConversation)
			}
			if len(got) > 0 && c.Conversation[len(got)-1].Text != tt.wantLastText {
				t.Errorf("last bubble text: got %q, want %q", c.Conversation[len(got)-1].Text, tt.wantLastText)
			}
		})
	}
}

// Each composer adds an IN entry and two LIKE conditions to a chunk's query, so a full
// chunk is the largest query SQLite has to accept; anything past it must spill into a
// second chunk whose rows merge with the first. Projects with that many chats are where
// a query over SQLite's expression-depth limit would break sync and watch outright.
func TestLoadComposerDataBatch_ChunkBoundary(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "one full chunk", count: composerBatchSize},
		{name: "spills into a second chunk", count: composerBatchSize + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := make(map[string]string)
			ids := make([]string, tt.count)
			for i := range tt.count {
				// Unpadded IDs (c1, c10, c100) are prefixes of one another, so a key that
				// matched the wrong composer would surface as a mismatch below.
				id := fmt.Sprintf("c%d", i)
				ids[i] = id
				question, answer := id+"-q", id+"-a"
				rows["bubbleId:"+id+":"+question] = bubbleJSON(t, question, 1, "question "+id)
				rows["bubbleId:"+id+":"+answer] = bubbleJSON(t, answer, 2, "answer "+id)
				if i%2 == 0 {
					// Cursor >= 3.27.14: headers paged, one bubble per page.
					rows["composerData:"+id] = pagedComposerJSON(t, id, id)
					rows["composerConvHeaders:"+id+":000000"] = fmt.Sprintf(`[{"bubbleId":%q,"type":1}]`, question)
					rows["composerConvHeaders:"+id+":000001"] = fmt.Sprintf(`[{"bubbleId":%q,"type":2}]`, answer)
				} else {
					// Cursor < 3.27.14: headers inline in the composerData row.
					raw, err := json.Marshal(ComposerData{
						ComposerID: id,
						Version:    17,
						FullConversationHeadersOnly: []ComposerConversationHeader{
							{BubbleID: question, Type: 1},
							{BubbleID: answer, Type: 2},
						},
					})
					if err != nil {
						t.Fatalf("marshal composer: %v", err)
					}
					rows["composerData:"+id] = string(raw)
				}
			}
			dbPath := createTestGlobalDB(t, rows)

			composers, err := LoadComposerDataBatch(dbPath, ids)
			if err != nil {
				t.Fatalf("LoadComposerDataBatch: %v", err)
			}
			if len(composers) != tt.count {
				t.Fatalf("loaded %d composers, want %d", len(composers), tt.count)
			}
			for _, id := range ids {
				c := composers[id]
				if c == nil {
					t.Fatalf("composer %s not loaded", id)
				}
				want := []string{id + "-q", id + "-a"}
				if got := conversationIDs(c.Conversation); !slices.Equal(got, want) {
					t.Fatalf("composer %s conversation: got %v, want %v", id, got, want)
				}
				if c.Conversation[1].Text != "answer "+id {
					t.Errorf("composer %s answer text: got %q", id, c.Conversation[1].Text)
				}
			}
		})
	}
}

// ListAllAgentChatSessions uses the header list to tell an empty composer from a
// populated one, so its loader must see the paged headers too. The lightweight loader
// must not, because it runs on every watcher check.
func TestLoadAllComposerDataWithHeaders(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{
		"composerData:paged":                pagedComposerJSON(t, "paged", "new chat"),
		"composerConvHeaders:paged:000000":  `[{"bubbleId":"u1","type":1},{"bubbleId":"u1/s:0","type":2}]`,
		"composerConvHeaders:paged:000001":  `[{"bubbleId":"u2","type":1}]`,
		"composerData:draft":                pagedComposerJSON(t, "draft", ""),
		"composerConvHeaders:orphan:000000": `[{"bubbleId":"z","type":1}]`,
		"composerConvHeaders:paged:bad":     `not json`,
	})

	composers, err := LoadAllComposerDataWithHeaders(dbPath)
	if err != nil {
		t.Fatalf("LoadAllComposerDataWithHeaders: %v", err)
	}
	if got := len(composers["paged"].FullConversationHeadersOnly); got != 3 {
		t.Errorf("paged composer: got %d headers, want 3", got)
	}
	if got := len(composers["draft"].FullConversationHeadersOnly); got != 0 {
		t.Errorf("draft composer: got %d headers, want 0", got)
	}
	if _, exists := composers["orphan"]; exists {
		t.Errorf("a page without a composerData row must not create a composer")
	}

	// The lightweight loader backs every watcher check, so it must stay metadata-only.
	light, err := LoadAllComposerDataLightweight(dbPath)
	if err != nil {
		t.Fatalf("LoadAllComposerDataLightweight: %v", err)
	}
	if got := len(light["paged"].FullConversationHeadersOnly); got != 0 {
		t.Errorf("lightweight loader: got %d headers, want 0 (pages must not be read)", got)
	}
}

// mergeHeaderPages must concatenate pages by their zero-padded suffix regardless of
// the order the rows were scanned in, and must leave an inline header list alone.
func TestMergeHeaderPages(t *testing.T) {
	page := func(suffix string, ids ...string) headerPage {
		headers := make([]ComposerConversationHeader, len(ids))
		for i, id := range ids {
			headers[i] = ComposerConversationHeader{BubbleID: id}
		}
		return headerPage{suffix: suffix, headers: headers}
	}
	headerIDs := func(headers []ComposerConversationHeader) []string {
		ids := make([]string, len(headers))
		for i, h := range headers {
			ids[i] = h.BubbleID
		}
		return ids
	}

	tests := []struct {
		name   string
		inline []ComposerConversationHeader
		pages  []headerPage
		want   []string
	}{
		{
			name:  "pages scanned in order",
			pages: []headerPage{page("000000", "a", "b"), page("000001", "c")},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "pages scanned in reverse",
			pages: []headerPage{page("000001", "c"), page("000000", "a", "b")},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "pages scanned shuffled, suffix order not insertion order",
			pages: []headerPage{page("000010", "e"), page("000000", "a"), page("000002", "d"), page("000001", "b", "c")},
			want:  []string{"a", "b", "c", "d", "e"},
		},
		{
			name:   "inline headers win over pages",
			inline: []ComposerConversationHeader{{BubbleID: "x"}},
			pages:  []headerPage{page("000000", "a", "b")},
			want:   []string{"x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composers := map[string]*ComposerData{"c": {ComposerID: "c", FullConversationHeadersOnly: tt.inline}}
			pages := map[string][]headerPage{"c": tt.pages}
			mergeHeaderPages(composers, pages)
			if got := headerIDs(composers["c"].FullConversationHeadersOnly); !slices.Equal(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
