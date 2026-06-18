# Agent Rules for the Lore Repo

You are working ON the lore engine/skill, not running it. Read [CONTRIBUTING.md](CONTRIBUTING.md)
first; these are the rules that bite:

- **Zero dependencies, ever.** Plain Node only (`node:sqlite`, `node:fs`, `node:crypto`). If a
  change seems to need an npm package, stop and ask.
- **The engine is deterministic; the agent judges.** Parsing/counting/fingerprinting/rendering live
  in `scripts/` and must be byte-reproducible. Judgment (naming, themes, verification) belongs in
  SKILL.md's contract or the workflow scripts.
- **Fixtures are the spec.** Every parsing or rendering claim needs a fixture
  (should-flag/should-pass style). New provider formats use REAL bytes verified against
  specstory-cli's writers - never invented. Parser behavior changes bump `PARSER_VERSION` in
  `scripts/lib/db.mjs`.
- **The golden file is intentional.** If `fixtures/golden/forge-plan.md` fails, either your change
  is wrong or the layout change is deliberate - regenerate with `UPDATE_GOLDEN=1 npm test` and say
  so in the commit body.
- **Never weaken the output contract.** PASS-THROUGH markers, sentinels, and the named failure
  modes in SKILL.md are enforcement and institutional memory; do not soften or delete them.
- **No em dashes** anywhere - docs, comments, or engine-emitted strings. Use " - ".
- **Run `npm test` after every change** (29+ tests; Node >= 22.5). Conventional commits with a
  why-body: `type(scope): subject`.
- Use a scratch corpus while developing: `--db /tmp/dev-lore.db`. Never write to the user's real
  `~/.specstory/lore.db` from tests or experiments.
