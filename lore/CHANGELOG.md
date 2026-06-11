# Changelog

## 3.8.2 - 2026-06-11

- **Moved into the getspecstory monorepo** as the top-level `lore/` directory (and with it,
  public). Installs change accordingly: `npx skills add specstoryai/getspecstory`,
  `/plugin marketplace add specstoryai/getspecstory`, dev symlinks point at
  `<clone>/lore`. Release tags are now namespaced `lore/vX.Y.Z`; releases are never marked
  "latest" (that pointer belongs to the CLI installer). The standalone `specstoryai/lore` repo
  is archived with a pointer.

## 3.8.1 - 2026-06-11

- **No-wrap inventory layout**: every `skills` line stays under ~100 columns (a wrapped line
  orphans the tree gutter). Names align into a padded column, the harness list compresses
  ("everywhere" / "agents+claude"), descriptions clip to one line, and the forged rows fold the
  install locations into the status line.

## 3.8.0 - 2026-06-11

- **The skills inventory** answers "what skills do I have and what does each do?" - `skills`
  scans every harness skills dir (agents, claude, codex, opencode, gemini), reads each SKILL.md's
  frontmatter description, dedupes through the symlink fan-out, and renders a pass-through view:
  Lore-forged skills with registry health (drift recommendation, sessions of evidence, outcome
  counts, hand-edit detection) plus every other installed skill, with broken symlinks and
  registered-but-not-installed orphans flagged. `/lore what skills do I have?` maps to it.

## 3.7.0 - 2026-06-10

- **Distribution structure** (the last30days-skill pattern): `.claude-plugin/plugin.json` +
  `marketplace.json` make the repo installable as a Claude Code plugin and its own marketplace
  (`/plugin marketplace add specstoryai/lore` then `/plugin install lore@specstory`); root
  SKILL.md stays - it is the first-class single-skill layout for both Claude Code (≥ v2.1.142)
  and `npx skills` (skills.sh). CI via GitHub Actions: `validate.yml` runs the suite on every
  push/PR; `release.yml` runs on `v*` tags, checks tag/version agreement, and publishes a
  `lore.skill` artifact (claude.ai-upload-ready zip, fixtures/tests stripped via
  `.gitattributes export-ignore`) to a GitHub Release.

- **Readable forge plan.** The card layout is redesigned for humans: one idea per line, real
  bullet lists instead of dot-joined runs, numbered candidates (`### 1 · name`), the trigger as a
  "Use when" blockquote, failure modes as what/recovery pairs, and `---` rules between cards.
  Mined content is untouched; only the engine's layout changed.
- **Canceled forges are recallable.** Every `plan render` persists its manifest; `plan last`
  re-renders the most recent one against the CURRENT corpus (grown themes and fresh fingerprints
  show), `plan list` shows the history. SKILL.md maps "show me the last plan / pick up where we
  left off" to it - an interrupted curation never costs a re-mine.
- **Fixtures are now the spec of BOTH channels, with real-run conformance:**
  - `projC`: a semantic-channel project paraphrasing practices mined from real runs
    (locate-before-touching, falsify-with-discriminating-observation) - conversation/read-only/
    write shapes, all four outcomes, every lens regex, and a planted practice with a near-miss
    (the snowball should-flag/should-pass pair).
  - provider-header fixtures for Gemini CLI, Factory Droid CLI, DeepSeek TUI, and Antigravity -
    the README's provider claims are now executable.
  - `fixtures/golden/forge-plan.md`: the rendered plan is byte-stable; regenerate intentionally
    with `UPDATE_GOLDEN=1 npm test` and review the diff.

## 3.6.0 - 2026-06-10

- **Snowball expansion: themes grow from anecdote to measurement.** A verified theme cites the
  4-8 beats a miner happened to read; the practice usually occurs in far more. `theme expand`
  extracts lift-scored discriminating vocabulary from member intents and scans the WHOLE corpus
  for scored candidates deterministically (24,829 beats in ~0.2s - no transcript reading); the
  agent verifies the shortlist via `beats --keys`; `theme grow` records confirmed members and
  refingerprints. LLM reads stay at the margins, which is what scales to huge corpora.
- **Outcome lift on theme cards**: with ≥3 judged members, `theme render` (and the forge plan)
  shows the practice's success rate vs the corpus baseline - "71% ✓ over 41 judged beats ·
  baseline 54% (+17 pts)". Curation can now lead with "this practice WORKS", not just "you do
  this".

## 3.5.0 - 2026-06-10

- **The forge plan is now an engine artifact, hook-enforced** (response to failure mode #3, where
  a plan-mode curation on another machine summarized the dossier evidence away):
  - `plan render --file <manifest>` assembles the ENTIRE curation document - badge, every dossier
    and theme card verbatim, skip list, on-approval contract, LAW 1 sentinel as the last line. The
    agent supplies only judgments (a JSON manifest of proposed/skipped); it never composes the plan.
  - a `PreToolUse` hook on `ExitPlanMode` (wired in SKILL.md frontmatter via `${CLAUDE_SKILL_DIR}`,
    Claude Code only) DENIES any plan that is not this artifact, with an actionable reason. LAW 1
    is enforcement now, not instruction.
- **Themes are first-class candidates end to end**: `beats --theme` joins selectBeats (the
  `theme:` dossier-key prefix is tolerated), the deep-mine workflow accepts `kind: "theme"`
  clusters, `plan render` embeds theme cards, and SKILL.md promotes the theme sweep from
  conditional to a standard phase of every full-pipeline run with a mandatory mixed curation
  slate - a conversation-heavy corpus can no longer yield a command-only proposal.

## 3.4.1 - 2026-06-10

- **forged registry hardening** (bugs surfaced by the first real end-to-end forge run):
  - cluster kind is now INFERRED from the key's shape (` × ` corr, ` ▸ ` gram, saved theme id,
    META id, else sig); the old `--kind` default of `corr` crashed `forged add`/`decline` on
    gram-shaped clusters.
  - **theme** is a first-class registry kind: theme candidates can be forged, declined, and
    drift-checked by theme id (stats resolve through the theme's member beats).
  - a stats lookup failure can no longer lose a registry write: `clusterState` degrades to empty
    stats with a stderr warning instead of throwing after partial output.
- README gains private-repo install instructions (clone + symlink fan-out) and the Codex
  invocation note (`$lore`, not `/lore`).

## 3.4.0 - 2026-06-10

- **Self-reporting**: the skill now accounts for what it has done. `status` renders the
  what-has-Lore-done view (corpus snapshot, mined themes/dossiers, forged-registry health with
  needs-attention items, recent activity); a `runs` journal records every notable engine invocation
  automatically plus the agent's end-of-run summary (`runs add`, a Step 6 duty); `theme render`
  turns saved themes into human-readable cards with freshness checks (member-beat fingerprints).
  All three are PASS-THROUGH artifacts under LAW 2.

## 3.3.0 - 2026-06-10

- **The unit is now the BEAT** (renamed from "episode", which collided with Stoa's branch
  vocabulary and undersold the construct). A beat is screenwriting's exact term for an
  action/reaction unit where the story state changes: the agent acts, the user's next prompt
  reacts and judges it. Tables/columns migrated in place (themes and dossiers survive);
  `episodes` remains a CLI alias for `beats`.
- Em dashes removed from all docs and emitted strings; the LAW 1 sentinel is now
  `=== dossiers above: N ===` (a deliberate machine-checkable form).
- Simpler /lore argument hint: Enter = guided setup, or plain English.

## 3.2.0 - 2026-06-10

- **Phase B′ - semantic theme mining (latent expertise)**: beat sampling lenses
  (`--shape conversation|read-only|shell|write`, `--intent-re`, `--project`, `--min-intent-len`);
  stable beat keys (`session_id#ord`) that survive re-indexing; `themes` table +
  `theme put/list/get`; `theme-sweep.workflow.js` with six thematic lenses + adversarial
  verification. Themes flow into deep-mine/dossier/forge unchanged. First harvest (specstory-monorepo,
  Dec 2024 sessions): sibling-as-spec review, reject-symptom-patch-demand-mechanism,
  thin-layer-responsibility-audit.
- **Corpus-audited parser (1,311 real sessions)**: multi-line `- command:` bullets recovered
  (2,313 blocks), legacy codex `Tool use: **shell**` with `Output:` markers recovered (6,787 lines),
  `TaskOutput`/`KillShell`/`BashOutput` excluded; empty shell extractions 3,139 → 1,025 (708 of those
  by design). `patterns.mjs` rewritten as an explicit format grammar - every regex preceded by the
  verbatim bytes it matches.
- **Hardening**: per-session transaction writes (atomic + much faster), `busy_timeout` for concurrent
  runs, session UUID stored with content-duplicate detection in `prune`, empty-corpus report guard,
  shell control-flow keywords dropped from command heads. PARSER_VERSION 6.

## 3.1.0 - 2026-06-09

- **Phase C deep-mine, built and validated**: `beats` export (exact spans per cluster,
  corrected-first, content fingerprint), per-cluster miner + adversarial verifier workflow
  (Claude Code fan-out; other harnesses use their own subagents), `dossier get/put/render` cache -
  deep-mining is once-per-cluster until the evidence changes. Falls back to top runbook clusters
  when corroboration is sparse.
- **Forged-skill registry**: provenance at forge time (cluster, fingerprint, outcome stats, content
  hash), declines recorded; `forged check` recommends update / update-carefully (hand-edit detected)
  / suppress / re-engage / orphaned. Re-runs converge to updates, never duplicates.
- **Authorship (shared repos)**: every session attributed via git add-author → home-dir sniff →
  machine user. Candidates show `👥 N authors`; multi-author = team practice (project scope);
  a teammate's workflow is never presented as the user's.
- **Visual feedback**: live indexing progress on stderr; canonical `📜 Lore mined!` pass-through
  footer; SKILL.md OUTPUT CONTRACT LAWs with named failure modes and the dossier sentinel.
- **Plan-style curation** (Claude Code): the forge plan IS the dossier display - approving the plan
  approves the forge set; dossier display is structurally unskippable.
- **Guided start**: bare `/lore` walks through scope / window / goal; friendly argument-hint.
- **Discovery & lifecycle**: `--scan <root>` finds histories at any depth (monorepos);
  legacy (~2025) `Tool use:` transcript format support; absolute-path storage; `reset` subcommand;
  `prune`; parser-versioned auto re-parse; engine refactored into `scripts/lib/` modules.
- 17 tests; fixtures cover every provider form factor, modern and legacy.

## 3.0.0 - 2026-06-09

- Renamed `skill-forge` → **Lore** (SpecStory Lore). Corpus moved to `~/.specstory/lore.db`.
- **Multi-agent**: generic session-header matching + shell detection via the shared
  `data-tool-type="shell"` envelope attribute - reads transcripts from every specstory provider
  (Claude Code, Codex, Cursor, Gemini, Factory Droid, DeepSeek, Antigravity). Sessions tagged per agent.
- **Harness-portable SKILL.md** (agentskills.io): tool references generalized; forge step writes once
  to `~/.agents/skills/<name>` and symlinks into all detected harness skills dirs.
- Repo created; skill dirs now symlink to this clone (live-clone freshness).

## 2.x - 2026-06-08 (as skill-forge)

- **v3 engine**: beat segmentation (intent → method → outcome from next-turn reaction), persistent
  SQLite corpus (`node:sqlite`, zero deps), `index`/`report` subcommands, CORROBORATED intent×procedure
  section with outcome rates, outcome-aware scoring.
- **v2 engine**: parsed the verified specstory-cli output formula (`<tool-use>` envelopes; single-line
  inline-backtick + in-summary + ```bash fence + legacy bullet command forms); cross-project mode via
  `git_id`; `--kind`/`--filter` flags.
- **v1 engine**: command n-grams over ```bash fences + task signatures + meta detectors (superseded).
