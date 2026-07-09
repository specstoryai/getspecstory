# Cloud Resume & Search

Planning document for extending the CLI's `specstory resume` and `specstory search` to include sessions stored in SpecStory Cloud.

> Status: **DRAFT** — design converged (D1–D22), no open questions, implementation plan in §4 (three development chunks). Ready for review / build.

## Goal

Extend the CLI's local-only `resume` and `search` functionality to also surface sessions that live in SpecStory Cloud (pushed there by other machines via cloud sync). This spans:

- **CLI** (this repo, `getspecstory` / `specstory-cli`)
- **Cloud API** — the `specstory-sync` Cloudflare Worker (`sync-cloud/worker/`, Hono + Drizzle/Postgres + Supabase Storage). **Note:** this is *not* in `specstory-monorepo` (which holds the CLI, VSCode extension, and a web frontend that consumes the same API).

## Decisions

- **D1 — `SessionData` is the canonical cloud representation.** The CLI fetches `SessionData` from the cloud and reconstructs from it; the raw provider transcript (`rawData`) is never the cloud-resume path.
- **D2 — The CLI pushes `SessionData` on sync.** The server stores it verbatim and serves it back, making `SessionData` the genuine source of truth end-to-end. Chosen over deriving `SessionData` server-side from parsed `exchanges` (rejected: reconstruction fidelity would be hostage to a markdown-oriented parse).
- **D3 — `SessionData`'s *contents* are stored as a new object-storage blob only** (Supabase Storage, `{workspace}/{session}/session-data.json`), mirroring how `rawData` is stored today — **no column holds the blob contents** and nothing indexes into it. Its only reader is resume, which needs the whole blob. Small filterable fields (agent/provider) already ride in the `sessions.metadata` JSONB for list/dedup.
- **D4 — `rawData` stays as-is.** It has a (new) consumer of its own, so it is untouched: the push keeps sending it and the server keeps writing `raw-data.json`. `SessionData` is **added alongside** `rawData`, not a replacement — the push gains a new `sessionData` field and the server writes a new `session-data.json` blob in addition to the existing writes.
- **D5 — A `session_data_size INTEGER` (nullable) column on `sessions` tracks blob presence** (byte count, set in `Session.upsert` when the push carries `sessionData`), mirroring the existing `markdown_size` / `raw_data_size` columns. `NULL` = legacy/no blob; `> 0` = cloud-resumable. Both read paths filter on it so browse/search only surface resumable cloud sessions — but the filter is **opt-in, not unconditional**: the list endpoint (`GET …/{projectId}/sessions`) is shared with the web app, and a blanket `WHERE session_data_size > 0` would hide every legacy session from cloud.specstory.com until re-synced (regressing a surface this feature doesn't own, contrary to §3's additive promise). So the CLI passes a new **`?resumable=true`** query param, which adds `WHERE session_data_size > 0`; without the param the endpoint behaves exactly as today. The aligned FTS search (D18) applies the same predicate on the `sessions` row unconditionally — it's a new resume-only surface, so no opt-in is needed there. Nullable represents the back-catalog with no backfill. This is metadata *about* the blob — consistent with D3, not a contents column.
- **D6 — Back-catalog heals by re-sync, not backfill.** Legacy sessions (`session_data_size IS NULL`) are filtered out of resume browse/search (D5) until a new-enough CLI re-syncs them and populates `SessionData`. No server-side backfill job.
- **D7 — Self-heal triggers via an explicit `session_data_size` signal.** The server exposes `session_data_size` on both sync-skip paths. **HEAD:** a new `X-Session-Data-Size` header (`NULL` coerced to `0`, so "legacy" and "no blob" read the same). **Bulk sizes:** the `GET …/sessions/sizes` response is a flat `{"<sessionID>": markdownSize}` map whose value type must stay a bare number for deployed CLIs — so rather than changing the shape (or minting a v2 endpoint), the server adds a **suffixed key per session**: `"<sessionID>:sessionData": <blob byte size>`, emitted only when `session_data_size` is non-`NULL`. This is backward-compatible by construction: old CLIs unmarshal the map fine (values are still numbers) and only ever do point lookups by plain sessionID, so the extra entries are inert; and `:` can't appear in a UUID, so keys can't collide. The new CLI reads `bulkSizes[sessionID+":sessionData"]` — Go's zero-value map lookup makes a missing key read as `0`, which is exactly the re-send signal, with no NULL handling anywhere. The CLI's `requiresSync` (`sync.go:460-595`) re-sends when **markdown is newer OR `serverSessionDataSize == 0`** (currently it compares markdown size only). `X-Markdown-Size` keeps its true meaning. Applies to both the HEAD path and the batch bulk-sizes path (so `specstory sync` heals too).
- **D8 — Resume fetches the `SessionData` blob by streaming through the API**, not via a signed storage URL. A new read (route or content-negotiated variant of `GET …/{sessionId}`) reuses the existing `Bearer` auth + access check and streams the `session-data.json` blob back via a new `downloadFromStorage` helper (Supabase `.download()`). **No blob-retrieval path exists today** — blobs are currently write-only (`uploadToStorage` + `deleteFromStorage`; the `getPublicUrl` return value is discarded), and reads serve `markdown_content` from the DB column — so this is greenfield. ETag/`If-None-Match` (304) is **not** on this read: resume is a one-shot interactive fetch, so revalidation caching buys little; it and signed URLs (client→storage direct, single egress) are documented **future optimizations** if blob sizes or resume volume grow — not needed for v1's low-frequency, interactive resume.
- **D9 — Machine identity reuses `metadata.deviceId`; add `machineName`.** The machine ID is the existing `metadata.deviceId` (`SHA256(first non-loopback MAC)`, fallback hostname-hash; `getDeviceID`, `sync.go:625`) — already pushed and stored in the `metadata` JSONB. **No new column.** (Note: the GraphQL `searchSessions` *declares* `deviceId`/`deviceIds` filter args, but the resolver ignores them — it forwards only `projectIds` + `timeFilter`, `graphql.ts:249-250` — so there's **no working server-side machine filter today**; D15 filters client-side regardless.) The push adds **`machineName` = `os.Hostname()`** to metadata — three server edit sites plus the CLI push: `WriteSessionRequestSchema`, `SessionMetadataSchema` (`shared/types.ts:71`), and the inline metadata block in `handleWriteSession` (`sessions.ts:63-92`). Group/badge by stable `deviceId`, display latest `machineName`. Enables: **machine badge** on rows, **machine filter** (like agent/project), and **browse-by-machine**. (Cross-store overlap is handled by session-identity dedup in the blend model, D13 — *not* by machine exclusion.) _Caveat:_ MAC-based `deviceId` isn't perfectly stable (VPN/virtual adapters/interface ordering) → a machine may occasionally split into two identities; kept for v1 (already ships; changing it orphans existing IDs), persisted-UUID is future hardening.
- **D10 — Cross-machine (cloud) search/resume is gated on SpecStory Pro, as a soft gate.** Eligibility = `cloud.IsAuthenticated()` AND `cloud.GetEntitlement().Features.Resume` (the entitlement client is already on this branch: `GET /api/v1/billing/entitlement` → `Entitlement{Plan, Features{Resume,…}}`, `pkg/cloud/skills.go:29-42`; server-side the `resume` feature already exists with `MIN_PLAN.resume = "pro"`, `specstory-sync/shared/entitlements.ts:33`, deployed to prod). **Local resume/search always works for everyone**; cloud is layered in only when eligible. Ineligible users get a nudge in the resume/search UX — not logged in: "Log into SpecStory Cloud to search & resume sessions from your other machines"; logged in but not Pro: "Upgrade to Pro to search & resume sessions from your other machines." **Push stays unconditional** (D2) so upgrading retroactively unlocks existing cloud history. Entitlement is fetched **async/cached** to keep the TUI local-first. The server also enforces entitlement on cloud endpoints (client check is friendly UX, not the security boundary) via the existing `requireFeature("resume")` route guard (`specstory-sync/.../handlers/billing.ts:293` — re-reads the subscription per request, returns 403 `upgrade_required`; already guards the skills and analytics routes).
- **D11 — Local-first, best-effort, with a live cloud-progress affordance.** Local results render **immediately** (instant, offline-capable); the cloud request runs async and **supplements** them. While it's in flight, the TUI shows a visible "searching cloud…" indicator; when cloud results arrive they're **deduped (session identity, local preferred — D13) and merged in**, and the indicator clears. On timeout or failure, silently degrade to local-only and clear the indicator (optionally a subtle "cloud unavailable" hint). Applies to both browse and search. _Detail for browse & search:_ preserve cursor/selection when cloud rows merge into the recency-sorted list so the user's position doesn't jump. _Search cadence:_ the cloud debounce is **decoupled from local's** — local FTS stays per-keystroke; the cloud query fires only after **~600ms of keyboard silence** (fire-on-pause), reusing the existing seq-guarded debounce machinery (`searchDebounce`, `session_tui_search.go`) to drop stale responses. That cuts cloud traffic from ~a request per keystroke to 1–2 per typed query, which — together with the aligned search living on its own rate-limit bucket (D18) — keeps the worker's per-path limiter out of play.
- **D12 — Resuming a cloud session reuses the existing resume path; the source of `SessionData` is irrelevant.** Once `SessionData` is in hand, resume is identical whether it came from parsing a local native file or from the cloud blob: `ReconstructSession(target)` → `NativeSessionPath` → write native file → launch → index (`LiveIndexer`). Only two cloud specifics, neither new machinery: (1) `SessionData` is **fetched from the cloud (D8)** instead of parsed locally, and arrives already-normalized (no parse step); (2) a cloud-only session always takes the **reconstruct path even for same-agent** (the existing cross-agent path with `from == to`), since there's no local native file to "resume in place." _Schema-version policy:_ `SessionData` carries `schemaVersion` (`types.go:32`); on fetch, if the blob's version is **newer** than the CLI's own SessionData schema version, refuse with "Resuming this session requires an updated SpecStory CLI." (no upgrade instructions — install paths vary); **older or equal** → proceed as normal.
- **D13 — Blend model is a unified list; browse is blended exactly like search.** Resume's per-project picker calls local `ListByProject(projectID)`; in parallel it issues the cloud `GET /api/v1/projects/{projectId}/sessions` for the same `project_id`, under the D11 async pattern (local-first, best-effort, "searching cloud…" indicator, then dedupe/merge/sort by recency on arrival). Same blended treatment for browse and search. Dedup by session identity, local preferred. _Selection-time guard:_ dedup can only collapse rows both sources *returned* — a cloud search hit can be a session that also exists locally (local FTS just didn't match it, e.g. corpus noise, D18), so it renders cloud-badged with no local row to prefer. On select, check the local index for `(agent, session_id)` first; if present, resume the local session in place instead of taking the cloud fetch/reconstruct path.
- **D14 — Row distinction (v1) is a simple cloud icon.** Cloud sessions get a cloud icon next to them in both browse and search results; local rows have no icon. Machine attribution is surfaced via the machine filter (D15), not a per-row machine badge, for v1.
- **D15 — Machine filter on the `m` key, modeled on the agent filter (`a`).** New `machineFilter` (`""` = all), cycled by `m` exactly as `cycleAgent`/`nextInCycle`/`allAgentCycle` do for agents (`session_tui.go:576-611`). Cycle ring: **all → "local only" → each remote machine**, wrapping. Entries are keyed by `deviceId` (stable), displayed by `machineName` (human-readable). The **"local only"** entry covers *this machine's identity*: local-index rows **plus** cloud rows whose `metadata.deviceId` matches this machine's `getDeviceID()` (so this machine's pruned-but-synced sessions are included) — the local machine's own hostname never appears as a ring entry, only other machines'. Remote entries are the distinct cloud `deviceId`s ≠ ours; when two share a `machineName`, disambiguate with a numeric suffix — `myLaptop (1)`, `myLaptop (2)` — assigned in a deterministic order (e.g. sorted `deviceId`) so labels are stable across refreshes. Filtering is client-side over cached results (like `refilterCurrentAgent`, no re-query).
- **D16 — The all-projects browse is blended too.** `resume` starts in the current project and `tab`s to the all-projects browse (`modeProjects`); there can be projects in the cloud that aren't local. That view lists cloud projects via `GET /api/v1/projects` (`handleListProjects`, `specstory-sync/.../projects.ts:25`) merged with the local project list under the same D11 async pattern (local-first, pending indicator, dedupe/merge/sort). Dedup by `project_id`; **cloud-only projects get a cloud icon** in the projects view.
- **D17 — Merged search results sort by `updated_at` (recency), not FTS relevance.** Because ordering is recency across the combined set (same as local today), the two FTS engines do **not** need matching relevance ranking — only matching *query semantics* (D18) and *snippet format* (D19).
- **D18 — Align cloud search to local (faithful), keeping local's prefix/type-ahead.** Local `unicode61` (literal, prefix/type-ahead, implicit-AND) and cloud `websearch_to_tsquery('english', …)` (stemming, stopword removal, no prefix, murky operators) are far apart — blending them raw feels incoherent. **Decision:** align the cloud to local (not local to cloud — that would sacrifice local live type-ahead for everyone, incl. offline/non-Pro users). Cloud-resume search runs against a **`simple`-config FTS** on `exchanges` (no stemming/stopwords, mirrors `unicode61`) with a server query builder mirroring `ftsQuery` (implicit AND, phrase `<->`, last-token prefix `:*`) via `to_tsquery('simple', …)`. _Input safety, by construction:_ like local `ftsQuery`, the builder never passes raw input to the query parser — it tokenizes the user's query to letter/digit runs and assembles the tsquery string entirely from those tokens, so no user-controlled character can reach `to_tsquery` (which, unlike `websearch_to_tsquery`, throws on malformed input). An empty token list (all-punctuation query) skips the DB call and returns zero hits (`to_tsquery('simple', '')` is itself a syntax error), and the handler wraps the search in a guard that returns empty results instead of 500 — degrading to D11's silent local-only fallback. Kept separate from the web app's existing `english` search as a **new endpoint** — not a `config`/`mode` param on `searchSessions`: the worker's rate limiter is keyed by pathname (`router.ts:79`), so a dedicated route gives CLI search its own rate-limit bucket, isolating the web app from CLI traffic (and vice versa). Local search is unchanged. **Lockstep hazard:** local's tokenizer is the *implicit* `unicode61` default (undeclared, `store.go:187-192`) and `ftsQuery` hard-codes its rules to mirror it — this server `simple` builder becomes a **third** component that must track that undeclared default; comment the coupling at all three sites. **Known corpus/scope asymmetry (accepted for v1):** the two engines index different renderings of the same `SessionData` — local indexes the whole-session flattened body (`flattenBody` → `FlattenSessionData`), cloud indexes markdown-parsed `exchanges`. Since the markdown is rendered from the same exchanges (thinking and tool renderings included), the cloud corpus is effectively a **superset** of local's plus noise: synthetic turns (slash-command/local-command output — flatten drops them, archival markdown keeps them) and marker/wrapper tokens (`User`, `Agent`, `Thought Process`, tool tags) that can over-match queries on those words. The one real recall gap is **AND-scope**: cloud requires all terms within a *single exchange*, local matches across the whole session — a multi-term query whose terms never co-occur in one exchange hits locally but misses in cloud (invisible-by-omission on cloud-only sessions; exchanges are large units, so co-occurrence is the common case). Net failure mode is "occasional multi-term misses + mild noise over-match," not lost content. Cheap improvement if noise annoys: strip marker lines from `content` in the exchange parser.
- **D19 — Cloud search snippets via Postgres `ts_headline`, delimiter-matched to local.** `ts_headline(config, content, <tsquery>, 'StartSel=<0x02>, StopSel=<0x03>, …')` uses the same STX/ETX markers local's `snippet(sessions_fts, 3, char(2), char(3), '…', 12)` emits, so the TUI renders local and cloud hits identically with no client special-casing. Returned as a field per hit; applied only to returned rows (`ts_headline` re-parses per document). Granularity note: local snippets come from the whole-session flattened `body`, cloud's from the matched `exchange` — both are highlighted excerpts around the match.
- **D20 — The `simple` FTS needs no backfill; it populates lazily on the same re-sync that grants `SessionData`.** `fts_simple` is added as a **plain nullable `tsvector` column** on `exchanges` (instant `ALTER`, no table rewrite) — **not** a `GENERATED … STORED` column, which would rewrite every legacy row on `ALTER`. It's populated on the write path (a `BEFORE INSERT/UPDATE` trigger `to_tsvector('simple', content)`, or in `storeExchanges`), so it fills in whenever `content` is (re)written — which the exchange-parser does on every re-sync via `onConflictDoUpdate` on `(sessionId, orderNumber)` (`exchange_parser_worker.ts:156-169`). Since re-sync is exactly what grants `SessionData` (D6/D7), `fts_simple` lands for precisely the searchable set. Legacy un-re-synced rows stay `NULL` — excluded by the `session_data_size > 0` filter (D5) and inert against `fts_simple @@ query` anyway. The GIN index on `fts_simple` builds cheaply (all-`NULL` initially; GIN skips NULLs). Net: a big one-time table rewrite becomes a trivial per-session recompute already happening on re-sync.
- **D21 — Cloud results are capped at 500 (recency), matching local; no paging.** Both cloud browse (`GET …/sessions`) and cloud search use `limit = 500` — the same bound as local search (`LIMIT 500` by recency) — so each source contributes its 500 newest, they merge and dedup (local preferred), and there is **no** "load more" affordance. Simplest model; matches today's local behavior.
- **D22 — The push gzips its body; timeouts rise.** Adding `sessionData` to a payload already carrying markdown + rawData grows an uncompressed JSON body against two cliffs: the worker's 50MB Content-Length cap (`router.ts:95`) and the CLI's 30s PUT client timeout (`sync.go:820`). Three changes: (1) the CLI gzips the PUT body (`Content-Encoding: gzip`) — transcript JSON compresses ~5–10×, shrinking upload time and effectively raising the 50MB cap (which is checked against the compressed size); (2) the per-request PUT timeout rises 30s → **60s**; (3) the aggregate `CloudSyncTimeout` rises 120s → **180s** (`sync.go:28`). Server-side, the PUT handler decompresses when `Content-Encoding: gzip` is present (e.g. `DecompressionStream`) before JSON parse; requests without the header behave exactly as today, so deployed CLIs are unaffected. _Ordering:_ the server change must deploy before the gzipping CLI ships — an old server can't parse a gzipped body.

## Open questions

None — all resolved (D1–D22). See Decisions above; §2/§3 below.

## 1. Data / schema comparison: Cloud vs local `sessions.db`

A session exists in several representations. The linchpin for resume is **`SessionData`** — the CLI's normalized, provider-agnostic model (`pkg/spi/schema/types.go:31`). Everything else is either a source you parse *into* `SessionData`, or a rendering derived *from* it.

- **Native transcript** — the raw provider bytes (Claude/Codex/Gemini/Droid/DeepSeek JSONL, or a Cursor `store.db`). This is a *transport/source*, not what resume consumes directly. Parsing a native transcript with the owning provider yields `SessionData`.
- **`SessionData`** — the normalized model resume actually operates on. Resume calls `ReconstructSession(data *SessionData, opts) → native format for the target agent` (`pkg/spi/provider.go:109`); the agent then launches from that reconstructed native file. (Same-agent *local* resume can shortcut straight to the existing on-disk native file, but any resume where the native file isn't already local — i.e. **every cloud resume** — must go through `SessionData` reconstruction.)
- **Flattened body** — plain text derived from `SessionData` (`FlattenSessionData`, `pkg/spi/reconstruct.go:59`, via `flattenBody`, `reindex.go:618`); what local `search` indexes into FTS5.
- **Markdown** — a human-readable rendering derived from `SessionData`.

**Design decision:** `SessionData` is the **canonical cloud representation**. The cloud stores and serves `SessionData` (JSON); the CLI fetches `SessionData` and reconstructs from it. We do **not** put the raw provider transcript (`rawData`) in the cloud, and cloud-resume never fetches a raw blob. `SessionData` is already provider-agnostic and reconstruction-ready, so a cloud session can be resumed into *any* agent with no provider-specific raw parsing on the fetch side.

This means the two ends of a session diverge in where the resume-source comes from — but it's the *same* `SessionData` shape on both:

|           Artifact            |                                Local (this machine)                                |                       Cloud (`specstory-sync`)                       |
| ----------------------------- | ---------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Resume source (`SessionData`) | reloaded on demand by the owning provider (`GetAgentChatSession`, `resume.go:315`) | **served by the API as `SessionData` JSON** (canonical; no raw blob) |
| Native transcript             | native file on disk                                                                | not stored in cloud                                                  |
| Markdown                      | `.specstory/history/*.md`                                                          | `sessions.markdown_content` (+ `markdown.md` blob)                   |
| Search corpus                 | flattened `body` in `sessions_fts` (SQLite FTS5)                                   | `exchanges.fts` (web app, `english`); CLI search adds `exchanges.fts_simple` (D18/D20) |

> **Current vs. target.** Today's sync push sends `markdown` + `rawData` (native transcript), and the server stores a `raw-data.json` blob. **Target (decided, D2):** the CLI pushes `SessionData`; the server stores and serves it verbatim as the canonical resume representation. `rawData` is not the cloud-resume path.

### Local `sessions.db` (CLI)

- **Location:** `~/.specstory/sessions.db` (SQLite, WAL mode). Authoritative design doc: `specstory-cli/docs/SESSIONS-DB.md`.
- **Schema:** `specstory-cli/pkg/sessionindex/store.go:156-193`

```sql
CREATE TABLE IF NOT EXISTS sessions (
    project_id   TEXT NOT NULL,
    project_name TEXT,
    agent        TEXT NOT NULL,          -- claude, codex, gemini, droid, deepseek, cursor
    session_id   TEXT NOT NULL,          -- native session uuid
    created_at   TEXT,                   -- ISO 8601, first turn
    updated_at   TEXT,                   -- ISO 8601, last activity
    user_turns   INTEGER,
    total_turns  INTEGER,
    slug         TEXT,
    name         TEXT,
    native_path  TEXT,                   -- absolute path to provider's native session file
    origin_cwd   TEXT,                   -- cwd the session was launched from (-> project_id)
    size          INTEGER,               -- native file size  ┐ freshness
    mtime         INTEGER,               -- native file mtime  │ fingerprint
    index_version INTEGER,               --                    ┘
    indexed_at    TEXT,
    fts_rowid     INTEGER,               -- O(1) link to sessions_fts row
    PRIMARY KEY (agent, session_id)
);
CREATE INDEX idx_sessions_project_recent ON sessions(project_id, updated_at, created_at);
CREATE VIRTUAL TABLE sessions_fts USING fts5(
    session_id UNINDEXED, agent UNINDEXED, name, body
    -- tokenizer is the implicit unicode61 DEFAULT (undeclared; store.go:187-192);
    -- ftsQuery hard-codes unicode61's tokenization rules to mirror it
);
```

- **Identity:** `PRIMARY KEY (agent, session_id)`. Project scoping via `project_id` (resolved git_id else workspace_id).
- **Ingest:** `specstory reindex` (cold, fingerprint-incremental) and `LiveIndexer` (real-time on run/watch/resume). Reads providers' **native transcript files**, resolves `project_id` from the cwd embedded *inside* the session file, flattens user/agent turns into `body`. (`reindex.go`)
- **`resume` read path:** `ListByProject(projectID)` → newest-first list for the picker (`store.go:447`); on select, the provider reloads the source session's `SessionData` via `GetAgentChatSession(fromCwd, sessionID)` (`resume.go:315`) and reconstructs/launches. **Resume fundamentally depends on being able to produce `SessionData`** — locally the provider reloads it from the agent's own session store (*not* from the indexed `native_path`); for a cloud session it means fetching `SessionData` from the cloud (D8). Either way the source must be reachable.
- **`search` read path:** FTS5 `MATCH` over `name`+`body`, joined back to `sessions`, newest-first, LIMIT 500 (`store.go:545`); snippet highlighting lazily for visible rows (`store.go:581`).

### Cloud push payload (CLI → cloud)

- **Trigger:** `specstory run`/`watch` (debounced autosave, 10s) and `specstory sync` (immediate). `main.go`, `pkg/cloud/sync.go`.
- **Endpoint:** `PUT https://cloud.specstory.com/api/v1/projects/{projectID}/sessions/{sessionID}` (Bearer auth). Plus `HEAD …/{sessionID}` (→ `X-Markdown-Size`) and `GET …/sessions/sizes` for dedup-by-size.
- **Payload** (`sync.go:79`):

```go
type APIRequest struct {
    ProjectID   string             `json:"projectId"`    // git_id or workspace_id
    ProjectName string             `json:"projectName"`
    Name        string             `json:"name"`         // filename w/o ext, e.g. 2026-02-26_..._slug
    Markdown    string             `json:"markdown"`     // rendered markdown
    RawData     string             `json:"rawData"`      // native transcript (JSONL) as string (see note)
    Metadata    APIRequestMetadata `json:"metadata"`
}
type APIRequestMetadata struct {
    ClientName    string `json:"clientName"`    // "specstory-cli"
    ClientVersion string `json:"clientVersion"`
    AgentName     string `json:"agentName"`     // "Claude Code", "Codex", ...
    DeviceID      string `json:"deviceId"`      // sha256(MAC|hostname)
}
```

- **Note on `rawData`:** today's push includes `rawData` (the native transcript). This is **not** the cloud-resume path — the canonical cloud representation is `SessionData` (see design decision above). The relevant question is not "does the cloud keep `rawData`" but "does the cloud hold/serve faithful `SessionData`" — which the CLI already has in hand at push time (`ProcessSingleSession` works from a `*spi.AgentChatSession` carrying `SessionData`; markdown is already rendered from `SessionData.Exchanges`). So pushing `SessionData` reuses data already in hand — but it's not a one-field change: it must be threaded through three signatures (`SyncSessionToCloud → performSync → APIRequest`) from where it's in scope (`session.go:231`).
- **Auth:** `~/.specstory/cli/auth.json` (`pkg/utils/path_utils.go:206`) holds a long-lived `cloud_refresh` token + short-lived `cloud_access` token; `Authorization: Bearer <access>` with lazy refresh (`pkg/cloud/auth.go`).
- **Note on identity (resolved):** the push URL keys sessions by `(projectID, sessionID)` only — **`agent` is not in the key** (it rides along in `metadata.agentName`). Locally the PK is `(agent, session_id)`. The cloud **cannot** hold two sessions with the same `sessionID` in one project — `unique(client_id, workspace_id)`, and `client_id` is uuid-typed. This is safe in practice because native session IDs are provider-generated UUIDs and every reconstruction mints a **fresh** UUID (`ReconstructedSession.SessionID`, `pkg/spi/reconstruct.go:33`), so no two agents ever produce the same ID. That fresh-ID invariant is what protects cloud identity — a future change that reuses source IDs on reconstruct would silently overwrite the original agent's cloud row.

### Cloud server-side storage

> **Repo correction:** the cloud session API does **not** live in `specstory-monorepo`. It's a separate Cloudflare Worker repo: **`specstory-sync/sync-cloud/worker/`** (Hono + Drizzle ORM + Postgres via Supabase + Supabase object storage). `specstory-monorepo` holds the CLI, the VSCode extension (`apps/vscode-extension`), an auth app, and a web frontend (`apps/web`). The web frontend consumes the same API. **For CLI cloud-resume/search, the backend of record is `specstory-sync`.**

**`sessions` table** (`specstory-sync/shared/db/schema.ts:165-191`):

```typescript
export const sessions = pgTable("sessions", {
  id:              uuid("id").defaultRandom().primaryKey(),   // internal uuid
  clientId:        uuid("client_id").notNull(),               // == CLI sessionId (native uuid)
  markdownContent: text("markdown_content").notNull(),        // markdown also kept in a DB column
  markdownSize:    integer("markdown_size").notNull(),
  rawDataSize:     integer("raw_data_size").notNull(),        // rawData itself is NOT a column
  metadata:        jsonb("metadata").$type<SessionMetadata>(),// clientName, agentName, deviceId,
                                                              //   gitBranches, llmModels, tags, summary…
  shouldSkipProcessing: boolean(...).default(false),
  shareStatus:     text("share_status").default("private"),   // 'private' | 'unlisted'
  name:            text("name").notNull(),
  userTitle:       text("user_title"),
  workspaceId:     uuid("workspace_id").notNull(),            // FK -> workspaces
  startedAt:       timestamp(...), endedAt: timestamp(...),
  createdAt, updatedAt,
});
// unique (client_id, workspace_id) + unique (workspace_id, id) — defined in SQL migrations, NOT schema.ts;
// idx on workspace_id, client_id, share_status
```

- **`markdown` and `rawData` blobs live in Supabase object storage**, not the DB:
  - markdown → `{workspace.id}/{session.id}/markdown.md`
  - **`rawData` (native transcript JSONL) → `{workspace.id}/{session.id}/raw-data.json`** (*not* the resume path — D1; resume will use the new `session-data.json` blob)
  - DB keeps only the sizes (`markdown_size`, `raw_data_size`) + a markdown copy in `markdown_content`.
- **`workspaces` table** = the cloud's "project": external key `project_id` (the CLI's git_id/workspace_id string), `ownerId` = the authenticated user id (JWT `sub`), unique `(project_id, owner_id)`. Single-owner; **no org/team sharing yet**.
- **`exchanges` table**: after each push, an async worker parses the session into per-exchange rows with a Postgres generated FTS column `fts = to_tsvector('english', content)` + GIN index (`schema.ts:112-115`). This is the server-side search corpus.
- **PUT handler** (`sync-cloud/worker/handlers/sessions.ts:34-172`), validated by `WriteSessionRequestSchema` (`types.ts:346`): upserts the session row, uploads both blobs to storage, enqueues async jobs (exchange parsing, duration, summary). Keyed by `(clientId, workspaceId)`.
- **Existing endpoints already in production:**
  - `GET /api/v1/projects/{projectId}/sessions` — list, `limit` (default 200), returns SessionSummary + ETags (`projects.ts:93`)
  - `GET /api/v1/projects/{projectId}/sessions/{sessionId}` — read full session; `Accept: application/json | text/markdown`; `If-None-Match`→304; returns exchanges (`sessions.ts:273`)
  - `GET /api/v1/sessions/recent` — recent across all the user's projects, `limit` 5 (`sessions.ts:439`)
  - `HEAD …/{sessionId}` → `X-Markdown-Size`, `X-Raw-Data-Size`, `ETag`, `Last-Modified`
  - `GET …/sessions/sizes` → per-session sizes
  - **`POST /api/v1/graphql` → `searchSessions(query, filters, limit=200, page=1)`** — Postgres `websearch_to_tsquery` over `exchanges.fts`, ranked by most-recent matching exchange; the GraphQL schema *declares* filters (clientName, agentName, deviceId, gitBranches, llmModels, tags, date range) but the resolver applies only `projectIds` + `timeFilter` (`graphql.ts:249-250`; `shared/models/session.ts:191-336`)
- **Auth:** Bearer JWT — **app-issued, verified with a shared HMAC secret** (`jwt.verify(token, env.JWT_SECRET)`, `shared.ts:48`), not Clerk-JWKS; `userId` from `sub`; per-request `Session.validateAccess(projectId, sessionId, userId)` / `user_permissions`. No DB-level RLS — enforced in app layer.

### Gap analysis

**The good news — most primitives already exist server-side:**

1. **Cloud must store & serve `SessionData` (the canonical resume representation).** Cloud-resume = fetch `SessionData` blob → `ReconstructSession(SessionData) → native` → launch. **Decided (D2/D3/D4):** the CLI adds `SessionData` to the sync push (alongside the untouched `markdown` + `rawData`); the server writes a new `session-data.json` object-storage blob and serves the whole blob back. Work items: (i) add a `sessionData` field to the sync push payload + `WriteSessionRequestSchema`; (ii) write the new `session-data.json` blob (existing `raw-data.json`/`markdown.md` writes unchanged); (iii) add a read path that returns the blob (whole-blob fetch, streamed through the API — D8). `exchanges`/FTS stay derived server-side for search, but are no longer the resume source of truth.

2. **Server-side FTS already exists** (`searchSessions` GraphQL over `exchanges.fts`). ⇒ **Cloud-search is feasible** without new infra. But ranking/tokenization differs from local: cloud = Postgres `websearch_to_tsquery` over parsed exchanges, ranked by recency-of-match; local = SQLite FTS5 over a flattened body, LIMIT 500 by recency. **Gap (since addressed):** merge/dedup and alignment strategy — resolved by D13 (blend/dedup), D17 (recency ordering), D18 (aligned `simple` FTS).

3. **Identity reconciliation.** Local PK `(agent, session_id)`; cloud key `(workspace=project_id+owner, client_id=session_id)`, with `agent` only in `metadata.agentName`. A session synced from *this* machine appears in both stores. **Gap (since addressed):** dedup on `session_id` (+ `project_id`), recovering `agent` from `metadata.agentName`, so the same session isn't listed twice and a local hit is preferred (it can resume offline/instantly) — resolved by D13, including its selection-time local-index guard.

4. **Project scoping aligns.** Both key project by git_id/workspace_id, so the current checkout resolves to the same `project_id` on both sides — clean basis for "show this project's cloud sessions too," and the cloud already has the per-project list endpoint.

5. **Resume fidelity across agents.** Reconstruction runs off `SessionData`, which is provider-agnostic, so a cloud session reconstructs into any target agent the same way local cross-agent resume already does. The fidelity question therefore collapses to: **is the `SessionData` the cloud serves faithful to the original?** — guaranteed because the CLI pushes `SessionData` verbatim (D2). (Bonus: this sidesteps the Cursor `store.db` problem entirely — Cursor is parsed to `SessionData` at the source; the cloud never needs to understand `store.db`.)

6. **Auth/scope.** CLI already holds a Bearer token — an app-issued JWT in `~/.specstory/cli/auth.json` — and uses it for push; the same token authorizes list/get/search/read. No new auth *client* work (server-side, Pro gating is one `requireFeature("resume")` line per new endpoint, D10). Org/team sharing doesn't exist yet, so cloud-resume is initially "your own sessions from your other machines," not teammates'.

**Net:** the heavy lifting (server FTS + list/get endpoints + auth) is already in place. The new server work centers on **making faithful `SessionData` fetchable** (D2/D8). The bulk of the effort is **CLI-side**: a cloud client, merging cloud results into the existing TUI, dedup, and reconstructing a fetched `SessionData` into a launchable native session.

## 2. UX design

The guiding principle is **local-first, cloud-additive**: `resume` and `search` feel exactly as they do today — instant, offline-capable — and cloud sessions from your *other machines* layer in on top without changing that baseline. Cloud never blocks, never slows the first paint, and its absence (offline, logged out, not Pro) degrades silently to today's behavior.

### 2.1 Entry points & flow

Nothing changes about how you invoke the features: `specstory resume` opens the picker scoped to the current project; `Tab` switches to the all-projects browser; `specstory search` opens the all-projects FTS search. The difference is what populates those views.

**Browse (`resume`).** The current-project picker renders local sessions immediately (as today), and in parallel fires the cloud request for the same `project_id`. While that's outstanding, a subtle **"searching cloud…" indicator** shows in the footer; when results return they're deduped and merged into the recency-sorted list, and the indicator clears (D11, D13). The all-projects browser behaves the same, and additionally its **project list is blended** — projects that exist only in the cloud (from another machine, never worked on locally) appear with a cloud icon so you can drill into them even though nothing about them is on this disk (D16).

**Search.** Local FTS runs instantly on every keystroke (debounced, as today); the cloud search fires on its own slower debounce — after ~600ms of typing silence (D11) — best-effort, and its hits merge in when they arrive. Because both sides are sorted by recency (`updated_at`), there's no relevance-ranking mismatch to reconcile (D17), and because the cloud search is semantics-aligned to the local one (D18), the merged results feel like one search rather than two engines disagreeing.

### 2.2 What a row looks like

Every session appears **once**. A small **cloud icon** marks rows that came from the cloud; local rows have no icon (D14). When the same session exists both locally and in the cloud (you made it here and it synced), the **local copy wins** the dedup — it can resume instantly and offline (D13). A session made on this machine but no longer on this disk (pruned) still surfaces from the cloud, cloud-badged — this is a dedup, not a machine exclusion (D13). Search rows carry a highlighted snippet; local and cloud snippets are rendered identically because the cloud returns them in the same delimiter format (D19).

### 2.3 Filters & sorting

Sorting stays recency-first (`updated_at`). The existing **agent filter (`a`)** is unchanged. A new **machine filter (`m`)** sits alongside it, modeled on the same cycling behavior: `all → "local only" → <each remote machine> → …`, wrapping (D15). This is how machine provenance surfaces day-to-day — rather than crowding every row with a machine badge, you cycle `m` to focus on a particular machine (or just this one). "Local only" means *this machine's* sessions — including its synced-then-pruned ones from the cloud — and this machine's hostname is never listed as a separate entry; remote machines show their hostnames captured at sync time (D9), with duplicate names disambiguated as `myLaptop (1)`, `myLaptop (2)` (D15).

### 2.4 Pro gating & the nudge

Cross-machine resume/search is a **SpecStory Pro** capability, enforced as a *soft* gate (D10): local resume/search always work for everyone. Cloud only layers in when you're **logged into Cloud and on a Pro plan**. When you're not, the feature still visibly exists as an invitation — a one-line nudge in the picker/search footer:

- Not logged in → *"Log into SpecStory Cloud to search & resume sessions from your other machines."*
- Logged in, not Pro → *"Upgrade to Pro to search & resume sessions from your other machines."*

Crucially, **everyone still pushes `SessionData`** regardless of plan (D2), so the day someone upgrades, their existing history across machines is already there to search and resume — nothing to re-capture.

### 2.5 Resuming a cloud session

Selecting a cloud session looks the same as resuming a local one. Under the hood the CLI fetches that session's `SessionData` from the cloud and hands it to the existing resume machinery; from there it's byte-for-byte the same path as a local cross-agent resume (D12). The user sees a brief fetch, then the agent launches. There's no separate "download then import" step in the UX — it's just "resume."

### 2.6 Offline & failure

If the network is down, auth is missing, or the cloud request times out, the views simply show local results and the indicator clears — no errors, no blocking (D11). The feature is never in the critical path of a local resume.

## 3. Technical design

The work splits cleanly between the **CLI** (this repo) and the **`specstory-sync` Cloudflare Worker** (the cloud API). Every server change is **additive** — a new field, a new blob, a new column, a new read/search path — so nothing regresses the existing sync/markdown/web-app surface. There is **no data migration/backfill** (D6, D20); the corpus heals forward as sessions re-sync.

### 3.1 Push: add `SessionData` + `machineName`

The CLI already holds `SessionData` at sync time (in scope at `session.go:231`; markdown is rendered from it), but it must be **threaded through three CLI signatures** — `SyncSessionToCloud → performSync → APIRequest` (`pkg/cloud/sync.go`) — not just added as a field. Extend the push payload (`APIRequest`) and, on the server, **both** the request validator (`WriteSessionRequestSchema`) and the metadata schema (`SessionMetadataSchema`, `shared/types.ts:71`) with:

- `sessionData` — the normalized `schema.SessionData` JSON (D2).
- `metadata.machineName` — `os.Hostname()` (D9). `metadata.deviceId` already ships.

`rawData` and everything else are untouched (D4).

The push body is **gzipped** (`Content-Encoding: gzip`) and the timeouts rise — PUT client 30s → 60s, `CloudSyncTimeout` 120s → 180s (D22).

### 3.2 Server storage & schema (`specstory-sync`)

- **Blob:** on PUT, write `SessionData` to Supabase Storage at `{workspace.id}/{session.id}/session-data.json` (new `uploadToStorage` call, alongside the existing `markdown.md` / `raw-data.json` writes) (D3).
- **`sessions.session_data_size INTEGER` (nullable):** set in `Session.upsert` to the `sessionData` byte length; mirrors `markdown_size` / `raw_data_size`. `NULL` = legacy/no blob, `> 0` = cloud-resumable (D5).
- **`sessions.metadata.machineName`:** add to `SessionMetadataSchema` (`shared/types.ts:71`) and persist in the **inline** metadata block in `handleWriteSession` (`sessions.ts:63-92` — there is no `cleanMetadata` helper), like `deviceId`/`agentName` (D9).
- **`exchanges.fts_simple tsvector` (plain, nullable):** for the aligned search. **Not** generated-stored (avoids an `ALTER` table rewrite). Populated on write by a `BEFORE INSERT/UPDATE` trigger `to_tsvector('simple', content)` (or in `storeExchanges`); GIN-indexed (cheap — NULL initially) (D20).

### 3.3 Server endpoints

- **PUT `…/sessions/{sessionId}`** — accept + persist `sessionData` (blob + `session_data_size`) and `machineName`; decompress the body when `Content-Encoding: gzip` (no header = today's path, old CLIs unaffected — D22); existing behavior otherwise (D2/D3/D9).
- **HEAD `…/sessions/{sessionId}`** — add `X-Session-Data-Size` (`NULL` → `0`). **`GET …/sessions/sizes`** — keep the flat numeric map; add a suffixed `"<sessionID>:sessionData"` key for each session that has a blob (D7).
- **New read (SessionData):** a route/negotiated variant of `GET …/{sessionId}` that streams the `session-data.json` blob via a new `downloadFromStorage` helper (Supabase `.download()`), reusing existing `Bearer` auth + `validateAccess` + ETag. Streamed through the API, not a signed URL (D8).
- **List (`GET …/{projectId}/sessions`)** — new opt-in **`?resumable=true`** param → filter `WHERE session_data_size > 0` (omitted = today's behavior, so the web app is untouched); return `metadata` (for the machine badge/filter); the CLI passes `limit=500` as a request param — the endpoint's default (200) is unchanged for the web app (D5/D9/D21).
- **List projects (`GET /api/v1/projects`)** — reused as-is for the blended project list (D16).
- **Aligned search** — a **new endpoint** (own pathname → own rate-limit bucket, D18): build `to_tsquery('simple', …)` mirroring `ftsQuery` (implicit AND, phrase `<->`, last-token `:*`) against `exchanges.fts_simple` — constructive tokenization only (no raw input reaches the tsquery parser), empty token list short-circuits to zero hits, errors return empty rather than 500 (D18); filter `session_data_size > 0`; return **`ts_headline`** snippets delimited with STX/ETX to match local `snippet()`; sort by recency; `limit = 500` (D17/D18/D19/D21). Kept separate from the web app's `english` `searchSessions`.
- **Entitlement (`GET /api/v1/billing/entitlement`)** — existing (`billing_router.ts:27`, deployed to prod); gates on `Features.Resume` (D10). Server enforcement on the cloud read/search paths = wrap the new SessionData read, `?resumable=true` list, and aligned search in the existing `requireFeature("resume")` guard (`handlers/billing.ts:293`) — one line per endpoint, defense in depth.

### 3.4 CLI: cloud client

A cloud client (extending `pkg/cloud`) covering: list sessions by project, list projects, aligned search, and fetch `SessionData` blob. `GetEntitlement` already exists on this branch (`pkg/cloud/skills.go`, D10). Auth reuses the existing `Bearer` token flow.

### 3.5 CLI: the blended TUI

- **Async layering:** on entering browse/search, render local immediately, then launch the cloud request in a goroutine (best-effort, timeout). On completion, dedup against local by `(agent, session_id)` — local preferred — merge into the recency-sorted model, **preserve cursor/selection**, and clear the "searching cloud…" indicator. On error/timeout, clear silently (D11/D13).
- **Cloud icon** on cloud rows; cloud icon on cloud-only projects (D14/D16).
- **Machine filter `m`:** a new `machineFilter` ("" = all) cycled by `m` exactly as `cycleAgent`/`nextInCycle` do (`session_tui.go:576-611`); ring is all → "local only" (this machine's `deviceId`: local rows + own pruned cloud rows) → distinct remote `deviceId`s labeled by `machineName`, duplicate names suffixed `(1)`/`(2)` deterministically; client-side re-filter of cached rows (D15).
- **Gating & nudges:** eligibility = `IsAuthenticated()` && `Features.Resume`; ineligible → skip the cloud request, show the appropriate footer nudge. Entitlement fetched async/cached to keep the TUI local-first (D10).

### 3.6 CLI: resume & self-heal

- **Resume:** for a cloud-only session, fetch its `SessionData`, then feed the existing reconstruct path (`ReconstructSession(target)` → `NativeSessionPath` → write → launch → `LiveIndexer`). No new resume machinery; cloud-only always takes the reconstruct path even same-agent (D12). Before fetching, apply D13's selection-time guard: look up `(agent, session_id)` in the local index — a cloud-badged search row can be locally present — and prefer the local in-place resume when found. After fetching, check `schemaVersion`: newer than this CLI understands → refuse with "Resuming this session requires an updated SpecStory CLI."; older or equal → proceed (D12).
- **Self-heal:** extend `requiresSync` (`sync.go:460-595`) to re-send when **markdown newer OR `serverSessionDataSize == 0`**, reading the new `X-Session-Data-Size` header (HEAD path) and the suffixed `"<sessionID>:sessionData"` bulk-sizes key (batch path; a missing key reads as `0`). This is what backfills `SessionData` (and thus `fts_simple`) for the back-catalog as sessions are touched (D6/D7/D20).

### 3.7 Dependencies & rollout

- **Dependency (resolved):** the entitlement client (`GetEntitlement`/`Entitlement`, `pkg/cloud/skills.go`) is on this branch (`skills-interface` merged); the server entitlement surface (`/api/v1/billing/entitlement`, `requireFeature`) is implemented in `specstory-sync` and deployed to prod.
- **Rollout is additive and self-healing:** every server change is additive; existing sessions become cloud-resumable/searchable as they re-sync (D6). No backfill, no coordinated migration. Non-Pro users transparently seed their cloud history for a future upgrade (D2). One ordering constraint: the server's gzip-accepting PUT deploys **before** the gzipping CLI ships (D22).

## 4. Implementation plan

Three **development chunks**, done in order — these are *work* phases, **not** releases. Each spans CLI + `specstory-sync` and ends with a **manual test/validation gate** before starting the next. Ordering follows the dependency graph: everything needs `SessionData` in the cloud (Chunk 1); resume establishes the reusable TUI plumbing (Chunk 2); search layers onto it (Chunk 3). Chunk 1 is done first so the corpus accumulates (and the back-catalog heals) while 2–3 are built.

### Chunk 1 — Seed & self-heal (plumbing, no UX)

Get `SessionData` flowing into the cloud and start healing the back-catalog. No user-facing change yet; everything downstream depends on this data existing.

- **CLI:** thread `sessionData` through `SyncSessionToCloud → performSync → APIRequest`; add `metadata.machineName = os.Hostname()` (D2, D9). `rawData` untouched (D4). Extend `requiresSync` to re-send on `serverSessionDataSize == 0`, reading `X-Session-Data-Size` (HEAD) and the suffixed `"<sessionID>:sessionData"` bulk-sizes key (D7). Gzip the PUT body; raise the PUT timeout to 60s and `CloudSyncTimeout` to 180s (D22).
- **Server:** accept `sessionData` (`WriteSessionRequestSchema`) + `machineName` (`SessionMetadataSchema`, `shared/types.ts:71`; metadata block in `handleWriteSession`); write the `session-data.json` blob (D3); add + set the `sessions.session_data_size` column (D5); expose `X-Session-Data-Size` on HEAD and the suffixed `"<sessionID>:sessionData"` keys in `GET …/sessions/sizes` (D7); accept `Content-Encoding: gzip` on the PUT — **deploy this before the CLI ships** (D22).
- **Validation:** push from two machines; confirm `session-data.json` blobs + `session_data_size`/`machineName` land; force a legacy session (no blob) and confirm re-sync heals it (size populates, no re-send once present); confirm a gzipped PUT round-trips and a no-header PUT (old-CLI shape) still works (D22). Purely additive — confirm existing web-app/markdown surfaces are unaffected.

### Chunk 2 — Cloud resume (browse)

First cross-machine capability: resume a session from another machine via the blended browser. Builds the shared TUI plumbing (async layering, dedup, machine filter, gating) that Chunk 3 reuses.

- **Server:** stream-`SessionData` read endpoint (`downloadFromStorage`, D8); `?resumable=true` on list + return `metadata`, `limit 500` (D5/D9/D21); reuse `GET /api/v1/projects` for blended projects (D16).
- **CLI:** cloud client (list sessions, list projects, fetch `SessionData`; `GetEntitlement` already on-branch, D10); blended browse TUI — async local-first + "searching cloud…" indicator (D11), unified list + dedup local-preferred (D13), cloud icon (D14), machine filter `m` (D15), blended project list + cloud-only icon (D16), Pro soft-gate + nudges (D10); cloud-only resume via the existing reconstruct path (D12).
- **Validation:** from machine A, browse/resume a session created on machine B (same- and cross-agent); verify dedup (local-preferred), the machine filter cycle, cloud-only projects, the async indicator, offline degrade-to-local, and the not-logged-in / not-Pro nudges.

### Chunk 3 — Cloud search

Cross-machine full-text search, semantics-aligned to local, reusing Chunk 2's TUI plumbing.

- **Server:** `exchanges.fts_simple` plain column + `BEFORE INSERT/UPDATE` trigger + GIN index (no backfill, D20); aligned search as a dedicated endpoint (own rate-limit bucket) — `to_tsquery('simple', …)` mirroring `ftsQuery` (D18), `ts_headline` snippets delimiter-matched to local (D19), recency sort + `limit 500` (D17/D21), `session_data_size > 0` filter.
- **CLI:** call cloud search on its own fire-on-pause debounce (~600ms of typing silence, D11); merge into the search TUI reusing Chunk 2's dedup / machine filter / gating / async layering; render local + cloud snippets uniformly.
- **Validation:** compare local vs cloud results for the same queries (stemming/stopword/prefix parity per D18); confirm snippets render identically, recency ordering across sources, the 500 cap, and that `fts_simple` populated via re-sync (no backfill run).
