package geminicli

import (
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
	fm := "Wrote `hello.fs`:\n\n```forth\n.\" Hello, World!\" CR\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "gemini-cli", Name: "Gemini CLI", Version: "x"},
		SessionID:     "orig-gemini-123",
		CreatedAt:     "2026-06-17T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-gemini-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Write hello world in Forth."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "I'll create hello.fs."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "Forth uses dot-quote for strings."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "write_file", Type: schema.ToolTypeWrite, FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Created hello.fs."}}},
				},
			},
		},
	}
}

// TestReconstructSession_RoundTrip reconstructs to native Gemini chat JSON, re-parses
// it through the forward path, and asserts the flattened transcript is preserved.
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
	if !strings.HasPrefix(out.Filename, "session-") || !strings.HasSuffix(out.Filename, ".json") {
		t.Errorf("filename %q should look like session-<ts>-<id>.json", out.Filename)
	}

	var session GeminiSession
	if err := json.Unmarshal(out.Content, &session); err != nil {
		t.Fatalf("reconstructed content is not valid GeminiSession JSON: %v", err)
	}
	if session.ID != out.SessionID {
		t.Errorf("session sessionId = %q, want %q", session.ID, out.SessionID)
	}

	regenerated, err := GenerateAgentSession(&session, "/tmp/proj")
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

// TestNativeSessionPath_CreatesProjectDir verifies that, for a project Gemini has
// not seen, NativeSessionPath creates the tmp project dir with a .project_root
// marker and returns a path under chats/.
func TestNativeSessionPath_CreatesProjectDir(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	originalHome := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = originalHome })

	path, err := NewProvider().NativeSessionPath(project, "session-x.json")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if filepath.Base(path) != "session-x.json" {
		t.Errorf("path %q should end with the filename", path)
	}
	if filepath.Base(filepath.Dir(path)) != "chats" {
		t.Errorf("path %q should be under a chats/ dir", path)
	}
	// The .project_root marker should exist alongside chats/ and point at the project.
	rootFile := filepath.Join(filepath.Dir(filepath.Dir(path)), ".project_root")
	content, err := os.ReadFile(rootFile)
	if err != nil {
		t.Fatalf("expected .project_root marker: %v", err)
	}
	if !strings.Contains(string(content), filepath.Base(project)) {
		t.Errorf(".project_root %q should reference the project path", string(content))
	}
}

func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
}
