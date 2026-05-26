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
// the same content is not re-emitted on every filesystem event.
type watchState struct {
	lastProcessed map[string]int64
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

	state := &watchState{lastProcessed: make(map[string]int64)}

	// Initial scan so existing sessions are emitted immediately.
	if err := scanAndProcessConversations(projectPath, debugRaw, sessionCallback, state); err != nil {
		slog.Debug("antigravity: initial scan failed", "error", err)
	}

	if err := setupWatches(watcher, brainDir); err != nil {
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
// level that currently exists. It is idempotent — re-adding an existing watch is
// a no-op — so it is safe to call on every relevant event.
func setupWatches(watcher *fsnotify.Watcher, brainDir string) error {
	if _, err := os.Stat(brainDir); os.IsNotExist(err) {
		ancestor := nearestExistingDir(filepath.Dir(brainDir))
		if ancestor == "" {
			return nil
		}
		slog.Debug("antigravity: brain dir missing, watching ancestor", "path", ancestor)
		return watcher.Add(ancestor)
	}

	if err := watcher.Add(brainDir); err != nil {
		return err
	}
	addConversationWatches(watcher, brainDir)
	return nil
}

// addConversationWatches adds a watch on each existing nested directory level of
// every conversation (<id>, <id>/.system_generated, and the logs dir that
// directly holds the transcript). Levels that don't exist yet are added later as
// their parent's Create event arrives.
func addConversationWatches(watcher *fsnotify.Watcher, brainDir string) {
	entries, err := os.ReadDir(brainDir)
	if err != nil {
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
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				_ = watcher.Add(dir)
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

	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			// A new directory appeared somewhere in the chain (possibly brain/
			// itself, or a new conversation's nested dirs). Re-establish watches
			// so the eventual transcript write is caught.
			if err := setupWatches(watcher, brainDir); err != nil {
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

	if preferredPath, err := resolveTranscriptPath(conversationID); err == nil && preferredPath != "" {
		transcriptPath = preferredPath
	} else if err != nil {
		slog.Debug("antigravity: unable to resolve preferred transcript", "conversationId", conversationID, "error", err)
		return
	}

	info, err := os.Stat(transcriptPath)
	if err != nil {
		return
	}
	modTime := info.ModTime().UnixNano()
	if last, ok := state.lastProcessed[transcriptPath]; ok && last >= modTime {
		return
	}

	history, _ := loadHistoryIndex()
	projectWorkspaces, _ := loadConversationWorkspaceIndex()
	session, err := parseTranscript(conversationID, transcriptPath, history, projectWorkspaces, true)
	if err != nil {
		slog.Debug("antigravity: skipping session", "path", transcriptPath, "error", err)
		return
	}
	if !sessionMatchesProject(session, projectPath) {
		return
	}
	chat := convertToAgentSession(session, projectPath, debugRaw)
	if chat == nil {
		return
	}

	state.lastProcessed[transcriptPath] = modTime
	dispatchSession(sessionCallback, chat)
}

// scanAndProcessConversations processes every conversation that has changed
// since last seen.
func scanAndProcessConversations(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) error {
	files, err := listConversationFiles()
	if err != nil {
		return err
	}
	history, _ := loadHistoryIndex()
	projectWorkspaces, _ := loadConversationWorkspaceIndex()

	for _, file := range files {
		if last, ok := state.lastProcessed[file.Path]; ok && last >= file.ModTime {
			continue
		}
		session, err := parseTranscript(file.ConversationID, file.Path, history, projectWorkspaces, true)
		if err != nil {
			slog.Debug("antigravity: skipping session", "path", file.Path, "error", err)
			continue
		}
		if !sessionMatchesProject(session, projectPath) {
			continue
		}
		chat := convertToAgentSession(session, projectPath, debugRaw)
		if chat == nil {
			continue
		}
		state.lastProcessed[file.Path] = file.ModTime
		dispatchSession(sessionCallback, chat)
	}
	return nil
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
