package copilotide

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeChatSessionsDir creates a workspace dir with a chatSessions subdirectory and
// returns both paths.
func makeChatSessionsDir(t *testing.T) (workspaceDir, chatSessionsDir string) {
	t.Helper()

	workspaceDir = t.TempDir()
	chatSessionsDir = GetChatSessionsPath(workspaceDir)
	if err := os.MkdirAll(chatSessionsDir, 0755); err != nil {
		t.Fatalf("failed to create chatSessions dir: %v", err)
	}
	return workspaceDir, chatSessionsDir
}

// writeSessionFixture writes a session file with the given name and content into the
// chatSessions dir and returns its full path.
func writeSessionFixture(t *testing.T, chatSessionsDir, name, content string) string {
	t.Helper()

	path := filepath.Join(chatSessionsDir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write session fixture %s: %v", name, err)
	}
	return path
}

func TestLoadSessionFile_JSON(t *testing.T) {
	_, chatSessions := makeChatSessionsDir(t)
	path := writeSessionFixture(t, chatSessions, "sess-json.json",
		`{"sessionId":"sess-json","version":3,"requesterUsername":"user",`+
			`"requests":[{"requestId":"r-1","message":{"text":"hello"}}]}`)

	composer, err := LoadSessionFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFile() error = %v", err)
	}
	if composer.SessionID != "sess-json" {
		t.Errorf("SessionID = %q, want %q", composer.SessionID, "sess-json")
	}
	if len(composer.Requests) != 1 || composer.Requests[0].Message.Text != "hello" {
		t.Errorf("Requests = %+v, want one request with text %q", composer.Requests, "hello")
	}
}

func TestLoadSessionFile_JSONL(t *testing.T) {
	_, chatSessions := makeChatSessionsDir(t)
	lines := []string{
		`{"kind":0,"v":{"sessionId":"sess-jsonl","version":3,"requests":[]}}`,
		`{"kind":2,"k":["requests"],"v":[{"requestId":"r-1","message":{"text":"first"}}]}`,
		`{"kind":1,"k":["customTitle"],"v":"Streamed Session"}`,
	}
	path := writeSessionFixture(t, chatSessions, "sess-jsonl.jsonl", strings.Join(lines, "\n"))

	composer, err := LoadSessionFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFile() error = %v", err)
	}
	if composer.SessionID != "sess-jsonl" {
		t.Errorf("SessionID = %q, want %q", composer.SessionID, "sess-jsonl")
	}
	if composer.CustomTitle != "Streamed Session" {
		t.Errorf("CustomTitle = %q, want %q", composer.CustomTitle, "Streamed Session")
	}
	if len(composer.Requests) != 1 || composer.Requests[0].Message.Text != "first" {
		t.Errorf("Requests = %+v, want one request with text %q", composer.Requests, "first")
	}
}

// TestLoadSessionFile_StreamedResponseGrowth simulates VS Code streaming a response:
// the file is rewritten with the SAME request count but the in-progress response text
// grown in place (a new kind:1 line replaces the request's response array with a
// longer one). A reload must reflect the grown text — this is the scenario that
// motivated fingerprint-based dedup in pkg/utils/watch_agents.go, where message-count
// heuristics alone would treat the grown file as unchanged.
func TestLoadSessionFile_StreamedResponseGrowth(t *testing.T) {
	_, chatSessions := makeChatSessionsDir(t)

	initial := `{"kind":0,"v":{"sessionId":"sess-grow","version":3,` +
		`"requests":[{"requestId":"r-1","message":{"text":"write the docs"},` +
		`"response":[{"value":"Partial"}]}]}}`
	path := writeSessionFixture(t, chatSessions, "sess-grow.jsonl", initial)

	composer, err := LoadSessionFile(path)
	if err != nil {
		t.Fatalf("initial LoadSessionFile() error = %v", err)
	}
	if len(composer.Requests) != 1 {
		t.Fatalf("initial len(Requests) = %d, want 1", len(composer.Requests))
	}
	initialText := ExtractTextFromResponseArray(composer.Requests[0].Response)
	if initialText != "Partial" {
		t.Fatalf("initial response text = %q, want %q", initialText, "Partial")
	}

	// Rewrite: VS Code appends an update line that replaces the streaming request's
	// response with a longer version. Message/request count stays identical.
	grown := initial + "\n" +
		`{"kind":1,"k":["requests",0,"response"],"v":[{"value":"Partial answer, now complete with more text."}]}`
	if err := os.WriteFile(path, []byte(grown), 0644); err != nil {
		t.Fatalf("failed to rewrite session fixture: %v", err)
	}

	composer, err = LoadSessionFile(path)
	if err != nil {
		t.Fatalf("reload LoadSessionFile() error = %v", err)
	}
	if len(composer.Requests) != 1 {
		t.Fatalf("reload len(Requests) = %d, want 1 (same message count)", len(composer.Requests))
	}
	grownText := ExtractTextFromResponseArray(composer.Requests[0].Response)
	if grownText != "Partial answer, now complete with more text." {
		t.Errorf("grown response text = %q, want the extended text", grownText)
	}
	if len(grownText) <= len(initialText) {
		t.Errorf("reloaded text did not grow: initial %d chars, reloaded %d chars",
			len(initialText), len(grownText))
	}
}

// TestLoadSessionFile_MalformedUpdateLineSkipped verifies a corrupt line in the middle
// of a JSONL session (e.g. a torn write) doesn't fail the load or drop later updates.
func TestLoadSessionFile_MalformedUpdateLineSkipped(t *testing.T) {
	_, chatSessions := makeChatSessionsDir(t)
	lines := []string{
		`{"kind":0,"v":{"sessionId":"sess-torn","version":3,"requests":[]}}`,
		`{"kind":2,"k":["requests"],"v":[{"requestId":"r-1","message":{"text":"first"}}]`, // torn: missing closing brace
		`{"kind":1,"k":["customTitle"],"v":"Survived"}`,
	}
	path := writeSessionFixture(t, chatSessions, "sess-torn.jsonl", strings.Join(lines, "\n"))

	composer, err := LoadSessionFile(path)
	if err != nil {
		t.Fatalf("LoadSessionFile() error = %v", err)
	}
	if composer.CustomTitle != "Survived" {
		t.Errorf("CustomTitle = %q, want %q (update after torn line must apply)",
			composer.CustomTitle, "Survived")
	}
	if len(composer.Requests) != 0 {
		t.Errorf("len(Requests) = %d, want 0 (torn append must be dropped)", len(composer.Requests))
	}
}

func TestLoadSessionFile_Errors(t *testing.T) {
	_, chatSessions := makeChatSessionsDir(t)

	tests := []struct {
		name            string
		fileName        string
		content         string // ignored when missing is true
		missing         bool
		wantErrContains string
	}{
		{
			name:            "missing file",
			fileName:        "does-not-exist.json",
			missing:         true,
			wantErrContains: "failed to read session file",
		},
		{
			name:            "empty JSONL file",
			fileName:        "empty.jsonl",
			content:         "\n\n",
			wantErrContains: "empty JSONL file",
		},
		{
			name:            "malformed first JSONL line",
			fileName:        "bad-first.jsonl",
			content:         `this is not json`,
			wantErrContains: "failed to parse first JSONL line",
		},
		{
			name:            "first JSONL line is not kind 0",
			fileName:        "wrong-kind.jsonl",
			content:         `{"kind":1,"k":["customTitle"],"v":"oops"}`,
			wantErrContains: "expected kind:0",
		},
		{
			name:            "malformed JSON file",
			fileName:        "bad.json",
			content:         `{"sessionId": `,
			wantErrContains: "failed to parse session JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(chatSessions, tt.fileName)
			if !tt.missing {
				path = writeSessionFixture(t, chatSessions, tt.fileName, tt.content)
			}

			_, err := LoadSessionFile(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErrContains)
			}
			if !strings.Contains(err.Error(), tt.wantErrContains) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErrContains)
			}
		})
	}
}

func TestLoadAllSessionFiles(t *testing.T) {
	workspaceDir, chatSessions := makeChatSessionsDir(t)

	writeSessionFixture(t, chatSessions, "a.json", `{}`)
	writeSessionFixture(t, chatSessions, "b.jsonl", `{}`)
	writeSessionFixture(t, chatSessions, "notes.txt", `ignore me`)
	if err := os.Mkdir(filepath.Join(chatSessions, "subdir.json"), 0755); err != nil {
		t.Fatalf("failed to create decoy subdir: %v", err)
	}

	files, err := LoadAllSessionFiles(workspaceDir)
	if err != nil {
		t.Fatalf("LoadAllSessionFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len(files) = %d, want 2 (json + jsonl only): %v", len(files), files)
	}
	for _, f := range files {
		base := filepath.Base(f)
		if base != "a.json" && base != "b.jsonl" {
			t.Errorf("unexpected session file %q", f)
		}
	}
}

func TestLoadAllSessionFiles_MissingDir(t *testing.T) {
	// Workspace dir exists but has no chatSessions subdirectory.
	_, err := LoadAllSessionFiles(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing chatSessions directory, got nil")
	}
	if !strings.Contains(err.Error(), "chatSessions directory not found") {
		t.Errorf("error = %q, want it to mention the missing chatSessions directory", err.Error())
	}
}

func TestLoadSessionByID(t *testing.T) {
	workspaceDir, chatSessions := makeChatSessionsDir(t)

	// Session with both formats present: .jsonl must win (newer format).
	writeSessionFixture(t, chatSessions, "dual.jsonl",
		`{"kind":0,"v":{"sessionId":"from-jsonl","version":3}}`)
	writeSessionFixture(t, chatSessions, "dual.json", `{"sessionId":"from-json"}`)

	// Session with only the older .json format.
	writeSessionFixture(t, chatSessions, "legacy.json", `{"sessionId":"legacy-json"}`)

	tests := []struct {
		name            string
		sessionID       string
		wantSessionID   string
		wantErrContains string
	}{
		{
			name:          "jsonl preferred over json",
			sessionID:     "dual",
			wantSessionID: "from-jsonl",
		},
		{
			name:          "json fallback when no jsonl",
			sessionID:     "legacy",
			wantSessionID: "legacy-json",
		},
		{
			name:            "unknown session id",
			sessionID:       "nope",
			wantErrContains: "session not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			composer, err := LoadSessionByID(workspaceDir, tt.sessionID)

			if tt.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrContains)
				}
				if !strings.Contains(err.Error(), tt.wantErrContains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if composer.SessionID != tt.wantSessionID {
				t.Errorf("SessionID = %q, want %q", composer.SessionID, tt.wantSessionID)
			}
		})
	}
}

// TestLoadStateFile verifies the optional-state contract: a valid file parses, while
// a missing or malformed file yields (nil, nil) rather than an error, because state
// only enriches a session and must never block loading it.
func TestLoadStateFile(t *testing.T) {
	tests := []struct {
		name      string
		content   string // empty means don't create the file
		wantState bool
	}{
		{
			name:      "valid state file",
			content:   `{"version":2,"sessionId":"s-1","timeline":{"operations":[{"type":"create","uri":{"fsPath":"/proj/a.go"}}]}}`,
			wantState: true,
		},
		{
			name:      "missing state file returns nil without error",
			wantState: false,
		},
		{
			name:      "malformed state file returns nil without error",
			content:   `{"version": `,
			wantState: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspaceDir := t.TempDir()
			const sessionID = "s-1"

			if tt.content != "" {
				statePath := GetStateFilePath(workspaceDir, sessionID)
				if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
					t.Fatalf("failed to create state dir: %v", err)
				}
				if err := os.WriteFile(statePath, []byte(tt.content), 0644); err != nil {
					t.Fatalf("failed to write state file: %v", err)
				}
			}

			state, err := LoadStateFile(workspaceDir, sessionID)
			if err != nil {
				t.Fatalf("LoadStateFile() error = %v (state is optional, must never error)", err)
			}

			if !tt.wantState {
				if state != nil {
					t.Errorf("expected nil state, got %+v", state)
				}
				return
			}

			if state == nil {
				t.Fatal("expected parsed state, got nil")
			}
			if state.Version != 2 || state.SessionID != "s-1" {
				t.Errorf("state = %+v, want version 2 session s-1", state)
			}
			if state.Timeline == nil || len(state.Timeline.Operations) != 1 {
				t.Errorf("state.Timeline = %+v, want one operation", state.Timeline)
			}
		})
	}
}
