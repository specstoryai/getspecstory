package piagent

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// These tests verify field-level coverage of the pi session format
// (https://pi.dev/docs/latest/session-format) using the fields.jsonl fixture,
// which exercises every message role and content-block type WITHOUT a
// compaction entry (so the field-bearing entries survive into the exchanges).

func parseFields(t *testing.T) *schema.SessionData {
	t.Helper()
	data, err := ParseSession(loadFixture(t, "fields.jsonl"))
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	return data
}

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
// content block: v1 drops images, but the sibling text block must survive and
// the image must not cause a parse failure.
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
// provider, model, stopReason, errorMessage, and the full usage object
// (input/output/cacheRead/cacheWrite). a1 has thinking+text+toolCall; a2 has
// stopReason=error with an errorMessage.
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
}

// TestFormatFields_NonConversationRolesSkipped covers the message roles v1 does
// not map into exchanges: bashExecution, custom, branchSummary, compactionSummary.
// They must be skipped without producing schema-invalid messages.
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
