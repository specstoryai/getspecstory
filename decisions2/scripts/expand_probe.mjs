#!/usr/bin/env node
// expand_probe.mjs - Pass 3 probe: build a fingerprint from commit seeds, run it
// over chat user-turns, and measure what it catches that the lexical grammar misses.
//
// This is a MEASUREMENT probe. Question it answers:
//   "Does a commit-seeded fingerprint find chat decisions the grammar misses?"
//   -> how many NEW FINDS (fingerprint catch && !grammar catch), and what precision?
//
// Fingerprint = two things mined from commit seeds:
//   1. ENTITY set: conventional-commit scopes (sync-cloud, ui, analytics, ...) +
//      distinctive symbols from prose messages. A user turn mentioning a
//      fingerprinted entity + a directive verb = candidate.
//   2. VERB-TEMPLATE set: decision verbs mined from commit messages (rename, switch,
//      redirect, move, integrate, gate, drop, demote, ...). A user turn matching a
//      fingerprinted verb template = candidate.
//
// Usage: node expand_probe.mjs --db <path> [--session <substr>] [-v]
import { readFileSync } from 'node:fs'

const ARGS = (() => {
  const a = { db: '', session: '', verbose: false }
  for (let i = 2; i < process.argv.length; i++) {
    const t = process.argv[i]
    if (t === '--db') a.db = process.argv[++i]
    else if (t === '--session') a.session = process.argv[++i]
    else if (t === '-v' || t === '--verbose') a.verbose = true
  }
  if (!a.db) { process.stderr.write('usage: expand_probe.mjs --db <path> [--session <substr>] [-v]\n'); process.exit(2) }
  return a
})()

const { openDb } = await import('../../decisions/scripts/lib/db.mjs')
const db = openDb(ARGS.db)

// ─── 1. EXTRACT SEEDS (reuse commit_seed logic, minimal) ─────────────────────
function readHeredocMessage(path, line) {
  try {
    const buf = readFileSync(path, 'utf8')
    const lines = buf.split('\n')
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

const CONV_PREFIX = /^(feat|fix|refactor|rename|move|remove|revert|switch|replace|build|ci|test|perf)(\(([^)]+)\))?:\s*(.+)/i
const DECIDE_VERB_RE = /\b(?:rename|switch(?:ed)?|replac(?:e|ed|ing)|remov(?:e|ed|ing)|mov(?:e|ed|ing)|merg(?:e|ed|ing)|revert(?:ed)?|redirect|replac\w+\s+with|use\s+\S+\s+instead\s+of|rather\s+than|go\s+with|standardiz\w+\s+on|default\s+to|settle\s+on|decid\w+\s+on|punt\w*\s+on|for\s+now|temporar\w+|integrat\w+\s+into|gate\s+\S+\s+behind|demote|consolidate|collapse|drop|tighten)\b/i
const NOISE_RE = /^(wip|tweak|cleanup|clean up|fix typo|merge branch|merge remote|chore:|style:|bump)/i

const cmdRows = db.prepare(`
  SELECT DISTINCT c.line line, c.raw raw, s.path path, s.project_name project
  FROM commands c JOIN beats b ON b.id=c.beat_id JOIN sessions s ON s.id=b.session_id
  WHERE c.raw LIKE '%git commit -m%' OR c.head LIKE 'git commit%'
  ORDER BY s.date
`).all()

const seeds = []
const seenLine = new Set()
for (const r of cmdRows) {
  if (seenLine.has(r.path + ':' + r.line)) continue
  seenLine.add(r.path + ':' + r.line)
  let msg = inlineMessage(r.raw)
  if (!msg) msg = readHeredocMessage(r.path, r.line)
  if (!msg || msg.length < 8 || NOISE_RE.test(msg)) continue
  if (!CONV_PREFIX.test(msg) && !DECIDE_VERB_RE.test(msg)) continue
  seeds.push({ msg, project: r.project })
}

// ─── 2. BUILD FINGERPRINT ────────────────────────────────────────────────────
// Entity set: conventional-commit scopes + distinctive symbols from prose
const entities = new Set()
const verbTemplates = new Map()  // verb -> count

for (const s of seeds) {
  const conv = s.msg.match(CONV_PREFIX)
  if (conv?.[3]) entities.add(conv[3].toLowerCase())
  // extract leading verb (after optional conv prefix)
  const body = conv?.[4] || s.msg
  const verbMatch = body.match(/^(\w+)\b/)
  if (verbMatch) {
    const v = verbMatch[1].toLowerCase()
    verbTemplates.set(v, (verbTemplates.get(v) || 0) + 1)
  }
  // also extract symbols from prose (camelCase, snake_case) with WORD BOUNDARIES.
  // The old regex matched mid-word ("SpecStory" → "pecStory"); the leading \b and the
  // lowercase-first form fix that.
  if (!conv) {
    const syms = body.match(/\b[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+\b|\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b/g)
    if (syms) for (const sym of syms) if (sym.length >= 4) entities.add(sym.toLowerCase())
  }
}

// Ubiquity cap: an entity appearing in too many sessions is a product/project name
// ("specstory", "posthog") and would chain unrelated arcs. Drop entities in > 5 sessions.
// (We approximate per-session count via the distinct seed messages mentioning it.)
const sessionCount = new Map()  // entity -> set of distinct seed msgs
for (const s of seeds) {
  const conv = s.msg.match(CONV_PREFIX)
  const scope = conv?.[3]?.toLowerCase()
  const syms = (conv?.[4] || s.msg).match(/\b[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+\b|\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b/g) || []
  const mentioned = new Set([...(scope ? [scope] : []), ...syms.map(x => x.toLowerCase())])
  for (const e of mentioned) {
    if (!sessionCount.has(e)) sessionCount.set(e, new Set())
    sessionCount.get(e).add(s.msg.slice(0, 60))
  }
}
const UBIQUITY_MAX = 30   // raised: project entities (ui, skills) appear in many commits but are still useful; product names (specstory, posthog) appear in >30
for (const [e, msgs] of sessionCount) {
  if (msgs.size > UBIQUITY_MAX) entities.delete(e)
}

// Build verb-template regexes from the top verbs.
// Focus on templates NOT already in the grammar (the new surface).
// Grammar already has: rename..to, switch..to, replace..with, default..to, revert..to, go..with
// Grammar does NOT have: redirect..to, move..to, integrate..into, gate..behind, drop, demote, consolidate, collapse, remove (standalone)
const FINGERPRINT_VERBS = [
  { name: 'redirect..to',  re: /\bredirect\b[^.?!]{0,40}?\bto\b/i },
  { name: 'move..to',      re: /\bmove\b[^.?!]{0,40}?\bto\b/i },
  { name: 'integrate..into', re: /\bintegrat\w+\b[^.?!]{0,40}?\binto\b/i },
  { name: 'gate..behind',  re: /\bgate\b[^.?!]{0,40}?\bbehind\b/i },
  { name: 'drop',          re: /\bdrop\b/i },
  { name: 'demote',        re: /\bdemote\b/i },
  { name: 'consolidate',   re: /\bconsolidat\w+\b/i },
  { name: 'collapse',      re: /\bcollapse\b/i },
  { name: 'remove',        re: /\bremov\w+\b/i },
  { name: 'switch..to',    re: /\bswitch\b[^.?!]{0,40}?\bto\b/i },   // grammar has this in CHANGE_RE but tight
  { name: 'rename..to',    re: /\brename\b[^.?!]{0,40}?\bto\b/i },   // grammar has this but tight (≤4 words)
  { name: 'replace..with', re: /\breplac\w+\b[^.?!]{0,40}?\bwith\b/i },
  { name: 'use..instead',  re: /\buse\b[^.?!]{0,40}?\binstead\b/i },
  { name: 'go..with',      re: /\bgo(?:ing)?\b[^.?!]{0,20}?\bwith\b/i },
  { name: 'default..to',   re: /\bdefault\b[^.?!]{0,20}?\bto\b/i },
]

// Entity-match directive verbs (light: any direction-like verb + fingerprinted entity)
const DIRECTIVE_RE = /\b(?:make|change|update|fix|add|remove|move|switch|use|try|implement|rename|redirect|put|set|need|want|going|we're|let'?s|should|replace|integrate|gate|drop|demote|consolidate|collapse|revert|merge)\b/i

// ─── 3. THE GRAMMAR (from decide.mjs, for comparison) ────────────────────────
const CHANGE_RE = /\b(?:actually,?\s+(?:let'?s|we|use|go|switch|make)|instead of what|changed?\s+(?:my|our)\s+mind|scrap\s+that|forget\s+that|on\s+second\s+thought|let'?s\s+not\b|go\s+back\s+to|switch(?:ing)?\s+(?:\S+\s+){0,3}?(?:back\s+)?to\b|revert\s+to|no\s+longer\s+(?:use|using)|replace\s+\S+\s+with)/i
const DECIDE_RE = /\b(?:let'?s\s+(?:go\s+with|use|stick\s+with|keep|standardize\s+on|make|call|do)|go(?:ing)?\s+with|we(?:'ll|\s+will)\s+(?:use|go|keep|stick)|decided?\s+(?:on|to|that)|decision\s*:|settle[d]?\s+on|agreed?\s+(?:on|to)\b|standardize\s+on|default\s+to|always\s+use|never\s+use|don'?t\s+use\s+(?:\S+\s+){0,4}?use|rename\s+(?:\S+\s+){0,4}?to\b|call\s+it\s+\S|(?:option|choice)\s+\d|the\s+(?:first|second|third)\s+option|use\s+(?:\S+\s+){0,4}?(?:instead\s+of|rather\s+than))/i
const OPEN_RE = /(?:\bshould\s+(?:we|i|it|this)\b[^.?!\n]*\?|\bwhich\b[^.?!\n]*\?|\bdo\s+we\s+want\b[^.?!\n]*\?|\bwhat\s+about\b[^.?!\n]*\?|\bor\s+should\b[^.?!\n]*\?)/i
const PROVISIONAL_RE = /\b(?:for\s+now|for\s+the\s+moment|temporar(?:y|ily)|as\s+a\s+stopgap|tbd|decide\s+(?:this\s+)?later|punt(?:ing)?\s+on|revisit\s+(?:this\s+)?later)\b/i

function grammarMatch(text) {
  return {
    change: CHANGE_RE.test(text),
    decide: DECIDE_RE.test(text),
    open: OPEN_RE.test(text),
    provisional: PROVISIONAL_RE.test(text),
  }
}

function fingerprintMatch(text, projEntities) {
  // technique 1: entity-match — turn mentions a fingerprinted entity + directive verb
  const lower = text.toLowerCase()
  const mentionedEntities = [...projEntities].filter(e => new RegExp('\\b' + e.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\b', 'i').test(text))
  const hasDirective = DIRECTIVE_RE.test(text)
  const entityHit = mentionedEntities.length > 0 && hasDirective
  // technique 2: template-match — turn matches a fingerprinted verb template
  const templateHits = FINGERPRINT_VERBS.filter(v => v.re.test(text)).map(v => v.name)
  return {
    entityHit,
    entities: mentionedEntities,
    templateHits,
    any: entityHit || templateHits.length > 0,
  }
}

// ─── 4. LOAD USER TURNS FROM TARGET SESSION(S) ──────────────────────────────
let sessionFilter = ARGS.session
const turnRows = db.prepare(`
  SELECT b.id id, b.ord ord, b.intent_raw intent, s.path path, s.project_name project, s.date date
  FROM beats b JOIN sessions s ON s.id = b.session_id
  WHERE b.intent_raw IS NOT NULL AND b.intent_raw != ''
  ${sessionFilter ? `AND s.path LIKE '%' || ? || '%'` : ''}
  ORDER BY s.date, b.ord
`).all(...(sessionFilter ? [sessionFilter] : []))

// dedupe by intent (resumed sessions repeat)
const seenTurn = new Set()
const turns = []
for (const r of turnRows) {
  const k = (r.intent || '').slice(0, 80)
  if (seenTurn.has(k)) continue
  seenTurn.add(k)
  turns.push(r)
}

// ─── 5. RUN BOTH AND FIND THE NEW FINDS ──────────────────────────────────────
let grammarCount = 0, fpCount = 0, overlap = 0, newFinds = 0
const newFindList = []
const grammarOnlyList = []

for (const t of turns) {
  const text = t.intent || ''
  if (text.length < 5) continue
  const g = grammarMatch(text)
  const gAny = g.change || g.decide || g.open || g.provisional
  const f = fingerprintMatch(text, entities)
  if (gAny) grammarCount++
  if (f.any) fpCount++
  if (gAny && f.any) overlap++
  if (f.any && !gAny) {
    newFinds++
    newFindList.push({ ord: t.ord, text: text.slice(0, 200), entities: f.entities, templates: f.templateHits, entityHit: f.entityHit, project: t.project })
  }
  if (gAny && !f.any) {
    grammarOnlyList.push({ ord: t.ord, text: text.slice(0, 200) })
  }
}

// ─── 6. REPORT ───────────────────────────────────────────────────────────────
process.stdout.write(`expand probe — fingerprint vs grammar\n`)
process.stdout.write(`  seeds:                    ${seeds.length}\n`)
process.stdout.write(`  fingerprint entities:     ${entities.size}  (${[...entities].slice(0, 15).join(', ')}${entities.size > 15 ? '...' : ''})\n`)
process.stdout.write(`  fingerprint verb templates: ${FINGERPRINT_VERBS.length}\n`)
process.stdout.write(`  user turns (unique):      ${turns.length}\n`)
process.stdout.write(`  grammar catches:          ${grammarCount}\n`)
process.stdout.write(`  fingerprint catches:      ${fpCount}\n`)
process.stdout.write(`  overlap (both):           ${overlap}\n`)
process.stdout.write(`  NEW FINDS (fp & !gram):   ${newFinds}\n`)
process.stdout.write(`  grammar-only (gram & !fp): ${grammarOnlyList.length}\n\n`)

process.stdout.write(`NEW FINDS (fingerprint caught, grammar missed) — for hand-checking:\n`)
const show = ARGS.verbose ? newFindList : newFindList.slice(0, 50)
for (const f of show) {
  const tags = []
  if (f.entityHit) tags.push(`entity:${f.entities.join(',')}`)
  if (f.templates.length) tags.push(`tpl:${f.templates.join(',')}`)
  process.stdout.write(`  [ord=${f.ord}] (${tags.join(' | ')})\n`)
  process.stdout.write(`    ${JSON.stringify(f.text).slice(0, 140)}\n`)
}
if (!ARGS.verbose && newFindList.length > 50) process.stdout.write(`  (+${newFindList.length - 50} more — rerun with -v)\n`)
