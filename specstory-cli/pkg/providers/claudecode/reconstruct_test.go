package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func strptr(s string) *string { return &s }

// reconstructSampleData returns a SessionData exercising user text, agent text,
// thinking, and a tool call (all of which flatten to user/agent text turns).
func reconstructSampleData() *schema.SessionData {
	fm := "Wrote `hello.c`:\n\n```c\n#include <stdio.h>\nint main(void){ printf(\"Hello, World!\\n\"); }\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "claude-code", Name: "Claude Code", Version: "x"},
		SessionID:     "orig-claude-123",
		CreatedAt:     "2026-06-17T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-claude-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Create a Hello World in C."}}},
					{Role: schema.RoleAgent, Model: "claude-x", Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "I'll create it."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "Need a main function."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Done."}}},
				},
			},
		},
	}
}

func parseClaudeJSONL(t *testing.T, content []byte) []JSONLRecord {
	t.Helper()
	var records []JSONLRecord
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", line, err)
		}
		records = append(records, JSONLRecord{Data: m})
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning content: %v", err)
	}
	return records
}

// TestReconstructSession_RoundTrip reconstructs a session to native Claude JSONL,
// re-parses it through the forward path, and asserts the flattened transcript is
// preserved.
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
	if !strings.HasSuffix(out.Filename, ".jsonl") {
		t.Errorf("filename %q should end in .jsonl", out.Filename)
	}

	records := parseClaudeJSONL(t, out.Content)
	regenerated, err := GenerateAgentSession(Session{SessionUuid: out.SessionID, Records: records}, "/tmp/proj")
	if err != nil {
		t.Fatalf("GenerateAgentSession: %v", err)
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

// TestReconstructSession_Chain verifies the records form a valid linear parentUuid
// chain rooted at null, all sharing the fresh session ID.
func TestReconstructSession_Chain(t *testing.T) {
	out, err := NewProvider().ReconstructSession(reconstructSampleData(), spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}

	records := parseClaudeJSONL(t, out.Content)
	if len(records) == 0 {
		t.Fatal("no records produced")
	}

	var prev string
	for i, r := range records {
		if sid, _ := r.Data["sessionId"].(string); sid != out.SessionID {
			t.Errorf("record %d sessionId = %q, want %q", i, sid, out.SessionID)
		}
		parent := r.Data["parentUuid"]
		if i == 0 {
			if parent != nil {
				t.Errorf("first record parentUuid = %v, want nil", parent)
			}
			if src, _ := r.Data["specstorySourceSessionId"].(string); src != "orig-claude-123" {
				t.Errorf("first record provenance = %q, want orig-claude-123", src)
			}
		} else if ps, _ := parent.(string); ps != prev {
			t.Errorf("record %d parentUuid = %v, want %q", i, parent, prev)
		}
		prev, _ = r.Data["uuid"].(string)
	}
}

func TestNativeSessionPath(t *testing.T) {
	path, err := NewProvider().NativeSessionPath("/tmp/some-claude-proj", "abc.jsonl")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if !strings.Contains(path, filepath.Join(".claude", "projects")) {
		t.Errorf("path %q should be under .claude/projects", path)
	}
	if !strings.HasSuffix(path, "abc.jsonl") {
		t.Errorf("path %q should end with the filename", path)
	}
	// The project segment encodes the cwd (non-alphanumerics become dashes).
	if !strings.Contains(path, "-tmp-some-claude-proj") {
		t.Errorf("path %q should contain the encoded project dir", path)
	}
}

func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
}
