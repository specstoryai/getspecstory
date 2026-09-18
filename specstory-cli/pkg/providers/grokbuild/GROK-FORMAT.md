# Grok Build session format

The supported baseline is **`grok 1.0.34 (3736acbc8658) [stable]`**, verified on macOS arm64. The provider ID is `grok`. The CLI version is not recorded in the session summary or transcript, so normalized `ProviderInfo.Version` is `unknown`; `current_model_id` and assistant `model_id` identify the model, not the application. Assistant messages keep their native model; reasoning/backend records without a model field remain unlabelled instead of borrowing a potentially different historical model.

## Native store and write lifecycle

Grok uses `$GROK_HOME`, defaulting to `~/.grok`. Sessions live under:

```text
sessions/<percent-encoded-canonical-cwd>/<session-uuid>/
    chat_history.jsonl
    summary.json
    updates.jsonl
    events.jsonl
    subagents/<child-id>/meta.json
```

The project name percent-encodes UTF-8 bytes outside the RFC 3986 unreserved set; spaces become `%20`, not `+`. Discovery decodes project names and canonicalizes local paths for comparison. A `.cwd` sidecar is also recognized for non-path group names. Global enumeration accepts only this exact group/UUID/transcript depth, ignoring archive and group-level lookalikes. It prefers `summary.json.info.cwd`; project matching never derives ownership from a file touched by a tool.

`chat_history.jsonl` is the durable conversation. The native agent appends turns and can rewrite the transcript; readers therefore parse its current full contents instead of retaining a byte offset. Summary and event/update sidecars change independently during a turn. File locks, `rewind_points.jsonl`, `system_prompt.txt`, `prompt_context.json`, `announcement_state.json`, `signals.json`, `usage.json`, and `terminal/` output files are not alternative conversation sources. A directory can exist before its first usable conversation/summary; the watcher waits for metadata before publishing it. No scratch/checkpoint file is used as a fallback transcript.

The watcher establishes an unchanged baseline before launching the agent, uses fsnotify on the project and at most 20 session directories, and reconciles signatures every 30 seconds. Signatures cover the transcript, summary, updates, events, and named subagent metadata. Missing directory ancestors are adopted as they arrive. Shutdown reconciles disk state and joins synchronous callbacks, including when the agent returns a nonzero status. It never recursively watches child transcripts or terminal logs.

## Transcript records

Records have a `type` discriminator and no timestamp. All accepted native JSON, including unfamiliar fields and unsupported content, is retained in `RawData`. Malformed or oversized JSONL records are warned about and skipped, with subsequent records retained; the shared limit is 64 MiB per record.

| Type | Stored content and interpretation |
|---|---|
| `system` | A string containing runtime instructions; retained raw, omitted from conversation Markdown. |
| `user` | An array of content parts. A real text prompt is wrapped in `<user_query>…</user_query>`. `prompt_index` joins its timestamp to updates. Synthetic records (`synthetic_reason`, such as `system_reminder` or `task_completed`) are scaffolding, not human prompts. Embedded tags inside a real prompt remain part of that prompt. |
| `reasoning` | `summary` contains readable `{type:"summary_text",text:…}` parts. Opaque `encrypted_content` remains raw and is never rendered as thought text. |
| `assistant` | String `content`, optional `model_id`, `model_fingerprint`, `reasoning_effort`, and `tool_calls`. Narration precedes tool calls from the same record. |
| `tool_result` | `tool_call_id` correlates the result to its invocation. Native observed results are strings. Unfamiliar structured payloads are preserved generically instead of dropped. |
| `backend_tool_call` | Server-side activity with a `kind` object. A 1.0.34 web-search record carried `tool_type:"web_search"`, an action/query/sources object, ID, and `status:"in_progress"`. That capture did not establish successful backend search output. |

`assistant.tool_calls` entries contain `id`, `name`, and **JSON encoded as a string** in `arguments`, requiring another decode. Malformed argument strings remain visible through generic rendering. The parser uses native call IDs, not textual matches in tool output, to pair calls and results.

Images and other nontext user parts are retained in raw/debug exports. The shared content schema represents text and thinking; embedded image bytes are not rendered or reconstructed as images. Unknown record kinds remain raw and are logged. Grok's `/context` executed during the audit without adding a human prompt or command scaffolding to `chat_history.jsonl`.

## Summary and sidecars

Session identity is the native directory UUID. A missing, invalid, or different UUID in the summary cannot replace it; a non-UUID directory is rejected. The original summary remains available as raw data. Reconstruction creates the UUID directory with an exclusive directory-creation attempt and checks the resulting entry with `Lstat`, rejecting directory links and other non-directory entries before writing a summary or returning a transcript path. This handles an already occupied path or competing creator; it does not make the later path-based SPI write atomic against arbitrary concurrent filesystem replacement.

`summary.json` carries `info.id`, `info.cwd`, `created_at`, `updated_at`, `session_summary`, optional `generated_title`, message counts, `current_model_id`, and `chat_format_version`. `session_kind:"subagent"` distinguishes a child agent from a human session; ordinary headless sessions record `session_kind:"headless"`. Child sessions are excluded from project lists and exports. The parent's `subagents/<id>/meta.json` can enrich its invocation with child type/status/duration.

Times normalize to RFC 3339 milliseconds. Missing summary timestamps fall back to the transcript's modification time. No parser path uses the current wall clock to invent historical message times.

`updates.jsonl` contains `timestamp` (Unix seconds), `method`, and `params`:

- `params._meta.agentTimestampMs` is the preferred event time; `params._meta.promptId` links agent messages to turn usage.
- `params.update.sessionUpdate` identifies `user_message_chunk`, `agent_message_chunk`, `agent_thought_chunk`, `tool_call`, `tool_call_update`, or `turn_completed`, among other UI events.
- User chunks carry `params.update._meta.promptIndex`. The first chunk establishes the prompt time; absent indices are retained independently and cannot overwrite index zero.
- Tool records identify `toolCallId` and may carry Grok's own tool taxonomy. SpecStory assigns its schema type from the known tool's purpose; unfamiliar tools remain `unknown`.
- `turn_completed` carries `prompt_id` and `usage` with input/output/cached-read/reasoning token counts. Totals are attached once to the exchange linked by that prompt ID, not matched by positional turn counts.

`tool_call_update.status` supplies completed/failed outcomes, including permission cancellation without a completion event. Result text alone is not proof of success. `events.jsonl` supplies additional outcomes from `tool_completed` / `mcp_tool_call_completed`, correlated by `tool_call_id`. A failed native tool result renders as an error; a pending backend call retains its pending status. Command process exit status also remains in native result text.

Raw transcript and debug output come from the same accepted parsing snapshot. `--debug-raw` writes pretty-printed transcript records as `1.json`, `2.json`, etc., plus `native-sidecars.json` containing the summary, ordered update/event records, and subagent metadata used by conversion. Unknown fields survive. Refresh removes obsolete numbered records, preserves CLI-owned `session-data.json`, and respects `--debug-dir`.

## Tool inventory and rendering

The harness emits `available_commands.tools` in `--output-format streaming-json`; the baseline declares 27 tools. The checked-in [inventory and native fixture](testdata/session-1.0.34/README.md) are the source of regression cases. An isolated factory run produces the same inventory without workstation plugins/MCP configuration.

Observed file reads preserve ranges; grep preserves options and fences its XML-like result; writes retain complete content; search/replace retains both complete edit halves; shell/monitor calls preserve command settings, Unicode, and nested fences while stripping terminal controls. Todo updates recover text from prior items. MCP discovery/dispatch retains the tool name and arguments; native errors take precedence over success formatting. Every specialized renderer retains unfamiliar input/result fields, and undeclared/unobserved payloads have generic rendering. Media payloads do not have speculative bespoke formatting.

The [review report](../../../docs/GROK-REVIEW.md) separates declared, exercised, and unexercised tools, successful paths from captured failures, and current QA limits.

## Native and cross-agent resume

`grok --resume <id>` resumes a conversation by ID; `--continue` selects the latest. `--session-id` creates a new conversation and does not resume it. SpecStory's selected resume ID overrides a configured `--resume` / `-r` value.

Cross-agent reconstruction uses `spi.PrepareTurns`: the migration note comes first, then the prepared user/agent text in order. Thinking and rendered tool activity become ordinary assistant text. Source system instructions, command scaffolding, native tool protocol, encrypted reasoning, historical models, usage, and account metadata are not replayed.

A newly reconstructed directory needs a transcript and usable summary. Once Grok has written update history, its native loader can also recover a missing transcript from that history: the file-removal probe still recalled the passphrase without `chat_history.jsonl`, but failed without `summary.json`. SpecStory deliberately exports the durable current transcript rather than replaying recovery data. The provider writes wrapped user text with consecutive prompt indices and ordinary assistant text. It omits a system record and assistant model IDs: 1.0.34 supplies its own current system prompt and resumed these records without warnings. `summary.json` requires `session_summary` and `current_model_id`; an empty `current_model_id` is accepted and avoids attributing imported history to an invented model. Source-session provenance is carried as `specstorySourceSessionId` on reconstructed user records. Files use private permissions and a fresh UUID.

Real baseline tests resumed the minimal conversation, recalled its passphrase, and appended to the same session. The CLI cross-agent tests also resumed Claude thinking/tool/slash-command content into Grok and Grok thinking/tool content into Claude, with successful recall in both directions. These are native loader tests, in addition to serializer tests.

## Factory

The executable scripts under `factory/` track the public stable manifest named by the [official installer](https://x.ai/cli/install.sh), install an exact standalone binary under `$HOME/.local/bin/grok`, and enumerate the harness declaration in a bounded headless run. Self-update is disabled. `list-tools` accepts only the named `GROK_AUTH_JSON` credential (a dedicated factory account's native auth JSON), unsets the duplicate JSON environment variable before launching Grok, and removes its temporary private credential file on exit. It does not read workstation credentials. Factory secret provisioning remains an operational enrollment step; no credential is checked in.
