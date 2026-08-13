# Grok Build Session Format

Grok Build (`grok`, SpaceXAI's terminal coding agent) records each session as a directory of JSONL and JSON files. Everything below was verified empirically against Grok Build 1.0.3 (1a29d5bc12d4) by driving the binary and inspecting the files it wrote.

## Store layout

```
~/.grok/sessions/
├── session_search.sqlite                  # global search index (not a transcript)
└── <percent-encoded-cwd>/                 # one dir per project, e.g. %2FUsers%2Fgdc%2Fpainpoints
    ├── prompt_history.jsonl               # the user's prompt line history for this project
    └── <session-uuid>/
        ├── chat_history.jsonl             # THE TRANSCRIPT
        ├── events.jsonl                   # timestamps, tool outcomes, turn boundaries
        ├── summary.json                   # session metadata (id, cwd, times, title, model)
        ├── updates.jsonl                  # ACP event stream: timestamps, tool kinds, usage
        ├── rewind_points.jsonl            # rewind/branch markers
        ├── hunk_records.jsonl             # file edit hunks
        ├── system_prompt.txt              # the system prompt for this session
        ├── prompt_context.json            # context assembly bookkeeping
        ├── subagents/<subagent-id>/       # meta.json + output.json only (NOT a transcript)
        ├── images/<n>.jpg                 # images produced or attached in the session
        ├── terminal/call-<id>-<n>.log     # per-command terminal output
        └── recap_requests/<uuid>.json     # context recap payloads
```

- The store is project-scoped. The project directory name is the session's working directory, percent-encoded, so `/Users/gdc/painpoints` becomes `%2FUsers%2Fgdc%2Fpainpoints`. Decoding the directory name recovers the absolute path, which is the project association (and reindex's OriginCwd).
- Session ids are UUIDv7, so they sort by creation time.
- Multi-turn sessions append to the same `chat_history.jsonl`. `grok -r <session-id>` resumes, `grok -c` continues the most recent session for the cwd, and `grok -p "<prompt>"` runs one headless turn and creates a normal session directory.

## Subagent sessions

A spawned subagent gets its own **top-level session directory**, a sibling of its parent, with a full `chat_history.jsonl`. Under the parent, `subagents/<subagent-id>/` holds only `meta.json` and `output.json`, which link the two:

```json
{
  "subagent_id": "019ff762-5563-7b12-a5f1-3a3e45bfb8b3",
  "parent_session_id": "019ff760-692d-7bf3-9004-67fba7cb95e9",
  "subagent_type": "general-purpose",
  "description": "Write demo output file",
  "prompt": "...",
  "status": "completed",
  "started_at": "2026-08-12T19:10:44.594884Z",
  "duration_ms": 4823,
  "tool_calls": 2,
  "turns": 1
}
```

Because subagent sessions sit at the top level, a naive scan would sync each one as its own session. Two independent signals exclude them:

1. `summary.json` carries `"session_kind": "subagent"`. The field is absent on real sessions.
2. A subagent transcript contains no `<user_query>` record, so it yields zero exchanges under the parsing rule below.

The provider filters on `session_kind` and the zero-exchange rule catches anything the field misses.

## chat_history.jsonl

Each line is one record. There is **no timestamp on any record**, which is why `updates.jsonl` and `events.jsonl` matter (see below).

| `type` | Role |
|---|---|
| `system` | The system prompt. Skip. |
| `user` | Either the real human turn or injected context. See the `<user_query>` rule below. |
| `reasoning` | Model thinking. `summary` is an array of `{"type": "summary_text", "text": ...}` and is human-readable. `encrypted_content` is opaque and must never be rendered. `status` is e.g. `completed`. |
| `assistant` | Model output. `content` is a plain string. `tool_calls` is an array (see below). Carries `model_id`, `model_fingerprint`, `reasoning_effort`. |
| `tool_result` | The outcome of one tool call, linked by `tool_call_id`. `content` is a plain string. |
| `backend_tool_call` | Server-side web and X tools. See below. |

### The `<user_query>` rule

A `user` record is only a real human turn when its text contains a `<user_query>...</user_query>` block. Everything between those tags is the human's actual prompt.

Every other `user` record is injected context that must be skipped:

- the first record, wrapping `<user_info>`, `<git_status>`, and `<rules>`
- a large `<system-reminder>` listing available skills, observed at 29 KB
- a `<system-reminder>` listing connected MCP servers
- `<system-reminder>` notifications about background subagents finishing

In the reference session, 6 `user` records contained only 2 real prompts. Rendering them all would put a 29 KB skills dump into the markdown.

### Tool calls

`assistant.tool_calls` entries look like this, and **`arguments` is a JSON string, not an object**, so it needs a second parse:

```json
{
  "id": "call-e2fe85f9-9b85-4d97-aad1-ba8dee688176-0",
  "name": "read_file",
  "arguments": "{\"target_file\":\"/path/to/file\"}"
}
```

The matching `tool_result` carries the same id in `tool_call_id` and its `content` is a plain string.

### Two indirect tool mechanisms

Some tools are not called by their own name, so a provider that only reads `tool_calls[].name` will render them poorly.

**MCP tools go through `use_tool`.** The real tool name is nested in the arguments:

```json
{"tool_name": "voice__list_voices", "tool_input": {}}
```

`use_tool` was the most frequent call in the reference session (36 of 96). A companion `search_tool` (`{"query": "figma", "limit": 255}`) discovers available tools.

**Web and X tools are `backend_tool_call` records.** They never appear in `tool_calls`. The `kind` object holds the detail:

- `kind.tool_type == "web_search"` with `kind.action.type` of `search`, `open_page`, or `find_in_page`
- `kind.tool_type == "x_search"` with `kind.name` of `x_user_search`, `x_semantic_search`, `x_keyword_search`, or `x_thread_fetch`, and `kind.input` as a JSON string

## events.jsonl

Every record carries a `ts` (RFC 3339). This is the only source of timing, and the only source of tool success or failure.

| `type` | Use |
|---|---|
| `turn_started` | Turn boundary. Carries `session_id`, `turn_number`, `model_id`, `session_relationship`. |
| `turn_ended` | Turn boundary, with `outcome`. |
| `tool_started` | Tool began. Carries `tool_name` but **no** `tool_call_id`. |
| `tool_completed` | Carries `tool_call_id`, `tool_name`, `duration_ms`, and `outcome` of `success` or `error`. |
| `mcp_tool_call_started` / `mcp_tool_call_completed` | Same for MCP calls. |
| `mcp_server_failed` / `mcp_server_connected` | MCP server health. |
| `phase_changed`, `first_token`, `loop_started` | UI phase telemetry. Skip. |

Correlating `tool_completed.tool_call_id` back to `chat_history` gives each tool call a timestamp and a success or error outcome. In the reference session, 4 of 96 tool calls failed, and that failure is visible **only** in `events.jsonl`. The `tool_result.content` for a failed call holds the error text but nothing marks it as an error.

`session_relationship` is `primary` on both real and subagent sessions, so it is not a subagent discriminator. Use `summary.json.session_kind`.

## summary.json

```json
{
  "info": { "id": "019ff760-...", "cwd": "/Users/gdc/painpoints" },
  "session_summary": "List Tool Calls Then Generate Full Report",
  "generated_title": "List Tool Calls Then Generate Full Report",
  "created_at": "2026-08-12T19:08:38.667534Z",
  "updated_at": "2026-08-13T00:02:02.455106Z",
  "num_messages": 368,
  "num_chat_messages": 164,
  "current_model_id": "grok-4.6",
  "chat_format_version": 1,
  "head_branch": "main",
  "agent_name": "grok-build-plan",
  "session_kind": "subagent"
}
```

- `info.id` and `info.cwd` give the session id and the project path without decoding the directory name.
- `created_at` and `updated_at` are the session times. They are the fallback when a message has no correlated event.
- `generated_title` is a readable title. It is absent on subagent sessions and on sessions that have not been titled yet, so fall back to `session_summary`, then to the first user query.
- `session_kind` is present only on subagent sessions.

## Auxiliary files

| File | Verdict |
|---|---|
| `chat_history.jsonl` | Required. The content spine. Complete and untruncated. |
| `updates.jsonl` | Required. Per-message timestamps, grok's own tool classification, and token usage. See below. |
| `summary.json` | Required. Identity, times, title, subagent gate. |
| `events.jsonl` | Useful. Tool success or failure outcome, correlated by `tool_call_id`. |
| `subagents/*/meta.json` | Useful. Enriches the `spawn_subagent` tool rendering with the description, prompt, status, and duration. |
| `terminal/*.log` | Optional. Per-command output, named `call-<tool-call-id>-<n>.log`. |
| `rewind_points.jsonl`, `hunk_records.jsonl`, `prompt_context.json`, `resources_state.json`, `signals.json`, `announcement_state.json`, `system_prompt.txt`, `recap_requests/` | Ignore for markdown. |

## updates.jsonl

This is the Agent Client Protocol event stream, one JSON object per line. Every line carries `timestamp` (Unix seconds) and `_meta.agentTimestampMs` (milliseconds), which makes it the only source of per-message timing.

| `params.update.sessionUpdate` | Carries |
|---|---|
| `user_message_chunk` | The user's prompt **without** the `<user_query>` wrapper, plus `_meta.promptIndex`. |
| `agent_message_chunk` | Assistant text, plus `_meta.promptId`. |
| `agent_thought_chunk` | Reasoning text. |
| `tool_call` | `toolCallId`, `title`, `rawInput` as a **parsed object**, and `_meta["x.ai/tool"]` with grok's own `kind`, `label`, `namespace`, and `read_only`. |
| `tool_call_update` | Progress and completion for a tool call. |
| `turn_completed` | `stop_reason` and a `usage` object with `inputTokens`, `outputTokens`, `cachedReadTokens`, and `reasoningTokens`. |
| `subagent_spawned` / `subagent_finished` | Subagent lifecycle, with parent and child ids. |
| `plan`, `session_recap`, `task_backgrounded`, `task_completed`, `scheduled_task_created`, `scheduled_task_deleted` | UI state. |

`_meta["x.ai/tool"].kind` is grok's own tool taxonomy, observed as `read`, `write`, `edit`, `execute`, `list`, `search`, `search_tool`, `task`, `plan`, `monitor`, `web_fetch`, `use_tool`, `image_gen`, `image_to_video`, `reference_to_video`, `workflow`, `background_task_action`, `kill_task_action`, and `other`.

The provider classifies a tool by its name first, because `chat_history.jsonl` is always present and complete, and falls back to this `kind` when the name is unrecognized. That fallback is what keeps MCP and future tools from rendering as `unknown`.

## CLI surface

| Flag | Meaning |
|---|---|
| `-p, --single <PROMPT>` | Headless single turn, prints to stdout and exits. |
| `-r, --resume [<ID_OR_TITLE>]` | Resume by session id or title, or the most recent when omitted. |
| `-c, --continue` | Continue the most recent session for the cwd. |
| `-s, --session-id <UUID>` | Use a specific UUID for a **new** session. |
| `--fork-session` | On resume, create a new session id instead of reusing it. |
| `--cwd <CWD>` | Working directory. |
| `--permission-mode <MODE>` | One of default, acceptEdits, auto, dontAsk, bypassPermissions, plan. |
| `--output-format <FMT>` | plain, json, streaming-json, streaming-messages-json. |
| `--version` | Prints e.g. `grok 1.0.3 (1a29d5bc12d4) [stable]`. |
