package grokbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func sampleSessionData() *schema.SessionData {
	return &schema.SessionData{
		SchemaVersion: "1.0",
		Provider:      schema.ProviderInfo{ID: "claude-code", Name: "Claude Code", Version: "1.0"},
		SessionID:     "source-session-id",
		CreatedAt:     "2026-08-12T10:00:00.000Z",
		WorkspaceRoot: "/Users/dev/project",
		Slug:          "add-a-divide-function",
		Exchanges: []schema.Exchange{
			{
				ExchangeID: "ex-1",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: "text", Text: "add a divide function"}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: "text", Text: "Done, I added div()."}}},
				},
			},
			{
				ExchangeID: "ex-2",
				Messages: []schema.Message{
					{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: "text", Text: "now add tests"}}},
					{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: "text", Text: "Tests added and passing."}}},
				},
			},
		},
	}
}

func TestReconstructSession_RoundTrip(t *testing.T) {
	provider := NewProvider()

	if !provider.SupportsReconstruction() {
		t.Fatal("the Grok provider should support reconstruction")
	}

	result, err := provider.ReconstructSession(sampleSessionData(), spi.ReconstructOptions{})
	if err != nil {
		t.Fatalf("ReconstructSession failed: %v", err)
	}

	if result.SessionID == "" {
		t.Error("a reconstructed session needs a fresh id")
	}
	// Grok keys a session by directory, so the filename carries one.
	if filepath.Dir(result.Filename) != result.SessionID {
		t.Errorf("Filename = %q, want it under the new session id", result.Filename)
	}
	if filepath.Base(result.Filename) != chatHistoryFile {
		t.Errorf("Filename = %q, want it to be the transcript", result.Filename)
	}

	// The transcript must read back through our own parser, which is also what
	// proves the <user_query> wrapper survives the round trip.
	home := withFakeGrokHome(t)
	project := t.TempDir()
	sessionDir := filepath.Join(home, "sessions", EncodeCwdDirname(canonical(project)), result.SessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, chatHistoryFile), result.Content, 0o644); err != nil {
		t.Fatal(err)
	}
	// summary.json is what NativeSessionPath writes; without it Grok does not see
	// the directory as a session at all.
	if _, err := provider.NativeSessionPath(project, result.Filename); err != nil {
		t.Fatalf("NativeSessionPath failed: %v", err)
	}

	session, err := ParseSessionDir(sessionDir)
	if err != nil {
		t.Fatalf("the reconstructed transcript did not parse: %v", err)
	}

	data, err := GenerateAgentSession(session, project)
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if !data.Validate() {
		t.Error("the reconstructed session failed schema validation")
	}
	if len(data.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2", len(data.Exchanges))
	}

	wantUser := []string{"add a divide function", "now add tests"}
	for i, want := range wantUser {
		got := data.Exchanges[i].Messages[0].Content[0].Text
		if got != want {
			t.Errorf("exchange %d user text = %q, want %q", i, got, want)
		}
	}
}

func TestReconstructSession_OmitsToolAndReasoningRecords(t *testing.T) {
	result, err := NewProvider().ReconstructSession(sampleSessionData(), spi.ReconstructOptions{})
	if err != nil {
		t.Fatalf("ReconstructSession failed: %v", err)
	}

	content := string(result.Content)
	// A tool_call without its tool_result would leave a dangling call, and
	// fabricated reasoning is bound to the model that produced it.
	if strings.Contains(content, `"tool_calls"`) {
		t.Error("reconstruction must not emit tool calls")
	}
	if strings.Contains(content, `"reasoning"`) {
		t.Error("reconstruction must not emit reasoning records")
	}
	// Grok's system prompt keys on this tag, and so does our own parser.
	if !strings.Contains(content, "<user_query>") {
		t.Error("user turns must be wrapped for Grok to read them as requests")
	}
	// Grok expects a system record first. Decode it rather than matching raw
	// text, because Go writes map keys in alphabetical order.
	firstLine := strings.SplitN(strings.TrimSpace(content), "\n", 2)[0]
	var first GrokRecord
	if err := json.Unmarshal([]byte(firstLine), &first); err != nil {
		t.Fatalf("the first record did not parse: %v", err)
	}
	if first.Type != "system" {
		t.Errorf("first record type = %q, want system", first.Type)
	}
}

func TestNativeSessionPath_PreparesSessionDirectory(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()

	path, err := NewProvider().NativeSessionPath(project, filepath.Join("019ffaaa-1111-7222-8333-444444444444", chatHistoryFile))
	if err != nil {
		t.Fatalf("NativeSessionPath failed: %v", err)
	}

	if !strings.HasPrefix(path, filepath.Join(home, "sessions")) {
		t.Errorf("path %q is not inside the Grok store", path)
	}
	if filepath.Base(path) != chatHistoryFile {
		t.Errorf("path basename = %q", filepath.Base(path))
	}

	// Grok reports a directory without summary.json as not found locally, so
	// NativeSessionPath has to write one.
	summaryPath := filepath.Join(filepath.Dir(path), summaryFile)
	if _, err := os.Stat(summaryPath); err != nil {
		t.Fatalf("summary.json was not written: %v", err)
	}

	summary, err := readSummary(summaryPath)
	if err != nil || summary == nil {
		t.Fatalf("summary.json did not parse: %v", err)
	}
	if summary.Info.ID != "019ffaaa-1111-7222-8333-444444444444" {
		t.Errorf("summary id = %q", summary.Info.ID)
	}
	if summary.Info.Cwd != project {
		t.Errorf("summary cwd = %q, want %q", summary.Info.Cwd, project)
	}
}

func TestNativeSessionPath_DoesNotClobberExistingSummary(t *testing.T) {
	home := withFakeGrokHome(t)
	project := t.TempDir()
	seedSession(t, home, project, "session-basic", "11111111-2222-7333-8444-555555555555")

	_, err := NewProvider().NativeSessionPath(project, filepath.Join("11111111-2222-7333-8444-555555555555", chatHistoryFile))
	if err != nil {
		t.Fatalf("NativeSessionPath failed: %v", err)
	}

	groupDir := filepath.Join(home, "sessions", EncodeCwdDirname(canonical(project)))
	summary, err := readSummary(filepath.Join(groupDir, "11111111-2222-7333-8444-555555555555", summaryFile))
	if err != nil || summary == nil {
		t.Fatalf("summary.json did not parse: %v", err)
	}
	if summary.SessionSummary != "Read the README" {
		t.Errorf("an existing session's metadata was overwritten: %q", summary.SessionSummary)
	}
}

func TestEncodeCwdDirname(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{name: "simple path", cwd: "/Users/gdc/painpoints", want: "%2FUsers%2Fgdc%2Fpainpoints"},
		{name: "nested path", cwd: "/Users/gdc/getspecstory/specstory-cli", want: "%2FUsers%2Fgdc%2Fgetspecstory%2Fspecstory-cli"},
		{name: "space becomes %20 not plus", cwd: "/tmp/my project", want: "%2Ftmp%2Fmy%20project"},
		{name: "unreserved characters survive", cwd: "/tmp/a-b_c.d~e", want: "%2Ftmp%2Fa-b_c.d~e"},
		{name: "unicode encodes per byte", cwd: "/tmp/café", want: "%2Ftmp%2Fcaf%C3%A9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EncodeCwdDirname(tt.cwd); got != tt.want {
				t.Errorf("EncodeCwdDirname(%q) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}

func TestEncodeCwdDirname_RoundTripsWithDecode(t *testing.T) {
	paths := []string{
		"/Users/gdc/painpoints",
		"/tmp/my project",
		"/tmp/café",
		"/tmp/a+b",
		"/tmp/a-b_c.d~e",
	}

	for _, path := range paths {
		encoded := EncodeCwdDirname(path)
		decoded, ok := DecodeCwdDirname(filepath.Join(t.TempDir(), encoded))
		if !ok || decoded != path {
			t.Errorf("round trip of %q gave %q (ok=%v) via %q", path, decoded, ok, encoded)
		}
	}
}
