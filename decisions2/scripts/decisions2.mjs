#!/usr/bin/env node
// decisions2.mjs - CLI entry. Multi-pass decision mining over SpecStory histories.
//
// Passes (each independently runnable and measurable):
//   index       --dir D | --projects P | --scan R [--db PATH] [--force]   (reuse decisions/ parser)
//   seed        Pass 1: commit-message seed extraction → seeds table
//   fingerprint Pass 2: per-project entity + verb fingerprints → fingerprints table
//   expand      Pass 3: fingerprint-guided expansion over chat turns → candidates table
//   arcs        cluster candidates + seeds into per-entity process arcs → arcs table
//   decisions   run all passes + render the digest
//
// Default DB: ~/.specstory/decisions2.db (own corpus; shares nothing with other skills).

import { join } from 'node:path'
import { homedir } from 'node:os'
import { openDb, resetDecisionsTables } from './lib/db.mjs'
import { indexCorpus } from './lib/indexer.mjs'
import { extractSeeds } from './lib/commit_seed.mjs'
import { buildFingerprints } from './lib/fingerprint.mjs'
import { expand } from './lib/expand.mjs'
import { buildArcs, renderDigest } from './lib/arcs.mjs'

function parseArgs(argv) {
  const a = { cmd: '', dirs: [], projects: '', scan: '', db: join(homedir(), '.specstory', 'decisions2.db'),
    days: 0, maxBytes: 200_000_000, force: false, json: false, out: '', reset: false }
  let i = 0
  if (argv[0] && !argv[0].startsWith('--')) { a.cmd = argv[0]; i = 1 }
  for (; i < argv.length; i++) {
    const t = argv[i]
    if (t === '--dir') a.dirs.push(argv[++i])
    else if (t === '--projects') a.projects = argv[++i]
    else if (t === '--scan') a.scan = argv[++i]
    else if (t === '--db') a.db = argv[++i]
    else if (t === '--days') a.days = +argv[++i]
    else if (t === '--max-bytes') a.maxBytes = +argv[++i]
    else if (t === '--force') a.force = true
    else if (t === '--reset') a.reset = true
    else if (t === '--out') a.out = argv[++i]
  }
  if (!a.cmd) a.cmd = 'decisions'
  return a
}

const ARGS = parseArgs(process.argv.slice(2))
const db = openDb(ARGS.db)

function runPipeline() {
  if (ARGS.reset) resetDecisionsTables(db)
  // clear decision tables so re-runs don't duplicate (indexing is separate)
  db.prepare('DELETE FROM seeds').run()
  db.prepare('DELETE FROM fingerprints').run()
  db.prepare('DELETE FROM candidates').run()
  db.prepare('DELETE FROM arcs').run()
  const s = extractSeeds(db)
  const f = buildFingerprints(db)
  const e = expand(db)
  const a = buildArcs(db)
  return { seeds: s, fingerprints: f, expand: e, arcs: a }
}

if (ARGS.cmd === 'index') {
  const r = indexCorpus(db, ARGS)
  if (r.error) { process.stderr.write(`decisions2 index: ${r.error}\n`); process.exit(2) }
  process.stdout.write(`indexed ${r.indexed} sessions across ${r.projects.length} project(s) -> ${ARGS.db}\n`)

} else if (ARGS.cmd === 'seed') {
  const r = extractSeeds(db)
  process.stdout.write(`Pass 1 (seed): ${r.seeds} seeds. by project: ${Object.entries(r.byProject).map(([p,n])=>`${p}=${n}`).join(', ')}\n`)

} else if (ARGS.cmd === 'fingerprint') {
  const r = buildFingerprints(db)
  for (const fp of r.projects) {
    process.stdout.write(`Pass 2 (fingerprint) ${fp.project}: ${fp.entities.length} entities, ${fp.verbs.length} verbs\n`)
  }

} else if (ARGS.cmd === 'expand') {
  const r = expand(db)
  process.stdout.write(`Pass 3 (expand): ${r.candidates} candidates. by role: ${Object.entries(r.byRole).map(([r,n])=>`${r}=${n}`).join(', ')}\n`)

} else if (ARGS.cmd === 'arcs') {
  const r = buildArcs(db)
  process.stdout.write(`arcs: ${r.arcs}. by state: ${Object.entries(r.byState).map(([s,n])=>`${s}=${n}`).join(', ')}\n`)

} else if (ARGS.cmd === 'decisions' || ARGS.cmd === 'report') {
  if (ARGS.dirs.length || ARGS.projects || ARGS.scan) {
    const r = indexCorpus(db, { ...ARGS, days: 0 })
    if (r.error) { process.stderr.write(`decisions2: ${r.error}\n`); process.exit(2) }
  }
  runPipeline()
  const digest = renderDigest(db, { days: ARGS.days })
  process.stdout.write(digest.endsWith('\n') ? digest : digest + '\n')
  if (ARGS.out) { const { writeFileSync } = await import('node:fs'); writeFileSync(ARGS.out, digest) }

} else {
  process.stderr.write('usage: decisions2.mjs index|seed|fingerprint|expand|arcs|decisions [--dir D | --projects P | --scan R] [--db PATH] [--days N] [--out FILE] [--reset]\n')
  process.exit(2)
}
db.close()
