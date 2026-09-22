# OpenCode Session Format

OpenCode (`opencode`, the open source terminal coding agent) keeps every session in one SQLite database owned by a background service. Everything below was verified empirically against OpenCode 2.0.14 on macOS by driving the binary (TUI, `opencode run`, `opencode session import`) and reading what it wrote.

Baseline version, exactly as `opencode --version` prints it:

```text
2.0.14
```

## Store layout

```text
$XDG_DATA_HOME/opencode/            (default ~/.local/share/opencode on every OS)
├── opencode.db                     # all projects, sessions and messages
├── opencode.db-wal                 # write-ahead log; nearly every write lands here first
├── opencode.db-shm
├── log/opencode.log
├── shell/<project-id>/             # spill files for user shell commands
└── snapshot/, repos/, tool-output/ # file snapshots and caches, not transcripts
```

- The data directory is `$XDG_DATA_HOME/opencode`, falling back to `~/.local/share/opencode`. OpenCode computes it the same way on macOS, Linux and Windows (`os.homedir()` joined with `.local/share`); there is no `%APPDATA%` branch. `opencode debug paths` prints it.
- The database file name is `$OPENCODE_DB` when set (resolved relative to the data directory; `:memory:` keeps no file), otherwise `opencode.db` for the `latest`, `dev`, `beta`, `next` and `prod` release channels, or when `OPENCODE_DISABLE_CHANNEL_DB` is `1`/`true`. Other channels use `opencode-<channel>.db`. Released builds report `channel=latest` in their log.
- The database is in WAL mode already. `opencode serve --service` (started automatically by the TUI and every CLI subcommand) is the only writer; the TUI and `opencode run` talk to it over HTTP.
- `account`, `control_account` and `credential` hold OAuth tokens and API keys. They are never read.

## Tables that matter

| Table | Role |
|---|---|
| `session_v2` | One row per session. `directory` is the working directory the session was started in (the project association and reindex's origin). `parent_id` is set for subagent sessions. `title` is OpenCode's own generated title (empty until the title model answers). `slug` is a random two-word label (`cosmic-wolf`), not derived from content. `version` is the OpenCode version that created the session. `time_created` / `time_updated` are Unix milliseconds. |
| `session_message` | One row per conversation record: `id`, `session_id`, `type`, `seq`, `time_created`, `time_updated`, and `data` (JSON text). Ordered by `seq` within a session. `seq` values are sparse (they share a counter with other session events), so gaps are normal. |
| `project` | `id`, `worktree`. The id is the repository's root commit hash for a git worktree, otherwise an opaque generated id (and `global` for some directories). Not used for matching: `session_v2.directory` is authoritative. |
| `event_sequence` | The next sequence number per session aggregate. Maintained by OpenCode; never written by this provider. |
| everything else | Permissions, instructions, workspaces, worktrees, inbox/pending queues, key-value settings. Not transcripts. |

## Message records

`session_message.type` selects the shape of `data`. The complete set in 2.0.14's OpenAPI schema (`Session.Message.Info`) is below; every kind marked "observed" was produced and inspected in a real session.

| type | Observed | Content | Rendered as |
|---|---|---|---|
| `user` | yes | `text`, `files[]` (attachments: `mime`, `name`, `source`, `data`), `agents[]` (`@agent` mentions), `skills[]` | A user message. Slash commands are expanded before storage: `/review` is stored as a `user` record whose `text` is the whole command template, with no marker naming the command. |
| `assistant` | yes | One model step: `agent`, `model` (`{id, providerID, variant}`), `content[]` parts, `finish`, `error`, `tokens`, `cost`, `time.{created,streamed,completed}` | Agent messages, one per content part, in part order. |
| `shell` | yes | A user `!command`: `shellID`, `command`, `status` (`running`, `exited`, `timeout`, `killed`), `exit`, `output.{output,truncated}` | A user message labeled "User ran a shell command" with the command, output and exit status. |
| `synthetic` | yes | Text OpenCode injects for the model: the output echo of a user shell command (`metadata.source == "shell"`) and Plan-mode `<system-reminder>` blocks | Not rendered: scaffolding. The shell echo duplicates the `shell` record. |
| `compaction` | yes | `status` (`running`, `completed`, `failed`), `reason` (`auto`, `manual`), `summary`, `recent`, `error` | A completed compaction renders its summary as an agent message; a failed one renders its error. A running one is not rendered until it completes. |
| `idle` | yes | `outcome` (`succeeded`, `failed`, `interrupted`): the end of a turn | Not rendered: lifecycle marker. |
| `agent-switched` | yes | `agent`, `previous` (for example `build` → `plan`) | Not rendered: the agent is recorded on each assistant step. |
| `model-switched` | yes | `model`, `previous` | Not rendered: the model is recorded on each assistant step. |
| `system` | no | `text`, `description` | Not rendered: scaffolding. |
| `skill` | no | `skill`, `name`, `text` (an injected skill body) | Not rendered: scaffolding (the skill body is instructions, not conversation). |
| `location-switched` | no | `location.directory`, `projectID`, `subpath` | Not rendered: metadata. |

An unfamiliar `type` is logged at Debug and skipped; its row is still preserved in `RawData` and the debug export.

### Assistant content parts

| part `type` | Fields | Notes |
|---|---|---|
| `text` | `text`, `state.itemId` | Whitespace-only text parts occur between tool calls and are skipped. |
| `reasoning` | `text`, `state`, `time` | Thinking. Some providers return only encrypted reasoning (`state.reasoningEncryptedContent`) with an empty `text`; only non-empty `text` is rendered. |
| `tool` | `id` (the call id), `name`, `executed`, `state`, `time.{created,ran,completed}` | The call and its result live in the same part, so no pairing is needed. One assistant step can hold several tool parts (parallel calls). |

Tool `state` by `status`:

| status | Fields |
|---|---|
| `streaming` | `input` is the partial argument string |
| `running` | `input` object, `metadata` |
| `completed` | `input` object, `content[]`, `metadata` |
| `error` | `input` object, `error.{type,message}`, optional `content[]` |

`content[]` items are `{type: "text", text}` or `{type: "file", uri, mime, name}`. A completed call can still report failure inside its text: `execute` returns the thrown error as its result with status `completed`.

An assistant step that fails before producing content has `finish: "error"`, an empty `content`, and `error.message` (for example an invalid model id); it renders as an agent error message.

## Tool shapes observed

Tool names are stored bare (`read`, not `functions.read`). The inventory and the calls below come from 2.0.14 with the `build` agent and the default configuration.

| Tool | Input | Result |
|---|---|---|
| `read` | `path` (relative or absolute), optional `offset`, `limit` | Text: `Read file <path>, lines a-b` then `N: <line>` rows. Missing file: status `error`, `File not found: <path>`. |
| `write` | `path`, `content` | Text acknowledgement (`Created file successfully: <path>`). |
| `edit` | `path`, `oldString`, `newString` | Text acknowledgement; `metadata.files[]` carries a unified diff per file (`patch`, `additions`, `deletions`). |
| `glob` | `pattern`, optional `path` | Newline-separated absolute paths; `metadata.count`. |
| `grep` | `pattern`, optional `path`, `include` | `Found N matches` then per-file `Line N:` rows; `metadata.matches`. |
| `shell` | `command`, optional `description`, `workdir`, `timeout` | Output text, then a separate `Command exited with code N.` text item for the model; `metadata.exit`. |
| `webfetch` | `url`, optional `format` | The page converted to markdown. |
| `websearch` | `query` | Only failures were observed (`Web search cancelled`, status `error`). |
| `skill` | `id` | `<skill_content name="...">` wrapping the skill document; `metadata.name`, `metadata.directory`. |
| `subagent` | `agent`, `description`, `prompt` | `<subagent sessionID="..." state="completed">` wrapping the subagent's final answer; `metadata.sessionID`. The subagent's own conversation is a child session (`parent_id` set). |
| `execute` | `code` (JavaScript run in OpenCode's "Code Mode" sandbox, calling `search()` and `tools.<path>(...)`) | The returned value as text, or the thrown error as text; `metadata.toolCalls[]` lists each inner call's `tool`, `status` and `input`. |

## Write lifecycle

- The `user` row is inserted when the prompt is submitted.
- Each model step inserts an `assistant` row as soon as it starts and rewrites that same row in place while parts stream in; `time_updated` advances on every rewrite. A new step (after tool calls) inserts a new `assistant` row.
- An `idle` row closes the turn.
- The title is generated asynchronously and written to `session_v2.title`, which also advances `session_v2.time_updated` without touching any message row.
- A session therefore changes when its `session_v2.time_updated`, its message count, or its newest message `time_updated` changes. All three are read in one query.
- Every write goes through SQLite's WAL, so `opencode.db-wal` is created or written on each change. SQLite may delete and recreate the WAL at checkpoints, so the data directory is watched rather than the file.
- Nothing is transient: there are no scratch files for in-flight messages. A partially streamed assistant step is visible in its row until it completes.

## Project association

`session_v2.directory` is the realpath of the directory OpenCode was started in (on macOS `/tmp/x` is recorded as `/private/tmp/x`). A session belongs to a project when that directory equals the project's canonical path. Subagent sessions (`parent_id` set) are part of their parent's conversation and are not listed on their own, matching `opencode session list`, which shows only top-level sessions.

## Version

`session_v2.version` records the OpenCode version that created the session. There is no per-message version.

## Resume

- Native resume is `opencode -s <session-id>` (TUI) or `opencode run -s <session-id> <message>` (headless). New records append to the same session.
- OpenCode reads a session only from the database, through its service. There is no per-session file to write.
- `opencode session import --directory <dir> <file.json>` loads a session in OpenCode's export format (`opencode session export <id>` produces it) through the service, which assigns the project for `<dir>` (creating it if the directory is new), numbers `seq` from 1 in array order, and initializes `event_sequence`. The session id in the file is preserved.
- The import schema requires `info.id`, `info.projectID` (any string: it is replaced by the project resolved from `--directory`), `info.cost`, `info.tokens`, `info.time.created`, `info.time.updated`, `info.location.directory`, and each message's `id`, `type`, `time.created`. A user message needs `text`; an assistant message needs `agent`, `model` and `content`.

### Reconstructed sessions

A reconstructed session is an export document with the flattened turns: user turns become `user` records, agent turns become `assistant` records with one `text` part and `finish: "stop"`. Verified with a minimal document containing a passphrase: `opencode -s <id>` opened it with both turns visible, answered "What is the magic passphrase?" from the imported context, and appended `agent-switched`, `model-switched`, `user`, `assistant` and `idle` records to the same session.

- The assistant `model` is required by the schema; it is written as `{"id": "", "providerID": ""}`. OpenCode accepts it and shows the imported turns with an empty model label; the next prompt uses the model configured for the project.
- `agent` is required on each assistant record; reconstructed records use `build`, the default primary agent, because the field names the OpenCode agent profile that would continue the turn, not historical attribution.
- `info.cost` and `info.tokens` are required and written as zero: the flattened transcript carries no usage.
- `info.metadata.specstorySourceSessionId` carries the source session id.
- Ids use OpenCode's layout: a `ses_` or `msg_` prefix, 12 hex digits of `(unix-ms << 12 | counter)` (bit-inverted for session ids so newer sessions sort first), and 14 random base62 characters.
- OpenCode's free "Zen" models refuse to continue any conversation whose assistant turns were not produced through Zen (`OpenCode's free tier can only be used from within OpenCode`, HTTP 403). This applies to every imported history, not to the reconstruction format: the same session continues normally with any other configured provider.
- `opencode run -s <id>` stalls on the first prompt sent to a freshly imported session and succeeds on the next attempt; the TUI (`opencode -s <id>`, which `specstory resume` launches) is not affected.

## What is not preserved

- `user.files[].data` (base64 attachment bodies) is not rendered; attachments render by name and MIME type.
- Encrypted reasoning, provider state, snapshots, costs and the `recent` field of compactions are not rendered. They remain in `RawData` and the debug export.
- The command name of an expanded slash command is not recorded by OpenCode, so the template text renders as the user's prompt and cannot be filtered from resumed transcripts.
