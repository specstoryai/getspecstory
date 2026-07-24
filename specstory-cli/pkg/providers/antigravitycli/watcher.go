package antigravitycli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// watchState tracks the last-processed modification time per transcript file so
// the same content is not re-emitted on every filesystem event, and the set of
// directories already handed to fsnotify so re-establishing watches after each
// directory creation stays cheap.
type watchState struct {
	lastProcessed map[string]int64
	watchedDirs   map[string]bool
}

func newWatchState() *watchState {
	return &watchState{
		lastProcessed: make(map[string]int64),
		watchedDirs:   make(map[string]bool),
	}
}

// watchSessions watches Antigravity CLI transcripts for new or modified content
// and invokes sessionCallback for each change. Antigravity stores each
// conversation's transcript deep under brain/<id>/.system_generated/logs/, and
// fsnotify is non-recursive, so we maintain a watch on the brain directory plus
// each nested directory level and extend the watches as new ones appear.
func watchSessions(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if sessionCallback == nil {
		return fmt.Errorf("session callback is required")
	}

	brainDir, err := resolveBrainDir()
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("antigravity: failed to create watcher: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	state := newWatchState()

	// Initial scan so existing sessions are emitted immediately.
	if err := scanAndProcessConversations(projectPath, debugRaw, sessionCallback, state); err != nil {
		slog.Debug("antigravity: initial scan failed", "error", err)
	}

	if err := setupWatches(watcher, brainDir, state); err != nil {
		slog.Debug("antigravity: setup watches failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			handleWatchEvent(event, watcher, brainDir, projectPath, debugRaw, sessionCallback, state)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Debug("antigravity: watcher error", "error", err)
		}
	}
}

// setupWatches (re)establishes the watch set. If the brain directory does not
// exist yet it watches the nearest existing ancestor so brain/ creation is
// noticed; otherwise it watches brain/ and every nested conversation directory
// level that currently exists. It is safe to call on every relevant event:
// directories already watched are skipped via state.watchedDirs, so a re-scan
// during an active session costs one ReadDir rather than an fsnotify.Add per
// conversation.
func setupWatches(watcher *fsnotify.Watcher, brainDir string, state *watchState) error {
	if _, err := os.Stat(brainDir); os.IsNotExist(err) {
		ancestor := nearestExistingDir(filepath.Dir(brainDir))
		if ancestor == "" {
			return nil
		}
		slog.Debug("antigravity: brain dir missing, watching ancestor", "path", ancestor)
		return addWatch(watcher, state, ancestor)
	}

	if err := addWatch(watcher, state, brainDir); err != nil {
		return err
	}
	addConversationWatches(watcher, brainDir, state)
	return nil
}

// addWatch registers dir with the watcher unless it is already watched.
func addWatch(watcher *fsnotify.Watcher, state *watchState, dir string) error {
	if state.watchedDirs[dir] {
		return nil
	}
	if err := watcher.Add(dir); err != nil {
		return err
	}
	state.watchedDirs[dir] = true
	return nil
}

// addConversationWatches adds a watch on each existing nested directory level of
// every conversation (<id>, <id>/.system_generated, and the logs dir that
// directly holds the transcript). Levels that don't exist yet are added later as
// their parent's Create event arrives.
func addConversationWatches(watcher *fsnotify.Watcher, brainDir string, state *watchState) {
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		// Without this listing no conversation is watched at all, so surface it.
		slog.Debug("antigravity: cannot list brain dir for watches", "path", brainDir, "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		idDir := filepath.Join(brainDir, entry.Name())
		levels := []string{
			idDir,
			filepath.Join(idDir, systemGeneratedDir),
			filepath.Join(idDir, systemGeneratedDir, logsDirName),
		}
		for _, dir := range levels {
			if state.watchedDirs[dir] {
				continue
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				continue
			}
			if err := addWatch(watcher, state, dir); err != nil {
				slog.Debug("antigravity: cannot watch transcript dir", "path", dir, "error", err)
			}
		}
	}
}

// nearestExistingDir walks up from dir and returns the first existing directory,
// or "" if none is found before reaching the filesystem root.
func nearestExistingDir(dir string) string {
	for dir != "" && dir != "." {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

// handleWatchEvent processes a filesystem event: directory creation extends the
// watch set (and triggers a scan when the brain dir itself appears), while a
// transcript write/create is parsed and dispatched.
func handleWatchEvent(event fsnotify.Event, watcher *fsnotify.Watcher, brainDir string, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) {
	slog.Debug("antigravity: received fs event", "op", event.Op.String(), "path", event.Name)

	// fsnotify drops the watch on a directory that goes away. Forget it so a
	// path that is recreated later (or renamed into place) is watched again
	// rather than being skipped as already-watched.
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		delete(state.watchedDirs, event.Name)
	}

	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			// A new directory appeared somewhere in the chain (possibly brain/
			// itself, or a new conversation's nested dirs). Re-establish watches
			// so the eventual transcript write is caught.
			if err := setupWatches(watcher, brainDir, state); err != nil {
				slog.Debug("antigravity: re-setup watches failed", "error", err)
			}
			if event.Name == brainDir {
				if err := scanAndProcessConversations(projectPath, debugRaw, sessionCallback, state); err != nil {
					slog.Debug("antigravity: scan after brain create failed", "error", err)
				}
			}
			return
		}
	}

	if !isTranscriptPath(event.Name) {
		return
	}
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}
	processTranscriptFile(event.Name, projectPath, debugRaw, sessionCallback, state)
}

// isTranscriptPath reports whether a path is a conversation transcript file.
func isTranscriptPath(path string) bool {
	base := filepath.Base(path)
	return base == transcriptFileName || base == fallbackTranscriptFileName
}

// conversationIDFromTranscriptPath recovers the conversation id (the brain
// subdirectory name) from a transcript path of the form
// brain/<id>/.system_generated/logs/transcript_full.jsonl.
func conversationIDFromTranscriptPath(transcriptPath string) string {
	logsDir := filepath.Dir(transcriptPath)
	sysDir := filepath.Dir(logsDir)
	idDir := filepath.Dir(sysDir)
	return filepath.Base(idDir)
}

// processTranscriptFile parses and dispatches a single transcript if it changed
// since last processed and matches the project filter.
func processTranscriptFile(transcriptPath string, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) {
	conversationID := conversationIDFromTranscriptPath(transcriptPath)
	if conversationID == "" {
		return
	}

	// An event may fire on the fallback transcript while the richer
	// transcript_full.jsonl also exists; always emit from the preferred file.
	preferredPath, err := resolveTranscriptPath(conversationID)
	if err != nil {
		slog.Debug("antigravity: unable to resolve preferred transcript", "conversationId", conversationID, "error", err)
		return
	}
	if preferredPath == "" {
		return
	}

	info, err := os.Stat(preferredPath)
	if err != nil {
		slog.Debug("antigravity: cannot stat transcript", "path", preferredPath, "error", err)
		return
	}

	history, projectWorkspaces := loadWorkspaceIndexes()
	emitConversation(conversationFile{
		ConversationID: conversationID,
		Path:           preferredPath,
		ModTime:        info.ModTime().UnixNano(),
	}, projectPath, debugRaw, history, projectWorkspaces, sessionCallback, state)
}

// scanAndProcessConversations processes every conversation that has changed
// since last seen.
func scanAndProcessConversations(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) error {
	files, err := listConversationFiles()
	if err != nil {
		return err
	}
	history, projectWorkspaces := loadWorkspaceIndexes()

	for _, file := range files {
		emitConversation(file, projectPath, debugRaw, history, projectWorkspaces, sessionCallback, state)
	}
	return nil
}

// emitConversation dispatches one conversation to the callback when it has
// changed since it was last seen and belongs to the project being watched.
// Both the event-driven and scanning paths funnel through here so they cannot
// drift apart on what counts as new or in-project.
func emitConversation(file conversationFile, projectPath string, debugRaw bool,
	history map[string]historyEntry, projectWorkspaces map[string]string,
	sessionCallback func(*spi.AgentChatSession), state *watchState) {

	if last, ok := state.lastProcessed[file.Path]; ok && last >= file.ModTime {
		return
	}
	chat := convertConversation(file, projectPath, debugRaw, history, projectWorkspaces)
	if chat == nil {
		return
	}
	state.lastProcessed[file.Path] = file.ModTime
	dispatchSession(sessionCallback, chat)
}

// dispatchSession invokes the session callback in a goroutine with panic
// recovery so a misbehaving callback cannot crash the watcher.
func dispatchSession(sessionCallback func(*spi.AgentChatSession), session *spi.AgentChatSession) {
	if sessionCallback == nil || session == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("antigravity: session callback panicked", "panic", r)
			}
		}()
		sessionCallback(session)
	}()
}
