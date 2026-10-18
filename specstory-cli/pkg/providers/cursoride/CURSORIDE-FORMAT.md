# Cursor IDE — On-Disk Session Storage Format Spec

Authoritative specification of how **Cursor IDE** (the VS Code fork, not the
`cursor-agent` CLI) stores Composer/Agent conversations on disk. Intended for an
engineer maintaining or extending the `cursoride` provider. Every claim below is
backed by the shipped parser in `pkg/providers/cursoride/` and by real captured
data.

- App: Cursor IDE (VS Code lineage). Baseline: Cursor 2 through Cursor 3.12+.
- Provider ID: `cursoride` (`pkg/providers/cursoride/provider.go:21`)
- SQLite driver: `modernc.org/sqlite` (pure Go, no CGO) — the same driver
  `cursorcli` uses.

Cursor keeps **all** conversations in one global key-value SQLite database, and
records project association in up to three different places depending on
version. Section 4 is the part that actually matters: getting project scoping
wrong is the difference between exporting the right sessions and exporting
someone else's.

> **Not to be confused with `cursorcli`.** That provider reads the Cursor CLI
> agent's per-session `~/.cursor/chats/<md5>/<session-id>/store.db`. Different
> product, different format, different storage. See §9.

---

## 1. Directory layout

```
<user-data-dir>/User/
├── globalStorage/
│   └── state.vscdb              # ★ ALL composer data for every project
│       state.vscdb-wal          # WAL sidecar — where live writes actually land
│       state.vscdb-shm
└── workspaceStorage/
    ├── <workspace-id>/          # md5 hash, see §4.1
    │   ├── workspace.json       # { "folder": "<uri>" } or { "workspace": "<uri>" }
    │   └── state.vscdb          # per-workspace ItemTable
    └── <workspace-id>/
        └── …
```

`<user-data-dir>` resolution, in precedence order
(`pkg/providers/cursoride/path_utils.go`):

1. `--user-data-dir cursoride:<path>` override (the path is the parent of `User`).
2. Under WSL only: the Windows-side install, found via `/mnt/c/Users/*/AppData/Roaming/Cursor`.
3. The OS default:

| OS      | Path                                              |
| ------- | ------------------------------------------------- |
| macOS   | `~/Library/Application Support/Cursor`             |
| Linux   | `~/.config/Cursor`                                 |
| Windows | `%APPDATA%\Cursor`                                 |

A missing override warns and falls through to the OS default rather than
disabling the provider; a missing OS default stays quiet, because that is just
"Cursor isn't installed."

---

## 2. The two database shapes

Cursor uses two different table schemas, and which one you are reading matters.

| Database                            | Table          | Columns        | Holds                                     |
| ----------------------------------- | -------------- | -------------- | ----------------------------------------- |
| `globalStorage/state.vscdb`         | `cursorDiskKV` | `key`, `value` | Conversations — composers and bubbles     |
| `globalStorage/state.vscdb`         | `ItemTable`    | `key`, `value` | Global UI state — sidebar, headers (§4.4) |
| `workspaceStorage/<id>/state.vscdb` | `ItemTable`    | `key`, `value` | Per-workspace UI state, composer refs     |
| `globalStorage/state.vscdb`         | `composerHeaders` | (SQL table) | Cursor ≥ 3.12 sidebar source (§4.4)       |

`value` is always a JSON string.

### 2.1 Key namespaces in `cursorDiskKV`

| Key pattern                        | Value                                     |
| ---------------------------------- | ----------------------------------------- |
| `composerData:<composerId>`        | `ComposerData` — conversation metadata    |
| `bubbleId:<composerId>:<bubbleId>` | `ComposerConversation` — one message      |
| `checkpoint*`                      | Editor checkpoints — **skip**             |
| `messageRequestContext*`           | Request context — **skip**                |
| `codeBlockDiff*`                   | Diff payloads — **skip**                  |

The three skipped namespaces are not needed for rendering and are large enough
to matter; the loader never selects them.

### 2.2 WAL mode is mandatory, not an optimization

Both the global and workspace databases are opened with `PRAGMA
journal_mode=WAL` (`spi.EnsureWALMode`). Failure is logged and tolerated. Two
independent reasons:

- **Reads must not block Cursor's writes.** Without WAL a reader can stall the
  running IDE.
- **The watcher depends on the `-wal` file existing.** In WAL mode nearly every
  write lands in `state.vscdb-wal`, not `state.vscdb`, so a watch on the main
  database file alone sees almost nothing.

Writers (the resume path) additionally issue `PRAGMA wal_checkpoint(PASSIVE)`
after committing, so the write lands in the main database file before Cursor
next opens it. Without the checkpoint SQLite flushes lazily and Cursor can read
a pre-write snapshot.

---

## 3. Conversation schema

Go types: `pkg/providers/cursoride/types.go`.

### 3.1 `composerData:<composerId>`

```go
type ComposerData struct {
    ComposerID                  string                       `json:"composerId"`
    Name                        string                       `json:"name,omitempty"`
    Version                     int                          `json:"_v,omitempty"`   // ★ 1 vs 3+, see §3.4
    Conversation                []ComposerConversation       `json:"conversation,omitempty"`
    FullConversationHeadersOnly []ComposerConversationHeader `json:"fullConversationHeadersOnly"`
    Capabilities                []Capability                 `json:"capabilities,omitempty"`
    ModelConfig                 *ModelConfig                 `json:"modelConfig,omitempty"`
    CreatedAt                   int64                        `json:"createdAt"`       // epoch ms
    LastUpdatedAt               int64                        `json:"lastUpdatedAt,omitempty"`
    WorkspaceIdentifier         *ComposerWorkspaceIdentifier `json:"workspaceIdentifier,omitempty"` // ★ §4.3
}
```

`_v` is the composer format version and selects where tool data lives (§3.4).

`fullConversationHeadersOnly` is the ordered bubble manifest. Each header carries
`grouping.isRenderable`; **Cursor itself skips headers without it**, and so must
any parser, or the export gains bubbles the user never saw.

### 3.2 `bubbleId:<composerId>:<bubbleId>`

```go
type ComposerConversation struct {
    BubbleID       string              `json:"bubbleId"`
    Type           int                 `json:"type"`                     // 1=user, 2=assistant
    Text           string              `json:"text"`
    Thinking       *ThinkingData       `json:"thinking,omitempty"`
    CapabilityType int                 `json:"capabilityType,omitempty"` // 15 = tool
    UnifiedMode    int                 `json:"unifiedMode,omitempty"`    // 1=Ask, 2=Agent, 5=Plan
    TimingInfo     *TimingInfo         `json:"timingInfo,omitempty"`
    ToolFormerData *BubbleConversation `json:"toolFormerData,omitempty"` // V3+ tool payload
    ModelInfo      *ModelInfo          `json:"modelInfo,omitempty"`
}
```

Bubbles are stored one row per message, **not** inside the composer row. A full
conversation load is therefore one query matching `composerData:<id>` plus
`bubbleId:<id>:%`.

`TimingInfo` fields are **floats**, not ints — Cursor writes fractional
milliseconds. Decoding them as `int64` fails.

### 3.3 Enums

| Field            | Value | Meaning                     |
| ---------------- | ----- | --------------------------- |
| `type`           | `1`   | user message                |
| `type`           | `2`   | assistant message           |
| `capabilityType` | `15`  | tool invocation             |
| `unifiedMode`    | `1`   | Ask mode                    |
| `unifiedMode`    | `2`   | Agent mode                  |
| `unifiedMode`    | `5`   | Plan mode                   |

### 3.4 Tool data lives in two places — V1 vs V3+

This is the single most version-sensitive part of the format. The payload shape
is identical (`BubbleConversation`); only its location changed:

- **V1** (`_v` absent or `< 3`): tool data sits in
  `composerData.capabilities[].data.bubbleDataMap`, keyed by bubble ID. Note
  `bubbleDataMap` may itself be a **JSON-encoded string** rather than an object.
- **V3+** (`_v >= 3`): tool data is embedded directly on the bubble as
  `toolFormerData`.

`resolveToolData` (`agent_session.go`) implements the fallback: prefer
`toolFormerData` when `_v >= 3`, else look up the capability map, else return
nil and fall back to the bubble's plain `text`.

```go
type BubbleConversation struct {
    Tool           int                    `json:"tool"`
    Name           string                 `json:"name"`
    RawArgs        string                 `json:"rawArgs,omitempty"`
    Params         string                 `json:"params,omitempty"`
    Result         string                 `json:"result,omitempty"`
    Status         string                 `json:"status,omitempty"`  // "error", "cancelled", …
    Error          string                 `json:"error,omitempty"`
    AdditionalData map[string]interface{} `json:"additionalData,omitempty"`
    UserDecision   string                 `json:"userDecision,omitempty"`
}
```

`Tool == 0` or `Status == "error"` renders as a failed invocation; `Status ==
"cancelled"` renders as cancelled. Both are normal, frequent states.

---

## 4. Project / workspace mapping ★ (read carefully)

There is **no** project field on a conversation in older Cursor versions, and no
workspace-DB record in newer ones. Correct scoping needs all three sources below,
merged (`FindProjectComposerIDs`, `workspace.go`).

### 4.1 Workspace storage IDs

`<workspace-id>` is `md5(folderPath + platform-stat-salt)`, mirroring the IDE's
own `getSingleFolderWorkspaceIdentifier` (`pkg/providers/vscode/workspace_id.go`,
verified byte-for-byte against real entries):

| OS      | Stat salt                                           |
| ------- | --------------------------------------------------- |
| macOS   | folder birthtime in ms, **rounded** (not truncated)  |
| Linux   | folder inode number                                  |
| Windows | (see `workspace_id_windows.go`)                      |

**Cursor hashes the path exactly as spelled.** The same folder opened via
differently-cased paths genuinely gets distinct workspace entries — this is
verified native behavior, not a bug to normalize away. (VS Code differs: it
resolves on-disk case first. See [COPILOTIDE-FORMAT.md](../copilotide/COPILOTIDE-FORMAT.md) §4.)

### 4.2 Source 1 — workspace-DB references (Cursor 2, early Cursor 3)

Read from `workspaceStorage/<id>/state.vscdb`, `ItemTable`:

| Key                                             | Shape                                    |
| ----------------------------------------------- | ---------------------------------------- |
| `composer.composerData`                         | `{allComposers:[{composerId}], selectedComposerIds:[…]}` |
| `workbench.panel.composerChatViewPane`          | JSON whose keys include `workbench.panel.aichat.view.<composerUUID>` |
| `workbench.panel.composerChatViewPane.<paneId>` | same, one per open tab                   |

`allComposers` is the **Cursor 2** format (every conversation).
`selectedComposerIds` is the **Cursor 3** format and holds only currently-open
tabs — so on Cursor 3 this source alone under-reports badly. The
`workbench.panel.*` scan exists to recover IDs Cursor 3 dropped from
`composer.composerData`; entries ending `.hidden` are excluded.

### 4.3 Source 2 — embedded `workspaceIdentifier` (Cursor ≥ 3.12) ★

```go
type ComposerWorkspaceIdentifier struct {
    ID  string                `json:"id"`            // workspace storage directory hash
    URI *ComposerWorkspaceURI `json:"uri,omitempty"` // { fsPath }
}
```

In Cursor ≥ 3.12 **the workspace DB no longer records conversations at all**, and
the global `composer.composerHeaders` key is flushed lazily — often not until
Cursor exits. This embedded field is the only association that updates *live*, so
it is the only way to see a session that was just created.

Matching it costs a scan of every `composerData:*` row (headers only, no bubbles
— `LoadAllComposerDataLightweight`). A composer belongs to the project when its
`workspaceIdentifier.id` is one of the project's workspace IDs, or its
`uri.fsPath` canonicalizes to the project path.

### 4.4 Source 3 — global sidebar keys (write path only)

Relevant when *reconstructing* a session into Cursor, not when reading. In
`globalStorage/state.vscdb`, `ItemTable`:

| Key                                    | Role                                                |
| -------------------------------------- | --------------------------------------------------- |
| `composer.composerHeaders`             | JSON `allComposers` array — older Cursor's sidebar   |
| `glass.localAgentProjects.v1`          | Cursor ≥ 3.12 Agent-sidebar project entities         |
| `glass.localAgentProjectMembership.v1` | Cursor ≥ 3.12 map composerID → projectID             |

Plus a dedicated `composerHeaders` **SQL table** (created by Cursor itself) which
Cursor ≥ 3.12 reads *instead of* the JSON key, gated on
`composer.composerHeaders.tableGateEnabled`. A reconstructed session that is not
registered in both the glass membership map and the `composerHeaders` table stays
invisible in newer Cursors even though its data is present. Older Cursors ignore
the glass keys, so writing them is always safe.

**Caveat:** a running Cursor can flush its own in-memory copy of any `ItemTable`
key over an external write. The resume flow accounts for this; ad-hoc writes
should not be attempted while Cursor is open.

### 4.5 Workspace matching methods

Matching is delegated to the shared VS Code-lineage engine
(`pkg/providers/vscode/workspace.go`, `FindWorkspaces`), which tries four methods:

1. **Direct canonical path equality** — the folder was opened directly.
2. **Folder-basename equality — for SSH remote / tunnel / dev-container entries
   only.** Those paths live on another machine or inside a container, so direct
   comparison can never succeed and the repository name is the only usable
   signal. Local workspaces are deliberately **excluded** from basename matching:
   two unrelated `backend` folders would otherwise export each other's sessions.
3. The entry is a `.code-workspace` file listing the project as a folder.
4. The project is itself a `.code-workspace` file and the entry is one of its folders.

`SelectPrimary` breaks ties: byte-for-byte canonical path match wins, then
most-recently-used.

WSL can produce several workspace entries for one project (different URI forms);
all are matched and their composer IDs deduplicated.

### 4.6 Workspace URI forms

`workspace.json` holds `folder` (single folder) or `workspace` (multi-root file).
Its value is a URI in one of these shapes (`vscode.URIToPath`):

| Shape                                             | Notes                                              |
| ------------------------------------------------- | -------------------------------------------------- |
| `file:///Users/me/proj`                           | plain local                                         |
| `file:///c%3A/Users/me/proj`                      | Windows drive letter, percent-encoded               |
| `file://wsl.localhost/Ubuntu/home/me/proj`         | host is `wsl.localhost`; **first path segment is the distro** and must be stripped |
| `vscode-remote://wsl%2Bubuntu/home/me/proj`        | `%2B` in the **host** — Go's `url.Parse` rejects this, so it is parsed manually |
| `vscode-remote://ssh-remote%2B<hex-json>/path`     | host carries hex-encoded `{"hostName":…}`; path is the remote path |
| `vscode-remote://tunnel%2B<host>/path`             | remote path                                         |
| `vscode-remote://dev-container%2B<hex>/path`       | container-internal path                             |

`spi.ParseVSCodeRemoteURI` accepts hosts `wsl`, `ssh-remote`, `tunnel`,
`dev-container`, bare or with a `+config` suffix, and rejects anything else.

On Windows, paths needing care (`vscode.NormalizePathForComparison`):

- `\\wsl.localhost\Ubuntu\…` and `\\wsl$\Ubuntu\…` normalize to `/home/…`.
- A Unix-shaped path on Windows (`/home/user/proj` or `\home\user\proj`, no
  volume name) must **not** go through `filepath.Abs`, which would prepend the
  current drive and corrupt it.

---

## 5. Session identity & timing

- **Session ID = `composerId`**, used verbatim. No translation.
- `createdAt` / `lastUpdatedAt` are epoch **milliseconds** on the composer row.
- Per-bubble timing comes from `timingInfo` (floats, §3.2).
- Session name: `ComposerData.name` when set, else a slug derived from the first
  user message.

---

## 6. Tool catalog

Handlers are registered by tool name (`tool_handlers.go`); unregistered names
fall through to a generic formatter, so an unknown tool degrades rather than
disappearing.

| Tool names                                                                              | Type      |
| --------------------------------------------------------------------------------------- | --------- |
| `read_file`, `read_file_v2`                                                              | `read`    |
| `edit_file`, `edit_file_v2`, `MultiEdit`, `edit_notebook`, `reapply`, `search_replace`, `write` | `write` |
| `delete_file`                                                                            | `write`   |
| `apply_patch`                                                                            | `write`   |
| `run_terminal_cmd`, `run_terminal_command`, `run_terminal_command_v2`                     | `shell`   |
| `grep`, `ripgrep`                                                                        | `search`  |
| `grep_search`                                                                            | `search`  |
| `file_search`, `glob_file_search`                                                        | `search`  |
| `list_directory`                                                                         | `generic` |

The provider's internal `ToolType` set includes `mcp`, which has no
`schema.ToolType` equivalent and maps to `generic` on the way out
(`toSchemaToolType`).

---

## 7. Watching

- Watches the **parent directory** of each database, never the files. In WAL mode
  SQLite deletes `state.vscdb-wal` when the last connection closes and recreates
  it on next open; a file watch is never added when the file doesn't exist yet
  and dies with the inode on checkpoint/restart, silently degrading everything to
  the safety-net poll. A directory watch survives both.
- Filters to `state.vscdb` and its `-wal`/`-shm` siblings, ignoring unrelated
  neighbours.
- A **safety-net poller** (default 2 minutes) catches anything fsnotify missed.
- On start, `seedKnownComposers` records every existing composer's
  `lastUpdatedAt` **without firing callbacks**, so startup does not re-emit the
  whole back catalogue. Seeding failure is non-fatal; the worst case is one
  redundant emit.

---

## 8. Edge cases

| Case                                       | Behavior                                                            |
| ------------------------------------------ | ------------------------------------------------------------------- |
| Global DB missing                          | Provider idle; error names `--user-data-dir cursoride:<path>`        |
| Workspace storage missing                  | Same                                                                 |
| Project never opened in Cursor             | Read path finds nothing; write path **mints** a workspace entry reproducing Cursor's own ID, so Cursor adopts it rather than creating a duplicate |
| Composer with no renderable bubbles        | Filtered out                                                         |
| Malformed JSON in a row                    | Warn and skip that row; the sweep continues                          |
| Same project in WSL + local + SSH          | All matching workspaces merged, composer IDs deduplicated            |
| Composer in several workspaces             | First-seen workspace path wins as `OriginCwd`                        |
| Large workspaces                           | Composer loads are chunked (`composerBatchSize = 200`)               |

---

## 9. Why `cursorcli` and `cursoride` are separate providers

|                  | `cursorcli` (Cursor CLI)                       | `cursoride` (Cursor IDE)                          |
| ---------------- | ---------------------------------------------- | ------------------------------------------------- |
| Product          | `cursor-agent` command                         | Cursor IDE (VS Code fork)                          |
| Storage          | `~/.cursor/chats/<md5>/<session-id>/store.db`   | one global `state.vscdb` + workspaceStorage        |
| Granularity      | one SQLite DB per session                      | one DB for every session on the machine            |
| Tables           | `blobs`, `meta`                                | `cursorDiskKV`, `ItemTable`, `composerHeaders`     |
| Project scoping  | md5 of canonical project path → directory      | workspace matching + embedded `workspaceIdentifier` |
| Message storage  | blob records, DAG structure                    | separate `bubbleId:*` rows                         |
| Tool data        | embedded in blob                               | `toolFormerData` / `capabilities.bubbleDataMap`    |
| Execution        | can launch `cursor-agent`                      | cannot — opens the IDE and watches                 |

A user may use both on the same project, so both providers are needed.

---

## 10. Open questions / risks

- **Glass-key stability.** `glass.localAgentProjects.v1` /
  `…Membership.v1` are versioned key names; a Cursor release bumping them would
  silently make reconstructed sessions invisible again. There is no detection for
  this beyond noticing the sidebar is empty.
- **`_v` values between 1 and 3.** Only `_v >= 3` and "everything else" are
  distinguished. No `_v == 2` corpus has been observed; if one exists its tool
  location is unverified.
- **SSH basename matching is a heuristic.** Two different repositories with the
  same directory name on local and remote would cross-match. Accepted: in
  practice the repository name is the only signal available.
- **Concurrent writes.** Any `ItemTable` write made while Cursor runs can be
  overwritten by Cursor's own flush on exit.
