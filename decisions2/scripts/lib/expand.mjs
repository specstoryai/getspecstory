// expand.mjs - Pass 3: fingerprint-guided expansion over chat user-turns.
//
// Uses the fingerprint (entities + verb templates) to find decision candidates the
// commit-seed pass doesn't anchor. Each candidate gets a ROLE:
//   proposed     — a firm direction (directive verb + entity, or a decide-template)
//   deliberated  — a question-form turn with decision content (options being weighed)
//   provisional  — "for now" / temporary language
//   open         — a genuine question with no decision signal beyond question syntax
// (chosen/reversed come from the commit-seed pass: chosen = commit, reversed = revert commit)
//
// Also runs the original lexical grammar (CHANGE/DECIDE/OPEN/PROVISIONAL) as a complementary
// candidate source — the fingerprint and grammar are almost disjoint (2 overlap in the probe),
// so both contribute.

import { redactSecrets } from '../../../decisions/scripts/lib/patterns.mjs'

const clip = (s, n = 160) => (s.length > n ? s.slice(0, n - 1) + '…' : s)

// ── the lexical grammar (from decide.mjs, complementary candidate source) ──
const CHANGE_RE = /\b(?:actually,?\s+(?:let'?s|we|use|go|switch|make)|instead of what|changed?\s+(?:my|our)\s+mind|scrap\s+that|forget\s+that|on\s+second\s+thought|let'?s\s+not\b|go\s+back\s+to|switch(?:ing)?\s+(?:\S+\s+){0,3}?(?:back\s+)?to\b|revert\s+to|no\s+longer\s+(?:use|using)|replace\s+\S+\s+with)/i
const DECIDE_RE = /\b(?:let'?s\s+(?:go\s+with|use|stick\s+with|keep|standardize\s+on|make|call|do)|go(?:ing)?\s+with|we(?:'ll|\s+will)\s+(?:use|go|keep|stick)|decided?\s+(?:on|to|that)|decision\s*:|settle[d]?\s+on|agreed?\s+(?:on|to)\b|standardize\s+on|default\s+to|always\s+use|never\s+use|don'?t\s+use\s+(?:\S+\s+){0,4}?use|rename\s+(?:\S+\s+){0,4}?to\b|call\s+it\s+\S|(?:option|choice)\s+\d|the\s+(?:first|second|third)\s+option|use\s+(?:\S+\s+){0,4}?(?:instead\s+of|rather\s+than))/i
const OPEN_RE = /(?:\bshould\s+(?:we|i|it|this)\b[^.?!\n]*\?|\bwhich\b[^.?!\n]*\?|\bdo\s+we\s+want\b[^.?!\n]*\?|\bwhat\s+about\b[^.?!\n]*\?|\bor\s+should\b[^.?!\n]*\?)/i
const PROVISIONAL_RE = /\b(?:for\s+now|for\s+the\s+moment|temporar(?:y|ily)|as\s+a\s+stopgap|tbd|decide\s+(?:this\s+)?later|punt(?:ing)?\s+on|revisit\s+(?:this\s+)?later)\b/i

// ── fingerprint verb templates (mined from commits, the new surface) ──
const FP_VERBS = [
  { name: 'redirect..to',    re: /\bredirect\b[^.?!]{0,40}?\bto\b/i },
  { name: 'move..to',        re: /\bmove\b[^.?!]{0,40}?\bto\b/i },
  { name: 'integrate..into', re: /\bintegrat\w+\b[^.?!]{0,40}?\binto\b/i },
  { name: 'gate..behind',    re: /\bgate\b[^.?!]{0,40}?\bbehind\b/i },
  { name: 'drop',            re: /\bdrop\b/i },
  { name: 'demote',          re: /\bdemote\b/i },
  { name: 'consolidate',     re: /\bconsolidat\w+\b/i },
  { name: 'collapse',        re: /\bcollapse\b/i },
  { name: 'remove',          re: /\bremov\w+\b/i },
  { name: 'switch..to',      re: /\bswitch\b[^.?!]{0,40}?\bto\b/i },
  { name: 'rename..to',      re: /\brename\b[^.?!]{0,40}?\bto\b/i },
  { name: 'replace..with',   re: /\breplac\w+\b[^.?!]{0,40}?\bwith\b/i },
  { name: 'use..instead',    re: /\buse\b[^.?!]{0,40}?\binstead\b/i },
  { name: 'go..with',        re: /\bgo(?:ing)?\b[^.?!]{0,20}?\bwith\b/i },
  { name: 'default..to',     re: /\bdefault\b[^.?!]{0,20}?\bto\b/i },
]
const DIRECTIVE_RE = /\b(?:make|change|update|fix|add|remove|move|switch|use|try|implement|rename|redirect|put|set|need|want|going|we'?re|let'?s|should|replace|integrate|gate|drop|demote|consolidate|collapse|revert|merge)\b/i

function entityWordBoundaryMatch(text, entity) {
  const esc = entity.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp('\\b' + esc + '\\b', 'i').test(text)
}

function sentences(text) {
  return (text || '').split(/(?<=[.?!])\s+|\n+/).map(s => s.trim()).filter(Boolean)
}

// Assign a role to a candidate beat based on its signals.
function roleOf(signals, text) {
  const hasQuestion = /[?!]/.test(text) && (OPEN_RE.test(text) || /\b(should|could|would|what about|is it best|do we really)\b/i.test(text))
  if (signals.includes('provisional')) return 'provisional'
  if (signals.includes('change')) return 'reversed'
  if (hasQuestion && !signals.some(s => s.startsWith('tpl:') || s.startsWith('decide'))) return 'deliberated'
  if (hasQuestion) return 'deliberated'  // question with a template hit = deliberation with a lean
  return 'proposed'
}

// Exported: run Pass 3 over all beats, populate the candidates table.
// Returns { candidates: N, byRole: {...} }
export function expand(db) {
  // load fingerprints per project
  const projects = db.prepare("SELECT DISTINCT project FROM fingerprints").all().map(r => r.project)
  const fps = new Map()
  for (const p of projects) {
    const ents = db.prepare("SELECT key FROM fingerprints WHERE project=? AND kind='entity'").all(p).map(r => r.key)
    fps.set(p, ents)
  }

  const beats = db.prepare(`
    SELECT b.id id, b.session_id sid, b.ord ord, b.start_line line, b.intent_raw intent,
           s.project_name project, s.path path, s.date date
    FROM beats b JOIN sessions s ON s.id=b.session_id
    WHERE b.intent_raw IS NOT NULL AND b.intent_raw != ''
    ORDER BY s.project_id, s.date, s.id, b.ord
  `).all()

  // dedup by intent (resumed sessions repeat)
  const seen = new Set()
  const ins = db.prepare(`INSERT INTO candidates(session_id, beat_id, ord, line, project, date, role, entity, quote, signals, evidence)
    VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
  let count = 0
  const byRole = {}

  for (const b of beats) {
    const text = b.intent
    if (text.length < 5) continue
    const k = text.slice(0, 80)
    if (seen.has(b.sid + k)) continue
    seen.add(b.sid + k)

    const projEnts = fps.get(b.project) || []
    const signals = []

    // grammar signals
    if (CHANGE_RE.test(text)) signals.push('change')
    if (DECIDE_RE.test(text)) signals.push('decide')
    if (OPEN_RE.test(text)) signals.push('open')
    if (PROVISIONAL_RE.test(text)) signals.push('provisional')

    // fingerprint entity signals (word-boundary)
    const mentioned = projEnts.filter(e => entityWordBoundaryMatch(text, e))
    if (mentioned.length && DIRECTIVE_RE.test(text)) signals.push('entity:' + mentioned.join(','))

    // fingerprint template signals
    for (const v of FP_VERBS) if (v.re.test(text)) signals.push('tpl:' + v.name)

    if (signals.length === 0) continue

    const role = roleOf(signals, text)
    const entity = mentioned[0] || null
    const ev = b.path.split('/').slice(-2).join('/') + ':' + b.line
    ins.run(b.sid, b.id, b.ord, b.line, b.project, b.date, role, entity,
      redactSecrets(clip(text)), JSON.stringify(signals), ev)
    count++
    byRole[role] = (byRole[role] || 0) + 1
  }

  return { candidates: count, byRole }
}
