package cmd

import (
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

// TestMergeCloudProjects verifies the all-projects session-level rollup:
//   - cloud-only projects get IsCloud=true with real per-agent chips + real activity date;
//   - shared projects keep IsCloud=false but gain cloud-only sessions in their counts, with
//     overlapping (agent, session_id) sessions NOT double-counted (local preferred);
//   - sessions whose agent can't be resolved to a provider id are skipped;
//   - a nil localKeys set degrades to counting all cloud sessions in shared projects;
//   - the union is re-sorted by recency (LastActivity desc).
func TestMergeCloudProjects(t *testing.T) {
	agentIDByName := map[string]string{
		"Claude Code": "claude",
		"Codex":       "codex",
	}

	// Local rollup: project "shared" already has 1 claude session + 1 codex session.
	local := []sessionindex.ProjectSummary{
		{
			ProjectID:    "shared",
			ProjectName:  "shared",
			Sessions:     2,
			LastActivity: "2026-06-18T10:00:00Z",
			AgentCounts:  map[string]int{"claude": 1, "codex": 1},
		},
	}

	// Local per-project session keys: "shared" has claude/aaa + codex/bbb (the overlap set).
	localKeys := map[string][]sessionindex.ProjectSessionKey{
		"shared": {
			{Agent: "claude", SessionID: "aaa"},
			{Agent: "codex", SessionID: "bbb"},
		},
	}

	cloudProjs := []cloud.CloudProject{
		{
			ID:   "shared",
			Name: "shared",
			Sessions: []cloud.CloudSession{
				// aaa/claude overlaps local -> must NOT double-count.
				{ClientID: "aaa", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Claude Code"},
					EndedAt: "2026-06-20T10:00:00Z"},
				// ccc/codex is cloud-only -> counts, and is newer than local's LastActivity.
				{ClientID: "ccc", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
					EndedAt: "2026-06-20T10:00:00Z"},
				// ddd/unknown-agent -> skipped.
				{ClientID: "ddd", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Wat?"},
					EndedAt: "2026-07-01T10:00:00Z"},
			},
		},
		{
			ID:   "cloudonly",
			Name: "cloudonly",
			Sessions: []cloud.CloudSession{
				{ClientID: "xxx", ProjectID: "cloudonly", Metadata: cloud.CloudSessionMetadata{AgentName: "Claude Code"},
					EndedAt: "2026-06-10T10:00:00Z"},
				{ClientID: "yyy", ProjectID: "cloudonly", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
					StartedAt: "2026-06-12T10:00:00Z"}, // no EndedAt -> falls back to StartedAt
			},
		},
		{
			ID:       "empty",
			Name:     "empty",
			Sessions: []cloud.CloudSession{}, // no resolvable sessions -> dropped entirely
		},
	}

	got := mergeCloudProjects(local, localKeys, cloudProjs, agentIDByName)

	byID := map[string]sessionindex.ProjectSummary{}
	for _, p := range got {
		byID[p.ProjectID] = p
	}

	shared, ok := byID["shared"]
	if !ok {
		t.Fatalf("shared project missing from merged result: %+v", got)
	}
	if shared.IsCloud {
		t.Errorf("shared.IsCloud = true, want false (it has local sessions)")
	}
	// 2 local + 1 cloud-only (ccc); aaa overlaps, ddd unknown -> 3 total.
	if shared.Sessions != 3 {
		t.Errorf("shared.Sessions = %d, want 3 (2 local + 1 cloud-only, overlap not double-counted)", shared.Sessions)
	}
	if shared.AgentCounts["claude"] != 1 {
		t.Errorf("shared.AgentCounts[claude] = %d, want 1 (overlap not double-counted)", shared.AgentCounts["claude"])
	}
	if shared.AgentCounts["codex"] != 2 {
		t.Errorf("shared.AgentCounts[codex] = %d, want 2 (1 local + 1 cloud-only)", shared.AgentCounts["codex"])
	}
	// ccc's EndedAt (2026-06-20) is newer than local's (2026-06-18) -> LastActivity advances.
	if shared.LastActivity != "2026-06-20T10:00:00Z" {
		t.Errorf("shared.LastActivity = %q, want 2026-06-20T10:00:00Z (max of union activity)", shared.LastActivity)
	}

	co, ok := byID["cloudonly"]
	if !ok {
		t.Fatalf("cloudonly project missing from merged result: %+v", got)
	}
	if !co.IsCloud {
		t.Errorf("cloudonly.IsCloud = false, want true (no local sessions)")
	}
	if co.Sessions != 2 {
		t.Errorf("cloudonly.Sessions = %d, want 2", co.Sessions)
	}
	if co.AgentCounts["claude"] != 1 || co.AgentCounts["codex"] != 1 {
		t.Errorf("cloudonly.AgentCounts = %v, want claude=1 codex=1", co.AgentCounts)
	}
	// max of xxx EndedAt (06-10) and yyy StartedAt (06-12) -> 06-12.
	if co.LastActivity != "2026-06-12T10:00:00Z" {
		t.Errorf("cloudonly.LastActivity = %q, want 2026-06-12T10:00:00Z (fallback to StartedAt, max)", co.LastActivity)
	}

	if _, ok := byID["empty"]; ok {
		t.Errorf("empty project should be dropped (no resolvable sessions), but it's in the result")
	}

	// Recency sort: shared (06-20) > cloudonly (06-12).
	if len(got) != 2 || got[0].ProjectID != "shared" || got[1].ProjectID != "cloudonly" {
		t.Errorf("merged not sorted by recency desc: %+v", got)
	}
}

// TestMergeCloudProjects_NilLocalKeys verifies the degrade path: when the local session-key
// fetch failed (nil), shared projects count ALL cloud sessions (over-counting overlaps) rather
// than dropping them — a logged, best-effort fallback that still surfaces cloud-only sessions.
func TestMergeCloudProjects_NilLocalKeys(t *testing.T) {
	agentIDByName := map[string]string{"Claude Code": "claude"}
	local := []sessionindex.ProjectSummary{
		{ProjectID: "shared", ProjectName: "shared", Sessions: 1, LastActivity: "2026-06-18T10:00:00Z",
			AgentCounts: map[string]int{"claude": 1}},
	}
	cloudProjs := []cloud.CloudProject{
		{ID: "shared", Name: "shared", Sessions: []cloud.CloudSession{
			// aaa overlaps local, but with nil localKeys we can't know -> counted.
			{ClientID: "aaa", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Claude Code"},
				EndedAt: "2026-06-20T10:00:00Z"},
		}},
	}

	got := mergeCloudProjects(local, nil, cloudProjs, agentIDByName)
	byID := map[string]sessionindex.ProjectSummary{}
	for _, p := range got {
		byID[p.ProjectID] = p
	}
	shared := byID["shared"]
	// 1 local + 1 cloud (overlap not subtracted because localKeys is nil).
	if shared.Sessions != 2 {
		t.Errorf("shared.Sessions = %d, want 2 (degrade: overlap not subtracted on nil localKeys)", shared.Sessions)
	}
	if shared.AgentCounts["claude"] != 2 {
		t.Errorf("shared.AgentCounts[claude] = %d, want 2 (degrade over-count)", shared.AgentCounts["claude"])
	}
}

// TestMergeCloudProjects_AgentResolution ensures a project whose every session has an unknown
// agent is dropped (no phantom cloud project with zero resolvable sessions).
func TestMergeCloudProjects_AgentResolution(t *testing.T) {
	local := []sessionindex.ProjectSummary{}
	cloudProjs := []cloud.CloudProject{
		{ID: "allunknown", Name: "allunknown", Sessions: []cloud.CloudSession{
			{ClientID: "zzz", ProjectID: "allunknown", Metadata: cloud.CloudSessionMetadata{AgentName: "Mystery"}},
		}},
	}
	got := mergeCloudProjects(local, nil, cloudProjs, map[string]string{})
	if len(got) != 0 {
		t.Errorf("expected empty result when all cloud sessions have unknown agents, got %+v", got)
	}
}

// TestMergeCloudProjects_DoesNotMutateLocal confirms the caller's local slice / AgentCounts maps
// are not mutated by the in-place augmentation of shared projects (the function clones counts).
func TestMergeCloudProjects_DoesNotMutateLocal(t *testing.T) {
	agentIDByName := map[string]string{"Claude Code": "claude"}
	local := []sessionindex.ProjectSummary{
		{ProjectID: "shared", Sessions: 1, LastActivity: "2026-06-18T10:00:00Z",
			AgentCounts: map[string]int{"claude": 1}},
	}
	localCountsBefore := local[0].AgentCounts["claude"]
	cloudProjs := []cloud.CloudProject{
		{ID: "shared", Sessions: []cloud.CloudSession{
			{ClientID: "ccc", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Claude Code"},
				EndedAt: "2026-06-20T10:00:00Z"},
		}},
	}
	_ = mergeCloudProjects(local, map[string][]sessionindex.ProjectSessionKey{}, cloudProjs, agentIDByName)
	if local[0].AgentCounts["claude"] != localCountsBefore {
		t.Errorf("mergeCloudProjects mutated the caller's local AgentCounts map: got %d, want %d",
			local[0].AgentCounts["claude"], localCountsBefore)
	}
	if local[0].Sessions != 1 {
		t.Errorf("mergeCloudProjects mutated the caller's local Sessions: got %d, want 1", local[0].Sessions)
	}
}

// TestMergeCloudProjects_SyncTimeDoesNotPoisonActivity is the regression test for the "now" date
// bug (Chunk 4): a cloud-only project whose newest real activity is mid-June but which has a
// legacy/re-synced session with no ended_at/started_at (only updated_at = sync time = today) must
// show the mid-June date, NOT today. updated_at is the cloud record's sync timestamp (reset to
// ~now() on every push), so it must never win the activity max when any session has real activity.
func TestMergeCloudProjects_SyncTimeDoesNotPoisonActivity(t *testing.T) {
	agentIDByName := map[string]string{"Claude Code": "claude", "Codex": "codex"}
	cloudProjs := []cloud.CloudProject{
		{ID: "cp1", Name: "cross-portable-1", Sessions: []cloud.CloudSession{
			// Real activity mid-June.
			{ClientID: "a1", ProjectID: "cp1", Metadata: cloud.CloudSessionMetadata{AgentName: "Claude Code"},
				EndedAt: "2026-06-17T14:59:06.469+00:00", UpdatedAt: "2026-07-13T15:55:45+00:00"},
			// No real activity — only sync time (today). Must NOT win.
			{ClientID: "c1", ProjectID: "cp1", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
				UpdatedAt: "2026-07-13T15:56:01+00:00"},
		}},
	}
	got := mergeCloudProjects(nil, nil, cloudProjs, agentIDByName)
	if len(got) != 1 {
		t.Fatalf("expected 1 project, got %d: %+v", len(got), got)
	}
	p := got[0]
	if p.LastActivity != "2026-06-17T14:59:06.469+00:00" {
		t.Errorf("LastActivity = %q, want 2026-06-17T14:59:06.469+00:00 (real activity, not sync time)", p.LastActivity)
	}
	// Both sessions still count toward chips/total — only the ACTIVITY max ignores sync time.
	if p.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2 (both sessions count even though one has no real activity)", p.Sessions)
	}
	if p.AgentCounts["claude"] != 1 || p.AgentCounts["codex"] != 1 {
		t.Errorf("AgentCounts = %v, want claude=1 codex=1", p.AgentCounts)
	}
}

// TestMergeCloudProjects_AllSyncTimeFallback confirms that when NO session in a cloud-only project
// has real activity (all legacy / duration-worker-not-run), the rollup falls back to the max sync
// time so the project still sorts somewhere and shows a date rather than rendering empty.
func TestMergeCloudProjects_AllSyncTimeFallback(t *testing.T) {
	agentIDByName := map[string]string{"Codex": "codex"}
	cloudProjs := []cloud.CloudProject{
		{ID: "legacy", Name: "legacy", Sessions: []cloud.CloudSession{
			{ClientID: "c1", ProjectID: "legacy", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
				UpdatedAt: "2026-07-13T15:56:01+00:00"},
			{ClientID: "c2", ProjectID: "legacy", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
				UpdatedAt: "2026-07-13T15:58:00+00:00"},
		}},
	}
	got := mergeCloudProjects(nil, nil, cloudProjs, agentIDByName)
	if len(got) != 1 {
		t.Fatalf("expected 1 project, got %d", len(got))
	}
	if got[0].LastActivity != "2026-07-13T15:58:00+00:00" {
		t.Errorf("LastActivity = %q, want max sync time as fallback when no real activity exists", got[0].LastActivity)
	}
}

// TestMergeCloudProjects_SharedProjectSyncTimeIgnored confirms a shared project's local LastActivity
// (already real) is NOT advanced by a cloud session's sync time — only by real cloud activity.
func TestMergeCloudProjects_SharedProjectSyncTimeIgnored(t *testing.T) {
	agentIDByName := map[string]string{"Codex": "codex"}
	local := []sessionindex.ProjectSummary{
		{ProjectID: "shared", Sessions: 1, LastActivity: "2026-06-18T10:00:00Z",
			AgentCounts: map[string]int{"codex": 1}},
	}
	localKeys := map[string][]sessionindex.ProjectSessionKey{
		"shared": {{Agent: "codex", SessionID: "local1"}},
	}
	cloudProjs := []cloud.CloudProject{
		{ID: "shared", Name: "shared", Sessions: []cloud.CloudSession{
			// Cloud-only session with NO real activity, only sync time (today). Must add to counts but
			// must NOT advance LastActivity past the local real activity (Jun 18).
			{ClientID: "cloud1", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
				UpdatedAt: "2026-07-13T15:56:01+00:00"},
		}},
	}
	got := mergeCloudProjects(local, localKeys, cloudProjs, agentIDByName)
	byID := map[string]sessionindex.ProjectSummary{}
	for _, p := range got {
		byID[p.ProjectID] = p
	}
	shared := byID["shared"]
	if shared.Sessions != 2 {
		t.Errorf("Sessions = %d, want 2 (local + cloud-only)", shared.Sessions)
	}
	if shared.LastActivity != "2026-06-18T10:00:00Z" {
		t.Errorf("LastActivity = %q, want 2026-06-18T10:00:00Z (sync time must not advance a shared project's real local activity)", shared.LastActivity)
	}
}

// TestMergeCloudProjects_MixedTimestampFormats verifies the LastActivity max compares timestamps
// as parsed RFC3339 instants, not strings: a local LastActivity with a non-UTC offset
// ("2026-07-13T18:00:00-05:00" = 23:00Z) must not be regressed by a cloud session whose real
// activity is byte-wise larger but chronologically earlier ("2026-07-13T20:00:00+00:00" = 20:00Z).
func TestMergeCloudProjects_MixedTimestampFormats(t *testing.T) {
	agentIDByName := map[string]string{"Codex": "codex"}
	local := []sessionindex.ProjectSummary{
		{ProjectID: "shared", Sessions: 1, LastActivity: "2026-07-13T18:00:00-05:00",
			AgentCounts: map[string]int{"codex": 1}},
	}
	localKeys := map[string][]sessionindex.ProjectSessionKey{
		"shared": {{Agent: "codex", SessionID: "local1"}},
	}
	cloudProjs := []cloud.CloudProject{
		{ID: "shared", Name: "shared", Sessions: []cloud.CloudSession{
			{ClientID: "cloud1", ProjectID: "shared", Metadata: cloud.CloudSessionMetadata{AgentName: "Codex"},
				EndedAt: "2026-07-13T20:00:00+00:00"},
		}},
	}
	got := mergeCloudProjects(local, localKeys, cloudProjs, agentIDByName)
	if len(got) != 1 {
		t.Fatalf("expected 1 project, got %d: %+v", len(got), got)
	}
	if got[0].LastActivity != "2026-07-13T18:00:00-05:00" {
		t.Errorf("LastActivity = %q, want the local -05:00 value (parsed comparison, not string order)",
			got[0].LastActivity)
	}
}

// TestLaterRFC3339 covers the cross-source timestamp max: parsed comparison across offsets and
// precisions, empty/unparseable values never beating parseable ones.
func TestLaterRFC3339(t *testing.T) {
	cases := []struct{ name, a, b, want string }{
		{"non-UTC offset beats byte order", "2026-07-13T18:00:00-05:00", "2026-07-13T20:00:00+00:00", "2026-07-13T18:00:00-05:00"},
		{"fractional seconds win within a second", "2026-06-01T00:00:00.500Z", "2026-06-01T00:00:00Z", "2026-06-01T00:00:00.500Z"},
		{"empty never wins", "2026-06-01T00:00:00Z", "", "2026-06-01T00:00:00Z"},
		{"empty first operand", "", "2026-06-01T00:00:00Z", "2026-06-01T00:00:00Z"},
		{"unparseable loses to parseable", "garbage", "2026-06-01T00:00:00Z", "2026-06-01T00:00:00Z"},
		{"both unparseable falls back to string order", "abc", "abd", "abd"},
	}
	for _, c := range cases {
		if got := laterRFC3339(c.a, c.b); got != c.want {
			t.Errorf("%s: laterRFC3339(%q, %q) = %q, want %q", c.name, c.a, c.b, got, c.want)
		}
	}
}

// TestProjectActivityTime confirms RFC3339 parsing for the recency sort (local "…Z" and cloud
// "…+00:00" with microseconds compare as equal instants, not misordered by string compare).
func TestProjectActivityTime(t *testing.T) {
	z := projectActivityTime(sessionindex.ProjectSummary{LastActivity: "2026-06-20T10:00:00Z"})
	offset := projectActivityTime(sessionindex.ProjectSummary{LastActivity: "2026-06-20T10:00:00.000+00:00"})
	if !z.Equal(offset) {
		t.Errorf("RFC3339 instants not equal: Z=%v offset=%v", z, offset)
	}
	if projectActivityTime(sessionindex.ProjectSummary{LastActivity: "garbage"}) != (time.Time{}) {
		t.Errorf("unparseable LastActivity should yield zero time (sorts last)")
	}
}
