// threads.test.mjs - tests for the work-thread rollup engine (lib/threads.mjs) over committed
// fixtures with FIXED dates. Classification is time-relative, so the tests pin a fixed reference
// `now` (the fixtures never age out, unlike a wall-clock run). Run: node --test tests/*.test.mjs
//
// Fixture lifecycles (relative to NOW = 2026-06-22):
//   threads-foo / CHECKOUT_FLOW   closed            (2 sessions, success, quiet ~13d)
//   threads-foo / PAYMENT_RETRY   closed + reverted (work then `git revert`, ~10d)
//   threads-bar / SEARCH_INDEX    open              (2 sessions, last ~2d, still failing)
//   threads-bar / NOTIF_BADGE     new               (1 session ~1d)

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { rmSync, mkdtempSync, mkdirSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { tmpdir } from 'node:os'
import { openDb } from '../scripts/lib/db.mjs'
import { indexCorpus } from '../scripts/lib/indexer.mjs'
import { computeThreads, renderDigest, threadsJson } from '../scripts/lib/threads.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const FIX = join(ROOT, 'fixtures')
const NOW = Date.parse('2026-06-22T12:00:00Z')   // fixed reference so fixed-date fixtures never age out

// Index the two committed fixture projects into a throwaway corpus, then compute threads at NOW.
function build() {
  const db = openDb(join(tmpdir(), `wt-threads-test-${process.pid}-${Math.floor(NOW)}.db`))
  for (const t of ['sessions', 'beats', 'commands', 'grams', 'meta_hits']) db.exec(`DELETE FROM ${t}`)
  const r = indexCorpus(db, {
    dirs: [join(FIX, 'threads-foo', '.specstory', 'history'), join(FIX, 'threads-bar', '.specstory', 'history')],
    maxBytes: 200_000_000, days: 0, force: true,
  })
  assert.equal(r.error, undefined, 'indexing fixtures should not error')
  const threads = computeThreads(db, { now: NOW, days: 7 })
  db.close()
  return threads
}

const bySymbol = (threads, sym) => threads.filter((t) => t.symbols.includes(sym) || t.title.includes(sym))

test('threads: a line of work spanning multiple sessions merges into ONE thread', () => {
  const threads = build()
  const checkout = bySymbol(threads, 'CHECKOUT_FLOW')
  assert.equal(checkout.length, 1, 'the two CHECKOUT_FLOW sessions must collapse to one thread')
  assert.equal(checkout[0].sessions, 2, 'thread should span both sessions')
  // it must not absorb the sibling PAYMENT_RETRY thread in the same project
  assert.ok(!checkout[0].symbols.includes('PAYMENT_RETRY'), 'CHECKOUT_FLOW must not over-merge PAYMENT_RETRY')
})

test('threads: grouping is per project', () => {
  const threads = build()
  const fooProjects = new Set(bySymbol(threads, 'CHECKOUT_FLOW').concat(bySymbol(threads, 'PAYMENT_RETRY')).map((t) => t.project))
  const barProjects = new Set(bySymbol(threads, 'SEARCH_INDEX').concat(bySymbol(threads, 'NOTIF_BADGE')).map((t) => t.project))
  assert.deepEqual([...fooProjects], ['threads-foo'])
  assert.deepEqual([...barProjects], ['threads-bar'])
  // every thread carries exactly one project
  for (const t of threads) assert.equal(typeof t.project, 'string')
})

test('threads: closed classification (success, quiet >= 3 days, within 30 days)', () => {
  const threads = build()
  const t = bySymbol(threads, 'CHECKOUT_FLOW')[0]
  assert.equal(t.status, 'closed')
  assert.equal(t.reverted, false)
  assert.equal(t.lastActivity, '2026-06-09')
})

test('threads: closed + reverted when a beat ran a revert command', () => {
  const threads = build()
  const t = bySymbol(threads, 'PAYMENT_RETRY')[0]
  assert.equal(t.status, 'closed')
  assert.equal(t.reverted, true, 'a `git revert` in the thread must set the reverted flag')
})

test('threads: open classification (unresolved, recent activity, older than the new window)', () => {
  const threads = build()
  const t = bySymbol(threads, 'SEARCH_INDEX')[0]
  assert.equal(t.status, 'open')
  assert.equal(t.reverted, false)
  assert.equal(t.sessions, 2)
})

test('threads: new classification (first activity within the last 7 days)', () => {
  const threads = build()
  const t = bySymbol(threads, 'NOTIF_BADGE')[0]
  assert.equal(t.status, 'new')
})

test('threads: digest prints all three section headers per project and is deterministic', () => {
  const threads = build()
  const d1 = renderDigest(threads, { days: 7 })
  const d2 = renderDigest(build(), { days: 7 })
  assert.equal(d1, d2, 'digest must be byte-identical across runs on the same corpus')
  for (const h of ['New', 'Open', 'Recently closed']) assert.ok(d1.includes(h), `digest missing header: ${h}`)
  assert.ok(d1.includes('reverted'), 'a reverted thread should carry the reverted marker')
})

test('threads: --json shape carries project, status, reverted, and files', () => {
  const json = threadsJson(build())
  assert.ok(Array.isArray(json))
  const STATUS = new Set(['new', 'open', 'closed'])
  for (const t of json) {
    assert.equal(typeof t.project, 'string')
    assert.ok(STATUS.has(t.status))
    assert.equal(typeof t.reverted, 'boolean')
    assert.ok(Array.isArray(t.files))
  }
})

// --- Clustering-quality guarantees (built from synthetic stress corpora in a temp dir).
// These lock in the real-corpus fixes: within-session collapse (no fragmentation), no
// bridging on ubiquitous keys / plain words (no over-merge), a bounded session cap, and
// strict per-project scoping. `projects` maps a name to sessions; a session is { date,
// turns:[{ intent, files }] }.
let SYN = 0
function synThreads(projects) {
  const root = mkdtempSync(join(tmpdir(), 'wt-syn-'))
  const dirs = []
  for (const [proj, sessions] of Object.entries(projects)) {
    const hist = join(root, proj, '.specstory', 'history')
    mkdirSync(hist, { recursive: true })
    dirs.push(hist)
    for (const s of sessions) {
      SYN++
      const uuid = `aaaaaaaa-0000-4000-8000-${String(SYN).padStart(12, '0')}`
      let body = `## ${s.date} 09:00:00Z\n\n<!-- Claude Code Session ${uuid} (${s.date}_09-00-00Z) -->\n`
      for (const t of s.turns) {
        const edits = t.files.map((f) => `Tool use: **Edit** \`${f}\``).join('\n\n')
        body += `\n_**User**_\n\n${t.intent}\n\n---\n\n_**Agent (claude-opus-4-20250514)**_\n\n${edits}\n`
      }
      writeFileSync(join(hist, `${s.date}_09-${String(SYN).padStart(2, '0')}-00Z-s.md`), body)
    }
  }
  const db = openDb(join(tmpdir(), `wt-syn-db-${process.pid}-${SYN}.db`))
  for (const t of ['sessions', 'beats', 'commands', 'grams', 'meta_hits']) db.exec(`DELETE FROM ${t}`)
  indexCorpus(db, { dirs, maxBytes: 200_000_000, days: 0, force: true })
  const threads = computeThreads(db, { now: NOW, days: 7 })
  db.close()
  return threads
}

test('threads: a long single session collapses into ONE thread (no per-prompt fragmentation)', () => {
  const turns = Array.from({ length: 10 }, (_, i) => ({ intent: `edit slide ${i}`, files: [`slides/slide-${i}.md`] }))
  const threads = synThreads({ deck: [{ date: '2026-06-20', turns }] })
  assert.equal(threads.length, 1, 'one session of many prompts must be exactly one thread')
  assert.equal(threads[0].beats, 10)
})

test('threads: unrelated lines do not merge on a ubiquitous file or a plain word, and stay under the session cap', () => {
  // 12 unrelated single-session lines; each also touches package.json and says "update".
  const sessions = Array.from({ length: 12 }, (_, i) => ({
    date: '2026-06-1' + (i % 9),
    turns: [{ intent: `update the Feature_${i} module`, files: [`src/Feature_${i}.ts`, 'package.json'] }],
  }))
  const threads = synThreads({ app: sessions })
  assert.ok(threads.length >= 10, `expected ~12 separate threads, got ${threads.length} (over-merged on package.json / "update")`)
  assert.ok(threads.every((t) => t.sessions <= 5), 'no thread may exceed the session cap')
})

test('threads: never merges across projects, even on an identical symbol and file', () => {
  const line = [{ date: '2026-06-20', turns: [{ intent: 'build the Shared_Widget', files: ['src/Shared_Widget.ts'] }] },
                { date: '2026-06-21', turns: [{ intent: 'finish the Shared_Widget', files: ['src/Shared_Widget.ts'] }] }]
  const threads = synThreads({ alpha: line, beta: line })
  const widget = threads.filter((t) => JSON.stringify(t).includes('Shared_Widget'))
  assert.equal(new Set(widget.map((t) => t.project)).size, 2, 'the two projects must stay as separate threads')
  for (const t of threads) assert.ok(t.project === 'alpha' || t.project === 'beta')
})
