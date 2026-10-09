# GitHub Copilot CLI Session Format

This describes what GitHub Copilot CLI writes for a session, as observed in sessions written by `GitHub Copilot CLI 1.0.95-2.` (the exact `copilot --version` banner; the provider's baseline) on macOS, and confirmed against `1.0.94` installed from npm by the factory scripts.

## Store layout

Copilot keeps its state in `$COPILOT_HOME`, which defaults to `~/.copilot`. Each session is a directory named by its UUID:

```text
~/.copilot/session-state/<session-id>/
├── events.jsonl          # the durable, append-only transcript (the only file the provider reads)
├── workspace.yaml        # session metadata Copilot rewrites as the session changes
├── checkpoints/          # compaction summaries
├── files/ research/      # the agent's scratch areas; may hold unrelated .jsonl files
├── rewind-file-snapshots/
└── inuse.<pid>.lock      # present only while a process has the session open
```

The same store holds every session regardless of project and every host Copilot runs under. `workspace.yaml` names the host in `client_name`: `github/cli` for the terminal CLI, `github/autopilot` for the GitHub Copilot app, `vscode-agent-host` for VS Code's Agents window and Autopilot mode, and `vscode` for VS Code's own CLI integration. All of them write the same `events.jsonl` format, so the provider covers them all.

`~/.copilot/session-store.db` is a search index over sessions. It is not used: it keeps each turn's user message but often no reply text, and no tool calls. `~/.copilot/data.db` is the GitHub Copilot app's own database (its sidebar), holding no transcripts.

## Record envelope

Every line of `events.jsonl` is one record:

```json
{"type": "user.message", "data": {"content": "..."}, "id": "<uuid>", "timestamp": "2026-10-09T13:46:56.840Z", "parentId": "<previous record's id>"}
```

`parentId` chains each record to the one before it; the first record's is `null`. The first record is always `session.start`.

## Record kinds

Observed kinds and how the provider treats them:

| Kind | Treatment |
|---|---|
| `session.start` | Header: `data.sessionId`, `data.copilotVersion`, `data.startTime`, `data.context.cwd` |
| `user.message` | A prompt, a message from another session, a sub-agent's prompt, or injected machinery (see below) |
| `assistant.message` | Agent text, reasoning and tool requests |
| `tool.execution_complete` | A tool call's result or error, paired to its request by `toolCallId` |
| `tool.execution_start` | Not rendered: duplicates the request already carried by `assistant.message` |
| `session.*` (other than start), `subagent.*`, `skill.*`, `external_tool.*`, `assistant.turn_*`, `system.notification`, `abort` | Not rendered: lifecycle, sub-agent and skill bookkeeping, turn boundaries, and notices shown in the terminal rather than in the conversation |
| `hook.*`, `permission.*`, `model.*`, `system.message`, `session.usage_checkpoint`, `session.usage_record` | Harness machinery: not rendered (see below) |

A kind not in this table is logged at debug level and otherwise ignored.

## User messages

`data.content` holds the text; `data.source` says who it came from, and `data.transformedContent` holds the text as the model received it:

| `source` | Meaning | Rendered as |
|---|---|---|
| empty or `user`, `content` starting `<system_notification>` | A runtime notice (a background agent finishing) | Not rendered |
| empty or `user`, `transformedContent` containing `<cross_session_message>` outside the typed text (which it repeats verbatim, so a prompt quoting the tag stays a prompt) | A message another Copilot session sent with `send_session_message`; the envelope names the sender in a `from_session_id:` line | A user turn labeled `_Message from Copilot session <id>:_` |
| empty or `user` | Something the user typed | A user turn |
| `agent-<id>` | The prompt the agent handed a sub-agent | A sidechain agent message |
| `autopilot`, `system`, `skill-<name>`, `instruction-discovery`, … | Autopilot and system nudges, skill and instruction injections | Not rendered |

`parentAgentTaskId` is present on genuine prompts too, so it does not identify sub-agent traffic. A session is named after its first prompt or cross-session message, which never changes as the file grows.

## Agent messages and tools

`assistant.message` carries `content`, `reasoningText` (when the model exposes it), `model`, `outputTokens`, and `toolRequests`, each `{toolCallId, name, arguments, intentionSummary}`. `arguments` is usually an object; `apply_patch` takes the raw patch text as a string, rendered as its `input`. Streaming chunk placeholders arrive with empty content and no requests and are skipped. `reasoningOpaque` and `encryptedContent` are opaque and not rendered.

`tool.execution_complete` carries `success` and either `result.content` or `error.message`. A failed call renders its error text as the result.

`task_complete` is how Copilot ends an autonomous turn: its `summary` argument is the closing answer the user sees, so it renders as agent text rather than as a tool block.

Tool bodies render as Copilot's one-line `intentionSummary`, then a shell command in a bash fence or the arguments as an `Input:` list, then the result in a fence capped at 2000 runes. The classification of every inventoried tool is in `toolTypes` in `events.go`, checked against `testdata/tools.txt`.

## Sub-agents

A `task` call starts a sub-agent. Its prompt arrives as a `user.message` with an `agent-<id>` source, and its own turns and tool results arrive inline in the same file with `data.parentToolCallId` set to the launching call. They are kept in file order, where they interleave with the parent's turns and with any parallel sub-agent's, and flagged as sidechain. The sub-agent's final report is the result of the launching `task` call.

## Write lifecycle

`events.jsonl` is appended as the session runs; the last line can be a record still being written, which a reader skips until it is complete. A run ends with `session.shutdown`; resuming appends `session.resume` and carries on in the same file. `workspace.yaml` and the lock files are rewritten in place and not read. Copilot prunes old transcripts: a session directory can outlive its `events.jsonl`, and such a session can no longer be exported.

## Working directory and project

`session.start.data.context.cwd` (and `cwd` in `workspace.yaml`) is the directory as the operating system reports it: symlinks resolved and in on-disk case. Started in `/tmp/x`, Copilot records `/private/tmp/x`. The provider canonicalizes the local project path the same way and compares exactly. A session started in VS Code's Agents window with no workspace selected is filed under a scratch directory such as `~/.copilot/chats/<id>`.

## Version

`session.start.data.copilotVersion` records the version, e.g. `1.0.95-2`. A reconstructed session records it empty, which the provider reports as `unknown`.

## Resume

`copilot --resume=<id>` (or `-r <id>`) reads `session-state/<id>/events.jsonl` and appends to it. `workspace.yaml` is not needed; Copilot writes one.

### Reconstructed sessions

A session another agent's conversation is reconstructed into needs only:

- `session.start` with `sessionId`, `version` `1`, `producer` `copilot-agent`, `startTime`, `context.cwd`, and `copilotVersion`. Copilot refuses a file without `copilotVersion` (`Session file is corrupted (line 1: missing field copilotVersion)`) but accepts it empty, so it is written empty rather than as a borrowed version.
- `user.message` records with `content`, and `assistant.message` records with `messageId`, `content` and an empty `toolRequests`, chained through `parentId`.

No end-of-session record is needed: resumed with `copilot --resume=<id> -p ...`, such a file loads without a warning, the agent answers questions about the imported turns, and Copilot appends `session.resume` and the new turn to the same file. A leading agent turn (the migration note) is accepted. `session.start` also carries `specstorySourceSessionId`, the source session's id, which Copilot ignores.

## What is not preserved

- Harness records (hooks, permission prompts, per-model-call telemetry including full context snapshots, the system prompt Copilot resends each turn, token counters) are not rendered. Like every accepted record, they are kept in the raw data and in the `--debug-raw` export.
- `tool.execution_start`, turn boundaries and lifecycle records are kept in the raw data but not rendered.
- A tool result's `detailedContent` (for example the diff of an edit) is not rendered; `content` is.
- Opaque reasoning (`reasoningOpaque`, `encryptedContent`) is kept in the raw data only.
- Reconstruction keeps the conversation as plain text turns; Copilot's own tool records, reasoning and model metadata are not recreated.
