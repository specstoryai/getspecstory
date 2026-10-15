package cursorcloud

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

var updateGolden = flag.Bool("update", false, "rewrite golden session-data files")

// testWorkspace is the project root used for golden output. It is POSIX
// shaped on every OS because path hints are computed by shape.
const testWorkspace = "/project"

// snapshotFromFixture mirrors a fixture into a temp dir and reads it back,
// exercising the same read path the provider uses.
func snapshotFromFixture(t *testing.T, name string) *agentSnapshot {
	t.Helper()
	fx := loadFixture(t, name)
	m := mirror{root: t.TempDir()}
	writeFixtureMirror(t, m, fx)
	snap, err := loadAgentSnapshot(m.agentDir(fx.agent.agent.ID))
	if err != nil || snap == nil {
		t.Fatalf("load snapshot: %v", err)
	}
	return snap
}

func marshalSession(t *testing.T, data *schema.SessionData) []byte {
	t.Helper()
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func TestBuildSessionDataGolden(t *testing.T) {
	for _, name := range []string{"fixture-run", "tool-enumeration", "failure-run"} {
		t.Run(name, func(t *testing.T) {
			snap := snapshotFromFixture(t, name)
			data, _ := buildSessionData(snap, testWorkspace)
			if data == nil {
				t.Fatal("no session built")
			}
			if !data.Validate() {
				t.Error("session data failed schema validation")
			}
			got := marshalSession(t, data)

			// A second build of the same snapshot must change no bytes.
			again, _ := buildSessionData(snap, testWorkspace)
			if !bytes.Equal(got, marshalSession(t, again)) {
				t.Error("a second build produced different output")
			}

			golden := filepath.Join("testdata", name, "session-data.golden.json")
			if *updateGolden {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("session data differs from %s; run with -update after checking the change\n%s", golden, firstDifference(got, want))
			}
		})
	}
}

func TestBuildSessionDataStructure(t *testing.T) {
	snap := snapshotFromFixture(t, "failure-run")
	data, firstPrompt := buildSessionData(snap, testWorkspace)

	if len(data.Exchanges) != 2 {
		t.Fatalf("got %d exchanges, want one per run", len(data.Exchanges))
	}
	if data.Exchanges[0].ExchangeID != "run-ec41805a-07c4-4c80-874f-f077b9877582" {
		t.Errorf("first exchange is %s, want the earlier run first", data.Exchanges[0].ExchangeID)
	}
	if !strings.HasPrefix(firstPrompt, "Verification task for a transcript parser") {
		t.Errorf("first prompt = %q", firstPrompt)
	}
	if data.Slug != "verification-task-for-a" {
		t.Errorf("slug = %q", data.Slug)
	}
	if data.Provider.Version != "unknown" || data.Provider.ID != ProviderID {
		t.Errorf("provider = %+v", data.Provider)
	}

	first := data.Exchanges[0]
	if first.Messages[0].Role != schema.RoleUser || first.Messages[0].Timestamp != "2026-10-13T16:30:30.790Z" {
		t.Errorf("first message = %+v, want the user prompt stamped from its event id", first.Messages[0])
	}
	// The run opens with thinking then text, sharing one agent message.
	if parts := first.Messages[1].Content; len(parts) != 2 || parts[0].Type != schema.ContentTypeThinking || parts[1].Type != schema.ContentTypeText {
		t.Errorf("second message parts = %+v, want thinking then text", parts)
	}
	if first.Metadata["branch"] != "cursor/specstory-failure-capture-1e05" || first.Metadata["agentUrl"] == nil {
		t.Errorf("metadata = %+v", first.Metadata)
	}
	// The result event duplicates the final text; it must appear once.
	final := "Completed all six verification steps"
	count := 0
	for _, msg := range first.Messages {
		for _, part := range msg.Content {
			count += strings.Count(part.Text, final)
		}
	}
	if count != 1 {
		t.Errorf("final text rendered %d times, want once", count)
	}
}

func TestParseRunEventsToolMerging(t *testing.T) {
	ev := func(id, event, data string) sseEvent {
		return sseEvent{ID: id, Event: event, Data: json.RawMessage(data)}
	}
	tests := []struct {
		name       string
		events     []sseEvent
		wantArgs   string
		wantResult string
	}{
		{
			name: "running event without args, args arrive later",
			events: []sseEvent{
				ev("1-0", "tool_call", `{"callId":"c1","name":"edit_file","status":"running"}`),
				ev("2-0", "tool_call", `{"callId":"c1","name":"edit_file","status":"running","args":{"path":"/workspace/a.txt"}}`),
				ev("3-0", "tool_call", `{"callId":"c1","name":"edit_file","status":"completed","args":{"path":"/workspace/a.txt","streamContent":"x"},"result":{"success":{"linesAdded":1}}}`),
			},
			wantArgs:   `{"path":"/workspace/a.txt","streamContent":"x"}`,
			wantResult: `{"success":{"linesAdded":1}}`,
		},
		{
			// Observed on get_mcp_tools: a placeholder error rides a running
			// event while arguments stream, then the call completes.
			name: "provisional error on running event is replaced",
			events: []sseEvent{
				ev("1-0", "tool_call", `{"callId":"c1","name":"get_mcp_tools","status":"running","args":{"toolCallId":"unknown-tool-call-id"},"result":{"error":{"error":"Invalid arguments"}}}`),
				ev("2-0", "tool_call", `{"callId":"c1","name":"get_mcp_tools","status":"running","args":{"server":"cursor"}}`),
				ev("3-0", "tool_call", `{"callId":"c1","name":"get_mcp_tools","status":"completed","args":{"server":"cursor"},"result":{"success":{"content":"{}"}}}`),
			},
			wantArgs:   `{"server":"cursor"}`,
			wantResult: `{"success":{"content":"{}"}}`,
		},
		{
			name: "interrupted call keeps no provisional result",
			events: []sseEvent{
				ev("1-0", "tool_call", `{"callId":"c1","name":"get_mcp_tools","status":"running","args":{"server":"cursor"},"result":{"error":{"error":"Invalid arguments"}}}`),
			},
			wantArgs:   `{"server":"cursor"}`,
			wantResult: `null`,
		},
		{
			name: "completed error result is kept",
			events: []sseEvent{
				ev("1-0", "tool_call", `{"callId":"c1","name":"read_file","status":"running","args":{"path":"/workspace/x"}}`),
				ev("2-0", "tool_call", `{"callId":"c1","name":"read_file","status":"completed","args":{"path":"/workspace/x"},"result":{"error":{"errorMessage":"File not found"}}}`),
			},
			wantArgs:   `{"path":"/workspace/x"}`,
			wantResult: `{"error":{"errorMessage":"File not found"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transcript := parseRunEvents(tt.events, "test")
			if len(transcript.segments) != 1 || transcript.segments[0].call == nil {
				t.Fatalf("segments = %+v, want one tool call", transcript.segments)
			}
			call := transcript.segments[0].call
			if got := compactJSON(call.args); got != tt.wantArgs {
				t.Errorf("args = %s, want %s", got, tt.wantArgs)
			}
			if got := compactJSON(call.result); got != tt.wantResult {
				t.Errorf("result = %s, want %s", got, tt.wantResult)
			}
			if call.time != "1970-01-01T00:00:00.001Z" {
				t.Errorf("call time = %q, want the first event's time", call.time)
			}
		})
	}
}

func TestParseRunEventsOrderingAndUnknowns(t *testing.T) {
	events := []sseEvent{
		{Event: "status", Data: json.RawMessage(`{"status":"RUNNING"}`)},
		{ID: "1000-0", Event: "interaction_update", Data: json.RawMessage(`{"type":"user-message-appended","userMessage":{"text":"hi"}}`)},
		{ID: "1001-0", Event: "thinking", Data: json.RawMessage(`{"text":"Let "}`)},
		{ID: "1001-0", Event: "interaction_update", Data: json.RawMessage(`{"type":"thinking-delta","text":"Let "}`)},
		{ID: "1002-0", Event: "thinking", Data: json.RawMessage(`{"text":"me"}`)},
		{ID: "1003-0", Event: "assistant", Data: json.RawMessage(`{"text":"Hello"}`)},
		{ID: "1004-0", Event: "brand_new_event", Data: json.RawMessage(`{}`)},
		{ID: "1005-0", Event: "interaction_update", Data: json.RawMessage(`{"type":"brand-new-type"}`)},
		{Event: "heartbeat", Data: json.RawMessage(`{}`)},
		{ID: "1006-0", Event: "assistant", Data: json.RawMessage(`{"text":" world"}`)},
		{ID: "1007-0", Event: "result", Data: json.RawMessage(`{"text":"Hello world"}`)},
		{ID: "1007-0", Event: "done", Data: json.RawMessage(`{}`)},
	}
	tr := parseRunEvents(events, "test")
	if tr.userText != "hi" || tr.startTime != "1970-01-01T00:00:01.000Z" || tr.resultTime != "1970-01-01T00:00:01.007Z" {
		t.Errorf("transcript = %+v", tr)
	}
	if len(tr.segments) != 2 || tr.segments[0].text.String() != "Let me" || tr.segments[1].text.String() != "Hello world" {
		var got []string
		for _, s := range tr.segments {
			got = append(got, s.text.String())
		}
		t.Errorf("segments = %q, want deltas concatenated per kind and unknown events ignored", got)
	}
}

func TestV0FallbackForExpiredPartialRun(t *testing.T) {
	snap := snapshotFromFixture(t, "failure-run")
	// Truncate the second run's capture as if the watcher stopped mid-run and
	// the stream expired before a sync could replay it.
	snap.runs[1].events = snap.runs[1].events[:40]
	// And drop the first run's capture entirely.
	snap.runs[0].events = nil

	data, _ := buildSessionData(snap, testWorkspace)
	for i, ex := range data.Exchanges {
		if ex.Metadata["source"] != "v0-conversation" {
			t.Errorf("exchange %d source = %v, want v0-conversation", i, ex.Metadata["source"])
		}
	}
	first := data.Exchanges[0]
	// Turn 0 starts with the agent's first user message, whose id is a bare UUID.
	if first.Messages[0].ID != "cde498a2-37da-4b54-b81f-82bc82341d4b" || first.Messages[0].Role != schema.RoleUser {
		t.Errorf("turn 0 first message = %+v", first.Messages[0])
	}
	if len(first.Messages) != 3 {
		t.Errorf("turn 0 has %d messages, want 3", len(first.Messages))
	}
	second := data.Exchanges[1]
	if second.Messages[0].ID != "turn-1:user" || len(second.Messages) != 2 {
		t.Errorf("turn 1 messages = %+v", second.Messages)
	}
	for _, msg := range append(first.Messages, second.Messages...) {
		if msg.Tool != nil {
			t.Error("the v0 fallback is text only")
		}
	}
}

// TestV0FallbackSkippedWhenARunIsMissing: v0 turns pair with runs by position,
// so a run missing from the mirror would hand turn 0's text to what is really
// run 1. The partial stream must be kept instead.
func TestV0FallbackSkippedWhenARunIsMissing(t *testing.T) {
	snap := snapshotFromFixture(t, "failure-run")
	snap.runs = snap.runs[1:]
	snap.runs[0].events = snap.runs[0].events[:40]

	data, _ := buildSessionData(snap, testWorkspace)
	if data == nil || len(data.Exchanges) != 1 {
		t.Fatalf("got %+v, want the remaining run's exchange", data)
	}
	ex := data.Exchanges[0]
	if ex.Metadata["source"] == "v0-conversation" {
		t.Error("v0 text used although its turns no longer line up with the runs")
	}
	for _, msg := range ex.Messages {
		if msg.ID == "cde498a2-37da-4b54-b81f-82bc82341d4b" {
			t.Error("turn 0's first prompt was attributed to run 1")
		}
	}
}

func TestPartialStreamUsedWhileRunIsLive(t *testing.T) {
	snap := snapshotFromFixture(t, "failure-run")
	snap.runs[1].events = snap.runs[1].events[:60]
	snap.runs[1].run.Status = "RUNNING"
	data, _ := buildSessionData(snap, testWorkspace)
	if src := data.Exchanges[1].Metadata["source"]; src != nil {
		t.Errorf("live run source = %v, want the partial stream", src)
	}
}

func TestReplayedStreamBuildsTheSameExchange(t *testing.T) {
	live := snapshotFromFixture(t, "failure-run")
	replay, err := os.ReadFile(filepath.Join("testdata", "failure-run", "replay-run-ec41805a-07c4-4c80-874f-f077b9877582.sse"))
	if err != nil {
		t.Fatal(err)
	}
	replayed := *live
	replayed.runs = append([]runSnapshot(nil), live.runs...)
	replayed.runs[0].events = parseSSEBytes(t, replay)

	a, _ := buildSessionData(live, testWorkspace)
	b, _ := buildSessionData(&replayed, testWorkspace)
	if !bytes.Equal(marshalSession(t, a), marshalSession(t, b)) {
		t.Error("a full replay must rebuild the live capture's exchange exactly")
	}
}

func TestPathHints(t *testing.T) {
	tests := []struct {
		name  string
		tool  string
		input map[string]any
		want  []string
	}{
		{"edit under workspace", "edit_file", map[string]any{"path": "/workspace/notes/hello.txt"}, []string{"notes/hello.txt"}},
		{"delete", "delete_file", map[string]any{"path": "/workspace/tmp.txt"}, []string{"tmp.txt"}},
		{"read is not a write", "read_file", map[string]any{"path": "/workspace/a.txt"}, nil},
		{"shell redirect", "run_terminal_cmd", map[string]any{"command": "echo hi > out.txt", "workingDirectory": "/workspace"}, []string{"out.txt"}},
		{"path outside workspace kept", "edit_file", map[string]any{"path": "/tmp/x"}, []string{"/tmp/x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathHints(tt.tool, tt.input, testWorkspace)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("pathHints = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildRawDataPreservesNativeObjects(t *testing.T) {
	snap := snapshotFromFixture(t, "failure-run")
	raw, err := buildRawData(snap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Agent map[string]any `json:"agent"`
		Runs  []struct {
			Run    map[string]any `json:"run"`
			Events []sseEvent     `json:"events"`
		} `json:"runs"`
		Conversation map[string]any `json:"conversation"`
		Usage        map[string]any `json:"usage"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	// env and workOnCurrentBranch are not read by the provider; they must survive.
	if decoded.Agent["env"] == nil || decoded.Agent["workOnCurrentBranch"] == nil {
		t.Error("unknown agent fields were dropped")
	}
	if len(decoded.Runs) != 2 || len(decoded.Runs[1].Events) != len(snap.runs[1].events) || decoded.Usage["cost"] == nil || decoded.Conversation["messages"] == nil {
		t.Error("raw data is missing runs, events, usage or conversation")
	}
}

// firstDifference describes the first line where got and want diverge, so a
// mismatch seen only on another platform can be diagnosed from the test log.
func firstDifference(got, want []byte) string {
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			return fmt.Sprintf("line %d:\n  got:  %q\n  want: %q", i+1, g, w)
		}
	}
	return fmt.Sprintf("same lines, lengths %d and %d", len(got), len(want))
}
