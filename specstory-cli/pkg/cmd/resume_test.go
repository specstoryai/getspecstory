package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestFTSQuery(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"single word is a prefix", "thank", "thank*"},
		{"bare words are independent prefixes", "thank you", "thank* you*"},
		{"punctuation in a bare word splits into an adjacency phrase", "max-cpu!", "max + cpu*"},
		{"a bare filename is an adjacency phrase with prefix last", "poem.txt", "poem + txt*"},
		{"a quoted filename is a committed phrase, no prefix", `"poem.txt"`, `"poem txt"`},
		{"an open-quoted filename keeps the last token a prefix", `"poem.txt`, "poem + txt*"},
		{"closed phrase is exact adjacency", `"thank you"`, `"thank you"`},
		{"closed single-word phrase has no prefix", `"thank"`, `"thank"`},
		{"open phrase keeps last word a prefix", `"thank yo`, "thank + yo*"},
		{"open single-word phrase is a prefix", `"thank`, "thank*"},
		{"phrase plus trailing bare word", `"thank you" now`, `"thank you" now*`},
		{"bare word before a closed phrase", `please "thank you"`, `please* "thank you"`},
		{"whitespace inside a phrase is collapsed", `"thank    you"`, `"thank you"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ftsQuery(c.in); got != c.want {
				t.Errorf("ftsQuery(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestQueryReady(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"a", false},   // one alnum char is below minQueryLen
		{`"a"`, false}, // quotes don't count toward the threshold
		{"ab", true},   // two alnum chars
		{"a!", false},  // punctuation doesn't count
		{"a b", true},  // two alnum chars across words
		{`"hi"`, true}, // alnum inside quotes counts
		{"日本", true},   // letters in other scripts count
	}
	for _, c := range cases {
		if got := queryReady(c.in); got != c.want {
			t.Errorf("queryReady(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestShortID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{"1234567890abc", "1234567890abc"}, // <= 13 chars: unchanged
		{"1234567890abcdef", "12345...bcdef"},
	}
	for _, c := range cases {
		if got := shortID(c.in); got != c.want {
			t.Errorf("shortID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteReconstructedSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "session.jsonl")
	content := []byte(`{"type":"user"}` + "\n")

	if err := writeReconstructedSession(path, content); err != nil {
		t.Fatalf("writeReconstructedSession: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	// The atomic-write temp file must not be left behind in the target directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.jsonl" {
			t.Errorf("unexpected leftover file in target dir: %q", e.Name())
		}
	}
}

func TestSessionFileReadable(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.jsonl")
	if ok, _ := sessionFileReadable(missing); ok {
		t.Error("missing file reported readable")
	}

	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := sessionFileReadable(empty); ok {
		t.Error("empty file reported readable")
	}

	full := filepath.Join(dir, "full.jsonl")
	if err := os.WriteFile(full, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := sessionFileReadable(full); !ok {
		t.Errorf("file with content reported not readable: %v", err)
	}
}

func TestWaitForSessionFileVisible(t *testing.T) {
	dir := t.TempDir()

	present := filepath.Join(dir, "present.jsonl")
	if err := os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := waitForSessionFileVisible(present, time.Second); err != nil {
		t.Errorf("expected an already-present file to be visible: %v", err)
	}

	// A file that never appears must time out with a diagnostic error rather than block forever.
	if err := waitForSessionFileVisible(filepath.Join(dir, "never.jsonl"), 100*time.Millisecond); err == nil {
		t.Error("expected timeout error for a file that never becomes visible")
	}
}

// TestBeginResumeWithPresetSkipsTargetStep verifies the `resume <agent>` contract: when a
// target agent was pre-selected, choosing a session resumes immediately into that agent
// rather than prompting for a target.
func TestBeginResumeWithPresetSkipsTargetStep(t *testing.T) {
	sess := &sessionindex.Session{SessionID: "s1", Agent: "codex"}
	m := sessionTUI{presetTo: "claude"}

	next, cmd := m.beginResume(sess)
	rm := next.(sessionTUI)

	if cmd == nil {
		t.Error("expected an immediate quit command when a target is preset")
	}
	if rm.mode == modeTarget {
		t.Error("a preset target must skip the target-selection step")
	}
	if rm.result.session != sess {
		t.Errorf("result session = %v, want the chosen session", rm.result.session)
	}
	if rm.result.targetID != "claude" {
		t.Errorf("result target = %q, want %q", rm.result.targetID, "claude")
	}
}

// TestBeginResumeWithoutPresetEntersTargetStep verifies the default flow: with no preset,
// choosing a session advances to the target-selection step (no immediate resume).
func TestBeginResumeWithoutPresetEntersTargetStep(t *testing.T) {
	sess := &sessionindex.Session{SessionID: "s1", Agent: "codex"}
	m := sessionTUI{}

	next, cmd := m.beginResume(sess)
	rm := next.(sessionTUI)

	if cmd != nil {
		t.Error("expected no immediate quit without a preset target")
	}
	if rm.mode != modeTarget {
		t.Errorf("mode = %v, want modeTarget", rm.mode)
	}
	if rm.chosen != sess {
		t.Error("chosen session must be recorded before target selection")
	}
}

// fakeProvider is a minimal spi.Provider for exercising prepareResumeTarget. It records
// the projectPath it is asked to load the source session from, and reconstructs into a
// caller-provided directory so the write/visibility tail succeeds.
type fakeProvider struct {
	name        string
	gotLoadPath string // projectPath captured from GetAgentChatSession
	nativeDir   string // where NativeSessionPath places the reconstructed file
}

func (f *fakeProvider) Name() string                  { return f.name }
func (f *fakeProvider) Check(string) spi.CheckResult  { return spi.CheckResult{Success: true} }
func (f *fakeProvider) DetectAgent(string, bool) bool { return false }

func (f *fakeProvider) GetAgentChatSession(projectPath, sessionID string, _ bool) (*spi.AgentChatSession, error) {
	f.gotLoadPath = projectPath
	return &spi.AgentChatSession{SessionID: sessionID, SessionData: &schema.SessionData{}}, nil
}

func (f *fakeProvider) ReconstructSession(*schema.SessionData, spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return &spi.ReconstructedSession{
		SessionID: "new-session-id",
		Filename:  "reconstructed.jsonl",
		Content:   []byte(`{"type":"user"}` + "\n"),
	}, nil
}

func (f *fakeProvider) NativeSessionPath(_ string, filename string) (string, error) {
	return filepath.Join(f.nativeDir, filename), nil
}

// Unused-by-these-tests interface methods.
func (f *fakeProvider) GetAgentChatSessions(string, bool, spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	return nil, nil
}
func (f *fakeProvider) ListAgentChatSessions(string) ([]spi.SessionMetadata, error) { return nil, nil }
func (f *fakeProvider) ExecAgentAndWatch(string, string, string, bool, func(*spi.AgentChatSession)) error {
	return nil
}
func (f *fakeProvider) WatchAgent(context.Context, string, bool, func(*spi.AgentChatSession)) error {
	return nil
}
func (f *fakeProvider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) { return nil, nil }

// TestPrepareResumeTargetLoadsSourceFromOriginCwd guards the cross-project resume fix:
// the source session must be loaded from the directory it was launched in (fromCwd),
// not the user's current cwd, while the reconstructed file is still written under the
// current cwd. Regression test for "source session ... has no data to reconstruct" when
// resuming a session picked from another project via the all-projects browser.
func TestPrepareResumeTargetLoadsSourceFromOriginCwd(t *testing.T) {
	nativeDir := t.TempDir()
	from := &fakeProvider{name: "Codex CLI"}
	to := &fakeProvider{name: "Claude Code", nativeDir: nativeDir}

	const originCwd = "/Users/jake/dev/tmp/blog-site"
	const currentCwd = "/Users/jake/dev/specstory-website"

	plan := &resumePlan{
		from:      from,
		fromID:    "codex",
		sessionID: "019c4cdd-917a-74e3-9b2e-fdb45e9eddc5",
		fromCwd:   originCwd,
		to:        to,
		toID:      "claude",
	}

	newID, err := prepareResumeTarget(plan, currentCwd, io.Discard)
	if err != nil {
		t.Fatalf("prepareResumeTarget returned error: %v", err)
	}
	if newID != "new-session-id" {
		t.Errorf("resume target id = %q, want %q", newID, "new-session-id")
	}
	if from.gotLoadPath != originCwd {
		t.Errorf("source loaded from %q, want origin cwd %q (current cwd %q must not be used)",
			from.gotLoadPath, originCwd, currentCwd)
	}
}

// TestPrepareResumeTargetFallsBackToCurrentCwd verifies that when the index row carries
// no origin cwd (older rows), the source load falls back to the current cwd rather than
// loading from an empty path.
func TestPrepareResumeTargetFallsBackToCurrentCwd(t *testing.T) {
	nativeDir := t.TempDir()
	from := &fakeProvider{name: "Codex CLI"}
	to := &fakeProvider{name: "Claude Code", nativeDir: nativeDir}

	const currentCwd = "/Users/jake/dev/blog-site"

	plan := &resumePlan{
		from:      from,
		fromID:    "codex",
		sessionID: "sid",
		fromCwd:   "", // older index row: no origin cwd recorded
		to:        to,
		toID:      "claude",
	}

	if _, err := prepareResumeTarget(plan, currentCwd, io.Discard); err != nil {
		t.Fatalf("prepareResumeTarget returned error: %v", err)
	}
	if from.gotLoadPath != currentCwd {
		t.Errorf("source loaded from %q, want fallback to current cwd %q", from.gotLoadPath, currentCwd)
	}
}

// TestLiveIndexerRecord exercises real-time indexing (watch/run/resume): a live session is
// upserted into the index, its FTS body is searchable, repeat records with no new activity are
// skipped, and new activity updates the same row in place.
func TestLiveIndexerRecord(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = store.Close() }()

	li := &LiveIndexer{
		store:       store,
		cwd:         "/work/proj",
		projectID:   "proj-1",
		projectName: "proj",
		indexedAt:   "2026-06-25T00:00:00Z",
		lastSeen:    map[string]string{},
	}

	msg := func(role, ts, text string) schema.Message {
		return schema.Message{
			Role:      role,
			Timestamp: ts,
			Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}},
		}
	}
	sess := func(msgs ...schema.Message) *spi.AgentChatSession {
		return &spi.AgentChatSession{
			SessionID: "sess-1",
			CreatedAt: "2026-06-25T00:00:00Z",
			Slug:      "hello-world",
			SessionData: &schema.SessionData{
				Provider:  schema.ProviderInfo{Name: "Claude Code"},
				Exchanges: []schema.Exchange{{Messages: msgs}},
			},
		}
	}
	user1 := msg(schema.RoleUser, "2026-06-25T00:01:00Z", "index this please")
	agent1 := msg(schema.RoleAgent, "2026-06-25T00:02:00Z", "done indexing")

	// First record: one user + one agent message → one fully-derived row.
	li.Record("claude", sess(user1, agent1))
	rows, err := store.ListByProject("proj-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Agent != "claude" || r.SessionID != "sess-1" || r.OriginCwd != "/work/proj" {
		t.Errorf("identity wrong: agent=%q session=%q cwd=%q", r.Agent, r.SessionID, r.OriginCwd)
	}
	if r.UserTurns != 1 || r.TotalTurns != 2 {
		t.Errorf("turns = %d/%d, want 1/2", r.UserTurns, r.TotalTurns)
	}
	if r.UpdatedAt != "2026-06-25T00:02:00Z" {
		t.Errorf("updatedAt = %q, want last message timestamp", r.UpdatedAt)
	}

	// The conversation body is full-text searchable immediately.
	if hits, _ := store.Search(ftsQuery("indexing"), "proj-1"); len(hits) != 1 {
		t.Errorf("search for body word returned %d hits, want 1", len(hits))
	}

	// Dedup guard: re-recording with no new activity is a no-op.
	li.Record("claude", sess(user1, agent1))
	if rows, _ := store.ListByProject("proj-1"); len(rows) != 1 {
		t.Errorf("dedup: want 1 row, got %d", len(rows))
	}

	// New activity (a later message) updates the same row in place.
	li.Record("claude", sess(user1, agent1, msg(schema.RoleUser, "2026-06-25T00:03:00Z", "more")))
	rows, _ = store.ListByProject("proj-1")
	if len(rows) != 1 {
		t.Fatalf("want 1 row after update, got %d", len(rows))
	}
	if rows[0].UserTurns != 2 || rows[0].TotalTurns != 3 || rows[0].UpdatedAt != "2026-06-25T00:03:00Z" {
		t.Errorf("after update: turns=%d/%d updatedAt=%q, want 2/3 and advanced timestamp",
			rows[0].UserTurns, rows[0].TotalTurns, rows[0].UpdatedAt)
	}

	// A nil indexer (index failed to open) is safe to use.
	var nilLI *LiveIndexer
	nilLI.Record("claude", sess(user1))
	nilLI.Close()
}
