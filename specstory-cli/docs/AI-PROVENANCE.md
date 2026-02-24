# AI Provenance

## Overview

The provenance system determines **which file changes were caused by which AI agent interactions**. It does this by:

1. **Receiving file events** — consumers push filesystem changes (create, modify, delete, rename)
2. **Receiving agent events** — consumers push records of agent file operations extracted from session data
3. **Storing both** — maintains internal SQLite storage for file events and agent activity
4. **Correlating on each event** — when a file event or agent event arrives, matches by path + timing
5. **Emitting provenance records** — attributions linking file changes to specific agent exchanges

The core engine lives in `pkg/provenance/`. It was originally extracted from Intent's correlation engine into a standalone library (`ai-provenance-lib`), then moved directly into specstory-cli so that both CLI and Intent can use it — Intent already imports specstory-cli as a library.

**Import path for consumers:**
```go
import "github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"
```

---

## Architecture

### Core Components

| File                        | Role                                                                                                                  |
|-----------------------------|-----------------------------------------------------------------------------------------------------------------------|
| `pkg/provenance/types.go`   | Input types (`FileEvent`, `AgentEvent`), output type (`ProvenanceRecord`), `NormalizePath()`, validation methods      |
| `pkg/provenance/engine.go`  | Correlation engine — `NewEngine()`, `PushFileEvent()`, `PushAgentEvent()`, path suffix matching, best-match selection |
| `pkg/provenance/store.go`   | SQLite persistence — WAL mode, event storage, unmatched queries, bidirectional match marking                          |

### Matching Algorithm

A file event correlates to an agent event when:

1. The agent path (normalized) is a **suffix** of the FS path **at a `/` directory boundary**, AND
2. The events occurred within **±5 seconds** of each other (configurable via `WithMatchWindow`)

**Path normalization:** Agent paths have backslashes replaced with `/` and a leading `/` added if missing.

**Multiple matches:** If multiple unmatched events match, the one with the **closest timestamp** wins.

**Consumption:** Once matched, both events get their `matched_with` field set and are excluded from future correlation.

**Examples:**

| FS Path                  | Agent Path            | Normalized            | Match?       | Why                 |
|--------------------------|-----------------------|-----------------------|--------------|---------------------|
| `/project/src/foo.go`    | `foo.go`              | `/foo.go`             | Yes (if ±5s) | Suffix at `/`       |
| `/project/src/foo.go`    | `src/foo.go`          | `/src/foo.go`         | Yes (if ±5s) | Suffix at `/`       |
| `/project/src/foo.go`    | `/project/src/foo.go` | `/project/src/foo.go` | Yes (if ±5s) | Exact match         |
| `/project/src/foo.go`    | `bar.go`              | `/bar.go`             | No           | Not a suffix        |
| `/project/src/foobar.go` | `bar.go`              | `/bar.go`             | No           | Not a suffix        |
| `/project/src/afoo.go`   | `foo.go`              | `/foo.go`             | No           | Not at `/` boundary |
| `/project/xsrc/foo.go`   | `src/foo.go`          | `/src/foo.go`         | No           | Not at `/` boundary |

### SQLite Storage

**Location:** `~/.specstory/provenance/provenance.db` (centralized, not per-project)

The database is centralized because:
- Agents can work on files outside their project directory
- Multiple consumers (CLI instances, Intent) may run simultaneously
- Need to correlate all FS events with all agent events globally

SQLite with WAL mode handles concurrent access from multiple processes.

**Schema:**

```sql
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,           -- 'file_event' or 'agent_event'
    file_path TEXT NOT NULL,      -- normalized path (forward slashes)
    timestamp INTEGER NOT NULL,   -- UnixNano (int64)
    matched_with TEXT,            -- ID of correlated entry (NULL = unmatched)
    payload TEXT NOT NULL         -- JSON: full FileEvent or AgentEvent data
);

CREATE INDEX idx_events_unmatched
    ON events(type, timestamp) WHERE matched_with IS NULL;
```

**Schema evolution strategy:** Uses SQLite `PRAGMA user_version` (integer in DB header). On startup, read version, run any needed `ALTER TABLE` statements, bump version. No migration framework needed.

**Performance pragmas:** `synchronous=NORMAL`, `cache_size=-64000`, `temp_store=MEMORY`, `mmap_size=268435456`, `page_size=8192`, `busy_timeout=15000`.

### Data Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│                    FILESYSTEM (fsnotify)                            │
│  File Changes → Create, Modify, Delete, Rename                      │
└───────────────────────┬─────────────────────────────────────────────┘
                        │
                        ↓
        ┌───────────────────────────────────────────────────┐
        │   CLI: FSWatcher (pkg/provenance/fswatcher.go)    │
        │   - Watches project directory recursively         │
        │   - Filters: .gitignore, binary files, temp files │
        │   - Debounces rapid changes                       │
        │                                                   │
        │   Output: FileEvent                               │
        └───────────────────────┬───────────────────────────┘
                                │
                                ↓
        ┌───────────────────────────────────────────────────┐
        │   Engine.PushFileEvent() (pkg/provenance/)        │
        │   - Stores FileEvent in SQLite                    │
        │   - Correlates against unmatched agent events     │
        │   - Returns *ProvenanceRecord if match found      │
        └───────────────────────────────────────────────────┘


┌─────────────────────────────────────────────────────────────────────┐
│                    AGENT SESSION (CLI Agent Watcher)                │
│  SessionData → Exchanges → Messages → Tool Use → PathHints          │
└───────────────────────┬─────────────────────────────────────────────┘
                        │
                        ↓
        ┌───────────────────────────────────────────────────┐
        │   CLI: Session Update Handler                     │
        │   - Extracts file operations from PathHints       │
        │   - Tool types: write, shell, generic             │
        │   - Deterministic ID for deduplication            │
        │   - SessionID, ExchangeID, MessageID              │
        │   - AgentType, AgentModel from session            │
        │                                                   │
        │   Output: AgentEvent                              │
        └───────────────────────┬───────────────────────────┘
                                │
                                ↓
        ┌───────────────────────────────────────────────────┐
        │   Engine.PushAgentEvent() (pkg/provenance/)       │
        │   - Stores AgentEvent in SQLite                   │
        │   - Correlates against unmatched file events      │
        │   - Returns *ProvenanceRecord if match found      │
        └───────────────────────────────────────────────────┘

                                │
                                ↓ (if record != nil)

        ┌───────────────────────────────────────────────────┐
        │   CLI: Provenance Handler                         │
        │   - Log attribution (file ← exchange)             │
        │   - Store in CLI's provenance storage             │
        │   - Eventually: git notes construction            │
        └───────────────────────────────────────────────────┘
```

---

## Implementation Status

### Complete: Core Engine (`pkg/provenance/`)

The correlation engine is fully implemented and tested:

- **`types.go`** — `FileEvent`, `AgentEvent`, `ProvenanceRecord`, `NormalizePath()`, validation methods with sentinel errors
- **`store.go`** — SQLite with WAL mode, `OpenStore()`, `PushFileEvent()`, `PushAgentEvent()`, `QueryUnmatchedFileEvents()`, `QueryUnmatchedAgentEvents()`, `SetMatchedWith()`, `INSERT OR IGNORE` for idempotent inserts
- **`engine.go`** — `NewEngine()` with functional options (`WithDBPath`, `WithMatchWindow`), `PushFileEvent()`, `PushAgentEvent()`, `pathSuffixMatch()`, `findBestMatch()`, `buildRecord()`, `Close()`
- **20 tests** across 3 test files (9 engine, 8 store, 3 types) — all passing

**Dependencies:** `modernc.org/sqlite` (pure Go SQLite driver, already in CLI's `go.mod`)

---

### Complete: `--provenance` Flag & Engine Lifecycle

**Status:** Complete

`run` and `watch` accept `--provenance`. When set, a provenance Engine is created via the shared `startProvenanceEngine()` helper and cleanly shut down on exit. DB created at `~/.specstory/provenance/provenance.db`.

**Key files:** `main.go`

---

### Complete: Extracting & Pushing Agent Events

**Status:** Complete

When provenance is enabled, `agent.go` extracts file-modifying tool uses from SessionData and pushes them as AgentEvents. The shared `processProvenanceEvents()` helper in `main.go` wires this into both `run` and `watch` session callbacks. Dedup is handled via `INSERT OR IGNORE` on deterministic IDs (SHA-256 of sessionID + exchangeID + messageID + path).

- **Tool types that generate events:** `write`, `shell`, `generic` (when PathHints present)
- **Skipped:** `read`, `search`, `task`, `unknown`

**Key files:** `pkg/provenance/agent.go`, `main.go`

---

### Complete: Project Directory File Watching

**Status:** Complete

FSWatcher (`pkg/provenance/fswatcher.go`) watches the project directory recursively using `fsnotify.Watcher`. Debounces rapid changes (100ms). Wired into both `run` and `watch` commands via `startProvenanceFSWatcher()` in `main.go`.

**Filtering** (checks ordered cheapest to most expensive):

1. **Excluded directories** (basename match): `.git`, `.specstory`, `node_modules`, `.next`, `__pycache__`, `.venv`, `venv`, `vendor`, `.idea`, `.vscode`, `dist`, `.claude`, `.cursor`, `.codex`, `.aider`, `.copilot`, `.github`, `.gradle`, `.mvn`, `build`, `target`, `.terraform`, `.cache`, `.tox`, `.eggs`, `.mypy_cache`, `.pytest_cache`, `.ruff_cache`, `coverage`
2. **Hidden files/directories** (basename starts with `.`)
3. **Excluded extensions**: `.exe`, `.dll`, `.so`, `.dylib`, `.o`, `.a`, `.out`, `.class`, `.jar`, `.pyc`, `.pyo`, `.wasm`, `.swp`, `.swo`, `.tmp`, `.bak`, `.log`
4. **`.gitignore` patterns** — loaded from every directory (scoped: patterns only apply under their directory)
5. **`.intentignore` patterns** — loaded from project root only

- **New dependency:** `sabhiram/go-gitignore` for gitignore pattern parsing
- **13 tests** in `fswatcher_test.go` covering filtering, gitignore, nested gitignore scoping, intentignore, debounce, event generation, and excluded directories

**Key files:** `pkg/provenance/fswatcher.go`, `pkg/provenance/fswatcher_test.go`, `main.go`

---

### Phase 4: Logging ProvenanceRecords

**Goal:** Structured logging of provenance matches for observability.

**Steps:**

1. When `PushFileEvent` or `PushAgentEvent` returns a non-nil ProvenanceRecord, log:
   ```go
   slog.Info("Provenance match",
       "filePath", record.FilePath,
       "changeType", record.ChangeType,
       "sessionID", record.SessionID,
       "exchangeID", record.ExchangeID,
       "agentType", record.AgentType,
       "agentModel", record.AgentModel,
       "matchedAt", record.MatchedAt)
   ```

2. Log provenance summary on CLI exit:
   - "Provenance: X file events, Y agent events, Z matches"

**Verification:**
- Manual: run a session with `--provenance --console`, verify readable provenance output
- No provenance logging when `--provenance` is not set

---

### Phase 5: Storing ProvenanceRecords

**Goal:** Persist ProvenanceRecords for later use (git notes construction, querying).

**Steps:**

1. Design storage — **decision point:**
   - Option A: `.specstory/provenance/records.json` (append-only JSON lines)
   - Option B: SQLite table in the provenance engine's DB (would add a method to the engine)

2. Write on match: when push returns non-nil, append to store; flush on shutdown

3. Query interface:
   - `GetRecordsForSession(sessionID string) []ProvenanceRecord`
   - `GetRecordsForFile(filePath string) []ProvenanceRecord`

**Key files:** `pkg/provenance/store.go` (extend) or `pkg/provenance/records.go` (new)

---

### Phase 6: Diff Patches for FS Events

**Goal:** Capture file diffs on each filesystem change for line-level attribution.

The CLI doesn't have a CRDT like Intent. It uses micro-diff chains to track incremental changes:

**On each file event:**
1. Compute diff from previous state
2. Store the micro-diff with the FileEvent ID
3. When ProvenanceRecord arrives, associate the micro-diff with that provenance

**At commit time:**
1. Collect all micro-diffs for files in the commit
2. "Play forward" the diff chain to determine final line provenance
3. Write per-line attribution to git notes

**Diff chain example:**
```
v0 (baseline, msg1):     v1 (msg3, +lines 4-5):   v2 (msg7, ~line 4):
1 - a                    1 - a                    1 - a
2 - b                    2 - b                    2 - b
3 - c                    3 - c                    3 - c
                         4 - d                    4 - d'
                         5 - e                    5 - e
```

**Provenance tracking walks diff results:**
- EQUAL lines: keep existing provenance
- DELETE lines: remove from tracking
- INSERT lines: provenance = current version

**Approach decision deferred** — options:
- `git diff` via shell (standard unified diffs, requires git)
- Pure Go diff library: [sergi/go-diff](https://github.com/sergi/go-diff) or [bluekeyes/go-gitdiff](https://github.com/bluekeyes/go-gitdiff)

**Baseline management:**
- First seen: baseline is file content at watcher start (or empty for new files)
- On each change: update baseline after capturing diff

---

### Phase 7: Git Notes Construction

**Goal:** Construct and write git notes from stored diffs and provenance records.

**Steps:**

1. Design git note format — structured content recording:
   - Which files were changed by AI in a commit
   - Which agent session/exchange made each change
   - The diff patches for each attributed change
   - Per-line attribution from diff chain playback

2. Trigger: on demand (new CLI command or flag) or at commit time (git hook)

3. Implementation:
   - Collect all ProvenanceRecords for files in the commit
   - Collect associated diff patches
   - Play forward diff chain for line-level attribution
   - Construct note content
   - Write via `git notes add` or `git notes append`

---

### Phase 8: DB Cleanup

**Goal:** Prevent unbounded growth of the events table.

**Steps:**

- Add TTL-based cleanup or `PruneOlderThan(time.Duration)` method to engine
- Run on startup or periodically
- Matched events can be pruned more aggressively than unmatched ones

---

## Consumer: Intent Integration

Intent already imports specstory-cli. To use the provenance engine:

```go
import "github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"

engine, _ := provenance.NewEngine()

// FileWatcher pushes filesystem changes
record, _ := engine.PushFileEvent(ctx, provenance.FileEvent{...})

// AgentObserver pushes agent operations
record, _ := engine.PushAgentEvent(ctx, provenance.AgentEvent{...})

// ProvenanceRecord → CRDT storage
if record != nil {
    persistToCRDT(*record)
}
```

Intent's migration involves:
- Replacing `pkg/correlation/engine.go` with the shared engine
- Deleting `pkg/correlation/matchers.go` (scoring system removed)
- Adapting FileWatcher to convert events to `provenance.FileEvent`
- Adapting AgentObserver to convert events to `provenance.AgentEvent`
- Mapping `ProvenanceRecord` output to CRDT storage

Detailed Intent migration planning stays in Intent's own docs.

---

## Key Types Reference

### FileEvent (input)

| Field        | Type        | Required | Description                            |
|--------------|-------------|----------|----------------------------------------|
| `ID`         | `string`    | Yes      | Unique identifier                      |
| `Path`       | `string`    | Yes      | Absolute path, forward slashes         |
| `ChangeType` | `string`    | Yes      | "create", "modify", "delete", "rename" |
| `Timestamp`  | `time.Time` | Yes      | File ModTime                           |

### AgentEvent (input)

| Field           | Type        | Required | Description                              |
|-----------------|-------------|----------|------------------------------------------|
| `ID`            | `string`    | Yes      | Deterministic ID for deduplication       |
| `FilePath`      | `string`    | Yes      | Path the agent touched (may be relative) |
| `ChangeType`    | `string`    | Yes      | "create", "edit", "write", "delete"      |
| `Timestamp`     | `time.Time` | Yes      | When the operation was recorded          |
| `SessionID`     | `string`    | Yes      | Agent session ID                         |
| `ExchangeID`    | `string`    | Yes      | Specific exchange within session         |
| `MessageID`     | `string`    | No       | Agent message ID                         |
| `AgentType`     | `string`    | Yes      | "claude-code", "cursor", etc.            |
| `AgentModel`    | `string`    | No       | "claude-sonnet-4-20250514", etc.         |
| `ActorHost`     | `string`    | No       | Machine hostname                         |
| `ActorUsername` | `string`    | No       | OS user                                  |

### ProvenanceRecord (output)

| Field           | Type        | Description                              |
|-----------------|-------------|------------------------------------------|
| `FilePath`      | `string`    | Absolute path, forward slashes           |
| `ChangeType`    | `string`    | "create", "modify", "delete", "rename"   |
| `Timestamp`     | `time.Time` | When file changed                        |
| `SessionID`     | `string`    | Agent session that caused the change     |
| `ExchangeID`    | `string`    | Specific exchange that caused the change |
| `AgentType`     | `string`    | "claude-code", "cursor", etc.            |
| `AgentModel`    | `string`    | Model used                               |
| `MessageID`     | `string`    | For tracing                              |
| `ActorHost`     | `string`    | Machine hostname                         |
| `ActorUsername` | `string`    | OS user                                  |
| `MatchedAt`     | `time.Time` | When correlation occurred                |

---

## Files Summary

| File                            | Status   | Purpose                                                          |
|---------------------------------|----------|------------------------------------------------------------------|
| `pkg/provenance/types.go`       | Complete | Input/output types, validation, path normalization               |
| `pkg/provenance/engine.go`      | Complete | Correlation engine, path matching, best-match selection          |
| `pkg/provenance/store.go`       | Complete | SQLite persistence, event storage, unmatched queries             |
| `pkg/provenance/agent.go`       | Complete | Extract AgentEvents from SessionData, push to engine             |
| `pkg/provenance/fswatcher.go`   | Phase 3  | Project directory file watcher with file/directory filtering     |

## New Dependencies

`github.com/sabhiram/go-gitignore` (or similar) - .gitignore/.intentignore parsing

## Decisions Deferred

- **Phase 5:** Storage format — JSON lines file vs SQLite table
- **Phase 6:** Diff approach — git shell vs Go library
- **Phase 7:** Git notes format and trigger mechanism
