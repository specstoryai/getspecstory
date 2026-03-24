package droidcli

import (
	"context"
	"fmt"
	"io/fs"
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
		return fmt.Errorf("droidcli: failed to create watcher: %w", err)
	}
	defer func() {
		_ = watcher.Close()
	}()

	state := &watchState{lastProcessed: make(map[string]int64)}

	// Initial scan to process existing sessions
	if err := scanAndProcessSessions(projectPath, debugRaw, sessionCallback, state); err != nil {
		slog.Debug("droidcli: initial scan failed", "error", err)
	}

	// Setup directory watching (handles non-existent dirs)
	if err := setupDirectoryWatches(watcher, sessionsDir, projectPath); err != nil {
		slog.Debug("droidcli: setup watches failed", "error", err)
	}

	// Event loop
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
			slog.Debug("droidcli: watcher error", "error", err)
		}
	}
}

// setupDirectoryWatches adds watches for the relevant session directories.
// When projectPath is set, only the project's derived subdirectory is watched so
// that unrelated projects' session directories are not monitored.
// If the required directory doesn't exist yet, parent directories are watched so
// creation can be detected.
func setupDirectoryWatches(watcher *fsnotify.Watcher, sessionsDir string, projectPath string) error {
	// Check if sessions directory exists
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		// Watch parent (.factory) for sessions dir creation
		parentDir := filepath.Dir(sessionsDir)
		if _, err := os.Stat(parentDir); os.IsNotExist(err) {
			// Watch home for .factory creation
			homeDir := filepath.Dir(parentDir)
			slog.Debug("droidcli: watching home for .factory creation", "path", homeDir)
			return watcher.Add(homeDir)
		}
		slog.Debug("droidcli: watching .factory for sessions creation", "path", parentDir)
		return watcher.Add(parentDir)
	}

	// When a project path is known, watch only that project's session subdirectory
	// rather than every project's directory under sessionsDir.
	if projectPath != "" {
		projectSessionDir, err := resolveProjectSessionDir(projectPath)
		if err != nil {
			slog.Debug("droidcli: failed to resolve project session dir, falling back to all", "error", err)
		} else if projectSessionDir != "" {
			if _, err := os.Stat(projectSessionDir); os.IsNotExist(err) {
				// Project dir not created yet; watch sessionsDir so we see it when it appears.
				slog.Debug("droidcli: watching sessions dir for project directory creation", "path", sessionsDir)
				return watcher.Add(sessionsDir)
			}
			slog.Debug("droidcli: watching project session directory", "path", projectSessionDir)
			return watcher.Add(projectSessionDir)
		}
	}

	// No project filter (or resolution failed): watch sessionsDir and all subdirectories.
	return filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if watchErr := watcher.Add(path); watchErr != nil {
				slog.Debug("droidcli: failed to watch directory", "path", path, "error", watchErr)
			} else {
				slog.Debug("droidcli: watching directory", "path", path)
			}
		}
		return nil
	})
}

// handleWatchEvent processes fsnotify events, adding watches to new directories
// and processing changed JSONL files.
func handleWatchEvent(event fsnotify.Event, watcher *fsnotify.Watcher, sessionsDir string, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession), state *watchState) {
	slog.Debug("droidcli: received fs event", "op", event.Op.String(), "path", event.Name)

	// Handle directory creation - add new watch
	if event.Has(fsnotify.Create) {
		info, err := os.Stat(event.Name)
		if err != nil {
			slog.Debug("droidcli: stat failed on created path", "path", event.Name, "error", err)
		} else if info.IsDir() {
			// When sessionsDir itself is created, set up watches properly.
			if event.Name == sessionsDir {
				if err := setupDirectoryWatches(watcher, sessionsDir, projectPath); err != nil {
					slog.Debug("droidcli: failed to setup watches for new sessions dir", "error", err)
				}
				return
			}
			// For a new project subdirectory, only watch it if it belongs to the current project.
			if isTargetProjectDir(event.Name, projectPath) {
				if watchErr := watcher.Add(event.Name); watchErr == nil {
					slog.Debug("droidcli: watching new directory", "path", event.Name)
					// Scan immediately in case the JSONL file was already written before we added the watch.
					if scanErr := scanAndProcessSessions(projectPath, debugRaw, sessionCallback, state); scanErr != nil {
						slog.Debug("droidcli: scan after new directory watch failed", "error", scanErr)
					}
				}
			} else {
				slog.Debug("droidcli: ignoring directory not belonging to project", "path", event.Name, "project", projectPath)
			}
			return
		}
	}

	// Only process .jsonl files
	if !strings.HasSuffix(event.Name, ".jsonl") {
		return
	}

	// Only process Create and Write events
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}

	slog.Debug("droidcli: jsonl file event", "op", event.Op.String(), "file", event.Name)

	// Process the specific file that changed
	processSessionFile(event.Name, projectPath, debugRaw, sessionCallback, state)
}

// processSessionFile processes a single JSONL file if it has been modified.
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

	session, err := parseFactorySession(filePath)
	if err != nil {
		slog.Debug("droidcli: skipping session", "path", filePath, "error", err)
		return
	}

	chat := convertToAgentSession(session, projectPath, debugRaw)
	if chat == nil {
		return
	}

	state.lastProcessed[filePath] = modTime
	dispatchSession(sessionCallback, chat)
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
		session, err := parseFactorySession(file.Path)
		if err != nil {
			slog.Debug("droidcli: skipping session", "path", file.Path, "error", err)
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

func dispatchSession(sessionCallback func(*spi.AgentChatSession), session *spi.AgentChatSession) {
	if sessionCallback == nil || session == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("droidcli: session callback panicked", "panic", r)
			}
		}()
		sessionCallback(session)
	}()
}
