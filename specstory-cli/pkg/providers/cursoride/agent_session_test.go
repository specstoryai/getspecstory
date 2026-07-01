package cursoride

import (
	"strings"
	"testing"
)

// TestGenerateSlug covers the slug sanitization fix: generateSlug must route through
// spi.GenerateFilenameFromUserMessage (via composer name or first user message) so
// characters like "/" and ":" never end up in a filename-derived slug.
func TestGenerateSlug(t *testing.T) {
	tests := []struct {
		name     string
		composer *ComposerData
		want     string
	}{
		{
			name: "composer name with slash is sanitized",
			composer: &ComposerData{
				ComposerID: "fallback-id",
				Name:       "Fix bug in src/utils.go",
			},
			want: "fix-bug-in-src", // spi.GenerateFilenameFromUserMessage caps at 4 words
		},
		{
			name: "composer name with colon is sanitized",
			composer: &ComposerData{
				ComposerID: "fallback-id",
				Name:       "Fix: login issue",
			},
			want: "fix-login-issue",
		},
		{
			name: "empty composer name falls back to first user message",
			composer: &ComposerData{
				ComposerID: "fallback-id",
				Conversation: []ComposerConversation{
					{BubbleID: "b1", Type: 1, Text: "How do I fix the auth/login flow?"},
				},
			},
			want: "how-do-i-fix", // spi.GenerateFilenameFromUserMessage caps at 4 words
		},
		{
			name: "punctuation-only composer name falls back to first user message",
			composer: &ComposerData{
				ComposerID: "fallback-id",
				Name:       "...",
				Conversation: []ComposerConversation{
					{BubbleID: "b1", Type: 1, Text: "add rate limiting"},
				},
			},
			want: "add-rate-limiting",
		},
		{
			name: "no name and no user message falls back to composer ID",
			composer: &ComposerData{
				ComposerID: "abc-123",
				Conversation: []ComposerConversation{
					{BubbleID: "b1", Type: 2, Text: "agent-only bubble"},
				},
			},
			want: "abc-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateSlug(tt.composer)
			if got != tt.want {
				t.Errorf("generateSlug() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestConvertToAgentChatSession_ToolRendering covers the duplicate tool-use rendering
// fix: a successfully-resolved tool bubble must populate Tool.FormattedMarkdown and
// leave Content empty (single render path via Tool), while error/cancelled/unresolvable
// tool bubbles must fall back to plain text in Content with Tool left nil.
func TestConvertToAgentChatSession_ToolRendering(t *testing.T) {
	tests := []struct {
		name           string
		toolData       *ToolInvocationData
		wantToolNonNil bool
		wantContentHas string // substring expected in Content, if wantToolNonNil is false
	}{
		{
			name: "successfully resolved tool populates Tool, not Content",
			toolData: &ToolInvocationData{
				Tool:   1,
				Name:   "run_terminal_cmd",
				Status: "completed",
				Params: `{"command":"ls -la"}`,
			},
			wantToolNonNil: true,
		},
		{
			name: "cancelled tool falls back to plain Content, no Tool",
			toolData: &ToolInvocationData{
				Tool:   1,
				Name:   "run_terminal_cmd",
				Status: "cancelled",
			},
			wantToolNonNil: false,
			wantContentHas: "Cancelled",
		},
		{
			name: "invalid tool (Tool=0) falls back to plain Content, no Tool",
			toolData: &ToolInvocationData{
				Tool: 0,
				Name: "unknown_tool",
			},
			wantToolNonNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composer := &ComposerData{
				ComposerID: "verify",
				Version:    3,
				Conversation: []ComposerConversation{
					{BubbleID: "b1", Type: 1, Text: "run it"},
					{BubbleID: "b2", Type: 2, CapabilityType: 15, ToolFormerData: tt.toolData},
				},
			}

			session, err := ConvertToAgentChatSession(composer, "/tmp/proj")
			if err != nil {
				t.Fatalf("ConvertToAgentChatSession failed: %v", err)
			}

			toolMsg := session.SessionData.Exchanges[0].Messages[1]

			if tt.wantToolNonNil {
				if toolMsg.Tool == nil {
					t.Fatalf("expected Tool to be set, got nil")
				}
				if toolMsg.Tool.FormattedMarkdown == nil || *toolMsg.Tool.FormattedMarkdown == "" {
					t.Errorf("expected Tool.FormattedMarkdown to be populated")
				}
				if len(toolMsg.Content) != 0 {
					t.Errorf("expected Content to be empty when Tool is set (else markdown.go renders the tool use twice), got: %+v", toolMsg.Content)
				}
				return
			}

			if toolMsg.Tool != nil {
				t.Errorf("expected Tool to be nil, got: %+v", toolMsg.Tool)
			}
			if tt.wantContentHas != "" {
				if len(toolMsg.Content) == 0 || !strings.Contains(toolMsg.Content[0].Text, tt.wantContentHas) {
					t.Errorf("expected Content to contain %q, got: %+v", tt.wantContentHas, toolMsg.Content)
				}
			}
		})
	}
}

// TestConvertToAgentChatSession_ExchangeIDs covers the empty-ExchangeID fix: every
// exchange must get a non-empty, sequential "sessionId:index" ID, even when the
// conversation's first surviving bubble isn't a user message.
func TestConvertToAgentChatSession_ExchangeIDs(t *testing.T) {
	tests := []struct {
		name         string
		conversation []ComposerConversation
		wantIDs      []string
	}{
		{
			name: "normal conversation starting with a user message",
			conversation: []ComposerConversation{
				{BubbleID: "b1", Type: 1, Text: "hello"},
				{BubbleID: "b2", Type: 2, Text: "hi there"},
				{BubbleID: "b3", Type: 1, Text: "next question"},
				{BubbleID: "b4", Type: 2, Text: "answer"},
			},
			wantIDs: []string{"sess:0", "sess:1"},
		},
		{
			name: "conversation opening with a non-user bubble",
			conversation: []ComposerConversation{
				{BubbleID: "b1", Type: 2, Text: "unexpected opening agent message"},
				{BubbleID: "b2", Type: 1, Text: "fix the bug"},
				{BubbleID: "b3", Type: 2, Text: "done"},
			},
			wantIDs: []string{"sess:0", "sess:1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composer := &ComposerData{
				ComposerID:   "sess",
				Conversation: tt.conversation,
			}

			session, err := ConvertToAgentChatSession(composer, "/tmp/proj")
			if err != nil {
				t.Fatalf("ConvertToAgentChatSession failed: %v", err)
			}

			if len(session.SessionData.Exchanges) != len(tt.wantIDs) {
				t.Fatalf("expected %d exchanges, got %d", len(tt.wantIDs), len(session.SessionData.Exchanges))
			}
			for i, want := range tt.wantIDs {
				got := session.SessionData.Exchanges[i].ExchangeID
				if got == "" {
					t.Errorf("exchange[%d] has empty ExchangeID", i)
				}
				if got != want {
					t.Errorf("exchange[%d].ExchangeID = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// TestConvertToAgentChatSession_WorkspaceRoot covers the WorkspaceRoot propagation fix:
// the workspaceRoot argument must be threaded through to SessionData.WorkspaceRoot.
func TestConvertToAgentChatSession_WorkspaceRoot(t *testing.T) {
	composer := &ComposerData{
		ComposerID: "verify",
		Conversation: []ComposerConversation{
			{BubbleID: "b1", Type: 1, Text: "hello"},
		},
	}

	session, err := ConvertToAgentChatSession(composer, "/Users/bago/code/myproject")
	if err != nil {
		t.Fatalf("ConvertToAgentChatSession failed: %v", err)
	}
	if session.SessionData.WorkspaceRoot != "/Users/bago/code/myproject" {
		t.Errorf("WorkspaceRoot = %q, want %q", session.SessionData.WorkspaceRoot, "/Users/bago/code/myproject")
	}
}
