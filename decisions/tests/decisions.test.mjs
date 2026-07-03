// decisions.test.mjs - tests for the decision-mining engine (lib/decide.mjs) over committed
// fixtures with FIXED dates. Extraction is date-relative only for windowing, so tests pin a
// fixed reference `now`. Run: node --test tests/*.test.mjs
//
// Fixture decisions (relative to NOW = 2026-06-24):
//   decisions-alpha / EventStore   Postgres (06-02) -> SQLite (06-09) -> DuckDB (06-15)
//                                  = 2 changed + 1 decided, churn hotspot (2 reversals)
//   decisions-alpha / AuthClient   "for now, keep the API key..." = decided + provisional debt
//   decisions-beta  / SettingsPanel  "should we use tabs or a sidebar...?" = open
//   decisions-beta  / FlagParser   "go with kebab-case..." = decided

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { tmpdir } from 'node:os'
import { openDb } from '../scripts/lib/db.mjs'
import { indexCorpus } from '../scripts/lib/indexer.mjs'
import { computeDecisions, renderDigest } from '../scripts/lib/decide.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const FIX = join(ROOT, 'fixtures')
const NOW = Date.parse('2026-06-24T12:00:00Z')

function build(days = 0) {
  const db = openDb(join(tmpdir(), `decisions-test-${process.pid}-${days}.db`))
  for (const t of ['sessions', 'beats', 'commands', 'grams', 'meta_hits']) db.exec(`DELETE FROM ${t}`)
  const r = indexCorpus(db, {
    dirs: [join(FIX, 'decisions-alpha', '.specstory', 'history'), join(FIX, 'decisions-beta', '.specstory', 'history')],
    maxBytes: 200_000_000, days: 0, force: true,
  })
  assert.equal(r.error, undefined, 'indexing fixtures should not error')
  const result = computeDecisions(db, { now: NOW, days })
  db.close()
  return result
}

const byEntity = (result, e) => result.decisions.filter((d) => d.entity === e)

test('decisions: a decision chain on one entity yields changed -> changed -> decided', () => {
  const r = build()
  const es = byEntity(r, 'EventStore')
  assert.equal(es.length, 3, 'three EventStore decisions')
  const decided = es.filter((d) => d.status === 'decided')
  const changed = es.filter((d) => d.status === 'changed')
  assert.equal(decided.length, 1)
  assert.equal(changed.length, 2)
  assert.ok(decided[0].summary.includes('DuckDB'), 'the standing decision is the latest (DuckDB)')
  // supersession chain links each superseded decision to its successor
  const postgres = changed.find((d) => d.summary.includes('Postgres'))
  assert.ok(postgres.supersededBy.includes('SQLite'), 'Postgres superseded by SQLite')
  const sqlite = changed.find((d) => d.summary.includes('SQLite'))
  assert.ok(sqlite.supersededBy.includes('DuckDB'), 'SQLite superseded by DuckDB')
})

test('decisions: a provisional "for now" decision is decided + flagged + surfaces as debt', () => {
  const r = build()
  const ac = byEntity(r, 'AuthClient')
  assert.equal(ac.length, 1)
  assert.equal(ac[0].status, 'decided')
  assert.equal(ac[0].provisional, true)
  assert.ok(r.insights.provisional.some((p) => p.entity === 'AuthClient'), 'AuthClient in provisional debt')
})

test('decisions: an unresolved question is open', () => {
  const r = build()
  const sp = byEntity(r, 'SettingsPanel')
  assert.equal(sp.length, 1)
  assert.equal(sp[0].status, 'open')
})

test('decisions: a plain firm decision is decided', () => {
  const r = build()
  const fp = byEntity(r, 'FlagParser')
  assert.equal(fp.length, 1)
  assert.equal(fp[0].status, 'decided')
  assert.equal(fp[0].provisional, false)
})

test('decisions: churn hotspot fires at >= 2 reversals, with the chain', () => {
  const r = build()
  assert.equal(r.insights.churn.length, 1)
  const c = r.insights.churn[0]
  assert.equal(c.entity, 'EventStore')
  assert.equal(c.reversals, 2)
  assert.equal(c.chain.length, 3)
})

test('decisions: entities cluster per project and carry evidence refs', () => {
  const r = build()
  for (const d of r.decisions) {
    assert.ok(['decisions-alpha', 'decisions-beta'].includes(d.project))
    assert.match(d.evidence, /\.md:\d+$/, 'evidence is path:line')
  }
  const projectsOf = (e) => new Set(byEntity(r, e).map((d) => d.project))
  assert.deepEqual([...projectsOf('EventStore')], ['decisions-alpha'])
  assert.deepEqual([...projectsOf('SettingsPanel')], ['decisions-beta'])
})

test('decisions: digest prints insights + per-project sections and is deterministic', () => {
  const r1 = build(), r2 = build()
  const d1 = renderDigest(r1), d2 = renderDigest(r2)
  assert.equal(d1, d2, 'digest must be byte-identical across runs')
  for (const h of ['Insights', 'Churn hotspots', 'Provisional decisions', 'Re-litigated', 'Decided', 'Changed', 'Open']) {
    assert.ok(d1.includes(h), `digest missing section: ${h}`)
  }
  assert.ok(d1.includes('superseded by:'), 'changed decisions show their successor')
})

test('decisions: --days windowing filters by date but supersession still holds', () => {
  // window of 17 days from NOW (2026-06-24) cuts at 06-07: excludes only the 06-02 Postgres decision
  const r = build(17)
  const es = byEntity(r, 'EventStore')
  assert.equal(es.length, 2, 'Postgres (06-02) outside the window')
  assert.ok(es.some((d) => d.status === 'decided' && d.summary.includes('DuckDB')))
})

test('decisions: json result shape', () => {
  const r = build()
  assert.ok(Array.isArray(r.decisions))
  assert.ok(r.insights && Array.isArray(r.insights.churn) && Array.isArray(r.insights.provisional) && Array.isArray(r.insights.reopened))
  const STATUS = new Set(['decided', 'changed', 'open'])
  for (const d of r.decisions) {
    assert.equal(typeof d.project, 'string')
    assert.ok(STATUS.has(d.status))
    assert.equal(typeof d.provisional, 'boolean')
    assert.equal(typeof d.entity, 'string')
    assert.ok(Array.isArray(d.entities))
    assert.equal(typeof d.summary, 'string')
    assert.equal(typeof d.evidence, 'string')
  }
})
