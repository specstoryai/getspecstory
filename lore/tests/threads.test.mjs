// threads.test.mjs - unit + integration tests for the `threads` work-thread lens.
// Run: node --test tests/threads.test.mjs   (or the whole suite: node --test tests/*.test.mjs)
//
// The committed fixtures live under fixtures/threads/history (NOT a .specstory layout), so the
// engine's project-discovery (`--scan`/`--projects fixtures`) never picks them up and the existing
// fixture-count assertions stay green; this test indexes them directly with `--dir`. Lifecycle is
// relative to the current date, so classification is asserted against a FIXED `nowMs` anchor
// (2026-04-01) - the fixtures encode known states relative to that anchor and never drift.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { rmSync, existsSync, readFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { tmpdir } from 'node:os'
import { openDb } from '../scripts/lib/db.mjs'
import { buildThreads, renderDigest } from '../scripts/lib/threads.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const HIST = join(ROOT, 'fixtures', 'threads', 'history')
const NOW = Date.parse('2026-04-01T12:00:00Z')   // fixed anchor: fixtures are dated relative to this

function freshCorpus() {
  const dbPath = join(tmpdir(), `lore-threads-${process.pid}-${Math.floor(NOW / 1000)}.db`)
  for (const suf of ['', '-wal', '-shm']) rmSync(dbPath + suf, { force: true })
  execFileSync('node', ['scripts/mine-skills.mjs', 'index', '--dir', HIST, '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
  return dbPath
}
function cleanup(dbPath) { for (const suf of ['', '-wal', '-shm']) rmSync(dbPath + suf, { force: true }) }
const touching = (threads, file) => threads.filter(t => t.files.some(f => f.includes(file)))

function insertGramOnlyBeat(db, sessionId, ord, intent) {
  const res = db.prepare('INSERT INTO beats(session_id,ord,start_line,intent_raw,intent_sig,n_tools,tool_mix,files,n_cmds,exit_fails,outcome) VALUES(?,?,?,?,?,?,?,?,?,?,?)')
    .run(sessionId, ord, 10 + ord, intent, null, 1, 'shell:1', '', 2, 0, 'neutral')
  const beatId = res.lastInsertRowid
  db.prepare('INSERT INTO commands(beat_id,ord,head,raw,line) VALUES(?,?,?,?,?)').run(beatId, 0, 'swift build', 'swift build', 20)
  db.prepare('INSERT INTO commands(beat_id,ord,head,raw,line) VALUES(?,?,?,?,?)').run(beatId, 1, 'swift test', 'swift test', 21)
  db.prepare('INSERT INTO grams(beat_id,n,gram) VALUES(?,?,?)').run(beatId, 2, 'swift build ▸ swift test')
}

// ---------- clustering: a line of work across multiple sessions is ONE thread ----------

test('threads: a line of work spanning multiple sessions merges into ONE thread', () => {
  const dbPath = freshCorpus()
  try {
    const { threads } = buildThreads(openDb(dbPath), { nowMs: NOW })
    const palette = touching(threads, 'Palette.tsx')
    assert.equal(palette.length, 1, 'the command-palette work (two sessions, same file) is one thread')
    assert.equal(palette[0].sessions, 2, 'both sessions merged into the single thread')
    const auth = touching(threads, 'src/auth/token.ts')
    assert.equal(auth.length, 1)
    assert.equal(auth[0].sessions, 2)
    // separation: distinct files are distinct threads, never over-merged
    assert.ok(!JSON.stringify(palette[0]).includes('token.ts'), 'palette thread did not absorb the auth work')
  } finally { cleanup(dbPath) }
})

test('threads: shared distinctive command grams reinforce clustering across sessions', () => {
  const dbPath = join(tmpdir(), `lore-threads-grams-${process.pid}.db`)
  for (const suf of ['', '-wal', '-shm']) rmSync(dbPath + suf, { force: true })
  try {
    const db = openDb(dbPath)
    db.prepare('INSERT INTO sessions(id,project_id,project_name,path,date,agent,size,beats) VALUES(?,?,?,?,?,?,?,?)')
      .run('p/one.md', 'p', 'proj', 'history/one.md', '2026-03-30', 'claude-code', 1, 1)
    db.prepare('INSERT INTO sessions(id,project_id,project_name,path,date,agent,size,beats) VALUES(?,?,?,?,?,?,?,?)')
      .run('p/two.md', 'p', 'proj', 'history/two.md', '2026-03-31', 'claude-code', 1, 1)
    insertGramOnlyBeat(db, 'p/one.md', 0, 'continue the package validation flow')
    insertGramOnlyBeat(db, 'p/two.md', 0, 'follow up on validation failures')

    const { threads } = buildThreads(db, { nowMs: NOW })
    const gramThreads = threads.filter(t => t.evidence.some(ev => ev.ref.startsWith('history/')))
    assert.equal(gramThreads.length, 1, 'two sessions with the same distinctive command gram merge')
    assert.equal(gramThreads[0].sessions, 2)
  } finally { cleanup(dbPath) }
})

// ---------- lifecycle labels: closed / open / new ----------

test('threads: a success-then-quiet line of work is CLOSED', () => {
  const dbPath = freshCorpus()
  try {
    const { threads } = buildThreads(openDb(dbPath), { nowMs: NOW })
    const palette = touching(threads, 'Palette.tsx')[0]
    assert.equal(palette.status, 'closed')
    assert.equal(palette.lastLabeledOutcome, 'success', 'closed because the most recent labeled outcome is success')
  } finally { cleanup(dbPath) }
})

test('threads: a recent unresolved line of work is OPEN', () => {
  const dbPath = freshCorpus()
  try {
    const { threads } = buildThreads(openDb(dbPath), { nowMs: NOW })
    const auth = touching(threads, 'src/auth/token.ts')[0]
    assert.equal(auth.status, 'open')
    assert.equal(auth.lastLabeledOutcome, 'corrected', 'still unresolved: last labeled outcome is a correction')
  } finally { cleanup(dbPath) }
})

test('threads: a first-seen-in-window line of work is NEW', () => {
  const dbPath = freshCorpus()
  try {
    const { threads } = buildThreads(openDb(dbPath), { nowMs: NOW })
    const search = touching(threads, 'SearchIndex.ts')[0]
    assert.equal(search.status, 'new')
    assert.equal(search.sessions, 1)
  } finally { cleanup(dbPath) }
})

// ---------- digest contract: the three headers, always, in order ----------

test('threads: digest always prints New / Open / Recently closed in order', () => {
  const dbPath = freshCorpus()
  try {
    const model = buildThreads(openDb(dbPath), { nowMs: NOW })
    const digest = renderDigest(model)
    const iNew = digest.indexOf('## New')
    const iOpen = digest.indexOf('## Open')
    const iClosed = digest.indexOf('## Recently closed')
    assert.ok(iNew >= 0 && iOpen >= 0 && iClosed >= 0, 'all three section headers present')
    assert.ok(iNew < iOpen && iOpen < iClosed, 'headers appear in the required order')
  } finally { cleanup(dbPath) }
})

test('threads: empty corpus still prints all three headers', () => {
  const dbPath = join(tmpdir(), `lore-threads-empty-${process.pid}.db`)
  for (const suf of ['', '-wal', '-shm']) rmSync(dbPath + suf, { force: true })
  try {
    const model = buildThreads(openDb(dbPath), { nowMs: NOW })
    const digest = renderDigest(model)
    for (const h of ['## New', '## Open', '## Recently closed']) assert.ok(digest.includes(h), `missing ${h}`)
  } finally { cleanup(dbPath) }
})

// ---------- CLI surface: subcommand, --json purity, --out, determinism ----------

test('threads CLI: digest headers, --out file, byte-identical across runs', () => {
  const dbPath = freshCorpus()
  const outFile = join(tmpdir(), `lore-threads-out-${process.pid}.md`)
  rmSync(outFile, { force: true })
  try {
    const run1 = execFileSync('node', ['scripts/mine-skills.mjs', 'threads', '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    const run2 = execFileSync('node', ['scripts/mine-skills.mjs', 'threads', '--db', dbPath], { cwd: ROOT, encoding: 'utf8' })
    assert.equal(run1, run2, 'two runs on the same corpus are byte-identical')
    for (const h of ['New', 'Open', 'Recently closed']) assert.ok(run1.includes(h), `digest missing ${h}`)
    execFileSync('node', ['scripts/mine-skills.mjs', 'threads', '--db', dbPath, '--out', outFile], { cwd: ROOT, encoding: 'utf8' })
    assert.ok(existsSync(outFile), '--out wrote a report file')
    const written = readFileSync(outFile, 'utf8')
    for (const h of ['New', 'Open', 'Recently closed']) assert.ok(written.includes(h), `report file missing ${h}`)
  } finally { cleanup(dbPath); rmSync(outFile, { force: true }) }
})

test('threads CLI: --json prints ONLY parseable JSON with valid status + files', () => {
  const dbPath = freshCorpus()
  try {
    const out = execFileSync('node', ['scripts/mine-skills.mjs', 'threads', '--db', dbPath, '--json'], { cwd: ROOT, encoding: 'utf8' })
    const parsed = JSON.parse(out)   // throws if anything non-JSON leaked to stdout
    const threads = Array.isArray(parsed) ? parsed : parsed.threads
    assert.ok(Array.isArray(threads), 'json is an array or {threads:[...]}')
    // window membership is relative to the real current date, so the count is not asserted here
    // (the lifecycle tests pin classification against a fixed anchor); shape is always checked.
    const VALID = new Set(['new', 'open', 'closed'])
    for (const t of threads) {
      assert.ok(VALID.has(t.status), `status must be new|open|closed, got ${t.status}`)
      assert.ok(Array.isArray(t.files), 'each thread lists the files it touched')
    }
  } finally { cleanup(dbPath) }
})
