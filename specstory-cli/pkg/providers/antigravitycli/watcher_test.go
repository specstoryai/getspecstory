package antigravitycli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestIsTranscriptPath(t *testing.T) {
	if !isTranscriptPath("/a/b/transcript_full.jsonl") {
		t.Errorf("transcript_full.jsonl should be recognized")
	}
	if !isTranscriptPath("/a/b/transcript.jsonl") {
		t.Errorf("transcript.jsonl fallback should be recognized")
	}
	if isTranscriptPath("/a/b/other.json") {
		t.Errorf("unrelated file should not be recognized")
	}
}

func TestIsTaskLogPath(t *testing.T) {
	if !isTaskLogPath("/h/.gemini/antigravity-cli/brain/c1/.system_generated/tasks/task-34.log") {
		t.Errorf("task log under tasks/ should be recognized")
	}
	if isTaskLogPath("/h/.gemini/antigravity-cli/brain/c1/.system_generated/logs/task-34.log") {
		t.Errorf("task-named file outside tasks/ should not be recognized")
	}
	if isTaskLogPath("/h/.gemini/antigravity-cli/brain/c1/.system_generated/tasks/other.log") {
		t.Errorf("non-task file in tasks/ should not be recognized")
	}
}

// A finished async command writes its output to tasks/task-<step>.log WITHOUT
// touching the transcript, so a task-log event must re-emit the session (with
// the output folded in) even though the transcript's mtime is unchanged —
// that's the freshness-key widening in processTranscriptFile.
func TestProcessTranscriptFile_TaskLogUpdateReEmits(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)
	path := writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-07-24T10:00:00Z","content":"<USER_REQUEST>\nrun it\n</USER_REQUEST>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-07-24T10:00:01Z","tool_calls":[{"name":"run_command","args":{"CommandLine":"slow-build"}}]}`,
		`{"step_index":2,"source":"MODEL","type":"RUN_COMMAND","status":"RUNNING","created_at":"2026-07-24T10:00:02Z","content":"Command still running."}`,
	)

	var mu sync.Mutex
	var calls int
	var lastOutput string
	cb := func(session *spi.AgentChatSession) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		for _, ex := range session.SessionData.Exchanges {
			for _, msg := range ex.Messages {
				if msg.Tool != nil && msg.Tool.Output != nil {
					lastOutput, _ = msg.Tool.Output["content"].(string)
				}
			}
		}
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	processTranscriptFile(path, "", false, cb, state)
	waitFor(t, &mu, &calls, 1)

	// The async output lands later; the transcript is untouched. Bump the log's
	// mtime explicitly so the test doesn't depend on filesystem tick resolution.
	tasksDir := filepath.Join(filepath.Dir(filepath.Dir(path)), tasksDirName)
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	taskPath := filepath.Join(tasksDir, "task-2.log")
	if err := os.WriteFile(taskPath, []byte("BUILD OK\n"), 0o644); err != nil {
		t.Fatalf("write task log: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(taskPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	processTranscriptFile(taskPath, "", false, cb, state)
	waitFor(t, &mu, &calls, 2)

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(lastOutput, "BUILD OK") {
		t.Errorf("re-emitted session should include the async task output, got %q", lastOutput)
	}
}

func TestConversationIDFromTranscriptPath(t *testing.T) {
	p := filepath.Join("/home", ".gemini", "antigravity-cli", "brain", "conv-xyz", ".system_generated", "logs", "transcript_full.jsonl")
	if got := conversationIDFromTranscriptPath(p); got != "conv-xyz" {
		t.Errorf("conversationIDFromTranscriptPath = %q, want conv-xyz", got)
	}
}

func TestProcessTranscriptFile_DedupByModTime(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)
	path := writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"hello"}`,
	)

	var mu sync.Mutex
	var calls int
	cb := func(*spi.AgentChatSession) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	processTranscriptFile(path, "", false, cb, state)
	processTranscriptFile(path, "", false, cb, state) // same modTime → no-op

	// Give the dispatched goroutine(s) a chance to run.
	waitFor(t, &mu, &calls, 1)
}

func TestProcessTranscriptFile_PrefersPrimaryForFallbackEvents(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)
	writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"from primary"}`,
	)
	fallbackPath := writeConversationFile(t, home, "conv-1", fallbackTranscriptFileName,
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"from fallback"}`,
	)

	var mu sync.Mutex
	var calls int
	var agentText string
	cb := func(session *spi.AgentChatSession) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		agentText = session.SessionData.Exchanges[0].Messages[1].Content[0].Text
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	processTranscriptFile(fallbackPath, "", false, cb, state)

	waitFor(t, &mu, &calls, 1)
	mu.Lock()
	defer mu.Unlock()
	if agentText != "from primary" {
		t.Errorf("fallback event parsed %q, want primary transcript content", agentText)
	}
}

func TestProcessTranscriptFile_IgnoresTranscriptOutsideBrain(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)

	dir := filepath.Join(t.TempDir(), "rogue-conv", systemGeneratedDir, logsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rogue transcript dir: %v", err)
	}
	path := filepath.Join(dir, transcriptFileName)
	if err := os.WriteFile(path, []byte(
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`+"\n"+
			`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"hello"}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write rogue transcript: %v", err)
	}

	var mu sync.Mutex
	var calls int
	cb := func(*spi.AgentChatSession) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	processTranscriptFile(path, "", false, cb, state)
	time.Sleep(25 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 0 {
		t.Fatalf("callback fired %d times for transcript outside Antigravity brain", calls)
	}
}

func TestScanAndProcessConversations(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)
	writeConversation(t, home, "conv-1",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-05-26T21:31:13Z","content":"<USER_REQUEST>\nhi\n</USER_REQUEST>"}`,
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-05-26T21:31:14Z","content":"hello"}`,
	)

	var mu sync.Mutex
	var calls int
	cb := func(*spi.AgentChatSession) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	if err := scanAndProcessConversations("", false, cb, state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	waitFor(t, &mu, &calls, 1)
}

// waitFor polls until *counter reaches want or the test times out, accounting
// for the watcher dispatching callbacks on a goroutine.
func waitFor(t *testing.T, mu *sync.Mutex, counter *int, want int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		mu.Lock()
		got := *counter
		mu.Unlock()
		if got == want {
			return
		}
		if got > want {
			t.Fatalf("callback fired %d times, want %d", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("callback never reached %d", want)
}
