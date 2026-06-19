package cmd

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func userMsg(ts, text string) schema.Message {
	return schema.Message{Role: schema.RoleUser, Timestamp: ts,
		Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}}}
}
func agentMsg(ts, text string) schema.Message {
	return schema.Message{Role: schema.RoleAgent, Timestamp: ts,
		Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}}}
}

func sampleData() *schema.SessionData {
	return &schema.SessionData{
		Exchanges: []schema.Exchange{
			{Messages: []schema.Message{userMsg("2026-06-18T10:00:00Z", "fix the bug"), agentMsg("2026-06-18T10:01:00Z", "on it")}},
			{Messages: []schema.Message{userMsg("2026-06-18T10:05:00Z", "now ship it"), agentMsg("2026-06-18T10:06:00Z", "done")}},
		},
	}
}

func TestCountTurns(t *testing.T) {
	user, total := countTurns(sampleData())
	if user != 2 {
		t.Errorf("user turns = %d; want 2", user)
	}
	if total != 4 {
		t.Errorf("total turns = %d; want 4", total)
	}
}

func TestLastTimestamp(t *testing.T) {
	if got := lastTimestamp(sampleData()); got != "2026-06-18T10:06:00Z" {
		t.Errorf("lastTimestamp = %q; want the final message ts", got)
	}
	// Empty when no messages carry a timestamp.
	noTS := &schema.SessionData{Exchanges: []schema.Exchange{{Messages: []schema.Message{userMsg("", "hi")}}}}
	if got := lastTimestamp(noTS); got != "" {
		t.Errorf("lastTimestamp with no timestamps = %q; want empty", got)
	}
}

func TestFlattenBody(t *testing.T) {
	body := flattenBody(sampleData())
	for _, want := range []string{"fix the bug", "on it", "now ship it", "done"} {
		if !strings.Contains(body, want) {
			t.Errorf("flattened body missing %q; got %q", want, body)
		}
	}
}

func TestProjectIDCacheUnknownForEmptyCwd(t *testing.T) {
	c := &projectIDCache{m: map[string]projectIDName{}}
	id, name := c.resolve("")
	if id != unknownProjectID || name != "" {
		t.Errorf("resolve(\"\") = (%q,%q); want (unknown, \"\")", id, name)
	}
}

func TestProjectIDCacheMemoizes(t *testing.T) {
	c := &projectIDCache{m: map[string]projectIDName{}}
	// Pre-seed the cache and confirm resolve returns it without recomputing.
	c.m["/some/repo"] = projectIDName{id: "abcd-1234", name: "repo"}
	id, name := c.resolve("/some/repo")
	if id != "abcd-1234" || name != "repo" {
		t.Errorf("cached resolve = (%q,%q); want the seeded value", id, name)
	}
}

func TestProgressBar(t *testing.T) {
	cases := []struct {
		done, total int64
		wantFull    int // number of █ expected
	}{
		{0, 10, 0},
		{10, 10, 14},
		{5, 10, 7},
		{1, 0, 0},    // zero total → no divide-by-zero, empty bar
		{20, 10, 14}, // clamped, never overflows
	}
	for _, tc := range cases {
		got := progressBar(tc.done, tc.total)
		if full := strings.Count(got, "█"); full != tc.wantFull {
			t.Errorf("progressBar(%d,%d) had %d full cells; want %d (%q)", tc.done, tc.total, full, tc.wantFull, got)
		}
	}
}

func TestSummarizeCounts(t *testing.T) {
	ids := []string{"claude", "codex", "cursor"}
	got := summarizeCounts(ids, map[string]int{"claude": 683, "codex": 0, "cursor": 49})
	if got != "claude 683 · cursor 49" {
		t.Errorf("summarizeCounts = %q; want zeros omitted, registry order", got)
	}
}
