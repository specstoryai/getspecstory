package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Robustness + edge-case tests: the parser must handle large lines, distinct
// tool-call IDs, EndTime on tool-final exchanges, parentId cycles (no hang),
// dropped assistant error events, and the scan path's slug/error contracts.

// TestFormatEdge_MultipleToolCallsGetDistinctMessageIDs asserts that when a
// single assistant message contains multiple toolCall blocks, each resulting
// tool Message gets a distinct ID (entry id + toolCall id), so downstream
// provenance keys that use msg.ID do not collide.
func TestFormatEdge_MultipleToolCallsGetDistinctMessageIDs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multitool.jsonl")
	session := `{"type":"session","version":3,"id":"multi-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-a","name":"read","arguments":{"path":"a.go"}},{"type":"toolCall","id":"call-b","name":"read","arguments":{"path":"b.go"}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var ids []string
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Tool != nil {
				ids = append(ids, msg.ID)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 tool messages, got %d", len(ids))
	}
	if ids[0] == ids[1] {
		t.Errorf("tool message IDs collide: both %q", ids[0])
	}
	if ids[0] != "a1:call-a" || ids[1] != "a1:call-b" {
		t.Errorf("tool IDs = %q, %q; want a1:call-a, a1:call-b", ids[0], ids[1])
	}
}

// TestFormatEdge_ExchangeEndTimeReflectsFinalToolResult asserts that when a
// toolResult is the final event in an exchange, the exchange's EndTime is the
// toolResult's timestamp.
func TestFormatEdge_ExchangeEndTimeReflectsFinalToolResult(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "endtime.jsonl")
	session := `{"type":"session","version":3,"id":"endtime-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"ls"}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
` +
		`{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-07-09T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"bash","content":[{"type":"text","text":"out"}],"isError":false,"timestamp":1783600003000}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	last := data.Exchanges[len(data.Exchanges)-1]
	if last.EndTime != "2026-07-09T10:00:03.000Z" {
		t.Errorf("last exchange EndTime = %q, want the toolResult tr1 timestamp", last.EndTime)
	}
}

// TestParse_LargeLineOverScannerCap proves the reader handles lines larger than
// bufio.Scanner's 16MB cap (bufio.Reader.ReadString has no line-size limit).
func TestParse_LargeLineOverScannerCap(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "largeline.jsonl")
	big := strings.Repeat("x", 17*1024*1024) // 17MB > 16MB Scanner cap
	session := `{"type":"session","version":3,"id":"large-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"cat big"}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
` +
		`{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-07-09T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"bash","content":[{"type":"text","text":"` + big + `"}],"isError":false,"timestamp":1783600003000}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error for a >16MB line: %v", err)
	}
	var found bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Tool != nil && msg.Tool.UseID == "call-1" && msg.Tool.Output != nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("tool result with >16MB content was not parsed")
	}
}

// TestParse_ParentIdCycleTerminates asserts walkToRoot's visited guard prevents
// an infinite loop when a corrupted session has a parentId cycle.
func TestParse_ParentIdCycleTerminates(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cycle.jsonl")
	// a -> b -> a (cycle). Leaf is a.
	session := `{"type":"session","version":3,"id":"cycle-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"a","parentId":"b","timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"a","timestamp":1783600001000}}
` +
		`{"type":"message","id":"b","parentId":"a","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"b"}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := ParseSession(path); err != nil {
			t.Errorf("ParseSession returned error: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ParseSession hung on a parentId cycle (visited guard missing)")
	}
}

// TestParse_AssistantErrorMessageSurfaced asserts an assistant entry with
// stopReason=error, an errorMessage, and an empty content array is surfaced as
// an agent text message (not dropped) — seen in real_world.jsonl entry 59c6b470.
func TestParse_AssistantErrorMessageSurfaced(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "errmsg.jsonl")
	session := `{"type":"session","version":3,"id":"errmsg-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[],"provider":"x","model":"m","stopReason":"error","errorMessage":"rate limited","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var found bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role == schema.RoleAgent && len(msg.Content) > 0 && strings.Contains(msg.Content[0].Text, "rate limited") {
				found = true
			}
		}
	}
	if !found {
		t.Error("assistant error message was dropped — expected an agent message with [error] rate limited")
	}
	if !data.Validate() {
		t.Error("SessionData.Validate() returned false")
	}
}

// ---- scan path ----

// TestScan_PopulatesSlugAndName asserts scanPiSession derives Slug/Name from the
// first user message in a bounded single pass (no full parse).
func TestScan_PopulatesSlugAndName(t *testing.T) {
	scan, err := scanPiSession(loadFixture(t, "fields.jsonl"))
	if err != nil {
		t.Fatalf("scanPiSession returned error: %v", err)
	}
	if scan == nil || !scan.foundUser {
		t.Fatal("scan did not find a user message")
	}
	if scan.firstUserMessage == "" {
		t.Error("firstUserMessage is empty")
	}
	ref := scanToGlobalRef(scan, "fields.jsonl")
	if ref == nil {
		t.Fatal("scanToGlobalRef returned nil")
	}
	if ref.Slug == "" {
		t.Error("ref.Slug is empty")
	}
	if ref.Name == "" {
		t.Error("ref.Name is empty")
	}
}

// TestScan_NonSessionFileReturnsNilNil asserts scanPiSession returns (nil, nil)
// for a file whose first line is not a pi session header, so ScanSessionsInParallel
// skips it silently (without logging an error).
func TestScan_NonSessionFileReturnsNilNil(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "notsession.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"x","parentId":null,"timestamp":"2026-07-09T10:00:00.000Z","message":{"role":"user","content":"hi"}}
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scan, err := scanPiSession(path)
	if err != nil {
		t.Fatalf("scanPiSession returned error for non-session file: %v", err)
	}
	if scan != nil {
		t.Errorf("scanPiSession returned %v for non-session file, want nil", scan)
	}
}

// TestScan_UnreadableFileReturnsError asserts scanPiSession returns a non-nil
// error when the file cannot be opened, so ScanSessionsInParallel logs it.
func TestScan_UnreadableFileReturnsError(t *testing.T) {
	_, err := scanPiSession(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("scanPiSession returned nil error for a missing file")
	}
}
