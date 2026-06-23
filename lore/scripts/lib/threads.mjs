// threads.mjs - cluster the beats corpus into WORK THREADS and label their lifecycle.
//
// Sibling lens to report.mjs: where /lore mines reproducible PROCEDURES, /workthreads tracks
// LINES OF WORK and where each one stands - what was recently started (new), what is still
// open/unresolved, and what was recently closed. It re-uses the SAME corpus the engine already
// builds (sessions/beats/commands/grams) and reads ONLY existing columns: no schema change, no
// PARSER_VERSION bump, no LLM or network call. All clustering and labeling is pure arithmetic
// over the corpus, so two runs on the same corpus are byte-identical.
//
// CLUSTERING: the primary signal is shared touched-files - beats that touch the same file are one
// thread - reinforced by a shared distinctive intent keyword (identifier-shaped tokens like
// WIDGET_ALPHA / CodeMirror, and recurring nouns). A line of work spread across many sessions
// merges into ONE thread. Generic, ubiquitous tokens are deliberately NOT used as links so
// unrelated work never collapses together. Two anti-merge guards keep distinct lines of work
// apart on a real multi-project corpus:
//   1. UBIQUITOUS files (present in a large share of sessions, e.g. .claude/settings.json,
//      CLAUDE.md, package.json, .env.local) are too common to mean two beats are related: they
//      stay as evidence but are NOT used as links.
//   2. File links are SCOPED BY PROJECT - the same relative path (.claude/settings.json) in two
//      different repos is two different link tokens, so unrelated projects never bridge.
// Synthetic / slash-command-output turns (<local-command-stdout>, <command-name>, ...) carry no
// real line of work, so they never seed or name a thread.
//
// LIFECYCLE (exactly one per thread, evaluated in this precedence order, all relative to today):
//   1. closed - most recent labeled outcome is `success`, quiet for >= 3 days, last activity <= 30d
//   2. new    - the thread's FIRST activity is within the last 7 days
//   3. open   - otherwise, if it has activity within the last 14 days
//   (threads older/quieter than the above are omitted from the digest)

import { writeFileSync } from 'node:fs'
import { STOP, VERBS, COMMON, NOISE, redactSecrets } from './patterns.mjs'

const DAY = 86_400_000

// Whole-day age of a corpus date (YYYY-MM-DD) relative to a reference instant.
function daysSince(date, nowMs) {
  const t = Date.parse(String(date || '') + 'T00:00:00Z')
  if (!Number.isFinite(t)) return Infinity
  return Math.floor((nowMs - t) / DAY)
}

// File-path-like tokens inside a raw command or intent string: a slashed path
// (src/Foo.swift, ./pkg/cli/root.go) or a bare filename.ext (README.md). Globs like ./... and
// flags are not paths, so they never match.
const PATH_RE = /(?:\.{0,2}\/)?(?:[\w@.+-]+\/)+[\w@.+-]+|\b[\w@+-]+\.[A-Za-z][A-Za-z0-9]{0,11}\b/g
function pathsIn(s) {
  const out = []
  for (const m of String(s || '').matchAll(PATH_RE)) {
    const p = m[0].replace(/^\.\//, '')
    if (p && p !== '...' && /[./]/.test(p)) out.push(p)
  }
  return out
}

// Lowercased basename stem of a path: src/WIDGET_ALPHA.swift -> widget_alpha.
function stemOf(path) {
  const base = String(path).split('/').pop() || ''
  return base.replace(/\.[A-Za-z][A-Za-z0-9]{0,11}$/, '').toLowerCase()
}

const GENERIC_STEM = new Set(['index', 'main', 'app', 'mod', 'init', 'types', 'type', 'utils', 'util',
  'config', 'test', 'tests', 'readme', 'package', 'lib', 'src'])

// A touched file is too COMMON to be a clustering link once it shows up in more than this share of
// sessions (a real corpus has .claude/settings.json, CLAUDE.md, package.json everywhere). The
// absolute floor keeps tiny corpora - where two of five sessions is still 40% - from being pruned.
const UBIQUITY_FRACTION = 0.25
const UBIQUITY_MIN_SESSIONS = 5

// Synthetic / slash-command-output turns: their intent is harness chrome, not a line of work, so
// they must never seed or name a thread.
const SYNTHETIC_RE = /<local-command-stdout|<\/local-command-stdout|<command-name|<command-message|<command-args/i
function isSynthetic(intent) { return SYNTHETIC_RE.test(String(intent || '')) }

// A name candidate must be a real, recognizable identifier/noun: at least 3 chars and never a bare
// stopword or imperative verb ("for", "set"). Aim for "checkout-flow", never "set".
function nameOk(tok) {
  return typeof tok === 'string' && tok.length >= 3 && !STOP.has(tok) && !VERBS.has(tok)
}

// Identifier-shaped words in an intent: ALL_CAPS / snake-with-caps (WIDGET_ALPHA) or CamelCase
// (CodeMirrorBundle). These are the distinctive names a line of work is known by.
function identifierTokens(intent) {
  const out = []
  for (const m of String(intent || '').matchAll(/\b[A-Za-z][A-Za-z0-9_]{2,}\b/g)) {
    const tok = m[0]
    if (/[A-Z]{2}/.test(tok) || (/[a-z]/.test(tok) && /[A-Z]/.test(tok))) out.push(tok.toLowerCase())
  }
  return out
}

// Plain content words of an intent (lowercased, stopwords/verbs/ubiquitous-tools removed).
function contentTokens(intent) {
  const toks = (String(intent || '').toLowerCase().match(/[a-z][a-z0-9_-]{3,}/g) || [])
  return toks.filter(w => !STOP.has(w) && !VERBS.has(w) && !COMMON.has(w))
}

function commandHeadBase(head) {
  return String(head || '').trim().split(/\s+/)[0].replace(/^\.\//, '').toLowerCase()
}

function distinctiveGram(gram) {
  const heads = String(gram || '').split(' ▸ ').map(s => s.trim()).filter(Boolean)
  if (heads.length < 2) return false
  return heads.some(h => {
    const base = commandHeadBase(h)
    return base && !COMMON.has(base) && !NOISE.has(base)
  })
}

function projectKey(b) {
  return String(b.project_id || b.project_name || b.session_id || '')
}

// --- union-find over seed beats ---
class DSU {
  constructor() { this.p = new Map() }
  find(x) {
    if (!this.p.has(x)) { this.p.set(x, x); return x }
    let r = x
    while (this.p.get(r) !== r) r = this.p.get(r)
    while (this.p.get(x) !== r) { const n = this.p.get(x); this.p.set(x, r); x = n }
    return r
  }
  union(a, b) { const ra = this.find(a), rb = this.find(b); if (ra !== rb) this.p.set(ra, rb) }
}

// Build the thread model from the corpus. Pure read; returns {threads, scope}.
export function buildThreads(db, opts = {}) {
  const nowMs = Number.isFinite(opts.nowMs) ? opts.nowMs : Date.now()

  const beatRows = db.prepare(`
    SELECT e.id, e.session_id, e.ord, e.start_line, e.intent_raw, e.files, e.outcome,
           s.date, s.path, s.project_name, s.project_id
    FROM beats e JOIN sessions s ON s.id = e.session_id
    ORDER BY s.date, e.session_id, e.ord`).all()
  if (!beatRows.length) return { threads: [], scope: { beats: 0 } }

  const cmdsByBeat = new Map()
  for (const r of db.prepare('SELECT beat_id, raw FROM commands ORDER BY beat_id, ord').all()) {
    if (!cmdsByBeat.has(r.beat_id)) cmdsByBeat.set(r.beat_id, [])
    cmdsByBeat.get(r.beat_id).push(r.raw || '')
  }

  const gramsByBeat = new Map()
  for (const r of db.prepare('SELECT beat_id, gram FROM grams ORDER BY beat_id, n, gram').all()) {
    if (!distinctiveGram(r.gram)) continue
    if (!gramsByBeat.has(r.beat_id)) gramsByBeat.set(r.beat_id, [])
    gramsByBeat.get(r.beat_id).push(r.gram)
  }

  // Per-beat signal extraction. A beat becomes a thread SEED when it carries a touched file, an
  // executed command, or a distinctive identifier in its intent - reaction-only turns ("perfect,
  // that works") carry none and are skipped so they never spawn empty threads. Synthetic turns
  // (slash-command output and other harness chrome) are never seeds: their intent is not a line
  // of work and would otherwise seed a junk thread named after a stray verb.
  const beats = []
  for (const r of beatRows) {
    const cmds = cmdsByBeat.get(r.id) || []
    const grams = new Set(gramsByBeat.get(r.id) || [])
    const files = new Set()
    for (const f of String(r.files || '').split(',')) { const p = f.trim().replace(/^\.\//, ''); if (p) files.add(p) }
    for (const c of cmds) for (const p of pathsIn(c)) files.add(p)
    const idents = new Set(identifierTokens(r.intent_raw))
    const content = new Set(contentTokens(r.intent_raw))
    const synthetic = isSynthetic(r.intent_raw)
    const hasFile = files.size > 0
    const hasCmd = cmds.length > 0
    const seed = !synthetic && (hasFile || hasCmd || idents.size > 0)
    beats.push({ ...r, cmds, grams, files, idents, content, seed })
  }

  const seeds = beats.filter(b => b.seed)

  // Ubiquity: how many distinct sessions touch each bare file path. A path present in too large a
  // share of sessions (with an absolute floor for tiny corpora) is too common to link work - it is
  // kept as evidence but excluded from clustering links below.
  const sessionsByFile = new Map()
  const allSessions = new Set()
  for (const b of beats) {
    allSessions.add(b.session_id)
    for (const f of b.files) {
      const key = f.toLowerCase()
      if (!sessionsByFile.has(key)) sessionsByFile.set(key, new Set())
      sessionsByFile.get(key).add(b.session_id)
    }
  }
  const totalSessions = allSessions.size
  const ubiquitous = new Set()
  for (const [key, sess] of sessionsByFile) {
    if (sess.size >= UBIQUITY_MIN_SESSIONS && sess.size > UBIQUITY_FRACTION * totalSessions) ubiquitous.add(key)
  }

  // Link tokens per seed: shared touched-files are the primary signal (full path, plus the
  // non-generic file stem so an intent that NAMES the file links to the beat that edits it),
  // reinforced by distinctive identifier keywords. Project-local work signals are SCOPED BY
  // PROJECT so the same relative path or basename in two repos never bridges them. Ubiquitous
  // paths (present in most sessions) are kept as evidence but are NOT links. Generic, ubiquitous
  // words are likewise excluded so unrelated lines of work never collapse into one thread.
  const linkTokens = (b) => {
    const t = new Set()
    const proj = projectKey(b)
    for (const f of b.files) {
      const low = f.toLowerCase()
      if (ubiquitous.has(low)) continue
      t.add('f:' + proj + ':' + low)
      const st = stemOf(f)
      if (st.length >= 4 && !GENERIC_STEM.has(st)) t.add('k:' + proj + ':' + st)
    }
    for (const id of b.idents) t.add('k:' + proj + ':' + id)
    for (const gram of b.grams) t.add('g:' + proj + ':' + gram.toLowerCase())
    return t
  }

  const dsu = new DSU()
  const tokenOwner = new Map()   // link token -> a seed beat id, to union same-token beats
  for (const b of seeds) {
    dsu.find(b.id)
    for (const tok of linkTokens(b)) {
      if (tokenOwner.has(tok)) dsu.union(b.id, tokenOwner.get(tok))
      else tokenOwner.set(tok, b.id)
    }
  }

  // Gather components.
  const groups = new Map()
  for (const b of seeds) { const root = dsu.find(b.id); if (!groups.has(root)) groups.set(root, []); groups.get(root).push(b) }

  const threads = []
  for (const members of groups.values()) {
    members.sort((a, b) => (a.date < b.date ? -1 : a.date > b.date ? 1 : a.session_id < b.session_id ? -1 : a.session_id > b.session_id ? 1 : a.ord - b.ord))
    const files = new Set()
    for (const m of members) for (const f of m.files) files.add(f)
    const dates = members.map(m => m.date).filter(Boolean).sort()
    const firstActivity = dates[0] || ''
    const lastActivity = dates[dates.length - 1] || ''
    // most recent labeled (success|corrected) outcome, by date then ord
    let lastLabeled = null, lastLabeledDate = ''
    for (const m of members) {
      if (m.outcome === 'success' || m.outcome === 'corrected') { lastLabeled = m.outcome; lastLabeledDate = m.date }
    }
    const status = classify({ firstActivity, lastActivity, lastLabeled }, nowMs)
    if (!status) continue

    const name = threadName(members, files)
    const evidence = members.map(m => ({
      ref: `${m.path}:${m.start_line}`,
      date: m.date,
      outcome: m.outcome,
      intent: (m.intent_raw || '').slice(0, 140),
    }))
    threads.push({
      id: `${members[0].session_id}#${members[0].ord}`,
      name,
      status,
      files: [...files].sort(),
      projects: [...new Set(members.map(m => m.project_name).filter(Boolean))].sort(),
      sessions: new Set(members.map(m => m.session_id)).size,
      beats: members.length,
      firstActivity,
      lastActivity,
      lastLabeledOutcome: lastLabeled,
      lastLabeledDate,
      rationale: rationaleFor(status, { firstActivity, lastActivity, lastLabeledDate }),
      evidence,
    })
  }

  // Stable order: precedence, then most-recent first, then id - byte-identical across runs.
  const rank = { new: 0, open: 1, closed: 2 }
  threads.sort((a, b) =>
    rank[a.status] - rank[b.status] ||
    (a.lastActivity < b.lastActivity ? 1 : a.lastActivity > b.lastActivity ? -1 : 0) ||
    (a.firstActivity < b.firstActivity ? 1 : a.firstActivity > b.firstActivity ? -1 : 0) ||
    (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))

  return { threads, scope: { beats: beatRows.length, threads: threads.length } }
}

function classify({ firstActivity, lastActivity, lastLabeled }, nowMs) {
  const dsLast = daysSince(lastActivity, nowMs)
  const dsFirst = daysSince(firstActivity, nowMs)
  if (lastLabeled === 'success' && dsLast >= 3 && dsLast <= 30) return 'closed'
  if (dsFirst <= 7) return 'new'
  if (dsLast <= 14) return 'open'
  return null
}

function rationaleFor(status, { firstActivity, lastActivity, lastLabeledDate }) {
  if (status === 'closed') return `most recent outcome success on ${lastLabeledDate || lastActivity}; quiet >= 3 days, last activity ${lastActivity} (within 30 days)`
  if (status === 'new') return `first activity ${firstActivity} within the last 7 days`
  return `active ${lastActivity} within the last 14 days; not yet resolved`
}

// Human-readable kebab label: prefer a distinctive identifier, then a file stem, then a content
// noun; deterministic (frequency then alphabetical). Candidates must be REAL identifiers/nouns -
// never a bare stopword or imperative verb, and at least 3 chars - so a thread is named
// "checkout-flow" or "auth-token", never "for" or "set". Not gated, purely for the digest.
function threadName(members, files) {
  const count = new Map()
  const bump = (k) => { if (nameOk(k)) count.set(k, (count.get(k) || 0) + 1) }
  for (const m of members) for (const id of m.idents) bump(id)
  if (!count.size) for (const f of files) { const st = stemOf(f); if (!GENERIC_STEM.has(st)) bump(st) }
  if (!count.size) for (const m of members) for (const w of m.content) bump(w)
  const best = [...count.entries()].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1))[0]
  const raw = best ? best[0] : 'work-thread'
  return raw.replace(/[_\s]+/g, '-').replace(/[^a-z0-9-]/g, '').replace(/^-+|-+$/g, '') || 'work-thread'
}

// --- rendering ---

const SECTIONS = [
  ['new', 'New'],
  ['open', 'Open'],
  ['closed', 'Recently closed'],
]

export function renderDigest(model, args = {}) {
  const L = []
  L.push('# Work threads')
  L.push('')
  L.push(`${model.scope.beats} beats clustered into ${model.threads.length} active thread(s).`)
  L.push('')
  for (const [key, title] of SECTIONS) {
    const rows = model.threads.filter(t => t.status === key)
    L.push(`## ${title}`)
    if (!rows.length) { L.push('_(none)_'); L.push(''); continue }
    for (const t of rows) {
      const proj = t.projects.length ? ` · ${t.projects.join(', ')}` : ''
      L.push(`### ${t.name}  (${t.sessions} session(s) · ${t.beats} beat(s)${proj} · ${t.firstActivity} -> ${t.lastActivity})`)
      L.push(`  last activity: ${t.lastActivity}`)
      L.push(`  status: ${t.status} - ${t.rationale}`)
      if (t.files.length) L.push(`  files: ${t.files.join(', ')}`)
      for (const ev of t.evidence.slice(0, 4)) {
        L.push(`  - ${ev.ref} [${ev.outcome}] ${JSON.stringify(ev.intent)}`)
      }
    }
    L.push('')
  }
  return redactSecrets(L.join('\n')) + '\n'
}

// Machine-readable view: status is exactly one of new|open|closed, and the touched files are listed.
function jsonModel(model) {
  return model.threads.map(t => ({
    id: t.id,
    name: t.name,
    status: t.status,
    files: t.files,
    projects: t.projects,
    sessions: t.sessions,
    beats: t.beats,
    firstActivity: t.firstActivity,
    lastActivity: t.lastActivity,
    lastLabeledOutcome: t.lastLabeledOutcome,
    rationale: t.rationale,
    evidence: t.evidence,
  }))
}

// CLI entry for the `threads` subcommand.
export function threads(db, args, write = (s) => process.stdout.write(s)) {
  const model = buildThreads(db, args)
  if (args.json) {
    write(redactSecrets(JSON.stringify({ threads: jsonModel(model) }, null, 2)) + '\n')
    return model
  }
  const digest = renderDigest(model, args)
  write(digest)
  if (args.out) writeFileSync(args.out, digest)
  return model
}
