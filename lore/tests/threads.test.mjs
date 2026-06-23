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

// ---------- anti-merge: ubiquitous files, cross-project paths, synthetic turns ----------

function emptyDb(slug) {
  const dbPath = join(tmpdir(), `lore-threads-${slug}-${process.pid}.db`)
  for (const suf of ['', '-wal', '-shm']) rmSync(dbPath + suf, { force: true })
  return dbPath
}
function addSession(db, id, project, date) {
  db.prepare('INSERT INTO sessions(id,project_id,project_name,path,date,agent,size,beats) VALUES(?,?,?,?,?,?,?,?)')
    .run(id, project, project, 'history/' + id, date, 'claude-code', 1, 1)
}
function addBeat(db, sessionId, ord, intent, files, outcome = 'neutral') {
  db.prepare('INSERT INTO beats(session_id,ord,start_line,intent_raw,intent_sig,n_tools,tool_mix,files,n_cmds,exit_fails,outcome) VALUES(?,?,?,?,?,?,?,?,?,?,?)')
    .run(sessionId, ord, 10 + ord, intent, null, 1, 'edit:1', files.join(','), 0, 0, outcome)
}

test('threads: a ubiquitous file (package.json) does NOT merge two unrelated threads', () => {
  const dbPath = emptyDb('ubiq')
  try {
    const db = openDb(dbPath)
    // Six sessions in one project, ALL touching package.json. A naive engine links every one of
    // them through that shared file into a single mega-bucket; the refined engine treats a file in
    // most sessions as too common to link, so the three feature lines stay distinct.
    const date = '2026-03-28'
    const feats = [['CartView', 'src/Cart.ts'], ['SearchIndex', 'src/Search.ts'], ['ProfilePanel', 'src/Profile.ts']]
    let n = 0
    for (const [ident, file] of feats) {
      for (const pass of ['implement', 'finish']) {
        n++
        const sid = `shop/s${n}.md`
        addSession(db, sid, 'shop', date)
        addBeat(db, sid, 0, `${pass} the ${ident} view`, [file, 'package.json'])
      }
    }
    const { threads } = buildThreads(db, { nowMs: NOW })
    const cart = touching(threads, 'src/Cart.ts')
    const search = touching(threads, 'src/Search.ts')
    assert.equal(cart.length, 1, 'cart work is exactly one thread')
    assert.equal(cart[0].sessions, 2, 'its two sessions merged via the real shared file, not package.json')
    assert.equal(search.length, 1, 'search work is exactly one thread')
    assert.notEqual(cart[0].id, search[0].id, 'package.json must not bridge cart and search')
    assert.ok(!JSON.stringify(cart[0]).includes('Search.ts'), 'cart thread did not absorb the search work')
    assert.ok(cart[0].files.includes('package.json'), 'the ubiquitous file is still shown as evidence')
    // No mega-bucket: each of the three feature lines is its own thread.
    assert.ok(threads.length >= 3, `expected at least three distinct threads, got ${threads.length}`)
  } finally { cleanup(dbPath) }
})

test('threads: the SAME relative path in two projects stays SEPARATE', () => {
  const dbPath = emptyDb('xproj')
  try {
    const db = openDb(dbPath)
    // src/config.ts exists in both repos. A naive engine bridges the two projects through the
    // identical relative path; project-scoped links keep them apart.
    addSession(db, 'alpha/s1.md', 'alpha', '2026-03-29')
    addBeat(db, 'alpha/s1.md', 0, 'wire up the AlphaWidget dashboard', ['src/config.ts', 'src/Alpha.ts'])
    addSession(db, 'beta/s1.md', 'beta', '2026-03-29')
    addBeat(db, 'beta/s1.md', 0, 'wire up the BetaWidget dashboard', ['src/config.ts', 'src/Beta.ts'])

    const { threads } = buildThreads(db, { nowMs: NOW })
    const alpha = touching(threads, 'src/Alpha.ts')
    const beta = touching(threads, 'src/Beta.ts')
    assert.equal(alpha.length, 1)
    assert.equal(beta.length, 1)
    assert.notEqual(alpha[0].id, beta[0].id, 'the shared relative path must not bridge the two projects')
    assert.ok(!JSON.stringify(alpha[0]).includes('Beta.ts'), 'alpha thread did not absorb the beta work')
    assert.equal(touching(threads, 'src/config.ts').length, 2, 'both threads list config.ts but stay separate')
  } finally { cleanup(dbPath) }
})

test('threads: same non-generic file stem in two projects stays SEPARATE', () => {
  const dbPath = emptyDb('xproj-stem')
  try {
    const db = openDb(dbPath)
    // This catches the stem-link variant of the cross-project merge bug: even if the full file
    // path token is scoped, a global stem token for checkout-flow would still bridge repos.
    addSession(db, 'store/s1.md', 'store', '2026-03-29')
    addBeat(db, 'store/s1.md', 0, 'wire up the StoreCheckout panel', ['src/checkout-flow.ts'])
    addSession(db, 'admin/s1.md', 'admin', '2026-03-29')
    addBeat(db, 'admin/s1.md', 0, 'wire up the AdminCheckout panel', ['src/checkout-flow.ts'])

    const { threads } = buildThreads(db, { nowMs: NOW })
    const checkout = touching(threads, 'src/checkout-flow.ts')
    assert.equal(checkout.length, 2, 'same relative path and stem appear as evidence in two separate threads')
    assert.notEqual(checkout[0].id, checkout[1].id, 'the shared stem must not bridge the two projects')
    assert.ok(checkout.every(t => t.projects.length === 1), 'each thread belongs to one project')
  } finally { cleanup(dbPath) }
})

test('threads: a <local-command-stdout> turn forms NO thread and never names one', () => {
  const dbPath = emptyDb('synthetic')
  try {
    const db = openDb(dbPath)
    addSession(db, 'noise/s1.md', 'noise', '2026-03-30')
    // A slash-command output turn that also ran a command - a naive engine seeds a junk thread
    // named after the stray verb "set".
    const res = db.prepare('INSERT INTO beats(session_id,ord,start_line,intent_raw,intent_sig,n_tools,tool_mix,files,n_cmds,exit_fails,outcome) VALUES(?,?,?,?,?,?,?,?,?,?,?)')
      .run('noise/s1.md', 0, 10, '<local-command-stdout>Set model to opus</local-command-stdout>', null, 1, 'shell:1', '', 1, 0, 'neutral')
    db.prepare('INSERT INTO commands(beat_id,ord,head,raw,line) VALUES(?,?,?,?,?)').run(res.lastInsertRowid, 0, 'echo', 'echo noop', 20)

    const { threads } = buildThreads(db, { nowMs: NOW })
    assert.equal(threads.length, 0, 'the synthetic turn seeds no thread')
    assert.ok(!threads.some(t => /local-command-stdout/i.test(JSON.stringify(t))), 'no thread carries the synthetic marker')
    assert.ok(!threads.some(t => t.name === 'set'), 'no thread is named after the stray verb')
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
