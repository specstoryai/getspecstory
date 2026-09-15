# VS Code Copilot — On-Disk Session Storage Format Spec

Authoritative specification of how **GitHub Copilot Chat inside a VS Code
distribution** stores sessions on disk. Intended for an engineer maintaining or
extending the `copilotide` provider. Every claim below is backed by the shipped
parser in `pkg/providers/copilotide/` and by real captured data.

- Apps: stock VS Code, VS Code Insiders, VSCodium, VSCodium Insiders (§1.1).
- Storage: plain JSON / JSONL files per workspace. **No SQLite for the
  conversations** — SQLite appears only for workspace identification and for the
  session index on the write path.

The conversation body is a polymorphic `response` array discriminated by a
`kind` field (§3.3); the tool metadata sits in a *parallel* structure keyed by
IDs that do not match (§3.5). Those two facts drive most of the parser.

---

## 1. Directory layout

```
<user-data-dir>/User/workspaceStorage/<workspace-id>/
├── workspace.json                      # { "folder": "<uri>" } or { "workspace": "<uri>" }
├── state.vscdb                         # ItemTable — chat session index (write path, §5)
├── chatSessions/
│   ├── <sessionId>.jsonl               # ★ current format (incremental, §2.2)
│   └── <sessionId>.json                # older format (single object)
└── chatEditingSessions/
    └── <sessionId>/
        └── state.json                  # optional edit history (§4)
```

A workspace with no `chatSessions` directory has simply never had a Copilot
chat. That is a normal state, reported as zero sessions rather than an error.

### 1.1 Distributions are separate providers

Each VS Code distribution is registered as its own provider
(`pkg/providers/copilotide/provider.go`), because each has its own data
directory and its own launcher:

| Provider ID                    | App               | Data dir name        | Launcher           |
| ------------------------------ | ----------------- | -------------------- | ------------------ |
| `copilotide`                   | VS Code           | `Code`               | `code`             |
| `copilotide-insiders`          | VS Code Insiders  | `Code - Insiders`    | `code-insiders`    |
| `copilotide-vscodium`          | VSCodium          | `VSCodium`           | `codium`           |
| `copilotide-vscodium-insiders` | VSCodium Insiders | `VSCodium - Insiders`| `codium-insiders`  |

VSCodium runs Copilot via a sideloaded VSIX (the extension is not on Open VSX),
but the chat session store is VS Code OSS core code, so the layout is identical.

Alternative distributions are registered **only when they have chat sessions**
(`HasAnyChatSessions`): merely launching the app creates workspace storage, so
the presence of a `chatSessions` directory is the signal that the distribution
is actually in use.

`<user-data-dir>` resolution, in precedence order (`path_utils.go`):

1. `--user-data-dir <provider-id>:<path>` (the path is the parent of `User`).
2. Under WSL only: the Windows-side install via `/mnt/c/Users/*/AppData/Roaming/<data-dir-name>`.
3. OS default: `~/Library/Application Support/<data-dir-name>` (macOS),
   `~/.config/<data-dir-name>` (Linux), `%APPDATA%\<data-dir-name>` (Windows).

Only the first two are existence-checked. The OS default is returned
unconditionally because it is also the path a not-yet-created storage directory
would take — workspace minting needs to target it before the app has made it.

---

## 2. Session file formats

Two on-disk formats coexist. The extension decides; a parser must handle both,
and `LoadSessionByID` prefers `.jsonl` then falls back to `.json`.

### 2.1 `.json` — single object

The whole `VSCodeComposer` (§3.1) as one JSON object.

### 2.2 `.jsonl` — snapshot plus incremental updates ★

Each line is an envelope `{"kind": N, …}`:

| `kind` | Fields      | Meaning                                                      |
| ------ | ----------- | ------------------------------------------------------------ |
| `0`    | `v`         | **Initial snapshot** of the whole composer. Must be line 1.  |
| `1`    | `k`, `v`    | **Replace** the value at key path `k` with `v`.               |
| `2`    | `k`, `v`    | **Append** `v` to the array at key path `k`.                  |

`k` is a key path mixing string map keys and numeric array indices, e.g.
`["requests", 3, "response"]`. JSON numbers decode as `float64`, so index
segments need conversion.

`kind:2` was introduced to split large payloads out of the initial snapshot. It
is critical that it **appends rather than replaces**: VS Code writes one `kind:2`
per user turn carrying only that turn's new request(s), *not* the full history.
Treating it as a replacement collapses a long session down to its last turn.

Unknown kinds are logged and skipped, so a future format addition degrades
rather than failing.

Implementation note: updates are applied to the session held as
`map[string]any` and decoded into the typed struct **once** at the end.
Round-tripping the whole composer per update line is O(session size) per line and
dominates load time on long sessions.

---

## 3. Conversation schema

Go types: `pkg/providers/copilotide/types.go`.

### 3.1 Top level

```go
type VSCodeComposer struct {
    Host              string               `json:"host"`              // always "vscode"
    SessionID         string               `json:"sessionId"`
    Name              string               `json:"name,omitempty"`
    CustomTitle       string               `json:"customTitle,omitempty"`
    Version           int                  `json:"version"`
    RequesterUsername string               `json:"requesterUsername"`
    ResponderUsername string               `json:"responderUsername"`
    CreationDate      int64                `json:"creationDate"`      // epoch ms
    LastMessageDate   int64                `json:"lastMessageDate"`   // epoch ms
    InitialLocation   string               `json:"initialLocation"`
    IsImported        bool                 `json:"isImported"`
    Requests          []VSCodeRequestBlock `json:"requests"`
}
```

### 3.2 A request block — one conversational turn

```go
type VSCodeRequestBlock struct {
    RequestID string            `json:"requestId"`
    Timestamp int64             `json:"timestamp"`
    Message   VSCodeMessage     `json:"message"`   // the user's prompt
    Response  []json.RawMessage `json:"response"`  // ★ polymorphic, see §3.3
    Result    VSCodeResult      `json:"result"`    // ★ tool metadata, see §3.4
    ModelID   string            `json:"modelId,omitempty"`
}
```

One request = one user prompt plus everything the agent emitted in reply.

### 3.3 The `response` array — kinds

`response` is an ordered, heterogeneous list. Each element is discriminated by
its `kind` field; **a missing `kind` means a plain markdown text fragment**
(`{"value": "…"}`), which is the normal prose body.

| `kind`                     | Handling                                                        |
| -------------------------- | --------------------------------------------------------------- |
| *(absent)*                 | Markdown text fragment. Rendered.                                |
| `toolInvocationSerialized` | A tool call. Rendered. Deduplicated — see below.                 |
| `textEditGroup`            | Applied edits: replacement text + ranges. Rendered as a collapsible edit block. |
| `notebookEditGroup`        | Same, for notebooks.                                             |
| `inlineReference`          | A file/symbol chip. Rendered, and **kept even when it renders no text** — it splits a sentence, so dropping it inserts a spurious paragraph break between its neighbours. |
| `confirmation`             | A prompt the user answered. Rendered as `> ❓ …`.                 |
| `warning`                  | User-visible warning. Rendered as `> ⚠️ …` — these explain otherwise-dead turns. |
| `codeblockUri`             | Labels the file a following code block belongs to. Not rendered; the content arrives as the adjacent `textEditGroup`. |
| `thinking`                 | Opaque blob. Not rendered.                                       |
| `undoStop`                 | Undo marker. Not rendered.                                       |
| `mcpServersStarting`       | MCP startup notice. Not rendered.                                |
| `autoModeResolution`       | Auto-model-selection metadata. Not rendered (the turn's model is read separately). |
| `progressMessage`          | Transient spinner text. Not rendered.                            |
| `command`                  | UI command button. Not rendered.                                 |

Two behaviours worth knowing:

- **Tool invocations are appended repeatedly.** VS Code writes a fresh
  serialization each time an invocation's state changes (running → completed).
  The same `toolCallId` therefore appears several times. Keep **one** item, at
  the *first* occurrence's position (that is where it ran chronologically),
  carrying the *last* serialization's data (that one has the final
  `resultDetails`).
- **Bare-fence text fragments are dropped.** VS Code emits lone ```` ``` ````
  fragments as delimiters around structured code-block items (`codeblockUri` /
  `textEditGroup`) that are not rendered as text. Keeping them leaves empty code
  blocks in the output.

### 3.4 `result.metadata` — the parallel tool record

```go
type VSCodeResultMetadata struct {
    ToolCallRounds  []VSCodeToolCallRound           `json:"toolCallRounds,omitempty"`
    ToolCallResults map[string]VSCodeToolCallResult `json:"toolCallResults,omitempty"`
    Messages        []VSCodeMetadataMessage         `json:"messages,omitempty"`
}

type VSCodeToolCallRound struct {
    Response  string               `json:"response"`  // narration, may look like thinking
    ToolCalls []VSCodeToolCallInfo `json:"toolCalls"`
}

type VSCodeToolCallInfo struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON encoded as a string
}
```

### 3.5 The ID mismatch ★

The two structures use **different ID spaces for the same tool call**:

| Structure                                | ID form                                               |
| ---------------------------------------- | ----------------------------------------------------- |
| `result.metadata.toolCallRounds[].toolCalls[].id` | OpenAI style: `call_mlWUQhVaoFaj26CRtR7sqD1j__vscode-1763048081513` |
| `response[].toolCallId` (`toolInvocationSerialized`) | VS Code GUID: `52142529-aec6-4fb4-94ac-ca0deb467986` |

They cannot be joined by ID. The parser instead builds an **ordered sequence**
from the rounds (`BuildToolCallSequence` flattens every round's calls in order)
and matches it positionally against the deduplicated invocations in the response
array.

When an invocation has no matching metadata call — the whole turn when it was
cancelled (VS Code then stores empty metadata), or the tail of a turn with more
invocations than recorded calls — it is still rendered from the invocation
alone: `resultDetails.input` carries the arguments as a JSON string and
`resultDetails` carries the output embeds. The tool name then comes from
`toolId`, defaulting to `"unknown"`.

### 3.6 Narration vs thinking

`toolCallRounds[].response` is the **visible streamed narration**, and VS Code
duplicates it verbatim into the `response` array. Emitting it as a thinking block
without filtering shows every paragraph twice.

The filter compares against the turn's final rendered text **ignoring all
whitespace**: the same narration is stored with different spacing in the two
places (rounds fuse streamed sentences with no separator; response fragments
carry their own newlines), so a plain substring check misses the duplicates.

---

## 4. `chatEditingSessions/<sessionId>/state.json` (optional)

Records the file operations Copilot applied. Two schema versions:

```go
type VSCodeStateFile struct {
    Version         int                   `json:"version"`
    SessionID       string                `json:"sessionId"`
    LinearHistory   []VSCodeLinearHistory `json:"linearHistory,omitempty"`   // v1
    RecentSnapshot  any                   `json:"recentSnapshot,omitempty"`  // array or object
    PendingSnapshot any                   `json:"pendingSnapshot,omitempty"` // array or object
    Timeline        *VSCodeTimeline       `json:"timeline,omitempty"`        // v2
}

type VSCodeOperation struct {
    Type  string     `json:"type"`  // "create" | "textEdit" | "delete"
    URI   *VSCodeUri `json:"uri,omitempty"`
    Edits []any      `json:"edits,omitempty"`
}
```

Entirely optional — a missing, unreadable, or unparseable state file is logged
and treated as absent, never as an error.

Its one load-bearing use: when a session has **no chat requests but does have
file operations**, synthetic request blocks are generated from the operations so
the work is not lost (`createSyntheticRequestsFromEditingState`).

---

## 5. Project / workspace mapping

Workspace identification is shared with Cursor via the VS Code-lineage engine in
`pkg/providers/vscode/`. See [CURSORIDE-FORMAT.md §4.5–4.6](../cursoride/CURSORIDE-FORMAT.md)
for the four match methods, the remote URI shapes (`wsl.localhost`,
`vscode-remote://wsl%2B…`, `ssh-remote`, `tunnel`, `dev-container`) and the
Windows path-normalization rules — they are identical here.

Two differences specific to VS Code:

- **Case handling when minting.** VS Code resolves the on-disk case when it
  opens a folder, so its callers pass the **canonical** path to
  `vscode.WorkspaceID`. (Cursor hashes the literal spelling instead — see
  [CURSORIDE-FORMAT.md](../cursoride/CURSORIDE-FORMAT.md) §4.1.)
- **`MatchOptions.RequireFile`.** Session *reads* require `chatSessions`, so
  workspaces that never had a chat are skipped. *Write* targets require nothing,
  because the directory is created on first write.

### 5.1 The chat session index — required for reconstruction

Writing a session file is not enough for VS Code to show it. The session must
also be registered in the workspace `state.vscdb` `ItemTable` under
`chat.ChatSessionStore.index`, keyed by session ID:

```go
type sessionIndexEntry struct {
    SessionID         string             `json:"sessionId"`
    Title             string             `json:"title"`
    LastMessageDate   int64              `json:"lastMessageDate"`
    Timing            sessionIndexTiming `json:"timing"`
    InitialLocation   string             `json:"initialLocation"`
    HasPendingEdits   bool               `json:"hasPendingEdits"`
    IsEmpty           bool               `json:"isEmpty"`
    IsExternal        bool               `json:"isExternal"`
    LastResponseState int                `json:"lastResponseState"`
    PermissionLevel   string             `json:"permissionLevel"`
}
```

`hasPendingEdits` / `isEmpty` / `isExternal` are marshalled **explicitly, not
`omitempty`** — VS Code stores them as literal `false` on real entries.

**The app must be fully quit, not reloaded.** VS Code holds the index in memory
in its main process and flushes it over any external write on shutdown. A
"Developer: Reload Window" keeps that process alive, so a reload is *not* enough
— verified empirically. Detection is per variant (a running stock VS Code does
not block an Insiders-targeted write), via the macOS app-bundle path, the Linux
launcher binary name matched as a whole path segment (so `code` cannot match
`codex`), or Windows `tasklist` filtered on `<DataDirName>.exe`. On Windows the
hit test is the image name echoing back in the output: `tasklist` exits 0 and
prints a localized "no tasks" message even when nothing matched, so neither the
exit code nor the message text can be trusted.

When stdin is not a terminal (scripted resume), prompting is impossible — the
provider warns once and proceeds; the session file still lands, only its panel
registration is at risk.

---

## 6. Tool catalog

Tool type is derived from the tool name (`MapToolType`). Any name starting
`mcp_` maps to `generic` (the schema has no MCP type). Unknown names log at debug
and fall back to `generic`.

| Tool name                                    | Type      |
| -------------------------------------------- | --------- |
| `read_file`                                  | `read`    |
| `apply_patch`, `insert_edit_into_file`, `create_file` | `write` |
| `grep_search`, `file_search`, `semantic_search` | `search`  |
| `manage_todo_list`                           | `task`    |
| `list_dir`, `get_errors`                     | `generic` |
| `mcp_*`                                      | `generic` |

Legacy names still mapped for older sessions: `bash` (`shell`), `search_files`,
`list_files`, `grep`, `find` (`search`), `write_to_file`, `str_replace_editor`
(`write`).

---

## 7. Watching

- Watches the workspace's `chatSessions` **directory**.
- When that directory does not exist yet, watches the workspace directory until
  it is created, then removes that watch so the `chatSessions` watch is the only
  one left on the shared watcher.
- Change detection is a `fileSignature` of size + mtime (held as `UnixNano` so
  struct equality is a plain value comparison); any real write moves one or both.
- At startup, existing files are stat-ed into `knownFiles` and **no callbacks are
  fired** — the back catalogue is not re-emitted.
- Callbacks are delivered with panic isolation so a failure downstream (markdown
  write, cloud sync) cannot crash the watcher.

---

## 8. Edge cases

| Case                                         | Behavior                                                    |
| -------------------------------------------- | ----------------------------------------------------------- |
| No `chatSessions` directory                  | Zero sessions, not an error                                  |
| Both `<id>.json` and `<id>.jsonl` exist       | `.jsonl` wins                                                |
| `.jsonl` first line is not `kind:0`           | Parse fails with an explicit error                           |
| Unknown `kind` on an update line              | Warn, skip the line, continue                                |
| Unknown `kind` in the `response` array        | Debug log, element ignored                                   |
| Missing / corrupt `state.json`                | Treated as absent                                            |
| Turn cancelled (empty `result.metadata`)      | Tools rendered from the invocation's `resultDetails` alone    |
| Session with no requests but with edit ops    | Synthetic request blocks generated from the operations       |
| Project never opened in this distribution     | Write path mints a workspace entry; read path finds nothing  |

---

## 9. Open questions / risks

- **Positional tool matching is order-dependent.** If VS Code ever emits
  invocations in a different order from `toolCallRounds`, tool names and
  arguments silently pair with the wrong invocation. There is no checksum to
  detect this.
- **`chat.ChatSessionStore.index` is an internal key.** A VS Code release
  renaming it, or moving the index out of `state.vscdb`, breaks reconstruction
  with no visible error — the session file lands and simply never appears.
- **`presentation: "hidden"`** exists on invocations but its exact semantics
  across VS Code versions are not fully characterized.
- **Todo statuses are hyphenated here and rendered differently from every other
  provider.** `manage_todo_list` items carry `status` values `completed` and
  **`in-progress`** — a hyphen, where every other agent uses `in_progress`. The
  renderer maps `completed` → `- [x]`, `in-progress` → `- [ ] …  _(in progress)_`
  (an *unchecked* box plus an italic suffix), default → `- [ ]`. Other providers
  render in-progress as `- [⚡]` via `spi.TodoSymbol`, which matches on the
  underscore form and would classify `in-progress` as unknown. Folding Copilot
  into the shared helper therefore needs the hyphen alias added first.
