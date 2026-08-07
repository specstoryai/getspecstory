package musecode

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func loadSession(t *testing.T, name string) *MuseSession {
	t.Helper()
	session, err := ParseSessionFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("failed to parse %s: %v", name, err)
	}
	return session
}

func generate(t *testing.T, name string) *SessionData {
	t.Helper()
	data, err := GenerateAgentSession(loadSession(t, name), "/Users/dev/project")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed for %s: %v", name, err)
	}
	if !data.Validate() {
		t.Errorf("SessionData from %s failed validation", name)
	}
	return data
}

// toolsByUseID indexes an exchange's tool messages for assertions.
func toolsByUseID(exchange Exchange) map[string]*ToolInfo {
	tools := make(map[string]*ToolInfo)
	for i := range exchange.Messages {
		if tool := exchange.Messages[i].Tool; tool != nil {
			tools[tool.UseID] = tool
		}
	}
	return tools
}

func TestGenerateAgentSession_Basic(t *testing.T) {
	data := generate(t, "session-basic.jsonl")

	if data.SchemaVersion != "1.0" {
		t.Errorf("SchemaVersion = %q, want 1.0", data.SchemaVersion)
	}
	if data.Provider.ID != "muse" || data.Provider.Name != "Muse Code" {
		t.Errorf("Provider = %+v, want muse/Muse Code", data.Provider)
	}
	if data.Provider.Version != "0.1.0" {
		t.Errorf("Provider.Version = %q, want the build semver", data.Provider.Version)
	}
	if data.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("SessionID = %q", data.SessionID)
	}
	if data.CreatedAt != "2026-08-07T22:00:00.000Z" {
		t.Errorf("CreatedAt = %q", data.CreatedAt)
	}

	if len(data.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1 (one run)", len(data.Exchanges))
	}
	exchange := data.Exchanges[0]
	if exchange.ExchangeID != "11111111-2222-3333-4444-555555555555:0" {
		t.Errorf("ExchangeID = %q, want sessionId:index", exchange.ExchangeID)
	}
	if exchange.EndTime != "2026-08-07T22:00:08.000Z" {
		t.Errorf("EndTime = %q, want the terminal event's timestamp", exchange.EndTime)
	}

	// user + read_file tool + assistant text. The reasoning event has empty
	// text (encrypted for the Meta provider) and must not become a message.
	if len(exchange.Messages) != 3 {
		t.Fatalf("message count = %d, want 3 (empty thinking suppressed)", len(exchange.Messages))
	}
	for i := range exchange.Messages {
		for _, part := range exchange.Messages[i].Content {
			if part.Type == schema.ContentTypeThinking {
				t.Errorf("message %d is a thinking message; empty reasoning must be suppressed", i)
			}
		}
	}

	if exchange.Messages[0].Role != schema.RoleUser {
		t.Errorf("first message role = %q, want user", exchange.Messages[0].Role)
	}
	if exchange.Messages[0].Content[0].Text != "Read notes.txt and summarize it." {
		t.Errorf("user text = %q", exchange.Messages[0].Content[0].Text)
	}
	if exchange.Messages[2].Model != "muse-spark-1.2" {
		t.Errorf("agent message model = %q, want muse-spark-1.2", exchange.Messages[2].Model)
	}

	tool := exchange.Messages[1].Tool
	if tool == nil {
		t.Fatal("second message is not a tool message")
	}
	if tool.Name != "read_file" || tool.Type != schema.ToolTypeRead || tool.UseID != "call_read_notes" {
		t.Errorf("tool = %+v", tool)
	}
	// args arrive as a JSON string and must be decoded into the input map.
	if path, _ := tool.Input["path"].(string); path != "notes.txt" {
		t.Errorf("tool input path = %v, want notes.txt", tool.Input["path"])
	}
	if out, _ := tool.Output["output"].(string); !strings.Contains(out, "1|alpha") {
		t.Errorf("tool output not folded in by call id: %v", tool.Output)
	}
	if tool.FormattedMarkdown == nil || *tool.FormattedMarkdown == "" {
		t.Error("FormattedMarkdown not populated")
	}
	if len(exchange.Messages[1].PathHints) != 1 || exchange.Messages[1].PathHints[0] != "notes.txt" {
		t.Errorf("PathHints = %v, want [notes.txt]", exchange.Messages[1].PathHints)
	}
}

func TestGenerateAgentSession_UsageAttachment(t *testing.T) {
	data := generate(t, "session-basic.jsonl")
	messages := data.Exchanges[0].Messages

	if messages[0].Usage != nil {
		t.Error("usage must never land on the user message")
	}

	// The runtime reports a tool step's usage before committing the calls, so
	// it carries over to the tool message.
	toolUsage := messages[1].Usage
	if toolUsage == nil {
		t.Fatal("usage missing from the tool message")
	}
	if toolUsage.InputTokens != 18280 || toolUsage.OutputTokens != 649 || toolUsage.ThoughtTokens != 551 {
		t.Errorf("tool message usage = %+v", toolUsage)
	}

	// The final step reports after committing its text, so it attaches back.
	textUsage := messages[2].Usage
	if textUsage == nil {
		t.Fatal("usage missing from the assistant text message")
	}
	if textUsage.InputTokens != 19000 || textUsage.OutputTokens != 40 || textUsage.ThoughtTokens != 12 {
		t.Errorf("text message usage = %+v", textUsage)
	}
	if textUsage.CachedTokens != 150 {
		t.Errorf("CachedTokens = %d, want 150 (cached_tokens + cache_read_tokens)", textUsage.CachedTokens)
	}
}

func TestGenerateAgentSession_ToolFoldingAndErrors(t *testing.T) {
	data := generate(t, "session-tools.jsonl")

	if len(data.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1", len(data.Exchanges))
	}
	tools := toolsByUseID(data.Exchanges[0])
	if len(tools) != 5 {
		t.Fatalf("tool message count = %d, want 5 (one per tool call)", len(tools))
	}

	tests := []struct {
		name     string
		useID    string
		wantName string
		wantType string
	}{
		{name: "bash success", useID: "call_bash_ok", wantName: "bash", wantType: schema.ToolTypeShell},
		{name: "bash failure", useID: "call_bash_fail", wantName: "bash", wantType: schema.ToolTypeShell},
		{name: "edit", useID: "call_edit", wantName: "edit_file", wantType: schema.ToolTypeWrite},
		{name: "todos", useID: "call_todos", wantName: "write_todos", wantType: schema.ToolTypeTask},
		{name: "failed read", useID: "call_read_missing", wantName: "read_file", wantType: schema.ToolTypeRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := tools[tt.useID]
			if tool == nil {
				t.Fatalf("no tool message for %s", tt.useID)
			}
			if tool.Name != tt.wantName || tool.Type != tt.wantType {
				t.Errorf("tool = %s/%s, want %s/%s", tool.Name, tool.Type, tt.wantName, tt.wantType)
			}
		})
	}

	// A "tool failed:" result is an error outcome, not output.
	failed := tools["call_read_missing"]
	if _, ok := failed.Output["output"]; ok {
		t.Error("failed tool must not carry an output key")
	}
	if errText, _ := failed.Output["error"].(string); !strings.HasPrefix(errText, "tool failed:") {
		t.Errorf("failed tool error = %v", failed.Output["error"])
	}

	// A non-zero exit code is still a successful tool call: the shell ran.
	bashFail := tools["call_bash_fail"]
	if _, ok := bashFail.Output["error"]; ok {
		t.Error("a non-zero exit code must not be recorded as a tool failure")
	}

	// workdir is JSON null on this call; the shell hint extractor must fall
	// back to the workspace root rather than treat "null" as a directory.
	for i := range data.Exchanges[0].Messages {
		msg := &data.Exchanges[0].Messages[i]
		for _, hint := range msg.PathHints {
			if strings.Contains(hint, "null") {
				t.Errorf("path hint %q derived from a JSON null argument", hint)
			}
		}
	}
}

func TestGenerateAgentSession_MultiTurn(t *testing.T) {
	data := generate(t, "session-multiturn.jsonl")

	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2 (one per run)", len(data.Exchanges))
	}
	for i, want := range []string{
		"what tools do you have access to",
		"is that the only set of tools you have?",
	} {
		exchange := data.Exchanges[i]
		if exchange.ExchangeID != fmt.Sprintf("%s:%d", data.SessionID, i) {
			t.Errorf("exchange %d ID = %q", i, exchange.ExchangeID)
		}
		if len(exchange.Messages) != 2 {
			t.Fatalf("exchange %d message count = %d, want 2", i, len(exchange.Messages))
		}
		if got := exchange.Messages[0].Content[0].Text; got != want {
			t.Errorf("exchange %d user text = %q, want %q", i, got, want)
		}
	}
}

func TestGenerateAgentSession_SubagentNoiseExcluded(t *testing.T) {
	data := generate(t, "session-subagent-noise.jsonl")

	if len(data.Exchanges) != 1 {
		t.Fatalf("exchange count = %d, want 1 (the subagent run must not open one)", len(data.Exchanges))
	}
	exchange := data.Exchanges[0]

	// user + subagent_spawn tool + assistant text.
	if len(exchange.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(exchange.Messages))
	}
	for i := range exchange.Messages {
		for _, part := range exchange.Messages[i].Content {
			if strings.Contains(part.Text, "Role: demo-worker") {
				t.Errorf("message %d rendered a subagent objective as conversation", i)
			}
		}
	}
	if tool := exchange.Messages[1].Tool; tool == nil || tool.Type != schema.ToolTypeTask {
		t.Errorf("subagent_spawn tool message missing or misclassified: %+v", exchange.Messages[1].Tool)
	}
}

func TestGenerateAgentSession_NoEvents(t *testing.T) {
	if _, err := GenerateAgentSession(&MuseSession{ID: "empty"}, "/Users/dev/project"); err == nil {
		t.Error("expected an error for a session with no conversation events")
	}
}

func TestClassifyMuseToolType(t *testing.T) {
	tests := []struct {
		toolName string
		expected string
	}{
		{toolName: "read_file", expected: schema.ToolTypeRead},
		{toolName: "read_memory", expected: schema.ToolTypeRead},
		{toolName: "write_file", expected: schema.ToolTypeWrite},
		{toolName: "edit_file", expected: schema.ToolTypeWrite},
		{toolName: "add_memory", expected: schema.ToolTypeWrite},
		{toolName: "edit_memory", expected: schema.ToolTypeWrite},
		{toolName: "search", expected: schema.ToolTypeSearch},
		{toolName: "web_search", expected: schema.ToolTypeSearch},
		{toolName: "bash", expected: schema.ToolTypeShell},
		{toolName: "bash_input", expected: schema.ToolTypeShell},
		{toolName: "write_todos", expected: schema.ToolTypeTask},
		{toolName: "subagent_spawn", expected: schema.ToolTypeTask},
		{toolName: "subagent_cancel", expected: schema.ToolTypeTask},
		{toolName: "get_goal", expected: schema.ToolTypeGeneric},
		{toolName: "cron_create", expected: schema.ToolTypeGeneric},
		{toolName: "read_skill", expected: schema.ToolTypeGeneric},
		{toolName: "snooze_reminder", expected: schema.ToolTypeGeneric},
		{toolName: "mystery_tool", expected: schema.ToolTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			if got := classifyMuseToolType(tt.toolName); got != tt.expected {
				t.Errorf("classifyMuseToolType(%q) = %q, want %q", tt.toolName, got, tt.expected)
			}
		})
	}
}

func TestConvertUsage(t *testing.T) {
	tests := []struct {
		name     string
		usage    *MuseUsage
		expected *Usage
	}{
		{
			name:     "nil usage",
			usage:    nil,
			expected: nil,
		},
		{
			name:     "all-zero step reports nothing",
			usage:    &MuseUsage{},
			expected: nil,
		},
		{
			name:  "reasoning tokens map to thought tokens",
			usage: &MuseUsage{InputTokens: 100, OutputTokens: 20, ReasoningTokens: 7},
			expected: &Usage{
				InputTokens: 100, OutputTokens: 20, ThoughtTokens: 7,
			},
		},
		{
			name:  "cached and cache-read tokens sum",
			usage: &MuseUsage{InputTokens: 5, CachedTokens: 30, CacheReadTokens: 12, CacheWriteTokens: 99},
			expected: &Usage{
				InputTokens: 5, CachedTokens: 42,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertUsage(tt.usage)
			switch {
			case tt.expected == nil && got != nil:
				t.Errorf("convertUsage() = %+v, want nil", got)
			case tt.expected != nil && got == nil:
				t.Errorf("convertUsage() = nil, want %+v", tt.expected)
			case tt.expected != nil && *got != *tt.expected:
				t.Errorf("convertUsage() = %+v, want %+v", got, tt.expected)
			}
		})
	}
}

func TestDecodeToolArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     string
		validate func(*testing.T, map[string]any)
	}{
		{
			name: "empty args",
			args: "",
			validate: func(t *testing.T, input map[string]any) {
				if input != nil {
					t.Errorf("input = %v, want nil", input)
				}
			},
		},
		{
			name: "json object string",
			args: `{"path":"notes.txt","limit":500}`,
			validate: func(t *testing.T, input map[string]any) {
				if path, _ := input["path"].(string); path != "notes.txt" {
					t.Errorf("path = %v, want notes.txt", input["path"])
				}
			},
		},
		{
			name: "undecodable args preserved verbatim",
			args: "not json at all",
			validate: func(t *testing.T, input map[string]any) {
				if raw, _ := input["raw"].(string); raw != "not json at all" {
					t.Errorf("raw = %v, want the original args", input["raw"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.validate(t, decodeToolArgs(tt.args))
		})
	}
}
