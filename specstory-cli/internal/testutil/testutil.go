// Package testutil provides helpers shared by tests across packages.
package testutil

import (
	"encoding/json"
	"testing"
)

// JSONString returns s as a JSON string literal, quotes included. Test fixtures
// that splice filesystem paths into hand-written JSON must escape them: Windows
// paths contain backslashes, which are JSON escape characters, so raw splicing
// produces invalid JSON on Windows while passing silently on Unix.
func JSONString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		// Marshaling a string can only fail for invalid UTF-8; fall back to a
		// bare quote wrap so the fixture failure surfaces in the test output.
		return `"` + s + `"`
	}
	return string(data)
}

// SetHome points the current test's home directory at dir on every platform,
// restoring the originals when the test ends. Tests that fake the home
// directory must set both variables: os.UserHomeDir reads HOME on Unix but
// USERPROFILE on Windows, so setting HOME alone silently leaves Windows test
// runs reading the real user profile instead of the fixture.
func SetHome(t testing.TB, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}
