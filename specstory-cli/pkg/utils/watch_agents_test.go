package utils

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// sessionWith builds a minimal session whose single exchange holds the given messages.
func sessionWith(messages ...schema.Message) *spi.AgentChatSession {
	return &spi.AgentChatSession{
		SessionID: "s-1",
		SessionData: &schema.SessionData{
			Exchanges: []schema.Exchange{{Messages: messages}},
		},
	}
}

func textMsg(role, text string) schema.Message {
	return schema.Message{Role: role, Content: []schema.ContentPart{{Type: "text", Text: text}}}
}

// TestFingerprintSession verifies the dedup fingerprint distinguishes in-place
// content growth from true duplicates. Patch-log providers (VS Code Copilot)
// stream a response by growing an existing message's text, so the message count
// alone must not be the only signal.
func TestFingerprintSession(t *testing.T) {
	base := sessionWith(textMsg("user", "question"), textMsg("agent", "partial"))

	t.Run("identical sessions produce equal fingerprints", func(t *testing.T) {
		other := sessionWith(textMsg("user", "question"), textMsg("agent", "partial"))
		if fingerprintSession(base) != fingerprintSession(other) {
			t.Error("expected equal fingerprints for identical content")
		}
	})

	t.Run("text growth in existing message changes fingerprint", func(t *testing.T) {
		grown := sessionWith(textMsg("user", "question"), textMsg("agent", "partial plus streamed continuation"))
		if fingerprintSession(base) == fingerprintSession(grown) {
			t.Error("expected fingerprint to change when message text grows with same count")
		}
	})

	t.Run("same-length in-place edit changes fingerprint", func(t *testing.T) {
		// Length and count alone can't see this change — only the content hash can.
		edited := sessionWith(textMsg("user", "question"), textMsg("agent", "PARTIAL"))
		if fingerprintSession(base) == fingerprintSession(edited) {
			t.Error("expected fingerprint to change for a same-length content edit")
		}
	})

	t.Run("added message changes fingerprint", func(t *testing.T) {
		more := sessionWith(textMsg("user", "question"), textMsg("agent", "partial"), textMsg("user", "next"))
		if fingerprintSession(base) == fingerprintSession(more) {
			t.Error("expected fingerprint to change when a message is added")
		}
	})

	t.Run("tool markdown growth changes fingerprint", func(t *testing.T) {
		short, long := "**Input:** a", "**Input:** a\n**Result:** long output"
		withTool := func(md string) *spi.AgentChatSession {
			return sessionWith(schema.Message{Role: "agent", Tool: &schema.ToolInfo{Name: "read_file", FormattedMarkdown: &md}})
		}
		if fingerprintSession(withTool(short)) == fingerprintSession(withTool(long)) {
			t.Error("expected fingerprint to change when tool markdown grows")
		}
	})

	t.Run("tool summary change alone changes fingerprint", func(t *testing.T) {
		// A provider can update just the summary — e.g. a running tool completing —
		// without touching message text or formatted markdown.
		running, done := "Running `go test`...", "Ran `go test` (ok)"
		withSummary := func(s string) *spi.AgentChatSession {
			return sessionWith(schema.Message{Role: "agent", Tool: &schema.ToolInfo{Name: "shell", Summary: &s}})
		}
		if fingerprintSession(withSummary(running)) == fingerprintSession(withSummary(done)) {
			t.Error("expected fingerprint to change when tool summary changes")
		}
	})

	t.Run("metadata-only differences keep fingerprint equal", func(t *testing.T) {
		// Non-content fields (timestamps, models, usage) must not affect the
		// fingerprint, or every UI metadata patch would defeat the dedup.
		a := sessionWith(schema.Message{Role: "agent", Model: "gpt-4", Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hi"}}})
		b := sessionWith(schema.Message{Role: "agent", Model: "gpt-5", Timestamp: "2026-07-08T10:00:00Z", Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hi"}}})
		if fingerprintSession(a) != fingerprintSession(b) {
			t.Error("expected equal fingerprints when only non-content metadata differs")
		}
	})
}
