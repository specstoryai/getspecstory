package grokbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) *GrokSession {
	t.Helper()
	session, err := ParseSessionDir(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ParseSessionDir(%s) failed: %v", name, err)
	}
	return session
}

func TestParseSessionDir_Basic(t *testing.T) {
	session := loadFixture(t, "session-basic")

	if session.ID != "11111111-2222-7333-8444-555555555555" {
		t.Errorf("ID = %q", session.ID)
	}
	if session.Cwd != "/Users/dev/project" {
		t.Errorf("Cwd = %q", session.Cwd)
	}
	if session.Title != "Read the README" {
		t.Errorf("Title = %q", session.Title)
	}
	if session.Model != "grok-4.6" {
		t.Errorf("Model = %q", session.Model)
	}
	if len(session.Records) != 10 {
		t.Errorf("record count = %d, want 10", len(session.Records))
	}
	// Grok writes microsecond precision; the schema uses milliseconds.
	if session.CreatedAt != "2026-08-12T19:08:38.667Z" {
		t.Errorf("CreatedAt = %q, want millisecond precision", session.CreatedAt)
	}
	if session.IsSubagent() {
		t.Error("basic session should not be flagged as a subagent")
	}
}

func TestParseSessionDir_MalformedLineSkipped(t *testing.T) {
	session := loadFixture(t, "session-malformed")

	if len(session.Records) != 2 {
		t.Errorf("record count = %d, want 2 (the corrupt line is skipped)", len(session.Records))
	}
}

func TestParseSessionDir_SubagentFlagged(t *testing.T) {
	session := loadFixture(t, "session-subagent")

	if !session.IsSubagent() {
		t.Error("session_kind subagent should set IsSubagent")
	}
	// A subagent transcript also has no user_query, which is the second,
	// independent reason it produces no exchanges.
	if session.FirstUserQuery() != "" {
		t.Errorf("subagent should have no user query, got %q", session.FirstUserQuery())
	}
}

func TestUserQuery(t *testing.T) {
	session := loadFixture(t, "session-basic")

	var queries []string
	for i := range session.Records {
		if query, ok := session.Records[i].UserQuery(); ok {
			queries = append(queries, query)
		}
	}

	if len(queries) != 2 {
		t.Fatalf("real user turns = %d, want 2 (injected context must be skipped)", len(queries))
	}
	if queries[0] != "read the README and tell me what it says" {
		t.Errorf("first query = %q", queries[0])
	}
	if queries[1] != "thanks" {
		t.Errorf("second query = %q", queries[1])
	}
}

func TestUserQuery_RejectsInjectedContext(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "user_info wrapper", text: "<user_info>\nOS Version: macos\n</user_info>"},
		{name: "system reminder", text: "<system-reminder>\nskills: a, b\n</system-reminder>"},
		{name: "git status", text: "<git_status>\n## main\n</git_status>"},
		{name: "empty query", text: "<user_query>\n\n</user_query>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := GrokRecord{Type: "user", Content: []byte(`[{"type":"text","text":` + quote(tt.text) + `}]`)}
			if _, ok := record.UserQuery(); ok {
				t.Errorf("%s should not count as a real user turn", tt.name)
			}
		})
	}
}

func TestThoughtContent_NeverLeaksEncrypted(t *testing.T) {
	session := loadFixture(t, "session-basic")

	for i := range session.Records {
		record := &session.Records[i]
		if record.Type != "reasoning" {
			continue
		}
		thought := record.ThoughtContent()
		if thought == "" {
			t.Error("reasoning summary should be readable")
		}
		if strings.Contains(thought, "OPAQUE") {
			t.Error("encrypted_content must never be rendered")
		}
	}
}

func TestToolCallArgs_DecodesJSONString(t *testing.T) {
	call := GrokToolCall{
		ID:        "call-1",
		Name:      "read_file",
		Arguments: `{"target_file":"/tmp/a.txt","limit":20}`,
	}
	args := call.Args()
	if args["target_file"] != "/tmp/a.txt" {
		t.Errorf("target_file = %v", args["target_file"])
	}

	// Malformed arguments should degrade to nil, not panic.
	bad := GrokToolCall{Arguments: "{not json"}
	if bad.Args() != nil {
		t.Error("malformed arguments should decode to nil")
	}
}

func TestSessionIndex_TimingAndOutcomes(t *testing.T) {
	session := loadFixture(t, "session-tools")

	if !session.Index.toolError["call-miss-1"] {
		t.Error("failed tool call should be marked from events.jsonl")
	}
	if session.Index.toolError["call-shell-1"] {
		t.Error("successful tool call should not be marked as an error")
	}
	if got := session.Index.toolKind["call-edit-1"]; got != "edit" {
		t.Errorf("tool kind from updates.jsonl = %q, want edit", got)
	}
	if got := session.Index.toolTime["call-edit-1"]; got == "" {
		t.Error("tool call should have a timestamp")
	}
	if len(session.Index.usage) != 1 {
		t.Errorf("usage entries = %d, want 1", len(session.Index.usage))
	}
}

func TestReadChatHistoryCapped_RefusesOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, chatHistoryFile)

	// One valid record, then a line past the cap. The scanner's buffer cap is
	// what bounds the allocation, so the oversized line must surface as a
	// refusal rather than being read whole first.
	oversized := `{"type":"assistant","content":"` + strings.Repeat("x", 4096) + `"}`
	content := `{"type":"user","content":[{"type":"text","text":"<user_query>hi</user_query>"}]}` + "\n" + oversized + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readChatHistoryCapped(path, 1024)
	if err == nil {
		t.Fatal("an oversized line should refuse the file")
	}
	if !strings.Contains(err.Error(), "size limit") {
		t.Errorf("error should name the size limit, got: %v", err)
	}
}

func TestForEachJSONLineCapped_StopsAtOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, updatesFile)

	oversized := `{"big":"` + strings.Repeat("y", 4096) + `"}`
	content := `{"n":1}` + "\n" + oversized + "\n" + `{"n":3}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var seen int
	forEachJSONLineCapped(path, 1024, func(raw []byte) { seen++ })

	// Sidecars are best effort: everything before the oversized line is kept,
	// and the scan stops there instead of allocating past the cap.
	if seen != 1 {
		t.Errorf("lines handed to fn = %d, want 1 (the line before the oversized one)", seen)
	}
}

func TestFindSessions_ExcludesSubagentsAndSorts(t *testing.T) {
	dir := t.TempDir()

	// Copy fixtures into a project-group-shaped directory under UUID names.
	copyFixture(t, "session-basic", filepath.Join(dir, "11111111-2222-7333-8444-555555555555"))
	copyFixture(t, "session-tools", filepath.Join(dir, "aaaaaaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee"))
	copyFixture(t, "session-subagent", filepath.Join(dir, "99999999-8888-7777-6666-555555555555"))
	// A non-session directory must be ignored.
	if err := os.MkdirAll(filepath.Join(dir, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}

	sessions, err := FindSessions(dir)
	if err != nil {
		t.Fatalf("FindSessions failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2 (subagent excluded)", len(sessions))
	}
	// Most recently updated first.
	if sessions[0].ID != "aaaaaaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee" {
		t.Errorf("sessions not sorted most recent first, got %s", sessions[0].ID)
	}
}

func TestDecodeCwdDirname(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want string
		ok   bool
	}{
		{name: "simple path", dir: "%2FUsers%2Fgdc%2Fpainpoints", want: "/Users/gdc/painpoints", ok: true},
		{name: "nested path", dir: "%2FUsers%2Fgdc%2Fgetspecstory%2Fspecstory-cli", want: "/Users/gdc/getspecstory/specstory-cli", ok: true},
		{name: "space encoded", dir: "%2Ftmp%2Fmy%20project", want: "/tmp/my project", ok: true},
		{name: "plus is literal", dir: "%2Ftmp%2Fa+b", want: "/tmp/a+b", ok: true},
		{name: "unicode", dir: "%2Ftmp%2Fcaf%C3%A9", want: "/tmp/café", ok: true},
		{name: "not a path", dir: "someslug-0123456789abcdef", want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DecodeCwdDirname(filepath.Join(t.TempDir(), tt.dir))
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("decoded = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeCwdDirname_LongPathFallback(t *testing.T) {
	// When the encoded name would be too long, Grok names the directory after a
	// slug and hash and writes the real path into a .cwd sidecar.
	dir := filepath.Join(t.TempDir(), "myproject-0123456789abcdef")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".cwd"), []byte("/very/long/real/path\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := DecodeCwdDirname(dir)
	if !ok || got != "/very/long/real/path" {
		t.Errorf("decoded = %q ok=%v, want the .cwd sidecar value", got, ok)
	}
}

// copyFixture copies a testdata session directory to dest.
func copyFixture(t *testing.T, fixture, dest string) {
	t.Helper()
	src := filepath.Join("testdata", fixture)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dest, entry.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// quote produces a JSON string literal for embedding in fixtures.
func quote(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	return "\"" + replacer.Replace(s) + "\""
}
