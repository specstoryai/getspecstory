package piagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestPath_DifferentCaseFindsSameProject(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "CaseProject")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	lower := filepath.Join(base, "caseproject")
	if _, err := os.Stat(lower); os.IsNotExist(err) {
		t.Skip("filesystem is case sensitive")
	} else if err != nil {
		t.Fatal(err)
	}
	canonical, err := spi.GetCanonicalPath(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, flat := range []bool{false, true} {
		t.Run(fmt.Sprint("flat=", flat), func(t *testing.T) {
			t.Setenv(envSessionDir, "")
			t.Setenv(envAgentDir, t.TempDir())
			if flat {
				t.Setenv(envSessionDir, t.TempDir())
			}
			dir, err := ProjectSessionDir(canonical)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeSessionForCwd(t, filepath.Join(dir, "case.jsonl"), "case", canonical)
			sessions, err := NewProvider().GetAgentChatSessions(lower, false, nil)
			if err != nil || len(sessions) != 1 {
				t.Fatalf("sessions=%d error=%v; want one session through alternate casing", len(sessions), err)
			}

			listed, err := NewProvider().ListAgentChatSessions(lower)
			if err != nil || len(listed) != 1 {
				t.Fatalf("list=%v error=%v", listed, err)
			}
			single, err := NewProvider().GetAgentChatSession(lower, "case", false)
			if err != nil || single == nil {
				t.Fatalf("by-id lookup failed: %v", err)
			}
			destination, err := NewProvider().NativeSessionPath(lower, "new.jsonl")
			if err != nil || destination != filepath.Join(dir, "new.jsonl") {
				t.Fatalf("destination=%q error=%v", destination, err)
			}
			reconstructed, err := NewProvider().ReconstructSession(single.SessionData, spi.ReconstructOptions{WorkspaceRoot: lower})
			if err != nil {
				t.Fatal(err)
			}
			if cwd := parsePiJSONL(t, reconstructed.Content)[0]["cwd"]; cwd != canonical {
				t.Fatalf("reconstructed cwd=%q; want %q", cwd, canonical)
			}
			ch, stop := startWatch(t, lower)
			defer stop()
			writeSessionForCwd(t, filepath.Join(dir, "case.jsonl"), "case", canonical)
			// A different-sized prompt forces a distinct signature even on filesystems
			// with coarse mtimes, while retaining the correctly cased native cwd.
			if err := os.WriteFile(filepath.Join(dir, "case.jsonl"), []byte(validSession("case", canonical, "updated canonical project")), 0600); err != nil {
				t.Fatal(err)
			}
			if got := waitForSession(t, ch); got.SessionData.WorkspaceRoot != canonical {
				t.Fatalf("watch changed recorded cwd: %q", got.SessionData.WorkspaceRoot)
			}
		})
	}
}

func TestParse_DebugExportUsesOriginalSnapshot(t *testing.T) {
	spi.SetDebugBaseDir(t.TempDir())
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	dir := spi.GetDebugDir("snapshot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Export deliberately overwrites the input in this temporary directory.
	// Any native-file reread after debug export would observe different data.
	path := filepath.Join(dir, "1.json")
	body := validSession("snapshot", t.TempDir(), "original conversation")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	chat, err := NewProvider().GetAgentChatSessionByPath(path, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if chat.RawData != body {
		t.Error("RawData was reread after the native file changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "session-data.json")); !os.IsNotExist(err) {
		t.Errorf("provider wrote the CLI-owned session-data.json: %v", err)
	}
}

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

// TestParse_RecordOverTheCapIsSkippedNotFatal proves a record past the shared
// per-record cap costs that record alone: the session still parses and the
// records around it survive. Failing the file instead would mean one poisoned
// record loses the user the whole session.
func TestParse_RecordOverTheCapIsSkippedNotFatal(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "largeline.jsonl")
	big := strings.Repeat("x", spi.MaxRecordLineSize+1) // one byte past the cap
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
		t.Fatalf("an oversized record must not fail the file: %v", err)
	}
	// The user turn and the tool call before the oversized result are intact.
	var sawUserTurn, sawToolCall, sawToolResult bool
	for _, ex := range data.Exchanges {
		for _, msg := range ex.Messages {
			if msg.Role == schema.RoleUser {
				sawUserTurn = true
			}
			if msg.Tool != nil && msg.Tool.UseID == "call-1" {
				sawToolCall = true
				if msg.Tool.Output != nil {
					sawToolResult = true
				}
			}
		}
	}
	if !sawUserTurn || !sawToolCall {
		t.Fatalf("records around the oversized one were lost: user=%v call=%v", sawUserTurn, sawToolCall)
	}
	if sawToolResult {
		t.Error("the oversized tool result was parsed despite exceeding the cap")
	}
}

// TestParse_OversizeFinalLineWithoutTrailingNewlineIsSkipped asserts readLines
// applies the cap to a final record that has no trailing '\n', where the reader
// returns data and io.EOF together, and still returns the session before it.
func TestParse_OversizeFinalLineWithoutTrailingNewlineIsSkipped(t *testing.T) {
	// Above the ~95-byte session header, below the oversized final record.
	origLineLimit := maxRecordLineSize
	maxRecordLineSize = 200
	t.Cleanup(func() {
		maxRecordLineSize = origLineLimit
	})

	path := filepath.Join(t.TempDir(), "no-newline-oversize.jsonl")
	payload := `{"type":"session","version":3,"id":"x","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}` + "\n" +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"kept","timestamp":1783600001000}}` + "\n" +
		strings.Repeat("x", 400) // no trailing newline on purpose
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	data, err := ParseSession(path)
	if err != nil {
		t.Fatalf("an oversized final record must not fail the file: %v", err)
	}
	if data == nil || data.SessionID != "x" || len(data.Exchanges) != 1 {
		t.Fatalf("records before the oversized final one were lost: %+v", data)
	}
}

// TestScan_DoesNotRetainMessagePayloads asserts the metadata-only scan path
// (readScanEntries) never keeps an entry's large message body in memory: for
// a session with a large assistant message and a large tool result, every
// non-user scanEntry must come back with an empty UserText, and the total
// bytes retained across all entries must be tiny relative to the session
// file — proving the scan discards each rawEntry's Message payload after a
// per-line decode instead of accumulating it the way a full parse must.
// This is the aggregate-memory fix from the greptile review: rather than
// capping session size, the scan path stops retaining what it never reads.
func TestScan_DoesNotRetainMessagePayloads(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "big-payloads.jsonl")
	bigAssistantText := strings.Repeat("a", 5*1024*1024) // 5MB assistant text
	bigToolResult := strings.Repeat("b", 5*1024*1024)    // 5MB tool result
	session := `{"type":"session","version":3,"id":"big-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":"/test"}
` +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"go","timestamp":1783600001000}}
` +
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-07-09T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"` + bigAssistantText + `"},{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"cat big"}}],"provider":"x","model":"m"}}
` +
		`{"type":"message","id":"tr1","parentId":"a1","timestamp":"2026-07-09T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"call-1","toolName":"bash","content":[{"type":"text","text":"` + bigToolResult + `"}],"isError":false,"timestamp":1783600003000}}
`
	if err := os.WriteFile(path, []byte(session), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	entries, _, err := readScanEntries(path)
	if err != nil {
		t.Fatalf("readScanEntries returned error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d scan entries, want 3", len(entries))
	}
	var retainedBytes int
	for _, e := range entries {
		retainedBytes += len(e.UserText)
		if e.ID == "u1" {
			continue // the one entry allowed to carry text
		}
		if e.UserText != "" {
			t.Errorf("non-user scan entry %q retained %d bytes of message text, want none", e.ID, len(e.UserText))
		}
	}
	if retainedBytes > 1*KB {
		t.Errorf("readScanEntries retained %d bytes across all entries, want well under the 10MB of message payloads in the fixture", retainedBytes)
	}

	// scanPiSession must still resolve the session end to end via the same
	// lightweight path.
	scan, err := scanPiSession(path)
	if err != nil {
		t.Fatalf("scanPiSession returned error: %v", err)
	}
	if scan == nil || !scan.foundUser || scan.firstUserMessage != "go" {
		t.Fatalf("scanPiSession = %+v, want foundUser with firstUserMessage \"go\"", scan)
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

// TestScan_CorruptHeaderReturnsError asserts a file whose first line fails to
// JSON-decode (corrupted/truncated header) surfaces an error so list/reindex
// log the file instead of silently hiding it — the full parser errors on the
// same input.
func TestScan_CorruptHeaderReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"x", TRUNCATED`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := scanPiSession(path); err == nil {
		t.Fatal("scanPiSession returned nil error for a corrupt header")
	}
}

// TestScan_EmptyFileSkippedSilently asserts a zero-byte session file (created
// then abandoned — a benign artifact) is skipped without an error, so reindex
// does not warn about empties.
func TestScan_EmptyFileSkippedSilently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scan, err := scanPiSession(path)
	if err != nil {
		t.Fatalf("scanPiSession returned error for an empty file: %v", err)
	}
	if scan != nil {
		t.Errorf("scanPiSession returned %v for an empty file, want nil", scan)
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
	// proj is a real OS temp dir path; on Windows it contains backslashes,
	// which are not valid unescaped inside a JSON string. json.Marshal
	// produces a correctly quoted/escaped JSON string literal so the fixture
	// is valid JSON on every platform, matching what a real pi session file
	// (written by an actual JSON encoder) would contain.
	projJSON, err := json.Marshal(proj)
	if err != nil {
		t.Fatalf("marshal proj: %v", err)
	}
	mine := `{"type":"session","version":3,"id":"mine-uuid","timestamp":"2026-07-09T10:00:00.000Z","cwd":` + string(projJSON) + `}
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

// TestPath_DefaultLayoutCollidingDirFiltersByHeaderCwd asserts that in the
// default per-cwd layout two projects whose paths encode to the same directory
// (EncodeCwd turns both <base>/a-b and <base>/a/b into the same name) each see
// only their own session file, because discovery checks the header cwd and not
// just the directory.
func TestPath_DefaultLayoutCollidingDirFiltersByHeaderCwd(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(envAgentDir, agentDir)
	base := t.TempDir()
	dashProj := filepath.Join(base, "a-b")
	slashProj := filepath.Join(base, "a", "b")

	dashDir, err := ProjectSessionDir(dashProj)
	if err != nil {
		t.Fatalf("ProjectSessionDir(dash): %v", err)
	}
	slashDir, err := ProjectSessionDir(slashProj)
	if err != nil {
		t.Fatalf("ProjectSessionDir(slash): %v", err)
	}
	if dashDir != slashDir {
		t.Fatalf("expected the two projects to share an encoded dir, got %q and %q", dashDir, slashDir)
	}
	if err := os.MkdirAll(dashDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeSessionForCwd(t, filepath.Join(dashDir, "dash.jsonl"), "dash-uuid", dashProj)
	writeSessionForCwd(t, filepath.Join(dashDir, "slash.jsonl"), "slash-uuid", slashProj)

	got, err := SessionFilesInProject(dashProj)
	if err != nil {
		t.Fatalf("SessionFilesInProject(dash): %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "dash.jsonl" {
		t.Errorf("dash project files = %v, want just dash.jsonl", got)
	}
	got, err = SessionFilesInProject(slashProj)
	if err != nil {
		t.Fatalf("SessionFilesInProject(slash): %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "slash.jsonl" {
		t.Errorf("slash project files = %v, want just slash.jsonl", got)
	}
}

// TestPath_DefaultLayoutKeepsFileWithoutHeaderCwd asserts the default layout
// fails open: a session file whose header has no cwd stays in the listing,
// because the encoded directory already names the project. Only a cwd that is
// present and names another project rejects a file.
func TestPath_DefaultLayoutKeepsFileWithoutHeaderCwd(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv(envAgentDir, agentDir)
	proj := filepath.Join(t.TempDir(), "proj")

	dir, err := ProjectSessionDir(proj)
	if err != nil {
		t.Fatalf("ProjectSessionDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	noCwd := `{"type":"session","version":3,"id":"nocwd-uuid","timestamp":"2026-07-09T10:00:00.000Z"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1783600001000}}
`
	if err := os.WriteFile(filepath.Join(dir, "nocwd.jsonl"), []byte(noCwd), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	writeSessionForCwd(t, filepath.Join(dir, "mine.jsonl"), "mine-uuid", proj)
	writeSessionForCwd(t, filepath.Join(dir, "other.jsonl"), "other-uuid", filepath.Join(t.TempDir(), "elsewhere"))

	got, err := SessionFilesInProject(proj)
	if err != nil {
		t.Fatalf("SessionFilesInProject: %v", err)
	}
	var names []string
	for _, f := range got {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	if want := []string{"mine.jsonl", "nocwd.jsonl"}; !slices.Equal(names, want) {
		t.Errorf("files = %v, want %v", names, want)
	}
}

// writeSessionForCwd writes a pi session file whose header records cwd, plus one
// user message. The cwd goes through json.Marshal so a Windows path with
// backslashes is a valid JSON string, as in a real pi session file.
func writeSessionForCwd(t *testing.T, path, id, cwd string) {
	t.Helper()
	cwdJSON, err := json.Marshal(cwd)
	if err != nil {
		t.Fatalf("marshal cwd: %v", err)
	}
	body := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-07-09T10:00:00.000Z","cwd":` + string(cwdJSON) + `}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-07-09T10:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1783600001000}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
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

func TestPath_SymlinkUsesCanonicalWatchAndResumeDestination(t *testing.T) {
	project, err := spi.GetCanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "linked project_name")
	if err := os.Symlink(project, alias); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Setenv(envSessionDir, "")
	t.Setenv(envAgentDir, t.TempDir())
	realDir, err := ProjectSessionDir(project)
	if err != nil {
		t.Fatal(err)
	}
	aliasDir, err := ProjectSessionDir(alias)
	if err != nil || aliasDir != realDir {
		t.Fatalf("symlink dir=%q canonical=%q error=%v", aliasDir, realDir, err)
	}
	ch, stop := startWatch(t, alias)
	defer stop()
	if err := os.MkdirAll(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	writeSessionForCwd(t, filepath.Join(realDir, "linked.jsonl"), "linked", project)
	if s := waitForSession(t, ch); s.SessionID != "linked" {
		t.Fatalf("wrong session %q", s.SessionID)
	}
	listed, err := NewProvider().ListAgentChatSessions(alias)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list=%v error=%v", listed, err)
	}
	destination, err := NewProvider().NativeSessionPath(alias, "resume.jsonl")
	if err != nil || destination != filepath.Join(realDir, "resume.jsonl") {
		t.Fatalf("destination=%q error=%v", destination, err)
	}
}

func TestParse_RawSnapshotPreservesAcceptedNativeRecords(t *testing.T) {
	oldLimit := maxRecordLineSize
	maxRecordLineSize = 1024
	t.Cleanup(func() { maxRecordLineSize = oldLimit })
	spi.SetDebugBaseDir(t.TempDir())
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	// Unknown fields and inactive branches belong to the raw transcript even
	// though the normalized conversation only follows the current active branch.
	header := piHeaderLine("native-snapshot", t.TempDir()) + "\r\n"
	first := piUserLine("first", "", "inactive branch") + "\n"
	last := strings.TrimSuffix(piUserLine("last", "", "active branch"), "}") + `,"unknownNative":{"keep":true}}`
	expected := header + first + last
	path := filepath.Join(t.TempDir(), "native.jsonl")
	oversized := `{"type":"custom","payload":"` + strings.Repeat("x", 1024) + `"}` + "\n"
	body := header + first + "{broken\n" + oversized + last
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, debug := range []bool{false, true} {
		t.Run(fmt.Sprint("debug=", debug), func(t *testing.T) {
			chat, err := parseToAgentSession(path, "", debug)
			if err != nil {
				t.Fatal(err)
			}
			if chat.RawData != expected {
				t.Fatalf("RawData lost original record bytes or retained rejected records: %q", chat.RawData)
			}
			if len(chat.SessionData.Exchanges) != 1 || chat.Slug != spi.GenerateFilenameFromUserMessage("active branch") {
				t.Fatalf("wrong active branch: %+v", chat.SessionData)
			}
			if debug {
				raw, err := os.ReadFile(filepath.Join(spi.GetDebugDir("native-snapshot"), "2.json"))
				if err != nil {
					t.Fatal(err)
				}
				var decoded map[string]any
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				if decoded["unknownNative"] == nil {
					t.Fatal("debug export lost unknown native fields")
				}
			}
		})
	}
}

func TestWorkspaceFallbackDoesNotInventOrigin(t *testing.T) {
	t.Setenv(envSessionDir, t.TempDir())
	t.Setenv(envAgentDir, "")
	project := t.TempDir()
	canonical, err := spi.GetCanonicalPath(project)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err = spi.GetCanonicalPath(cwd)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(os.Getenv(envSessionDir), "missing-cwd.jsonl")
	// A relative tool path exercises enrichment after fallback resolution.
	body := validSession("missing-cwd", "", "prompt") + `{"type":"message","id":"a","parentId":"m2","message":{"role":"assistant","content":[{"type":"toolCall","id":"call","name":"read","arguments":{"path":"src/main.go","file_path":"invented.go"}}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ origin, want string }{{canonical, canonical}, {"", cwd}, {`C:\foreign\project`, `C:\foreign\project`}} {
		chat, err := NewProvider().GetAgentChatSessionByPath(path, tc.origin, false)
		if err != nil {
			t.Fatal(err)
		}
		data := chat.SessionData
		if data.WorkspaceRoot != tc.want || data.Provider.Version != "unknown" || data.SchemaVersion != schema.CurrentSchemaVersion {
			t.Fatalf("incorrect metadata: %+v", data)
		}
		tool := data.Exchanges[0].Messages[len(data.Exchanges[0].Messages)-1]
		wantHints := []string{spi.NormalizePath("src/main.go", tc.want)}
		if !reflect.DeepEqual(tool.PathHints, wantHints) {
			t.Fatalf("hints=%v want=%v", tool.PathHints, wantHints)
		}
	}
	refs, err := NewProvider().ListAllAgentChatSessions()
	if err != nil || len(refs) != 1 || refs[0].OriginCwd != "" {
		t.Fatalf("invented origin: %+v, err=%v", refs, err)
	}
	// Native cwd wins even when a different caller fallback is supplied.
	if err := os.WriteFile(path, []byte(validSession("native", "/foreign/native", "prompt")), 0600); err != nil {
		t.Fatal(err)
	}
	chat, err := NewProvider().GetAgentChatSessionByPath(path, canonical, false)
	if err != nil || chat.SessionData.WorkspaceRoot != "/foreign/native" {
		t.Fatalf("native cwd replaced: %+v %v", chat, err)
	}
}

func TestPathHintsIgnoreUnverifiedToolsAndAliases(t *testing.T) {
	input := map[string]any{"path": "real.go", "file_path": "fake.go", "target": "fake2.go", "command": "cat command.go"}
	if got := extractPathHints("read", input, "/project"); !reflect.DeepEqual(got, []string{spi.NormalizePath("real.go", "/project")}) {
		t.Fatalf("hints=%v", got)
	}
	if got := extractPathHints("custom", input, "/project"); len(got) != 0 {
		t.Fatalf("guessed extension paths=%v", got)
	}
}

func TestRecordDiagnosticsIncludeFileAndPhysicalLine(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	path := filepath.Join(t.TempDir(), "diagnostics.jsonl")
	data := `{"type":"session","version":3,"id":"diag","cwd":"/project"}` + "\n\n" +
		"{broken\n" +
		`{"type":"message","id":"u","parentId":null,"message":{"role":"user","content":"survives"}}` + "\n" +
		`{"type":"future_kind","id":"x","parentId":"u"}` + "\n" +
		`{"type":"message","id":"r","parentId":"x","message":{"role":"future_role"}}` + "\n" +
		`{"type":"label","id":"l","parentId":"r"}` + "\n" +
		`{"type":"message","id":"a","parentId":"l","message":{"role":"assistant","content":[{"type":"text","text":"still here"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSession(path)
	if err != nil || len(parsed.Exchanges) != 1 || len(parsed.Exchanges[0].Messages) != 2 {
		t.Fatalf("lost tree through unknown entries: %+v %v", parsed, err)
	}
	for _, pass := range []string{"parse", "scan"} {
		if pass == "scan" {
			logs.Reset()
			if _, _, err := readScanEntries(path); err != nil {
				t.Fatal(err)
			}
		}
		lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
		if len(lines) != 3 {
			t.Fatalf("%s: want 3 warnings, got %s", pass, logs.String())
		}
		for i, line := range lines {
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}
			wantLine := []float64{3, 5, 6}[i]
			if record["file"] != path || record["lineNumber"] != wantLine || record["level"] != "WARN" {
				t.Errorf("%s: incomplete diagnostic %s", pass, line)
			}
		}
	}
}

func TestSyncProgressIncludesSkippedCandidates(t *testing.T) {
	for _, flat := range []bool{false, true} {
		t.Run(fmt.Sprint("flat=", flat), func(t *testing.T) {
			t.Setenv(envAgentDir, t.TempDir())
			t.Setenv(envSessionDir, "")
			if flat {
				t.Setenv(envSessionDir, t.TempDir())
			}
			project := t.TempDir()
			canonical, err := spi.GetCanonicalPath(project)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := ProjectSessionDir(project)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"valid.jsonl":   validSession("valid", canonical, "prompt"),
				"bad.jsonl":     "{corrupt header\n",
				"empty.jsonl":   "",
				"missing.jsonl": validSession("missing", "", "prompt"),
				"other.jsonl":   validSession("other", canonical+"-elsewhere", "prompt"),
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var progress [][2]int
			chats, err := NewProvider().GetAgentChatSessions(project, false, func(done, total int) { progress = append(progress, [2]int{done, total}) })
			if err != nil {
				t.Fatal(err)
			}
			if want := [][2]int{{1, 4}, {2, 4}, {3, 4}, {4, 4}}; !reflect.DeepEqual(progress, want) {
				t.Errorf("progress=%v want=%v", progress, want)
			}
			wantCount := 2
			if flat {
				wantCount = 1
			}
			if len(chats) != wantCount {
				t.Fatalf("got %d sessions, want %d", len(chats), wantCount)
			}
			for _, chat := range chats {
				if chat.SessionData.WorkspaceRoot != canonical {
					t.Errorf("fallback=%q want=%q", chat.SessionData.WorkspaceRoot, canonical)
				}
			}
		})
	}
}
