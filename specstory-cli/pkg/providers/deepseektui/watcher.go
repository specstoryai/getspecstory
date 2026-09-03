package deepseektui

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

type watchState struct {
	lastProcessed map[string]int64
}

// watchSessions watches the DeepSeek TUI sessions directory for new or modified
// session files and invokes sessionCallback for each change.
func watchSessions(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if sessionCallback == nil {
		return fmt.Errorf("session callback is required")
	}

	sessionsDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("deepseek: failed to create watcher: %w", err)
	}
	defer func() {
		_ = watcher.Close()
	}()

	state := &watchState{lastProcessed: make(map[string]int64)}

	// Record what is already on disk instead of emitting it.
	seedProcessedSessions(state)

	// Set up watching on the sessions directory.
	if err := setupWatcher(watcher, sessionsDir); err != nil {
		slog.Debug("deepseek: setup watches failed", "error", err)
	}

	// Event loop.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			handleWatchEvent(event, watcher, sessionsDir, projectPath, debugRaw, sessionCallback, state)
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Debug("deepseek: watcher error", "error", err)
		}
	}
}

// setupWatcher adds a watch on the sessions directory.
// If the directory doesn't exist yet, it watches the parent .deepseek directory
// so we can detect when sessions/ is created.
func setupWatcher(watcher *fsnotify.Watcher, sessionsDir string) error {
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		// Watch parent .deepseek directory.
		parentDir := filepath.Dir(sessionsDir)
		if _, err := os.Stat(parentDir); os.IsNotExist(err) {
			homeDir := filepath.Dir(parentDir)
			slog.Debug("deepseek: watching home for .deepseek creation", "path", homeDir)
			return watcher.Add(homeDir)
		}
		slog.Debug("deepseek: watching .deepseek for sessions creation", "path", parentDir)
		return watcher.Add(parentDir)
	}

	slog.Debug("deepseek: watching sessions directory", "path", sessionsDir)
	return watcher.Add(sessionsDir)
}

// handleWatchEvent processes a filesystem event for new or modified session files.
func handleWatchEvent(event fsnotify.Event, watcher *fsnotify.Watcher, sessionsDir string, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) {
	slog.Debug("deepseek: received fs event", "op", event.Op.String(), "path", event.Name)

	// Handle directory creation — add new watches.
	if event.Has(fsnotify.Create) {
		info, err := os.Stat(event.Name)
		if err != nil {
			return
		}
		if info.IsDir() {
			// If sessions dir itself was just created, set up proper watch.
			if event.Name == sessionsDir {
				if err := setupWatcher(watcher, sessionsDir); err != nil {
					slog.Debug("deepseek: failed to setup watch on new sessions dir", "error", err)
				}
				// Scan immediately for any pre-existing files.
				if scanErr := scanAndProcessSessions(projectPath, debugRaw, sessionCallback, state); scanErr != nil {
					slog.Debug("deepseek: scan after new dir watch failed", "error", scanErr)
				}
			}
			return
		}
	}

	// Only process .json files.
	if !strings.HasSuffix(event.Name, ".json") {
		return
	}

	// Process on Create and Write events.
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}

	slog.Debug("deepseek: json file event", "op", event.Op.String(), "file", event.Name)
	processSessionFile(event.Name, projectPath, debugRaw, sessionCallback, state)
}

// processSessionFile processes a single JSON session file if it has been modified.
func processSessionFile(filePath string, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) {
	info, err := os.Stat(filePath)
	if err != nil {
		return
	}

	modTime := info.ModTime().UnixNano()
	if last, ok := state.lastProcessed[filePath]; ok && last >= modTime {
		return
	}

	if projectPath != "" && !sessionMentionsProject(filePath, projectPath) {
		return
	}

	session, err := parseSessionFile(filePath, true)
	if err != nil {
		slog.Debug("deepseek: skipping session", "path", filePath, "error", err)
		return
	}

	chat := convertToAgentSession(session, projectPath, debugRaw)
	if chat == nil {
		return
	}

	state.lastProcessed[filePath] = modTime
	spi.DispatchSession("deepseek", sessionCallback, chat)
}

// seedProcessedSessions marks every session already on disk as seen, without
// parsing or emitting any of it. Those sessions predate the watcher and are
// `sync`'s to save; re-emitting them on every start would rewrite their
// markdown and re-sync them for content that has not changed. Recording only
// the modification times means the next scan still emits any of them that
// DeepSeek actually touches.
//
// Failures are non-fatal: the worst case is the first scan re-emitting existing
// sessions, which is the behavior this exists to avoid rather than a
// correctness problem.
func seedProcessedSessions(state *watchState) {
	files, err := listSessionFiles()
	if err != nil {
		slog.Debug("deepseek: could not seed existing sessions", "error", err)
		return
	}
	for _, file := range files {
		state.lastProcessed[file.Path] = file.ModTime
	}
	slog.Debug("deepseek: seeded existing sessions as known", "count", len(files))
}

// scanAndProcessSessions scans all session files and processes any that have been modified.
func scanAndProcessSessions(projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) error {
	files, err := listSessionFiles()
	if err != nil {
		return err
	}

	for _, file := range files {
		if last, ok := state.lastProcessed[file.Path]; ok && last >= file.ModTime {
			continue
		}
		if projectPath != "" && !sessionMentionsProject(file.Path, projectPath) {
			continue
		}
		session, err := parseSessionFile(file.Path, true)
		if err != nil {
			slog.Debug("deepseek: skipping session", "path", file.Path, "error", err)
			continue
		}
		chat := convertToAgentSession(session, projectPath, debugRaw)
		if chat == nil {
			continue
		}
		state.lastProcessed[file.Path] = file.ModTime
		spi.DispatchSession("deepseek", sessionCallback, chat)
	}
	return nil
}
