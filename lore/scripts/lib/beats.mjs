// beats.mjs - deterministic beat-span export + dossier cache (the Phase C primitives).
//
// Deep-mine agents must never grep whole transcripts. The corpus knows every beat's
// (path, start_line) and its successor's start_line, so we can slice the EXACT span: the user's
// intent, the agent's method, bounded. `exportBeats` returns those spans for one cluster,
// stratified so the corrected (failure) beats are always included - they carry the
// failure-modes content that makes a skill deep.
//
// The dossier cache stores deep-mine results keyed by (cluster_key, fingerprint). The fingerprint
// is computed from stable beat identity (session_id, ord, start_line, outcome, n_cmds) - NOT
// autoincrement ids - so it survives re-indexing of unchanged content and invalidates when the
// underlying beats actually change.

import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { tokenize, COMMON } from './patterns.mjs'

// Resolve a cluster selector to beat rows. sel: one of {corr:"isig × gram", gram, sig, meta, theme}.
export function selectBeats(db, sel) {
  const base = `SELECT e.id, e.session_id, e.ord, e.start_line, e.outcome, e.n_cmds, e.tool_mix,
                       s.path, s.agent, s.project_name, s.date
                FROM beats e JOIN sessions s ON s.id = e.session_id`
  if (sel.corr) {
    const parts = sel.corr.split('×').map(s => s.trim())
    if (parts.length !== 2) throw new Error('--corr expects "<intent_sig> × <gram>"')
    return db.prepare(`${base} JOIN grams g ON g.beat_id = e.id
      WHERE e.intent_sig = ? AND g.gram = ? ORDER BY s.date DESC`).all(parts[0], parts[1])
  }
  if (sel.gram) return db.prepare(`${base} JOIN grams g ON g.beat_id = e.id WHERE g.gram = ? ORDER BY s.date DESC`).all(sel.gram)
  if (sel.sig) return db.prepare(`${base} WHERE e.intent_sig = ? ORDER BY s.date DESC`).all(sel.sig)
  if (sel.meta) return db.prepare(`${base} JOIN meta_hits m ON m.beat_id = e.id WHERE m.meta_id = ? ORDER BY s.date DESC`).all(sel.meta)
  if (sel.theme) {
    // themes resolve through their stored member keys, not a column join. The "theme:" prefix is
    // tolerated because deep-mine dossiers for themes are cached under that key shape.
    const t = getTheme(db, String(sel.theme).replace(/^theme:/, ''))
    return t ? rowsByKeys(db, JSON.parse(t.beat_keys)) : []
  }
  throw new Error('beats: need --corr, --gram, --sig, --meta, or --theme')
}

// Stable content fingerprint for a set of beats (order-independent).
export function beatsFingerprint(rows) {
  const h = createHash('sha256')
  for (const k of rows.map(r => `${r.session_id}|${r.ord}|${r.start_line}|${r.outcome}|${r.n_cmds}`).sort()) h.update(k + '\n')
  return h.digest('hex').slice(0, 16)
}

// Stratified pick: ALL corrected first (failure modes), then success, then recent neutral.
function stratify(rows, max) {
  const by = (o) => rows.filter(r => r.outcome === o)
  const picked = [...by('corrected'), ...by('success'), ...by('neutral'), ...by('end')]
  return picked.slice(0, max)
}

// Shared span slicer: beat rows -> bounded transcript spans with stable keys.
// The KEY is (session_id # ord) - stable across re-indexing (autoincrement ids are not).
export function exportRows(db, rows, opts = {}, label = '') {
  const spanLines = opts.spanLines || 120
  const nextLine = db.prepare('SELECT start_line FROM beats WHERE session_id = ? AND ord = ?')
  const fileCache = new Map()
  const beats = []
  for (const r of rows) {
    let lines = fileCache.get(r.path)
    if (!lines) {
      try { lines = readFileSync(r.path, 'utf8').split('\n') } catch { continue }   // pruned/moved file
      fileCache.set(r.path, lines)
    }
    const nxt = nextLine.get(r.session_id, r.ord + 1)
    const end = Math.min(nxt ? nxt.start_line - 1 : lines.length, r.start_line - 1 + spanLines)
    beats.push({
      key: `${r.session_id}#${r.ord}`,
      session: r.session_id, project: r.project_name, agent: r.agent, date: r.date,
      ref: `${r.path}:${r.start_line}`, outcome: r.outcome, toolMix: r.tool_mix,
      truncated: end < (nxt ? nxt.start_line - 1 : lines.length),
      text: lines.slice(r.start_line - 1, end).join('\n'),
    })
  }
  return { cluster: label, exported: beats.length, fingerprint: beatsFingerprint(rows), beats }
}

export function exportBeats(db, sel, opts = {}) {
  const all = selectBeats(db, sel)
  const out = exportRows(db, stratify(all, opts.max || 25), opts, sel.corr || sel.gram || sel.sig || sel.meta)
  out.totalMatching = all.length
  out.fingerprint = beatsFingerprint(all)   // fingerprint the WHOLE cluster, not the sample
  return out
}

// ---------- semantic sampling lenses (theme mining reads what the command channel ignores) ----------

// Beat SHAPE from the stored tool mix - deterministic, no parsing needed.
//   conversation: no tools at all (pure judgment/discussion - 98% of some corpora)
//   read-only:    read/search but nothing executed or written (diagnosis)
//   shell / write: method beats
export function shapeOf(toolMix) {
  if (!toolMix) return 'conversation'
  if (/write/.test(toolMix)) return 'write'
  if (/shell/.test(toolMix)) return 'shell'
  return 'read-only'
}

// Stratified sample for theme miners: filter by project/shape/intent-regex, spread evenly
// across the timeline so eras are represented, cap at max.
export function sampleBeats(db, opts = {}) {
  const where = ['1=1']
  const args = []
  if (opts.project) { where.push('s.project_name = ?'); args.push(opts.project) }
  let rows = db.prepare(`
    SELECT e.session_id, e.ord, e.start_line, e.outcome, e.n_cmds, e.tool_mix, e.intent_raw,
           s.path, s.agent, s.project_name, s.date
    FROM beats e JOIN sessions s ON s.id = e.session_id
    WHERE ${where.join(' AND ')} ORDER BY s.date, e.session_id, e.ord`).all(...args)
  if (opts.shape) rows = rows.filter(r => shapeOf(r.tool_mix) === opts.shape)
  if (opts.intentRe) { const re = new RegExp(opts.intentRe, 'i'); rows = rows.filter(r => re.test(r.intent_raw || '')) }
  if (opts.minIntentLen) rows = rows.filter(r => (r.intent_raw || '').length >= opts.minIntentLen)
  const max = opts.max || 30
  const step = Math.max(1, Math.floor(rows.length / max))
  const picked = []
  for (let i = 0; i < rows.length && picked.length < max; i += step) picked.push(rows[i])
  const out = exportRows(db, picked, opts, `sample(${opts.project || 'all'}/${opts.shape || 'any'}${opts.intentRe ? '/' + opts.intentRe : ''})`)
  out.totalMatching = rows.length
  return out
}

// Resolve stable keys ("session_id#ord") back to beat rows (theme members survive re-indexing).
export function rowsByKeys(db, keys) {
  const q = db.prepare(`
    SELECT e.session_id, e.ord, e.start_line, e.outcome, e.n_cmds, e.tool_mix, e.intent_raw,
           s.path, s.agent, s.project_name, s.date
    FROM beats e JOIN sessions s ON s.id = e.session_id WHERE e.session_id = ? AND e.ord = ?`)
  const rows = []
  for (const k of keys) {
    const h = k.lastIndexOf('#')
    const r = q.get(k.slice(0, h), +k.slice(h + 1))
    if (r) rows.push(r)
  }
  return rows
}

// ---------- themes: semantic clusters proposed by LLM miners, first-class corpus objects ----------

export function ensureThemeTable(db) {
  db.exec(`CREATE TABLE IF NOT EXISTS themes(
    theme_id TEXT PRIMARY KEY, title TEXT, description TEXT,
    beat_keys TEXT,          -- JSON array of stable "session_id#ord" keys
    fingerprint TEXT,           -- beatsFingerprint over the member rows at save time
    evidence TEXT, created TEXT)`)
}

export function putTheme(db, t) {
  ensureThemeTable(db)
  const rows = rowsByKeys(db, t.beatKeys || t.episodeKeys || [])
  db.prepare('INSERT OR REPLACE INTO themes(theme_id,title,description,beat_keys,fingerprint,evidence,created) VALUES(?,?,?,?,?,?,?)')
    .run(t.id, t.title || '', t.description || '', JSON.stringify(t.beatKeys || t.episodeKeys || []),
      beatsFingerprint(rows), JSON.stringify(t.evidence || []), new Date().toISOString())
  return { members: rows.length }
}

export function listThemes(db) {
  ensureThemeTable(db)
  return db.prepare('SELECT theme_id, title, description, beat_keys, fingerprint, created FROM themes ORDER BY created').all()
}

export function getTheme(db, id) {
  ensureThemeTable(db)
  return db.prepare('SELECT * FROM themes WHERE theme_id = ?').get(id) || null
}

// ---------- snowball expansion: grow a theme from anecdote to measurement ----------
//
// A verified theme cites 4-8 beats a miner happened to read. Its member intents carry
// discriminating vocabulary, and the corpus knows every beat's intent - so candidate members are
// a deterministic lexical search, not more transcript reading. The agent verifies only the
// shortlist (via `beats --keys`), then `growTheme` records the confirmed ones. LLM reads stay at
// the margins; the engine does the scanning, which is what scales to 25k-beat corpora.

// Discriminating terms: tokens frequent among member intents but rare corpus-wide (lift-scored).
function discriminatingTerms(members, corpus, maxTerms) {
  const memberSets = members.map(m => new Set(tokenize(m.intent_raw || '')))
  const memberDf = new Map()
  for (const s of memberSets) for (const w of s) memberDf.set(w, (memberDf.get(w) || 0) + 1)
  const need = Math.max(2, Math.ceil(members.length * 0.3))
  const scored = []
  for (const [w, dfM] of memberDf) {
    if (dfM < need || COMMON.has(w)) continue
    const dfC = corpus.df.get(w) || 0
    scored.push({ term: w, lift: (dfM / members.length) / ((dfC + 1) / corpus.total) })
  }
  scored.sort((a, b) => b.lift - a.lift)
  // lift < 3 means the term is barely rarer in the corpus than in the members - near-stopwords
  // ("still", "think", "work") that flood the candidate list. On tiny corpora nothing clears the
  // bar (every term is relatively common), so fall back to the strongest terms unthresholded.
  const strong = scored.filter(s => s.lift >= 3)
  return (strong.length >= 4 ? strong : scored).slice(0, maxTerms).map(s => s.term)
}

export function expandTheme(db, id, opts = {}) {
  const t = getTheme(db, id)
  if (!t) throw new Error(`expand: no such theme "${id}"`)
  const memberKeys = new Set(JSON.parse(t.beat_keys))
  const members = rowsByKeys(db, [...memberKeys])
  if (!members.length) throw new Error(`expand: theme "${id}" has no resolvable members`)
  const all = db.prepare(`
    SELECT e.session_id, e.ord, e.intent_raw, e.outcome, s.project_name, s.date
    FROM beats e JOIN sessions s ON s.id = e.session_id WHERE e.intent_raw != ''`).all()
  const df = new Map()
  const tokenCache = new Map()
  for (const r of all) {
    const set = new Set(tokenize(r.intent_raw))
    tokenCache.set(`${r.session_id}#${r.ord}`, set)
    for (const w of set) df.set(w, (df.get(w) || 0) + 1)
  }
  const terms = discriminatingTerms(members, { df, total: all.length }, opts.maxTerms || 12)
  const minScore = opts.minScore || Math.min(3, Math.max(2, Math.floor(terms.length / 4)))
  const candidates = []
  for (const r of all) {
    const key = `${r.session_id}#${r.ord}`
    if (memberKeys.has(key)) continue
    const set = tokenCache.get(key)
    const score = terms.reduce((n, w) => n + (set.has(w) ? 1 : 0), 0)
    if (score >= minScore) {
      candidates.push({ key, score, outcome: r.outcome, project: r.project_name, date: r.date,
        intent: (r.intent_raw || '').slice(0, 160) })
    }
  }
  candidates.sort((a, b) => b.score - a.score || (b.date || '').localeCompare(a.date || ''))
  const max = opts.max || 100
  return {
    theme: id, terms, minScore, members: members.length,
    totalCandidates: candidates.length,
    candidates: candidates.slice(0, max),
  }
}

// Append verified member keys to a theme (dedup, refingerprint). The agent confirms candidates by
// reading their spans (`beats --keys`) BEFORE calling this - growth without verification would
// turn measured prevalence back into keyword soup.
export function growTheme(db, id, keys) {
  const t = getTheme(db, id)
  if (!t) throw new Error(`grow: no such theme "${id}"`)
  const before = JSON.parse(t.beat_keys)
  const merged = [...new Set([...before, ...keys])]
  const rows = rowsByKeys(db, merged)
  db.prepare('UPDATE themes SET beat_keys = ?, fingerprint = ? WHERE theme_id = ?')
    .run(JSON.stringify(merged), beatsFingerprint(rows), id)
  return { members: merged.length, added: merged.length - before.length }
}

// ---------- dossier cache ----------

export function ensureDossierTable(db) {
  db.exec(`CREATE TABLE IF NOT EXISTS dossiers(
    cluster_key TEXT PRIMARY KEY, fingerprint TEXT, json TEXT, created TEXT)`)
}

export function getDossier(db, key) {
  ensureDossierTable(db)
  return db.prepare('SELECT cluster_key, fingerprint, json, created FROM dossiers WHERE cluster_key = ?').get(key) || null
}

export function putDossier(db, key, fingerprint, json) {
  ensureDossierTable(db)
  db.prepare('INSERT OR REPLACE INTO dossiers(cluster_key, fingerprint, json, created) VALUES(?,?,?,?)')
    .run(key, fingerprint, json, new Date().toISOString())
}

// One dossier card as markdown lines (shared by renderDossiers and renderForgePlan).
// Layout principle: one idea per line, lists not ·-joined runs, blank lines between field groups.
// opts.ord numbers the card in a plan; opts.name lets the plan show the user-chosen skill name.
function dossierCardLines(r, opts = {}) {
  let d
  try { d = JSON.parse(r.json) } catch { return [] }
  const fm = (d.failureModes || [])
  const L = []
  L.push(`### ${opts.ord ? `${opts.ord} · ` : ''}${opts.name || d.name || r.cluster_key}`)
  L.push([`**${d.confidence || '?'} confidence**`, d.verdict ? `verifier: ${d.verdict}` : null, `from \`${r.cluster_key}\``].filter(Boolean).join(' · '))
  if (d.trigger) L.push('', `> **Use when:** ${d.trigger}`)
  if (d.preconditions?.length) { L.push('', '**Needs first:**'); for (const p of d.preconditions) L.push(`- ${p}`) }
  if (d.steps?.length) { L.push('', '**The method:**'); d.steps.forEach((s, i) => L.push(`${i + 1}. ${s}`)) }
  if (d.variations?.length) { L.push('', '**Variations seen:**'); for (const v of d.variations) L.push(`- ${v}`) }
  if (d.verification?.length) { L.push('', '**Done when:**'); for (const v of d.verification) L.push(`- ${v}`) }
  if (fm.length) {
    L.push('', '**When it goes wrong:**')
    for (const f of fm) { L.push(`- ⚠ ${f.what}`); L.push(`  ↳ ${f.recovery}${f.ref ? `  (\`${f.ref}\`)` : ''}`) }
  }
  if (d.problems?.length) { L.push('', '**Apply before forging:**'); for (const p of d.problems) L.push(`- ${p}`) }
  return L
}

// One theme card as markdown lines (shared by renderThemes and renderForgePlan).
function themeCardLines(db, r, opts = {}) {
  let keys = [], ev = []
  try { keys = JSON.parse(r.beat_keys) } catch { /* empty */ }
  try { ev = JSON.parse(r.evidence) } catch { /* empty */ }
  const members = rowsByKeys(db, keys)
  const fresh = beatsFingerprint(members) === r.fingerprint ? 'evidence unchanged' : 'evidence has GROWN since mining'
  const meta = ev.find(e => e.key === 'meta')
  const quotes = ev.filter(e => e.key !== 'meta').slice(0, 3)
  const L = []
  L.push(`### ${opts.ord ? `${opts.ord} · ` : ''}${opts.name || r.theme_id}`)
  L.push([`**latent practice**`, `${members.length} beats`, fresh,
    opts.name && opts.name !== r.theme_id ? `from theme \`${r.theme_id}\`` : null].filter(Boolean).join(' · '))
  L.push('', `**${r.title}**`, '', r.description)
  const lift = themeLift(db, keys)
  if (lift) L.push('', lift)
  if (meta) L.push('', `*Miner's note: ${meta.quote}*`)
  if (quotes.length) {
    L.push('', '**In their own words:**')
    for (const q of quotes) { L.push(`> "${q.quote}"`); L.push(`>   (\`${q.key}\`)`) }
  }
  return L
}

// Outcome lift: does the practice WORK? Success rate over the theme's judged member beats
// (success/corrected only - neutral and end carry no judgment) vs the corpus baseline. Only shown
// with enough judged members to mean something. This is the strongest forging argument a theme
// can carry: not "you do this" but "when you do this, it ends in approval more often".
function themeLift(db, keys) {
  const judged = rowsByKeys(db, keys).filter(m => m.outcome === 'success' || m.outcome === 'corrected')
  if (judged.length < 3) return null
  const ok = judged.filter(m => m.outcome === 'success').length
  const base = db.prepare(`SELECT SUM(outcome='success') ok, SUM(outcome='corrected') bad FROM beats`).get()
  const baseJudged = (base.ok || 0) + (base.bad || 0)
  if (!baseJudged) return null
  const pct = Math.round(100 * ok / judged.length)
  const basePct = Math.round(100 * (base.ok || 0) / baseJudged)
  const delta = pct - basePct
  return `**Outcome:** ${pct}% ✓ over ${judged.length} judged beats · corpus baseline ${basePct}% (${delta >= 0 ? '+' : ''}${delta} pts)`
}

// Render cached dossiers as canonical user-facing markdown (pass-through > authorship: the agent
// pastes this verbatim instead of authoring dossier prose, per SKILL.md LAW 1). Ends with the
// LAW 1 sentinel so the dossier message is mechanically checkable.
export function renderDossiers(db, key = null) {
  ensureDossierTable(db)
  const rows = key
    ? [db.prepare('SELECT * FROM dossiers WHERE cluster_key = ?').get(key)].filter(Boolean)
    : db.prepare('SELECT * FROM dossiers ORDER BY created').all()
  if (!rows.length) return null
  const L = ['<!-- PASS-THROUGH DOSSIERS: render verbatim, then ask (SKILL.md LAW 1). -->']
  for (const r of rows) {
    const card = dossierCardLines(r)
    if (card.length) L.push(...card, '')
  }
  L.push(`=== dossiers above: ${rows.length} ===`)
  L.push('<!-- END PASS-THROUGH DOSSIERS -->')
  return L.join('\n')
}

// Render saved themes as human-readable cards (pass-through, like renderDossiers): the latent
// practice, why it is latent, verbatim evidence, member count, and whether the underlying beats
// have changed since the theme was mined (fingerprint check).
export function renderThemes(db, key = null) {
  ensureThemeTable(db)
  const rows = key
    ? [db.prepare('SELECT * FROM themes WHERE theme_id = ?').get(key)].filter(Boolean)
    : db.prepare('SELECT * FROM themes ORDER BY created').all()
  if (!rows.length) return null
  const L = ['<!-- PASS-THROUGH THEMES: render verbatim (these are mined latent practices). -->']
  for (const r of rows) L.push(...themeCardLines(db, r), '')
  L.push(`=== themes above: ${rows.length} ===`)
  L.push('<!-- END PASS-THROUGH THEMES -->')
  return L.join('\n')
}

// ---------- the forge plan: the ENTIRE curation document, engine-assembled ----------
//
// Models summarize when they compose and paste when handed a finished artifact - the thin-plan
// failure (SKILL.md failure mode #3) came from asking the agent to assemble the plan from pieces.
// renderForgePlan builds the whole plan body deterministically from a manifest; the agent's only
// job is to pass stdout UNEDITED to ExitPlanMode (a PreToolUse hook rejects anything else).
//
// manifest: {
//   project?: string, scope?: string,
//   proposed: [ {cluster: "<dossier cache key>", name?} | {theme: "<theme_id>", name?} ],
//   skipped:  [ {candidate, reason} ]
// }
// A theme that was deep-mined can be proposed by its dossier key ({cluster: "theme:<id>"});
// {theme: "<id>"} renders the verified theme card directly when no dossier exists.
export function renderForgePlan(db, manifest) {
  const proposed = manifest.proposed || []
  if (!proposed.length) throw new Error('plan: manifest.proposed is empty - nothing to curate')
  ensureDossierTable(db)
  ensureThemeTable(db)
  const L = ['<!-- PASS-THROUGH FORGE PLAN: present this document VERBATIM as the plan body (SKILL.md LAW 1, failure mode #3). -->']
  L.push(`# ⚒ Forge plan · ${manifest.project || 'your'} lore`)
  L.push('')
  const stats = db.prepare('SELECT (SELECT COUNT(*) FROM sessions) s, (SELECT COUNT(*) FROM beats) b').get()
  L.push(`📜 ${stats.s.toLocaleString('en-US')} sessions · ${stats.b.toLocaleString('en-US')} beats · ${proposed.length} candidate${proposed.length === 1 ? '' : 's'} proposed`)
  L.push('')
  L.push(`## Proposed (${proposed.length})`)
  proposed.forEach((p, i) => {
    L.push('', '---', '')
    if (p.theme) {
      const r = getTheme(db, p.theme)
      if (!r) throw new Error(`plan: no saved theme "${p.theme}" - run the theme sweep and \`theme put\` first`)
      L.push(...themeCardLines(db, r, { ord: i + 1, name: p.name }))
    } else {
      const r = db.prepare('SELECT * FROM dossiers WHERE cluster_key = ?').get(p.cluster)
      if (!r) throw new Error(`plan: no cached dossier for "${p.cluster}" - deep-mine it first (Step 2c)`)
      L.push(...dossierCardLines(r, { ord: i + 1, name: p.name }))
    }
  })
  const skipped = manifest.skipped || []
  if (skipped.length) {
    L.push('', '---', '', `## Skipping (${skipped.length})`, '')
    for (const k of skipped) L.push(`- **${k.candidate}**: ${k.reason || 'no reason given'}`)
  }
  L.push('', '---', '', '## On approval', '')
  L.push(`1. Forge each skill to ${manifest.scope || 'personal scope (~/.agents/skills/<name>)'}`)
  L.push('2. Symlink into every harness skills dir')
  L.push('3. Register each with `forged add`; record a `forged decline` for every skip')
  L.push('')
  L.push(`=== dossiers above: ${proposed.length} ===`)
  return L.join('\n')
}

// ---------- plan persistence: a canceled forge must be recallable ----------
//
// Every rendered plan saves its MANIFEST (the judgments), not the rendered text: re-rendering
// later re-reads the corpus, so a recalled plan shows current evidence (grown themes, fresh
// fingerprints) instead of a stale snapshot.

export function ensurePlansTable(db) {
  db.exec(`CREATE TABLE IF NOT EXISTS plans(
    id INTEGER PRIMARY KEY AUTOINCREMENT, created TEXT, project TEXT, manifest TEXT)`)
}

export function savePlan(db, manifest) {
  ensurePlansTable(db)
  db.prepare('INSERT INTO plans(created, project, manifest) VALUES(?,?,?)')
    .run(new Date().toISOString(), manifest.project || '', JSON.stringify(manifest))
}

export function lastPlan(db) {
  ensurePlansTable(db)
  return db.prepare('SELECT * FROM plans ORDER BY id DESC LIMIT 1').get() || null
}

export function listPlans(db, limit = 10) {
  ensurePlansTable(db)
  return db.prepare('SELECT id, created, project, manifest FROM plans ORDER BY id DESC LIMIT ?').all(limit)
    .map(r => {
      let m = {}
      try { m = JSON.parse(r.manifest) } catch { /* keep empty */ }
      return { id: r.id, created: r.created, project: r.project,
        proposed: (m.proposed || []).map(p => p.name || p.cluster || p.theme), skipped: (m.skipped || []).length }
    })
}
