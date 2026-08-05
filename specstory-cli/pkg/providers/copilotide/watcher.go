package copilotide

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// WatchChatSessions watches the chatSessions directory for new/modified session files
func (p *Provider) WatchChatSessions(
	ctx context.Context,
	workspaceDir string,
	projectPath string,
	debugRaw bool,
	sessionCallback func(*spi.AgentChatSession),
) error {
	chatSessionsPath := GetChatSessionsPath(workspaceDir)

	slog.Info("Starting Copilot watcher",
		"app", p.variant.AppName,
		"workspaceDir", workspaceDir,
		"chatSessionsPath", chatSessionsPath)

	// Create fsnotify watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			slog.Warn("Failed to close watcher", "error", err)
		}
	}()

	// Sessions are delivered to sessionCallback from a single worker goroutine,
	// in arrival order. Delivery must be off the event loop (slow callback I/O —
	// markdown writes, cloud sync — must not stall fsnotify event handling), but
	// it must not be concurrent either: two in-flight callbacks for the same
	// session have no ordering guarantee, and the older snapshot could be
	// written last, leaving stale markdown until the next event. The buffer
	// absorbs event bursts; if it ever fills, the event loop blocks (the
	// per-file debounce bounds the event rate, so this is effectively never).
	sessionQueue := make(chan *spi.AgentChatSession, 64)
	var callbackWg sync.WaitGroup
	callbackWg.Go(func() {
		for session := range sessionQueue {
			deliverSession(session, sessionCallback)
		}
	})
	// On return: stop accepting deliveries, then drain the queue so no callback
	// is still writing when we exit. Runs before the watcher-close defer (LIFO).
	defer func() {
		close(sessionQueue)
		callbackWg.Wait()
	}()

	// The chatSessions directory does not exist until the workspace's first
	// Copilot chat is opened. Failing on a missing directory would permanently
	// disable a watch started before that first chat, so instead wait for VS
	// Code to create it (via a watch on the workspace directory, which always
	// exists) and only then watch it.
	dirExisted := true
	if _, statErr := os.Stat(chatSessionsPath); statErr != nil {
		dirExisted = false
		if err := waitForChatSessionsDir(ctx, watcher, workspaceDir, chatSessionsPath); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}

	// Watch the chatSessions directory
	if err := watcher.Add(chatSessionsPath); err != nil {
		return fmt.Errorf("failed to watch directory: %w", err)
	}

	slog.Info("Watching chatSessions directory", "path", chatSessionsPath)

	// Track known sessions and their modification times
	knownSessions := make(map[string]int64)

	// Debouncing map - track last processed time for each file
	lastProcessed := make(map[string]time.Time)
	debounceWindow := 500 * time.Millisecond

	// processSessionFile loads one session file and, when it is new or has
	// advanced past its last known lastMessageDate, queues it for delivery.
	// Shared by the event loop and the fresh-directory catch-up scan below.
	// Returns false when the file could not be loaded (likely caught mid-write)
	// so the event loop leaves its debounce window open and the follow-up write
	// is not swallowed.
	processSessionFile := func(path string) bool {
		composer, err := LoadSessionFile(path)
		if err != nil {
			slog.Warn("Failed to load session file", "path", path, "error", err)
			return false
		}

		sessionID := composer.SessionID
		lastKnownTime, exists := knownSessions[sessionID]
		isNew := !exists
		isUpdated := exists && composer.LastMessageDate > lastKnownTime
		if !isNew && !isUpdated {
			return true
		}

		// Update known sessions
		knownSessions[sessionID] = composer.LastMessageDate

		if isNew {
			slog.Info("New session detected", "sessionId", sessionID, "name", composer.Name)
		} else {
			slog.Info("Session updated", "sessionId", sessionID, "name", composer.Name)
		}

		// Load state file (optional)
		state, err := LoadStateFile(workspaceDir, sessionID)
		if err != nil {
			slog.Warn("Failed to load state file", "sessionId", sessionID, "error", err)
		}

		// Same emptiness rule as the read paths (GetAgentChatSessions et al):
		// a just-opened chat writes its session file before any content
		// exists, and emitting it would create an empty markdown file. The
		// session stays tracked in knownSessions, so the first real message
		// still arrives as an update once lastMessageDate advances.
		if len(composer.Requests) == 0 && !hasEditingActivity(state) {
			slog.Debug("Skipping empty session (no chat or editing activity)", "sessionId", sessionID)
			return true
		}

		// Convert to AgentChatSession
		session := p.ConvertToSessionData(*composer, projectPath, state)

		// Write debug files if requested
		if debugRaw {
			if err := WriteDebugFiles(composer, sessionID); err != nil {
				slog.Warn("Failed to write debug files", "sessionId", sessionID, "error", err)
			}
		}

		// Hand off to the delivery worker; give up if the user interrupts while
		// the queue is full so shutdown can't hang on a stuck callback (the
		// event loop exits on its own context check).
		slog.Info("Queueing callback for session", "sessionId", sessionID, "slug", session.Slug)
		select {
		case sessionQueue <- &session:
		case <-ctx.Done():
		}
		return true
	}

	if dirExisted {
		// Track existing sessions as known without emitting them: historical
		// sessions were already synced, and re-emitting on every watch start
		// would rewrite their markdown needlessly.
		existingSessions, err := LoadAllSessionFiles(workspaceDir)
		if err != nil {
			slog.Warn("Failed to load existing sessions", "error", err)
		} else {
			for _, sessionPath := range existingSessions {
				composer, err := LoadSessionFile(sessionPath)
				if err != nil {
					slog.Warn("Failed to load session", "path", sessionPath, "error", err)
					continue
				}

				// Track as known
				knownSessions[composer.SessionID] = composer.LastMessageDate

				slog.Debug("Tracked existing session", "sessionId", composer.SessionID)
			}
		}
	} else {
		// The directory was created while we were waiting, so any file already
		// inside landed between the create event and our watch registration and
		// will produce no event of its own. Process (and emit) those now, or the
		// project's very first chat message could be missed.
		if files, err := LoadAllSessionFiles(workspaceDir); err == nil {
			for _, path := range files {
				processSessionFile(path)
			}
		}
	}

	// Event loop
	for {
		select {
		case <-ctx.Done():
			slog.Info("Watcher context canceled, stopping")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher events channel closed")
			}

			// Only process JSON and JSONL files
			if !strings.HasSuffix(event.Name, ".json") && !strings.HasSuffix(event.Name, ".jsonl") {
				continue
			}

			// Only process Write and Create events
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			// Debounce rapid events for the same file
			now := time.Now()
			if lastTime, ok := lastProcessed[event.Name]; ok && now.Sub(lastTime) < debounceWindow {
				slog.Debug("Debouncing rapid event", "path", event.Name)
				continue
			}

			slog.Debug("File event detected", "path", event.Name, "op", event.Op)

			// Don't record the debounce timestamp on a load failure: the file
			// was likely caught mid-write, and the follow-up write event must
			// not be swallowed by the debounce window or the update is lost.
			if processSessionFile(event.Name) {
				lastProcessed[event.Name] = now
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher errors channel closed")
			}
			slog.Warn("Watcher error", "error", err)
		}
	}
}

// waitForChatSessionsDir blocks until the workspace's chatSessions directory
// exists, watching the workspace directory for its creation. Returns nil once
// the directory exists or the context is canceled — callers must check ctx to
// tell the two apart. The workspace-directory watch is removed on return so the
// caller's chatSessions watch is the only one left on the shared watcher.
func waitForChatSessionsDir(ctx context.Context, watcher *fsnotify.Watcher, workspaceDir, chatSessionsPath string) error {
	if err := watcher.Add(workspaceDir); err != nil {
		return fmt.Errorf("failed to watch workspace directory: %w", err)
	}
	defer func() {
		if err := watcher.Remove(workspaceDir); err != nil {
			slog.Warn("Failed to stop watching workspace directory", "error", err)
		}
	}()

	slog.Info("chatSessions directory does not exist yet; waiting for the first Copilot chat",
		"path", chatSessionsPath)

	for {
		// Stat before waiting: the directory may already exist by the time the
		// watch is registered, or appear without a matching event (any event in
		// the workspace directory triggers a re-check rather than matching on
		// event names, which could miss a rename-into-place).
		if _, err := os.Stat(chatSessionsPath); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("watcher events channel closed")
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("watcher errors channel closed")
			}
			slog.Warn("Watcher error while waiting for chatSessions directory", "error", err)
		}
	}
}

// deliverSession invokes the callback with panic isolation so a failure during
// processing (e.g. markdown write or cloud sync) can't crash the watcher.
func deliverSession(session *spi.AgentChatSession, sessionCallback func(*spi.AgentChatSession)) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("Session callback panicked", "panic", r, "sessionId", session.SessionID)
		}
	}()
	sessionCallback(session)
}
