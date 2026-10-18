package codexcli

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

// reconstructSampleData exercises user text, agent text, thinking, and a tool
// call carrying both a summary and formatted markdown.
func reconstructSampleData() *schema.SessionData {
	summary := "Tool use: **exec_command** `ls`"
	fm := "```\nhello.c\nhello\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "codex-cli", Name: "Codex CLI", Version: "x"},
		SessionID:     "orig-codex-123",
		CreatedAt:     "2026-06-17T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-codex-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Create a hello world in D."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Adding hello.d."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "Check for a compiler."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "exec_command", Type: schema.ToolTypeShell, Summary: strptr(summary), FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Created hello.d."}}},
				},
			},
		},
	}
}

func parseCodexJSONL(t *testing.T, content []byte) []map[string]interface{} {
	t.Helper()
	var records []map[string]interface{}
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
		records = append(records, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning content: %v", err)
	}
	return records
}

// TestReconstructSession_RoundTrip reconstructs to native Codex rollout format,
// re-parses through the forward path, and asserts the flattened transcript is
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
	if !strings.HasPrefix(out.Filename, "rollout-") || !strings.HasSuffix(out.Filename, ".jsonl") {
		t.Errorf("filename %q should look like rollout-<ts>-<id>.jsonl", out.Filename)
	}

	records := parseCodexJSONL(t, out.Content)
	regenerated, err := GenerateAgentSession(records, "/tmp/proj")
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

// TestReconstructSession_Structure checks the first record is session_meta with a
// matching id and provenance, that there is exactly one user_message event, and
// that no synthesized environment_context is emitted (Codex injects its own).
func TestReconstructSession_Structure(t *testing.T) {
	out, err := NewProvider().ReconstructSession(reconstructSampleData(), spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}

	records := parseCodexJSONL(t, out.Content)
	if len(records) == 0 {
		t.Fatal("no records produced")
	}

	meta := records[0]
	if meta["type"] != "session_meta" {
		t.Fatalf("first record type = %v, want session_meta", meta["type"])
	}
	payload, _ := meta["payload"].(map[string]interface{})
	if payload["id"] != out.SessionID {
		t.Errorf("session_meta id = %v, want %q", payload["id"], out.SessionID)
	}
	if payload["specstorySourceSessionId"] != "orig-codex-123" {
		t.Errorf("provenance = %v, want orig-codex-123", payload["specstorySourceSessionId"])
	}

	// Exactly one user_message event per user turn; no synthesized environment_context anywhere.
	userEvents := 0
	for _, r := range records {
		if strings.Contains(toJSON(t, r), "environment_context") {
			t.Error("reconstruction should not synthesize an environment_context block")
		}
		if r["type"] != "event_msg" {
			continue
		}
		p, _ := r["payload"].(map[string]interface{})
		if p["type"] == "user_message" {
			userEvents++
		}
	}
	if userEvents != 1 {
		t.Errorf("got %d user_message events, want 1", userEvents)
	}
}

func toJSON(t *testing.T, v map[string]interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestNativeSessionPath(t *testing.T) {
	path, err := NewProvider().NativeSessionPath("/tmp/some-codex-proj", "rollout-x.jsonl")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if !strings.Contains(path, filepath.Join(".codex", "sessions")) {
		t.Errorf("path %q should be under .codex/sessions", path)
	}
	if !strings.HasSuffix(path, "rollout-x.jsonl") {
		t.Errorf("path %q should end with the filename", path)
	}
}

func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
}
