# Cursor Cloud Agent session format

A Cursor Cloud Agent runs in Cursor's backend. Its conversation is never written to the user's machine, so the provider's native store is the Cloud Agents REST API at `https://api.cursor.com`, and a local mirror of that API is what every read path parses. Everything below was observed against the live API on 2026-10-13 (API `info.version` 1.0.0, v1 public beta plus legacy v0). The captures are in `testdata/` and `examples/captures/`.

## Baseline version

The API exposes no agent version. `CheckResult.Version` is empty, so `check` omits the version line as it does for IDE providers, and `ProviderInfo.Version` is `"unknown"`. The factory sensor tracks the OpenAPI document's `info.version` (`1.0.0`). Hook payloads inside the VM report `cursor_version 1.0.0`, which is not reachable from outside the VM.

## Authentication and limits

- `Authorization: Bearer <key>`, where the key comes from `https://cursor.com/dashboard/api`. The provider reads `CURSOR_API_KEY`, and otherwise `cursor_api_key` under `[providers]` in `specstory.toml`.
- The documented limit is 20 requests per minute. The OpenAPI document promises `Retry-After` and `X-RateLimit-*` headers on `429`, but no rate-limit headers appeared on successful responses. The client spaces every request 3 s apart across the whole process and retries `429` and `5xx` responses, honouring `Retry-After` when it is present. Transport failures (DNS, connection reset, timeout) take the same backoff; a cancelled context is never retried.
- Accounts on Privacy Mode (Legacy) get `400 validation_error` "Cloud agent is not supported in Privacy Mode (Legacy)" from every agent call.

## Endpoints read

| Call | Shape |
| --- | --- |
| `GET /v1/me` | `{apiKeyName, userId, createdAt, userEmail, userFirstName, userLastName}`. Check reports `apiKeyName` only |
| `GET /v1/agents?limit=100&cursor=` | `{items:[agent], nextCursor?}`, newest first. `agent` = `{id: "bc-<uuid>", name, status: ACTIVE\|IDLE\|ARCHIVED, env, repos:[{url}], workOnCurrentBranch, autoCreatePR?, openAsCursorGithubApp, skipReviewerRequest, url, createdAt, updatedAt, latestRunId}` |
| `GET /v1/agents/{id}` | one `agent` |
| `GET /v1/agents/{id}/runs` | `{items:[run]}`, newest first. `run` = `{id: "run-<uuid>", agentId, status: CREATING\|RUNNING\|FINISHED\|ERROR\|CANCELLED\|EXPIRED, createdAt, updatedAt, durationMs?, git:{branches:[{repoUrl, branch, prUrl?}]}}`. The single-run GET adds `result` (the final text); the list omits it |
| `GET /v1/agents/{id}/runs/{runId}/stream` | SSE, described below. Returns `410 {"error":{"code":"stream_expired","message":"Run stream is no longer available"}}` once retention has passed |
| `GET /v0/agents/{id}/conversation` | `{id, messages:[{id, type: user_message\|assistant_message, text}]}`, covering every run of the agent, text only, with no timestamps. Kept indefinitely |
| `GET /v1/agents/{id}/usage` | `{totalUsage:{inputTokens, outputTokens, cacheWriteTokens, cacheReadTokens, totalTokens}, cost:{rawCostCents, chargedCents}, runs:[{id, usageUuid, usage, cost}]}` |

`repos[].url` on v1 is `https://github.com/org/repo`, while `git.branches[].repoUrl` and v0 `source.repository` are `github.com/org/repo`. Both normalize to the same string as the project's origin.

## Run stream grammar

Each SSE block has an optional `id: <epochMillis>-<seq>`, an `event: <name>` and a `data: <json>` line. The id is the only timestamp in the stream. Ids are not unique: a `thinking` event and its `interaction_update` `thinking-delta` share one. The stream is retained for `x-cursor-stream-retention-seconds: 86400` after a run ends. A finished run replays in full from the start, and the replay matches the live capture block for block, except that the opening `status` says `FINISHED` and live-only `heartbeat` events are absent.

| Event | Data | Rendering |
| --- | --- | --- |
| `status` | `{runId, status}` (no id) | none; the first and last block of a stream |
| `heartbeat` | `{}` (no id) | none; sent on a quiet live stream |
| `interaction_update` `user-message-appended` | `{userMessage:{type, session_id, text}}` | the user prompt, which is the only place the stream records it |
| `interaction_update` `token-delta`, `text-delta`, `thinking-delta` | deltas | none; these duplicate the typed events |
| `interaction_update` `step-started`, `step-completed`, `thinking-completed`, `tool-call-started`, `tool-call-completed`, `tool-requests-listed`, `partial-tool-call`, `turn-ended` | lifecycle | none |
| `thinking` | `{text}` delta | consecutive deltas concatenate into one thinking part |
| `assistant` | `{text}` delta | consecutive deltas concatenate into one text part |
| `tool_call` | `{callId, name, status: running\|completed, args?, result?}` | one tool message per `callId`, placed at its first event |
| `result` | `{runId, status, text, durationMs, git}` | its time ends the exchange; `text` repeats the final assistant text and is not rendered |
| `done` | `{}` | marks the end of the stream; a mirrored capture ending in `done` is whole |

A `tool_call` arrives several times per call. A `running` event may have no `args` yet while they stream in. The last event's fields win. A `running` event can carry a provisional `result: {error: {error: "Invalid arguments"}}` with `args: {toolCallId: "unknown-tool-call-id"}`, which the completed event then replaces, so results are taken only from non-`running` events.

Result shapes:

- Success: `{success: {...}}`.
- Tool failure: `{error: {error: "..."}}` (`delete_file`, `grep_search`, `web_fetch`, which also adds `url`) or `{error: {errorMessage: "..."}}` (`read_file`).
- A shell command that exits non-zero: `{failure: {command, exitCode, stderr, interleavedOutput, executionTime}, isBackground}`.

The provider removes the plumbing keys `toolCallId`, `conversationId` and `requestId` from inputs. Every tool path sits under the VM checkout `/workspace`. Paths are rendered relative to it, and path hints are rewritten onto the local project before they are made relative.

## Tools

The names recorded in `tool_call.name` differ from the names the model sees. `testdata/tools.txt` holds both lists and the mapping between them.

| Name | Type | Input rendered | Result rendered |
| --- | --- | --- | --- |
| `run_terminal_cmd` | shell | `command` in a bash fence, plus `workingDirectory` when it is not `/workspace`. The summary uses `description` | `interleavedOutput` (or `stdout` + `stderr`) in a text fence. `failure` adds `Exit code`, and `isBackground` adds a note |
| `read_file` | read | `path` in the summary | `content` fenced by file extension, with a line range when partial |
| `edit_file` | write | the result's `diffString` as a diff, or `streamContent` while the call is still streaming | none (the diff is the body) |
| `delete_file` | write | `path` | `prevContent` as removed lines |
| `file_search` | search | `globPattern` in the summary, plus `targetDirectory` | file list |
| `grep_search` | search | `pattern` in the summary, plus `path` and flags | `file:line: content` lines from `workspaceResults` |
| `web_search` | search | `searchTerm` | each reference's `chunk`, which is already a markdown link list plus a synthesis |
| `web_fetch` | read | `url` | `markdown` in a markdown fence |
| `todo_write` | task | `todos[]` as a checklist (`TODO_STATUS_*` mapped) | none |
| `mcp` | generic | `serverIdentifier`, `toolName`; other arguments as JSON | `content[].text.text`; `structuredContent` repeats it and is omitted |
| `get_mcp_tools` | generic | `server`, `toolName` | `content` (a JSON string), plus `outputFilePath` |
| `await` | generic | `blockUntilMs` | `complete.runtimeMs` |
| `generate_image` | generic | `description`, `aspectRatio`, `filePath` | saved `filePath` |
| `write_canvas` / `read_canvas` | generic | `title` and `contents` / `canvasId` | link to the canvas `url`, plus `source` |
| `pr_management` | generic | seen once in the research spike, but not captured; rendered generically | generic |

Any other name renders as JSON with type `unknown`.

## Session model

- One agent is one session. The id is `bc-…`, `CreatedAt` is `agent.createdAt`, `UpdatedAt` is `agent.updatedAt`, and the slug comes from the first user prompt. `Name` is `agent.name`, which Cursor derives from the prompt when the launcher gives none.
- One run is one exchange, in `createdAt` order, and `ExchangeID` is the run id. `StartTime` is the first event id, and `EndTime` is the `result` event id. Metadata holds `runId`, `status`, `durationMs`, `branch`, `prUrl` and `agentUrl`.
- `Model` is empty because the API does not expose it. `Usage` is nil because usage is per run, not per message. Both stay in `RawData`.
- When a run's stream was never captured, or a finished run's capture is partial and the stream has expired, the exchange is built from the v0 conversation instead. That fallback is text only, and the exchange is marked `"source": "v0-conversation"`. Messages with ids `turn-N:…` belong to run index N; the first user message has a bare UUID id and belongs to turn 0. The user message takes the run's `createdAt` as its time, and agent messages have none.
- `WorkspaceRoot` is the caller's project path. `GlobalSessionRef.OriginCwd` is empty, because the API records a repository and never a directory. `GlobalSessionRef.OriginRepoURL` carries the agent's repository instead, and reindex hashes it into the same `git_id` a local clone of that repository resolves to, then indexes the session's text through `GetAgentChatSessionByPath`.

## The mirror (write lifecycle)

The mirror defaults to `<project>/.specstory/cursorcloud/`. The `cursorcloud_mirror_dir` setting or `--user-data-dir cursorcloud:<path>` relocates it. A mirror that resolves to a directory inside the project, whether by default or by configuration, gets a `.gitignore` of `*` (verified and rewritten on every sync), because it holds unredacted raw transcripts. A mirror outside the project, or one pointed at the project root itself, gets none.

```text
agents/<agentId>/agent.json                  agent object (written last in a refresh)
agents/<agentId>/conversation.json           v0 conversation
agents/<agentId>/usage.json                  usage
agents/<agentId>/runs/<runId>.json           run object from the run list
agents/<agentId>/runs/<runId>.events.jsonl   one {"id","event","data"} object per SSE block
```

- Every write is atomic (a temporary file, then a rename), and a file whose bytes are unchanged is not rewritten, so a sync that finds nothing new leaves every byte and mtime as it was.
- Runs, conversation and usage are re-fetched only when `agent.updatedAt` differs from the mirrored `agent.json`.
- A finished run whose events are missing or partial is replayed while it is within 24 h of its `updatedAt`. A `410` leaves the partial capture in place. A whole capture is never fetched again.
- `sync` never attaches to an unfinished run, because reading its stream blocks until the run ends. `watch` attaches live, appends events, and rewrites the file atomically after each burst. A listing refused with a 4xx other than 429 (rejected key, Privacy Mode) ends `watch` and `run` with that error, because every later poll would fail the same way; transport, 429 and 5xx failures are retried on the next poll.
- Detection (`sync`, `list`) consults the mirror before the API, so histories already mirrored stay readable without a key.
- An agent directory without `agent.json` is not an agent yet. Readers skip it.

## Resume

Same-agent resume (`specstory resume` of a cloud session into `cursorcloud`) opens `https://cursor.com/agents/<id>`, where a follow-up run can be started, and then watches. Resuming another agent's session into `cursorcloud` is unsupported: the only way to create a cloud agent conversation is to launch a new paid run, so `ReconstructSession` returns `spi.ErrReconstructionUnsupported`. Resuming a cloud session into a local agent works through the shared `SessionData` flattening, from any directory: with no `OriginCwd` on the index row, resume reads the session by its mirror path (`agent.json`) through `GetAgentChatSessionByPath`.
