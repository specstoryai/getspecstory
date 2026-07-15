#!/usr/bin/env node
// arc_test.mjs - Pull all candidates (grammar + fingerprint + commit seeds) from
// one session, sorted by turn order, to hand-test whether they form per-entity arcs.
//
// Usage: node arc_test.mjs --db <path> --session <substr>
import { readFileSync } from 'node:fs'

const ARGS = (() => {
  const a = { db: '', session: '' }
  for (let i = 2; i < process.argv.length; i++) {
    if (process.argv[i] === '--db') a.db = process.argv[++i]
    else if (process.argv[i] === '--session') a.session = process.argv[++i]
  }
  if (!a.db || !a.session) { process.stderr.write('usage: arc_test.mjs --db <path> --session <substr>\n'); process.exit(2) }
  return a
})()

const { openDb } = await import('../../decisions/scripts/lib/db.mjs')
const db = openDb(ARGS.db)

// ── grammars (from decide.mjs) ──
const CHANGE_RE = /\b(?:actually,?\s+(?:let'?s|we|use|go|switch|make)|instead of what|changed?\s+(?:my|our)\s+mind|scrap\s+that|forget\s+that|on\s+second\s+thought|let'?s\s+not\b|go\s+back\s+to|switch(?:ing)?\s+(?:\S+\s+){0,3}?(?:back\s+)?to\b|revert\s+to|no\s+longer\s+(?:use|using)|replace\s+\S+\s+with)/i
const DECIDE_RE = /\b(?:let'?s\s+(?:go\s+with|use|stick\s+with|keep|standardize\s+on|make|call|do)|go(?:ing)?\s+with|we(?:'ll|\s+will)\s+(?:use|go|keep|stick)|decided?\s+(?:on|to|that)|decision\s*:|settle[d]?\s+on|agreed?\s+(?:on|to)\b|standardize\s+on|default\s+to|always\s+use|never\s+use|don'?t\s+use\s+(?:\S+\s+){0,4}?use|rename\s+(?:\S+\s+){0,4}?to\b|call\s+it\s+\S|(?:option|choice)\s+\d|the\s+(?:first|second|third)\s+option|use\s+(?:\S+\s+){0,4}?(?:instead\s+of|rather\s+than))/i
const OPEN_RE = /(?:\bshould\s+(?:we|i|it|this)\b[^.?!\n]*\?|\bwhich\b[^.?!\n]*\?|\bdo\s+we\s+want\b[^.?!\n]*\?|\bwhat\s+about\b[^.?!\n]*\?|\bor\s+should\b[^.?!\n]*\?)/i
const PROVISIONAL_RE = /\b(?:for\s+now|for\s+the\s+moment|temporar(?:y|ily)|as\s+a\s+stopgap|tbd|decide\s+(?:this\s+)?later|punt(?:ing)?\s+on|revisit\s+(?:this\s+)?later)\b/i
function grammarTags(text) {
  const t = []
  if (CHANGE_RE.test(text)) t.push('change')
  if (DECIDE_RE.test(text)) t.push('decide')
  if (OPEN_RE.test(text)) t.push('open')
  if (PROVISIONAL_RE.test(text)) t.push('provisional')
  return t
}

// ── fingerprint (from expand_probe) ──
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
  { name: 'switch..to',    re: /\bswitch\b[^.?!]{0,40}?\bto\b/i },
  { name: 'rename..to',    re: /\brename\b[^.?!]{0,40}?\bto\b/i },
  { name: 'replace..with', re: /\breplac\w+\b[^.?!]{0,40}?\bwith\b/i },
  { name: 'use..instead',  re: /\buse\b[^.?!]{0,40}?\binstead\b/i },
  { name: 'go..with',      re: /\bgo(?:ing)?\b[^.?!]{0,20}?\bwith\b/i },
  { name: 'default..to',   re: /\bdefault\b[^.?!]{0,20}?\bto\b/i },
]
const DIRECTIVE_RE = /\b(?:make|change|update|fix|add|remove|move|switch|use|try|implement|rename|redirect|put|set|need|want|going|we're|let'?s|should|replace|integrate|gate|drop|demote|consolidate|collapse|revert|merge)\b/i
// entity set from all seeds (built same as expand_probe)
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
const cmdRows = db.prepare(`SELECT DISTINCT c.line line, c.raw raw, s.path path, s.project_name project
  FROM commands c JOIN beats b ON b.id=c.beat_id JOIN sessions s ON s.id=b.session_id
  WHERE c.raw LIKE '%git commit -m%' OR c.head LIKE 'git commit%' ORDER BY s.date`).all()
const entities = new Set()
const seedsByPath = new Map()  // path -> [seed msgs]
for (const r of cmdRows) {
  let msg = inlineMessage(r.raw)
  if (!msg) msg = readHeredocMessage(r.path, r.line)
  if (!msg || msg.length < 8 || NOISE_RE.test(msg)) continue
  if (!CONV_PREFIX.test(msg) && !DECIDE_VERB_RE.test(msg)) continue
  const conv = msg.match(CONV_PREFIX)
  if (conv?.[3]) entities.add(conv[3].toLowerCase())
  if (!conv) {
    const syms = msg.match(/[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+|[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+/g)
    if (syms) for (const sym of syms) if (sym.length >= 4) entities.add(sym.toLowerCase())
  }
  if (!seedsByPath.has(r.path)) seedsByPath.set(r.path, [])
  seedsByPath.get(r.path).push({ msg, line: r.line })
}
function fingerprintTags(text) {
  const lower = text.toLowerCase()
  const mentionedEntities = [...entities].filter(e => lower.includes(e))
  const hasDirective = DIRECTIVE_RE.test(text)
  const tags = []
  if (mentionedEntities.length && hasDirective) tags.push('entity:' + mentionedEntities.join(','))
  const tpls = FINGERPRINT_VERBS.filter(v => v.re.test(text)).map(v => v.name)
  if (tpls.length) tags.push('tpl:' + tpls.join(','))
  return tags
}

// ── load turns for the target session ──
const session = db.prepare(`SELECT id, path, project_name, date FROM sessions WHERE path LIKE '%' || ? || '%' LIMIT 1`).get(ARGS.session)
const turnRows = db.prepare(`SELECT ord, start_line, intent_raw FROM beats WHERE session_id=? AND intent_raw IS NOT NULL AND intent_raw != '' ORDER BY ord`).all(session.id)
const seenTurn = new Set()
const turns = []
for (const r of turnRows) {
  const k = (r.intent_raw || '').slice(0, 80)
  if (seenTurn.has(k)) continue
  seenTurn.add(k)
  turns.push(r)
}

// ── combine: for each turn, grammar tags + fingerprint tags ──
const candidates = []
for (const t of turns) {
  const text = t.intent_raw || ''
  if (text.length < 5) continue
  const g = grammarTags(text)
  const f = fingerprintTags(text)
  const allTags = [...g, ...f]
  if (allTags.length === 0) continue
  candidates.push({ ord: t.ord, line: t.start_line, tags: allTags, text: text.slice(0, 220) })
}

// ── commit seeds for this session, as "chosen" beats at their line ──
const sessionSeeds = seedsByPath.get(session.path) || []

// ── report ──
process.stdout.write(`arc test — session: ${session.path.split('/').slice(-1)[0]}\n`)
process.stdout.write(`  project: ${session.project_name}  date: ${session.date}\n`)
process.stdout.write(`  unique user turns: ${turns.length}\n`)
process.stdout.write(`  candidates (grammar|fingerprint): ${candidates.length}\n`)
process.stdout.write(`  commit seeds in this session: ${sessionSeeds.length}\n\n`)

process.stdout.write(`=== CANDIDATES (chat turns), sorted by ord ===\n`)
for (const c of candidates) {
  process.stdout.write(`[ord=${c.ord} line=${c.line}] {${c.tags.join(' | ')}}\n  ${JSON.stringify(c.text).slice(0, 180)}\n\n`)
}

process.stdout.write(`=== COMMIT SEEDS (finalized choices), sorted by line ===\n`)
for (const s of sessionSeeds) {
  process.stdout.write(`[line=${s.line}] {chosen}\n  ${JSON.stringify(s.msg).slice(0, 180)}\n\n`)
}
