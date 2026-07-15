# Decisions2

_A multi-pass decision report mined from SpecStory coding histories._

**Experimental.** An exploration of a multi-pass approach to decision mining, seeded by
commit messages and expanded via a commit-seeded fingerprint. Treats a decision as a **PROCESS**
(the whole arc: proposal, deliberation, choice, reversal), not a final choice.

## How it works

Four deterministic passes, each independently runnable and measurable:

1. **Seed** — commit messages with decision-shaped language (~85-90% precision). The strongest
   decision indicator. Conventional-commit scopes are clean entities.
2. **Fingerprint** — per-project entity sets + verb templates mined from seeds.
3. **Expand** — fingerprint + lexical grammar over all chat turns. Each candidate gets a ROLE
   (proposed / deliberated / reversed / provisional / open).
4. **Arcs** — cluster candidates + seeds into per-entity (or per-file) process arcs. Each arc has
   a STATE (in-formation / decided / changed / abandoned).

The calling agent then does the precision pass: drop false positives, resolve referents, name
entities, split over-linked arcs, write the report.

## Install

```zsh
cd decisions2
./install.sh
```

Requirements: Node >= 22.5 (for `node:sqlite`), and the SpecStory CLI capturing histories into
`.specstory/history/`.

## Use

```bash
node scripts/decisions2.mjs index --projects ~/dev --db ~/.specstory/decisions2.db
node scripts/decisions2.mjs decisions --db ~/.specstory/decisions2.db --days 30
```

Subcommands: `index | seed | fingerprint | expand | arcs | decisions`.

## Develop

```zsh
cd decisions2
npm test
```

11 tests over fixtures encoding a full process arc (Postgres → SQLite reversal, provisional debt,
open deliberation).

## Lineage

Borrows the deterministic-engine + agent-pass split from [SpecStory Lore](../lore/). The
multi-pass approach (commit-seeded fingerprint → expand → arcs) is new. See
`../decisions-worklog.md` for the full design journey.

## License

Apache-2.0.
