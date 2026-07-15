// arcs.mjs - cluster candidates + seeds into per-entity (or per-file) process arcs.
//
// An arc is a timeline of decision beats (proposed → deliberated → chosen → reversed → ...)
// anchored by a shared entity OR shared file/topic. Commit seeds are `chosen` beats;
// chat candidates are everything else.
//
// Arc state:
//   in-formation — deliberation/proposal beats but no chosen beat
//   decided      — has a chosen beat, no later reversal
//   changed      — a later chosen supersedes an earlier one
//   abandoned    — open question, stale (no beats in a long time)

import { relative } from 'node:path'

const clip = (s, n = 160) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

// Disjoint-set for clustering.
function makeDSU(n) {
  const p = Array.from({ length: n }, (_, i) => i)
  const find = (x) => { while (p[x] !== x) { p[x] = p[p[x]]; x = p[x] } return x }
  const union = (a, b) => { const ra = find(a), rb = find(b); if (ra !== rb) p[Math.max(ra, rb)] = Math.min(ra, rb) }
  return { find, union }
}

// Exported: cluster candidates + seeds into arcs. Populates the arcs table.
// Returns { arcs: N, byState: {...} }
export function buildArcs(db) {
  // gather all beats: candidates (chat) + seeds (commits, as chosen beats)
  const beats = []
  for (const c of db.prepare('SELECT id, session_id, ord, line, project, date, role, entity, quote, signals, evidence FROM candidates').all()) {
    beats.push({ ...c, kind: 'candidate', role: c.role || 'proposed' })
  }
  for (const s of db.prepare('SELECT beat_id, session_id, line, project, date, message, entity, evidence FROM seeds').all()) {
    // detect revert commits
    const isRevert = /^revert/i.test(s.message) || /\brevert\b/i.test(s.message)
    beats.push({
      id: null, session_id: s.session_id, ord: 9999, line: s.line, project: s.project, date: s.date,
      role: isRevert ? 'reversed' : 'chosen', entity: s.entity,
      quote: clip(s.message), signals: '["commit"]', evidence: s.evidence, kind: 'seed',
    })
  }

  // sort by project, date, session, ord (seeds sort by line within their session)
  beats.sort((a, b) => a.project.localeCompare(b.project) || a.date.localeCompare(b.date) ||
    a.session_id.localeCompare(b.session_id) || (a.line - b.line))

  // cluster by shared entity, per project. Entity-less beats form singleton arcs.
  const ENTITY_MAX = 5  // ubiquity cap for clustering (different from fingerprint cap)
  const entityBeats = new Map()
  for (const b of beats) {
    if (!b.entity) continue
    const k = b.project + '\t' + b.entity.toLowerCase()
    if (!entityBeats.has(k)) entityBeats.set(k, [])
    entityBeats.get(k).push(b)
  }
  const ubiquitous = new Set()
  for (const [k, arr] of entityBeats) if (arr.length > ENTITY_MAX * 3) ubiquitous.add(k)  // too many → probably generic

  const dsu = makeDSU(beats.length)
  const owner = new Map()
  beats.forEach((b, i) => {
    if (!b.entity) return
    const k = b.project + '\t' + b.entity.toLowerCase()
    if (ubiquitous.has(k)) return
    if (owner.has(k)) dsu.union(i, owner.get(k))
    else owner.set(k, i)
  })

  const clusters = new Map()
  beats.forEach((b, i) => {
    const root = dsu.find(i)
    if (!clusters.has(root)) clusters.set(root, [])
    clusters.get(root).push(b)
  })

  // build arcs
  db.prepare('DELETE FROM arcs').run()
  const ins = db.prepare('INSERT INTO arcs(project, entity, state, first_date, last_date, reversals, beat_ids) VALUES(?,?,?,?,?,?,?)')
  const byState = {}
  let arcCount = 0

  for (const members of clusters.values()) {
    members.sort((a, b) => a.date.localeCompare(b.date) || a.line - b.line)
    const entity = members.find(m => m.entity)?.entity || '(unnamed)'
    const project = members[0].project
    const firstDate = members[0].date
    const lastDate = members[members.length - 1].date

    const chosen = members.filter(m => m.role === 'chosen')
    const reversals = Math.max(0, chosen.length - 1)
    const hasReversal = members.some(m => m.role === 'reversed')
    const hasDeliberation = members.some(m => m.role === 'deliberated' || m.role === 'proposed')

    let state
    if (chosen.length === 0 && hasDeliberation) state = 'in-formation'
    else if (hasReversal || reversals >= 1) state = 'changed'
    else if (chosen.length > 0) state = 'decided'
    else state = 'abandoned'

    const beatIds = members.map(m => ({ role: m.role, quote: m.quote, date: m.date, evidence: m.evidence, entity: m.entity }))
    ins.run(project, entity, state, firstDate, lastDate, reversals, JSON.stringify(beatIds))
    byState[state] = (byState[state] || 0) + 1
    arcCount++
  }

  return { arcs: arcCount, byState }
}

// Exported: render a digest of arcs (the report skeleton).
export function renderDigest(db, { days = 0 } = {}) {
  const arcs = db.prepare('SELECT * FROM arcs ORDER BY project, state, last_date').all()
  const projects = new Set(arcs.map(a => a.project))
  const L = []
  L.push(`decisions2 report - ${arcs.length} arc(s) across ${projects.size} project(s) (window: ${days > 0 ? `last ${days} days` : 'all time'})`)
  L.push('')

  const stateLabel = { 'in-formation': 'In formation (deliberating)', decided: 'Decided', changed: 'Changed', abandoned: 'Abandoned' }
  const byProject = new Map()
  for (const a of arcs) {
    if (!byProject.has(a.project)) byProject.set(a.project, [])
    byProject.get(a.project).push(a)
  }

  for (const name of [...byProject.keys()].sort()) {
    L.push(`## ${name}`)
    const list = byProject.get(name)
    for (const [label, state] of Object.entries(stateLabel)) {
      const items = list.filter(a => a.state === label)
      if (!items.length) continue
      L.push(`  ${label}`)
      for (const a of items) {
        const beats = JSON.parse(a.beat_ids)
        L.push(`    - ${a.entity} (${a.first_date} → ${a.last_date}, ${beats.length} beat(s)${a.reversals ? `, ${a.reversals} reversal(s)` : ''})`)
        for (const b of beats.slice(0, 5)) {
          L.push(`        [${b.role}] "${clip(b.quote, 100)}" · ${b.date} · ${b.evidence}`)
        }
        if (beats.length > 5) L.push(`        (+${beats.length - 5} more)`)
      }
      L.push('')
    }
  }
  return L.join('\n').replace(/\n+$/, '\n')
}
