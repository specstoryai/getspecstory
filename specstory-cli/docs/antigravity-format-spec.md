# Antigravity CLI — On-Disk Session Storage Format Spec

Authoritative, reverse-engineered specification of how Google's **Antigravity
agentic coding CLI** (`agy` binary, version **1.0.2**) stores sessions on disk.
Intended for an engineer writing a Go parser. Every claim below is backed by
real captured data observed on macOS (Darwin 24.6.0).

- App: `agy` on `PATH` (for example, `~/.local/bin/agy`)
- Version observed: `1.0.2` (via `agy --version`)
- App data root: `~/.gemini/antigravity-cli/`
- Capture date: 2026-05-26
- Scenarios used for this spec (captured on disk at the time of analysis):
  - interactive TUI session, 11 steps, all `run_command`
  - `agy -p "say hello"`, 3 steps, no tools (text-only)
  - `agy -p` read+search without `--add-dir`, 80 steps, `run_command`/`list_dir`/`list_permissions`, has `RUNNING` + `SYSTEM_MESSAGE` + `ERROR`
  - `agy -p --add-dir`, file tools `view_file`/`write_to_file`/`grep_search`/`list_dir`
  - `agy -p --add-dir` edit + failing command, then `agy -c` continuation (multi-turn, append-only)

---

## 1. Directory layout

```
~/.gemini/                                          # shared with Gemini CLI — DO NOT confuse
├── oauth_creds.json                                # OAuth creds (access_token, refresh_token, id_token, expiry_date, scope, token_type). Antigravity auth works off these even when CLI logs "not logged into Antigravity".
├── config/
│   └── projects/<projectId>.json                   # projectId(UUID) -> workspace path map  ★ project mapping
└── antigravity-cli/                                # ★ all Antigravity data lives here
    ├── history.jsonl                               # interactive-TUI prompt log (NOT print-mode) ★
    ├── installation_id                             # UUID string, no newline
    ├── settings.json                               # {enableTelemetry, model, trustedWorkspaces[]}
    ├── keybindings.json
    ├── last_check.timestamp                        # often empty
    ├── cache/onboarding.json
    ├── bin/{agentapi,webm_encoder}                 # helper binaries (agentapi is a 2-line sh shim)
    ├── updater/update.lock
    ├── knowledge/knowledge.lock
    ├── cli.log -> log/cli-YYYYMMDD_HHMMSS.log      # symlink to latest log
    ├── log/cli-YYYYMMDD_HHMMSS.log                 # klog-format server logs
    ├── conversations/<conversationId>.pb           # ENCRYPTED/high-entropy — NOT protobuf. IGNORE.
    ├── implicit/<uuid>.pb                           # ENCRYPTED/high-entropy. Different UUID namespace (NOT conversationId). IGNORE.
    ├── scratch/                                    # default workspace when no --add-dir given (see §5)
    └── brain/<conversationId>/                      # ★ one dir per conversation
        └── .system_generated/
            ├── logs/
            │   ├── transcript_full.jsonl           # ★ PRIMARY SOURCE — clean native-JSON tool args
            │   └── transcript.jsonl                # same steps, but tool-arg VALUES double-escaped (avoid)
            ├── messages/                            # internal system-message inbox (optional)
            │   ├── <messageId>.json                # one system message
            │   ├── read.json                       # {"<messageId>": true}
            │   └── cursor.json                     # {"last_read_unix_nano": <int>}
            └── tasks/                               # optional
                └── task-<N>.log                    # captured stdout of async (RUNNING) command N
```

### Encryption confirmation
`conversations/*.pb` and `implicit/*.pb` are **encrypted, not parseable**. The
146 KB `conversations/<conversationId>.pb` has a gzip compression ratio of **1.00**
(incompressible) and `strings` yields only random 4-char fragments. `implicit`
files (200–600 B) gzip to >100% of original. **Both should be skipped entirely.**
The `.pb` extension is misleading — do not attempt protobuf decode.

---

## 2. Session identity & timing

- **conversationId** is a UUIDv4. It names the `brain/<conversationId>/` dir AND
  the `conversations/<conversationId>.pb` file. This is the canonical session ID.
- There is **no top-level `conversationId` field inside the transcript files.** The
  ID is only the directory name. (It does appear embedded in the encrypted env
  var `ANTIGRAVITY_SOURCE_METADATA` that the CLI injects into child processes —
  see §3.4 — but not as a transcript field.)
- **createdAt** for a session = `created_at` of step_index 0 (the first
  `USER_INPUT`). There is no separate session-metadata file.
- **Per-step `created_at`** is RFC3339 **UTC** with `Z` suffix and **second**
  precision: `"2026-05-26T22:00:46Z"`. (The `messages/<id>.json` timestamps carry
  microsecond precision: `"2026-05-26T22:01:35.545540Z"`.)
- The user's local timezone is recoverable from the `<ADDITIONAL_METADATA>` block
  inside USER_INPUT content (e.g. `2026-05-26T18:00:46-04:00`).

---

## 3. Transcript schema (`transcript_full.jsonl`)

One JSON object per line, ordered by `step_index`. **`transcript_full.jsonl` is
the file to parse.** Top-level keys observed (union across all sessions):

| key | type | present on | notes |
|---|---|---|---|
| `step_index` | int | every step | monotonically increasing; has exactly ONE gap (see §4) |
| `source` | string | every step | enum, see §3.1 |
| `type` | string | every step | enum, see §3.2 |
| `status` | string | every step | enum, see §3.3 |
| `created_at` | string | every step | RFC3339 UTC `Z`, second precision |
| `content` | string | most steps | absent on bare tool-call steps that only carry `tool_calls` and on `CONVERSATION_HISTORY` |
| `tool_calls` | array | only on PLANNER_RESPONSE steps that invoke a tool | see §3.5 |
| `thinking` | string | some PLANNER_RESPONSE steps | model's private reasoning; may be absent |

`transcript.jsonl` has the **same line count and same step structure**, but every
tool-call **arg value** is individually JSON-stringified (see §3.6). Prefer
`transcript_full.jsonl`; fall back to `transcript.jsonl` only if missing, and
then unwrap each arg value with a second JSON-decode.

### 3.1 `source` enum (all observed values)
| value | meaning |
|---|---|
| `USER_EXPLICIT` | a real user message (the prompt) |
| `SYSTEM` | system-generated context/notice steps |
| `MODEL` | anything emitted by the model: its reasoning, tool calls, and tool results |

Note: tool *results* carry `source: "MODEL"` (not SYSTEM), because they are
folded back into the model's turn.

### 3.2 `type` enum (all observed values)
| type | source | role | `content` shape |
|---|---|---|---|
| `USER_INPUT` | USER_EXPLICIT | the user prompt | wrapped XML-ish blocks, see §3.7 |
| `CONVERSATION_HISTORY` | SYSTEM | turn-start context marker | **no `content` field** (empty placeholder) |
| `SYSTEM_MESSAGE` | SYSTEM | injected system notice (e.g. server restart, subagent stop) | `<SYSTEM_MESSAGE>...</SYSTEM_MESSAGE>` wrapper text, see §3.8 |
| `PLANNER_RESPONSE` | MODEL | model turn: text and/or `tool_calls` (+optional `thinking`) | the model's prose reply (may be empty when it's only a tool call) |
| `RUN_COMMAND` | MODEL | result of a `run_command` tool call | command output block, see §3.9 |
| `VIEW_FILE` | MODEL | result of a `view_file` tool call | line-numbered file dump, see §6 |
| `CODE_ACTION` | MODEL | result of `write_to_file` OR `replace_file_content` | creation note or unified diff, see §6 |
| `GREP_SEARCH` | MODEL | result of a `grep_search` tool call | JSON-lines of matches, see §6 |
| `LIST_DIRECTORY` | MODEL | result of a `list_dir` tool call | JSON-lines of entries + summary, see §6 |
| `GENERIC` | MODEL | result of tools without a dedicated type (e.g. `list_permissions`) | freeform text |

The result step's `type` is derived from the tool category, NOT a single generic
"tool result" type. A `run_command` call produces a `RUN_COMMAND` result; a
`view_file` call produces a `VIEW_FILE` result; etc. Tools lacking a dedicated
type produce `GENERIC`.

### 3.3 `status` enum (all observed values)
| value | meaning |
|---|---|
| `DONE` | step complete (the overwhelming majority) |
| `RUNNING` | an async / long-running command not yet finished at capture time (seen on `RUN_COMMAND` result steps where `WaitMsBeforeAsync` elapsed; its output later lands in `tasks/task-<N>.log`) |
| `ERROR` | step failed at the tool-execution layer (seen on `LIST_DIRECTORY` for a non-existent dir; content = `Encountered error in step execution: directory ... does not exist`) |

**IMPORTANT — failed shell commands are NOT `ERROR`.** A `run_command` whose
process exits non-zero still has `status: "DONE"`; the failure is signalled only
in the `content` text: `"The command failed with exit code: 1"`. So to detect a
failed shell command, parse the content, not the status. `ERROR` status is
reserved for tool-framework-level failures (e.g. directory not found).

### 3.4 Tool calls — `tool_calls` array
Present only on `PLANNER_RESPONSE` steps that invoke a tool. Each element:
```json
{"name":"<tool_name>","args":{ ... , "toolAction":"...","toolSummary":"..."}}
```
Every tool's args include human-readable `toolAction` and `toolSummary` strings.
Only **one tool call per PLANNER_RESPONSE** was observed across all sessions
(the planner emits one tool, gets its result in the next step, then plans
again). No parallel/multi-tool steps were seen, but parser should tolerate an
array of length >1.

The corresponding **result** appears as the **immediately following step**
(`step_index + 1`, modulo the single gap at index 4 — see §4). There is no
explicit call→result ID field in the transcript; correlation is purely
positional (call at step N → result at next step). An internal tool-call `id`
(e.g. `"8bo3srsr"`) exists but is only visible inside the injected
`ANTIGRAVITY_SOURCE_METADATA` env var, not in transcript fields.

### 3.5 Tool catalog (real args observed)

| tool `name` | exact `args` keys (excl. `toolAction`/`toolSummary`) | result step `type` | result content shape | SpecStory tool type |
|---|---|---|---|---|
| `run_command` | `CommandLine` (str), `Cwd` (str, abs path), `WaitMsBeforeAsync` (int, e.g. 5000) | `RUN_COMMAND` | `Created At:`/`Completed At:` header + "The command completed successfully." or "The command failed with exit code: N" + `Output:`/`Stdout:`/`Stderr:` | **shell** |
| `view_file` | `AbsolutePath` (str) | `VIEW_FILE` | `File Path:` + `Total Lines`/`Total Bytes` + line-numbered body (`N: <line>`) | **read** |
| `write_to_file` | `CodeContent` (str, full file body), `Description` (str), `IsArtifact` (bool), `Overwrite` (bool), `TargetFile` (str abs path) | `CODE_ACTION` | `Created file file://... with requested content.` | **write** |
| `replace_file_content` | `TargetFile` (str), `TargetContent` (str, old text), `ReplacementContent` (str, new text), `StartLine` (int), `EndLine` (int), `Instruction` (str), `Description` (str), `AllowMultiple` (bool) | `CODE_ACTION` | `The following changes were made by the replace_file_content tool to: <path>` + unified diff between `[diff_block_start]`/`[diff_block_end]` markers | **write** (edit) |
| `grep_search` | `Query` (str), `SearchPath` (str), `MatchPerLine` (bool) | `GREP_SEARCH` | JSON-lines, one per match: `{"File":...,"LineNumber":N,"LineContent":...}` | **search** |
| `list_dir` | `DirectoryPath` (str) | `LIST_DIRECTORY` | JSON-lines per entry `{"name":...,"isDir":true}` or `{"name":...,"sizeBytes":"NN"}`, then `Summary: This directory contains X subdirectories and Y files.` (or `Empty directory`) | **read** |
| `list_permissions` | *(none beyond toolAction/toolSummary)* | `GENERIC` | bulleted list of permission grants | **generic** |

Tools NOT exercised in capture but likely to exist (the planner referred to its
"search tool"/"file viewer" by capability, and the CLI has a sandbox/browser
subsystem): `codebase_search` (semantic search), browser/URL tools
(`execute_url`/`read_url` appear in `list_permissions` output), MCP tools
(`mcp(*)` permission), subagent/task tools. A parser should treat any unknown
tool `name` as **generic** and map by `type` of the following result step.

### 3.6 `transcript.jsonl` double-escaping (the difference)
Same step at step_index 7 (`write_to_file`), both files:

`transcript_full.jsonl` (native — USE THIS):
```json
{"CodeContent":"hi there\n","IsArtifact":false,"Overwrite":true,"TargetFile":"/tmp/.../greeting.txt", ...}
```
`transcript.jsonl` (each value re-stringified — booleans/ints become strings):
```json
{"CodeContent":"\"hi there\\n\"","IsArtifact":"false","Overwrite":"true","TargetFile":"\"/tmp/.../greeting.txt\"", ...}
```
Rule for `transcript.jsonl`: after parsing the line, each `args` value is itself
a JSON-encoded scalar — decode a second time (string→string, `"false"`→bool,
`"5000"`→int).

### 3.7 USER_INPUT content structure
```
<USER_REQUEST>
<the real prompt text>
</USER_REQUEST>
<ADDITIONAL_METADATA>
The current local time is: 2026-05-26T17:58:35-04:00.
</ADDITIONAL_METADATA>
<USER_SETTINGS_CHANGE>
The user changed setting `Model Selection` from None to Gemini 3.5 Flash (High). ...
</USER_SETTINGS_CHANGE>
```
- **The real prompt is ONLY the text between `<USER_REQUEST>` and
  `</USER_REQUEST>`** (trim surrounding whitespace).
- `<ADDITIONAL_METADATA>` gives local time (timezone source).
- `<USER_SETTINGS_CHANGE>` is present only on the FIRST turn (model selection),
  absent on continuation turns. Filter both `<ADDITIONAL_METADATA>` and
  `<USER_SETTINGS_CHANGE>` out of the displayed prompt.
- The `model` chosen is recoverable from `<USER_SETTINGS_CHANGE>` ("Gemini 3.5
  Flash (High)") and also lives in `settings.json` `model`.

### 3.8 SYSTEM_MESSAGE content
```
The following is a <SYSTEM_MESSAGE> not actually sent by the user. ...
<SYSTEM_MESSAGE>
[Message] timestamp=... sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents ...
</SYSTEM_MESSAGE>
```
These mirror an entry in `messages/<messageId>.json` whose `recipient` is the
conversationId. Generally `hideFromUser: true` — treat as non-user-visible.

### 3.9 RUN_COMMAND result content
Two observed shapes (the leading tabs are literal in the file):
Success:
```
Created At: 2026-05-26T21:31:15Z
Completed At: 2026-05-26T21:31:17Z

				The command completed successfully.
				Output:
				<stdout...>
```
Failure (note: status still `DONE`):
```
Created At: ...
Completed At: ...

				The command failed with exit code: 1
				Output:
				cat: nonexistent_file.xyz: No such file or directory
```
Some results use `Stdout:`/`Stderr:` sub-labels instead of `Output:` (seen when
both streams are empty). Strip leading tabs/whitespace when extracting output.

---

## 4. step_index semantics & the gap

- `step_index` starts at **0** and increases by 1 per persisted step, with **one
  consistent exception: index 4 is always missing** in any session that makes at
  least one tool call.
- Pattern (verified across 4 tool-using sessions, all identical): `0,1,2,3,5,6,7,...`
  — i.e. exactly ONE skipped index, the slot right after the first tool's result
  (step 3). From index 5 onward the sequence is fully contiguous, even across
  later tool calls, system messages, and a continuation turn.
- A session with **no tool calls** (the text-only "say hello" session) has NO
  gap: steps `0,1,2`.
- **Interpretation:** index 4 is a reserved/hidden internal step the CLI emits
  once per session immediately after the first tool round-trip (most likely a
  hidden context/history refresh that mirrors the step-1 `CONVERSATION_HISTORY`
  but is not persisted to the transcript). It is NOT a sign of dropped data and
  NOT periodic.
- **Ordering is reliable.** Sort by `step_index` (equivalently, file line order —
  they already match). The gap at 4 should be ignored, not treated as missing
  content. Do not assume `result_index == call_index + 1` blindly across the gap:
  the first tool call is at step 2, its result at step 3, and the next planner
  step is 5 (4 skipped).

---

## 5. Project / workspace mapping  ★ (read carefully)

This is the trickiest part because **the transcript contains no workspace field**
and `history.jsonl` does not cover print-mode sessions.

### 5.1 What history.jsonl gives you (interactive TUI only)
`~/.gemini/antigravity-cli/history.jsonl`, one line per user prompt typed in the
interactive TUI:
```json
{"display":"first prompt","timestamp":1779831073907,"workspace":"/path/to/project"}
{"display":"second prompt","timestamp":1779831156198,"workspace":"/path/to/project","conversationId":"00000000-0000-4000-8000-000000000001"}
```
- `display` = the prompt text, `timestamp` = epoch ms, `workspace` = **absolute
  path** of the project (NOT symlink-resolved; it's the path as launched).
- `conversationId` is present from the **second** prompt onward; the FIRST prompt
  of a session lacks it (the session ID isn't assigned until the first turn
  completes). So to map prompt→workspace→conversationId, the conversationId on a
  line tags the session that the *previous* prompt(s) belong to.
- **CRITICAL LIMITATION:** `agy -p`/`--print` (non-interactive) sessions are
  **NOT written to history.jsonl**. Confirmed: after running multiple `-p`
  sessions, history.jsonl stayed at its original 2 (interactive) lines while 3
  new `brain/` dirs appeared. So history.jsonl maps only interactive sessions.

### 5.2 The reliable, universal mapping (works for print AND interactive)
`~/.gemini/config/projects/<projectId>.json`:
```json
{
  "id": "00000000-0000-4000-8000-000000000002",
  "name": "/tmp",
  "projectResources": { "resources": [
    { "gitFolder": { "folderUri": "file:///tmp", "allowWrite": true } }
  ] }
}
```
- `name` and `gitFolder.folderUri` (`file://` URI) give the **workspace root
  path**. The `--add-dir /tmp/agy-recon-...` run resolved its project to
  `/tmp` (the git root), and the project id matched the
  `ANTIGRAVITY_PROJECT_ID` env var the CLI injected during that session.
- The path is the **git repository root** (an `--add-dir` of a subdir of `/tmp`
  mapped to project `/tmp`). It is absolute and appears NOT symlink-resolved.

**However**, there is **no on-disk file that directly links a `conversationId`
(brain dir) to a `projectId` or workspace path.** The linkage exists only at
runtime (env var `ANTIGRAVITY_PROJECT_ID` / `ANTIGRAVITY_TRAJECTORY_ID`) and
inside the encrypted `conversations/*.pb`. So a parser cannot get the workspace
purely from the conversationId.

### 5.3 Recommended workspace-inference strategy for the parser
In priority order:
1. **history.jsonl `workspace`** keyed by matching `conversationId` — authoritative
   when present (interactive sessions only).
2. **Tool-arg paths in the transcript** — derive the workspace from the most
   common ancestor of `Cwd` (run_command), `AbsolutePath` (view_file),
   `TargetFile` (write/edit), `SearchPath` (grep_search), `DirectoryPath`
   (list_dir). For print-mode `--add-dir` sessions these are absolute and
   consistently point at the workspace.
3. Fall back to `config/projects/*.json` paths if a single project plausibly
   contains all the transcript's paths.
4. If none resolve (e.g. the no-`--add-dir` session that wandered the whole
   filesystem), mark workspace **unknown** — do not guess. Note: `agy -p` with no
   `--add-dir` defaults the workspace to `~/.gemini/antigravity-cli/scratch`
   (an empty dir), so the model flails; such sessions have no meaningful project.

---

## 6. Annotated result-content examples (file tools)

`VIEW_FILE` (result of `view_file`):
```
File Path: `file:///tmp/agy-recon-1779832704/main.go`
Total Lines: 9
Total Bytes: 98
Showing lines 1 to 9
The following code has been modified to include a line number before every line, in the format: <line_number>: <original_line>. ...
1: package main
2:
3: import "fmt"
...
The above content shows the entire, complete file contents of the requested file.
```
Strip the `N: ` line-number prefixes to recover original file content.

`CODE_ACTION` (result of `write_to_file`):
```
Created file file:///tmp/agy-recon-1779832704/greeting.txt with requested content.
If relevant, proactively run terminal commands to execute this code for the USER. Don't ask for permission.
```
The actual written bytes are in the *call's* `CodeContent` arg, not the result.

`CODE_ACTION` (result of `replace_file_content`):
```
The following changes were made by the replace_file_content tool to: /tmp/.../main.go. ...
[diff_block_start]
@@ -4,6 +4,6 @@
 ...
-	fmt.Println("hello world")
+	fmt.Println("goodbye world")
 ...
[diff_block_end]
```
Diff is between `[diff_block_start]`/`[diff_block_end]`; shows up to 3 context
lines. Old/new text are also in the call's `TargetContent`/`ReplacementContent`.

`GREP_SEARCH` (result of `grep_search`):
```
{"File":"/tmp/agy-recon-1779832704/main.go","LineNumber":7,"LineContent":"\tfmt.Println(\"hello world\")"}
```
One JSON object per match line.

`LIST_DIRECTORY` (result of `list_dir`):
```
{"name":".git", "isDir":true}
{"name":"README.md", "sizeBytes":"56"}
{"name":"main.go", "sizeBytes":"98"}

Summary: This directory contains 1 subdirectories and 3 files.
```

---

## 7. Multi-turn / continuation behavior

- `agy -c -p "..."` (continue most recent conversation) **appends to the SAME
  transcript file in the SAME `brain/<conversationId>/` dir.** No new brain dir,
  no new conversationId. Confirmed: the file grew from 11→14 lines.
- The continuation's `USER_INPUT` continues the monotonic `step_index` (e.g. a
  second turn's user input at index 12, right after the first turn ended at 11).
- A continuation turn's `USER_INPUT` content has `<USER_REQUEST>` +
  `<ADDITIONAL_METADATA>` but **no `<USER_SETTINGS_CHANGE>`** (settings unchanged).
- A turn boundary is marked at the start by either a `CONVERSATION_HISTORY`
  (`SYSTEM`) step or a `SYSTEM_MESSAGE` step. Detect new user turns by
  `type == "USER_INPUT"` occurrences rather than relying on a fixed structure.
- **Transcripts are append-only during and across turns** (writer appends one
  line per step; no rewriting of earlier lines was observed).
- `agy --conversation <id>` resumes a specific conversation by ID (per `--help`).

---

## 8. Version detection

- **Command:** `agy --version`
- **Output:** a single line, just the semver, e.g.:
  ```
  1.0.2
  ```
  (no "v" prefix, no extra text; exit 0)
- Avoid `agy version` (no `--`): it tries to open a TTY and errors in
  non-interactive contexts (`bubbletea: could not open TTY`).
- `agy changelog` prints release notes grouped by version, newest first
  (`1.0.2:` then bullet lines prefixed with `·`, blank line, `1.0.1:` etc.) — also
  non-interactive and exit 0; the first `N.N.N:` line is the current version.

---

## 9. Auth note
`log/cli-*.log` repeatedly logs `Failed to poll ... You are not logged into
Antigravity.` yet sessions still receive model responses. Auth works off
`~/.gemini/oauth_creds.json` (keys: `access_token`, `refresh_token`, `id_token`,
`expiry_date`, `scope`, `token_type`). The log warnings are non-fatal; do not use
them as a "session failed" signal.

---

## 10. Edge cases (all verified unless noted)

| case | behavior |
|---|---|
| Text-only session (no tools) | 3 steps: `USER_INPUT`(0), `CONVERSATION_HISTORY`(1), `PLANNER_RESPONSE`(2). No step_index gap. |
| Empty/aborted session | Not directly captured. A session that produces a `brain/` dir but a transcript with only steps 0–1 (no PLANNER_RESPONSE) would be an aborted/no-response session — treat absence of any `PLANNER_RESPONSE` as "no assistant output". |
| Failed shell command | `RUN_COMMAND` with `status:"DONE"` and content `"The command failed with exit code: N"`. NOT `ERROR`. |
| Tool-framework error | `status:"ERROR"`, content `"Encountered error in step execution: ..."` (e.g. dir not found). |
| Async / long command | `RUN_COMMAND` step with `status:"RUNNING"`; output later in `tasks/task-<N>.log`. |
| Parallel tool calls | Not observed (always 1 per PLANNER_RESPONSE) but tolerate `tool_calls` length >1. |
| Very large outputs | `list -la ~` was truncated in content with a literal `<truncated 494 lines>` marker. Expect inline truncation markers. |
| `.system_generated` naming | **Stable across runs** — every brain dir uses the literal `.system_generated/logs/` path; not randomized or regenerated under a different name. |
| `messages/` & `tasks/` dirs | Optional — only present when the session received a system message / ran an async task. Parser must tolerate their absence. |
| Append-only | Confirmed; transcripts are appended to, not rewritten. |
| Two transcript files | `transcript.jsonl` and `transcript_full.jsonl` always have identical line/step counts; only arg-escaping differs. |

---

## 11. Open questions / risks

1. **conversationId → workspace has no direct on-disk link** for print-mode
   sessions. history.jsonl covers interactive only; the projectId link is
   runtime-only (env var) / inside the encrypted `.pb`. Workspace must be inferred
   from transcript tool-arg paths (§5.3). RISK: a session that never touches files
   (text-only) has NO recoverable workspace.
2. **history.jsonl scope uncertainty.** Confirmed `-p` is excluded; unconfirmed
   whether `--prompt-interactive` (`-i`) writes to it. Assume only fully
   interactive TUI sessions are logged there.
3. **`implicit/*.pb` purpose unknown** (encrypted; different UUID namespace than
   conversationId, count happens to equal conversation count). Likely "implicit"
   memory/context blobs. Skip.
4. **Untested tools:** `codebase_search`, browser/URL tools (`read_url`,
   `execute_url`), MCP tools, subagent/task spawning. Their exact `args` keys and
   result `type` are unconfirmed — the only safe assumption is that an unknown
   tool's result is the next step and its `type` may be a new dedicated type or
   `GENERIC`. Parser should map by capability and default unknowns to generic.
5. **`thinking` field** carries model reasoning text. Map it to SpecStory's
   `thinking` content type, consistent with other providers; renderers can
   decide whether and how to expose it.
6. **The single index-4 gap** is inferred to be a hidden context-refresh step; its
   exact nature is not directly observable (it's never written to the transcript).
   Risk is low — treat the gap as benign, not data loss.
7. **`ANTIGRAVITY_SOURCE_METADATA` env var** (seen in an `env` command's captured
   output) contains a per-tool-call `id`, `thinkingSignature`, and the
   conversationId+stepIndex — this is the only place a call `id` surfaces, but
   it's incidental (only appears if the session happened to run `env`). Do not
   rely on it.
