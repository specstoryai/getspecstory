# AI Provenance CLI Integration Plan

## Context

The `ai-provenance-lib` (Phase 1-3 complete) provides a correlation engine that matches filesystem changes to AI agent activity. This plan covers Phase 4: integrating the library into `specstory-cli` so that `run` and `watch` commands can track which file changes were made by AI agents.

The integration is incremental — each phase is independently testable with unit tests and manual verification before moving to the next.

## Architecture Overview

Provenance is a cross-cutting concern added at the CLI command level. The existing Provider interface and SPI are **not modified**. Instead, provenance wraps the existing session callback pattern:

```
main.go (run/watch commands)
    │
    ├── provenance.Engine (created if --provenance flag)
    ├── provenance.FSWatcher (watches project dir → pushes FileEvents)
    │
    └── sessionCallback (wraps existing callback)
        ├── processSingleSession (existing — markdown + cloud sync)
        └── provenance.PushAgentEvent (new — extracts from SessionData, pushes AgentEvent)
```

New code lives in `pkg/provenance/` in the CLI repo. The provenance library is imported as `github.com/specstoryai/ai-provenance-lib`.

## Key Files Reference

| File                                  | Role                                                |
|---------------------------------------|-----------------------------------------------------|
| `main.go`                             | Command definitions, flag setup, session callbacks  |
| `pkg/spi/schema/types.go`             | SessionData, Exchange, Message, ToolInfo, PathHints |
| `../ai-provenance-lib/types.go`       | FileEvent, AgentEvent, ProvenanceRecord             |

---

## Phase 1: Local Build Setup

**Goal:** CLI compiles with the provenance library as a dependency.

### Steps

1. **Add `go.work.example` to specstory-cli root** (committed to git):

```go
// Copy this file to go.work and adjust paths as needed.
// go.work is gitignored — it's a local development convenience.
go 1.25.6

use (
    .
    ../ai-provenance-lib
)
```

2. **Add `go.work` to `.gitignore`** (and `go.work.sum`)

3. **Create local `go.work`** by copying `go.work.example`

4. **Add dependency to `go.mod`:**

```go
require github.com/specstoryai/ai-provenance-lib v0.1.0
```

With `go.work` active, Go resolves this locally. CI will use `GOPRIVATE` + GitHub PAT.

5. **Verify build:** `go build -o specstory`

### Verification

- `go build -o specstory` succeeds
- `go test ./...` passes (no behavioral changes)
- `golangci-lint run` passes

---

## Phase 2: --provenance Flag & Engine Lifecycle

**Goal:** `run` and `watch` accept `--provenance`. When set, a provenance Engine is created and cleanly shut down.

### Steps

1. **Create `pkg/provenance/provenance.go`** — engine lifecycle management:

- `StartEngine(dbPath string) (*provenance.Engine, error)` — creates engine with optional custom DB path
- `StopEngine(engine *provenance.Engine)` — closes engine
- Uses the library's default DB path by not specifying one (`~/.specstory/provenance/provenance.db`)

2. **Add flags in `main.go`:**

- `--provenance` (bool) on `runCmd` and `watchCmd`
- Add flag variable: `var provenanceEnabled bool`

3. **Wire up in run/watch command handlers:**

- If `--provenance` is true, call `StartEngine()`
- Defer `StopEngine()` for cleanup
- Log engine creation at INFO level
- Log engine stop at INFO level

### Verification

- `specstory run --provenance` starts normally, logs "Provenance engine started", creates DB file at `~/.specstory/provenance/provenance.db`
- `specstory run` (without flag) works unchanged
- `specstory watch --provenance` same behavior

---

## Phase 3: Extracting & Pushing Agent Events

**Goal:** When provenance is enabled, extract file-modifying tool uses from SessionData and push them as AgentEvents.

### Steps

1. **In `pkg/provenance/provenance.go`:**

- **Which tools generate AgentEvents:**
  - `write`, `shell`, `generic` when there is a path hint, create one AgentEvent per path hint
  - Skip: `read`, `search`, `task`, `unknown` (don't modify files directly)

- `ExtractAgentEvents(sessionData *schema.SessionData) []provenance.AgentEvent`
  - Iterates exchanges → messages
  - For each message with a specified type tool AND PathHints:
  - Creates an AgentEvent per PathHint
  - Deterministic ID: SHA-256 of `sessionID + exchangeID + messageID + path` (stable across re-processing)
  - Maps tool type to change type: `write` tools → `"write"`, could refine later to `"create"` vs `"edit"`
  - Timestamp from message timestamp
  - SessionID, ExchangeID from exchange
  - MessageID from message ID
  - AgentType from `sessionData.Provider.ID` (e.g., "claude-code")
  - AgentModel from message Model field
  - Returns deduplicated list

2. **In `pkg/provenance/provenance.go`:**

- `PushAgentEvents(ctx context.Context, engine *provenance.Engine, events []provenance.AgentEvent) []*provenance.ProvenanceRecord`
  - Pushes each event to engine
  - Collects and returns any non-nil ProvenanceRecords
  - Logs each push at DEBUG level, each match at INFO level

1. **Wire into session callback in `main.go`:**

- When provenance is enabled, after `processSingleSession()`:

    ```go
    if provenanceEngine != nil && session.SessionData != nil {
        events := provenance.ExtractAgentEvents(session.SessionData)
        records := provenance.PushAgentEvents(ctx, provenanceEngine, events)
        // records logged inside PushAgentEvents
    }
    ```

- The library handles dedup via `INSERT OR IGNORE`, so pushing the same events on every callback is safe

### Verification

- Unit tests for `ExtractAgentEvents`:
- SessionData with Write tool + PathHints → produces AgentEvent
- SessionData with Read tool → produces nothing
- Multiple PathHints on one tool → multiple AgentEvents
- Deterministic IDs: same input → same ID
- Empty SessionData → empty result
- Manual test: `specstory run --provenance --console --debug`, use Claude Code to edit a file, see "Agent event pushed" log entries
- `golangci-lint run` passes

---

## Phase 4: Project Directory File Watching

**Goal:** Watch the user's project directory for file changes and push FileEvents to the provenance engine.

### Steps

1. **Create `pkg/provenance/fswatcher.go`:**

- `FSWatcher` struct:
  - Wraps `fsnotify.Watcher` (already a CLI dependency)
  - Watches project directory **recursively** (walk tree, add each subdirectory, add new dirs on Create events)
  - On file Create/Write/Remove events, generates `provenance.FileEvent`
  - Pushes to engine via `PushFileEvent`
  - Debounces rapid changes (same file within 100ms)

- `NewFSWatcher(engine *provenance.Engine, projectDir string) (*FSWatcher, error)`
- `Start(ctx context.Context) error` — begins watching
- `Stop()` — stops watching, cleans up

- **Recursive watching pattern** (fsnotify doesn't do this natively):
  - `filepath.WalkDir` to add all subdirectories on startup
  - On `fsnotify.Create` of a directory, add it to the watcher
  - On `fsnotify.Remove` of a directory, remove it (fsnotify may do this automatically)

1. **Create `pkg/provenance/filter.go`:**

- File/directory filtering logic:
  - **Hardcoded directory exclusions:** `.git`, `.specstory`, `node_modules`, `.next`, `__pycache__`, `.venv`, `vendor`, `.idea`, `.vscode`
  - **Hardcoded file exclusions:** binary extensions (`.exe`, `.bin`, `.o`, `.so`, `.dylib`, `.png`, `.jpg`, `.gif`, `.pdf`, `.zip`, `.tar`, `.gz`), lock files (`package-lock.json`, `yarn.lock`,
`go.sum`), temp files (`*~`, `.swp`, `.swo`)
  - **Gitignore-style patterns:** Parse `.gitignore` and `.intentignore` files from the project root
  - For gitignore parsing: evaluate `sabhiram/go-gitignore` (lightweight, popular) or `go-git/go-git/plumbing/format/gitignore` (from go-git, heavier). Recommend `sabhiram/go-gitignore` for
simplicity — new dependency, needs approval.
- `ShouldWatch(path string, isDir bool) bool` — returns whether a path should be watched

1. **Wire into run/watch commands in `main.go`:**

- When provenance is enabled:

    ```go
    fsWatcher, err := provenance.NewFSWatcher(provenanceEngine, cwd)
    fsWatcher.Start(ctx)
    defer fsWatcher.Stop()
    ```

1. **FileEvent details:**

- ID: UUID (each FS event is unique, no dedup needed)
- Path: absolute path from fsnotify (already absolute)
- ChangeType: map fsnotify ops → "create", "modify", "delete"
- Timestamp: file's ModTime from `os.Stat()` (not event time)

### Verification

- Unit tests for `filter.go`:
- `.git/` excluded
- `.specstory/` excluded
- `node_modules/` excluded
- Binary files excluded
- Normal source files included
- `.gitignore` patterns respected
- `.intentignore` patterns respected
- Unit tests for `fswatcher.go`:
- Mock or temp dir with file creates/modifications
- Verify FileEvents generated with correct paths and types
- Manual test: `specstory run --provenance --console --debug`, edit a file in the project, see:
- "File event pushed" log
- If agent is also editing → "Provenance match!" log showing correlation
- `golangci-lint run` passes

---

## Phase 5: Logging ProvenanceRecords

**Goal:** Formalize structured logging of provenance matches for observability.

### Steps

1. **Enhance logging in `pkg/provenance/push.go`:**
- When `PushFileEvent` or `PushAgentEvent` returns a non-nil ProvenanceRecord:
    ```
    slog.Info("Provenance match",
        "filePath", record.FilePath,
        "changeType", record.ChangeType,
        "sessionID", record.SessionID,
        "exchangeID", record.ExchangeID,
        "agentType", record.AgentType,
        "agentModel", record.AgentModel,
        "matchedAt", record.MatchedAt)
    ```
- Also log summary statistics periodically or on shutdown:
    - Total file events pushed
    - Total agent events pushed
    - Total matches found

1. **Log provenance summary on CLI exit** (in the deferred shutdown):
- "Provenance: X file events, Y agent events, Z matches"

### Verification

- Manual test: run a full coding session with `--provenance --console`, verify readable provenance output
- Verify no provenance logging when `--provenance` is not set

---

## Phase 6: Storing ProvenanceRecords

**Goal:** Persist ProvenanceRecords so they can be used for git notes construction later.

### Steps

1. **Design storage in `pkg/provenance/store.go`:**
- Store ProvenanceRecords in a local file: `.specstory/provenance/records.json` (append-only JSON lines)
- Each line is a JSON-serialized ProvenanceRecord
- Keyed by file path + timestamp for later lookup
- Alternative: use a SQLite table in the provenance lib's DB (would require adding a method to the lib). **Decision point — discuss with user when we reach this phase.**

1. **Write on match:**
- When PushFileEvent or PushAgentEvent returns non-nil, append to store
- Flush on shutdown

1. **Query interface:**
- `GetRecordsForSession(sessionID string) []ProvenanceRecord`
- `GetRecordsForFile(filePath string) []ProvenanceRecord`

### Verification

- Unit tests for store read/write
- Manual test: run a session, verify records appear in storage file
- Verify records survive across CLI restarts

---

## Phase 7: Diff Patches for FS Events

**Goal:** Capture file diffs on each filesystem change so we have the actual content changes alongside provenance attribution.

### Steps

1. **Approach: TBD** — decided to defer this choice. Options:
- `git diff` via shell (standard unified diffs, requires git)
- Pure Go diff library (no git dependency for this piece)

1. **Capture flow:**
- On each FileEvent, before pushing to engine:
    - Read current file content
    - Compute diff from previous known state
    - Store diff patch associated with the FileEvent ID

1. **Baseline management:**
- Need to track "last known state" of each file
- On first seen: baseline is the file content at watcher start (or empty for new files)
- On each change: update baseline after capturing diff

1. **Storage:**
- Diff patches stored alongside ProvenanceRecords
- Associated by FileEvent ID

### Verification

- Unit tests for diff capture
- Manual test: edit a file, verify diff patch is captured and associated with provenance
- Verify diffs are standard unified diff format

---

## Phase 8: Git Notes Construction

**Goal:** Construct and write git notes from stored diffs and provenance records.

### Steps

1. **Design git note format** — structured content that records:
- Which files were changed by AI in a commit
- Which agent session/exchange made each change
- The diff patches for each attributed change

1. **Trigger:** On demand (new CLI command or flag) or at commit time (git hook)

2. **Implementation:**
- Collect all ProvenanceRecords for files in the commit
- Collect associated diff patches
- Construct note content
- Write via `git notes add` or `git notes append`

### Verification

- Manual test: make AI-attributed changes, commit, verify git note content
- `git notes show` displays attribution data

---

## New Files Summary

| File                                | Purpose                                                         |
|-------------------------------------|-----------------------------------------------------------------|
| `go.work.example`                   | Template for local development workspace                        |
| `pkg/provenance/provenance.go`      | Engine lifecycle (start/stop)                                   |
|                                     | Extract AgentEvents from SessionData                            |
|                                     | Push events to engine, handle records                           |
| `pkg/provenance/provenance_test.go` | Unit tests for agent event extraction                           |
| `pkg/provenance/fswatcher.go`       | Project directory file watcher                                  |
|                                     | File/directory filtering (.gitignore, .intentignore, hardcoded) |
| `pkg/provenance/fswatcher_test.go`  | Unit tests for file filtering                                   |
|                                     | Unit tests for file watcher                                     |
| `pkg/provenance/store.go`           | ProvenanceRecord persistence                                    |
| `pkg/provenance/store_test.go`      | Unit tests for record storage                                   |

## New Dependencies

| Dependency                                      | Purpose                          | Phase              |
|-------------------------------------------------|----------------------------------|--------------------|
| `github.com/specstoryai/ai-provenance-lib`      | Provenance correlation engine    | 1                  |
| `github.com/sabhiram/go-gitignore` (or similar) | .gitignore/.intentignore parsing | 4 (needs approval) |

## Decisions Deferred

- **Phase 6:** Storage format — JSON lines file vs SQLite table
- **Phase 7:** Diff approach — git shell vs Go library
- **Phase 8:** Git notes format and trigger mechanism