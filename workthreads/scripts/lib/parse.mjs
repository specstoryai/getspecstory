// parse.mjs - pure transcript parsing: text in, beats out. No I/O, no DB.
//
// All format knowledge lives in patterns.mjs (the grammar); this file is only the walk:
// segment user turns into beats, attach tool activity, label outcomes retroactively.
//
// THE UNIT IS THE BEAT: one user turn (INTENT) + all agent activity until the next user turn
// (METHOD: tool mix, executed commands, files touched, exit codes) + the NEXT user turn's reaction
// (OUTCOME label). Commands come only from executed shell tool blocks, so every command counted
// was genuinely run by the agent.

import {
  SESSION_HDR, USER_MARK, TURN_MARK, TOOLUSE_OPEN, SHELL_TOOLS, SHELL_EXCLUDE,
  SUMMARY_CMD, INLINE_CMD, FENCE_LINE, SHELL_FENCE_LANGS, HEREDOC_OPEN,
  BULLET_CMD, BULLET_CMD_OPEN, BULLET_CMD_CLOSE,
  LEGACY_TOOL, LEGACY_TYPE, LEGACY_SHELL_NAMES, LEGACY_OUTPUT_MARK,
  EXIT_CODE, ERROR_HEAD, NOISE, META, VERBS,
  tokenize, leadingVerb, classifyOutcome,
} from './patterns.mjs'

// raw command string -> ordered list of meaningful command heads (project tools kept, recon dropped)
export function headsFrom(cmd) {
  const heads = []
  for (let seg of cmd.split(/&&|;/)) {
    seg = seg.trim().split('|')[0].trim().replace(/^\$\s+/, '')
    if (!seg) continue
    seg = seg.replace(/^(?:[A-Z_][A-Z0-9_]*=\S+\s+)+/, '')                 // strip FOO=bar prefixes
    const w = seg.match(/^(?:bash|sh|zsh)\s+-l?c\s+["']?(.+?)["']?$/)      // unwrap bash -lc '...'
    if (w) seg = w[1].trim()
    const toks = seg.split(/\s+/)
    let head = toks[0]
    // head must look like a command name (lowercase start; optional ./ for project scripts);
    // this drops heredoc/prose lines like "Co-Authored-By:" and "EOF" that leak from fences.
    if (!head || !/^(?:\.\/)?[a-z][a-z0-9._/+-]*$/.test(head) || NOISE.has(head)) continue
    if (toks[1] && /^[a-z][a-z0-9_-]*$/.test(toks[1])) head += ' ' + toks[1]   // keep subcommand
    if (heads[heads.length - 1] !== head) heads.push(head)
  }
  return heads
}

// Extract executed shell commands + failure signals from one MODERN <tool-use> block.
// Command locations (see patterns.mjs §3): (a) in-summary, (b) inline-backtick line,
// (c) shell-language fence (heredocs skipped), (d) "- command:" bullet - single- OR multi-line.
// Output fences are never commands; they are scanned for exit codes / error heads only.
export function extractShellBlock(summary, body, line) {
  const cmds = []
  let fails = 0
  const sm = summary.match(SUMMARY_CMD)                                   // (a)
  if (sm) cmds.push({ cmd: sm[1], line })
  let inFence = false, lang = '', fbuf = [], sawOutput = false, bulletOpen = false
  for (const raw of body) {
    if (bulletOpen) {                                                     // (d) multi-line bullet tail
      if (BULLET_CMD_CLOSE.test(raw.trim())) bulletOpen = false
      continue                                                            // bullet content ≠ commands
    }
    const fm = raw.match(FENCE_LINE)
    if (fm) {
      if (!inFence) { inFence = true; lang = fm[1].toLowerCase(); fbuf = [] }
      else {
        if (SHELL_FENCE_LANGS.has(lang)) {                                // (c)
          let skipUntil = null
          for (const cl of fbuf) {
            const t = cl.trim()
            if (skipUntil !== null) { if (t === skipUntil) skipUntil = null; continue }
            if (!t || t.startsWith('#')) continue
            const hd = t.match(HEREDOC_OPEN)
            if (hd) skipUntil = hd[1]
            cmds.push({ cmd: t, line })
          }
        } else {
          sawOutput = true
          for (const ol of fbuf) {
            const xc = EXIT_CODE.exec(ol)
            if (xc && xc[1] !== '0') fails++
            else if (ERROR_HEAD.test(ol.trim())) fails++
          }
        }
        inFence = false
      }
      continue
    }
    if (inFence) { fbuf.push(raw); continue }
    if (sawOutput) {
      const xc = EXIT_CODE.exec(raw)
      if (xc && xc[1] !== '0') fails++
      continue
    }
    const im = raw.match(INLINE_CMD)                                      // (b)
    if (im) { cmds.push({ cmd: im[1], line }); continue }
    const lm = raw.match(BULLET_CMD)                                      // (d) single-line
    if (lm) { cmds.push({ cmd: stripBullet(lm[1]), line }); continue }
    const lo = raw.match(BULLET_CMD_OPEN)                                 // (d) multi-line opener:
    if (lo) { cmds.push({ cmd: stripBullet(lo[1]), line }); bulletOpen = true }   // first line is the command
  }
  return { cmds, fails }
}
const stripBullet = (s) => s.replace(/^\[\s*/, '').replace(/\s*\]$/, '')

// Pull file paths from non-shell tool blocks (Read/Edit backticked relpath; apply_patch headers).
export function extractFiles(summary, body) {
  const files = new Set()
  const grab = (s) => {
    for (const m of s.matchAll(/`((?:\.{0,2}\/)?[\w@./-]+\.[a-z]{1,12})`/g)) files.add(m[1])
    for (const m of s.matchAll(/\*\*(?:Add|Modify|Update|Delete):\s*`([^`]+)`\*\*/g)) files.add(m[1])
  }
  grab(summary)
  for (const l of body.slice(0, 6)) grab(l)   // paths appear in the first lines, not in content fences
  return [...files]
}

// Parse one whole transcript: text in -> { agent, uuid, beats } out. Pure function; unit-test me.
export function parseSessionFile(text) {
  const head = text.slice(0, 600)
  const hdr = SESSION_HDR.exec(head)
  const agent = hdr ? hdr[1].toLowerCase().replace(/\s+/g, '-') : 'unknown'   // claude-code, codex-cli, cursor, ...
  const uuid = hdr ? hdr[2] : ''   // provider session id - detects the same session copied into two corpora

  const lines = text.split('\n')
  const eps = []
  let cur = null
  let i = 0
  while (i < lines.length) {
    const line = lines[i]

    // ---- a user turn opens a new beat ----
    if (USER_MARK.test(line)) {
      if (cur) eps.push(cur)
      const buf = []
      let j = i + 1
      for (; j < lines.length && buf.length < 30; j++) {
        const l = lines[j]
        if (TURN_MARK.test(l)) break
        if (buf.length > 0 && /^#{1,4}\s/.test(l)) break
        buf.push(l)
      }
      const intent = buf.join('\n').trim()
      cur = { startLine: i + 1, intent, tools: {}, nTools: 0, cmds: [], files: new Set(), fails: 0, metas: [] }
      if (intent) {
        const firstLine = (intent.split('\n').find(x => x.trim()) || '').trim()
        cur.firstLine = firstLine
        const scan = intent.slice(0, 1200)
        for (const m of META) {
          const hit = m.re.test(firstLine) ? firstLine : (m.re.test(scan) ? (intent.split('\n').find(x => m.re.test(x)) || firstLine) : null)
          if (hit) cur.metas.push({ id: m.id, quote: hit.trim().slice(0, 160), line: i + 1 })
        }
      }
      i = j
      continue
    }

    // ---- MODERN era: <tool-use> envelope ----
    const tm = TOOLUSE_OPEN.exec(line)
    if (tm && cur) {
      const ttype = tm[1] || 'generic', name = tm[2]
      cur.tools[ttype] = (cur.tools[ttype] || 0) + 1
      cur.nTools++
      let j = i + 1, summary = ''
      const body = []
      while (j < lines.length && !lines[j].includes('</tool-use>')) {
        if (lines[j].includes('<summary>') && !summary) summary = lines[j]
        else body.push(lines[j])
        j++
      }
      if ((ttype === 'shell' && !SHELL_EXCLUDE.has(name)) || SHELL_TOOLS.has(name)) {
        const { cmds, fails } = extractShellBlock(summary, body, i + 1)
        cur.fails += fails
        for (const c of cmds) for (const h of headsFrom(c.cmd)) cur.cmds.push({ head: h, raw: c.cmd.slice(0, 160), line: c.line })
      } else if (ttype === 'read' || ttype === 'write') {
        for (const f of extractFiles(summary, body)) cur.files.add(f)
      }
      i = j + 1
      continue
    }

    // ---- LEGACY era (~2025): bare "Tool use: **Name**" lines ----
    const lt = LEGACY_TOOL.exec(line)
    if (lt && cur) {
      const name = lt[1]
      const ttype = LEGACY_TYPE[name] || 'generic'
      cur.tools[ttype] = (cur.tools[ttype] || 0) + 1
      cur.nTools++
      if (ttype === 'read' || ttype === 'write') {
        for (const f of extractFiles(lt[2] || '', [])) cur.files.add(f)
      }
      if (LEGACY_SHELL_NAMES.has(name)) {
        // commands follow as inline-backtick lines or shell fences, until Result:/Output:/next marker
        let j = i + 1, inFence = false, lang = '', inResult = false
        for (; j < lines.length && j < i + 60; j++) {
          const l = lines[j]
          if (TURN_MARK.test(l) || LEGACY_TOOL.test(l)) break
          const fm = l.match(FENCE_LINE)
          if (fm) { inFence = !inFence; if (inFence) lang = fm[1].toLowerCase(); continue }
          if (inFence) {
            if (!inResult && SHELL_FENCE_LANGS.has(lang)) {
              const t = l.trim()
              if (t && !t.startsWith('#')) for (const h of headsFrom(t)) cur.cmds.push({ head: h, raw: t.slice(0, 160), line: j + 1 })
            } else {
              const xc = EXIT_CODE.exec(l)
              if (xc && xc[1] !== '0') cur.fails++
              else if (ERROR_HEAD.test(l.trim())) cur.fails++
            }
            continue
          }
          if (LEGACY_OUTPUT_MARK.test(l)) { inResult = true; continue }
          if (!inResult) {
            const im = l.trim().match(INLINE_CMD)
            if (im) for (const h of headsFrom(im[1])) cur.cmds.push({ head: h, raw: im[1].slice(0, 160), line: j + 1 })
          }
        }
        i = j
        continue
      }
      i++
      continue
    }
    i++
  }
  if (cur) eps.push(cur)

  // outcome labels from the NEXT beat's opening line - the user's reply is free supervision
  for (let k = 0; k < eps.length; k++) eps[k].outcome = classifyOutcome(k + 1 < eps.length ? eps[k + 1].firstLine : null)

  return { agent, uuid, beats: eps }
}

// Intent signature: leading imperative verb + first salient keyword, e.g. "write:commit".
export function intentSig(intent) {
  const words = tokenize(intent || '')
  const verb = leadingVerb(words)
  if (!verb) return null
  const kw = words.find(w => w !== verb && !VERBS.has(w))
  return verb + (kw ? ':' + kw : '')
}

// Author fallback: transcripts leak the session owner's home dir in commands/tool output
// (/Users/<name>/ on macOS, /home/<name>/ on Linux). Most frequent name wins.
export function sniffAuthor(text) {
  const counts = {}
  for (const m of text.matchAll(/\/(?:Users|home)\/([a-z][a-z0-9_-]{1,30})\//gi)) {
    const n = m[1].toLowerCase()
    if (n === 'shared' || n === 'runner') continue
    counts[n] = (counts[n] || 0) + 1
  }
  const best = Object.entries(counts).sort((a, b) => b[1] - a[1])[0]
  return best ? best[0] : null
}
