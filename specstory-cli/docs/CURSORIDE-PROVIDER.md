# Implementation Plan: Add Cursor IDE Provider (cursoride)

## Overview

Add a new provider called `cursoride` that reads Cursor's SQLite database directly (same as the extension does) to export conversations to the `.specstory` folder. This is phase 1 - focusing on database reading, conversation extraction, workspace filtering, and debug output. Tool export formatting differences will be addressed in later phases.

## Quick Summary

**Goal:** Port extension's Cursor database reading functionality to CLI as a new provider

**Key Features:**
- Read from global Cursor database (`~/.cursor/extensions/.../state.vscdb`)
- Filter by workspace (matches Claude Code's project-scoped behavior)
- Export conversations to markdown in `.specstory/history/`
- Debug mode for raw data inspection

**Technology:**
- `modernc.org/sqlite` (pure Go, no CGO)
- Workspace filtering via workspace metadata matching
- Standard SPI provider interface

**Complexity:** Medium-High (workspace filtering adds complexity but matches Claude Code behavior)

## Background

The existing extension (in `ts-extension/`) reads from Cursor's SQLite database at `~/.cursor/extensions/cursor-context-manager-*/globalStorage/cursor-context-manager/state.vscdb`. We're porting this functionality to the CLI as a new provider.

## Critical Files

### New Files to Create
1. `pkg/providers/cursoride/provider.go` - Main provider implementation
2. `pkg/providers/cursoride/database.go` - SQLite database reading
3. `pkg/providers/cursoride/parser.go` - Parse Cursor native format to internal representation
4. `pkg/providers/cursoride/agent_session.go` - Convert to unified SessionData format
5. `pkg/providers/cursoride/types.go` - Go structs for Cursor data structures
6. `pkg/providers/cursoride/path_utils.go` - Cursor-specific path resolution

### Files to Modify
1. `pkg/spi/factory/registry.go` - Register the new provider
2. `go.mod` - Add SQLite dependency (e.g., `modernc.org/sqlite` or `github.com/mattn/go-sqlite3`)

## Implementation Steps

### Step 1: Add SQLite Dependency
- **Decision needed:** Choose between CGO-free (`modernc.org/sqlite`) vs CGO-based (`github.com/mattn/go-sqlite3`)
  - `modernc.org/sqlite`: Pure Go, easier cross-compilation, no CGO needed
  - `github.com/mattn/go-sqlite3`: More mature, faster, requires CGO
- Add to `go.mod`

### Step 2: Create Provider Structure
Create `pkg/providers/cursoride/provider.go`:
- Define `Provider` struct (empty struct, stateless)
- Implement `NewProvider()` constructor
- Implement `Name()` → return "Cursor IDE"
- Stub out all 6 required interface methods initially

### Step 3: Path Resolution
Create `pkg/providers/cursoride/path_utils.go`:
- `GetGlobalDatabasePath()` → Find `~/.cursor/extensions/cursor-context-manager-*/globalStorage/cursor-context-manager/state.vscdb`
- `GetWorkspaceStoragePath()` → Find `~/Library/Application Support/Cursor/User/workspaceStorage/` (macOS) or equivalent
- `FindMatchingWorkspace(projectPath)` → Match projectPath to workspace directory (if Option B chosen)
- Use glob pattern to find versioned extension directory
- Handle case where database doesn't exist
- Check for WAL files (`.vscdb-wal`)

### Step 4: Define Data Structures
Create `pkg/providers/cursoride/types.go` - Port TypeScript types to Go:
```go
type ComposerData struct {
    ComposerID                    string                           `json:"composerId"`
    Name                          string                           `json:"name,omitempty"`
    Conversation                  []ComposerConversation           `json:"conversation,omitempty"`
    FullConversationHeadersOnly   []ComposerConversationHeader     `json:"fullConversationHeadersOnly"`
    Capabilities                  []Capability                     `json:"capabilities,omitempty"`
    CodeBlockData                 map[string][]CodeBlockData       `json:"codeBlockData,omitempty"`
    CreatedAt                     int64                            `json:"createdAt"`
    LastUpdatedAt                 int64                            `json:"lastUpdatedAt,omitempty"`
}

type ComposerConversation struct {
    BubbleID        string                 `json:"bubbleId"`
    Type            int                    `json:"type"` // 1=user, 2=assistant
    Text            string                 `json:"text"`
    Thinking        *ThinkingData          `json:"thinking,omitempty"`
    CapabilityType  int                    `json:"capabilityType,omitempty"` // 15=tool
    TimingInfo      *TimingInfo            `json:"timingInfo,omitempty"`
    CodeBlocks      []CodeBlockReference   `json:"codeBlocks,omitempty"`
    ModelInfo       *ModelInfo             `json:"modelInfo,omitempty"`
    UnifiedMode     int                    `json:"unifiedMode,omitempty"` // 1=Ask, 2=Agent, 5=Plan
    ToolFormerData  *ToolInvocationData    `json:"toolFormerData,omitempty"`
}

// ... other supporting types
```

### Step 5: Database Reading
Create `pkg/providers/cursoride/database.go`:
- `OpenDatabase(path string)` → Open SQLite in read-only mode
- `EnableWALMode(db)` → Enable WAL mode for non-blocking reads
  - **Use cursorcli's simpler approach:** Just execute `PRAGMA journal_mode=WAL`
  - Log warning if it fails but continue (non-fatal)
  - Simpler than extension's reopen-in-readwrite approach
  - Works because database is likely already in WAL mode
- `LoadComposerData(composerID string)` → Query for specific composer from global database
- `LoadAllComposerIDs()` → Query for all `composerData:*` keys from global database
- `LoadComposerDataBatch(composerIDs []string)` → Load multiple composers efficiently (used with workspace filtering)
- `LoadWorkspaceComposerRefs(workspaceDbPath)` → Load `composer.composerData.allComposers` from workspace database
- SQL queries (matching extension's implementation):
  ```sql
  -- Load single composer and its bubbles
  SELECT key, value FROM cursorDiskKV
  WHERE value IS NOT NULL AND (
    key = 'composerData:' || ?
    OR key LIKE 'bubbleId:' || ? || ':%'
  )

  -- Load multiple composers (for workspace filtering)
  -- Uses IN clause for composerData (indexed) and LIKE for bubbleId (prefix match, indexed)
  SELECT key, value FROM cursorDiskKV
  WHERE value IS NOT NULL AND (
    key IN ('composerData:id1', 'composerData:id2', ...)
    OR (key LIKE 'bubbleId:id1:%' OR key LIKE 'bubbleId:id2:%' ...)
  )

  -- Load all composer IDs (for detection/listing)
  SELECT key FROM cursorDiskKV
  WHERE key LIKE 'composerData:%'

  -- Load workspace composer refs
  SELECT key, value FROM ItemTable
  WHERE key = 'composer.composerData'
  ```
- Parse JSON values into Go structs
- **Important:** Skip checkpoint, messageRequestContext, codeBlockDiff keys (not needed for rendering, per extension)

### Step 6: Workspace Matching
Create `pkg/providers/cursoride/workspace.go`:
- `FindWorkspaceForProject(projectPath string)` → Find matching workspace directory
- `GetWorkspaceStoragePath()` → Get macOS/Linux workspace storage location
  - macOS: `~/Library/Application Support/Cursor/User/workspaceStorage/`
  - Linux: `~/.config/Cursor/User/workspaceStorage/`
- `MatchWorkspaceURI(projectPath, workspaceURI string)` → Compare paths accounting for symlinks
- `LoadWorkspaceComposerIDs(workspaceDbPath)` → Read `composer.composerData.allComposers[]` from workspace DB
- Handle errors gracefully:
  - Workspace storage doesn't exist
  - No matching workspace found
  - Workspace database missing or corrupted
  - No composer data in workspace

### Step 7: Parse Conversations
Create `pkg/providers/cursoride/parser.go`:
- `ParseComposerData(jsonStr string)` → Unmarshal JSON to ComposerData
- `LoadFullConversation(composerID string, db *sql.DB)` → Load composer + all bubbles
- `AssembleConversation(composer ComposerData, bubbles map[string]ComposerConversation)` → Build complete conversation
- Handle both conversation array and bubble loading patterns
- Skip empty conversations (no messages)

### Step 8: Convert to Unified Format
Create `pkg/providers/cursoride/agent_session.go`:
- `ConvertToSessionData(composer ComposerData, bubbles map[string]ComposerConversation)` → Build `spi.SessionData`
- Map Cursor message types to unified format:
  - type 1 → role "user"
  - type 2 → role "agent"
- Extract tool invocations from capabilityType=15 and toolFormerData
- Build exchanges array (user message + assistant response pairs)
- For now, use simple tool classification (defer detailed tool mapping to phase 2)
- Generate slug from conversation name or first user message
- Format timestamps as ISO 8601

### Step 9: Implement Core Provider Methods

In `pkg/providers/cursoride/provider.go`:

1. **Check(customCommand):**
   - Verify database file exists at expected path
   - Try to open database in read-only mode
   - Return version info (could query database schema version or Cursor version)
   - Return CheckResult with success/error

2. **DetectAgent(projectPath, helpOutput):**
   - Check if global database exists
   - Check if workspace storage directory exists
   - Try to match projectPath to a workspace (use workspace matching logic)
   - If match found, check if that workspace has any composer IDs
   - Return true if workspace found with composer data
   - If helpOutput=true, print guidance about:
     - Global database location
     - Workspace storage location
     - Expected workspace path

3. **GetAgentChatSession(projectPath, sessionID, debugRaw):**
   - Open database
   - Load specific composer by ID (sessionID = composerID)
   - Parse conversation and bubbles
   - Convert to AgentChatSession
   - If debugRaw=true, write raw composer JSON to `.specstory/debug/<sessionID>/raw-composer.json`
   - Return single session

4. **GetAgentChatSessions(projectPath, debugRaw):**
   - Find workspace storage path (`~/Library/Application Support/Cursor/User/workspaceStorage/`)
   - Match projectPath to workspace:
     - Read each workspace directory's `workspace.json`
     - Compare workspace URI with projectPath
     - Find matching workspace directory
   - Open workspace database (`<workspace-dir>/state.vscdb`)
   - Load composer IDs from `composer.composerData.allComposers`
   - Open global composer database
   - For each composer ID, load full conversation from global database
   - Filter out empty conversations
   - Convert each to AgentChatSession
   - If debugRaw=true, write debug files for each
   - Return array of sessions (workspace-filtered)

5. **ExecAgentAndWatch(...):**
   - Return error: "Cursor IDE does not support execution via CLI"
   - This provider is read-only (IDE-based, not CLI-based)

6. **WatchAgent(ctx, projectPath, debugRaw, callback):**
   - Use fsnotify to watch database file and WAL file
   - On file changes, reload all sessions
   - Invoke callback for new/updated sessions
   - Support graceful shutdown via context
   - Debounce rapid changes (database writes can trigger multiple events)

### Step 10: Register Provider
Modify `pkg/spi/factory/registry.go`:
```go
import (
    // ... existing imports ...
    "github.com/specstoryai/SpecStoryCLI/pkg/providers/cursoride"
)

func (r *Registry) registerAll() {
    // ... existing registrations ...

    cursorideProvider := cursoride.NewProvider()
    r.providers["cursoride"] = cursorideProvider
    slog.Debug("Registered provider", "id", "cursoride", "name", cursorideProvider.Name())

    r.initialized = true
}
```

### Step 11: Debug Output Support

When `--debug-raw` flag is passed:
- Write to `.specstory/debug/<composerID>/`
- Files to write:
  - `raw-composer.json` - Raw composer data from database
  - `bubbles.json` - All bubble data
  - `session-data.json` - Unified SessionData format (handled by central CLI)
- Use `spi.WriteDebugSessionData()` utility

## Usage Examples

```zsh
# Check if Cursor IDE database exists
./specstory check cursoride

# Sync all Cursor IDE conversations
./specstory sync cursoride

# Sync specific conversation
./specstory sync cursoride -u <composer-id>

# Debug mode - see raw data
./specstory sync cursoride --debug-raw

# Watch mode - monitor for new conversations
./specstory watch cursoride
```

## Phase 1 Scope (This Plan)

**In Scope:**
- ✅ Database reading from SQLite (global + workspace)
- ✅ Workspace matching and filtering
- ✅ Conversation parsing (composer + bubbles)
- ✅ Basic tool invocation detection (capabilityType=15)
- ✅ Conversion to unified SessionData
- ✅ Debug output support
- ✅ Provider registration
- ✅ WAL mode handling

**Out of Scope (Future Phases):**
- ❌ Detailed tool output formatting differences
- ❌ Code block diff rendering
- ❌ Capability data parsing
- ❌ Tool-specific markdown formatting
- ❌ Multi-version ComposerData support (V1 vs V3)
- ❌ Checkpoint data handling
- ❌ messageRequestContext parsing
- ❌ codeBlockDiff data

## Important Implementation Notes

### Database Access Patterns
1. **WAL Mode:** Both extension and cursorcli enable WAL mode for non-blocking reads
   - **Use cursorcli's approach:** Simply execute `PRAGMA journal_mode=WAL` and treat failure as non-fatal
   - Extension uses more complex reopen-in-readwrite approach, but cursorcli's simpler method works fine
   - Purpose: Prevents readers from blocking Cursor IDE's writers to the database
2. **Two Database Types:** Global database uses `cursorDiskKV` table, workspace databases use `ItemTable`
3. **Key Filtering:** Extension explicitly skips `checkpoint*`, `messageRequestContext*`, `codeBlockDiff*` keys - we should too
4. **Query Optimization:** Use IN clause for exact matches, LIKE with trailing % for prefix matches (both use index)

### Workspace Matching Challenges
1. **Symlinks:** Must use canonical paths for matching (like cursorcli does with MD5)
2. **Multiple Workspaces:** User may have same project opened in multiple workspace IDs - use newest
3. **Workspace Types:** Can be single folder or multi-root workspace file
4. **OS-Specific Paths:**
   - macOS: `~/Library/Application Support/Cursor/User/workspaceStorage/`
   - Linux: `~/.config/Cursor/User/workspaceStorage/`

### Error Handling
1. **Missing Databases:** Graceful failure if global DB or workspace storage doesn't exist
2. **No Workspace Match:** Clear error message explaining workspace detection
3. **Empty Conversations:** Filter out composers with no conversation messages
4. **Corrupted Data:** Skip malformed JSON, log warning, continue processing other composers
5. **Large Databases:** Extension has retry logic with delays - consider similar approach

## Testing Strategy

Manual testing approach (no unit tests initially per project conventions):
1. Create test database with sample conversations
2. Test `check` command - verify database detection
3. Test `sync` command - verify conversation export
4. Test `--debug-raw` - verify debug files are written
5. Verify markdown files are created in `.specstory/history/`
6. Test with empty database
7. Test with missing database

## Verification Checklist

After implementation:
- [ ] `./specstory check cursoride` successfully finds database
- [ ] `./specstory sync cursoride` exports conversations to `.specstory/history/`
- [ ] `./specstory sync cursoride --debug-raw` creates debug files
- [ ] Markdown files have correct format (timestamps, speakers, messages)
- [ ] Tool invocations are detected (basic level)
- [ ] Empty conversations are filtered out
- [ ] Provider appears in help text for all commands
- [ ] No errors with missing database (graceful failure)

## Provider Comparison: cursorcli vs cursoride

### Overview
**Important:** `cursorcli` and `cursoride` are **completely different systems** for different Cursor products:
- **cursorcli** = Cursor CLI (command-line agent tool)
- **cursoride** = Cursor IDE (VS Code-based editor with Composer)

### Detailed Comparison

| Aspect | cursorcli (Cursor CLI) | cursoride (Cursor IDE) |
|--------|------------------------|------------------------|
| **Product** | Cursor CLI agent (`cursor-agent` command) | Cursor IDE (VS Code fork) |
| **Data Location** | `~/.cursor/chats/<md5-hash>/<session-id>/store.db` | Global: `~/.cursor/extensions/.../state.vscdb`<br>Workspace: `~/Library/.../workspaceStorage/` |
| **Database Structure** | One SQLite DB per session | Single global SQLite DB for all composers |
| **Database Schema** | `blobs` table (id, data)<br>`meta` table (key, value) | Global DB: `cursorDiskKV` (key, value)<br>Workspace DB: `ItemTable` (key, value)<br>Keys: `composerData:*`, `bubbleId:*` |
| **Project Matching** | MD5 hash of canonical project path | Workspace URI matching via `workspace.json` |
| **Project Scoping** | Directory-based (one hash dir per project) | Workspace filtering (IDs in workspace DB) |
| **SQLite Library** | `modernc.org/sqlite` (pure Go) | `modernc.org/sqlite` (pure Go) - **Same!** |
| **Execution** | ✅ Can execute `cursor-agent` CLI | ❌ Cannot execute (IDE-based) |
| **Watch Mode** | Watches `store.db` files in session dirs | Watches global database + WAL file |
| **Session Format** | Blob records with DAG structure | Composer data with conversation arrays |
| **Message Storage** | Blobs with JSON data field | Separate bubble records for messages |
| **Tool Invocations** | Embedded in blob data | `toolFormerData`, `capabilityType=15` |
| **CLI Already Exists** | ✅ Yes (provider: `cursorcli`) | ❌ No (what we're building) |

### Key Architectural Differences

**cursorcli (Existing):**
```
~/.cursor/chats/
  └── a1b2c3d4.../ (MD5 hash of /Users/bago/code/project)
      ├── session-uuid-1/
      │   └── store.db (SQLite: blobs, meta tables)
      └── session-uuid-2/
          └── store.db
```
- **Project matching:** Calculate MD5(canonical_project_path), look for that directory
- **Simple:** One DB per session, inherently project-scoped
- **Execution:** Can launch `cursor-agent` command

**cursoride (What We're Building):**
```
~/.cursor/extensions/cursor-context-manager-*/
  └── globalStorage/cursor-context-manager/
      └── state.vscdb (ALL composer data)

~/Library/Application Support/Cursor/User/workspaceStorage/
  ├── workspace-id-1/
  │   ├── workspace.json (contains workspace URI)
  │   └── state.vscdb (composer.composerData.allComposers: [...])
  └── workspace-id-2/
      ├── workspace.json
      └── state.vscdb
```
- **Project matching:** Find workspace where URI matches projectPath, load composer IDs from workspace DB, query global DB
- **Complex:** Global DB + workspace filtering required
- **Execution:** Cannot launch (IDE-based, not CLI)

### Why We Need Both Providers

These are fundamentally different systems:
1. **Different use cases:** CLI tool vs IDE
2. **Different data formats:** Blob-based vs Composer-based
3. **Different storage:** Per-session files vs global database
4. **Different tools:** Can execute vs read-only

Users may use both Cursor CLI and Cursor IDE on the same project, so SpecStory CLI needs to support both!

## Decisions

1. **SQLite Library:** `modernc.org/sqlite`
   - Pure Go implementation (no CGO)
   - Easier cross-compilation for macOS/Linux
   - Better for distribution and CI/CD
   - **Already used by `cursorcli` provider** - proven to work well

2. **Workspace Filtering:** Implement workspace-scoped filtering (matches Claude Code behavior)
   - Read workspace metadata from `~/Library/Application Support/Cursor/User/workspaceStorage/`
   - Match projectPath to workspace by comparing URIs in `workspace.json` files
   - Load composer IDs from that workspace's `state.vscdb`
   - Filter global database queries to only those composer IDs
   - Properly project-scoped like Claude Code

3. **Session ID Mapping:** Use composerID directly as sessionID
   - Maintains compatibility with extension
   - Simple mapping, no translation needed

## Dependencies

- `modernc.org/sqlite` - Pure Go SQLite library (no CGO)
- Existing SPI infrastructure
- fsnotify (already in use for claudecode)
- Standard library: encoding/json, path/filepath, os

## Success Criteria

- New `cursoride` provider is registered and available
- Can read Cursor SQLite database
- Can parse conversations from database
- Can export conversations to markdown files
- Debug mode shows raw data structures
- No crashes or panics with missing/invalid data
- Graceful error messages for common issues
