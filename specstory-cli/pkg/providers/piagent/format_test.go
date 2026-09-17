package piagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
// custom_message. All six sit on the live leaf path of full_format.jsonl (the
// compaction there no longer truncates the transcript), so this genuinely
// exercises the skip logic: they must not break the parse and must not produce
// exchange messages (custom_message content is extension context, not a user
// turn).
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

// TestFormatEntries_CompactionKeepsHistoryAndSummary covers the compaction
// entry on full_format.jsonl: unlike pi's context building (which drops
// entries before firstKeptEntryId to fit the LLM window), the transcript keeps
// the full leaf path AND renders the compaction summary as a marker message.
func TestFormatEntries_CompactionKeepsHistoryAndSummary(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "full_format.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPre, hasPost, hasSummary bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "hello as a plain string") {
					hasPre = true
				}
				if strings.Contains(part.Text, "after compaction prompt") {
					hasPost = true
				}
				if strings.Contains(part.Text, "final compaction") {
					hasSummary = true
				}
			}
		}
	}
	if !hasPre {
		t.Error("pre-compaction history was dropped; the full transcript must be preserved")
	}
	if !hasPost {
		t.Error("post-compaction prompt was dropped")
	}
	if !hasSummary {
		t.Error("compaction summary was not rendered as a marker message")
	}
}

// TestFormatEntries_RealWorldCompactionAndBashExecution uses a trimmed slice of
// a real pi session (testdata/real_world.jsonl) to assert the hard-to-synthesize
// format features: the pre-compaction history is PRESERVED in the transcript,
// the compaction summary is rendered as a marker, and the bashExecution message
// role on the live leaf path is skipped (it sits before the compaction entry,
// which no longer truncates the path — so this skip is genuinely exercised).
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
	var hasPreCompaction, hasPostCompaction, hasSummary, hasBashExecContent bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "Read README.md and NOTES.md") {
					hasPreCompaction = true
				}
				if strings.Contains(part.Text, "summarize it now") {
					hasPostCompaction = true
				}
				if strings.Contains(part.Text, "## Goal") {
					hasSummary = true
				}
				if strings.Contains(part.Text, "total 24") {
					hasBashExecContent = true
				}
			}
		}
	}
	if !hasPreCompaction {
		t.Error("pre-compaction user prompt was dropped; the full transcript must be preserved")
	}
	if !hasPostCompaction {
		t.Error("post-compaction user prompt was dropped")
	}
	if !hasSummary {
		t.Error("compaction summary was not rendered as a marker message")
	}
	if hasBashExecContent {
		t.Error("bashExecution content leaked into exchanges (should be skipped)")
	}
}

// TestFormatEntries_CompactionPreservesAllEntries asserts that every entry on
// the leaf path survives a compaction (pre-kept, kept, and post-compaction)
// and the summary marker is rendered at the compaction point.
func TestFormatEntries_CompactionPreservesAllEntries(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPrePrompt, hasPreAnswer, hasSummary, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				switch {
				case strings.Contains(part.Text, "first prompt before compaction"):
					hasPrePrompt = true
				case strings.Contains(part.Text, "first answer before compaction"):
					hasPreAnswer = true
				case strings.Contains(part.Text, "summarized earlier turns"):
					hasSummary = true
				case strings.Contains(part.Text, "after compaction"):
					hasPost = true
				}
			}
		}
	}
	if !hasPrePrompt || !hasPreAnswer {
		t.Errorf("pre-compaction entries dropped (prompt kept=%v, answer kept=%v); the full transcript must be preserved", hasPrePrompt, hasPreAnswer)
	}
	if !hasSummary {
		t.Error("compaction summary was not rendered as a marker message")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped")
	}
}

// TestFormatEntries_CompactionDanglingKeptIdHarmless asserts a compaction whose
// firstKeptEntryId does not exist on the path parses cleanly: the transcript
// keeps everything regardless, and the summary marker still renders.
func TestFormatEntries_CompactionDanglingKeptIdHarmless(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction_missing.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPre, hasSummary, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "prompt before compaction") {
					hasPre = true
				}
				if strings.Contains(part.Text, "summarized earlier turns") {
					hasSummary = true
				}
				if strings.Contains(part.Text, "prompt after compaction") {
					hasPost = true
				}
			}
		}
	}
	if !hasPre {
		t.Error("pre-compaction user prompt was dropped; the full transcript must be preserved")
	}
	if !hasSummary {
		t.Error("compaction summary was not rendered as a marker message")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped")
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
	if data.Provider.Version != "unknown" {
		t.Errorf("Provider.Version = %q, want unknown", data.Provider.Version)
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
// content block: v1 drops images, but the adjacent text block must survive.
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

// TestFormatTree_Version1Legacy covers a REAL unmigrated v1 file (as written
// by pre-tree pi versions): the header omits the version field, entries have
// NO id/parentId (pi's migrateV1ToV2 synthesizes them at load), and compaction
// uses firstKeptEntryIndex. The parser must synthesize the linear chain and
// map the full conversation.
func TestFormatTree_Version1Legacy(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "v1_legacy.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if data.SessionID != "v1-uuid" {
		t.Errorf("SessionID = %q, want v1-uuid", data.SessionID)
	}
	if data.Provider.Version != "unknown" {
		t.Errorf("Provider.Version = %q, want unknown", data.Provider.Version)
	}
	var hasFirst, hasSecond, hasSummary bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "legacy prompt") {
					hasFirst = true
				}
				if strings.Contains(part.Text, "legacy answer two") {
					hasSecond = true
				}
				if strings.Contains(part.Text, "legacy compaction summary") {
					hasSummary = true
				}
			}
		}
	}
	if !hasFirst || !hasSecond {
		t.Errorf("v1 conversation dropped (first=%v second=%v); id-less linear entries must be chained", hasFirst, hasSecond)
	}
	if !hasSummary {
		t.Error("v1 compaction summary was not rendered")
	}
	if !data.Validate() {
		t.Error("Validate() returned false for v1 session")
	}

	// The scan path must also handle id-less v1 files so list/reindex and sync
	// agree on the session's existence and slug.
	scan, scanErr := scanPiSession(loadFixture(t, "v1_legacy.jsonl"))
	if scanErr != nil {
		t.Fatalf("scanPiSession returned error for v1 file: %v", scanErr)
	}
	if scan == nil || !scan.foundUser {
		t.Fatal("scanPiSession did not find the v1 first user message")
	}
	if scan.firstUserMessage != "legacy prompt" {
		t.Errorf("scan firstUserMessage = %q, want legacy prompt", scan.firstUserMessage)
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

// TestFormatTree_MultipleCompactionsAllRendered asserts that a session with
// multiple compaction entries keeps the entire history and renders each
// compaction's summary as its own marker.
func TestFormatTree_MultipleCompactionsAllRendered(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "multi_compaction.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasA1, hasU2, hasU3, hasFirstSummary, hasSecondSummary bool
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
				if strings.Contains(part.Text, "first compaction") {
					hasFirstSummary = true
				}
				if strings.Contains(part.Text, "second compaction") {
					hasSecondSummary = true
				}
			}
		}
	}
	if !hasA1 || !hasU2 || !hasU3 {
		t.Errorf("history dropped across compactions (a1=%v u2=%v u3=%v); the full transcript must be preserved", hasA1, hasU2, hasU3)
	}
	if !hasFirstSummary || !hasSecondSummary {
		t.Errorf("compaction summaries missing (first=%v second=%v); each should render as a marker", hasFirstSummary, hasSecondSummary)
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

func TestToolMarkdownPreservesNestedFences(t *testing.T) {
	content := "before\n````markdown\n```text\ninner\n```\n````\nafter"
	for _, name := range []string{"bash", "write"} {
		_, got := formatToolMarkdown(&schema.ToolInfo{Name: name, Input: map[string]interface{}{"path": "notes.md", "command": content, "content": content}, Output: map[string]interface{}{"content": content}})
		if !strings.Contains(got, content) || !strings.Contains(got, "`````\n") {
			t.Fatalf("unsafe fence: %s", got)
		}
	}
}

// The inventory and parameter shapes were captured from Pi 0.85.1's
// pi.getAllTools() declaration, including the Windows-only PowerShell tool.
func TestNativeToolInventoryAndRendering(t *testing.T) {
	tests := []struct {
		name, kind, input string
		want              []string
	}{
		{"read", schema.ToolTypeRead, `{"path":"main.go","offset":7,"limit":20}`, []string{"Path", "main.go", "Offset (line)", "7", "Limit", "20", "```go\nresult"}},
		{"bash", schema.ToolTypeShell, `{"command":"echo hi","timeout":12}`, []string{"```bash\necho hi", "Timeout (seconds)", "12", "```text\nresult"}},
		{"powershell", schema.ToolTypeShell, `{"command":"Get-Content main.go","timeout":13}`, []string{"```powershell\nGet-Content main.go", "13", "```text\nresult"}},
		{"edit", schema.ToolTypeWrite, `{"path":"main.go","edits":[{"oldText":"old","newText":"new","future":true}]}`, []string{"```diff\n-old\n+new", `"future": true`}},
		{"write", schema.ToolTypeWrite, `{"path":"main.go","content":"package main"}`, []string{"```go\npackage main"}},
		{"grep", schema.ToolTypeSearch, `{"pattern":"TODO","path":"src","glob":"*.go","ignoreCase":true,"literal":false,"context":2,"limit":9}`, []string{"Pattern", "TODO", "src", "Glob", "*.go", "Ignore case", "true", "Literal", "false", "Context (lines)", "2", "Limit", "9"}},
		{"find", schema.ToolTypeSearch, `{"pattern":"*.go","path":"src","limit":11}`, []string{"Pattern", "*.go", "Path", "src", "Limit", "11"}},
		{"ls", schema.ToolTypeRead, `{"limit":17}`, []string{"Limit", "17"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyToolType(tt.name); got != tt.kind {
				t.Fatalf("classification = %q, want %q", got, tt.kind)
			}
			var input map[string]any
			if err := json.Unmarshal([]byte(tt.input), &input); err != nil {
				t.Fatal(err)
			}
			input["newOption"] = map[string]any{"z": 2, "a": 1}
			tool := &schema.ToolInfo{Name: tt.name, Input: input, Output: map[string]any{"content": "result", "is_error": false, "details": map[string]any{"futureResult": "kept"}}}
			_, got := formatToolMarkdown(tool)
			for _, want := range append(tt.want, `"newOption"`, `"futureResult": "kept"`) {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			_, again := formatToolMarkdown(tool)
			if got != again {
				t.Error("rendering is not deterministic")
			}
		})
	}
	for _, name := range []string{"web_search", "fetch_content", "until_done_plan", "future_tool"} {
		if classifyToolType(name) != schema.ToolTypeUnknown {
			t.Errorf("unverified tool %q has bespoke classification", name)
		}
		_, got := formatToolMarkdown(&schema.ToolInfo{Name: name, Input: map[string]any{"weird": []any{"retained"}}, Output: map[string]any{"answer": 42}})
		if !strings.Contains(got, "retained") || !strings.Contains(got, `"answer": 42`) {
			t.Errorf("generic tool lost data: %s", got)
		}
	}
}

func TestEditNativeResultAndErrorPrecedence(t *testing.T) {
	// details is the result of executing Pi 0.85.1 createEditTool on a scratch file.
	var output map[string]any
	if err := json.Unmarshal([]byte(`{"content":"Successfully replaced 1 block(s) in native-edit.txt.","is_error":false,"details":{"diff":"-1 old\n+1 new","patch":"--- native-edit.txt\n+++ native-edit.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n","firstChangedLine":1}}`), &output); err != nil {
		t.Fatal(err)
	}
	tool := &schema.ToolInfo{Name: "edit", Input: map[string]any{"path": "native-edit.txt"}, Output: output}
	_, got := formatToolMarkdown(tool)
	for _, want := range []string{"```diff\n-1 old\n+1 new", "```diff\n--- native-edit.txt", `"firstChangedLine": 1`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	output["is_error"] = true
	output["content"] = "Could not find unique match"
	_, got = formatToolMarkdown(tool)
	if !strings.HasPrefix(got, "**Error:**") || !strings.Contains(got, "Could not find unique match") || strings.Contains(got, "```diff") || !strings.Contains(got, `"patch"`) || !strings.Contains(got, "native-edit.txt") {
		t.Errorf("error was success-formatted or lost diagnostics: %s", got)
	}
}

func TestToolOutputSanitizationAndBounds(t *testing.T) {
	for _, name := range []string{"bash", "powershell"} {
		tool := &schema.ToolInfo{Name: name, Input: map[string]any{"command": "echo \"```\"\n" + strings.Repeat("x", 6000)}, Output: map[string]any{"content": "\x1b[31mred\x1b[0m\x00\a\r\n\tline\n" + strings.Repeat("界", 6000), "details": map[string]any{"huge": strings.Repeat("x", 6000)}}}
		original, _ := json.Marshal(tool)
		_, got := formatToolMarkdown(tool)
		if strings.ContainsAny(got, "\x1b\x00\a\r") || !strings.Contains(got, "```text\nred\n\tline") || !strings.Contains(got, "… (output truncated)") || !strings.Contains(got, strings.Repeat("x", 6000)) || !strings.HasSuffix(got, "```") {
			t.Errorf("unsafe or incomplete rendering: %.250s", got)
		}
		after, _ := json.Marshal(tool)
		if string(original) != string(after) {
			t.Error("rendering modified native input/output")
		}
	}
}

func TestAssistantInterleavedNarrationOrder(t *testing.T) {
	e := rawEntry{ID: "a1", Timestamp: "2026-09-16T12:00:00Z", Message: json.RawMessage(`{"role":"assistant","model":"native-model","usage":{"input":12,"output":8},"content":[{"type":"text","text":"Before"},{"type":"toolCall","id":"first","name":"read","arguments":{"path":"a.go"}},{"type":"text","text":"After"},{"type":"thinking","thinking":"Reasoning"},{"type":"toolCall","id":"second","name":"ls","arguments":{}},{"type":"text","text":"Done"}]}`)}
	messages := buildAgentMessages(e)
	var order []string
	seen := map[string]bool{}
	usages := 0
	for _, msg := range messages {
		if msg.ID == "" || seen[msg.ID] {
			t.Fatalf("duplicate/empty message ID: %q", msg.ID)
		}
		seen[msg.ID] = true
		if msg.Model != "native-model" {
			t.Errorf("model missing on %q", msg.ID)
		}
		if msg.Usage != nil {
			usages++
			if msg.Usage.InputTokens != 12 || msg.Usage.OutputTokens != 8 {
				t.Errorf("wrong usage: %+v", msg.Usage)
			}
		}
		if msg.Tool != nil {
			order = append(order, msg.Tool.UseID)
		}
		for _, part := range msg.Content {
			order = append(order, part.Text)
		}
	}
	if want := []string{"Before", "first", "After", "Reasoning", "second", "Done"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v, want %v", order, want)
	}
	if usages != 1 {
		t.Fatalf("usage assigned %d times", usages)
	}
	if !reflect.DeepEqual(messages, buildAgentMessages(e)) {
		t.Fatal("message IDs or contents are unstable")
	}
	// Missing call IDs must not collide with each other or with narration IDs.
	e.Message = json.RawMessage(`{"role":"assistant","content":[{"type":"toolCall","name":"read"},{"type":"text","text":"middle"},{"type":"toolCall","name":"ls"}]}`)
	messages = buildAgentMessages(e)
	if len(messages) != 3 || messages[0].ID == messages[1].ID || messages[0].ID == messages[2].ID || messages[1].ID == messages[2].ID {
		t.Fatalf("colliding IDs: %+v", messages)
	}
}
