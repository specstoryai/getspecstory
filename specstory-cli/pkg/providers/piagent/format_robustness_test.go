package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Robustness tests: the parser must not hang or drop error events on corrupted
// or edge-case real-world sessions.

// TestParse_ParentIdCycleTerminates asserts walkToRoot's visited guard prevents
// an infinite loop when a corrupted session has a parentId cycle. Without the
// guard, sync/reindex would hang on such a file.
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
// an agent text message (not dropped), so error events survive into the
// transcript — seen in real_world.jsonl entry 59c6b470.
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
