package antigravitycli

import (
	"os"
	"path/filepath"
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
			name: "mismatched metadata tags are not stripped",
			raw:  "keep\n<ADDITIONAL_METADATA>\ndo not delete\n</USER_SETTINGS_CHANGE>\n<SYSTEM_MESSAGE>\ndrop\n</SYSTEM_MESSAGE>",
			want: "keep\n<ADDITIONAL_METADATA>\ndo not delete\n</USER_SETTINGS_CHANGE>",
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

func TestBuildExchanges_SkipsCheckpoint(t *testing.T) {
	// A CHECKPOINT step is a conversation-truncation marker, not user-visible
	// content. It must be skipped deliberately: it should neither start/extend an
	// exchange nor leak its "{{ CHECKPOINT N }} … truncated …" text into any
	// message.
	session := &agSession{
		ConversationID: "conv-cp",
		CreatedAt:      "2026-07-21T10:00:00Z",
		Steps: []transcriptStep{
			{StepIndex: 0, Type: typeUserInput, Source: sourceUserExplicit, CreatedAt: "2026-07-21T10:00:00Z", Content: "<USER_REQUEST>\nremember X\n</USER_REQUEST>"},
			{StepIndex: 1, Type: typePlannerResponse, CreatedAt: "2026-07-21T10:00:01Z", Content: "Noted."},
			{StepIndex: 2, Type: typeCheckpoint, CreatedAt: "2026-07-21T10:00:02Z", Content: "{{ CHECKPOINT 0 }}\nThe earlier parts of this conversation have been truncated due to its long length."},
			{StepIndex: 3, Type: typeUserInput, Source: sourceUserExplicit, CreatedAt: "2026-07-21T10:00:03Z", Content: "<USER_REQUEST>\nwhat is X\n</USER_REQUEST>"},
			{StepIndex: 4, Type: typePlannerResponse, CreatedAt: "2026-07-21T10:00:04Z", Content: "X."},
		},
	}

	exchanges := buildExchanges(session, "")

	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges (checkpoint skipped), got %d", len(exchanges))
	}
	for _, ex := range exchanges {
		for _, msg := range ex.Messages {
			for _, part := range msg.Content {
				if strings.Contains(part.Text, "CHECKPOINT") || strings.Contains(part.Text, "truncated") {
					t.Errorf("checkpoint content leaked into a message: %q", part.Text)
				}
			}
		}
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

// A dedicated result type (here SEARCH_WEB, observed in real transcripts) must
// route past an older pending call of a different type to the matching one —
// under FIFO the web search completing before a slow command would attach its
// result to the command.
func TestAttachToolResult_DedicatedResultTypesRouteByType(t *testing.T) {
	current := &Exchange{Messages: []Message{
		{Role: schema.RoleAgent, Tool: &ToolInfo{Name: "run_command", Type: schema.ToolTypeShell, Input: map[string]any{"CommandLine": "sleep 5"}}},
		{Role: schema.RoleAgent, Tool: &ToolInfo{Name: "search_web", Type: schema.ToolTypeSearch, Input: map[string]any{"query": "x"}}},
	}}

	attachToolResult(transcriptStep{Type: typeSearchWeb, Content: "WEB-RESULTS"}, current, nil)

	if current.Messages[0].Tool.Output != nil {
		t.Errorf("run_command wrongly received the search result")
	}
	if out, _ := current.Messages[1].Tool.Output["content"].(string); out != "WEB-RESULTS" {
		t.Errorf("search_web output = %q, want WEB-RESULTS", out)
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
	// The model belongs on each agent message, not on Provider.Version (which is
	// the CLI version — unrecorded by Antigravity, so a constant "unknown").
	agentMsg := data.Exchanges[0].Messages[1]
	if agentMsg.Model != "Gemini 3.5 Flash (High)" {
		t.Errorf("expected agent message to carry the model, got %q", agentMsg.Model)
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

func TestResolveSessionWorkspace(t *testing.T) {
	tests := []struct {
		name              string
		history           map[string]historyEntry
		projectWorkspaces map[string]string
		want              string
	}{
		{
			name:              "history is authoritative over the project mapping",
			history:           map[string]historyEntry{"conv-1": {Workspace: "/from/history"}},
			projectWorkspaces: map[string]string{"conv-1": "/from/project"},
			want:              "/from/history",
		},
		{
			name:              "falls back to the CLI log/config project mapping",
			history:           map[string]historyEntry{},
			projectWorkspaces: map[string]string{"conv-1": "/from/project"},
			want:              "/from/project",
		},
		{
			name:              "a history entry with a blank workspace does not shadow the mapping",
			history:           map[string]historyEntry{"conv-1": {Workspace: "  "}},
			projectWorkspaces: map[string]string{"conv-1": "/from/project"},
			want:              "/from/project",
		},
		{
			// Deliberately NOT inferred from the paths the tools touched: the
			// common ancestor collapses above the project as soon as one path
			// falls outside it, which would attach the session to every
			// unrelated project beneath that ancestor.
			name:              "no stated workspace yields empty rather than a guess",
			history:           map[string]historyEntry{},
			projectWorkspaces: nil,
			want:              "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveSessionWorkspace("conv-1", tt.history, tt.projectWorkspaces); got != tt.want {
				t.Errorf("resolveSessionWorkspace() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A session with no stated workspace (agy -p print mode) is placed purely by the
// paths its tools touched. It must reach the project it actually worked in and
// no other — in particular it must not match a sibling project just because both
// live under a directory the session also read from.
func TestSessionMatchesProject_NoStatedWorkspace(t *testing.T) {
	home := t.TempDir()
	projA := filepath.Join(home, "Source", "projA")
	projB := filepath.Join(home, "Source", "projB")
	for _, dir := range []string{projA, projB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// The files must exist: path matching canonicalizes both sides, and on macOS
	// the temp root is a symlink, so an absent file cannot be resolved to the
	// same real path as the project directory it sits in.
	touched := filepath.Join(projA, "main.go")
	dotfile := filepath.Join(home, ".zshrc")
	for _, f := range []string{touched, dotfile} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	session := &agSession{
		Workspace: "",
		Steps: []transcriptStep{{Type: typePlannerResponse, ToolCalls: []transcriptToolCall{
			{Name: "view_file", Args: map[string]any{"AbsolutePath": dotfile}},
			{Name: "view_file", Args: map[string]any{"AbsolutePath": touched}},
		}}},
	}

	if !sessionMatchesProject(session, projA) {
		t.Errorf("session should match the project whose files it touched")
	}
	if sessionMatchesProject(session, projB) {
		t.Errorf("session must not match a sibling project it never touched")
	}
}

func TestWorkspacesOverlap(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name      string
		workspace string
		project   string
		want      bool
	}{
		{name: "identical", workspace: root, project: root, want: true},
		{name: "workspace inside project", workspace: sub, project: root, want: true},
		{name: "project inside workspace (monorepo root)", workspace: root, project: sub, want: true},
		{name: "unrelated", workspace: root, project: t.TempDir(), want: false},
		{name: "empty workspace never overlaps", workspace: "", project: root, want: false},
		{name: "blank workspace never overlaps", workspace: "   ", project: root, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspacesOverlap(tt.workspace, tt.project); got != tt.want {
				t.Errorf("workspacesOverlap(%q, %q) = %v, want %v", tt.workspace, tt.project, got, tt.want)
			}
		})
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
