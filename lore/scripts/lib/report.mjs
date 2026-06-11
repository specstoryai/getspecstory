// report.mjs - corroboration queries, scoring, and the evidence-block emitters.
// All ranking is SQL + arithmetic over the corpus; no parsing happens here.

import { homedir } from 'node:os'
import { COMMON, META } from './patterns.mjs'

export function wantedKinds(kinds) {
  if (!kinds || !kinds.length) return new Set(['cmd', 'task', 'meta', 'corr'])
  const out = new Set()
  for (const k of kinds) {
    if (['cmd', 'command', 'commands', 'runbook-cmd'].includes(k)) out.add('cmd')
    else if (['task', 'tasks', 'intent', 'intents', 'runbook-task'].includes(k)) out.add('task')
    else if (['meta', 'meta-skill', 'metaskill', 'meta-skills'].includes(k)) out.add('meta')
    else if (['corr', 'corroborated', 'deep'].includes(k)) out.add('corr')
    else if (['runbook', 'runbooks'].includes(k)) { out.add('cmd'); out.add('task'); out.add('corr') }
  }
  return out.size ? out : new Set(['cmd', 'task', 'meta', 'corr'])
}

function recencyBoost(last, maxDate) {
  if (!last || !maxDate) return 0.5
  const d = (Date.parse(maxDate) - Date.parse(last)) / 864e5
  return d <= 30 ? 1 : d <= 90 ? 0.6 : 0.3
}
function specificityOf(key) {
  const heads = key.split(' ▸ ')
  const non = heads.filter(h => !COMMON.has(h.split(' ')[0])).length
  return heads.length ? non / heads.length : 0.5
}

export function report(db, args, write = (s) => process.stdout.write(s)) {
  const WK = wantedKinds(args.kinds)
  const dayFilter = args.days > 0 ? `AND s.date >= '${new Date(Date.now() - args.days * 864e5).toISOString().slice(0, 10)}'` : ''
  const like = args.filter ? args.filter.replace(/'/g, "''") : ''

  const scope = db.prepare(`SELECT COUNT(*) c, COUNT(DISTINCT project_id) p, MIN(date) d0, MAX(date) d1 FROM sessions s WHERE 1=1 ${dayFilter}`).get()
  if (!scope.c) {
    write('📜 lore · the corpus is empty - index something first: mine-skills.mjs index --scan <project>\n')
    return { scope: { sessions: 0, projects: 0 }, runbooks: [], intents: [], metas: [], corroborated: [] }
  }
  const maxDate = scope.d1
  const projNames = Object.fromEntries(db.prepare('SELECT DISTINCT project_id, project_name FROM sessions').all().map(r => [r.project_id, r.project_name]))
  const crossProject = scope.p > 1

  function score(r, reg, spec) {
    return +(0.25 * Math.min(r.sess / 30, 1) + 0.1 * Math.min(((Date.parse(r.last) - Date.parse(r.first)) / 864e5 || 0) / 120, 1)
      + 0.1 * recencyBoost(r.last, maxDate) + 0.15 * reg + 0.15 * spec
      + 0.25 * (r.done > 0 ? r.ok / r.done : 0.5)).toFixed(3)   // outcome success-rate is a first-class term
  }
  const OUTCOME_COLS = `
    SUM(CASE WHEN e.outcome='success' THEN 1 ELSE 0 END) ok,
    SUM(CASE WHEN e.outcome='corrected' THEN 1 ELSE 0 END) bad,
    SUM(CASE WHEN e.outcome IN ('success','corrected') THEN 1 ELSE 0 END) done`

  // RUNBOOKS - command grams with outcome rates
  let runbooks = []
  if (WK.has('cmd')) {
    runbooks = db.prepare(`
      SELECT g.gram key, COUNT(DISTINCT e.session_id) sess, COUNT(DISTINCT s.project_id) np,
             COUNT(DISTINCT s.author) na, GROUP_CONCAT(DISTINCT s.author) alist,
             GROUP_CONCAT(DISTINCT s.project_id) plist, MIN(s.date) first, MAX(s.date) last, ${OUTCOME_COLS},
             MIN(e.id) sample_ep
      FROM grams g JOIN beats e ON e.id=g.beat_id JOIN sessions s ON s.id=e.session_id
      WHERE 1=1 ${dayFilter} ${like ? `AND lower(g.gram) LIKE '%${like}%'` : ''}
      GROUP BY g.gram HAVING sess >= ? ORDER BY sess DESC LIMIT 400`).all(args.minSessions)
    runbooks.forEach(r => { r.kind = 'runbook-cmd'; r.score = score(r, Math.min(r.key.split(' ▸ ').length / 4, 1), specificityOf(r.key)) })
    runbooks.sort((a, b) => b.score - a.score)
    runbooks = runbooks.filter((c, idx) => !runbooks.some((o, j) => j < idx && o.key.includes(c.key) && o.key.length > c.key.length && o.sess >= c.sess))
  }

  // INTENTS - task signatures with outcome rates
  let intents = []
  if (WK.has('task')) {
    intents = db.prepare(`
      SELECT e.intent_sig key, COUNT(DISTINCT e.session_id) sess, COUNT(DISTINCT s.project_id) np,
             COUNT(DISTINCT s.author) na, GROUP_CONCAT(DISTINCT s.author) alist,
             GROUP_CONCAT(DISTINCT s.project_id) plist, MIN(s.date) first, MAX(s.date) last, ${OUTCOME_COLS},
             MIN(e.id) sample_ep
      FROM beats e JOIN sessions s ON s.id=e.session_id
      WHERE e.intent_sig IS NOT NULL ${dayFilter}
        ${like ? `AND (lower(e.intent_sig) LIKE '%${like}%' OR lower(e.intent_raw) LIKE '%${like}%')` : ''}
      GROUP BY e.intent_sig HAVING sess >= ? ORDER BY sess DESC LIMIT 200`).all(args.minSessions)
    intents.forEach(r => { r.kind = 'runbook-task'; r.score = score(r, 0.7, 0.5) })
    intents.sort((a, b) => b.score - a.score)
  }

  // META - detector hits
  let metas = []
  if (WK.has('meta')) {
    metas = db.prepare(`
      SELECT m.meta_id key, COUNT(DISTINCT e.session_id) sess, COUNT(DISTINCT s.project_id) np,
             COUNT(DISTINCT s.author) na, GROUP_CONCAT(DISTINCT s.author) alist,
             GROUP_CONCAT(DISTINCT s.project_id) plist, MIN(s.date) first, MAX(s.date) last, ${OUTCOME_COLS},
             MIN(e.id) sample_ep
      FROM meta_hits m JOIN beats e ON e.id=m.beat_id JOIN sessions s ON s.id=e.session_id
      WHERE 1=1 ${dayFilter} ${like ? `AND lower(m.quote) LIKE '%${like}%'` : ''}
      GROUP BY m.meta_id HAVING sess >= ? ORDER BY sess DESC LIMIT 50`).all(Math.max(2, args.minSessions - 1))
    metas.forEach(r => { r.kind = 'meta-skill'; r.score = score(r, 0.7, 0.5) })
    metas.sort((a, b) => b.score - a.score)
  }

  // CORROBORATED - intent × runbook co-occurring in the SAME beats (the deep-skill seeds)
  let corr = []
  if (WK.has('corr')) {
    corr = db.prepare(`
      SELECT e.intent_sig isig, g.gram gram, COUNT(DISTINCT e.id) eps, COUNT(DISTINCT e.session_id) sess,
             COUNT(DISTINCT s.project_id) np, COUNT(DISTINCT s.author) na, GROUP_CONCAT(DISTINCT s.author) alist,
             GROUP_CONCAT(DISTINCT s.project_id) plist,
             MIN(s.date) first, MAX(s.date) last, ${OUTCOME_COLS}, MIN(e.id) sample_ep
      FROM beats e JOIN grams g ON g.beat_id=e.id JOIN sessions s ON s.id=e.session_id
      WHERE e.intent_sig IS NOT NULL AND g.n >= 2 ${dayFilter}
        ${like ? `AND (lower(e.intent_sig) LIKE '%${like}%' OR lower(g.gram) LIKE '%${like}%')` : ''}
      GROUP BY e.intent_sig, g.gram
      HAVING sess >= ? ORDER BY eps DESC LIMIT 300`).all(Math.max(2, args.minSessions - 1))
    corr.forEach(r => {
      r.kind = 'corroborated'
      r.key = `${r.isig}  ×  ${r.gram}`
      r.score = +(Math.min(r.sess / 15, 1) * 0.4 + (r.done > 0 ? r.ok / r.done : 0.5) * 0.35 + specificityOf(r.gram) * 0.25).toFixed(3)
    })
    corr.sort((a, b) => b.score - a.score)
    corr = corr.filter((c, idx) => !corr.some((o, j) => j < idx && o.isig === c.isig && o.gram.includes(c.gram) && o.sess >= c.sess))
  }

  // evidence: 3 sample beats per candidate (path:line + intent or command)
  const epInfo = db.prepare(`SELECT e.id, e.start_line, e.intent_raw, e.outcome, e.tool_mix, s.path FROM beats e JOIN sessions s ON s.id=e.session_id WHERE e.id=?`)
  const epForGram = db.prepare(`SELECT DISTINCT e.id FROM beats e JOIN grams g ON g.beat_id=e.id JOIN sessions s ON s.id=e.session_id WHERE g.gram=? ${dayFilter} LIMIT 3`)
  const epForSig = db.prepare(`SELECT DISTINCT e.id FROM beats e JOIN sessions s ON s.id=e.session_id WHERE e.intent_sig=? ${dayFilter} LIMIT 3`)
  const epForMeta = db.prepare(`SELECT DISTINCT e.id FROM meta_hits m JOIN beats e ON e.id=m.beat_id JOIN sessions s ON s.id=e.session_id WHERE m.meta_id=? ${dayFilter} LIMIT 3`)
  const epForCorr = db.prepare(`SELECT DISTINCT e.id FROM beats e JOIN grams g ON g.beat_id=e.id JOIN sessions s ON s.id=e.session_id WHERE e.intent_sig=? AND g.gram=? ${dayFilter} LIMIT 3`)
  const cmdSample = db.prepare('SELECT raw, line FROM commands WHERE beat_id=? LIMIT 2')

  function evidence(c) {
    let ids = []
    if (c.kind === 'runbook-cmd') ids = epForGram.all(c.key)
    else if (c.kind === 'runbook-task') ids = epForSig.all(c.key)
    else if (c.kind === 'meta-skill') ids = epForMeta.all(c.key)
    else if (c.kind === 'corroborated') ids = epForCorr.all(c.isig, c.gram)
    return ids.map(r => {
      const e = epInfo.get(r.id)
      const cmds = cmdSample.all(r.id).map(x => x.raw).join('  &&  ')
      return { path: e.path, line: e.start_line, outcome: e.outcome, quote: (e.intent_raw || '').slice(0, 110), cmds: cmds.slice(0, 130) }
    })
  }

  const out = { scope: { sessions: scope.c, projects: scope.p, dateRange: [scope.d0, scope.d1], crossProject },
    runbooks: runbooks.slice(0, args.top), intents: intents.slice(0, args.top),
    metas: metas.slice(0, args.top), corroborated: corr.slice(0, args.top) }
  for (const list of [out.runbooks, out.intents, out.metas, out.corroborated]) for (const c of list) c.evidence = evidence(c)

  if (args.emit === 'json') { write(JSON.stringify(out, null, 2) + '\n'); return out }

  const L = []
  L.push(`📜  lore · ${scope.c} sessions · ${scope.p} project(s) · ${scope.d0}→${scope.d1}`)
  L.push('')
  L.push('<!-- EVIDENCE FOR SYNTHESIS - raw candidates for YOU to name, judge, verify, and forge. Not user-facing. -->')
  const okRate = c => c.done > 0 ? ` · outcomes ${c.ok}✓/${c.bad}✗ (${Math.round(100 * c.ok / c.done)}% ok)` : ''
  const projTag = c => crossProject ? ` · ${c.np}proj [${(c.plist || '').split(',').map(p => projNames[p] || p).join(', ')}]` : ''
  const teamTag = c => (c.na || 0) > 1 ? ` · 👥 ${c.na} authors [${c.alist}]` : ''
  function row(c) {
    L.push(`#### ${c.key}  (score ${c.score} · ${c.sess} sessions${projTag(c)}${teamTag(c)}${okRate(c)} · ${c.first}→${c.last})`)
    for (const ev of c.evidence) {
      L.push(`  - ${ev.path}:${ev.line} [${ev.outcome}] intent=${JSON.stringify(ev.quote)}${ev.cmds ? ` cmds=${JSON.stringify(ev.cmds)}` : ''}`)
    }
  }
  function section(title, rows, splitPortable) {
    L.push(`## ${title}`)
    if (!rows.length) { L.push('_(none above threshold)_'); L.push(''); return }
    if (splitPortable && crossProject) {
      const por = rows.filter(c => c.np >= 2), spec = rows.filter(c => c.np < 2)
      L.push('### PORTABLE (≥2 projects → personal scope)'); por.length ? por.forEach(row) : L.push('_(none)_')
      L.push('### PROJECT-SPECIFIC (1 project → that repo)'); spec.length ? spec.forEach(row) : L.push('_(none)_')
    } else rows.forEach(row)
    L.push('')
  }
  if (WK.has('corr')) section('CORROBORATED - intent × procedure co-occurring in the same beats (DEEP-SKILL SEEDS - verify these first)', out.corroborated, true)
  if (WK.has('cmd')) section('RUNBOOKS - executed command procedures', out.runbooks, true)
  if (WK.has('task')) section('INTENTS - recurring task types from your prompts', out.intents, true)
  if (WK.has('meta')) section('META-SKILLS - how you work', out.metas, true)
  L.push('<!-- END EVIDENCE FOR SYNTHESIS -->')
  L.push('')
  L.push('## Stats')
  L.push(`- corpus: ${args.db}`)
  L.push(`- candidates: ${out.corroborated.length} corroborated, ${out.runbooks.length} runbooks, ${out.intents.length} intents, ${out.metas.length} meta`)
  L.push(`- outcome legend: ✓ next-turn approval (e.g. "ok write a commit"), ✗ next-turn steering correction - free supervision from your own replies`)
  L.push(`- mode: ${crossProject ? 'CROSS-PROJECT' : 'single-project'} · min-sessions ${args.minSessions}` +
    (args.kinds ? ` · kind=${[...WK].join('+')}` : '') + (args.filter ? ` · filter="${args.filter}"` : ''))
  L.push('')
  L.push(...passThroughFooter(db, args, dayFilter, out))
  write(L.join('\n') + '\n')
  return out
}

// The canonical end-of-mining visual, built deterministically by the engine. The calling agent is
// contractually required (SKILL.md LAW 2) to render this block VERBATIM in its user-facing message -
// the same mechanism last30days uses to keep its emoji-tree footer immune to model improvisation.
function passThroughFooter(db, args, dayFilter, out) {
  const projs = db.prepare(`SELECT project_name n, COUNT(*) c FROM sessions s WHERE 1=1 ${dayFilter} GROUP BY project_name ORDER BY c DESC`).all()
  const agents = db.prepare(`SELECT agent a, COUNT(*) c FROM sessions s WHERE 1=1 ${dayFilter} GROUP BY agent ORDER BY c DESC`).all()
  const eps = db.prepare(`SELECT COUNT(*) c, SUM(e.outcome='success') ok, SUM(e.outcome='corrected') bad FROM beats e JOIN sessions s ON s.id=e.session_id WHERE 1=1 ${dayFilter}`).get()
  const cmds = db.prepare(`SELECT COUNT(*) c FROM commands x JOIN beats e ON e.id=x.beat_id JOIN sessions s ON s.id=e.session_id WHERE 1=1 ${dayFilter}`).get()
  const fmt = (n) => (n || 0).toLocaleString('en-US')
  const dbDisplay = String(args.db).replace(homedir(), '~')
  const body = [
    `🗂  projects: ${projs.map(p => `${p.n} ${fmt(p.c)}`).join(' · ')}`,
    `🧠 beats: ${fmt(eps.c)} · ⚙️ executed commands: ${fmt(cmds.c)}`,
    `🤖 agents: ${agents.map(a => `${a.a} ${fmt(a.c)}`).join(' · ')}`,
    `👥 authors: ${db.prepare(`SELECT author a, COUNT(*) c FROM sessions s WHERE 1=1 ${dayFilter} GROUP BY author ORDER BY c DESC`).all().map(a => `${a.a || '?'} ${fmt(a.c)}`).join(' · ')}`,
    `🎯 outcomes: ${fmt(eps.ok)} ✓ approvals · ${fmt(eps.bad)} ✗ corrections`,
    `🏺 candidates: ${out.corroborated.length} corroborated · ${out.runbooks.length} runbooks · ${out.intents.length} intents · ${out.metas.length} meta`,
    `📦 your lore: ${dbDisplay}`,
  ]
  const tree = body.map((l, i) => `${i === body.length - 1 ? '└─' : '├─'} ${l}`)
  return [
    '<!-- PASS-THROUGH FOOTER: render this block VERBATIM in your user-facing message (SKILL.md LAW 2). -->',
    '---',
    '📜 Lore mined!',
    ...tree,
    '---',
    '<!-- END PASS-THROUGH FOOTER -->',
  ]
}

// ---------- status: the standalone "what has Lore done?" view ----------
// A pass-through visual (LAW 2 applies): corpus snapshot, mined artifacts, forged-registry health,
// and recent activity from the runs journal. The agent renders it verbatim.

import { listRuns } from './db.mjs'
import { listThemes, ensureDossierTable } from './beats.mjs'
import { checkForged } from './forged.mjs'

export function status(db, args, write = (s) => process.stdout.write(s)) {
  const fmt = (n) => (n || 0).toLocaleString('en-US')
  const dbDisplay = String(args.db).replace(homedir(), '~')
  const scope = db.prepare('SELECT COUNT(*) c, COUNT(DISTINCT project_id) p, MIN(date) d0, MAX(date) d1 FROM sessions').get()
  const L = [`📜 Lore status · ${dbDisplay}`, '']
  if (!scope.c) {
    write(L[0] + '\n\n   empty corpus - index something first: mine-skills.mjs index --scan <project>\n')
    return
  }
  const beats = db.prepare("SELECT COUNT(*) c, SUM(outcome='success') ok, SUM(outcome='corrected') bad FROM beats").get()
  const cmds = db.prepare('SELECT COUNT(*) c FROM commands').get()
  const projs = db.prepare('SELECT project_name n, COUNT(*) c FROM sessions GROUP BY 1 ORDER BY c DESC').all()
  const authors = db.prepare('SELECT author a, COUNT(*) c FROM sessions GROUP BY 1 ORDER BY c DESC LIMIT 5').all()
  L.push(`CORPUS    🗂 ${projs.map(p => `${p.n} ${fmt(p.c)}`).join(' · ')}`)
  L.push(`          🧠 ${fmt(beats.c)} beats · ⚙️ ${fmt(cmds.c)} commands · 🎯 ${fmt(beats.ok)} ✓ / ${fmt(beats.bad)} ✗`)
  L.push(`          👥 ${authors.map(a => `${a.a || '?'} ${fmt(a.c)}`).join(' · ')}   (${scope.d0} → ${scope.d1})`)
  L.push('')
  const themes = listThemes(db)
  ensureDossierTable(db)
  const dossiers = db.prepare('SELECT COUNT(*) c FROM dossiers').get()
  const newest = themes.length ? themes[themes.length - 1] : null
  L.push(`MINED     🏺 ${themes.length} themes · 📋 ${dossiers.c} deep dossiers cached`)
  if (newest) L.push(`          newest theme: ${newest.theme_id} (${newest.created.slice(0, 10)})`)
  L.push('')
  const reg = checkForged(db)
  const active = reg.filter(r => r.status === 'active')
  const declined = reg.filter(r => r.status === 'declined')
  const needsWork = reg.filter(r => /^(update|re-engage|orphaned)/.test(r.recommendation || ''))
  L.push(`FORGED    🔨 ${active.length} skills · ${declined.length} declines` +
    (needsWork.length ? ` · ⚠ ${needsWork.length} need attention:` : (reg.length ? ' · registry clean' : '   (nothing forged yet)')))
  for (const r of needsWork.slice(0, 4)) L.push(`          ${r.name}: ${r.recommendation.split(':')[0]}`)
  L.push('')
  const runs = listRuns(db, 5)
  L.push('RECENT' + (runs.length ? '' : '    (no journal entries yet)'))
  for (const r of runs) L.push(`          ${r.ts.slice(0, 16).replace('T', ' ')}  ${r.cmd.padEnd(12)} ${r.scope.padEnd(22).slice(0, 22)} ${r.summary}`)
  write('<!-- PASS-THROUGH STATUS: render verbatim (SKILL.md LAW 2). -->\n' + L.join('\n') + '\n<!-- END PASS-THROUGH STATUS -->\n')
}

// ---------- the skills inventory: every installed skill, lore-forged or not ----------
//
// "What skills do I have and what does each do?" is answerable deterministically: the harness
// skills dirs ARE the install state, each SKILL.md's frontmatter description IS what it does,
// and the forged registry says which ones Lore made and whether their evidence has drifted.

import { readdirSync, readFileSync as readFs, lstatSync, statSync, realpathSync, existsSync as exists } from 'node:fs'
import { join as joinPath } from 'node:path'

// label -> dir for every harness skills location this machine might have.
function harnessRoots() {
  return [
    ['agents', joinPath(homedir(), '.agents', 'skills')],
    ['claude', joinPath(homedir(), '.claude', 'skills')],
    ['codex', joinPath(homedir(), '.codex', 'skills')],
    ['opencode', joinPath(homedir(), '.config', 'opencode', 'skills')],
    ['gemini', joinPath(homedir(), '.gemini', 'skills')],
  ]
}

function skillFrontmatter(dir) {
  try {
    const m = /^---\n([\s\S]*?)\n---/.exec(readFs(joinPath(dir, 'SKILL.md'), 'utf8'))
    if (!m) return null
    const pick = (k) => { const r = new RegExp(`^${k}:\\s*["']?(.+?)["']?\\s*$`, 'm').exec(m[1]); return r ? r[1] : null }
    return { name: pick('name'), description: pick('description') || '' }
  } catch { return null }
}

export function skillsInventory(db, roots = null) {
  const rootList = roots || harnessRoots()
  const byReal = new Map()   // realpath -> {dir, entry, harnesses} (symlink fan-outs dedupe here)
  const broken = []
  for (const [label, root] of rootList) {
    let ents = []
    try { ents = readdirSync(root, { withFileTypes: true }) } catch { continue }
    for (const e of ents) {
      if (e.name.startsWith('.')) continue
      const p = joinPath(root, e.name)
      let real
      try { real = realpathSync(p) } catch {
        try { lstatSync(p); broken.push({ path: p.replace(homedir(), '~'), harness: label }) } catch { /* gone entirely */ }
        continue
      }
      let st
      try { st = statSync(real) } catch { continue }
      if (!st.isDirectory() || !exists(joinPath(real, 'SKILL.md'))) continue
      const cur = byReal.get(real) || { dir: real, entry: e.name, harnesses: [] }
      if (!cur.harnesses.includes(label)) cur.harnesses.push(label)
      byReal.set(real, cur)
    }
  }
  const reg = checkForged(db)
  const regByName = new Map(reg.filter(r => r.status === 'active').map(r => [r.name, r]))
  const forged = [], other = []
  for (const s of byReal.values()) {
    const fm = skillFrontmatter(s.dir) || {}
    const name = fm.name || s.entry
    const row = { name, description: fm.description || '(no description)', harnesses: s.harnesses, dir: s.dir.replace(homedir(), '~') }
    const r = regByName.get(name)
    if (r) {
      row.registry = { sessions: r.now?.sessions ?? r.atForge.sessions, ok: r.now?.ok ?? r.atForge.ok,
        bad: r.now?.bad ?? r.atForge.bad, recommendation: r.recommendation, file: r.file }
      forged.push(row)
    } else {
      other.push(row)
    }
  }
  forged.sort((a, b) => a.name.localeCompare(b.name))
  other.sort((a, b) => a.name.localeCompare(b.name))
  // registered as active but installed nowhere we looked: the registry's orphans
  const missing = [...regByName.values()].filter(r => !forged.some(f => f.name === r.name))
    .map(r => ({ name: r.name, skillPath: (r.skillPath || '').replace(homedir(), '~') }))
  return { forged, other, broken, missing }
}

// Layout rule: every emitted line stays under ~100 columns so terminals never wrap it (a wrapped
// line orphans the tree gutter and turns the view into noise). Names are padded into a column,
// the harness list is compressed, and descriptions are clipped to one line.
export function renderSkillsInventory(db, roots = null, write = (s) => process.stdout.write(s)) {
  const inv = skillsInventory(db, roots)
  const clip = (s, n) => (s.length > n ? s.slice(0, n - 1).trimEnd() + '…' : s)
  const where = (h) => (h.length >= 4 ? 'everywhere' : h.join('+'))
  const all = [...inv.forged, ...inv.other]
  const nameW = Math.min(28, Math.max(12, ...all.map(s => s.name.length)))
  const pad = (s) => (s.length > nameW ? s : s.padEnd(nameW))
  const L = []
  L.push(`⚒ FORGED BY LORE (${inv.forged.length})` + (inv.forged.length ? '' : '   nothing forged yet - run /lore to mine candidates'))
  for (const s of inv.forged) {
    const r = s.registry
    L.push(`├─ ${pad(s.name)}  ${r.recommendation.split(':')[0]} · ${r.sessions} sessions · ${r.ok}✓ ${r.bad}✗${r.file === 'hand-edited' ? ' · hand-edited' : ''} · ${where(s.harnesses)}`)
    L.push(`│      ${clip(s.description, 86)}`)
  }
  L.push('')
  L.push(`📚 OTHER INSTALLED SKILLS (${inv.other.length})`)
  for (const s of inv.other) {
    L.push(`├─ ${pad(s.name)}  ${where(s.harnesses)}`)
    L.push(`│      ${clip(s.description, 86)}`)
  }
  if (inv.missing.length) {
    L.push('', `⚠ REGISTERED BUT NOT INSTALLED (${inv.missing.length})`)
    for (const m of inv.missing) L.push(`└─ ${pad(m.name)}  expected at ${m.skillPath} - re-forge or remove`)
  }
  if (inv.broken.length) {
    L.push('', `⚠ BROKEN SYMLINKS (${inv.broken.length})`)
    for (const b of inv.broken) L.push(`└─ ${b.path}   (${b.harness})`)
  }
  write('<!-- PASS-THROUGH SKILLS: render verbatim (SKILL.md LAW 2). -->\n' + L.join('\n') + '\n<!-- END PASS-THROUGH SKILLS -->\n')
}
