// indexer.mjs - walk projects, parse transcripts, persist beats + commands.
// REUSES the transcript-format grammar (parse.mjs, patterns.mjs, discover.mjs) from decisions/.
// Uses decisions2's db schema (same beats + commands tables, no grams/meta_hits).

import { readFileSync, statSync } from 'node:fs'
import { basename, relative } from 'node:path'
import { execFileSync } from 'node:child_process'
import { realpathSync } from 'node:fs'
import { discoverProjects, walkMd, fileDate } from '../../../decisions/scripts/lib/discover.mjs'
import { parseSessionFile, intentSig, sniffAuthor } from '../../../decisions/scripts/lib/parse.mjs'
import { PARSER_VERSION, deleteSessionRows } from './db.mjs'

function gitAuthors(historyDir) {
  const map = new Map()
  try {
    const root = execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd: historyDir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim()
    const rel = relative(root, realpathSync(historyDir))
    const out = execFileSync('git', ['log', '--diff-filter=A', '--format=\x01%an', '--name-only', '--', rel],
      { cwd: root, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024, stdio: ['ignore', 'pipe', 'ignore'] })
    let author = ''
    for (const line of out.split('\n')) {
      if (line.startsWith('\x01')) { author = line.slice(1).trim(); continue }
      const f = line.trim()
      if (f && author && !map.has(basename(f))) map.set(basename(f), author)
    }
  } catch { /* not a git repo */ }
  return map
}

export function indexCorpus(db, args) {
  const projects = discoverProjects(args)
  if (!projects.length) return { indexed: 0, skippedKnown: 0, skippedBig: 0, projects, error: 'no .specstory/history found (use --dir, --scan, or --projects)' }

  const known = new Map(db.prepare('SELECT id, size, mtime, parser FROM sessions').all().map(r => [r.id, r]))
  const insSession = db.prepare('INSERT OR REPLACE INTO sessions(id,project_id,project_name,path,date,agent,size,beats,mtime,parser,author,uuid) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)')
  const insBeat = db.prepare('INSERT INTO beats(session_id,ord,start_line,intent_raw,intent_sig,n_tools,tool_mix,files,n_cmds,exit_fails,outcome) VALUES(?,?,?,?,?,?,?,?,?,?,?)')
  const insCmd = db.prepare('INSERT INTO commands(beat_id,ord,head,raw,line) VALUES(?,?,?,?,?)')

  let indexed = 0, skippedKnown = 0, skippedBig = 0
  for (const proj of projects) {
    process.stderr.write(`📜 decisions2 · indexing ${proj.name} …\n`)
    const authors = gitAuthors(proj.historyDir)
    let projNew = 0
    for (const path of walkMd(proj.historyDir)) {
      let st
      try { st = statSync(path) } catch { continue }
      const size = st.size, mtime = Math.floor(st.mtimeMs)
      if (size > args.maxBytes) { skippedBig++; continue }
      const sid = proj.id + '/' + basename(path)
      const k = known.get(sid)
      if (!args.force && k && k.size === size && k.mtime === mtime && k.parser === PARSER_VERSION) {
        skippedKnown++; continue
      }
      if (k) deleteSessionRows(db, sid)

      let text
      try { text = readFileSync(path, 'utf8') } catch { continue }
      const date = fileDate(path, text.slice(0, 600))
      const { agent, uuid, beats } = parseSessionFile(text)
      if (!beats) continue
      indexed++; projNew++

      const author = authors.get(basename(path)) || sniffAuthor(text) || process.env.USER || 'unknown'
      db.exec('BEGIN')
      try {
        deleteSessionRows(db, sid)
        insSession.run(sid, proj.id, proj.name, path, date || '', agent || '', size, beats.length, mtime, PARSER_VERSION, author, uuid || '')
        for (let k2 = 0; k2 < beats.length; k2++) {
          const e = beats[k2]
          const sig = intentSig(e.intent)
          const mix = Object.entries(e.tools || {}).map(([t, c]) => `${t}:${c}`).join(',')
          const res = insBeat.run(sid, k2, e.startLine, (e.firstLine || '').slice(0, 200), sig,
            e.nTools, mix, [...(e.files || [])].slice(0, 20).join(','), (e.cmds || []).length, e.fails, e.outcome)
          const beatId = Number(res.lastInsertRowid)
          ;(e.cmds || []).forEach((c, ord) => insCmd.run(beatId, ord, c.head, c.raw, c.line))
        }
        db.exec('COMMIT')
      } catch (err) {
        try { db.exec('ROLLBACK') } catch { /* not in a tx */ }
        throw err
      }
    }
  }
  return { indexed, skippedKnown, skippedBig, projects: projects.map(p => p.name) }
}
