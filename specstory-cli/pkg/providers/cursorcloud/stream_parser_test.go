package cursorcloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []sseEvent
	}{
		{
			name:  "blocks with and without ids",
			input: "event: status\ndata: {\"status\":\"RUNNING\"}\n\nid: 1791904019438-1\nevent: thinking\ndata: {\"text\":\"a\"}\n\n",
			want: []sseEvent{
				{Event: "status", Data: []byte(`{"status":"RUNNING"}`)},
				{ID: "1791904019438-1", Event: "thinking", Data: []byte(`{"text":"a"}`)},
			},
		},
		{
			name:  "final block without a trailing blank line",
			input: "id: 1-0\nevent: done\ndata: {}",
			want:  []sseEvent{{ID: "1-0", Event: "done", Data: []byte(`{}`)}},
		},
		{
			name:  "comment lines and CRLF are ignored",
			input: ": keepalive\r\nevent: heartbeat\r\ndata: {}\r\n\r\n",
			want:  []sseEvent{{Event: "heartbeat", Data: []byte(`{}`)}},
		},
		{
			name:  "multi-line data joins with newlines and stays a string",
			input: "event: note\ndata: one\ndata: two\n\n",
			want:  []sseEvent{{Event: "note", Data: []byte(`"one\ntwo"`)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSSEBytes(t, []byte(tt.input))
			if len(got) != len(tt.want) {
				t.Fatalf("got %d events %+v, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i].ID != tt.want[i].ID || got[i].Event != tt.want[i].Event || string(got[i].Data) != string(tt.want[i].Data) {
					t.Errorf("event %d = {%s %s %s}, want {%s %s %s}", i, got[i].ID, got[i].Event, got[i].Data, tt.want[i].ID, tt.want[i].Event, tt.want[i].Data)
				}
			}
		})
	}
}

func TestReadSSESkipsOversizedBlock(t *testing.T) {
	prev := maxSSELineSize
	maxSSELineSize = 64
	t.Cleanup(func() { maxSSELineSize = prev })

	input := "id: 1-0\nevent: assistant\ndata: {\"text\":\"" + strings.Repeat("x", 200) + "\"}\n\n" +
		"id: 2-0\nevent: assistant\ndata: {\"text\":\"kept\"}\n\n"
	got := parseSSEBytes(t, []byte(input))
	if len(got) != 1 || got[0].ID != "2-0" {
		t.Fatalf("got %+v, want only the block after the oversized one", got)
	}
}

func TestReadSSEStopsWhenEmitDeclines(t *testing.T) {
	input := "id: 1-0\nevent: done\ndata: {}\n\nid: 2-0\nevent: assistant\ndata: {}\n\n"
	var seen []string
	err := readSSE(strings.NewReader(input), "test", func(ev sseEvent) bool {
		seen = append(seen, ev.Event)
		return ev.Event != "done"
	})
	if err != nil || len(seen) != 1 {
		t.Fatalf("seen %v err %v, want only done", seen, err)
	}
}

func TestReadEventsFileSkipsCorruptedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.events.jsonl")
	content := `{"id":"1-0","event":"thinking","data":{"text":"a"}}` + "\n" +
		`{not json` + "\n" +
		`{"id":"2-0","data":{}}` + "\n" + // no event name: not a record this provider wrote
		`{"id":"3-0","event":"done","data":{}}` // final line without newline
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := readEventsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != "1-0" || events[1].Event != "done" {
		t.Fatalf("events = %+v", events)
	}
	if !eventsAreWhole(events) {
		t.Error("a file ending in done should be whole")
	}
}

func TestEventsRoundTripThroughMirrorFile(t *testing.T) {
	fx := loadFixture(t, "fixture-run")
	events := parseSSEBytes(t, fx.streams[fx.runs[0].run.ID])
	m := mirror{root: t.TempDir()}
	if err := m.writeEvents("bc-1", "run-1", events); err != nil {
		t.Fatal(err)
	}
	back, err := readEventsFile(m.eventsFile("bc-1", "run-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(events) {
		t.Fatalf("round trip kept %d of %d events", len(back), len(events))
	}
	for i := range events {
		if back[i].ID != events[i].ID || back[i].Event != events[i].Event || string(back[i].Data) != string(events[i].Data) {
			t.Fatalf("event %d changed in the round trip", i)
		}
	}
}

func TestEventTime(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"1791904019438-1", "2026-10-13T15:06:59.438Z"},
		{"1791904062577-0", "2026-10-13T15:07:42.577Z"},
		{"", ""},
		{"abc-1", ""},
	}
	for _, tt := range tests {
		if got := eventTime(tt.id); got != tt.want {
			t.Errorf("eventTime(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestValidIDs(t *testing.T) {
	tests := []struct {
		id    string
		agent bool
		run   bool
	}{
		{"bc-a2a25972-6529-47c0-9691-8f135d0a6527", true, false},
		{"run-718a7da2-309a-4396-b54b-469483f74e5a", false, true},
		{"bc-../../etc", false, false},
		{"bc-", false, false},
		{"bc-abc/def", false, false},
	}
	for _, tt := range tests {
		if got := validAgentID(tt.id); got != tt.agent {
			t.Errorf("validAgentID(%q) = %v", tt.id, got)
		}
		if got := validRunID(tt.id); got != tt.run {
			t.Errorf("validRunID(%q) = %v", tt.id, got)
		}
	}
}
