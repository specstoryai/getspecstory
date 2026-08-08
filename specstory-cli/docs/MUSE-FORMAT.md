# Muse Code Session Format

Muse Code (`muse`, Meta's terminal coding agent, powered by Muse Spark models) records each session as an event-sourced JSONL transcript. Everything below was verified empirically against Muse Code 0.1.0 (build 427a430436) by driving the binary and inspecting the files it wrote.

## Store layout

```
~/.local/share/muse/sessions/YYYY/MM/DD/<session-id>/
├── session.jsonl        # the transcript (append-only event log)
├── cron.db              # scheduled jobs (not a transcript)
├── goals.db             # goal state (not a transcript)
├── tool-outputs/        # spill dir for large tool outputs (may be empty)
└── subagent/<subagent-id>/session.jsonl   # one transcript per spawned subagent
```

- The store is date-sharded and global, like Codex (`~/.codex/sessions/YYYY/MM/DD/`). It is NOT project-scoped: the project association comes from `workspace_root` inside the transcript's metadata record.
- Config lives at `~/.config/muse/` (auth.json, settings.json, trust.json). XDG paths throughout.
- Multi-turn sessions append more runs to the same `session.jsonl`. `muse resume <session-uuid>` (or `--last`) continues a session; `muse exec "<prompt>"` runs one headless turn and creates a normal session directory.

## Record envelope

Each line is one event:

```json
{
  "schema_version": 1,
  "id": "<record-uuid>",
  "stream": {"kind": "session", "id": "<session-id>"},
  "sequence": 1,
  "recorded_at": 1786138660396634,
  "record_type": "event",
  "durability": "durable",
  "causation_id": null,
  "payload_type": "runtime.session.metadata",
  "payload_schema_version": 1,
  "payload": { ... }
}
```

- `recorded_at` is **microseconds** since the Unix epoch.
- `stream.id` is the critical filter: records that belong to the session itself carry the session's own id. Subagent-task runs are ALSO recorded in the parent file, but with a different `stream.id` (the task stream id, e.g. `018f0000-...`). Processing only records whose `stream.id` matches the session id excludes subagent noise; otherwise every subagent objective renders as a fake user turn ("Role: demo-worker Objective: ...").

## Payload types that matter

| payload_type | Role |
|---|---|
| `runtime.session.metadata` | `payload.record` carries `workspace_root`, `provider_id`, `model_id`, `build.semver`, `tool_surface_version`. First record of the file. The workspace root read here is the project association (and reindex's OriginCwd). |
| `runtime.session` | The conversation. `payload.kind` is `run` (model turns) or `task` (execution bookkeeping — ignore for markdown). |
| `session.end` | `payload.record.exit_reason` etc. End of a session process. |
| everything else | Telemetry, subagent control, cron/reminder machinery. Skip for markdown. |

## Conversation events (`payload.kind == "run"`, keyed by `payload.event.kind`)

A turn (exchange) is one `run_id`. Events for a run, in order:

| event.kind | Content |
|---|---|
| `started` | `{prompt}` — the user's message. Starts an exchange. |
| `reasoning_committed` | `{message_id, text, encrypted_content}` — thinking. `text` is usually EMPTY because reasoning is encrypted for the Meta provider; render `text` only when non-empty. |
| `assistant_message_committed` | `{message_id, text}` — assistant prose. |
| `assistant_tool_calls_committed` | `{message_id, tool_calls: [{id, call_id, name, args}]}` — `args` is a **JSON string**, not an object. |
| `tool_result_batch_committed` | `{batch_id, results: [{tool_call_index, tool_call_id, text}]}` — fold into the matching call by `tool_call_id`. `text` is a plain string; for `bash` it is a JSON object string (see below); errors are `"tool failed: <reason>"`. |
| `model_completed` | `{usage: {input_tokens, output_tokens, cached_tokens, cache_write_tokens, cache_read_tokens, reasoning_tokens}, duration_ms, finish_reason, model}` — one per model step; a run can have several. Attach usage per step. |
| `terminal` | `{terminal: "completed", turn_duration_ms, ...}` — run end (exchange EndTime). |
| `todo_snapshot_updated` | `{revision, source_tool, items: [{text, status}]}` — todo state after write_todos. |
| everything else | Diagnostics (context blocks, provider options, trace records, reminders, goals). Skip. |

## Tool call shapes (from tool-demo-report.md plus driven sessions)

Native tools: `read_file` (path/offset/limit; output is `N|line` numbered text), `write_file` (path/content; output "wrote N bytes to <path>"), `edit_file` (path/find/replace; output "edited — diff: -old +new" or "edited changed lines: ..." with a mini unified diff), `search` (ripgrep: pattern/mode/paths/output_mode; output is rg-style `file:line:text`), `bash` (command/workdir/tty; result text is a JSON object string with `command`, `description`, `exit_code`, `terminal_status`, `output`, `truncated`), `bash_input` (chars, drives a PTY started by `bash` with tty:true), `web_search`, `read_memory`/`add_memory`/`edit_memory` (scope/path; JSON result), `write_todos` (todos[{text,status}]; JSON `{ok,revision,items}` result), `get_goal`/`create_goal`/`update_goal`/`report_progress` (JSON), `subagent_spawn`/`subagent_status`/`subagent_wait`/`subagent_read_result`/`subagent_send_message`/`subagent_cancel` (JSON), `cron_create`/`cron_list`/`cron_delete`, `read_skill`, `request_user_input`, `snooze_reminder`.

Note: `write_todos` items use `text` (not `content` like Qwen or `description` like Gemini).

## Subagents

A `subagent_spawn` produces (a) tool call/result records in the parent (with `stream.id` = parent session id), (b) task-stream run records in the parent file (`stream.id` = task id — excluded by the stream filter), and (c) a full independent transcript at `subagent/<subagent-id>/session.jsonl` with the same event format. Enumeration must only treat top-level `<session-id>/session.jsonl` files as sessions; anything under a `subagent/` path segment is not an independent session.

## Provider design decisions

- One exchange per `run_id`, opened by `started`, closed by `terminal`.
- `stream.id != session id` → record skipped.
- `payload.kind == "task"` → skipped (execution bookkeeping; statuses like "opening meta model stream").
- Only top-level session.jsonl files enumerate; `subagent/` transcripts are excluded.
- Watching must be bounded to a trailing date window (the Codex fd-exhaustion lesson, changelog v2.6.0): never watch every historical date directory.
- Timestamps convert from microseconds to ISO 8601 UTC.
- `bash` result text parses as JSON to extract `output` and `exit_code` for rendering; on parse failure the raw text renders inside a fence.
