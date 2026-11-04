package claudecode

import (
	"os"
	"sort"
	"testing"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
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

// With --project-path the agent runs in the project, not the launch directory,
// and its session must arrive live: the sweep at exit would hide a watcher left
// on the launch directory, so this asserts delivery before anything stops.
func TestWatchForProjectDirFollowsProjectNotLaunchDirectory(t *testing.T) {
	testutil.SetHome(t, t.TempDir())
	// An inherited override outranks the temp home and would put the fake
	// transcript in the developer's real store.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Chdir(t.TempDir())
	project := spi.CanonicalizePathOrClean(t.TempDir())
	dir, err := resolveClaudeProjectDir(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	delivered := make(chan string, 4)
	SetWatcherCallback(func(session *spi.AgentChatSession) { delivered <- session.SessionID })
	defer ClearWatcherCallback()
	if err := WatchForProjectDir(project); err != nil {
		t.Fatal(err)
	}
	defer StopWatcher()

	provider := NewProvider()
	native, err := provider.ReconstructSession(&schema.SessionData{
		SchemaVersion: schema.CurrentSchemaVersion,
		SessionID:     "source-session",
		WorkspaceRoot: project,
		Exchanges: []schema.Exchange{{Messages: []schema.Message{
			{Role: schema.RoleUser, Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "live work"}}},
		}}},
	}, spi.ReconstructOptions{WorkspaceRoot: project})
	if err != nil {
		t.Fatal(err)
	}
	path, err := provider.NativeSessionPath(project, native.Filename)
	if err != nil {
		t.Fatal(err)
	}
	// Keep writing, as a running agent does: the watch registers in the
	// background, and a lone write ahead of it would look like existing history.
	deadline := time.After(10 * time.Second)
	for {
		appendToFile(t, path, native.Content)
		select {
		case id := <-delivered:
			if id != native.SessionID {
				t.Errorf("delivered session %q, want %q", id, native.SessionID)
			}
			return
		case <-deadline:
			t.Fatal("no live delivery for the project's session")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// appendToFile opens and closes the file for each write: Windows may not report
// writes to a directory watcher until the writing handle closes.
func appendToFile(t *testing.T, path string, data []byte) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
}
