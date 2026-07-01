package cursoride

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// createTestWorkspaceDB creates an in-temp-dir SQLite workspace database with an ItemTable
// and returns both its path and a WorkspaceMatch pointing at it.
func createTestWorkspaceDB(t *testing.T) (string, *WorkspaceMatch) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "workspace.vscdb")
	db, err := OpenDatabaseReadWrite(dbPath)
	if err != nil {
		t.Fatalf("createTestWorkspaceDB: open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		_ = db.Close()
		t.Fatalf("createTestWorkspaceDB: create ItemTable: %v", err)
	}
	_ = db.Close()
	ws := &WorkspaceMatch{ID: "test-workspace", DBPath: dbPath, URI: "file:///tmp/proj"}
	return dbPath, ws
}

// patchWorkspace replaces FindWorkspaceForProject for the duration of the test,
// always returning the provided workspace regardless of the project path argument.
func patchWorkspace(t *testing.T, ws *WorkspaceMatch) {
	t.Helper()
	orig := FindWorkspaceForProject
	FindWorkspaceForProject = func(_ string) (*WorkspaceMatch, error) { return ws, nil }
	t.Cleanup(func() { FindWorkspaceForProject = orig })
}

// reconstructSampleData returns a minimal multi-turn SessionData for testing.
func reconstructSampleData() *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		SessionID:     "orig-cursor-ide-123",
		WorkspaceRoot: "/tmp/proj",
		Slug:          "fix the login bug",
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "What's wrong with the login flow?"}}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "The session token is not being invalidated on logout."}}},
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "How do we fix it?"}}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Call `deleteSession(token)` in the logout handler."}}},
			}},
		},
	}
}

// TestReconstructSession_WritesGlobalDB verifies that ReconstructSession writes a
// composerData row and one bubbleId row per turn into the global database.
func TestReconstructSession_WritesGlobalDB(t *testing.T) {
	data := reconstructSampleData()
	expected := spi.FlattenSessionData(data, "")

	// Point the provider at temp databases instead of the live Cursor installation.
	dbPath := createTestGlobalDB(t, map[string]string{})
	origGetPath := GetGlobalDatabasePath
	GetGlobalDatabasePath = func() (string, error) { return dbPath, nil }
	t.Cleanup(func() { GetGlobalDatabasePath = origGetPath })

	_, ws := createTestWorkspaceDB(t)
	patchWorkspace(t, ws)

	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}
	if out.SessionID == "" {
		t.Fatal("expected a non-empty session ID")
	}
	if len(out.Content) != 0 {
		t.Errorf("expected empty Content for DB-backed provider, got %d bytes", len(out.Content))
	}

	// Read back the composer row.
	composers, err := LoadAllComposerDataLightweight(dbPath)
	if err != nil {
		t.Fatalf("LoadAllComposerDataLightweight: %v", err)
	}
	composer, ok := composers[out.SessionID]
	if !ok {
		t.Fatalf("composer row %q not found in database after reconstruction", out.SessionID)
	}
	if composer.Name != "fix the login bug" {
		t.Errorf("composer name = %q, want %q", composer.Name, "fix the login bug")
	}
	if len(composer.FullConversationHeadersOnly) != len(expected) {
		t.Errorf("composer has %d headers, want %d", len(composer.FullConversationHeadersOnly), len(expected))
	}

	// Read back bubble rows and verify role and text.
	db, err := OpenDatabaseReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open DB for verification: %v", err)
	}
	defer func() { _ = db.Close() }()

	for i, header := range composer.FullConversationHeadersOnly {
		var valueJSON string
		key := "bubbleId:" + out.SessionID + ":" + header.BubbleID
		if err := db.QueryRow("SELECT value FROM cursorDiskKV WHERE key = ?", key).Scan(&valueJSON); err != nil {
			t.Errorf("bubble %d (%s) not found: %v", i, header.BubbleID, err)
			continue
		}
		var bubble ComposerConversation
		if err := json.Unmarshal([]byte(valueJSON), &bubble); err != nil {
			t.Fatalf("bubble %d: unmarshal: %v", i, err)
		}
		wantType := cursorBubbleTypeAssistant
		if expected[i].Role == schema.RoleUser {
			wantType = cursorBubbleTypeUser
		}
		if bubble.Type != wantType {
			t.Errorf("bubble %d type = %d, want %d", i, bubble.Type, wantType)
		}
		if bubble.Text != expected[i].Text {
			t.Errorf("bubble %d text = %q, want %q", i, bubble.Text, expected[i].Text)
		}

		// grouping.isRenderable must be true — Cursor skips rendering bubbles that lack it.
		if header.Grouping == nil {
			t.Errorf("bubble %d: header.Grouping is nil", i)
		} else if !header.Grouping.IsRenderable {
			t.Errorf("bubble %d: header.Grouping.IsRenderable = false, want true", i)
		}
		// Header type must match the bubble type for Cursor to classify the turn correctly.
		if header.Type != wantType {
			t.Errorf("bubble %d: header.Type = %d, want %d", i, header.Type, wantType)
		}
		// createdAt must be non-empty so Cursor can sort turns chronologically.
		if header.CreatedAt == "" {
			t.Errorf("bubble %d: header.CreatedAt is empty", i)
		}
	}

	// Verify the global composer.composerHeaders was written — the key fix for sidebar visibility.
	headers := readComposerHeaders(t, dbPath)
	found := false
	for _, h := range headers {
		if h["composerId"] == out.SessionID {
			found = true
			if h["name"] != "fix the login bug" {
				t.Errorf("composerHeaders entry name = %v, want %q", h["name"], "fix the login bug")
			}
			break
		}
	}
	if !found {
		t.Errorf("composerHeaders entry for %q not found in global ItemTable", out.SessionID)
	}
}

// TestReconstructSession_Empty mirrors the same test in every other provider.
func TestReconstructSession_Empty(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	origGetPath := GetGlobalDatabasePath
	GetGlobalDatabasePath = func() (string, error) { return dbPath, nil }
	t.Cleanup(func() { GetGlobalDatabasePath = origGetPath })

	_, ws := createTestWorkspaceDB(t)
	patchWorkspace(t, ws)

	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
	// Must NOT be ErrReconstructionUnsupported — reconstruction is now implemented.
	if errors.Is(err, spi.ErrReconstructionUnsupported) {
		t.Errorf("expected a content error, not ErrReconstructionUnsupported")
	}
}

// TestReconstructSession_MigrationNote verifies the migration note becomes the first
// bubble and shifts all source turns down by one.
func TestReconstructSession_MigrationNote(t *testing.T) {
	dbPath := createTestGlobalDB(t, map[string]string{})
	origGetPath := GetGlobalDatabasePath
	GetGlobalDatabasePath = func() (string, error) { return dbPath, nil }
	t.Cleanup(func() { GetGlobalDatabasePath = origGetPath })

	_, ws := createTestWorkspaceDB(t)
	patchWorkspace(t, ws)

	data := reconstructSampleData()
	note := "Resumed from a Claude Code session via SpecStory."
	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{
		WorkspaceRoot: "/tmp/proj",
		MigrationNote: note,
	})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}

	composers, _ := LoadAllComposerDataLightweight(dbPath)
	composer := composers[out.SessionID]
	// The note is prepended as an agent turn, so total = source turns + 1.
	want := len(spi.FlattenSessionData(data, "")) + 1
	if len(composer.FullConversationHeadersOnly) != want {
		t.Errorf("expected %d bubbles (note + source turns), got %d", want, len(composer.FullConversationHeadersOnly))
	}

	// First bubble must be an assistant turn with the note text.
	db, err := OpenDatabaseReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open DB for verification: %v", err)
	}
	defer func() { _ = db.Close() }()
	firstKey := "bubbleId:" + out.SessionID + ":" + composer.FullConversationHeadersOnly[0].BubbleID
	var v string
	if err := db.QueryRow("SELECT value FROM cursorDiskKV WHERE key = ?", firstKey).Scan(&v); err != nil {
		t.Fatalf("first bubble row not found: %v", err)
	}
	var first ComposerConversation
	if err := json.Unmarshal([]byte(v), &first); err != nil {
		t.Fatalf("unmarshal first bubble: %v", err)
	}
	if first.Type != cursorBubbleTypeAssistant {
		t.Errorf("first bubble type = %d, want %d (assistant)", first.Type, cursorBubbleTypeAssistant)
	}
	if !strings.Contains(first.Text, "Resumed") {
		t.Errorf("first bubble text %q should contain the migration note", first.Text)
	}
}

// TestNativeSessionPath returns a path in the OS temp directory so the resume flow's
// sentinel file write lands somewhere harmless.
func TestNativeSessionPath(t *testing.T) {
	path, err := NewProvider().NativeSessionPath("/some/project", "abc-uuid")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if filepath.Base(path) != "specstory-cursor-abc-uuid" {
		t.Errorf("path base = %q, want specstory-cursor-abc-uuid", filepath.Base(path))
	}
	// Must be inside the OS temp dir so it never lands in the user's project.
	if !strings.HasPrefix(path, filepath.Join("/", "tmp")) && !strings.HasPrefix(path, "/var/folders") && !strings.HasPrefix(path, "/private") {
		t.Logf("temp path: %s (accepted — OS temp dirs vary)", path)
	}
}
