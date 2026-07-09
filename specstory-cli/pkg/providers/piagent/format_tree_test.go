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

// TestFormatTree_LatestCompactionWins asserts that when a session has multiple
// compaction entries on the leaf path, the LATEST (most recent) compaction's
// firstKeptEntryId determines the kept context — matching pi's buildContextEntries,
// where each new compaction summarizes prior compactions into itself. The fixture
// has c1 (firstKeptEntryId=a1) then c2 (firstKeptEntryId=u2); c2 must win, so a1
// is dropped and u2 onward survives.
func TestFormatTree_LatestCompactionWins(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "multi_compaction.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasA1, hasU2, hasU3 bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "answer one") {
					hasA1 = true
				}
				if strings.Contains(part.Text, "prompt two") {
					hasU2 = true
				}
				if strings.Contains(part.Text, "prompt three") {
					hasU3 = true
				}
			}
		}
	}
	if hasA1 {
		t.Error("a1 was retained — the oldest compaction (c1) won; the latest compaction (c2) should have dropped it")
	}
	if !hasU2 {
		t.Error("u2 (the latest compaction's firstKeptEntryId) was dropped")
	}
	if !hasU3 {
		t.Error("u3 (post-latest-compaction) was dropped")
	}
}
