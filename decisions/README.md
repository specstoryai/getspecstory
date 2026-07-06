# Decisions

_A decision report mined from your SpecStory coding histories._

Teams make dozens of consequential decisions inside coding sessions - which database,
which naming convention, which layout - and they evaporate into scrollback. Decisions
reads the `.specstory/history` transcripts your coding agents already write and
recovers them into an **evidence-cited decision log** (think auto-mined ADRs):

- **Decided** - firm choices still standing
- **Changed** - decisions later superseded, with the full chain
  (e.g. Postgres -> SQLite -> DuckDB) and what replaced them
- **Open** - questions raised but never resolved
- **Entities** - the thing each decision was about (a component, system, UI element,
  file, or convention), used to build per-entity decision timelines

## Insights

Beyond the log itself, the report surfaces:

- **Churn hotspots** - entities whose decisions were reversed 2+ times (design thrash,
  unstable requirements)
- **Provisional-decision debt** - "for now" / temporary decisions that were never
  revisited (each one is latent rework)
- **Re-litigated decisions** - settled entities where a later open question re-opened
  the debate
- **Decisions lacking rationale** - flagged in the written report (the ones nobody will
  be able to reconstruct later)

## How it works

A **deterministic engine** (plain Node, zero dependencies, no LLM or network) does the
high-recall pass: signal grammars find candidate decisions in user turns ("let's go
with...", "actually, switch to...", "should we...?", "for now..."), entities are
extracted from the matching sentence, candidates cluster into per-entity timelines per
project, and supersession/status falls out of timeline order. Duplicated prompt text is
deduped and ubiquitous entities (e.g. a product name that appears everywhere) never
chain unrelated decisions. Output is deterministic - byte-identical across runs.

The **calling agent** then does the high-precision pass from the engine's JSON: drops
false positives, resolves referents ("I like option 1" -> what option 1 actually was,
by reading the cited evidence), names entities, and writes the final report.

## Install

From a clone of this repo:

```zsh
cd decisions
./install.sh
```

That bundles the engine and the skill into `~/.agents/skills/decisions` and symlinks it
into `~/.claude/skills/decisions`, so `/decisions` is available from **any** Claude Code
session in **any** project. It is self-contained - it does not read from any other
skill's directory. Re-run `./install.sh` any time to update.

Requirements: Node >= 22.5 (for `node:sqlite`), and the SpecStory CLI capturing
histories into `.specstory/history/`.

## Use

Start a new Claude Code session (skills load at session start), then:

```
/decisions
```

or just ask: _"what did we decide this month?"_, _"what's still undecided?"_,
_"what decisions changed?"_. With no arguments it asks three short questions - **Scope**
(which repos), **Window** (last 7 days default / 30 / 90, same as workthreads; 0 = all
time), **Goal** (full report / open
decisions / changed decisions / insights) - then runs and saves the report to
`.specstory/decisions/<YYYY-MM-DD>-decisions.md`.

Sample digest shape:

```
decisions report - 124 decision(s) across 5 project(s) (window: all time)

Insights
  Churn hotspots (decisions reversed 2+ times)
    - EventStore (api): changed 2 time(s) - Postgres -> SQLite -> DuckDB
  Provisional decisions never revisited ("for now" debt)
    - nav (website, 2026-03-11): "It's ok for now if /specstory isn't linked to from the nav"

## website
  Decided
    - root logo: "let's make the logo in the top left be the SpecStory logo"  [decided] · 2026-03-11
        .specstory/history/2026-03-11_19-52-23Z.md:80802
  Changed
    (none)
  Open
    - ads config: "should I leave these or do different config in google ads console?"  [open] · 2026-04-09
```

Each decision carries its evidence ref (`path:line`) so every claim is checkable.

## Develop

The engine is plain Node (ESM, zero dependencies, `node:sqlite`). Run the tests:

```zsh
cd decisions
npm test
```

The engine's own corpus lives at `~/.specstory/decisions.db` (never shared with other
skills); use `--db /tmp/scratch.db` while developing.

## License

Apache-2.0.
