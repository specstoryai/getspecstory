# Cloud Sync Debouncing Implementation Plan

## Summary

Optimize cloud sync for `specstory run` by:
1. Skipping wasteful HEAD requests (we just wrote the file, we know cloud doesn't have it)
2. Debouncing rapid sync events (max 1 sync per 10s per session)
3. Coalescing updates (newer events replace queued ones)
4. Auto-flushing after debounce period expires
5. Flushing all pending syncs on exit

## Background

### Current Flow

When `specstory run` is active:
1. Watcher detects JSONL file changes
2. `processSingleSession()` writes markdown locally
3. `cloud.SyncSessionToCloud()` is called immediately
4. Inside sync goroutine:
   - Makes HEAD request to check if sync needed
   - Makes PUT request to upload session
5. Both requests acquire the HTTP semaphore (max 10 concurrent)

### Problems

1. **Wasteful HEAD requests in run mode**: We just wrote the markdown file for the first time, we KNOW the cloud doesn't have this content yet. The HEAD check is only useful for `specstory sync` where we're processing existing files.

2. **Excessive sync rate**: Some providers generate very rapid sequences of responses (multiple per second). This causes:
   - Rapid PUT requests to cloud API
   - Wasted bandwidth syncing intermediate states
   - Only the final state matters

3. **No debouncing**: Every watcher event triggers an immediate sync attempt, regardless of how recently we synced the same session.

## Requirements

### Debounce Behavior Example

Given this event sequence (in seconds):

```
t0  - event 1
t1  - event 2
t2  - event 3
t21 - event 4
t22 - event 5
t23 - event 6
t26 - specstory run exits
```

Expected behavior:

- **t0**: Event 1 syncs immediately (no prior sync), starts 10s debounce until t10
- **t1**: Event 2 queues (waiting for t10)
- **t2**: Event 3 replaces event 2 in queue (only latest matters)
- **t10**: Timer fires → event 3 syncs, starts 10s debounce until t20
- **t20**: Timer fires → nothing queued, debounce expires silently
- **t21**: Event 4 syncs immediately (>10s since last sync), starts debounce until t31
- **t22**: Event 5 queues (waiting for t31)
- **t23**: Event 6 replaces event 5 in queue
- **t26**: Exit → event 6 syncs immediately (flush on shutdown)

### Key Rules

1. **Immediate sync** when no sync in progress and >10s since last sync for this session
2. **Queue and coalesce** when within 10s debounce window
3. **Auto-flush** when debounce timer expires and event is queued
4. **Forced flush** on shutdown to ensure no data loss
5. **Per-session** debouncing (different sessions sync independently)

## Implementation Plan

### 1. Add Debounce Data Structures

**Location**: `pkg/cloud/sync.go` (after line 30)

```go
// pendingSyncRequest holds a queued sync request during debounce period
type pendingSyncRequest struct {
	sessionID     string
	mdPath        string
	mdContent     string
	rawData       []byte
	agentName     string
	skipHeadCheck bool
}

// sessionDebounceState tracks debounce state for a single session
type sessionDebounceState struct {
	mu           sync.Mutex
	lastSyncTime time.Time          // When last sync started
	pending      *pendingSyncRequest // Queued request (if any)
	timer        *time.Timer         // Auto-flush timer
}
```

### 2. Update SyncManager Structure

**Location**: `pkg/cloud/sync.go:166`

Add fields:
```go
type SyncManager struct {
	enabled          bool
	wg               sync.WaitGroup
	silent           bool
	syncCount        int32
	httpSemaphore    chan struct{}
	stats            CloudSyncStats
	debounceInterval time.Duration  // NEW: 10s for run mode
	debounceSessions sync.Map       // NEW: sessionID -> *sessionDebounceState
}
```

### 3. Update SyncSessionToCloud Signature

**Location**: `pkg/cloud/sync.go:417`

Change from:
```go
func SyncSessionToCloud(sessionID string, mdPath string, mdContent string, rawData []byte, agentName string)
```

To:
```go
func SyncSessionToCloud(sessionID string, mdPath string, mdContent string, rawData []byte, agentName string, skipHeadCheck bool, enableDebounce bool)
```

Parameters:
- `skipHeadCheck`: If true, skip HEAD request (use in run mode)
- `enableDebounce`: If true, apply 10s debouncing (use in run mode)

### 4. Update InitSyncManager

**Location**: `pkg/cloud/sync.go:390`

Add initialization:
```go
func InitSyncManager(enabled bool) {
	syncManagerMutex.Lock()
	defer syncManagerMutex.Unlock()
	globalSyncManager = &SyncManager{
		enabled:          enabled,
		silent:           false,
		httpSemaphore:    make(chan struct{}, MaxConcurrentHTTPRequests),
		debounceInterval: 10 * time.Second, // NEW
	}
	slog.Debug("Cloud sync manager initialized", "enabled", enabled, "maxConcurrentHTTPRequests", MaxConcurrentHTTPRequests)
}
```

### 5. Implement Debounce Logic

**Location**: `pkg/cloud/sync.go` (new methods)

#### 5a. Extract performSync Method

Extract the current goroutine body from `SyncSessionToCloud` (lines 443-625) into:

```go
// performSync executes the actual sync operation (HEAD check + PUT request)
func (syncMgr *SyncManager) performSync(sessionID, mdPath, mdContent string, rawData []byte, agentName string, skipHeadCheck bool) {
	// All the current sync logic from the goroutine
	// Lines 449-625 moved here
}
```

#### 5b. Implement debouncedSync

```go
// debouncedSync implements debouncing logic for a session
func (syncMgr *SyncManager) debouncedSync(sessionID, mdPath, mdContent string, rawData []byte, agentName string, skipHeadCheck bool) {
	// Get or create debounce state for this session
	stateInterface, _ := syncMgr.debounceSessions.LoadOrStore(sessionID, &sessionDebounceState{})
	state := stateInterface.(*sessionDebounceState)

	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	timeSinceLastSync := now.Sub(state.lastSyncTime)

	// Check if we can sync immediately (no recent sync or first sync)
	if state.lastSyncTime.IsZero() || timeSinceLastSync >= syncMgr.debounceInterval {
		// Sync immediately
		state.lastSyncTime = now
		state.pending = nil

		// Cancel any pending timer
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}

		// Start sync in goroutine
		syncMgr.wg.Add(1)
		atomic.AddInt32(&syncMgr.syncCount, 1)
		go func() {
			defer func() {
				syncMgr.wg.Done()
				atomic.AddInt32(&syncMgr.syncCount, -1)
			}()
			syncMgr.performSync(sessionID, mdPath, mdContent, rawData, agentName, skipHeadCheck)
		}()

		slog.Debug("Cloud sync started immediately",
			"sessionId", sessionID,
			"timeSinceLastSync", timeSinceLastSync)
		return
	}

	// Within debounce window - queue or replace pending request
	state.pending = &pendingSyncRequest{
		sessionID:     sessionID,
		mdPath:        mdPath,
		mdContent:     mdContent,
		rawData:       rawData,
		agentName:     agentName,
		skipHeadCheck: skipHeadCheck,
	}

	// Set timer if not already set
	if state.timer == nil {
		timeUntilSync := syncMgr.debounceInterval - timeSinceLastSync
		state.timer = time.AfterFunc(timeUntilSync, func() {
			syncMgr.flushPendingSync(sessionID)
		})

		slog.Debug("Cloud sync queued with new timer",
			"sessionId", sessionID,
			"timeUntilSync", timeUntilSync)
	} else {
		slog.Debug("Cloud sync queued (replaced pending)",
			"sessionId", sessionID)
	}
}
```

#### 5c. Implement flushPendingSync

```go
// flushPendingSync is called by timer to flush a queued sync
func (syncMgr *SyncManager) flushPendingSync(sessionID string) {
	stateInterface, ok := syncMgr.debounceSessions.Load(sessionID)
	if !ok {
		return
	}
	state := stateInterface.(*sessionDebounceState)

	state.mu.Lock()
	defer state.mu.Unlock()

	// Check if there's a pending request
	if state.pending == nil {
		state.timer = nil
		slog.Debug("Timer fired but no pending sync",
			"sessionId", sessionID)
		return
	}

	// Sync the pending request
	req := state.pending
	state.pending = nil
	state.timer = nil
	state.lastSyncTime = time.Now()

	// Start sync in goroutine
	syncMgr.wg.Add(1)
	atomic.AddInt32(&syncMgr.syncCount, 1)
	go func() {
		defer func() {
			syncMgr.wg.Done()
			atomic.AddInt32(&syncMgr.syncCount, -1)
		}()
		syncMgr.performSync(req.sessionID, req.mdPath, req.mdContent, req.rawData, req.agentName, req.skipHeadCheck)
	}()

	slog.Debug("Flushed pending sync after debounce",
		"sessionId", sessionID)
}
```

#### 5d. Implement flushAllPending

```go
// flushAllPending flushes all pending debounced syncs (called on shutdown)
func (syncMgr *SyncManager) flushAllPending() {
	slog.Debug("Flushing all pending debounced syncs")

	syncMgr.debounceSessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		state := value.(*sessionDebounceState)

		state.mu.Lock()

		// Cancel timer
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}

		// If there's a pending request, sync it immediately
		if state.pending != nil {
			req := state.pending
			state.pending = nil

			// Start sync in goroutine
			syncMgr.wg.Add(1)
			atomic.AddInt32(&syncMgr.syncCount, 1)
			go func() {
				defer func() {
					syncMgr.wg.Done()
					atomic.AddInt32(&syncMgr.syncCount, -1)
				}()
				syncMgr.performSync(req.sessionID, req.mdPath, req.mdContent, req.rawData, req.agentName, req.skipHeadCheck)
			}()

			slog.Info("Flushing pending sync on shutdown",
				"sessionId", sessionID)
		}

		state.mu.Unlock()
		return true
	})
}
```

### 6. Update SyncSessionToCloud Implementation

**Location**: `pkg/cloud/sync.go:417`

Replace the function body:

```go
func SyncSessionToCloud(sessionID string, mdPath string, mdContent string, rawData []byte, agentName string, skipHeadCheck bool, enableDebounce bool) {
	syncManagerMutex.RLock()
	syncMgr := globalSyncManager
	syncManagerMutex.RUnlock()

	if syncMgr == nil || !syncMgr.enabled {
		if syncMgr == nil {
			slog.Warn("Cloud sync not initialized, skipping sync", "sessionId", sessionID)
		} else {
			slog.Debug("Cloud sync disabled by flag, skipping sync", "sessionId", sessionID)
		}
		return
	}

	// Check authentication
	if !IsAuthenticated() {
		slog.Warn("Cloud sync skipped: user not authenticated", "sessionId", sessionID)
		return
	}

	// Use debouncing or immediate sync based on parameter
	if enableDebounce {
		syncMgr.debouncedSync(sessionID, mdPath, mdContent, rawData, agentName, skipHeadCheck)
	} else {
		// Immediate sync (current behavior for sync command)
		syncMgr.wg.Add(1)
		atomic.AddInt32(&syncMgr.syncCount, 1)
		go func() {
			defer func() {
				syncMgr.wg.Done()
				atomic.AddInt32(&syncMgr.syncCount, -1)
			}()
			syncMgr.performSync(sessionID, mdPath, mdContent, rawData, agentName, skipHeadCheck)
		}()
	}
}
```

### 7. Update requiresSync to Skip HEAD Check

**Location**: `pkg/cloud/sync.go:203`

Modify `requiresSync` signature and add early return:

```go
func (syncMgr *SyncManager) requiresSync(sessionID, mdPath, mdContent, projectID string, skipHeadCheck bool) (bool, error) {
	// Skip HEAD check if requested (used in run mode where we know we just wrote new content)
	if skipHeadCheck {
		slog.Debug("Skipping HEAD check (new content in run mode)",
			"sessionId", sessionID)
		return true, nil
	}

	// Get size from passed content
	localSize := len(mdContent)

	// ... rest of existing HEAD check logic
}
```

### 8. Update performSync to Use skipHeadCheck

In the extracted `performSync` method, update the call to `requiresSync`:

```go
// Check if sync is needed using HEAD request
shouldSync, err := syncMgr.requiresSync(sessionID, mdPath, mdContent, projectID, skipHeadCheck)
```

### 9. Update Call Site in main.go

**Location**: `main.go:689`

Change from:
```go
cloud.SyncSessionToCloud(session.SessionID, fileFullPath, session.Markdown, []byte(session.RawData), provider.Name())
```

To:
```go
// Use skipHeadCheck and debounce when in autosave mode (run command)
// Don't use them for manual sync command
cloud.SyncSessionToCloud(session.SessionID, fileFullPath, session.Markdown, []byte(session.RawData), provider.Name(), isAutosave, isAutosave)
```

Rationale:
- When `isAutosave=true` (`specstory run`): skip HEAD + enable debounce
- When `isAutosave=false` (`specstory sync`): do HEAD + no debounce

### 10. Update Shutdown to Flush Pending

**Location**: `pkg/cloud/sync.go:634`

Update `Shutdown` function to flush pending syncs first:

```go
func Shutdown(timeout time.Duration) *CloudSyncStats {
	syncManagerMutex.RLock()
	sm := globalSyncManager
	syncManagerMutex.RUnlock()

	if sm == nil || !sm.enabled {
		return nil
	}

	// NEW: Flush all pending debounced syncs before waiting
	sm.flushAllPending()

	// Create a channel to signal when all syncs are done
	done := make(chan struct{})

	// ... rest of existing shutdown logic
}
```

## Testing Strategy

### Unit Tests

Create `pkg/cloud/sync_debounce_test.go` with:

1. **TestImmediateSyncFirstEvent**: First event for session syncs immediately
2. **TestQueueingDuringDebounce**: Events within 10s window get queued
3. **TestReplaceQueuedEvent**: Newer event replaces older queued event
4. **TestAutoFlushOnTimer**: Queued event syncs after debounce period
5. **TestTimerExpiresWithoutEvent**: Timer expires silently when nothing queued
6. **TestFlushOnShutdown**: Pending events sync on shutdown
7. **TestConcurrentEventsForSession**: Multiple goroutines updating same session
8. **TestMultipleSessionsIndependent**: Different sessions debounce independently
9. **TestSkipHeadCheck**: Verify HEAD request skipped when flag set
10. **TestDebounceDisabled**: Verify immediate sync when debounce disabled

### Manual Testing

1. **Run mode rapid events**: Start `specstory run`, make rapid changes, verify debouncing
2. **Run mode with exit**: Make changes, exit immediately, verify flush
3. **Sync mode unchanged**: Run `specstory sync`, verify HEAD checks still work
4. **Multiple sessions**: Edit multiple JSONL files, verify independent debouncing

## Rollout Plan

1. Implement changes in branch `cloud-debounce`
2. Run unit tests: `go test -v ./pkg/cloud/...`
3. Run integration tests: Manual testing with `specstory run`
4. Monitor logs with `--log` flag to verify debouncing behavior
5. Merge to `dev` branch
6. Release in next version

## Metrics to Track

After deployment, monitor:
- Average syncs per session in run mode (should decrease)
- HEAD request count (should be ~0 in run mode)
- PUT request count (should decrease for rapid-update sessions)
- Time to sync on exit (should remain low)
- Cloud API error rate (should remain same or improve)

## Edge Cases

1. **Very long session**: >60min runtime, 100s of updates → debouncing saves many syncs
2. **Exit during debounce**: Pending sync flushes immediately ✓
3. **Exit with no debounce active**: No pending syncs, normal shutdown ✓
4. **Network failure during debounced sync**: Existing error handling applies ✓
5. **Multiple rapid exits**: Each session's state is independent ✓

## Backward Compatibility

- `specstory sync` behavior unchanged (no debounce, HEAD checks preserved)
- Cloud API contract unchanged
- Statistics tracking unchanged
- Semaphore limits unchanged (orthogonal concern)
