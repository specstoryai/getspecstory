package copilotcli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// sourceSession is a session from another agent: a thinking block, a tool call
// with pre-rendered markdown, and two exchanges.
func sourceSession(workspaceRoot string) *schema.SessionData {
	summary := "Tool use: **Bash**"
	body := "\n" + spi.CodeFence("bash", "echo hi") + "\n"
	return &schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		Provider:      schema.ProviderInfo{ID: "claude", Name: "Claude Code", Version: "2.0.0"},
		SessionID:     "source-session",
		CreatedAt:     "2026-10-09T10:00:00Z",
		WorkspaceRoot: workspaceRoot,
		Exchanges: []schema.Exchange{
			{ExchangeID: "e1", Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "The passphrase is ORANGE-PENGUIN-31. Run echo hi."}}},
				{Role: schema.RoleAgent, Model: "m", Content: []schema.ContentPart{
					{Type: schema.ContentTypeThinking, Text: "I should run it."},
					{Type: schema.ContentTypeText, Text: "Running it."},
				}},
				{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Bash", Type: schema.ToolTypeShell, Summary: &summary, FormattedMarkdown: &body}},
			}},
			{ExchangeID: "e2", Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "Thanks."}}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "You are welcome."}}},
			}},
		},
	}
}

func TestReconstructSession(t *testing.T) {
	useSessionStore(t)
	project := newProjectDir(t, "project")
	data := sourceSession("/elsewhere/source-project")
	opts := spi.ReconstructOptions{WorkspaceRoot: project, MigrationNote: "Imported from Claude Code."}
	p := NewProvider()

	rec, err := p.ReconstructSession(data, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Native structure: session.start first, every record chained to the one
	// before it through parentId, the conversation in prepared order.
	var events []rawEvent
	var parents []any
	for line := range strings.Lines(string(rec.Content)) {
		var envelope struct {
			rawEvent
			ParentID any `json:"parentId"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("not a native record: %q", line)
		}
		events = append(events, envelope.rawEvent)
		parents = append(parents, envelope.ParentID)
	}
	if len(events) == 0 || events[0].Type != eventSessionStart {
		t.Fatalf("first record is not session.start: %v", events)
	}
	for i := 1; i < len(events); i++ {
		if parents[i] != events[i-1].ID {
			t.Errorf("record %d parentId = %v, want %s", i, parents[i], events[i-1].ID)
		}
	}
	if parents[0] != nil {
		t.Errorf("session.start parentId = %v, want null", parents[0])
	}

	var start map[string]any
	if err := json.Unmarshal(events[0].Data, &start); err != nil {
		t.Fatal(err)
	}
	if start["sessionId"] != rec.SessionID || start["specstorySourceSessionId"] != "source-session" {
		t.Errorf("session.start = %v, want the new id and the provenance back-link", start)
	}
	if version, present := start["copilotVersion"]; !present || version != "" {
		t.Errorf("copilotVersion = %v (present %v), want present and empty", version, present)
	}
	if cwd := start["context"].(map[string]any)["cwd"]; cwd != project {
		t.Errorf("cwd = %v, want the resume destination %s", cwd, project)
	}

	want, err := spi.PrepareTurns(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	var got []spi.Turn
	for _, event := range events[1:] {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(event.Data, &body); err != nil {
			t.Fatal(err)
		}
		role := schema.RoleAgent
		if event.Type == eventUserMessage {
			role = schema.RoleUser
		}
		got = append(got, spi.Turn{Role: role, Text: body.Content})
	}
	if !slices.Equal(got, want) {
		t.Errorf("reconstructed turns =\n%q\nwant the prepared turns\n%q", got, want)
	}

	// The file lands where the provider will find and resume it.
	path, err := p.NativeSessionPath(project, rec.Filename)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := sessionsRoot()
	if path != filepath.Join(root, rec.SessionID, eventsFileName) {
		t.Errorf("NativeSessionPath = %s, want the session's own events.jsonl", path)
	}
}

// TestReconstructedSessionReadsBack checks the provider's own reader accepts
// what reconstruction writes, named by the first user prompt rather than the
// migration note.
func TestReconstructedSessionReadsBack(t *testing.T) {
	home := useSessionStore(t)
	project := newProjectDir(t, "project")
	p := NewProvider()
	rec, err := p.ReconstructSession(sourceSession(project), spi.ReconstructOptions{WorkspaceRoot: project, MigrationNote: "Imported."})
	if err != nil {
		t.Fatal(err)
	}
	writeSession(t, home, rec.SessionID, strings.Split(strings.TrimSuffix(string(rec.Content), "\n"), "\n")...)

	session, err := p.GetAgentChatSession(project, rec.SessionID, false)
	if err != nil || session == nil {
		t.Fatalf("GetAgentChatSession = %v, %v", session, err)
	}
	if session.Slug != "the-passphrase-is-orange" {
		t.Errorf("slug = %q, want it named by the first user prompt", session.Slug)
	}
	if session.SessionData.Provider.Version != "unknown" || !session.SessionData.Validate() {
		t.Errorf("read-back session data invalid or versioned: %+v", session.SessionData.Provider)
	}
}

func TestNativeSessionPathRejectsForeignNames(t *testing.T) {
	useSessionStore(t)
	p := NewProvider()
	if !p.SupportsReconstruction() {
		t.Fatal("SupportsReconstruction must agree with a working ReconstructSession")
	}
	for _, name := range []string{"events.jsonl", "abc.json", "../escape.jsonl", "a/b.jsonl", ".jsonl"} {
		if _, err := p.NativeSessionPath("", name); err == nil || errors.Is(err, spi.ErrReconstructionUnsupported) {
			t.Errorf("NativeSessionPath(%q) = %v, want a rejection", name, err)
		}
	}
}
