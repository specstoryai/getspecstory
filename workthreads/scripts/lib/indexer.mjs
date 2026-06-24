// indexer.mjs - walk projects, parse new/changed transcripts (via parse.mjs), persist beats.
//
// IDEMPOTENCY CONTRACT:
// - session identity = project_id + '/' + filename (stable across re-runs and dir reorganizations)
// - fingerprint = size + mtime + PARSER_VERSION: a session is skipped only when the file is
//   unchanged AND it was indexed by the current parser. Grown/edited sessions are REPLACED whole
//   (beats + children deleted, re-inserted). Engine upgrades re-parse automatically.
// - `--force` re-indexes everything regardless. `prune` removes sessions whose file is gone.

import { readFileSync, statSync, existsSync, realpathSync } from 'node:fs'
import { basename, relative } from 'node:path'
import { execFileSync } from 'node:child_process'
import { discoverProjects, walkMd, fileDate } from './discover.mjs'
import { parseSessionFile, intentSig, sniffAuthor } from './parse.mjs'
import { PARSER_VERSION, deleteSessionRows } from './db.mjs'

// Authoritative author per transcript: who ADDED the file to git (one batched call per project).
// Histories committed to a shared repo carry their session owner this way; local-only files fall
// through to path-sniffing, then the machine user (an uncommitted transcript on this machine is
// almost certainly the local user's session).
function gitAuthors(historyDir) {
  const map = new Map()
  try {
    const root = execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd: historyDir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
    const rel = relative(root, realpathSync(historyDir))   // realpath: /tmp vs /private/tmp etc.
    const out = execFileSync('git', ['log', '--diff-filter=A', '--format=\x01%an', '--name-only', '--', rel],
      { cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, stdio: ['ignore', 'pipe', 'ignore'] })
    let author = ''
    for (const line of out.split('\n')) {
      if (line.startsWith('\x01')) { author = line.slice(1).trim(); continue }
      const f = line.trim()
      if (f && author && !map.has(basename(f))) map.set(basename(f), author)
    }
  } catch { /* not a git repo, or git unavailable - fall through to sniffing */ }
  return map
}

export function indexCorpus(db, args) {
  const projects = discoverProjects(args)
  if (!projects.length) return { indexed: 0, skippedKnown: 0, skippedBig: 0, projects, error: 'no .specstory history found' }
  const cutoff = args.days > 0 ? new Date(Date.now() - args.days * 864e5).toISOString().slice(0, 10) : null

  const known = new Map(db.prepare('SELECT id, size, mtime, parser FROM sessions').all().map(r => [r.id, r]))
  const insSession = db.prepare('INSERT OR REPLACE INTO sessions(id,project_id,project_name,path,date,agent,size,beats,mtime,parser,author,uuid) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)')
  const insEp = db.prepare('INSERT INTO beats(session_id,ord,start_line,intent_raw,intent_sig,n_tools,tool_mix,files,n_cmds,exit_fails,outcome) VALUES(?,?,?,?,?,?,?,?,?,?,?)')
  const insCmd = db.prepare('INSERT INTO commands(beat_id,ord,head,raw,line) VALUES(?,?,?,?,?)')
  const insGram = db.prepare('INSERT INTO grams(beat_id,n,gram) VALUES(?,?,?)')
  const insMeta = db.prepare('INSERT INTO meta_hits(beat_id,meta_id,quote,line) VALUES(?,?,?,?)')

  let indexed = 0, skippedKnown = 0, skippedBig = 0
  for (const proj of projects) {
    // live feedback to stderr: visible in the agent window, never pollutes stdout/--emit json
    const t0 = Date.now()
    let projNew = 0
    process.stderr.write(`📜 lore · indexing ${proj.name} …\n`)
    const authors = gitAuthors(proj.historyDir)
    for (const path of walkMd(proj.historyDir)) {
      let st
      try { st = statSync(path) } catch { continue }
      const size = st.size, mtime = Math.floor(st.mtimeMs)
      if (size > args.maxBytes) { skippedBig++; continue }
      const sid = proj.id + '/' + basename(path)
      const k = known.get(sid)
      if (!args.force && k && k.size === size && k.mtime === mtime && k.parser === PARSER_VERSION) {
        skippedKnown++; continue   // unchanged file, current parser → already indexed
      }

      let text
      try { text = readFileSync(path, 'utf8') } catch { continue }
      const date = fileDate(path, text.slice(0, 600))
      if (cutoff && date && date < cutoff) continue

      const { agent, uuid, beats: eps } = parseSessionFile(text)
      indexed++; projNew++
      if (projNew % 100 === 0) process.stderr.write(`   … ${projNew} sessions parsed\n`)
      // author ladder: git add-author (authoritative) > home-dir sniff > machine user
      const author = authors.get(basename(path)) || sniffAuthor(text) || process.env.USER || 'unknown'

      // replace this session's prior rows, then write fresh - ATOMICALLY. Without the transaction,
      // a crash mid-write leaves a fingerprint-matched session row with missing children that the
      // incremental skip would then preserve forever. (Batching also makes indexing much faster.)
      db.exec('BEGIN')
      try {
        deleteSessionRows(db, sid)
        insSession.run(sid, proj.id, proj.name, path, date, agent, size, eps.length, mtime, PARSER_VERSION, author, uuid)
        for (let k2 = 0; k2 < eps.length; k2++) {
          const e = eps[k2]
          const sig = intentSig(e.intent)
          const mix = Object.entries(e.tools).map(([t, c]) => `${t}:${c}`).join(',')
          const res = insEp.run(sid, k2, e.startLine, (e.firstLine || '').slice(0, 200), sig,
            e.nTools, mix, [...e.files].slice(0, 20).join(','), e.cmds.length, e.fails, e.outcome)
          const epId = res.lastInsertRowid
          e.cmds.forEach((c, ord) => insCmd.run(epId, ord, c.head, c.raw, c.line))
          const heads = e.cmds.map(c => c.head)
          const seen = new Set()
          for (let n = 2; n <= 4; n++) for (let s = 0; s + n <= heads.length; s++) {
            const g = heads.slice(s, s + n).join(' ▸ ')
            const key = n + '|' + g
            if (!seen.has(key)) { seen.add(key); insGram.run(epId, n, g) }   // dedupe within beat
          }
          for (const m of e.metas) insMeta.run(epId, m.id, m.quote, m.line)
        }
        db.exec('COMMIT')
      } catch (err) {
        try { db.exec('ROLLBACK') } catch { /* not in a tx */ }
        throw err
      }
    }
    process.stderr.write(`   ${proj.name}: +${projNew} new (${((Date.now() - t0) / 1000).toFixed(1)}s)\n`)
  }
  return { indexed, skippedKnown, skippedBig, projects }
}

// prune: drop sessions whose transcript no longer exists on disk (deleted/moved corpora),
// and report duplicate filename groups that exist under multiple project_ids (the
// "project gained a git remote → new git_id" drift case) so the caller can decide.
export function pruneCorpus(db) {
  const rows = db.prepare('SELECT id, path FROM sessions').all()
  let removed = 0
  for (const r of rows) {
    if (!existsSync(r.path)) {
      deleteSessionRows(db, r.id)
      db.prepare('DELETE FROM sessions WHERE id=?').run(r.id)
      removed++
    }
  }
  const dupes = db.prepare(`
    SELECT path, COUNT(DISTINCT project_id) np, GROUP_CONCAT(DISTINCT project_id) pids
    FROM sessions GROUP BY path HAVING np > 1`).all()
  // the SAME session (by provider UUID) indexed from two places - copied/cloned corpora
  // double-count every pattern and fake "portable" signals
  const contentDupes = db.prepare(`
    SELECT uuid, COUNT(*) n, GROUP_CONCAT(id, ' | ') ids
    FROM sessions WHERE uuid != '' GROUP BY uuid HAVING n > 1`).all()
  return { removed, remaining: rows.length - removed, dupes, contentDupes }
}
