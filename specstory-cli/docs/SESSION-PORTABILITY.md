# Session Portability (Reverse Data Flow)

This document describes the design for **session portability**: reconstructing an agent's native session format *from* the SpecStory CLI's neutral session schema, so a conversation captured from one agent can be resumed in another — and, eventually, on another machine, and by another user.

Our sequencing of this is:

- 1. cross-provider resume (local only plumbing)
- 2. cross-project resume (local only plumbing)
- 3. cross-machine resume (SpecStory Cloud plumbing)
- 4. cross-user resume (Requires SpecStory Cloud Orgs/Teams)

## Overview

Today the CLI's data flow is one-directional:

```
agent-native format  ──►  neutral SessionData  ──►  markdown / cloud
   (per provider)            (schema.SessionData)       (rendered output)
```

Each provider parses its agent's native session store into the unified `schema.SessionData`, which is then rendered to markdown and synced to the cloud.

Session portability **reverses** the second arrow, adding a new provider responsibility:

```
neutral SessionData  ──►  agent-native format
 (schema.SessionData)        (per provider)
```

A provider gains the ability to take a `SessionData` and emit a native session file that its agent can load and resume. Combined with a new `specstory resume` experience, this enables a conversation to continue in a *different* agent than the one that created it, on a different machine than the one that created it (via SpecStory cloud), and by a different user than the one that created it (via SpecStory Cloud teams product).

## Use Cases

Portability widens **one dimension at a time**, following the staged sequencing above. The stages are cumulative: each builds on the plumbing of the prior one and adds a single new axis of "crossing" — provider, then project, then machine, then user. The dimensions also compose (e.g. a future cross-machine resume can also be cross-provider), so the reconstruction core built in stage 1 is exercised by every later stage.

### Stage 1 — Cross-provider resume *(local only)*

Start a session in one agent and continue it in another, on the same machine and project (e.g. Claude Code → Codex CLI, or vice versa). The neutral `SessionData` is the interchange format; each provider reconstructs its own native format from it. This is the foundation for everything that follows: it establishes the `SessionData → native` reconstruction and the `specstory resume` flow, entirely with local plumbing.

### Stage 2 — Cross-project resume *(local only)*

Resume a session into a *different project directory* on the same machine. Everything stays local, but both the source lookup and the target write must be re-pointed at the destination workspace: the native store location and (for Claude Code) the cwd-derived project folder are computed for the destination project rather than the origin. No cloud involvement.

### Stage 3 — Cross-machine resume *(SpecStory Cloud plumbing)*

A session created on machine A is carried via SpecStory Cloud to machine B and resumed there. Machine B has no local native store for the session, so `SessionData` must be sourced from the cloud. This depends on the cloud persisting and serving `SessionData` (see [SessionData Sourcing](#sessiondata-sourcing)) — today the cloud stores only rendered Markdown + native RawData, so this stage is gated on cloud-side work.

### Stage 4 — Cross-user resume *(requires SpecStory Cloud Orgs/Teams)*

A *different user* resumes a session shared with them through a team or organization. On top of the cross-machine plumbing, this requires the cloud's sharing, identity, and permission model (the Orgs/Teams product) to govern who may resume whose sessions.

## Goals and Non-Goals

### Goals

- A provider can reconstruct a resumable native session file from `SessionData`.
- Cross-provider reconstruction works from day one (Claude ↔ Codex), since the tool/model/usage mapping problems are fundamental and must be confronted early.
- The reconstructed session is **valid** to the target agent's loader and conveys the **gist** of the prior conversation as context for the agent's next request.

### Non-Goals

- **Byte-for-byte or structural round-trip.** The agent only needs the prior conversation as context for subsequent model requests; it does not need a faithful replica of the original session's internal structure.
- **Faithful tool replay.** Reconstructed sessions do not reproduce native `tool_use` / `function_call` structures (see [Reconstruction Model](#reconstruction-model)).
- **Working reconstruction for every provider at once.** Reconstruction is on the `spi.Provider` interface so all providers carry the responsibility, but only Claude Code and Codex CLI have working implementations initially; the rest return `ErrReconstructionUnsupported` until implemented.

### Fidelity Bar

Between "the agent parses it without error" and "best-effort readable." The reconstructed session must be structurally valid enough that the target agent loads and resumes it, and must carry enough of the conversation that the agent has useful context going forward. Synthesized IDs, timestamps, and chains are acceptable.

## Background: Why the Reverse Path Is Not a Mirror

The forward parsers read a **different projection** of each native file than the agent needs in order to *resume*. Reconstruction therefore cannot simply "run the parser backwards" — it must synthesize records the forward path never reads.

- **Codex CLI.** The forward parser builds `SessionData` from the `event_msg` UI-event stream (`user_message`, `agent_message`, `agent_reasoning`). But `codex resume` replays the `response_item` stream — the model-facing transcript (`{type:"message", role, content:[…]}` plus tool-call items). Both streams coexist in every rollout file; reconstruction must regenerate the `response_item` transcript.

- **Claude Code.** The forward parser collapses the `parentUuid` linked-list into linear exchanges and keeps only the record `uuid`. It drops the API `message.id`, the chain, and thinking-block signatures. `claude --resume` needs a valid `parentUuid` chain and API-shaped messages.

Combined with cross-provider conversion (a Claude `Edit` has no clean Codex `apply_patch` equivalent; model names and token-usage fields are meaningless to the other agent), faithful structural tool replay is intractable. The pragmatic, robust target is a **flattened transcript** in the target's native shape.

## Reconstruction Model

`SessionData` is flattened into an ordered list of **plain user/agent text turns**, then serialized into the target's native format. Everything the agent "said," "thought," or "did" collapses into agent text:

| `SessionData` element                                | Becomes                                                          |
|------------------------------------------------------|------------------------------------------------------------------|
| user message text                                    | user turn                                                        |
| agent message text                                   | agent turn                                                       |
| thinking content (`ContentTypeThinking`)             | agent turn (text)                                                |
| tool call                                            | agent turn (text, via `Tool.Summary` / `Tool.FormattedMarkdown`) |
| `model`, `Usage`, `PathHints`                        | dropped                                                          |
| synthetic local-command turns (slash-command invocations, their stdout, caveats; e.g. Claude's `<command-name>`/`<local-command-*>`, `<TEXTBLOCK>`) | dropped — **reconstruction-only** noise filtering |
| source agent system / `environment_context` preamble | not copied (target injects its own)                              |

Dropping the synthetic local-command turns happens during flattening, **not** in the forward parser — so archival `.specstory` markdown stays a faithful record of the source session, while the reconstructed context fed to a resumed agent is kept clean (the caveat text, in particular, is actively misleading to a resumed agent).

The tool-call flattening reuses work already done on the forward pass: each `ToolInfo` already carries a pre-rendered markdown rendition (`FormattedMarkdown`, and for Codex also `Summary`). Reconstruction emits that markdown as the text of an agent turn, so reconstructed sessions contain **only user and agent text turns** — no native tool structures, no dangling tool results, no thinking signatures. This is what makes cross-provider reconstruction safe: there is nothing provider-specific or API-fragile left to mistranslate.

## Validated Spike

The core hypothesis was validated before any converter code was written (project `~/Source/SpecStory/compositions/cross-portable-1`, 2026-06-16). Hand-built cross-provider session files — a Codex session synthesized from a Claude conversation, and a Claude session synthesized from a Codex conversation — were both loaded and continued successfully by `claude --resume` and `codex resume`.

The spike files were deliberately **minimal**: only what the converter could produce from `SessionData` plus the target cwd. This confirmed:

- A minimal user/agent text transcript is sufficient to resume. The runtime scaffolding the converter cannot produce was omitted and **not** required — Codex `base_instructions` / developer-permissions / `turn_context`, and Claude `file-history-snapshot` / `mode` / attachment records. Each agent injects its own scaffolding on resume.
- Tool-calls-as-agent-text are accepted. No native `tool_use` / `function_call` and no `tool_result` pairing are needed.
- Fresh native-format session IDs work, with the ID embedded in the filename.

## Architecture

### Reconstruction on the Provider Interface

Reconstruction is a **first-class provider responsibility**: `ReconstructSession` is added to the `spi.Provider` interface, so every provider carries it. Claude Code and Codex CLI get working implementations first (Stage 1); the remaining providers satisfy the interface with a stub that returns a clear "reconstruction not yet supported" error until their implementations land. Consumers call `ReconstructSession` directly — no capability check, no type assertion.

```go
// pkg/spi/provider.go — added to the Provider interface

// ReconstructSession rebuilds the provider's native session format from the
// neutral SessionData so the agent can resume the conversation. Providers that
// do not yet implement it return ErrReconstructionUnsupported.
ReconstructSession(data *schema.SessionData, opts ReconstructOptions) (*ReconstructedSession, error)
```

```go
// pkg/spi/reconstruct.go — shared types

// ReconstructOptions controls how a session is reconstructed.
type ReconstructOptions struct {
    WorkspaceRoot string // target cwd; native paths/IDs are derived from this
    MigrationNote string // optional one-line note prepended as context
}

// ReconstructedSession is the native output of reconstruction.
type ReconstructedSession struct {
    SessionID string // freshly minted, native-format ID
    Filename  string // suggested native filename (relative)
    Content   []byte // native session file bytes
}
```

`ReconstructSession` is a **pure transform** — no filesystem access — which makes it trivially unit-testable and the natural home for round-trip tests. (The `specstory resume` command, not the provider, writes the result into the live store.)

### Shared Flattening Helper

The `SessionData → []Turn` flattening (the [Reconstruction Model](#reconstruction-model) table) lives in one shared place so the cross-provider policy is defined once and every provider consumes it.

```go
type Turn struct {
    Role string // schema.RoleUser or schema.RoleAgent
    Text string
}
```

### Per-Provider Serializers

Each provider converts `[]Turn` into its native, resumable file:

- **Claude Code** (`pkg/providers/claudecode/reconstruct.go`): a `parentUuid`-chained JSONL (one `user` record followed by `assistant` text records) written to `~/.claude/projects/<cwd-encoded>/<new-uuid>.jsonl`. Fresh UUIDv4.

- **Codex CLI** (`pkg/providers/codexcli/reconstruct.go`): a `session_meta` record plus a linear `response_item` message transcript (and matching `event_msg` stream) written to `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<new-id>.jsonl`. Fresh UUIDv7-shape ID.

- **Gemini CLI** (`pkg/providers/geminicli/reconstruct.go`): a single chat-JSON document (`{sessionId, projectHash, startTime, lastUpdated, kind:"main", messages:[…]}`) with `user` (`content:[{text}]`) and `gemini` (`content:"…"`) text messages, fresh UUIDv4. Unlike the others, Gemini associates a tmp dir with a project via a `.project_root` marker, so `NativeSessionPath` *prepares* the store (`~/.gemini/tmp/<name>/` + marker + `chats/`) rather than only computing a path.

- **Factory Droid** (`pkg/providers/droidcli/reconstruct.go`): a `session_start` header plus `parentId`-chained `message` records (`{message:{role, content:[{type:"text",text}]}}`) written to `~/.factory/sessions/<cwd-encoded>/<new-uuid>.jsonl`. Project-scoped like Claude. Fresh UUIDv4.

- **DeepSeek TUI** (`pkg/providers/deepseektui/reconstruct.go`): a single JSON document (`{schema_version, system_prompt, metadata, messages}`) with `user`/`assistant` `content:[{type:"text",text}]` messages, written to `~/.deepseek/sessions/<new-uuid>.json`. Not project-scoped by directory — the project is recorded in `metadata.workspace`. Fresh UUIDv4.

- **Cursor CLI**: stub `ReconstructSession` returning `ErrReconstructionUnsupported` — its store is a SQLite `store.db` (blob-encoded, DAG-ordered), a larger lift handled separately.

Implemented serializers reuse existing path-derivation code (`GetClaudeCodeProjectDir`, `codexSessionsRoot`, `ResolveGeminiProjectDir`, `resolveProjectSessionDir`, `resolveSessionsDir`).

### Dependency Flow

```
cmd/resume.go
  → spi.Provider.ReconstructSession   (every provider implements it)
  → existing ExecAgentAndWatch (resume)

providers/* → spi (ReconstructSession + shared flattening helper + schema)
```

## Cross-Provider Mapping Policy

Because reconstruction flattens to text, most mapping problems dissolve. The explicit rules:

- **Model name** — dropped. The target agent uses its own configured model going forward.
- **Token usage** — dropped. Irrelevant to a resumed conversation.
- **Thinking / reasoning** — flattened to agent text. (Not dropped, not emitted as a signed thinking block.)
- **Tool calls** — flattened to agent text via the pre-rendered markdown.
- **System / `environment_context` preamble** — not copied from the source agent, and not synthesized for the target either; the target agent injects its own on resume (e.g. Codex rebuilds its environment context every turn). An optional one-line migration note may be prepended so the agent understands the prior turns were imported.

## Session Identity and Provenance

Reconstruction always mints a **fresh native-format session ID** (UUIDv4 for Claude, UUIDv7-shape for Codex) — the session did not previously exist in the target store, and the ID must be valid for the target. The original session ID is recorded as provenance metadata so the lineage is traceable.

## The `specstory resume` Flow

`specstory resume` is interactive. Its Stage 1 scope is **cross-agent within the current project**. It behaves like `specstory run` plus a selection UI and, for cross-agent, a reconstruct-and-store step before launch.

It reuses the existing `ExecAgentAndWatch(projectPath, customCommand, resumeSessionID, debugRaw, callback)` — **no new exec/resume method is needed**. All six providers already implement native resume (`claude --resume`, `codex resume`, `cursor --resume`, `gemini --resume`, `droid --resume`, `deepseek --resume`), so same-agent resume works for any agent; cross-agent resume requires the target to reconstruct (Claude/Codex today).

Interactive steps (plain numbered stdin menus — no TUI dependency for the first cut):

1. **Pick the source agent.** For every registered provider, list its sessions in the current project (`ListAgentChatSessions`). Show only agents with ≥1 session, each with name, session count, and date range.
2. **Pick the session.** Show the chosen agent's sessions, reverse-chronological, paginated, labeled by date and name/slug.
3. **Pick the target agent.** Show installed agents (`Check().Success`), including the source agent itself (labeled "same agent — native resume").
4. **Resume:**
   - **Same agent (from == to):** skip reconstruction; native-resume the existing session via `ExecAgentAndWatch(..., resumeSessionID = chosen session, ...)`.
   - **Cross-agent (from != to):**
     1. `from.GetAgentChatSession(...)` → `SessionData`.
     2. `to.ReconstructSession(data, opts)` with a default migration note (cross-agent only) → native bytes + fresh ID. If the target returns `ErrReconstructionUnsupported`, fail with a clear message (only Claude/Codex reconstruct today).
     3. `to.NativeSessionPath(projectPath, rec.Filename)` → destination path; the command does `MkdirAll` + `WriteFile`.
     4. `to.ExecAgentAndWatch(..., resumeSessionID = rec.SessionID, ...)`, with the same autosave callback `run`/`watch` use.

Writing into another application's data store is the one sensitive action. It is **scoped to exactly the single session being resumed**, and only happens when the user explicitly invokes `resume`.

### Path resolution

`ReconstructSession` stays a pure transform (bytes + base filename + fresh ID; no filesystem). A separate `NativeSessionPath(projectPath, filename) (string, error)` method on `spi.Provider` resolves where the file belongs in the target's native store — e.g. `~/.claude/projects/<cwd-encoded>/<id>.jsonl` or `~/.codex/sessions/YYYY/MM/DD/rollout-…-<id>.jsonl` — without requiring the directory to exist; the resume command performs the `MkdirAll` + `WriteFile`. Providers without a serializer return `ErrReconstructionUnsupported`.

## SessionData Sourcing

The reconstruction input is always `SessionData`. Where that `SessionData` comes from is the main thing that gates each stage:

| Stage             | Source of `SessionData`                                                               | Status                                                                |
|-------------------|---------------------------------------------------------------------------------------|-----------------------------------------------------------------------|
| 1. Cross-provider | Re-parse the local native store (origin project) through the existing forward path    | First build target                                                    |
| 2. Cross-project  | Re-parse the local native store (origin project); reconstruct against destination cwd | Local; follows stage 1                                                |
| 3. Cross-machine  | Cloud persists and serves `SessionData`                                               | Gated on cloud-side work (cloud stores only Markdown + RawData today) |
| 4. Cross-user     | Cloud serves `SessionData` **and** Orgs/Teams sharing + permissions                   | Gated on the cloud Orgs/Teams product                                 |

Stages 1 and 2 are entirely local — `SessionData` is rebuilt on demand by re-parsing the native store through the existing forward path. Stages 3 and 4 need the cloud to carry `SessionData` between machines and users.

Note that `SessionData` is currently **ephemeral** — built in memory, used to render markdown, then discarded (it is only written to disk under `--debug-raw`). Persisting/serving it is the cloud-side work that stages 3 and 4 depend on.

> A pure same-provider, same-project, same-machine "resume" is **not** a product use case — the agent's own native session already exists, so there is nothing to reconstruct. That trivial round-trip is used only as a development and test vehicle for the reconstruction core.

## Build Plan (Stage 1: Cross-Provider Resume)

These phases deliver **Stage 1** — the reconstruction core, the two native serializers, and the `specstory resume` command, all with local plumbing. The same core is reused by every later stage; stages 2–4 add sourcing and sharing plumbing around it rather than changing it (see [Later Stages](#later-stages)).

- **Phase 1 — Resume-acceptance spike. _(Complete.)_** Hand-built cross-provider files resumed successfully in both agents. Validated the minimal flattened-transcript approach.

- **Phase 2 — Shared core + interface.** Add `ReconstructSession` to `spi.Provider`; add `pkg/spi/reconstruct.go` (option/result types, `ErrReconstructionUnsupported`, and the shared `SessionData → []Turn` flattening helper); add not-supported stubs to the four providers without serializers so the build compiles. Unit-tested.

- **Phase 3 — Native serializers.** `reconstruct.go` in `claudecode` and `codexcli`, each minting a fresh ID and recording provenance. Round-trip tests: reconstruct → re-parse via the forward parser → assert turns match.

- **Phase 4 — `specstory resume` command (Stage 1: cross-agent). _(In progress.)_** `pkg/cmd/resume.go` + wiring, mirroring `watch`/`run` setup. Interactive numbered menus (source agent → session → target agent); adds `NativeSessionPath` to the SPI; cross-agent injects a default migration note and reconstructs+writes before launch; same-agent bypasses reconstruction and native-resumes; reuses `ExecAgentAndWatch` for launch + autosave. Errors clearly when the chosen target can't reconstruct.

- **Phase 5 — End-to-end validation and edge hardening. _(Done for Claude → Codex.)_** Cross-agent resume confirmed through the command in a fresh project: Codex resumed cleanly **without** a synthesized `environment_context` (it injects its own), the migration note appeared as the leading turn, no synthetic local-command noise leaked, and a follow-up ("what did we just do?") proved the reconstructed transcript fed the model's context. Two fixes landed along the way: synthetic local-command turns are filtered at flatten time, and the synthesized `environment_context` was removed. Edge cases handled with tests: blank/whitespace turns and tool-only sessions flatten correctly; large tool outputs are already truncated upstream; UUID-based filenames avoid collisions; JSON encoding preserves unicode. Remaining: a Codex → Claude confirmation pass.

- **Phase 6 — Other providers. _(Gemini, Droid, DeepSeek done; Cursor remains.)_** Gemini CLI, Factory Droid, and DeepSeek TUI now have working serializers + `NativeSessionPath` (all round-trip tested), so each can be a cross-agent resume target. Only Cursor remains (SQLite `store.db` — a larger lift warranting its own pass). Real-agent resume confirmed for Gemini (round-trip); Droid and DeepSeek await a real-agent confirmation pass like Codex/Gemini got.

- **Phase 7 — Docs and analytics.** README/help for `resume`; resume analytics events; provenance recorded.

## Risks and Open Questions

- **Loader strictness across agent versions.** The spike validated current Claude Code and Codex CLI versions. Future versions could tighten their loaders. Round-trip tests guard our own parsing; real-agent validation (Phase 5) guards theirs.
- **Codex `environment_context` necessity.** Resolved by design: reconstruction no longer synthesizes one — Codex injects a fresh environment context on every resume, so a fabricated block is redundant and inconsistent with the "target supplies its own preamble" policy. Pending one real-agent confirmation that resume works without it.
- **Loss of tool fidelity.** By design, the resumed agent cannot inspect exact prior tool inputs/outputs beyond what the flattened text conveys. Accepted under the gist fidelity bar.

## Later Stages

These follow Stage 1 in the sequencing and are not part of the initial build:

- **Stage 2 — Cross-project resume.** Re-point source lookup and target write at a destination workspace; derive the destination project folder / native store path from the new cwd. Local-only; reuses the Stage 1 reconstruction core unchanged.
- **Stage 3 — Cross-machine resume.** Requires the cloud to persist and serve `SessionData` (today it stores only Markdown + native RawData), plus a CLI path that sources `SessionData` from the cloud instead of a local re-parse.
- **Stage 4 — Cross-user resume.** Requires the SpecStory Cloud Orgs/Teams product — session sharing, identity, and permissions governing who may resume whose sessions.

## Deferred

- A working native serializer for Cursor CLI. It carries the `ReconstructSession` responsibility from the start but returns `ErrReconstructionUnsupported` until implemented. Claude Code, Codex CLI, Gemini CLI, Factory Droid, and DeepSeek TUI have working serializers; Cursor is the notable lift (its store is a SQLite `store.db` with blob-encoded, DAG-ordered messages rather than a flat file).

## Related Documents

- [PROVIDER-SPI.md](PROVIDER-SPI.md) — the provider interface and registry.
- [SPI-SESSION-DATA-SCHEMA.md](SPI-SESSION-DATA-SCHEMA.md) — the neutral `SessionData` schema.
