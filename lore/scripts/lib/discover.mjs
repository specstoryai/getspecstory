// discover.mjs - find projects and transcripts on disk; resolve stable project identity.

import { readFileSync, readdirSync, existsSync } from 'node:fs'
import { join, basename, dirname, resolve } from 'node:path'
import { FILE_DATE } from './patterns.mjs'

// Recursively list every *.md under a history dir (handles specstory-organize year/month layouts).
export function walkMd(dir) {
  const out = []
  let ents
  try { ents = readdirSync(dir, { withFileTypes: true }) } catch { return out }
  for (const e of ents) {
    const p = join(dir, e.name)
    if (e.isDirectory()) out.push(...walkMd(p))
    else if (e.isFile() && e.name.endsWith('.md')) out.push(p)
  }
  return out
}

// Stable project identity from .specstory/.project.json: git_id (hash of normalized git remote -
// globally stable) preferred over workspace_id (path hash - machine-local), else the dir name.
export function readLabel(historyDir) {
  const pj = join(historyDir, '..', '.project.json')
  let id = null, name = null
  try {
    const j = JSON.parse(readFileSync(pj, 'utf8'))
    id = j.git_id || j.workspace_id || null
    name = j.project_name || null
  } catch { /* not a .specstory layout */ }
  const root = basename(dirname(dirname(historyDir))) || basename(historyDir)
  return { id: id || root, name: name || root }
}

// Find every .specstory/history under a root, at ANY depth (monorepos nest histories in
// sub-packages). Skips dependency/build dirs and other dotdirs; bounded depth as a tripwire.
export function scanForHistories(root, maxDepth = 6) {
  const SKIP = new Set(['node_modules', 'build', 'dist', 'out', 'target', 'vendor', 'Pods', 'DerivedData'])
  const found = []
  const walk = (dir, depth) => {
    if (depth > maxDepth) return
    let ents
    try { ents = readdirSync(dir, { withFileTypes: true }) } catch { return }
    for (const e of ents) {
      if (!e.isDirectory()) continue
      if (e.name === '.specstory') {
        const hd = join(dir, '.specstory', 'history')
        if (existsSync(hd)) found.push(hd)
        continue
      }
      if (SKIP.has(e.name) || e.name.startsWith('.')) continue
      walk(join(dir, e.name), depth + 1)
    }
  }
  walk(resolve(root), 0)
  return found
}

// Resolve --dir flags, a --projects parent, and/or a --scan root into [{historyDir, id, name}].
// Dirs are resolved to ABSOLUTE paths before anything is stored: the corpus carries session
// paths that must stay valid from any future working directory (prune, beats export,
// evidence refs). Relative input is a cwd-of-the-moment accident, never a contract.
export function discoverProjects(a) {
  const out = [], seen = new Set()
  const addHist = (hd) => {
    if (!hd) return
    hd = resolve(hd)
    if (!existsSync(hd) || seen.has(hd)) return
    seen.add(hd)
    const lab = readLabel(hd)
    out.push({ historyDir: hd, id: lab.id, name: lab.name })
  }
  if (a.scan) for (const hd of scanForHistories(a.scan)) addHist(hd)
  if (a.projects) {
    const parent = resolve(a.projects)
    let kids = []
    try { kids = readdirSync(parent) } catch { /* ignore */ }
    for (const k of kids) addHist(join(parent, k, '.specstory', 'history'))
  }
  for (const d of a.dirs) addHist(d)
  return out
}

export function fileDate(path, head) {
  let m = FILE_DATE.exec(basename(path))
  if (m) return m[1]
  m = FILE_DATE.exec(head)
  return m ? m[1] : null
}
