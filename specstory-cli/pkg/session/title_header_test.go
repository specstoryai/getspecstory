package session

import (
	"strings"
	"testing"
)

func TestGenerateMarkdown_TitleHeader(t *testing.T) {
	base := &SessionData{
		SchemaVersion: "1.0",
		Provider:      ProviderInfo{Name: "Claude Code"},
		SessionID:     "s1",
		CreatedAt:     "2026-06-17T08:33:43Z",
		Exchanges: []Exchange{{
			StartTime: "2026-06-17T08:33:43Z",
			Messages:  []Message{{Role: "user", Timestamp: "2026-06-17T08:33:43Z", Content: []ContentPart{{Type: "text", Text: "hi"}}}},
		}},
	}

	withTitle := *base
	withTitle.Title = "Understand Wispr ASR pipeline"
	md, err := GenerateMarkdownFromAgentSession(&withTitle, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "**Title:** Understand Wispr ASR pipeline") {
		t.Errorf("expected title metadata line, got:\n%s", md)
	}

	md2, err := GenerateMarkdownFromAgentSession(base, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md2, "**Title:**") {
		t.Error("no title line expected when Title is blank")
	}
}
