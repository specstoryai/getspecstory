package cmd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

const testViewerEmail = "eric@specstory.com"

var gregOwner = sessionindex.Owner{OwnerID: "user_greg", OwnerName: "Greg", OwnerEmail: "greg@specstory.com"}

// TestOwnerLabel pins the conservative attribution rule.
//
// A team-shared project's listings union your own rows with teammates' copies, so a row has to
// say when it is not yours — but only when that is POSITIVELY known. Each silent case below is
// indistinguishable from "this row is yours", and labelling your own session with a colleague's
// name is a worse failure than showing nothing.
//
// Who gets credited must stay in step with ownerLabel() in the VS Code extension
// (specstory-monorepo, src/webview/lib/format.ts): the same session should read the same way in
// both. The sanitizing and cap cases are terminal-only.
func TestOwnerLabel(t *testing.T) {
	tests := []struct {
		name        string
		owner       sessionindex.Owner
		viewerEmail string
		want        string
	}{
		{
			name:        "names the teammate who owns the row",
			owner:       gregOwner,
			viewerEmail: testViewerEmail,
			want:        "Greg",
		},
		{
			name:        "falls back to the email when the profile has no name",
			owner:       sessionindex.Owner{OwnerID: "user_greg", OwnerEmail: "greg@specstory.com"},
			viewerEmail: testViewerEmail,
			want:        "greg@specstory.com",
		},
		{
			name:        "a whitespace-only name is not a name",
			owner:       sessionindex.Owner{OwnerID: "user_greg", OwnerName: "   ", OwnerEmail: "greg@specstory.com"},
			viewerEmail: testViewerEmail,
			want:        "greg@specstory.com",
		},
		{
			name:        "credits nobody for your own row",
			owner:       sessionindex.Owner{OwnerID: "user_me", OwnerName: "Eric", OwnerEmail: testViewerEmail},
			viewerEmail: testViewerEmail,
			want:        "",
		},
		{
			name:        "matches your own row regardless of email casing",
			owner:       sessionindex.Owner{OwnerID: "user_me", OwnerName: "Eric", OwnerEmail: "Eric@SpecStory.com"},
			viewerEmail: testViewerEmail,
			want:        "",
		},
		{
			// An older cloud deployment: the fields are simply not on the wire.
			name:        "silent when the cloud sends no attribution",
			viewerEmail: testViewerEmail,
			want:        "",
		},
		{
			// ownerId known, but the Clerk webhook has not backfilled a profile, so there is
			// nothing to compare against the viewer.
			name:        "silent when the owner has no cached profile",
			owner:       sessionindex.Owner{OwnerID: "user_ghost"},
			viewerEmail: testViewerEmail,
			want:        "",
		},
		{
			// The dangerous shape: a name arrives without an email, so the row LOOKS
			// attributable but cannot be compared against the viewer. Returning the name here
			// is how you end up telling someone their own session belongs to a colleague.
			name:        "silent when a name arrives without an email to compare",
			owner:       sessionindex.Owner{OwnerID: "user_unknown", OwnerName: "Eric"},
			viewerEmail: testViewerEmail,
			want:        "",
		},
		{
			// Not signed in, a session token, or auth.json unreadable — nothing is comparable.
			name:  "silent when the viewer is unknown",
			owner: gregOwner,
			want:  "",
		},
		{
			name:        "a newline in a name cannot split the row",
			owner:       sessionindex.Owner{OwnerID: "u", OwnerName: "Greg\nJones", OwnerEmail: "greg@specstory.com"},
			viewerEmail: testViewerEmail,
			want:        "Greg Jones",
		},
		{
			name:        "an escape sequence in a name cannot reach the terminal",
			owner:       sessionindex.Owner{OwnerID: "u", OwnerName: "Gr\x1beg\x07", OwnerEmail: "greg@specstory.com"},
			viewerEmail: testViewerEmail,
			want:        "Greg",
		},
		{
			name:        "a name of only control characters falls back to the email",
			owner:       sessionindex.Owner{OwnerID: "u", OwnerName: "\x1b\x07", OwnerEmail: "greg@specstory.com"},
			viewerEmail: testViewerEmail,
			want:        "greg@specstory.com",
		},
		{
			name:        "a long name is capped",
			owner:       sessionindex.Owner{OwnerID: "u", OwnerName: "Gregory Montgomery Andersson", OwnerEmail: "greg@specstory.com"},
			viewerEmail: testViewerEmail,
			want:        "Gregory Montgomery …",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownerLabel(tc.owner, tc.viewerEmail); got != tc.want {
				t.Errorf("ownerLabel(%+v, %q) = %q, want %q", tc.owner, tc.viewerEmail, got, tc.want)
			}
		})
	}
}

// TestProjectOwnerLabel covers the "+N" tail. It must count only the copies the named owner
// doesn't cover, and must never appear without a label to attach to.
func TestProjectOwnerLabel(t *testing.T) {
	yours := sessionindex.Owner{OwnerID: "user_me", OwnerName: "Eric", OwnerEmail: testViewerEmail}

	tests := []struct {
		name         string
		owner        sessionindex.Owner
		contributors int
		want         string
	}{
		{name: "older deployment sends no count", owner: gregOwner, contributors: 0, want: "Greg"},
		{name: "one copy adds no tail", owner: gregOwner, contributors: 1, want: "Greg"},
		{name: "second copy is counted", owner: gregOwner, contributors: 2, want: "Greg +1"},
		{name: "every extra copy is counted", owner: gregOwner, contributors: 4, want: "Greg +3"},
		{name: "your own copy shows nothing", owner: yours, contributors: 3, want: ""},
		{name: "no owner shows no bare tail", contributors: 3, want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := sessionindex.ProjectSummary{Owner: tc.owner, ContributorCount: tc.contributors}
			if got := projectOwnerLabel(p, testViewerEmail); got != tc.want {
				t.Errorf("projectOwnerLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMergeCloudProjectsAttribution covers the deliberate asymmetry: a cloud-ONLY project
// carries its owner, a project you also have locally does not. A project present on this
// machine is one you work in yourself, even when the only synced copy is a teammate's.
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

	cloudOwner := cloud.Owner(gregOwner)
	gregs := cloud.CloudSession{
		ClientID: "bbb",
		Metadata: cloud.CloudSessionMetadata{AgentName: "Claude Code"},
		EndedAt:  "2026-08-02T10:00:00Z",
		Owner:    cloudOwner,
	}

	merged := mergeCloudProjects(local, nil, []cloud.CloudProject{
		{ID: "shared", Name: "shared", Sessions: []cloud.CloudSession{gregs}, Owner: cloudOwner, ContributorCount: 2},
		{ID: "cloudonly", Name: "cloudonly", Sessions: []cloud.CloudSession{gregs}, Owner: cloudOwner, ContributorCount: 2},
	}, agentIDByName)

	byID := map[string]sessionindex.ProjectSummary{}
	for _, p := range merged {
		byID[p.ProjectID] = p
	}

	cloudOnly, ok := byID["cloudonly"]
	if !ok {
		t.Fatal("cloud-only project missing from the merge")
	}
	if got := projectOwnerLabel(cloudOnly, testViewerEmail); got != "Greg +1" {
		t.Errorf("cloud-only project label = %q, want %q", got, "Greg +1")
	}

	shared, ok := byID["shared"]
	if !ok {
		t.Fatal("shared project missing from the merge")
	}
	if got := projectOwnerLabel(shared, testViewerEmail); got != "" {
		t.Errorf("a project held locally must not be attributed to a teammate, got %q", got)
	}
}

// attributedRows lists every row that can show attribution. Each lays the label out differently,
// so one can lose it, or overflow, while the others still work. snippet is ignored by the
// project row, which has none.
func attributedRows() []struct {
	name   string
	render func(m sessionTUI, title, snippet string, owner sessionindex.Owner) string
} {
	// Recent on purpose: an older date renders wider once it gains a year stamp, which would make
	// the width assertions depend on when the test runs.
	updatedAt := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)

	session := func(title string, owner sessionindex.Owner) sessionindex.Session {
		return sessionindex.Session{
			ProjectID: "p1", ProjectName: "stoa", Agent: "claude", SessionID: "aaaaaaaa",
			Name: title, UpdatedAt: updatedAt, IsCloud: true, Owner: owner,
		}
	}
	sessionRowIn := func(mode string) func(sessionTUI, string, string, sessionindex.Owner) string {
		return func(m sessionTUI, title, snippet string, owner sessionindex.Owner) string {
			m.viewMode = mode
			return m.sessionRow(session(title, owner), false, snippet)
		}
	}
	globalRowIn := func(mode string) func(sessionTUI, string, string, sessionindex.Owner) string {
		return func(m sessionTUI, title, snippet string, owner sessionindex.Owner) string {
			m.viewMode = mode
			return m.globalRow(session(title, owner), false, snippet)
		}
	}

	return []struct {
		name   string
		render func(m sessionTUI, title, snippet string, owner sessionindex.Owner) string
	}{
		{"sessionRow sparse", sessionRowIn("sparse")},
		{"sessionRow dense", sessionRowIn("dense")},
		{"globalRow sparse", globalRowIn("sparse")},
		{"globalRow dense", globalRowIn("dense")},
		{"projectRow", func(m sessionTUI, title, _ string, owner sessionindex.Owner) string {
			return m.projectRow(sessionindex.ProjectSummary{
				ProjectID: "p1", ProjectName: title, IsCloud: true, LastActivity: updatedAt,
				AgentCounts: map[string]int{"claude": 12, "codex": 3, "cursor": 9},
				Owner:       owner,
			}, false)
		}},
	}
}

// TestRowsRenderAttribution checks the label reaches every row type, stays off your own rows,
// and leaves no dangling separator when there is nothing to say.
func TestRowsRenderAttribution(t *testing.T) {
	m := sessionTUI{viewerEmail: testViewerEmail, agentColW: agentColMinWidth}

	// "Winifred" appears nowhere else in a row, so a hit is unambiguously the attribution.
	mine := sessionindex.Owner{OwnerID: "user_me", OwnerName: "Winifred", OwnerEmail: testViewerEmail}

	for _, row := range attributedRows() {
		t.Run(row.name, func(t *testing.T) {
			if got := stripANSI(row.render(m, "retry logic", "", gregOwner)); !strings.Contains(got, "Greg") {
				t.Errorf("teammate's row should name Greg, got %q", got)
			}
			if got := stripANSI(row.render(m, "retry logic", "", mine)); strings.Contains(got, "Winifred") {
				t.Errorf("your own row must not be attributed, got %q", got)
			}

			// An older cloud deployment sends no owner fields.
			lines := strings.Split(stripANSI(row.render(m, "retry logic", "", sessionindex.Owner{})), "\n")
			if last := strings.TrimSpace(lines[len(lines)-1]); strings.HasSuffix(last, "·") {
				t.Errorf("un-attributed row should not end in a separator, got %q", last)
			}
		})
	}
}

// TestRowsKeepAttributionOnScreen guards the row budgets. The terminal clips an overlong line
// and attribution sits last, so an overflow silently cuts off the name, and widening the
// terminal doesn't help because title budgets grow with it.
func TestRowsKeepAttributionOnScreen(t *testing.T) {
	longText := strings.Repeat("refactor the retry logic ", 12)

	owners := []struct {
		name  string
		owner sessionindex.Owner
	}{
		{"short name", gregOwner},
		{"long name", sessionindex.Owner{OwnerID: "u", OwnerName: "Gregory Montgomery Andersson", OwnerEmail: "greg@specstory.com"}},
		{"email only", sessionindex.Owner{OwnerID: "u", OwnerEmail: "gregory.andersson@specstory.com"}},
	}
	labels := []struct {
		name           string
		title, snippet string
	}{
		{"long title", longText, ""},
		{"long snippet", "retry logic", longText},
	}

	for _, width := range []int{80, 120} {
		m := sessionTUI{viewerEmail: testViewerEmail, agentColW: agentColMinWidth, width: width}
		for _, row := range attributedRows() {
			for _, o := range owners {
				for _, l := range labels {
					t.Run(row.name+"/"+o.name+"/"+l.name, func(t *testing.T) {
						want := ownerLabel(o.owner, testViewerEmail)
						rendered := stripANSI(row.render(m, l.title, l.snippet, o.owner))
						if !strings.Contains(rendered, want) {
							t.Fatalf("row should contain %q, got %q", want, rendered)
						}
						for line := range strings.SplitSeq(rendered, "\n") {
							if lipgloss.Width(line) > width {
								t.Errorf("line is %d wide on a %d-wide terminal: %q",
									lipgloss.Width(line), width, line)
							}
						}
					})
				}
			}
		}
	}
}

// TestRowsFitTerminal covers what TestRowsKeepAttributionOnScreen can't reach with its cloud rows:
// local rows, whose turn count sits last and is what the terminal clips, and a wide agent column.
func TestRowsFitTerminal(t *testing.T) {
	longText := strings.Repeat("refactor the retry logic ", 12)
	updatedAt := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)

	rows := []struct {
		name   string
		render func(m sessionTUI, s sessionindex.Session, snippet string) string
	}{
		{"sessionRow", func(m sessionTUI, s sessionindex.Session, snippet string) string {
			return m.sessionRow(s, false, snippet)
		}},
		{"globalRow", func(m sessionTUI, s sessionindex.Session, snippet string) string {
			return m.globalRow(s, false, snippet)
		}},
	}
	labels := []struct {
		name           string
		title, snippet string
	}{
		{"long title", longText, ""},
		{"long snippet", "retry logic", longText},
	}

	for _, width := range []int{80, 120} {
		for _, agentColW := range []int{agentColMinWidth, agentColMinWidth + 4} {
			for _, mode := range []string{"sparse", "dense"} {
				for _, turns := range []int{7, 1234} {
					for _, row := range rows {
						for _, l := range labels {
							name := fmt.Sprintf("%d/agent%d/%s/%d turns/%s/%s", width, agentColW, mode, turns, row.name, l.name)
							t.Run(name, func(t *testing.T) {
								m := sessionTUI{agentColW: agentColW, width: width, viewMode: mode}
								s := sessionindex.Session{
									ProjectID: "p1", ProjectName: "stoa", Agent: "claude", SessionID: "aaaaaaaa",
									Name: l.title, UpdatedAt: updatedAt, UserTurns: turns,
								}
								for line := range strings.SplitSeq(stripANSI(row.render(m, s, l.snippet)), "\n") {
									if lipgloss.Width(line) > width {
										t.Errorf("line is %d wide on a %d-wide terminal: %q", lipgloss.Width(line), width, line)
									}
								}
							})
						}
					}
				}
			}
		}
	}
}
