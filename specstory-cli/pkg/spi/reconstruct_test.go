package spi

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func strptr(s string) *string { return &s }

func TestFlattenSessionData(t *testing.T) {
	summary := "Tool use: **shell** `ls`"
	formatted := "```\nfile.txt\n```"
	onlyFormatted := "Wrote `hello.c`"

	data := &schema.SessionData{
		Exchanges: []schema.Exchange{
			{
				Messages: []schema.Message{
					// User text.
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "do a thing"}}},
					// Agent text; model/usage must be ignored by flattening.
					{Role: schema.RoleAgent, Model: "gpt-5-codex", Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "on it"}}, Usage: &schema.Usage{InputTokens: 10}},
					// Thinking folds into agent text.
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "let me reason"}}},
					// Tool with both summary and formatted markdown.
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "shell", Type: schema.ToolTypeShell, Summary: strptr(summary), FormattedMarkdown: strptr(formatted)}, PathHints: []string{"x"}},
					// Tool with only formatted markdown.
					{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", Type: schema.ToolTypeWrite, FormattedMarkdown: strptr(onlyFormatted)}},
					// Empty agent message (no content/tool) is skipped.
					{Role: schema.RoleAgent, PathHints: []string{"y"}},
				},
			},
		},
	}

	got := FlattenSessionData(data, "")

	want := []Turn{
		{Role: schema.RoleUser, Text: "do a thing"},
		{Role: schema.RoleAgent, Text: "on it"},
		{Role: schema.RoleAgent, Text: "let me reason"},
		{Role: schema.RoleAgent, Text: summary + "\n\n" + formatted},
		{Role: schema.RoleAgent, Text: onlyFormatted},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFlattenSessionData_MigrationNote(t *testing.T) {
	data := &schema.SessionData{
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "hi"}}},
			}},
		},
	}

	got := FlattenSessionData(data, "  Resumed from another agent.  ")
	if len(got) != 2 {
		t.Fatalf("got %d turns, want 2", len(got))
	}
	if got[0].Role != schema.RoleAgent || got[0].Text != "Resumed from another agent." {
		t.Errorf("migration note turn = %+v", got[0])
	}
	if got[1].Role != schema.RoleUser || got[1].Text != "hi" {
		t.Errorf("user turn = %+v", got[1])
	}
}

func TestFlattenSessionData_ToolFallback(t *testing.T) {
	data := &schema.SessionData{
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "MysteryTool", Type: schema.ToolTypeUnknown}},
			}},
		},
	}

	got := FlattenSessionData(data, "")
	if len(got) != 1 {
		t.Fatalf("got %d turns, want 1", len(got))
	}
	if got[0].Text != "Tool use: MysteryTool" {
		t.Errorf("fallback tool text = %q", got[0].Text)
	}
}

func TestFlattenSessionData_DropsSyntheticTurns(t *testing.T) {
	data := &schema.SessionData{
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "real prompt"}}},
				// Claude Code slash-command artifacts captured as user messages.
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "<command-name>/exit</command-name>\n<command-message>exit</command-message>\n<command-args></command-args>"}}},
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "<local-command-stdout>Catch you later!</local-command-stdout>"}}},
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "<local-command-caveat>Caveat: ...DO NOT respond...</local-command-caveat>"}}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "real answer"}}},
			}},
		},
	}

	got := FlattenSessionData(data, "")
	want := []Turn{
		{Role: schema.RoleUser, Text: "real prompt"},
		{Role: schema.RoleAgent, Text: "real answer"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFlattenSessionData_Nil(t *testing.T) {
	if got := FlattenSessionData(nil, ""); got != nil {
		t.Errorf("expected nil turns for nil data, got %+v", got)
	}
}

func TestFlattenSessionData_SkipsBlankTurns(t *testing.T) {
	data := &schema.SessionData{
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "   \n\t  "}}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: ""}}},
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "real"}}},
			}},
		},
	}
	got := FlattenSessionData(data, "")
	if len(got) != 1 || got[0].Text != "real" {
		t.Errorf("expected only the non-blank turn, got %+v", got)
	}
}

func TestFlattenSessionData_ToolOnlySession(t *testing.T) {
	// A session whose agent turns are entirely tool calls still produces agent text.
	a := "Ran `ls`"
	b := "Wrote `main.go`"
	data := &schema.SessionData{
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{
				{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "shell", FormattedMarkdown: strptr(a)}},
				{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Write", FormattedMarkdown: strptr(b)}},
			}},
		},
	}
	got := FlattenSessionData(data, "")
	want := []Turn{{Role: schema.RoleAgent, Text: a}, {Role: schema.RoleAgent, Text: b}}
	if len(got) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("turn %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}
