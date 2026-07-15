// db.mjs - decisions2 corpus schema (SQLite via node:sqlite, Node >= 22.5; zero deps).
//
// The schema is arc-oriented (a decision is a PROCESS, not a final choice):
//   sessions  - one per transcript
//   beats     - one per user turn (intent + method + outcome). REUSED from decisions/ indexer.
//   commands  - executed commands per beat (for commit-message seed extraction). REUSED.
//   seeds     - Pass 1 output: decision-shaped commit messages, the high-confidence anchors.
//   fingerprints - Pass 2 output: per-project entity sets + verb-template tables.
//   candidates - Pass 3 output: beats flagged as decision candidates with a ROLE.
//   arcs      - candidates clustered into per-entity (or per-file) process arcs.
//
// A candidate beat has a ROLE (proposed | deliberated | chosen | reversed | provisional | open).
// An arc has a STATE (in-formation | decided | changed | abandoned).
// Commit seeds are `chosen` beats; chat candidates are everything else.

import { mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { DatabaseSync } from 'node:sqlite'

export const PARSER_VERSION = 6   // matches decisions/ (we reuse its parse.mjs + patterns.mjs)

export function openDb(path) {
  mkdirSync(dirname(path), { recursive: true })
  const db = new DatabaseSync(path)
  db.exec(`
    PRAGMA journal_mode=WAL;
    PRAGMA busy_timeout=10000;
    CREATE TABLE IF NOT EXISTS sessions(
      id TEXT PRIMARY KEY, project_id TEXT, project_name TEXT, path TEXT, date TEXT,
      agent TEXT, size INTEGER, beats INTEGER, mtime INTEGER DEFAULT 0, parser INTEGER DEFAULT 0,
      author TEXT DEFAULT '', uuid TEXT DEFAULT '');
    CREATE TABLE IF NOT EXISTS beats(
      id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, ord INTEGER, start_line INTEGER,
      intent_raw TEXT, intent_sig TEXT,
      n_tools INTEGER, tool_mix TEXT, files TEXT, n_cmds INTEGER, exit_fails INTEGER,
      outcome TEXT);
    CREATE TABLE IF NOT EXISTS commands(
      beat_id INTEGER, ord INTEGER, head TEXT, raw TEXT, line INTEGER);
    -- decisions2 tables (the arc-oriented layer):
    CREATE TABLE IF NOT EXISTS seeds(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id TEXT, beat_id INTEGER, line INTEGER,
      project TEXT, author TEXT, date TEXT,
      message TEXT, entity TEXT, verb TEXT, type TEXT,
      evidence TEXT);
    CREATE TABLE IF NOT EXISTS fingerprints(
      project TEXT, kind TEXT, key TEXT, value TEXT,  -- kind: entity|verb|type; value: count or template
      PRIMARY KEY (project, kind, key));
    CREATE TABLE IF NOT EXISTS candidates(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      session_id TEXT, beat_id INTEGER, ord INTEGER, line INTEGER,
      project TEXT, date TEXT,
      role TEXT,            -- proposed | deliberated | chosen | reversed | provisional | open
      entity TEXT,          -- the thing decided about (may be null until agent refines)
      quote TEXT,           -- the beat's intent text (clipped)
      signals TEXT,         -- JSON array: which techniques caught it (grammar, entity:X, tpl:Y, commit)
      evidence TEXT);
    CREATE TABLE IF NOT EXISTS arcs(
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      project TEXT, entity TEXT,     -- the arc's anchor entity (or file/topic)
      state TEXT,            -- in-formation | decided | changed | abandoned
      first_date TEXT, last_date TEXT, reversals INTEGER DEFAULT 0,
      beat_ids TEXT);        -- JSON array of candidate ids in timeline order
    CREATE INDEX IF NOT EXISTS idx_beats_session ON beats(session_id);
    CREATE INDEX IF NOT EXISTS idx_cmds_beat ON commands(beat_id);
    CREATE INDEX IF NOT EXISTS idx_seeds_project ON seeds(project);
    CREATE INDEX IF NOT EXISTS idx_cands_project ON candidates(project);
    CREATE INDEX IF NOT EXISTS idx_arcs_project ON arcs(project);
  `)
  return db
}

export function deleteSessionRows(db, sessionId) {
  // delete decision tables that reference session_id directly
  for (const t of ['seeds', 'candidates']) {
    db.prepare(`DELETE FROM ${t} WHERE session_id=?`).run(sessionId)
  }
  // delete commands via beat_id, then beats, then session
  for (const r of db.prepare('SELECT id FROM beats WHERE session_id=?').all(sessionId)) {
    db.prepare('DELETE FROM commands WHERE beat_id=?').run(r.id)
  }
  db.prepare('DELETE FROM beats WHERE session_id=?').run(sessionId)
  db.prepare('DELETE FROM sessions WHERE id=?').run(sessionId)
}

export function resetDecisionsTables(db) {
  for (const t of ['seeds', 'fingerprints', 'candidates', 'arcs']) db.prepare(`DELETE FROM ${t}`).run()
}
