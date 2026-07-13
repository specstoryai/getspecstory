# Cursor IDE Provider — Gap Analysis vs. SPI & Peer Providers

> **⚠️ Historical document — the analysis below is outdated.** This was a point-in-time
> gap analysis; the blockers it describes have since been resolved on this branch:
> `cursoride` now implements the full `spi.Provider` interface (including
> `ListAllAgentChatSessions`, `ReconstructSession`, and `NativeSessionPath`), the binary
> builds, `ExecAgentAndWatch` opens Cursor IDE instead of erroring, the watcher is
> fsnotify-based (with polling as fallback), and the package has unit tests
> (`agent_session_test.go`, `database_test.go`, `reconstruct_test.go`, `workspace_test.go`).
> It is kept for context on the decisions made; do not treat its findings as current.

## Bottom line

**The branch does not compile.** `cursoride` implements only **7 of the 10** methods on `spi.Provider`. When the session-portability SPI was merged from `dev` into `cursor-ide`, three new interface methods were added but `cursoride` was never updated, so the registry fails to build:

```
pkg/spi/factory/registry.go:83:29: *cursoride.Provider does not implement spi.Provider
(missing method ListAllAgentChatSessions)
```

The `cursoride` package compiles and its tests pass *in isolation* (Go only checks interface satisfaction at the assignment in `factory`), which is why this may have gone unnoticed. But the whole `specstory` binary currently cannot be built from this branch. That is the #1 release blocker.

## SPI conformance matrix

| Method | cursorcli | cursoride | Notes |
|---|---|---|---|
| `Name` | ✅ | ✅ | |
| `Check` | ✅ | ✅ | cursoride returns Version = `"Cursor IDE"` (a label, not a version) |
| `DetectAgent` | ✅ | ✅ | |
| `GetAgentChatSession` | ✅ | ✅ | |
| `GetAgentChatSessions` | ✅ | ✅ | |
| `ListAgentChatSessions` | ✅ | ✅ | |
| `ExecAgentAndWatch` | ✅ | ⚠️ returns error | correct — IDE can't be launched via CLI |
| `WatchAgent` | ✅ fsnotify | ⚠️ 2-min poll | functional but degraded (see below) |
| `ListAllAgentChatSessions` | ✅ | ❌ **missing** | **compile blocker** |
| `ReconstructSession` | ✅ | ❌ **missing** | **compile blocker** |
| `NativeSessionPath` | ✅ | ❌ **missing** | **compile blocker** |

## Gaps ranked by severity

### 1. Blocker — three unimplemented SPI methods (doesn't build)

- **`ReconstructSession` / `NativeSessionPath`**: For Cursor IDE these should be **permanent stubs returning `spi.ErrReconstructionUnsupported`**, and that is architecturally *correct*, not a shortcut. `resume.go:329` already degrades gracefully ("Cursor IDE can't yet be a cross-agent resume target"). Cursor can never be a resume *target* anyway: `resume.go:277` unconditionally calls `ExecAgentAndWatch` on the target, which cursoride refuses. Resuming *into* Cursor would require a different flow entirely (write to `store.db`, then have the user open Cursor). So stubbing these unblocks the build with the right semantics.
- **`ListAllAgentChatSessions`**: This one deserves a *real* implementation. It powers `reindex` → the all-projects restore browser (`sessions.db`). An empty stub compiles and is safe (`reindex.go` tolerates empty/erroring providers), but Cursor IDE sessions would silently never appear in the global browser — a real feature gap. The remaining work is the `md5(path)` cwd reverse-lookup so each `GlobalSessionRef` carries its originating project; sessions currently index as "unknown."

### 2. High — test coverage is a fraction of every peer

`cursoride` has **1 test file / 4 test functions** against 16 source files. Peers: claudecode 40, cursorcli 40, deepseek 34, codex 23, gemini 23, droid 17. Untested: `agent_session.go` (the core conversion + exchange-grouping logic, which has had multiple race-condition fixes), `database.go`, all `tool_*.go` handlers, `watcher.go`, `path_utils.go`, and `provider.go`. Every other provider has `provider_test.go` + `reconstruct_test.go` + parser tests. This is the largest quality-parity gap after the compile blocker.

### 3. Medium — `run cursoride` errors instead of guiding

`specstory run cursoride` calls `ExecAgentAndWatch` → immediate `"cursor IDE does not support execution via CLI"`. `cursoride` is still listed as a `run` provider in the dynamically-built examples. Peers all support `run`. Needs either a guard that routes IDE providers to `specstory watch` with a helpful message, or exclusion of `cursoride` from `run`'s provider list.

### 4. Medium — watch is poll-based, not event-based

Every CLI provider uses fsnotify for real-time updates; cursoride hardcodes a 2-minute `checkInterval` poll of the SQLite DB. Given the WAL-backed moving-target DB (the git log shows several "session updated but exchanges not yet on disk" race fixes), this is a real latency/reliability difference. Acceptable for release as a documented limitation, but worth an explicit decision.

### 5. Low — consistency & polish

- **Tool metadata shape**: cursoride embeds tool markdown into `Content[].Text` and sets only `Tool.Name`/`Tool.Type`, never `Tool.Summary`/`Tool.FormattedMarkdown`. Other providers populate the `Tool` fields. Flattening still works (it reads `Content` text), but it diverges from the SPI's intended shape and leaves the `toolTurnText` reconstruct path unexercised.
- **Provider ID**: `"cursoride"` is the only verbose ID; peers are short (`claude`, `cursor`, `codex`, `gemini`, `droid`, `deepseek`).
- **Docs**: `docs/CURSORIDE-PROVIDER.md` is a *phase-1 implementation plan* that explicitly defers tool formatting to "later phases"; `agent_session.go:15` still says "This is a minimal implementation - markdown output will be improved later." **README has zero mention of cursoride.** No user-facing docs exist.

## Recommended path to release

**Tier 1 — must-do to build & ship (small):**

1. Add `ReconstructSession` + `NativeSessionPath` returning `spi.ErrReconstructionUnsupported` (correct permanent behavior).
2. Add a `run`-command guard for IDE providers (message → use `watch`), or drop cursoride from `run`.
3. Update README + replace the phase-1 plan doc with real user docs stating watch/sync-only, poll interval, and no cross-agent-resume-target support.

**Tier 2 — feature parity (medium):**

4. Real `ListAllAgentChatSessions` with the deferred `md5(path)` cwd reverse-lookup so Cursor sessions appear in the all-projects restore browser.
5. Bring test coverage up to peer level — at minimum `provider_test.go` and `agent_session_test.go` covering exchange grouping, the timestamp/relative-time logic, and the tool handlers.

**Tier 3 — nice-to-have:** fsnotify-based watching; align tool metadata onto `Tool.FormattedMarkdown`.

Recommendation: ship on **Tier 1 + item 4** (Cursor showing up in the restore browser is the kind of thing users will immediately notice missing), with Tier 2 tests as a fast-follow.

## Open decisions

- **Reconstruct**: confirm stubs (recommended) vs. actually building resume-into-Cursor via `store.db` — a much larger effort given the reverse-engineered Merkle-DAG format.
- **`ListAllAgentChatSessions`**: real implementation now (Tier 1) vs. empty stub to unblock the build and defer the browser integration.
