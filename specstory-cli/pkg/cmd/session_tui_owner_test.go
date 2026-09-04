package cmd

import (
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

const viewer = "eric@specstory.com"

// TestOwnerLabel pins the conservative attribution rule.
//
// A team-shared project's listings union your own rows with teammates' copies, so a row has to
// say when it is not yours — but only when that is POSITIVELY known. Each silent case below is
// indistinguishable from "this row is yours", and labelling your own session with a colleague's
// name is a worse failure than showing nothing.
//
// This must stay in step with ownerLabel() in the VS Code extension
// (src/webview/lib/format.ts): the same session should read the same way in both.
func TestOwnerLabel(t *testing.T) {
	tests := []struct {
		name                           string
		ownerID, ownerName, ownerEmail string
		viewerEmail                    string
		want                           string
	}{
		{
			name:        "names the teammate who owns the row",
			ownerID:     "user_greg",
			ownerName:   "Greg",
			ownerEmail:  "greg@specstory.com",
			viewerEmail: viewer,
			want:        "Greg",
		},
		{
			name:        "falls back to the email when the profile has no name",
			ownerID:     "user_greg",
			ownerEmail:  "greg@specstory.com",
			viewerEmail: viewer,
			want:        "greg@specstory.com",
		},
		{
			name:        "a whitespace-only name is not a name",
			ownerID:     "user_greg",
			ownerName:   "   ",
			ownerEmail:  "greg@specstory.com",
			viewerEmail: viewer,
			want:        "greg@specstory.com",
		},
		{
			name:        "credits nobody for your own row",
			ownerID:     "user_me",
			ownerName:   "Eric",
			ownerEmail:  viewer,
			viewerEmail: viewer,
			want:        "",
		},
		{
			name:        "matches your own row regardless of email casing",
			ownerID:     "user_me",
			ownerName:   "Eric",
			ownerEmail:  "Eric@SpecStory.com",
			viewerEmail: viewer,
			want:        "",
		},
		{
			// An older cloud deployment: the fields are simply not on the wire.
			name:        "silent when the cloud sends no attribution",
			viewerEmail: viewer,
			want:        "",
		},
		{
			// ownerId known, but the Clerk webhook has not backfilled a profile, so there is
			// nothing to compare against the viewer.
			name:        "silent when the owner has no cached profile",
			ownerID:     "user_ghost",
			viewerEmail: viewer,
			want:        "",
		},
		{
			// The dangerous shape: a name arrives without an email, so the row LOOKS
			// attributable but cannot be compared against the viewer. Returning the name here
			// is how you end up telling someone their own session belongs to a colleague.
			name:        "silent when a name arrives without an email to compare",
			ownerID:     "user_unknown",
			ownerName:   "Eric",
			viewerEmail: viewer,
			want:        "",
		},
		{
			// Not signed in, or auth.json unreadable — nothing is comparable.
			name:       "silent when the viewer is unknown",
			ownerID:    "user_greg",
			ownerName:  "Greg",
			ownerEmail: "greg@specstory.com",
			want:       "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ownerLabel(tc.ownerID, tc.ownerName, tc.ownerEmail, tc.viewerEmail)
			if got != tc.want {
				t.Errorf("ownerLabel(%q, %q, %q, %q) = %q, want %q",
					tc.ownerID, tc.ownerName, tc.ownerEmail, tc.viewerEmail, got, tc.want)
			}
		})
	}
}

// TestCloudToSessionsCarriesOwner verifies the browse listing's converter keeps attribution on
// the row. The fields are carried RAW rather than pre-formatted, so the "is this mine?"
// comparison happens once at render against the signed-in user.
func TestCloudToSessionsCarriesOwner(t *testing.T) {
	agentIDByName := map[string]string{"Claude Code": "claude"}

	got := cloudToSessions([]cloud.CloudSession{
		{
			ClientID:   "aaa",
			ProjectID:  "p1",
			Name:       "n",
			Metadata:   cloud.CloudSessionMetadata{AgentName: "Claude Code"},
			OwnerID:    "user_greg",
			OwnerName:  "Greg",
			OwnerEmail: "greg@specstory.com",
		},
	}, agentIDByName)

	if len(got) != 1 {
		t.Fatalf("expected 1 session, got %d", len(got))
	}
	if got[0].OwnerID != "user_greg" || got[0].OwnerName != "Greg" ||
		got[0].OwnerEmail != "greg@specstory.com" {
		t.Errorf("owner not carried through: %+v", got[0])
	}
	if ownerLabel(got[0].OwnerID, got[0].OwnerName, got[0].OwnerEmail, viewer) != "Greg" {
		t.Error("converted row should attribute to Greg for a different viewer")
	}
}

// TestCloudSearchHitsCarryOwner does the same for the search path. Search collapses every
// teammate's copy of a conversation into ONE hit, so which owner survives decides whose blob a
// resume from that hit pulls — the label has to describe that copy, not an arbitrary one.
func TestCloudSearchHitsCarryOwner(t *testing.T) {
	agentIDByName := map[string]string{"Claude Code": "claude"}

	sessions, _ := cloudSearchHitsToSessions([]cloud.CloudSearchHit{
		{
			CloudSession: cloud.CloudSession{
				ClientID:   "aaa",
				ProjectID:  "p1",
				Metadata:   cloud.CloudSessionMetadata{AgentName: "Claude Code"},
				OwnerID:    "user_greg",
				OwnerName:  "Greg",
				OwnerEmail: "greg@specstory.com",
			},
			Snippet: "hit",
		},
	}, agentIDByName)

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].OwnerEmail != "greg@specstory.com" {
		t.Errorf("owner not carried through: %+v", sessions[0])
	}
}

// TestMergeCloudProjectsAttribution covers the deliberate asymmetry: a cloud-ONLY project
// carries its owner, a project you also have locally does not. A project present on this
// machine is one you work in yourself, so labelling it with whichever teammate's copy the
// server picked as representative would be actively misleading.
func TestMergeCloudProjectsAttribution(t *testing.T) {
	agentIDByName := map[string]string{"Claude Code": "claude"}

	local := []sessionindex.ProjectSummary{
		{
			ProjectID:    "shared",
			ProjectName:  "shared",
			Sessions:     1,
			LastActivity: "2026-08-01T10:00:00Z",
			AgentCounts:  map[string]int{"claude": 1},
		},
	}

	gregs := cloud.CloudSession{
		ClientID:   "bbb",
		Metadata:   cloud.CloudSessionMetadata{AgentName: "Claude Code"},
		EndedAt:    "2026-08-02T10:00:00Z",
		OwnerID:    "user_greg",
		OwnerName:  "Greg",
		OwnerEmail: "greg@specstory.com",
	}

	merged := mergeCloudProjects(local, nil, []cloud.CloudProject{
		{
			ID: "shared", Name: "shared", Sessions: []cloud.CloudSession{gregs},
			OwnerID: "user_greg", OwnerName: "Greg", OwnerEmail: "greg@specstory.com",
			ContributorCount: 2,
		},
		{
			ID: "cloudonly", Name: "cloudonly", Sessions: []cloud.CloudSession{gregs},
			OwnerID: "user_greg", OwnerName: "Greg", OwnerEmail: "greg@specstory.com",
			ContributorCount: 2,
		},
	}, agentIDByName)

	byID := map[string]sessionindex.ProjectSummary{}
	for _, p := range merged {
		byID[p.ProjectID] = p
	}

	cloudOnly, ok := byID["cloudonly"]
	if !ok {
		t.Fatal("cloud-only project missing from the merge")
	}
	if got := ownerLabel(cloudOnly.OwnerID, cloudOnly.OwnerName, cloudOnly.OwnerEmail, viewer); got != "Greg" {
		t.Errorf("cloud-only project should attribute to Greg, got %q", got)
	}
	if cloudOnly.ContributorCount != 2 {
		t.Errorf("contributorCount = %d, want 2", cloudOnly.ContributorCount)
	}

	shared, ok := byID["shared"]
	if !ok {
		t.Fatal("shared project missing from the merge")
	}
	if got := ownerLabel(shared.OwnerID, shared.OwnerName, shared.OwnerEmail, viewer); got != "" {
		t.Errorf("a project held locally must not be attributed to a teammate, got %q", got)
	}
}

// TestProjectRowRendersAttribution checks the label actually reaches the rendered row, and that
// a row with nothing to say renders no trailing separator. Style codes are stripped by
// comparing on substrings rather than exact output, which would pin the styling too.
func TestProjectRowRendersAttribution(t *testing.T) {
	m := sessionTUI{viewerEmail: viewer}

	theirs := sessionindex.ProjectSummary{
		ProjectID: "p1", ProjectName: "stoa", IsCloud: true,
		LastActivity: "2026-08-02T10:00:00Z",
		OwnerID:      "user_greg", OwnerName: "Greg", OwnerEmail: "greg@specstory.com",
	}
	if got := m.projectRow(theirs, false); !strings.Contains(got, "Greg") {
		t.Errorf("teammate's project row should name Greg, got %q", got)
	}

	mine := theirs
	mine.OwnerEmail = viewer
	mine.OwnerName = "Eric"
	got := m.projectRow(mine, false)
	if strings.Contains(got, "Eric") {
		t.Errorf("your own project row must not be attributed, got %q", got)
	}

	// An un-attributed row (older cloud deployment) must not render a dangling separator.
	bare := theirs
	bare.OwnerID, bare.OwnerName, bare.OwnerEmail = "", "", ""
	if strings.HasSuffix(strings.TrimSpace(m.projectRow(bare, false)), "·") {
		t.Error("un-attributed row should not end in a separator")
	}
}

// TestGlobalRowRendersAttribution covers the search rows, in both view modes.
//
// globalRow lays out sparse and dense differently — sparse puts the label on the row's second
// (meta) line, dense appends it to the single line — so a regression can drop it from one while
// the other still works. Both are asserted, along with the un-attributed case not leaving a
// dangling separator behind.
func TestGlobalRowRendersAttribution(t *testing.T) {
	// agentColW at its historical minimum keeps the label width maths at pad=0, and lineWidth()
	// falls back to 80 when width is unset — enough room that the title is not truncated into
	// the assertions.
	base := sessionTUI{viewerEmail: viewer, agentColW: agentColMinWidth}

	theirs := sessionindex.Session{
		ProjectID: "p1", ProjectName: "stoa", Agent: "claude",
		SessionID: "aaaaaaaa", Name: "retry logic", UpdatedAt: "2026-08-02T10:00:00Z",
		IsCloud: true,
		OwnerID: "user_greg", OwnerName: "Greg", OwnerEmail: "greg@specstory.com",
	}

	mine := theirs
	mine.OwnerID, mine.OwnerName, mine.OwnerEmail = "user_me", "Winifred", viewer

	bare := theirs
	bare.OwnerID, bare.OwnerName, bare.OwnerEmail = "", "", ""

	for _, mode := range []string{"sparse", "dense"} {
		t.Run(mode, func(t *testing.T) {
			m := base
			m.viewMode = mode

			if got := m.globalRow(theirs, false, ""); !strings.Contains(got, "Greg") {
				t.Errorf("teammate's search row should name Greg, got %q", got)
			}

			// "Winifred" appears nowhere else in the row, so a hit here is unambiguously the
			// attribution label rather than an incidental substring.
			if got := m.globalRow(mine, false, ""); strings.Contains(got, "Winifred") {
				t.Errorf("your own search row must not be attributed, got %q", got)
			}

			// An older cloud deployment sends no owner fields; the row must not trail a
			// separator with nothing after it.
			got := m.globalRow(bare, false, "")
			lines := strings.Split(got, "\n")
			// stripANSI is the package's existing helper (skills_test.go) — a suffix assertion
			// must see visible text, not a trailing style reset.
			last := strings.TrimSpace(stripANSI(lines[len(lines)-1]))
			if strings.HasSuffix(last, "·") {
				t.Errorf("un-attributed row should not end in a separator, got %q", last)
			}
		})
	}
}
