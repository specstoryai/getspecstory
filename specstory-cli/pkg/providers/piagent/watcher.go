package piagent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Package-global watcher state, shared by `run` (ExecAgentAndWatch) and `watch`
// (WatchAgent). Shape mirrors the sibling providers' watchers exactly.
var (
	watcherCtx      context.Context
	watcherCancel   context.CancelFunc
	watcherWg       sync.WaitGroup
	watcherCallback func(*spi.AgentChatSession) // invoked for each parsed session update
	watcherDebugRaw bool                        // whether to write debug-raw artifacts while watching
	watcherMutex    sync.RWMutex                // protects watcherCallback and watcherDebugRaw
)

func init() {
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
}

// SetWatcherCallback sets the callback invoked for each session update.
func SetWatcherCallback(callback func(*spi.AgentChatSession)) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = callback
	slog.Info("pi: watcher callback set", "isNil", callback == nil)
}

// ClearWatcherCallback clears the session-update callback.
func ClearWatcherCallback() {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = nil
	slog.Info("pi: watcher callback cleared")
}

// SetWatcherDebugRaw toggles debug-raw artifact writing during watch.
func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
	slog.Debug("pi: watcher debug-raw set", "debugRaw", debugRaw)
}

// getWatcherDebugRaw returns the current debug-raw setting (thread-safe).
func getWatcherDebugRaw() bool {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherDebugRaw
}

// getWatcherCallback returns the current callback (thread-safe).
func getWatcherCallback() func(*spi.AgentChatSession) {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherCallback
}

// StopWatcher cancels the watch context and waits for the watch goroutine to
// finish. Both `run` and `watch` rely on this graceful join before returning.
func StopWatcher() {
	slog.Info("pi: signaling watcher to stop")
	watcherCancel()
	watcherWg.Wait()
	slog.Info("pi: watcher stopped")
}

// WatchForProjectDir starts watching the pi session directory for the given
// project and dispatches each parsed session to the registered callback as pi
// writes JSONL. It is shared by `run` and `watch`; both register a callback
// first, then call this.
//
// The watch context is re-armed here so a prior StopWatcher (which cancels the
// context) does not immediately terminate a subsequent watch. run/watch each
// start exactly one watch session per process; tests start several.
func WatchForProjectDir(projectPath string) error {
	root, flat, err := piSessionsRoot()
	if err != nil {
		return fmt.Errorf("pi: resolving sessions root: %w", err)
	}
	targetDir, err := ProjectSessionDir(projectPath)
	if err != nil {
		return fmt.Errorf("pi: resolving project session dir: %w", err)
	}
	candidates, err := projectCandidates(projectPath)
	if err != nil {
		return fmt.Errorf("pi: resolving project candidates: %w", err)
	}

	watcherMutex.Lock()
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
	ctx := watcherCtx
	watcherMutex.Unlock()

	return startPiWatcher(ctx, targetDir, root, flat, candidates)
}

// startPiWatcher creates the fsnotify watcher and runs the event loop in a
// wait-group goroutine (wg.Go per CLAUDE.md). pi keeps a project's *.jsonl files
// flat in a single directory (the encoded-cwd dir, or the flat override root),
// so a single directory watch — not per-file or hierarchical date-dir watching
// like codex — captures every create/write of a child session file. If the
// target directory does not exist yet (pi has not written to this project), the
// goroutine first waits for it to appear by watching the nearest existing
// ancestor.
func startPiWatcher(ctx context.Context, targetDir, root string, flat bool, candidates []string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("pi: creating file watcher: %w", err)
	}

	watcherWg.Go(func() {
		defer func() {
			// Best-effort cleanup; a close error here is not recoverable.
			_ = watcher.Close()
		}()

		if !awaitTargetDir(ctx, watcher, targetDir) {
			return // context cancelled before the directory appeared
		}

		if err := watcher.Add(targetDir); err != nil {
			log.UserError("pi: failed to watch session directory: %v", err)
			slog.Error("pi: failed to watch session directory", "directory", targetDir, "error", err)
			return
		}
		slog.Info("pi: watching session directory", "directory", targetDir, "flat", flat)

		for {
			select {
			case <-ctx.Done():
				slog.Info("pi: watch context cancelled")
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if !strings.HasSuffix(event.Name, ".jsonl") {
					continue // only pi session files
				}
				// A removed/renamed file has nothing to emit; the next create/write
				// of a successor produces its own event.
				if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					continue
				}
				if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
					continue
				}
				slog.Info("pi: session file changed", "file", event.Name, "op", event.Op.String())
				emitSession(event.Name, flat, candidates)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.UserError("pi: watcher error: %v", err)
				slog.Error("pi: watcher error", "error", err)
			}
		}
	})

	return nil
}

// awaitTargetDir blocks until targetDir exists, watching its nearest existing
// ancestor and re-checking on each filesystem event as intermediate directories
// are created (pi lazily creates ~/.pi/agent/sessions/--<cwd>-- on first write).
// Returns true when the directory exists, false if the context is cancelled
// first. Keeping this minimal: it re-stats after each event rather than tracking
// exact create names, which is enough for a directory that appears once.
func awaitTargetDir(ctx context.Context, watcher *fsnotify.Watcher, targetDir string) bool {
	for {
		if _, err := os.Stat(targetDir); err == nil {
			return true
		}
		ancestor := nearestExistingAncestor(targetDir)
		if err := watcher.Add(ancestor); err != nil {
			slog.Error("pi: failed to watch ancestor directory while bootstrapping", "ancestor", ancestor, "error", err)
			return false
		}
		// Re-check after adding the watch so a directory created during Add is not
		// missed while we block on the next event.
		if _, err := os.Stat(targetDir); err == nil {
			_ = watcher.Remove(ancestor)
			return true
		}
		slog.Info("pi: session directory not present yet, watching ancestor", "target", targetDir, "ancestor", ancestor)
		select {
		case <-ctx.Done():
			return false
		case _, ok := <-watcher.Events:
			// Drop the ancestor watch and loop: the re-stat at the top either
			// resolves the target or re-adds the (now deeper) nearest ancestor.
			_ = watcher.Remove(ancestor)
			if !ok {
				return false
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return false
			}
			slog.Debug("pi: watcher error while bootstrapping directory", "error", err)
		}
	}
}

// nearestExistingAncestor walks up from dir until it finds a directory that
// exists, so the bootstrap watcher always has a live directory to watch.
func nearestExistingAncestor(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir // reached the filesystem root
		}
		dir = parent
	}
}

// emitSession parses one changed pi session file and dispatches it to the
// callback. Parsing reuses the existing read path (parseToAgentSession →
// ParseSession), so slug/name derivation, latest session_info rename, active
// leaf-path selection, and partial-line tolerance all come for free. A parse
// error or nil session (empty/header-only file, or a truncated trailing line
// mid-write) is treated as "nothing to emit yet" and skipped — the next write
// event re-parses. In the flat PI_CODING_AGENT_SESSION_DIR layout, sessions for
// every project share one directory, so files are filtered by header cwd first.
func emitSession(path string, flat bool, candidates []string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("pi: emitSession panicked", "path", path, "panic", r)
		}
	}()

	callback := getWatcherCallback()
	if callback == nil {
		slog.Debug("pi: no watcher callback set; skipping session", "path", path)
		return
	}

	if flat {
		h, err := readHeader(path)
		if err != nil || h == nil || !cwdMatchesCandidate(h.Cwd, candidates) {
			return // not a session for this project (or not a session file yet)
		}
	}

	chat, err := parseToAgentSession(path, getWatcherDebugRaw())
	if err != nil || chat == nil {
		slog.Debug("pi: nothing to emit yet for session", "path", path, "error", err)
		return
	}

	// Dispatch in a recover-guarded goroutine so a slow or panicking callback
	// never blocks the watch loop (mirrors the sibling providers).
	go func(s *spi.AgentChatSession) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("pi: watcher callback panicked", "sessionId", s.SessionID, "panic", r)
			}
		}()
		callback(s)
	}(chat)
}

// cwdMatchesCandidate reports whether a session header cwd matches one of the
// project's candidate working-directory forms (raw and symlink-resolved).
func cwdMatchesCandidate(cwd string, candidates []string) bool {
	for _, c := range candidates {
		if cwd == c {
			return true
		}
	}
	return false
}
