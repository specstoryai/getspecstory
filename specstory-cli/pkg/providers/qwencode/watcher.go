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

// WatchQwenProject watches ~/.qwen/projects/<sanitized-cwd>/chats for session
// transcript changes. Directories that don't exist yet (fresh install, first
// session in a project) are awaited via fsnotify on their parent, stepping
// down the chain: ~/.qwen → projects → <sanitized-cwd> → chats.
func WatchQwenProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	// Default the path before recording it as the workspace root, so sessions
	// converted by the watcher never carry an empty root.
	projectPath, err := defaultProjectPath(projectPath)
	if err != nil {
		return err
	}

	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)

	projectsDir, err := GetQwenProjectsDir()
	if err != nil {
		return fmt.Errorf("failed to get qwen projects dir: %w", err)
	}
	qwenDir := filepath.Dir(projectsDir) // ~/.qwen

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

		projectDir, err := waitForProjectDir(watcherCtx, projectsDir, projectPath)
		if err != nil {
			slog.Debug("Stopped Qwen watcher while waiting for project directory", "error", err)
			return
		}

		chatsDir := filepath.Join(projectDir, "chats")
		if err := waitForDirectoryFsnotify(watcherCtx, chatsDir, "Qwen chats directory"); err != nil {
			slog.Debug("Stopped Qwen watcher while waiting for chats directory", "error", err)
			return
		}

		if err := startChatsWatcher(chatsDir); err != nil {
			slog.Error("Failed to start Qwen chats watcher", "error", err)
		}
	}()

	return nil
}

// waitForProjectDir waits for the project's sanitized directory to appear
// under projectsDir. Both candidate names (canonical and absolute path
// sanitizations) are accepted.
func waitForProjectDir(ctx context.Context, projectsDir, projectPath string) (string, error) {
	candidates := candidateProjectDirNames(projectPath)

	checkAll := func() string {
		for _, name := range candidates {
			dir := filepath.Join(projectsDir, name)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				return dir
			}
		}
		return ""
	}

	if dir := checkAll(); dir != "" {
		return dir, nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return "", fmt.Errorf("failed to create fsnotify watcher for project dir: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(projectsDir); err != nil {
		return "", fmt.Errorf("failed to watch projects directory %q: %w", projectsDir, err)
	}

	slog.Debug("Qwen watcher: watching for project directory creation",
		"projectsDir", projectsDir, "candidates", candidates)

	// Re-check after adding watcher to close the race window
	if dir := checkAll(); dir != "" {
		return dir, nil
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return "", fmt.Errorf("fsnotify events channel closed for project dir watcher")
			}
			if !event.Has(fsnotify.Create) {
				continue
			}
			if dir := checkAll(); dir != "" {
				return dir, nil
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return "", fmt.Errorf("fsnotify errors channel closed for project dir watcher")
			}
			return "", fmt.Errorf("fsnotify watcher error for project dir: %w", err)
		}
	}
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
				// Only .jsonl transcripts matter; the chats dir also holds
				// <session-id>.runtime.json files that churn during a session.
				if strings.HasSuffix(event.Name, ".jsonl") && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
					processSessionChange(event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Error("Qwen watcher error", "error", err)
			}
		}
	}()

	return nil
}

func processSessionChange(filePath string) {
	slog.Debug("processSessionChange: Detected Qwen session file change", "file", filePath)

	session, err := ParseSessionFile(filePath)
	if err != nil {
		slog.Error("processSessionChange: Failed to parse session file", "file", filePath, "error", err)
		return
	}
	if len(session.Records) == 0 {
		slog.Debug("processSessionChange: Session file has no records yet", "file", filePath)
		return
	}

	agentSession := convertToAgentChatSession(session, getWatcherWorkspaceRoot(), getWatcherDebugRaw())
	triggerCallback(agentSession)
}

// triggerCallback is a helper to call the watcher callback with proper locking.
//
// Delivery is synchronous so transcript changes reach the consumer in the order
// fsnotify reported them, but a panic in the consumer is contained here: it
// would otherwise unwind the fsnotify event goroutine and take down the whole
// process over one malformed session.
func triggerCallback(agentSession *spi.AgentChatSession) {
	watcherMutex.RLock()
	cb := watcherCallback
	watcherMutex.RUnlock()

	if cb == nil || agentSession == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Error("qwen: session callback panicked", "sessionId", agentSession.SessionID, "panic", r)
		}
	}()
	cb(agentSession)
}

// waitForDirectoryFsnotify waits for a directory to exist using fsnotify on its parent.
// The parent directory must already exist; if it doesn't, the function returns an error.
// label is a human-readable name used in log messages (e.g., "Qwen projects directory").
func waitForDirectoryFsnotify(ctx context.Context, dir string, label string) error {
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
