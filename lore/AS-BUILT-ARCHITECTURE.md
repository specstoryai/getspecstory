# Lore As-Built Architecture

**Last Updated**: 2026-06-10

> **Scope note**: This document describes the Lore skill as built - the deterministic engine, the corpus, the agent contract, and the deep-mine pipeline. The planned v4 Go indexer inside `specstory-cli` is covered only in [Roadmap](#known-limitations--roadmap); it does not exist yet.

> **Vocabulary note**: The product name is **SpecStory Lore**; the skill/command is **`lore`** (renamed from `skill-forge`, 2026-06-09). "Lore" is the asset - the persistent corpus of mined sessions. "**Forge**" is reserved as the verb for the final act of creating a skill the user selected; mining and judging phases never use forge-language (see the Voice rule in `SKILL.md`).

This document describes the technical architecture of Lore - a harness-portable agent skill that mines SpecStory coding histories (from any AI coding agent) into a persistent beat corpus, surfaces reproducible workflows with corroborated evidence and outcome labels, deep-mines the top candidates into verified dossiers, and interactively forges the chosen ones into skills installed across every agent harness on the machine.

## Table of Contents

1. [System Overview](#system-overview)
2. [Core Concepts](#core-concepts)
3. [Repository Layout & Install Topology](#repository-layout--install-topology)
4. [The Engine](#the-engine)
5. [Corpus Schema](#corpus-schema)
6. [Transcript Format Parsing](#transcript-format-parsing)
7. [Beat Model & Outcome Labeling](#beat-model--outcome-labeling)
8. [Candidate Generation & Scoring](#candidate-generation--scoring)
9. [Phase C: Deep-Mine](#phase-c-deep-mine)
10. [The Agent Contract (SKILL.md)](#the-agent-contract-skillmd)
11. [Idempotency Contract](#idempotency-contract)
12. [Testing](#testing)
13. [Design Provenance & Decisions](#design-provenance--decisions)
14. [Known Limitations & Roadmap](#known-limitations--roadmap)

## System Overview

Lore is two halves with a sharp boundary (the "last30days split"): a **deterministic engine** that parses, segments, joins, and counts - and an **agent contract** that names, judges, verifies, and forges. The engine cannot hallucinate (no LLM, no network, no API keys); the agent never reads whole transcripts (only engine-exported evidence).

```
 .specstory/history/*.md  (any provider: Claude Code, Codex, Cursor, Gemini, ...)
        │
        ▼  index (incremental, idempotent)
 ┌──────────────────────────┐
 │  ~/.specstory/lore.db    │   sessions / beats / commands / grams / meta_hits / dossiers
 └──────────────────────────┘
        │                         │                          │
        ▼  report (SQL joins)     ▼  beats (span export)    ▼  beats --shape/--intent-re (sampling lenses)
 EVIDENCE FOR SYNTHESIS      exact transcript spans        theme-sweep (LLM lenses propose SEMANTIC
 (corroborated / runbooks /       │                        clusters of latent expertise; adversarial verify)
  intents / meta-skills)          │                             │
        │                         ▼  deep-mine (per-harness)    ▼  themes table (stable session#ord keys)
        │                    verified dossiers ──► dossier cache ◄── beats --theme <id>
        ▼
 calling agent: synthesize → verify (2b) → deep-mine (2c) → dossiers (3) → curate → FORGE
        │
        ▼
 ~/.agents/skills/<name>/SKILL.md   (written once, symlinked into every harness)
```

## Core Concepts

**Beat** - the unit of analysis. One user turn (the INTENT) + all agent activity until the next user turn (the METHOD: tool mix, executed commands, files touched, exit-code failures) + the next user turn's reaction (the OUTCOME label). N-grams of commands are computed *within* an beat, never across beats.

**Outcome label** - free supervision extracted from the user's own next reply: a steering correction ("no / wait / still broken") labels the prior beat `corrected`; approval ("ok write a commit", "perfect") labels it `success`; anything else `neutral`; the last beat of a session is `end`. Classifiers: `CORRECTED_RE` / `SUCCESS_RE` in `scripts/lib/patterns.mjs`.

**Corroboration** - the strongest truth signal the engine can compute deterministically: an intent signature and a command n-gram co-occurring in the *same beats* ("the user asked for X and the agent did Y, repeatedly"), reported with outcome rates. These are the deep-skill seeds.

**Executed-command attribution** - commands are extracted only from inside shell `<tool-use>` envelopes (or legacy `Tool use: **Bash**` blocks), so every counted command was genuinely run by the agent - never a pasted example in prose.

**Theme** - a SEMANTIC cluster: beats grouped by meaning rather than surface form, proposed by
thematic LLM miners over engine-sampled spans and adversarially verified. Members are stored by stable
key (`session_id#ord`, survives re-indexing); a theme flows into deep-mine/dossier/forge exactly like
a corroborated cluster. This is the latent-expertise channel - practices the user operates without
naming (review judgment, decision craft, model direction).

**Shape** - a deterministic beat classification from the stored tool mix (`shapeOf`):
`conversation` (no tools - pure judgment), `read-only` (diagnosis), `shell`, `write`. Sampling lenses
use shapes to point theme miners at exactly the beats the command channel ignores.

**Portability** - in cross-project mode, candidates recurring in ≥2 projects are PORTABLE (forge to personal scope) vs PROJECT-SPECIFIC (forge into that repo). Project identity is the stable `git_id` from `.specstory/.project.json` (SHA-256 of the normalized git origin URL); the path-hash `workspace_id` is machine-local and deliberately not used.

## Repository Layout & Install Topology

```
<clone>/lore                         ← github.com/specstoryai/getspecstory (public monorepo)
├── SKILL.md                          the agent contract (agentskills.io format, repo root
│                                     so the skill dir can symlink straight to the repo)
├── AS-BUILT-ARCHITECTURE.md          this document
├── README.md / CHANGELOG.md / package.json   (zero dependencies; engines: node >= 22.5)
├── scripts/
│   ├── mine-skills.mjs               thin CLI entry (args + subcommand dispatch)
│   ├── deep-mine.workflow.js         Phase C orchestration for Claude Code's Workflow fan-out
│   ├── theme-sweep.workflow.js       Phase B′ thematic lenses (latent-expertise mining + verification)
│   └── lib/                          purpose-driven modules (see The Engine)
├── fixtures/                         synthetic per-provider transcripts + projC semantic channel + golden plan (executable spec)
└── tests/engine.test.mjs             29 node:test cases (npm test)
```

Install topology - **live-clone freshness**: the repo is the single source of truth; a `git pull` updates every harness instantly because nothing is copied:

```
<local clone>    ←  ~/.agents/skills/lore (symlink; Amp reads this dir natively)
                      ←  ~/.claude/skills/lore          (Claude Code)
                      ←  ~/.codex/skills/lore           (Codex CLI)
                      ←  ~/.config/opencode/skills/lore (OpenCode)
```

Forged skills follow the same pattern: written once to `~/.agents/skills/<name>/`, then symlinked into every detected harness skills dir (SKILL.md Step 4).

## The Engine

Entry point `scripts/mine-skills.mjs` parses flags and dispatches; all logic lives in `scripts/lib/`:

| Module | Responsibility | Key exports |
|---|---|---|
| `patterns.mjs` | every regex, vocabulary set, and tiny classifier (the verified output formula encoded) | `SESSION_HDR`, `TOOLUSE_OPEN`, `LEGACY_TOOL`, `SHELL_TOOLS`/`SHELL_EXCLUDE`, `VERBS`/`NOISE`/`COMMON`/`STOP`, `META`, `CORRECTED_RE`/`SUCCESS_RE`, `classifyOutcome` |
| `discover.mjs` | find projects/transcripts; resolve stable project identity | `walkMd` (recursive), `readLabel` (git_id), `discoverProjects`, `fileDate` |
| `parse.mjs` | **pure** text → beats parsing; no I/O, fully unit-testable | `parseSessionFile`, `extractShellBlock`, `extractFiles`, `headsFrom`, `intentSig`, `sniffAuthor` |
| `db.mjs` | corpus schema + migrations | `openDb`, `PARSER_VERSION`, `deleteSessionRows` |
| `indexer.mjs` | incremental indexing; the idempotency contract | `indexCorpus`, `pruneCorpus` |
| `report.mjs` | corroboration SQL, scoring, evidence-block emitters | `report`, `wantedKinds` |
| `beats.mjs` | span export, sampling lenses, dossier cache/render, themes | `exportBeats`/`exportRows`, `sampleBeats`, `shapeOf`, `rowsByKeys`, `beatsFingerprint`, `getDossier`/`putDossier`/`renderDossiers`, `putTheme`/`listThemes`/`getTheme` |
| `forged.mjs` | forged-skill registry: provenance, declines, drift detection (cluster kind inferred from key shape; theme kind first-class) | `addForged`, `declineCandidate`, `listForged`, `checkForged`, `inferKind` |

Subcommands:

| Command | Purpose |
|---|---|
| `index --dir <hist> \| --projects <parent> \| --scan <root> [--days N] [--force]` | parse new/changed transcripts into the corpus (incremental; `--scan` finds histories at any depth) |
| `report [--min-sessions N] [--top N] [--days N] [--kind cmd,task,meta,corr] [--filter S] [--emit json]` | ranked candidates wrapped in `<!-- EVIDENCE FOR SYNTHESIS -->` markers |
| `beats --corr\|--gram\|--sig\|--meta` · `--keys <k,k>` · `--theme <id>` · or sampling `--project/--shape/--intent-re/--min-intent-len` | transcript spans: per cluster, per stable key set, per theme, or stratified samples for theme mining (every span carries a stable `session_id#ord` key) |
| `dossier get\|put\|render` | deep-mine result cache + canonical pass-through rendering (LAW 1 sentinel) |
| `plan render --file <manifest>` · `plan last` · `plan list` | the ENTIRE curation plan, engine-assembled from a judgments manifest (`proposed:[{cluster\|theme, name}]`, `skipped:[{candidate, reason}]`); dossier and theme cards embedded verbatim, sentinel last. A PreToolUse hook on ExitPlanMode (SKILL.md frontmatter, `${CLAUDE_SKILL_DIR}/scripts/hooks/validate-plan.mjs`) denies any plan that is not this artifact |
| `status` | the what-has-Lore-done view: corpus, mined artifacts, registry health, recent activity (pass-through) |
| `runs add\|list` | the activity journal: auto-entries per engine invocation + the agent's end-of-run summary |
| `theme put\|list\|get\|render\|expand\|grow` | semantic clusters as corpus objects (members by stable key, fingerprinted). `expand` finds corpus-wide candidate members deterministically (lift-scored discriminating vocabulary from member intents); `grow` records agent-verified members and refingerprints; `render` shows prevalence + outcome lift vs corpus baseline (≥3 judged members) |
| `forged add\|decline\|list\|check` | the forged-skill registry: provenance at forge time, user declines, and the drift report (`check` recommends update / suppress / re-engage / orphaned per row) |
| `prune` | drop sessions whose transcript files no longer exist; flag duplicate project identities |
| `reset [--and-skills]` | wipe ALL persistence (corpus, dossiers, registry); optionally remove forged skill files |
| *(legacy)* `--dir` with no subcommand | index + report in one shot |

Default corpus: `~/.specstory/lore.db` (override `--db`). Performance: ~1,250 stoa sessions index in ~26s; reports are instant SQL.

## Corpus Schema

`scripts/lib/db.mjs` - SQLite via `node:sqlite` (built into Node ≥ 22.5; zero dependencies):

```sql
sessions(id TEXT PK,            -- project_id + '/' + filename (stable across reorganizations)
         project_id, project_name, path, date, agent,
         size INTEGER, mtime INTEGER, parser INTEGER,   -- the idempotency fingerprint
         author TEXT,                                  -- git add-author > path-sniff > machine user
         beats INTEGER)
beats(id INTEGER PK AUTOINCREMENT, session_id, ord, start_line,
         intent_raw, intent_sig,                         -- "write:commit"
         n_tools, tool_mix,                              -- "shell:5,read:3,write:2"
         files, n_cmds, exit_fails, outcome)             -- success|corrected|neutral|end
commands(beat_id, ord, head, raw, line)               -- head = "git status", raw = full command
grams(beat_id, n, gram)                               -- per-beat command n-grams, n=2..4
meta_hits(beat_id, meta_id, quote, line)              -- way-of-working detector hits
dossiers(cluster_key TEXT PK, fingerprint, json, created)  -- Phase C cache
themes(theme_id TEXT PK, title, description,
       beat_keys,                              -- JSON array of stable session_id#ord keys
       fingerprint, evidence, created)
forged(name TEXT PK, status,                               -- active | declined
       skill_path, cluster_key, kind, fingerprint,
       sessions, ok, bad,                                  -- evidence state at forge/decline time
       content_sha, created, note)                         -- hand-edit detection + provenance
```

Sessions also carry `uuid` (the provider session id - `prune` flags the same session indexed from
two places). Writes are per-session transactions (`busy_timeout=10s` for concurrent runs).

`PARSER_VERSION` (currently 2) is stamped on every session; `openDb` runs additive `ALTER TABLE` migrations for corpora created before a column existed.

## Transcript Format Parsing

The parser encodes the **verified specstory-cli output formula** (reverse-engineered from `pkg/session/markdown.go` + per-provider `markdown_tools.go`, validated against real transcripts).

**Session header** (all providers): `<!-- <Provider Name> Session <uuid> (<ts>) -->` - matched generically by `SESSION_HDR`; the provider name is slugified into the agent tag (`claude-code`, `codex-cli`, `cursor`, `gemini-cli`, ...). New providers work without code changes.

**Turn markers**: `_**User (ts)**_` and `_**Agent (model ts)**_` (sidechain subagents carry a ` - sidechain` suffix). Legacy 2025 transcripts use `_**User**_` without a timestamp - also matched.

**Modern (Markdown v2.1) tool envelope**: every tool call is `<tool-use data-tool-type="T" data-tool-name="N"><details>…</details></tool-use>`. Shell detection is **type-based** (`data-tool-type="shell"`, each provider's own classifier) with a name fallback (`SHELL_TOOLS`) and a noise exclusion (`SHELL_EXCLUDE`: LS, list_directory). The four command locations:

| Provider / form | Where the command lives |
|---|---|
| Claude Code, single-line | inline `` `cmd` `` on its own body line (no fence) |
| Codex, single-line | inline backtick **inside the `<summary>` line** |
| Any, multi-line | ` ```bash ` fence (heredoc bodies skipped) |
| Codex legacy `shell` | `- command: ` `` `[bash -lc …]` `` bullet under `**Input:**` |

Tool *output* renders as ` ```text ` (Claude) or a plain ` ``` ` fence (Codex) and is never read as commands - only scanned for `exited with code N` and error-head patterns (`fails` counter).

**Legacy (~2025, pre-envelope) format**: bare `Tool use: **Name** desc` lines. `Tool use: **Bash**` is followed by an inline `` `cmd` `` line (and/or a bash fence), then `Result:` with a plain output fence. Handled by the `LEGACY_TOOL` branch in `parseSessionFile`; `LEGACY_TYPE` maps tool names to types. This recovers command data from older corpora (e.g. BearClaude's 1,013 Bash calls, previously invisible).

**Corpus-audited (1,311 real sessions):** the four command locations include multi-line
`- command:` bullets (close-backtick lands lines later - 2,313 recovered) and the legacy codex
`Tool use: **shell**` form with `Output:` markers (6,787 recovered); `TaskOutput`/`KillShell`/
`BashOutput` are excluded as non-executor shell types. `patterns.mjs` is organized as an explicit
format grammar: every regex is preceded by the verbatim transcript bytes it matches.

**Command normalization** (`headsFrom`): split on `&&`/`;`, first segment of pipelines, strip `FOO=bar` env prefixes, unwrap `bash -lc '…'`, reject non-command-shaped tokens (kills heredoc/prose leakage like `Co-Authored-By:`), drop `NOISE` recon commands (`nl`, `cd`, `grep`, …), keep subcommands (`git status`, `supabase link`) and project scripts (`./scripts/run.sh deploy`).

## Beat Model & Outcome Labeling

`parseSessionFile` (pure function) walks a transcript once:

1. A `_**User**_` marker closes the previous beat and opens a new one; the intent block is captured (≤30 lines, stopping at the next turn marker). The intent yields `intent_sig` (`leadingVerb` from `VERBS` + first salient keyword via `tokenize`/`STOP`) and `META` detector hits (read-only-diagnosis, steering-correction-adjacent patterns, reasoning-dial, as-built-doc, goal-rider-spec, …).
2. Tool blocks accumulate onto the current beat: `tool_mix` counts by type; shell blocks yield commands + failure signals; read/write blocks yield file paths.
3. After the walk, **outcomes are assigned retroactively**: beat *k*'s label = `classifyOutcome(beat k+1's opening line)`.

Per-beat command n-grams (n = 2..4, deduped within the beat) become the `grams` rows - the procedure signal.

## Candidate Generation & Scoring

`report.mjs` computes four candidate families as SQL `GROUP BY`s with shared outcome columns (`ok`/`bad`/`done`):

- **RUNBOOKS** - `grams` grouped by gram (distinct-session count ≥ `--min-sessions`), longer n-grams subsuming shorter ones with equal support.
- **INTENTS** - beats grouped by `intent_sig`.
- **META-SKILLS** - `meta_hits` grouped by detector id (threshold `max(2, minSessions-1)`).
- **CORROBORATED** - the headline: `beats ⋈ grams` grouped by `(intent_sig, gram)` with `g.n ≥ 2` - intent × procedure in the same beats, with outcome rates.

Scoring (single-channel candidates):

```
score = 0.25·min(sessions/30, 1)        frequency
      + 0.10·min(spanDays/120, 1)       persistence over time
      + 0.10·recencyBoost               ≤30d: 1.0 · ≤90d: 0.6 · else 0.3
      + 0.15·regularity                 longer command sequences score higher
      + 0.15·specificity                fraction of heads NOT in COMMON (supabase > bare git)
      + 0.25·outcomeRate                ok/done, 0.5 when no labeled outcomes
```

Corroborated pairs use `0.4·support + 0.35·outcomeRate + 0.25·specificity`. Cross-project mode splits every section into PORTABLE (`np ≥ 2`) vs PROJECT-SPECIFIC. Output is wrapped in `<!-- EVIDENCE FOR SYNTHESIS … -->` markers - the contract that this is raw material for the agent, never user-facing; each evidence line is one openable beat: `path:line [outcome] intent="…" cmds="…"`.

## Phase C: Deep-Mine

Sampling 1–2 beats per candidate produces shallow dossiers; Phase C reads **every beat in a cluster** - especially the `corrected` ones, which carry the failure-modes content that distinguishes a deep skill from a runbook.

**`beats` export** (`beats.mjs`) - the deterministic prep: resolves a cluster selector to its beats, **stratifies corrected-first** (then success, then recent neutral, capped by `--max`), slices each span precisely (`start_line` → next beat's `start_line`, capped by `--span-lines`), and computes a **content fingerprint** from stable beat identity `(session_id, ord, start_line, outcome, n_cmds)` - NOT autoincrement ids - so it survives re-indexing of unchanged content and invalidates when the cluster actually changes.

**Dossier cache** - `dossiers` table keyed by `(cluster_key, fingerprint)`. Deep-mining is once-per-cluster-ever; `/lore` reruns reuse cached dossiers when the fingerprint still matches (verified live: a corpus rebuild that grew the clusters correctly invalidated both cached dossiers).

**Orchestration** (`deep-mine.workflow.js` + SKILL.md Step 2c) - per cluster, one **miner** agent (runs the `beats` export via Bash, reads every span, returns a `DOSSIER_SCHEMA` object: trigger, preconditions, canonical steps, variations, verification moves, failure modes with recoveries and refs, varying parameters, confidence) and one **adversarial verifier** (re-reads the spans and tries to refute: one procedure or several conflated? do the failure modes' refs really say that?). Parallelism is **capability-based per harness**: Claude Code runs the bundled Workflow script; Codex/Amp spawn their own parallel subagents with the same briefs; sequential execution is the last resort only.

Live validation (2026-06-09): 2 real clusters, 4 agents, ~380s - both dossiers high-confidence with legitimate verifier corrections (e.g. "the heredoc commit style is claude-code-only; don't claim it as universal").

## The Agent Contract (SKILL.md)

The pipeline the calling agent executes, in order:

| Step | What | Key rule |
|---|---|---|
| 0.25 | guided start on bare invocation: one question round (scope / window / goal) | navigation questions are exempt from LAW 1 |
| 0 / 0.5 | locate histories (nested via `--scan`); interpret the user's input into engine flags | echo the resolved interpretation in one line |
| 1 | `index` then `report` | work only from the evidence block; never read whole transcripts |
| 2 | synthesize candidates (name, trigger, procedure, kind, scope) | check installed skills first - update-or-skip, never duplicate |
| 2b | truth check | start from CORROBORATED; re-open evidence spans; weigh outcomes honestly; refute the cheap explanation |
| 2b′ | theme sweep: thematic lenses over conversational/read-only samples → verified semantic themes | cache-first (`theme list`); themes join deep-mine like any cluster |
| 2c | deep-mine top ~6 corroborated clusters | cache-first; harness's own fan-out; verified dossiers replace sampled ones |
| 3 | **present dossiers, THEN curate** | Claude Code: curation as a PLAN via ExitPlanMode (dossiers ARE the approval surface); fallback: sentinel-proven dossier message + per-candidate previews |
| 4 | forge selected skills | write once to `~/.agents/skills/<name>`, symlink everywhere; bodies harness-agnostic; Verification + Failure-modes sections required |
| 5 | privacy scrub | secrets, project-refs, third-party names, `/Users/<name>/` paths |
| 6 | report | what was forged/discarded and why |

**Voice rule**: narrate as *mining lore for skill candidates*; "forge" appears only for Step 4's act. The frontmatter follows the agentskills.io standard (portable across 30+ tools); Claude-specific tool names are framed as "use your harness's equivalent."

## Idempotency Contract

| Scenario | Behavior |
|---|---|
| re-run on unchanged repo | skipped via fingerprint = **size + mtime + PARSER_VERSION** |
| new sessions appear | appended (accumulation is the design) |
| a session file grows/changes | beats replaced whole - never duplicated |
| engine/parser upgrade | `PARSER_VERSION` bump → automatic one-time full re-parse |
| `--force` | re-index everything regardless |
| transcript deleted/moved away | `prune` drops orphaned rows (no dangling beats - tested) |
| project gains a git remote (new `git_id`) | `prune` flags the duplicate-identity group |
| re-running `/lore` after forging | the `forged` registry is authoritative: `forged check` compares each skill's forge-time evidence state to the current corpus and recommends `up-to-date` / `update` (new corrected beats or ≥1.5× session growth) / `update-carefully` (file hand-edited, detected by content hash) / `orphaned` (file deleted) |
| user declines a candidate | recorded via `forged decline`; suppressed on future runs until evidence grows materially (`re-engage`) |
| dossier cache | reused only while the cluster fingerprint matches |

## Testing

`tests/engine.test.mjs` - 19 `node:test` cases, zero test dependencies (`npm test`):

- **Unit** (via the pure `parse.mjs`): `headsFrom` normalization; all four modern command locations + the legacy format; beat segmentation; outcome labeling (success *and* corrected); meta detectors; `intentSig`.
- **Integration**: index the fixtures → assert multi-agent tags (`claude-code`/`codex-cli`/`cursor-cli`), exit-code capture, corroborated pair with both outcome polarities, cross-project portability (`np: 2`).
- **Idempotency**: index twice (second run fully skipped) → `--force` (replace, not duplicate) → delete a transcript → `prune` (no orphan rows).
- **Phase C**: stratified export, fingerprint stability across calls, dossier cache roundtrip.

`fixtures/` is the **executable specification of the transcript formula** - two fake projects with `.project.json` identities covering: Claude inline/fence/heredoc-commit, Codex in-summary/exit-codes/legacy-bullet, Cursor type-based shell detection, and the legacy 2025 `Tool use:` format. These are the conformance bar for the future Go port.

## Design Provenance & Decisions

- **The last30days split** - deterministic engine + agent synthesis, the same architecture as the most sophisticated skill surveyed (~20k-line Python pipeline with SQLite store). Lore deliberately keeps the engine **zero-key** (no LLM calls in the engine, unlike last30days) because the calling agent's harness already has a model.
- **25-patterns lineage** - the mine → synthesize → adversarially-verify → critique discipline comes from the 3-stage Workflow that produced *25 Patterns in Agentic Engineering* (`extract-agentic-engineering/scripts/stage{1,2,3}-*.workflow.js`); Phase C's miner/verifier pair is that machinery pointed at pre-segmented beat spans.
- **Beats over n-grams** - deep skills live in the arc *intent → method → outcome*, not in token frequencies; the user's next reply is free outcome supervision.
- **Node + `node:sqlite`** for the skill-layer engine (most-available runtime across harnesses; zero deps); **Go in specstory-cli** is the agreed binary-runtime path, not bundled binaries in the skill folder (no mainstream skill ships prebuilt binaries; compile-on-demand or interpreter runtimes are the ecosystem norm).
- **Top-level `lore/` in the `getspecstory` monorepo** (moved 2026-06-11 from the standalone `specstoryai/lore`, now archived) - one public repo, namespaced `lore/v*` release tags; the symlink chain still gives live-clone freshness.

## Known Limitations & Roadmap

**Limitations (current, by design or deferred):**

- Outcome labels are conservative: most beats are `neutral` (the next prompt is usually a new task); success/corrected denominators are small and the SKILL.md instructs treating them as signal, not proof.
- Intent clustering is lexical (`verb:keyword`) - paraphrases split. The THEME channel covers the semantic gap for discovery (LLM lenses cluster by meaning); embeddings remain deferred for deterministic semantic clustering.
- `META` detectors are a fixed hand-curated list.
- Evidence `path:line` refs can drift if a file changes after indexing (self-corrects on re-index; Step 2b re-opens refs before forging).
- Sessions with no recognizable header tag as `unknown` (29 of BearClaude's 253).

**Roadmap:**

1. **Git-log augmentation (beyond author attribution, which is built):**
   *commit-survival outcomes* - beats containing `git commit` matched to real commits
   (session time-window + author); a commit that reached main and was never reverted upgrades the
   beat to durable success, far stronger than next-prompt reactions; *session↔commit linking*
   via Co-Authored-By trailers so dossiers can claim "this procedure produced N commits that
   shipped"; *evidence freshness* - verify files cited in dossier steps still exist at HEAD before
   forging; *convention mining* - ground commit-style skills in real `git log` examples.
2. **v4: `specstory lore` Go subcommand** in specstory-cli - consume raw provider JSONL via the existing providers instead of parsing the markdown render; the fixtures/tests here are the conformance spec. The skill stays the thin portable contract.
2. Semantic intent clustering (local embeddings cached in the corpus) if lexical signatures prove too coarse in practice.
3. Cursor/Gemini machine-level install points for the forge fan-out, once their skills directories are verified.
