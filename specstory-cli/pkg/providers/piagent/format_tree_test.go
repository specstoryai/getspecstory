package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// These tests cover the pi session tree structure (branching + leaf selection),
// legacy session versions, and the tool-call-only assistant edge case — from
// https://pi.dev/docs/latest/session-format.

// TestFormatTree_BranchingLeafSelection covers the tree structure: when a
// session branches (one parent, two children), the parser walks the current
// leaf path — the LAST entry in file order — not the alternate branch.
func TestFormatTree_BranchingLeafSelection(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "branching.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasLeft, hasRight bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "left branch") {
					hasLeft = true
				}
				if strings.Contains(part.Text, "right branch") {
					hasRight = true
				}
			}
		}
	}
	if hasLeft {
		t.Error("alternate (non-leaf) branch was included; parser should walk only the leaf path")
	}
	if !hasRight {
		t.Error("leaf branch was dropped; parser should walk the last entry's path to root")
	}
}

// TestFormatTree_Version1Legacy covers version 1: a linear entry sequence where
// the header omits the version field. The parser must still map it
// (Provider.Version defaults to v1).
func TestFormatTree_Version1Legacy(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "v1_legacy.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if data.SessionID != "v1-uuid" {
		t.Errorf("SessionID = %q, want v1-uuid", data.SessionID)
	}
	if data.Provider.Version != "v1" {
		t.Errorf("Provider.Version = %q, want v1", data.Provider.Version)
	}
	if len(data.Exchanges) == 0 {
		t.Error("v1 session produced no exchanges")
	}
	if !data.Validate() {
		t.Error("Validate() returned false for v1 session")
	}
}

// TestFormatTree_ToolCallOnlyAssistantRetainsModelAndUsage covers an assistant
// message containing only toolCall blocks (no text/thinking): it must still
// carry model + usage on the tool message and emit no schema-invalid empty
// agent message.
func TestFormatTree_ToolCallOnlyAssistantRetainsModelAndUsage(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "toolonly.jsonl")
	session := `{"type":"session","version":3,"id":"toolonly-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"run ls","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"ls"}}],"provider":"fireworks","model":"glm","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var toolMsg *schema.Message
	for _, ex := range data.Exchanges {
		for i := range ex.Messages {
			if ex.Messages[i].Tool != nil {
				toolMsg = &ex.Messages[i]
			}
		}
	}
	if toolMsg == nil {
		t.Fatal("no tool message produced from tool-call-only assistant entry")
	}
	if toolMsg.Model != "glm" {
		t.Errorf("tool message Model = %q, want glm", toolMsg.Model)
	}
	if toolMsg.Usage == nil || toolMsg.Usage.InputTokens == 0 {
		t.Error("tool message did not retain usage from the assistant entry")
	}
	if !data.Validate() {
		t.Error("SessionData.Validate() returned false")
	}
}

// TestFormatTree_MultipleToolCallsGetDistinctMessageIDs asserts that when a
// single assistant message contains multiple toolCall blocks, each resulting
// tool Message gets a distinct ID (entry id + toolCall id), so downstream
// provenance keys that use msg.ID do not collide.
func TestFormatTree_MultipleToolCallsGetDistinctMessageIDs(t *testing.T) {
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

// TestFormatTree_ExchangeEndTimeReflectsFinalToolResult asserts that when a
// toolResult is the final event in an exchange, the exchange's EndTime is the
// toolResult's timestamp (downstream stats/telemetry read the last exchange's
// EndTime for the session end time). Uses an in-memory session whose last entry
// is a toolResult, so no later assistant message advances EndTime further.
func TestFormatTree_ExchangeEndTimeReflectsFinalToolResult(t *testing.T) {
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
