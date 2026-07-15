// decisions2.test.mjs - tests for the multi-pass decision mining engine.
//
// Fixture (decisions2-alpha) encodes a full process arc on EventStore:
//   06-02: deliberation (Postgres or SQLite?) -> proposed (go with Postgres) -> chosen (commit)
//   06-09: reversed (switch to SQLite) -> chosen (commit)
//   = an arc with state "changed", 2 chosen beats, 1 reversed
// Plus:
//   AuthClient: provisional "for now" -> in-formation arc (no commit)
//   SettingsPanel: deliberation question -> in-formation arc (no commit)
//
// Run: node --test tests/*.test.mjs

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { tmpdir } from 'node:os'
import { openDb } from '../scripts/lib/db.mjs'
import { indexCorpus } from '../scripts/lib/indexer.mjs'
import { extractSeeds } from '../scripts/lib/commit_seed.mjs'
import { buildFingerprints } from '../scripts/lib/fingerprint.mjs'
import { expand } from '../scripts/lib/expand.mjs'
import { buildArcs } from '../scripts/lib/arcs.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const FIX = join(ROOT, 'fixtures')

function build() {
  const db = openDb(join(tmpdir(), `decisions2-test-${process.pid}.db`))
  const r = indexCorpus(db, {
    dirs: [join(FIX, 'decisions2-alpha', '.specstory', 'history')],
    maxBytes: 200_000_000, days: 0, force: true,
  })
  assert.equal(r.error, undefined, 'indexing fixtures should not error')
  return db
}

function runPipeline(db) {
  extractSeeds(db)
  buildFingerprints(db)
  expand(db)
  return buildArcs(db)
}

// ── Pass 1: seeds ──────────────────────────────────────────────────────────────

test('Pass 1: extracts decision-shaped commit messages as seeds', () => {
  const db = build()
  const r = extractSeeds(db)
  assert.ok(r.seeds >= 2, `at least 2 seeds (Postgres + SQLite commits), got ${r.seeds}`)
  const seeds = db.prepare('SELECT message, entity, verb, type FROM seeds ORDER BY date').all()
  // both EventStore commits should be seeds
  const postgres = seeds.find(s => /Postgres/i.test(s.message))
  const sqlite = seeds.find(s => /SQLite/i.test(s.message))
  assert.ok(postgres, 'Postgres commit is a seed')
  assert.ok(sqlite, 'SQLite commit is a seed')
  assert.match(postgres.message, /Postgres/i)
  assert.match(sqlite.message, /SQLite/i)
  // conventional-commit scope is the entity
  assert.equal(postgres.entity, 'eventstore')
  assert.equal(sqlite.entity, 'eventstore')
  db.close()
})

test('Pass 1: commit verbs are extracted from the message body', () => {
  const db = build()
  extractSeeds(db)
  const seeds = db.prepare('SELECT verb FROM seeds').all()
  // verb is extracted from the body after the conv prefix ("use Postgres..." -> "use")
  assert.ok(seeds.some(s => s.verb), 'commit verbs extracted')
  db.close()
})

// ── Pass 2: fingerprints ───────────────────────────────────────────────────────

test('Pass 2: builds per-project entity fingerprint from seeds', () => {
  const db = build()
  extractSeeds(db)
  const r = buildFingerprints(db)
  const proj = r.projects.find(p => p.entities.includes('eventstore'))
  assert.ok(proj, 'eventstore is in the fingerprint entity set (from conventional-commit scope)')
  db.close()
})

// ── Pass 3: expand (candidates with roles) ─────────────────────────────────────

test('Pass 3: assigns role=proposed to firm directions', () => {
  const db = build()
  runPipeline(db)
  const cands = db.prepare("SELECT role, quote FROM candidates WHERE quote LIKE '%go with Postgres%'").all()
  assert.ok(cands.length > 0, 'the "go with Postgres" chat turn is a candidate')
  assert.equal(cands[0].role, 'proposed', 'firm direction = proposed')
  db.close()
})

test('Pass 3: assigns role=deliberated to question-form turns with decision content', () => {
  const db = build()
  runPipeline(db)
  const cands = db.prepare("SELECT role, quote FROM candidates WHERE quote LIKE '%tabs or a sidebar%'").all()
  assert.ok(cands.length > 0, 'the SettingsPanel question is a candidate')
  assert.equal(cands[0].role, 'deliberated', 'question with decision content = deliberated')
  db.close()
})

test('Pass 3: assigns role=reversed to change signals', () => {
  const db = build()
  runPipeline(db)
  const cands = db.prepare("SELECT role, quote FROM candidates WHERE quote LIKE '%switch the EventStore%'").all()
  assert.ok(cands.length > 0, 'the "switch the EventStore to SQLite" turn is a candidate')
  assert.equal(cands[0].role, 'reversed', 'change/switch signal = reversed')
  db.close()
})

test('Pass 3: assigns role=provisional to "for now" turns', () => {
  const db = build()
  runPipeline(db)
  const cands = db.prepare("SELECT role, quote FROM candidates WHERE quote LIKE '%for now%' AND quote LIKE '%AuthClient%'").all()
  assert.ok(cands.length > 0, 'the AuthClient "for now" turn is a candidate')
  assert.equal(cands[0].role, 'provisional', '"for now" = provisional')
  db.close()
})

// ── Arcs: process arcs with states ─────────────────────────────────────────────

test('arcs: EventStore arc has state=changed (Postgres -> SQLite)', () => {
  const db = build()
  runPipeline(db)
  const arcs = db.prepare("SELECT state, reversals, beat_ids FROM arcs WHERE entity = 'eventstore'").all()
  assert.ok(arcs.length >= 1, 'an EventStore arc exists')
  // the arc should be "changed" (SQLite commit supersedes Postgres commit)
  const changed = arcs.find(a => a.state === 'changed')
  assert.ok(changed, 'EventStore arc is in state "changed"')
  assert.ok(changed.reversals >= 1, 'at least 1 reversal (Postgres -> SQLite)')
  const beats = JSON.parse(changed.beat_ids)
  // the arc should contain both a "proposed" (chat) and "chosen" (commit) beat
  const roles = beats.map(b => b.role)
  assert.ok(roles.includes('chosen'), 'arc has a chosen beat (commit)')
  assert.ok(roles.includes('proposed') || roles.includes('reversed'), 'arc has a chat beat')
  db.close()
})

test('arcs: AuthClient arc has state=in-formation (provisional, no commit)', () => {
  const db = build()
  runPipeline(db)
  // AuthClient has a provisional beat but no commit -> in-formation
  const arcs = db.prepare("SELECT state, beat_ids FROM arcs WHERE entity LIKE '%authclient%' OR beat_ids LIKE '%AuthClient%'").all()
  assert.ok(arcs.length >= 1, 'an AuthClient arc exists')
  const inForm = arcs.find(a => a.state === 'in-formation' || a.state === 'abandoned')
  assert.ok(inForm, 'AuthClient arc is in-formation or abandoned (no commit)')
  db.close()
})

test('arcs: SettingsPanel arc has state=in-formation (deliberation, no commit)', () => {
  const db = build()
  runPipeline(db)
  const arcs = db.prepare("SELECT state, beat_ids FROM arcs WHERE beat_ids LIKE '%tabs or a sidebar%'").all()
  assert.ok(arcs.length >= 1, 'a SettingsPanel arc exists')
  assert.equal(arcs[0].state, 'in-formation', 'deliberation with no commit = in-formation')
  db.close()
})

test('arcs: the full pipeline is deterministic (re-run produces identical arcs)', () => {
  const db = build()
  runPipeline(db)
  const arcs1 = db.prepare('SELECT project, entity, state, beat_ids FROM arcs ORDER BY entity').all()
  runPipeline(db)  // re-run (tables cleared by pipeline)
  const arcs2 = db.prepare('SELECT project, entity, state, beat_ids FROM arcs ORDER BY entity').all()
  assert.equal(arcs1.length, arcs2.length, 'same arc count')
  for (let i = 0; i < arcs1.length; i++) {
    assert.equal(arcs1[i].entity, arcs2[i].entity, `arc ${i} entity stable`)
    assert.equal(arcs1[i].state, arcs2[i].state, `arc ${i} state stable`)
    assert.equal(arcs1[i].beat_ids, arcs2[i].beat_ids, `arc ${i} beats stable`)
  }
  db.close()
})
