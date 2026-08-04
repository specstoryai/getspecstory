package codexcli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var (
	watcherCtx      context.Context
	watcherCancel   context.CancelFunc
	watcherWg       sync.WaitGroup
	watcherCallback func(*spi.AgentChatSession) // Callback for session updates
	watcherDebugRaw bool                        // Whether to write debug raw data files
	watcherMutex    sync.RWMutex                // Protects watcherCallback and watcherDebugRaw
)

func init() {
	watcherCtx, watcherCancel = context.WithCancel(context.Background())
}

// SetWatcherCallback sets the callback function for session updates.
func SetWatcherCallback(callback func(*spi.AgentChatSession)) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = callback
	slog.Info("SetWatcherCallback: Callback set", "isNil", callback == nil)
}

// ClearWatcherCallback clears the callback function.
func ClearWatcherCallback() {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherCallback = nil
	slog.Info("ClearWatcherCallback: Callback cleared")
}

// getWatcherCallback returns the current callback function (thread-safe).
func getWatcherCallback() func(*spi.AgentChatSession) {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherCallback
}

// SetWatcherDebugRaw sets whether to write debug raw data files.
func SetWatcherDebugRaw(debugRaw bool) {
	watcherMutex.Lock()
	defer watcherMutex.Unlock()
	watcherDebugRaw = debugRaw
	slog.Debug("SetWatcherDebugRaw: Debug raw set", "debugRaw", debugRaw)
}

// getWatcherDebugRaw returns the current debug raw setting (thread-safe).
func getWatcherDebugRaw() bool {
	watcherMutex.RLock()
	defer watcherMutex.RUnlock()
	return watcherDebugRaw
}

// StopWatcher gracefully stops the watcher goroutine.
func StopWatcher() {
	slog.Info("StopWatcher: Signaling watcher to stop")
	watcherCancel()
	slog.Info("StopWatcher: Waiting for watcher goroutine to finish")
	watcherWg.Wait()
	slog.Info("StopWatcher: Watcher stopped")
}

// WatchForCodexSessions watches for Codex CLI sessions that match the given project path.
// If resumeSessionID is provided, it finds and watches the directory containing that session.
// Otherwise, watches hierarchically for new sessions, handling date changes across days/months/years.
func WatchForCodexSessions(projectPath string, resumeSessionID string) error {
	slog.Info("WatchForCodexSessions: Starting Codex session watcher",
		"projectPath", projectPath,
		"resumeSessionID", resumeSessionID)

	homeDir, err := osUserHomeDir()
	if err != nil {
		slog.Error("WatchForCodexSessions: Failed to get home directory", "error", err)
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	sessionsRoot := codexSessionsRoot(homeDir)
	slog.Info("WatchForCodexSessions: Sessions root", "path", sessionsRoot)

	var initialDayDir string

	// A resumed session may live in a day directory older than the watch window;
	// pin that directory so it stays watched for the life of the process.
	var pinnedDayDir string

	if resumeSessionID != "" {
		// Find the directory containing the resumed session
		slog.Info("WatchForCodexSessions: Finding directory for resumed session", "sessionID", resumeSessionID)

		// Use findCodexSessions to locate the specific session (will short-circuit when found)
		sessions, err := findCodexSessions(projectPath, resumeSessionID, false)
		if err != nil {
			slog.Error("WatchForCodexSessions: Failed to find resumed session", "error", err)
			return fmt.Errorf("failed to find resumed session: %w", err)
		}

		// Check if session was found
		if len(sessions) == 0 {
			slog.Error("WatchForCodexSessions: Resumed session not found", "sessionID", resumeSessionID)
			return fmt.Errorf("resumed session %s not found", resumeSessionID)
		}

		// Get the directory containing the session file
		initialDayDir = filepath.Dir(sessions[0].SessionPath)
		pinnedDayDir = initialDayDir
		slog.Info("WatchForCodexSessions: Found resumed session directory", "path", initialDayDir)
	} else {
		// Calculate today's directory (will be watched along with hierarchical watching)
		now := time.Now()
		initialDayDir = filepath.Join(sessionsRoot, fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", now.Month()), fmt.Sprintf("%02d", now.Day()))
		slog.Info("WatchForCodexSessions: Initial day directory", "path", initialDayDir)
	}

	return startCodexSessionWatcher(projectPath, sessionsRoot, initialDayDir, pinnedDayDir)
}

// dirType determines the type of directory relative to sessionsRoot based on the
// YYYY/MM/DD structure. Returns "year", "month", "day", or "" if not a recognized type.
func dirType(path string, sessionsRoot string) string {
	rel, err := filepath.Rel(sessionsRoot, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "" // Path is not under sessionsRoot
	}

	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch len(parts) {
	case 1: // YYYY
		if len(parts[0]) == 4 {
			return "year"
		}
	case 2: // YYYY/MM
		if len(parts[0]) == 4 && len(parts[1]) == 2 {
			return "month"
		}
	case 3: // YYYY/MM/DD
		if len(parts[0]) == 4 && len(parts[1]) == 2 && len(parts[2]) == 2 {
			return "day"
		}
	}
	return ""
}

// watchWindowDays is how many days of lookback before today remain under an
// active fsnotify watch; the window also includes today, so up to
// watchWindowDays+1 day directories are watched at once. Historical day
// directories are still scanned at startup, but watching them is avoided
// because fsnotify's kqueue backend on macOS holds an open file descriptor for
// every file in a watched directory. Watching all history pins one fd per
// rollout file ever written (~25k on large installs), which can exhaust the
// system-wide fd table; the trailing window keeps fd usage flat while still
// live-streaming sessions that span midnight or were recently resumed outside
// SpecStory.
const watchWindowDays = 7

// watchWindowCutoff returns the earliest date (local midnight) whose day
// directory is still watched.
func watchWindowCutoff(now time.Time) time.Time {
	year, month, day := now.AddDate(0, 0, -watchWindowDays).Date()
	return time.Date(year, month, day, 0, 0, 0, 0, now.Location())
}

// dirWithinWatchWindow reports whether a year/month/day directory could contain
// sessions dated on or after cutoff, and therefore deserves an fsnotify watch.
// Directories that are not part of the YYYY/MM/DD structure return false.
func dirWithinWatchWindow(path string, sessionsRoot string, cutoff time.Time) bool {
	kind := dirType(path, sessionsRoot)
	if kind == "" {
		return false
	}

	// dirType already validated the path is under sessionsRoot with the right shape
	rel, err := filepath.Rel(sessionsRoot, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	nums := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return false
		}
		nums[i] = n
	}

	switch kind {
	case "year":
		return nums[0] >= cutoff.Year()
	case "month":
		if nums[1] < 1 || nums[1] > 12 {
			return false
		}
		return nums[0] > cutoff.Year() ||
			(nums[0] == cutoff.Year() && time.Month(nums[1]) >= cutoff.Month())
	case "day":
		dirDate := time.Date(nums[0], time.Month(nums[1]), nums[2], 0, 0, 0, 0, cutoff.Location())
		// time.Date normalizes out-of-range values (month 13 becomes January of
		// the next year, Feb 30 becomes March 2); a round-trip mismatch means
		// the components were not a real calendar date
		if dirDate.Year() != nums[0] || dirDate.Month() != time.Month(nums[1]) || dirDate.Day() != nums[2] {
			return false
		}
		return !dirDate.Before(cutoff)
	}
	return false
}

// startCodexSessionWatcher starts watching hierarchically for Codex sessions.
// Watches sessionsRoot/YYYY/MM/DD/ structure to handle date changes across days, months, and years.
// The initialDayDir is scanned immediately if it exists.
//
// All historical day directories are scanned at startup, but fsnotify watches are
// limited to directories within the trailing watchWindowDays window (plus
// pinnedDayDir, if non-empty, which holds a resumed session and is watched
// regardless of age). See watchWindowDays for why watching everything is harmful.
func startCodexSessionWatcher(projectPath string, sessionsRoot string, initialDayDir string, pinnedDayDir string) error {
	slog.Info("startCodexSessionWatcher: Creating hierarchical watcher",
		"sessionsRoot", sessionsRoot,
		"initialDayDir", initialDayDir,
		"pinnedDayDir", pinnedDayDir)

	// Create a new watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %v", err)
	}

	// Increment wait group before starting goroutine
	watcherWg.Add(1)

	// Start watching in a goroutine
	go func() {
		// Decrement wait group when done
		defer watcherWg.Done()

		// Log when goroutine starts
		slog.Info("startCodexSessionWatcher: Goroutine started")

		// Defer cleanup
		defer func() {
			slog.Info("startCodexSessionWatcher: Goroutine exiting, closing watcher")
			if err := watcher.Close(); err != nil {
				slog.Debug("startCodexSessionWatcher: Error closing watcher", "error", err)
			}
		}()

		// Track which directories are being watched to avoid duplicates
		watchedDirs := make(map[string]bool)
		var watchedDirsMutex sync.Mutex

		// Helper to add a directory to watcher if not already watched
		addWatch := func(dir string) error {
			watchedDirsMutex.Lock()
			defer watchedDirsMutex.Unlock()

			if watchedDirs[dir] {
				return nil // Already watching
			}

			if err := watcher.Add(dir); err != nil {
				return err
			}
			watchedDirs[dir] = true
			slog.Info("startCodexSessionWatcher: Added watch", "directory", dir)
			return nil
		}

		// Helper to scan a day directory for existing sessions
		scanDayDir := func(dayDir string) {
			if _, err := os.Stat(dayDir); err == nil {
				slog.Info("startCodexSessionWatcher: Scanning day directory", "directory", dayDir)
				ScanCodexSessions(projectPath, dayDir, nil)
			}
		}

		// Helper to remove a directory from the watcher if currently watched
		removeWatch := func(dir string) {
			watchedDirsMutex.Lock()
			defer watchedDirsMutex.Unlock()

			if !watchedDirs[dir] {
				return
			}

			// Removal can fail if the directory was deleted; the kernel already
			// released its fds in that case, so just drop our bookkeeping.
			if err := watcher.Remove(dir); err != nil {
				slog.Debug("startCodexSessionWatcher: Failed to remove watch",
					"directory", dir,
					"error", err)
			}
			delete(watchedDirs, dir)
			slog.Info("startCodexSessionWatcher: Removed watch", "directory", dir)
		}

		// Helper deciding whether a directory deserves an fsnotify watch: the
		// pinned (resumed session) day directory always does, otherwise only
		// directories within the trailing watch window.
		shouldWatch := func(dir string) bool {
			if dir == pinnedDayDir {
				return true
			}
			return dirWithinWatchWindow(dir, sessionsRoot, watchWindowCutoff(time.Now()))
		}

		// Helper to drop watches on directories that have aged out of the window.
		// Called at day rollover, which is the only time the watched set grows,
		// so fd usage stays flat no matter how long the process runs.
		pruneStaleWatches := func() {
			watchedDirsMutex.Lock()
			staleDirs := make([]string, 0)
			for dir := range watchedDirs {
				if dir == sessionsRoot || dir == pinnedDayDir {
					continue
				}
				if !dirWithinWatchWindow(dir, sessionsRoot, watchWindowCutoff(time.Now())) {
					staleDirs = append(staleDirs, dir)
				}
			}
			watchedDirsMutex.Unlock()

			for _, dir := range staleDirs {
				removeWatch(dir)
			}
		}

		// Helper to watch a day directory (if within the watch window) and scan it.
		// The watch is added before the scan so files created between the two are
		// not missed.
		watchDayDir := func(dayDir string) {
			if shouldWatch(dayDir) {
				if err := addWatch(dayDir); err != nil {
					slog.Error("startCodexSessionWatcher: Failed to watch day directory",
						"directory", dayDir,
						"error", err)
					return
				}
			}
			scanDayDir(dayDir)
		}

		// Helper to watch a month directory (if within the watch window) and
		// process its existing day directories
		watchMonthDir := func(monthDir string) {
			if shouldWatch(monthDir) {
				if err := addWatch(monthDir); err != nil {
					slog.Error("startCodexSessionWatcher: Failed to watch month directory",
						"directory", monthDir,
						"error", err)
					return
				}
			}

			// Scan for existing day directories
			entries, err := os.ReadDir(monthDir)
			if err != nil {
				slog.Debug("startCodexSessionWatcher: Cannot read month directory",
					"directory", monthDir,
					"error", err)
				return
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				// Day directories are named 01-31
				if len(entry.Name()) == 2 {
					dayDir := filepath.Join(monthDir, entry.Name())
					watchDayDir(dayDir)
				}
			}
		}

		// Helper to watch a year directory (if within the watch window) and
		// process its existing month directories
		watchYearDir := func(yearDir string) {
			if shouldWatch(yearDir) {
				if err := addWatch(yearDir); err != nil {
					slog.Error("startCodexSessionWatcher: Failed to watch year directory",
						"directory", yearDir,
						"error", err)
					return
				}
			}

			// Scan for existing month directories
			entries, err := os.ReadDir(yearDir)
			if err != nil {
				slog.Debug("startCodexSessionWatcher: Cannot read year directory",
					"directory", yearDir,
					"error", err)
				return
			}

			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				// Month directories are named 01-12
				if len(entry.Name()) == 2 {
					monthDir := filepath.Join(yearDir, entry.Name())
					watchMonthDir(monthDir)
				}
			}
		}

		// Watch sessions root for year directories
		if err := addWatch(sessionsRoot); err != nil {
			log.UserError("Error watching sessions root: %v", err)
			slog.Error("startCodexSessionWatcher: Failed to watch sessions root", "error", err)
			return
		}

		// Scan for existing year directories
		entries, err := os.ReadDir(sessionsRoot)
		if err != nil {
			slog.Warn("startCodexSessionWatcher: Cannot read sessions root",
				"directory", sessionsRoot,
				"error", err)
		} else {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				// Year directories are named YYYY (4 digits)
				if len(entry.Name()) == 4 {
					yearDir := filepath.Join(sessionsRoot, entry.Name())
					watchYearDir(yearDir)
				}
			}
		}

		// Scan the initial day directory if it exists
		scanDayDir(initialDayDir)

		// Watch for events
		slog.Info("startCodexSessionWatcher: Now watching for file and directory events")
		for {
			select {
			case <-watcherCtx.Done():
				slog.Info("startCodexSessionWatcher: Context cancelled, stopping watcher")
				return

			case event, ok := <-watcher.Events:
				if !ok {
					slog.Info("startCodexSessionWatcher: Watcher events channel closed")
					return
				}

				if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) && !event.Has(fsnotify.Remove) {
					continue
				}

				// Determine what type of path this is
				eventPath := event.Name
				parentDir := filepath.Dir(eventPath)

				// Check if this is a JSONL file (in a day directory)
				if strings.HasSuffix(eventPath, ".jsonl") {
					switch {
					case event.Has(fsnotify.Create), event.Has(fsnotify.Write):
						slog.Info("startCodexSessionWatcher: JSONL file event",
							"operation", event.Op.String(),
							"file", eventPath)
						ScanCodexSessions(projectPath, parentDir, &eventPath)
					case event.Has(fsnotify.Remove):
						slog.Info("startCodexSessionWatcher: JSONL file removed", "file", eventPath)
						ScanCodexSessions(projectPath, parentDir, nil)
					}
					continue
				}

				// Check if this is a directory creation
				if event.Has(fsnotify.Create) {
					switch dirType(eventPath, sessionsRoot) {
					case "year":
						slog.Info("startCodexSessionWatcher: New year directory created", "directory", eventPath)
						watchYearDir(eventPath)
					case "month":
						slog.Info("startCodexSessionWatcher: New month directory created", "directory", eventPath)
						watchMonthDir(eventPath)
					case "day":
						slog.Info("startCodexSessionWatcher: New day directory created", "directory", eventPath)
						watchDayDir(eventPath)
						// Day rollover is the only event that grows the watched
						// set, so it is also the moment to shed aged-out watches
						pruneStaleWatches()
					}
				}

			case err, ok := <-watcher.Errors:
				if !ok {
					slog.Info("startCodexSessionWatcher: Watcher errors channel closed")
					return
				}
				log.UserWarn("Watcher error: %v", err)
				slog.Error("startCodexSessionWatcher: Watcher error", "error", err)
			}
		}
	}()

	return nil
}

// ScanCodexSessions scans JSONL files in the session directory and processes sessions
// that match the project path. If changedFile is nil, scans all JSONL files in the directory.
// If changedFile is non-nil, only processes that specific file.
func ScanCodexSessions(projectPath string, sessionDir string, changedFile *string) {
	// Ensure logs are flushed even if we panic
	defer func() {
		if r := recover(); r != nil {
			log.UserWarn("PANIC in ScanCodexSessions: %v", r)
			slog.Error("ScanCodexSessions: PANIC recovered", "panic", r)
		}
	}()

	slog.Info("ScanCodexSessions: === START SCAN ===", "timestamp", time.Now().Format(time.RFC3339))
	if changedFile != nil {
		slog.Info("ScanCodexSessions: Scanning with changed file",
			"projectPath", projectPath,
			"sessionDir", sessionDir,
			"changedFile", *changedFile)
	} else {
		slog.Info("ScanCodexSessions: Scanning all sessions",
			"projectPath", projectPath,
			"sessionDir", sessionDir)
	}

	// Get the session callback
	callback := getWatcherCallback()
	if callback == nil {
		slog.Error("ScanCodexSessions: No callback provided - this should not happen")
		return
	}

	// Normalize project path for comparison
	normalizedProjectPath := normalizeCodexPath(projectPath)
	if normalizedProjectPath == "" {
		slog.Debug("ScanCodexSessions: Unable to normalize project path", "projectPath", projectPath)
	}

	// If we have a specific changed file, only process that file
	if changedFile != nil {
		if err := processCodexSessionFile(*changedFile, projectPath, normalizedProjectPath, callback); err != nil {
			slog.Debug("ScanCodexSessions: Failed to process changed file",
				"file", *changedFile,
				"error", err)
		}
		slog.Info("ScanCodexSessions: === END SCAN ===", "timestamp", time.Now().Format(time.RFC3339))
		return
	}

	// Otherwise, scan all JSONL files in the session directory
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		slog.Error("ScanCodexSessions: Failed to read session directory",
			"directory", sessionDir,
			"error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}

		sessionPath := filepath.Join(sessionDir, entry.Name())
		if err := processCodexSessionFile(sessionPath, projectPath, normalizedProjectPath, callback); err != nil {
			slog.Debug("ScanCodexSessions: Failed to process session file",
				"file", sessionPath,
				"error", err)
			// Continue processing other files even if one fails
		}
	}

	slog.Info("ScanCodexSessions: Processing complete")
	slog.Info("ScanCodexSessions: === END SCAN ===", "timestamp", time.Now().Format(time.RFC3339))
}

// processCodexSessionFile processes a single Codex session file and calls the callback
// if the session matches the project path.
func processCodexSessionFile(sessionPath string, projectPath string, normalizedProjectPath string, callback func(*spi.AgentChatSession)) error {
	// Load session metadata
	meta, err := loadCodexSessionMeta(sessionPath)
	if err != nil {
		return fmt.Errorf("failed to load session meta: %w", err)
	}

	// Check if session matches project path
	sessionID := strings.TrimSpace(meta.Payload.ID)
	normalizedCWD := normalizeCodexPath(meta.Payload.CWD)
	if normalizedCWD == "" {
		slog.Debug("processCodexSessionFile: Session meta missing cwd", "sessionID", sessionID, "path", sessionPath)
		return fmt.Errorf("session meta missing cwd")
	}

	// Check if this session matches the project path
	matched := false
	if normalizedProjectPath != "" {
		if normalizedCWD == normalizedProjectPath || strings.EqualFold(normalizedCWD, normalizedProjectPath) {
			matched = true
		}
	} else if normalizedCWD == projectPath || strings.EqualFold(normalizedCWD, projectPath) {
		matched = true
	}

	if !matched {
		slog.Debug("processCodexSessionFile: Session does not match project path",
			"sessionID", sessionID,
			"sessionCWD", normalizedCWD,
			"projectPath", normalizedProjectPath)
		return nil // Not an error, just doesn't match
	}

	slog.Info("processCodexSessionFile: Session matched project",
		"sessionID", sessionID,
		"sessionPath", sessionPath)

	// Create session info
	sessionInfo := &codexSessionInfo{
		SessionID:   sessionID,
		SessionPath: sessionPath,
		Meta:        meta,
	}

	// Process the session
	agentSession, err := processSessionToAgentChat(sessionInfo, projectPath, getWatcherDebugRaw())
	if err != nil {
		return fmt.Errorf("failed to process session: %w", err)
	}

	// Skip empty sessions
	if agentSession == nil {
		slog.Debug("processCodexSessionFile: Skipping empty session", "sessionID", sessionID)
		return nil
	}

	// Skip sessions without user message (empty slug)
	if agentSession.Slug == "" {
		slog.Debug("processCodexSessionFile: Skipping session without user message",
			"sessionID", agentSession.SessionID)
		return nil
	}

	slog.Info("processCodexSessionFile: Calling callback for session", "sessionID", agentSession.SessionID)
	// Call the callback in a goroutine to avoid blocking
	go func(s *spi.AgentChatSession) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("processCodexSessionFile: Callback panicked", "panic", r)
			}
		}()
		callback(s)
	}(agentSession)

	return nil
}
