# Workthreads - As Built (2026-06-24)

What the `george-report` branch delivers, how it works, and the decisions behind it.
This is the single source of record for the work; it replaces the per-run deadreckon
audit docs.

## What shipped

A new, **standalone** Claude/agent skill: **`/workthreads`** - a weekly work-thread
rollup over SpecStory coding histories. It lives in a self-contained top-level
[`workthreads/`](../workthreads) directory and can be installed on its own.

Relative to `dev`:

- **`workthreads/`** - new: the entire skill (engine, CLI, SKILL.md, README, tests,
  fixtures, installer).
- **`lore/`** - unchanged (identical to `dev`).
- **`.gitignore`** - added entries for local deadreckon / specstory artifacts.
- **`docs/`** - this one file.

## What the skill does

Given the `.specstory/history` transcripts coding agents already write, it groups a
time window's sessions into **threads of work, per project**, and labels each:

- **new** - first activity within the last 7 days
- **open** - unresolved, recently active (the "open loops")
- **closed** - latest outcome was success and it has gone quiet; flagged **reverted**
  when a beat ran a rollback command (`git revert` / `git reset --hard` /
  `git checkout -- ...`)

It renders a digest (per project: New / Open / Recently closed, with evidence refs,
last-activity date, and a one-line rationale), supports `--json` and `--out`, and is
deterministic (stable sort, no wall-clock in the body). The **engine produces the
structure; the agent writes the narrative rollup** from that evidence - no LLM or
network call in the engine path.

## Architecture

```
workthreads/
├── SKILL.md                 the agent contract (guided start, default weekly-rollup flow)
├── README.md                what it is, install, use
├── install.sh               self-contained installer (bundles the engine)
├── package.json             node >=22.5, `npm test`
├── scripts/
│   ├── workthreads.mjs       CLI: `index` and `threads`
│   └── lib/                  patterns, parse, discover, db, indexer, threads (zero deps)
├── tests/threads.test.mjs    11 tests over committed fixtures + synthetic stress corpora
└── fixtures/                 threads-foo / threads-bar (known-lifecycle transcripts)
```

`workthreads.mjs` has two subcommands: `index` (builds a SQLite corpus from
`.specstory/history`, default `~/.specstory/workthreads.db`) and `threads` (clusters +
classifies + renders). `--projects`/`--scan`/`--dir` let `threads` index-then-render
in one shot.

### Clustering (the load-bearing part)

The engine clusters beats into threads with three rules, in order:

1. **A session is one line of work.** All of a session's beats are unioned - a long
   multi-prompt session becomes ONE thread, not one-thread-per-prompt.
2. **Cross-session merge needs >= 2 shared RARE keys.** A key is a file or a
   distinctive symbol (snake_case / camelCase / ALL_CAPS_WITH_UNDERSCORE). Ubiquitous
   keys - any in more than 4 sessions, plus config files (`.env`, `package.json`, tool
   dirs) and short abbreviations (`AI`, `API`, `EOF`) - are ignored. Two sessions merge
   only when they share at least two such keys, so a lone shared utility file or a
   plain word never bridges unrelated work.
3. **Bounded union.** A thread is capped at 5 sessions; a merge that would exceed the
   cap is refused.

Lifecycle status is assigned per thread relative to today, precedence: **closed**
(latest outcome success, quiet >= 3 days, last activity <= 30 days) > **new** (first
activity <= 7 days) > **open** (activity <= 14 days). Threads grouped by project.

### Why it is this way

Two failure modes were found and fixed against the real corpus:

- **Under-merge (fragmentation):** naive per-beat clustering produced **372**
  single-prompt "threads" - a single coherent session shattered. Fixed by rule 1.
- **Over-merge (mega-threads):** single-linkage on shared files/symbols chains a whole
  codebase into one giant thread (observed: 4368 beats collapsed into 2). No per-edge
  threshold prevents this - a shared codebase is inherently densely connected. Fixed by
  rules 2 and 3 together (rare-key requirement + hard session cap).

Net on the real corpus: **372 fragments -> ~30 coherent threads**, max 5 sessions per
thread, no cross-project bleed.

## Install and use

```zsh
cd workthreads && ./install.sh
```

Bundles the engine + SKILL.md into `~/.agents/skills/workthreads` and symlinks it into
`~/.claude/skills/workthreads`, so `/workthreads` works from any Claude Code session in
any project. Re-run to update. Then, in a new session: `/workthreads` (or "give me the
weekly rollup"). The rollup is also saved to `.specstory/workthreads/<YYYY>-W<week>.md`.

## Design decisions

- **Standalone, not coupled.** Workthreads has no runtime dependency on any other
  skill. It carries its own copy of the shared engine modules - **independence over
  DRY**, a deliberate choice. (An earlier iteration reused lore's engine; that coupling
  was removed and `lore/` reverted to `dev`.)
- **Engine deterministic, agent judges.** Clustering/classification/rendering are pure
  and byte-reproducible; the weekly narrative is the agent's job.
- **Conventions:** Node ESM, zero npm dependencies, Node >= 22.5, no em dashes.

## Verification

- `workthreads` tests: **11/11** (`cd workthreads && npm test`).
- `lore` tests: **32/32** (unaffected by the split).
- Output deterministic (two runs byte-identical).
- Exercised against the real `~/.specstory` corpus and from an unrelated project
  directory via the installed skill.
