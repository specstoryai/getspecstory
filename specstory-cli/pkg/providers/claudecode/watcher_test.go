package claudecode

import (
	"sort"
	"testing"
	"time"
)

func TestReconcileFileWatches(t *testing.T) {
	cutoff := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	recent := cutoff.Add(24 * time.Hour)
	old := cutoff.Add(-24 * time.Hour)

	tests := []struct {
		name           string
		files          map[string]time.Time
		watched        map[string]bool
		expectedAdd    []string
		expectedRemove []string
	}{
		{
			name:           "empty directory and nothing watched",
			files:          map[string]time.Time{},
			watched:        map[string]bool{},
			expectedAdd:    nil,
			expectedRemove: nil,
		},
		{
			name: "startup: recent files gain watches, old files do not",
			files: map[string]time.Time{
				"/p/recent-a.jsonl": recent,
				"/p/recent-b.jsonl": recent,
				"/p/old.jsonl":      old,
			},
			watched:        map[string]bool{},
			expectedAdd:    []string{"/p/recent-a.jsonl", "/p/recent-b.jsonl"},
			expectedRemove: nil,
		},
		{
			name: "steady state: watched recent files stay, nothing changes",
			files: map[string]time.Time{
				"/p/recent.jsonl": recent,
				"/p/old.jsonl":    old,
			},
			watched:        map[string]bool{"/p/recent.jsonl": true},
			expectedAdd:    nil,
			expectedRemove: nil,
		},
		{
			name: "new session file appears",
			files: map[string]time.Time{
				"/p/existing.jsonl": recent,
				"/p/new.jsonl":      recent,
			},
			watched:        map[string]bool{"/p/existing.jsonl": true},
			expectedAdd:    []string{"/p/new.jsonl"},
			expectedRemove: nil,
		},
		{
			name: "self-heal: dormant file woken by a write regains its watch",
			files: map[string]time.Time{
				"/p/woken.jsonl": recent,
			},
			watched:        map[string]bool{},
			expectedAdd:    []string{"/p/woken.jsonl"},
			expectedRemove: nil,
		},
		{
			name: "prune: watched file aged past the cutoff",
			files: map[string]time.Time{
				"/p/idle.jsonl": old,
			},
			watched:        map[string]bool{"/p/idle.jsonl": true},
			expectedAdd:    nil,
			expectedRemove: []string{"/p/idle.jsonl"},
		},
		{
			name:  "prune: watched file deleted from disk",
			files: map[string]time.Time{},
			watched: map[string]bool{
				"/p/deleted.jsonl": true,
			},
			expectedAdd:    nil,
			expectedRemove: []string{"/p/deleted.jsonl"},
		},
		{
			name: "mtime exactly at cutoff stays watched",
			files: map[string]time.Time{
				"/p/boundary.jsonl": cutoff,
			},
			watched:        map[string]bool{"/p/boundary.jsonl": true},
			expectedAdd:    nil,
			expectedRemove: nil,
		},
		{
			name: "mixed: add, keep, prune, and delete in one pass",
			files: map[string]time.Time{
				"/p/new.jsonl":   recent,
				"/p/kept.jsonl":  recent,
				"/p/aging.jsonl": old,
			},
			watched: map[string]bool{
				"/p/kept.jsonl":    true,
				"/p/aging.jsonl":   true,
				"/p/deleted.jsonl": true,
			},
			expectedAdd:    []string{"/p/new.jsonl"},
			expectedRemove: []string{"/p/aging.jsonl", "/p/deleted.jsonl"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			add, remove := reconcileFileWatches(tt.files, tt.watched, cutoff)

			// Map iteration order is random, so compare sorted
			sort.Strings(add)
			sort.Strings(remove)

			if !equalStringSlices(add, tt.expectedAdd) {
				t.Errorf("add = %v, expected %v", add, tt.expectedAdd)
			}
			if !equalStringSlices(remove, tt.expectedRemove) {
				t.Errorf("remove = %v, expected %v", remove, tt.expectedRemove)
			}
		})
	}
}

// equalStringSlices compares two string slices treating nil and empty as equal
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
