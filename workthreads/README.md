# Workthreads

_A weekly work-thread rollup from your SpecStory coding histories._

Workthreads reads the SpecStory transcripts your coding agents already write and
answers, at a glance: **what happened this week, what got finished, and what is
still open and needs a next step.** It groups the window's sessions into **threads
of work per project** and labels each one:

- **new** - first activity within the last 7 days
- **open** - unresolved, still active (the "open loops")
- **closed** - latest outcome was success and it has gone quiet (flagged
  **reverted** when a beat ran a rollback command)

A lead can use it for a weekly standup; an individual can use it to re-orient after
time away ("what was I in the middle of?").

## Relationship to Lore

Workthreads is the **sibling lens to [`/lore`](../lore)** over the **same corpus**.
Where Lore mines your history for reproducible *procedures* and forges them into
skills, Workthreads reports your *lines of work and their lifecycle*. It **reuses
Lore's deterministic engine** (`lore/scripts/mine-skills.mjs threads`) rather than
duplicating it - the engine does the retrieval, clustering, and classification; the
calling agent writes the human rollup from that evidence.

## How it clusters (deterministic, no LLM in the engine)

The engine is robust against both failure modes of naive clustering:

- **A session is one line of work** - a long multi-prompt session collapses into one
  thread, not one-thread-per-prompt.
- **Cross-session merge needs >= 2 shared *rare* keys** (a distinct file or symbol;
  ubiquitous files like `package.json` and short abbreviations are ignored) - so a
  single shared utility file or a plain word never bridges unrelated work.
- **Bounded threads** - a hard session cap stops a whole codebase from chaining into
  one mega-thread.

Output is deterministic (stable sort, no wall-clock in the body), so two runs on the
same corpus are byte-identical.

## Install

From a clone of this repo:

```zsh
cd workthreads
./install.sh
```

This bundles Lore's engine and the skill into `~/.agents/skills/workthreads` and
symlinks it into `~/.claude/skills/workthreads`, so `/workthreads` is available from
**any** Claude Code session in **any** project. Re-run it any time to update.

Requirements: Node >= 22.5 (for `node:sqlite`), and the SpecStory CLI capturing
histories into `.specstory/history/`.

## Use

Start a new Claude Code session (skills load at session start), then:

```
/workthreads
```

or just ask in plain English: _"give me the weekly rollup"_, _"what's still open?"_,
_"what did we finish this week?"_. With no arguments it asks three short questions -
**Scope** (which repos), **Window** (how many days, default 7), **Goal** (full rollup
/ just open loops / recently closed / status) - then runs.

The digest groups threads by project under **New / Open / Recently closed**, each
with evidence refs (`path:line`), last-activity date, and a one-line rationale. It is
also saved to a dated file (`.specstory/workthreads/<YYYY>-W<week>.md`) so rollups are
durable and diffable week over week.

Sample shape:

```
workthreads digest - 31 thread(s) across 3 active project(s) (window: last 7 days)

## marketing
  New
    - run the first-cut skill on the maker video  [new]  · last 2026-06-19 · 1 session, 20 beats
  Open
    - resend contact-topic sync still failing  [open]  · last 2026-06-20 · 2 sessions, 7 beats
  Recently closed
    - extract survey emails for non-solo respondents  [closed]  · last 2026-06-18 · 1 session, 6 beats
```

## License

Apache-2.0.
