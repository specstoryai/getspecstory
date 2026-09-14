package spi

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWatchWindowCutoff(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		expected time.Time
	}{
		{
			name:     "mid-month afternoon truncates to midnight",
			now:      time.Date(2026, 8, 3, 15, 4, 5, 0, time.UTC),
			expected: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "window spans a year boundary",
			now:      time.Date(2026, 1, 4, 9, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 12, 28, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WatchWindowCutoff(tt.now)
			if !got.Equal(tt.expected) {
				t.Errorf("WatchWindowCutoff(%v) = %v, expected %v", tt.now, got, tt.expected)
			}
		})
	}
}

func TestDateDirWithinWatchWindow(t *testing.T) {
	sessionsRoot := filepath.Join("home", "user", ".agent", "sessions")

	// Cutoff of 2026-07-27 corresponds to "now" being 2026-08-03 with a 7-day window
	cutoff := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)

	// Cutoff spanning a year boundary: "now" 2026-01-04, window back to 2025-12-28
	yearBoundaryCutoff := time.Date(2025, 12, 28, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		path     string
		maxDepth int
		cutoff   time.Time
		expected bool
	}{
		// Day directories
		{"day within window", filepath.Join(sessionsRoot, "2026", "07", "28"), 3, cutoff, true},
		{"day is today", filepath.Join(sessionsRoot, "2026", "08", "03"), 3, cutoff, true},
		{"day exactly at cutoff", filepath.Join(sessionsRoot, "2026", "07", "27"), 3, cutoff, true},
		{"day just before cutoff", filepath.Join(sessionsRoot, "2026", "07", "26"), 3, cutoff, false},
		{"day in the future", filepath.Join(sessionsRoot, "2026", "08", "04"), 3, cutoff, true},
		{"day in a much older month", filepath.Join(sessionsRoot, "2025", "01", "15"), 3, cutoff, false},

		// Month directories
		{"month containing cutoff", filepath.Join(sessionsRoot, "2026", "07"), 3, cutoff, true},
		{"current month", filepath.Join(sessionsRoot, "2026", "08"), 3, cutoff, true},
		{"month before cutoff", filepath.Join(sessionsRoot, "2026", "06"), 3, cutoff, false},
		{"same month number in older year", filepath.Join(sessionsRoot, "2025", "07"), 3, cutoff, false},

		// Year directories
		{"current year", filepath.Join(sessionsRoot, "2026"), 3, cutoff, true},
		{"previous year", filepath.Join(sessionsRoot, "2025"), 3, cutoff, false},

		// Window spanning a year boundary
		{"old year still holding window days", filepath.Join(sessionsRoot, "2025"), 3, yearBoundaryCutoff, true},
		{"december holding window days", filepath.Join(sessionsRoot, "2025", "12"), 3, yearBoundaryCutoff, true},
		{"november outside window", filepath.Join(sessionsRoot, "2025", "11"), 3, yearBoundaryCutoff, false},
		{"december day at cutoff", filepath.Join(sessionsRoot, "2025", "12", "28"), 3, yearBoundaryCutoff, true},
		{"december day before cutoff", filepath.Join(sessionsRoot, "2025", "12", "27"), 3, yearBoundaryCutoff, false},
		{"january day in new year", filepath.Join(sessionsRoot, "2026", "01", "01"), 3, yearBoundaryCutoff, true},

		// Values that pass the width check but are not real calendar dates
		{"month out of range", filepath.Join(sessionsRoot, "2026", "13"), 3, cutoff, false},
		{"month zero", filepath.Join(sessionsRoot, "2026", "00"), 3, cutoff, false},
		{"day out of range", filepath.Join(sessionsRoot, "2026", "08", "32"), 3, cutoff, false},
		{"day zero", filepath.Join(sessionsRoot, "2026", "08", "00"), 3, cutoff, false},
		{"feb 30 does not exist", filepath.Join(sessionsRoot, "2026", "02", "30"), 3, cutoff, false},
		{"feb 29 in a non-leap year", filepath.Join(sessionsRoot, "2026", "02", "29"), 3, cutoff, false},
		{"feb 29 in a leap year", filepath.Join(sessionsRoot, "2028", "02", "29"), 3, cutoff, true},

		// Non-date paths never get a watch from the window rule
		{"sessions root itself", sessionsRoot, 3, cutoff, false},
		{"path outside sessions root", filepath.Join("home", "user", "elsewhere", "2026"), 3, cutoff, false},
		{"non-numeric year", filepath.Join(sessionsRoot, "20a6"), 3, cutoff, false},
		{"wrong-width day component", filepath.Join(sessionsRoot, "2026", "08", "3"), 3, cutoff, false},
		{"too-deep path at depth 3", filepath.Join(sessionsRoot, "2026", "08", "03", "extra"), 3, cutoff, false},

		// Depth-4 stores (one directory per session inside each day directory):
		// a session directory inherits its day's date, and anything deeper
		// (e.g. a subagent transcript directory) is out of the layout.
		{"session dir inside today", filepath.Join(sessionsRoot, "2026", "08", "03", "sid"), 4, cutoff, true},
		{"session dir inside an aged-out day", filepath.Join(sessionsRoot, "2026", "07", "26", "sid"), 4, cutoff, false},
		{"too-deep path at depth 4", filepath.Join(sessionsRoot, "2026", "08", "03", "sid", "subagent"), 4, cutoff, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DateDirWithinWatchWindow(tt.path, sessionsRoot, tt.maxDepth, tt.cutoff)
			if got != tt.expected {
				t.Errorf("DateDirWithinWatchWindow(%q, %q, %d, %v) = %v, expected %v",
					tt.path, sessionsRoot, tt.maxDepth, tt.cutoff, got, tt.expected)
			}
		})
	}
}
