// fingerprint.mjs - Pass 2: build per-project fingerprints from seeds.
//
// A fingerprint has two dimensions:
//   ENTITY set — conventional-commit scopes + word-boundary symbols from prose (ubiquity-capped).
//   VERB-TEMPLATE set — leading verbs from commit messages, with counts.
//
// The fingerprint is used by Pass 3 (expand) to find less-obvious decision candidates in chat.

const SYMBOL_RE = /\b[a-z][a-z0-9]*(?:[A-Z][a-z0-9]+)+\b|\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b/g
const UBIQUITY_MAX = 30

// Exported: build per-project fingerprints from the seeds table.
// Returns { projects: [{ project, entities: [...], verbs: [{verb, count}] }] }
export function buildFingerprints(db) {
  const seeds = db.prepare('SELECT project, message, entity, verb, type FROM seeds').all()

  // group by project
  const byProject = new Map()
  for (const s of seeds) {
    if (!byProject.has(s.project)) byProject.set(s.project, { project: s.project, seeds: [] })
    byProject.get(s.project).seeds.push(s)
  }

  // ubiquity: count distinct messages per entity GLOBALLY (a product name is ubiquitous everywhere)
  const entityMsgs = new Map()
  for (const s of seeds) {
    if (!s.entity) continue
    if (!entityMsgs.has(s.entity)) entityMsgs.set(s.entity, new Set())
    entityMsgs.get(s.entity).add(s.message.slice(0, 60))
  }
  const ubiquitous = new Set()
  for (const [e, msgs] of entityMsgs) if (msgs.size > UBIQUITY_MAX) ubiquitous.add(e)

  const fingerprints = []
  for (const [project, { seeds: pseeds }] of byProject) {
    const entities = new Set()
    const verbs = new Map()
    for (const s of pseeds) {
      if (s.entity && !ubiquitous.has(s.entity)) entities.add(s.entity)
      // also extract symbols from prose (word-boundary)
      const syms = s.message.match(SYMBOL_RE) || []
      for (const sym of syms) if (sym.length >= 4 && !ubiquitous.has(sym.toLowerCase())) entities.add(sym.toLowerCase())
      if (s.verb) verbs.set(s.verb, (verbs.get(s.verb) || 0) + 1)
    }
    fingerprints.push({
      project,
      entities: [...entities].sort(),
      verbs: [...verbs.entries()].sort((a, b) => b[1] - a[1]).map(([verb, count]) => ({ verb, count })),
    })
  }

  // persist to fingerprints table
  db.prepare('DELETE FROM fingerprints').run()
  const ins = db.prepare('INSERT OR REPLACE INTO fingerprints(project, kind, key, value) VALUES(?,?,?,?)')
  for (const fp of fingerprints) {
    for (const e of fp.entities) ins.run(fp.project, 'entity', e, '1')
    for (const v of fp.verbs) ins.run(fp.project, 'verb', v.verb, String(v.count))
  }

  return { projects: fingerprints, ubiquitous: [...ubiquitous] }
}

// Load a project's fingerprint from the DB (for Pass 3).
export function loadFingerprint(db, project) {
  const entities = db.prepare("SELECT key FROM fingerprints WHERE project=? AND kind='entity'").all(project).map(r => r.key)
  const verbs = db.prepare("SELECT key, value FROM fingerprints WHERE project=? AND kind='verb'").all(project)
  return { project, entities, verbs: verbs.map(v => ({ verb: v.key, count: +v.value })) }
}
