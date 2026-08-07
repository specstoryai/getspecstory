package musecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// sourceSessionData builds a two-turn-pair SessionData to reconstruct from.
func sourceSessionData(workspaceRoot string) *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "claude", Name: "Claude Code", Version: "1.0.0"},
		SessionID:     "source-session-id",
		CreatedAt:     "2026-08-07T22:00:00.000Z",
		WorkspaceRoot: workspaceRoot,
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "source-session-id:0",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Read notes.txt"}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "It lists alpha, beta and gamma."}}},
				},
			},
			{
				ExchangeID: "source-session-id:1",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Now add sub() to calc.py"}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Added sub(a, b)."}}},
				},
			},
		},
	}
}

// writeReconstructed writes reconstructed bytes to a store-shaped path so the
// parser sees the same layout Muse would.
func writeReconstructed(t *testing.T, rec *spi.ReconstructedSession) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), rec.Filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rec.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReconstructSession_RoundTrip(t *testing.T) {
	provider := NewProvider()
	workspaceRoot := t.TempDir()

	rec, err := provider.ReconstructSession(sourceSessionData(workspaceRoot), spi.ReconstructOptions{
		WorkspaceRoot: workspaceRoot,
	})
	if err != nil {
		t.Fatalf("ReconstructSession failed: %v", err)
	}

	if rec.SessionID == "" {
		t.Error("SessionID is empty")
	}
	// Muse keys a session by directory, so the filename carries the directory.
	if rec.Filename != filepath.Join(rec.SessionID, sessionFileName) {
		t.Errorf("Filename = %q, want %q", rec.Filename, filepath.Join(rec.SessionID, sessionFileName))
	}

	path := writeReconstructed(t, rec)

	session, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("reconstructed transcript failed to parse: %v", err)
	}
	if session.ID != rec.SessionID {
		t.Errorf("parsed session ID = %q, want %q", session.ID, rec.SessionID)
	}
	if session.WorkspaceRoot != workspaceRoot {
		t.Errorf("parsed workspace root = %q, want %q", session.WorkspaceRoot, workspaceRoot)
	}
	if session.Version != reconstructedSemver {
		t.Errorf("parsed version = %q, want %q", session.Version, reconstructedSemver)
	}

	data, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		t.Fatalf("GenerateAgentSession on reconstructed transcript failed: %v", err)
	}
	if !data.Validate() {
		t.Error("reconstructed session data failed validation")
	}

	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2 (one run per user turn)", len(data.Exchanges))
	}
	expected := [][2]string{
		{"Read notes.txt", "It lists alpha, beta and gamma."},
		{"Now add sub() to calc.py", "Added sub(a, b)."},
	}
	for i, want := range expected {
		messages := data.Exchanges[i].Messages
		if len(messages) != 2 {
			t.Fatalf("exchange %d message count = %d, want 2", i, len(messages))
		}
		if got := messages[0].Content[0].Text; got != want[0] {
			t.Errorf("exchange %d user text = %q, want %q", i, got, want[0])
		}
		if got := messages[1].Content[0].Text; got != want[1] {
			t.Errorf("exchange %d agent text = %q, want %q", i, got, want[1])
		}
	}
}

func TestReconstructSession_RecordEnvelope(t *testing.T) {
	provider := NewProvider()
	workspaceRoot := t.TempDir()

	rec, err := provider.ReconstructSession(sourceSessionData(workspaceRoot), spi.ReconstructOptions{
		WorkspaceRoot: workspaceRoot,
	})
	if err != nil {
		t.Fatalf("ReconstructSession failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(rec.Content)), "\n")
	// metadata + (started + assistant + terminal) per turn pair.
	if len(lines) != 7 {
		t.Fatalf("record count = %d, want 7", len(lines))
	}

	var previousSequence, previousRecordedAt int64
	for i, line := range lines {
		var record MuseRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("record %d does not parse: %v", i, err)
		}
		if record.Stream.Kind != "session" || record.Stream.ID != rec.SessionID {
			t.Errorf("record %d stream = %+v, want session/%s", i, record.Stream, rec.SessionID)
		}
		if record.RecordType != "event" {
			t.Errorf("record %d record_type = %q, want event", i, record.RecordType)
		}
		if record.Sequence <= previousSequence {
			t.Errorf("record %d sequence = %d, not monotonic after %d", i, record.Sequence, previousSequence)
		}
		if record.RecordedAt <= previousRecordedAt {
			t.Errorf("record %d recorded_at = %d, not monotonic after %d", i, record.RecordedAt, previousRecordedAt)
		}
		// recorded_at is microseconds since the epoch, not milliseconds or
		// seconds: a value in any other unit converts to an absurd date.
		if age := time.Since(time.UnixMicro(record.RecordedAt)); age > time.Hour || age < -time.Hour {
			t.Errorf("record %d recorded_at %d is not a microsecond timestamp near now", i, record.RecordedAt)
		}
		previousSequence = record.Sequence
		previousRecordedAt = record.RecordedAt
	}

	// The first record carries the lineage back-link.
	var first MuseRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.PayloadType != payloadTypeMetadata {
		t.Fatalf("first record payload_type = %q, want %q", first.PayloadType, payloadTypeMetadata)
	}
	var payload struct {
		Record map[string]any `json:"record"`
	}
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if got, _ := payload.Record["specstorySourceSessionId"].(string); got != "source-session-id" {
		t.Errorf("lineage back-link = %v, want source-session-id", payload.Record["specstorySourceSessionId"])
	}
	if got, _ := payload.Record["provider_id"].(string); got != "meta" {
		t.Errorf("provider_id = %v, want meta", payload.Record["provider_id"])
	}
}

func TestReconstructSession_MigrationNoteOpensItsOwnRun(t *testing.T) {
	provider := NewProvider()
	workspaceRoot := t.TempDir()

	rec, err := provider.ReconstructSession(sourceSessionData(workspaceRoot), spi.ReconstructOptions{
		WorkspaceRoot: workspaceRoot,
		MigrationNote: "This conversation was imported from another agent.",
	})
	if err != nil {
		t.Fatalf("ReconstructSession failed: %v", err)
	}

	session, err := ParseSessionFile(writeReconstructed(t, rec))
	if err != nil {
		t.Fatalf("reconstructed transcript failed to parse: %v", err)
	}
	data, err := GenerateAgentSession(session, workspaceRoot)
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// The note is a leading agent turn with no user turn to belong to, so it
	// becomes its own exchange rather than being dropped.
	if len(data.Exchanges) != 3 {
		t.Fatalf("exchange count = %d, want 3 (note + two turn pairs)", len(data.Exchanges))
	}
	first := data.Exchanges[0].Messages
	if len(first) != 1 || first[0].Role != schema.RoleAgent {
		t.Fatalf("first exchange = %+v, want a single agent message", first)
	}
	if !strings.Contains(first[0].Content[0].Text, "imported from another agent") {
		t.Errorf("migration note missing: %q", first[0].Content[0].Text)
	}
}

func TestReconstructSession_Errors(t *testing.T) {
	provider := NewProvider()

	tests := []struct {
		name string
		data *schema.SessionData
	}{
		{name: "nil session data", data: nil},
		{
			name: "no content to reconstruct",
			data: &schema.SessionData{SchemaVersion: "1.0", SessionID: "empty", Exchanges: nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := provider.ReconstructSession(tt.data, spi.ReconstructOptions{}); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestNativeSessionPath(t *testing.T) {
	sessionsRoot := seedStore(t)
	provider := NewProvider()

	got, err := provider.NativeSessionPath(t.TempDir(), filepath.Join("new-session-id", sessionFileName))
	if err != nil {
		t.Fatalf("NativeSessionPath failed: %v", err)
	}

	now := time.Now()
	want := filepath.Join(sessionsRoot,
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", int(now.Month())),
		fmt.Sprintf("%02d", now.Day()),
		"new-session-id", sessionFileName)
	if got != want {
		t.Errorf("NativeSessionPath = %q, want %q", got, want)
	}

	// The caller creates the directory on its way to writing the file; probing
	// the path must not have side effects.
	if _, err := os.Stat(filepath.Dir(got)); !os.IsNotExist(err) {
		t.Errorf("NativeSessionPath created directories: %v", err)
	}
}

func TestSupportsReconstruction(t *testing.T) {
	if !NewProvider().SupportsReconstruction() {
		t.Error("SupportsReconstruction() = false, want true")
	}
}
