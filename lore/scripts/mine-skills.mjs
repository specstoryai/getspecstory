#!/usr/bin/env node
// lore engine (SpecStory Lore) - CLI entry. The real work lives in purpose-driven modules:
//   lib/patterns.mjs   regexes, vocabularies, outcome classifiers (the verified output formula)
//   lib/discover.mjs   project/transcript discovery + stable project identity (git_id)
//   lib/parse.mjs      pure transcript → beats parsing (unit-testable, no I/O)
//   lib/db.mjs         the SQLite corpus schema (node:sqlite, zero deps)
//   lib/indexer.mjs    incremental indexing of sessions into the corpus
//   lib/report.mjs     corroboration queries, scoring, evidence-block emitters
//
// MULTI-AGENT: reads transcripts from EVERY specstory provider (Claude Code, Codex CLI, Cursor CLI,
// Gemini CLI, Factory Droid CLI, DeepSeek TUI, Antigravity CLI, ...). Session headers are matched
// generically and shell commands are detected via the shared data-tool-type="shell" attribute.
//
// Subcommands:
//   index   --dir <history-dir> [--dir ...] [--projects <parent>] [--db <path>] [--days N]
//   report  [--db <path>] [--min-sessions N] [--top N] [--days N] [--kind cmd,task,meta,corr]
//           [--filter <substr>] [--emit compact|json]
//   (legacy) no subcommand with --dir = index then report, same flags.
//
// Default DB: ~/.specstory/lore.db (your lore - accumulates across projects, agents, and runs).

import { join } from 'node:path'
import { homedir } from 'node:os'
import { openDb, addRun, listRuns } from './lib/db.mjs'
import { indexCorpus, pruneCorpus } from './lib/indexer.mjs'
import { report, status, skillsInventory, renderSkillsInventory } from './lib/report.mjs'
import { exportBeats, exportRows, sampleBeats, rowsByKeys, getDossier, putDossier, renderDossiers, putTheme, listThemes, getTheme, renderThemes, renderForgePlan, expandTheme, growTheme, savePlan, lastPlan, listPlans } from './lib/beats.mjs'
import { addForged, declineCandidate, listForged, checkForged } from './lib/forged.mjs'
import { readFileSync, rmSync } from 'node:fs'
import { dirname } from 'node:path'

function parseArgs(argv) {
  const a = { cmd: '', emit: 'compact', minSessions: 3, top: 12, days: 0, maxBytes: 200_000_000,
    dirs: [], projects: '', db: join(homedir(), '.specstory', 'lore.db'), kinds: null, filter: '', force: false }
  let i = 0
  if (argv[0] && !argv[0].startsWith('--')) { a.cmd = argv[0]; i = 1 }
  for (; i < argv.length; i++) {
    const t = argv[i]
    if (t === '--dir') a.dirs.push(argv[++i])
    else if (t === '--projects') a.projects = argv[++i]
    else if (t === '--scan') a.scan = argv[++i]
    else if (t === '--db') a.db = argv[++i]
    else if (t.startsWith('--emit=')) a.emit = t.slice(7)
    else if (t === '--emit') a.emit = argv[++i]
    else if (t === '--min-sessions') a.minSessions = +argv[++i]
    else if (t === '--top') a.top = +argv[++i]
    else if (t === '--days') a.days = +argv[++i]
    else if (t === '--max-bytes') a.maxBytes = +argv[++i]
    else if (t === '--kind') a.kinds = (argv[++i] || '').split(',').map(s => s.trim().toLowerCase()).filter(Boolean)
    else if (t === '--filter') a.filter = (argv[++i] || '').toLowerCase()
    else if (t === '--force') a.force = true
    else if (t === '--corr') a.corr = argv[++i]
    else if (t === '--gram') a.gram = argv[++i]
    else if (t === '--sig') a.sig = argv[++i]
    else if (t === '--meta') a.meta = argv[++i]
    else if (t === '--max') a.max = +argv[++i]
    else if (t === '--span-lines') a.spanLines = +argv[++i]
    else if (t === '--key') a.key = argv[++i]
    else if (t === '--fingerprint') a.fingerprint = argv[++i]
    else if (t === '--file') a.file = argv[++i]
    else if (t === '--name') a.name = argv[++i]
    else if (t === '--path') a.path = argv[++i]
    else if (t === '--cluster') a.cluster = argv[++i]
    else if (t === '--note') a.note = argv[++i]
    else if (t === '--and-skills') a.andSkills = true
    else if (t === '--shape') a.shape = argv[++i]
    else if (t === '--intent-re') a.intentRe = argv[++i]
    else if (t === '--project') a.project = argv[++i]
    else if (t === '--keys') a.keys = (argv[++i] || '').split(',').map(s => s.trim()).filter(Boolean)
    else if (t === '--theme') a.theme = argv[++i]
    else if (t === '--min-intent-len') a.minIntentLen = +argv[++i]
    else if (t === '--min-score') a.minScore = +argv[++i]
    else if (t === '--roots') a.roots = argv[++i]
    else if (t === '--summary') a.summary = argv[++i]
  }
  if (!a.cmd) a.cmd = (a.dirs.length || a.projects || a.scan) ? 'legacy' : 'report'
  return a
}

const ARGS = parseArgs(process.argv.slice(2))
if (ARGS.cmd === 'episodes') ARGS.cmd = 'beats'   // compat alias: the unit was renamed episode -> beat
const db = openDb(ARGS.db)

if (ARGS.cmd === 'index') {
  const r = indexCorpus(db, ARGS)
  if (r.error) { process.stderr.write(`lore index: ${r.error}\n`); process.exit(2) }
  if (r.indexed) addRun(db, 'index', r.projects.map(p => p.name).join(','), `+${r.indexed} sessions (${r.skippedKnown} unchanged)`)
  process.stdout.write(`indexed ${r.indexed} sessions (${r.skippedKnown} unchanged, ${r.skippedBig} too large) across ${r.projects.length} project(s) → ${ARGS.db}\n`)
} else if (ARGS.cmd === 'report') {
  report(db, ARGS)
} else if (ARGS.cmd === 'prune') {
  const r = pruneCorpus(db)
  process.stdout.write(`pruned ${r.removed} sessions whose transcripts no longer exist (${r.remaining} remain)\n`)
  for (const d of r.dupes) process.stdout.write(`⚠ duplicate project identity for ${d.path}: [${d.pids}] - likely a project that gained a git remote; prune after re-indexing\n`)
  for (const d of r.contentDupes || []) process.stdout.write(`⚠ same session indexed ${d.n}× (uuid ${d.uuid}): ${d.ids} - copied corpus inflates counts; remove one copy and re-index\n`)
} else if (ARGS.cmd === 'beats') {
  // export transcript spans: by cluster (--corr/--gram/--sig/--meta), by stable keys (--keys),
  // by saved theme (--theme), or as a stratified SAMPLE (--shape/--intent-re/--project) for theme mining
  let out
  if (ARGS.theme) {
    const t = getTheme(db, ARGS.theme)
    if (!t) { process.stderr.write('no such theme\n'); process.exit(1) }
    out = exportRows(db, rowsByKeys(db, JSON.parse(t.beat_keys)), { spanLines: ARGS.spanLines }, 'theme:' + ARGS.theme)
  } else if (ARGS.keys) {
    out = exportRows(db, rowsByKeys(db, ARGS.keys), { spanLines: ARGS.spanLines }, 'keys')
  } else if (ARGS.corr || ARGS.gram || ARGS.sig || ARGS.meta) {
    out = exportBeats(db, { corr: ARGS.corr, gram: ARGS.gram, sig: ARGS.sig, meta: ARGS.meta },
      { max: ARGS.max, spanLines: ARGS.spanLines })
  } else {
    out = sampleBeats(db, { project: ARGS.project, shape: ARGS.shape, intentRe: ARGS.intentRe,
      minIntentLen: ARGS.minIntentLen, max: ARGS.max, spanLines: ARGS.spanLines })
  }
  process.stdout.write(JSON.stringify(out, null, 2) + '\n')
} else if (ARGS.cmd === 'theme') {
  // theme put --file <json|->     payload: {id,title,description,beatKeys:["sess#ord",...],evidence:[...]}
  // theme list | theme get --key <id>
  const sub = process.argv[3] && !process.argv[3].startsWith('--') ? process.argv[3] : ''
  if (sub === 'put') {
    const body = JSON.parse(ARGS.file && ARGS.file !== '-' ? readFileSync(ARGS.file, 'utf8') : readFileSync(0, 'utf8'))
    const r = putTheme(db, body)
    addRun(db, 'theme', body.id, `saved (${r.members} member beats)`)
    process.stdout.write(`theme ${body.id} saved (${r.members}/${(body.beatKeys || []).length} member beats resolved)\n`)
  } else if (sub === 'list') {
    process.stdout.write(JSON.stringify(listThemes(db), null, 2) + '\n')
  } else if (sub === 'get') {
    const t = getTheme(db, ARGS.key)
    if (!t) { process.stderr.write('no such theme\n'); process.exit(1) }
    process.stdout.write(JSON.stringify(t, null, 2) + '\n')
  } else if (sub === 'render') {
    const md = renderThemes(db, ARGS.key || null)
    if (!md) { process.stderr.write('no themes saved yet\n'); process.exit(1) }
    process.stdout.write(md + '\n')
  } else if (sub === 'expand') {
    // deterministic snowball: discriminating terms from member intents -> scored corpus-wide
    // candidate list. The agent verifies the shortlist (beats --keys) before `theme grow`.
    try {
      process.stdout.write(JSON.stringify(expandTheme(db, ARGS.key, { max: ARGS.max, minScore: ARGS.minScore }), null, 2) + '\n')
    } catch (err) { process.stderr.write(`${err.message}\n`); process.exit(1) }
  } else if (sub === 'grow') {
    try {
      const r = growTheme(db, ARGS.key, ARGS.keys || [])
      addRun(db, 'theme', ARGS.key, `grew to ${r.members} members (+${r.added} verified)`)
      process.stdout.write(`theme ${ARGS.key} grown to ${r.members} members (+${r.added})\n`)
    } catch (err) { process.stderr.write(`${err.message}\n`); process.exit(1) }
  } else {
    process.stderr.write('usage: mine-skills.mjs theme put|list|get|render|expand|grow [--file J] [--key ID] [--max N] [--min-score S] [--keys k1,k2]\n'); process.exit(2)
  }
} else if (ARGS.cmd === 'dossier') {
  // dossier get --key K            -> cached dossier JSON (exit 1 if absent)
  // dossier put --key K --fingerprint F --file <json-path|-> (stdin)
  const sub = process.argv[3] && !process.argv[3].startsWith('--') ? process.argv[3] : ''
  if (sub === 'get') {
    const d = getDossier(db, ARGS.key)
    if (!d) { process.stderr.write('no cached dossier for key\n'); process.exit(1) }
    process.stdout.write(JSON.stringify(d, null, 2) + '\n')
  } else if (sub === 'put') {
    const body = ARGS.file && ARGS.file !== '-' ? readFileSync(ARGS.file, 'utf8') : readFileSync(0, 'utf8')
    JSON.parse(body)   // validate it is JSON before storing
    putDossier(db, ARGS.key, ARGS.fingerprint || '', body)
    addRun(db, 'dossier', ARGS.key.slice(0, 40), 'deep-mine dossier cached')
    process.stdout.write(`cached dossier for ${ARGS.key}\n`)
  } else if (sub === 'render') {
    // canonical user-facing dossier blocks + LAW 1 sentinel - paste verbatim, then ask
    const md = renderDossiers(db, ARGS.key || null)
    if (!md) { process.stderr.write('no cached dossiers to render\n'); process.exit(1) }
    process.stdout.write(md + '\n')
  } else {
    process.stderr.write('usage: mine-skills.mjs dossier get|put|render --key K [--fingerprint F] [--file J]\n'); process.exit(2)
  }
} else if (ARGS.cmd === 'skills') {
  // skills [--roots "label=dir,label=dir"] [--emit json]
  // The installed-skills inventory: lore-forged (with registry health) + everything else found
  // in the harness skills dirs, deduped through symlinks; broken links and orphans flagged.
  const roots = ARGS.roots
    ? ARGS.roots.split(',').map(s => { const i = s.indexOf('='); return [s.slice(0, i), s.slice(i + 1)] })
    : null
  if (ARGS.emit === 'json') process.stdout.write(JSON.stringify(skillsInventory(db, roots), null, 2) + '\n')
  else renderSkillsInventory(db, roots)
} else if (ARGS.cmd === 'plan') {
  // plan render --file <json|->   assemble the ENTIRE curation plan from a manifest:
  //   {project?, scope?, proposed:[{cluster|theme, name?},...], skipped:[{candidate, reason},...]}
  // stdout IS the plan body the agent presents (verbatim) - the ExitPlanMode hook rejects any
  // plan that is not this artifact, so summarizing is structurally impossible.
  const sub = process.argv[3] && !process.argv[3].startsWith('--') ? process.argv[3] : ''
  if (sub === 'render') {
    try {
      const body = JSON.parse(ARGS.file && ARGS.file !== '-' ? readFileSync(ARGS.file, 'utf8') : readFileSync(0, 'utf8'))
      const md = renderForgePlan(db, body)
      // persist the manifest so a canceled forge is recallable later via `plan last`
      savePlan(db, body)
      addRun(db, 'plan', body.project || '', `rendered ${(body.proposed || []).length} candidate(s)`)
      process.stdout.write(md + '\n')
    } catch (err) {
      process.stderr.write(`${err.message}\n`); process.exit(1)
    }
  } else if (sub === 'last') {
    // recall: re-render the most recent manifest against the CURRENT corpus (fresh evidence)
    const row = lastPlan(db)
    if (!row) { process.stderr.write('no saved plans - render one first\n'); process.exit(1) }
    try { process.stdout.write(renderForgePlan(db, JSON.parse(row.manifest)) + '\n') }
    catch (err) { process.stderr.write(`${err.message}\n`); process.exit(1) }
  } else if (sub === 'list') {
    process.stdout.write(JSON.stringify(listPlans(db), null, 2) + '\n')
  } else {
    process.stderr.write('usage: mine-skills.mjs plan render --file <manifest.json|-> | plan last | plan list\n'); process.exit(2)
  }
} else if (ARGS.cmd === 'forged') {
  // forged add --name N --path P --cluster K [--kind K] [--note ...]   register a forged skill
  // forged decline --cluster K [--kind K] [--note ...]                  remember a user "no"
  // forged list                                                          registry contents
  // forged check                                                         drift report vs current corpus
  // kind (corr | gram | sig | meta | theme) is INFERRED from the cluster's shape when omitted.
  const sub = process.argv[3] && !process.argv[3].startsWith('--') ? process.argv[3] : ''
  const kind = ARGS.kinds ? ARGS.kinds[0] : undefined
  if (sub === 'add') {
    const st = addForged(db, { name: ARGS.name, path: ARGS.path, cluster: ARGS.cluster, kind, note: ARGS.note || '' })
    addRun(db, 'forge', ARGS.name, `forged from ${ARGS.cluster.slice(0, 50)}`)
    process.stdout.write(`registered ${ARGS.name} ← ${ARGS.cluster} (fp ${st.fingerprint}, ${st.sessions} sessions, ${st.ok}✓/${st.bad}✗)\n`)
  } else if (sub === 'decline') {
    declineCandidate(db, { cluster: ARGS.cluster, kind, note: ARGS.note || '' })
    addRun(db, 'decline', ARGS.cluster.slice(0, 40), ARGS.note || 'user declined')
    process.stdout.write(`recorded decline for ${ARGS.cluster}\n`)
  } else if (sub === 'list') {
    process.stdout.write(JSON.stringify(listForged(db), null, 2) + '\n')
  } else if (sub === 'check') {
    process.stdout.write(JSON.stringify(checkForged(db), null, 2) + '\n')
  } else {
    process.stderr.write('usage: mine-skills.mjs forged add|decline|list|check [--name N --path P --cluster K --kind corr|gram|sig|meta|theme --note S]\n'); process.exit(2)
  }
} else if (ARGS.cmd === 'reset') {
  // Wipe ALL Lore persistence: the corpus file (sessions, beats, commands, grams, meta_hits,
  // dossier cache, forged registry). Forged skill FILES on disk are kept unless --and-skills.
  const counts = {}
  for (const t of ['sessions', 'beats', 'commands', 'grams', 'meta_hits', 'dossiers', 'forged']) {
    try { counts[t] = db.prepare(`SELECT COUNT(*) c FROM ${t}`).get().c } catch { counts[t] = 0 }
  }
  let skills = []
  try { skills = listForged(db).filter(r => r.status === 'active') } catch { /* no registry */ }
  db.close()
  for (const suf of ['', '-wal', '-shm']) rmSync(ARGS.db + suf, { force: true })
  const wiped = Object.entries(counts).filter(([, c]) => c).map(([t, c]) => `${t} ${c}`).join(' · ')
  process.stdout.write(`🧹 lore reset - deleted ${ARGS.db}\n   wiped: ${wiped || '(empty corpus)'}\n`)
  if (skills.length) {
    if (ARGS.andSkills) {
      for (const s of skills) { rmSync(dirname(s.skill_path), { recursive: true, force: true }); process.stdout.write(`   removed forged skill: ${s.name} (${dirname(s.skill_path)})\n`) }
      process.stdout.write('   note: harness symlinks pointing at removed skills are now dangling - clean them up.\n')
    } else {
      process.stdout.write(`   forged skill FILES kept (${skills.map(s => s.name).join(', ')}) - re-run with --and-skills to remove them too\n`)
    }
  }
} else if (ARGS.cmd === 'status') {
  status(db, ARGS)
} else if (ARGS.cmd === 'runs') {
  // runs add --summary "..." [--scope S]   the agent's end-of-run account (SKILL.md Step 6)
  // runs list                              recent journal entries
  const sub = process.argv[3] && !process.argv[3].startsWith('--') ? process.argv[3] : 'list'
  if (sub === 'add') {
    addRun(db, 'lore-run', ARGS.project || '', ARGS.summary || '')
    process.stdout.write('run recorded\n')
  } else {
    process.stdout.write(JSON.stringify(listRuns(db, ARGS.top || 20), null, 2) + '\n')
  }
} else if (ARGS.cmd === 'legacy') {
  const r = indexCorpus(db, ARGS)
  if (r.error) { process.stderr.write(`lore: ${r.error}\n`); process.exit(2) }
  process.stderr.write(`[index] ${r.indexed} new, ${r.skippedKnown} unchanged\n`)
  report(db, ARGS)
} else {
  process.stderr.write('usage: mine-skills.mjs index|report|beats|theme|dossier|plan|skills|forged|prune|reset [flags]\n  index   --dir <hist> | --projects <parent> | --scan <root> [--days N] [--force]\n  report  [--min-sessions N] [--top N] [--kind cmd,task,meta,corr] [--filter S] [--emit json]\n  beats --corr|--gram|--sig|--meta|--theme | --keys K | sampling: --project P --shape S --intent-re R [--max N]\n  theme   put|list|get|render [--file J] [--key ID]\n  dossier get|put|render --key K [--fingerprint F] [--file J]\n  plan    render --file <manifest.json|->  (the full curation plan, engine-assembled)\n  forged  add|decline|list|check [--name N --path P --cluster K]\n  reset   [--and-skills]\n  status  the what-has-lore-done view (corpus, mined, forged, recent activity)\n  runs    add --summary S | list\n')
  process.exit(2)
}
