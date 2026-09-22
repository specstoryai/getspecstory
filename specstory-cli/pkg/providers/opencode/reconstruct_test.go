package opencode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

var openCodeIDPattern = regexp.MustCompile(`^(ses|msg)_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

// reconstructionSource is a session with plain turns, thinking and a tool
// call, as the forward path would produce it.
func reconstructionSource() *schema.SessionData {
	summary := "Tool use: **shell**"
	formatted := "\n```bash\nls\n```\n\nResult:\n\n```text\nhello.py\n```\n"
	return &schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		Provider:      schema.ProviderInfo{ID: "claude", Name: "Claude Code", Version: "2.1.0"},
		SessionID:     "source-session-id",
		CreatedAt:     "2026-09-22T10:00:00Z",
		Slug:          "list-the-files",
		WorkspaceRoot: "/Users/dev/project",
		Exchanges: []schema.Exchange{{
			ExchangeID: "e1",
			Messages: []schema.Message{
				{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "The magic passphrase is PURPLE-ELEPHANT-42. List the files."}}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeThinking, Text: "I should run ls."}}},
				{Role: schema.RoleAgent, Tool: &schema.ToolInfo{Name: "Bash", Type: schema.ToolTypeShell, Summary: &summary, FormattedMarkdown: &formatted}},
				{Role: schema.RoleAgent, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "There is one file: hello.py."}}},
			},
		}},
	}
}

func TestReconstructSession(t *testing.T) {
	source := reconstructionSource()
	opts := spi.ReconstructOptions{WorkspaceRoot: "/Users/dev/target", MigrationNote: "Resumed from a Claude Code session via SpecStory."}
	p := NewProvider()

	rec, err := p.ReconstructSession(source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !openCodeIDPattern.MatchString(rec.SessionID) || !strings.HasPrefix(rec.SessionID, sessionIDPrefix) {
		t.Errorf("SessionID = %q, not an OpenCode session id", rec.SessionID)
	}
	if rec.Filename != rec.SessionID+".json" {
		t.Errorf("Filename = %q", rec.Filename)
	}

	var document struct {
		Info     map[string]any   `json:"info"`
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(rec.Content, &document); err != nil {
		t.Fatalf("export document is not JSON: %v", err)
	}

	info := document.Info
	if info["id"] != rec.SessionID || info["title"] != "list-the-files" {
		t.Errorf("info id/title = %v / %v", info["id"], info["title"])
	}
	if location, _ := info["location"].(map[string]any); location["directory"] != "/Users/dev/target" {
		t.Errorf("location = %v, want the target workspace", info["location"])
	}
	if metadata, _ := info["metadata"].(map[string]any); metadata["specstorySourceSessionId"] != "source-session-id" {
		t.Errorf("metadata = %v, want the provenance back-link", info["metadata"])
	}
	// Required import fields, with neutral values.
	for _, field := range []string{"projectID", "cost", "tokens", "time"} {
		if _, ok := info[field]; !ok {
			t.Errorf("info.%s missing", field)
		}
	}

	// The records carry the prepared turns verbatim and in order.
	turns, err := spi.PrepareTurns(source, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Messages) != len(turns) {
		t.Fatalf("messages = %d, turns = %d", len(document.Messages), len(turns))
	}
	var previousCreated float64
	seen := map[string]bool{}
	for i, message := range document.Messages {
		turn := turns[i]
		id, _ := message["id"].(string)
		if !openCodeIDPattern.MatchString(id) || !strings.HasPrefix(id, messageIDPrefix) || seen[id] {
			t.Errorf("message %d id %q invalid or repeated", i, id)
		}
		seen[id] = true
		created, _ := message["time"].(map[string]any)["created"].(float64)
		if created <= previousCreated {
			t.Errorf("message %d created %v does not follow %v", i, created, previousCreated)
		}
		previousCreated = created

		if turn.Role == schema.RoleUser {
			if message["type"] != recordUser || message["text"] != turn.Text {
				t.Errorf("message %d = %v, want user %q", i, message, turn.Text)
			}
			continue
		}
		content, _ := message["content"].([]any)
		part, _ := content[0].(map[string]any)
		if message["type"] != recordAssistant || len(content) != 1 || part["type"] != "text" || part["text"] != turn.Text {
			t.Errorf("message %d = %v, want assistant text %q", i, message, turn.Text)
		}
		if message["agent"] != "" || message["finish"] != "stop" {
			t.Errorf("message %d agent/finish = %v / %v", i, message["agent"], message["finish"])
		}
		if model, _ := message["model"].(map[string]any); model["id"] != "" || model["providerID"] != "" {
			t.Errorf("message %d model = %v, want neutral", i, message["model"])
		}
	}
	first, _ := document.Messages[0]["content"].([]any)
	if note, _ := first[0].(map[string]any); note["text"] != opts.MigrationNote {
		t.Errorf("first record = %v, want the migration note", document.Messages[0])
	}
	// Thinking and tool activity arrive as ordinary agent text.
	if !strings.Contains(string(rec.Content), "I should run ls.") || !strings.Contains(string(rec.Content), "hello.py") {
		t.Error("thinking or rendered tool activity missing from the reconstruction")
	}
}

// isolateUserCache points the user cache directory at a temp dir on every OS.
func isolateUserCache(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	testutil.SetHome(t, dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	t.Setenv("LocalAppData", filepath.Join(dir, "localappdata"))
}

func TestReconstructionCapabilityAgrees(t *testing.T) {
	isolateUserCache(t)
	p := NewProvider()
	if !p.SupportsReconstruction() {
		t.Fatal("SupportsReconstruction() = false")
	}
	rec, err := p.ReconstructSession(reconstructionSource(), spi.ReconstructOptions{WorkspaceRoot: "/Users/dev/target"})
	if errors.Is(err, spi.ErrReconstructionUnsupported) || err != nil {
		t.Fatalf("ReconstructSession() error = %v", err)
	}
	path, err := p.NativeSessionPath("/Users/dev/target", rec.Filename)
	if err != nil {
		t.Fatalf("NativeSessionPath() error = %v", err)
	}
	// The resume flow writes the document where ExecAgentAndWatch looks for it.
	staged, err := stagedImportPath(stagedImportFilename(rec.SessionID))
	if err != nil || path != staged {
		t.Errorf("NativeSessionPath() = %q, staged import path = %q (%v)", path, staged, err)
	}
	// The staged conversation is private to the user.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("staging directory mode = %v, want 0700", info.Mode().Perm())
		}
	}
}

func TestNewOpenCodeIDOrdering(t *testing.T) {
	early := time.UnixMilli(1790117553306)
	late := early.Add(time.Second)

	earlySession := newOpenCodeID(sessionIDPrefix, early, true)
	lateSession := newOpenCodeID(sessionIDPrefix, late, true)
	// OpenCode's session ids sort newest first.
	if lateSession >= earlySession {
		t.Errorf("session ids not descending: %s then %s", earlySession, lateSession)
	}

	first := newOpenCodeID(messageIDPrefix, late, false)
	second := newOpenCodeID(messageIDPrefix, late, false)
	if second <= first {
		t.Errorf("message ids minted in the same millisecond not ascending: %s then %s", first, second)
	}
	// The layout matches an id OpenCode 2.0.14 minted at the same time.
	if got := newOpenCodeID(sessionIDPrefix, early, true)[:16]; got[:12] != "ses_f34addb6" {
		t.Errorf("session id prefix = %s, want ses_f34addb6... like OpenCode's", got)
	}
}

// fakeOpenCode installs a script that records its arguments, one per line.
func fakeOpenCode(t *testing.T, exitCode string) (command, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	command = filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\" >> '" + argsFile + "'; done\necho imported\nexit " + exitCode + "\n"
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return command, argsFile
}

func TestImportStagedSession(t *testing.T) {
	isolateUserCache(t)
	project := "/Users/dev/target"
	sessionID := "ses_staged"
	staged, err := stagedImportPath(stagedImportFilename(sessionID))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("an id that is not a plain name is never imported", func(t *testing.T) {
		command, argsFile := fakeOpenCode(t, "0")
		outside := filepath.Join(t.TempDir(), "outside.json")
		if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(filepath.Dir(staged), strings.TrimSuffix(outside, ".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := importStagedSession(command, project, relative); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
			t.Errorf("opencode imported a file outside the staging directory: %v", err)
		}
	})

	t.Run("nothing staged runs nothing", func(t *testing.T) {
		command, argsFile := fakeOpenCode(t, "0")
		if err := importStagedSession(command, project, sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
			t.Errorf("opencode ran without a staged session: %v", err)
		}
	})

	t.Run("staged session is imported and removed", func(t *testing.T) {
		command, argsFile := fakeOpenCode(t, "0")
		if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(staged, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Arguments of a custom command configure the interactive launch and
		// are not passed to the import.
		if err := importStagedSession(command+" --auto", project, sessionID); err != nil {
			t.Fatal(err)
		}
		args, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Join([]string{"session", "import", "--directory", project, staged}, "\n") + "\n"
		if string(args) != want {
			t.Errorf("import args =\n%s\nwant\n%s", args, want)
		}
		if _, err := os.Stat(staged); !os.IsNotExist(err) {
			t.Errorf("staged session not removed after import: %v", err)
		}
	})

	t.Run("failed import is reported and the staged session kept", func(t *testing.T) {
		command, _ := fakeOpenCode(t, "4")
		if err := os.WriteFile(staged, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := importStagedSession(command, project, sessionID)
		if err == nil || !strings.Contains(err.Error(), "session import") {
			t.Fatalf("importStagedSession() error = %v", err)
		}
		if _, err := os.Stat(staged); err != nil {
			t.Errorf("staged session removed after a failed import: %v", err)
		}
	})
}

func TestExecuteOpenCodeResumeArguments(t *testing.T) {
	for _, tt := range []struct {
		name, custom, resume string
		want                 []string
		exitCode             string
	}{
		{name: "resume adds the session flag", resume: "ses_new", want: []string{"--session", "ses_new"}, exitCode: "0"},
		{name: "requested session replaces a pinned one", custom: "-s ses_pinned --auto", resume: "ses_new", want: []string{"-s", "ses_new", "--auto"}, exitCode: "0"},
		{name: "no resume keeps the configured arguments", custom: "--auto", want: []string{"--auto"}, exitCode: "0"},
		{name: "non-zero exit is the agent's status", exitCode: "7"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command, argsFile := fakeOpenCode(t, tt.exitCode)
			err := executeOpenCode(strings.TrimSpace(command+" "+tt.custom), t.TempDir(), tt.resume)
			if tt.exitCode != "0" {
				var exitErr *spi.AgentExitError
				if !errors.As(err, &exitErr) || exitErr.Code != 7 {
					t.Fatalf("executeOpenCode() error = %v, want AgentExitError 7", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			args, _ := os.ReadFile(argsFile)
			if got := strings.Fields(string(args)); strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("args = %v, want %v", got, tt.want)
			}
		})
	}
}
