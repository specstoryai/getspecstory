package antigravitycli

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestCleanUserPrompt(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "extracts USER_REQUEST and drops metadata",
			raw:  "<USER_REQUEST>\ncan we move to dev branch\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-05-26T17:31:13-04:00.\n</ADDITIONAL_METADATA>",
			want: "can we move to dev branch",
		},
		{
			name: "drops settings change block too",
			raw:  "<USER_REQUEST>\nhello\n</USER_REQUEST>\n<USER_SETTINGS_CHANGE>\nThe user changed setting Model Selection from None to X.\n</USER_SETTINGS_CHANGE>",
			want: "hello",
		},
		{
			name: "no wrapper falls back to metadata stripping",
			raw:  "just text\n<ADDITIONAL_METADATA>\nnoise\n</ADDITIONAL_METADATA>",
			want: "just text",
		},
		{
			name: "empty",
			raw:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanUserPrompt(tt.raw); got != tt.want {
				t.Errorf("cleanUserPrompt() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeriveModel(t *testing.T) {
	tests := []struct {
		name  string
		steps []transcriptStep
		want  string
	}{
		{
			name: "version with decimal point is not truncated",
			steps: []transcriptStep{{
				Type:    typeUserInput,
				Content: "<USER_REQUEST>\nhi\n</USER_REQUEST>\n<USER_SETTINGS_CHANGE>\nThe user changed setting `Model Selection` from None to Gemini 3.5 Flash (Medium). No need to comment.\n</USER_SETTINGS_CHANGE>",
			}},
			want: "Gemini 3.5 Flash (Medium)",
		},
		{
			name:  "no settings block yields empty",
			steps: []transcriptStep{{Type: typeUserInput, Content: "<USER_REQUEST>\nhi\n</USER_REQUEST>"}},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveModel(tt.steps); got != tt.want {
				t.Errorf("deriveModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildExchanges_GroupsAndAttaches(t *testing.T) {
	session := &agSession{
		ConversationID: "conv-1",
		CreatedAt:      "2026-05-26T21:31:13Z",
		Steps: []transcriptStep{
			{StepIndex: 0, Type: typeUserInput, Source: sourceUserExplicit, CreatedAt: "2026-05-26T21:31:13Z", Content: "<USER_REQUEST>\nrun ls\n</USER_REQUEST>"},
			{StepIndex: 1, Type: typeConversationHistory, CreatedAt: "2026-05-26T21:31:13Z"},
			{StepIndex: 2, Type: typePlannerResponse, CreatedAt: "2026-05-26T21:31:14Z", Content: "I will list files.", ToolCalls: []transcriptToolCall{{Name: "run_command", Args: map[string]any{"CommandLine": "ls", "Cwd": "/x"}}}},
			{StepIndex: 3, Type: typeRunCommand, CreatedAt: "2026-05-26T21:31:15Z", Content: "\t\t\tThe command completed successfully.\n\t\t\tOutput:\n\t\t\tfile.txt"},
			{StepIndex: 5, Type: typePlannerResponse, CreatedAt: "2026-05-26T21:31:16Z", Content: "Done."},
			{StepIndex: 6, Type: typeUserInput, Source: sourceUserExplicit, CreatedAt: "2026-05-26T21:31:20Z", Content: "<USER_REQUEST>\nthanks\n</USER_REQUEST>"},
		},
	}

	exchanges := buildExchanges(session, "/x")

	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}
	if exchanges[0].ExchangeID != "conv-1:0" || exchanges[1].ExchangeID != "conv-1:1" {
		t.Errorf("unexpected exchange IDs: %q, %q", exchanges[0].ExchangeID, exchanges[1].ExchangeID)
	}

	// First exchange: user + agent text + tool + agent text = 4 messages.
	first := exchanges[0]
	if len(first.Messages) != 4 {
		t.Fatalf("expected 4 messages in first exchange, got %d", len(first.Messages))
	}
	if first.Messages[0].Role != schema.RoleUser || first.Messages[0].Content[0].Text != "run ls" {
		t.Errorf("unexpected first user message: %+v", first.Messages[0])
	}
	// The tool message must have the command output attached and rendered.
	toolMsg := first.Messages[2]
	if toolMsg.Tool == nil || toolMsg.Tool.Name != "run_command" {
		t.Fatalf("expected run_command tool message, got %+v", toolMsg)
	}
	if toolMsg.Tool.Output == nil {
		t.Errorf("expected tool output to be attached")
	}
	if toolMsg.Tool.FormattedMarkdown == nil || !strings.Contains(*toolMsg.Tool.FormattedMarkdown, "file.txt") {
		t.Errorf("expected rendered tool output to include command result, got %v", toolMsg.Tool.FormattedMarkdown)
	}
	// Leading tabs from the RUN_COMMAND block must be stripped.
	if toolMsg.Tool.FormattedMarkdown != nil && strings.Contains(*toolMsg.Tool.FormattedMarkdown, "\t\t\t") {
		t.Errorf("expected leading tabs to be stripped from output")
	}
}

func TestAttachToolResult_FIFOAndOrphan(t *testing.T) {
	// Two pending tool calls; two results should attach in order (FIFO).
	current := &Exchange{Messages: []Message{
		{Role: schema.RoleAgent, Tool: &ToolInfo{Name: "run_command", Input: map[string]any{"CommandLine": "a"}}},
		{Role: schema.RoleAgent, Tool: &ToolInfo{Name: "run_command", Input: map[string]any{"CommandLine": "b"}}},
	}}

	attachToolResult(transcriptStep{Type: typeRunCommand, Content: "result-A"}, current, nil)
	attachToolResult(transcriptStep{Type: typeRunCommand, Content: "result-B"}, current, nil)

	if out, _ := current.Messages[0].Tool.Output["content"].(string); out != "result-A" {
		t.Errorf("first tool got %q, want result-A", out)
	}
	if out, _ := current.Messages[1].Tool.Output["content"].(string); out != "result-B" {
		t.Errorf("second tool got %q, want result-B", out)
	}

	// An orphan result (no pending tool) must not panic or misattribute.
	attachToolResult(transcriptStep{Type: typeRunCommand, Content: "orphan"}, current, nil)
	if out, _ := current.Messages[1].Tool.Output["content"].(string); out != "result-B" {
		t.Errorf("orphan result overwrote a resolved tool: got %q", out)
	}
}

func TestAttachToolResult_AsyncTaskOutput(t *testing.T) {
	current := &Exchange{Messages: []Message{
		{Role: schema.RoleAgent, Tool: &ToolInfo{Name: "run_command", Input: map[string]any{"CommandLine": "long-task"}}},
	}}

	attachToolResult(
		transcriptStep{StepIndex: 34, Type: typeRunCommand, Status: "RUNNING", Content: "Command still running."},
		current,
		map[int]string{34: "final async output"},
	)

	out, _ := current.Messages[0].Tool.Output["content"].(string)
	if !strings.Contains(out, "Command still running.") || !strings.Contains(out, "final async output") {
		t.Errorf("expected inline and task output to be preserved, got %q", out)
	}
}

func TestConvertToolCallMessage(t *testing.T) {
	tc := transcriptToolCall{Name: "view_file", Args: map[string]any{"AbsolutePath": "/proj/main.go"}}
	msg := convertToolCallMessage(tc, "conv:2:0", "model-x", "2026-05-26T21:31:14Z", "/proj")

	if msg.Role != schema.RoleAgent {
		t.Errorf("expected agent role, got %q", msg.Role)
	}
	if msg.Tool == nil || msg.Tool.Type != schema.ToolTypeRead || msg.Tool.UseID != "conv:2:0" {
		t.Fatalf("unexpected tool: %+v", msg.Tool)
	}
	if msg.Tool.FormattedMarkdown == nil || !strings.Contains(*msg.Tool.FormattedMarkdown, "main.go") {
		t.Errorf("expected rendered input to mention the path")
	}
	if len(msg.PathHints) == 0 {
		t.Errorf("expected path hints to be extracted")
	}
}

func TestGenerateAgentSession_Validates(t *testing.T) {
	session := &agSession{
		ConversationID: "conv-1",
		Workspace:      "/proj",
		CreatedAt:      "2026-05-26T21:31:13Z",
		UpdatedAt:      "2026-05-26T21:31:16Z",
		Model:          "Gemini 3.5 Flash (High)",
		Steps: []transcriptStep{
			{StepIndex: 0, Type: typeUserInput, Source: sourceUserExplicit, CreatedAt: "2026-05-26T21:31:13Z", Content: "<USER_REQUEST>\nhi\n</USER_REQUEST>"},
			{StepIndex: 2, Type: typePlannerResponse, CreatedAt: "2026-05-26T21:31:14Z", Content: "Hello!"},
		},
	}

	data, err := generateAgentSession(session, "/proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.Provider.ID != providerSchemaID || data.Provider.Name != providerName {
		t.Errorf("unexpected provider info: %+v", data.Provider)
	}
	if data.Provider.Version != "Gemini 3.5 Flash (High)" {
		t.Errorf("expected version to be the model, got %q", data.Provider.Version)
	}
	if data.SessionID != "conv-1" || data.WorkspaceRoot != "/proj" {
		t.Errorf("unexpected session fields: %+v", data)
	}
	if !data.Validate() {
		t.Errorf("expected generated session data to pass schema validation")
	}
}

func TestGenerateAgentSession_NoExchanges(t *testing.T) {
	// A session whose only steps are non-conversational yields an error.
	session := &agSession{
		ConversationID: "conv-empty",
		Steps:          []transcriptStep{{StepIndex: 0, Type: typeConversationHistory}},
	}
	if _, err := generateAgentSession(session, "/proj"); err == nil {
		t.Errorf("expected error for session with no exchanges")
	}
}

func TestGenerateAgentSession_UnknownModelFallsBack(t *testing.T) {
	session := &agSession{
		ConversationID: "conv-1",
		Steps: []transcriptStep{
			{Type: typeUserInput, Source: sourceUserExplicit, Content: "<USER_REQUEST>\nhi\n</USER_REQUEST>"},
			{Type: typePlannerResponse, Content: "hello"},
		},
	}
	data, err := generateAgentSession(session, "/proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.Provider.Version != "unknown" {
		t.Errorf("expected version fallback to 'unknown', got %q", data.Provider.Version)
	}
}

func TestCommonPathPrefix(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{name: "empty", paths: nil, want: ""},
		{name: "single path yields itself", paths: []string{"/a/b/c.go"}, want: "/a/b/c.go"},
		{name: "common ancestor", paths: []string{"/a/b/c.go", "/a/b/d.txt", "/a/b/sub/e"}, want: "/a/b"},
		{name: "no shared root", paths: []string{"/a/x", "/b/y"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commonPathPrefix(tt.paths); got != tt.want {
				t.Errorf("commonPathPrefix(%v) = %q, want %q", tt.paths, got, tt.want)
			}
		})
	}
}

func TestResolveSessionWorkspace(t *testing.T) {
	steps := []transcriptStep{
		{Type: typePlannerResponse, ToolCalls: []transcriptToolCall{{Name: "view_file", Args: map[string]any{"AbsolutePath": "file:///proj/a.go"}}}},
		{Type: typePlannerResponse, ToolCalls: []transcriptToolCall{{Name: "run_command", Args: map[string]any{"Cwd": "/proj"}}}},
	}

	// History is authoritative when present.
	history := map[string]historyEntry{"conv-1": {Workspace: "/from/history"}}
	if got := resolveSessionWorkspace("conv-1", steps, history, map[string]string{"conv-1": "/from/project"}); got != "/from/history" {
		t.Errorf("expected history workspace, got %q", got)
	}

	// CLI log/config project mapping is preferred over tool-path inference.
	if got := resolveSessionWorkspace("conv-1", steps, map[string]historyEntry{}, map[string]string{"conv-1": "/from/project"}); got != "/from/project" {
		t.Errorf("expected project mapping workspace, got %q", got)
	}

	// Otherwise inferred from tool paths' common ancestor.
	if got := resolveSessionWorkspace("conv-1", steps, map[string]historyEntry{}, nil); got != "/proj" {
		t.Errorf("expected inferred workspace /proj, got %q", got)
	}
}

func TestResolveSessionWorkspace_SingleFileUsesParent(t *testing.T) {
	steps := []transcriptStep{
		{Type: typePlannerResponse, ToolCalls: []transcriptToolCall{{Name: "view_file", Args: map[string]any{"AbsolutePath": "file:///proj/sub/main.go"}}}},
	}

	if got := resolveSessionWorkspace("conv-1", steps, map[string]historyEntry{}, nil); got != "/proj/sub" {
		t.Errorf("expected inferred workspace /proj/sub, got %q", got)
	}
}

func TestSessionMatchesProject(t *testing.T) {
	root := t.TempDir()

	session := &agSession{
		Workspace: root,
		Steps:     []transcriptStep{{Type: typePlannerResponse, ToolCalls: []transcriptToolCall{{Name: "view_file", Args: map[string]any{"AbsolutePath": root + "/main.go"}}}}},
	}

	if !sessionMatchesProject(session, "") {
		t.Errorf("empty project path should match any session")
	}
	if !sessionMatchesProject(session, root) {
		t.Errorf("session should match its own workspace")
	}
	if sessionMatchesProject(session, t.TempDir()) {
		t.Errorf("session should not match an unrelated project dir")
	}
}

func TestDeriveSlug(t *testing.T) {
	session := &agSession{Steps: []transcriptStep{
		{Type: typeUserInput, Content: "<USER_REQUEST>\nFix the login bug\n</USER_REQUEST>"},
	}}
	if got := deriveSlug(session); got == "" || got == fallbackSlug {
		t.Errorf("expected a derived slug, got %q", got)
	}

	empty := &agSession{Steps: []transcriptStep{{Type: typePlannerResponse, Content: "x"}}}
	if got := deriveSlug(empty); got != fallbackSlug {
		t.Errorf("expected fallback slug, got %q", got)
	}
}
