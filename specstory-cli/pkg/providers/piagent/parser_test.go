package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// loadFixture returns the absolute path to a testdata file.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

// TestParseSession_MapsRealPiV3Session asserts the pi JSONL v3 parser maps a
// real (trimmed) pi session into the unified schema.SessionData correctly.
// This is the RED test for the parser — it fails until ParseSession exists.
func TestParseSession_MapsRealPiV3Session(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "sample.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if data == nil {
		t.Fatal("ParseSession returned nil SessionData")
	}

	t.Run("header fields", func(t *testing.T) {
		if data.SchemaVersion != "1.0" {
			t.Errorf("SchemaVersion = %q, want %q", data.SchemaVersion, "1.0")
		}
		if data.Provider.ID != "pi" {
			t.Errorf("Provider.ID = %q, want %q", data.Provider.ID, "pi")
		}
		if data.Provider.Name == "" {
			t.Error("Provider.Name is empty")
		}
		if data.SessionID != "019f4836-1df4-79e7-83b0-57a808be0c71" {
			t.Errorf("SessionID = %q, want the header uuid", data.SessionID)
		}
		if data.CreatedAt == "" {
			t.Error("CreatedAt is empty")
		}
		if data.WorkspaceRoot == "" {
			t.Error("WorkspaceRoot is empty")
		}
	})

	t.Run("at least one exchange", func(t *testing.T) {
		if len(data.Exchanges) == 0 {
			t.Fatal("expected at least one exchange, got 0")
		}
	})

	t.Run("user message present with text", func(t *testing.T) {
		var foundUser bool
		for _, ex := range data.Exchanges {
			for _, msg := range ex.Messages {
				if msg.Role != schema.RoleUser {
					continue
				}
				foundUser = true
				if len(msg.Content) == 0 {
					t.Error("user message has no content parts")
				}
			}
		}
		if !foundUser {
			t.Error("no user message found in any exchange")
		}
	})

	t.Run("agent message has text and thinking parts", func(t *testing.T) {
		var hasText, hasThinking bool
		for _, ex := range data.Exchanges {
			for _, msg := range ex.Messages {
				if msg.Role != schema.RoleAgent || len(msg.Content) == 0 {
					continue
				}
				for _, part := range msg.Content {
					if part.Type == schema.ContentTypeText {
						hasText = true
					}
					if part.Type == schema.ContentTypeThinking {
						hasThinking = true
					}
				}
			}
		}
		if !hasText {
			t.Error("no agent text content part found")
		}
		if !hasThinking {
			t.Error("no agent thinking content part found")
		}
	})

	t.Run("tool call mapped to ToolInfo", func(t *testing.T) {
		var foundTool bool
		for _, ex := range data.Exchanges {
			for _, msg := range ex.Messages {
				if msg.Role != schema.RoleAgent || msg.Tool == nil {
					continue
				}
				foundTool = true
				if msg.Tool.Name != "bash" {
					t.Errorf("Tool.Name = %q, want %q", msg.Tool.Name, "bash")
				}
				if msg.Tool.Type != schema.ToolTypeShell {
					t.Errorf("Tool.Type = %q, want %q", msg.Tool.Type, schema.ToolTypeShell)
				}
				if msg.Tool.UseID == "" {
					t.Error("Tool.UseID is empty")
				}
			}
		}
		if !foundTool {
			t.Error("no agent message with a Tool found")
		}
	})

	t.Run("tool result merged into the tool call", func(t *testing.T) {
		var merged bool
		for _, ex := range data.Exchanges {
			for _, msg := range ex.Messages {
				if msg.Role != schema.RoleAgent || msg.Tool == nil {
					continue
				}
				if msg.Tool.Output != nil {
					merged = true
					if _, ok := msg.Tool.Output["content"]; !ok {
						t.Error("Tool.Output has no 'content' key")
					}
				}
			}
		}
		if !merged {
			t.Error("toolResult was not merged into the matching ToolInfo.Output")
		}
	})

	t.Run("token usage populated on agent messages", func(t *testing.T) {
		var foundUsage bool
		for _, ex := range data.Exchanges {
			for _, msg := range ex.Messages {
				if msg.Role != schema.RoleAgent || msg.Usage == nil {
					continue
				}
				foundUsage = true
				if msg.Usage.InputTokens == 0 {
					t.Error("Usage.InputTokens is 0")
				}
			}
		}
		if !foundUsage {
			t.Error("no agent message with Usage found")
		}
	})
}

// TestParseSession_ProviderVersionPopulated asserts Provider.Version is non-empty
// (schema.Validate() warns when it is empty) — addresses Copilot review comment.
func TestParseSession_ProviderVersionPopulated(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "sample.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	if data.Provider.Version == "" {
		t.Error("Provider.Version is empty; schema.Validate() requires it non-empty")
	}
	if !data.Validate() {
		t.Error("SessionData.Validate() returned false")
	}
}

// TestParseSession_CompactionHonorsFirstKeptEntryId asserts that when a
// compaction entry is on the leaf path, entries before firstKeptEntryId are
// dropped from the conversation. The fixture's compaction points at a1, so the
// pre-compaction user prompt u1 must NOT appear, while u2 (post-compaction) must.
func TestParseSession_CompactionHonorsFirstKeptEntryId(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPre, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role != schema.RoleUser {
				continue
			}
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
		t.Error("pre-compaction user prompt was not dropped by compaction")
	}
	if !hasPost {
		t.Error("post-compaction user prompt was dropped by compaction")
	}
}

// TestParseSession_CompactionMissingKeptIdDropsPreCompaction asserts the fallback
// path: when firstKeptEntryId is not found in the leaf path, pre-compaction
// entries are still dropped (kept from the compaction entry forward) rather than
// retained — addresses Copilot review comment on keepFromIndex returning 0.
func TestParseSession_CompactionMissingKeptIdDropsPreCompaction(t *testing.T) {
	data, err := ParseSession(loadFixture(t, "compaction_missing.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var hasPre, hasPost bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role != schema.RoleUser {
				continue
			}
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

// TestParseSession_ToolCallOnlyAssistantRetainsModelAndUsage asserts an
// assistant message containing only toolCall blocks (no text/thinking) still
// carries the model + usage on the tool message, and emits no schema-invalid
// empty agent message — addresses Copilot review comment.
func TestParseSession_ToolCallOnlyAssistantRetainsModelAndUsage(t *testing.T) {
	// Build an in-memory session with a tool-call-only assistant message.
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
		t.Errorf("tool message Model = %q, want %q", toolMsg.Model, "glm")
	}
	if toolMsg.Usage == nil || toolMsg.Usage.InputTokens == 0 {
		t.Error("tool message did not retain usage from the assistant entry")
	}
	if !data.Validate() {
		t.Error("SessionData.Validate() returned false (schema-invalid messages present)")
	}
}
