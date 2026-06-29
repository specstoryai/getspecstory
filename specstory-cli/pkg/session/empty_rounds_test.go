package session

import (
	"strings"
	"testing"
)

func TestGenerateMarkdown_SkipsEmptyAgentRounds(t *testing.T) {
	// Mirrors how Claude Code splits one agent turn into several records: a
	// signature-only thinking block (no renderable content) followed by the text.
	sd := &SessionData{
		Provider:  ProviderInfo{Name: "Claude Code"},
		SessionID: "s1",
		CreatedAt: "2026-06-16T04:08:17Z",
		Exchanges: []Exchange{{
			StartTime: "2026-06-16T04:08:00Z",
			Messages: []Message{
				{Role: "user", Timestamp: "2026-06-16T04:08:00Z", Content: []ContentPart{{Type: "text", Text: "hi"}}},
				{Role: "agent", Timestamp: "2026-06-16T04:08:17Z", Content: []ContentPart{{Type: "thinking", Text: ""}}},   // signature-only thinking: present part, empty body
				{Role: "agent", Timestamp: "2026-06-16T04:08:18Z", Content: []ContentPart{{Type: "text", Text: "hello!"}}}, // real text
			},
		}},
	}

	md, err := GenerateMarkdownFromAgentSession(sd, false, true)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if got := strings.Count(md, "_**Agent"); got != 1 {
		t.Errorf("expected 1 agent header (empty one skipped), got %d\n---\n%s", got, md)
	}
	if !strings.Contains(md, "hello!") {
		t.Error("real agent text was dropped")
	}
	// Exactly one separator, and it must sit between the user turn and the
	// agent turn — this is what the skipped empty round could have broken by
	// advancing prevRole.
	if got := strings.Count(md, "\n---\n"); got != 1 {
		t.Errorf("expected exactly 1 separator, got %d\n---\n%s", got, md)
	}
	userIdx := strings.Index(md, "_**User")
	sepIdx := strings.Index(md, "\n---\n")
	agentIdx := strings.Index(md, "_**Agent")
	if userIdx >= sepIdx || sepIdx >= agentIdx {
		t.Errorf("separator not positioned between user and agent turns:\n%s", md)
	}
}

func TestHasRenderableContent(t *testing.T) {
	tool := &ToolInfo{Name: "Bash"}
	cases := []struct {
		name string
		msg  Message
		want bool
	}{
		{"empty", Message{Role: "agent"}, false},
		{"whitespace only", Message{Content: []ContentPart{{Type: "text", Text: "  \n"}}}, false},
		{"signature-only thinking", Message{Content: []ContentPart{{Type: "thinking", Text: ""}}}, false},   // the bug this fix targets
		{"non-rendered type with text", Message{Content: []ContentPart{{Type: "image", Text: "x"}}}, false}, // type renderContentParts ignores → not renderable
		{"text", Message{Content: []ContentPart{{Type: "text", Text: "hi"}}}, true},
		{"thinking", Message{Content: []ContentPart{{Type: "thinking", Text: "hmm"}}}, true},
		{"empty part then text", Message{Content: []ContentPart{{Type: "thinking", Text: ""}, {Type: "text", Text: "hi"}}}, true}, // any renderable part wins
		{"tool only", Message{Tool: tool}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasRenderableContent(c.msg); got != c.want {
				t.Errorf("hasRenderableContent = %v, want %v", got, c.want)
			}
		})
	}
}
