# AI Git Provenance Systems Summary (10 Systems)

This document is an executive summary / tl;dr comparing 10 existing approaches to "AI blame" and AI code provenance using (or adjacent to) Git. It focuses on the **big picture differences**, the **most important trade-offs**, and **side-by-side tables** across the **7 challenge areas** from `docs/git-provenance/RESEARCH-TASK.md`.

## Big Picture: Three "Provenance Granularities"

Most systems fall into one of these "where does provenance attach?" buckets:

1. **Commit-level markers (lowest fidelity, lowest friction)**
   - Provenance is "this commit was AI-assisted" via trailers/author markers.
   - Examples: **Aider**, **AIttributor**.

2. **Commit-attached context (commit-level, human-readable, not blame-grade)**
   - Provenance is "what we talked about / tools used around the commit", attached as a note or local JSON.
   - Examples: **cnotes** (git notes), **Tempo CLI** (local JSON), **Git With Intent** (run bundles in `.gwi/`).

3. **Line-aware provenance (blame-grade)**
   - Provenance is a **line-range mapping** (often tied back to prompts / sessions) stored in Git Notes (or equivalent) and queried by an overlay blame tool.
   - Examples: **Git AI**, **Agent Blame**, **whogitit**, **Intent Git Mode (us)**.

Entire is a distinct, "workflow provenance" approach: it optimizes for **checkpoint / rewind / resume / explain** with a dedicated metadata branch, rather than line-level blame.

## Quick Comparison (What You Actually Get)

| System               | "AI Blame" Fidelity                             | Git-Native Provenance Storage                 | Micro-versioning Before Commit         | Multi-Agent Coverage                                   | Primary UX                          |
|----------------------|-------------------------------------------------|-----------------------------------------------|----------------------------------------|--------------------------------------------------------|-------------------------------------|
| Intent Git Mode (us) | High (line-aware via CRDT marks + notes export) | Git Notes (`refs/notes/intent/*`)             | Yes (every FS change)                  | SpecStory providers (Claude/Cursor/Codex/Gemini/Droid) | `intent blame`, `intent trace`      |
| Agent Blame          | High (line ranges + prompt linkage)             | Git Notes (`refs/notes/agentblame`)           | Yes (deltas + checkpoints)             | Cursor, Claude Code, OpenCode                          | `ab blame`, GitHub PR extension     |
| Git AI               | High (line ranges + prompt linkage)             | Git Notes (`refs/notes/ai`)                   | Yes (checkpoint stream)                | Broad (Claude, Codex, Cursor, Gemini, Copilot, ...)    | `git-ai blame` (+ open format)      |
| whogitit             | High (per-line classification)                  | Git Notes (`refs/notes/whogitit`)             | Yes (pre/post snapshots)               | Claude Code only                                       | `whogitit blame`                    |
| Entire               | Medium (checkpoint/session explainability)      | Dedicated branch (`entire/checkpoints/v1`)    | Yes (shadow checkpoints)               | Claude Code (deep), Gemini (partial)                   | `entire explain/rewind/resume`      |
| cnotes               | Low-Medium (commit-level conversation excerpt)  | Git Notes (`refs/notes/claude-conversations`) | No                                     | Claude Code only                                       | `cnotes show/list`                  |
| Tempo CLI            | Low (commit + file-level attribution)           | No (local `.tempo/` by default)               | No                                     | Multiple (detects sessions)                            | JSON records / dashboards           |
| Aider                | Low (commit-level "AI helped")                  | Commit metadata (trailers/author)             | Commit-level only (auto-commit cycles) | Aider runtime only                                     | Standard `git log/blame` inspection |
| AIttributor          | Low (commit-level "AI tool present")            | Commit metadata (trailers)                    | No                                     | Multiple (detection only)                              | Standard `git log/blame` inspection |
| Git With Intent      | Medium (run/audit provenance)                   | No (artifacts in `.gwi/`)                     | No                                     | Internal GWI agents                                    | Run lifecycle + approvals           |

## The 7 Challenge Areas: Comparisons

Legend:
- "Hooks" = explicit agent hook callbacks (PreToolUse/PostToolUse equivalents)
- "Detect" = process/env/session-artifact detection without full transcript/tool capture

### Challenge 1: Capturing Agent Activity

This is the foundation: if capture is weak or ambiguous, everything downstream (micro-versioning, correlation, blame) becomes either lossy or heavily heuristic.

Major options that show up across the systems:
- **Hooks + normalization**: precise prompt/tool boundaries and often explicit file paths. Pros: highest fidelity; best path to blame-grade attribution. Cons: per-agent integration work; brittle to upstream hook/schema changes; requires config permissions.
- **Transcript parsing/scraping**: reconstruct events from agent transcripts/session artifacts. Pros: can recover richer context when hook payloads are thin. Cons: format drift and partial visibility; more parsing complexity.
- **Commit-time detection**: infer "AI was involved" from processes/env vars/breadcrumbs. Pros: extremely low friction. Cons: coarse and can be wrong (multiple sessions/tools around the commit).
- **Internal pipeline logs**: record the system's own agent workflow and audit events. Pros: strong governance trail. Cons: not a drop-in substitute for external coding-session provenance.

| System               | Capture mechanism               | Agent coverage today                                |
|----------------------|---------------------------------|-----------------------------------------------------|
| Intent Git Mode (us) | Provider watchers (SpecStory)   | Claude/Cursor/Codex/Gemini/Droid                    |
| Agent Blame          | Hooks + prompt/tool capture     | Cursor, Claude Code, OpenCode                       |
| Git AI               | Hooks + transcript parsing      | Broad (Claude, Codex, Cursor, Gemini, Copilot, ...) |
| whogitit             | Hooks                           | Claude Code only                                    |
| Entire               | Hooks + lifecycle state machine | Claude Code (deep), Gemini (partial)                |
| cnotes               | Hooks gated to commit command   | Claude Code only                                    |
| Tempo CLI            | Detect (files/process/trailers) | Multiple tools (detection only)                     |
| Aider                | Aider runtime                   | Aider only                                          |
| AIttributor          | Detect at commit time           | Multiple tools (detection only)                     |
| Git With Intent      | Internal agent pipeline         | GWI internal agents                                 |

### Challenge 2: Capturing File Change and Micro-versioning

The core issue: Git collapses many intermediate edits into one commit diff. If you want "which prompt/tool produced this line" you need **pre-commit history** that survives until attribution is written.

Major options:
- **Per-edit checkpoints/deltas**: append a stream of checkpoints/deltas as edits happen, then compile at commit time. Pros: preserves iterative agent loops; supports line-level mapping. Cons: local state management (storage, cleanup, perf) and more complex failure modes.
- **Always-on micro-versioning (journal/CRDT first)**: record every filesystem change continuously, then export/attach provenance at commit/push time. Pros: strongest at preserving causality; can attribute dirty/uncommitted lines. Cons: highest system complexity and operational footprint.
- **Commit-boundary only**: accept flattening and treat commit as the provenance unit. Pros: minimal overhead and easy distribution. Cons: cannot disambiguate multiple prompts/iterations inside a commit without expensive post-hoc inference.

| System               | Micro-versioning approach                                     |
|----------------------|---------------------------------------------------------------|
| Intent Git Mode (us) | CRDT history with provenance marks on every filesystem change |
| Agent Blame          | Delta ledger + before/after checkpoints between commits       |
| Git AI               | Per-edit checkpoints under `.git/ai/working_logs/<base>/`     |
| whogitit             | Pre/post snapshots per edit into a pending buffer             |
| Entire               | Shadow-branch checkpoints; optional auto-commit strategy      |
| cnotes               | None (commit-triggered transcript excerpt only)               |
| Tempo CLI            | None (commit boundary only)                                   |
| Aider                | Commit-level only (auto-commit cycles)                        |
| AIttributor          | None                                                          |
| Git With Intent      | None (run artifacts only)                                     |

### Challenge 3: Correlating Agent Change to File Change

Correlation decides whether provenance is precise (attached to the right files/lines) or merely suggestive. The hardest cases are shell-based generators, multi-file refactors, and concurrent sessions touching the same repo.

Major options:
- **Tool/file-path-driven correlation**: use explicit `file_path` fields from tool events, then diff/replay. Pros: deterministic and accurate when available. Cons: depends on hook/tool surfaces; shell generators can be opaque.
- **Multi-signal heuristics**: fuse hook hints, transcript parsing, git status/diff, and prior context. Pros: practical coverage in real repos. Cons: can misattribute under concurrency or under-specified events.
- **Snapshot/three-way analysis**: store snapshots around edits and compute mapping at commit time. Pros: avoids brittle shell parsing; can be robust for edit/overwrite cases. Cons: more storage and algorithmic complexity.
- **Commit-time presence detection**: correlate by "agent was active" at commit time. Pros: simplest DX. Cons: lowest precision.

| System               | Correlation strategy                                                |
|----------------------|---------------------------------------------------------------------|
| Intent Git Mode (us) | Scored matcher over time/path/hash/session affinity                 |
| Agent Blame          | Tool file paths + diff/delta replay + commit added-line filtering   |
| Git AI               | Layered: hook file hints + transcript extraction + git status/diff  |
| whogitit             | Snapshot-driven commit-time three-way analysis over captured edits  |
| Entire               | Hook phase boundaries + transcript tool extraction + repo snapshots |
| cnotes               | Time-window transcript excerpt anchored at commit SHA               |
| Tempo CLI            | Intersect commit file list with files observed in detected sessions |
| Aider                | "Commit == correlation unit" (agent commits its own edits)          |
| AIttributor          | Repo cwd + "agent present" heuristic at commit time                 |
| Git With Intent      | RunId anchors patchset; not designed for per-line correlation       |

### Challenge 4: Representing Agent Provenance

Representation is the "API surface" for future humans and tools. The key question is whether it is blame-grade (line ranges) and whether it carries enough context to answer "why" without leaking sensitive prompts.

Major options:
- **Line-range maps + prompt/session metadata**: compact ranges plus a metadata section that resolves ranges to prompts/models/tool calls. Pros: fast blame lookup and scalable. Cons: schema/versioning discipline required; payloads can grow.
- **Per-line classification arrays**: a classification per line number. Pros: conceptually simple and explicit. Cons: heavier payloads and sensitive to line churn.
- **Checkpoint-centric metadata**: represent provenance as checkpoints with transcripts and "files touched". Pros: great for narrative explain/rewind/resume. Cons: does not directly answer per-line origin without additional mapping.
- **Commit markers**: encode provenance in trailers/authors. Pros: tiny and durable. Cons: not queryable at line granularity.

| System               | Representation (what gets queried later)                                                      |
|----------------------|-----------------------------------------------------------------------------------------------|
| Intent Git Mode (us) | CRDT marks (dirty/uncommitted) + git notes with exchanges/metadata and file line ranges       |
| Agent Blame          | Git note JSON with sessions/prompts + per-file line ranges                                    |
| Git AI               | Authorship Log (`authorship/3.0.0`): file + line-range attestations + prompt metadata         |
| whogitit             | Git note JSON (`AIAttribution`) with per-line classification + prompt index                   |
| Entire               | Checkpoint IDs (trailers) + structured checkpoint metadata tree (transcripts, prompts, files) |
| cnotes               | Git note JSON "conversation excerpt" per commit                                               |
| Tempo CLI            | JSON report per commit (file-level attribution), stored locally                               |
| Aider                | Commit trailers / author markers                                                              |
| AIttributor          | Commit trailers (e.g. `Ai-assisted`)                                                          |
| Git With Intent      | Run bundles + audit logs + approvals under `.gwi/`                                            |

### Challenge 5: Storing Agent Provenance in Git

This is where "Git-native" becomes operational: you need a persistence strategy that survives distributed collaboration, plus a story for rewrite operations (rebase/squash/amend).

Major options:
- **Commit object metadata (trailers/authors)**: in-band, distributes naturally. Pros: rewrite survival is mostly free. Cons: shallow; hard to carry rich provenance.
- **Git Notes refs**: out-of-band blobs attached to commits. Pros: fits rich blame-grade payloads while keeping commits clean. Cons: notes sync is non-default; rewrite survival requires explicit remapping/transfer.
- **Dedicated metadata branch**: provenance is a queryable tree of objects. Pros: structured and scalable. Cons: branch discipline and merge/conflict semantics become part of the system.
- **Not in Git (local/cloud artifacts)**: store elsewhere and link by commit SHA. Pros: easiest privacy controls and deployment knobs. Cons: loses Git's ubiquity unless you build equivalent syncing.

| System               | Git persistence strategy                                                                               |
|----------------------|--------------------------------------------------------------------------------------------------------|
| Intent Git Mode (us) | Git notes (`refs/notes/intent/exchanges`, `refs/notes/intent/metadata`) + wrapper push/merge semantics |
| Agent Blame          | Git notes (`refs/notes/agentblame`) + analytics notes; explicit transfer for squash/rebase             |
| Git AI               | Git notes (`refs/notes/ai`) with rewrite-aware remapping + fetch/push sync                             |
| whogitit             | Git notes (`refs/notes/whogitit`) + hooks for push/rewrite                                             |
| Entire               | Dedicated metadata branch (`entire/checkpoints/v1`) + shadow branches + commit trailers                |
| cnotes               | Git notes (`refs/notes/claude-conversations`)                                                          |
| Tempo CLI            | Not stored in git by default (local `.tempo/` + optional cloud sync)                                   |
| Aider                | Stored directly in commit object metadata                                                              |
| AIttributor          | Stored directly in commit object metadata                                                              |
| Git With Intent      | Not stored as git-native provenance (artifacts in `.gwi/`)                                             |

### Challenge 6: AI Blame

Blame UX is where the system either becomes "daily useful" or stays an audit artifact. In practice, blame-grade UX means: start from a file/line, jump to the responsible prompt/session, and do it fast.

Major options:
- **Blame overlay tools**: run `git blame`, then enrich each blamed commit with provenance records. Pros: matches developer mental models; CLI-friendly. Cons: depends on notes being present and synced; needs careful performance work.
- **PR/review augmentation**: annotate diffs in code review UIs. Pros: high value during review; good adoption lever. Cons: often focuses on "added lines in a diff", not full historical blame.
- **Explainability without line mapping**: jump from commit to checkpoint/transcript and explain changes. Pros: strong narrative understanding. Cons: not a direct per-line origin answer.

| System               | AI blame UX                                                             |
|----------------------|-------------------------------------------------------------------------|
| Intent Git Mode (us) | `intent blame` (includes dirty overlay) + `intent trace` inverse lookup |
| Agent Blame          | `ab blame` overlay + GitHub PR line markers                             |
| Git AI               | `git-ai blame` overlay with multiple output modes                       |
| whogitit             | `whogitit blame` overlay                                                |
| Entire               | No line overlay; commit/checkpoint explainability (`entire explain`)    |
| cnotes               | No (manual inspection of note content)                                  |
| Tempo CLI            | No (consume JSON reports)                                               |
| Aider                | No (manual patterns in `git blame` / commit trailers)                   |
| AIttributor          | No (manual patterns in `git blame` / commit trailers)                   |
| Git With Intent      | No line overlay (run-level audit/explain)                               |

### Challenge 7: Developer Experience (DX)

DX determines whether high-fidelity provenance actually ships in real teams. The dominant tension is "invisible automation" vs "explicit commands/services" vs "minimal adoption cost".

Major options:
- **Wrapper/shim UX**: preserve `git commit/push` muscle memory by intercepting Git operations. Pros: low behavior change. Cons: path/environment coupling and extra failure surface.
- **Repo init + managed hooks/CI**: install repo-local hooks and workflows for notes sync and rewrite handling. Pros: explicit, enforceable. Cons: can conflict with existing hook managers and enterprise policies.
- **Background service + custom commands**: keep watchers running and require wrapper commands for commit/push. Pros: enables always-on micro-versioning and dirty attribution. Cons: highest operational burden.
- **Minimal hook/detect**: install one small hook and stop. Pros: easiest adoption. Cons: shallowest provenance.

| System               | DX shape (what devs must do / learn)                                               |
|----------------------|------------------------------------------------------------------------------------|
| Intent Git Mode (us) | Run background service + use `intent git commit/push` wrappers; higher complexity  |
| Agent Blame          | Repo init + hooks + CI workflow + notes sync; heavier but rich UX                  |
| Git AI               | Wrapper/shim + global hook installers; keep notes sync and privacy mode consistent |
| whogitit             | Claude hook setup + repo hooks; moderate complexity                                |
| Entire               | Learn sessions/checkpoints/strategies; manage metadata/shadow branches             |
| cnotes               | Simple Claude hook; notes sharing requires team git-notes discipline               |
| Tempo CLI            | Post-commit hook; no git-notes complexity (but not Git-native)                     |
| Aider                | Use Aider; rely on commit markers; minimal extra learning                          |
| AIttributor          | Install hook; keep committing as usual; lowest friction, coarsest signal           |
| Git With Intent      | Adopt run/approval workflow; great governance, not blame-first                     |

## The Core Trade-offs (What Actually Matters)

### 1) Fidelity vs Friction

- **Commit markers** (Aider/AIttributor) are almost free operationally, but cannot answer "which prompt produced this line?"
- **Line-aware notes** (Git AI / Agent Blame / whogitit / Intent Git Mode (us)) can answer blame-grade questions, but require:
  - agent hooks or robust capture,
  - pre-commit state tracking (checkpoints/deltas),
  - and consistent notes sync + rewrite survival.
- **Checkpoint workflows** (Entire) optimize for "explain/rewind/resume" and can capture rich session context without forcing per-line mapping, at the cost of extra moving parts (shadow branches, condensation, metadata branch).

### 2) Where You Pay The Complexity: Capture, Correlation, or Replay

Systems "choose their hard problem":

- **Hook-first** (Git AI, Agent Blame, whogitit): invest in per-agent integrations so the system sees explicit edit boundaries and file paths.
- **Replay-first** (Agent Blame, whogitit): keep a pending/delta ledger, then compute final attribution at commit time (often with three-way analysis or delta replay).
- **Always-on micro-versioning** (Intent Git Mode (us)): record every FS change (CRDT history + marks) and correlate later using scoring.
- **Detect-only** (AIttributor, Tempo CLI): don't attempt replay or line mapping; accept coarse attribution.

### 3) "Git-Native" Is Not One Thing

Git can carry provenance in very different ways:

| Strategy                          | Examples                                                    | Pros                                                       | Cons                                                            |
|-----------------------------------|-------------------------------------------------------------|------------------------------------------------------------|-----------------------------------------------------------------|
| Commit metadata (trailers/author) | Aider, AIttributor                                          | Survives rewrites naturally; no extra refs                 | Too shallow for blame-grade provenance                          |
| Git Notes refs                    | Git AI, Agent Blame, whogitit, cnotes, Intent Git Mode (us) | Keeps commits clean; can attach large structured payloads  | Notes are out-of-band: must push/fetch; many tools ignore notes |
| Dedicated metadata branch         | Entire                                                      | Queryable structured tree; avoids "notes tooling mismatch" | Requires branch discipline and sync; more custom semantics      |
| Local artifacts (not in Git)      | Tempo CLI, Git With Intent                                  | No Git plumbing friction; easier privacy controls          | Not ubiquitous/distributed unless extra syncing is built        |

### 4) Rewrite Survival Is The Hidden Cost Center

If you want blame-grade provenance in Git, you must decide how it survives:

- **Commit metadata**: "free" survival (trailers ride with rewritten commits, assuming the message survives).
- **Notes without rewrite logic**: can silently degrade when SHAs change.
- **Notes with rewrite logic**:
  - Git AI implements explicit remapping across many git lifecycle operations.
  - Agent Blame uses explicit transfer workflows (`ab sync`) plus a CI "post-merge" mapping step for squash/rebase merges.
  - whogitit relies on post-rewrite hook copying notes.
- **Branch-based checkpoint IDs** (Entire): uses commit trailers to link code commits to checkpoint metadata; the link remains as long as that trailer is preserved.

### 5) Privacy / Compliance Is A First-Class Design Constraint

The biggest practical distinction isn't technical, it's "what data ends up shared":

- **Commit markers** (Aider/AIttributor) leak minimal information.
- **Notes/branches with prompts/transcripts** can leak sensitive content unless redacted.
- Git AI explicitly offers storage modes (`default` / `notes` / `local`) to control how much prompt content is persisted into Git notes.
- cnotes includes redaction and excerpt limits but still stores conversation content in notes.
- Entire's metadata branch stores structured prompt/context artifacts, which is powerful but requires clear team policy.

## When To Use What (Rules Of Thumb)

- If you only need "AI-assisted commit" labeling: **AIttributor** (detect) or **Aider** (agent-driven commits).
- If you need "what was the conversation around this commit?" (human audit trail): **cnotes**.
- If you need blame-grade provenance for real teams: start by studying **Git AI**, **Agent Blame**, and **Intent Git Mode (us)** (they cover the full stack), and treat **whogitit** as a strong Claude-only reference implementation.
- If you want "checkpoint workflows" (rewind/resume/explain) more than line blame: **Entire**.
- If you want governance/approval pipelines more than blame: **Git With Intent**.
