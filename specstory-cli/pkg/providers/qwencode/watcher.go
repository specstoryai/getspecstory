package qwencode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var (
	watcherCtx           context.Context
	watcherCancel        context.CancelFunc
	watcherWg            sync.WaitGroup
	watcherCallback      func(*spi.AgentChatSession)
	watcherMutex         sync.RWMutex
	watcherDebugRaw      bool
	watcherWorkspaceRoot string
)

func init() {
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
}

func SetWatcherCallback(callback func(*spi.AgentChatSession)) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = callback
}

func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
}

func SetWatcherWorkspaceRoot(workspaceRoot string) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherWorkspaceRoot = workspaceRoot
}

func getWatcherWorkspaceRoot() string {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherWorkspaceRoot
}

func getWatcherDebugRaw() bool {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherDebugRaw
}

func StopWatcher() {
	watcherCancel()
	watcherWg.Wait()
}

// WatchQwenProject starts watching the Qwen Code chats directory for the
// given project and returns immediately. Session updates are delivered via
// callback. The directory chain (~/.qwen → projects → <project> → chats) is
// waited on step by step so a watcher can be installed before Qwen Code
// creates any of it.
func WatchQwenProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)

	qwenDir, err := GetQwenDir()
	if err != nil {
		return fmt.Errorf("failed to get qwen directory: %w", err)
	}
	projectsDir := filepath.Join(qwenDir, "projects")

	projectDir, err := resolveQwenProjectDir(projectPath)
	if err != nil {
		return fmt.Errorf("failed to resolve qwen project directory: %w", err)
	}

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()

		if err := waitForDirectoryFsnotify(watcherCtx, qwenDir, "Qwen root directory"); err != nil {
			slog.Debug("Stopped Qwen watcher while waiting for root directory", "error", err)
			return
		}

		if err := waitForDirectoryFsnotify(watcherCtx, projectsDir, "Qwen projects directory"); err != nil {
			slog.Debug("Stopped Qwen watcher while waiting for projects directory", "error", err)
			return
		}

		if err := waitForDirectoryFsnotify(watcherCtx, projectDir, "Qwen project directory"); err != nil {
			slog.Debug("Stopped Qwen watcher while waiting for project directory", "error", err)
			return
		}

		chatsDir := filepath.Join(projectDir, "chats")
		if err := waitForDirectoryFsnotify(watcherCtx, chatsDir, "Qwen chats directory"); err != nil {
			slog.Debug("Stopped Qwen watcher while waiting for chats directory", "error", err)
			return
		}

		if err := startChatsWatcher(chatsDir); err != nil {
			slog.Error("Failed to start chats watcher", "error", err)
		}
	}()

	return nil
}

func startChatsWatcher(chatsDir string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()
		defer func() {
			_ = watcher.Close()
		}()

		if err := watcher.Add(chatsDir); err != nil {
			slog.Error("Failed to add chats dir to watcher", "error", err)
			return
		}

		for {
			select {
			case <-watcherCtx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Qwen Code writes <session-id>.jsonl transcripts and
				// <session-id>.runtime.json process markers; only the
				// transcripts carry conversation content.
				if strings.HasSuffix(event.Name, ".jsonl") && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
					processSessionChange(event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Watcher error", "error", err)
			}
		}
	}()

	return nil
}

func processSessionChange(filePath string) {
	slog.Debug("processSessionChange: Detected session file change", "file", filePath)

	session, err := ParseSessionFile(filePath)
	if err != nil {
		slog.Error("processSessionChange: Failed to parse session file", "file", filePath, "error", err)
		return
	}

	slog.Info("processSessionChange: Successfully processed session change",
		"sessionId", session.ID,
		"entryCount", len(session.Entries),
		"startTime", session.StartTime,
		"lastUpdated", session.LastUpdated)

	agentSession := convertToAgentChatSession(session, getWatcherWorkspaceRoot(), getWatcherDebugRaw())
	triggerCallback(agentSession)
}

// triggerCallback is a helper to call the watcher callback with proper locking
func triggerCallback(agentSession *spi.AgentChatSession) {
	watcherMutex.RLock()
	cb := watcherCallback
	watcherMutex.RUnlock()

	if cb != nil && agentSession != nil {
		cb(agentSession)
	}
}

// waitForDirectoryFsnotify waits for a directory to exist using fsnotify on its parent.
// The parent directory must already exist; if it doesn't, the function returns an error.
// label is a human-readable name used in log messages (e.g., "Qwen chats directory").
func waitForDirectoryFsnotify(ctx context.Context, dir string, label string) error {
	// Check if directory already exists
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		slog.Debug("Qwen watcher: directory ready", "label", label, "path", dir)
		return nil
	}

	parentDir := filepath.Dir(dir)
	childName := filepath.Base(dir)

	// Parent must exist — caller guarantees this via the sequential wait chain
	if _, err := os.Stat(parentDir); err != nil {
		return fmt.Errorf("parent directory %q does not exist for %s: %w", parentDir, label, err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher for %s: %w", label, err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(parentDir); err != nil {
		return fmt.Errorf("failed to watch parent directory %q for %s: %w", parentDir, label, err)
	}

	slog.Debug("Qwen watcher: watching for directory creation", "label", label, "parent", parentDir, "child", childName)

	// Re-check after adding watcher to close the race window
	info, err = os.Stat(dir)
	if err == nil && info.IsDir() {
		slog.Debug("Qwen watcher: directory ready", "label", label, "path", dir)
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return fmt.Errorf("fsnotify events channel closed for %s", label)
			}
			if event.Has(fsnotify.Create) && filepath.Base(event.Name) == childName {
				slog.Debug("Qwen watcher: directory ready", "label", label, "path", dir)
				return nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return fmt.Errorf("fsnotify errors channel closed for %s", label)
			}
			return fmt.Errorf("fsnotify watcher error for %s: %w", label, err)
		}
	}
}
