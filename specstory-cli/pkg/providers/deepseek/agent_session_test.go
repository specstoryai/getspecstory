package deepseek

import (
	"reflect"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// TestBuildExchanges_DeterministicTimestamps: re-parsing the same session must
// produce identical EndTimes. Fix: buildExchanges uses UpdatedAt (fallback
// CreatedAt) instead of time.Now() so output stays byte-stable.
func TestBuildExchanges_DeterministicTimestamps(t *testing.T) {
	session := &dsSession{
		Metadata: dsMetadata{
			ID:        "sess-stable",
			CreatedAt: "2026-05-01T10:00:00Z",
			UpdatedAt: "2026-05-01T10:05:00Z",
		},
		Messages: []dsMessage{
			{Role: "user", Content: []dsContentPart{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "hello"}}},
		},
	}

	first := buildExchanges(session, "")
	second := buildExchanges(session, "")

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 exchange each, got %d / %d", len(first), len(second))
	}
	if first[0].EndTime != second[0].EndTime {
		t.Errorf("EndTime not deterministic: %q vs %q", first[0].EndTime, second[0].EndTime)
	}
	if first[0].EndTime != "2026-05-01T10:05:00Z" {
		t.Errorf("expected EndTime=UpdatedAt, got %q", first[0].EndTime)
	}
	if first[0].StartTime != "2026-05-01T10:00:00Z" {
		t.Errorf("expected StartTime=CreatedAt, got %q", first[0].StartTime)
	}
}

// TestBuildExchanges_FallbackToCreatedAt: assistantTs falls back to CreatedAt when UpdatedAt is empty.
func TestBuildExchanges_FallbackToCreatedAt(t *testing.T) {
	session := &dsSession{
		Metadata: dsMetadata{
			ID:        "sess-fallback",
			CreatedAt: "2026-05-01T10:00:00Z",
			// UpdatedAt intentionally empty
		},
		Messages: []dsMessage{
			{Role: "user", Content: []dsContentPart{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "hello"}}},
		},
	}

	exchanges := buildExchanges(session, "")
	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(exchanges))
	}
	if exchanges[0].EndTime != "2026-05-01T10:00:00Z" {
		t.Errorf("expected EndTime to fall back to CreatedAt, got %q", exchanges[0].EndTime)
	}
}

// TestAttachToolResults_ParallelToolCalls: when the assistant emits parallel
// tool calls (id=A, id=B) and the next user message contains tool_results for
// B then A, each result must land on the message owning its tool_use_id — not
// be concatenated onto whichever assistant tool message came last.
func TestAttachToolResults_ParallelToolCalls(t *testing.T) {
	session := &dsSession{
		Metadata: dsMetadata{
			ID:        "sess-parallel",
			CreatedAt: "2026-05-01T10:00:00Z",
			UpdatedAt: "2026-05-01T10:05:00Z",
		},
		Messages: []dsMessage{
			{Role: "user", Content: []dsContentPart{{Type: "text", Text: "do two reads"}}},
			{Role: "assistant", Content: []dsContentPart{
				{Type: "tool_use", Name: "read_file", ID: "A", Input: map[string]interface{}{"file_path": "a.go"}},
				{Type: "tool_use", Name: "read_file", ID: "B", Input: map[string]interface{}{"file_path": "b.go"}},
			}},
			// Results returned in REVERSE order to make sure we match by id, not order.
			{Role: "user", Content: []dsContentPart{
				{Type: "tool_result", ToolUseID: "B", ToolResultContent: "contents-of-B"},
				{Type: "tool_result", ToolUseID: "A", ToolResultContent: "contents-of-A"},
			}},
		},
	}

	exchanges := buildExchanges(session, "")
	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(exchanges))
	}
	msgs := exchanges[0].Messages
	// user text + 2 tool_use messages = 3
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Find each tool message by UseID.
	byID := map[string]*Message{}
	for i := range msgs {
		if msgs[i].Tool != nil {
			byID[msgs[i].Tool.UseID] = &msgs[i]
		}
	}
	a, b := byID["A"], byID["B"]
	if a == nil || b == nil {
		t.Fatalf("missing tool messages: A=%v B=%v", a, b)
	}

	gotA, _ := a.Tool.Output["content"].(string)
	gotB, _ := b.Tool.Output["content"].(string)
	if gotA != "contents-of-A" {
		t.Errorf("tool A got wrong result: %q", gotA)
	}
	if gotB != "contents-of-B" {
		t.Errorf("tool B got wrong result: %q", gotB)
	}

	// Each FormattedMarkdown should now include its own Output.
	if a.Tool.FormattedMarkdown == nil || !strings.Contains(*a.Tool.FormattedMarkdown, "contents-of-A") {
		t.Errorf("tool A markdown missing its result: %v", a.Tool.FormattedMarkdown)
	}
	if b.Tool.FormattedMarkdown == nil || !strings.Contains(*b.Tool.FormattedMarkdown, "contents-of-B") {
		t.Errorf("tool B markdown missing its result: %v", b.Tool.FormattedMarkdown)
	}
	// Critically: A's markdown should NOT contain B's result (the old bug).
	if a.Tool.FormattedMarkdown != nil && strings.Contains(*a.Tool.FormattedMarkdown, "contents-of-B") {
		t.Errorf("tool A leaked tool B's result (the bug we fixed): %s", *a.Tool.FormattedMarkdown)
	}
}

// TestAttachToolResults_OrphanResultDropped confirms that a tool_result whose
// tool_use_id matches no assistant tool call is silently dropped (not attached
// to a random message).
func TestAttachToolResults_OrphanResultDropped(t *testing.T) {
	target := Message{Tool: &ToolInfo{UseID: "real", Name: "read_file"}}
	current := &Exchange{Messages: []Message{target}}

	msg := &dsMessage{Content: []dsContentPart{
		{Type: "tool_result", ToolUseID: "ghost", ToolResultContent: "should-be-dropped"},
	}}

	attachToolResults(msg, current)

	if current.Messages[0].Tool.Output != nil {
		t.Errorf("orphan tool_result was incorrectly attached: %v", current.Messages[0].Tool.Output)
	}
}

// TestAttachToolResults_EmptyContentSkipped verifies that an empty result body
// doesn't overwrite or even create an Output map.
func TestAttachToolResults_EmptyContentSkipped(t *testing.T) {
	target := Message{Tool: &ToolInfo{UseID: "X", Name: "read_file"}}
	current := &Exchange{Messages: []Message{target}}

	msg := &dsMessage{Content: []dsContentPart{
		{Type: "tool_result", ToolUseID: "X", ToolResultContent: "   "},
	}}

	attachToolResults(msg, current)
	if current.Messages[0].Tool.Output != nil {
		t.Errorf("empty result should not produce Output, got %v", current.Messages[0].Tool.Output)
	}
}

func TestIsToolResultOnly(t *testing.T) {
	tests := []struct {
		name string
		msg  dsMessage
		want bool
	}{
		{
			name: "single tool_result is tool-result-only",
			msg:  dsMessage{Content: []dsContentPart{{Type: "tool_result", ToolUseID: "X"}}},
			want: true,
		},
		{
			name: "tool_result plus turn_meta is tool-result-only",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "text", Text: "<turn_meta>foo"},
				{Type: "tool_result", ToolUseID: "X"},
			}},
			want: true,
		},
		{
			name: "tool_result plus real text is NOT tool-result-only",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "text", Text: "actual user follow-up"},
				{Type: "tool_result", ToolUseID: "X"},
			}},
			want: false,
		},
		{
			name: "no tool_result returns false",
			msg:  dsMessage{Content: []dsContentPart{{Type: "text", Text: "hi"}}},
			want: false,
		},
		{
			name: "empty content returns false",
			msg:  dsMessage{Content: nil},
			want: false,
		},
		{
			name: "tool_result plus whitespace-only text",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "text", Text: "   "},
				{Type: "tool_result", ToolUseID: "X"},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isToolResultOnly(&tt.msg)
			if got != tt.want {
				t.Errorf("isToolResultOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertAssistantMessage(t *testing.T) {
	tests := []struct {
		name       string
		msg        dsMessage
		wantCount  int
		wantChecks func(t *testing.T, msgs []Message)
	}{
		{
			name: "text + thinking combined into one message",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "thinking", Thinking: "let me think"},
				{Type: "text", Text: "the answer is 42"},
			}},
			wantCount: 1,
			wantChecks: func(t *testing.T, msgs []Message) {
				if len(msgs[0].Content) != 2 {
					t.Errorf("expected 2 content parts, got %d", len(msgs[0].Content))
				}
				if msgs[0].Content[0].Type != "thinking" {
					t.Errorf("expected first part to be thinking, got %q", msgs[0].Content[0].Type)
				}
				if msgs[0].Content[1].Type != "text" {
					t.Errorf("expected second part to be text, got %q", msgs[0].Content[1].Type)
				}
			},
		},
		{
			name: "each tool_use becomes its own message",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "text", Text: "running tools"},
				{Type: "tool_use", Name: "read_file", ID: "id-1", Input: map[string]interface{}{"file_path": "a.go"}},
				{Type: "tool_use", Name: "read_file", ID: "id-2", Input: map[string]interface{}{"file_path": "b.go"}},
			}},
			wantCount: 3, // 1 text + 2 tools
			wantChecks: func(t *testing.T, msgs []Message) {
				if msgs[0].Tool != nil {
					t.Errorf("first message should be text, not tool")
				}
				if msgs[1].Tool == nil || msgs[1].Tool.UseID != "id-1" {
					t.Errorf("expected second message to be tool id-1")
				}
				if msgs[2].Tool == nil || msgs[2].Tool.UseID != "id-2" {
					t.Errorf("expected third message to be tool id-2")
				}
				// Both tool messages should have FormattedMarkdown rendered from input.
				if msgs[1].Tool.FormattedMarkdown == nil {
					t.Errorf("expected FormattedMarkdown to be set for tool id-1")
				}
			},
		},
		{
			name:       "empty content yields no messages",
			msg:        dsMessage{Content: nil},
			wantCount:  0,
			wantChecks: func(t *testing.T, msgs []Message) {},
		},
		{
			name: "whitespace-only text is skipped",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "text", Text: "   "},
			}},
			wantCount:  0,
			wantChecks: func(t *testing.T, msgs []Message) {},
		},
		{
			name: "tool_use without name gets unknown",
			msg: dsMessage{Content: []dsContentPart{
				{Type: "tool_use", ID: "x"},
			}},
			wantCount: 1,
			wantChecks: func(t *testing.T, msgs []Message) {
				if msgs[0].Tool == nil || msgs[0].Tool.Name != "unknown" {
					t.Errorf("expected tool name 'unknown', got %v", msgs[0].Tool)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := convertAssistantMessage(tt.msg, "deepseek-r1", "2026-05-01T10:00:00Z", "")
			if len(msgs) != tt.wantCount {
				t.Fatalf("expected %d messages, got %d", tt.wantCount, len(msgs))
			}
			tt.wantChecks(t, msgs)
		})
	}
}

func TestGenerateAgentSession_MetadataWorkspaceAndUsage(t *testing.T) {
	tests := []struct {
		name              string
		metadataWorkspace string
		callerWorkspace   string
		wantWorkspace     string
	}{
		{
			name:              "metadata workspace overrides caller workspace",
			metadataWorkspace: "/metadata/workspace",
			callerWorkspace:   "/caller/workspace",
			wantWorkspace:     "/metadata/workspace",
		},
		{
			name:              "caller workspace used when metadata workspace empty",
			metadataWorkspace: "",
			callerWorkspace:   "/caller/workspace",
			wantWorkspace:     "/caller/workspace",
		},
		{
			name:              "whitespace metadata workspace does not override caller workspace",
			metadataWorkspace: "   ",
			callerWorkspace:   "/caller/workspace",
			wantWorkspace:     "/caller/workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &dsSession{
				Metadata: dsMetadata{
					ID:          "sess-metadata",
					CreatedAt:   "2026-05-01T10:00:00Z",
					UpdatedAt:   "2026-05-01T10:05:00Z",
					TotalTokens: 12345,
					Model:       "deepseek-r1",
					Workspace:   tt.metadataWorkspace,
				},
				Messages: []dsMessage{
					{Role: "user", Content: []dsContentPart{{Type: "text", Text: "hello"}}},
					{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "hi"}}},
				},
			}

			data, err := generateAgentSession(session, tt.callerWorkspace)
			if err != nil {
				t.Fatalf("generateAgentSession() error = %v", err)
			}
			if data.Provider.ID != "deepseek-tui" {
				t.Errorf("Provider.ID = %q, want deepseek-tui", data.Provider.ID)
			}
			if data.Provider.Name != "DeepSeek TUI" {
				t.Errorf("Provider.Name = %q, want DeepSeek TUI", data.Provider.Name)
			}
			if data.Provider.Version != "deepseek-r1" {
				t.Errorf("Provider.Version = %q, want deepseek-r1", data.Provider.Version)
			}
			if data.WorkspaceRoot != tt.wantWorkspace {
				t.Errorf("WorkspaceRoot = %q, want %q", data.WorkspaceRoot, tt.wantWorkspace)
			}

			for _, exchange := range data.Exchanges {
				for _, msg := range exchange.Messages {
					if msg.Usage != nil {
						t.Fatalf("TotalTokens should not synthesize per-message Usage, got %+v", msg.Usage)
					}
				}
			}
		})
	}
}

func TestConvertAssistantMessage_AttachesPathHints(t *testing.T) {
	msgs := convertAssistantMessage(dsMessage{Content: []dsContentPart{
		{Type: "tool_use", Name: "read_file", ID: "read", Input: map[string]interface{}{"file_path": "/repo/pkg/main.go"}},
		{Type: "tool_use", Name: "exec_shell", ID: "shell", Input: map[string]interface{}{"command": "echo hi > out.txt", "workdir": "/repo"}},
	}}, "deepseek-r1", "2026-05-01T10:00:00Z", "/repo")

	if len(msgs) != 2 {
		t.Fatalf("expected 2 tool messages, got %d", len(msgs))
	}
	if !reflect.DeepEqual(msgs[0].PathHints, []string{"pkg/main.go"}) {
		t.Errorf("read_file PathHints = %v, want [pkg/main.go]", msgs[0].PathHints)
	}
	foundShellOutput := false
	for _, hint := range msgs[1].PathHints {
		if hint == "out.txt" {
			foundShellOutput = true
			break
		}
	}
	if !foundShellOutput {
		t.Errorf("exec_shell PathHints = %v, want to contain out.txt", msgs[1].PathHints)
	}
}

func TestClassifyToolType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "write_file", want: schema.ToolTypeWrite},
		{name: "edit_file", want: schema.ToolTypeWrite},
		{name: "read_file", want: schema.ToolTypeRead},
		{name: "grep_files", want: schema.ToolTypeSearch},
		{name: "exec_shell", want: schema.ToolTypeShell},
		{name: "checklist_write", want: schema.ToolTypeTask},
		{name: "task_create", want: schema.ToolTypeTask},
		{name: "unknown", want: schema.ToolTypeGeneric},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyToolType(tt.name); got != tt.want {
				t.Errorf("classifyToolType(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestDeriveSlug(t *testing.T) {
	tests := []struct {
		name    string
		session *dsSession
		want    string // empty means "non-empty"; specific string means exact match
	}{
		{
			name: "first user message becomes slug source",
			session: &dsSession{Messages: []dsMessage{
				{Role: "user", Content: []dsContentPart{{Type: "text", Text: "Help me refactor the parser"}}},
			}},
		},
		{
			name: "skips assistant messages and turn_meta blocks",
			session: &dsSession{Messages: []dsMessage{
				{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "ignore me"}}},
				{Role: "user", Content: []dsContentPart{{Type: "text", Text: "<turn_meta>noise"}}},
				{Role: "user", Content: []dsContentPart{{Type: "text", Text: "Real question here"}}},
			}},
		},
		{
			name:    "empty session yields default slug",
			session: &dsSession{Messages: []dsMessage{}},
			want:    "deepseek-session",
		},
		{
			name: "no user messages yields default slug",
			session: &dsSession{Messages: []dsMessage{
				{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "hi"}}},
			}},
			want: "deepseek-session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveSlug(tt.session)
			if tt.want != "" {
				if got != tt.want {
					t.Errorf("deriveSlug() = %q, want %q", got, tt.want)
				}
				return
			}
			if got == "" || got == "deepseek-session" {
				t.Errorf("expected non-default slug, got %q", got)
			}
		})
	}
}

// TestBuildExchanges_TwoTurns covers exchange grouping: a real user turn
// boundary should start a new exchange while tool-result-only user messages
// stay attached to the current assistant turn.
func TestBuildExchanges_TwoTurns(t *testing.T) {
	session := &dsSession{
		Metadata: dsMetadata{
			ID:        "sess-2turn",
			CreatedAt: "2026-05-01T10:00:00Z",
			UpdatedAt: "2026-05-01T10:05:00Z",
		},
		Messages: []dsMessage{
			{Role: "user", Content: []dsContentPart{{Type: "text", Text: "first question"}}},
			{Role: "assistant", Content: []dsContentPart{
				{Type: "tool_use", Name: "read_file", ID: "A"},
			}},
			{Role: "user", Content: []dsContentPart{
				{Type: "tool_result", ToolUseID: "A", ToolResultContent: "result"},
			}},
			{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "answer one"}}},
			{Role: "user", Content: []dsContentPart{{Type: "text", Text: "second question"}}},
			{Role: "assistant", Content: []dsContentPart{{Type: "text", Text: "answer two"}}},
		},
	}

	exchanges := buildExchanges(session, "")
	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}

	// Exchange IDs should be deterministic and indexed.
	if exchanges[0].ExchangeID != "sess-2turn:0" {
		t.Errorf("expected first ExchangeID 'sess-2turn:0', got %q", exchanges[0].ExchangeID)
	}
	if exchanges[1].ExchangeID != "sess-2turn:1" {
		t.Errorf("expected second ExchangeID 'sess-2turn:1', got %q", exchanges[1].ExchangeID)
	}
}
