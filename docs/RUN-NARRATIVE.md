# Plan Result Wrapper

**Run ID:** `90a54694e80c4e7a8d8571e81af5110b`
**Plan ID:** `4808dceb3f844dcf8d3edba788a82960`
**Merged result run:** `29334e4460584e6ea5f212b78198ac55`
**Goal:** # Goal: `/workthreads` - weekly work-thread rollup (mirror lore)

## Purpose

A lead (George) needs a weekly answer across his team's repos: what work happened
this week, what got finished, and what is still open and needs a next step.
`/workthreads` produces that rollup from SpecStory histories - it groups the
window's sessions into **threads of work per project** and labels each
**new / open / recently closed**, so the lead sees what shipped, what is unresolved
("open loops"), and what was just started. Sibling lens to `/lore` over the **same
corpus**: lore mines procedures into skills; workthreads reports lines of work and
their lifecycle. The engine produces deterministic structure; the agent writes the
rollup from that evidence (same split as lore).

## Target shape (George's weekly report)

Reproduce his report's shape: (a) high-level result - session count and active
projects; (b) per-project **highlights** of completed work; (c) **open loops** -
unresolved or needs-verification threads; (d) notable rollbacks/abandoned efforts;
(e) a written, dated rollup file.

## Engine (deterministic; mirror lore)

Add `lore/scripts/lib/threads.mjs` and a `threads` subcommand in
`lore/scripts/mine-skills.mjs`. It reads the corpus that `index` builds (`--db`,
`node:sqlite`); `index` already discovers projects via `--projects`/`--scan`.
`threads --db --days N` (default 7) groups the indexed sessions **by project** and
clusters beats into threads by shared touched-files (plus intent/command grams); merge a line of work across sessions into ONE thread. Assign one
lifecycle status per thread, relative to the current date, in this precedence:
1. **closed** - latest outcome is success, quiet >= 3 days, last activity within 30
   days. Also closed and flagged `reverted` if a beat ran a revert command (`git
   revert` / `git reset --hard` / `git checkout -- <path>`).
2. **new** - first activity within the last 7 days.
3. **open** - otherwise, activity within the last 14 days (these are the open loops).
No schema change, no `PARSER_VERSION` bump, no LLM or network in the engine path.

## Output

- A **digest grouped by project**: a top line (session count + active projects in the
  window), then per project the three sections in order - `New`, `Open`,
  `Recently closed` - each thread showing evidence refs (`path:line`), last-activity
  date, status, and a `reverted` marker. Print all three headers per
  project even when empty.
- `--json`: only JSON to stdout - an array of threads, each with `project`, `status`
  (`"new"|"open"|"closed"`), `reverted` (bool), the files touched, and last-activity date.
- `--out <file>`: also write the digest to a file.
- **Deterministic**: byte-identical across two runs on the same corpus (stable sort,
  no wall-clock timestamps in the body).

## Fixtures + tests

Add fixtures under `lore/fixtures` (real Claude Code transcript bytes, like
`lore/fixtures/projA`) across at least two projects, encoding threads of known
lifecycle, and add `lore/tests/threads.test.mjs` (`node --test`, like
`lore/tests/engine.test.mjs`) asserting: multi-session merge into ONE thread;
per-project grouping; and closed / open / new classification. The full
`node --test tests/*.test.mjs` (existing + new) must pass.

## Skill surface

Add `lore/workthreads/SKILL.md` (agentskills.io format, mirroring `lore/SKILL.md`)
named `workthreads`, with a `description`, `allowed-tools`, and a body whose default
flow is the weekly rollup: run `threads` cross-project for the last 7 days, turn the
evidence into a George-style summary (the Target shape above, plus a caveat that the
week may be in progress), and write it to a dated file (e.g.
`.specstory/workthreads/<YYYY>-W<week>.md`). Guided start: Scope, Window, and Goal (rollup / open loops / recently closed / status).

## Conventions (mirror lore)

Node ESM `.mjs`; **zero npm dependencies**; Node >= 22.5; **no em dashes anywhere**;
conventional commits. **Do NOT modify** `lore/SKILL.md`,
`lore/fixtures/golden/forge-plan.md`, or `PARSER_VERSION`. The existing test suite
must stay green.

This run materializes a completed plan result. It has no provider turns of its own; read the consolidated plan documentation instead.

- [Plan narrative](./PLAN-NARRATIVE.md)
- [Plan as built](./PLAN-AS-BUILT.md)
- [Plan decisions](./PLAN-DECISIONS.md)
- [Plan children](./PLAN-CHILDREN.md)
