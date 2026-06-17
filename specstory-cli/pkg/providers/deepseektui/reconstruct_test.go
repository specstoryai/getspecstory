package deepseektui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func strptr(s string) *string { return &s }

func reconstructSampleData() *schema.SessionData {
	fm := "Ran `ls`:\n\n```\nfile.txt\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "deepseek-tui", Name: "DeepSeek TUI", Version: "x"},
		SessionID:     "orig-deepseek-123",
		CreatedAt:     "2026-06-17T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-deepseek-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "List the files here."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Listing files."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "Use ls."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "shell", Type: schema.ToolTypeShell, FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "There is one file."}}},
				},
			},
		},
	}
}

// TestReconstructSession_RoundTrip reconstructs to native DeepSeek JSON, re-parses
// it through the forward path, and asserts the flattened transcript is preserved.
func TestReconstructSession_RoundTrip(t *testing.T) {
	data := reconstructSampleData()
	expected := spi.FlattenSessionData(data, "")

	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}
	if out.SessionID == "" || !strings.HasSuffix(out.Filename, ".json") {
		t.Fatalf("unexpected id/filename: %q / %q", out.SessionID, out.Filename)
	}

	var session dsSession
	if err := json.Unmarshal(out.Content, &session); err != nil {
		t.Fatalf("reconstructed content is not valid dsSession JSON: %v", err)
	}
	if session.Metadata.ID != out.SessionID {
		t.Errorf("metadata.id = %q, want %q", session.Metadata.ID, out.SessionID)
	}
	if session.Metadata.Workspace != "/tmp/proj" {
		t.Errorf("metadata.workspace = %q, want /tmp/proj", session.Metadata.Workspace)
	}

	regenerated, err := generateAgentSession(&session, "/tmp/proj")
	if err != nil {
		t.Fatalf("generateAgentSession: %v", err)
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

func TestNativeSessionPath(t *testing.T) {
	path, err := NewProvider().NativeSessionPath("/tmp/whatever", "abc.json")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if !strings.Contains(path, filepath.Join(".deepseek", "sessions")) {
		t.Errorf("path %q should be under .deepseek/sessions", path)
	}
	if !strings.HasSuffix(path, "abc.json") {
		t.Errorf("path %q should end with the filename", path)
	}
}

func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
}
