package piagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// Robustness + edge-case tests: the parser must handle large lines, distinct
// tool-call IDs, EndTime on tool-final exchanges, parentId cycles (no hang),
// dropped assistant error events, and the scan path's slug/error contracts.

// TestFormatEdge_MultipleToolCallsGetDistinctMessageIDs asserts that when a
// single assistant message contains multiple toolCall blocks, each resulting
// tool Message gets a distinct ID (entry id + toolCall id), so downstream
// provenance keys that use msg.ID do not collide.
func TestFormatEdge_MultipleToolCallsGetDistinctMessageIDs(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multitool.jsonl")
	session := `{"type":"session","version":3,"id":"multi-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-a","name":"read","arguments":{"path":"a.go"}},{"type":"toolCall","id":"call-b","name":"read","arguments":{"path":"b.go"}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var ids []string
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Tool != nil {
				ids = append(ids, msg.ID)
			}
		}
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 tool messages, got %d", len(ids))
	}
	if ids[0] == ids[1] {
		t.Errorf("tool message IDs collide: both %q", ids[0])
	}
	if ids[0] != "a1:call-a" || ids[1] != "a1:call-b" {
		t.Errorf("tool IDs = %q, %q; want a1:call-a, a1:call-b", ids[0], ids[1])
	}
}

// TestFormatEdge_ExchangeEndTimeReflectsFinalToolResult asserts that when a
// toolResult is the final event in an exchange, the exchange's EndTime is the
// toolResult's timestamp.
func TestFormatEdge_ExchangeEndTimeReflectsFinalToolResult(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "endtime.jsonl")
	session := `{"type":"session","version":3,"id":"endtime-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"ls"}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
` +
		`{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-07-09T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"bash","content":[{"type":"text","text":"out"}],"isError":false,"timestamp":1783600003000}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	last := data.Exchanges[len(data.Exchanges)-1]
	if last.EndTime != "2026-07-09T10:00:03.000Z" {
		t.Errorf("last exchange EndTime = %q, want the toolResult tr1 timestamp", last.EndTime)
	}
}

// TestParse_LargeLineOverScannerCap proves the reader handles lines larger than
// bufio.Scanner's 16MB cap (bufio.Reader.ReadString has no line-size limit).
func TestParse_LargeLineOverScannerCap(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "largeline.jsonl")
	big := strings.Repeat("x", 17*1024*1024) // 17MB > 16MB Scanner cap
	session := `{"type":"session","version":3,"id":"large-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"cat big"}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
` +
		`{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-07-09T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"bash","content":[{"type":"text","text":"` + big + `"}],"isError":false,"timestamp":1783600003000}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error for a >16MB line: %v", err)
	}
	var found bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Tool != nil && msg.Tool.UseID == "call-1" && msg.Tool.Output != nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("tool result with >16MB content was not parsed")
	}
}

// TestParse_ParentIdCycleTerminates asserts walkToRoot's visited guard prevents
// an infinite loop when a corrupted session has a parentId cycle.
func TestParse_ParentIdCycleTerminates(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cycle.jsonl")
	// a -> b -> a (cycle). Leaf is a.
	session := `{"type":"session","version":3,"id":"cycle-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"a","parentId":"b","timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"a","timestamp":1783600001000}}
` +
		`{"type":"message","id":"b","parentId":"a","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"b"}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := ParseSession(path); err != nil {
			t.Errorf("ParseSession returned error: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ParseSession hung on a parentId cycle (visited guard missing)")
	}
}

// TestParse_AssistantErrorMessageSurfaced asserts an assistant entry with
// stopReason=error, an errorMessage, and an empty content array is surfaced as
// an agent text message (not dropped) — seen in real_world.jsonl entry 59c6b470.
func TestParse_AssistantErrorMessageSurfaced(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "errmsg.jsonl")
	session := `{"type":"session","version":3,"id":"errmsg-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[],"provider":"x","model":"m","stopReason":"error","errorMessage":"rate limited","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var found bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role == schema.RoleAgent && len(msg.Content) > 0 && strings.Contains(msg.Content[0].Text, "rate limited") {
				found = true
			}
		}
	}
	if !found {
		t.Error("assistant error message was dropped — expected an agent message with [error] rate limited")
	}
	if !data.Validate() {
		t.Error("SessionData.Validate() returned false")
	}
}

// ---- scan path ----

// TestScan_PopulatesSlugAndName asserts scanPiSession derives Slug/Name from
// the first user message on the active leaf path (matching the full parse's
// deriveSlug, so list/reindex titles agree with sync markdown filenames).
func TestScan_PopulatesSlugAndName(t *testing.T) {
	scan, err := scanPiSession(loadFixture(t, "fields.jsonl"))
	if err != nil {
		t.Fatalf("scanPiSession returned error: %v", err)
	}
	if scan == nil || !scan.foundUser {
		t.Fatal("scan did not find a user message")
	}
	if scan.firstUserMessage == "" {
		t.Error("firstUserMessage is empty")
	}
	ref := scanToGlobalRef(scan, "fields.jsonl")
	if ref == nil {
		t.Fatal("scanToGlobalRef returned nil")
	}
	if ref.Slug == "" {
		t.Error("ref.Slug is empty")
	}
	if ref.Name == "" {
		t.Error("ref.Name is empty")
	}
}

// TestScan_NonSessionFileReturnsNilNil asserts scanPiSession returns (nil, nil)
// for a file whose first line is not a pi session header, so ScanSessionsInParallel
// skips it silently (without logging an error).
func TestScan_NonSessionFileReturnsNilNil(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "notsession.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","id":"x","parentId":null,"timestamp":"2026-07-09T10:00:00.000Z","message":{"role":"user","content":"hi"}}
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scan, err := scanPiSession(path)
	if err != nil {
		t.Fatalf("scanPiSession returned error for non-session file: %v", err)
	}
	if scan != nil {
		t.Errorf("scanPiSession returned %v for non-session file, want nil", scan)
	}
}

// TestScan_UnreadableFileReturnsError asserts scanPiSession returns a non-nil
// error when the file cannot be opened, so ScanSessionsInParallel logs it.
func TestScan_UnreadableFileReturnsError(t *testing.T) {
	_, err := scanPiSession(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("scanPiSession returned nil error for a missing file")
	}
}

// TestScan_SessionInfoNameWinsOverFirstPrompt asserts the display name follows
// pi's semantics: the LATEST session_info entry in file order wins, and an
// empty name explicitly clears the title (falling back to the first prompt).
func TestScan_SessionInfoNameWinsOverFirstPrompt(t *testing.T) {
	header := `{"type":"session","version":3,"id":"named-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"the first prompt","timestamp":1783600001000}}
`
	cases := []struct {
		name     string
		extra    string
		wantName string
	}{
		{
			name: "latest rename wins",
			extra: `{"type":"session_info","id":"s1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","name":"Old Name"}
{"type":"session_info","id":"s2","parentId":"s1","timestamp":"2026-07-09T10:00:03.000Z","name":"My Renamed Session"}
`,
			wantName: "My Renamed Session",
		},
		{
			name: "empty name clears back to prompt-derived",
			extra: `{"type":"session_info","id":"s1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","name":"Old Name"}
{"type":"session_info","id":"s2","parentId":"s1","timestamp":"2026-07-09T10:00:03.000Z","name":""}
`,
			wantName: "", // sessionName cleared; scanName falls back to readable name
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "named.jsonl")
			if err := os.WriteFile(path, []byte(header+tt.extra), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			scan, err := scanPiSession(path)
			if err != nil {
				t.Fatalf("scanPiSession returned error: %v", err)
			}
			if scan == nil {
				t.Fatal("scanPiSession returned nil for a real session")
			}
			if scan.sessionName != tt.wantName {
				t.Errorf("sessionName = %q, want %q", scan.sessionName, tt.wantName)
			}
			if tt.wantName == "" && scanName(scan) == "" {
				t.Error("scanName fell through to empty; want prompt-derived fallback")
			}
		})
	}
}

// TestPath_EncodeCwdMatchesPiEncoder asserts EncodeCwd mirrors pi's own
// encoder: strip one leading / or \, then replace every '/', '\', ':' with '-'.
func TestPath_EncodeCwdMatchesPiEncoder(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Users/jane/proj", "--Users-jane-proj--"},
		{"/Users/jane/dev/foo:bar", "--Users-jane-dev-foo-bar--"},
		{`/Users/jane/we\ird`, "--Users-jane-we-ird--"},
		{"/Users/jane", "--Users-jane--"},
	}
	for _, tt := range cases {
		if got := EncodeCwd(tt.in); got != tt.want {
			t.Errorf("EncodeCwd(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestPath_EnvOverrides asserts piSessionsRoot honors pi's documented env
// overrides: PI_CODING_AGENT_SESSION_DIR is a flat sessions dir used directly;
// PI_CODING_AGENT_DIR relocates the agent dir with sessions/ appended.
func TestPath_EnvOverrides(t *testing.T) {
	t.Run("session dir override is flat", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(envSessionDir, dir)
		root, flat, err := piSessionsRoot()
		if err != nil {
			t.Fatalf("piSessionsRoot: %v", err)
		}
		if root != dir || !flat {
			t.Errorf("root=%q flat=%v, want %q flat=true", root, flat, dir)
		}
	})
	t.Run("agent dir override appends sessions", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(envAgentDir, dir)
		root, flat, err := piSessionsRoot()
		if err != nil {
			t.Fatalf("piSessionsRoot: %v", err)
		}
		if root != filepath.Join(dir, "sessions") || flat {
			t.Errorf("root=%q flat=%v, want %q flat=false", root, flat, filepath.Join(dir, "sessions"))
		}
	})
}

// TestPath_FlatSessionDirFiltersByHeaderCwd asserts that in the flat
// PI_CODING_AGENT_SESSION_DIR layout, SessionFilesInProject keeps only files
// whose header cwd matches the project (pi applies the same filter to custom
// session dirs).
func TestPath_FlatSessionDirFiltersByHeaderCwd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envSessionDir, dir)
	proj := t.TempDir()
	mine := `{"type":"session","version":3,"id":"mine-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"` + proj + `"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1783600001000}}
`
	other := `{"type":"session","version":3,"id":"other-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/somewhere/else"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1783600001000}}
`
	if err := os.WriteFile(filepath.Join(dir, "mine.jsonl"), []byte(mine), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.jsonl"), []byte(other), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	files, err := SessionFilesInProject(proj)
	if err != nil {
		t.Fatalf("SessionFilesInProject: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "mine.jsonl" {
		t.Errorf("files = %v, want just mine.jsonl", files)
	}
}

// TestGlobal_NestedSubagentFilesExcluded asserts the global enumeration skips
// *.jsonl files nested below the per-project directories (extension/subagent
// run files) that the project-scoped APIs can never resolve by id.
func TestGlobal_NestedSubagentFilesExcluded(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(envAgentDir, agentDir)
	projDir := filepath.Join(agentDir, "sessions", "--test-proj--")
	nestedDir := filepath.Join(projDir, "2026-07-09T10-00-00-000Z_top-uuid", "abc123", "run-0")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	session := `{"type":"session","version":3,"id":"top-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test/proj"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"top level","timestamp":1783600001000}}
`
	nested := `{"type":"session","version":3,"id":"nested-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test/proj"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"nested run","timestamp":1783600001000}}
`
	if err := os.WriteFile(filepath.Join(projDir, "2026-07-09T10-00-00-000Z_top-uuid.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "session.jsonl"), []byte(nested), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil {
		t.Fatalf("ListAllAgentChatSessions: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1 (nested subagent file must be excluded): %+v", len(refs), refs)
	}
	if refs[0].SessionID != "top-uuid" {
		t.Errorf("ref SessionID = %q, want top-uuid", refs[0].SessionID)
	}
}

// TestFormatEdge_ToolEnrichment asserts tool messages carry PathHints (for
// provenance) and Summary/FormattedMarkdown (for markdown rendering), matching
// the sibling providers.
func TestFormatEdge_ToolEnrichment(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "enrich.jsonl")
	session := `{"type":"session","version":3,"id":"enrich-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test/proj"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-r","name":"read","arguments":{"path":"/test/proj/main.go"}},{"type":"toolCall","id":"call-b","name":"bash","arguments":{"command":"go test ./..."}}],"provider":"x","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var readMsg, bashMsg *schema.Message
	for _, ex := range data.Exchanges {
		for i := range ex.Messages {
			m := &ex.Messages[i]
			if m.Tool == nil {
				continue
			}
			switch m.Tool.UseID {
			case "call-r":
				readMsg = m
			case "call-b":
				bashMsg = m
			}
		}
	}
	if readMsg == nil || bashMsg == nil {
		t.Fatal("tool messages not found")
	}
	if len(readMsg.PathHints) == 0 {
		t.Error("read tool message has no PathHints; provenance extraction needs them")
	}
	if readMsg.Tool.FormattedMarkdown == nil || !strings.Contains(*readMsg.Tool.FormattedMarkdown, "main.go") {
		t.Error("read tool FormattedMarkdown missing or lacks the file path")
	}
	if bashMsg.Tool.Summary == nil || !strings.Contains(*bashMsg.Tool.Summary, "go test ./...") {
		t.Error("single-line bash command should produce an inline-code Summary")
	}
}

// TestFormatEdge_ReasoningTokensMapped asserts pi's usage.reasoning field maps
// into schema Usage (same field codexcli uses) instead of being dropped.
func TestFormatEdge_ReasoningTokensMapped(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "reasoning.jsonl")
	session := `{"type":"session","version":3,"id":"reason-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"think","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"thought about it"}],"provider":"x","model":"m","usage":{"input":10,"output":5,"reasoning":857,"cacheRead":0,"cacheWrite":0,"totalTokens":872,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("ParseSession returned error: %v", err)
	}
	var found bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Usage != nil && msg.Usage.ReasoningOutputTokens == 857 {
				found = true
			}
		}
	}
	if !found {
		t.Error("usage.reasoning was not mapped to Usage.ReasoningOutputTokens")
	}
}
