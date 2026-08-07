package qwencode

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

func sampleSessionData() *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "claude", Name: "Claude Code", Version: "1.0"},
		SessionID:     "src-session-id",
		CreatedAt:     "2026-08-07T10:00:00Z",
		WorkspaceRoot: "/Users/dev/project",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "ex-1",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: "text", Text: "add a divide function"}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: "text", Text: "Done - added div()."}}},
				},
			},
		},
	}
}

func TestReconstructSession(t *testing.T) {
	p := NewProvider()

	if !p.SupportsReconstruction() {
		t.Fatal("qwen provider should support reconstruction")
	}

	result, err := p.ReconstructSession(sampleSessionData(), spi.ReconstructOptions{})
	if err != nil {
		t.Fatalf("ReconstructSession failed: %v", err)
	}

	if result.SessionID == "" {
		t.Error("reconstructed session should have a fresh ID")
	}
	if result.Filename != result.SessionID+".jsonl" {
		t.Errorf("Filename = %q, want <session-id>.jsonl", result.Filename)
	}

	// Parse the emitted JSONL and verify the record chain
	var records []QwenRecord
	scanner := bufio.NewScanner(bytes.NewReader(result.Content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record QwenRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL line: %v\n%s", err, line)
		}
		records = append(records, record)
	}

	if len(records) < 2 {
		t.Fatalf("record count = %d, want at least a user and an assistant turn", len(records))
	}

	if records[0].Type != "user" || records[0].ParentUUID != "" {
		t.Errorf("first record should be a root user turn: %+v", records[0])
	}
	for i := 1; i < len(records); i++ {
		if records[i].ParentUUID != records[i-1].UUID {
			t.Errorf("record %d parentUuid = %q, want previous uuid %q", i, records[i].ParentUUID, records[i-1].UUID)
		}
		if records[i].SessionID != result.SessionID {
			t.Errorf("record %d sessionId = %q, want %q", i, records[i].SessionID, result.SessionID)
		}
	}

	// The transcript must round-trip through our own parser
	dir := t.TempDir()
	path := filepath.Join(dir, result.Filename)
	if err := os.WriteFile(path, result.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("reconstructed transcript failed to parse: %v", err)
	}
	if session.ID != result.SessionID {
		t.Errorf("round-trip session ID = %q, want %q", session.ID, result.SessionID)
	}
	if session.FirstRealUserText() != "add a divide function" {
		t.Errorf("round-trip first user text = %q", session.FirstRealUserText())
	}
}

func TestNativeSessionPath(t *testing.T) {
	home := withFakeHome(t)
	projectPath := t.TempDir()

	p := NewProvider()
	path, err := p.NativeSessionPath(projectPath, "abc.jsonl")
	if err != nil {
		t.Fatalf("NativeSessionPath failed: %v", err)
	}

	if filepath.Base(path) != "abc.jsonl" {
		t.Errorf("path basename = %q", filepath.Base(path))
	}
	if !strings.HasPrefix(path, filepath.Join(home, ".qwen", "projects")) {
		t.Errorf("path %q not under the qwen store", path)
	}
	// The chats directory must have been prepared
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("chats dir not created: %v", err)
	}
}
