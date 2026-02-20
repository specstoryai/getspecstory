# Git AI System Research

_Last updated: 2026-02-18 (UTC)_

## Executive Summary

**Metaphor:** Git AI behaves like a **flight data recorder bolted onto Git**:
- It records fine-grained agent/human editing events before commit (checkpoint stream).
- At commit time, it compiles those events into a durable attribution artifact (Authorship Log).
- It stores that artifact in Git Notes and keeps it alive across history rewrites.

Git AI is currently one of the most complete end-to-end implementations of AI provenance in Git: it covers capture, micro-versioning, rewrite survival, and line-level AI blame.

## System At A Glance

| Dimension                | What Git AI Does                                                                                   |
|--------------------------|----------------------------------------------------------------------------------------------------|
| Core model               | Git proxy + agent hooks + per-edit checkpoints + commit-time authorship logs                       |
| Primary provenance store | `refs/notes/ai` Git Notes (open `authorship/3.0.0` format)                                         |
| In-flight store          | `.git/ai/working_logs/<base-commit>/` checkpoint stream + blob store                               |
| Micro-versioning         | Yes (checkpoint per edit cycle, not just per commit)                                               |
| Line-level attribution   | Yes (`git-ai blame`)                                                                               |
| Rewrite handling         | Rebase/cherry-pick/merge/reset/stash/amend-aware note rewriting/remapping                          |
| Agent integrations       | Claude Code, Codex, Cursor, Gemini, Copilot, Continue, OpenCode, Droid, JetBrains/Junie/Rovo paths |
| Privacy modes            | `default` (CAS/upload + strip from notes), `notes` (redacted in notes), `local` (local only)       |

## Architecture (End-to-End)

```mermaid
flowchart LR
  A[Agent Hook Event\nPreToolUse and PostToolUse] --> B[git-ai checkpoint preset]
  B --> C[.git/ai/working_logs/(base)/checkpoints.jsonl]
  B --> D[.git/ai/working_logs/(base)/blobs/*]
  C --> E[git commit pre+post hooks]
  D --> E
  E --> F[Authorship Log\nattestations + metadata]
  F --> G[Git Note refs/notes/ai @ commit SHA]
  G --> H[git-ai blame, show, stats]
  G --> I[fetch, push, clone note sync]
```

---

## Challenge 1: Capturing Agent Activity

### Implementation

Git AI installs hooks into agent config files and normalizes incoming hook payloads into a common checkpoint input (`AgentRunResult`) containing:
- agent identity (`tool`, `id`, `model`)
- transcript-derived messages/tool use
- event kind (`Human`, `AiAgent`, `AiTab`)
- edited file hints (`edited_filepaths`, `will_edit_filepaths`)

### Supported Agents (Current)

Publicly listed by Git AI docs/README:
- Claude Code
- Codex
- Cursor
- OpenCode
- Gemini
- GitHub Copilot
- Continue
- Droid
- Junie
- Rovo Dev

Observed in current code paths:
- Hook installers: Claude Code, Codex, Cursor, VS Code integrations, OpenCode, Gemini, Droid, JetBrains integrations
- Checkpoint parsers/presets: `claude`, `codex`, `cursor`, `gemini`, `github-copilot`, `continue-cli`, `droid`, `ai_tab`, `opencode`, `agent-v1` (generic extension path)

Coverage note:
- “Supported” means different depths per agent: some have dedicated hook installers + transcript parsers; others are integrated through VS Code/JetBrains channels or generic preset pathways.

### How It Actually Runs Per Edit Cycle

1. Agent hook fires (`PreToolUse` or `PostToolUse` style event).
2. Hook command executes `git-ai checkpoint <preset> --hook-input ...`.
3. Preset parser normalizes payload/transcript into `AgentRunResult`.
4. Checkpoint logic resolves candidate changed files and computes line attribution deltas.
5. Result is appended to `.git/ai/working_logs/<base>/checkpoints.jsonl` with blob snapshots.

### Evidence

| Evidence                                         | What It Shows                                                                               |
|--------------------------------------------------|---------------------------------------------------------------------------------------------|
| `src/mdm/agents/mod.rs`                          | Central installer registry for Claude/Codex/Cursor/VSCode/OpenCode/Gemini/Droid/JetBrains   |
| `src/mdm/agents/claude_code.rs`                  | Injects PreToolUse/PostToolUse commands invoking `git-ai checkpoint ... --hook-input stdin` |
| `src/commands/checkpoint_agent/agent_presets.rs` | Per-agent parsing logic for hook payloads/transcripts/IDs/models/file paths                 |
| `README.md` + docs supported agents page         | Publicly declared integration coverage                                                      |

### Strengths / Trade-offs

| Strength                                                                        | Trade-off                                                         |
|---------------------------------------------------------------------------------|-------------------------------------------------------------------|
| High fidelity for integrated agents (explicit hook events + transcript parsing) | Coverage quality depends on each agent integration’s hook surface |
| Unified preset interface lowers integration friction                            | Unsupported agents fall back to human-style checkpoint behavior   |

---

## Challenge 2: Capturing File Change & Micro-versioning

### Implementation

Git AI does not wait for commit to infer provenance. It persists **checkpoint deltas continuously** under `.git/ai/working_logs/<base>/`:
- `checkpoints.jsonl`: ordered checkpoint events
- `blobs/`: file snapshots content-addressed by SHA
- `INITIAL`: carry-forward baseline attributions

Each checkpoint stores file-level/line-level attribution state + optional transcript/agent metadata.

### Micro-versioning Mechanics

1. Determine current base commit (or `initial` for zero-commit repos).
2. Load previous checkpoint state for that base.
3. Snapshot latest file contents into blob store.
4. Compute per-file and per-line attribution deltas.
5. Append a checkpoint event, preserving temporal order of edits before commit.

### Evidence

| Evidence                        | What It Shows                                                              |
|---------------------------------|----------------------------------------------------------------------------|
| `src/git/repo_storage.rs`       | Working log directory layout and blob/checkpoint persistence               |
| `src/authorship/working_log.rs` | Checkpoint schema with timestamp, kind, transcript, agent ID, line stats   |
| `src/commands/checkpoint.rs`    | Checkpoint append flow, pre-commit checkpoint behavior, file-state hashing |

### Metaphor

Git commit history is the **photo album** (few snapshots).
Git AI checkpoints are the **security camera feed** between photos.

### Strengths / Trade-offs

| Strength                                                             | Trade-off                                     |
|----------------------------------------------------------------------|-----------------------------------------------|
| Preserves intermediate AI/human edit intent before commit flattening | Extra local state under `.git/ai/` to manage  |
| Captures iterative agent loops and partial edits                     | Storage/perf complexity vs commit-only models |

---

## Challenge 3: Correlating Agent Change to File Change

### Implementation

Git AI uses layered correlation:
1. Hook payload path hints (`edited_filepaths` / `will_edit_filepaths`)
2. Transcript-derived tool-use extraction (agent-specific parsers)
3. Git status + tracked file delta + prior checkpoint/INITIAL context
4. Text-file filtering and path normalization to avoid cross-repo/path errors

This allows attribution even when some events are under-specified.

### Correlation Order (Highest To Lowest Confidence)

1. Explicit hook file paths from agent payload.
2. Transcript-derived tool-use file paths.
3. Existing AI-touched files in working log + INITIAL carry-forward.
4. Current git status/diff over tracked text files.
5. Dirty-file overrides when hook runtime provides in-memory file content.

### Evidence

| Evidence                                                                  | What It Shows                                                |
|---------------------------------------------------------------------------|--------------------------------------------------------------|
| `src/commands/checkpoint.rs` (`pathspec_filter`, `get_all_tracked_files`) | Multi-source file set construction and status reconciliation |
| `agent_presets.rs` (Copilot/Cursor/Claude/Gemini extractors)              | Tool-event and transcript-based file discovery               |
| `src/git/repo_storage.rs` (`all_ai_touched_files`)                        | Prior AI-touched files included in ongoing correlation       |

### Strengths / Trade-offs

| Strength                                                                 | Trade-off                                                                         |
|--------------------------------------------------------------------------|-----------------------------------------------------------------------------------|
| Explicit hook + transcript + git-state fusion reduces missed attribution | Generic shell actions without explicit paths may still require heuristic fallback |
| Session-level IDs keep multi-session edits separable                     | Accuracy depends on hook payload quality from each upstream agent                 |

---

## Challenge 4: Representing Agent Provenance

### Implementation

Git AI standardizes provenance as an **Authorship Log** with two sections:
- **Attestation section**: file + line ranges mapped to prompt hashes
- **Metadata JSON**: schema/version/base SHA/prompt records/agent ID/messages/tool use

Open specification: `authorship/3.0.0`.

### Concrete Representation Samples

Representative Git Note attachment:

```text
refs/notes/ai @ <commit-sha>
```

Representative note payload (attestation + metadata):

```text
src/server/auth.ts
  abcd1234abcd1234 10-24,31-33
  e5f6a7b8c9d0e1f2 40-47
---
{
  "schema_version": "authorship/3.0.0",
  "git_ai_version": "1.1.4",
  "base_commit_sha": "7734793b756b3921c88db5375a8c156e9532447b",
  "prompts": {
    "abcd1234abcd1234": {
      "agent_id": {
        "tool": "claude",
        "id": "cb947e5b-246e-4253-a953-631f7e464c6b",
        "model": "claude-sonnet-4-5"
      },
      "human_author": "Dev Example <dev@example.com>",
      "messages": [
        {"type": "user", "text": "Add OAuth callback validation"},
        {"type": "assistant", "text": "I'll add validation and tests"},
        {"type": "tool_use", "name": "Edit", "input": {"file_path": "src/server/auth.ts"}}
      ],
      "total_additions": 36,
      "total_deletions": 4,
      "accepted_lines": 27,
      "overridden_lines": 3
    }
  }
}
```

### Representation Resolution Path

1. `git blame` identifies source commit for a line.
2. Git AI reads note from `refs/notes/ai` for that commit.
3. Attestation section resolves prompt hash for the exact line range.
4. Metadata section resolves agent/model/session/messages for that prompt hash.

### Evidence

| Evidence                                         | What It Shows                                                      |
|--------------------------------------------------|--------------------------------------------------------------------|
| `specs/git_ai_standard_v3.0.0.md`                | Formal format and required semantics                               |
| `src/authorship/authorship_log_serialization.rs` | Serialization/version handling                                     |
| `src/authorship/post_commit.rs`                  | Prompt redaction/stripping based on storage mode before note write |

### Metaphor

The attestation map is the **street map**; prompt metadata is the **trip diary** for each road segment.

### Strengths / Trade-offs

| Strength                                                  | Trade-off                                                |
|-----------------------------------------------------------|----------------------------------------------------------|
| Line-range precision is directly queryable by blame tools | Rich metadata can be large unless redacted/stripped      |
| Open spec enables interoperability                        | Standard evolution requires version migration discipline |

---

## Challenge 5: Storing Agent Provenance In Git

### Implementation

Git AI stores final provenance in **Git Notes**:
- primary: `refs/notes/ai`
- stash-specific: `refs/notes/ai-stash`

It also includes lifecycle maintenance for history operations (commit/amend/rebase/cherry-pick/merge/reset/stash/switch/checkout) and note sync logic during fetch/push/clone.

### Storage Lifecycle Details

1. In-flight attribution accumulates in working log under `.git/ai/working_logs/<base>/`.
2. On commit, Git AI converts working log state to Authorship Log.
3. Authorship Log is attached to new commit in `refs/notes/ai`.
4. Rewrite events trigger remap/recompute paths to preserve note semantics across new SHAs.
5. Fetch/push/clone hooks sync notes to keep distributed provenance consistent.

### Evidence

| Evidence                                                         | What It Shows                                        |
|------------------------------------------------------------------|------------------------------------------------------|
| `src/git/refs.rs`                                                | Note add/batch add, note namespace conventions       |
| `src/commands/git_handlers.rs`                                   | Pre/post hook dispatch across git lifecycle commands |
| `src/authorship/rebase_authorship.rs` + `src/git/rewrite_log.rs` | Rewrite event processing and attribution remapping   |
| `src/git/sync_authorship.rs` + fetch/push/clone hooks            | Remote notes fetch/merge/push behavior               |
| `src/commands/hooks/stash_hooks.rs`                              | Dedicated stash provenance preservation              |

### Strengths / Trade-offs

| Strength                                                               | Trade-off                                             |
|------------------------------------------------------------------------|-------------------------------------------------------|
| Provenance survives commit SHA changes through rewrite-aware remapping | Notes are out-of-band; teams must sync notes reliably |
| Keeps commit objects clean (no commit-message bloat)                   | Some tools/workflows ignore notes by default          |

---

## Challenge 6: AI Blame

### Implementation

`git-ai blame` overlays native `git blame` hunks with Git AI authorship records and can expose prompt-linked context (`--show-prompt`, porcelain/json/incremental modes).

### Blame Query Path

1. Run native blame hunk resolution for target file/range.
2. For each commit in blame hunks, load and parse its Git AI note (if present).
3. Match blamed line numbers to attested line ranges.
4. Emit blended output with git author + AI session/agent attribution.

### Evidence

| Evidence                | What It Shows                                           |
|-------------------------|---------------------------------------------------------|
| `src/commands/blame.rs` | Blame hunk parsing + AI overlay pipeline + output modes |
| README + docs reference | UX parity goal with `git blame` flags                   |

### Strengths / Trade-offs

| Strength                                                                  | Trade-off                                                                            |
|---------------------------------------------------------------------------|--------------------------------------------------------------------------------------|
| Real line-level provenance answers “who/which agent/which session/prompt” | If notes are missing/unsynced, output quality degrades to baseline git blame context |
| Multiple output modes enable CLI + machine consumption                    | Prompt visibility depends on configured prompt storage policy                        |

---

## Challenge 7: Developer Experience (DX)

### Implementation

Git AI optimizes for low-friction adoption:
- install script places `git-ai` + `git` shim in `~/.git-ai/bin` and preserves original git as `git-og`
- `install-hooks` configures supported agents globally
- “no per-repo setup” workflow for supported environments
- normal `git commit/push/fetch` habits preserved with wrapper interception

### Operational Burden (What Devs Must Remember)

1. Ensure `git-ai` shim is active in PATH where git commands run.
2. Keep agent hook installation healthy (`git-ai install-hooks` when needed).
3. Ensure notes are fetched/pushed in team workflows (or allow Git AI sync hooks to run).
4. Choose prompt storage mode intentionally (`default`, `notes`, `local`) based on privacy/compliance needs.

### Evidence

| Evidence                           | What It Shows                            |
|------------------------------------|------------------------------------------|
| `install.sh`                       | PATH shim strategy, automatic hook setup |
| README claim                       | No per-repo setup positioning            |
| `src/config.rs` + `post_commit.rs` | Prompt storage/privacy policy knobs      |

### Strengths / Trade-offs

| Strength                                                    | Trade-off                                                                              |
|-------------------------------------------------------------|----------------------------------------------------------------------------------------|
| Minimal behavioral change for users already living in Git   | Wrapper/shim approach introduces environment/path coupling                             |
| Works offline/local-first while supporting team sync models | Advanced behavior depends on successful hook installation and agent config permissions |

---

## Activity, Support, and Community (as of 2026-02-18 UTC)

### GitHub Signals

| Repo                    | Stars | Forks | Open Issues | Last Push (UTC)      |
|-------------------------|------:|------:|------------:|----------------------|
| `git-ai-project/git-ai` | 1,057 |    74 |          51 | 2026-02-18T05:58:29Z |
| `git-ai-project/action` |     2 |     2 |           1 | 2025-12-28T22:33:52Z |

### Delivery Cadence (recent releases)

| Repo                    | Version               | Published (UTC)      | Channel    |
|-------------------------|-----------------------|----------------------|------------|
| `git-ai-project/git-ai` | `v1.1.4`              | 2026-02-17T23:06:56Z | Stable     |
| `git-ai-project/git-ai` | `v1.1.3`              | 2026-02-12T14:23:41Z | Stable     |
| `git-ai-project/git-ai` | `v1.1.2`              | 2026-02-11T17:11:02Z | Stable     |
| `git-ai-project/git-ai` | `v1.1.1`              | 2026-02-09T19:25:42Z | Stable     |
| `git-ai-project/git-ai` | `v1.1.1-next-6ad2609` | 2026-02-09T14:06:13Z | Prerelease |

### Collaboration Signals

| Metric                                 |                                        Value |
|----------------------------------------|---------------------------------------------:|
| Open PRs (`git-ai`)                    |                                           14 |
| Closed PRs (`git-ai`)                  |                                          351 |
| Top contributors (recent API snapshot) | `acunniffe`, `svarlamov`, `jwiegley`, others |

Interpretation: active repo with frequent releases and sustained merge volume.

---

## Overall Assessment For SpecStory Research

Git AI’s strongest differentiator is that it ships a **full chain** from hook capture to line-level blame with rewrite-safe git-note persistence and an open format. If your target is rigorous per-line provenance in Git-native workflows, this is a high-quality reference design.

Main cost centers to study carefully for SpecStory architecture decisions:
- complexity of rewrite maintenance
- note sync reliability assumptions
- dependency on high-fidelity agent hook ecosystems

---

## Sources

### Product / Docs
- https://usegitai.com/
- https://usegitai.com/docs/cli
- https://usegitai.com/docs/cli/reference
- https://usegitai.com/docs/cli/add-your-agent
- https://usegitai.com/docs/enterprise

### Code / Spec
- https://github.com/git-ai-project/git-ai
- https://github.com/git-ai-project/git-ai/blob/main/specs/git_ai_standard_v3.0.0.md
- https://github.com/git-ai-project/action

### Activity Metrics (GitHub API)
- https://api.github.com/repos/git-ai-project/git-ai
- https://api.github.com/repos/git-ai-project/action
- https://api.github.com/repos/git-ai-project/git-ai/releases?per_page=10
- https://api.github.com/search/issues?q=repo:git-ai-project/git-ai+type:pr+state:open
- https://api.github.com/search/issues?q=repo:git-ai-project/git-ai+type:pr+state:closed
- https://api.github.com/repos/git-ai-project/git-ai/contributors?per_page=10
