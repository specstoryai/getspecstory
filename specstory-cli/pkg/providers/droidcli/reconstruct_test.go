package droidcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func strptr(s string) *string { return &s }

func reconstructSampleData() *schema.SessionData {
	fm := "Wrote `hello.c`:\n\n```c\n#include <stdio.h>\nint main(void){ printf(\"Hello, World!\\n\"); }\n```"
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "droid-cli", Name: "Factory Droid", Version: "x"},
		SessionID:     "orig-droid-123",
		CreatedAt:     "2026-06-17T10:00:00.000Z",
		WorkspaceRoot: "/tmp/proj",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "orig-droid-123:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Create a hello world in C."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "I'll create it."}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "Need a main function."}}},
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Create", Type: schema.ToolTypeWrite, FormattedMarkdown: strptr(fm)}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Done."}}},
				},
			},
		},
	}
}

// TestReconstructSession_RoundTrip reconstructs to native Droid JSONL, re-parses it
// through the forward path, and asserts the flattened transcript is preserved.
func TestReconstructSession_RoundTrip(t *testing.T) {
	data := reconstructSampleData()
	expected := spi.FlattenSessionData(data, "")

	out, err := NewProvider().ReconstructSession(data, spi.ReconstructOptions{WorkspaceRoot: "/tmp/proj"})
	if err != nil {
		t.Fatalf("ReconstructSession: %v", err)
	}
	if out.SessionID == "" || !strings.HasSuffix(out.Filename, ".jsonl") {
		t.Fatalf("unexpected id/filename: %q / %q", out.SessionID, out.Filename)
	}

	tmp := filepath.Join(t.TempDir(), out.Filename)
	if err := os.WriteFile(tmp, out.Content, 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	session, err := parseFactorySession(tmp)
	if err != nil {
		t.Fatalf("parseFactorySession: %v", err)
	}
	regenerated, err := GenerateAgentSession(session, "/tmp/proj")
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

func TestNativeSessionPath(t *testing.T) {
	path, err := NewProvider().NativeSessionPath("/tmp/some-droid-proj", "abc.jsonl")
	if err != nil {
		t.Fatalf("NativeSessionPath: %v", err)
	}
	if !strings.Contains(path, filepath.Join(".factory", "sessions")) {
		t.Errorf("path %q should be under .factory/sessions", path)
	}
	if !strings.HasSuffix(path, "abc.jsonl") {
		t.Errorf("path %q should end with the filename", path)
	}
	if !strings.Contains(path, "-tmp-some-droid-proj") {
		t.Errorf("path %q should contain the encoded project dir", path)
	}
}

func TestReconstructSession_Empty(t *testing.T) {
	_, err := NewProvider().ReconstructSession(&schema.SessionData{SessionID: "x", WorkspaceRoot: "/tmp"}, spi.ReconstructOptions{})
	if err == nil {
		t.Fatal("expected error reconstructing a session with no content")
	}
}
