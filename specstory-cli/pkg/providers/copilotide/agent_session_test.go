package copilotide

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// TestConvertToSessionData_Basic verifies the top-level conversion: variant identity,
// session metadata, workspace root threading, exchange construction, and that RawData
// round-trips as valid JSON carrying the original session.
func TestConvertToSessionData_Basic(t *testing.T) {
	composer := VSCodeComposer{
		Host:            "vscode",
		SessionID:       "sess-1",
		CustomTitle:     "Fix the loader",
		Version:         3,
		CreationDate:    1700000000000,
		LastMessageDate: 1700000600000,
		Requests: []VSCodeRequestBlock{
			{
				RequestID: "r-1",
				Timestamp: 1700000100000,
				Message:   VSCodeMessage{Text: "Please fix the loader"},
				Result: VSCodeResult{
					Metadata: VSCodeResultMetadata{
						Messages: []VSCodeMetadataMessage{
							{Role: "assistant", Content: "Fixed it."},
						},
					},
				},
				ModelID: "gpt-5",
			},
		},
	}

	p := NewProvider(VSCode)
	session := p.ConvertToSessionData(composer, "/Users/me/proj", nil)

	if session.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", session.SessionID, "sess-1")
	}
	if session.Slug != "fix-the-loader" {
		t.Errorf("Slug = %q, want %q", session.Slug, "fix-the-loader")
	}
	if session.SessionData.Provider.ID != "copilotide" {
		t.Errorf("Provider.ID = %q, want %q", session.SessionData.Provider.ID, "copilotide")
	}
	if session.SessionData.WorkspaceRoot != "/Users/me/proj" {
		t.Errorf("WorkspaceRoot = %q, want %q", session.SessionData.WorkspaceRoot, "/Users/me/proj")
	}
	if session.CreatedAt == "" || session.SessionData.UpdatedAt == "" {
		t.Errorf("timestamps must be populated: created=%q updated=%q",
			session.CreatedAt, session.SessionData.UpdatedAt)
	}

	if len(session.SessionData.Exchanges) != 1 {
		t.Fatalf("len(Exchanges) = %d, want 1", len(session.SessionData.Exchanges))
	}
	exchange := session.SessionData.Exchanges[0]
	if exchange.ExchangeID != "r-1" {
		t.Errorf("ExchangeID = %q, want %q", exchange.ExchangeID, "r-1")
	}
	if len(exchange.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2 (user + agent)", len(exchange.Messages))
	}
	if exchange.Messages[0].Role != schema.RoleUser {
		t.Errorf("Messages[0].Role = %q, want %q", exchange.Messages[0].Role, schema.RoleUser)
	}
	if exchange.Messages[1].Role != schema.RoleAgent {
		t.Errorf("Messages[1].Role = %q, want %q", exchange.Messages[1].Role, schema.RoleAgent)
	}
	if got := exchange.Messages[1].Content[0].Text; got != "Fixed it." {
		t.Errorf("agent text = %q, want %q", got, "Fixed it.")
	}

	// RawData must be valid JSON preserving the original session.
	var raw VSCodeComposer
	if err := json.Unmarshal([]byte(session.RawData), &raw); err != nil {
		t.Fatalf("RawData is not valid JSON: %v", err)
	}
	if raw.SessionID != "sess-1" {
		t.Errorf("RawData SessionID = %q, want %q", raw.SessionID, "sess-1")
	}
}

// TestConvertToSessionData_EmptySession verifies an empty session (no requests, no
// state) converts without exchanges rather than failing.
func TestConvertToSessionData_EmptySession(t *testing.T) {
	p := NewProvider(VSCode)
	session := p.ConvertToSessionData(VSCodeComposer{SessionID: "empty-1"}, "/proj", nil)

	if session.SessionID != "empty-1" {
		t.Errorf("SessionID = %q, want %q", session.SessionID, "empty-1")
	}
	if len(session.SessionData.Exchanges) != 0 {
		t.Errorf("expected no exchanges, got %d", len(session.SessionData.Exchanges))
	}
	if session.Slug != "untitled" {
		t.Errorf("Slug = %q, want %q for empty session", session.Slug, "untitled")
	}
}

// TestConvertToSessionData_SyntheticFromEditingState verifies editing-only sessions
// (no chat requests but a state file with file operations) produce a synthetic
// exchange describing the operations.
func TestConvertToSessionData_SyntheticFromEditingState(t *testing.T) {
	tests := []struct {
		name          string
		state         *VSCodeStateFile
		wantExchanges int
		wantAgentHas  []string
	}{
		{
			name: "v2 timeline operations summarized",
			state: &VSCodeStateFile{
				Version:   2,
				SessionID: "edit-1",
				Timeline: &VSCodeTimeline{
					Operations: []VSCodeOperation{
						{Type: "create", URI: &VSCodeUri{FSPath: "/proj/new.go"}},
						{Type: "textEdit", URI: &VSCodeUri{Path: "/proj/main.go"}, Edits: []any{"e1", "e2"}},
						{Type: "delete", URI: &VSCodeUri{FSPath: "/proj/old.go"}},
					},
				},
			},
			wantExchanges: 1,
			wantAgentHas: []string{
				"Created file: `new.go`",
				"Edited `main.go` (2 edits)",
				"Deleted file: `old.go`",
			},
		},
		{
			name: "v1 snapshot entries summarized",
			state: &VSCodeStateFile{
				Version:   1,
				SessionID: "edit-2",
				RecentSnapshot: map[string]any{
					"entries": []any{
						map[string]any{"resource": "file:///proj/util.go"},
					},
				},
			},
			wantExchanges: 1,
			wantAgentHas:  []string{"Modified `util.go`"},
		},
		{
			name:          "state without editing activity yields no exchanges",
			state:         &VSCodeStateFile{Version: 2, SessionID: "edit-3"},
			wantExchanges: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composer := VSCodeComposer{
				SessionID:    "edit-session",
				CustomTitle:  "Editing pass",
				CreationDate: 1700000000000,
			}
			p := NewProvider(VSCode)
			session := p.ConvertToSessionData(composer, "/proj", tt.state)

			if len(session.SessionData.Exchanges) != tt.wantExchanges {
				t.Fatalf("len(Exchanges) = %d, want %d",
					len(session.SessionData.Exchanges), tt.wantExchanges)
			}
			if tt.wantExchanges == 0 {
				return
			}

			messages := session.SessionData.Exchanges[0].Messages
			if len(messages) < 2 {
				t.Fatalf("expected user + agent messages, got %d", len(messages))
			}
			if got := messages[0].Content[0].Text; got != "Editing pass" {
				t.Errorf("synthetic user text = %q, want the custom title", got)
			}
			agentText := messages[len(messages)-1].Content[0].Text
			for _, want := range tt.wantAgentHas {
				if !strings.Contains(agentText, want) {
					t.Errorf("agent text missing %q:\n%s", want, agentText)
				}
			}
		})
	}
}

// TestConvertRequestToMessages covers the per-request message assembly: user message
// always present, thinking only alongside real tool calls, tool messages built from
// the response array, and the final-text fallback chain (metadata messages ->
// toolCallRounds response -> response array values).
func TestConvertRequestToMessages(t *testing.T) {
	tests := []struct {
		name      string
		req       VSCodeRequestBlock
		wantRoles []string // in order; "user"/"agent"
		wantTexts []string // substring expected somewhere in the messages' text parts
		wantTool  string   // tool name expected on exactly one message, "" for none
	}{
		{
			name: "final text from metadata messages",
			req: VSCodeRequestBlock{
				RequestID: "r-1",
				Message:   VSCodeMessage{Text: "hello"},
				Result: VSCodeResult{Metadata: VSCodeResultMetadata{
					Messages: []VSCodeMetadataMessage{{Role: "assistant", Content: "hi there"}},
				}},
			},
			wantRoles: []string{schema.RoleUser, schema.RoleAgent},
			wantTexts: []string{"hello", "hi there"},
		},
		{
			name: "no tool calls: toolCallRounds response becomes the final text without thinking",
			req: VSCodeRequestBlock{
				RequestID: "r-2",
				Message:   VSCodeMessage{Text: "explain"},
				Result: VSCodeResult{Metadata: VSCodeResultMetadata{
					ToolCallRounds: []VSCodeToolCallRound{{Response: "the explanation"}},
				}},
			},
			wantRoles: []string{schema.RoleUser, schema.RoleAgent},
			wantTexts: []string{"explain", "the explanation"},
		},
		{
			name: "tool calls: thinking, tool message, and response-array final text",
			req: VSCodeRequestBlock{
				RequestID: "r-3",
				Message:   VSCodeMessage{Text: "create the file"},
				Response: rawMessages(
					`{"kind":"toolInvocationSerialized","toolCallId":"vs-1"}`,
					`{"value":"File created."}`,
				),
				Result: VSCodeResult{Metadata: VSCodeResultMetadata{
					ToolCallRounds: []VSCodeToolCallRound{{
						Response: "I will create the file now",
						ToolCalls: []VSCodeToolCallInfo{
							{ID: "call-1", Name: "create_file", Arguments: `{"filePath":"/proj/a.go"}`},
						},
					}},
				}},
				ModelID: "gpt-5",
			},
			wantRoles: []string{schema.RoleUser, schema.RoleAgent, schema.RoleAgent, schema.RoleAgent},
			wantTexts: []string{"create the file", "I will create the file now", "File created."},
			wantTool:  "create_file",
		},
		{
			name: "nothing but the user message",
			req: VSCodeRequestBlock{
				RequestID: "r-4",
				Message:   VSCodeMessage{Text: ""},
			},
			wantRoles: []string{schema.RoleUser},
		},
		{
			name: "malformed response entries are skipped without dropping the rest",
			req: VSCodeRequestBlock{
				RequestID: "r-5",
				Message:   VSCodeMessage{Text: "robust"},
				Response: rawMessages(
					`not json at all`,
					`{"value":"still extracted"}`,
				),
			},
			wantRoles: []string{schema.RoleUser, schema.RoleAgent},
			wantTexts: []string{"robust", "still extracted"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := ConvertRequestToMessages(tt.req)

			if len(messages) != len(tt.wantRoles) {
				t.Fatalf("len(messages) = %d, want %d: %+v", len(messages), len(tt.wantRoles), messages)
			}
			for i, wantRole := range tt.wantRoles {
				if messages[i].Role != wantRole {
					t.Errorf("messages[%d].Role = %q, want %q", i, messages[i].Role, wantRole)
				}
			}

			// Collect all text parts across messages for substring checks.
			var allText strings.Builder
			var toolName string
			for _, msg := range messages {
				for _, part := range msg.Content {
					allText.WriteString(part.Text)
					allText.WriteString("\n")
				}
				if msg.Tool != nil {
					toolName = msg.Tool.Name
				}
			}
			for _, want := range tt.wantTexts {
				if !strings.Contains(allText.String(), want) {
					t.Errorf("messages missing text %q in:\n%s", want, allText.String())
				}
			}
			if toolName != tt.wantTool {
				t.Errorf("tool name = %q, want %q", toolName, tt.wantTool)
			}
		})
	}
}

// TestConvertRequestToMessages_ThinkingPart verifies thinking is emitted as a
// dedicated ContentTypeThinking part carrying the model ID, not as plain text.
func TestConvertRequestToMessages_ThinkingPart(t *testing.T) {
	req := VSCodeRequestBlock{
		RequestID: "r-think",
		Message:   VSCodeMessage{Text: "do it"},
		Response: rawMessages(
			`{"kind":"toolInvocationSerialized","toolCallId":"vs-1"}`,
		),
		Result: VSCodeResult{Metadata: VSCodeResultMetadata{
			ToolCallRounds: []VSCodeToolCallRound{{
				Response:  "reasoning first",
				ToolCalls: []VSCodeToolCallInfo{{ID: "call-1", Name: "read_file"}},
			}},
		}},
		ModelID: "gpt-5",
	}

	messages := ConvertRequestToMessages(req)
	if len(messages) < 2 {
		t.Fatalf("expected at least user + thinking messages, got %d", len(messages))
	}

	thinking := messages[1]
	if thinking.Role != schema.RoleAgent {
		t.Errorf("thinking message role = %q, want %q", thinking.Role, schema.RoleAgent)
	}
	if thinking.Model != "gpt-5" {
		t.Errorf("thinking message model = %q, want %q", thinking.Model, "gpt-5")
	}
	if len(thinking.Content) != 1 || thinking.Content[0].Type != schema.ContentTypeThinking {
		t.Fatalf("expected one %q content part, got %+v", schema.ContentTypeThinking, thinking.Content)
	}
	if thinking.Content[0].Text != "reasoning first" {
		t.Errorf("thinking text = %q, want %q", thinking.Content[0].Text, "reasoning first")
	}
}

// TestMapToolType verifies the VS Code tool name -> schema tool type mapping,
// including the MCP prefix rule and the generic fallback for unknown names.
func TestMapToolType(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     string
	}{
		{"read tool", "read_file", schema.ToolTypeRead},
		{"write tool", "create_file", schema.ToolTypeWrite},
		{"search tool", "grep_search", schema.ToolTypeSearch},
		{"shell tool", "bash", schema.ToolTypeShell},
		{"task tool", "manage_todo_list", schema.ToolTypeTask},
		{"mcp tool maps to generic", "mcp_github_create_issue", schema.ToolTypeGeneric},
		{"unknown tool maps to generic", "never_heard_of_it", schema.ToolTypeGeneric},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapToolType(tt.toolName); got != tt.want {
				t.Errorf("MapToolType(%q) = %q, want %q", tt.toolName, got, tt.want)
			}
		})
	}
}
