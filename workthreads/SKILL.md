---
name: workthreads
description: SpecStory Workthreads - a weekly work-thread rollup across a team's repos from SpecStory coding histories (any agent - Claude Code, Codex, Cursor, Gemini, and more). It groups the window's sessions into threads of work per project and labels each new / open / recently closed, so a lead sees what shipped, what is still an open loop, and what was just started. Use when someone asks "what happened this week", "what is still open", "what did the team finish", "give me the weekly rollup", or wants a status report over a .specstory/history corpus.
argument-hint: "Enter = guided setup · or plain English, e.g. 'last 7 days, just the open loops'"
allowed-tools: Bash, Read, Write, AskUserQuestion
license: Apache-2.0
metadata:
  author: Greg Ceccarelli
  version: "1.0.0"
---

# Workthreads

A lead needs a weekly answer across the team's repos: what work happened this week, what got
finished, and what is still open and needs a next step. **Workthreads** produces that **rollup**
from SpecStory histories - the `.specstory/history` transcripts your coding agents already write.
It reports **lines of work and their lifecycle** (new / open / recently closed).

A deterministic engine (`scripts/workthreads.mjs threads`) does the retrieval, clustering, and
classification; **you do the synthesis** - you turn its evidence into the lead's weekly report.
Do not try to read raw transcripts yourself; they can be hundreds of thousands of lines. Run the
engine and write the rollup from its output.

This skill is **harness-portable** (agentskills.io format). Where it names a specific tool
(e.g. `AskUserQuestion`), treat that as "use your harness's equivalent; fall back to plain chat."

## How the engine splits the work

- The engine groups the window's beats **by project** and clusters them into **threads** (a line
  of work that can span several sessions). It assigns each thread one lifecycle **status** relative
  to today:
  - **new** - first activity within the last 7 days.
  - **open** - unresolved, still active (the open loops).
  - **closed** - latest outcome was success and the thread has gone quiet; flagged **reverted**
    when a beat ran a rollback command (`git revert` / `git reset --hard` / `git checkout -- ...`).
- Output is deterministic (stable sort, no wall-clock timestamps in the body), so two runs on the
  same corpus are byte-identical.

## Default flow: the weekly rollup

1. **Index the corpus** into workthreads' own DB. Point at the team's repos and build/update it:
   ```bash
   node "${CLAUDE_SKILL_DIR}/scripts/workthreads.mjs" index --projects <parent-of-repos> --db <db>
   # or a single tree:  --scan <root>     or a single history dir:  --dir <dir>
   ```

2. **Run `threads` cross-project for the last 7 days** and capture the evidence:
   ```bash
   node "${CLAUDE_SKILL_DIR}/scripts/workthreads.mjs" threads --db <db> --days 7            # human digest
   node "${CLAUDE_SKILL_DIR}/scripts/workthreads.mjs" threads --db <db> --days 7 --json     # machine-readable
   ```
   The digest prints, per project, three sections in order - **New**, **Open**, **Recently
   closed** - each thread with its evidence refs (`path:line`), last-activity date, status, and a
   `reverted` marker. `--json` emits an array of threads (`project`, `status`, `reverted`, the files
   touched, last-activity date).

3. **Write the rollup** from that evidence, in the lead's shape:
   - (a) a high-level result: session count and active projects in the window;
   - (b) per-project **highlights** of completed work (the `closed` threads);
   - (c) **open loops** - the `open` threads, unresolved or needing verification, with a suggested
     next step each;
   - (d) notable **rollbacks / abandoned efforts** (the `reverted` threads);
   - (e) cite evidence refs (`path:line`) so each claim is checkable.
   Add a caveat that **the week may still be in progress**, so `open` and `new` threads are
   snapshots, not final outcomes.

4. **Save it to a dated file** so the rollup is durable and diffable week over week:
   ```
   .specstory/workthreads/<YYYY>-W<week>.md
   ```
   (ISO week number, e.g. `.specstory/workthreads/2026-W25.md`). Also offer `threads --out <file>`
   to drop the raw digest beside your written summary.

## Guided start

If the user just invokes the skill with no specifics, ask three short questions (use
`AskUserQuestion` or plain chat), then run the default flow with the answers:

- **Scope** - which repos / parent directory holds the team's `.specstory/history` corpus?
- **Window** - how many days back? (default **7** for the weekly rollup; `--days N` to widen.)
- **Goal** - the whole **rollup**, just the **open loops**, just **recently closed**, or a quick
  **status** line? Tailor which sections you emphasize to the answer.

## Conventions

Node ESM only, zero dependencies, Node >= 22.5. No em dashes anywhere (use " - "). The engine path
never calls an LLM or the network; all judgment (the written narrative, suggested next steps,
emphasis) is yours.
