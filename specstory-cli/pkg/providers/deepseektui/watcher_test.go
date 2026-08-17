package deepseektui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
)

// Seeding is what keeps `specstory watch` from re-emitting sessions that were
// already on disk when it started: scanAndProcessSessions skips any file whose
// recorded time is at least its modification time, so every existing file has
// to be recorded, with a time no older than the file itself.
func TestSeedProcessedSessions(t *testing.T) {
	home := t.TempDir()
	testutil.SetHome(t, home)

	dir := filepath.Join(home, deepSeekRootDir, deepSeekSessionsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create sessions dir: %v", err)
	}

	existing := []string{"one.json", "two.json"}
	for _, name := range existing {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
	// Not a transcript, so it must not be recorded — otherwise a later rename
	// onto this path would arrive already marked as seen.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to write notes.txt: %v", err)
	}

	state := &watchState{lastProcessed: make(map[string]int64)}
	seedProcessedSessions(state)

	if len(state.lastProcessed) != len(existing) {
		t.Errorf("seeded %d entries, want %d: %v", len(state.lastProcessed), len(existing), state.lastProcessed)
	}
	for _, name := range existing {
		path := filepath.Join(dir, name)
		seeded, ok := state.lastProcessed[path]
		if !ok {
			t.Errorf("existing session not seeded: %s", path)
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("failed to stat %s: %v", path, err)
		}
		// Anything older would let the next scan treat the file as changed.
		if seeded < info.ModTime().UnixNano() {
			t.Errorf("seeded time for %s is older than the file: %d < %d",
				name, seeded, info.ModTime().UnixNano())
		}
	}
}

// DeepSeek may never have run on this machine, which must not be an error: there
// is simply nothing to suppress.
func TestSeedProcessedSessions_NoStore(t *testing.T) {
	testutil.SetHome(t, t.TempDir())

	state := &watchState{lastProcessed: make(map[string]int64)}
	seedProcessedSessions(state)

	if len(state.lastProcessed) != 0 {
		t.Errorf("seeded %v against a missing store, want nothing", state.lastProcessed)
	}
}
