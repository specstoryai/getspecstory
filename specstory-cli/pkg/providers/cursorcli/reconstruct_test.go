package cursorcli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func strptr(s string) *string { return &s }

func reconstructSampleData() *schema.SessionData {
	fm := "Created `hello.rs`:\n\n```rust\nfn main() { println!(\"Hello, World!\"); }\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "cursor", Name: "Cursor CLI", Version: "x"},
		SessionID:     "orig-cursor-123",
		CreatedAt:     "2026-06-18T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Slug:          "hello world in rust",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-cursor-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Create a hello world in Rust."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "I'll create it."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "write", Type: schema.ToolTypeWrite, FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Done."}}},
				},
			},
		},
	}
}

// openStoreBytes writes the reconstructed store.db bytes to a temp file and reads
// back its blobs + meta head record.
func openStoreBytes(t *testing.T, content []byte) (map[string][]byte, map[string]interface{}) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write store: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = db.Close() }()

	blobs := map[string][]byte{}
	rows, err := db.Query("SELECT id, data FROM blobs")
	if err != nil {
		t.Fatalf("query blobs: %v", err)
	}
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			t.Fatalf("scan: %v", err)
		}
		blobs[id] = data
	}
	_ = rows.Close()

	var metaHex string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='0'").Scan(&metaHex); err != nil {
		t.Fatalf("meta: %v", err)
	}
	metaBytes, err := hex.DecodeString(metaHex)
	if err != nil {
		t.Fatalf("meta hex: %v", err)
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("meta json: %v", err)
	}
	return blobs, meta
}

// refsOf scans a protobuf node for 32-byte blob-id references in the given field.
func refsOf(node []byte, fieldNum int) []string {
	tag := byte(fieldNum<<3 | 2)
	var refs []string
	for i := 0; i+34 <= len(node); i++ {
		if node[i] == tag && node[i+1] == 0x20 {
			refs = append(refs, hex.EncodeToString(node[i+2:i+34]))
		}
	}
	return refs
}

// TestReconstructSession_Structure validates the store.db has the content-addressed
// shape Cursor requires: sha256 ids, a head pointer, and a root whose field-1 lists
// the message blobs in order with a field-2 metadata node.
func TestReconstructSession_Structure(t *testing.T) {
	data := reconstructSampleData()
	expected := spi.FlattenSessionData(data, "") // user + 3 agent turns

	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}
	if out.SessionID == "" || filepath.Base(out.Filename) != "store.db" {
		t.Fatalf("unexpected id/filename: %q / %q", out.SessionID, out.Filename)
	}

	blobs, meta := openStoreBytes(t, out.Content)

	// Every blob is content-addressed by sha256.
	for id, d := range blobs {
		if sum := sha256.Sum256(d); hex.EncodeToString(sum[:]) != id {
			t.Errorf("blob id %s != sha256(data)", id)
		}
	}

	head, _ := meta["latestRootBlobId"].(string)
	root, ok := blobs[head]
	if !ok {
		t.Fatalf("head %q not present in blobs", head)
	}
	if meta["agentId"] != out.SessionID {
		t.Errorf("meta agentId %v != session id %q", meta["agentId"], out.SessionID)
	}

	// Root field 1 = message blobs in order; verify roles + text match the turns.
	msgRefs := refsOf(root, 1)
	if len(msgRefs) != len(expected) {
		t.Fatalf("root references %d messages, want %d", len(msgRefs), len(expected))
	}
	for i, ref := range msgRefs {
		var m struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(blobs[ref], &m); err != nil {
			t.Fatalf("message %d not JSON: %v", i, err)
		}
		wantRole := "assistant"
		if expected[i].Role == schema.RoleUser {
			wantRole = "user"
		}
		if m.Role != wantRole {
			t.Errorf("message %d role = %q, want %q", i, m.Role, wantRole)
		}
		if len(m.Content) == 0 || m.Content[0].Text != expected[i].Text {
			t.Errorf("message %d text mismatch:\n got: %q\nwant: %q", i, m.Content, expected[i].Text)
		}
	}

	// Root field 2 = metadata node, which must resolve.
	metaRefs := refsOf(root, 2)
	if len(metaRefs) != 1 || blobs[metaRefs[0]] == nil {
		t.Errorf("root should reference exactly one metadata node; got %v", metaRefs)
	}
}

func TestNativeSessionPath(t *testing.T) {
	path, err := NewProvider().NativeSessionPath(t.TempDir(), filepath.Join("sess-id", "store.db"))
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if !strings.Contains(path, filepath.Join(".cursor", "chats")) {
		t.Errorf("path %q should be under .cursor/chats", path)
	}
	if !strings.HasSuffix(path, filepath.Join("sess-id", "store.db")) {
		t.Errorf("path %q should end with <session-id>/store.db", path)
	}
}

func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
}
