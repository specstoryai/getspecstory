package copilotide

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/vscode"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// createTestWorkspace creates a temp workspace storage directory with a state.vscdb
// containing an empty ItemTable, and returns a WorkspaceMatch pointing at it.
func createTestWorkspace(t *testing.T) *vscode.WorkspaceEntry {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", GetWorkspaceStateDBPath(dir))
	if err != nil {
		t.Fatalf("createTestWorkspace: open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS ItemTable (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		_ = db.Close()
		t.Fatalf("createTestWorkspace: create ItemTable: %v", err)
	}
	_ = db.Close()
	return &vscode.WorkspaceEntry{ID: "test-workspace", Dir: dir, URI: "file:///tmp/proj", ResolvedPath: "/tmp/proj"}
}

// newTestProvider returns a VS Code provider whose reconstruction workspace lookup
// always resolves to the provided workspace, regardless of the project path argument.
func newTestProvider(ws *vscode.WorkspaceEntry) *Provider {
	p := NewProvider(VSCode)
	p.findWorkspaceForReconstruction = func(_ string) (*vscode.WorkspaceEntry, error) { return ws, nil }
	return p
}

// reconstructSampleData returns a minimal multi-turn SessionData for testing.
func reconstructSampleData() *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		SessionID:     "orig-session-123",
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

// readIndexEntries reads and parses the chat session index from a workspace's state.vscdb.
func readIndexEntries(t *testing.T, workspaceDir string) map[string]sessionIndexEntry {
	t.Helper()
	db, err := sql.Open("sqlite", GetWorkspaceStateDBPath(workspaceDir))
	if err != nil {
		t.Fatalf("readIndexEntries: open: %v", err)
	}
	defer func() { _ = db.Close() }()

	var raw string
	if err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", chatSessionIndexKey).Scan(&raw); err != nil {
		t.Fatalf("readIndexEntries: scan: %v", err)
	}
	var index struct {
		Version int                          `json:"version"`
		Entries map[string]sessionIndexEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &index); err != nil {
		t.Fatalf("readIndexEntries: parse: %v", err)
	}
	if index.Version != 1 {
		t.Errorf("index version = %d, want 1", index.Version)
	}
	return index.Entries
}

// TestReconstructSession_RoundTrip verifies the generated snapshot parses back through
// the provider's own loader with the conversation intact, and that the session is
// registered in the workspace index.
func TestReconstructSession_RoundTrip(t *testing.T) {
	ws := createTestWorkspace(t)
	p := newTestProvider(ws)
	rec, err := p.ReconstructSession(reconstructSampleData(), spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}
	if rec.SessionID == "" || rec.Filename != rec.SessionID+".jsonl" {
		t.Errorf("unexpected identity: sessionID=%q filename=%q", rec.SessionID, rec.Filename)
	}

	// The file must mirror a session VS Code itself compacted on close: a
	// single kind:0 snapshot with the conversation inline, and every request
	// carrying the completed-state fields (agent identity, modeInfo,
	// modelState, result) — VS Code's revival drops requests without them and
	// then auto-deletes the resulting empty session.
	contentLines := strings.Split(strings.TrimSpace(string(rec.Content)), "\n")
	if len(contentLines) != 1 {
		t.Fatalf("content should be a single compacted snapshot line, got %d lines", len(contentLines))
	}
	var first struct {
		Kind int `json:"kind"`
		V    struct {
			Requests []map[string]any `json:"requests"`
		} `json:"v"`
	}
	if err := json.Unmarshal([]byte(contentLines[0]), &first); err != nil {
		t.Fatalf("parse snapshot line: %v", err)
	}
	if first.Kind != 0 {
		t.Fatalf("snapshot line kind = %d, want 0", first.Kind)
	}
	if len(first.V.Requests) == 0 {
		t.Fatal("snapshot must inline the requests")
	}
	for _, required := range []string{"agent", "modeInfo", "modelState", "result", "responseTimestamp"} {
		if _, present := first.V.Requests[0][required]; !present {
			t.Errorf("request must carry completed-state field %q", required)
		}
	}
	if parts, ok := first.V.Requests[0]["message"].(map[string]any)["parts"].([]any); ok && len(parts) > 0 {
		if kind, _ := parts[0].(map[string]any)["kind"].(string); kind != "text" {
			t.Errorf("message part kind = %q, want \"text\" (current VS Code serializes the discriminator)", kind)
		}
	}

	// Write the content where the resume flow would and parse it back with the loader.
	sessionsDir := GetChatSessionsPath(ws.Dir)
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir chatSessions: %v", err)
	}
	path := filepath.Join(sessionsDir, rec.Filename)
	if err := os.WriteFile(path, rec.Content, 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	composer, err := LoadSessionFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFile: %v", err)
	}

	if composer.SessionID != rec.SessionID {
		t.Errorf("sessionId = %q, want %q", composer.SessionID, rec.SessionID)
	}
	if composer.Version != copilotSessionVersion {
		t.Errorf("version = %d, want %d", composer.Version, copilotSessionVersion)
	}
	if composer.CustomTitle != "fix the login bug" {
		t.Errorf("customTitle = %q, want source slug", composer.CustomTitle)
	}
	if !composer.IsImported {
		t.Error("isImported should be true on reconstructed sessions")
	}
	if len(composer.Requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(composer.Requests))
	}
	if composer.Requests[0].Message.Text != "What's wrong with the login flow?" {
		t.Errorf("request[0] user text = %q", composer.Requests[0].Message.Text)
	}
	if got := ExtractTextFromResponseArray(composer.Requests[0].Response); got != "The session token is not being invalidated on logout." {
		t.Errorf("request[0] agent text = %q", got)
	}
	if got := ExtractTextFromResponseArray(composer.Requests[1].Response); !strings.Contains(got, "deleteSession(token)") {
		t.Errorf("request[1] agent text = %q", got)
	}

	// The session must be registered in the workspace's chat session index.
	entries := readIndexEntries(t, ws.Dir)
	entry, ok := entries[rec.SessionID]
	if !ok {
		t.Fatalf("session %s missing from %s", rec.SessionID, chatSessionIndexKey)
	}
	if entry.Title != "fix the login bug" {
		t.Errorf("index title = %q, want source slug", entry.Title)
	}
	if entry.IsEmpty {
		t.Error("index entry isEmpty should be false")
	}
	if entry.Timing.Created == 0 || entry.LastMessageDate < entry.Timing.Created {
		t.Errorf("index timing inconsistent: created=%d lastMessageDate=%d", entry.Timing.Created, entry.LastMessageDate)
	}
}

// TestReconstructSession_MigrationNote verifies a leading agent turn (the migration
// note) is hosted by a synthetic user request rather than dropped.
func TestReconstructSession_MigrationNote(t *testing.T) {
	ws := createTestWorkspace(t)
	p := newTestProvider(ws)
	note := "Resumed from a Claude Code session via SpecStory."
	rec, err := p.ReconstructSession(reconstructSampleData(), spi.ReconstructOptions{
		WorkspaceRoot: "/tmp/proj",
		MigrationNote: note,
	})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}

	// Parse the incremental JSONL through the provider's own loader.
	composer, err := parseJSONL(rec.Content)
	if err != nil {
		t.Fatalf("parseJSONL: %v", err)
	}
	if len(composer.Requests) != 3 {
		t.Fatalf("requests = %d, want 3 (synthetic host + 2 real)", len(composer.Requests))
	}
	if composer.Requests[0].Message.Text != importedRequestText {
		t.Errorf("request[0] user text = %q, want synthetic host text", composer.Requests[0].Message.Text)
	}
	if got := ExtractTextFromResponseArray(composer.Requests[0].Response); got != note {
		t.Errorf("request[0] agent text = %q, want migration note", got)
	}
}

// TestReconstructSession_Errors verifies the shared guards reject nil and empty sessions.
func TestReconstructSession_Errors(t *testing.T) {
	ws := createTestWorkspace(t)
	p := newTestProvider(ws)

	if _, err := p.ReconstructSession(nil, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"}); err == nil {
		t.Error("nil session data should error")
	}
	empty := &schema.SessionData{SchemaVersion: "1.0", SessionID: "empty", WorkspaceRoot: "/tmp/proj"}
	if _, err := p.ReconstructSession(empty, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"}); err == nil {
		t.Error("empty session data should error")
	}
}

// TestWriteSessionIndexEntry_PreservesExisting verifies merging into an existing index
// keeps the other entries byte-for-byte, including fields we do not model.
func TestWriteSessionIndexEntry_PreservesExisting(t *testing.T) {
	ws := createTestWorkspace(t)
	dbPath := GetWorkspaceStateDBPath(ws.Dir)

	existing := `{"version":1,"entries":{"other-id":{"sessionId":"other-id","title":"Existing","lastMessageDate":5,"timing":{"created":5},"initialLocation":"panel","hasPendingEdits":false,"isEmpty":false,"isExternal":false,"lastResponseState":1,"someFutureField":42}}}`
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("INSERT INTO ItemTable (key, value) VALUES (?, ?)", chatSessionIndexKey, existing); err != nil {
		_ = db.Close()
		t.Fatalf("seed index: %v", err)
	}
	_ = db.Close()

	entry := sessionIndexEntry{
		SessionID:         "new-id",
		Title:             "New",
		LastMessageDate:   10,
		Timing:            sessionIndexTiming{Created: 9},
		InitialLocation:   "panel",
		LastResponseState: indexResponseStateComplete,
	}
	if err := writeSessionIndexEntry(dbPath, entry); err != nil {
		t.Fatalf("writeSessionIndexEntry: %v", err)
	}

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	var raw string
	if err := db.QueryRow("SELECT value FROM ItemTable WHERE key = ?", chatSessionIndexKey).Scan(&raw); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var index struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &index); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(index.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(index.Entries))
	}
	// Unmodeled fields on the pre-existing entry must survive the merge untouched.
	if !strings.Contains(string(index.Entries["other-id"]), `"someFutureField":42`) {
		t.Errorf("existing entry lost unmodeled fields: %s", index.Entries["other-id"])
	}
}

// TestEnsureWorkspaceForReconstruction_MintsEntry verifies that resuming into a
// project never opened in VS Code mints a workspace entry VS Code will adopt:
// the directory name is VS Code's own md5(path+salt) ID, workspace.json points
// at the folder, and state.vscdb carries the ItemTable the index write needs.
func TestEnsureWorkspaceForReconstruction_MintsEntry(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	storageRoot := filepath.Join(fakeHome, "Library", "Application Support", "Code", "User", "workspaceStorage")
	if runtime.GOOS == "linux" {
		storageRoot = filepath.Join(fakeHome, ".config", "Code", "User", "workspaceStorage")
	}
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(fakeHome, "proj")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	p := NewProvider(VSCode)
	ws, err := p.ensureWorkspaceForReconstruction(projectDir)
	if err != nil {
		t.Fatalf("ensureWorkspaceForReconstruction: %v", err)
	}

	canonical, err := spi.GetCanonicalPath(projectDir)
	if err != nil {
		canonical = projectDir
	}
	wantID, err := vscode.WorkspaceID(canonical)
	if err != nil {
		t.Fatalf("vscodeWorkspaceID: %v", err)
	}
	if ws.ID != wantID {
		t.Errorf("minted ID = %q, want VS Code's own %q", ws.ID, wantID)
	}

	wsJSON, err := vscode.ReadWorkspaceJSON(GetWorkspaceMetadataPath(ws.Dir))
	if err != nil {
		t.Fatalf("minted workspace.json unreadable: %v", err)
	}
	if got, err := vscode.URIToPath(wsJSON.Folder); err != nil || got != canonical {
		t.Errorf("workspace.json folder = %q (%v), want %q", got, err, canonical)
	}

	db, err := sql.Open("sqlite", GetWorkspaceStateDBPath(ws.Dir))
	if err != nil {
		t.Fatalf("minted state.vscdb unopenable: %v", err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM ItemTable").Scan(&n); err != nil {
		t.Errorf("minted state.vscdb lacks ItemTable: %v", err)
	}

	// A second call must find the minted entry, not mint a duplicate.
	again, err := p.ensureWorkspaceForReconstruction(projectDir)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if again.ID != ws.ID {
		t.Errorf("second ensure minted a different entry: %q vs %q", again.ID, ws.ID)
	}
}

// TestNativeSessionPath verifies path resolution and that a missing chatSessions
// directory is not required (the caller creates it).
func TestNativeSessionPath(t *testing.T) {
	ws := createTestWorkspace(t)
	p := newTestProvider(ws)
	got, err := p.NativeSessionPath("/tmp/proj", "abc.jsonl")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	want := filepath.Join(GetChatSessionsPath(ws.Dir), "abc.jsonl")
	if got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if _, err := os.Stat(GetChatSessionsPath(ws.Dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test premise broken: chatSessions should not exist yet")
	}
}
