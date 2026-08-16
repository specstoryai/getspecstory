package claudecode

import (
	"strings"
	"testing"
)

func TestBuildExchanges_RendersAwaySummaryRecap(t *testing.T) {
	records := []JSONLRecord{
		{Data: map[string]interface{}{
			"type": "user", "uuid": "u1", "timestamp": "2026-06-16T04:08:00Z", "sessionId": "s1",
			"message": map[string]interface{}{"role": "user", "content": "hi"},
		}},
		{Data: map[string]interface{}{
			"type": "assistant", "uuid": "a1", "timestamp": "2026-06-16T04:08:05Z", "sessionId": "s1",
			"message": map[string]interface{}{"role": "assistant", "content": []interface{}{
				map[string]interface{}{"type": "text", "text": "ok"},
			}},
		}},
		{Data: map[string]interface{}{
			"type": "system", "subtype": "away_summary", "uuid": "sys1", "parentUuid": "a1",
			"timestamp": "2026-06-16T04:12:00Z", "sessionId": "s1",
			"content": "You are setting up MCPs in Claude Code.",
		}},
		// A non-recap system record must NOT be rendered.
		{Data: map[string]interface{}{
			"type": "system", "subtype": "compact_boundary", "uuid": "sys2", "parentUuid": "sys1",
			"timestamp": "2026-06-16T04:13:00Z", "sessionId": "s1",
		}},
	}

	exchanges, err := buildExchangesFromRecords(records, "")
	if err != nil {
		t.Fatal(err)
	}

	recapCount := 0
	for _, e := range exchanges {
		for _, m := range e.Messages {
			if r, _ := m.Metadata["recap"].(bool); r {
				recapCount++
				if len(m.Content) == 0 || !strings.Contains(m.Content[0].Text, "setting up MCPs") {
					t.Errorf("recap message missing content: %+v", m.Content)
				}
			}
		}
	}
	if recapCount != 1 {
		t.Errorf("expected exactly 1 recap message (away_summary only), got %d", recapCount)
	}
}
