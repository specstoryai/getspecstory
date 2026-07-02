package cursoride

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
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
