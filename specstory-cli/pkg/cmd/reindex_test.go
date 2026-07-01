package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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

// TestDedupRefs verifies the collapse done before indexing: a session enumerated more than
// once (same agent+id) keeps the freshest native file by mtime, sessions with a blank id are
// dropped, a failed provider (nil) is skipped entirely, and foundPerAgent counts distinct
// sessions. statNative reads real files, so the duplicates point at temp files with set mtimes.
func TestDedupRefs(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "older.jsonl")
	newer := filepath.Join(dir, "newer.jsonl")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldT := time.Unix(1_000_000, 0)
	newT := time.Unix(2_000_000, 0)
	if err := os.Chtimes(older, oldT, oldT); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, newT, newT); err != nil {
		t.Fatal(err)
	}

	ids := []string{"claude", "codex"}
	provs := []spi.Provider{&fakeProvider{}, nil} // codex failed to load → its refs must be skipped
	perProvider := [][]spi.GlobalSessionRef{
		{
			{SessionID: "s1", NativePath: older},
			{SessionID: "s1", NativePath: newer}, // same session, fresher file → wins
			{SessionID: "  ", NativePath: older}, // blank id → dropped
			{SessionID: "s2", NativePath: newer},
		},
		{{SessionID: "x1", NativePath: newer}}, // belongs to the nil provider → skipped
	}

	best, order, found := dedupRefs(ids, provs, perProvider)

	s1 := sessionindex.FingerprintKey("claude", "s1")
	if best[s1].ref.NativePath != newer {
		t.Errorf("dedup kept %q for s1, want the fresher file %q", best[s1].ref.NativePath, newer)
	}
	if len(order) != 2 {
		t.Fatalf("order = %v, want exactly s1 and s2 (blank + nil-provider dropped)", order)
	}
	if found["claude"] != 2 || found["codex"] != 0 {
		t.Errorf("foundPerAgent = %v, want claude:2 codex:0", found)
	}
}

// TestSelectWork covers the incremental skip: a session is reindexed only when its native
// file's size or mtime changed, or the logic version was bumped; a session with no prior
// fingerprint is flagged isNew (so the writer can skip the FTS delete) unless --force, where
// the fingerprint set is empty and absence proves nothing.
func TestSelectWork(t *testing.T) {
	const cwd = "/work/proj"
	key := sessionindex.FingerprintKey("claude", "s1")
	best := map[string]reindexItem{
		key: {agent: "claude", ref: spi.GlobalSessionRef{SessionID: "s1", OriginCwd: cwd}, size: 100, mtime: 5000},
	}
	order := []string{key}

	tests := []struct {
		name          string
		fingerprints  map[string]sessionindex.Fingerprint
		force         bool
		wantWork      int
		wantUnchanged int
		wantIsNew     bool // checked only when wantWork == 1
	}{
		{
			name:          "unchanged: same size+mtime+version is skipped",
			fingerprints:  map[string]sessionindex.Fingerprint{key: {Size: 100, Mtime: 5000, Version: reindexVersion}},
			wantWork:      0,
			wantUnchanged: 1,
		},
		{
			name:         "changed mtime forces reindex, not new",
			fingerprints: map[string]sessionindex.Fingerprint{key: {Size: 100, Mtime: 9999, Version: reindexVersion}},
			wantWork:     1,
			wantIsNew:    false,
		},
		{
			name:         "version bump forces reindex even when the file is unchanged",
			fingerprints: map[string]sessionindex.Fingerprint{key: {Size: 100, Mtime: 5000, Version: reindexVersion - 1}},
			wantWork:     1,
			wantIsNew:    false,
		},
		{
			name:         "new session (no fingerprint) is flagged isNew",
			fingerprints: map[string]sessionindex.Fingerprint{},
			wantWork:     1,
			wantIsNew:    true,
		},
		{
			name:         "force: empty fingerprints, not flagged new so deletes are kept",
			fingerprints: map[string]sessionindex.Fingerprint{},
			force:        true,
			wantWork:     1,
			wantIsNew:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &projectIDCache{m: map[string]projectIDName{}}
			work, totals, unchanged := selectWork(order, best, tt.fingerprints, "", tt.force, cache)
			if len(work) != tt.wantWork {
				t.Fatalf("work = %d, want %d", len(work), tt.wantWork)
			}
			if unchanged != tt.wantUnchanged {
				t.Errorf("unchanged = %d, want %d", unchanged, tt.wantUnchanged)
			}
			if tt.wantWork == 1 {
				if work[0].isNew != tt.wantIsNew {
					t.Errorf("isNew = %v, want %v", work[0].isNew, tt.wantIsNew)
				}
				if totals["claude"] != 1 {
					t.Errorf("totals[claude] = %d, want 1", totals["claude"])
				}
			}
		})
	}
}

// TestSelectWorkProjectFilter verifies the background warm's current-project pass: only
// sessions whose origin cwd resolves to the requested project id are kept. The cache is
// pre-seeded so resolution is deterministic and touches no real paths.
func TestSelectWorkProjectFilter(t *testing.T) {
	k1 := sessionindex.FingerprintKey("claude", "s1")
	k2 := sessionindex.FingerprintKey("claude", "s2")
	best := map[string]reindexItem{
		k1: {agent: "claude", ref: spi.GlobalSessionRef{SessionID: "s1", OriginCwd: "/work/a"}, size: 1, mtime: 1},
		k2: {agent: "claude", ref: spi.GlobalSessionRef{SessionID: "s2", OriginCwd: "/work/b"}, size: 1, mtime: 1},
	}
	order := []string{k1, k2}
	cache := &projectIDCache{m: map[string]projectIDName{
		"/work/a": {id: "proj-a", name: "a"},
		"/work/b": {id: "proj-b", name: "b"},
	}}

	work, _, _ := selectWork(order, best, map[string]sessionindex.Fingerprint{}, "proj-a", false, cache)
	if len(work) != 1 || work[0].ref.SessionID != "s1" {
		t.Fatalf("project filter kept %d items, want only s1: %+v", len(work), work)
	}
}

// TestLiveIndexerRecordConcurrent exercises the mutex that serializes concurrent provider
// watchers: many goroutines record distinct sessions at once, and each must land exactly one
// row with no data race on lastSeen or the writer. Run with -race to catch a dropped lock.
func TestLiveIndexerRecordConcurrent(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = store.Close() }()

	li := &LiveIndexer{
		store:       store,
		cwd:         "/work/proj",
		projectID:   "proj-1",
		projectName: "proj",
		indexedAt:   "2026-06-25T00:00:00Z",
		lastSeen:    map[string]string{},
	}

	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("sess-%d", i)
			li.Record("claude", &spi.AgentChatSession{
				SessionID: sid,
				CreatedAt: "2026-06-25T00:00:00Z",
				SessionData: &schema.SessionData{
					Exchanges: []schema.Exchange{{Messages: []schema.Message{
						userMsg("2026-06-25T00:01:00Z", "hi "+sid),
					}}},
				},
			})
		}(i)
	}
	wg.Wait()

	rows, err := store.ListByProject("proj-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != n {
		t.Errorf("got %d rows, want %d (one per distinct session)", len(rows), n)
	}
}

// TestProcessWorkReturnsOnWriteError guards the pipeline against a deadlock when the store
// write fails: a closed store makes every UpsertBatch fail, so the writer goroutine exits on
// the first flush. With more queued items than the sessionsCh buffer (256) and the write batch
// (200), the workers would block forever on the full, no-longer-drained buffer unless they tear
// down on writerDone. processWork must instead return the write error promptly.
func TestProcessWorkReturnsOnWriteError(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	_ = store.Close() // a closed store fails every UpsertBatch

	// Empty OriginCwd keeps buildSession metadata-only (no provider/file access). 1000 items
	// overflow both the 256-slot buffer and the 200-row write batch, so a flush fires mid-stream.
	work := make([]reindexItem, 1000)
	for i := range work {
		work[i] = reindexItem{
			agent: "claude",
			ref:   spi.GlobalSessionRef{SessionID: fmt.Sprintf("s%d", i)},
		}
	}

	cache := &projectIDCache{m: map[string]projectIDName{}}
	done := make(chan error, 1)
	go func() {
		done <- processWork(context.Background(), store, work, cache, "2026-01-01T00:00:00Z", 4, nopReporter{})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a write error from the closed store, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("processWork deadlocked on write error instead of returning it")
	}
}
