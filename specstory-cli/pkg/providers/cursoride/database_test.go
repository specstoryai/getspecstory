package cursoride

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// createTestGlobalDB builds a minimal global state.vscdb in a temp directory with
// the cursorDiskKV table populated from kvPairs. The driver is registered by
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
		t.Fatalf("createTestGlobalDB: create table: %v", err)
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
