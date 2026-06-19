package sessionindex

import (
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
	hits, err := s.SearchWithSnippets("oauth", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "c1" {
		t.Fatalf("Search(oauth) = %+v; want only c1", hits)
	}

	// Name match
	hits, err = s.SearchWithSnippets("billing", "")
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
	if hits, _ := s.SearchWithSnippets("first", ""); len(hits) != 0 {
		t.Errorf("stale FTS row survived re-upsert: %+v", hits)
	}
	if hits, _ := s.SearchWithSnippets("second", ""); len(hits) != 1 {
		t.Errorf("new FTS content not searchable after re-upsert")
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

func TestSearchWithSnippets(t *testing.T) {
	s := openTemp(t)
	mustUpsert(t, s, newSession("claude", "c1", "proj-a", "Auth", "we rewrote the login flow with oauth tokens today"))
	mustUpsert(t, s, newSession("codex", "x1", "proj-b", "Bill", "billing moved to stripe"))

	// Scoped to proj-a finds c1 with a snippet marking the match.
	hits, err := s.SearchWithSnippets("oauth*", "proj-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "c1" {
		t.Fatalf("scoped search = %+v; want only c1", hits)
	}
	if !strings.Contains(hits[0].Snippet, "\x02oauth\x03") {
		t.Errorf("snippet missing marked match: %q", hits[0].Snippet)
	}

	// Scoping excludes other projects.
	if hits, _ := s.SearchWithSnippets("oauth*", "proj-b"); len(hits) != 0 {
		t.Errorf("proj-b should not match oauth: %+v", hits)
	}
	// Empty projectID searches everything.
	if hits, _ := s.SearchWithSnippets("stripe*", ""); len(hits) != 1 || hits[0].SessionID != "x1" {
		t.Errorf("global search for stripe = %+v; want x1", hits)
	}
}
