package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Watcher timing. Package variables so tests can shorten them.
var (
	// debounceDelay collapses the burst of WAL writes one streamed update
	// produces into a single read, taken after the burst's last write.
	debounceDelay = 300 * time.Millisecond

	// maxBurstDelay bounds how long a continuous burst (a long streaming
	// response) can postpone a read, so the markdown keeps growing while the
	// agent is still writing.
	maxBurstDelay = 2 * time.Second

	// reconcileInterval is the periodic re-read that recovers from missed
	// filesystem events. One indexed query per interval.
	reconcileInterval = 15 * time.Second
)

// watcherLabel names this watcher in callback panic logs.
const watcherLabel = "OpenCode watcher"

// sessionWatcher follows one project's sessions in OpenCode's database and
// delivers each new or changed session, in order, on a single worker.
//
// OpenCode keeps every session in one SQLite database, so the watch is a fixed
// set of one directory (the database's, or its nearest existing ancestor
// until OpenCode creates it) no matter how many sessions accumulate.
type sessionWatcher struct {
	projectPath string
	debugRaw    bool
	callback    func(*spi.AgentChatSession)
	dbPath      string
	dbDir       string

	fsWatcher  *fsnotify.Watcher
	watchedDir string // owned by the worker after start

	// startMillis is the startup boundary: sessions last written before it
	// form the baseline; anything written at or after it is activity.
	startMillis int64
	// adoptExisting is set when the database did not exist at startup: every
	// session in a store that arrives later is new activity, even one whose
	// timestamps predate startup (a store copied or restored into place).
	adoptExisting bool
	// known holds the signature last delivered (or baselined) per session.
	// Owned by the worker goroutine after start.
	known map[string]signature

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// startWatcher records the startup boundary, establishes the watch and starts
// the worker. It returns an error only when no watch can be established at all.
func startWatcher(projectPath string, debugRaw bool, callback func(*spi.AgentChatSession)) (*sessionWatcher, error) {
	if callback == nil {
		return nil, errors.New("session callback must not be nil")
	}
	dbPath, err := getDatabasePath()
	if err != nil {
		return nil, err
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	w := &sessionWatcher{
		projectPath: projectPath,
		debugRaw:    debugRaw,
		callback:    callback,
		dbPath:      dbPath,
		dbDir:       filepath.Dir(dbPath),
		fsWatcher:   fsWatcher,
		known:       make(map[string]signature),
		ctx:         ctx,
		cancel:      cancel,
	}

	// The boundary is taken before the watch exists, so a write that lands
	// while the watch is being set up or before the first read is newer than
	// the boundary and is emitted rather than absorbed into the baseline.
	w.startMillis = time.Now().UnixMilli()

	if err := w.updateWatch(); err != nil {
		cancel()
		_ = fsWatcher.Close() // nothing was delivered; the watch error is what matters
		return nil, err
	}

	if _, err := os.Stat(dbPath); err == nil {
		// OpenCode's database is normally in WAL mode already; this makes sure
		// every write reaches the -wal file the watch relies on.
		if err := spi.EnsureWALMode(dbPath); err != nil {
			slog.Warn("WatchAgent: Failed to ensure WAL mode on OpenCode database", "path", dbPath, "error", err)
		}
	} else {
		w.adoptExisting = true
	}

	if beforeFirstCheck != nil {
		beforeFirstCheck()
	}

	slog.Info("OpenCode watcher started",
		"projectPath", projectPath,
		"dbPath", dbPath,
		"watchedDir", w.watchedDir,
		"storeExists", !w.adoptExisting)

	w.wg.Go(w.run)
	return w, nil
}

// beforeFirstCheck, when set by a test, runs after the watch is established
// and before the first read, where real activity can race the startup.
var beforeFirstCheck func()

// Stop ends the watch after delivering any change that has not been delivered
// yet. Safe to call more than once.
func (w *sessionWatcher) Stop() {
	w.stopOnce.Do(func() {
		slog.Info("Stopping OpenCode watcher")
		w.cancel()
		w.wg.Wait()
		if err := w.fsWatcher.Close(); err != nil {
			slog.Debug("Failed to close OpenCode file watcher", "error", err)
		}
		slog.Info("OpenCode watcher stopped")
	})
}

// run is the single worker: it owns the known map, the watch, and every
// callback, so deliveries are ordered and shutdown has one thing to join.
func (w *sessionWatcher) run() {
	reconcile := time.NewTicker(reconcileInterval)
	defer reconcile.Stop()

	debounce := time.NewTimer(debounceDelay)
	debounce.Stop()
	var debounceC <-chan time.Time
	var burstStart time.Time

	events := w.fsWatcher.Events
	watchErrors := w.fsWatcher.Errors

	// The first read records the baseline and delivers anything written
	// since the boundary.
	w.check("startup")

	for {
		select {
		case <-w.ctx.Done():
			debounce.Stop()
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
			wait := min(debounceDelay, max(maxBurstDelay-now.Sub(burstStart), 0))
			debounce.Reset(wait)
			debounceC = debounce.C

		case <-debounceC:
			debounceC = nil
			w.check("file-change")

		case <-reconcile.C:
			if w.watchedDir != w.dbDir {
				// Recovers a missed directory-creation event.
				if err := w.updateWatch(); err != nil {
					slog.Debug("OpenCode watcher: Failed to update watch", "error", err)
				}
			}
			w.check("reconcile")

		case err, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			slog.Warn("OpenCode watcher: File watch error", "error", err)
		}
	}
}

// handleEvent reacts to one filesystem event and reports whether the
// database may have changed.
func (w *sessionWatcher) handleEvent(event fsnotify.Event) bool {
	slog.Debug("OpenCode watcher: File event", "path", event.Name, "op", event.Op)

	if w.watchedDir != w.dbDir {
		// Waiting for OpenCode to create its data directory: follow each
		// directory it creates on the way down.
		if event.Has(fsnotify.Create) {
			if err := w.updateWatch(); err != nil {
				slog.Warn("OpenCode watcher: Failed to follow new directory", "path", event.Name, "error", err)
			}
		}
		return w.watchedDir == w.dbDir
	}

	if event.Name == w.dbDir && (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
		// The data directory went away; fall back to waiting for it.
		if err := w.updateWatch(); err != nil {
			slog.Warn("OpenCode watcher: Failed to re-establish watch", "error", err)
		}
		return false
	}

	isDatabaseFile := event.Name == w.dbPath || strings.HasPrefix(event.Name, w.dbPath+"-")
	return isDatabaseFile && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create))
}

// updateWatch points the single directory watch at the database's directory,
// or at its nearest existing ancestor until that directory exists, so the
// watcher never disables itself because OpenCode has not run yet.
func (w *sessionWatcher) updateWatch() error {
	target := nearestExistingDir(w.dbDir)
	if target == w.watchedDir {
		return nil
	}
	if err := w.fsWatcher.Add(target); err != nil {
		return fmt.Errorf("failed to watch %s: %w", target, err)
	}
	if w.watchedDir != "" {
		// The previous directory may already be gone; its watch is dead either way.
		_ = w.fsWatcher.Remove(w.watchedDir)
	}
	slog.Debug("OpenCode watcher: Watching directory", "dir", target, "waitingForStore", target != w.dbDir)
	w.watchedDir = target
	return nil
}

// predatesStart reports whether every write to the session happened before
// the watcher started.
func (w *sessionWatcher) predatesStart(summary sessionSummary) bool {
	return summary.TimeUpdated < w.startMillis && summary.LastMessageUpdate < w.startMillis
}

// nearestExistingDir returns path if it is an existing directory, otherwise
// its closest existing ancestor.
func nearestExistingDir(path string) string {
	for {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// check reads the project's sessions and delivers each one whose signature
// changed since it was last delivered or baselined. A session seen for the
// first time joins the baseline instead when its last write predates startup,
// whichever check first reads it, so a failed or late first read cannot
// republish history.
func (w *sessionWatcher) check(trigger string) {
	delivered := 0
	err := withDatabase(func(db *sql.DB) error {
		summaries, err := listSessionSummaries(db, w.projectPath)
		if err != nil {
			return err
		}
		for _, summary := range summaries {
			sig := summary.signature()
			known, seen := w.known[summary.ID]
			if seen && known == sig {
				continue
			}
			if !seen && !w.adoptExisting && w.predatesStart(summary) {
				w.known[summary.ID] = sig
				continue
			}
			snapshot, err := readSessionSnapshot(db, summary.ID)
			if err != nil {
				// Left unrecorded so the next check retries it.
				slog.Warn("OpenCode watcher: Failed to read session", "sessionId", summary.ID, "error", err)
				continue
			}
			session := convertSnapshot(snapshot, w.projectPath, w.debugRaw)
			if session == nil {
				// No prompt yet; retried once the first message lands.
				continue
			}
			w.known[summary.ID] = sig
			slog.Info("OpenCode watcher: Delivering session update",
				"sessionId", session.SessionID, "trigger", trigger)
			spi.DeliverSession(watcherLabel, w.callback, session)
			delivered++
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoDatabase) {
		slog.Warn("OpenCode watcher: Check failed", "trigger", trigger, "error", err)
		return
	}
	slog.Debug("OpenCode watcher: Check complete", "trigger", trigger, "delivered", delivered)
}
