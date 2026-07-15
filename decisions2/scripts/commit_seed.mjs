#!/usr/bin/env node
// commit_seed.mjs - Pass 1 probe: extract decision-shaped commit messages from an
// indexed corpus as high-confidence decision SEEDS.
//
// This is a MEASUREMENT probe, not production code. Question it answers:
//   "Are commit messages a high-confidence seed source for decisions2?"
//   -> how many seeds, and what precision (hand-checked)?
//
// Commits come in two forms in transcripts:
//   (a) single-line:  git commit -m "Add debugging for citation ID mismatch"
//   (b) heredoc:      git commit -m "$(cat <<'EOF'
//                     feat: add retry logic to sync worker
//
//                     Co-Authored-By: ...
//                     EOF
//                     )"
// The commands table stores the command LINE only; heredoc bodies live in the
// transcript after the command's line. We read both.
//
// Usage: node commit_seed.mjs --db <path> [--min-len N] [--verbose]
import { readFileSync, openSync, readSync, closeSync, statSync } from 'node:fs'

const ARGS = (() => {
  const a = { db: '', minLen: 8, verbose: false }
  for (let i = 2; i < process.argv.length; i++) {
    const t = process.argv[i]
    if (t === '--db') a.db = process.argv[++i]
    else if (t === '--min-len') a.minLen = +process.argv[++i]
    else if (t === '--verbose' || t === '-v') a.verbose = true
  }
  if (!a.db) { process.stderr.write('usage: commit_seed.mjs --db <path> [--min-len N] [-v]\n'); process.exit(2) }
  return a
})()

// --- decision-shape filter (LIBERAL: false positives are cheap for a candidate generator) ---
// Conventional-commit prefixes that name a choice. `chore`/`wip`/`tweak` excluded (not decisions).
const CONV_PREFIX = /^(feat|fix|refactor|rename|move|remove|revert|switch|replace|build|ci|test|perf)(\(.+\))?:/i
// Decision-shaped verbs/templates in the message body (commit OR chat language).
const DECIDE_VERB = /\b(?:rename|switch(?:ed)?|replac(?:e|ed|ing)|remov(?:e|ed|ing)|mov(?:e|ed|ing)|merg(?:e|ed|ing)|revert(?:ed)?|redirect|replace\s+\S+\s+with|use\s+\S+\s+instead\s+of|rather\s+than|go\s+with|standardiz(?:e|ed)\s+on|default\s+to|settle\s+on|decid(?:e|ed)\s+on|punt(?:ing)?\s+on|for\s+now|temporar(?:y|ily))\b/i
// Exclusion: micro/maintenance commits with no decision content.
const NOISE = /^(wip|tweak|cleanup|clean up|fix typo|merge branch|merge dev|revert commit|chore:|docs:|style:|bump)/i

function isDecisionShaped(msg) {
  const m = msg.trim()
  if (m.length < ARGS.minLen) return false
  if (NOISE.test(m)) return false
  return CONV_PREFIX.test(m) || DECIDE_VERB.test(m)
}

// --- entity extraction (rough: the conventional-commit subject, or the noun-phrase after the verb) ---
function entityOf(msg) {
  const m = msg.trim()
  const conv = m.match(/^[a-z]+(?:\(([^)]+)\))?:\s*(.+)/i)
  if (conv) return (conv[1] || conv[2]).split(/\s+/).slice(0, 4).join(' ')
  // first backtick span, or first CamelCase token, or first 4 words
  const tick = m.match(/`([^`]{2,40})`/)
  if (tick) return tick[1]
  const sym = m.match(/[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+|[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+/)
  if (sym) return sym[0]
  return m.split(/\s+/).slice(0, 4).join(' ')
}

// --- read a heredoc commit message from a transcript ---
// `line` is the <tool-use> envelope line in the commands table; the actual git command
// (with <<'EOF') is several lines later inside a ```bash fence. Search forward for the
// heredoc opener, then the message SUBJECT is the first non-empty line after it (the body
// until a blank line or the EOF terminator).
function readHeredocMessage(path, line) {
  try {
    const buf = readFileSync(path, 'utf8')
    const lines = buf.split('\n')
    // search forward (up to ~15 lines) for the heredoc opener
    let openerIdx = -1, tag = null
    for (let i = line - 1; i < Math.min(line + 15, lines.length); i++) {
      const m = lines[i]?.match(/<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?/)
      if (m) { openerIdx = i; tag = m[1]; break }
    }
    if (openerIdx < 0) return null
    // message subject = first non-empty line after the opener; include body lines until
    // a blank line, the tag terminator, or a Co-Authored-By trailer.
    const msgLines = []
    for (let i = openerIdx + 1; i < lines.length; i++) {
      const t = lines[i].trim()
      if (!t) break                      // blank line ends the subject (body is detail)
      if (t === tag) break
      if (/^Co-Authored-By/.test(t) || /^[A-Za-z-]+: /.test(t) || t === ')') break
      msgLines.push(t)
      if (msgLines.length >= 3) break    // subject + up to 2 body lines is enough
    }
    return msgLines.join(' ').replace(/`/g, '').trim() || null
  } catch { return null }
}

// --- single-line -m "message" extraction from raw ---
function inlineMessage(raw) {
  // git commit -m "message"   (message has no embedded quotes)
  const m = raw.match(/git commit -m "([^"]{4,200})"/)
  if (m) return m[1]
  // git commit -m 'message'
  const m2 = raw.match(/git commit -m '([^']{4,200})'/)
  if (m2) return m2[1]
  return null
}

// --- main ---
const { DatabaseSync } = await import('node:sqlite')
const { openDb } = await import('../../decisions/scripts/lib/db.mjs')
const db = openDb(ARGS.db)

// join commands -> beats -> sessions; only git commit commands
const rows = db.prepare(`
  SELECT c.beat_id beat_id, c.line line, c.raw raw,
         s.path path, s.date date, s.project_name project, s.author author
  FROM commands c
  JOIN beats b ON b.id = c.beat_id
  JOIN sessions s ON s.id = b.session_id
  WHERE c.raw LIKE '%git commit -m%' OR c.head LIKE 'git commit%'
  ORDER BY s.date, c.beat_id
`).all()

// dedupe by (path, line) — git add && git commit produces two command rows at the same line
const seen = new Set()
const seeds = []
let totalCommits = 0
for (const r of rows) {
  const k = r.path + ':' + r.line
  if (seen.has(k)) continue
  seen.add(k)
  totalCommits++
  let msg = inlineMessage(r.raw)
  if (!msg && r.raw.includes("<<'EOF'") || r.raw.includes('<<EOF') || r.raw.includes('<<"EOF"')) {
    msg = readHeredocMessage(r.path, r.line)
  }
  if (!msg) continue
  if (!isDecisionShaped(msg)) continue
  seeds.push({
    project: r.project, date: r.date, author: r.author,
    message: msg.slice(0, 160),
    entity: entityOf(msg),
    evidence: r.path.split('/').slice(-2).join('/') + ':' + r.line,
  })
}

// report
const byProject = new Map()
for (const s of seeds) { byProject.set(s.project, (byProject.get(s.project) || 0) + 1) }
process.stdout.write(`commit-message seed probe\n`)
process.stdout.write(`  total git commit commands (deduped): ${totalCommits}\n`)
process.stdout.write(`  decision-shaped seeds:               ${seeds.length}\n`)
process.stdout.write(`  seed rate:                           ${(100 * seeds.length / Math.max(totalCommits, 1)).toFixed(1)}% of commits\n`)
process.stdout.write(`  by project: ${[...byProject.entries()].map(([p, n]) => `${p}=${n}`).join(', ')}\n\n`)

if (ARGS.verbose) {
  for (const s of seeds) {
    process.stdout.write(`  [${s.date}] ${s.project}: ${JSON.stringify(s.message).slice(0, 90)}\n`)
    process.stdout.write(`      entity: ${s.entity}  ·  ${s.evidence}\n`)
  }
} else {
  // print first 30 for hand-checking
  for (const s of seeds.slice(0, 30)) {
    process.stdout.write(`  [${s.date}] ${s.project}: ${JSON.stringify(s.message).slice(0, 90)}\n`)
    process.stdout.write(`      entity: ${s.entity}  ·  ${s.evidence}\n`)
  }
  if (seeds.length > 30) process.stdout.write(`  (+${seeds.length - 30} more — rerun with -v to see all)\n`)
}
