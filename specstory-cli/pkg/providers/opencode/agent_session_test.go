package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// loadSession loads fixtures with fixtureToolsDir mapped to a fresh project
// and returns the converted session.
func loadSession(t *testing.T, sessionID string, fixtures ...string) (*spi.AgentChatSession, string) {
	t.Helper()
	project := newProjectDir(t, "oc-tools")
	loadFixtures(t, map[string]string{fixtureToolsDir: project, fixtureOC1Dir: project}, fixtures...)
	session, err := NewProvider().GetAgentChatSession(project, sessionID, false)
	if err != nil {
		t.Fatalf("GetAgentChatSession(%s) error = %v", sessionID, err)
	}
	if session == nil {
		t.Fatalf("GetAgentChatSession(%s) = nil", sessionID)
	}
	return session, project
}

// allMessages flattens the session's exchanges.
func allMessages(data *schema.SessionData) []schema.Message {
	var messages []schema.Message
	for _, exchange := range data.Exchanges {
		messages = append(messages, exchange.Messages...)
	}
	return messages
}

func messageText(message schema.Message) string {
	var parts []string
	for _, part := range message.Content {
		parts = append(parts, part.Text)
	}
	return strings.Join(parts, "\n")
}

// nativeToolCalls lists the tool part names of one session in the order the
// native records store them.
func nativeToolCalls(t *testing.T, fixture, sessionID string) []string {
	t.Helper()
	var names []string
	for _, record := range readFixture(t, fixture, nil) {
		if record.Table != messageTable || record.Row["session_id"] != sessionID || record.Row["type"] != recordAssistant {
			continue
		}
		data, _ := json.Marshal(record.Row["data"])
		var msg nativeMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		for _, part := range msg.Content {
			if part.Type == partTool {
				names = append(names, part.Name)
			}
		}
	}
	return names
}

func TestConvertToolExerciseSession(t *testing.T) {
	session, project := loadSession(t, toolSessionID, "tool-exercise.jsonl")
	data := session.SessionData

	if data.Provider != (schema.ProviderInfo{ID: "opencode", Name: "OpenCode", Version: "2.0.14"}) {
		t.Errorf("Provider = %+v", data.Provider)
	}
	if data.WorkspaceRoot != project {
		t.Errorf("WorkspaceRoot = %q, want %q", data.WorkspaceRoot, project)
	}
	if data.CreatedAt != "2026-09-22T23:04:03.168Z" {
		t.Errorf("CreatedAt = %q", data.CreatedAt)
	}
	if session.Slug != "please-use-each-of" || data.Slug != session.Slug {
		t.Errorf("Slug = %q / %q", session.Slug, data.Slug)
	}
	if !data.Validate() {
		t.Error("SessionData failed schema validation")
	}

	// Every native invocation renders once, in native order, with markdown.
	var rendered []string
	for _, message := range allMessages(data) {
		if message.Tool == nil {
			continue
		}
		rendered = append(rendered, message.Tool.Name)
		if message.Tool.FormattedMarkdown == nil || strings.TrimSpace(*message.Tool.FormattedMarkdown) == "" {
			t.Errorf("tool %s (%s) has no formatted markdown", message.Tool.Name, message.Tool.UseID)
		}
	}
	if want := nativeToolCalls(t, "tool-exercise.jsonl", toolSessionID); !slices.Equal(rendered, want) {
		t.Errorf("tool calls = %v, want native order %v", rendered, want)
	}

	// The only prompt opens the only exchange.
	if len(data.Exchanges) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(data.Exchanges))
	}
}

func TestConvertTimestampsNeverRunBackwards(t *testing.T) {
	for _, tt := range []struct{ fixture, sessionID string }{
		{"tool-exercise.jsonl", toolSessionID},
		{"tui-session.jsonl", tuiSessionID},
	} {
		t.Run(tt.fixture, func(t *testing.T) {
			session, _ := loadSession(t, tt.sessionID, tt.fixture)
			previous := ""
			for _, message := range allMessages(session.SessionData) {
				if message.Timestamp < previous {
					t.Errorf("message %s at %s precedes %s", message.ID, message.Timestamp, previous)
				}
				previous = message.Timestamp
			}
		})
	}
}

func TestConvertUsageRecordedOncePerStep(t *testing.T) {
	session, _ := loadSession(t, toolSessionID, "tool-exercise.jsonl")

	steps := map[string]int{}
	for _, message := range allMessages(session.SessionData) {
		if message.Usage == nil {
			continue
		}
		step, _, _ := strings.Cut(message.ID, ":")
		steps[step]++
	}
	// The session has seven assistant steps, each with token accounting.
	if len(steps) != 7 {
		t.Errorf("steps with usage = %d, want 7", len(steps))
	}
	for step, count := range steps {
		if count != 1 {
			t.Errorf("step %s carries usage %d times", step, count)
		}
	}
}

func TestConvertTUISession(t *testing.T) {
	session, _ := loadSession(t, tuiSessionID, "tui-session.jsonl")
	data := session.SessionData

	var userTexts []string
	for _, exchange := range data.Exchanges {
		if first := exchange.Messages[0]; first.Role == schema.RoleUser {
			userTexts = append(userTexts, messageText(first))
		}
	}
	if len(userTexts) != 5 {
		t.Fatalf("exchanges opened by user records = %d, want 5: %q", len(userTexts), userTexts)
	}

	// A user `!` command reads as historical activity, with its output.
	shell := userTexts[1]
	for _, want := range []string{"User ran a shell command:", "echo hello from user shell && ls *.py", "hello from user shell", "Exit code: 0"} {
		if !strings.Contains(shell, want) {
			t.Errorf("shell message missing %q:\n%s", want, shell)
		}
	}

	// OpenCode stores an expanded slash command as the user's text, with no
	// marker naming the command.
	if !strings.HasPrefix(userTexts[2], "You are a code reviewer.") {
		t.Errorf("expanded /review template not rendered as the prompt: %.60q", userTexts[2])
	}

	var all strings.Builder
	var compaction string
	for _, message := range allMessages(data) {
		text := messageText(message)
		all.WriteString(text)
		if strings.HasPrefix(text, "Conversation compacted (manual). Summary:") {
			compaction = text
		}
	}
	if !strings.Contains(compaction, "## Objective") {
		t.Errorf("compaction summary not rendered: %.80q", compaction)
	}
	// Scaffolding OpenCode injects for the model never renders.
	for _, scaffold := range []string{"<system-reminder>", "The following shell command was executed by the user"} {
		if strings.Contains(all.String(), scaffold) {
			t.Errorf("scaffolding %q rendered", scaffold)
		}
	}
}

func TestConvertFailedStepAndEncryptedReasoning(t *testing.T) {
	session, _ := loadSession(t, modelErrSessionID, "model-error.jsonl")
	messages := allMessages(session.SessionData)

	var texts []string
	for _, message := range messages {
		for _, part := range message.Content {
			if part.Type == schema.ContentTypeThinking {
				// The only reasoning part is encrypted, with no text.
				t.Errorf("unexpected thinking %q", part.Text)
			}
		}
		if message.Role == schema.RoleAgent {
			texts = append(texts, message.Model+": "+messageText(message))
		}
	}
	want := []string{
		"au.anthropic.claude-opus-5-5: [error] The provided model identifier is invalid.",
		"muse-spark-1.2-contributor-free: Hi! How can I help you today?",
	}
	if !slices.Equal(texts, want) {
		t.Errorf("agent messages = %q, want %q", texts, want)
	}
}

func TestConvertSkipsCorruptRecord(t *testing.T) {
	db := createFixtureDB(t, useFixtureStore(t))
	project := newProjectDir(t, "project")
	insertSession(t, db, "ses_corrupt", project, 1000, 4000)
	insertMessage(t, db, "ses_corrupt", "msg_1", recordUser, 1, 1000, userData(1000, "first prompt"))
	insertMessage(t, db, "ses_corrupt", "msg_2", recordAssistant, 2, 2000, `{"time": {"created": 2000}, "content": [`)
	insertMessage(t, db, "ses_corrupt", "msg_3", recordAssistant, 3, 3000, assistantData(3000, "still here"))

	session, err := NewProvider().GetAgentChatSession(project, "ses_corrupt", false)
	if err != nil || session == nil {
		t.Fatalf("GetAgentChatSession() = %v, %v", session, err)
	}
	messages := allMessages(session.SessionData)
	if len(messages) != 2 || messageText(messages[1]) != "still here" {
		t.Errorf("messages around a corrupt record = %+v", messages)
	}
}

func TestRawDataPreservesEveryRow(t *testing.T) {
	session, _ := loadSession(t, modelErrSessionID, "model-error.jsonl")

	lines := strings.Split(strings.TrimSpace(session.RawData), "\n")
	fixtureLines := len(readFixture(t, "model-error.jsonl", nil))
	if len(lines) != fixtureLines {
		t.Fatalf("RawData lines = %d, want %d (session row + every message row)", len(lines), fixtureLines)
	}
	var first rawRecord
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil || first.Table != sessionTable {
		t.Fatalf("first RawData record = %+v, %v", first, err)
	}
	// Fields the converter never reads survive: provider state, the encrypted
	// reasoning blob, and session columns such as idle_outcome.
	for _, field := range []string{`"providerState"`, `"reasoningEncryptedContent"`, `"idle_outcome"`} {
		if !strings.Contains(session.RawData, field) {
			t.Errorf("RawData dropped %s", field)
		}
	}
}

func TestDebugRawExport(t *testing.T) {
	debugDir := testutil.IsolateDebugDir(t)
	db := createFixtureDB(t, useFixtureStore(t))
	project := newProjectDir(t, "project")
	insertSession(t, db, "ses_debug", project, 1000, 3000)
	insertMessage(t, db, "ses_debug", "msg_1", recordUser, 1, 1000, userData(1000, "hello"))
	insertMessage(t, db, "ses_debug", "msg_2", recordAssistant, 2, 2000,
		`{"time":{"created":2000},"agent":"build","model":{"id":"m","providerID":"p"},"content":[{"type":"text","text":"hi"}],"unrecognizedField":{"kept":true}}`)
	p := NewProvider()
	sessionDir := filepath.Join(debugDir, "ses_debug")

	if _, err := p.GetAgentChatSession(project, "ses_debug", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("debug output written without debugRaw: %v", err)
	}

	if _, err := p.GetAgentChatSession(project, "ses_debug", true); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(sessionDir, "1.json"))
	if err != nil || !strings.Contains(string(first), `"table": "session_v2"`) {
		t.Fatalf("1.json = %s, %v", first, err)
	}
	third, err := os.ReadFile(filepath.Join(sessionDir, "3.json"))
	if err != nil || !strings.Contains(string(third), `"unrecognizedField"`) {
		t.Fatalf("3.json lost an unrecognized field: %s, %v", third, err)
	}

	// Refreshing after the session shrinks removes the stale record file but
	// keeps the CLI's own session-data.json.
	if _, err := db.Exec(`DELETE FROM session_message WHERE id = 'msg_2'`); err != nil {
		t.Fatal(err)
	}
	testutil.AssertDebugRefresh(t, sessionDir, []string{"3.json"}, []string{"session-data.json"}, func() {
		if _, err := p.GetAgentChatSession(project, "ses_debug", true); err != nil {
			t.Fatal(err)
		}
	})
}
