// forged.mjs - the forged-skill registry: Lore's memory of what it has already surfaced and built.
//
// Three jobs, all deterministic:
// 1. PROVENANCE - when a skill is forged, record which cluster it came from and the evidence
//    state at forge time (fingerprint, session count, outcome rates, content hash of the file).
// 2. DECISIONS - when the user declines a candidate, record that too, so later runs do not
//    re-propose it unless the evidence has grown materially.
// 3. DRIFT - `check` recomputes each cluster's current state and reports what changed since
//    forge time: new beats, new corrected beats (fresh failure-mode material), outcome
//    shifts, and whether the installed file was hand-edited or deleted. The calling agent turns
//    drift into "propose an update to skill X", never a duplicate.

import { readFileSync, existsSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { selectBeats, beatsFingerprint, getTheme } from './beats.mjs'
import { META } from './patterns.mjs'

export function ensureForgedTable(db) {
  db.exec(`CREATE TABLE IF NOT EXISTS forged(
    name TEXT PRIMARY KEY,            -- skill name (or 'declined:<cluster>' for declines)
    status TEXT,                      -- active | declined
    skill_path TEXT,                  -- where the SKILL.md was written ('' for declines)
    cluster_key TEXT, kind TEXT,      -- kind: corr | gram | sig | meta | theme
    fingerprint TEXT,                 -- beatsFingerprint at forge/decline time
    sessions INTEGER, ok INTEGER, bad INTEGER,   -- evidence stats at forge/decline time
    content_sha TEXT,                 -- sha256 of the written SKILL.md (hand-edit detection)
    created TEXT, note TEXT)`)
}

// Infer the cluster kind from its shape when the caller does not say. Order matters:
//   "theme:<id>" or a saved theme id -> theme     "intent × gram" -> corr     "a ▸ b" -> gram
//   a META detector id -> meta                    otherwise -> sig ("write:commit")
export function inferKind(db, key) {
  if (key.startsWith('theme:')) return 'theme'
  if (key.includes(' × ')) return 'corr'
  if (key.includes(' ▸ ')) return 'gram'
  try { if (getTheme(db, key)) return 'theme' } catch { /* no themes table yet */ }
  if (META.some(m => m.id === key)) return 'meta'
  return 'sig'
}

// Evidence stats for a cluster of ANY kind (selectBeats resolves themes too). A stats lookup must
// never lose a registry write: on any failure this degrades to zeros instead of throwing.
function clusterState(db, kind, key) {
  try {
    const rows = selectBeats(db, { [kind]: key })
    const sess = new Set(rows.map(r => r.session_id)).size
    const ok = rows.filter(r => r.outcome === 'success').length
    const bad = rows.filter(r => r.outcome === 'corrected').length
    return { fingerprint: beatsFingerprint(rows), beats: rows.length, sessions: sess, ok, bad }
  } catch (err) {
    process.stderr.write(`forged: stats lookup failed for ${kind}:"${key}" (${err.message}) - recording with empty stats\n`)
    return { fingerprint: '', beats: 0, sessions: 0, ok: 0, bad: 0 }
  }
}

function sha(path) {
  try { return createHash('sha256').update(readFileSync(path)).digest('hex').slice(0, 16) } catch { return '' }
}

export function addForged(db, { name, path, cluster, kind, note = '' }) {
  ensureForgedTable(db)
  kind = kind || inferKind(db, cluster)
  const st = clusterState(db, kind, cluster)
  db.prepare(`INSERT OR REPLACE INTO forged(name,status,skill_path,cluster_key,kind,fingerprint,sessions,ok,bad,content_sha,created,note)
              VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
    .run(name, 'active', path, cluster, kind, st.fingerprint, st.sessions, st.ok, st.bad, sha(path), new Date().toISOString(), note)
  return st
}

export function declineCandidate(db, { cluster, kind, note = '' }) {
  ensureForgedTable(db)
  kind = kind || inferKind(db, cluster)
  const st = clusterState(db, kind, cluster)
  db.prepare(`INSERT OR REPLACE INTO forged(name,status,skill_path,cluster_key,kind,fingerprint,sessions,ok,bad,content_sha,created,note)
              VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
    .run('declined:' + cluster, 'declined', '', cluster, kind, st.fingerprint, st.sessions, st.ok, st.bad, '', new Date().toISOString(), note)
  return st
}

export function listForged(db) {
  ensureForgedTable(db)
  return db.prepare('SELECT * FROM forged ORDER BY created').all()
}

// Material growth: enough new evidence to revisit a decision or refresh a skill.
function materialGrowth(old, cur) {
  return cur.bad > (old.bad || 0)                          // any NEW corrected beat (failure-mode material)
    || cur.sessions >= Math.max((old.sessions || 0) + 3, (old.sessions || 0) * 1.5)
}

// The drift report: for every registered row, compare forge-time state to current corpus state.
export function checkForged(db) {
  ensureForgedTable(db)
  const out = []
  for (const r of listForged(db)) {
    let cur
    try { cur = clusterState(db, r.kind, r.cluster_key) } catch { cur = null }
    const entry = {
      name: r.name, status: r.status, cluster: r.cluster_key, kind: r.kind, skillPath: r.skill_path,
      atForge: { sessions: r.sessions, ok: r.ok, bad: r.bad, fingerprint: r.fingerprint },
      now: cur ? { sessions: cur.sessions, ok: cur.ok, bad: cur.bad, fingerprint: cur.fingerprint } : null,
      drift: cur ? cur.fingerprint !== r.fingerprint : null,
      newCorrected: cur ? Math.max(0, cur.bad - (r.bad || 0)) : 0,
      file: 'n/a',
    }
    if (r.status === 'active') {
      if (!existsSync(r.skill_path)) entry.file = 'missing'
      else entry.file = sha(r.skill_path) === r.content_sha ? 'unchanged' : 'hand-edited'
    }
    // recommendation for the calling agent
    if (r.status === 'declined') {
      entry.recommendation = cur && materialGrowth(r, cur)
        ? 're-engage: evidence grew materially since the user declined'
        : 'suppress: user declined and evidence is unchanged'
    } else if (entry.file === 'missing') {
      entry.recommendation = 'orphaned: skill file deleted - re-forge or remove from registry'
    } else if (cur && materialGrowth(r, cur)) {
      entry.recommendation = (entry.file === 'hand-edited' ? 'update-carefully (user hand-edited the file): ' : 'update: ')
        + `${entry.newCorrected} new corrected beat(s), sessions ${r.sessions}→${cur.sessions} - deep-mine the cluster and propose a diff`
    } else if (entry.drift) {
      entry.recommendation = 'minor drift: cluster changed but not materially - no action needed'
    } else {
      entry.recommendation = 'up-to-date'
    }
    out.push(entry)
  }
  return out
}
