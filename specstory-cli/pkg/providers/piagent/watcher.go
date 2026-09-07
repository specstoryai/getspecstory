package piagent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	callbackWg      sync.WaitGroup              // in-flight callback goroutines started by emitSession
	watcherCallback func(*spi.AgentChatSession) // invoked for each parsed session update
	watcherDebugRaw bool                        // whether to write debug-raw artifacts while watching
	watcherTarget   *watchTarget                // the directory the current watch follows; nil when none was started
	watcherMutex    sync.RWMutex                // protects watcherCallback, watcherDebugRaw and watcherTarget
)

// watchTarget describes the directory a watch follows. WatchForProjectDir sets
// it under watcherMutex; the watch loop and StopWatcher read it for the sweeps.
type watchTarget struct {
	dir        string    // the directory pi writes this project's *.jsonl files into
	flat       bool      // flat PI_CODING_AGENT_SESSION_DIR layout (filter strictly by header cwd)
	candidates []string  // project cwd forms a session header may carry
	startedAt  time.Time // when the watch started; the sweeps report only files modified since
}

// fileStamp identifies one version of a session file on disk.
type fileStamp struct {
	size  int64
	mtime time.Time
}

// emittedStamps records, per session file path, the stamp of the version last
// handed to the callback. emitSession writes it when it dispatches; the sweeps
// read it to skip a file whose current version has already been emitted.
// WatchForProjectDir resets it when a watch starts.
var (
	emittedMutex  sync.Mutex
	emittedStamps = make(map[string]fileStamp)
)

// callbackDrainTimeout bounds how long StopWatcher waits for in-flight
// callbacks (markdown write, cloud sync) after the watch loop has exited. The
// bound keeps a stuck callback from hanging `run` or `watch` exit forever; a
// callback that outlives it is logged and abandoned, and the next `sync pi`
// picks the session up from disk. Tests shorten it.
var callbackDrainTimeout = 10 * time.Second

// callbackWaitDone is closed once the goroutine the most recent StopWatcher
// used to wait on callbackWg has seen the count reach zero. StopWatcher gives
// up on that goroutine after callbackDrainTimeout, but it stays parked in Wait
// until the stuck callback finishes, and a WaitGroup must not be reused while a
// Wait is in progress. run and watch start one watch per process and exit after
// StopWatcher, so nothing there reuses it; the test that lets a callback outlive
// the timeout waits on this channel before the next test dispatches a callback.
var callbackWaitDone <-chan struct{}

// sweepStartGrace is how far before startedAt a file's mtime may fall and still
// count as activity of this watch. Kernels stamp mtimes from a coarse clock
// that can lag time.Now by a few milliseconds, and some filesystems store
// whole-second or two-second mtimes, so a file written right after the watch
// started can carry an mtime that reads as slightly before it.
const sweepStartGrace = 2 * time.Second

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

// takeWatcherTarget returns the current watch target and clears it, so a later
// StopWatcher without a new watch has nothing to sweep.
func takeWatcherTarget() *watchTarget {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	target := watcherTarget
	watcherTarget = nil
	return target
}

// StopWatcher cancels the watch context, waits for the watch goroutine to
// finish, sweeps the watched directory once more, then waits (bounded by
// callbackDrainTimeout) for every callback dispatched. Both `run` and `watch`
// rely on this graceful join before returning: pi writes its session file right
// before it exits, and the callback for that write is what saves the markdown.
// Returning before it has run loses the session until the next `sync pi`.
//
// The sweep is what makes the final write safe. fsnotify's Events channel is
// unbuffered and the kernel may not have handed the loop the event for pi's
// last write by the time the context is cancelled, so the loop's non-blocking
// drain can come up empty. The sweep reads the directory instead of the event
// queue and emits every file changed since the watch started that has not been
// emitted in its current version. When no watch was started there is nothing
// to sweep.
//
// The callback join happens after the sweep so no new callbackWg.Go can race
// the Wait: emitSession runs from the watch loop, which has exited by then, and
// from the sweep on this goroutine.
func StopWatcher() {
	slog.Info("pi: signaling watcher to stop")
	watcherCancel()
	watcherWg.Wait()
	if target := takeWatcherTarget(); target != nil {
		rescanWatchedDir(target)
	}
	select {
	case <-waitForCallbacks():
	case <-time.After(callbackDrainTimeout):
		slog.Warn("pi: gave up waiting for in-flight session callbacks", "timeout", callbackDrainTimeout)
	}
	slog.Info("pi: watcher stopped")
}

// waitForCallbacks starts a goroutine that waits for callbackWg to reach zero
// and returns the channel it closes when that happens, recorded as
// callbackWaitDone. The goroutine exits on its own once the count is zero, so
// a caller that stops waiting does not leak it forever.
func waitForCallbacks() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		callbackWg.Wait()
		close(done)
	}()
	watcherMutex.Lock()
	callbackWaitDone = done
	watcherMutex.Unlock()
	return done
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
	_, flat, err := piSessionsRoot()
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
	target := &watchTarget{dir: targetDir, flat: flat, candidates: candidates, startedAt: time.Now()}

	resetEmittedStamps()
	watcherMutex.Lock()
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
	ctx := watcherCtx
	watcherTarget = target
	watcherMutex.Unlock()

	if err := startPiWatcher(ctx, target); err != nil {
		takeWatcherTarget()
		return err
	}
	return nil
}

// startPiWatcher creates the fsnotify watcher and runs the event loop in a
// wait-group goroutine (wg.Go per CLAUDE.md). pi keeps a project's *.jsonl files
// flat in a single directory (the encoded-cwd dir, or the flat override root),
// so a single directory watch — not per-file or hierarchical date-dir watching
// like codex — captures every create/write of a child session file. If the
// target directory does not exist yet (pi has not written to this project), the
// goroutine first waits for it to appear by watching the nearest existing
// ancestor.
func startPiWatcher(ctx context.Context, target *watchTarget) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("pi: creating file watcher: %w", err)
	}

	watcherWg.Go(func() {
		defer func() {
			// Best-effort cleanup; a close error here is not recoverable.
			_ = watcher.Close()
		}()

		if !awaitTargetDir(ctx, watcher, target.dir) {
			return // context cancelled before the directory appeared
		}

		if err := watcher.Add(target.dir); err != nil {
			log.UserError("pi: failed to watch session directory: %v", err)
			slog.Error("pi: failed to watch session directory", "directory", target.dir, "error", err)
			return
		}
		slog.Info("pi: watching session directory", "directory", target.dir, "flat", target.flat)
		// Files written between the directory appearing (or the watch starting)
		// and the Add above produced no event on this watch. An agent that
		// creates the directory and writes a whole file at once lands here.
		rescanWatchedDir(target)

		for {
			select {
			case <-ctx.Done():
				slog.Info("pi: watch context cancelled")
				// pi writes its session file and exits in the same instant, so an
				// event for that write can already be queued when the context is
				// cancelled. Hand those to the callback before leaving instead of
				// dropping them on the floor. StopWatcher sweeps the directory
				// afterwards for a write whose event never made it this far.
				drainPendingEvents(watcher, target.flat, target.candidates)
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				handleEvent(event, target.flat, target.candidates)
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

// handleEvent filters one fsnotify event down to a create/write of a pi session
// file and emits the session for it.
func handleEvent(event fsnotify.Event, flat bool, candidates []string) {
	if !strings.HasSuffix(event.Name, ".jsonl") {
		return // only pi session files
	}
	// A removed/renamed file has nothing to emit; the next create/write
	// of a successor produces its own event.
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		return
	}
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}
	slog.Info("pi: session file changed", "file", event.Name, "op", event.Op.String())
	emitSession(event.Name, flat, candidates)
}

// drainPendingEvents handles every event already waiting on the watcher
// channel without blocking, so a write that landed just before cancellation
// still reaches the callback.
func drainPendingEvents(watcher *fsnotify.Watcher, flat bool, candidates []string) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			handleEvent(event, flat, candidates)
		default:
			return
		}
	}
}

// rescanWatchedDir lists the *.jsonl files directly in the watched directory
// (not recursive; pi keeps a project's sessions flat) and emits every file that
// events may have missed. A file whose mtime is older than the watch start
// (less sweepStartGrace) is left alone: watch reports new activity only, the
// same rule the CLI's watch command follows, and older sessions belong to
// `sync pi`. A file whose current (size, mtime) equals the stamp recorded at
// its last emit is skipped as already delivered. Everything else goes through
// emitSession, which stays the single place that parses and dispatches, so the
// cwd filter and the partial-file tolerance apply here as well.
func rescanWatchedDir(target *watchTarget) {
	files, err := jsonlFilesInDir(target.dir)
	if err != nil {
		slog.Error("pi: failed to list session directory for sweep", "directory", target.dir, "error", err)
		return
	}
	cutoff := target.startedAt.Add(-sweepStartGrace)
	for _, path := range files {
		stamp, ok := stampFile(path)
		if !ok {
			continue // removed between the listing and the stat
		}
		if stamp.mtime.Before(cutoff) {
			continue
		}
		if last, seen := lastEmittedStamp(path); seen && last == stamp {
			continue
		}
		slog.Info("pi: session file found by sweep", "file", path)
		emitSession(path, target.flat, target.candidates)
	}
}

// stampFile returns the current (size, mtime) of the file at path.
func stampFile(path string) (fileStamp, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{size: info.Size(), mtime: info.ModTime()}, true
}

// lastEmittedStamp returns the stamp recorded when path was last emitted.
func lastEmittedStamp(path string) (fileStamp, bool) {
	emittedMutex.Lock()
	defer emittedMutex.Unlock()
	stamp, ok := emittedStamps[path]
	return stamp, ok
}

// recordEmittedStamp remembers the version of path handed to the callback.
func recordEmittedStamp(path string, stamp fileStamp) {
	emittedMutex.Lock()
	defer emittedMutex.Unlock()
	emittedStamps[path] = stamp
}

// resetEmittedStamps forgets every recorded stamp; a new watch starts clean.
func resetEmittedStamps() {
	emittedMutex.Lock()
	defer emittedMutex.Unlock()
	emittedStamps = make(map[string]fileStamp)
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
// event re-parses. Files are filtered by header cwd first in both layouts: in
// the flat PI_CODING_AGENT_SESSION_DIR layout sessions for every project share
// one directory, and in the default layout the encoded directory can be shared
// by projects whose paths collide under EncodeCwd (/a-b and /a/b). Only the
// flat layout is strict; the default layout keeps a file whose header has no
// cwd yet (see headerBelongsToProject).
//
// The file is stamped before it is parsed. A write that lands between the stat
// and the parse then leaves a stamp older than the file, and the next sweep
// emits the file again rather than skipping content nobody delivered.
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

	if !sessionFileBelongsToProject(path, candidates, flat) {
		return // not a session for this project (or, when flat, not a session file yet)
	}

	stamp, stamped := stampFile(path)
	chat, err := parseToAgentSession(path, getWatcherDebugRaw())
	if err != nil || chat == nil {
		slog.Debug("pi: nothing to emit yet for session", "path", path, "error", err)
		return
	}
	if stamped {
		recordEmittedStamp(path, stamp)
	}

	// Dispatch in a recover-guarded goroutine so a slow or panicking callback
	// never blocks the watch loop (mirrors the sibling providers). The goroutine
	// is tracked in callbackWg so StopWatcher can join it: without the join,
	// `run pi` returned as soon as pi exited and the save for pi's final write
	// never ran.
	callbackWg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("pi: watcher callback panicked", "sessionId", chat.SessionID, "panic", r)
			}
		}()
		callback(chat)
	})
}
