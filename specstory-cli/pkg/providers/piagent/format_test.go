package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Format-coverage tests for the pi session file format
// (https://pi.dev/docs/latest/session-format): entry types, field-level
// mapping, tree structure, and compaction. Fixtures live in testdata/.

// parseFields loads the fields.jsonl fixture (every message role + content-block
// type, NO compaction — so field-bearing entries survive into the exchanges).
func parseFields(t *testing.T) *schema.SessionData {
	t.Helper()
	data, err := ParseSession(loadFixture(t, "fields.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	return data
}

// ---- entry types / control entries / compaction ----

// TestFormatEntries_ControlEntriesSkipped covers the non-message entry types:
// model_change, thinking_level_change, session_info, label, custom, and
// custom_message. They must not break the parse and must not produce exchange
// messages (custom_message content is extension context, not a user turn).
func TestFormatEntries_ControlEntriesSkipped(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if !data.Validate() {
		t.Error("Validate() returned false; a control entry leaked an invalid message")
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "Injected context from extension") {
					t.Error("custom_message content leaked into exchanges as a user turn")
				}
				if strings.Contains(part.Text, "branch explored approach B") {
					t.Error("branch_summary content leaked into exchanges")
				}
			}
		}
	}
}

// TestFormatEntries_BranchSummaryEntry covers the top-level branch_summary entry
// (type:"branch_summary" with fromId, summary, details, fromHook). It must be
// skipped from exchanges without error.
func TestFormatEntries_BranchSummaryEntry(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "explored approach B") {
					t.Error("top-level branch_summary leaked into exchanges")
				}
			}
		}
	}
}

// TestFormatEntries_CompactionDropsPreKept covers the compaction entry's
// firstKeptEntryId field on full_format.jsonl: firstKeptEntryId=a2, so the
// common answer (before a2) is dropped and the post-compaction prompt survives.
func TestFormatEntries_CompactionDropsPreKept(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasCommon, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "common answer") {
					hasCommon = true
				}
				if strings.Contains(part.Text, "after compaction prompt") {
					hasPost = true
				}
			}
		}
	}
	if hasCommon {
		t.Error("entry before firstKeptEntryId was not dropped by compaction")
	}
	if !hasPost {
		t.Error("post-compaction prompt was dropped")
	}
}

// TestFormatEntries_RealWorldCompactionAndBashExecution uses a trimmed slice of
// a real pi session (testdata/real_world.jsonl) to assert the hard-to-synthesize
// format features: a compaction entry carrying details.readFiles/modifiedFiles +
// tokensBefore, a bashExecution message role (skipped from exchanges), and that
// compaction drops the pre-kept user prompt while keeping the post-compaction one.
func TestFormatEntries_RealWorldCompactionAndBashExecution(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "real_world.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if data.SessionID != "real-world-uuid" {
		t.Errorf("SessionID = %q, want real-world-uuid", data.SessionID)
	}
	if !data.Validate() {
		t.Error("Validate() returned false for the real-world session")
	}
	var hasPreCompaction, hasPostCompaction, hasBashExecContent bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "Read README.md and NOTES.md") {
					hasPreCompaction = true
				}
				if strings.Contains(part.Text, "summarize it now") {
					hasPostCompaction = true
				}
				if strings.Contains(part.Text, "total 24") {
					hasBashExecContent = true
				}
			}
		}
	}
	if hasPreCompaction {
		t.Error("pre-compaction user prompt was not dropped by compaction")
	}
	if !hasPostCompaction {
		t.Error("post-compaction user prompt was dropped by compaction")
	}
	if hasBashExecContent {
		t.Error("bashExecution content leaked into exchanges (should be skipped)")
	}
}

// TestFormatEntries_CompactionHonorsFirstKeptEntryId asserts that when a
// compaction entry is on the leaf path, entries before firstKeptEntryId are
// dropped and entries from firstKeptEntryId forward are kept.
func TestFormatEntries_CompactionHonorsFirstKeptEntryId(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasDroppedPre, hasKept, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				switch {
				case strings.Contains(part.Text, "first prompt before compaction"):
					hasDroppedPre = true
				case strings.Contains(part.Text, "first answer before compaction"):
					hasKept = true
				case strings.Contains(part.Text, "after compaction"):
					hasPost = true
				}
			}
		}
	}
	if hasDroppedPre {
		t.Error("pre-kept user prompt (u1) was not dropped by compaction")
	}
	if !hasKept {
		t.Error("kept entry (a1, firstKeptEntryId) was dropped")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped by compaction")
	}
}

// TestFormatEntries_CompactionMissingKeptIdDropsPreCompaction asserts the
// fallback: when firstKeptEntryId is not found in the leaf path, pre-compaction
// entries are still dropped (kept from the compaction entry forward).
func TestFormatEntries_CompactionMissingKeptIdDropsPreCompaction(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction_missing.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPre, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "before compaction") {
					hasPre = true
				}
				if strings.Contains(part.Text, "after compaction") {
					hasPost = true
				}
			}
		}
	}
	if hasPre {
		t.Error("pre-compaction user prompt retained when firstKeptEntryId was missing")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped in the missing-kept-id fallback")
	}
}

// ---- field-level coverage ----

// TestFormatFields_SessionHeader covers the session header: id, timestamp, cwd,
// version, and the optional parentSession field (fork/clone marker).
func TestFormatFields_SessionHeader(t *testing.T) {
	data := parseFields(t)
	if data.SessionID != "fields-uuid" {
		t.Errorf("SessionID = %q, want fields-uuid", data.SessionID)
	}
	if data.CreatedAt != "2026-07-09T10:00:00.000Z" {
		t.Errorf("CreatedAt = %q", data.CreatedAt)
	}
	if data.WorkspaceRoot != "/test/proj" {
		t.Errorf("WorkspaceRoot = %q, want /test/proj", data.WorkspaceRoot)
	}
	if data.Provider.Version != "v3" {
		t.Errorf("Provider.Version = %q, want v3", data.Provider.Version)
	}
}

// TestFormatFields_UserMessageStringContent covers UserMessage.content as a
// plain string (mapped to a single text content part).
func TestFormatFields_UserMessageStringContent(t *testing.T) {
	data := parseFields(t)
	var found bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role == schema.RoleUser && len(msg.Content) == 1 &&
				msg.Content[0].Text == "hello as a plain string" {
				found = true
			}
		}
	}
	if !found {
		t.Error("user message with string content was not mapped to a single text part")
	}
}

// TestFormatFields_UserMessageImageSkipped covers a user message with an image
// content block: v1 drops images, but the sibling text block must survive.
func TestFormatFields_UserMessageImageSkipped(t *testing.T) {
	data := parseFields(t)
	var foundText bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role != schema.RoleUser {
				continue
			}
			for _, part := range msg.Content {
				if part.Text == "hello as array" {
					foundText = true
				}
			}
		}
	}
	if !foundText {
		t.Error("text block alongside an image block was dropped")
	}
}

// TestFormatFields_AssistantAllFields covers the assistant message: api,
// provider, model, stopReason, errorMessage, and the full usage object.
func TestFormatFields_AssistantAllFields(t *testing.T) {
	data := parseFields(t)
	var a1, a2 *schema.Message
	for _, ex := range data.Exchanges {
		for i := range ex.Messages {
			m := &ex.Messages[i]
			if m.Role != schema.RoleAgent || m.Tool != nil || len(m.Content) == 0 {
				continue
			}
			if m.Content[0].Type == schema.ContentTypeThinking {
				a1 = m
			}
			if m.Model == "glm-1" && len(m.Content) == 1 && m.Content[0].Text == "done" {
				a2 = m
			}
		}
	}
	if a1 == nil {
		t.Fatal("assistant message with thinking not found")
	}
	if a1.Model != "glm-1" {
		t.Errorf("a1 Model = %q, want glm-1", a1.Model)
	}
	if a1.Usage == nil {
		t.Fatal("a1 Usage nil")
	}
	if a1.Usage.InputTokens != 100 || a1.Usage.OutputTokens != 50 {
		t.Errorf("a1 usage input/output = %d/%d, want 100/50", a1.Usage.InputTokens, a1.Usage.OutputTokens)
	}
	if a1.Usage.CacheReadInputTokens != 10 || a1.Usage.CacheCreationInputTokens != 5 {
		t.Errorf("a1 cache read/create = %d/%d, want 10/5", a1.Usage.CacheReadInputTokens, a1.Usage.CacheCreationInputTokens)
	}
	if a2 == nil {
		t.Fatal("assistant message with stopReason=error not found")
	}
	if a2.Usage == nil || a2.Usage.OutputTokens != 20 {
		t.Errorf("a2 output tokens = %v, want 20", a2.Usage)
	}
}

// TestFormatFields_ToolResultFields covers the toolResult message: toolCallId,
// toolName, content (text), isError, and details. The result must merge into the
// matching tool message's ToolInfo.Output keyed by toolCallId.
func TestFormatFields_ToolResultFields(t *testing.T) {
	data := parseFields(t)
	var tool *schema.Message
	for _, ex := range data.Exchanges {
		for i := range ex.Messages {
			if ex.Messages[i].Tool != nil && ex.Messages[i].Tool.UseID == "call-1" {
				tool = &ex.Messages[i]
			}
		}
	}
	if tool == nil {
		t.Fatal("tool message with UseID call-1 not found")
	}
	if tool.Tool.Name != "bash" || tool.Tool.Type != schema.ToolTypeShell {
		t.Errorf("tool name/type = %q/%q, want bash/shell", tool.Tool.Name, tool.Tool.Type)
	}
	if tool.Tool.Output == nil {
		t.Fatal("tool output not merged from toolResult")
	}
	content, _ := tool.Tool.Output["content"].(string)
	if content != "file1\nfile2" {
		t.Errorf("tool output content = %q, want file1\\nfile2", content)
	}
	if isErr, _ := tool.Tool.Output["is_error"].(bool); isErr {
		t.Error("tool output is_error = true, want false")
	}
	details, hasDetails := tool.Tool.Output["details"]
	if !hasDetails {
		t.Fatal("tool output missing 'details' — toolResult details were dropped")
	}
	detMap, ok := details.(map[string]any)
	if !ok {
		t.Fatalf("details is %T, want map[string]any", details)
	}
	if exitCode, _ := detMap["exitCode"].(float64); exitCode != 0 {
		t.Errorf("details.exitCode = %v, want 0", detMap["exitCode"])
	}
}

// TestFormatFields_NonConversationRolesSkipped covers the message roles v1 does
// not map into exchanges: bashExecution, custom, branchSummary, compactionSummary.
func TestFormatFields_NonConversationRolesSkipped(t *testing.T) {
	data := parseFields(t)
	if !data.Validate() {
		t.Error("Validate() returned false; a non-conversation role leaked an invalid message")
	}
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				for _, marker := range []string{"echo hi", "extension content", "explored approach A", "compacted earlier"} {
					if strings.Contains(part.Text, marker) {
						t.Errorf("non-conversation role content leaked into exchange: %q", part.Text)
					}
				}
			}
		}
	}
}

// ---- tree structure / versions ----

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
// the header omits the version field. The parser must still map it.
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
// compaction entries on the leaf path, the LATEST compaction's firstKeptEntryId
// determines the kept context (matching pi's buildContextEntries).
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
		t.Error("a1 was retained — the oldest compaction won; the latest should have dropped it")
	}
	if !hasU2 {
		t.Error("u2 (the latest compaction's firstKeptEntryId) was dropped")
	}
	if !hasU3 {
		t.Error("u3 (post-latest-compaction) was dropped")
	}
}

// TestFormatTree_UserOnlyExchangeHasEndTime asserts that an exchange containing
// only a user message (session ends right after a prompt) still gets an EndTime
// from the user message timestamp.
func TestFormatTree_UserOnlyExchangeHasEndTime(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "useronly.jsonl")
	session := `{"type":"session","version":3,"id":"useronly-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:05.000Z","message":{"role":"user","content":"orphan prompt","timestamp":1783600005000}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if len(data.Exchanges) != 1 {
		t.Fatalf("want 1 exchange, got %d", len(data.Exchanges))
	}
	ex := data.Exchanges[0]
	if ex.EndTime != "2026-07-09T10:00:05.000Z" {
		t.Errorf("user-only exchange EndTime = %q, want the user message timestamp", ex.EndTime)
	}
}
