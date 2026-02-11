package provenance

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestShouldWatch(t *testing.T) {
	// Create a minimal FSWatcher (no real fsnotify watcher needed for filter tests)
	w := &FSWatcher{
		rootDir:  "/tmp/test-project",
		lastSeen: make(map[string]time.Time),
	}

	tests := []struct {
		name   string
		path   string
		isDir  bool
		expect bool
	}{
		// Excluded directories
		{".git dir", "/tmp/test-project/.git", true, false},
		{"node_modules dir", "/tmp/test-project/node_modules", true, false},
		{".specstory dir", "/tmp/test-project/.specstory", true, false},
		{"vendor dir", "/tmp/test-project/vendor", true, false},
		{".vscode dir", "/tmp/test-project/.vscode", true, false},
		{"__pycache__ dir", "/tmp/test-project/__pycache__", true, false},
		{".next dir", "/tmp/test-project/.next", true, false},
		{"dist dir", "/tmp/test-project/dist", true, false},

		// Allowed directories
		{"src dir", "/tmp/test-project/src", true, true},
		{"pkg dir", "/tmp/test-project/pkg", true, true},
		{"lib dir", "/tmp/test-project/lib", true, true},

		// Hidden files (starts with .)
		{".DS_Store file", "/tmp/test-project/.DS_Store", false, false},
		{".env file", "/tmp/test-project/.env", false, false},
		{".gitignore file", "/tmp/test-project/.gitignore", false, false},

		// Excluded extensions
		{"exe file", "/tmp/test-project/main.exe", false, false},
		{"dll file", "/tmp/test-project/lib.dll", false, false},
		{"so file", "/tmp/test-project/lib.so", false, false},
		{"pyc file", "/tmp/test-project/module.pyc", false, false},
		{"swp file", "/tmp/test-project/.main.go.swp", false, false},
		{"log file", "/tmp/test-project/debug.log", false, false},
		{"tmp file", "/tmp/test-project/scratch.tmp", false, false},

		// Allowed files
		{"go file", "/tmp/test-project/main.go", false, true},
		{"ts file", "/tmp/test-project/src/index.ts", false, true},
		{"py file", "/tmp/test-project/app.py", false, true},
		{"json file", "/tmp/test-project/package.json", false, true},
		{"md file", "/tmp/test-project/README.md", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.shouldWatch(tt.path, tt.isDir)
			if got != tt.expect {
				t.Errorf("shouldWatch(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
			}
		})
	}
}

func TestShouldWatch_Gitignore(t *testing.T) {
	// Create a temp directory with a .gitignore
	tmpDir := t.TempDir()

	gitignoreContent := "build/\n*.generated.go\noutput.txt\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &FSWatcher{
		rootDir:  tmpDir,
		lastSeen: make(map[string]time.Time),
	}
	w.loadIgnoreFile(tmpDir, ".gitignore")

	if len(w.ignoreRules) == 0 {
		t.Fatal("expected ignore rules to be loaded")
	}

	tests := []struct {
		name   string
		path   string
		isDir  bool
		expect bool
	}{
		{"build dir matches gitignore", filepath.Join(tmpDir, "build"), true, false},
		{"src dir allowed", filepath.Join(tmpDir, "src"), true, true},
		{"generated go file matches gitignore", filepath.Join(tmpDir, "foo.generated.go"), false, false},
		{"regular go file allowed", filepath.Join(tmpDir, "foo.go"), false, true},
		{"output.txt matches gitignore", filepath.Join(tmpDir, "output.txt"), false, false},
		{"input.txt allowed", filepath.Join(tmpDir, "input.txt"), false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.shouldWatch(tt.path, tt.isDir)
			if got != tt.expect {
				t.Errorf("shouldWatch(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
			}
		})
	}
}

func TestShouldWatch_Intentignore(t *testing.T) {
	// Verify .intentignore patterns are also loaded
	tmpDir := t.TempDir()

	intentignoreContent := "secrets/\n*.secret\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".intentignore"), []byte(intentignoreContent), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &FSWatcher{
		rootDir:  tmpDir,
		lastSeen: make(map[string]time.Time),
	}
	w.loadIgnoreFile(tmpDir, ".intentignore")

	if len(w.ignoreRules) == 0 {
		t.Fatal("expected ignore rules to be loaded from .intentignore")
	}

	tests := []struct {
		name   string
		path   string
		isDir  bool
		expect bool
	}{
		{"secrets dir matches intentignore", filepath.Join(tmpDir, "secrets"), true, false},
		{"secret file matches intentignore", filepath.Join(tmpDir, "api.secret"), false, false},
		{"regular file allowed", filepath.Join(tmpDir, "config.yaml"), false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.shouldWatch(tt.path, tt.isDir)
			if got != tt.expect {
				t.Errorf("shouldWatch(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
			}
		})
	}
}

func TestFSWatcher_FileEvents(t *testing.T) {
	// Create a temp directory to watch
	tmpDir := t.TempDir()

	// Create a provenance engine with a temp DB
	dbPath := filepath.Join(t.TempDir(), "test-provenance.db")
	engine, err := NewEngine(WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()

	// Create and start the watcher
	w, err := NewFSWatcher(engine, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	w.Start(ctx)
	defer w.Stop()

	// Create a file in the watched directory
	testFile := filepath.Join(tmpDir, "hello.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give fsnotify and the event loop time to process
	time.Sleep(300 * time.Millisecond)

	// Query unmatched file events from the store — should have at least one
	since := time.Now().Add(-10 * time.Second)
	until := time.Now().Add(10 * time.Second)
	events, err := engine.store.QueryUnmatchedFileEvents(ctx, since, until)
	if err != nil {
		t.Fatal(err)
	}

	if len(events) == 0 {
		t.Error("expected at least one file event to be pushed to the engine")
	}

	// Verify the event path matches
	found := false
	for _, e := range events {
		if e.FilePath == testFile {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find file event for %s, got events: %v", testFile, events)
	}
}

func TestFSWatcher_ExcludedDirs(t *testing.T) {
	// Create a temp directory with an excluded subdirectory
	tmpDir := t.TempDir()
	nodeModules := filepath.Join(tmpDir, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "test-provenance.db")
	engine, err := NewEngine(WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()

	w, err := NewFSWatcher(engine, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	w.Start(ctx)
	defer w.Stop()

	// Create a file inside node_modules (should be excluded)
	excludedFile := filepath.Join(nodeModules, "package.json")
	if err := os.WriteFile(excludedFile, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give fsnotify time to process
	time.Sleep(300 * time.Millisecond)

	// Query file events — should have none
	since := time.Now().Add(-10 * time.Second)
	until := time.Now().Add(10 * time.Second)
	events, err := engine.store.QueryUnmatchedFileEvents(ctx, since, until)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range events {
		if e.FilePath == excludedFile {
			t.Errorf("did not expect file event for excluded path %s", excludedFile)
		}
	}
}

func TestFSWatcher_Debounce(t *testing.T) {
	tmpDir := t.TempDir()

	dbPath := filepath.Join(t.TempDir(), "test-provenance.db")
	engine, err := NewEngine(WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engine.Close() }()

	w, err := NewFSWatcher(engine, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	w.Start(ctx)
	defer w.Stop()

	// Create a file, then write to it again rapidly
	testFile := filepath.Join(tmpDir, "rapid.txt")
	if err := os.WriteFile(testFile, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write again immediately (within debounce window)
	if err := os.WriteFile(testFile, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give fsnotify time to process
	time.Sleep(300 * time.Millisecond)

	// Query file events — debouncing should have collapsed rapid writes
	since := time.Now().Add(-10 * time.Second)
	until := time.Now().Add(10 * time.Second)
	events, err := engine.store.QueryUnmatchedFileEvents(ctx, since, until)
	if err != nil {
		t.Fatal(err)
	}

	// Count events for our test file
	count := 0
	for _, e := range events {
		if e.FilePath == testFile {
			count++
		}
	}

	// We should have exactly 1 event (create), with the rapid write debounced away
	if count != 1 {
		t.Errorf("expected 1 file event after debouncing, got %d", count)
	}
}

func TestShouldDebounce(t *testing.T) {
	w := &FSWatcher{
		lastSeen: make(map[string]time.Time),
	}

	// First call should not debounce
	if w.shouldDebounce("/foo/bar.go") {
		t.Error("first call should not debounce")
	}

	// Immediate second call should debounce
	if !w.shouldDebounce("/foo/bar.go") {
		t.Error("rapid second call should debounce")
	}

	// Different path should not debounce
	if w.shouldDebounce("/foo/baz.go") {
		t.Error("different path should not debounce")
	}
}

func TestMapChangeType(t *testing.T) {
	tests := []struct {
		name   string
		op     fsnotify.Op
		expect string
	}{
		{"create", fsnotify.Create, "create"},
		{"write", fsnotify.Write, "modify"},
		{"remove", fsnotify.Remove, "delete"},
		{"rename", fsnotify.Rename, "rename"},
		{"chmod", fsnotify.Chmod, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapChangeType(tt.op)
			if got != tt.expect {
				t.Errorf("mapChangeType(%v) = %q, want %q", tt.op, got, tt.expect)
			}
		})
	}
}

func TestLoadIgnoreFile_NoFile(t *testing.T) {
	tmpDir := t.TempDir()

	// No .gitignore → should not add any rules
	w := &FSWatcher{rootDir: tmpDir, lastSeen: make(map[string]time.Time)}
	w.loadIgnoreFile(tmpDir, ".gitignore")
	if len(w.ignoreRules) != 0 {
		t.Error("expected no ignore rules when file doesn't exist")
	}
}

func TestShouldWatch_NestedGitignore(t *testing.T) {
	// Verify that a .gitignore in a subdirectory only affects paths under that subdirectory
	tmpDir := t.TempDir()

	subDir := filepath.Join(tmpDir, "services", "api")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Root .gitignore ignores *.log everywhere
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Nested .gitignore in services/api/ ignores generated/ and *.gen
	// (using .gen not .tmp because .tmp is in the hardcoded excludedExtensions)
	if err := os.WriteFile(filepath.Join(subDir, ".gitignore"), []byte("generated/\n*.gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &FSWatcher{
		rootDir:  tmpDir,
		lastSeen: make(map[string]time.Time),
	}
	w.loadIgnoreFile(tmpDir, ".gitignore")
	w.loadIgnoreFile(subDir, ".gitignore")

	tests := []struct {
		name   string
		path   string
		isDir  bool
		expect bool
	}{
		// Root .gitignore applies everywhere
		{"root log excluded at root", filepath.Join(tmpDir, "app.log"), false, false},
		{"root log excluded in subdir", filepath.Join(subDir, "server.log"), false, false},

		// Nested .gitignore only applies under services/api/
		{"nested generated dir excluded under api", filepath.Join(subDir, "generated"), true, false},
		{"nested tmp file excluded under api", filepath.Join(subDir, "cache.gen"), false, false},

		// Nested patterns must NOT affect paths outside their directory
		{"nested pattern does not affect root", filepath.Join(tmpDir, "cache.gen"), false, true},
		{"nested pattern does not affect sibling", filepath.Join(tmpDir, "services", "web", "cache.gen"), false, true},
		{"nested generated dir allowed at root", filepath.Join(tmpDir, "generated"), true, true},

		// Normal files still allowed
		{"go file in subdir allowed", filepath.Join(subDir, "main.go"), false, true},
		{"go file at root allowed", filepath.Join(tmpDir, "main.go"), false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.shouldWatch(tt.path, tt.isDir)
			if got != tt.expect {
				t.Errorf("shouldWatch(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
			}
		})
	}
}
