package cursoride

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// defaultCheckInterval is the safety-net polling interval used when the caller doesn't
// specify one. File events normally trigger checks long before it elapses; the poll only
// catches changes fsnotify missed.
const defaultCheckInterval = 2 * time.Minute

// CursorIDEWatcher monitors Cursor IDE databases for changes and notifies when sessions are updated
type CursorIDEWatcher struct {
	projectPath     string
	workspaces      []WorkspaceMatch // All workspace entries matching projectPath (e.g. WSL/SSH/.code-workspace can produce more than one)
	globalDbPath    string
	debugRaw        bool
	sessionCallback func(*spi.AgentChatSession)
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	callbackWg      sync.WaitGroup // Tracks in-flight sessionCallback goroutines
	mu              sync.RWMutex
	lastCheck       time.Time
	// knownComposers is the per-composer change watermark (composerID -> lastUpdatedAt).
	// Seeded in Start() with the sessions that already exist so startup doesn't re-emit
	// every historical session; only timestamps that advance past the seeded (or last
	// processed) value trigger the callback.
	knownComposers    map[string]int64
	checkInterval     time.Duration // How often to check DB if no file events (default: 2 minutes)
	throttleDuration  time.Duration // Minimum time between checks (default: 10 seconds)
	lastThrottledCall time.Time     // Track last throttled call
	pendingCheck      bool          // Whether a check is pending after throttle
	fsWatcher         *fsnotify.Watcher
	// watchedDBPaths lists the database paths whose parent directories are being
	// watched, so handleFileEvents can filter directory events down to the db files
	// (state.vscdb and its -wal/-shm siblings) and ignore unrelated neighbors like
	// workspace.json. Populated in Start() before the event goroutine launches, and
	// read-only afterwards, so it needs no locking.
	watchedDBPaths    []string
	initialCheckTimer *time.Timer // Cancelled in Stop() so it can't fire a check after shutdown begins
	// pendingIncomplete tracks sessions whose last bubble is a user message with no agent
	// response yet. Cursor writes the user bubble and updates lastUpdatedAt atomically,
	// so the header count matches the loaded bubble count (the existing bubble-count guard
	// passes), but the agent has not responded yet. We skip these sessions and retry on
	// the next check. If a session stays incomplete beyond incompleteTimeout (e.g. the
	// user abandoned the chat mid-flight and the agent never replied), we process it
	// anyway so the markdown is not lost permanently.
	pendingIncomplete map[string]time.Time // composerID -> time first seen as incomplete
	incompleteTimeout time.Duration        // How long to wait before processing an incomplete session anyway
	// checking prevents concurrent checkForChanges executions. If a check takes
	// longer than the throttle window, a new trigger could launch a second
	// goroutine that opens its own DB connections, multiplying file descriptors.
	checking sync.Mutex
}

// NewCursorIDEWatcher creates a new watcher for Cursor IDE databases
func NewCursorIDEWatcher(
	projectPath string,
	debugRaw bool,
	sessionCallback func(*spi.AgentChatSession),
	checkInterval time.Duration,
) (*CursorIDEWatcher, error) {
	// A nil callback would only surface as a panic deep inside checkForChanges,
	// long after construction — fail fast here instead.
	if sessionCallback == nil {
		return nil, fmt.Errorf("sessionCallback must not be nil")
	}

	// Find all workspaces matching the project (a project can match more than one
	// workspace entry — e.g. opened via .code-workspace, over SSH, or from WSL).
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace for project: %w", err)
	}

	// Get global database path
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}

	// Create fsnotify watcher
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create file watcher: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Set default check interval if not provided
	if checkInterval == 0 {
		checkInterval = defaultCheckInterval
	}

	return &CursorIDEWatcher{
		projectPath:      projectPath,
		workspaces:       workspaces,
		globalDbPath:     globalDbPath,
		debugRaw:         debugRaw,
		sessionCallback:  sessionCallback,
		ctx:              ctx,
		cancel:           cancel,
		knownComposers:   make(map[string]int64),
		checkInterval:    checkInterval,
		throttleDuration: 10 * time.Second,
		// Zero value, not time.Now(): lastThrottledCall means "the last time a check
		// actually ran," not "when the watcher was constructed." Seeding it with the
		// current time made the very first real check (5s after Start()) look like it
		// arrived too soon after a "previous" check that never happened, throttling it
		// by another full throttleDuration and delaying startup by up to ~2x intended.
		lastThrottledCall: time.Time{},
		pendingCheck:      false,
		fsWatcher:         fsWatcher,
		pendingIncomplete: make(map[string]time.Time),
		incompleteTimeout: 60 * time.Second,
	}, nil
}

// Start begins monitoring the Cursor IDE databases
func (w *CursorIDEWatcher) Start() error {
	slog.Info("Starting Cursor IDE watcher",
		"projectPath", w.projectPath,
		"workspaceCount", len(w.workspaces),
		"globalDbPath", w.globalDbPath,
		"checkInterval", w.checkInterval,
		"throttleDuration", w.throttleDuration)

	// Ensure WAL mode is enabled on all databases before watching.
	// WAL mode is required so that the -wal file exists for fsnotify to detect changes.
	// This is a one-time read-write operation at startup; all subsequent reads are read-only.
	for _, ws := range w.workspaces {
		if err := EnsureWALMode(ws.DBPath); err != nil {
			slog.Warn("Failed to ensure WAL mode on workspace database",
				"workspaceID", ws.ID,
				"error", err)
		}
	}
	if err := EnsureWALMode(w.globalDbPath); err != nil {
		slog.Warn("Failed to ensure WAL mode on global database", "error", err)
	}

	// Seed knownComposers with the sessions that already exist so startup doesn't
	// re-emit every historical session — matching the other providers' watchers
	// (e.g. Cursor CLI's SetInitialState). Sessions updated after this point still
	// process because their lastUpdatedAt advances past the seeded watermark.
	w.seedKnownComposers()

	// Set up file watching on all matching workspace databases
	for _, ws := range w.workspaces {
		if err := w.watchDatabaseFiles(ws.DBPath); err != nil {
			slog.Warn("Failed to watch workspace database files",
				"workspaceID", ws.ID,
				"error", err)
		}
	}

	// Set up file watching on global database
	if err := w.watchDatabaseFiles(w.globalDbPath); err != nil {
		slog.Warn("Failed to watch global database files", "error", err)
	}

	// Start the file event handler goroutine
	w.wg.Add(1)
	go w.handleFileEvents()

	// Start the safety net polling goroutine
	w.wg.Add(1)
	go w.safetyNetPoller()

	// Perform initial check after a short delay. The timer is stored so Stop() can
	// cancel it if it hasn't fired yet.
	w.initialCheckTimer = time.AfterFunc(5*time.Second, func() {
		w.triggerCheck("initial")
	})

	slog.Info("Cursor IDE watcher started successfully")
	return nil
}

// seedKnownComposers records the current lastUpdatedAt of every composer already in the
// project's workspaces, without firing callbacks. Uses the lightweight loader (composer
// rows only, no bubbles) since only the timestamps are needed. Failures are non-fatal:
// the worst case is that the first check re-emits existing sessions, which is the exact
// behavior this seeding exists to avoid, not a correctness problem.
func (w *CursorIDEWatcher) seedKnownComposers() {
	// One lightweight scan serves both needs here: discovery (via projectComposerIDs)
	// and the per-composer timestamps read below.
	composers, err := LoadAllComposerDataLightweight(w.globalDbPath)
	if err != nil {
		slog.Warn("Failed to seed existing sessions; first check may re-emit them", "error", err)
		return
	}
	composerIDs, err := projectComposerIDs(composers, w.projectPath, w.workspaces)
	if err != nil || len(composerIDs) == 0 {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	seeded := 0
	for _, composerID := range composerIDs {
		composer, exists := composers[composerID]
		if !exists {
			continue
		}
		timestamp := composer.LastUpdatedAt
		if timestamp == 0 {
			timestamp = composer.CreatedAt
		}
		w.knownComposers[composerID] = timestamp
		seeded++
	}
	slog.Debug("Seeded existing sessions as known", "count", seeded)
}

// Stop gracefully stops the watcher
func (w *CursorIDEWatcher) Stop() {
	slog.Info("Stopping Cursor IDE watcher")

	// Stop the initial-check timer in case it hasn't fired yet — no point starting a
	// check that would just have to be waited on below.
	if w.initialCheckTimer != nil {
		w.initialCheckTimer.Stop()
	}

	// Perform one final check before stopping
	slog.Info("Performing final check for changes before stopping")
	w.checkForChanges("shutdown")

	// Close fsnotify watcher
	if w.fsWatcher != nil {
		if err := w.fsWatcher.Close(); err != nil {
			slog.Warn("Error closing fsnotify watcher", "error", err)
		}
	}

	// Cancel under w.mu, then wait. Every check-spawning wg.Add happens under this
	// lock (triggerCheck/executePendingCheck), so once cancel lands no trigger can
	// pass the ctx check and Add after Wait has started — Add-concurrent-with-Wait
	// on a drained WaitGroup is a documented misuse.
	w.mu.Lock()
	w.cancel()
	w.mu.Unlock()
	w.wg.Wait()

	// Wait for any pending callback goroutines to complete
	slog.Info("Waiting for pending callbacks to complete")
	w.callbackWg.Wait()

	slog.Info("Cursor IDE watcher stopped")
}

// watchDatabaseFiles sets up file watching for a database and its WAL file.
//
// Why the parent directory instead of the files themselves: in WAL mode nearly every
// write lands in state.vscdb-wal, and SQLite deletes that file when the last
// connection closes and recreates it on the next open. A watch on the file itself is
// never added when the file doesn't exist yet (watcher started before Cursor), and
// dies with the inode when Cursor checkpoints/restarts — silently degrading all
// updates to the 2-minute safety-net poll. A directory watch keeps delivering
// Create/Write events for the db and its -wal across those lifecycles.
func (w *CursorIDEWatcher) watchDatabaseFiles(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if err := w.fsWatcher.Add(dir); err != nil {
		// Log but don't fail - we'll rely on polling
		slog.Warn("Failed to watch database directory", "dir", dir, "error", err)
		return err
	}

	// Register the db path so handleFileEvents can filter directory events
	// down to this database's files.
	w.watchedDBPaths = append(w.watchedDBPaths, dbPath)
	slog.Debug("Watching database directory", "dir", dir, "dbPath", dbPath)

	return nil
}

// isWatchedDBFile reports whether a file event path belongs to one of the watched
// databases (the db file itself or a sibling like -wal/-shm/-journal).
func (w *CursorIDEWatcher) isWatchedDBFile(name string) bool {
	for _, dbPath := range w.watchedDBPaths {
		if name == dbPath || strings.HasPrefix(name, dbPath+"-") {
			return true
		}
	}
	return false
}

// handleFileEvents processes file system events from fsnotify
func (w *CursorIDEWatcher) handleFileEvents() {
	defer w.wg.Done()

	for {
		select {
		case <-w.ctx.Done():
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Only care about Write and Create events on the database files —
			// the watch is on the parent directory, so unrelated neighbors
			// (e.g. workspace.json) must be filtered out.
			if (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) && w.isWatchedDBFile(event.Name) {
				slog.Debug("Database file changed", "path", event.Name, "op", event.Op)
				w.triggerCheck("file-change")
			}

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			slog.Warn("File watcher error", "error", err)
		}
	}
}

// safetyNetPoller ensures we check the database periodically even if file events are missed
func (w *CursorIDEWatcher) safetyNetPoller() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			slog.Debug("Safety net poll triggered")
			w.triggerCheck("poll")
		}
	}
}

// triggerCheck requests a check, respecting throttle limits
func (w *CursorIDEWatcher) triggerCheck(trigger string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Shutdown guard: Stop() cancels the context under this same lock, so a trigger
	// arriving after cancellation must not wg.Add (Stop may already be in wg.Wait).
	// This covers the throttle branch below; runCheckAsync re-checks for its own path.
	if w.ctx.Err() != nil {
		return
	}

	now := time.Now()
	timeSinceLastCall := now.Sub(w.lastThrottledCall)

	if timeSinceLastCall < w.throttleDuration {
		// Within throttle window - mark as pending
		if !w.pendingCheck {
			w.pendingCheck = true
			// Schedule a check after throttle duration expires
			delay := w.throttleDuration - timeSinceLastCall
			slog.Debug("Check throttled, will retry after delay",
				"trigger", trigger,
				"delay", delay)

			w.wg.Add(1)
			go func() {
				defer w.wg.Done()
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-w.ctx.Done():
					return
				case <-timer.C:
					w.executePendingCheck()
				}
			}()
		}
		return
	}

	// Execute immediately
	w.lastThrottledCall = now
	w.pendingCheck = false
	w.runCheckAsync(trigger)
}

// runCheckAsync runs checkForChanges in a new goroutine tracked by w.wg, so Stop()'s
// w.wg.Wait() actually waits for it instead of returning while it's still running.
// It's a no-op if the watcher's context is already cancelled.
// Must be called with w.mu held: Stop() cancels the context under the same lock, which
// is what makes the ctx check and wg.Add here atomic with respect to shutdown.
func (w *CursorIDEWatcher) runCheckAsync(trigger string) {
	if w.ctx.Err() != nil {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.checkForChanges(trigger)
	}()
}

// executePendingCheck executes a pending check if one was scheduled
func (w *CursorIDEWatcher) executePendingCheck() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.pendingCheck {
		return
	}

	now := time.Now()
	timeSinceLastCall := now.Sub(w.lastThrottledCall)

	if timeSinceLastCall >= w.throttleDuration {
		w.lastThrottledCall = now
		w.pendingCheck = false
		w.runCheckAsync("throttled")
	}
}

// checkForChanges queries the databases and processes any new or updated sessions.
// The checking mutex prevents concurrent executions that would multiply DB connections.
func (w *CursorIDEWatcher) checkForChanges(trigger string) {
	// Prevent overlapping checks. Normal triggers use TryLock and skip so slow checks
	// don't pile up goroutines, but the shutdown flush must not be skipped — its whole
	// purpose is to catch changes an in-flight check's snapshot missed, so it blocks
	// until that check finishes.
	if trigger == "shutdown" {
		w.checking.Lock()
	} else if !w.checking.TryLock() {
		slog.Debug("Skipping check, previous check still running", "trigger", trigger)
		return
	}
	defer w.checking.Unlock()

	slog.Debug("Checking for changes", "trigger", trigger)

	w.mu.Lock()
	lastCheck := w.lastCheck
	w.lastCheck = time.Now()
	w.mu.Unlock()

	// Load the project's composer IDs from both association sources: workspace-DB
	// references (older Cursor) and the workspaceIdentifier embedded in global
	// composerData rows (the only live source in Cursor >= 3.12).
	composerIDs, err := FindProjectComposerIDs(w.globalDbPath, w.projectPath, w.workspaces)
	if err != nil {
		slog.Error("Failed to load project composer IDs", "error", err)
		return
	}

	if len(composerIDs) == 0 {
		slog.Debug("No composers found for project")
		return
	}

	// Load composer data from global database
	composers, err := LoadComposerDataBatch(w.globalDbPath, composerIDs)
	if err != nil {
		slog.Error("Failed to load composer data", "error", err)
		return
	}

	// Track new/updated sessions
	newCount := 0
	updatedCount := 0

	for composerID, composer := range composers {
		// Skip if conversation is empty
		if len(composer.Conversation) == 0 {
			continue
		}

		// Check if this is a new or updated session
		w.mu.RLock()
		knownTimestamp, exists := w.knownComposers[composerID]
		w.mu.RUnlock()

		currentTimestamp := composer.LastUpdatedAt
		if currentTimestamp == 0 {
			currentTimestamp = composer.CreatedAt
		}

		// Determine if we should process this session
		shouldProcess := false
		if !exists {
			// New session
			shouldProcess = true
			newCount++
			slog.Info("Found new session", "composerID", composerID, "name", composer.Name)
		} else if currentTimestamp > knownTimestamp {
			// Updated session
			shouldProcess = true
			updatedCount++
			slog.Info("Found updated session",
				"composerID", composerID,
				"name", composer.Name,
				"oldTimestamp", knownTimestamp,
				"newTimestamp", currentTimestamp)
		}

		if shouldProcess {
			// Guard against a race condition: Cursor may commit the composerData record
			// (updating LastUpdatedAt and FullConversationHeadersOnly) before the
			// corresponding bubble records are committed to the database. If the header
			// list references more bubbles than were actually loaded, the session data is
			// incomplete. By not advancing knownComposers, the next check cycle will
			// re-process this session once all bubbles are available.
			if len(composer.FullConversationHeadersOnly) > 0 &&
				len(composer.Conversation) < len(composer.FullConversationHeadersOnly) {
				// Debug, not Warn: this is expected, transient state while Cursor is
				// mid-write, and it fires routinely during normal streaming.
				slog.Debug("Incomplete composer data (bubble records not yet committed), will retry on next check",
					"composerID", composerID,
					"expectedBubbles", len(composer.FullConversationHeadersOnly),
					"loadedBubbles", len(composer.Conversation))
				continue
			}

			// Guard against a second race condition: the bubble count may match the header
			// count (so the guard above passes), but the last committed bubble is a user
			// message with no agent response yet. This happens because Cursor writes the
			// user bubble and updates lastUpdatedAt in one step, before the agent has
			// produced a reply. Processing now would generate markdown that is missing the
			// agent response entirely.
			//
			// We skip the session and let knownComposers stay at the old timestamp so it
			// is re-evaluated on the next check cycle (when the agent reply arrives).
			// The pendingIncomplete map records when we first saw the session in this
			// state. If the agent never replies (e.g. the user abandoned the chat),
			// we process the session anyway after incompleteTimeout so the markdown is
			// not lost permanently.
			if len(composer.Conversation) > 0 {
				lastBubble := &composer.Conversation[len(composer.Conversation)-1]
				if lastBubble.Type == 1 {
					w.mu.Lock()
					firstSeen, alreadyPending := w.pendingIncomplete[composerID]
					if !alreadyPending {
						w.pendingIncomplete[composerID] = time.Now()
						w.mu.Unlock()
						// Debug, not Warn: waiting for the agent reply is the normal
						// state while the user's request is in flight.
						slog.Debug("Last bubble is a user message (agent not yet responded), will retry on next check",
							"composerID", composerID,
							"lastBubbleID", lastBubble.BubbleID)
						continue
					}
					if time.Since(firstSeen) < w.incompleteTimeout {
						w.mu.Unlock()
						slog.Debug("Still waiting for agent response, will retry on next check",
							"composerID", composerID,
							"waited", time.Since(firstSeen))
						continue
					}
					// Timed out — the agent likely never replied; process anyway.
					delete(w.pendingIncomplete, composerID)
					w.mu.Unlock()
					slog.Warn("Timed out waiting for agent response, processing incomplete session",
						"composerID", composerID,
						"waited", time.Since(firstSeen))
				} else {
					// Last bubble is an agent message — clear any stale pending state.
					w.mu.Lock()
					delete(w.pendingIncomplete, composerID)
					w.mu.Unlock()
				}
			}

			// Convert to AgentChatSession
			session, err := ConvertToAgentChatSession(composer, w.projectPath)
			if err != nil {
				// Don't advance knownComposers on failure — leave the watermark where it
				// was so this composer is retried on the next check instead of being
				// silently skipped forever.
				slog.Warn("Failed to convert composer to session",
					"composerID", composerID,
					"error", err)
				continue
			}

			// Update known timestamp. Only done after a successful conversion so a
			// failure (see above) doesn't permanently poison the watermark for this composer.
			w.mu.Lock()
			w.knownComposers[composerID] = currentTimestamp
			w.mu.Unlock()

			// Write debug output if requested
			if w.debugRaw {
				if err := writeDebugOutput(session); err != nil {
					slog.Warn("Failed to write debug output",
						"sessionID", session.SessionID,
						"error", err)
				}
			}

			// Invoke callback asynchronously so a panic during processing (e.g. markdown
			// write or cloud sync) doesn't crash the watcher, and slow callback I/O
			// doesn't delay processing of the remaining sessions in this check.
			slog.Info("Invoking callback for session",
				"sessionID", session.SessionID,
				"slug", session.Slug)
			w.callbackWg.Add(1)
			go func(s *spi.AgentChatSession) {
				defer w.callbackWg.Done()
				defer func() {
					if r := recover(); r != nil {
						slog.Error("Session callback panicked", "panic", r, "sessionID", s.SessionID)
					}
				}()
				w.sessionCallback(s)
			}(session)
		}
	}

	// On the very first check lastCheck is the zero time; report 0 instead of a
	// nonsensical ~2000-year duration.
	var elapsed time.Duration
	if !lastCheck.IsZero() {
		elapsed = time.Since(lastCheck)
	}
	slog.Info("Completed check for changes",
		"trigger", trigger,
		"totalComposers", len(composers),
		"newSessions", newCount,
		"updatedSessions", updatedCount,
		"elapsedSinceLastCheck", elapsed)
}
