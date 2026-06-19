# Sessions Index (`sessions.db`)

> **STATUS: FIRST CUT RATIFIED; LATER STAGES STRAWMAN.** The first build chunk — the
> `sessions.db` schema, the global-enumeration SPI method, and the `specstory reindex` command
> — is **agreed** and specified in
> [Build Plan — First Cut](#build-plan--first-cut). Everything
> about *keeping the index warm* (steady-state hooks, staleness, eviction) and the cloud
> stages remains strawman; sections still marked **OPEN** are unresolved. The reconstruction
> design this serves lives in [SESSION-PORTABILITY.md](SESSION-PORTABILITY.md).

## Purpose

`specstory resume` (cross-provider and, next, cross-project resume — see
[SESSION-PORTABILITY.md](SESSION-PORTABILITY.md)) needs a good selection UX: *"show me the
session I want to continue, wherever it came from."* For cross-project resume in
particular, that means surfacing sessions that originated in **other** projects, from one
place, with full-text search to help find them.

Today the SPI answers only a narrower question — *"for this project dir, what sessions
exist?"* — by mapping a `projectPath` forward to a native store location. The resume
experience needs a broader, searchable view across everything we know about. `sessions.db`
is the proposed index that backs it.

This doc is about **where that index lives, and WHEN entries get into it** — not the
column-level schema (that is deliberately deferred; see [Non-Goals](#non-goals)).

## Decisions taken so far

Settled in conversation:

- **One global index, not per-project.** A single machine-level `sessions.db`
  (`~/.specstory/sessions.db`, no leading dot — matches its sibling `lore.db`) indexes
  sessions from every project, each row tagged with `project_id`. Cross-project resume's
  defining need is to see across projects from one place; a per-project index would force a
  full-disk discovery + N-database union at every pick, and a per-project index file inside
  `.specstory/` is either a privacy leak (if committed) or doesn't travel (if gitignored) —
  so it earns nothing.
- **Separate from `lore.db`.** Lore keeps its own corpus (`~/.specstory/lore.db`), with its
  own `PARSER_VERSION` cadence, fed by parsing rendered markdown. `sessions.db` is a new,
  independent database fed by the Go providers from raw native session data. Different
  inputs, different lifecycles — coupling them would couple two unrelated version cadences.
  They can still share discovery and the project-identity code.
- **Command surface: `specstory reindex` (top-level).** The full cold sweep is its own
  top-level command, *not* a subcommand of `resume`. The existing interactive picker stays
  `specstory resume`. `sessions.db` is an internal implementation-detail name — neither command
  is renamed to `restore`.
- **No new dependency.** SQLite is already vendored: `modernc.org/sqlite` (pure Go), used by
  the Cursor reader and `pkg/provenance/store.go`. `sessions.db` mirrors the provenance house
  style (`database/sql` + a `file:…?_pragma=busy_timeout=…&_pragma=journal_mode(WAL)` DSN +
  perf pragmas) — **not** lore's `node:sqlite` (lore is a separate Node skill; nothing shared
  at the code level).
- **First cut indexes full-body full-text, for all six providers.** `reindex` parses each
  session's full `SessionData` and indexes the entire conversation body into FTS5 (not a
  metadata-only sweep), and every provider implements the global enumerator from day one. See
  [Build Plan — First Cut](#build-plan--first-cut).

## Guiding principle: `sessions.db` is a derived cache

The authoritative source of truth is, and stays, the **native session stores**. `sessions.db`
is a **cache** over them. Two consequences drive the rest of this design:

1. **It must be fully rebuildable from scratch at any time.** Deleting it is always safe.
2. **Incremental population is an optimization, never the only path.** If the only way data
   got in were live SpecStory events, any session we failed to witness would be missing
   *forever*. Because catchup can always rebuild, gaps are transient, not permanent.

## Project identity: reuse the algorithm, NOT the stored files

`pkg/utils/project_identity.go` already defines the identity *algorithm* this index needs:

- `git_id` = `sha256(normalized origin remote URL)` → `xxxx-xxxx-xxxx-xxxx`. Derived from the
  remote, so it is **stable across directory, machine, and user** — the cross-project /
  cross-machine join key.
- `workspace_id` = `sha256(absolute path)` — machine-local fallback for repos with no remote.

What is **already solved** is identity across *clones / machines*: two checkouts of the same
remote both compute the same `git_id` (verified — two clones of one repo, differing
`workspace_id`s, identical `git_id`).

What is **NOT solved by the stored files** is the monorepo case, and this is the trap to avoid.
`EnsureProjectIdentity` / `generateGitID` look for `.git/config` at *exactly* the project root
they were given and **never walk up**. So when an agent runs from a subdirectory of a repo, the
written `.specstory/.project.json` there has **no `git_id`** and a fresh, path-based
`workspace_id` — three launch directories in one repo (`/`, `/a`, `/b`) produce three different
identities. **Reading the nearest stored `.project.json` would therefore *fragment* one
monorepo into many projects.**

**Resolution strategy for `sessions.db` (the new part — Build Plan step 1).** Identity is
computed fresh per session by `ComputeProjectID(cwd)`, which **walks up from the session's cwd
to the git root**, computes `git_id` from *that* root's remote, and **ignores any per-directory
`.project.json`**. From `/a` it walks up, finds the repo's `.git`, and yields the same `git_id`
as `/`. Root, `/a`, `/b` all collapse to one project — which is the monorepo behavior we want.
It also computes `workspace_id` from the **walked-up root**, not the launch subdir, so even a
remote-less monorepo's subdirectories share one identity on a given machine.

**Caveats to keep honest:**

- **Remote-less repos** still don't cross machines: walk-up finds `.git` but no origin, so
  there is no `git_id`, only the path-based `workspace_id`. Subdirectories collapse (shared
  root), but the project is machine-local. A real constraint for the later cross-machine /
  cross-user stages.
- **No `.git` anywhere up the tree** has no natural root and genuinely fragments; that is the
  irreducible case.

`project_id` (the resolved `git_id`, else `workspace_id`) is the same vocabulary the cloud sync
and Lore speak, so the index, the cloud, and Lore share one identity space.

**The writer is fixed too, not just sessions.db.** To avoid maintaining two divergent identity
strategies, the existing `EnsureProjectIdentity` writer is switched onto the same walk-up core
(Build Plan step 1), so the stored `.specstory/.project.json` files stop fragmenting. The cloud
groups sessions by `project_id`, so monorepo-subdir sessions consequently regroup under the
repo's project. That is **intended and desirable**; the rare case of a previously-synced subdir
session "moving" cloud project is an accepted edge case, not a concern.

## When do entries get into `sessions.db`?

Two modes: **steady state** (warm, incremental) and **catchup** (cold, full rebuild).

### Steady state — incremental upsert at every sighting

The commands that already parse sessions upsert into `sessions.db` as a side effect. They
already hold a parsed `SessionData`, so they can populate the metadata row **and** the
full-text content in one shot, for free:

- `specstory sync` — every session it processes.
- `specstory run` / `specstory watch` — every session update the watcher sees.
- `specstory resume` — a **double write**: re-index the source session, and index the
  freshly reconstructed target session so it is itself resumable next time.

### The witness gap — and why more hooks don't close it

Event-driven population only sees sessions specstory was attached to. An agent session run
without specstory will be absent. **This gap cannot be closed by adding more event points** —
there is always a session we didn't witness. The gap-closer is the catchup scan (below), run
opportunistically (e.g. when `reindex` finds the index stale or empty), not an ever-growing
list of hooks. The reframe: the gap is something the periodic scan *sweeps up*, not a hole we
must plug live.

### Catchup — cold rebuild / explicit reindex

Triggered explicitly by the user via `specstory reindex` (and, in a later chunk, automatically
when `sessions.db` is missing or detected stale). This is the path that enumerates *everything*
and is the reason for the new SPI capability below — and it is the **entirety of the first
cut** ([Build Plan](#build-plan--first-cut)): the only population path that exists initially.

## The missing SPI capability: project-*discovering* enumeration

Today's listing is project-**scoped**: `projectPath → store location → sessions`. Catchup
needs the inverse — enumerate **every** native session, then **discover** which project each
belongs to. The project becomes an *output*, not an input.

That changes the return shape. A global enumeration must surface each session's **originating
cwd**, because the cwd is the only thing that lets the CLI attribute the session to a
`project_id`. Identity resolution stays in the CLI (one source of truth) via the new
read-only `ComputeProjectID(cwd)` helper (see [Build Plan](#build-plan--first-cut)) — providers
surface the cwd, they do not compute `project_id`.

Signature (ratified for the first cut):

```go
// ListAllAgentChatSessions enumerates every session in this provider's native store,
// regardless of project. Unlike ListAgentChatSessions(projectPath), the project is not an
// input — each returned ref carries the originating cwd so the caller can resolve project
// identity. Lightweight: metadata + cwd only, no full SessionData parse (reindex re-fetches
// full data per ref via the existing GetAgentChatSession(originCwd, sessionID)).
ListAllAgentChatSessions() ([]GlobalSessionRef, error)

type GlobalSessionRef struct {
    SessionID  string // native session id (uuid)
    CreatedAt  string // ISO 8601
    Slug       string // human-readable, filename-safe
    Name       string // human-readable description (may be empty)
    NativePath string // absolute path to the native session file
    OriginCwd  string // working dir the session was launched from (→ project_id)
}
```

**cwd is always read from *inside* the session file — uniformly, for every provider.** Claude
Code writes `cwd` on every JSONL record; Codex writes it in the `session_meta` record. The
store's *directory* is at best a hint and at worst misleading:

- **Claude Code** — store dir is `~/.claude/projects/<cwd-encoded>/`, but the encoding
  (`encodeProjectDirName`) replaces *every* non-alphanumeric character with a dash and is
  therefore **lossy and not reversible** — do **not** try to decode cwd from the directory
  name; read the record `cwd`. Enumerate all project dirs, read each session's `cwd`.
- **Codex CLI** — sessions live under `~/.codex/sessions/YYYY/MM/DD/`, **not** project-keyed
  at all; cwd is inside the rollout `session_meta`. Proof that "the project is a property
  discovered from the session," not from its location. The existing project-scoped listing is
  already a global walk filtered by cwd (`findCodexSessions`) — the enumerator is that walk
  with the filter removed.
- **Gemini, Droid, DeepSeek, Cursor** — each has its own layout; same contract: return
  sessions + the cwd recorded inside each.

## Monorepos: `git_id` dissolves most of it

Because `git_id` hashes the *remote*, any cwd inside a repo boundary — child, peer, or parent
directory — resolves to the **same** `git_id` once we walk up to `.git` and read origin. So
sessions launched from different directories of one monorepo attribute to one project
automatically, *provided we resolve each session's cwd to its git root*.

Sharp consequence: the cwd-scoped store lookup is **too narrow even for the current project**
— a session launched from a sibling directory of the same repo lives under a *different*
encoded store dir and a project-scoped list misses it. So "all sessions for THIS project" in a
monorepo is best answered by **enumerate-all-then-filter-by-`git_id`** — the same global
primitive — not by the forward store lookup. The new capability is therefore not only a
catchup tool; it is also the correct primitive for the monorepo current-project view.

Still fragments: remote-less directories (only `workspace_id`, which is path-based). Flagged,
not solved.

## Two fill-levels (a future optimization, not the first cut)

The index has two natural fill-levels:

1. **Existence + metadata** — `project_id`, `uuid`, `agent`, `date`, `slug`, native path.
   Obtainable cheaply from the global enumeration (no `SessionData` parse). Enough to *browse
   and pick*.
2. **Content / full-text** — requires parsing `SessionData`.

**First-cut decision: `reindex` does both at once — it parses full `SessionData` and indexes
the whole conversation body into FTS5.** A *first* index of a session is therefore a full
parse, not a cheap metadata sweep; full-body search is the UX we want from day one.

**`reindex` is incremental, though.** It loads every indexed session's freshness fingerprint
(`size + mtime + index_version`) up front and **skips any session whose native file is
unchanged and was indexed by the current logic version** — exactly Lore's idempotency contract.
So the *first* run is a full parse, but re-runs only touch new/changed sessions (verified:
~20s → ~2s with most sessions unchanged). `--force` re-indexes everything; bumping
`index_version` (when parse/derivation logic changes) auto-invalidates every row. The cheap
metadata-only enumeration still runs every time (it's how sessions are discovered), but the
expensive full parse is gated by the fingerprint.

The two-level split stays relevant *later*, for keeping the index warm without an explicit
`reindex`: steady-state hooks populate content for free (they already hold the `SessionData`).
That warm-keeping is **out of scope** for the first cut — the only population path is `reindex`
(now incremental).

## Build Plan — First Cut

Scope: the cold-rebuild path only — `reindex` + the global enumerator + the schema. The
interactive picker's *rewiring* to read `sessions.db`, and all warm-keeping, are later chunks.

1. **Shared walk-up identity core** — extract one resolver in
   `pkg/utils/project_identity.go` that, given a directory, walks **up** to the git root
   (nearest ancestor with `.git`, handling `.git` as a **file** for worktrees/submodules, not
   just a directory); computes `git_id` from *that root's* remote (reusing `normalizeGitURL` /
   `createHash`), else `workspace_id` from the **same walked-up root** (so remote-less monorepo
   subdirs still collapse to one id). Two callers share it:
   - `ComputeProjectID(cwd) (id, name, err)` — **read-only**, used by `reindex`. Ignores any
     per-directory `.specstory/.project.json` (those fragment a monorepo) and never writes one.
   - `EnsureProjectIdentity` — the existing **writer**, switched onto the same core so the
     stored files stop fragmenting. Existing subdir files self-correct (they get the repo
     `git_id` + name on next run). This regroups monorepo-subdir sessions under the repo's
     cloud project — **intended** (the cloud groups sessions by `project_id`); the rare
     prior-synced subdir sessions "moving" cloud project is acceptable. See
     [Project identity](#project-identity-reuse-the-algorithm-not-the-stored-files).

2. **SPI extension** — add `GlobalSessionRef` + `ListAllAgentChatSessions()` to the
   `spi.Provider` interface (type in a small new `pkg/spi/global.go`). Lightweight; cwd read
   from inside each session file.

3. **Per-provider enumerators — all six.** Mostly "existing store walk, minus the project
   filter, plus surface the recorded cwd." Cursor (SQLite `store.db`) gets the most care.

4. **New store package `pkg/sessionindex`** — `sessions.db` in the provenance house style
   (`database/sql` + `modernc.org/sqlite`, WAL DSN + perf pragmas). Two tables (full column
   specs in [Schema](#schema)). FTS body = the reconstruction-flatten of `SessionData` (plain
   user/agent turns, synthetic noise already stripped — provider-neutral, reuses existing
   code). API: `Open`, `Upsert`, and `Search` / `ListByProject` stubs the picker will consume
   next.

5. **`specstory reindex` command** — a new top-level command (its own `pkg/cmd/reindex.go`,
   registered in `main.go` alongside `resume`). Enumerated refs are first **deduped by
   `(agent, session_id)`, keeping the freshest file by mtime** — a resumed session can span
   multiple native files sharing one id, and they all map to one row, so without dedup the
   duplicates ping-pong (each run, one file's stat never matches the fingerprint the other just
   stored, re-indexing both forever). Then per deduped session: stat the native file → skip if
   its `size + mtime + index_version` fingerprint is unchanged (the incremental path; `--force`
   overrides) → else `ComputeProjectID(OriginCwd)` → `GetAgentChatSession(OriginCwd, SessionID)`
   for full data → flatten body + count turns + derive timestamps → `Upsert` (metadata + FTS,
   `INSERT OR REPLACE`). Runs as a concurrent pipeline — see
   [Reindex Concurrency Model](#reindex-concurrency-model). Refs with no resolvable cwd (a few
   Claude warmups; all Cursor for now) are indexed metadata-only under the `unknown` project_id
   rather than dropped; refs with no native id are skipped (unresumable, and would collide on
   the primary key). Dedup also makes the "found" count equal the indexed count.

6. **Tests** — table-driven: identity walk-up (subdir / monorepo / no-remote), store
   upsert+FTS round-trip, per-provider enumeration against fixtures.

### Reindex Concurrency Model

The hot path is the per-session full parse (`GetAgentChatSession` → `SessionData` → flatten
body, count turns, derive timestamps). Each session is an independent, read-only parse of its
own native file — embarrassingly parallel. `reindex` runs a **3-stage streaming pipeline**:

```
enumerate (6 providers, parallel)  →  parse+build (bounded worker pool)  →  write (1 goroutine)
      cheap, independent                  the hot CPU+IO fan-out             SQLite single-writer
```

- **Enumerate** — the six `ListAllAgentChatSessions()` run concurrently; each walks its own
  store. Cheap, and lets parsing start streaming before enumeration finishes.
- **Parse + build** — a bounded worker pool (`min(NumCPU, ~8–12)`) over all refs. Workers are
  pure `ref → sessionindex.Session`. A per-session parse failure is logged and skipped, never aborts
  the sweep.
- **Write** — a *single* writer goroutine drains a channel and upserts in **batched
  transactions** (commit every ~200 rows). SQLite is single-writer (`MaxOpenConns=1`), so this
  serializes anyway; batching avoids thousands of tiny WAL commits — the main perf lever after
  parse concurrency.

**Streaming, not collect-then-write:** session bodies are large (1–5 MB each), so parsed
sessions stream parse → FTS-index → discard; we never retain a body after its upsert, keeping
memory bounded.

**`cwd → project_id` cache.** Many sessions share a cwd (same project), and `ComputeProjectID`
walks the filesystem to the git root each call. A mutex-guarded `map[cwd]projectID` (or a
pre-pass over the unique cwd set) eliminates the redundant walks.

**Provider concurrency-safety** is a precondition: `GetAgentChatSession` is called from N
goroutines, so the providers' parse paths must hold no shared mutable state. Audited before
implementation (providers are effectively stateless; per-call caches like Cursor's
`MessageTimestampCache` are local).

**No cross-provider barrier in this cut.** The deferred Cursor cwd recovery (above) *will*
introduce one (collect all other cwds before md5-matching Cursor); until then the pipeline is
fully streaming.

### Reindex Progress UX

`reindex` aims for a world-class, engaging readout. All progress goes to **stderr** (stdout
reserved for a future `--emit json`); the renderer is **TTY-aware**. Three moments:

1. **Discover** (instant — enumeration is fast and runs first):

   ```
   🔍  Scanning 6 agents…
   ✓   Found 822 sessions  ·  claude 683 · codex 70 · cursor 49 · droid 16 · deepseek 3 · gemini 1
   ```

2. **Index** (the long parse phase) — a **multi-line, in-place per-agent progress block**, one
   bar per agent ticking independently, plus an aggregate header:

   ```
   Indexing  512/822 · 23 projects · 130/s

     claude   ▕███████████░░░▏ 470/683
     codex    ▕██████████████▏  70/70 ✓
     cursor   ▕████░░░░░░░░░░▏  12/49
     droid    ▕██████████████▏  16/16 ✓
     deepseek ▕██████████████▏   3/3  ✓
     gemini   ▕██████████████▏   1/1  ✓
   ```

   Implementation: workers bump **atomic per-agent counters**; a single render goroutine ticks
   (~10 Hz) and redraws the block in place via ANSI cursor-up. The distinct-project counter is a
   mutex-guarded set. No worker draws directly (avoids interleaving). Fixed-width bars; no color
   dependency.

3. **Summary** — a clean, scannable final block, honest about gaps:

   ```
   ✓   Indexed 822 sessions into ~/.specstory/sessions.db  (6.1s)

         claude 683   codex 70   cursor 49   droid 16   deepseek 3   gemini 1

         31 projects  ·  49 unattributed
   ```

**Non-TTY fallback** (piped / CI): no ANSI, no spinner — periodic plain `indexed N/822…` lines
plus the same final summary, so logs stay clean.

## Schema

`~/.specstory/sessions.db`. Two tables.

### `sessions` — one row per known session

| Column          | Type    | Description                                                                                                                                            |
|-----------------|---------|--------------------------------------------------------------------------------------------------------------------------------------------------------|
| `project_id`    | TEXT    | Resolved identity (walk-up `git_id`, else `workspace_id`). The value the cloud groups by and Lore speaks. Indexed.                                     |
| `project_name`  | TEXT    | Human-readable project name (repo name from the walked-up root).                                                                                       |
| `agent`         | TEXT    | Provider id: `claude`, `codex`, `gemini`, `droid`, `deepseek`, `cursor`. Part of the primary key.                                                      |
| `session_id`    | TEXT    | Native session id (uuid). Part of the primary key.                                                                                                     |
| `created_at`    | TEXT    | ISO 8601 session creation timestamp (first turn). Powers the "Created" sort.                                                                           |
| `updated_at`    | TEXT    | ISO 8601 last-activity timestamp (last turn, else file mtime). Powers "X ago", the "Updated" sort, and resume-last.                                    |
| `user_turns`    | INTEGER | Count of user prompts — the headline "how much work is here" signal.                                                                                   |
| `total_turns`   | INTEGER | Count of all messages (user + agent). Computed alongside `user_turns`.                                                                                 |
| `slug`          | TEXT    | Filename-safe slug derived from the first user message.                                                                                                |
| `name`          | TEXT    | Human-readable session description (may be empty).                                                                                                     |
| `native_path`   | TEXT    | Absolute path the provider opens to read this session — a JSONL/JSON file for most agents, or a per-session `store.db` for Cursor. Unique per session. |
| `origin_cwd`    | TEXT    | Working directory the session was launched from (the input to identity resolution).                                                                    |
| `size`          | INTEGER | Native file size in bytes — part of the freshness fingerprint (incremental-skip on re-run).                                                            |
| `mtime`         | INTEGER | Native file modification time, epoch ms — part of the freshness fingerprint.                                                                           |
| `index_version` | INTEGER | reindex logic version that wrote the row — part of the fingerprint; bumping it forces a full re-parse.                                                 |
| `indexed_at`    | TEXT    | ISO 8601 time this row was last written by `reindex`.                                                                                                  |

Primary key: `(agent, session_id)` — a session is unique within a provider, and belongs to
exactly one project. `project_id` is indexed for per-project filtering. `created_at` /
`updated_at` / `user_turns` / `total_turns` come from the full `SessionData` parse `reindex`
already performs; the rest come from the lightweight enumeration ref.

**Considered and deliberately excluded (first cut):**

- **Git branch.** Claude Code records `gitBranch` per turn (stable within a session), but Codex
  and the others record nothing — a half-populated, Claude-only column isn't worth it yet. If
  added later it belongs on the enumeration ref as native metadata, *not* in neutral
  `SessionData`.
- **User naming / rename.** No `custom_name`; sessions are not renameable in the first cut.
  Keeps `sessions` a purely rebuildable cache (no user-authoritative fields to preserve across
  `reindex`).
- **Lineage.** No `source_session_id`. The breadcrumb already exists natively for free —
  reconstruction writes `specstorySourceSessionId` (the source session id) into the rebuilt
  file's meta (`claudecode`/`codexcli`/`droidcli` `reconstruct.go`) — so it can be surfaced
  later (ideally also capturing the source *agent*, which that field does not) when fork/resume
  lineage UX is actually designed.

### `sessions_fts` — FTS5 full-text index (kept in tandem with `sessions`)

| Column       | In FTS index?    | Description                                                                                     |
|--------------|------------------|-------------------------------------------------------------------------------------------------|
| `session_id` | No (`UNINDEXED`) | Join key back to `sessions`; stored in the row but not tokenized.                               |
| `agent`      | No (`UNINDEXED`) | Join key back to `sessions`; stored in the row but not tokenized.                               |
| `name`       | Yes              | Session description / first-message-derived name; tokenized + searchable.                       |
| `body`       | Yes              | Full conversation text — the reconstruction-flattened user/agent turns; tokenized + searchable. |

`UNINDEXED` is an FTS5 per-column keyword meaning the column is **stored but excluded from the
full-text index** — not "lacks a b-tree index" (FTS5 tables have no secondary b-tree indexes at
all). The two join keys ride along in the row so a `MATCH` hit maps straight back to its
`sessions` row without a separate lookup table. Default FTS5 tokenizer. Rows are
inserted/replaced alongside their `sessions` row during
`reindex`; deletion of a `sessions` row removes its `sessions_fts` row.

## Non-Goals (first cut)

- **The picker rewiring.** `specstory resume`'s interactive flow still uses the project-scoped
  `ListAgentChatSessions` until a later chunk points it at `sessions.db`.
- **Any warm-keeping.** No steady-state upserts from `sync`/`run`/`watch`/`resume` yet — the
  only population path is the explicit `reindex` (which is incremental, so re-runs are cheap).
- **Storing serialized `SessionData`.** The first cut stores metadata + full-text body, not a
  `SessionData` blob. A blob becomes relevant for cloud Stages 3–4 (where the cloud is the
  source). Deferred.

## Deferred — DO NEXT (Cursor cwd recovery)

Cursor CLI sessions are currently enumerated with `OriginCwd=""` and therefore indexed
under the **`unknown` project_id**. This is a first-cut shortcut, not the end state — it is
the immediate follow-up after the `reindex` first cut.

Cursor stores sessions at `~/.cursor/chats/<projectHash>/<sessionID>/store.db`, where
`projectHash = md5(canonical project path)` (`cursorcli/path_utils.go` `GetProjectHashDir`),
and records no workspace path inside the store. md5 is one-way, but the hash is embedded in
`GlobalSessionRef.NativePath` (two directories up from `store.db`). **Recovery plan:** the
`reindex` orchestrator builds `map[md5(canonicalize(cwd))]cwd` from every *other* provider's
cwds (optionally augmented by a filesystem scan for git/`.specstory` roots), then matches each
Cursor ref's `projectHash` to recover its cwd → resolve `project_id` via `ComputeProjectID`.
Unmatched Cursor sessions remain `unknown`. (Tracked in memory: `restore-cursor-cwd-deferred`.)

## Open questions (later chunks)

- **OPEN — Warm-keeping triggers.** Which of `sync`/`run`/`watch`/`resume` upsert, and the
  reindex-on-stale trigger. What counts as "stale" (reuse Lore's `size + mtime` fingerprint
  per native file?). **`specstory resume` is explicitly one of these occasions** — the picker
  should refresh staleness on launch (alongside `run`/`sync`/`watch`); for now it only
  *creates* the index when missing (see [RESUME-TUI.md](RESUME-TUI.md)), with full
  staleness-refresh deferred to this thread.
- **OPEN — Eviction / prune.** When a native session is deleted, when does its row leave
  `sessions.db`? (Lore prunes on rescan when the file is gone.)
- **OPEN — Remote-less projects.** How (if at all) cross-project / cross-machine works for
  repos with only a `workspace_id`.

## Related Documents

- [SESSION-PORTABILITY.md](SESSION-PORTABILITY.md) — the reconstruction / resume design this
  index serves.
- [PROVIDER-SPI.md](PROVIDER-SPI.md) — the provider interface the new enumeration method joins.
- `pkg/utils/project_identity.go` — `git_id` / `workspace_id` / `GetProjectID()`.
