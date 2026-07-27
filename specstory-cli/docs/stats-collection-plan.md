# Implementation Plan: Statistics Collection for Sync Command

## Context

The sync command currently generates markdown files from JSONL session data and optionally syncs them to the cloud. Users want the ability to collect session statistics (message counts, timestamps, markdown size) automatically and optionally skip markdown/cloud operations when only statistics are needed.

Use cases:
- Automatic statistics tracking for all sessions (always-on by default)
- Collecting metrics without re-writing existing files (--only-stats mode)
- Analyzing session data without cloud sync overhead
- Lightweight batch statistics collection

The feature:
- **Always collects statistics** during sync operations (no flag required)
- Adds `--only-stats` flag: Skip local markdown files AND cloud sync, only collect statistics

**Critical constraint**: Markdown must be generated only once per session, not separately for stats collection and file writing/cloud sync.

## Key Design Decisions

1. **Always-on statistics**: Statistics collection is so lightweight (~5ms per session) that it's enabled by default for all sync operations. No opt-in flag required.

2. **Reuse existing markdown generation points**: The sync flow already generates markdown once in two places (`processSingleSession` and `syncProvider`). We collect statistics immediately after generation, before persistence.

3. **Purpose-driven flag**: `--only-stats` is a clear, single-purpose flag that replaces the need for `--no-cloud-sync --no-local-save --stats`.

4. **Statistics storage**: Per-session JSON file at `.specstory/statistics.json` with atomic writes and mutex protection for concurrent access.

5. **Non-blocking failures**: Statistics collection failures should warn but never fail the sync operation.

## Implementation Steps

### 1. Add Global Variables and Flags

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

In the "Sync Options" section (around line 40-45):
```go
var noCloudSync bool   // flag to disable cloud sync
var onlyCloudSync bool // flag to skip local markdown writes and only sync to cloud
var onlyStats bool     // flag to only collect statistics, skip local markdown and cloud sync
var cloudURL string    // custom cloud API URL (hidden flag)
```

In the syncCmd flags section (around line 2190-2195):
```go
syncCmd.Flags().BoolVar(&noCloudSync, "no-cloud-sync", false, "disable cloud sync functionality")
syncCmd.Flags().BoolVar(&onlyCloudSync, "only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
syncCmd.Flags().BoolVar(&onlyStats, "only-stats", false, "only collect statistics, skip local markdown files and cloud sync")
```

### 2. Add Flag Validation

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

In `validateFlags()` function (around line 90-96):
```go
if onlyStats && onlyCloudSync {
    return utils.ValidationError{Message: "cannot use --only-stats and --only-cloud-sync together. These flags are mutually exclusive"}
}
if onlyStats && noCloudSync {
    return utils.ValidationError{Message: "--only-stats already skips cloud sync, no need for --no-cloud-sync"}
}
```

### 3. Create Statistics Module

**File**: `/Users/bago/code/getspecstory/specstory-cli/pkg/utils/statistics.go` (new file)

Create this file with:
- `SessionStatistics` struct with fields: UserMessageCount, StartTimestamp, EndTimestamp, MarkdownSizeBytes, Provider (provider ID like "claude", "codex"), LastUpdated
- `StatisticsFile` struct with Sessions map[string]SessionStatistics
- `StatisticsCollector` type with mutex for thread-safe file operations
- `ComputeSessionStatistics()` function that extracts stats from SessionData
- `AddSessionStats()` method that reads, merges, and writes statistics.json atomically

**Key implementation details**:
- Use temp file + rename for atomic writes
- Count user messages by iterating through exchanges and filtering Role == "user"
- Extract timestamps from message data, fall back to session CreatedAt/UpdatedAt
- Handle corrupt JSON gracefully (start fresh with warning)
- Use `sync.Mutex` for concurrent access protection
- Store provider ID (e.g., "claude") not display name (e.g., "Claude Code")

### 4. Add Statistics Collection to processSingleSession

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

After markdown generation (around line 1010-1017):
```go
// Collect statistics (always enabled)
if err := collectSessionStatistics(session, markdownContent, providerID, config); err != nil {
    slog.Warn("Failed to collect session statistics", "sessionId", session.SessionID, "error", err)
    // Don't fail the operation, just log warning
}
```

Update file writing condition (around line 1050):
```go
// Write file if needed (skip if only-cloud-sync or only-stats is enabled)
if !onlyCloudSync && !onlyStats {
    // ... file writing logic
}
```

Update outcome messages (around line 1120-1130):
```go
} else {
    // Only cloud sync or only stats mode - no local file operations
    if onlyCloudSync {
        outcome = "synced to cloud only"
        slog.Info("Skipping local file write (only-cloud-sync mode)", "sessionId", session.SessionID)
    } else if onlyStats {
        outcome = "statistics collected"
        slog.Info("Skipping local file write (only-stats mode)", "sessionId", session.SessionID)
    }
}
```

Update cloud sync logic (around line 1134-1138):
```go
// Trigger cloud sync with provider-specific data
// Skip cloud sync if: only-stats mode OR noCloudSync is enabled
// In only-cloud-sync mode: always sync (no file to check for identical content)
// In normal mode: skip sync only if identical content AND in autosave mode
if !onlyStats && !noCloudSync {
    if onlyCloudSync || !identicalContent || !isAutosave {
        cloud.SyncSessionToCloud(session.SessionID, fileFullPath, markdownContent, []byte(session.RawData), provider.Name(), isAutosave)
    }
}
```

### 5. Add Statistics Collection to syncProvider

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

After markdown generation in the loop (around line 1253-1259):
```go
// Collect statistics (always enabled)
if err := collectSessionStatistics(session, markdownContent, providerID, config); err != nil {
    slog.Warn("Failed to collect session statistics", "sessionId", session.SessionID, "error", err)
    // Don't fail the operation, just log warning
}
```

Update file writing condition (around line 1282):
```go
// Write file if needed (skip if only-cloud-sync or only-stats is enabled)
if !onlyCloudSync && !onlyStats {
    // ... file writing logic
}
```

Update log messages (around line 1330-1337):
```go
} else {
    // In cloud-only or only-stats mode, count as skipped since no local file operation occurred
    stats.SessionsSkipped++
    if onlyCloudSync {
        slog.Info("Skipping local file write (only-cloud-sync mode)", "sessionId", session.SessionID)
    } else if onlyStats {
        slog.Info("Skipping local file write (only-stats mode)", "sessionId", session.SessionID)
    }
}
```

Update cloud sync logic (around line 1342-1347):
```go
// Trigger cloud sync with provider-specific data
// Skip cloud sync if: only-stats mode OR noCloudSync is enabled
// Manual sync command: perform immediate sync with HEAD check (not autosave mode)
// In only-cloud-sync mode: always sync
if !onlyStats && !noCloudSync {
    cloud.SyncSessionToCloud(session.SessionID, fileFullPath, markdownContent, []byte(session.RawData), provider.Name(), false)
}
```

### 6. Add Helper Functions

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

Add near other helper functions (around line 980):

```go
// collectSessionStatistics computes and saves session statistics
func collectSessionStatistics(session *spi.AgentChatSession, markdownContent string, providerID string, config utils.OutputConfig) error {
    stats := utils.ComputeSessionStatistics(session.SessionData, markdownContent, providerID)

    specstoryDir := getSpecStoryDir(config)
    collector := utils.NewStatisticsCollector(specstoryDir)
    return collector.AddSessionStats(session.SessionID, stats)
}

// getSpecStoryDir determines the .specstory directory path from config
func getSpecStoryDir(config utils.OutputConfig) string {
    historyDir := config.GetHistoryDir()
    if filepath.Base(historyDir) == "history" {
        return filepath.Dir(historyDir)  // Default: go up from .specstory/history to .specstory
    }
    return historyDir  // Custom output dir: the history dir IS the base
}
```

### 7. Add User Feedback

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

After the summary output in `syncProvider()` (around line 1377):
```go
// Show statistics collection message (always collected)
if sessionCount > 0 && !silent {
    specstoryDir := getSpecStoryDir(config)
    statsPath := filepath.Join(specstoryDir, "statistics.json")
    fmt.Printf("\n📊 Statistics collected: %s\n", statsPath)
}
```

### 8. Add Provider ID Parameter

**File**: `/Users/bago/code/getspecstory/specstory-cli/main.go`

Update `processSingleSession` signature to include providerID:
```go
func processSingleSession(session *spi.AgentChatSession, provider spi.Provider, providerID string, config utils.OutputConfig, showOutput bool, isAutosave bool, debugRaw bool, useUTC bool) error
```

Update all call sites to pass provider ID:
- Run command: Pass `providerID` from command context
- Watch command: Capture `id` in goroutine closure, pass as parameter
- Sync command (specified provider): Track and pass `specifiedProviderID`
- Sync command (all providers): Pass `id` from the provider loop
- syncProvider function: Use existing `providerID` parameter

## Verification Steps

After implementation, test these scenarios:

1. **Normal sync (statistics collected automatically)**: `./specstory sync`
   - Verify `.specstory/statistics.json` created with correct data
   - Verify markdown files are written
   - Verify cloud sync happens (if authenticated)

2. **Only statistics**: `./specstory sync --only-stats`
   - Verify statistics.json updated
   - Verify NO markdown files written to .specstory/history
   - Verify NO cloud sync occurs
   - Verify user sees "📊 Statistics collected: ..." message

3. **Only stats with specific sessions**: `./specstory sync --only-stats -s <uuid1> -s <uuid2>`
   - Verify both sessions in statistics.json
   - Verify incremental updates preserve existing entries
   - Verify no markdown or cloud operations

4. **Flag validation**: `./specstory sync --only-stats --only-cloud-sync`
   - Verify error message about mutually exclusive flags

5. **Flag validation**: `./specstory sync --only-stats --no-cloud-sync`
   - Verify warning about redundant flag

6. **Concurrent access**: Run multiple `./specstory sync` commands simultaneously
   - Verify no corruption in statistics.json

## Critical Files

- `/Users/bago/code/getspecstory/specstory-cli/main.go` - Main implementation
- `/Users/bago/code/getspecstory/specstory-cli/pkg/utils/statistics.go` - Statistics module
- `/Users/bago/code/getspecstory/specstory-cli/pkg/spi/schema/types.go` - SessionData structure
- `/Users/bago/code/getspecstory/specstory-cli/pkg/utils/path_utils.go` - OutputConfig pattern

## Statistics JSON Schema

```json
{
  "sessions": {
    "session-uuid-1": {
      "user_message_count": 5,
      "start_timestamp": "2026-02-09T10:00:00Z",
      "end_timestamp": "2026-02-09T10:15:30Z",
      "markdown_size_bytes": 4523,
      "provider": "claude",
      "last_updated": "2026-02-09T11:00:00Z"
    },
    "session-uuid-2": {
      "user_message_count": 3,
      "start_timestamp": "2026-02-09T12:00:00Z",
      "end_timestamp": "2026-02-09T12:08:45Z",
      "markdown_size_bytes": 2891,
      "provider": "codex",
      "last_updated": "2026-02-09T12:10:00Z"
    }
  }
}
```

## Design Summary

**Before:**
- Optional statistics with `--stats` flag
- Complex flag combinations: `--stats --no-local-save --no-cloud-sync`

**After:**
- Statistics always collected (negligible overhead ~5ms/session)
- Simple purpose-driven flag: `--only-stats` for statistics-only mode
- Cleaner UX, less cognitive load, consistent behavior
