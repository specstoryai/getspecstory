# Cursor Cloud Agent session format

A Cursor Cloud Agent runs in Cursor's backend. Its conversation is never written to the user's machine, so the provider's native store is the Cloud Agents REST API at `https://api.cursor.com`, and a local mirror of that API is what every read path parses. Everything below was observed against the live API on 2026-10-13 (API `info.version` 1.0.0, v1 public beta plus legacy v0). The captures are in `testdata/` and `examples/captures/`.

## Baseline version

The API exposes no agent version. `CheckResult.Version` is empty, so `check` omits the version line as it does for IDE providers, and `ProviderInfo.Version` is `"unknown"`. The factory sensor tracks the OpenAPI document's `info.version` (`1.0.0`). Hook payloads inside the VM report `cursor_version 1.0.0`, which is not reachable from outside the VM.

## Authentication and limits

- `Authorization: Bearer <key>`, where the key comes from `https://cursor.com/dashboard/api`. The provider reads `CURSOR_API_KEY`, and otherwise `cursor_api_key` under `[providers]` in `config.toml` (user or project).
- The documented limit is 20 requests per minute, but it is not enforced at that rate: on 2026-10-15, about 520 read requests in six minutes, peaking at 17 per second, all returned `200`. The OpenAPI document promises `Retry-After` and `X-RateLimit-Limit`, `-Remaining` and `-Reset` headers on `429`; no rate-limit headers appear on successful responses. The client therefore sends requests one at a time without pacing, and retries `429` responses after `Retry-After` (else `X-RateLimit-Reset`, else exponential backoff), holding every caller in the process meanwhile. A GET also retries `5xx` responses and transport failures (DNS, connection reset, timeout) with the same backoff. A launch (`POST`) retries only `429`, because after a `5xx` or a dropped connection the agent may already exist, and resending would start a second paid run. A cancelled context is never retried.
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

Any other name, including `pr_management` (seen once in the research spike but never captured), renders as JSON with type `unknown`.

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

## Run

`specstory run cursorcloud` launches a new agent with `POST /v1/agents` instead of opening the bare agents page, because that page cannot be told which repository to use: an agent started there without one lands in a scratch repository (`origin.cursor.com/git/<user>/tmp-….git`) that never matches the project. The launch sets `repos[0].url` to `https://` plus the normalized origin and `startingRef` to the checked-out branch. The API validates the branch before creating anything (`400 validation_error`, "Branch '…' does not exist in repository …"), so an unpushed branch is retried once without `startingRef`, starting from the default branch. The API cannot create an agent without a prompt, so the agent is named `SpecStory session` and opens with a fixed no-tools greeting; both are skipped when choosing the session title and slug, and an agent with only the greeting yields no session yet. A launch is never resent after a 5xx or a transport failure, because the first attempt may already have started a paid run. `run` then waits for the first run to leave `CREATING` (polling the run list each second, for at most 30 seconds), opens `https://cursor.com/agents/<id>` and watches. Cursor's agent page renders blank, and stays blank until reloaded, when it is opened while the run is still `CREATING`; a resume that launches a copy waits the same way.

## Resume

Resuming into `cursorcloud` continues the same agent when it can, and otherwise copies the session into a new agent:

- A cloud agent's own session reopens that agent (`https://cursor.com/agents/<id>`), where a follow-up run can be started, and then watches. This covers a local session (resumed by agent id) and one from SpecStory Cloud (whose reconstruction remembers the source agent). An archived agent is unarchived first (`POST /v1/agents/{id}/unarchive`).
- The agent is copied instead when Cursor no longer has it (`404`) or it is on another repository, since this project's watch would never pick up its activity. A local session is copied from the mirror.
- Any other agent's session is copied. `ReconstructSession` flattens it with the shared turn flattening into one prompt (`resumePromptHead`, then `## User` / `## Agent` sections) and returns it as a pending launch: session id `pending-<uuid>`, stored as JSON in the mirror's `pending/` directory, behind the mirror's `.gitignore`. `ExecAgentAndWatch` reads and deletes that file, then launches. The copy is named after the source slug, and its transcript prompt is skipped when choosing the title and slug, like the `run` greeting.
- A copy's prompt is capped at 1,000,000 characters (`maxResumePromptChars`). Probing on 2026-10-18 found no API limit: launch prompts of 1k, 10k, 100k, 1M and 10M characters were all accepted (`201`), and the OpenAPI document sets no `maxLength` on `prompt.text`. Past about 1M characters (~250k tokens) a transcript no longer fits a model's context, so a larger one is refused before anything is billed. A session from a cloud agent is checked only when it has to be copied.
- The picker labels `cursorcloud` "starts a new paid cloud run" for other agents' sessions (`spi.ResumeTargetNoter`). Resuming a cloud session into a local agent works through the shared `SessionData` flattening, from any directory: with no `OriginCwd` on the index row, resume reads the session by its mirror path (`agent.json`) through `GetAgentChatSessionByPath`.
