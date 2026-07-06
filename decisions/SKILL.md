---
name: decisions
description: SpecStory Decisions - a decision report mined from SpecStory coding histories (any agent - Claude Code, Codex, Cursor, Gemini, and more). It finds the decisions made in a window of sessions, the decisions that were later changed (with the supersession chain), the still-open decisions, and the entities they were about - plus insights like churn hotspots and "for now" provisional-decision debt. Use when someone asks "what did we decide", "what decisions changed", "what's still undecided", "why did we choose X", or wants a decision log / ADR-style report over a .specstory/history corpus.
argument-hint: "Enter = guided setup · or plain English, e.g. 'last 30 days, just the open decisions'"
allowed-tools: Bash, Read, Write, AskUserQuestion
license: Apache-2.0
metadata:
  author: SpecStory
  version: "1.0.0"
---

# Decisions

Teams make dozens of small, consequential decisions inside coding sessions - which database,
which naming scheme, which layout - and they evaporate. **Decisions** recovers them from
SpecStory histories into an evidence-cited decision report (a lightweight, auto-mined ADR log):
what was **decided**, what was later **changed** (and what superseded it), what is still
**open**, and **which entities** (components, systems, UI elements) each decision was about.

The work is split in two, and the split is the design:

- A **deterministic engine** (`scripts/decisions.mjs`) does high-RECALL extraction: signal
  grammars find candidate decisions in user turns, entities cluster them into per-entity
  timelines, supersession and insights fall out of timeline order. Every candidate carries its
  **quote** and an **evidence ref** (`path:line`). No LLM, no network in the engine.
- **You do the high-PRECISION pass**: drop false positives, resolve referents, name entities,
  and write the report. Do not read raw transcripts wholesale - work from the engine's JSON and
  open specific evidence refs only when a quote needs context.

## Default flow

1. **Index the corpus** (its own DB - nothing shared with other skills):
   ```bash
   node "${CLAUDE_SKILL_DIR}/scripts/decisions.mjs" index --projects <parent-of-repos> --db <db>
   # or a single tree:  --scan <root>     or a single history dir:  --dir <dir>
   ```

2. **Mine decisions** and capture both views:
   ```bash
   node "${CLAUDE_SKILL_DIR}/scripts/decisions.mjs" decisions --db <db> [--days N]          # digest
   node "${CLAUDE_SKILL_DIR}/scripts/decisions.mjs" decisions --db <db> [--days N] --json   # for your pass
   ```
   The JSON has `decisions[]` (project, status decided|changed|open, provisional flag, entity,
   summary/quote, date, evidence, supersededBy, chain) and `insights` (churn, provisional
   debt, reopened).

3. **Your precision pass** - for each candidate:
   - **Drop non-decisions.** The grammars are deliberately loose; an imperative instruction
     with no choice in it ("look at all the places where...") is not a decision. Keep only
     genuine choices between alternatives, policies, namings, or commitments.
   - **Resolve referents.** Quotes like "I like option 1" or "go with your recommendation" are
     real decisions with invisible content. `Read` the evidence file around the ref line to
     recover WHAT was chosen, and restate it ("chose option 1: server-side rendering for the
     landing page").
   - **Name entities.** Many candidates are `(unnamed)` - infer the entity from the quote and
     context (a component, route, system, convention). Merge near-duplicate decisions.
   - **Keep the evidence.** Every decision in your report cites its `path:line`. Never invent
     a decision that has no engine candidate behind it.

4. **Write the report** and save it to `.specstory/decisions/<YYYY-MM-DD>-decisions.md`:
   - **Summary** - counts, projects, window.
   - **Insights** - churn hotspots (entities re-decided 2+ times, with the chain), provisional
     "for now" debt (never revisited - each is latent rework), re-litigated decisions (settled,
     then re-questioned), and one you judge yourself: **decisions lacking any stated rationale**
     (flag them; they are the ones nobody will be able to reconstruct later).
   - **Per project:** **Decided** (entity, decision, date, rationale if stated, evidence) ·
     **Changed** (old -> new, when, and why if stated) · **Open** (the question, how long it has
     been open, and your suggested next step for each).
   Also offer `--out <file>` to keep the raw digest beside your written report.

## Guided start

Invoked bare, ask three short questions (`AskUserQuestion` or plain chat), then run:

- **Scope** - which repos / parent directory of `.specstory/history` corpora?
- **Window** - how many days back? Same choices as the workthreads rollup: last **7**
  days (default) / **30** / **90** (`--days N` for anything else, `--days 0` for all
  time). Indexing is always unbounded, so supersession chains keep their full history
  regardless of the report window.
- **Goal** - the full **report**, just **open decisions**, just **changed decisions**, or the
  **insights** (churn + debt)?

## Conventions

Node ESM only, zero dependencies, Node >= 22.5. No em dashes anywhere (use " - "). The engine
is deterministic (byte-identical across runs on the same corpus); all judgment - filtering,
naming, rationale, next steps - is yours.
