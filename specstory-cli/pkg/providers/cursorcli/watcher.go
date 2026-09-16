package cursorcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

type fileStamp struct {
	size     int64
	modified time.Time
}

// A WAL write need not change the database file or the number of rows.
// Track both durable database content and the WAL, ignoring read-side shm churn.
type databaseStamp [2]fileStamp

func cursorDatabaseStamp(path string) databaseStamp {
	var stamp databaseStamp
	for i, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(path + suffix); err == nil {
			stamp[i] = fileStamp{size: info.Size(), modified: info.ModTime()}
		}
	}
	return stamp
}

// CursorWatcher monitors this project's session directories with fsnotify.
// Its single worker owns discovery, parsing, and callback delivery.
type CursorWatcher struct {
	projectPath     string
	hashDir         string
	debugRaw        bool
	sessionCallback func(*spi.AgentChatSession)
	tsCache         *MessageTimestampCache
	mu              sync.Mutex // Serializes starts and stops, including the final save.
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	watcher         *fsnotify.Watcher
	watched         map[string]bool
	stamps          map[string]databaseStamp
	walEnabled      map[string]bool
}

// NewCursorWatcher creates a watcher without starting it.
func NewCursorWatcher(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) (*CursorWatcher, error) {
	hashDir, err := GetProjectHashDir(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get project hash directory: %w", err)
	}
	return &CursorWatcher{projectPath: projectPath, hashDir: hashDir, debugRaw: debugRaw, sessionCallback: sessionCallback}, nil
}

// Start records existing sessions without emitting them and arms watches before
// returning, so a launched agent's first write cannot precede the baseline.
func (w *CursorWatcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		return fmt.Errorf("cursor watcher already started")
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher
	w.watched = make(map[string]bool)
	w.stamps = make(map[string]databaseStamp)
	w.walEnabled = make(map[string]bool)
	w.tsCache = NewMessageTimestampCache()
	if err := w.reconcile(false); err != nil {
		_ = watcher.Close()
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Go(func() { w.watchLoop(ctx) })
	slog.Info("Cursor watcher started", "projectPath", w.projectPath)
	return nil
}

// Stop joins the event worker, including its final scan and all callbacks.
func (w *CursorWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel == nil {
		return
	}
	w.cancel()
	w.wg.Wait()
	w.cancel = nil
	slog.Info("Cursor watcher stopped")
}

func (w *CursorWatcher) watchLoop(ctx context.Context) {
	defer func() { _ = w.watcher.Close() }()
	// Events drive change detection. Reconciliation recovers missed events and
	// prunes idle directory watches as the store ages.
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if err := w.reconcile(true); err != nil {
				slog.Warn("Cursor final session scan failed", "error", err)
			}
			return
		case _, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// A SQLite commit often produces a burst across the database and
			// WAL. Read after the burst, rather than parsing each partial write.
			debounce.Reset(50 * time.Millisecond)
			pending = debounce.C
			continue
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("Cursor watcher event error", "error", err)
		case <-ticker.C:
		case <-pending:
			pending = nil
		}
		if err := w.reconcile(true); err != nil {
			slog.Warn("Cursor watcher reconciliation failed", "error", err)
		}
	}
}

func (w *CursorWatcher) reconcile(emit bool) error {
	// Watch the nearest existing ancestor for a first-ever agent session.
	root := w.hashDir
	for {
		info, err := os.Stat(root)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", root)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return err
		}
		root = parent
	}
	wanted := map[string]bool{root: true}
	// A deleted directory loses its OS watch even if recreated under the same
	// name; the watcher's live list is authoritative at each reconciliation.
	live := make(map[string]bool)
	for _, path := range w.watcher.WatchList() {
		live[path] = true
	}
	w.watched = live
	add := func(path string) error {
		if w.watched[path] {
			return nil
		}
		if err := w.watcher.Add(path); err != nil {
			return err
		}
		w.watched[path] = true
		return nil
	}
	if err := add(root); err != nil {
		return err
	}
	if root == w.hashDir {
		ids, err := GetCursorSessionDirs(w.hashDir)
		if err != nil {
			return err
		}
		for _, id := range ids {
			dir := filepath.Join(w.hashDir, id)
			path := filepath.Join(dir, "store.db")
			stamp := cursorDatabaseStamp(path)
			previous, known := w.stamps[id]
			// New and changed sessions are always adopted, including dormant
			// sessions outside the ordinary watch window.
			active := stamp[0].modified.IsZero() || (emit && (!known || stamp != previous)) || stamp[0].modified.After(spi.WatchWindowCutoff(time.Now())) || stamp[1].modified.After(spi.WatchWindowCutoff(time.Now()))
			if active {
				wanted[dir] = true
				if err := add(dir); err != nil {
					return err
				}
			}
			if !w.walEnabled[id] && !stamp[0].modified.IsZero() {
				if err := spi.EnsureWALMode(path); err != nil {
					slog.Warn("Cursor watcher could not enable WAL", "path", path, "error", err)
				} else {
					w.walEnabled[id] = true
				}
			}
			stamp = cursorDatabaseStamp(path)
			if !emit || (known && stamp == previous) {
				w.stamps[id] = stamp
				continue
			}
			if !stamp[0].modified.IsZero() && w.processSessionChanges(id, path) {
				w.stamps[id] = stamp
			}
		}
	}
	for path := range w.watched {
		if !wanted[path] {
			_ = w.watcher.Remove(path)
			delete(w.watched, path)
		}
	}
	return nil
}

// processSessionChanges handles changes detected in a session
func (w *CursorWatcher) processSessionChanges(sessionID string, dbPath string) bool {
	slog.Info("Processing Cursor session changes", "sessionId", sessionID)

	// Read the session data
	sessionPath := filepath.Dir(dbPath) // Get the session directory from db path
	createdAt, slug, blobRecords, _, err := ReadSessionData(sessionPath)
	if err != nil {
		slog.Error("Failed to read session data", "sessionId", sessionID, "error", err)
		return false
	}

	if len(blobRecords) == 0 {
		slog.Debug("Session has no message records", "sessionId", sessionID)
		return false
	}

	// Generate SessionData from blob records
	sessionData, err := GenerateAgentSession(blobRecords, w.projectPath, sessionID, createdAt, slug, w.tsCache)
	if err != nil {
		slog.Error("Failed to generate SessionData", "sessionId", sessionID, "error", err)
		return false
	}

	// Marshal blob records to JSON for raw data
	rawDataJSON, err := json.Marshal(blobRecords)
	if err != nil {
		slog.Error("Failed to marshal blob records", "sessionId", sessionID, "error", err)
		return false
	}

	// Write provider-specific debug output if requested
	if w.debugRaw {
		if err := writeDebugOutput(sessionID, string(rawDataJSON), nil); err != nil {
			slog.Debug("Failed to write debug output", "sessionID", sessionID, "error", err)
			// Don't fail the operation if debug output fails
		}
	}

	// Create the AgentChatSession
	agentSession := &spi.AgentChatSession{
		SessionID:   sessionID,
		CreatedAt:   createdAt,
		Slug:        slug,
		SessionData: sessionData,
		RawData:     string(rawDataJSON),
	}

	// One worker delivers updates in order and Stop joins the final callback.
	if w.sessionCallback != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Cursor session callback panicked", "sessionId", sessionID, "panic", r)
				}
			}()
			w.sessionCallback(agentSession)
		}()
	}
	return true
}

// WatchCursorProject starts monitoring a Cursor project.
func WatchCursorProject(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) (*CursorWatcher, error) {
	watcher, err := NewCursorWatcher(projectPath, debugRaw, sessionCallback)
	if err != nil {
		return nil, err
	}
	if err := watcher.Start(); err != nil {
		return nil, err
	}
	return watcher, nil
}
