// Package testutil provides helpers shared by tests across packages.
package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// EqualPaths reports whether two filesystem paths refer to the same location
// under the platform's comparison rules: case-insensitive on Windows (NTFS
// folds case, and file-URI round-trips lowercase the drive letter per VS
// Code's serialization), exact match elsewhere.
func EqualPaths(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

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

// IsolateDebugDir redirects debug exports to a temporary directory for this
// test. The SPI override is global, so callers must not run in parallel.
func IsolateDebugDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	spi.SetDebugBaseDir(dir)
	t.Cleanup(func() { spi.SetDebugBaseDir("") })
	return dir
}

// AssertDebugRefresh checks that a refresh removes existing provider-owned
// files and preserves unrelated files, including CLI-owned session-data.json.
func AssertDebugRefresh(t testing.TB, dir string, staleNames, keepNames []string, refresh func()) {
	t.Helper()
	for _, name := range staleNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("stale-file precondition %s: %v", name, err)
		}
	}
	for _, name := range keepNames {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("preserve:"+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	refresh()
	for _, name := range staleNames {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("stale file %s survived: %v", name, err)
		}
	}
	for _, name := range keepNames {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "preserve:"+name {
			t.Errorf("unowned file %s changed: %q (%v)", name, data, err)
		}
	}
}
