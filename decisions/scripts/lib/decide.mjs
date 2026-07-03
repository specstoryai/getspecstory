// decide.mjs - the decision-mining engine (query-time lens over the indexed corpus).
//
// Deterministic and high-RECALL: signal grammars find decision candidates in user turns,
// entities are extracted from the matching sentence, candidates cluster into per-entity
// timelines (per project), and supersession/status falls out of the timeline order. Every
// candidate carries its QUOTE and evidence ref so the agent (SKILL.md) can do the
// high-PRECISION pass - dropping false positives and naming decisions - without reading
// raw transcripts. No LLM or network call in this module.
//
// STATUS MODEL (one per decision):
//   decided - a firm choice, still standing (the latest firm decision on its entity)
//   changed - superseded by a later firm decision on the same entity (chained)
//   open    - raised but unresolved: a question, or provisional ("for now") with no
//             later firm decision on the entity
// Flags: provisional ("for now"/TBD language), reopened (a later open question on an
// entity that already had a firm decision).
//
// INSIGHTS (computed here, deterministically):
//   churn        - entities whose decisions were reversed >= 2 times (design thrash)
//   provisional  - "for now" decisions never revisited (decision debt)
//   reopened     - settled entities re-questioned later (re-litigated decisions)

import { relative } from 'node:path'
import { redactSecrets } from './patterns.mjs'

// --- signal grammars (ordered by precedence: CHANGE > OPEN > DECIDE > PROVISIONAL) ---
// A change is itself a decision (it decides the new thing) AND marks its predecessors.
const CHANGE_RE = /\b(?:actually,?\s+(?:let'?s|we|use|go|switch|make)|instead of what|changed?\s+(?:my|our)\s+mind|scrap\s+that|forget\s+that|on\s+second\s+thought|let'?s\s+not\b|go\s+back\s+to|switch(?:ing)?\s+(?:\S+\s+){0,3}?(?:back\s+)?to\b|revert\s+to|no\s+longer\s+(?:use|using)|replace\s+\S+\s+with)/i
const DECIDE_RE = /\b(?:let'?s\s+(?:go\s+with|use|stick\s+with|keep|standardize\s+on|make|call|do)|go(?:ing)?\s+with|we(?:'ll|\s+will)\s+(?:use|go|keep|stick)|decided?\s+(?:on|to|that)|decision\s*:|settle[d]?\s+on|agreed?\s+(?:on|to)\b|standardize\s+on|default\s+to|always\s+use|never\s+use|don'?t\s+use\s+(?:\S+\s+){0,4}?use|rename\s+(?:\S+\s+){0,4}?to\b|call\s+it\s+\S|(?:option|choice)\s+\d|the\s+(?:first|second|third)\s+option|use\s+(?:\S+\s+){0,4}?(?:instead\s+of|rather\s+than))/i
const OPEN_RE = /(?:\bshould\s+(?:we|i|it|this)\b[^.?!\n]*\?|\bwhich\b[^.?!\n]*\?|\bdo\s+we\s+want\b[^.?!\n]*\?|\bwhat\s+about\b[^.?!\n]*\?|\bor\s+should\b[^.?!\n]*\?)/i
// NOTE: "placeholder" is deliberately absent - it is a common UI noun ("placeholder text"),
// not a provisional-decision marker, and it flooded the report with false positives.
const PROVISIONAL_RE = /\b(?:for\s+now|for\s+the\s+moment|temporar(?:y|ily)|as\s+a\s+stopgap|tbd|decide\s+(?:this\s+)?later|punt(?:ing)?\s+on|revisit\s+(?:this\s+)?later)\b/i

// Entity extraction from the matching sentence: distinctive symbols (camelCase,
// snake_case, ALL_CAPS_WITH_UNDERSCORE), backtick spans, and file-like tokens. Plain
// English words are deliberately NOT entities (too generic to cluster on).
const SYMBOL_RE = /[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+|[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+|[A-Z][a-z0-9]+(?:[A-Z][a-z0-9]+)+/g
const FILE_RE = /(?:\.{0,2}\/)?(?:[\w@.-]+\/)+[\w@-]+\.[A-Za-z][\w]{0,9}|[\w@-]+\.[A-Za-z][\w]{0,9}/g
const TICK_RE = /`([^`\n]{2,60})`/g
const NOISE_ENTITY = new Set(['env.local', '.env.local', 'package.json', 'readme.md', 'claude.md'])

const matchAll = (re, s) => { const out = []; if (!s) return out; for (const m of s.matchAll(re)) out.push(m[1] ?? m[0]); return out }

function entitiesOf(sentence) {
  const found = new Set()
  for (const t of matchAll(TICK_RE, sentence)) for (const s of matchAll(SYMBOL_RE, t)) found.add(s)
  for (const s of matchAll(SYMBOL_RE, sentence)) found.add(s)
  for (const f of matchAll(FILE_RE, sentence)) if (!NOISE_ENTITY.has(f.toLowerCase())) found.add(f)
  return [...found].filter((e) => e.length >= 4 && !NOISE_ENTITY.has(e.toLowerCase()))
}

// Split a user turn into sentence-ish units (newlines and terminal punctuation).
function sentences(text) {
  return (text || '').split(/(?<=[.?!])\s+|\n+/).map((s) => s.trim()).filter(Boolean)
}

const clip = (s, n = 160) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

// Disjoint-set for entity clustering.
function makeDSU(n) {
  const p = Array.from({ length: n }, (_, i) => i)
  const find = (x) => { while (p[x] !== x) { p[x] = p[p[x]]; x = p[x] } return x }
  const union = (a, b) => { const ra = find(a), rb = find(b); if (ra !== rb) p[Math.max(ra, rb)] = Math.min(ra, rb) }
  return { find, union }
}

// Pure read of the corpus DB; `now` injectable for deterministic tests.
export function computeDecisions(db, { now = Date.now(), days = 0 } = {}) {
  const rows = db.prepare(`
    SELECT s.id sid, s.project_id pid, s.project_name pname, s.date date, s.path path,
           e.ord ord, e.start_line line, e.intent_raw intent
    FROM beats e JOIN sessions s ON s.id = e.session_id
    WHERE s.date IS NOT NULL AND s.date != '' AND e.intent_raw IS NOT NULL AND e.intent_raw != ''
    ORDER BY s.project_id, s.date, s.id, e.ord
  `).all()

  const cutoff = days > 0 ? new Date(now - days * 86400000).toISOString().slice(0, 10) : null
  const cwd = process.cwd()

  // 1) Extract candidates: one per (beat, matching sentence).
  const cands = []
  for (const r of rows) {
    if (cutoff && r.date < cutoff) continue
    for (const sent of sentences(r.intent)) {
      const change = CHANGE_RE.test(sent)
      const open = OPEN_RE.test(sent)
      const decide = DECIDE_RE.test(sent)
      const provisional = PROVISIONAL_RE.test(sent)
      if (!change && !open && !decide && !provisional) continue
      // precedence: an explicit question is open even if it contains decision verbs
      const kind = change ? 'change' : (open ? 'open' : (decide ? 'decide' : 'provisional'))
      cands.push({
        pid: r.pid, project: r.pname, sid: r.sid, date: r.date, ord: r.ord,
        evidence: `${relative(cwd, r.path) || r.path}:${r.line}`,
        quote: redactSecrets(clip(sent)),
        kind, provisional,
        entities: entitiesOf(sent),
        signals: [change && 'change', open && 'open', decide && 'decide', provisional && 'provisional'].filter(Boolean),
      })
    }
  }
  // 1b) Dedupe: resumed/re-included sessions repeat the same prompt text verbatim,
  //     which would multiply one decision into many. Keep the EARLIEST occurrence of an
  //     identical (project, quote) pair - rows arrive in project/date order.
  const seen = new Set()
  const uniq = []
  for (const c of cands) {
    const k = c.pid + '\t' + c.quote.toLowerCase()
    if (seen.has(k)) continue
    seen.add(k)
    uniq.push(c)
  }
  const list = uniq
  if (!list.length) return { decisions: [], insights: { churn: [], provisional: [], reopened: [] } }

  // 2) Cluster candidates by shared entity, per project. Decisions are sparse, so a
  //    single shared distinctive entity is a legitimate link (unlike work threads) -
  //    EXCEPT ubiquitous entities (a product name appears everywhere and would chain
  //    unrelated decisions). An entity in more than ENTITY_MAX sessions is not a key.
  const ENTITY_MAX = 5
  const entitySids = new Map()
  for (const c of list) for (const e of c.entities) {
    const k = c.pid + '\t' + e.toLowerCase()
    if (!entitySids.has(k)) entitySids.set(k, new Set())
    entitySids.get(k).add(c.sid)
  }
  const ubiquitous = (pid, e) => (entitySids.get(pid + '\t' + e.toLowerCase())?.size || 0) > ENTITY_MAX
  const dsu = makeDSU(list.length)
  const owner = new Map()
  list.forEach((c, i) => {
    for (const e of c.entities) {
      if (ubiquitous(c.pid, e)) continue
      const k = c.pid + '\t' + e.toLowerCase()
      if (owner.has(k)) dsu.union(i, owner.get(k))
      else owner.set(k, i)
    }
  })
  const clusters = new Map()
  list.forEach((c, i) => {
    const root = dsu.find(i)
    if (!clusters.has(root)) clusters.set(root, [])
    clusters.get(root).push(c)
  })

  // 3) Per-cluster timeline -> statuses, supersession chain, flags, insights.
  const decisions = []
  const churn = [], provisionalDebt = [], reopened = []
  for (const members of clusters.values()) {
    members.sort((a, b) => a.date.localeCompare(b.date) || a.sid.localeCompare(b.sid) || a.ord - b.ord)
    // firm = anything that settles a choice (even provisionally); open questions are not firm
    const firm = members.filter((m) => m.kind !== 'open')
    // entity display name: most frequent original-case entity across the cluster,
    // preferring distinctive (non-ubiquitous) names over product-name noise
    const counts = new Map()
    for (const m of members) for (const e of m.entities) counts.set(e, (counts.get(e) || 0) + 1)
    const ranked = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    const entity = (ranked.find(([e]) => !ubiquitous(members[0].pid, e)) || ranked[0])?.[0] || '(unnamed)'

    const lastFirm = firm.length ? firm[firm.length - 1] : null
    const reversals = Math.max(0, firm.length - 1)
    const lastOpen = [...members].reverse().find((m) => m.kind === 'open')
    const isReopened = !!(lastFirm && lastOpen && (lastOpen.date > lastFirm.date || (lastOpen.date === lastFirm.date && lastOpen.ord > lastFirm.ord)))

    for (const m of members) {
      let status
      if (m.kind === 'open') status = 'open'
      else if (m === lastFirm) status = 'decided'
      else status = 'changed'                          // an earlier firm decision, superseded
      const idx = firm.indexOf(m)
      decisions.push({
        project: m.project, status, entity, entities: m.entities,
        provisional: m.provisional, reopened: status === 'decided' && isReopened,
        summary: m.quote, date: m.date, evidence: m.evidence, signals: m.signals,
        supersededBy: status === 'changed' && idx >= 0 && firm[idx + 1] ? firm[idx + 1].quote : (status === 'changed' && lastFirm ? lastFirm.quote : null),
        chain: firm.length > 1 ? firm.map((f) => f.quote) : null,
      })
    }
    if (reversals >= 2) churn.push({ project: members[0].project, entity, reversals, chain: firm.map((f) => clip(f.quote, 80)) })
    if (lastFirm && lastFirm.provisional) provisionalDebt.push({ project: lastFirm.project, entity, date: lastFirm.date, quote: lastFirm.quote, evidence: lastFirm.evidence })
    if (isReopened) reopened.push({ project: members[0].project, entity, decidedOn: lastFirm.date, reopenedOn: lastOpen.date, question: lastOpen.quote })
  }

  // Deterministic order: project, status rank, date, summary.
  const rank = { decided: 0, changed: 1, open: 2 }
  decisions.sort((a, b) => a.project.localeCompare(b.project) || rank[a.status] - rank[b.status] ||
    a.date.localeCompare(b.date) || a.summary.localeCompare(b.summary))
  const byName = (a, b) => a.project.localeCompare(b.project) || a.entity.localeCompare(b.entity)
  churn.sort(byName); provisionalDebt.sort(byName); reopened.sort(byName)
  return { decisions, insights: { churn, provisional: provisionalDebt, reopened } }
}

const SECTIONS = [['Decided', 'decided'], ['Changed', 'changed'], ['Open', 'open']]

// Digest: insights first (the "so what"), then per project: Decided / Changed / Open.
export function renderDigest(result, { days = 0 } = {}) {
  const { decisions, insights } = result
  const projects = new Set(decisions.map((d) => d.project))
  const L = []
  L.push(`decisions report - ${decisions.length} decision(s) across ${projects.size} project(s) (window: ${days > 0 ? `last ${days} days` : 'all time'})`)
  L.push('')
  L.push('Insights')
  L.push('  Churn hotspots (decisions reversed 2+ times)')
  if (!insights.churn.length) L.push('    (none)')
  for (const c of insights.churn) L.push(`    - ${c.entity} (${c.project}): changed ${c.reversals} time(s) - ${c.chain.join(' -> ')}`)
  L.push('  Provisional decisions never revisited ("for now" debt)')
  if (!insights.provisional.length) L.push('    (none)')
  const prov = [...insights.provisional].sort((a, b) => b.date.localeCompare(a.date) || a.entity.localeCompare(b.entity))
  for (const p of prov.slice(0, 12)) L.push(`    - ${p.entity} (${p.project}, ${p.date}): "${p.quote}"`)
  if (prov.length > 12) L.push(`    (+${prov.length - 12} more - see the JSON or full report)`)
  L.push('  Re-litigated (settled, then re-questioned)')
  if (!insights.reopened.length) L.push('    (none)')
  for (const r of insights.reopened) L.push(`    - ${r.entity} (${r.project}): decided ${r.decidedOn}, re-opened ${r.reopenedOn}: "${r.question}"`)
  L.push('')

  const byProject = new Map()
  for (const d of decisions) {
    if (!byProject.has(d.project)) byProject.set(d.project, [])
    byProject.get(d.project).push(d)
  }
  const names = [...byProject.keys()].sort()
  if (!names.length) L.push('(no decisions found in this window)')
  for (const name of names) {
    L.push(`## ${name}`)
    const list = byProject.get(name)
    for (const [label, status] of SECTIONS) {
      L.push(`  ${label}`)
      const items = list.filter((d) => d.status === status)
      if (!items.length) { L.push('    (none)'); continue }
      for (const d of items) {
        const marks = [d.provisional && 'provisional', d.reopened && 're-opened'].filter(Boolean)
        L.push(`    - ${d.entity}: "${d.summary}"  [${d.status}${marks.length ? ' · ' + marks.join(' · ') : ''}]  · ${d.date}`)
        if (d.status === 'changed' && d.supersededBy) L.push(`        superseded by: "${clip(d.supersededBy, 100)}"`)
        L.push(`        ${d.evidence}`)
      }
    }
    L.push('')
  }
  return L.join('\n').replace(/\n+$/, '\n')
}

// JSON view: the full result object (decisions array + insights), agent-consumable.
export function decisionsJson(result) {
  return result
}
