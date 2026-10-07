# Cursor Cloud Agents: why SpecStory misses them, and how to close the gap

Research spike, 2026-10-06, in two phases: desk research (docs, forum, Cursor app bundle strings, local databases) followed by a hands-on spike that launched real cloud agents through the Cursor API against a throwaway repo. Covers the CLI (this repo) and the VS Code / Cursor extension (`specstory-monorepo`), which since February 2026 only wraps the CLI (`sync` then `watch`) and does no Cursor reading of its own.

## 1. The problem in one paragraph

Cursor Cloud Agents (formerly Background Agents) run the agent loop, inference and conversation state in Cursor's cloud. The transcript is stored server-side only, "kept indefinitely", and is never written to the user's machine. SpecStory's two Cursor providers only read local stores: `cursoride` reads `globalStorage/state.vscdb` (`composerData:` / `bubbleId:`) plus `workspaceStorage`, and `cursorcli` reads `~/.cursor/chats/<md5(cwd)>/<id>/store.db`. A cloud agent launched from cursor.com/agents, Slack, GitHub, Linear, the iOS app, the API, Automations, or the `&` prefix in the CLI never touches either store, so SpecStory never sees it. This is a data-location gap, not a parsing bug. The hands-on spike confirmed every part of this: two agents, three runs, and nothing SpecStory reads was written anywhere.

## 2. What was verified

### 2.1 Where cloud agent data lives

| Surface | Where the transcript is | Reachable by SpecStory today? |
| --- | --- | --- |
| Cursor-hosted cloud VM | Cursor backend, encrypted per agent, indefinite retention (Enterprise can cap at 90 days). Inside the VM there is no transcript file at all (verified, see 2.3) | No |
| "My Machines" / Team Pool self-hosted workers | Still the Cursor backend. The worker only executes tool calls; docs say workers write no transcripts or chat files locally | No |
| Desktop IDE, cloud agent viewed in a window | No `composerData:` entry, no `bubbleId:` rows, no `~/.cursor/chats` store, no `agent-transcripts` file (verified). The IDE caches the agent list in `ItemTable` (`cloudAgentRepository.agents.<userId>`, `glass.cloudAgentProjects.v1`), protobuf state in an IndexedDB named `cloudAgentCache`, and a flattened, HTML-escaped text rendering in `conversation-search.db` (`source='cloud-cache'`) only for agents the user has actually opened | No for the providers; a lossy text copy exists only for viewed agents |
| "Move to Local" handoff | Becomes an ordinary local agent; the chat context is carried over. Not exercised in the spike (GUI only, and Cursor staff say the option only appears when a local agent for the same repo is already open) | Only after a manual handoff |
| Cursor mobile app | Device cache only | No |

Sources: `cursor.com/docs/cloud-agent`, `/docs/cloud-agent/security`, `/docs/cloud-agent/self-hosted`, forum thread 165486 (Cursor staff: cloud sync "does not cover this local back and forth"), strings in `/Applications/Cursor.app` `workbench.desktop.main.js` (3.13.25), and the spike in section 6.

Cursor also writes `~/.cursor/projects/<slug>/agent-transcripts/<id>/<id>.jsonl` for local IDE and CLI agents. This is a flattened text-only transcript (no tool results). The cloud VM exports `AGENT_TRANSCRIPTS=/home/ubuntu/.cursor/projects/workspace/agent-transcripts`, but that directory is never created.

An account on "Privacy Mode (Legacy)" cannot use cloud agents at all. Every v0 and v1 agents call returns `400 validation_error: "Cloud agent is not supported in Privacy Mode (Legacy)"`. Customers reporting missing cloud agent histories are therefore on standard Privacy Mode, whose conversations are stored server-side.

### 2.2 The Cloud Agents REST API (`api.cursor.com`)

Available on all plans in beta; cloud agent runs themselves need a paid plan. Auth is a user API key or a team service-account key (`crsr_…`), Basic or Bearer. Documented rate limit is 20 requests per minute; no rate limit headers are returned. All shapes below were observed live.

| Need | v1 (current, public beta) | v0 (legacy, still live, no sunset date) |
| --- | --- | --- |
| Identity | `GET /v1/me` returns `apiKeyName`, `userId`, `userEmail`, names | `GET /v0/me` |
| List agents | `GET /v1/agents?limit&cursor&prUrl&includeArchived` returns `{items:[…], nextCursor}` sorted newest first; each item has `id` (`bc-…`), `name`, `status` (`ACTIVE` / `IDLE` / `ARCHIVED`), `repos[].url`, `url`, `createdAt`, `updatedAt`, `latestRunId` | `GET /v0/agents` returns `{agents:[…]}` with `status`, `source{repository, ref}`, `target{branchName, url, prUrl}`, `linesAdded`, `filesChanged` |
| Runs | `GET /v1/agents/{id}/runs[/{runId}]` with `status`, `durationMs`, `git.branches[]{repoUrl, branch, prUrl}`. `POST /v1/agents/{id}/runs` adds a follow-up turn to the same agent and VM | none |
| Full transcript | **No conversation endpoint.** `GET /v1/agents/{id}/runs/{runId}/stream` is SSE with `id:` lines (`<ms>-<seq>`) for `Last-Event-ID` resume. Events: `status`, `assistant` (text deltas), `thinking`, `tool_call` with `args` and `result` (including `stdout` of shell commands), `interaction_update` (step, token and tool lifecycle), `result` (`durationMs`, `git.branches`), `done`. **Retention is 24 hours** (`x-cursor-stream-retention-seconds: 86400`) and a finished run replays in full from the start within that window | `GET /v0/agents/{id}/conversation` returns `{id, messages:[{id, type, text}]}` with `type` `user_message` / `assistant_message`. Text segments only: a run with 25 tool calls yielded 4 messages. Follow-up runs append to the same list. Message ids are stable across calls (`turn-0:step:22:assistant`, `turn-1:user`); the forum claim that they change did not reproduce |
| Webhooks | "coming" | `statusChange` on `FINISHED` / `ERROR` only, set at launch time, no transcript in payload |
| Usage | `GET /v1/agents/{id}/usage` returns per-run tokens and `cost.chargedCents` | none |

Cost observed: the three spike runs were billed at 43, 23 and 12 cents, so a typical short cloud run costs tens of cents. A provider that only reads data costs nothing.

### 2.3 Inside the cloud VM

Verified by having the agents run probe commands and report the output:

- Environment: `CURSOR_AGENT=1`, `CURSOR_CONVERSATION_ID=<bc-id>`, `CURSOR_REQUEST_ID`, `CURSOR_AGENT_SOCKET=/run/cursor/api.sock`. The metadata socket answers `agent/id`, `agent/name`, `agent/source` (`API`), `agent/runtime` (`managed`), `turn/id` (the run id), `workspace/repo-url`, `workspace/branch-name`.
- No transcript exists in the VM. `~/.cursor` holds only `agent-hooks`, `bin`, `projects/workspace/{canvases,terminals}` and `skills-cursor`. A filesystem-wide search for `*.jsonl` or `store.db` found nothing. `~/.cursor/hooks.json` is absent, so only repo hooks apply.
- Repo hooks from `.cursor/hooks.json` do fire: `afterShellExecution` (command and output), `postToolUse` (`tool_name`, `tool_input`, `tool_output`), `afterAgentResponse` (the full assistant `text`), and `stop` (`generation_id` is the run id). `sessionStart` never fires. Every payload has `transcript_path: null` and `user_email: null`, with `conversation_id` set to the agent id.
- `/tmp` and the working tree persist between runs of the same agent while it is `IDLE`, so a follow-up run sees files from the previous run.

### 2.4 Official export paths

The only official transcript export is the Enterprise OpenTelemetry "Conversation content" family, opt-in, which today covers Cloud Agents only, truncated to 32 KiB per message and 8 KiB per tool I/O side. The Admin and Analytics APIs expose usage metadata (`cloudAgentId`, `conversationId`) but never content. The web UI has no export or share for cloud sessions.

## 3. What SpecStory has to work with

- Every provider today is file-based. No provider imports `net/http`, there is no credential config, and the provider guide mandates fsnotify watchers. A cloud provider is a new pattern.
- Project identity is `git_id`, a hash of the normalized origin URL (`docs/PROJECT-IDENTITY.md`). The API's `repos[].url` normalizes the same way, so cloud agents can be attributed to the right project without any local cwd.
- Cloud sync (`pkg/cloud/sync.go`) already carries `rawData`, `sessionData`, `agentName`, and metadata; the Cloud Resume work means anything pushed as `SessionData` is browsable and resumable from any machine (`docs/CLOUD-RESUME.md`). Local markdown, however, is only produced by the CLI on the machine that captured the session.
- `specstory-sync` matches agents by substring (`agentName.includes(id)`); a new agent name containing "cursor" collides with the existing `cursor` entry and needs a distinct id.

## 4. Options

### A. New `cursorcloud` provider in the CLI, pulling from the Cursor API

The user supplies a Cursor API key (`CURSOR_API_KEY` or `specstory.toml`). On `sync`, the provider lists agents, keeps those whose `repos[].url` matches the current project's origin, and for every run updated since the last sync replays the v1 stream, which within 24 hours of the run contains the complete structured transcript including tool arguments and outputs. Runs older than 24 hours fall back to the v0 conversation endpoint, which gives user and assistant text only. On `watch`, the provider polls the agent list and attaches to the stream of any active run. Markdown lands in `.specstory/history/` as usual and existing cloud sync pushes it.

- Pros: covers every launch surface (web, Slack, GitHub, Linear, mobile, API). Reuses the whole provider → markdown → cloud pipeline. Works for local-only users with no SpecStory Cloud. Project attribution by repo URL is exact. Full fidelity for any run finished within the last day, which a daily `sync` or a running `watch` satisfies.
- Cons: new HTTP-provider pattern and credential handling. Runs more than a day old degrade to text only, and v0 may be sunset. v1 is beta and "may change". 20 rpm cap bounds polling. Requires the user to create an API key.

### B. Server-side poller in SpecStory Cloud (`specstory-sync`)

Users store a Cursor API key in SpecStory Cloud; a scheduled Worker lists agents and replays streams directly, always on.

- Pros: never misses the 24 hour window, no CLI needed, and a team service-account key could cover a whole team.
- Cons: SpecStory Cloud holds third-party API keys (security and support burden). Only helps SpecStory Cloud customers. Local `.specstory/history/*.md` does not appear until the CLI pulls it down, which is new work (today only resume pulls cloud `SessionData`). Project mapping must happen server-side. Builds a second ingestion pipeline outside the CLI.

### C. In-VM capture via repo hooks (`.cursor/hooks.json`)

Ship a hook script that forwards `afterAgentResponse` and `postToolUse` payloads to SpecStory Cloud, or writes `.specstory/history/` inside the VM so it rides along on the agent's branch and PR. The spike confirmed these hooks fire in the VM and together carry the assistant text plus tool I/O, which is enough to render a session.

- Pros: runs where the agent runs, independent of API retention windows.
- Cons: must be committed to every repo (or Enterprise-managed hooks). Writing history into the agent's branch changes the PR diff. Needs a SpecStory token as an environment secret. No `sessionStart` or transcript file, so the hook has to assemble the session itself from per-event payloads. Fragile across Cursor changes and invisible to users who did not set it up.

### D. Opportunistic desktop capture (extend `cursoride`)

Read the `cloud-cache` rows of `conversation-search.db` or the `cloudAgentCache` IndexedDB.

- Pros: no API key.
- Cons: the spike showed this only exists for agents the user has opened in a window, and the search body is a flattened, HTML-escaped text blob with no structure. LevelDB/protobuf parsing is brittle. Misses most launch surfaces. Not a solution on its own.

### E. Enterprise OpenTelemetry ingest

Accept Cursor's OTLP conversation-content export into SpecStory Cloud.

- Pros: official, admin-sanctioned, team-wide, no per-user keys.
- Cons: Enterprise only, opt-in by the Cursor admin, truncated content, cloud-only with the same local-markdown gap as B. Useful later for enterprise accounts, not a general fix.

## 5. Recommendation

Build **Option A** first, with **Option B as a follow-on** for SpecStory Cloud customers, and in parallel ask Cursor for a v1 conversation endpoint.

Why A: it is the only option that works for every launch surface and every user tier while reusing the existing provider, markdown and cloud-sync pipeline, so both deliverables fall out of one piece of work. The user gets history on their local machine in the repo's `.specstory/history/`, and SpecStory Cloud receives it through the sync path already in place. The spike raised A's ceiling: the 24 hour stream replay means a daily `sync` captures the full structured transcript, tool output included, without needing to be online during the run. The text-only v0 fallback only matters for runs older than a day that were never synced. Attribution by repo URL is exact and needs no cwd guessing.

Why B second rather than first: it closes the "machine off for more than a day" case but introduces key custody and a cloud-to-local pull that does not exist yet. Doing A first keeps B a thin addition rather than a parallel pipeline.

Why not C or D: C requires per-repo setup and alters PR diffs; D only sees agents the user opened in the desktop app and yields lossy text. Neither can be the primary path. E is worth tracking for enterprise accounts.

### Design notes for Option A

- Provider id `cursorcloud`, display name "Cursor Cloud Agent", session id = the `bc-…` agent id, `OriginCwd` empty unless a local clone with a matching origin exists. Pick a cloud-side id that does not substring-match `cursor`.
- Polling: one `GET /v1/agents` per interval, per-agent work only when `updatedAt` advances. Stay well under 20 rpm.
- Transcript source per run: v1 stream replay (primary), v0 conversation (fallback after 24 hours). Treat `tool_call` events keyed by `callId` as the tool record; `assistant` and `thinking` deltas concatenate into the message; `interaction_update` can be ignored for rendering.
- Raw data: the captured SSE events plus the v0 conversation JSON, stored together so a later re-render can improve.
- Session metadata should carry `url`, branch and PR URL from `runs[].git.branches`, which is what users want to click, and `cost.chargedCents` from usage if we want to show it.
- `ExecAgentAndWatch` and `ReconstructSession` return unsupported for v1 of the provider; resume into a local agent still works through the generic `SessionData` path.

## 6. Hands-on spike

Method: a personal API key, a private throwaway repo `specstoryai/cloud-agent-test` connected to Cursor's GitHub app, and three runs launched with `POST /v1/agents` and `POST /v1/agents/{id}/runs`. The first agent probed the VM and committed a logging `.cursor/hooks.json` to its branch; the second started from that branch so the hooks were active, and a follow-up run read the hook log back. Each run was streamed live and re-fetched after completion, and the v0 conversation was fetched twice to compare ids. The first agent was then opened in the desktop app through `cursor://anysphere.cursor-deeplink/background-agent?bcId=…` (which only works once a workspace window is open) and the local databases were diffed before and after. Total spend was under one dollar.

Still unverified:

- Whether "Move to Local" materialises a local composer. It needs a GUI session with a local agent open on the same repo.
- Whether a team service-account key lists other members' agents. The account used is personal, so no service-account key was available.

## 7. Sources

- Cursor docs: `cursor.com/docs/cloud-agent`, `/docs/cloud-agent/security`, `/docs/cloud-agent/self-hosted`, `/docs/cloud-agent/self-hosted/my-machines`, `/docs/cloud-agent/choose-runtime`, `/docs/cloud-agent/api/endpoints`, `/docs/cloud-agent/api/webhooks`, `/docs/cloud-agent/setup`, `/docs/cloud-agent/builds`, `/docs/cloud-agent/metadata`, `/docs/hooks`, `/docs/cli`, `/docs/sdk/typescript`, `/docs/enterprise/opentelemetry-export`, `/docs/account/teams/admin-api`, `/docs-static/cloud-agents-openapi.yaml`.
- Cursor forum threads 165486, 165263, 162470, 167711, 157311, 157711, 158713, 138839.
- Local inspection on macOS: Cursor 3.13.25 app bundle strings and logs, `~/Library/Application Support/Cursor/User/globalStorage/{state.vscdb,conversation-search.db}`, `~/Library/Application Support/Cursor/IndexedDB`, `~/.cursor/chats`, `~/.cursor/projects/*/agent-transcripts`, `cursor-agent 2026.09.15` (`agent worker --help`).
- Spike agents: `bc-4d216726-179c-4a67-92a9-5f429c32630b` (probe) and `bc-a6e21adf-24fd-49ba-bc37-63cc51a58b81` (hooks), branch `cursor/specstory-spike-probe-14e2` in `specstoryai/cloud-agent-test`.
- This repo: `pkg/providers/cursoride/CURSORIDE-FORMAT.md`, `pkg/providers/cursorcli/`, `pkg/cloud/sync.go`, `docs/PROJECT-IDENTITY.md`, `docs/CLOUD-RESUME.md`, `NEW-PROVIDER-GUIDE.md`; `specstory-monorepo` commit `3029adc6` (extension delegates to CLI).
