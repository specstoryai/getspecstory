---
name: decisions2
description: SpecStory Decisions2 - a multi-pass decision report mined from SpecStory coding histories. Treats a decision as a PROCESS (proposal, deliberation, choice, reversal), not a final choice. A deterministic engine extracts commit-message seeds, builds a per-project fingerprint, expands to candidates with roles, and clusters them into per-entity process arcs. You do the precision pass: drop false positives, resolve referents, name entities, split over-linked arcs, and write the report. Use when someone asks "what did we decide", "what decisions changed", "what's still being considered", or wants a decision log / ADR-style report over a .specstory/history corpus.
argument-hint: "Enter = guided setup · or plain English, e.g. 'last 30 days, just the open deliberations'"
allowed-tools: Bash, Read, Write, AskUserQuestion
license: Apache-2.0
metadata:
  author: SpecStory
  version: "0.1.0"
  status: experimental
---

# Decisions2

Teams make dozens of consequential decisions inside coding sessions — which database, which naming
scheme, which layout — and they evaporate into scrollback. **Decisions2** recovers them from
SpecStory histories into an evidence-cited decision report that treats a decision as a **PROCESS**:
the whole arc of proposal, deliberation, choice, and reversal, not just the final choice.

The work is split in two, and the split is the design:

- A **deterministic engine** (`scripts/decisions2.mjs`) does high-RECALL extraction in four passes:
  1. **Seed** — commit messages with decision-shaped language (the strongest signal; ~85-90% precision).
  2. **Fingerprint** — per-project entity sets + verb templates mined from seeds.
  3. **Expand** — fingerprint + lexical grammar over ALL chat turns, assigning each candidate a ROLE
     (proposed / deliberated / reversed / provisional / open).
  4. **Arcs** — cluster candidates + seeds into per-entity (or per-file) process arcs, each with a
     STATE (in-formation / decided / changed / abandoned).
  Every candidate carries its **quote** and an **evidence ref** (`path:line`). No LLM, no network.

- **You do the high-PRECISION pass**: drop false positives, resolve referents, name entities, split
  over-linked arcs, and write the report. Do not read raw transcripts wholesale — work from the
  engine's arc digest and open specific evidence refs only when a beat needs context.

## Why "decision as process"

A decision is not just the final choice. The deliberation ("should we use tabs or a sidebar?"), the
rejected alternatives ("Postgres is overkill here"), the provisional deferrals ("for now, keep it
as an env var"), and the reversals ("actually, switch to SQLite") are all part of the decision. They
are the **why** — the content that evaporates and that an ADR is supposed to capture. The engine
surfaces the full arc; you write it up.

## Default flow

1. **Index the corpus** (the engine's own DB — shares nothing with other skills):
   ```bash
   node "${CLAUDE_SKILL_DIR}/scripts/decisions2.mjs" index --projects <parent-of-repos> --db <db>
   # or a single tree:  --scan <root>     or a single history dir:  --dir <dir>
   ```

2. **Run the full pipeline** and capture the digest:
   ```bash
   node "${CLAUDE_SKILL_DIR}/scripts/decisions2.mjs" decisions --db <db> [--days N]
   # add --out <file> to save the raw digest beside your report
   ```
   The digest is per-project, per-state (In formation / Decided / Changed / Abandoned), with each
   arc's beats in timeline order and evidence refs.

3. **Your precision pass** — for each arc:
   - **Drop non-decisions.** The engine is deliberately high-recall. An imperative instruction with
     no choice in it ("let's keep moving") is not a decision. Keep only genuine choices, deliberations,
     and reversals.
   - **Resolve referents.** Quotes like "let's go with option 1" are real decisions with invisible
     content. `Read` the evidence file around the ref line to recover WHAT was chosen, and restate it.
   - **Name entities.** Many arcs are labeled `(unnamed)` or by a file basename. Infer the real entity
     (a component, route, system, convention) from the quotes and context.
   - **Split over-linked arcs.** File-based clustering can merge related-but-separate decisions that
     share files. If an arc's beats are about genuinely different topics, split it into separate arcs.
   - **Classify question-form beats.** A `deliberated` beat may be a genuine open question (state 1),
     a decision in formation (state 2), or a softened choice (state 3 — "do we really need X?" often
     means "let's remove X"). Read the surrounding context to tell which.
   - **Keep the evidence.** Every beat in your report cites its `path:line`. Never invent a decision
     that has no engine candidate behind it.

4. **Write the report** and save it to `.specstory/decisions/<YYYY-MM-DD>-decisions.md`:
   - **Summary** — counts, projects, window.
   - **Per project:** arcs grouped by state:
     - **Decided** — the final choice, with the rationale if stated, and the evidence.
     - **Changed** — the supersession chain (old → new), when, and why if stated.
     - **In formation** — open deliberations: the question, the options on the table, how long it has
       been open, and your suggested next step.
     - **Abandoned** — provisional "for now" decisions never revisited (decision debt).
   - **Insights** (if the engine surfaces them): churn hotspots (arcs reversed 2+ times), provisional
     debt, re-litigated decisions.

## Guided start

Invoked bare, ask three short questions (`AskUserQuestion` or plain chat), then run:

- **Scope** — which repos / parent directory of `.specstory/history` corpora?
- **Window** — how many days back? Last **7** days (default) / **30** / **90** (`--days N` for
  anything else, `--days 0` for all time). Indexing is always unbounded, so supersession chains keep
  their full history regardless of the report window.
- **Goal** — the full **report**, just **in-formation** (open deliberations), just **changed**
  decisions, or just **decided**?

## Conventions

Node ESM only, zero dependencies, Node >= 22.5. No em dashes anywhere (use " - "). The engine is
deterministic (byte-identical across runs on the same corpus); all judgment — filtering, naming,
splitting, rationale, next steps — is yours.

## Status

**Experimental.** This is an exploration of a multi-pass approach (commit-seeded fingerprint +
lexical grammar + file-based arc linking) to the recall ceiling of the original `decisions` skill.
The engine's recall and the arc linking have been measured against real corpora; the precision pass
and the report format are still being refined.
