package provenance

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
	ignore "github.com/sabhiram/go-gitignore"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

const debounceWindow = 100 * time.Millisecond

// excludedDirs are directory basenames that should never be watched.
// Checked via filepath.Base for O(1) lookup.
var excludedDirs = map[string]bool{
	".git":          true,
	".specstory":    true,
	"node_modules":  true,
	".next":         true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	"vendor":        true,
	".idea":         true,
	".vscode":       true,
	"dist":          true,
	".claude":       true,
	".cursor":       true,
	".codex":        true,
	".aider":        true,
	".copilot":      true,
	".github":       true,
	".gradle":       true,
	".mvn":          true,
	"build":         true,
	"target":        true,
	".terraform":    true,
	".cache":        true,
	".tox":          true,
	".eggs":         true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	"coverage":      true,
}

// excludedExtensions are file extensions that should never generate events.
var excludedExtensions = map[string]bool{
	".exe":   true,
	".dll":   true,
	".so":    true,
	".dylib": true,
	".o":     true,
	".a":     true,
	".out":   true,
	".class": true,
	".jar":   true,
	".pyc":   true,
	".pyo":   true,
	".wasm":  true,
	".swp":   true,
	".swo":   true,
	".tmp":   true,
	".bak":   true,
	".log":   true,
}

// scopedIgnore pairs a directory with its compiled ignore patterns.
// Patterns only apply to paths under the scoped directory.
type scopedIgnore struct {
	dir     string
	matcher *ignore.GitIgnore
}

// FSWatcher watches the user's project directory for filesystem changes and
// pushes FileEvents to the provenance Engine for correlation with agent activity.
type FSWatcher struct {
	engine      *Engine
	rootDir     string
	watcher     *fsnotify.Watcher
	ignoreRules []scopedIgnore // scoped .gitignore/.intentignore matchers
	cancel      context.CancelFunc
	wg          sync.WaitGroup

	// Debounce: track last event time per path
	mu       sync.Mutex
	lastSeen map[string]time.Time
}

// NewFSWatcher creates a filesystem watcher rooted at rootDir that will push
// FileEvents to the given engine. It loads .gitignore files from the root and
// all subdirectories (plus .intentignore at root), then recursively adds all
// non-excluded directories. The watcher is not running yet — call Start to begin.
func NewFSWatcher(engine *Engine, rootDir string) (*FSWatcher, error) {
	// Canonicalize rootDir so fsnotify events use the true filesystem case.
	// On macOS (case-insensitive APFS) the CWD may have non-canonical casing
	// which would cause path suffix matching to fail against agent-reported paths.
	if canonical, err := spi.GetCanonicalPath(rootDir); err == nil {
		rootDir = canonical
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &FSWatcher{
		engine:   engine,
		rootDir:  rootDir,
		watcher:  watcher,
		lastSeen: make(map[string]time.Time),
	}

	// .intentignore is root-only (Intent-specific patterns)
	w.loadIgnoreFile(rootDir, ".intentignore")

	// Recursively add all non-excluded directories, loading .gitignore
	// files from each directory as we descend. WalkDir visits top-down,
	// so parent patterns are available before child directories are filtered.
	err = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip directories we can't read
		}
		if !d.IsDir() {
			return nil
		}
		if !w.shouldWatch(path, true) {
			return filepath.SkipDir
		}
		w.loadIgnoreFile(path, ".gitignore")
		if addErr := watcher.Add(path); addErr != nil {
			slog.Debug("Failed to watch directory", "path", path, "error", addErr)
		}
		return nil
	})
	if err != nil {
		_ = watcher.Close()
		return nil, err
	}

	slog.Info("FSWatcher initialized", "rootDir", rootDir)

	return w, nil
}

// loadIgnoreFile reads an ignore file (e.g. ".gitignore", ".intentignore") from
// dir and appends its patterns as a scoped rule. No-op if the file doesn't exist.
func (w *FSWatcher) loadIgnoreFile(dir, filename string) {
	filePath := filepath.Join(dir, filename)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	lines := strings.Split(string(content), "\n")
	matcher := ignore.CompileIgnoreLines(lines...)
	w.ignoreRules = append(w.ignoreRules, scopedIgnore{dir: dir, matcher: matcher})
}

// Start begins the event loop in a background goroutine.
// The watcher runs until Stop is called or the context is cancelled.
func (w *FSWatcher) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go w.eventLoop(ctx)
	slog.Info("FSWatcher started", "rootDir", w.rootDir)
}

// Stop cancels the event loop, waits for it to finish, and closes the watcher.
func (w *FSWatcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	_ = w.watcher.Close()
	slog.Info("FSWatcher stopped", "rootDir", w.rootDir)
}

// eventLoop processes fsnotify events until the context is cancelled.
func (w *FSWatcher) eventLoop(ctx context.Context) {
	defer w.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(ctx, event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Debug("FSWatcher error", "error", err)
		}
	}
}

// handleEvent processes a single fsnotify event, filtering and debouncing
// before pushing a FileEvent to the engine.
func (w *FSWatcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	path := event.Name

	// Determine if this is a directory (for create events, new dirs need watching)
	info, statErr := os.Stat(path)
	isDir := statErr == nil && info.IsDir()

	if !w.shouldWatch(path, isDir) {
		return
	}

	// For new directories, load any .gitignore and add to the watcher
	if isDir && event.Has(fsnotify.Create) {
		w.loadIgnoreFile(path, ".gitignore")
		if addErr := w.watcher.Add(path); addErr != nil {
			slog.Debug("Failed to watch new directory", "path", path, "error", addErr)
		}
		return // directories themselves don't produce FileEvents
	}

	// Only file events from here on
	if isDir {
		return
	}

	changeType := mapChangeType(event.Op)
	if changeType == "" {
		return
	}

	if w.shouldDebounce(path) {
		return
	}

	// Determine timestamp: use file ModTime when possible, fall back to now
	var timestamp time.Time
	if statErr == nil {
		timestamp = info.ModTime()
	} else {
		// File was deleted/renamed — can't stat it
		timestamp = time.Now()
	}

	fileEvent := FileEvent{
		ID:         uuid.New().String(),
		Path:       path,
		ChangeType: changeType,
		Timestamp:  timestamp,
	}

	if _, err := w.engine.PushFileEvent(ctx, fileEvent); err != nil {
		slog.Debug("Failed to push file event",
			"path", path,
			"changeType", changeType,
			"error", err)
	}
}

// mapChangeType converts fsnotify operations to provenance change types.
func mapChangeType(op fsnotify.Op) string {
	switch {
	case op.Has(fsnotify.Create):
		return "create"
	case op.Has(fsnotify.Write):
		return "modify"
	case op.Has(fsnotify.Remove):
		return "delete"
	case op.Has(fsnotify.Rename):
		return "rename"
	default:
		return ""
	}
}

// shouldDebounce returns true if the same path had an event less than
// debounceWindow ago, preventing duplicate events from rapid writes.
func (w *FSWatcher) shouldDebounce(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if last, ok := w.lastSeen[path]; ok && now.Sub(last) < debounceWindow {
		return true
	}
	w.lastSeen[path] = now
	return false
}

// shouldWatch returns true if the given path should be watched/produce events.
// Checks are ordered from cheapest to most expensive.
func (w *FSWatcher) shouldWatch(path string, isDir bool) bool {
	base := filepath.Base(path)

	// Hardcoded directory exclusions
	if isDir && excludedDirs[base] {
		return false
	}

	// Hidden files/directories (starts with .)
	if strings.HasPrefix(base, ".") {
		return false
	}

	// Hardcoded file extension exclusions
	if !isDir {
		ext := strings.ToLower(filepath.Ext(base))
		if excludedExtensions[ext] {
			return false
		}
	}

	// Scoped ignore rules (.gitignore from each directory, .intentignore from root).
	// Each rule only applies to paths under its directory.
	for _, rule := range w.ignoreRules {
		relPath, err := filepath.Rel(rule.dir, path)
		if err != nil || strings.HasPrefix(relPath, "..") {
			continue // path is not under this rule's directory
		}
		if isDir {
			relPath = relPath + "/"
		}
		if rule.matcher.MatchesPath(relPath) {
			return false
		}
	}

	return true
}
