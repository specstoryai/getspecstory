package antigravitycli

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// writeConversationSummary creates (or extends) conversation_summaries.db under
// home and inserts one row, so tests can exercise the best-effort name overlay.
func writeConversationSummary(t *testing.T, home, conversationID, title, preview string) {
	t.Helper()
	dir := filepath.Join(home, geminiRootDir, antigravityDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, conversationSummariesFileName))
	if err != nil {
		t.Fatalf("open summaries db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS conversation_summaries (conversation_id text PRIMARY KEY, title text NOT NULL DEFAULT "", preview text NOT NULL DEFAULT "")`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO conversation_summaries (conversation_id, title, preview) VALUES (?, ?, ?)`, conversationID, title, preview); err != nil {
		t.Fatalf("insert summary: %v", err)
	}
}

func TestName(t *testing.T) {
	if got := NewProvider().Name(); got != providerName {
		t.Errorf("Name() = %q, want %q", got, providerName)
	}
}

func TestClassifyCheckError(t *testing.T) {
	if got := classifyCheckError(&exec.Error{Name: "agy", Err: exec.ErrNotFound}); got != "not_found" {
		t.Errorf("not-found error classified as %q", got)
	}
	if got := classifyCheckError(&os.PathError{Op: "exec", Path: "agy", Err: os.ErrPermission}); got != "permission_denied" {
		t.Errorf("permission error classified as %q", got)
	}
	if got := classifyCheckError(os.ErrInvalid); got != "version_failed" {
		t.Errorf("generic error classified as %q", got)
	}
}

func TestBuildCheckErrorMessage(t *testing.T) {
	tests := []struct {
		name      string
		errorType string
		isCustom  bool
		stderr    string
		want      []string
	}{
		{name: "not found default", errorType: "not_found", want: []string{"Antigravity CLI was not found", "agy", "PATH"}},
		{name: "not found custom", errorType: "not_found", isCustom: true, want: []string{"custom path"}},
		{name: "permission", errorType: "permission_denied", want: []string{"chmod +x"}},
		{name: "version failed with stderr", errorType: "version_failed", stderr: "boom", want: []string{"agy --version", "boom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := buildCheckErrorMessage(tt.errorType, "agy", tt.isCustom, tt.stderr)
			for _, want := range tt.want {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q missing %q", msg, want)
				}
			}
		})
	}
}

func TestDetectAgent_NoSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if NewProvider().DetectAgent("", false) {
		t.Errorf("expected false when no sessions exist")
	}
}

func TestDetectAgent_EmptyProjectMatchesAny(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConversation(t, home, "conv-1", `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`)

	if !NewProvider().DetectAgent("", false) {
		t.Errorf("expected true when any session exists and project path is empty")
	}
}

func TestDetectAgent_ProjectMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()

	writeHistory(t, home, `{"display":"hi","timestamp":1779831156198,"workspace":"`+proj+`","conversationId":"conv-1"}`)
	writeConversation(t, home, "conv-1", `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`)

	p := NewProvider()
	if !p.DetectAgent(proj, false) {
		t.Errorf("expected true when session workspace matches project")
	}
	if p.DetectAgent(t.TempDir(), false) {
		t.Errorf("expected false for an unrelated project")
	}
}

func TestDetectAgent_ProjectMatchFromLogMapping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()

	writeProjectConfig(t, home, testProjectID, `{
		"id":"`+testProjectID+`",
		"name":"`+proj+`",
		"projectResources":{"resources":[]}
	}`)
	writeAntigravityLog(t, home, "cli-test.log",
		`I server.go:726] Conversation using project ID: `+testProjectID,
		`I server.go:747] Created conversation `+testConversationID,
	)
	writeConversation(t, home, testConversationID,
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nsay hello\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"Hello!"}`,
	)

	p := NewProvider()
	if !p.DetectAgent(proj, false) {
		t.Errorf("expected true when log/config workspace mapping matches project")
	}
	if p.DetectAgent(t.TempDir(), false) {
		t.Errorf("expected false for an unrelated project")
	}
}

func TestGetAgentChatSession_RoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()

	writeHistory(t, home, `{"display":"hi","timestamp":1779831156198,"workspace":"`+proj+`","conversationId":"conv-1"}`)
	writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"hello there"}`,
	)

	chat, err := NewProvider().GetAgentChatSession(proj, "conv-1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat == nil {
		t.Fatalf("expected a session")
	}
	if chat.SessionID != "conv-1" || chat.SessionData == nil {
		t.Errorf("unexpected session: %+v", chat)
	}
	if !chat.SessionData.Validate() {
		t.Errorf("expected valid session data")
	}

	// Missing session → nil, nil.
	missing, err := NewProvider().GetAgentChatSession(proj, "nope", false)
	if err != nil || missing != nil {
		t.Errorf("expected (nil,nil) for missing session, got (%v,%v)", missing, err)
	}
}

func TestGetAgentChatSession_UnscopedReturnedByID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A text-only print-mode session: no history entry, no tool paths → unscoped.
	writeConversation(t, home, "conv-text",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nsay hello\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"Hello!"}`,
	)

	// Explicit by-id retrieval from an arbitrary project still returns it.
	chat, err := NewProvider().GetAgentChatSession(t.TempDir(), "conv-text", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chat == nil {
		t.Errorf("expected unscoped session to be returned on explicit by-id request")
	}
}

func TestListAgentChatSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConversation(t, home, "conv-1", `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nFix the bug\n</USER_REQUEST>"}`)

	metas, err := NewProvider().ListAgentChatSessions("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(metas) != 1 || metas[0].SessionID != "conv-1" {
		t.Fatalf("unexpected metadata: %+v", metas)
	}
}

func TestListAllAgentChatSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()

	// A workspace-scoped session: the workspace is recoverable from history.jsonl,
	// so its ref must carry that path as OriginCwd (the project reindex maps it to).
	writeHistory(t, home, `{"display":"hi","timestamp":1779831156198,"workspace":"`+proj+`","conversationId":"conv-1"}`)
	writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nFix the bug\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"done"}`,
	)
	// A text-only print-mode session with no recoverable workspace: still
	// enumerated (its project is discovered later), just with an empty OriginCwd.
	writeConversation(t, home, "conv-2",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:15Z","content":"<USER_REQUEST>\nsay hello\n</USER_REQUEST>"}`,
	)

	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d: %+v", len(refs), refs)
	}

	byID := map[string]spi.GlobalSessionRef{}
	for _, r := range refs {
		byID[r.SessionID] = r
	}

	scoped, ok := byID["conv-1"]
	if !ok {
		t.Fatalf("conv-1 not enumerated: %+v", refs)
	}
	if scoped.OriginCwd != proj {
		t.Errorf("expected OriginCwd %q, got %q", proj, scoped.OriginCwd)
	}
	if scoped.NativePath == "" || scoped.Slug == "" {
		t.Errorf("expected NativePath and Slug populated, got %+v", scoped)
	}

	unscoped, ok := byID["conv-2"]
	if !ok {
		t.Fatalf("conv-2 not enumerated: %+v", refs)
	}
	if unscoped.OriginCwd != "" {
		t.Errorf("expected empty OriginCwd for text-only session, got %q", unscoped.OriginCwd)
	}
}

func TestListAgentChatSessions_PrefersSummaryName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// conv-1: a generated summary title is the preferred Name.
	writeConversation(t, home, "conv-1", `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nplease fix the flaky login test\n</USER_REQUEST>"}`)
	writeConversationSummary(t, home, "conv-1", "Fix Flaky Login Test", "ignored preview")

	// conv-2: title empty but preview present → preview is used.
	writeConversation(t, home, "conv-2", `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:15Z","content":"<USER_REQUEST>\nadd a changelog\n</USER_REQUEST>"}`)
	writeConversationSummary(t, home, "conv-2", "", "Add A Changelog")

	// conv-3: no summary row → Name falls back to the prompt-derived readable name.
	writeConversation(t, home, "conv-3", `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:17Z","content":"<USER_REQUEST>\nrename the widget\n</USER_REQUEST>"}`)

	metas, err := NewProvider().ListAgentChatSessions("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byID := map[string]spi.SessionMetadata{}
	for _, m := range metas {
		byID[m.SessionID] = m
	}

	if got := byID["conv-1"].Name; got != "Fix Flaky Login Test" {
		t.Errorf("conv-1: expected summary title as Name, got %q", got)
	}
	if got := byID["conv-2"].Name; got != "Add A Changelog" {
		t.Errorf("conv-2: expected summary preview as Name, got %q", got)
	}
	// Fallback name is derived from the prompt: non-empty and not a summary value.
	if got := byID["conv-3"].Name; got == "" || got == "Fix Flaky Login Test" || got == "Add A Changelog" {
		t.Errorf("conv-3: expected derived fallback Name, got %q", got)
	}
}

func TestWatchAgent_RequiresCallback(t *testing.T) {
	if err := NewProvider().WatchAgent(t.Context(), "/proj", false, nil); err == nil {
		t.Errorf("expected error when callback is nil")
	}
}
