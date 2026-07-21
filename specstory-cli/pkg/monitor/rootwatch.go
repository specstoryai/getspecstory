package monitor

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

// WatchRootForNewEntries watches root for filesystem activity and invokes
// onCreate with the affected path until ctx is cancelled. It generalizes the
// provider-specific pattern in pkg/providers/claudecode/watcher.go without the
// hardcoded name matching.
//
// Two deliberate extensions beyond a literal single-directory Create watcher,
// both required for the monitor to see real agent activity:
//
//   - Subdirectories (existing and newly created) are watched too, because
//     fsnotify is not recursive and activity lands inside them: a new session
//     .jsonl appears inside an existing ~/.claude/projects/<dir>, Codex files
//     sessions under a YYYY/MM/DD chain that materializes over time, and Cursor
//     creates session dirs inside per-project hash dirs.
//
//   - Write events fire the callback as well as Create events, because agents
//     append to the same session file for the lifetime of a session. If only
//     Create counted, a reaped watch child would never be respawned while the
//     user keeps working in an already-created session.
//
// Callback errors/filtering are the caller's concern: onCreate is invoked for
// every event path and the caller's resolver decides what is interesting.
func WatchRootForNewEntries(ctx context.Context, root string, onCreate func(path string)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() {
		_ = watcher.Close() // Best-effort cleanup; nothing to do if closing fails.
	}()

	// Watch root and every existing subdirectory. Per-entry errors (permission
	// denied, entries vanishing mid-walk) are logged and skipped so one bad
	// directory can't take down the whole watch.
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing/unreadable root means nothing can ever be watched —
			// surface that instead of silently blocking forever. Deeper
			// unreadable entries are logged and skipped.
			if p == root {
				return err
			}
			slog.Warn("WatchRootForNewEntries: skipping unreadable entry", "path", p, "error", err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if addErr := watcher.Add(p); addErr != nil {
				slog.Warn("WatchRootForNewEntries: failed to watch directory", "path", p, "error", addErr)
			}
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	for {
		select {
		case <-ctx.Done():
			// Cancellation is the normal way to stop watching, not an error.
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			switch {
			case event.Has(fsnotify.Create):
				// A newly created directory must be watched immediately so
				// activity inside it (the next Create/Write) is not missed.
				if info, statErr := os.Stat(event.Name); statErr == nil && info.IsDir() {
					if addErr := watcher.Add(event.Name); addErr != nil {
						slog.Warn("WatchRootForNewEntries: failed to watch new directory", "path", event.Name, "error", addErr)
					}
				}
				onCreate(event.Name)
			case event.Has(fsnotify.Write):
				onCreate(event.Name)
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			// Watcher errors are usually transient (overflow, races); keep
			// watching rather than tearing down the whole monitor.
			slog.Warn("WatchRootForNewEntries: watcher error", "root", root, "error", watchErr)
		}
	}
}
