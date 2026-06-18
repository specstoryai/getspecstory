// db.mjs - the lore corpus schema (SQLite via node:sqlite, Node >= 22.5; zero deps).

import { mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { DatabaseSync } from 'node:sqlite'

// Bump whenever the PARSER's extraction behavior changes: sessions indexed under an older
// parser are automatically re-parsed on the next `index` run (no manual purge needed).
export const PARSER_VERSION = 6   // 6 = shell control-flow keywords dropped from command heads

export function openDb(path) {
  mkdirSync(dirname(path), { recursive: true })
  const db = new DatabaseSync(path)
  // one-time migration: the unit was renamed episode -> beat (2026-06-10). In-place renames so
  // existing corpora keep their expensive artifacts (themes, dossiers) without re-mining.
  try { db.exec('ALTER TABLE episodes RENAME TO beats') } catch { /* already migrated or fresh */ }
  for (const [t, a, b] of [['commands', 'episode_id', 'beat_id'], ['grams', 'episode_id', 'beat_id'],
    ['meta_hits', 'episode_id', 'beat_id'], ['themes', 'episode_keys', 'beat_keys'], ['sessions', 'episodes', 'beats']]) {
    try { db.exec(`ALTER TABLE ${t} RENAME COLUMN ${a} TO ${b}`) } catch { /* already migrated or fresh */ }
  }
  db.exec(`
    PRAGMA journal_mode=WAL;
    PRAGMA busy_timeout=10000;   -- concurrent runs (e.g. a session-end hook + a manual index) wait, not crash
    CREATE TABLE IF NOT EXISTS sessions(
      id TEXT PRIMARY KEY, project_id TEXT, project_name TEXT, path TEXT, date TEXT,
      agent TEXT, size INTEGER, beats INTEGER);
    CREATE TABLE IF NOT EXISTS beats(
      id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, ord INTEGER, start_line INTEGER,
      intent_raw TEXT, intent_sig TEXT,
      n_tools INTEGER, tool_mix TEXT, files TEXT, n_cmds INTEGER, exit_fails INTEGER,
      outcome TEXT);
    CREATE TABLE IF NOT EXISTS commands(
      beat_id INTEGER, ord INTEGER, head TEXT, raw TEXT, line INTEGER);
    CREATE TABLE IF NOT EXISTS grams(
      beat_id INTEGER, n INTEGER, gram TEXT);
    CREATE TABLE IF NOT EXISTS meta_hits(
      beat_id INTEGER, meta_id TEXT, quote TEXT, line INTEGER);
    CREATE INDEX IF NOT EXISTS idx_ep_session ON beats(session_id);
    CREATE INDEX IF NOT EXISTS idx_grams ON grams(gram);
    CREATE INDEX IF NOT EXISTS idx_meta ON meta_hits(meta_id);
    CREATE INDEX IF NOT EXISTS idx_cmd_ep ON commands(beat_id);
  `)
  // migrations for corpora created before these columns existed
  for (const col of ['mtime INTEGER DEFAULT 0', 'parser INTEGER DEFAULT 0', "author TEXT DEFAULT ''", "uuid TEXT DEFAULT ''"]) {
    try { db.exec(`ALTER TABLE sessions ADD COLUMN ${col}`) } catch { /* already there */ }
  }
  return db
}

// ---------- the runs journal: Lore's memory of its own activity ----------
// One row per notable engine invocation (auto) plus one per /lore run (the agent's Step 6 duty).
// This is what makes "what has Lore done?" a query instead of archaeology.

export function ensureRunsTable(db) {
  db.exec(`CREATE TABLE IF NOT EXISTS runs(
    id INTEGER PRIMARY KEY AUTOINCREMENT, ts TEXT, cmd TEXT, scope TEXT, summary TEXT)`)
}

export function addRun(db, cmd, scope, summary) {
  ensureRunsTable(db)
  db.prepare('INSERT INTO runs(ts, cmd, scope, summary) VALUES(?,?,?,?)')
    .run(new Date().toISOString(), cmd, scope || '', summary || '')
}

export function listRuns(db, limit = 10) {
  ensureRunsTable(db)
  return db.prepare('SELECT ts, cmd, scope, summary FROM runs ORDER BY id DESC LIMIT ?').all(limit)
}

// Delete a session's beat children + beats (shared by re-index and prune).
export function deleteSessionRows(db, sessionId) {
  for (const r of db.prepare('SELECT id FROM beats WHERE session_id=?').all(sessionId)) {
    for (const t of ['commands', 'grams', 'meta_hits']) db.prepare(`DELETE FROM ${t} WHERE beat_id=?`).run(r.id)
  }
  db.prepare('DELETE FROM beats WHERE session_id=?').run(sessionId)
}
