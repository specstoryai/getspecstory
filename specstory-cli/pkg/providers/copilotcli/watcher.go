package copilotcli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Watcher timing. Package variables so tests can shorten them.
var (
	// debounceDelay collapses the burst of appends one streamed reply makes
	// into a single read, taken after the burst's last write.
	debounceDelay = 300 * time.Millisecond

	// maxBurstDelay bounds how long a continuous burst (a long agent turn) can
	// postpone a read, so the markdown keeps growing while Copilot works.
	maxBurstDelay = 2 * time.Second

	// reconcileInterval is the periodic re-scan that discovers new sessions
	// (the sessions root is deliberately not watched; see sessionWatcher) and
	// recovers from missed filesystem events.
	reconcileInterval = 3 * time.Second
)

// watcherLabel names this watcher in callback panic logs.
const watcherLabel = "Copilot CLI watcher"

// fileStamp is the change-detection key for a transcript: Copilot only ever
// appends to events.jsonl, so any new record moves its size and mtime.
type fileStamp struct {
	size    int64
	modTime time.Time
}

func stampOf(file sessionFile) fileStamp {
	return fileStamp{size: file.Size, modTime: file.ModTime}
}

// sessionWatcher follows one project's Copilot CLI sessions and delivers each
// new or changed session, in order, on a single worker.
//
// The watch is on individual transcript files, not directories: on macOS
// fsnotify's kqueue backend opens a descriptor for every entry of a watched
// directory, so watching session-state would hold one per session ever
// recorded (and each session directory holds a dozen entries of its own).
// Only this project's transcripts written inside spi.WatchWindowDays are
// watched, one descriptor each; the reconcile scan discovers new sessions and
// covers everything else.
type sessionWatcher struct {
	projectRoot string
	debugRaw    bool
	callback    func(*spi.AgentChatSession)
	fsWatcher   *fsnotify.Watcher

	// The fields below are owned by the worker goroutine after start.
	// watched holds the transcript files under an fsnotify watch.
	watched map[string]bool
	// known holds the stamp last delivered (or baselined) per transcript.
	known map[string]fileStamp
	// owned caches whether a transcript belongs to the project; a session's
	// starting directory never changes, so it is read once.
	owned map[string]bool

	// The startup boundary, fixed before the worker starts. startTime splits
	// history from activity; storeAtStartup and startupDirs tell a session
	// that existed at startup but was unreadable then (history, once it can
	// be read) from one that arrived afterwards (activity, whatever its
	// timestamps say, since a copied or restored session keeps its old ones).
	startTime      time.Time
	storeAtStartup bool
	// startupDirs is nil when the session store existed but could not be
	// listed, in which case timestamps alone decide.
	startupDirs map[string]bool

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// startWatcher records the startup boundary, establishes the watch and starts
// the worker. Sessions already on disk are `sync`'s to save, so only changes
// after this call are delivered; the boundary is taken before Copilot launches
// so a fast first write cannot be absorbed into it.
func startWatcher(projectRoot string, debugRaw bool, callback func(*spi.AgentChatSession)) (*sessionWatcher, error) {
	return startWatcherWithHook(projectRoot, debugRaw, callback, nil)
}

// startWatcherWithHook is startWatcher with a hook that runs once the baseline
// is taken and before the worker starts: the window where real activity can
// race startup. Tests use it to land writes in that window; production passes
// nil.
func startWatcherWithHook(projectRoot string, debugRaw bool, callback func(*spi.AgentChatSession), beforeFirstCheck func()) (*sessionWatcher, error) {
	if callback == nil {
		return nil, errors.New("session callback must not be nil")
	}
	root, err := sessionsRoot()
	if err != nil {
		return nil, err
	}
	fsWatcher, err := spi.NewFSWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &sessionWatcher{
		projectRoot: projectRoot,
		debugRaw:    debugRaw,
		callback:    callback,
		fsWatcher:   fsWatcher,
		watched:     make(map[string]bool),
		known:       make(map[string]fileStamp),
		owned:       make(map[string]bool),
		startTime:   time.Now(),
		ctx:         ctx,
		cancel:      cancel,
	}

	files, err := listSessionFiles()
	if err != nil {
		slog.Warn("Copilot CLI watcher: Could not baseline existing sessions", "error", err)
	}
	for _, file := range files {
		// A write that lands after the boundary is activity, even when it
		// beats the baseline listing to the file.
		if file.ModTime.Before(w.startTime) {
			w.known[file.Path] = stampOf(file)
		}
	}
	// Listed after the baseline, so a session directory created in between is
	// counted as present at startup only when its transcript also predates
	// the boundary; one written after it is still delivered.
	w.storeAtStartup, w.startupDirs = listStartupDirs(root)
	w.updateWatches(files)

	if beforeFirstCheck != nil {
		beforeFirstCheck()
	}

	slog.Info("Copilot CLI watcher started",
		"projectRoot", projectRoot, "sessionsRoot", root, "storeExists", w.storeAtStartup,
		"baselined", len(w.known), "watchedFiles", len(w.watched))
	w.wg.Go(w.run)
	return w, nil
}

// listStartupDirs reports whether the session store exists and which session
// directories it holds. The set is nil when the store exists but cannot be
// listed.
func listStartupDirs(root string) (bool, map[string]bool) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		slog.Warn("Copilot CLI watcher: Cannot list session store at startup", "path", root, "error", err)
		return true, nil
	}
	dirs := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs[entry.Name()] = true
		}
	}
	return true, dirs
}

// predatesStartup reports whether a transcript first seen after startup is
// history rather than activity: its session existed at startup (it was only
// unreadable then) and it has not been written since.
func (w *sessionWatcher) predatesStartup(file sessionFile) bool {
	if !w.storeAtStartup || !file.ModTime.Before(w.startTime) {
		return false
	}
	return w.startupDirs == nil || w.startupDirs[file.SessionID]
}

// Stop ends the watch after delivering any change not yet delivered. Safe to
// call more than once.
func (w *sessionWatcher) Stop() {
	w.stopOnce.Do(func() {
		slog.Info("Stopping Copilot CLI watcher")
		w.cancel()
		w.wg.Wait()
		if err := w.fsWatcher.Close(); err != nil {
			slog.Debug("Failed to close Copilot CLI file watcher", "error", err)
		}
		slog.Info("Copilot CLI watcher stopped")
	})
}

// run is the single worker: it owns the watcher state and every callback, so
// deliveries are ordered and shutdown has one thing to join.
func (w *sessionWatcher) run() {
	reconcile := time.NewTicker(reconcileInterval)
	defer reconcile.Stop()

	debounce := time.NewTimer(debounceDelay)
	debounce.Stop()
	var debounceC <-chan time.Time
	var burstStart time.Time

	events := w.fsWatcher.Events
	watchErrors := w.fsWatcher.Errors

	for {
		select {
		case <-w.ctx.Done():
			debounce.Stop()
			// Copilot has exited: catch its last writes even if fsnotify has
			// not delivered them yet.
			w.check("shutdown")
			return

		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if !w.handleEvent(event) {
				continue
			}
			now := time.Now()
			if debounceC == nil {
				burstStart = now
			}
			debounce.Reset(min(debounceDelay, max(maxBurstDelay-now.Sub(burstStart), 0)))
			debounceC = debounce.C

		case <-debounceC:
			debounceC = nil
			w.check("file-change")

		case <-reconcile.C:
			w.check("reconcile")

		case err, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			slog.Warn("Copilot CLI watcher: File watch error", "error", err)
		}
	}
}

// handleEvent reports whether a watched transcript may have changed.
func (w *sessionWatcher) handleEvent(event fsnotify.Event) bool {
	slog.Debug("Copilot CLI watcher: File event", "path", event.Name, "op", event.Op)

	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		// The session was deleted; its watch is gone with it. Forgetting it
		// lets the reconcile scan re-watch a transcript recreated at the path.
		delete(w.watched, event.Name)
		return false
	}
	return w.watched[event.Name] && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create))
}

// updateWatches watches every transcript of this project written inside the
// watch window and drops the watch on one that has aged out of it, so a long
// watch holds descriptors only for recent sessions. Watch failures are not
// fatal: the reconcile scan still notices those changes, just later, and
// re-watches an aged-out transcript once it is written again.
func (w *sessionWatcher) updateWatches(files []sessionFile) {
	cutoff := spi.WatchWindowCutoff(time.Now())
	for _, file := range files {
		if file.ModTime.Before(cutoff) {
			if w.watched[file.Path] {
				if err := w.fsWatcher.Remove(file.Path); err != nil {
					slog.Debug("Copilot CLI watcher: Failed to unwatch transcript", "path", file.Path, "error", err)
				}
				delete(w.watched, file.Path)
			}
			continue
		}
		if w.watched[file.Path] {
			continue
		}
		if belongs, cached := w.owned[file.Path]; cached && !belongs {
			continue
		}
		if _, belongs := w.ownership(file.Path); !belongs {
			continue
		}
		if err := w.fsWatcher.Add(file.Path); err != nil {
			slog.Debug("Copilot CLI watcher: Failed to watch transcript", "path", file.Path, "error", err)
			continue
		}
		w.watched[file.Path] = true
	}
}

// check delivers every project session whose transcript changed since it was
// last delivered or baselined.
//
// Updates are collected first and delivered afterwards, so a slow callback (a
// markdown write) never delays noticing the next change.
func (w *sessionWatcher) check(trigger string) {
	files, err := listSessionFiles()
	if err != nil {
		slog.Warn("Copilot CLI watcher: Check failed", "trigger", trigger, "error", err)
		return
	}
	w.updateWatches(files)

	var updates []*spi.AgentChatSession
	for _, file := range files {
		stamp := stampOf(file)
		known, seen := w.known[file.Path]
		if seen && known == stamp {
			continue
		}
		if !seen && w.predatesStartup(file) {
			// Unreadable when the baseline was taken and untouched since:
			// history discovered late, not activity.
			w.known[file.Path] = stamp
			continue
		}
		header, belongs := w.ownership(file.Path)
		if header == nil {
			// Not readable yet (Copilot is still writing the first record);
			// left unrecorded so the next check retries it.
			continue
		}
		if !belongs {
			w.known[file.Path] = stamp
			continue
		}
		session, err := convertSession(file, header, w.debugRaw, true)
		if err != nil {
			slog.Warn("Copilot CLI watcher: Failed to read session", "sessionId", file.SessionID, "error", err)
			continue
		}
		if session == nil {
			// No prompt yet; retried once the first message lands.
			continue
		}
		w.known[file.Path] = stamp
		updates = append(updates, session)
	}

	for _, session := range updates {
		slog.Info("Copilot CLI watcher: Delivering session update", "sessionId", session.SessionID, "trigger", trigger)
		spi.DeliverSession(watcherLabel, w.callback, session)
	}
	slog.Debug("Copilot CLI watcher: Check complete", "trigger", trigger, "delivered", len(updates))
}

// ownership returns a transcript's header and whether it belongs to the
// project, caching the answer once the header is readable.
func (w *sessionWatcher) ownership(path string) (*sessionHeader, bool) {
	header, err := readSessionHeader(path, true)
	if err != nil {
		slog.Debug("Copilot CLI watcher: Session header not readable yet", "path", path, "error", err)
		return nil, false
	}
	belongs, cached := w.owned[path]
	if !cached {
		belongs = belongsToProject(header, w.projectRoot)
		w.owned[path] = belongs
	}
	return header, belongs
}
