// Package testutil provides helpers shared by tests across packages.
package testutil

import "testing"

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
