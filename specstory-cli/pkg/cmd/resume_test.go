package cmd

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

func TestSortSessionsDesc(t *testing.T) {
	sessions := []spi.SessionMetadata{
		{SessionID: "a", CreatedAt: "2026-06-10T10:00:00Z"},
		{SessionID: "b", CreatedAt: "2026-06-16T09:00:00Z"},
		{SessionID: "c", CreatedAt: "2026-06-12T12:00:00Z"},
	}
	sortSessionsDesc(sessions)
	want := []string{"b", "c", "a"}
	for i, w := range want {
		if sessions[i].SessionID != w {
			t.Errorf("position %d: got %s, want %s", i, sessions[i].SessionID, w)
		}
	}
}

func TestDateRange(t *testing.T) {
	tests := []struct {
		name     string
		sessions []spi.SessionMetadata
		want     string
	}{
		{
			name:     "single day",
			sessions: []spi.SessionMetadata{{CreatedAt: "2026-06-16T09:00:00Z"}, {CreatedAt: "2026-06-16T20:00:00Z"}},
			want:     "Jun 16, 2026",
		},
		{
			name:     "span",
			sessions: []spi.SessionMetadata{{CreatedAt: "2026-06-10T09:00:00Z"}, {CreatedAt: "2026-06-16T20:00:00Z"}},
			want:     "Jun 10, 2026 – Jun 16, 2026",
		},
		{
			name:     "unparseable",
			sessions: []spi.SessionMetadata{{CreatedAt: "not-a-date"}},
			want:     "dates unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dateRange(tt.sessions); got != tt.want {
				t.Errorf("dateRange = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSessionLabel(t *testing.T) {
	tests := []struct {
		name string
		in   spi.SessionMetadata
		want string
	}{
		{"name wins", spi.SessionMetadata{Name: "Fix the bug", Slug: "fix-bug", SessionID: "1234567890abcdef"}, "Fix the bug"},
		{"slug fallback", spi.SessionMetadata{Slug: "fix-bug", SessionID: "1234567890abcdef"}, "fix-bug"},
		{"id fallback", spi.SessionMetadata{SessionID: "1234567890abcdef"}, "12345...bcdef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionLabel(tt.in); got != tt.want {
				t.Errorf("sessionLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPlural(t *testing.T) {
	if plural(1) != "" {
		t.Errorf("plural(1) = %q, want empty", plural(1))
	}
	if plural(0) != "s" || plural(2) != "s" {
		t.Errorf("plural(0)/plural(2) should be %q", "s")
	}
}
