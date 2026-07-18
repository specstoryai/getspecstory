package claudecode

import "testing"

func userRecord(uuid, text, ts string) JSONLRecord {
	return JSONLRecord{Data: map[string]interface{}{
		"type":      "user",
		"uuid":      uuid,
		"timestamp": ts,
		"sessionId": "s1",
		"message":   map[string]interface{}{"role": "user", "content": text},
	}}
}

func assistantRecord(uuid, text, ts string) JSONLRecord {
	return JSONLRecord{Data: map[string]interface{}{
		"type":      "assistant",
		"uuid":      uuid,
		"timestamp": ts,
		"sessionId": "s1",
		"message": map[string]interface{}{
			"role":    "assistant",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": text}},
		},
	}}
}

func queueOpRecord(operation, content, ts string) JSONLRecord {
	data := map[string]interface{}{
		"type":      "queue-operation",
		"operation": operation,
		"timestamp": ts,
		"sessionId": "s1",
	}
	if content != "" {
		data["content"] = content
	}
	return JSONLRecord{Data: data}
}

func enqueueRecord(text, ts string) JSONLRecord {
	return queueOpRecord("enqueue", text, ts)
}

// collect all text from a built session's exchanges, with whether it was tagged queued.
func collectMessages(exchanges []Exchange) []struct {
	text   string
	queued bool
} {
	var out []struct {
		text   string
		queued bool
	}
	for _, ex := range exchanges {
		for _, m := range ex.Messages {
			var text string
			for _, c := range m.Content {
				if c.Type == "text" {
					text += c.Text
				}
			}
			q, _ := m.Metadata["queued"].(bool)
			out = append(out, struct {
				text   string
				queued bool
			}{text, q})
		}
	}
	return out
}

func TestQueuedInput_NeverSentIsSurfacedAndTagged(t *testing.T) {
	records := []JSONLRecord{
		userRecord("u1", "start the task", "2026-06-16T06:00:00Z"),
		assistantRecord("a1", "working on it", "2026-06-16T06:00:05Z"),
		enqueueRecord("ffmpeg is a must have!", "2026-06-16T06:00:10Z"),
	}

	exchanges, err := buildExchangesFromRecords(records, "")
	if err != nil {
		t.Fatalf("buildExchangesFromRecords: %v", err)
	}

	msgs := collectMessages(exchanges)
	var found bool
	for _, m := range msgs {
		if m.text == "ffmpeg is a must have!" {
			found = true
			if !m.queued {
				t.Errorf("never-sent queued input should be tagged queued=true")
			}
		}
	}
	if !found {
		t.Errorf("never-sent queued input %q was dropped; messages=%+v", "ffmpeg is a must have!", msgs)
	}
}

func TestQueuedInput_DeliveredIsNotDuplicated(t *testing.T) {
	// User queued "use conda" while busy; it was later sent and appears as a real
	// user message. It must render exactly once, untagged.
	records := []JSONLRecord{
		userRecord("u1", "start", "2026-06-16T06:00:00Z"),
		assistantRecord("a1", "ok", "2026-06-16T06:00:05Z"),
		enqueueRecord("use conda", "2026-06-16T06:00:10Z"),
		userRecord("u2", "use conda", "2026-06-16T06:00:20Z"),
	}

	exchanges, err := buildExchangesFromRecords(records, "")
	if err != nil {
		t.Fatalf("buildExchangesFromRecords: %v", err)
	}

	count := 0
	for _, m := range collectMessages(exchanges) {
		if m.text == "use conda" {
			count++
			if m.queued {
				t.Errorf("delivered input should render as a normal (untagged) user message")
			}
		}
	}
	if count != 1 {
		t.Errorf("delivered queued input rendered %d times, want 1 (no duplication)", count)
	}
}

func TestIsQueuedInputRecord(t *testing.T) {
	tests := []struct {
		name   string
		record JSONLRecord
		want   bool
	}{
		{"enqueue with content", enqueueRecord("ffmpeg is a must have!", "2026-06-16T06:00:10Z"), true},
		{"enqueue empty content", queueOpRecord("enqueue", "", "2026-06-16T06:00:10Z"), false},
		{"dequeue", queueOpRecord("dequeue", "", "2026-06-16T06:00:11Z"), false},
		{"remove", queueOpRecord("remove", "", "2026-06-16T06:00:12Z"), false},
		{"non-queue record", userRecord("u1", "hi", "2026-06-16T06:00:00Z"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQueuedInputRecord(tt.record); got != tt.want {
				t.Errorf("isQueuedInputRecord = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSyntheticQueuedUUID_Deterministic(t *testing.T) {
	r := enqueueRecord("ffmpeg is a must have!", "2026-06-16T06:00:10Z")
	if got1, got2 := syntheticQueuedUUID(r), syntheticQueuedUUID(r); got1 != got2 {
		t.Errorf("synthetic uuid must be deterministic: %q vs %q", got1, got2)
	}
	other := enqueueRecord("different text", "2026-06-16T06:00:10Z")
	if syntheticQueuedUUID(r) == syntheticQueuedUUID(other) {
		t.Error("different content should yield different synthetic uuids")
	}
}
