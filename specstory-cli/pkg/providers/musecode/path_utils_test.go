package musecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Session ids baked into the testdata fixtures. The store keys a session
// directory by its id, so fixtures must be seeded under their own id.
const (
	basicSessionID     = "11111111-2222-3333-4444-555555555555"
	toolsSessionID     = "22222222-3333-4444-5555-666666666666"
	multiturnSessionID = "33333333-4444-5555-6666-777777777777"
	subagentSessionID  = "44444444-5555-6666-7777-888888888888"

	// fixtureWorkspaceRoot is the neutralized root inside every fixture; tests
	// rewrite it to a real temp directory so path canonicalization is exercised
	// against paths that actually exist.
	fixtureWorkspaceRoot = "/Users/dev/project"
)

// seedStore points the provider at a fresh fake Muse store and returns its
// sessions root.
func seedStore(t *testing.T) string {
	t.Helper()
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	return filepath.Join(dataHome, "muse", "sessions")
}

// writeSession copies a fixture into the store under datePath/sessionID,
// rewriting its workspace root. Returns the transcript path.
func writeSession(t *testing.T, sessionsRoot, datePath, sessionID, fixture, workspaceRoot string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", fixture, err)
	}
	content := strings.ReplaceAll(string(data), fixtureWorkspaceRoot, workspaceRoot)

	dir := filepath.Join(sessionsRoot, filepath.FromSlash(datePath), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}
	path := filepath.Join(dir, sessionFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write session file: %v", err)
	}
	return path
}

// writeFileFrom copies an existing file to dst, creating dst's parents.
func writeFileFrom(t *testing.T, src, dst string) {
	t.Helper()

	content, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("failed to create %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", dst, err)
	}
}

func TestGetMuseSessionsDir(t *testing.T) {
	t.Run("honors XDG_DATA_HOME", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")

		got, err := GetMuseSessionsDir()
		if err != nil {
			t.Fatalf("GetMuseSessionsDir failed: %v", err)
		}
		if got != filepath.Join("/tmp/xdg-data", "muse", "sessions") {
			t.Errorf("sessions dir = %q", got)
		}
	})

	t.Run("falls back to the XDG default under home", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		original := osUserHomeDir
		osUserHomeDir = func() (string, error) { return "/home/tester", nil }
		t.Cleanup(func() { osUserHomeDir = original })

		got, err := GetMuseSessionsDir()
		if err != nil {
			t.Fatalf("GetMuseSessionsDir failed: %v", err)
		}
		if got != filepath.Join("/home/tester", ".local", "share", "muse", "sessions") {
			t.Errorf("sessions dir = %q", got)
		}
	})
}

func TestDefaultProjectPath(t *testing.T) {
	original := osGetwd
	osGetwd = func() (string, error) { return "/current/dir", nil }
	t.Cleanup(func() { osGetwd = original })

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty falls back to cwd", input: "", expected: "/current/dir"},
		{name: "explicit path passes through", input: "/some/project", expected: "/some/project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := defaultProjectPath(tt.input)
			if err != nil {
				t.Fatalf("defaultProjectPath failed: %v", err)
			}
			if got != tt.expected {
				t.Errorf("defaultProjectPath(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestReadSessionWorkspaceRoot(t *testing.T) {
	t.Run("reads the metadata record", func(t *testing.T) {
		got, err := readSessionWorkspaceRoot(filepath.Join("testdata", "session-basic.jsonl"))
		if err != nil {
			t.Fatalf("readSessionWorkspaceRoot failed: %v", err)
		}
		if got != fixtureWorkspaceRoot {
			t.Errorf("workspace root = %q, want %q", got, fixtureWorkspaceRoot)
		}
	})

	t.Run("no metadata record yields empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), sessionFileName)
		content := `{"schema_version":1,"id":"r1","stream":{"kind":"session","id":"s1"},"payload_type":"runtime.session","payload":{"kind":"run","run_id":"r","event":{"kind":"started","prompt":"hi"}}}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		got, err := readSessionWorkspaceRoot(path)
		if err != nil {
			t.Fatalf("readSessionWorkspaceRoot failed: %v", err)
		}
		if got != "" {
			t.Errorf("workspace root = %q, want empty", got)
		}
	})
}

func TestFindMuseSessions(t *testing.T) {
	sessionsRoot := seedStore(t)
	projectA := t.TempDir()
	projectB := t.TempDir()

	writeSession(t, sessionsRoot, "2026/08/05", basicSessionID, "session-basic.jsonl", projectA)
	writeSession(t, sessionsRoot, "2026/08/07", toolsSessionID, "session-tools.jsonl", projectA)
	writeSession(t, sessionsRoot, "2026/08/06", multiturnSessionID, "session-multiturn.jsonl", projectB)

	sessions, err := findMuseSessions(projectA)
	if err != nil {
		t.Fatalf("findMuseSessions failed: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("session count = %d, want 2 (only this project's sessions)", len(sessions))
	}
	// The date-sharded path sorts chronologically, so descending path order is
	// most-recent-first.
	if sessions[0].SessionID != toolsSessionID {
		t.Errorf("first session = %q, want the most recent (%q)", sessions[0].SessionID, toolsSessionID)
	}
	if sessions[1].SessionID != basicSessionID {
		t.Errorf("second session = %q, want %q", sessions[1].SessionID, basicSessionID)
	}
	for _, session := range sessions {
		if session.WorkspaceRoot != projectA {
			t.Errorf("session %q workspace root = %q, want %q", session.SessionID, session.WorkspaceRoot, projectA)
		}
	}
}

func TestFindMuseSessions_ExcludesSubagentTranscripts(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()

	parentPath := writeSession(t, sessionsRoot, "2026/08/07", subagentSessionID, "session-subagent-noise.jsonl", project)

	// A subagent transcript in the same format, nested under its parent session.
	subagentDir := filepath.Join(filepath.Dir(parentPath), subagentDirName, "d8340c3d-4c88-4d5b-a5f8-aa0c605ffecf")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentContent, err := os.ReadFile(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subagentDir, sessionFileName), parentContent, 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := findMuseSessions(project)
	if err != nil {
		t.Fatalf("findMuseSessions failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1 (subagent transcript excluded)", len(sessions))
	}
	if sessions[0].Path != parentPath {
		t.Errorf("found %q, want the top-level transcript %q", sessions[0].Path, parentPath)
	}
}

func TestFindMuseSessions_MissingStore(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	sessions, err := findMuseSessions(t.TempDir())
	if err != nil {
		t.Fatalf("findMuseSessions failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("session count = %d, want 0 when Muse has never run", len(sessions))
	}
}

func TestFindMuseSessionPathByID(t *testing.T) {
	sessionsRoot := seedStore(t)
	project := t.TempDir()
	want := writeSession(t, sessionsRoot, "2026/08/07", basicSessionID, "session-basic.jsonl", project)

	tests := []struct {
		name      string
		sessionID string
		expected  string
	}{
		{name: "known id", sessionID: basicSessionID, expected: want},
		{name: "unknown id", sessionID: "no-such-session", expected: ""},
		{name: "empty id", sessionID: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findMuseSessionPathByID(tt.sessionID)
			if err != nil {
				t.Fatalf("findMuseSessionPathByID failed: %v", err)
			}
			if got != tt.expected {
				t.Errorf("path = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestIsSubagentTranscript(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{path: "/store/2026/08/07/sid/session.jsonl", expected: false},
		{path: "/store/2026/08/07/sid/subagent/abc/session.jsonl", expected: true},
		{path: "/store/2026/08/07/subagent-lookalike/session.jsonl", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsSubagentTranscript(tt.path); got != tt.expected {
				t.Errorf("IsSubagentTranscript(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}
