# New Provider Review

Use this supplement to evaluate a new provider submission against [NEW-PROVIDER-GUIDE.md](../NEW-PROVIDER-GUIDE.md). The guide defines what an acceptable provider must do; this document explains how to assess the evidence and report the result. Keep implementation requirements in the guide so contributors and reviewers work from the same standard.

## Establish the scope

Identify the submission's revision, comparison branch, and any local changes included in the review. Record the agent version, store format, supported capabilities, and available test environments. If the branch is stale, distinguish integration gaps from provider defects and existing failures from introduced ones. Recheck the revision when work resumes or the branch changes.

Follow the current task's authorization for review and fixes. A request to evaluate a submission does not itself authorize implementation or release work; an explicit request to fix findings should not be interrupted by approval rituals inherited from earlier reviews.

## Review against the current standard

Use the guide's requirements and self-review checklist, the current [SPI contracts](../pkg/spi/provider.go), and the repository's [code-review](../.claude/commands/code-review.md) or [PR-review](../.claude/commands/pr-review.md) checklist. Review documentation and release claims too, even where a general review command excludes them.

Trace the implementation through discovery, parsing, rendering, watching, and resume, including its command and configuration wiring. Compare local helpers with the shared equivalents and investigate differences in behavior. Use the guide's concern-specific exemplars as examples, not proof of correctness: established providers can contain drift. Resolve discrepancies against the contract and observed native data rather than copying a sibling blindly.

Audit fixture placement against the guide's [testdata/examples rule](../NEW-PROVIDER-GUIDE.md#separate-test-fixtures-from-examples). Identify the automated test consumer and asserted behavior for every fixture set under `testdata/`; copying a containing directory is not consumption. Saved outputs that no test compares, manual QA results, and other review artifacts belong in `examples/`.

## Verify the risky behavior

Start with the cheapest experiment that can distinguish the suspected defect from correct behavior. Build a fresh `./specstory` for manual verification and use the guide's command matrix and tool audit. Compare native records, normalized session data, and rendered Markdown when diagnosing missing or incorrect content.

Assess tests by the behavior they can disprove, not by their number. A regression test should fail without its fix and preserve the legitimate case. For resume, a serializer/parser round trip is insufficient evidence that the real agent can load and continue the conversation.

Separate code inspection, automated tests, and real-agent verification in the report. State the tested revision, agent version, and OS, and identify unavailable credentials, platforms, or sessions that limit verification. Run the applicable build, lint, and test checks; verify relevant CI results, including Windows, against the reviewed revision. Earlier green CI does not validate later local edits. Untested behavior remains untested, even when the code looks plausible.

## Check effects beyond the provider

When shared code changes, identify affected callers and verify the relevant existing-provider behavior. For rendering changes, compare regenerated Markdown from representative captured sessions; explain intended differences. For path or identity changes, check consequences for persisted indexes, session identities, and output paths.

If copied code exposes a defect in a sibling, investigate and report its scope. Fix related code when the task authorizes it; otherwise record a follow-up. Do not automatically expand a provider review into a cross-provider refactor.

## Produce actionable findings

Number findings so they can be discussed individually. Each should identify the code location, requirement or expected behavior, user impact, supporting evidence, and recommended action. Label uncertainty explicitly. Prioritize data loss, incorrect attribution, broken commands, and regressions; keep optional improvements separate from defects. Avoid findings based only on personal style, hypothetical scaling, or a smaller test suite than an exemplar.

Evaluate bot feedback against the current code, including whether later changes already addressed it. Verify both the diagnosis and proposed remedy. Record a technical reason for accepting or rejecting a finding; the bot's severity is not evidence.

## Hand back a decision

State whether the submission is ready to merge, blocked by specific findings, or awaiting verification. Identify the most important outstanding issues, what was verified, and what remains untested or deliberately deferred. If fixes were made, summarize their purpose and validation and identify any uncommitted work.

Keep merge readiness separate from release follow-ups. List outstanding version-floor and announcement checks, website documentation, and factory enrollment work with enough detail for someone to act. Do not describe a follow-up as completed without evidence.
