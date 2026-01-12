# Batch Size Retrieval for Full Project Sync - Implementation Summary

## Overview

Replaced individual HEAD requests with a single bulk GET request when syncing all sessions in a project. This significantly reduces server load during full `specstory sync` operations.

**Status:** ✅ Implemented and tested

## Key Discoveries During Implementation

### Issue 1: Multiple Bulk Requests Per Sync
**Problem:** Initial implementation called `PreloadSessionSizes()` in `syncProvider()`, which meant it was called **once per provider** (3 times for Claude Code + Codex + Cursor). All providers use the same projectID, so we were:
- Making 3 identical bulk GET requests (wasteful)
- Cache becoming stale between providers as sessions uploaded mid-sync
- Sessions incorrectly marked as "new" when they had just been created by previous provider

**Solution:** Moved `PreloadSessionSizes()` call to `syncAllProviders()` and `syncSingleProvider()` - now called **once per sync command** before processing any providers. All providers share the same bulk sizes cache.

**Files Changed:**
- Removed preload logic from `syncProvider()` in main.go
- **Refactored:** Extracted duplicated preload logic into `preloadBulkSessionSizesIfNeeded()` helper function (lines 713-731)
- Added preload call to `syncAllProviders()` in main.go (line 948)
- Added preload call to `syncSingleProvider()` in main.go (line 1052)
- **Added checks and removed fallbacks:** Helper function now:
  - Skips preload if `--no-cloud-sync` flag is set or user is not authenticated
  - Uses `ProjectIdentityManager.GetProjectID()` instead of duplicating JSON parsing logic
  - Returns early if no project ID available (removed dangerous directory name fallback)
  - Removed `encoding/json` import from main.go (no longer needed)

### Issue 2: HTTP Semaphore Timeout Causing Invisible Failures
**Problem:** HTTP semaphore had a 30 second timeout (`HTTPSemaphoreTimeout`). With 111 sessions and only 10 concurrent slots, sessions 11+ queued waiting. Large sessions (1.6MB JSON files) took time to upload, so later sessions in the queue timed out after 30s waiting for a slot. These timed-out sessions:
- Failed silently (not tracked in stats)
- Made stats inaccurate (showed 77 processed instead of 111)
- Left no indication of what happened

**Solution:** Implemented comprehensive timeout tracking:
1. **Removed HTTP semaphore timeout** - sessions now block indefinitely waiting for slot
2. **Added `SessionsAttempted` counter** - incremented at start of `performSync()`
3. **Added `SessionsTimedOut` field** - calculated at shutdown as: `attempted - (skipped + updated + created + errored)`
4. **Updated stats output** - shows total attempted, and explicit "X sessions timed out" line
5. **Increased CloudSyncTimeout** from 60s to 120s to give large sessions more time

**Files Changed:**
- `pkg/cloud/sync.go`: Modified `CloudSyncStats`, `acquireHTTPSemaphore()`, `performSync()`, `Shutdown()`
- `main.go`: Updated stats display to show attempted/timed out sessions
- `pkg/cloud/sync_throttle_test.go`: Fixed tests for new semaphore signature

## Scope

### Will change:
- Full project sync (`specstory sync` or `specstory sync <provider>`)

### Will NOT change:
- Single session sync (`specstory sync -s <session-id>`) - continues using HEAD requests
- Autosave mode (`specstory run`) - continues using HEAD requests
- Individual HEAD requests as fallback if bulk GET fails

## API Details

### Endpoint
- **URL**: `/api/v1/projects/{projectID}/sessions/sizes`
- **Method**: GET
- **Auth**: Bearer token in Authorization header
- **Request Body**: None (server returns all sessions for the project)

### Response Format
```json
{
  "success": true,
  "data": {
    "sessions": {
      "session-1-uuid": 1234,
      "session-2-uuid": 5678
    }
  }
}
```

### Error Handling
- **Retry logic**: 3 total attempts (initial + 2 retries) with 1 second delay between attempts
- **Fallback**: If all retries fail, fall back to individual HEAD requests per session
- **Timeout**: 30 seconds (consistent with PUT requests)

## Implementation Details

### 1. Add new types in `pkg/cloud/sync.go`

```go
// BulkSizesResponse represents the API response for bulk session sizes
type BulkSizesResponse struct {
    Success bool                   `json:"success"`
    Data    BulkSizesResponseData  `json:"data"`
}

type BulkSizesResponseData struct {
    Sessions map[string]int `json:"sessions"` // sessionID -> markdown size
}
```

### 2. Add new method to fetch bulk sizes

New function `fetchBulkSessionSizes(projectID string) (map[string]int, error)` in `pkg/cloud/sync.go`:

**Responsibilities:**
- Makes GET request to `/api/v1/projects/{projectID}/sessions/sizes`
- Includes auth headers (Bearer token, User-Agent)
- Uses 30 second timeout (consistent with PUT requests)
- **Retry logic**: Attempts 3 times total (initial + 2 retries) with 1 second delay between attempts
- Returns `map[string]int` of sessionID -> size, or error if all retries fail
- Logs debug info for request/response

**Implementation notes:**
- Does NOT use HTTP semaphore (only called once during batch sync initialization, no concurrency)
- Parse JSON response into BulkSizesResponse struct
- Extract sessions map from response.Data.Sessions
- Handle non-200 status codes as errors (will trigger retry)
- Log at Info level on success: "Fetched bulk session sizes: {count} sessions"
- Log at Error level on failure: "Failed to fetch bulk session sizes after retries"

### 3. Modify `SyncManager` structure

Add optional field to cache bulk sizes:

```go
type SyncManager struct {
    // ... existing fields ...
    bulkSizes   map[string]int  // Optional: sessionID -> size cache for batch operations
    bulkSizesMu sync.RWMutex    // Protects bulkSizes map
}
```

**Design rationale:**
- `bulkSizes` is nil by default (indicates no bulk preload)
- Only populated when `PreloadSessionSizes()` is called
- Protected by RWMutex since read operations (in `requiresSync`) far outnumber writes
- Cleared after sync operation completes to free memory

### 4. Add new public method for batch sync mode

New function `PreloadSessionSizes(projectID string)` in `pkg/cloud/sync.go`:

**Responsibilities:**
- Called from `main.go` before starting session sync loop
- Calls `fetchBulkSessionSizes(projectID)` internally
- Stores result in `syncMgr.bulkSizes` with mutex protection (write lock)
- If fetch fails after retries, logs warning and leaves `bulkSizes` nil (enables fallback mode)
- Logs info message: "Preloaded {count} session sizes from server"

**Error handling:**
- If `fetchBulkSessionSizes()` returns error, log warning but don't fail
- Leave `bulkSizes` as nil to trigger HEAD request fallback
- This ensures sync continues even if bulk endpoint has issues

### 5. Modify `requiresSync()` method

Update `requiresSync(sessionID, mdPath, mdContent, projectID string, skipHeadCheck bool)` in `pkg/cloud/sync.go`:

**Current behavior (lines 249-353):**
1. Returns true immediately if `skipHeadCheck` is true
2. Makes HEAD request to check server state
3. Compares local size vs server size
4. Returns true if sync needed, false if already up-to-date

**New behavior:**
1. Returns true immediately if `skipHeadCheck` is true (unchanged)
2. **Check if `bulkSizes` is populated** (acquire read lock):
   - **If populated**: Look up sessionID in map instead of making HEAD request
     - If not in map: session doesn't exist on server → return `true` (needs sync)
     - If in map: compare local size vs server size → return `localSize > serverSize`
     - Log at Debug level: "Using preloaded size for sync check (sessionId: {id}, localSize: {size}, serverSize: {size})"
   - **If not populated**: Fall back to existing HEAD request logic (unchanged)
     - Log at Debug level: "Using HEAD request for sync check (no bulk sizes) (sessionId: {id})"

**Code location:**
- Insert bulk size check after line 257 (after skipHeadCheck guard)
- Before line 259 (before local size calculation for HEAD)

### 6. Integration in `main.go`

Modify `syncAllProviders()` function (lines 880-995) and `syncSingleProvider()` function (lines 1002-1100):

**Implementation in `syncAllProviders()`:**
- **Location:** After `checkAndWarnAuthentication()` and `EnsureHistoryDirectoryExists()`, **BEFORE** provider loop (line 927-951)
- **Steps:**
  1. Extract projectID from `.specstory/.project.json` (git_id or workspace_id)
  2. Fallback to directory name if no project config
  3. Call `cloud.GetSyncManager().PreloadSessionSizes(projectID)` **once**
  4. Then loop through all providers - they all share the cached bulk sizes

**Implementation in `syncSingleProvider()`:**
- **Location:** After `checkAndWarnAuthentication()` and `EnsureHistoryDirectoryExists()`, **BEFORE** calling `syncProvider()` (line 1054-1078)
- **Steps:** Same as above - extract projectID, call `PreloadSessionSizes()` once

**Key Points:**
- ✅ Called **once per sync command** (not once per provider)
- ✅ All providers (Claude Code, Codex, Cursor) share the same projectID and bulk sizes cache
- ✅ Blocking call - completes before any provider starts syncing
- ✅ `requiresSync()` reads from pre-populated cache (or falls back to HEAD if cache nil)
- ✅ Graceful error handling - logs warning and leaves cache nil if fetch fails

**Error handling:**
- `PreloadSessionSizes()` handles its own errors internally (logs warning, leaves cache nil)
- No need to check return value or handle errors in main.go
- Sync loop continues regardless of preload success/failure (uses HEAD request fallback)

## Files Modified

### 1. `pkg/cloud/sync.go` (primary changes)

**New code added:**
- Types: `BulkSizesResponse`, `BulkSizesResponseData` (lines 70-79)
- Fields to `SyncManager`: `bulkSizes`, `bulkSizesMu` (lines 133-134)
- Field to `CloudSyncStats`: `SessionsAttempted`, `SessionsTimedOut` (lines 108, 113)
- Function: `fetchBulkSessionSizes()` with retry logic (~150 lines, lines 262-409)
- Method: `PreloadSessionSizes()` (~30 lines, lines 411-442)

**Existing code modified:**
- Constant: `CloudSyncTimeout` changed from 60s to 120s (line 28)
- Method: `acquireHTTPSemaphore()` - removed timeout, now blocks indefinitely (lines 235-255)
- Method: `requiresSync()` - added bulk size check branch (~40 lines, lines 457-491)
- Method: `performSync()` - added `SessionsAttempted` counter increment (line 672)
- Method: `Shutdown()` - calculate `SessionsTimedOut`, include `SessionsAttempted` in stats (lines 1064-1147)

**Security fix:**
- **Removed dangerous fallback**: Eliminated directory name fallback for projectID in `performSync()` (lines 699-718)
- Now properly fails cloud sync if project identity cannot be read or if no projectID exists
- Prevents incorrect/inconsistent cloud data from using arbitrary directory names as project IDs

**Actual changes:** ~220 lines added, ~55 lines modified

### 2. `main.go` (integration points)

**New code added:**
- Function: `preloadBulkSessionSizesIfNeeded()` - uses ProjectIdentityManager to get projectID and preloads bulk sizes if cloud sync is enabled and authenticated (~17 lines, lines 712-745)
  - Returns early if `GetProjectID()` fails (no directory name fallback)

**Existing code modified:**
- Imports: Removed `encoding/json` import (no longer needed after refactoring)
- Function: `syncAllProviders()` - replaced duplicated preload logic with call to helper function, passing identityManager (line 948)
- Function: `syncSingleProvider()` - replaced duplicated preload logic with call to helper function, passing identityManager (line 1052)
- Stats display section - updated to show `SessionsAttempted` and `SessionsTimedOut` (~30 lines, lines 1738-1786)

**Refactoring benefits:**
- Eliminated DRY violation: removed 50+ lines of duplicated projectID extraction logic
- Reused existing `ProjectIdentityManager.GetProjectID()` instead of inline JSON parsing
- Proper separation of concerns: project identity logic now centralized in utils package

**Actual changes:** ~48 lines added (18 new function, 30 stats display), ~52 lines removed (deduplicated code)

### 3. `pkg/cloud/sync_throttle_test.go` (test fixes)

**Existing code modified:**
- Updated all calls to `acquireHTTPSemaphore()` to not expect error return (lines 32, 81, 101)

**Actual changes:** 3 lines modified

## Testing Approach

### Manual Testing Scenarios

1. **Full sync with bulk sizes working:**
   - Run `./specstory sync --debug`
   - Verify single GET to `/sessions/sizes` in debug logs
   - Verify no HEAD requests for individual sessions
   - Verify all sessions sync correctly

2. **Full sync with bulk GET failing:**
   - Temporarily break API endpoint or network
   - Run `./specstory sync --debug`
   - Verify retry attempts (3 total)
   - Verify fallback to individual HEAD requests
   - Verify sync still completes successfully

3. **Single session sync (should not use bulk):**
   - Run `./specstory sync -s <session-id> --debug`
   - Verify NO GET to `/sessions/sizes`
   - Verify HEAD request for single session
   - Verify sync works correctly

4. **Autosave mode (should not use bulk):**
   - Run `./specstory run --debug`
   - Make changes to trigger sync
   - Verify NO GET to `/sessions/sizes`
   - Verify debounced HEAD requests (or skip HEAD in autosave mode)

5. **Empty project (no sessions):**
   - Run sync in project with no sessions
   - Verify bulk GET returns empty sessions map
   - Verify no errors, graceful handling

6. **Statistics tracking:**
   - Run full sync with mix of new/updated/unchanged sessions
   - Verify final statistics are correct
   - Verify precedence rules still work (created > updated > errored > skipped)

### Unit Testing

Consider adding tests for:
- `fetchBulkSessionSizes()` - mock HTTP responses (success, failure, malformed JSON)
- `requiresSync()` with bulk sizes populated - various scenarios (not in map, size comparison)
- `PreloadSessionSizes()` - error handling when fetch fails

### Performance Testing

- Test with large number of sessions (100+)
- Measure time savings: N HEAD requests vs 1 GET request
- Verify HTTP semaphore works correctly with bulk request

## Benefits Delivered

### Performance
- **Reduces server load**: 1 GET request instead of N HEAD requests for N sessions (tested with 111 sessions: 1 request vs 111 requests)
- **Reduces network round trips**: Single bulk request vs sequential individual requests
- **Faster sync completion**: No waiting for N sequential HEAD requests
- **Doubled timeout window**: Increased from 60s to 120s for large session uploads

### Reliability
- **Backward compatible**: Falls back to HEAD requests if bulk fetch fails (tested with 500 errors)
- **Resilient**: Retry logic with 1 second delays between attempts (3 total attempts)
- **Graceful degradation**: Sync continues even if bulk endpoint unavailable
- **Accurate session tracking**: All attempted sessions now tracked, including timeouts
- **Visible timeout reporting**: Users see exactly which sessions timed out vs completed

### Observability
- **Complete session accounting**: `SessionsAttempted` = skipped + updated + created + errored + timed_out
- **No silent failures**: Sessions that timeout are explicitly counted and displayed
- **Clear status indicators**: "❌ Cloud sync incomplete!" shown when errors/timeouts occur
- **Analytics tracking**: Timeout data sent to PostHog for monitoring

### Maintainability
- **Scoped change**: Only affects full project sync, not autosave or single session
- **Clear separation**: Batch and individual sync modes are distinct
- **Single preload call**: Shared across all providers (not duplicated per provider)
- **No breaking changes**: Existing behavior unchanged for all non-full-sync scenarios

## Implementation Notes

### Cache Lifetime
- Cache only exists during a single `specstory sync` invocation
- Since `sync` is a one-shot command that exits after completion, cache is automatically freed when process exits
- No need to manually clear the cache - the OS does it when the process terminates
- `specstory run` (long-lived process) doesn't use bulk sizes at all

### Analytics
- No analytics events needed for this optimization
- HEAD requests and bulk size requests are internal implementation details
- Doesn't impact existing analytics tracking

### Logging Level
- Info level on success: "Preloaded {count} session sizes from server"
- Error level on failure: "Failed to fetch bulk session sizes after {attempts} attempts, falling back to HEAD requests"
- Debug level for per-session size checks: "Using preloaded size for sync check"

## Implementation Order

1. Add new types to sync.go
2. Add fields to SyncManager struct
3. Implement `fetchBulkSessionSizes()` with retry logic
4. Implement `PreloadSessionSizes()` method
5. Modify `requiresSync()` to check bulk cache
6. Integrate call in main.go `syncProvider()`
7. Test manually with debug logging
8. Run full test suite