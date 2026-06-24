#!/usr/bin/env node
// workthreads engine - CLI entry. A standalone skill: no dependency on any other skill.
// The real work lives in purpose-driven modules under lib/ (zero npm deps, Node built-ins only):
//   lib/patterns.mjs  regexes / classifiers     lib/parse.mjs    transcript -> beats
//   lib/discover.mjs  project + file discovery   lib/db.mjs       SQLite corpus schema
//   lib/indexer.mjs   incremental indexing       lib/threads.mjs  thread clustering + lifecycle
//
// Subcommands:
//   index    --dir <history-dir> [--dir ...] | --projects <parent> | --scan <root> [--db <path>] [--days N] [--force]
//   threads  [--db <path>] [--days N] [--json] [--out <file>]
//            (also accepts the discovery flags above: it will index first, then render)
//
// Default DB: ~/.specstory/workthreads.db  (its own corpus; does not share lore's).
import { join } from 'node:path'
import { homedir } from 'node:os'
import { writeFileSync } from 'node:fs'
import { openDb } from './lib/db.mjs'
import { indexCorpus } from './lib/indexer.mjs'
import { computeThreads, renderDigest, threadsJson } from './lib/threads.mjs'

function parseArgs(argv) {
  const a = {
    cmd: '', dirs: [], projects: '', scan: '',
    db: join(homedir(), '.specstory', 'workthreads.db'),
    days: 0, maxBytes: 200_000_000, force: false, json: false, out: '',
  }
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
    else if (t === '--json' || t === '--emit=json') a.json = true
    else if (t === '--out') a.out = argv[++i]
  }
  if (!a.cmd) a.cmd = 'threads'
  return a
}

const ARGS = parseArgs(process.argv.slice(2))
const db = openDb(ARGS.db)

if (ARGS.cmd === 'index') {
  const r = indexCorpus(db, ARGS)
  if (r.error) { process.stderr.write(`workthreads index: ${r.error}\n`); process.exit(2) }
  process.stdout.write(`indexed ${r.indexed} sessions (${r.skippedKnown} unchanged, ${r.skippedBig} too large) across ${r.projects.length} project(s) -> ${ARGS.db}\n`)
} else if (ARGS.cmd === 'threads') {
  // Convenience: if discovery flags are given, index first (unbounded - a 7-day rollup still needs
  // older context to classify "open" and "recently closed"), then render from the same corpus.
  if (ARGS.dirs.length || ARGS.projects || ARGS.scan) {
    const r = indexCorpus(db, { ...ARGS, days: 0 })
    if (r.error) { process.stderr.write(`workthreads threads: ${r.error}\n`); process.exit(2) }
  }
  const threads = computeThreads(db, { days: ARGS.days })
  if (ARGS.json) {
    process.stdout.write(JSON.stringify(threadsJson(threads), null, 2) + '\n')
  } else {
    const digest = renderDigest(threads, { days: ARGS.days })
    const text = digest.endsWith('\n') ? digest : digest + '\n'
    process.stdout.write(text)
    if (ARGS.out) writeFileSync(ARGS.out, text)
  }
} else {
  process.stderr.write('usage: workthreads.mjs index|threads [--dir D | --projects P | --scan R] [--db PATH] [--days N] [--json] [--out FILE]\n')
  process.exit(2)
}
