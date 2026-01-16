package geminicli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
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

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
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

func WatchGeminiProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)

	projectDir, err := GetGeminiProjectDir(projectPath)
	if err != nil {
		return fmt.Errorf("failed to get gemini project dir: %w", err)
	}

	tmpDir := filepath.Dir(projectDir)
	chatsDir := filepath.Join(projectDir, "chats")

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()

		if err := waitForDirectory(watcherCtx, tmpDir, "Gemini tmp directory"); err != nil {
			slog.Warn("Stopped Gemini watcher while waiting for tmp directory", "error", err)
			return
		}

		if err := waitForDirectory(watcherCtx, projectDir, "Gemini project directory"); err != nil {
			slog.Warn("Stopped Gemini watcher while waiting for project directory", "error", err)
			return
		}

		if err := waitForDirectory(watcherCtx, chatsDir, "Gemini chats directory"); err != nil {
			slog.Warn("Stopped Gemini watcher while waiting for chats directory", "error", err)
			return
		}

		if err := startChatsWatcher(chatsDir); err != nil {
			slog.Error("Failed to start chats watcher", "error", err)
		}

		if err := startArtifactWatcher(filepath.Join(projectDir, "logs.json"), "logs.json"); err != nil {
			slog.Warn("Failed to watch logs.json", "error", err)
		}

		if err := startArtifactWatcher(filepath.Join(projectDir, "shell_history"), "shell_history"); err != nil {
			slog.Warn("Failed to watch shell_history", "error", err)
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
				if strings.HasSuffix(event.Name, ".json") && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
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

	// Extract short session ID from filename to find related files
	filename := filepath.Base(filePath)
	shortID := extractSessionIDFromFilename(filename)
	if shortID == "" {
		slog.Error("processSessionChange: Could not extract session ID from filename", "file", filename)
		return
	}

	// Parse the changed file to get the full session ID
	session, err := ParseSessionFile(filePath)
	if err != nil {
		slog.Error("processSessionChange: Failed to parse session file", "file", filePath, "error", err)
		return
	}

	fullSessionID := session.ID

	// Find all files for this session to merge them
	// Why: Gemini CLI may have created multiple files for this session
	chatsDir := filepath.Dir(filePath)
	entries, err := os.ReadDir(chatsDir)
	if err != nil {
		slog.Error("processSessionChange: Failed to read chats directory", "dir", chatsDir, "error", err)
		// Fall back to single-file session
		agentSession := convertToAgentChatSession(session, getWatcherWorkspaceRoot(), getWatcherDebugRaw())
		triggerCallback(agentSession)
		return
	}

	// Collect all files that match this session's short ID
	var sessionFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if extractSessionIDFromFilename(entry.Name()) == shortID {
			sessionFiles = append(sessionFiles, filepath.Join(chatsDir, entry.Name()))
		}
	}

	slog.Debug("processSessionChange: Found session files to merge",
		"shortID", shortID,
		"fileCount", len(sessionFiles))

	// Merge all files for this session
	mergedSession, err := parseAndMergeSessionFiles(shortID, sessionFiles)
	if err != nil {
		slog.Error("processSessionChange: Failed to merge session files",
			"sessionId", fullSessionID,
			"fileCount", len(sessionFiles),
			"error", err)
		// Fall back to single-file session
		agentSession := convertToAgentChatSession(session, getWatcherWorkspaceRoot(), getWatcherDebugRaw())
		triggerCallback(agentSession)
		return
	}

	slog.Info("processSessionChange: Successfully processed session change",
		"sessionId", mergedSession.ID,
		"fileCount", len(sessionFiles),
		"messageCount", len(mergedSession.Messages),
		"startTime", mergedSession.StartTime,
		"lastUpdated", mergedSession.LastUpdated)

	agentSession := convertToAgentChatSession(mergedSession, getWatcherWorkspaceRoot(), getWatcherDebugRaw())
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

func waitForDirectory(ctx context.Context, dir string, label string) error {
	backoff := initialBackoff
	for {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			slog.Info("Gemini watcher: directory ready", "label", label, "path", dir)
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			slog.Warn("Gemini watcher: failed to stat directory", "label", label, "path", dir, "error", err)
		} else {
			slog.Info("Gemini watcher: waiting for directory", "label", label, "path", dir, "retryIn", backoff)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}
}

func startArtifactWatcher(filePath string, label string) error {
	dir := filepath.Dir(filePath)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		return err
	}

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()
		defer func() {
			_ = watcher.Close()
		}()

		slog.Info("Gemini watcher: monitoring artifact", "artifact", label, "path", filePath)

		for {
			select {
			case <-watcherCtx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Clean(event.Name) != filepath.Clean(filePath) {
					continue
				}
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
					slog.Info("Gemini artifact updated", "artifact", label, "path", event.Name, "op", event.Op.String())
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				slog.Warn("Gemini artifact watcher error", "artifact", label, "error", err)
			}
		}
	}()

	return nil
}
