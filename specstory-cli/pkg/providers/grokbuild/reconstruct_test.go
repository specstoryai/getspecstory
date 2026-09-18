package grokbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	sessionDir := filepath.Join(home, "sessions", EncodeCwdDirname(spi.CanonicalizePathOrClean(project)), result.SessionID)
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
	if strings.Contains(content, `"type":"system"`) || strings.Contains(content, "grok-4.6") {
		t.Error("reconstruction must not fabricate a system prompt or model identity")
	}
	if strings.Contains(content, `"model_id"`) || !strings.Contains(content, `"specstorySourceSessionId":"source-session-id"`) {
		t.Error("fabricated model fields or missing source provenance")
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
	if summary.Info.Cwd != spi.CanonicalizePathOrClean(project) {
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

	groupDir := filepath.Join(home, "sessions", EncodeCwdDirname(spi.CanonicalizePathOrClean(project)))
	summary, err := readSummary(filepath.Join(groupDir, "11111111-2222-7333-8444-555555555555", summaryFile))
	if err != nil || summary == nil {
		t.Fatalf("summary.json did not parse: %v", err)
	}
	if summary.SessionSummary != "Read the README" {
		t.Errorf("an existing session's metadata was overwritten: %q", summary.SessionSummary)
	}
}

func TestNativeSessionPathRejectsLinkedOrNonDirectorySession(t *testing.T) {
	for _, kind := range []string{"directory link", "dangling link", "file"} {
		t.Run(kind, func(t *testing.T) {
			home := withFakeGrokHome(t)
			project := t.TempDir()
			id := "11111111-2222-7333-8444-555555555555"
			group := filepath.Join(home, "sessions", EncodeCwdDirname(spi.CanonicalizePathOrClean(project)))
			if err := os.MkdirAll(group, 0o700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(group, id)
			target := t.TempDir()
			sentinel := filepath.Join(target, chatHistoryFile)
			if err := os.WriteFile(sentinel, []byte("other project's conversation"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				if err := os.WriteFile(dir, []byte("occupied"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				linkTarget := target
				if kind == "dangling link" {
					linkTarget = filepath.Join(target, "missing")
				}
				if err := os.Symlink(linkTarget, dir); err != nil {
					t.Skipf("directory symlinks unavailable: %v", err)
				}
			}
			path, err := NewProvider().NativeSessionPath(project, filepath.Join(id, chatHistoryFile))
			if err == nil || path != "" {
				t.Fatalf("accepted %s: %q, %v", kind, path, err)
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 1 || entries[0].Name() != chatHistoryFile {
				t.Fatalf("reconstruction changed link target: %v, %v", entries, err)
			}
			contents, err := os.ReadFile(sentinel)
			if err != nil || string(contents) != "other project's conversation" {
				t.Fatalf("reconstruction changed other transcript: %q, %v", contents, err)
			}
		})
	}
}

func TestNativeSessionPathRejectsLinkedOrNonDirectoryGroup(t *testing.T) {
	for _, kind := range []string{"directory link", "dangling link", "file"} {
		t.Run(kind, func(t *testing.T) {
			home := withFakeGrokHome(t)
			project := t.TempDir()
			id := "11111111-2222-7333-8444-555555555555"
			group := filepath.Join(home, "sessions", EncodeCwdDirname(spi.CanonicalizePathOrClean(project)))
			if err := os.MkdirAll(filepath.Dir(group), 0o700); err != nil {
				t.Fatal(err)
			}
			dir := group
			target := t.TempDir()
			sentinel := filepath.Join(target, chatHistoryFile)
			if err := os.WriteFile(sentinel, []byte("other project's conversation"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				if err := os.WriteFile(dir, []byte("occupied"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				linkTarget := target
				if kind == "dangling link" {
					linkTarget = filepath.Join(target, "missing")
				}
				if err := os.Symlink(linkTarget, dir); err != nil {
					t.Skipf("directory symlinks unavailable: %v", err)
				}
			}
			path, err := NewProvider().NativeSessionPath(project, filepath.Join(id, chatHistoryFile))
			if err == nil || path != "" {
				t.Fatalf("accepted %s: %q, %v", kind, path, err)
			}
			entries, err := os.ReadDir(target)
			if err != nil || len(entries) != 1 || entries[0].Name() != chatHistoryFile {
				t.Fatalf("reconstruction changed link target: %v, %v", entries, err)
			}
			contents, err := os.ReadFile(sentinel)
			if err != nil || string(contents) != "other project's conversation" {
				t.Fatalf("reconstruction changed other transcript: %q, %v", contents, err)
			}
		})
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

func TestNativeSessionPathRejectsEscapingFilename(t *testing.T) {
	withFakeGrokHome(t)
	for _, name := range []string{filepath.Join("..", "outside", chatHistoryFile), filepath.Join("not-a-session", chatHistoryFile), "summary.json"} {
		if path, err := NewProvider().NativeSessionPath(t.TempDir(), name); err == nil {
			t.Errorf("accepted %q as %q", name, path)
		}
	}
}

func TestNativeSessionPathCanonicalFirstUse(t *testing.T) {
	for _, mode := range []string{"symlink", "case"} {
		t.Run(mode, func(t *testing.T) {
			withFakeGrokHome(t)
			root := t.TempDir()
			project := filepath.Join(root, "Project space_under")
			if err := os.Mkdir(project, 0o700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(root, "project SPACE_UNDER")
			if mode == "symlink" {
				alias = filepath.Join(root, "alias")
				if err := os.Symlink(project, alias); err != nil {
					t.Skipf("directory symlinks unavailable: %v", err)
				}
			} else if _, err := os.Stat(alias); err != nil {
				t.Skip("filesystem is case sensitive")
			}
			id := "019ffaaa-1111-7222-8333-444444444444"
			path, err := NewProvider().NativeSessionPath(alias, filepath.Join(id, chatHistoryFile))
			if err != nil {
				t.Fatal(err)
			}
			group, err := ResolveGrokProjectDir(project)
			if err != nil || path != filepath.Join(group, id, chatHistoryFile) {
				t.Fatalf("canonical discovery cannot find reconstruction: %q, %v", group, err)
			}
			summary, err := readSummary(filepath.Join(filepath.Dir(path), summaryFile))
			if err != nil || summary.Info.Cwd != spi.CanonicalizePathOrClean(project) {
				t.Fatalf("summary does not carry canonical project: %#v, %v", summary, err)
			}
		})
	}
}

func TestReconstructionPreservesPreparedTurns(t *testing.T) {
	data := sampleSessionData()
	summary, markdown := "Read QA file", "Result: QA-TOOL-CONTENT"
	data.Exchanges[0].Messages = append(data.Exchanges[0].Messages,
		schema.Message{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: "thinking", Text: "QA-THINKING-CONTENT"}}},
		schema.Message{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "read_file", Summary: &summary, FormattedMarkdown: &markdown}},
		schema.Message{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: "text", Text: "<command-name>/context</command-name>"}}},
	)
	opts := spi.ReconstructOptions{MigrationNote: "Imported QA conversation"}
	want, err := spi.PrepareTurns(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	native, err := NewProvider().ReconstructSession(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	var got []spi.Turn
	for _, line := range strings.Split(strings.TrimSpace(string(native.Content)), "\n") {
		var record GrokRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if text, ok := record.UserQuery(); ok {
			got = append(got, spi.Turn{Role: schema.RoleUser, Text: text})
		} else if record.Type == "assistant" {
			got = append(got, spi.Turn{Role: schema.RoleAgent, Text: record.TextContent()})
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prepared conversation changed: %#v, want %#v", got, want)
	}
	if strings.Contains(string(native.Content), "command-name") {
		t.Fatal("slash-command scaffolding replayed")
	}
}

func TestSummaryNeverFollowsExistingLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "native-summary.json")
	path := filepath.Join(dir, summaryFile)
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if err := writeSessionSummary(dir, "/project"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("summary creation followed existing entry: %v", err)
	}
}
