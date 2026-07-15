// commit_seed.mjs - Pass 1: extract decision-shaped commit messages as high-confidence seeds.
//
// A seed is a finalized choice (role: chosen). It anchors arcs and builds the fingerprint.
// Extraction: git commit -m "..." (single-line) or heredoc ("$(cat <<'EOF' ... EOF)") form.
// Filter: liberal for decision-shaped language (false positives are cheap for a candidate
// generator). Conventional-commit prefixes (feat/fix/refactor/rename/move/remove/revert/...)
// OR decision verbs in the body (rename, switch, replace, remove, move, redirect, ...).
//
// Entity extraction: conventional-commit scopes are clean entities; prose messages get
// word-boundary camelCase/snake_case symbols. Ubiquity cap drops product names.

import { readFileSync } from 'node:fs'

const CONV_PREFIX = /^(feat|fix|refactor|rename|move|remove|revert|switch|replace|build|ci|test|perf)(\(([^)]+)\))?:\s*(.+)/i
const DECIDE_VERB_RE = /\b(?:rename|switch(?:ed)?|replac(?:e|ed|ing)|remov(?:e|ed|ing)|mov(?:e|ed|ing)|merg(?:e|ed|ing)|revert(?:ed)?|redirect|replac\w+\s+with|use\s+\S+\s+instead\s+of|rather\s+than|go\s+with|standardiz\w+\s+on|default\s+to|settle\s+on|decid\w+\s+on|punt\w*\s+on|for\s+now|temporar\w+|integrat\w+\s+into|gate\s+\S+\s+behind|demote|consolidate|collapse|drop|tighten)\b/i
const NOISE_RE = /^(wip|tweak|cleanup|clean up|fix typo|merge branch|merge remote|chore:|style:|bump)/i
const SYMBOL_RE = /\b[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+\b|\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b/g
const UBIQUITY_MAX = 30

const _fileCache = new Map()  // path -> lines array (one file at a time; cleared between files)
function getLines(path) {
  if (!_fileCache.has(path)) {
    try { _fileCache.set(path, readFileSync(path, 'utf8').split('\n')) } catch { _fileCache.set(path, null) }
  }
  return _fileCache.get(path)
}
function clearFileCache() { _fileCache.clear() }

function readHeredocMessage(path, line) {
  const lines = getLines(path)
  if (!lines) return null
  try {
    let openerIdx = -1
    for (let i = line - 1; i < Math.min(line + 15, lines.length); i++) {
      if (lines[i]?.match(/<<-?\s*['"]?EOF/)) { openerIdx = i; break }
    }
    if (openerIdx < 0) return null
    for (let i = openerIdx + 1; i < lines.length; i++) {
      const t = lines[i].trim()
      if (!t || t === 'EOF' || /^Co-Authored/.test(t) || /^[A-Za-z-]+: /.test(t) || t === ')') return null
      return t.replace(/`/g, '').trim()
    }
  } catch { return null }
}

function inlineMessage(raw) {
  return raw.match(/git commit -m "([^"]{4,200})"/)?.[1] || raw.match(/git commit -m '([^']{4,200})'/)?.[1] || null
}

function entityOf(msg, conv) {
  if (conv?.[3]) return conv[3].toLowerCase()
  const body = conv?.[4] || msg
  const tick = body.match(/`([^`]{2,40})`/)
  if (tick) return tick[1].toLowerCase()
  const syms = body.match(SYMBOL_RE)
  if (syms?.length) return syms[0].toLowerCase()
  return null
}

function verbOf(msg, conv) {
  const body = conv?.[4] || msg
  return body.match(/^(\w+)\b/)?.[1]?.toLowerCase() || null
}

// Decision-type taxonomy (rough, deterministic; agent refines).
function typeOf(msg, conv) {
  const body = (conv?.[4] || msg).toLowerCase()
  if (/\brenam|call it|terminology|name\b/.test(body)) return 'naming'
  if (/\barchitect|shard|backend|queue|worker|service\b/.test(body)) return 'architectural'
  if (/\bscope|section|page|route|feature|remove.*from\b/.test(body)) return 'scope'
  if (/\bconfig|flag|env|setting\b/.test(body)) return 'config'
  if (/\bui|layout|button|card|nav|sidebar|modal|dialog|css\b/.test(body)) return 'ui-layout'
  if (/\bdep|package|upgrade|pin\b/.test(body)) return 'dependency'
  if (/\bintegrat|wire|connect|checkout|stripe|posthog\b/.test(body)) return 'integration'
  return 'other'
}

// Exported: run Pass 1 over the corpus, populate the seeds table.
// Returns { seeds: N, byProject: {...} }
export function extractSeeds(db) {
  const rows = db.prepare(`
    SELECT c.beat_id beat_id, c.line line, c.raw raw,
           s.id sid, s.path path, s.date date, s.project_name project, s.author author
    FROM commands c JOIN beats b ON b.id=c.beat_id JOIN sessions s ON s.id=b.session_id
    WHERE c.raw LIKE '%git commit -m%' OR c.head LIKE 'git commit%'
    ORDER BY s.date, c.beat_id
  `).all()

  // group commit rows by path so we read each transcript ONCE (some are 10MB)
  const byPath = new Map()
  for (const r of rows) {
    if (!byPath.has(r.path)) byPath.set(r.path, [])
    byPath.get(r.path).push(r)
  }

  // first pass: extract all candidate seeds, dedup by (path, line), read each file once
  const seenLine = new Set()
  const raw = []
  for (const [path, prows] of byPath) {
    const lines = getLines(path)
    for (const r of prows) {
      if (seenLine.has(r.path + ':' + r.line)) continue
      seenLine.add(r.path + ':' + r.line)
      let msg = inlineMessage(r.raw)
      if (!msg) msg = readHeredocMessage(r.path, r.line)
      if (!msg || msg.length < 8 || NOISE_RE.test(msg)) continue
      if (!CONV_PREFIX.test(msg) && !DECIDE_VERB_RE.test(msg)) continue
      const conv = msg.match(CONV_PREFIX)
      raw.push({ ...r, msg, conv, entity: entityOf(msg, conv), verb: verbOf(msg, conv), type: typeOf(msg, conv) })
    }
    clearFileCache()  // release the 10MB array before reading the next file
  }

  // ubiquity cap: count distinct seed messages per entity; drop entities in > UBIQUITY_MAX
  const entityMsgs = new Map()
  for (const s of raw) {
    if (!s.entity) continue
    if (!entityMsgs.has(s.entity)) entityMsgs.set(s.entity, new Set())
    entityMsgs.get(s.entity).add(s.msg.slice(0, 60))
  }
  const ubiquitous = new Set()
  for (const [e, msgs] of entityMsgs) if (msgs.size > UBIQUITY_MAX) ubiquitous.add(e)

  // dedup by (project, normalized message) — same commit appears 2-3x across re-runs
  const seenMsg = new Set()
  const seeds = []
  for (const s of raw) {
    if (ubiquitous.has(s.entity)) s.entity = null  // don't cluster on ubiquitous, keep the seed
    const key = s.project + '\t' + s.msg.toLowerCase().slice(0, 100)
    if (seenMsg.has(key)) continue
    seenMsg.add(key)
    seeds.push(s)
  }

  // persist
  const ins = db.prepare(`INSERT INTO seeds(session_id, beat_id, line, project, author, date, message, entity, verb, type, evidence)
    VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
  for (const s of seeds) {
    const ev = s.path.split('/').slice(-2).join('/') + ':' + s.line
    ins.run(s.sid, s.beat_id, s.line, s.project, s.author, s.date, s.msg.slice(0, 200),
      s.entity, s.verb, s.type, ev)
  }

  const byProject = {}
  for (const s of seeds) byProject[s.project] = (byProject[s.project] || 0) + 1
  return { seeds: seeds.length, byProject }
}
