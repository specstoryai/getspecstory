# decisions-skill — work log

**Purpose.** A running log of work on the `decisions-skill` branch (the SpecStory
Decisions report skill). Raw fodder for a blog post and presentation on **the
definition of done as the whole game**, up-front vs. incremental work, and TDD vs.
goal-driven development. Written as I go, not in retrospect — keep it honest, keep
it specific (real files, real SHAs, real test counts, real moments of doubt).

**Status of this file.** Untracked, not part of the shipped skill. The skill at
`decisions/` is self-contained and installable; this log is a personal artifact
sitting at the repo root. Commit it to the branch, gitignore it, or move it — TBD.

---

## The lens (what to watch for)

The essay's central claim: **the definition of done is the whole game.** The five
modes (auto-completion, vibe coding, agent-assisted exploration, spec-driven,
goal-driven) are a spectrum of interactivity, and the selector is *how
deterministic your DoD can be*. The two bookends are the spec (what you want) and
the DoD (how you'll know you got it); everyone over-invests in the spec and
under-invests in the DoD, which is backwards. A qualitative DoD means the human
must stay in the room; a deterministic DoD is what buys you the right to leave.

**The live question, and why this is the interesting project to log.**
Right now I am *in exploration mode on the design itself* — which is the essay's
undervalued mode, the one where you don't yet have a spec because you don't yet
know what you want. Specifically: the skill is currently built as two passes —

- **The engine** (`decisions/scripts/lib/*.mjs`) — deterministic, zero-dep,
  byte-identical across runs, `npm test` exits 0 (9 tests). High-recall extraction
  via **signal grammars** (regexes over specific strings of words: "let's go
  with…", "actually, switch to…", "should we…?", "for now…").
- **The agent precision pass** (described in `SKILL.md`) — drop false positives,
  resolve referents, name entities, write the report.

…*and I have an intuition that this split may be wrong.* The engine's recall is
bounded by the grammars — it only catches decisions that happen to be phrased
with specific strings of words. Decisions get made in transcripts all the time
in phrasings the grammars don't match, and those are invisible to the engine.
If the recall ceiling is low, the "deterministic high-recall + qualitative
high-precision" split is built on a false premise: the engine isn't actually
high-recall, it's high-recall-*over-a-narrow-surface*, and the agent pass is
silently doing both recall *and* precision on everything the grammars miss —
which means the split buys me less than I think.

**This is the real fodder for the blog, and it's the essay's thesis live in a
real decision:** *I cannot yet write a deterministic definition of done for the
decisions skill, because I don't yet know how much the deterministic layer is
missing.* The DoD question ("what does done look like for this skill?") is
blocked on an exploration question ("what's the engine's real recall ceiling,
and is the two-pass design even the right shape?"). That's the essay's point:
exploration comes first, you explore to earn the right to write a spec, and you
can't jump to a goal-driven loop on a thing whose DoD you can't yet define.
Pretending I could would be exactly the failure mode the essay names — a
qualitative DoD dressed up as a deterministic one.

So the work log should track: what do I do when my deterministic layer might be
missing the point? How do I measure recall on a thing with no ground truth? When
do I decide the split is right vs. wrong, and what evidence would change my mind?

Three threads to track explicitly:

1. **DoD determinism — and its ceiling.** The engine has a deterministic DoD
   (`npm test`, 9 tests, byte-identical output). The *skill* does not — its DoD
   is qualitative ("does the report capture the real decisions?"). The live
   question is whether the engine's deterministic DoD is even measuring the right
   thing: if the grammars miss most real decisions, the tests pass on a surface
   that isn't the actual problem. Track every moment where I confuse "the engine
   passes its tests" with "the engine is high-recall."
2. **Up-front vs. incremental — and the cost of the up-front design I already
   did.** The skill shipped with a lot of up-front design baked in (the two-pass
   split, the signal grammars, the entity-ubiquity cap, the fixed-date fixtures
   encoding a full lifecycle). If the split turns out to be wrong, that up-front
   design was premature commitment — waterfall-on-a-medium-task. Track whether the
   existing design is an asset or a sunk cost pulling me toward keeping a wrong
   shape.
3. **TDD vs. goal-driven — and where each is a category error here.** The engine
   was built TDD-style and that's appropriate. But I cannot put a goal-driven
   loop over the *skill's* real DoD (report quality) because it's qualitative —
   and per the essay, a cross-model judge on a qualitative DoD is an LLM opinion
   wearing a robe, not governance. Track every temptation to do exactly that.

---

## What decisions-skill actually is (state as of today)

A standalone skill at `decisions/` that mines `.specstory/history` transcripts into
an evidence-cited decision log (auto-mined ADRs). Self-contained: own engine, own
corpus DB (`~/.specstory/decisions.db`), own `install.sh`, zero npm deps, Node
ESM, Node >= 22.5.

Structure:
```
decisions/
  README.md, SKILL.md, package.json, install.sh
  scripts/
    decisions.mjs                          # CLI: index | decisions [--json|--out]
    lib/{db,decide,discover,indexer,parse,patterns}.mjs   # ~1010 lines, zero deps
  tests/decisions.test.mjs                 # 9 tests, node:test
  fixtures/
    decisions-alpha/.specstory/history/{eventstore-postgres,sqlite,duckdb,authclient-fornow}.md
    decisions-beta/.specstory/history/{settingspanel-question,flagparser-kebab}.md
```

Report outputs: **Decided** / **Changed** (with supersession chains) / **Open** /
**Entities** (per-entity timelines). Insights: churn hotspots (2+ reversals),
provisional "for now" debt, re-litigated decisions, decisions lacking rationale.

Git: `decisions-skill` branch, rebased onto `dev` today, 2 commits on top:
`fe4bc7e feat(decisions): add standalone decision-report skill` and
`5148761 docs(decisions): align guided-start window choices with workthreads`.
Force-pushed to `origin/decisions-skill`.

---

## 2026-07-13 — session: get clean, pick a workstream

**Starting state.** Two local branches carrying decisions work: `decisions-report`
and `decisions-skill`, plus `origin/decisions-skill`. I couldn't remember which was
the real workstream. Wanted to get clean before continuing.

**What I did.** This was a small exploration→decision→verify loop, and it's worth
noting because the verify step was *deterministic* — which is exactly the essay's
point about when you're allowed to feel done.

1. **Audit state before changing anything.** `git branch -a`, `git log`,
   `git diff --stat`. Found: `decisions-report` tip = `8930eeb` (an old commit
   that's an ancestor of `dev`); `decisions-skill` = `8930eeb` + 2 feature commits.
   The `git diff --stat dev..decisions-report` output looked like the branch had
   *deleted* a bunch of `specstory-cli` files (telemetry, session_tui, sessionindex).
   That was a mirage — those were files **dev added since the fork point**, not
   things the branch removed. Good reminder: diff-stat against a moving baseline
   is misleading; check the merge-base.
2. **Map the decision space.** `git log A..B` both directions.
   `decisions-skill..decisions-report` = empty. `decisions-report..decisions-skill`
   = the 2 feature commits. So `decisions-skill` strictly contains `decisions-report`.
   The decision was trivial once the state was audited: continue on
   `decisions-skill`, delete `decisions-report`.
3. **Execute + verify.** `git checkout decisions-skill && git rebase dev` — clean,
   no conflicts (the decisions/ dir and dev's touched files are disjoint).
   `cd decisions && npm test` → **9/9 pass** on Node v24.18.
   `git branch -d decisions-report` (safe: confirmed ancestor first via
   `git merge-base --is-ancestor`). `git push --force-with-lease origin decisions-skill`.

**DoD note.** "Get clean" had a deterministic DoD I could have written down in
advance: *tests pass on the rebased branch, stale branch deleted, remote matches
local.* I didn't write it down — I just did it — but it was checkable, and I
checked it. The essay's point: this is the kind of task where a goal-driven loop
*could* have run unattended if I'd externalized that DoD. I didn't, because the
task was small and the judgement (which branch to keep) was a one-liner once state
was audited. **Calibrate rigor to task size** — a markdown DoD for a 4-command
cleanup would have been waterfall-on-a-small-task. Noting this as a data point
*against* over-applying goal-driven ceremony, in line with the essay.

**Up-front vs. incremental note.** The branch cleanup needed almost no up-front
planning. The exploration (audit + map) *earned* the decision; the decision was
trivial once the state was known. This is the essay's "explore to earn the right
to write a spec" at micro-scale: I explored the branch state, and the spec
("continue on decisions-skill, delete decisions-report, rebase onto dev") was
obvious. No spec document needed. The exploration *was* the spec.

---

## Open questions / things to watch for (fodder prompts)

- [ ] **THE live question: what is the engine's real recall ceiling?** The
      grammars match specific strings. How many real decisions in a real corpus
      get phrased in ways the grammars don't catch? I have an intuition it's a
      lot. How do I measure recall on a thing with no ground truth — do I sample
      transcripts by hand, do I ask an agent to read the same transcripts and
      compare, do I widen the grammars and watch the false-positive rate climb?
      This is the blocking question for the whole design.
- [ ] **Is the two-pass split even the right shape?** If recall is low, the
      engine isn't doing high-recall, it's doing high-recall-over-a-narrow-
      surface, and the agent pass is silently doing both jobs on everything else.
      Alternative shapes to hold in mind: (a) engine does *indexing* only
      (parse + store + cite) and the agent does all decision detection; (b)
      engine is one of several *candidate generators* the agent reconciles; (c)
      throw the split out, agent reads transcripts directly with engine only for
      citation/dedupe. Don't pick yet — explore.
- [ ] **What would change my mind about the split?** Write down the falsification
      condition *before* I go looking, so I don't move the goalposts. E.g. "if a
      hand-sample of N transcripts shows the grammars miss > X% of real decisions,
      the split is wrong." Stating it up-front is the essay's point about earning
      the right to walk away — except here I'm earning the right to *commit* to a
      design.
- [ ] The engine's 9 tests are over fixed-date fixtures that encode a full
      lifecycle (Postgres→SQLite→DuckDB churn, "for now" debt, open question,
      plain decided). The tests pass — but they test the grammars against
      transcripts *written to match the grammars*. That's a closed loop. It tells
      me the engine is self-consistent, not that it's high-recall. Track this as a
      specific instance of the general trap: a deterministic DoD that passes on
      synthetic fixtures and fails on reality.
- [ ] Where would a `/until-done`-style goal loop be a category error on this
      skill? The engine, sure. The report? Never. The boundary is the exact
      boundary the essay draws — and the recall question is precisely about where
      that boundary actually sits.

---

## Ongoing entries

_(Append dated entries below. Keep them specific: what I did, what I decided,
what the DoD was, whether it was deterministic or qualitative, and any moment
where the up-front/incremental or TDD/goal-driven tension showed up.)_

### 2026-07-13 — the plan: multi-pass decisions2, seeded by commit messages

**Decision.** We're going to use a multi-pass approach, in a NEW skill
`decisions2/` (new directory, same branch). The current `decisions/` skill stays
as-is for comparison and as the cold-start fallback. The first pass seeds from
the strongest decision indicator — commit messages — and those high-confidence
examples establish a "context fingerprint" used by later passes to find
less-obvious candidates. The second part (the fingerprint) was fuzzy; refined it
below.

### 2026-07-13 — refining the "context fingerprint" into something concrete

**The fingerprint has three dimensions, each scoped at three levels.**

Dimensions:
1. **Lexical — HOW decisions are stated.** The current DECIDE_RE is a GENERIC
   English decision grammar ("let's go with", "decided on") — calibrated to
   textbook language, not how the user actually talks. The lexical fingerprint
   is the user/project's ACTUAL decision vocabulary, mined from commits.
   Extract TEMPLATES (verb + object + optional destination) and VERBS, not full
   phrases, because commit-language and chat-language differ in surface but
   share structure. "Move assistant message copy button to right side" →
   template [move|redirect|put] [entity] [destination], which matches "make it
   so /teams redirects to /team" via synonym. Full-phrase extraction won't
   transfer from commits to chat; template extraction does. This is the RISKY
   dimension (transfer risk) — lean on templates/verbs, and let entity carry
   more weight.
2. **Entity — WHAT gets decided about.** The set of components/modules/files/
   conventions this project decides on, extracted from commit objects. A user
   turn mentioning a fingerprinted entity + any directive verb = candidate.
   Catches decisions phrased as plain instructions ("make the dialog twice as
   wide") because the entity (Dialog) is fingerprinted even though the verb
   isn't a decision verb. Transfers most cleanly from commits to chat → should
   carry the most recall weight.
3. **Decision-type — WHICH KINDS.** A taxonomy (naming, architectural, scope,
   config, UI/layout, dependency) + the project's distribution over it.
   Calibrates where to look hardest; groups the report coherently.

Scopes:
- **User fingerprint** — portable across the user's projects; mainly lexical +
  decision-type tendencies. Built from all the user's commits across projects.
- **Project fingerprint** — codebase-specific; mainly entity + project-specific
  vocabulary. Built from this project's commits + confirmed decisions.
- **Session fingerprint** — running set of decisions and open questions IN THIS
  session; used for question→answer pairing and continuation detection. Cheapest,
  most localized, computed incrementally.

A user turn is evaluated against the composition of all three.

**The passes:**
- **Pass 1 SEED (deterministic)** — commit-message recall. Scan corpus for
  `git commit -m` with decision-shaped language. Extract decision text, entity,
  verb/template, type, author, project, session. The only pass that runs without
  a fingerprint; it BUILDS the fingerprint.
- **Pass 2 FINGERPRINT (deterministic)** — from seeds, build lexical verb/template
  tables per user+project, entity sets per project, type distribution per project.
- **Pass 3 EXPAND (mostly deterministic)** — run fingerprints over ALL user turns
  to find less-obvious candidates. Techniques in cost order: entity-match
  (deterministic) · lexical-template-match (deterministic) · question→answer
  pairing (deterministic, session-fingerprint) · embedding similarity (local,
  zero-key) · LLM few-shot (qualitative, last resort, residue only).
- **Pass 4 SNOWBALL (deterministic)** — agent-confirmed candidates get added back
  to the fingerprint. Grows over runs; re-runs converge, never duplicate. This
  is the self-improving loop (Greg's snowball, generalized).

**Cold start.** A new user/project has no commits → no fingerprint. Fallback:
the existing generic grammar (DECIDE_RE etc.) as the DEFAULT fingerprint until
enough seeds accumulate. So the current `decisions/` engine isn't thrown away —
it's the cold-start prior; the fingerprint is the personalized posterior.

**The risk to measure.** Commit-language and chat-language differ in surface.
Dimension 1 (lexical) is the risky one. After Pass 1, measure: how often does
the lexical fingerprint match in-chat phrasings vs. how often the entity
fingerprint does? If lexical underperforms, shift weight to entity + embedding.

**Blog connection (sharper thesis).** This adds a move the essay doesn't name:
the DoD ladder is SELF-SEEDING. Each rung's high-confidence output becomes the
training signal for the next rung's detection. Commits (rung 1, deterministic)
seed the fingerprint; the fingerprint (rung 2, deterministic) improves
entity/template detection (rung 3, deterministic); confirmed candidates
snowball back. The qualitative LLM rung only handles the residue. Richer than
"add more deterministic channels" — it's the deterministic channels TEACH each
other. And it addresses the essay's intent-staleness concern: the fingerprint
is scoped per user/project/session and updates from recent commits, so it
tracks intent as it moves, rather than grinding against a stale generic grammar.
The fingerprint is how you keep the DoD fresh without a human re-specifying.

### 2026-07-13 — definition settled: a decision is a PROCESS, not a final choice

**Question that came up.** Some decision candidates are questions. Is
questioning automatically a signal that something is not yet a decision? I
noticed the expand probe was flagging question-form turns as decisions (e.g.
ord=169 "do we really need to put posthog in billing code?", ord=172 "is it
best for a single event at submit or should we create patterns?").

**Answer.** No — questioning is not automatically the absence of a decision.
There are (at least) three states that question-form turns can be, and the
current engine collapses all three into OPEN:
1. **Genuine open question (undecided)** — "Should we use tabs or a sidebar?"
   The user is asking for input, hasn't chosen, no decision exists. A question
   here IS correctly the absence of a decision.
2. **Decision in formation (deliberating)** — "is it best for a single event at
   submit or should we create patterns?" The decision is in flight, options
   being weighed. Not final, but it's decision WORK — the deliberation that
   shapes the final choice.
3. **Decision stated as question (softened choice)** — "do we really need to
   put posthog in billing code?" Read literally a question; pragmatically a
   proposed choice (remove posthog from billing) wearing question syntax as
   politeness/softening.

**The definition question this exposes.** What counts as "a decision" for this
skill? Three possible definitions:
- **Strict: a finalized choice.** Only resolved state-1 and state-3 count.
  Current engine's implicit definition. Clean but misses the WHY — the
  deliberation and alternatives that shaped the choice, which is the most
  valuable content in an ADR.
- **Process: the whole arc — proposal, deliberation, choice, reversal.** States
  1, 2, 3 all count as decision EVENTS in a timeline that culminates (or
  doesn't) in a final choice. The report tells the story of how a decision was
  reached. Noisier, harder to scope.
- **Choice-points: any point where the user steered the agent between
  alternatives.** Includes state-3 and the ANSWER to state-1, but not state-1
  itself or state-2 unless it produced a steer. Clean but makes the
  deliberation invisible.

**Decision: a decision is a PROCESS.** The whole arc — proposal, deliberation,
choice, reversal, open-question-that-never-resolved. This is what makes
decisions2 different from a glorified commit log: it captures the WHY and the
alternatives considered, not just the final choice. The essay's framing ("the
expensive half of modern software is the intent behind the code"; "decisions
evaporate into scrollback") points at process, not finality — what evaporates
is the deliberation, the rejected alternatives, the rationale, not the final
choice (which is often in the commit message or the code).

**Three implications for the design.**
1. **The report tells a timeline per entity**, not just a status. A decision
   record for "EventStore" is the full arc: open question (06-02) → proposal →
   choice: Postgres (06-02) → reversal → choice: SQLite (06-09) → reversal →
   choice: DuckDB (06-15). The current engine already does this for the
   changed/decided arc; process means extending it to include the deliberation
   beats (state 2) and the open questions (state 1) as part of the SAME record,
   not a separate "Open" bucket.
2. **Status becomes a per-beat attribute, not a per-decision classification.**
   Each beat in the timeline has a role: `proposed`, `deliberated`, `chosen`,
   `reversed`, `reopened`, `open`. The decision (the whole arc) has a current
   state: `in-formation`, `decided`, `changed`, `abandoned` (open and stale).
3. **Disambiguating state 1 from states 2/3 is a qualitative judgment — the
   agent pass's job.** No deterministic grammar can reliably tell "genuine open
   question" from "softened choice" — it depends on context (prior proposal?
   responding to an agent suggestion? what does the user do next turn?). Clean
   division of labor: the engine flags question-form turns with decision
   signals as CANDIDATES (recall), the agent reads context and assigns the
   per-beat role (precision). The engine doesn't have to get the status right;
   it just has to surface the candidate. This REINFORCES the two-pass design —
   status disambiguation is precision work, and the essay's "human at the DoD
   boundary" maps exactly to the agent doing this disambiguation.

**Blog fodder.** This is a clean instance of the essay's "invest in the DoD
like your ability to walk away depends on it" — the DEFINITION of what counts
as a decision IS a DoD question, and getting it wrong (strict instead of
process) silently produces a worse artifact (a commit log, not an ADR) that
still passes its tests. The 9-green-tests on the current engine are green for
the strict definition; they would fail (correctly) for the process definition.
The tests encode a DoD that doesn't match the actual intent. And this was
discovered by doing, not by up-front spec — the question only surfaced when I
hand-checked the expand probe's output and noticed question-form candidates
felt wrong. **The measure-first discipline surfaces DoD questions the up-front
spec would have missed.**

### 2026-07-13 — entity extraction fix + re-measure, then scaffold

**The fix.** Two changes to `expand_probe.mjs`:
1. **Word boundaries on entity extraction.** The old regex
   `[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+` matched mid-word ("SpecStory" →
   "pecStory" because 'p' is not a word boundary but the regex didn't require
   one). Added `\b` at both ends: `\b[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+\b`.
   This kills "pecstory" and "osthog" at the extraction step.
2. **Ubiquity cap, calibrated.** `UBIQUITY_MAX=30` (initially tried 5, too
   aggressive — killed good project entities like `ui`, `skills`, `analytics`
   that appear in many commits because the project makes many UI decisions,
   not because they're noise). At 30, product names (`specstory`) die but
   project entities survive.
3. **Word boundaries on entity MATCHING.** Changed `lower.includes(e)` to a
   `\b...\b` regex test, so "pecstory" can't match inside "specstory" even if
   it somehow got into the set.

**The re-measured numbers (website session, 169 turns).**
```
  BEFORE fix:  21 new finds, ~71% precision, 19 entities (many broken)
  AFTER fix:    9 new finds, ~89% precision, 12 entities (all clean)
```
Traded quantity for quality: 21→9 new finds, but 71%→89% precision. That's the
RIGHT trade for a candidate generator feeding arcs — cleaner candidates =
cleaner arcs. The 12 "lost" finds were mostly entity matches on "pecstory"
that happened to be in real decision turns but for the wrong reason (the
project name appeared, not a real entity signal).

**Technique breakdown after fix.** Template-match is the workhorse: 7 of 7
template-only hits are real decisions (100% — `remove`, `move..to` patterns).
Entity-match is now clean and contributes: ord=120 (entity:analytics, real),
ord=172 (entity:somehow, real — the posthog deliberation). The one false
positive (ord=6) is a DIFFERENT bug: "remove" matching inside QUOTED AGENT
TEXT ("Say the word if you'd rather I remove it" — the user quoted the
agent's text). That's a parse-level fix (strip quoted agent text from
intent_raw before matching), not an entity fix. Noted for the real
implementation.

**Remaining issue for the real implementation.** The ubiquity cap is too
blunt: "specstory" as an entity is genuinely ubiquitous (project name) but
turns mentioning it + a directive verb ARE often real decisions about the
project. The real implementation should use per-project entity sets (not
global), and ubiquity = appears in >X% of a project's turns, not >X absolute.
Or: don't use a ubiquitous entity as a CLUSTERING key, but DO use it as a
CANDIDATE signal. The probe is good enough to scaffold from.

**Verdict.** The entity fix is confirmed as a prerequisite for arc clustering
(issue 2 from the arc test). Precision is now 89%, entity set is clean,
template-match carries recall. Ready to scaffold.

### 2026-07-13 — tests + SKILL.md (the skill is now installable)

**Tests.** 11 `node --test` tests over a fixture (`decisions2-alpha`) encoding a
full process arc:
- EventStore: deliberation (Postgres or SQLite?) → proposed (go with Postgres)
  → chosen (commit) → reversed (switch to SQLite) → chosen (commit) = an arc
  with state=changed, 2 chosen beats, 1 reversal.
- AuthClient: provisional "for now" with no commit = in-formation arc.
- SettingsPanel: deliberation question with no commit = in-formation arc.

Tests cover all four passes: seed extraction (commit messages + entities +
verbs), fingerprint building (entity in the set), expand (role assignment:
proposed/deliberated/reversed/provisional), and arcs (state=changed for
EventStore, state=in-formation for AuthClient + SettingsPanel, plus a
determinism test that re-running the pipeline produces identical arcs).

**Two bugs found by writing tests (the measure-first discipline, again):**
1. **Non-idempotent passes.** `extractSeeds` and `expand` didn't clear their
   tables before inserting, so re-running doubled the candidates. The
determinism test caught it immediately (second run had 2x the beats). Fixed by
   adding `DELETE FROM ...` at the start of each pass. This is the same lesson
   as the scaffolding bug #2 (idempotency is a DoD), now enforced by a test.
2. **Verb extraction test was wrong.** I expected `feat`/`refactor` as the verb,
   but the verb is extracted from the BODY after the conv prefix ("use Postgres"
   → verb "use"). The test was testing my wrong mental model of the code.
   Writing the test caught the mismatch. Lesson: tests don't just catch bugs —
   they catch misunderstandings between the spec-in-your-head and the code.

**SKILL.md.** The agent contract for the precision pass. The key sections:
- **Why "decision as process"** — the deliberation, rejected alternatives,
  provisional deferrals, and reversals are the WHY that evaporates. The engine
  surfaces the full arc; the agent writes it up.
- **The precision pass** — five jobs for the agent: drop non-decisions, resolve
  referents (Read the evidence file), name entities, split over-linked arcs
  (file clustering can merge unrelated decisions), classify question-form beats
  (genuine open vs. decision-in-formation vs. softened choice — the three states
  from the definition discussion).
- **The report format** — per-project, per-state (Decided / Changed / In
  formation / Abandoned), with insights (churn, debt, re-litigated).
- **Status: experimental.** Explicitly marked; the engine's recall and arc
  linking have been measured, the precision pass and report format are still
  being refined.

**README.md + install.sh.** The skill is now installable via `./install.sh`
(symlinks into `~/.agents/skills/decisions2` and `~/.claude/skills/decisions2`).

**The skill structure is now complete:**
```
decisions2/
  README.md, SKILL.md, package.json, install.sh
  scripts/
    decisions2.mjs              # CLI
    lib/{db,indexer,commit_seed,fingerprint,expand,arcs}.mjs
    commit_seed.mjs, expand_probe.mjs, arc_test.mjs  # the probes (kept)
  tests/decisions2.test.mjs     # 11 tests, all green
  fixtures/decisions2-alpha/.specstory/history/  # 4 transcripts, full arc
```

**Blog fodder from this step.** The two test-caught bugs reinforce a point from
the essay that I hadn't connected before: **tests are a DoD technology, but they
test the engine's contract with itself, not the skill's contract with the
user.** The determinism test caught the idempotency bug (engine contract); the
verb test caught my mental-model mismatch (spec-vs-code). Neither test tells
you whether the REPORT is good — that's the qualitative DoD the agent pass
owns. The 11-green-tests are a real rung on the ladder (the engine is
self-consistent and deterministic) but they are NOT the top rung (the report
actually captures the real decisions). The top rung is still the human/agent
judgement at the DoD boundary, exactly as the essay argues. **The tests buy
you the right to trust the engine, not the right to trust the report.**

### 2026-07-13 — file-based arc linking (the Workthreads arc now forms)

**The problem.** After scaffolding, the Workthreads `[proposed]` (chat) and
`[chosen]` (commit) were in SEPARATE arcs because neither had a fingerprinted
entity. The arc test predicted this: entity clustering alone doesn't link all
arcs; the `files` field is the missing linker.

**Two issues to solve.**
1. Candidates have files (from the beats table), but SEEDS often have empty
   files — the `git commit` beat is separate from the `git add` beat that
   captured the files. So commit seeds have no files to cluster on.
2. Need a second clustering key (files) alongside entities, with its own
   ubiquity cap (package.json, etc. would chain everything).

**The fix in `arcs.mjs`.**
1. **File inheritance for seeds.** A seed beat with empty files inherits files
   from nearby beats in the same session (ord +/- 3). The `git add` that
   staged the files is usually within a few beats of the `git commit`.
2. **Dual clustering key.** Union-find on shared entity OR shared file (per
   project). Entity ubiquity cap = 15; file ubiquity cap = 10; noise-files
   denylist (package.json, tsconfig.json, readme.md, claude.md, .gitignore,
   go.mod, go.sum, package-lock.json).
3. **Arc label.** Prefer a non-ubiquitous entity; fall back to the most common
   non-noise file basename; else (unnamed).

**Result.** Arc count dropped 7987 -> 5003 (file linking merged singletons).
The Workthreads arc (id 51270) now has **18 beats** with the full process:
```
[chosen] Add /team (Loop) product page; wire Loop into /pricing
[provisional] on /pricing we need a Learn more link... for now
[proposed] we're going to use the terminology "Workthreads" instead of "Loops"
[chosen] Rename "Loops" to "Workthreads" across product pages
[chosen] Complete Loops->Workthreads rename (mockup + page copy)
[provisional] put a big CTA "Find your perfect plan" as placeholder for now
[proposed] remove the "Coming Soon" from all sections
[chosen] Pricing matrix: add Knowledge base section; rename groups; tidy rows
... (+ 10 more beats, including a reversed and more chosen)
```
The chat proposal and the commit choice are now in ONE arc, linked via
`./app/team/LoopMockup.tsx`. **File-based arc linking works.**

**The over-linking tradeoff (noted, not fixed yet).** The 18-beat Workthreads
arc also pulled in the localstorage decision and the Coming Soon removal,
because they share files in the same session. That's the classic clustering
tradeoff: file linking merges related arcs but also merges unrelated ones
touching the same files. Two ways to tighten later: (a) scope file clustering
within a session window (beats close in time, not across the whole project),
(b) let the agent pass split over-linked arcs by reading the evidence. The
latter is the two-pass design doing its job — the engine over-links (high
recall), the agent splits (precision).

**Remaining single-beat (unnamed) arcs.** Many candidates have no entity and no
files (e.g. "let's make the interview"). These stay as singleton arcs. The agent
pass would merge them into the right arc by reading context, or drop them as
non-decisions. This is expected at the engine stage — the engine's job is
recall, not tidy arcs.

**Numbers after file linking.**
```
arcs: 5003 (down from 7987 — file linking merged singletons)
  in-formation: 3960
  decided:       662
  changed:       226
  abandoned:     155
```
The decided count dropped 1818 -> 662 because many commit-only "arcs" that were
decided-by-virtue-of-being-a-commit now merge into larger arcs (some of which
become "changed" because they have multiple chosen beats). More honest.

**Next.** Tests over fixtures encoding full process arcs, then SKILL.md (the
agent contract for the precision pass). The over-linking and the (unnamed)
singletons are agent-pass problems, not engine problems — the engine has done
its job (recall + linking); precision is the agent's.

### 2026-07-13 — scaffolding decisions2/ (for real)

**Built the full skill structure** and ran it end-to-end against the real corpus
(4350 sessions across 27 projects):

```
decisions2/
  scripts/
    decisions2.mjs          # CLI: index | seed | fingerprint | expand | arcs | decisions
    lib/
      db.mjs                # arc-oriented schema: sessions, beats, commands, seeds,
                            #   fingerprints, candidates (with ROLE), arcs (with STATE)
      indexer.mjs           # reuses decisions/ parse.mjs + patterns.mjs + discover.mjs
      commit_seed.mjs       # Pass 1: commit-message seed extraction (role: chosen)
      fingerprint.mjs       # Pass 2: per-project entity + verb fingerprints
      expand.mjs            # Pass 3: fingerprint + grammar guided expansion (roles:
                            #   proposed/deliberated/reversed/provisional/open)
      arcs.mjs              # cluster candidates + seeds into per-entity process arcs
                            #   (states: in-formation/decided/changed/abandoned)
  scripts/commit_seed.mjs, expand_probe.mjs, arc_test.mjs  # the probes (kept)
```

**End-to-end pipeline on the real corpus:**
```
index:     4350 sessions across 27 projects
seed:      2050 seeds (Pass 1) — by project: intent=531, stoa=1106, sync=148,
           deadreckon=239, website=15, ...
fingerprint: 8 projects with entities + verbs (Pass 2) — stoa=198 entities/345 verbs,
           intent=117/145, sync=19/112, ...
expand:    6543 candidates (Pass 3) — proposed=5401, deliberated=720,
           reversed=223, provisional=199
arcs:      7987 arcs — in-formation=5684, decided=1818, changed=286, abandoned=199
```

**The digest renders with per-project per-state timelines.** The Workthreads
rename appears as `[proposed]` in chat ("we're going to use the terminology
'Workthreads' instead of 'Loops'") and `[chosen]` in commits ("Rename Loops to
Workthreads across product pages") — both surfaced, both with evidence refs.

**Three bugs fixed during scaffolding (each a real lesson):**
1. **OOM from file reads.** `readHeredocMessage` read+split each 10MB transcript
   per commit command. Fixed by grouping commits by file, reading each file once,
   and clearing the cache between files. Lesson: deterministic doesn't mean
   cheap — the engine's resource budget is a DoD constraint too.
2. **Table duplication on re-runs.** The `decisions` command re-ran all passes
   without clearing tables, doubling candidates. Fixed by clearing decision
   tables at the start of `runPipeline()`. Lesson: idempotency is a DoD.
3. **The `Object.entries(stateLabel)` bug.** Destructured `[label, state]` and
   filtered by `a.state === state` — but `state` was the VALUE ('In formation')
   not the KEY ('in-formation'). The digest showed project headers with no
   content. Fixed by filtering by `label` (the key). Lesson: a 1-character bug
   can make a working pipeline look broken; the digest was empty not because
   the data was missing but because the rendering compared against the wrong
   half of a key-value pair.

**Key issue confirmed (from the arc test): the arcs don't fully link yet.** The
Workthreads rename's `[proposed]` (chat) and `[chosen]` (commit) are in SEPARATE
arcs because neither has a fingerprinted entity — the chat turn's entity isn't
in the fingerprint, and the commit's entity isn't extracted from the prose
message. The arc test predicted this: entity clustering alone doesn't link all
arcs; the `files` field (what files the agent touched) is the missing linker.
The scaffold has the `files` field in the beats table but `arcs.mjs` doesn't use
it for clustering yet. That's the next implementation step.

**What's working vs. what's next.**
Working: the multi-pass pipeline (index → seed → fingerprint → expand → arcs →
  digest), the role classifier, the arc state classifier, the per-project
  fingerprint, the commit-message seed extraction with heredoc support, the
  full-grammar + fingerprint combined candidate generation.
Next:
  1. **File-based arc linking** — use the `files` field as a second clustering
     key so the Workthreads proposed (chat) + chosen (commit) link into one arc.
  2. **Entity extraction for prose commit messages** — conventional-commit
     scopes are clean; prose messages like "Rename Loops to Workthreads" need
     the entity ("Loops"/"Workthreads") extracted so they cluster.
  3. **Dedup across resumed sessions** — same transcript content appears in
     multiple files (re-included sessions); dedup on (project, quote).
  4. **Tests** — the probe scripts validated the approach; the real modules
     need `node --test` tests over fixtures encoding full process arcs.
  5. **SKILL.md + install.sh** — the agent contract (the agent does the
     precision pass: drop false positives, resolve referents, name entities,
     write the report from the engine's arc digest).

**Blog fodder from the scaffolding.** The three bugs each illustrate a point
from the essay: (1) the OOM is a resource-budget DoD the up-front spec missed —
determinism doesn't buy you the right to walk away if the engine OOMs on real
data; (2) idempotency is a DoD — a non-idempotent pipeline isn't re-runnable,
and re-runnability is what makes the deterministic layer cacheable; (3) the
key-value-pair bug is why you measure against reality — the tests would have
passed (the data was there) but the artifact was broken. **A deterministic DoD
that renders empty output is still broken, even if every internal step
'works.'** The DoD is the OUTPUT, not the pipeline.

**What I built.** `decisions2/scripts/arc_test.mjs` — pulls ALL candidates
(grammar catches + fingerprint catches + commit seeds) from one session,
sorted by turn order, so I can hand-cluster them by entity/topic into arcs and
test whether the process definition is realizable on top of the current
candidate set.

**Target.** The website session (169 unique user turns, 32 candidates, 6
commit seeds in-session). Hand-clustered the 38 beats by entity/topic.

**The arcs DO form. 9 coherent arcs from 32 candidates + 6 seeds.**

1. **Workthreads/Loops naming** — proposed (ord=44, decide: "we're going to
   use the terminology 'Workthreads' instead of 'Loops'") → chosen (3 commits:
   "Rename Loops to Workthreads across product pages"). CLEAN ARC. The chat
   proposal + the commit choice link perfectly.

2. **/team page + /teams redirect** — proposed (ord=5, entity:ui: "build three
   product pages '/pro', '/team' and '/enterprise'") → chosen (2 commits: "Add
   /team product page; remove old /teams", "Redirect /teams to /team
   (permanent)"). CLEAN ARC.

3. **Pricing Learn More links** — proposed (ord=30, provisional: "'/pro' as a
   link to nowhere for now") → refined (ord=40, 42, 65: add maven course link,
   reword copy, point Free card to docs). DELIBERATION ARC — the user iterates
   on the Learn More links across 4 turns. The "for now" at ord=30 is
   provisional, later refined into real destinations.

4. **Coming Soon removal** — standalone decision (ord=67, tpl:remove: "remove
   the 'Coming Soon' from all sections. they're now available"). SINGLE-BEAT
   ARC (scope decision: un-gate features).

5. **/plan page interview** — proposed (ord=74, decide: "let's make the
   interview") → change (ord=104: "Edit my answers" link should go back to
   beginning) → remove (ord=108: remove "Back to Pricing") → provisional
   (ord=135: "single page interview ... for now forget the 'For me' 'For my
   team' 'For'") → cleanup (ord=137) → **reversal** (ord=143, change: "let me
   change my mind on Role ... click already selected role → no role
   selected"). RICH ARC WITH REVERSAL + PROVISIONAL. The explicit "change my
   mind" at ord=143 is the reversal signal the CHANGE_RE grammar caught.

6. **Pricing grid / Skills section layout** — proposed (ord=59, entity:skills +
   tpl:move..to: "move 'Skill generation' to the top") → refined (ord=70,
   tpl:move..to: "move 'Skill auto steering' up one") → refinement (ord=110,
   decide: "refinement to the pricing grid") → chosen (commit: "Pricing matrix:
   add Knowledge base section; rename groups; tidy rows"). CLEAN ARC — multiple
   proposed beats leading to a commit.

7. **Stripe checkout integration** — decision (ord=84, decide: "let's make a
   [different sandbox]") → decision (ord=133: "update website to have checkout
   links go to the real live pages") → debugging (ord=177, 184: bug reports on
   the Pro purchase flow). MIXED ARC — decisions + investigation beats on the
   same thread.

8. **PostHog in billing — THE state-2 arc** — deliberation (ord=161: "What are
   the best practices for tracking users with posthog?") → deliberation
   (ord=169: "do we really need to put posthog specific data into our billing
   code?") → deliberation (ord=172: "is it best for a single event at submit or
   should we create properties on a person in posthog?"). UNRESOLVED
   DELIBERATION ARC — three question-form turns, no final choice, no commit.
   This is exactly the state-2 (decision in formation) arc that the strict
   definition would miss entirely and the process definition captures. It's a
   real architecture decision being weighed, and it belongs in the report as
   "under consideration" with the options on the table.

9. **"Find your perfect plan" CTA — provisional debt resolved** — provisional
   (ord=62: "put a big CTA 'Find your perfect plan' as placeholder for now") →
   reversed (ord=102, tpl:remove: "remove this specific 'Find your perfect
   plan' button"). The "for now" at ord=62 became a removal at ord=102. The
   engine should link these as a provisional-debt-resolved arc.

**Verdict: the process definition is realizable.** The candidates naturally
form coherent arcs with the proposed→deliberated→chosen→reversed structure.
All five roles appear: `proposed` (ord=5, 44, 74), `deliberated` (the posthog
arc), `chosen` (the 6 commits), `reversed` (ord=143 "change my mind", ord=102
removal of the placeholder), `provisional` (ord=30, 62, 135). The commit seeds
anchor arcs with `chosen` beats; chat candidates fill in the
proposal/deliberation/reversal beats. The two sources are complementary IN THE
ARC STRUCTURE, not just in recall.

**Three issues the arc test surfaced.**

1. **Entity clustering alone doesn't link all arcs — some need topic/file
   clustering.** The /plan arc (#5: ord=74, 104, 108, 135, 137, 143) is linked
   by the topic "/plan page", not by a fingerprinted entity. The turns don't
   share an entity — they share a file/route. So entity clustering alone won't
   link them. **The `files` field in the beats table (what files the agent
   touched) is the missing linker** — beats that touch the same file cluster
   into the same arc. This is mechanism A (action-based recall) from the
   brainstorm, now confirmed as necessary for ARC LINKING, not just recall.

2. **The ubiquitous-entity noise drags arc coherence.** "pecstory" (from
   "SpecStory", mid-word match) fires on 8 of 32 candidates just because the
   project name appears. It would chain unrelated arcs if used as a clustering
   key. The entity extraction fix (Step 2) isn't just a precision win — it's
   necessary for arc clustering to work. The current engine's ubiquity cap
   (ENTITY_MAX=5) handles this; decisions2 needs the same, plus the
   word-boundary fix.

3. **The posthog arc (state 2) has no `chosen` beat — it's unresolved.** The
   process definition says this is a valid arc ("under consideration"), but
   the report needs a way to represent it. The current engine's `open` status
   is close but not quite right — `open` implies a yes/no question; the posthog
   arc is a multi-option deliberation. The report needs a `deliberating` /
   `in-formation` status for arcs with deliberation beats but no chosen beat.

**What the arc test tells us about the schema.**
- An arc is a cluster of candidate beats linked by shared entity OR shared
  file/topic (not just entity — issue 1).
- Each beat in an arc has a role: proposed / deliberated / chosen / reversed /
  provisional / open.
- An arc has a current state: in-formation (deliberation beats, no chosen) /
  decided (has a chosen beat, no later reversal) / changed (has a later
  chosen that supersedes) / abandoned (open + stale).
- The commit seeds are the `chosen` beats; chat candidates are everything else.
- The `files` field is a second clustering key alongside entities.

**Next step: fix entity extraction, then scaffold.** The arc test confirms the
process definition is realizable and tells us the schema shape (arcs clustered
by entity OR file, with per-beat roles and per-arc states). The entity
extraction fix (Step 2) is now confirmed as a prerequisite — not just for
precision but for arc clustering to work. After that, scaffold decisions2/
with the arc schema.

### 2026-07-13 — best next steps, given the process definition

The process definition changes the shape of the skill in three concrete ways
that should drive what gets built next:
1. **The schema is a timeline per entity, not a flat list of decisions.** The
   beats table already supports this (beats have ord, date, entities, outcome).
   The seeds/candidates/fingerprints tables need to be designed around arcs,
   not flat candidates — a candidate is a beat in an arc, not a standalone
   decision.
2. **The engine needs a `role` classifier per candidate beat**
   (proposed/deliberated/chosen/reversed/reopened/open) — deterministic where
   possible, agent-assigned where not. The commit-seed pass assigns `chosen`
   (a commit is a finalized choice) and sometimes `reversed` (a revert commit).
   The fingerprint/expand pass assigns `proposed` or `deliberated` to
   question-form candidates. The agent pass refines.
3. **The report is per-entity timelines with the full arc**, grouped by
   decision-type, with insights (churn, debt, re-litigated) computed over the
   arc — which the current engine already does for the changed/decided subset.

Given that, the best next steps (measure-first discipline preserved):

**Step 1 (next probe): the arc test.** Take the website session's 30 candidates
(grammar + fingerprint catches) and hand-cluster them into arcs per entity
(Workthreads/Loops, pricing page, checkout integration, posthog-in-billing).
Question: do the candidates naturally form coherent arcs with the
proposed→deliberated→chosen→reversed structure? If yes, the process definition
is implementable and the schema design follows. If no (candidates are too
sparse or don't link), the process definition needs a relational pass
(question→answer pairing, mechanism E from the brainstorm) before it's viable.
This is the cheapest test of whether the process definition is realizable on
top of the current candidate set.

**Step 2 (fix precision drag): entity extraction.** Fix the Symbol-regex
mid-word match bug ("pecStory", "osthog") — use word-boundary matching or
proper NP extraction. Re-run the expand probe and measure precision lift. This
is the biggest single precision win and it's cheap.

**Step 3 (scaffold for real): decisions2/ with the arc schema.** Now that both
probes confirm the recall hypothesis and the process definition is settled,
scaffold the real skill with the timeline-per-entity schema, the role
classifier, and the per-entity-arc report. Tests over fixtures that encode full
arcs (the intent session's EventStore Postgres→SQLite→DuckDB is already a perfect
process-arc fixture).

I'd do Step 1 first (the arc test) because it tests whether the process
definition is realizable BEFORE we sink time into entity extraction and
scaffolding — same measure-first discipline. If the candidates don't form
arcs, the process definition needs more work and scaffolding is premature.

### 2026-07-13 — Pass 3 probe: fingerprint→expand, the second real number

**What I built.** `decisions2/scripts/expand_probe.mjs` — builds a minimal
fingerprint from the commit seeds, runs it over chat user-turns, and compares
against the lexical grammar. Two techniques:
- **entity-match**: user turn mentions a fingerprinted project entity (from
  conventional-commit scopes like `ui`, `analytics`, `skills`, `sync-cloud`)
  + any directive verb → candidate.
- **template-match**: user turn matches a verb template mined from commits
  (`remove`, `move..to`, `redirect..to`, `integrate..into`, `gate..behind`,
  `drop`, `demote`, `consolidate`, etc.) → candidate.

Target session: `2026-06-19_14-05-45Z-can-you-see-the.md` (the website session,
169 unique user turns, where the Loops→Workthreads rename happens).

**The numbers.**
```
  grammar catches:          11
  fingerprint catches:      23
  overlap (both):            2
  NEW FINDS (fp & !gram):   21
  grammar-only (gram & !fp):  9
```
**The grammar and fingerprint are almost disjoint** (2 overlap out of 34
total). They're complementary, not redundant — the grammar catches explicit
decision-language ("let's go with", "should we?"), the fingerprint catches
directive + entity and commit-template patterns. Combined: 30 unique catches
vs 11 grammar-only = ~2.7x more candidates.

**Hand-checked precision of the 21 new finds: ~71% real decisions.**

Real decisions (15): ord=5 (scope: build three product pages), ord=42 (copy:
wording change), ord=59 (layout: move Skill generation to top), ord=65 (UI:
add Learn More link), ord=67 (scope: remove Coming Soon gating), ord=70
(layout: reorder section), ord=95 (remove ugly element), ord=102 (remove
button), ord=108 (remove Back to Pricing), ord=120 (add line under analytics
section), ord=133 (integration: update checkout links to cloud), ord=161
(architecture open: best checkout approach), ord=169 (architecture: scope of
posthog in billing), ord=172 (architecture open: posthog event approach),
ord=191 (scope: what to build next).

False positives (3): ord=6 ("remove" in QUOTED AGENT TEXT, not user's words),
ord=83 (investigation request: entity "db" + "confirm"), ord=184 (bug report:
entity match + "look at").

Borderline (3): ord=40 (instruction about page content), ord=159 (git merge
direction — process not design), ord=177 (reporting a bug).

**Template-match is higher precision than entity-match (validates the
hypothesis).** Template-only hits: 5, of which 4 are real decisions (80%).
Entity hits (incl. both): 16, of which ~11 are real (~69%). The template
patterns mined from commits (especially `remove`, `move..to`) transfer cleanly
to chat language and catch real decisions the grammar's DECIDE_RE misses.

**The entity extraction bug shows up here concretely.** "pecstory" (from
"SpecStory" — the SYMBOL_RE regex matches mid-word) fires on many turns just
because the project name appears, adding noise. "osthog" (from "PostHog")
same issue. Two of the three false positives are from broken entity matches.
With fixed entity extraction, precision would be higher. The conventional-
commit-scope entities (`ui`, `analytics`, `skills`) are clean and fire well.

**The qualitative finding (the real point, more robust than the exact ratio):**
the fingerprint catches real decisions the grammar misses — UI layout choices
("move Skill generation to the top"), scope decisions ("remove Coming Soon
from all sections"), architecture questions ("do we really need to put posthog
specific data into our billing code"), integration decisions ("update checkout
links to go to the cloud"). These are exactly the kinds of decisions that
should appear in a decision log and that the lexical grammar structurally
cannot catch because they're phrased as plain instructions or questions, not
as explicit decision-language.

**What this means for the design (the multi-pass hypothesis is now
 doubly confirmed).**
1. Commit seeds are a high-confidence source (Pass 1 probe: 118 seeds, ~85-90%
   precision).
2. A fingerprint built from those seeds, run over chat turns, finds real
   decisions the grammar misses (Pass 3 probe: 21 new finds, ~71% precision,
   complementary not redundant with the grammar).
3. The two techniques (entity-match and template-match) have different
   precision profiles (80% vs 69%), and both contribute — so the fingerprint
   should use both, with template-match weighted higher.
4. The entity extraction bug is the main precision drag — fix it and precision
   rises. Conventional-commit scopes are clean; prose-entity extraction needs
   proper NP extraction, not the Symbol regex.

**What I have NOT measured yet (the remaining unknowns).**
- The false-negative rate of the COMBINED grammar+fingerprint. I have 30
catches from 169 turns — but how many real decisions are in those 169 turns
that BOTH miss? Need a full hand-read to get the denominator. (The earlier
experiment on the intent session showed ~67% miss rate for grammar-only; the
fingerprint should cut that substantially, but I haven't measured it.)
- Whether the fingerprint generalizes across projects. The fingerprint was
built from website+sync seeds and tested on a website session. Does a
sync-built fingerprint find decisions in a website session? (User-level
fingerprint portability — the essay's intent-staleness angle.)
- The LLM residue: how many decisions do BOTH miss that only an LLM proposer
catches? Still unknown — that's Pass 3 technique 5, deliberately last.

**Next step options.**
A. Fix the entity extraction (the biggest precision drag), re-run the probe,
   and measure precision improvement.
B. Do a full hand-read of the 169 turns to get the false-negative denominator
   — how many real decisions are in the session, and what % do grammar+
   fingerprint catch combined?
C. Scaffold the real decisions2/ skill (now that both probes confirm the
   hypothesis) and build Pass 1 + Pass 2 + Pass 3 properly with tests.
D. Test cross-project generalization (build fingerprint from sync, run on
   website) — the portability question.

I'd lean A then C: fix the precision drag, then scaffold for real. B is the
most rigorous but most expensive (hand-reading 169 turns). D is interesting
but can wait until the skill is scaffolded.

### 2026-07-13 — Pass 1 probe: commit-message seed extraction, first real number

**What I built.** A minimal probe `decisions2/scripts/commit_seed.mjs` (single
file, reuses `decisions/scripts/lib/db.mjs`). Extracts `git commit -m` messages
from the indexed corpus — both single-line (`-m "msg"`) and heredoc
(`"$(cat <<'EOF' ... EOF)"`) forms. The heredoc case needed transcript reading:
the `commands` table stores only the command line, not the heredoc body, and
the `line` field points at the `<tool-use>` envelope, not the command (off by
~5 lines). Fixed by searching forward ~15 lines for the `<<'EOF'` opener, then
reading the message subject (first non-empty line after it). Filters liberally
for decision-shaped language: conventional-commit prefixes (`feat/fix/refactor/
rename/move/remove/revert/switch/replace/perf`) OR decision verbs in the body
(rename, switch, replace, remove, move, merge, revert, redirect, use X instead
of, rather than, go with, standardize on, default to, settle on, for now).
Excludes noise prefixes (wip, tweak, cleanup, fix typo, merge branch, chore,
docs, style, bump). False positives are cheap for a candidate generator, so the
filter is liberal.

**The corpus.** 308 sessions across 2 projects (specstory-website 68,
specstory-sync 240), already indexed in `/tmp/dec-rec2.db` from the earlier
recall experiment. 10,338 total commands, 445 deduped git-commit commands.

**The number.** **118 decision-shaped seeds from 445 commits = 26.5% seed
rate.** By project: sync 107, website 11. (sync is heavier on conventional-
commit format; website commits are plainer prose, which the verb filter still
catches.)

**Precision (hand-checked across the full 118).** Roughly **85-90% true
positives.** Genuinely strong decisions include:
- "Rename \"Loops\" to \"Workthreads\" across product pages" — **THE naming
  decision the grammar missed** (the chat turn was "we're going to use the
  terminology 'Workthreads' instead of 'Loops'", missed because "we're going to
  use" isn't in DECIDE_RE). The commit seed catches it. This is the proof point.
- "Switch to GA4 via @next/third-parties, remove manual Google Ads gtag.js" —
  analytics architecture decision.
- "Redirect /teams to /team (permanent)" — URL structure decision.
- "Default to a single owner-wide shard (all projects), demote sharding" —
  architecture decision.
- "feat(analytics): gate Analytics behind the Pro entitlement" — product/
  business decision.
- "feat(flags): dark-launch /analytics, /resume, /skills, /billing via
  Cloudflare Flagship" — feature-flagging decision.
- "Integrate lore-cloud/ into the existing site; remove the parallel dir" —
  architecture decision.
- "Remove premature Loading state - only show assistant message when content
  starts streaming" — behavior decision.

Noise (~10-15%): git merge plumbing ("Merge remote-tracking branch..." — my
NOISE filter caught "merge branch" but not "merge remote-tracking"), a few
test/ci/docs commits caught via the verb filter ("move as-built to..."), and
micro-UI fixes that are technically decisions but trivial ("collapsed ribbon
uses an arrow-less panel icon"). All fine for a candidate generator.

**Two free wins the probe surfaced (fodder for Pass 2 fingerprint):**
1. **Conventional-commit scopes are clean entities.** `feat(ui):`,
   `fix(sync-cloud):`, `feat(analytics):`, `feat(flags):`, `feat(spec-score):`
   — the scope in parens is a ready-made project entity, no entity extraction
   needed. The fingerprint's entity dimension gets a high-quality seed source
   for free from conventional-commit scopes.
2. **The verb templates are exactly what the chat grammar lacks.** "Rename X
   to Y", "Switch to X", "Redirect X to Y", "Default to X", "Move X to Y",
   "Integrate X into Y", "gate X behind Y" — these are the templates that would
   catch in-chat decisions if added to the lexical fingerprint. The commit
   corpus is a template MINE for the chat detector.

**The hypothesis is confirmed (provisionally).** Commit messages are a
high-confidence seed source: 26.5% of commits yield decision-shaped seeds at
~85-90% precision, and they catch decisions the lexical grammar misses (the
Loops→Workthreads rename is the proof). This is enough to justify building
Pass 1 for real and moving to Pass 2 (fingerprint) + Pass 3 (expand).

**Rough edges to fix in the real implementation (not the probe):**
- entity extraction is crude ("pecStory" from "SpecStory", "oogleAnalytics"
  from "GoogleAnalytics" — the Symbol regex matches mid-word). Conventional-
  commit scopes are clean; prose-message entities need better NP extraction.
- the NOISE filter should also exclude "merge remote-tracking" and reconsider
  whether `test:`/`ci:` prefixes are decisions (usually not).
- dedup: the same commit message appears 2-3x (re-runs, resumed sessions).
  Dedup on (project, normalized-message) for fingerprinting.

**Next: measure the expand hypothesis.** The probe proves seeds are findable.
  The next measurement is the harder one: take the 118 seeds, build a minimal
  fingerprint (entity set per project + verb-template set per project), run it
  over the chat user-turns in the same sessions, and hand-count how many
  LESS-OBVIOUS chat decisions it catches that the grammars missed. That's the
  test of whether the fingerprint→expand move actually raises recall, or
  whether commit-language and chat-language are too different to transfer.
  Build that as the next probe before committing to the full decisions2/
  scaffold.

### 2026-07-13 — reframing: false positives are cheap; the only number that
matters for a recall layer is the false-negative rate

I conflated precision and recall in the last entry. Correction: the first
pass's whole job is to SELECT CANDIDATES. False positives are cheap — the agent
pass filters them. The real cost is FALSE NEGATIVES — real decisions that never
become candidates, so the agent never sees them at all. The ~67% miss rate is
the only number that matters; the ~75% false-positive rate is a non-issue for a
candidate generator (it's only an issue if it's so high it drowns the agent, and
12 candidates per session is not drowning). So the question is NOT "how do we
make the grammars more precise" — it's "what other mechanisms can we use to find
decision candidates the grammars miss?" Sampled `~/dev/specstory-website` and
`~/dev/specstory-sync` histories (308 sessions) for real-world phrasings.

### 2026-07-13 — the big untapped surface: the engine reads ONE channel

The `beats` table already stores: `intent_raw` (user words), `files` (what the
agent touched), `n_cmds` / `tool_mix` (what the agent ran), `outcome`
(corrected/success/neutral, from the user's next turn). `decide.mjs` reads
**only `intent_raw`**. The entire "method" side of the beat — the agent's
ACTIONS and the session's OUTCOMES — is unused for decision detection. That's
the hole. Decisions leave traces in multiple channels; we're reading one.

Concrete miss from the corpus that proves it: the site-wide naming decision
`ord=44` "we're going to use the terminology 'Workthreads' instead of 'Loops'"
— a massive decision, MISSED by the grammar ("we're going to use" not matched;
only "we'll use"/"we will use"). But the same beat's `files` field shows the
agent touched `./app/team/LoopMockup.tsx`. The ACTION would catch what the WORDS
missed.

### 2026-07-13 — brainstorm: other mechanisms for recall (candidate generation)

Organized by SIGNAL SOURCE. The current mechanism is one source (user words) +
one technique (lexical match). Each alternative below is either a different
source, a different technique, or both.

**Mechanism A — Action-based recall (read what the agent DID).**
The beats table already has `files` and `n_cmds`. A beat where the agent
created/deleted/renamed a file, or touched a config/dependency file, is a
candidate decision-point; the entity is the file/module. Deterministic,
already-available, orthogonal to user words. Catches: the Workthreads rename
(agent touched `LoopMockup.tsx`), architectural decisions visible in file
creates/deletes, dependency adds/removes. Cost: needs a file-op classifier
(create vs delete vs rename vs edit) over the `files` field + the command
stream. Limitation: many beats touch files without a decision being made, so
this is noisy — but noise is fine for a candidate generator.

**Mechanism B — Commit-message recall.**
`git commit -m "..."` messages are LITERAL decision records — sampled from the
sync corpus: "Move assistant message copy button to right side", "Remove
premature Loading state", "Merge dev into error-pages branch", "feat: /pricing
marketing gateway". They're already parsed as shell commands in the transcript.
Developers intentionally record decisions in commit messages, so they're
high-signal and low-false-positive. A beat containing `git commit -m "..."` with
decision-shaped language (rename, switch, replace, remove, move, merge, revert,
or any conventional-commit `feat/fix/refactor:` that names a choice) is a
candidate decision. Deterministic, near-zero cost. This might be the single
highest-value add — commit messages are the closest thing to a deliberate
decision log that already exists in the transcript. Caveat: many commits are
micro ("WIP", "fix typo") — filter on decision-shaped verbs in the message,
not every commit.

**Mechanism C — Corrected-beat as reversal signal.**
The engine already computes `outcome` (corrected/success/neutral) from the
user's NEXT turn via `CORRECTED_RE`. A `corrected` beat = the user redirected
the agent = likely a decision reversal or correction. "no wait", "actually",
"revert", "go back", "that's bad practice", "removing the type label was a bad
move" (from the sync corpus, a real reversal the grammar missed). Reuses
existing deterministic signal; catches the CHANGE class even when the user's
correction doesn't contain a CHANGE_RE phrase. Cost: near-zero (outcome already
computed). Limitation: corrections include non-decision rejections ("no, that's
a bug") — fine for a candidate generator.

**Mechanism D — Agent-proposal → user-approval pairing (cross-turn structural).**
In many transcripts, the agent's turn enumerates options ("option 1: X, option
2: Y", "we could A or B", a numbered list) and the user replies "option 1",
"yep", "let's do X", or even just "yeah". The agent's proposal turn is a strong
decision-context signal. Detect agent turns that enumerate options and treat
the FOLLOWING user turn as a decision candidate regardless of its phrasing.
Deterministic-ish (numbered lists / "option N" / " vs " are detectable). Catches:
"yeah, tabs not a sidebar" (answers an earlier question, no decide phrase).
Cost: needs the indexer to store agent-turn text or an option-enumeration flag
(the beats table currently stores only `intent_raw`, not agent text) — a schema
change. Limitation: detecting proposals reliably across all providers is real
work.

**Mechanism E — Question→answer pairing (relational, same session).**
An open question in turn N followed by a firm direction in turn N+k on the same
entity = a resolved decision. Pair OPEN candidates with later non-open turns on
the same entity even if the later turn doesn't match a DECIDE grammar. Catches:
"Should it really be 15 seconds?" → "lets reduce the window" (the answer is the
decision, "lets reduce" isn't a DECIDE phrase). Deterministic. Cost: a join
over beats within a session keyed by entity. Limitation: needs entity
extraction to work on the open question (it often does) and the answer to share
the entity (it often doesn't — referents again).

**Mechanism F — LLM candidate proposer (Greg's theme-sweep move).**
Deterministic engine slices/samples user turns; an LLM proposes decision
candidates by MEANING. Highest recall, catches the semantic surface no
deterministic channel covers (decisions phrased as statements of fact,
architectural assertions with no signal phrase). Two sub-options: (F1) LLM in
the engine — breaks zero-key, but Greg's lore roadmap already accepts this for
semantic intent clustering; (F2) LLM in the agent pass — the agent reads
engine-sampled user turns and proposes candidates, then the engine snowballs
verified candidates back to deterministic measurement. F2 preserves zero-key.
Cost: LLM calls. Limitation: needs adversarial verification (Greg's ≥4-real-
members-or-it-dies) to avoid pattern-matched wishfulness.

**Mechanism G — File-target semantics (decision recordings).**
Edits to files matching `docs/design/`, `ADR-*`, `DECISIONS.md`, `*.plan.md`,
`docs/implementation/*` are decision recordings — the agent wrote the decision
down. The beat that touched such a file is a candidate decision, and the file
content (citable) is the evidence. Deterministic. Catches: "create a full
implementation plan for this in @docs/implementation" (from the intent
corpus). Cost: a file-path classifier. Limitation: only catches decisions that
 got written to a design doc, which is a minority.

### Synthesis — the shape of the fix (hypothesis, not a decision yet)

The recall fix is probably NOT "widen the grammars" (validated: `placeholder`
was already tried and backed out; widening catches more instruction-verb false
positives without catching the semantic misses). The fix is probably **add
deterministic channels that read the other signals already in the beat**:
- B (commit messages) — highest signal-to-noise, near-zero cost, already parsed.
  Start here.
- A (file actions) — orthogonal surface, already in `files` field, catches
  architectural decisions visible in creates/deletes/renames.
- C (corrected outcomes) — already computed, catches reversals the grammar
  misses.
- E (question→answer pairing) — cheap join, catches resolved decisions where
  the answer has no decide phrase.
- F2 (LLM proposer in the agent pass, snowballed back) — the fallback for the
  semantic residue no deterministic channel catches. Last, after measuring how
  much residue remains after A+B+C+E.

The key insight for the blog: **the deterministic layer's recall can be grown by
adding more DETERMINISTIC channels, not by making the one channel less
deterministic.** A, B, C, E are all deterministic. So you can raise recall
substantially WITHOUT breaking zero-key / cacheability / re-runnability. The
LLM channel (F) is only needed for the residue. This refines the "DoD ladder"
metaphor from the Greg research: each new deterministic channel is a new rung
that's checkable, and you only invoke the qualitative layer for what's left
after the deterministic rungs. The ladder is wider than I thought — it's not one
rung per layer, it's several deterministic rungs side by side, then the
qualitative rung on top of whatever they miss.

**Next step: measure.** Index a real corpus, run A+B+C+E as candidate
generators alongside the existing grammars, hand-count the false-negative rate
on the same dense session. If the miss rate drops from ~67% to something
defensible (<20%?), the lexical-only era is over and the question becomes
architecture (how to combine channels). If it doesn't, F (LLM proposer) moves
from "fallback" to "necessary."

### 2026-07-13 — the signal grammars, enumerated (the recall ceiling, made concrete)

The decision-detection grammars live in `decisions/scripts/lib/decide.mjs` (NOT
`patterns.mjs` — that's the transcript *format* grammar inherited from lore).
Four grammars, ordered by precedence CHANGE > OPEN > DECIDE > PROVISIONAL.

**CHANGE_RE** (reversal / supersession — highest precedence; a change is itself a
decision AND marks predecessors):
`actually, let's|we|use|go|switch|make` · `instead of what` · `changed? my|our mind`
· `scrap that` · `forget that` · `on second thought` · `let's not` · `go back to`
· `switch(ing)…(back )?to` (≤3 words between) · `revert to` · `no longer use|using`
· `replace…with`

**OPEN_RE** (unresolved question — a `?` containing any of):
`should we|i|it|this…?` · `which…?` · `do we want…?` · `what about…?` · `or should…?`

**DECIDE_RE** (firm decision):
`let's go with|use|stick with|keep|standardize on|make|call|do` · `go(ing) with`
· `we'll|we will use|go|keep|stick` · `decided? on|to|that` · `decision:` · `settled? on`
· `agreed? on|to` · `standardize on` · `default to` · `always use` · `never use`
· `don't use…use` (≤4 words between) · `rename…to` (≤4 words) · `call it <X>`
· `option|choice <digit>` · `the first|second|third option` · `use…instead of|rather than`

**PROVISIONAL_RE** (temporary / deferred):
`for now` · `for the moment` · `temporary|temporarily` · `as a stopgap` · `tbd`
· `decide (this )?later` · `punt(ing) on` · `revisit (this )?later`

**Deliberate exclusion (a data point for the false-positive-climb worry):** the code
comments out `placeholder` — it's a common UI noun ("placeholder text"), not a
provisional-decision marker, and including it flooded the report with false
positives. They already tried widening and backed it out. Direct validation of my
instinct that widening the grammars is the wrong move.

**Entity extraction** (what clusters into per-entity timelines): SYMBOL_RE
(camelCase / snake_case / ALL_CAPS) · FILE_RE (file-like tokens) · TICK_RE
(backtick spans 2-60 chars) · NOISE_ENTITY denylist (env.local, package.json,
readme.md, claude.md) · ubiquity cap ENTITY_MAX=5 (an entity in >5 sessions is
treated as a product name and not used as a clustering key, so "SpecStory"
doesn't chain unrelated decisions).

**The recall ceiling, now enumerable.** The engine fires only on user turns
containing one of ~40 specific phrases. It catches decisions that are *explicitly
framed as decisions in English* ("let's go with", "decided on", "should we?").
It misses decisions framed as:
- **statements of fact** — "Postgres is overkill here, let's just use SQLite"
  ("let's just use" isn't in DECIDE_RE; only "let's use" / "let's go with")
- **answers to earlier questions** — "yeah, tabs not a sidebar" (no `should we?`,
  no decide phrase)
- **architectural assertions** — "the EventStore sits behind the auth layer"
  (no signal phrase)
- **implicit choices visible only in the agent's subsequent actions** — the user
  approved a plan that uses SQLite without ever saying "let's use SQLite"

This is Greg's 95%-conversation-beats finding in miniature: decisions are a
*semantic* phenomenon, and a lexical grammar is a ceiling on them. Next step:
get a real number, not just an intuition — index a real corpus, hand-read one
transcript, count engine catches vs. real decisions.

### 2026-07-13 — the recall experiment: a real number, and it's worse than expected

**Methodology.** Indexed 4315 real sessions across 27 of my own projects
(`node scripts/decisions.mjs index --projects ~/dev`) into a scratch DB. Engine
found 1614 "decisions." Then hand-read the single densest transcript the engine
flagged — `intent/.specstory/history/2025-11-01_17-47-30Z-ok-i-want-you.md`
(1.1MB, 37k lines, 125 unique user turns, a long event-bus-architecture
implementation session) — and counted real decisions by hand vs. what the engine
caught. N=1 session, hand-counted, rough — but it's the engine's *best* session
(most candidates found), so it's a fair test, not a strawman.

**First red flag.** This repo's own 4 real transcripts (the `getspecstory`
sessions) returned **0 decisions**. Two are mechanical lint-fixing (fair), but
one is a 240KB cloud-architecture session whose single substantive user turn is
"let's design a change so there is no HEAD request before a cloud sync during
`specstory run`" — a real design decision, missed because "let's design" is not
in DECIDE_RE (only "let's go with|use|stick with|keep|standardize on|make|call|do").
Confirmed miss on the first transcript I checked.

**The dense session: hand-counted real decisions vs. engine catches.**

Real decisions a human would count in the session (non-exhaustive, ord = user-turn index):
- ord=34 "lets reduce the window" — decision to reduce the polling window. **MISS** ("lets reduce" not a signal)
- ord=39 "OK we can do all of that except we don't have to worry about the tray app" — scope decision (exclude tray app). **MISS** (no signal phrase)
- ord=47 "lets move forward with phase 6... remove all of the legacy polling paths" — architectural decision (remove polling). **MISS** ("lets move forward" not a signal)
- ord=56 "ah should we move the FTS loop and replace it with an outbox subscriber?" — **CAUGHT** (but misclassified as `decided`/change because "replace...with" fires CHANGE_RE, when it's actually an open question — "should we...?")
- ord=60 "OK awesome, lets implement that plus also make the FTS indexer an outbox topic" — decision to implement. **MISS** ("lets implement" not a signal)
- ord=81 "great. lets make that fix for tmp files" — **CAUGHT** ("lets make") but borderline — it's an instruction to do work, not a choice between alternatives
- ord=98 "OK lets implement that" (streaming/chunking approach) — decision to adopt streaming. **MISS** ("lets implement")
- ord=104 "should we consider perhaps not storing most code as automerge.text and rather use something that stores lines?" — **CAUGHT** as open (genuine open design question)
- ord=120 "make it 15s per 16kb instead of ~128 KB of payload" — real decision (change timeout formula from payload-based to fixed-per-chunk). **MISS** ("make it" not a signal)

**Engine's 12 catches in this session, hand-judged:**
- True positives (real decisions or genuine open design questions): ~3 (ord 56, 104, and arguably ord 30 "Should it really be 15 seconds?")
- False positives (instructions misread as decisions, via "let's do/keep/make"): ~5 (ord 3 "OK lets keep moving", ord 12 "Lets do that as well so we can commit", ord 13 "Lets do that update our doc and write a commit header", ord 25 "OK lets make sure we implement the manual attribution", ord 81 "lets make that fix for tmp files")
- Operational questions caught as `open` but not design decisions: ~4 ("what about looking up automerge 3?", "what about the first time there is a save?", "OK in that test what about this?", "now what should I be able to functionally do...")

**The numbers (rough, N=1, the engine's best session):**
- Recall: ~3 of ~9 real decisions caught ≈ **33%** (and 2 of those 3 are open questions, not firm decisions)
- Precision: ~3 of 12 catches are true positives ≈ **25%**
- The engine is simultaneously **missing most real decisions** AND **flagging mostly non-decisions**.

**The qualitative finding (more robust than the exact ratio, and the real point):**
The grammars fire on the WRONG verbs. `let's do|keep|make` are instruction verbs
("lets keep moving", "lets do that as well so we can commit") — they pattern-match
as decisions but are just "continue / do the work" directions. Meanwhile the
verbs that actually carry decisions — `lets reduce`, `lets implement`, `make it
<spec>`, `remove`, `we don't have to worry about` — are absent from the grammars.
The engine's lexical surface is correlated with decision language but not
causally tied to decision *content*. This is the recall ceiling made measurable,
and it confirms the intuition: a lexical grammar on decisions has both a recall
floor AND a precision ceiling that move together — you can't fix one by widening
the other.

**What this means for the design (the live question, now with evidence).**
The two-pass split assumes the engine is high-RECALL and the agent is
high-PRECISION. The measurement says the engine is neither — it's ~25% precision
AND ~33% recall on its best session. So the agent pass is currently doing both
jobs (filtering the 75% false positives AND supplying the 67% missed decisions
from its own reading), which means the engine isn't earning its keep as the
"high-recall" layer. This is the exact situation Greg hit with lore's 95%
conversation beats, and his answer (the theme sweep: deterministic sample → LLM
propose by meaning → adversarial verify → snowball back) is now the obvious
shape to try for decisions. The measurement is the falsification condition I
needed: **the lexical-only engine is not high-recall, by ~2/3, on its best
case.** Keeping it as the recall layer is not defensible; the question is whether
to add a semantic channel alongside it (Greg's move) or restructure what the
engine is for (e.g. engine does indexing + citation only, agent does all
detection).

**Fodder for the blog, sharpened again.** The essay says "invest in the DoD like
your ability to walk away depends on it — because it does." This experiment is
the converse warning: **a deterministic DoD that passes its own tests (9/9 green)
can still be measuring the wrong surface.** The 9 tests pass on fixtures written
to match the grammars — a closed loop — while the engine catches 33% of real
decisions and flags 75% non-decisions on a real transcript. The tests are a
correctness DoD for the grammar machinery, not a recall DoD for the skill. The
ladder metaphor from the Greg research holds: the grammar layer is a real rung
(checkable, cacheable, deterministic) but it is NOT the "done" rung, and the
9-green-tests made it easy to pretend it was. The measurement breaks the
pretense. **You cannot know your DoD is real until you've measured it against the
qualitative thing it's supposed to be capturing — and that measurement step is
itself not something a deterministic loop can do for you.** It's the human's job,
at the DoD boundary, exactly as the essay argues.

### 2026-07-13 — Greg's intent behind the two-pass split (lineage research)

I borrowed the deterministic-engine + agent-pass split from Greg's `lore` skill.
Wanted to understand his *intent* before I keep building on it — especially given
my recall-ceiling worry. Read `lore/README.md`, `lore/HOW-IT-WORKS.md`,
`lore/AS-BUILT-ARCHITECTURE.md` (Design Provenance & Decisions section),
`lore/CHANGELOG.md`, and `lore/SKILL.md` (the three LAWs + named failure modes).
Git history in this repo is thin — the engine was built in a private standalone
repo (`specstoryai/lore`) and squash-imported here as `4e38e7b` (v3.8.2,
2026-06-11), so the design intent lives in the docs and changelog, not commit
messages.

**Greg's architecture rule, stated as a law:** *the deterministic engine finds,
samples, counts, and caches; the agent reads, names, judges, and writes. The
engine cannot hallucinate; the agent never reads a 400k-line transcript.*

**Why deterministic first — three reasons, in priority order:**
1. **Hallucination containment.** The engine is the trust boundary. Anything
   that touches raw transcript bytes and could be wrong *must* be deterministic,
   because a hallucinated fact that's also the thing the agent judges is poison
   the agent can't catch.
2. **Context-budget firewall.** The engine does lossy compression (parse →
   beats → clusters → exported spans) so the agent's context window is spent on
   judgement, not scrolling. Without the engine the problem doesn't fit in a
   model.
3. **Determinism = re-runnable = cacheable.** Engine outputs are byte-identical,
   which is what makes the dossier cache, the fingerprint ladder, idempotent
   re-index, and forged-skill drift detection all work. Re-runnable work goes in
   the engine; run-once-and-cache work goes in the agent.

**Why agentic second — what the engine cannot do:** name, judge, verify
adversarially, write prose. Phase C runs *two* subagents (miner + skeptic) — a
deterministic verifier would just re-check the same facts; refutation needs a
*different reading*.

**The part most relevant to my live question: Greg already hit my recall problem
and added a second channel to fix it.** The engine's first three channels are
all lexical (command n-grams, verb:keyword intents, fixed meta-detectors) —
exactly the "specific strings of words" surface I'm worried about in decisions.
Greg hit the same wall in the same place: decisions, reviews, corrections.
`HOW-IT-WORKS.md`: "In some corpora 95%+ of beats are pure conversation —
reviews, decisions, corrections — and form no command cluster at all."

His answer was NOT to widen the regexes (validating my instinct that widening →
false-positive climb is the wrong move). His answer was a **second, structurally
different channel: the theme sweep** (changelog 3.2.0, 2026-06-10):
- Deterministic engine only *slices and samples* (`beats --shape conversation`,
  stratified) — picks which beats to look at, not what's in them.
- Six thematic LLM lenses (decision-craft, review-judgment, model-direction,
  verification-discipline, diagnosis-style, regenerate-vs-patch) propose
  clusters by *meaning*, not surface form.
- Every proposed theme runs through an **adversarial verifier** ("one coherent
  practice, or pattern-matched wishfulness? ≥4 real members or it dies").
- Verified themes get **snowballed back into the engine**: `theme expand`
  deterministically finds every corpus occurrence using lift-scored vocabulary,
  so the LLM's semantic discovery becomes a deterministic measurement.

So the full lore design is NOT "deterministic engine + agent precision pass."
It's **deterministic engine for the lexical surface + LLM lenses for the
semantic surface + adversarial verification + deterministic snowball back to
measurement.** The split is *layered*, and the meaning layer is itself a
sample → propose → refute → expand sandwich. And the roadmap explicitly lists
local embeddings as the next move "if lexical signatures prove too coarse" —
the lexical engine is acknowledged as a ceiling, not a feature.

**The failure modes that shaped the agent contract (empirical, not a priori):**
three named failure modes in `lore/SKILL.md`, all real runs:
- #1: agent deep-mined 4 skills, asked the user to pick with no dossier shown →
  LAW 1: dossiers before candidate questions, proven by a sentinel.
- #2 (same day, fresh session): agent narrated correctly but still dropped the
  evidence → LAW 2: render the engine's visuals verbatim, in a real message.
- #3 (teammate's machine, plan-mode): agent showed a plan but summarized the
  dossier cards instead of embedding them → hook-enforced: a PreToolUse hook on
  ExitPlanMode denies any plan that isn't the engine-rendered artifact.

The pattern: **the agent keeps finding ways to summarize away the engine's
evidence**, and the design keeps responding by forcing the engine's output
through verbatim. The split isn't just capability allocation — it's preventing
the agent from replacing evidence with narration. Hard-won from failures.

**Named lineage:** (1) the "last30days split" — deterministic engine + agent
synthesis, credited to a surveyed ~20k-line Python skill; Lore improved on it by
making the engine zero-key (no LLM in the engine, the harness already has a
model). (2) "25-patterns lineage" — mine → synthesize → adversarially-verify →
critique, from the 3-stage Workflow behind *25 Patterns in Agentic Engineering*.

### What this means for decisions-skill (the live question, updated)

My intuition that the grammar-based engine is missing most real decisions is
*the same finding Greg had*, and he already prototyped the answer. The honest
path for decisions is probably not "widen the grammars" and not "throw out the
split" — it's the layered move: keep the deterministic engine for the lexical
surface (decisions phrased with explicit signal words), add a semantic channel
for the rest (sample → LLM proposes decision candidates by meaning → adversarial
verify → snowball back to deterministic measurement). The decisions engine's
9 tests prove self-consistency on grammar-matched fixtures; they do NOT prove
recall, and Greg's 95%-conversation-beats finding is direct evidence that the
recall ceiling on a lexical-only engine is low for exactly the content type
decisions cares about.

**Fodder for the blog, sharpened:** the essay says "if you can write down what
done looks like in a form a machine can verify, you can step away." The lore
lineage shows the *process* by which you earn that: you start with a lexical
deterministic layer (cheap, checkable, cacheable), discover its recall ceiling
empirically, add a verified semantic layer for what it misses, and snowball that
back into the deterministic layer so the measurement reverts to checkable. The
DoD doesn't have to exist up-front — it can be *earned layer by layer*, and each
layer's contribution to recall is itself measurable. That's a nuance the essay's
binary "deterministic vs qualitative" framing doesn't capture, and decisions-
skill is the project where I can show it concretely. **The DoD is not a single
artifact; it's a ladder, and each rung unlocks a different mode on the essay's
spectrum.**

