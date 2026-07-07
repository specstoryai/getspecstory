package sessionindex

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func newSession(agent, id, projectID, name, body string) Session {
	return Session{
		ProjectID:   projectID,
		ProjectName: "widgets",
		Agent:       agent,
		SessionID:   id,
		CreatedAt:   "2026-06-18T10:00:00Z",
		UpdatedAt:   "2026-06-18T11:00:00Z",
		UserTurns:   3,
		TotalTurns:  9,
		Slug:        "do-the-thing",
		Name:        name,
		NativePath:  "/store/" + id + ".jsonl",
		OriginCwd:   "/repo",
		Size:        1234,
		Mtime:       1700000000000,
		IndexedAt:   "2026-06-18T12:00:00Z",
		Body:        body,
	}
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestUpsertAndListByProject(t *testing.T) {
	s := openTemp(t)

	if err := s.Upsert(newSession("claude", "c1", "proj-a", "Auth refactor", "rewrote the login flow")); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(newSession("codex", "x1", "proj-a", "Billing migration", "moved billing to stripe")); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(newSession("claude", "c2", "proj-b", "Other project", "unrelated work")); err != nil {
		t.Fatal(err)
	}

	if n, err := s.Count(); err != nil || n != 3 {
		t.Fatalf("Count = %d, %v; want 3", n, err)
	}

	got, err := s.ListByProject("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByProject(proj-a) = %d rows; want 2", len(got))
	}
	for _, sess := range got {
		if sess.ProjectID != "proj-a" {
			t.Errorf("got project_id %q; want proj-a", sess.ProjectID)
		}
		if sess.UserTurns != 3 || sess.TotalTurns != 9 {
			t.Errorf("turn counts not round-tripped: user=%d total=%d", sess.UserTurns, sess.TotalTurns)
		}
	}
}

func TestSearchFTS(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth refactor", "rewrote the login flow with oauth"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-a", "Billing migration", "moved billing to stripe webhooks"))

	// Body match
	hits, err := s.Search("oauth", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "c1" {
		t.Fatalf("Search(oauth) = %+v; want only c1", hits)
	}

	// Name match
	hits, err = s.Search("billing", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "x1" {
		t.Fatalf("Search(billing) = %+v; want only x1", hits)
	}
}

func TestUpsertIsIdempotent(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "First name", "first body text"))
	// Re-index the same (agent, session_id) with changed content.
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Renamed", "second body text"))

	if n, _ := s.Count(); n != 1 {
		t.Fatalf("Count = %d after re-upsert; want 1 (replaced, not duplicated)", n)
	}
	// Old FTS content must be gone; new content searchable.
	if hits, _ := s.Search("first", ""); len(hits) != 0 {
		t.Errorf("stale FTS row survived re-upsert: %+v", hits)
	}
	if hits, _ := s.Search("second", ""); len(hits) != 1 {
		t.Errorf("new FTS content not searchable after re-upsert")
	}
}

// TestUpsertHandlesLegacyNullRowid covers the migration window: rows written before fts_rowid
// existed have it NULL. Both the body read and the replace-before-insert must fall back to the
// by-key path for such a row, and a re-index must repopulate fts_rowid without duplicating or
// orphaning the FTS row.
func TestUpsertHandlesLegacyNullRowid(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Legacy", "old body text"))

	// Simulate a pre-migration row: clear the fts_rowid link the writer just set.
	if _, err := s.db.Exec(`UPDATE sessions SET fts_rowid = NULL WHERE agent = 'claude' AND session_id = 'c1'`); err != nil {
		t.Fatalf("clear fts_rowid: %v", err)
	}

	// Body read must fall back to the by-key lookup and still find the body.
	if body, err := s.SessionBody("claude", "c1"); err != nil || body != "old body text" {
		t.Errorf("SessionBody (NULL fts_rowid) = %q, %v; want fallback to old body", body, err)
	}

	// Re-index the same session: with no fts_rowid to seek, the delete must fall back to the
	// by-key path so the stale FTS row is removed rather than duplicated.
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Repopulated", "new body text"))

	if n, _ := s.Count(); n != 1 {
		t.Fatalf("Count = %d after re-upsert of legacy row; want 1", n)
	}
	if hits, _ := s.Search("old", ""); len(hits) != 0 {
		t.Errorf("stale FTS row survived re-upsert of legacy row: %+v", hits)
	}
	if hits, _ := s.Search("new", ""); len(hits) != 1 {
		t.Errorf("new FTS content not searchable after re-upsert of legacy row")
	}
	// fts_rowid is now repopulated, so the body read takes the fast path again.
	var rowid sql.NullInt64
	if err := s.db.QueryRow(`SELECT fts_rowid FROM sessions WHERE agent = 'claude' AND session_id = 'c1'`).Scan(&rowid); err != nil {
		t.Fatal(err)
	}
	if !rowid.Valid {
		t.Error("fts_rowid still NULL after re-upsert; want repopulated")
	}
	if body, err := s.SessionBody("claude", "c1"); err != nil || body != "new body text" {
		t.Errorf("SessionBody after repopulate = %q, %v; want new body via fast path", body, err)
	}
}

func mustUpsert(t *testing.T, s *Store, sess Session) {
	t.Helper()
	if err := s.Upsert(sess); err != nil {
		t.Fatalf("Upsert(%s): %v", sess.SessionID, err)
	}
}

func TestFingerprintsRoundTrip(t *testing.T) {
	s := openTemp(t)
	a := newSession("claude", "c1", "proj-a", "Auth", "body")
	a.Size, a.Mtime, a.IndexVersion = 111, 222, 3
	mustUpsert(t, s, a)
	mustUpsert(t, s, newSession("codex", "x1", "proj-a", "Bill", "body"))

	fps, err := s.Fingerprints()
	if err != nil {
		t.Fatal(err)
	}
	if len(fps) != 2 {
		t.Fatalf("got %d fingerprints; want 2", len(fps))
	}
	got, ok := fps[FingerprintKey("claude", "c1")]
	if !ok {
		t.Fatal("missing fingerprint for claude/c1")
	}
	if got.Size != 111 || got.Mtime != 222 || got.Version != 3 {
		t.Errorf("fingerprint = %+v; want {111 222 3}", got)
	}
}

func TestProjectAndUnattributedCounts(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "a", "b"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-a", "a", "b")) // same project
	mustUpsert(t, s, newSession("claude", "c2", "proj-b", "a", "b"))
	mustUpsert(t, s, newSession("cursor", "z1", "unknown", "a", "b"))
	mustUpsert(t, s, newSession("cursor", "z2", "unknown", "a", "b"))

	if n, err := s.ProjectCount("unknown"); err != nil || n != 2 {
		t.Errorf("ProjectCount = %d, %v; want 2 (proj-a, proj-b)", n, err)
	}
	if n, err := s.UnattributedCount("unknown"); err != nil || n != 2 {
		t.Errorf("UnattributedCount = %d, %v; want 2", n, err)
	}
}

func TestListProjectsRollup(t *testing.T) {
	s := openTemp(t)
	a := newSession("claude", "c1", "proj-a", "n", "b")
	a.UpdatedAt = "2026-06-18T10:00:00Z"
	mustUpsert(t, s, a)
	b := newSession("codex", "x1", "proj-a", "n", "b")
	b.UpdatedAt = "2026-06-18T12:00:00Z"
	mustUpsert(t, s, b)
	c := newSession("claude", "c2", "proj-b", "n", "b")
	c.UpdatedAt = "2026-06-17T09:00:00Z"
	mustUpsert(t, s, c)

	projs, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projs) != 2 {
		t.Fatalf("got %d projects; want 2", len(projs))
	}
	// proj-a is most recently active (codex at 12:00) → first.
	if projs[0].ProjectID != "proj-a" {
		t.Errorf("expected proj-a first (most recent), got %s", projs[0].ProjectID)
	}
	if projs[0].Sessions != 2 || projs[0].AgentCounts["claude"] != 1 || projs[0].AgentCounts["codex"] != 1 {
		t.Errorf("proj-a rollup wrong: sessions=%d agents=%v", projs[0].Sessions, projs[0].AgentCounts)
	}
	if projs[0].LastActivity != "2026-06-18T12:00:00Z" {
		t.Errorf("proj-a last activity = %q; want the codex timestamp", projs[0].LastActivity)
	}
}

func TestSessionBody(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "name", "the full conversation text"))
	body, err := s.SessionBody("claude", "c1")
	if err != nil || body != "the full conversation text" {
		t.Errorf("SessionBody = %q, %v; want the body", body, err)
	}
	// Missing session → empty, no error.
	if body, err := s.SessionBody("claude", "nope"); err != nil || body != "" {
		t.Errorf("SessionBody(missing) = %q, %v; want empty", body, err)
	}
}

func TestSearchAndSnippets(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth", "we rewrote the login flow with oauth tokens today"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-b", "Bill", "billing moved to stripe"))

	// Scoped to proj-a finds c1; snippets are fetched lazily for the visible rows.
	hits, err := s.Search("oauth*", "proj-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "c1" {
		t.Fatalf("scoped search = %+v; want only c1", hits)
	}
	snips, err := s.Snippets("oauth*", hits)
	if err != nil {
		t.Fatal(err)
	}
	snip := snips[FingerprintKey("claude", "c1")]
	if !strings.Contains(snip, "\x02oauth\x03") {
		t.Errorf("snippet missing marked match: %q", snip)
	}

	// Scoping excludes other projects.
	if hits, _ := s.Search("oauth*", "proj-b"); len(hits) != 0 {
		t.Errorf("proj-b should not match oauth: %+v", hits)
	}
	// Empty projectID searches everything.
	if hits, _ := s.Search("stripe*", ""); len(hits) != 1 || hits[0].SessionID != "x1" {
		t.Errorf("global search for stripe = %+v; want x1", hits)
	}
}

// TestSoftDeleteSessionHidesFromReads verifies a soft-deleted session vanishes from every read
// path (count, project list, search) while its siblings remain, and reports one row affected.
func TestSoftDeleteSessionHidesFromReads(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth refactor", "rewrote the login flow"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-a", "Billing", "moved billing to stripe"))

	affected, err := s.SoftDeleteSession("claude", "c1")
	if err != nil || affected != 1 {
		t.Fatalf("SoftDeleteSession = %d, %v; want 1, nil", affected, err)
	}

	if n, _ := s.Count(); n != 1 {
		t.Errorf("Count = %d after delete; want 1", n)
	}
	got, _ := s.ListByProject("proj-a")
	if len(got) != 1 || got[0].SessionID != "x1" {
		t.Errorf("ListByProject after delete = %+v; want only x1", got)
	}
	if hits, _ := s.Search("login", ""); len(hits) != 0 {
		t.Errorf("deleted session still searchable: %+v", hits)
	}
	if hits, _ := s.Search("stripe", ""); len(hits) != 1 {
		t.Errorf("sibling session no longer searchable after delete")
	}
}

// TestSoftDeleteSurvivesReupsert is the tombstone guard: once soft-deleted, re-indexing the same
// session (even with new content, as reindex/live/sync would) must not resurrect it.
func TestSoftDeleteSurvivesReupsert(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth", "old body"))
	mustSoftDeleteSession(t, s, "claude", "c1")

	// A later write with fresh content — must be a no-op against the tombstone.
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth v2", "brand new body"))

	if n, _ := s.Count(); n != 0 {
		t.Errorf("Count = %d; tombstone was resurrected by re-upsert", n)
	}
	if hits, _ := s.Search("brand", ""); len(hits) != 0 {
		t.Errorf("re-upsert leaked new content into search: %+v", hits)
	}
	fps, _ := s.Fingerprints()
	if fp := fps[FingerprintKey("claude", "c1")]; !fp.Deleted {
		t.Errorf("fingerprint.Deleted = false; want true for tombstone")
	}
}

// TestSoftDeleteProject tombstones every session in a project while leaving other projects intact,
// and confirms a NEW session in the deleted project still indexes (the project isn't blacklisted).
func TestSoftDeleteProject(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "a1", "body"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-a", "a2", "body"))
	mustUpsert(t, s, newSession("claude", "c2", "proj-b", "b1", "body"))

	if _, err := s.SoftDeleteProject("proj-a"); err != nil {
		t.Fatalf("SoftDeleteProject: %v", err)
	}
	if got, _ := s.ListByProject("proj-a"); len(got) != 0 {
		t.Errorf("proj-a not fully hidden: %+v", got)
	}
	if got, _ := s.ListByProject("proj-b"); len(got) != 1 {
		t.Errorf("proj-b affected by proj-a delete: %+v", got)
	}

	// A future session in the deleted project indexes normally — only pre-existing rows are gone.
	mustUpsert(t, s, newSession("claude", "c3", "proj-a", "a3", "fresh work"))
	if got, _ := s.ListByProject("proj-a"); len(got) != 1 || got[0].SessionID != "c3" {
		t.Errorf("new session in deleted project should index: %+v", got)
	}
}

// TestSoftDeleteAffectedCount pins the row-count contract of both soft-delete entry points across
// the combinations that matter: a live target reports how many rows it tombstoned, while an
// already-deleted or never-indexed target is a no-op (0) — so the caller (and its analytics
// "affected" property) can trust the number. Each row runs on a fresh store so state can't leak.
func TestSoftDeleteAffectedCount(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, s *Store)
		del   func(s *Store) (int, error)
		want  int
	}{
		{
			name:  "single live session",
			setup: func(t *testing.T, s *Store) { mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth", "body")) },
			del:   func(s *Store) (int, error) { return s.SoftDeleteSession("claude", "c1") },
			want:  1,
		},
		{
			name: "already-tombstoned session is a no-op",
			setup: func(t *testing.T, s *Store) {
				mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth", "body"))
				mustSoftDeleteSession(t, s, "claude", "c1")
			},
			del:  func(s *Store) (int, error) { return s.SoftDeleteSession("claude", "c1") },
			want: 0,
		},
		{
			name:  "never-indexed session",
			setup: func(t *testing.T, s *Store) {},
			del:   func(s *Store) (int, error) { return s.SoftDeleteSession("claude", "ghost") },
			want:  0,
		},
		{
			name: "whole project with two live sessions",
			setup: func(t *testing.T, s *Store) {
				mustUpsert(t, s, newSession("claude", "c1", "proj-a", "a1", "body"))
				mustUpsert(t, s, newSession("codex", "x1", "proj-a", "a2", "body"))
			},
			del:  func(s *Store) (int, error) { return s.SoftDeleteProject("proj-a") },
			want: 2,
		},
		{
			name: "already-tombstoned project is a no-op",
			setup: func(t *testing.T, s *Store) {
				mustUpsert(t, s, newSession("claude", "c1", "proj-a", "a1", "body"))
				if _, err := s.SoftDeleteProject("proj-a"); err != nil {
					t.Fatal(err)
				}
			},
			del:  func(s *Store) (int, error) { return s.SoftDeleteProject("proj-a") },
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTemp(t)
			tt.setup(t, s)
			got, err := tt.del(s)
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if got != tt.want {
				t.Errorf("affected = %d; want %d", got, tt.want)
			}
		})
	}
}

// TestSoftDeleteUpdatesProjectSummary covers the all-projects browser's rolled-up counts: deleting
// one of a project's sessions must lower ListProjects' per-project (and per-agent) totals, not just
// hide the row from ListByProject. The project itself stays visible because a live session remains.
func TestSoftDeleteUpdatesProjectSummary(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "a1", "body"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-a", "a2", "body"))
	mustSoftDeleteSession(t, s, "claude", "c1")

	projs, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	var proj *ProjectSummary
	for i := range projs {
		if projs[i].ProjectID == "proj-a" {
			proj = &projs[i]
		}
	}
	if proj == nil {
		t.Fatalf("proj-a missing from ListProjects after deleting one of two sessions")
	}
	if proj.Sessions != 1 {
		t.Errorf("summary Sessions = %d after deleting one of two; want 1", proj.Sessions)
	}
	if proj.AgentCounts["claude"] != 0 || proj.AgentCounts["codex"] != 1 {
		t.Errorf("summary AgentCounts = %+v; want only codex=1", proj.AgentCounts)
	}
}

// mustSoftDeleteSession tombstones a session in test setup, failing the test on error.
func mustSoftDeleteSession(t *testing.T, s *Store, agent, sessionID string) {
	t.Helper()
	if _, err := s.SoftDeleteSession(agent, sessionID); err != nil {
		t.Fatalf("SoftDeleteSession(%s, %s): %v", agent, sessionID, err)
	}
}
