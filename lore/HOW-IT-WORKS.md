# How Lore Works

A narrative walkthrough of the whole pipeline, stage by stage. The companion
[AS-BUILT-ARCHITECTURE.md](AS-BUILT-ARCHITECTURE.md) is the reference; this is the tour.

## The one-screen version

```
                        YOUR CODING SESSIONS (any agent, any era)
                     .specstory/history/*.md  ·  committed or local
                                      │
                       ╔══════════════▼═══════════════╗
                       ║   1. PARSE → BEATS        ║   deterministic · zero deps
                       ║   (intent → method → outcome)║   node:sqlite · no LLM
                       ╚══════════════╤═══════════════╝
                                      │ incremental, idempotent, atomic
                                      ▼
                          ┌──────────────────────┐
                          │  ~/.specstory/lore.db │  ← ALL durable state, one file
                          └──────────┬───────────┘
              ┌──────────────┬───────┴────────┬──────────────────┐
              ▼              ▼                ▼                  ▼
      2a. COMMANDS    2b. INTENTS      2c. META           2d. THEMES (semantic)
      n-grams over    verb:keyword     ways-of-working    LLM lenses sample the
      executed cmds   from prompts     detectors          conversational beats
              └──────────────┴───────┬────────┴──────────────────┘
                                     ▼
                     3. CANDIDATES  (scored · corroborated · outcome-rated
                                     · portable-vs-project · team-vs-personal)
                                     │
                       ╔═════════════▼═════════════╗
                       ║  4. DEEP-MINE             ║  one miner reads EVERY beat
                       ║     + adversarial verify  ║  one skeptic tries to refute it
                       ╚═════════════╤═════════════╝
                                     │ dossiers (cached by content fingerprint)
                                     ▼
                     5. CURATE  - the Forge Plan IS the evidence display
                                     │ user approves
                                     ▼
                     6. FORGE   →  ~/.agents/skills/<name>/SKILL.md
                                     │ symlinked into every harness
                                     ▼
                     7. REMEMBER - the forged registry watches the evidence
                                   grow and proposes UPDATES, never duplicates
```

The architecture rule that everything follows: **the deterministic engine finds, samples, counts,
and caches; the agent (and its subagents) read, name, judge, and write.** The engine cannot
hallucinate; the agent never reads a 400,000-line transcript.

## Stage 1 - From transcript to beats

A transcript is a rendered conversation. The parser walks it once and cuts it at every user turn:

```
  TRANSCRIPT BYTES (verbatim)                       WHAT THE PARSER EXTRACTS
  ─────────────────────────────────────────────     ─────────────────────────────────────
  <!-- Claude Code Session ec78…b4d (…) -->     →   agent: claude-code  ·  uuid: ec78…b4d

  _**User (2026-05-01 10:00:00Z)**_             →   ┌─ BEAT k ──────────────────────┐
  can we run a build and all the tests          →   │ intent: "can we run a build…"    │
                                                    │ intent_sig: build:run            │
  _**Agent (claude-opus-4-6 …)**_                   │                                  │
  <tool-use data-tool-type="shell"                  │ method:                          │
      data-tool-name="Bash"><details>           →   │   cmds: go build · go test       │
  <summary>Tool use: **Bash**</summary>             │   tool_mix: shell:2              │
  `go build ./...`                                  │   exit_fails: 0                  │
  ```text                                           │                                  │
  ok                                ← output,       │                                  │
  ```                                 never a cmd   │                                  │
  </details></tool-use>                             │                                  │
                                                    │                                  │
  _**User (2026-05-01 10:05:00Z)**_             →   │ outcome: SUCCESS ✓  ◄────────────┼── labeled by the
  ok lets write a commit                            └──────────────────────────────────┘   NEXT user turn
                                                    ┌─ BEAT k+1 ────────────────────┐
                                                    │ intent: "ok lets write a commit" │ …
```

**The outcome trick** - your own next reply is free supervision:

```
  beat k        beat k+1 opens with…           beat k gets labeled…
  ─────────        ───────────────────────           ───────────────────────
  (any work)   →   "no wait, that's wrong…"      →   ✗ corrected   (failure-mode gold)
  (any work)   →   "perfect, commit this"        →   ✓ success
  (any work)   →   "now let's look at the API"   →   · neutral     (most beats)
```

Sessions are attributed to their **author** (git add-author → home-dir sniff → machine user) and
their **project** (the `git_id` hash of the repo's origin URL - stable across machines).

## Where commands hide (the part that took a corpus audit)

Commands only count when the agent actually executed them - they are extracted exclusively from
shell tool blocks. But every provider and era renders them differently:

```
  MODERN ENVELOPE  <tool-use data-tool-type="shell" data-tool-name="…">

  (a) Codex, single-line - inside the <summary> itself:
      <summary>Tool use: **exec_command** `git status --short`</summary>
                                          └────────┬─────────┘
  (b) Claude, single-line - an inline-backtick body line:
      `go build ./...`
       └─────┬──────┘
  (c) Any, multi-line - a shell-language fence (heredoc bodies skipped):
      ```bash
      go test ./...
      golangci-lint run
      ```
  (d) Codex "shell", key-value render - a bullet, sometimes MULTI-LINE:
      - command: `[bash -lc python - <<'PY'     ← the command is THIS line
      with open('docs/plan.md') as f: …         ← payload, skipped
      PY]`                                      ← close

  LEGACY (~2025)   bare lines, no envelope:
      Tool use: **shell**
      `bash -lc rg -n "NewIntegrator" -g'*.go'`
      Output:                                   ← everything after = output
      ```…```
```

Output fences (` ```text ` / plain ` ``` `) are never read as commands - only scanned for
`exited with code N` and error heads, which feed the beat's failure counter.

## Stage 2 - Four channels turn beats into candidates

```
   beats ──┬─► COMMANDS   per-beat n-grams of command heads
              │              "supabase link ▸ supabase db ▸ supabase migration"
              │
              ├─► INTENTS    verb:keyword from your prompts → "write:commit"
              │
              ├─► META       fixed detectors → "read-only-diagnosis", "reasoning-dial"
              │
              └─► THEMES     semantic clusters (next section) → "restart-carries-a-hypothesis"

   CORROBORATION - the strongest deterministic truth signal - is a JOIN:

        INTENTS                       COMMANDS
     "build:run" ████████╗      ╔████████ "npm run ▸ npm run"
                 ████████╠══╦═══╣████████
                 ████████╝  ║   ╚████████
                            ▼
              beats where BOTH hold:
              "the user asked for X and the agent did Y - repeatedly"
              reported with ✓/✗ outcome rates
```

Scoring blends frequency, persistence-over-time, recency, regularity, **specificity** (your
`supabase` workflow outranks everyone's `git status`), and the **outcome success rate**. Cross-project
recurrence splits candidates into PORTABLE (≥2 projects → personal skill) vs PROJECT-SPECIFIC
(→ committed to that repo); multi-author recurrence marks TEAM practices.

## Stage 2d - The theme sweep (latent expertise)

Most lore is not commands. In some corpora 95%+ of beats are pure conversation - reviews,
decisions, corrections - and form no command cluster at all. The theme sweep mines them:

```
            THE CORPUS, SLICED BY SHAPE (from tool_mix, deterministically)
   ┌─────────────────────────────────────────────────────────────────┐
   │  conversation ████████████████████████████████████  (no tools)  │
   │  read-only    ██████        (diagnosis: reads, no edits)        │
   │  shell        ████████████                                      │
   │  write        ████████                                          │
   └───────────────┬─────────────────────────────────────────────────┘
                   │  beats --shape conversation --min-intent-len 40
                   │  (stratified samples, spread across the timeline)
                   ▼
   ┌─ SIX THEMATIC LENSES (one miner each, in parallel) ─────────────┐
   │ decision-craft        "how do they reason through choices?"     │
   │ review-judgment       "what do they systematically look for?"   │
   │ model-direction       "how do they steer the agent?"            │
   │ verification-discipline  "what proof do they demand?"           │
   │ diagnosis-style       "how do they investigate before acting?"  │
   │ regenerate-vs-patch   "when do they restart vs repair?"         │
   └──────────────────────────────┬──────────────────────────────────┘
                                  │ themes: title · claim · WHY IT'S LATENT
                                  │ · member keys (session#ord) · verbatim quotes
                                  ▼
                      ADVERSARIAL VERIFIER per theme
              "one coherent practice, or pattern-matched wishfulness?
               re-read every cited member; trim or refute (≥4 real members or it dies)"
                                  │
                                  ▼
                      themes table in the corpus
              (members stored by stable key - they survive re-indexing)
```

The bar each theme must clear: it would make the user say **"huh, I _do_ do that."** A theme then
behaves exactly like any cluster - `beats --theme <id>` exports its spans into deep-mine.

## Stage 3 - Deep-mine: from cluster to dossier

Sampling three beats tells you *that* a pattern exists; reading all of them tells you *how it
actually works* - especially the failures:

```
   cluster (e.g. "xcodebuild ▸ xcodebuild", 49 beats)
        │
        │  beats --gram "…"        ┌────────────────────────────────┐
        │  exports EVERY span,        │ fingerprint: 3b2b609cb7b1a8c3  │
        │  ✗ corrected FIRST ────────►│ (from beat identities - NOT │
        ▼                             │  row ids; survives re-index)   │
   ┌─ MINER (subagent) ─────────┐     └───────────────┬────────────────┘
   │ reads all spans, returns:  │                     │
   │  trigger · preconditions   │      cache check: same fingerprint?
   │  canonical steps           │      ┌─ yes → reuse cached dossier (free)
   │  variations · verification │      └─ no  → cluster changed → re-mine
   │  FAILURE MODES + recoveries│
   │  parameters · confidence   │
   └────────────┬───────────────┘
                ▼
   ┌─ ADVERSARIAL VERIFIER ─────┐     "needs-edits: the heredoc commit style is
   │ re-reads the same spans,   │      claude-code-only - don't claim it's
   │ tries to REFUTE the dossier│ ───► universal" ← corrections ride along
   └────────────┬───────────────┘      to forge time
                ▼
        dossier cache (lore.db) - once per cluster, ever
```

Parallelism is per-harness: Claude Code runs the bundled Workflow script; Codex/Amp spawn their own
subagents; sequential is the last resort. Either way the output is identical.

## Stages 4–6 - Curate, forge, install

```
   verified dossiers + verified themes
        │
        ▼  agent writes a manifest (judgments only); `plan render` builds the document
   ┌─ THE FORGE PLAN (engine-rendered; Claude Code: plan mode) ──────┐
   │  # Forge plan - <project> lore                                  │
   │  📜 badge                                                       │
   │  ## Proposed: forge these N skills                              │
   │     ### <name> - full dossier / theme card, VERBATIM            │
   │     (the evidence IS the approval surface)                      │
   │  ## Skipping (with reasons)                                     │
   │  ## On approval: forge · symlink · register · record declines   │
   │  === dossiers above: N ===                                      │
   └──────────────┬──────────────────────────────────────────────────┘
                  │ a PreToolUse hook DENIES any plan that is not
                  │ this artifact (no summarized-away evidence)
                  │ approve            │ reject + feedback
                  ▼                    └─► revised manifest, re-rendered
   write ONCE:   ~/.agents/skills/<name>/SKILL.md
   symlink into: ~/.claude/skills · ~/.codex/skills · ~/.config/opencode/skills
                 (Amp reads ~/.agents/skills natively)
```

A forged skill carries Steps, **Verification**, and **Failure modes** - the last one mined from the
`✗ corrected` beats, which is what separates a skill from a runbook.

## Stage 7 - The registry remembers (and the loop closes)

```
                      ┌──────────────────────────────────────────────┐
                      │            forged registry (lore.db)         │
                      │  name · cluster · evidence-state-at-forge    │
                      │  (fingerprint, sessions, ✓/✗) · content hash │
                      └──────────────────┬───────────────────────────┘
                                         │ forged check (every run)
              ┌──────────────┬───────────┴┬──────────────┬───────────────┐
              ▼              ▼            ▼              ▼               ▼
         up-to-date      update:      update-       suppress:       re-engage:
         (exclude from   new ✗        carefully     user declined,  declined but
         candidates)     beats →   (file was     evidence        evidence grew
                         propose a    hand-edited:  unchanged       materially
                         DIFF         show diff,
                                      never clobber)
```

Your skills compound with your lore: hit two new failure modes this month, and the next run says
*"verify-build: 2 new corrected beats since forging - here's the diff adding them."*

## Why re-running is always safe (the fingerprint ladder)

```
   level            fingerprint                      invalidates when…
   ─────            ───────────                      ─────────────────
   session          size + mtime + PARSER_VERSION    file grows/changes, or the
                                                     parser itself improves (auto
                                                     one-time re-parse, no purges)
   cluster/theme    hash of member beat           any member beat's content
                    identities (session#ord + …)     changes → cached dossier stale
   forged skill     evidence state + content sha     cluster grows / file hand-edited
   session content  provider UUID                    same session indexed twice
                                                     (copied corpora) → prune flags
```

Every write is a per-session transaction; concurrent runs wait (`busy_timeout`) instead of crashing;
`prune` cleans up deleted transcripts; `reset` is the one-command full wipe. One file -
`~/.specstory/lore.db` - regenerable from your transcripts in seconds, except the dossiers and
themes, which is exactly why they're cached.

## Using the engine directly

The whole engine is one zero-dependency script (Node ≥ 22.5) - every stage above is a subcommand:

| Command | What it does |
|---|---|
| `node scripts/mine-skills.mjs index --scan .` | Mine every history in this repo (any depth) into your corpus. |
| `node scripts/mine-skills.mjs report` | Ranked candidates: corroborated pairs, runbooks, intents, meta-skills. |
| `node scripts/mine-skills.mjs beats --gram "go build ▸ go test"` | The exact transcript spans behind one pattern. |
| `node scripts/mine-skills.mjs beats --shape conversation --max 30` | Stratified samples for theme mining. |
| `node scripts/mine-skills.mjs theme list` | The semantic themes mined from your conversational lore. |
| `node scripts/mine-skills.mjs theme expand --key freeze-first` | Corpus-wide candidate members for a theme (lift-scored vocabulary, no transcript reading). |
| `node scripts/mine-skills.mjs theme grow --key freeze-first --keys k1,k2` | Record verified members; the theme's card gains prevalence and outcome lift. |
| `node scripts/mine-skills.mjs beats --theme freeze-first` | The exact spans behind one mined theme (deep-mine input). |
| `node scripts/mine-skills.mjs dossier render` | Cached deep-mine dossiers as pasteable markdown. |
| `node scripts/mine-skills.mjs plan render --file manifest.json` | The full curation plan, engine-assembled from your manifest (and saved for recall). |
| `node scripts/mine-skills.mjs plan last` | Re-render the most recent plan against the current corpus - a canceled forge is never lost. |
| `node scripts/mine-skills.mjs forged check` | Are your forged skills still current, or has the evidence grown? |
| `node scripts/mine-skills.mjs skills` | The installed-skills inventory: lore-forged + everything else, deduped through symlinks. |
| `node scripts/mine-skills.mjs prune` | Drop sessions whose transcripts are gone; flag duplicates. |
| `node scripts/mine-skills.mjs reset` | Wipe the corpus and start fresh. |

Everything accumulates in one file - `~/.specstory/lore.db` - incrementally and idempotently, across
all your projects and agents. Run anything with `--emit json` for structured output, and
`--db <path>` to use a scratch corpus for experiments.
