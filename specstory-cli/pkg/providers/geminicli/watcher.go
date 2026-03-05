package geminicli

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

func WatchGeminiProject(projectPath string, callback func(*spi.AgentChatSession)) error {
	SetWatcherCallback(callback)
	SetWatcherWorkspaceRoot(projectPath)

	hashDir, err := GetGeminiProjectDir(projectPath)
	if err != nil {
		return fmt.Errorf("failed to get gemini project dir: %w", err)
	}

	// Build the full directory chain: ~/.gemini → tmp → resolvedDir → chats
	// Each step's parent is guaranteed to exist by the previous step.
	// $HOME (parent of geminiDir) always exists.
	geminiDir := filepath.Dir(filepath.Dir(hashDir)) // ~/.gemini
	tmpDir := filepath.Dir(hashDir)                  // ~/.gemini/tmp

	watcherWg.Add(1)
	go func() {
		defer watcherWg.Done()

		if err := waitForDirectoryFsnotify(watcherCtx, geminiDir, "Gemini root directory"); err != nil {
			slog.Debug("Stopped Gemini watcher while waiting for root directory", "error", err)
			return
		}

		if err := waitForDirectoryFsnotify(watcherCtx, tmpDir, "Gemini tmp directory"); err != nil {
			slog.Debug("Stopped Gemini watcher while waiting for tmp directory", "error", err)
			return
		}

		// Wait for either hash-based or .project_root-based project directory
		resolvedDir, err := waitForProjectDir(watcherCtx, tmpDir, projectPath, hashDir)
		if err != nil {
			slog.Debug("Stopped Gemini watcher while waiting for project directory", "error", err)
			return
		}

		chatsDir := filepath.Join(resolvedDir, "chats")
		if err := waitForDirectoryFsnotify(watcherCtx, chatsDir, "Gemini chats directory"); err != nil {
			slog.Debug("Stopped Gemini watcher while waiting for chats directory", "error", err)
			return
		}

		if err := startChatsWatcher(chatsDir); err != nil {
			slog.Error("Failed to start chats watcher", "error", err)
		}

		if err := startArtifactWatcher(filepath.Join(resolvedDir, "logs.json"), "logs.json"); err != nil {
			slog.Warn("Failed to watch logs.json", "error", err)
		}

		if err := startArtifactWatcher(filepath.Join(resolvedDir, "shell_history"), "shell_history"); err != nil {
			slog.Warn("Failed to watch shell_history", "error", err)
		}
	}()

	return nil
}

// waitForProjectDir waits for a Gemini project directory using three strategies:
//  1. Basename hint — tmpDir/<project-basename> with matching .project_root
//  2. Hash-based — the legacy hash directory
//  3. Full .project_root scan — handles suffixed dirs like my-project-1
//
// If none exist yet, it watches tmpDir for new directory creation.
func waitForProjectDir(ctx context.Context, tmpDir, projectPath, hashDir string) (string, error) {
	canonicalProjectPath := canonicalizeProjectPath(projectPath)
	hashName := filepath.Base(hashDir)

	// checkAll runs all three strategies and returns the first match.
	checkAll := func() string {
		// 1. Basename hint
		if dir := findProjectDirByBasename(tmpDir, projectPath, canonicalProjectPath); dir != "" {
			return dir
		}
		// 2. Hash-based
		if info, err := os.Stat(hashDir); err == nil && info.IsDir() {
			slog.Debug("Gemini watcher: hash-based project directory ready", "path", hashDir)
			return hashDir
		}
		// 3. Full scan
		if match, err := findProjectDirByProjectRoot(tmpDir, projectPath); err == nil && match != "" {
			return match
		}
		return ""
	}

	// Try existing directories first
	if dir := checkAll(); dir != "" {
		return dir, nil
	}

	// Nothing exists yet — watch tmpDir for new directory creation
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return "", fmt.Errorf("failed to create fsnotify watcher for project dir: %w", err)
	}
	defer func() { _ = watcher.Close() }()

	if err := watcher.Add(tmpDir); err != nil {
		return "", fmt.Errorf("failed to watch tmp directory %q: %w", tmpDir, err)
	}

	slog.Debug("Gemini watcher: watching for project directory creation",
		"tmpDir", tmpDir, "hashDir", hashName)

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
			// Re-run all strategies on any Create event rather than checking
			// only the newly created entry. This handles the race where a
			// directory was created moments ago but its .project_root file
			// wasn't yet written when we last checked.
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

// waitForDirectoryFsnotify waits for a directory to exist using fsnotify on its parent.
// The parent directory must already exist; if it doesn't, the function returns an error.
// label is a human-readable name used in log messages (e.g., "Gemini tmp directory").
func waitForDirectoryFsnotify(ctx context.Context, dir string, label string) error {
	// Check if directory already exists
	info, err := os.Stat(dir)
	if err == nil && info.IsDir() {
		slog.Debug("Gemini watcher: directory ready", "label", label, "path", dir)
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

	slog.Debug("Gemini watcher: watching for directory creation", "label", label, "parent", parentDir, "child", childName)

	// Re-check after adding watcher to close the race window
	info, err = os.Stat(dir)
	if err == nil && info.IsDir() {
		slog.Debug("Gemini watcher: directory ready", "label", label, "path", dir)
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
				slog.Debug("Gemini watcher: directory ready", "label", label, "path", dir)
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
