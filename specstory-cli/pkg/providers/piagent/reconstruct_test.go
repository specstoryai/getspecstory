package piagent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func strptr(s string) *string { return &s }

// reconstructSampleData exercises every flatten path: user text, agent text,
// agent thinking, and a tool call carrying both a summary and formatted markdown.
func reconstructSampleData() *schema.SessionData {
	summary := "Tool use: **bash** `ls`"
	fm := "```\nhello.c\nhello\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: providerID, Name: providerName, Version: "v3"},
		SessionID:     "orig-pi-123",
		CreatedAt:     "2026-06-17T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-pi-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Create a hello world in D."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Adding hello.d."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "Check for a compiler."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "bash", Type: schema.ToolTypeShell, Summary: strptr(summary), FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Created hello.d."}}},
				},
			},
		},
	}
}

// parsePiJSONL splits reconstructed content into per-line maps. It uses a
// bufio.Reader (not bufio.Scanner) with unbounded line size because pi session
// lines can legitimately exceed the 16 MB Scanner cap; reconstructed lines are
// small, but mirroring the read side's reader keeps the helper honest.
func parsePiJSONL(t *testing.T, content []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	reader := bufio.NewReader(bytes.NewReader(content))
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			var m map[string]any
			if uErr := json.Unmarshal([]byte(trimmed), &m); uErr != nil {
				t.Fatalf("invalid JSONL line %q: %v", trimmed, uErr)
			}
			records = append(records, m)
		}
		if err != nil {
			break // io.EOF or a read error ends the scan
		}
	}
	return records
}

// TestReconstructSession_RoundTrip reconstructs to native pi v3 JSONL, re-parses
// through pi's own ParseSession, and asserts the flattened transcript is
// preserved turn-for-turn — proving pi's reader accepts our output.
func TestReconstructSession_RoundTrip(t *testing.T) {
	data := reconstructSampleData()
	expected := spi.FlattenSessionData(data, "")

	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}
	if out.SessionID == "" {
		t.Fatal("expected a fresh session ID")
	}
	// Filename shape: <timestamp>_<uuid>.jsonl, ending with the session id.
	if !strings.HasSuffix(out.Filename, "_"+out.SessionID+".jsonl") {
		t.Errorf("filename %q should end with _<sessionID>.jsonl", out.Filename)
	}
	if strings.ContainsAny(out.Filename, ":") {
		t.Errorf("filename %q must be filesystem-safe (no ':')", out.Filename)
	}

	// Re-parse the reconstructed bytes through pi's own read path.
	dir := t.TempDir()
	path := filepath.Join(dir, out.Filename)
	if wErr := os.WriteFile(path, out.Content, 0o600); wErr != nil {
		t.Fatalf("writing reconstructed file: %v", wErr)
	}
	regenerated, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession of reconstructed file: %v", err)
	}

	actual := spi.FlattenSessionData(regenerated, "")
	if len(actual) != len(expected) {
		t.Fatalf("round-trip produced %d turns, want %d\n got: %+v\nwant: %+v", len(actual), len(expected), actual, expected)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Errorf("turn %d mismatch:\n got: %+v\nwant: %+v", i, actual[i], expected[i])
		}
	}
}

// TestReconstructSession_Chain asserts the header + strictly-linear parentId
// chain, and that feeding the message entries through pi's own walkToRoot
// terminates and yields every turn in order (no accidental cycle).
func TestReconstructSession_Chain(t *testing.T) {
	data := reconstructSampleData()
	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}

	records := parsePiJSONL(t, out.Content)
	if len(records) < 2 {
		t.Fatalf("expected a header + at least one message, got %d records", len(records))
	}

	// Line 0 is the session header.
	header := records[0]
	if header["type"] != entrySession {
		t.Errorf("first record type = %v, want %q", header["type"], entrySession)
	}
	if header["id"] != out.SessionID {
		t.Errorf("header id = %v, want %q", header["id"], out.SessionID)
	}
	if header["specstorySourceSessionId"] != data.SessionID {
		t.Errorf("provenance = %v, want %q", header["specstorySourceSessionId"], data.SessionID)
	}
	if v, ok := header["version"].(float64); !ok || int(v) != piHeaderVersion {
		t.Errorf("header version = %v, want %d", header["version"], piHeaderVersion)
	}

	// Messages: first parentId null, each subsequent parentId == previous id.
	msgs := records[1:]
	var entries []rawEntry
	var prevID string
	for i, m := range msgs {
		if m["type"] != entryMessage {
			t.Errorf("record %d type = %v, want %q", i+1, m["type"], entryMessage)
		}
		id, _ := m["id"].(string)
		if id == "" {
			t.Fatalf("message %d has no id", i)
		}
		if i == 0 {
			if m["parentId"] != nil {
				t.Errorf("first message parentId = %v, want null", m["parentId"])
			}
		} else if m["parentId"] != prevID {
			t.Errorf("message %d parentId = %v, want %q", i, m["parentId"], prevID)
		}
		var pid *string
		if i > 0 {
			p := prevID
			pid = &p
		}
		entries = append(entries, rawEntry{Type: entryMessage, ID: id, ParentID: pid})
		prevID = id
	}

	// walkToRoot from the leaf must terminate and cover every message in order.
	byID := indexByID(entries)
	path := walkToRoot(entries[len(entries)-1], byID)
	reverse(path)
	if len(path) != len(entries) {
		t.Fatalf("walkToRoot covered %d entries, want %d (chain broken or cyclic)", len(path), len(entries))
	}
	for i := range entries {
		if path[i].ID != entries[i].ID {
			t.Errorf("walkToRoot order mismatch at %d: got %q want %q", i, path[i].ID, entries[i].ID)
		}
	}
}

// TestReconstructSession_Empty confirms pi inherits the shared no-content guard.
func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
	if !strings.Contains(err.Error(), "no content to reconstruct") {
		t.Errorf("error = %q, want it to mention no content to reconstruct", err.Error())
	}
}

// TestNativeSessionPath covers both the default encoded-cwd layout and the flat
// PI_CODING_AGENT_SESSION_DIR override.
func TestNativeSessionPath(t *testing.T) {
	t.Run("default encoded layout", func(t *testing.T) {
		projectPath := filepath.FromSlash("/tmp/some-pi-proj")
		path, err := NewProvider().NativeSessionPath(projectPath, "sess-x.jsonl")
		if err != nil {
			t.Fatalf("NativeSessionPath: %v", err)
		}
		if !strings.Contains(path, filepath.Join(".pi", "agent", "sessions")) {
			t.Errorf("path %q should be under .pi/agent/sessions", path)
		}
		abs, _ := filepath.Abs(projectPath)
		if !strings.Contains(path, EncodeCwd(abs)) {
			t.Errorf("path %q should contain encoded cwd segment %q", path, EncodeCwd(abs))
		}
		if !strings.HasSuffix(path, "sess-x.jsonl") {
			t.Errorf("path %q should end with the filename", path)
		}
	})

	t.Run("flat override layout", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv(envSessionDir, tmp)
		path, err := NewProvider().NativeSessionPath("/tmp/whatever", "sess-y.jsonl")
		if err != nil {
			t.Fatalf("NativeSessionPath: %v", err)
		}
		want := filepath.Join(tmp, "sess-y.jsonl")
		if path != want {
			t.Errorf("flat path = %q, want %q (no encoded segment)", path, want)
		}
	})
}
