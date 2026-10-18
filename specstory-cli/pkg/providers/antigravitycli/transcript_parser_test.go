package antigravitycli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestLoadHistoryIndexAcceptsLargeSidecarRecords(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)
	// Above the old 16MB cap, within the shared 64MB native-record limit.
	display := strings.Repeat("x", 17*1024*1024)
	line, err := json.Marshal(historyEntry{Display: display, ConversationID: "large", Workspace: "/project"})
	if err != nil {
		t.Fatal(err)
	}
	writeHistory(t, home, string(line))
	index, err := loadHistoryIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := index["large"]; got.Display != display || got.Workspace != "/project" {
		t.Fatal("large history record lost")
	}
}

func TestTaskLogOwnershipMatchesNativeNames(t *testing.T) {
	for _, step := range []int{-1, 0, 1, 42} {
		name := fmt.Sprintf("task-%d.log", step)
		got, ok := taskLogStep(name)
		if !ok || got != step || !isTaskLogName(name) {
			t.Errorf("native task log %q not recognized", name)
		}
	}
	// Discovery accepts these native numeric spellings; cleanup must too.
	for _, name := range []string{"task-01.log", "task-+1.log"} {
		if !isTaskLogName(name) {
			t.Errorf("native task log %q not owned", name)
		}
	}
	for _, name := range []string{"notes.log", "task-.log", "task-1.log.bak", "../task-1.log", "task-999999999999999999999999.log"} {
		if isTaskLogName(name) {
			t.Errorf("unrelated task file %q owned", name)
		}
	}
}

func TestDebugRawPreservesSequencedRecordsAndTaskSnapshot(t *testing.T) {
	testutil.IsolateDebugDir(t)
	home := t.TempDir()
	user := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-18T12:00:00Z","content":"hello","unknownNative":9007199254740993}`
	call := `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-09-18T12:00:01Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"echo hello\"","unknownArg":"\"keep\""}}]}`
	result := `{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"RUNNING","created_at":"2026-09-18T12:00:02Z","content":"running","unknownResult":{"keep":true}}`
	// The fallback transcript has encoded args, and async results can be out
	// of order. Debug records must preserve the native values in step order.
	path := writeConversationFile(t, home, "debug-tasks", fallbackTranscriptFileName, result, user, "{broken", call)
	tasksDir := filepath.Join(filepath.Dir(filepath.Dir(path)), tasksDirName)
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(tasksDir, "task-2.log")
	logBytes := []byte("  original async output\r\n\n")
	if err := os.WriteFile(logPath, logBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := parseTranscript("debug-tasks", path, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Steps[1].ToolCalls[0].Args["CommandLine"]; got != "echo hello" {
		t.Fatalf("fallback normalization failed: %v", got)
	}
	wantRaw := result + "\n" + user + "\n" + call + "\n"
	if snapshot.RawData != wantRaw {
		t.Fatalf("raw transcript retained malformed data or lost accepted bytes: %s", snapshot.RawData)
	}
	if chat := convertToAgentSession(snapshot, home, false); chat == nil {
		t.Fatal("conversion failed")
	}
	dir := spi.GetDebugDir(snapshot.ConversationID)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("debug-disabled conversion created output: %v", err)
	}
	// The transcript and sidecar can both change before conversion.
	if err := os.WriteFile(path, []byte(user+"\n"+call+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("new output"), 0o600); err != nil {
		t.Fatal(err)
	}
	chat := convertToAgentSession(snapshot, home, true)
	if chat == nil || chat.RawData != wantRaw {
		t.Fatal("conversion lost native snapshot")
	}
	var tool *ToolInfo
	for _, message := range chat.SessionData.Exchanges[0].Messages {
		if message.Tool != nil {
			tool = message.Tool
			break
		}
	}
	if tool == nil || !strings.Contains(fmt.Sprint(tool.Output), "original async output") || strings.Contains(fmt.Sprint(tool.Output), "new output") {
		t.Fatalf("conversion did not use original sidecar: %+v", tool)
	}
	for i, line := range []string{user, call, result} {
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%d.json", i+1)))
		if err != nil || !bytes.Contains(data, []byte("\n  \"")) {
			t.Fatalf("record %d missing or not pretty printed: %v", i+1, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil || compact.String() != line {
			t.Fatalf("record %d changed native values/order: %s (%v)", i+1, data, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "tasks", "task-2.log"))
	if err != nil || !bytes.Equal(data, logBytes) {
		t.Fatalf("sidecar bytes changed: %q (%v)", data, err)
	}
	data, err = os.ReadFile(filepath.Join(dir, "raw-transcript.jsonl"))
	if err != nil || string(data) != wantRaw {
		t.Fatalf("raw copy differs from accepted snapshot: %s (%v)", data, err)
	}
	testutil.AssertDebugRefresh(t, dir, []string{"3.json", "tasks/task-2.log"},
		[]string{"session-data.json", "3-notes.json", "tasks/notes.log"}, func() {
			if err := os.Remove(logPath); err != nil {
				t.Fatal(err)
			}
			newSnapshot, err := parseTranscript("debug-tasks", path, nil, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			if convertToAgentSession(newSnapshot, home, true) == nil {
				t.Fatal("refresh failed")
			}
		})
}

func TestLoadHistoryIndex(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)

	// Missing file → empty map, nil error.
	idx, err := loadHistoryIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(idx) != 0 {
		t.Errorf("expected empty index, got %d", len(idx))
	}

	writeHistory(t, home,
		`{"display":"first prompt","timestamp":1779831073907,"workspace":"/proj"}`, // no conversationId → skipped
		`{"display":"second","timestamp":1779831156198,"workspace":"/proj","conversationId":"conv-1"}`,
		`bad json`,
	)

	idx, err = loadHistoryIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(idx) != 1 {
		t.Fatalf("expected 1 mapped conversation, got %d", len(idx))
	}
	if idx["conv-1"].Workspace != "/proj" {
		t.Errorf("expected workspace /proj, got %q", idx["conv-1"].Workspace)
	}
}

func TestParseTranscript(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)

	path := writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		``, // blank line tolerated
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-05-26T21:31:16Z","content":"hello"}`,
		`{bad json`, // malformed line skipped
	)

	history := map[string]historyEntry{"conv-1": {Workspace: "/proj"}}
	session, err := parseTranscript("conv-1", path, history, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(session.Steps) != 2 {
		t.Fatalf("expected 2 decoded steps, got %d", len(session.Steps))
	}
	if session.CreatedAt != "2026-05-26T21:31:13Z" || session.UpdatedAt != "2026-05-26T21:31:16Z" {
		t.Errorf("unexpected created/updated: %q / %q", session.CreatedAt, session.UpdatedAt)
	}
	if session.Workspace != "/proj" {
		t.Errorf("expected workspace from history, got %q", session.Workspace)
	}
	if session.RawData == "" {
		t.Errorf("expected raw data to be retained when wantRawData is true")
	}

	// wantRawData=false must not retain bytes.
	session2, err := parseTranscript("conv-1", path, history, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session2.RawData != "" {
		t.Errorf("expected no raw data when wantRawData is false")
	}
}

func TestNormalizeFallbackArgs(t *testing.T) {
	// transcript.jsonl double-encodes every value; normalization decodes once.
	in := map[string]any{
		"CommandLine":       `"git status"`,
		"WaitMsBeforeAsync": "5000",
		"Overwrite":         "true",
	}
	out := normalizeFallbackArgs(in)
	if out["CommandLine"] != "git status" {
		t.Errorf("CommandLine = %v, want git status", out["CommandLine"])
	}
	if out["Overwrite"] != true {
		t.Errorf("Overwrite = %v (%T), want bool true", out["Overwrite"], out["Overwrite"])
	}
	if n, ok := out["WaitMsBeforeAsync"].(float64); !ok || n != 5000 {
		t.Errorf("WaitMsBeforeAsync = %v (%T), want 5000", out["WaitMsBeforeAsync"], out["WaitMsBeforeAsync"])
	}
}

func TestLoadTaskOutputs(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)
	path := writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
	)
	tasksDir := filepath.Join(filepath.Dir(filepath.Dir(path)), "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "task-34.log"), []byte("async output\n"), 0o644); err != nil {
		t.Fatalf("write task log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tasksDir, "not-a-task.log"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored task log: %v", err)
	}

	session, err := parseTranscript("conv-1", path, nil, nil, false)
	if err != nil {
		t.Fatalf("parseTranscript: %v", err)
	}
	if got := session.TaskOutputs[34]; got != "async output" {
		t.Errorf("TaskOutputs[34] = %q, want async output", got)
	}
}

func TestSessionMetadata(t *testing.T) {
	session := &agSession{
		ConversationID: "conv-1",
		CreatedAt:      "2026-05-26T21:31:13Z",
		Steps: []transcriptStep{
			{Type: typeUserInput, Content: "<USER_REQUEST>\nFix the bug\n</USER_REQUEST>"},
		},
	}
	meta := sessionMetadata(session, map[string]historyEntry{}, nil)
	if meta == nil {
		t.Fatalf("expected metadata")
	}
	if meta.SessionID != "conv-1" || meta.CreatedAt != "2026-05-26T21:31:13Z" {
		t.Errorf("unexpected metadata: %+v", meta)
	}
	if meta.Slug == "" || meta.Name == "" {
		t.Errorf("expected slug and name to be derived, got %+v", meta)
	}

	// No user prompt → nil (skip).
	empty := &agSession{ConversationID: "c", Steps: []transcriptStep{{Type: typePlannerResponse, Content: "x"}}}
	if sessionMetadata(empty, map[string]historyEntry{}, nil) != nil {
		t.Errorf("expected nil metadata for session without user prompt")
	}
}

func TestMsEpochToRFC3339(t *testing.T) {
	if got := msEpochToRFC3339(0); got != "" {
		t.Errorf("expected empty for 0, got %q", got)
	}
	if got := msEpochToRFC3339(1779831073907); got == "" {
		t.Errorf("expected a timestamp for valid epoch ms")
	}
}
