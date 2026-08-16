package spi

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WatchWindowDays is how many days of lookback before today stay under an
// active fsnotify watch; the window also includes today. One shared constant
// because the window is a single product decision, not a per-provider tunable:
// fsnotify's kqueue backend on macOS holds an open file descriptor for every
// file in a watched directory, so watching a store's full history pins one fd
// per session ever recorded (~25k on large Codex installs, which exhausted the
// system-wide fd table — changelog v2.6.0). Bounding watches to a trailing
// window keeps fd usage flat while still live-streaming sessions that span
// midnight or were resumed outside SpecStory. Providers with date-sharded
// stores apply it via WatchWindowCutoff/DateDirWithinWatchWindow; providers
// with flat stores apply it to file modification times.
const WatchWindowDays = 7

// WatchWindowCutoff returns the earliest date (local midnight) still inside
// the watch window.
func WatchWindowCutoff(now time.Time) time.Time {
	year, month, day := now.AddDate(0, 0, -WatchWindowDays).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}

// DateDirWithinWatchWindow reports whether a directory in a date-sharded
// session store (YYYY/MM/DD under sessionsRoot) could hold sessions dated on
// or after cutoff, and therefore deserves an fsnotify watch. A year or month
// directory qualifies when any day inside it could still be in the window.
//
// maxDepth is how many path components below sessionsRoot belong to the store
// layout: 3 for stores whose day directories hold session files directly
// (Codex), 4 for stores with one directory per session inside each day
// directory (Muse); components below the date inherit the day's date. Anything
// deeper, outside the root, or not shaped like a zero-padded calendar date
// gets no watch from this rule — the width check is what keeps look-alike
// directories (a stray "8" or "tmp") unwatched.
func DateDirWithinWatchWindow(path string, sessionsRoot string, maxDepth int, cutoff time.Time) bool {
	rel, err := filepath.Rel(sessionsRoot, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > maxDepth {
		return false
	}

	dateParts := parts
	if len(dateParts) > 3 {
		dateParts = dateParts[:3]
	}
	widths := [3]int{4, 2, 2} // zero-padded YYYY / MM / DD
	var nums [3]int
	for i, part := range dateParts {
		if len(part) != widths[i] {
			return false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return false
		}
		nums[i] = n
	}

	switch len(dateParts) {
	case 1: // YYYY
		return nums[0] >= cutoff.Year()
	case 2: // YYYY/MM
		if nums[1] < 1 || nums[1] > 12 {
			return false
		}
		return nums[0] > cutoff.Year() ||
			(nums[0] == cutoff.Year() && time.Month(nums[1]) >= cutoff.Month())
	default: // YYYY/MM/DD, with deeper components inheriting this date
		date := time.Date(nums[0], time.Month(nums[1]), nums[2], 0, 0, 0, 0, cutoff.Location())
		// time.Date normalizes out-of-range values (Feb 30 becomes March 2); a
		// round-trip mismatch means the components were not a real calendar date.
		if date.Year() != nums[0] || date.Month() != time.Month(nums[1]) || date.Day() != nums[2] {
			return false
		}
		return !date.Before(cutoff)
	}
}
