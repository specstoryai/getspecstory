# Plan As Built

**Generated:** 2026-06-22T23:50:24.632171+00:00
**Plan ID:** `4808dceb3f844dcf8d3edba788a82960`
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
**Status:** completed
**Mode:** review
**Result run:** `29334e4460584e6ea5f212b78198ac55`
**Doc-writer:** plan-docs deterministic

## System Overview

This document consolidates the merged plan result from child summaries, child docs, and the final result inventory.

## Result Inventory

- `.claude-plugin/marketplace.json`
- `.cursorindexingignore`
- `.github/ISSUE_TEMPLATE/cloud-bug-feature.md`
- `.github/ISSUE_TEMPLATE/extension_report.md`
- `.github/ISSUE_TEMPLATE/lore_bug_report.yml`
- `.github/ISSUE_TEMPLATE/lore_feature_request.yml`
- `.github/copilot-instructions.md`
- `.github/dependabot.yml`
- `.github/workflows/ci.yml`
- `.github/workflows/codeql.yml`
- `.github/workflows/lore-release.yml`
- `.github/workflows/lore-validate.yml`
- `.github/workflows/release.yml`
- `.gitignore`
- `.goreleaser.yml`
- `CODE-OF-CONDUCT.md`
- `CONTRIBUTING.md`
- `LICENSE.txt`
- `README.md`
- `deadreckon-plan-manifest.json`
- `install.sh`
- `lore/.claude-plugin/plugin.json`
- `lore/.gitattributes`
- `lore/.gitignore`
- `lore/AGENTS.md`
- `lore/AS-BUILT-ARCHITECTURE.md`
- `lore/CHANGELOG.md`
- `lore/CLAUDE.md`
- `lore/CONTRIBUTING.md`
- `lore/HOW-IT-WORKS.md`
- `lore/LICENSE`
- `lore/README.md`
- `lore/SKILL.md`
- `lore/fixtures/.workthreads/threads-bar/.specstory/history/2026-06-06_09-00-00Z-scaffold-search-index.md`
- `lore/fixtures/.workthreads/threads-bar/.specstory/history/2026-06-20_11-00-00Z-fix-search-index.md`
- `lore/fixtures/.workthreads/threads-bar/.specstory/history/2026-06-21_15-00-00Z-start-notif-badge.md`
- `lore/fixtures/.workthreads/threads-foo/.specstory/history/2026-06-02_09-00-00Z-begin-checkout-flow.md`
- `lore/fixtures/.workthreads/threads-foo/.specstory/history/2026-06-09_10-00-00Z-finish-checkout-flow.md`
- `lore/fixtures/.workthreads/threads-foo/.specstory/history/2026-06-12_14-00-00Z-payment-retry-rollback.md`
- `lore/fixtures/golden/forge-plan.md`
- `lore/fixtures/projA/.specstory/.project.json`
- `lore/fixtures/projA/.specstory/history/2025-07-10_09-00-00Z-fix-the-gitignore.md`
- `lore/fixtures/projA/.specstory/history/2026-05-01_10-00-00Z-can-we-run-a.md`
- `lore/fixtures/projA/.specstory/history/2026-05-02_11-00-00Z-can-we-run-a.md`
- `lore/fixtures/projA/.specstory/history/2026-05-03_12-00-00Z-can-we-run-a.md`
- `lore/fixtures/projA/.specstory/history/2026-05-04_13-00-00Z-check-the-vet.md`
- `lore/fixtures/projA/tools/subpkg/.specstory/history/2026-05-07_10-00-00Z-tune-the-subpkg.md`
- `lore/fixtures/projB/.specstory/.project.json`
- `lore/fixtures/projB/.specstory/history/2026-05-05_14-00-00Z-can-we-run-a.md`
- `lore/fixtures/projC/.specstory/.project.json`
- `lore/fixtures/projC/.specstory/history/2026-05-10_09-00-00Z-where-is-save-implemented.md`
- `lore/fixtures/projC/.specstory/history/2026-05-11_10-00-00Z-where-is-token-validation.md`
- `lore/fixtures/provider-antigravity/.specstory/history/2026-05-23_09-00-00Z-run-the-linter.md`
- `lore/fixtures/provider-deepseek/.specstory/history/2026-05-22_09-00-00Z-count-source-lines.md`
- `lore/fixtures/provider-droid/.specstory/history/2026-05-21_09-00-00Z-show-git-status.md`
- `lore/fixtures/provider-gemini/.specstory/history/2026-05-20_09-00-00Z-list-the-workspace.md`
- `lore/package.json`
- `lore/scripts/deep-mine.workflow.js`
- `lore/scripts/hooks/validate-plan.mjs`
- `lore/scripts/lib/beats.mjs`
- `lore/scripts/lib/db.mjs`
- `lore/scripts/lib/discover.mjs`
- `lore/scripts/lib/forged.mjs`
- `lore/scripts/lib/indexer.mjs`
- `lore/scripts/lib/parse.mjs`
- `lore/scripts/lib/patterns.mjs`
- `lore/scripts/lib/report.mjs`
- `lore/scripts/lib/threads.mjs`
- `lore/scripts/mine-skills.mjs`
- `lore/scripts/theme-sweep.workflow.js`
- `lore/tests/engine.test.mjs`
- `lore/tests/threads.test.mjs`
- `lore/workthreads/SKILL.md`
- `manifest.json`
- `scripts/update-homebrew-formula.sh`
- `specstory-cli/.claude/commands/code-review.md`
- `specstory-cli/.claude/commands/pr-review.md`
- `specstory-cli/.cursorindexingignore`
- `specstory-cli/.gitignore`
- `specstory-cli/.golangci.yml`
- `specstory-cli/.specstory/.gitignore`
- `specstory-cli/.specstory/history/2026-01-12_15-08-38Z-update-our-docs-to.md`
- `specstory-cli/.specstory/history/2026-01-13_16-29-49Z-pkg-providers-claudecode-13.md`
- `specstory-cli/.specstory/history/2026-01-16_15-23-32Z-take-a-look-at.md`
- `specstory-cli/.specstory/history/2026-01-16_16-26-02Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-01-16_16-26-02Z.md`
- `specstory-cli/.specstory/history/2026-01-16_16-46-41Z-command-name-exit-command.md`
- `specstory-cli/.specstory/history/2026-01-16_16-46-41Z.md`
- `specstory-cli/.specstory/history/2026-01-16_22-28-53Z-after-a-cloud-sync.md`
- `specstory-cli/.specstory/history/2026-01-23_14-20-54Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-23_14-20-54Z.md`
- `specstory-cli/.specstory/history/2026-01-23_20-42-06Z-command-name-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-25_12-30-58Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-25_12-30-58Z.md`
- `specstory-cli/.specstory/history/2026-01-25_14-58-39Z-01-25-26-9.md`
- `specstory-cli/.specstory/history/2026-01-25_21-28-55Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-25_22-47-22Z-the-coordinated-universtal-timezone.md`
- `specstory-cli/.specstory/history/2026-01-25_23-00-06Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-28_13-10-29Z-take-a-look-at.md`
- `specstory-cli/.specstory/history/2026-01-28_13-36-04Z-command-name-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-28_13-36-04Z.md`
- `specstory-cli/.specstory/history/2026-01-28_13-42-57Z-this-isn-t-good.md`
- `specstory-cli/.specstory/history/2026-01-28_14-55-51Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-28_14-55-51Z.md`
- `specstory-cli/.specstory/history/2026-01-29_14-35-22Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-29_14-35-22Z.md`
- `specstory-cli/.specstory/history/2026-01-29_15-58-30Z-the-droid-provider-uses.md`
- `specstory-cli/.specstory/history/2026-01-29_16-21-35Z-explain-this-commit-to.md`
- `specstory-cli/.specstory/history/2026-01-29_16-26-43Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-29_16-26-43Z.md`
- `specstory-cli/.specstory/history/2026-01-29_17-39-47Z-the-droid-factory-provider.md`
- `specstory-cli/.specstory/history/2026-01-30_18-58-14Z-command-name-pr-review.md`
- `specstory-cli/.specstory/history/2026-01-30_18-58-14Z.md`
- `specstory-cli/.specstory/history/2026-01-31_14-47-58Z-take-a-look-at.md`
- `specstory-cli/.specstory/history/2026-01-31_15-19-39Z-let-s-consider-what.md`
- `specstory-cli/.specstory/history/2026-01-31_15-39-29Z-when-running-specstory-sync.md`
- `specstory-cli/.specstory/history/2026-02-07_14-02-42Z-review-the-entire-repo.md`
- `specstory-cli/.specstory/history/2026-02-08_22-24-43Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-08_22-42-56Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-09_20-36-38Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-09_20-36-38Z.md`
- `specstory-cli/.specstory/history/2026-02-09_22-24-05Z-the-way-the-list.md`
- `specstory-cli/.specstory/history/2026-02-09_22-53-07Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-02-09_22-53-07Z.md`
- `specstory-cli/.specstory/history/2026-02-10_14-21-48Z-ok-read-at-docs.md`
- `specstory-cli/.specstory/history/2026-02-10_21-49-42Z-command-message-insights-command.md`
- `specstory-cli/.specstory/history/2026-02-10_21-49-42Z.md`
- `specstory-cli/.specstory/history/2026-02-10_22-12-29Z-command-name-copy-command.md`
- `specstory-cli/.specstory/history/2026-02-10_22-12-29Z.md`
- `specstory-cli/.specstory/history/2026-02-10_22-16-11Z-command-name-fast-command.md`
- `specstory-cli/.specstory/history/2026-02-10_22-16-11Z.md`
- `specstory-cli/.specstory/history/2026-02-11_14-35-11Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-11_16-29-09Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-02-11_16-29-09Z.md`
- `specstory-cli/.specstory/history/2026-02-11_21-30-58Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-02-11_21-30-58Z.md`
- `specstory-cli/.specstory/history/2026-02-11_21-43-59Z-command-name-extra-usage.md`
- `specstory-cli/.specstory/history/2026-02-11_21-43-59Z.md`
- `specstory-cli/.specstory/history/2026-02-11_22-34-04Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-11_22-34-04Z.md`
- `specstory-cli/.specstory/history/2026-02-12_15-08-34Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-02-12_15-08-34Z.md`
- `specstory-cli/.specstory/history/2026-02-12_17-20-59Z-notice-what-we-did.md`
- `specstory-cli/.specstory/history/2026-02-12_22-37-48Z-command-name-clear-command.md`
- `specstory-cli/.specstory/history/2026-02-12_22-37-48Z.md`
- `specstory-cli/.specstory/history/2026-02-16_13-04-52Z-in-the-claude-code.md`
- `specstory-cli/.specstory/history/2026-02-16_13-22-08Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-02-16_13-22-08Z.md`
- `specstory-cli/.specstory/history/2026-02-16_15-21-13Z-look-at-our-5.md`
- `specstory-cli/.specstory/history/2026-02-16_16-27-42Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-16_17-16-47Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-02-16_17-16-47Z.md`
- `specstory-cli/.specstory/history/2026-02-16_23-35-56Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-20_15-53-44Z-read-docs-git-provenance.md`
- `specstory-cli/.specstory/history/2026-02-20_15-57-50Z-read-docs-git-provenance.md`
- `specstory-cli/.specstory/history/2026-02-20_16-33-05Z-start-with-docs-git.md`
- `specstory-cli/.specstory/history/2026-02-20_21-04-48Z-recent-cursor-cli-session.md`
- `specstory-cli/.specstory/history/2026-02-20_22-54-02Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-20_22-54-02Z.md`
- `specstory-cli/.specstory/history/2026-02-20_23-42-22Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-20_23-57-00Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-21_14-41-17Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-22_13-40-18Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-22_13-40-18Z.md`
- `specstory-cli/.specstory/history/2026-02-22_14-46-55Z-there-s-a-merge.md`
- `specstory-cli/.specstory/history/2026-02-22_15-19-19Z-this-branch-adds-an.md`
- `specstory-cli/.specstory/history/2026-02-22_15-59-39Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-23_12-05-09Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-23_16-02-56Z-i-just-merged-dev.md`
- `specstory-cli/.specstory/history/2026-02-23_21-34-27Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-23_21-34-27Z.md`
- `specstory-cli/.specstory/history/2026-02-23_22-28-34Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-23_22-28-34Z.md`
- `specstory-cli/.specstory/history/2026-02-23_23-01-52Z-all-provider-specific-token.md`
- `specstory-cli/.specstory/history/2026-02-24_13-47-24Z-use-gh-to-check.md`
- `specstory-cli/.specstory/history/2026-02-24_14-14-29Z-i-ve-got-a.md`
- `specstory-cli/.specstory/history/2026-02-24_14-32-59Z-move-the-specstory-statistics.md`
- `specstory-cli/.specstory/history/2026-02-24_14-40-35Z-this-output-is-confusing.md`
- `specstory-cli/.specstory/history/2026-02-24_21-21-18Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-24_21-21-18Z.md`
- `specstory-cli/.specstory/history/2026-02-25_13-41-51Z-the-multi-agent-sync.md`
- `specstory-cli/.specstory/history/2026-02-25_14-02-37Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-02-25_15-39-50Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-25_15-39-50Z.md`
- `specstory-cli/.specstory/history/2026-02-26_09-53-09Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-02-26_09-53-09Z.md`
- `specstory-cli/.specstory/history/2026-03-02_22-30-03Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-03-02_22-30-03Z.md`
- `specstory-cli/.specstory/history/2026-03-02_22-47-49Z-take-a-look-at.md`
- `specstory-cli/.specstory/history/2026-03-02_23-01-22Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-03-02_23-16-17Z-implement-the-following-plan.md`
- `specstory-cli/.specstory/history/2026-03-02_23-29-33Z-command-message-code-review.md`
- `specstory-cli/.specstory/history/2026-03-02_23-29-33Z.md`
- `specstory-cli/.specstory/history/2026-03-06_15-54-14Z-command-message-pr-review.md`
- `specstory-cli/.specstory/history/2026-03-06_15-54-14Z.md`
- `specstory-cli/.specstory/history/2026-03-06_16-11-26Z-consider-this-review-feedback.md`
- `specstory-cli/.specstory/history/2026-03-06_20-09-49Z-another-common-synthetic-message.md`
- `specstory-cli/.specstory/history/2026-03-18_10-13-59Z.md`
- `specstory-cli/.specstory/history/2026-03-18_10-33-34Z-take-a-look-at.md`
- `specstory-cli/.specstory/history/2026-04-13_12-53-52Z.md`

## Child Contributions

### task-0

# Goal: `/workthreads` - weekly work-thread rollup (mirror lore)

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

- `.claude-plugin/marketplace.json`
- `.cursorindexingignore`
- `.deadreckon/acceptance/deps-unchanged.mjs`
- `.deadreckon/acceptance/no-em-dash.sh`
- `.deadreckon/acceptance/threads_classify.mjs`
- `.deadreckon/acceptance/unchanged-files.sh`
- `.deadreckon/codebase.json`
- `.deadreckon/docs/RUN-AS-BUILT.md`
- `.deadreckon/docs/RUN-DECISIONS.md`
- `.deadreckon/docs/RUN-NARRATIVE.md`
- `.deadreckon/docs/_incremental.jsonl`
- `.deadreckon/docs/polish.json`
- `.deadreckon/parent.json`
- `.deadreckon/provenance.jsonl`
- `.deadreckon/sleep-prevention.json`
- `.deadreckon/spend.jsonl`
- `.deadreckon/traces.jsonl`
- `.github/ISSUE_TEMPLATE/cloud-bug-feature.md`
- `.github/ISSUE_TEMPLATE/extension_report.md`
- `.github/ISSUE_TEMPLATE/lore_bug_report.yml`
- `.github/ISSUE_TEMPLATE/lore_feature_request.yml`
- `.github/copilot-instructions.md`
- `.github/dependabot.yml`
- `.github/workflows/ci.yml`
- `.github/workflows/codeql.yml`
- `.github/workflows/lore-release.yml`
- `.github/workflows/lore-validate.yml`
- `.github/workflows/release.yml`
- `.gitignore`
- `.goreleaser.yml`
- `CODE-OF-CONDUCT.md`
- `CONTRIBUTING.md`
- `LICENSE.txt`
- `README.md`
- `docs/RUN-AS-BUILT.md`
- `docs/RUN-DECISIONS.md`
- `docs/RUN-NARRATIVE.md`
- `implementation-notes.html`
- `install.sh`
- `lore/.claude-plugin/plugin.json`
- `lore/.gitattributes`
- `lore/.gitignore`
- `lore/AGENTS.md`
- `lore/AS-BUILT-ARCHITECTURE.md`
- `lore/CHANGELOG.md`
- `lore/CLAUDE.md`
- `lore/CONTRIBUTING.md`
- `lore/HOW-IT-WORKS.md`
- `lore/LICENSE`
- `lore/README.md`

### task-1

Review the completed implementation for: # Goal: `/workthreads` - weekly work-thread rollup (mirror lore)

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
must stay green.. Write .deadreckon/REVIEW.md first, then apply only fixes tied to findings and acceptance.

- `.claude-plugin/marketplace.json`
- `.cursorindexingignore`
- `.deadreckon/REVIEW.md`
- `.deadreckon/acceptance/deps-unchanged.mjs`
- `.deadreckon/acceptance/no-em-dash.sh`
- `.deadreckon/acceptance/threads_classify.mjs`
- `.deadreckon/acceptance/unchanged-files.sh`
- `.deadreckon/codebase.json`
- `.deadreckon/docs/RUN-AS-BUILT.md`
- `.deadreckon/docs/RUN-DECISIONS.md`
- `.deadreckon/docs/RUN-NARRATIVE.md`
- `.deadreckon/docs/_incremental.jsonl`
- `.deadreckon/docs/polish.json`
- `.deadreckon/parent.json`
- `.deadreckon/provenance.jsonl`
- `.deadreckon/sleep-prevention.json`
- `.deadreckon/spend.jsonl`
- `.deadreckon/traces.jsonl`
- `.github/ISSUE_TEMPLATE/cloud-bug-feature.md`
- `.github/ISSUE_TEMPLATE/extension_report.md`
- `.github/ISSUE_TEMPLATE/lore_bug_report.yml`
- `.github/ISSUE_TEMPLATE/lore_feature_request.yml`
- `.github/copilot-instructions.md`
- `.github/dependabot.yml`
- `.github/workflows/ci.yml`
- `.github/workflows/codeql.yml`
- `.github/workflows/lore-release.yml`
- `.github/workflows/lore-validate.yml`
- `.github/workflows/release.yml`
- `.gitignore`
- `.goreleaser.yml`
- `CODE-OF-CONDUCT.md`
- `CONTRIBUTING.md`
- `LICENSE.txt`
- `README.md`
- `docs/RUN-AS-BUILT.md`
- `docs/RUN-DECISIONS.md`
- `docs/RUN-NARRATIVE.md`
- `implementation-notes.html`
- `install.sh`
- `lore/.claude-plugin/plugin.json`
- `lore/.gitattributes`
- `lore/.gitignore`
- `lore/AGENTS.md`
- `lore/AS-BUILT-ARCHITECTURE.md`
- `lore/CHANGELOG.md`
- `lore/CLAUDE.md`
- `lore/CONTRIBUTING.md`
- `lore/HOW-IT-WORKS.md`
- `lore/LICENSE`

## Gaps

- provider fallback: provider JSON parse failed: expected value at line 1 column 1
