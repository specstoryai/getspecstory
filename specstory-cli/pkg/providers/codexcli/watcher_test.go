package codexcli

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// dirInWatchScope gates both the fsnotify watches and the startup scan, so a
// directory falling out of scope is one the watcher neither watches nor
// reprocesses. The scan side is what keeps a long-lived store from replaying
// its whole history on every watcher start.
func TestDirInWatchScope(t *testing.T) {
	sessionsRoot := filepath.Join(t.TempDir(), "sessions")
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.Local)

	dayDir := func(t time.Time) string {
		return filepath.Join(sessionsRoot,
			fmt.Sprintf("%04d", t.Year()),
			fmt.Sprintf("%02d", int(t.Month())),
			fmt.Sprintf("%02d", t.Day()))
	}

	today := dayDir(now)
	inWindow := dayDir(now.AddDate(0, 0, -3))
	aged := dayDir(now.AddDate(0, 0, -30))
	ancient := dayDir(now.AddDate(-2, 0, 0))

	tests := []struct {
		name      string
		dir       string
		pinnedDay string
		expected  bool
	}{
		{"today", today, "", true},
		{"inside the trailing window", inWindow, "", true},
		{"aged out of the window", aged, "", false},
		{"years old", ancient, "", false},
		{"current year directory", filepath.Join(sessionsRoot, "2026"), "", true},
		{"current month directory", filepath.Join(sessionsRoot, "2026", "08"), "", true},
		{"old year directory", filepath.Join(sessionsRoot, "2024"), "", false},

		// A resumed session is being worked in right now, so its day directory
		// stays in scope however old the session is.
		{"pinned day directory, however old", ancient, ancient, true},
		{"pinning one old day does not reprieve another", aged, ancient, false},

		{"outside the sessions root", filepath.Join(t.TempDir(), "2026", "08", "17"), "", false},
		{"not shaped like a date", filepath.Join(sessionsRoot, "tmp"), "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dirInWatchScope(tt.dir, sessionsRoot, tt.pinnedDay, now); got != tt.expected {
				t.Errorf("dirInWatchScope(%q, pinned=%q) = %v, want %v",
					tt.dir, tt.pinnedDay, got, tt.expected)
			}
		})
	}
}
