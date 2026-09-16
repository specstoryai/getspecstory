package claudecode

import (
	"testing"
)

func assistantRecord(sidechain bool, tools ...[2]string) JSONLRecord {
	var content []interface{}
	for _, t := range tools {
		content = append(content, map[string]interface{}{
			"type":  "tool_use",
			"id":    t[0],
			"name":  t[1],
			"input": map[string]interface{}{},
		})
	}
	return JSONLRecord{Data: map[string]interface{}{
		"type":        "assistant",
		"isSidechain": sidechain,
		"message":     map[string]interface{}{"role": "assistant", "content": content},
	}}
}

func toolResultRecord(ids ...string) JSONLRecord {
	var content []interface{}
	for _, id := range ids {
		content = append(content, map[string]interface{}{
			"type":        "tool_result",
			"tool_use_id": id,
			"content":     "ok",
		})
	}
	return JSONLRecord{Data: map[string]interface{}{
		"type":    "user",
		"message": map[string]interface{}{"role": "user", "content": content},
	}}
}

func promptRecord(sidechain bool) JSONLRecord {
	return JSONLRecord{Data: map[string]interface{}{
		"type":        "user",
		"isSidechain": sidechain,
		"message":     map[string]interface{}{"role": "user", "content": "hello"},
	}}
}

func TestOpenShellToolUses(t *testing.T) {
	tests := []struct {
		name    string
		records []JSONLRecord
		want    []string
	}{
		{
			name:    "no tool calls",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false)},
			want:    nil,
		},
		{
			name:    "open bash call",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false, [2]string{"t1", "Bash"})},
			want:    []string{"t1"},
		},
		{
			name: "bash call closed by its result",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Bash"}),
				toolResultRecord("t1"),
			},
			want: nil,
		},
		{
			name:    "open non-shell tool is not gated",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false, [2]string{"t1", "Read"}, [2]string{"t2", "Agent"})},
			want:    nil,
		},
		{
			name: "parallel calls: one closed, one open",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Bash"}),
				assistantRecord(false, [2]string{"t2", "Bash"}),
				toolResultRecord("t1"),
			},
			want: []string{"t2"},
		},
		{
			name: "new user prompt resets an open call from the previous turn",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Bash"}),
				promptRecord(false),
			},
			want: nil,
		},
		{
			name: "sidechain prompt does not reset, sidechain bash is gated",
			records: []JSONLRecord{
				promptRecord(false),
				assistantRecord(false, [2]string{"t1", "Agent"}),
				promptRecord(true),
				assistantRecord(true, [2]string{"t2", "Bash"}),
			},
			want: []string{"t2"},
		},
		{
			name:    "tool name matching is case-insensitive and covers PowerShell",
			records: []JSONLRecord{promptRecord(false), assistantRecord(false, [2]string{"t1", "bash"}, [2]string{"t2", "PowerShell"})},
			want:    []string{"t1", "t2"},
		},
		{
			name: "malformed records are ignored",
			records: []JSONLRecord{
				{Data: map[string]interface{}{"type": "assistant"}},
				{Data: map[string]interface{}{"type": "user", "message": "not a map"}},
				{Data: map[string]interface{}{"type": "system", "subtype": "turn_duration"}},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := openShellToolUses(tt.records)
			if len(got) != len(tt.want) {
				t.Fatalf("openShellToolUses() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("openShellToolUses() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestDeferScanRegistry(t *testing.T) {
	const file = "/tmp/shell-gate-test.jsonl"
	clearDeferredScan(file)

	deferScan("/tmp/project", file, []string{"t1"})
	deferredScansMu.Lock()
	entry, armed := deferredScans[file]
	deferredScansMu.Unlock()
	if !armed || entry.claudeProjectDir != "/tmp/project" {
		t.Fatalf("expected a deferred entry for %s, got armed=%v entry=%+v", file, armed, entry)
	}

	// A second deferral keeps the first timer so the fallback deadline is stable
	deferScan("/tmp/project", file, []string{"t1", "t2"})
	deferredScansMu.Lock()
	same := deferredScans[file].timer == entry.timer
	deferredScansMu.Unlock()
	if !same {
		t.Fatalf("expected the original timer to be kept on repeated deferral")
	}

	clearDeferredScan(file)
	deferredScansMu.Lock()
	_, still := deferredScans[file]
	deferredScansMu.Unlock()
	if still {
		t.Fatalf("expected the deferred entry to be cleared")
	}
}
