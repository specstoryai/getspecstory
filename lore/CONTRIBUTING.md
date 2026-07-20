# Contributing to Lore

Thanks for wanting to make Lore better. This guide covers the project's structure, the rules that
keep the engine trustworthy, and how to land a change.

## Development setup

```zsh
git clone git@github.com:specstoryai/getspecstory.git ~/getspecstory
mkdir -p ~/.agents/skills && ln -sfn ~/getspecstory/lore ~/.agents/skills/lore

# fan out into the harnesses you use (symlink, never copy)
for h in ~/.claude/skills ~/.codex/skills; do
  [ -d "$h" ] && ln -sfn ~/.agents/skills/lore "$h/lore"
done

npm test    # Node >= 22.5; no install step - there are no dependencies
```

Because the install is a symlink, every edit is live in your harness immediately.

> [!WARNING]
> If you have ever run `npx skills add` for lore on this machine, it left a frozen COPY at
> `~/.agents/skills/lore` - a real directory, not a symlink. `ln -sfn` against it silently
> creates a nested link inside instead of replacing it, and your dev install gets shadowed by
> the stale copy. Check with `readlink ~/.agents/skills/lore`; if it prints nothing,
> `rm -rf ~/.agents/skills/lore` first, then symlink.

Use a scratch corpus while developing so you never pollute your real one:

```zsh
node scripts/mine-skills.mjs index --scan <some-project> --db /tmp/dev-lore.db
```

## The two rules that are not negotiable

1. **Zero dependencies.** The engine is plain Node (`node:sqlite`, `node:fs`, `node:crypto`). No
   npm packages, no network calls, no API keys. If a change seems to need a dependency, open an
   issue first - the answer is almost always a small amount of plain code.
2. **The engine is deterministic; the agent judges.** Parsing, counting, fingerprinting, and
   rendering live in `scripts/` and must be reproducible byte for byte. Anything requiring
   judgment (naming, theme proposal, verification) belongs in `SKILL.md`'s contract or the
   workflow scripts, never in the engine.

## Layout

```
SKILL.md                    the agent contract (the skill itself; repo root = skill dir)
scripts/mine-skills.mjs     thin CLI entry
scripts/lib/                the engine: patterns (format grammar), parse, db, indexer,
                            report, beats (themes/dossiers/plans), forged (registry), discover
scripts/hooks/              harness enforcement (ExitPlanMode plan validation)
scripts/*.workflow.js       Claude Code Workflow fan-outs (deep-mine, theme-sweep)
fixtures/                   the executable spec - see below
tests/engine.test.mjs       the suite (node --test)
```

## Fixtures are the spec

Every parsing or rendering claim must be backed by a fixture, in the should-flag/should-pass style:

- **A new transcript format or provider**: add a minimal session under
  `fixtures/provider-<name>/.specstory/history/` with the provider's REAL header and tool-envelope
  bytes (verify against specstory-cli's writers - do not invent formats), plus a test asserting
  the agent slug, outcome labeling, and command capture.
- **A new extraction rule**: add the verbatim byte example to the relevant section of
  `scripts/lib/patterns.mjs` (every regex there is preceded by the real bytes it matches) and a
  fixture or inline case exercising it.
- **A parser behavior change**: bump `PARSER_VERSION` in `scripts/lib/db.mjs` so existing corpora
  re-parse automatically on the next index.
- **A plan/render layout change**: the golden file will fail - regenerate with
  `UPDATE_GOLDEN=1 npm test` and include the `fixtures/golden/forge-plan.md` diff in your PR so
  the layout change is reviewable.

Run the full suite after every change: `npm test` (29 tests and growing; all must pass).

## Style

- Idiomatic modern Node ESM; small pure functions; comments explain WHY, not what.
- No em dashes anywhere - in docs, code comments, or engine-emitted strings (use " - ").
- Engine output that the agent must render verbatim is wrapped in PASS-THROUGH markers and ends
  with a machine-checkable sentinel; never weaken these (they are LAW 1/2 of the output contract).
- SKILL.md changes: keep the Voice rule (mining/candidates language; "forge" only for the final
  act) and never remove a named failure mode - they are the contract's institutional memory.

## Commits and PRs

- Conventional commits: `type(scope): subject` with a body that explains the why
  (`feat(theme): ...`, `fix(forged): ...`, `docs(readme): ...`).
- One logical change per PR. If you touched the golden file, say why in the body.
- CI (`validate.yml`) runs the suite and syntax checks on every PR; it must be green.

## Releases (maintainers)

1. Update `CHANGELOG.md`.
2. Bump the version in FOUR places: `SKILL.md` frontmatter (`metadata.version`),
   `lore/.claude-plugin/plugin.json`, the repo-root `.claude-plugin/marketplace.json`, and
   `lore/package.json`.
3. Tag and push: `git tag lore/vX.Y.Z && git push origin lore/vX.Y.Z`.
4. `lore-release.yml` runs the suite, verifies the tag matches all four version spots, and
   publishes the GitHub Release (`lore-vX.Y.Z`, never marked latest - that pointer belongs to
   the CLI installer) with the `lore.skill` artifact.

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
