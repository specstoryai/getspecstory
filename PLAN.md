# PLAN — Pi provider `run` / `watch` / `reconstruct` (SPE-77)

Stage 1 (PLANNER) of the strict PLANNER → GENERATOR → EVALUATOR pipeline from parent **SPE-76** (orchestrator branch `feat/pi-provider`). **No code is written in this stage** — this is a file-level implementation spec for Stage 2 (GENERATOR).

---

## 0. Critical context: which branch / which layout this plan targets

This planning branch (`cyrus1/spe-77-…`) is based on **`dev`**, and on `dev` the pi provider **does not exist yet** and the docs have **no pi row/note**. Everything this plan touches lives on the parent orchestrator branch **`feat/pi-provider`**, which is where Stage 2 will implement. All file contents quoted below were read from `feat/pi-provider`.

- Go module root: **`specstory-cli/`** (monorepo). Module import prefix: `github.com/specstoryai/getspecstory/specstory-cli`.
- Pi provider package: **`specstory-cli/pkg/providers/piagent/`** (import `…/specstory-cli/pkg/providers/piagent`), registered in `specstory-cli/pkg/spi/factory/registry.go` as `r.providers["pi"] = piagent.NewProvider()`.
- **All paths in this plan are written relative to the module root `specstory-cli/`** (i.e. `pkg/providers/piagent/…`), matching how the issue phrases them. Stage 2 must prefix `specstory-cli/` in the actual worktree.

The four stub methods + `notYetSupport` const to replace are at `pkg/providers/piagent/provider.go:22-28` and `:175-200` (verbatim below in §5–§6).

### One MUST-VERIFY unknown before writing run/watch code
The codebase contains **no information on how the `pi` binary resumes an existing session by id** (pi's `run`/`watch` were out of v1 scope, so nothing invokes pi). The two sibling patterns are:
- **claudecode**: resume flag → `claude --resume <id>` (`parseClaudeCommand` appends `--resume <id>`).
- **codexcli**: resume subcommand → `codex resume <id>` (built via `spi.EnsureResumeArgs(args, "resume", id)`).

`path_utils.go`'s own comments note pi has a `--session-dir` flag and a `settings.json sessionDir`, but the **resume-by-id** mechanism is unconfirmed. **Stage 2 must first run `pi --help` (and `pi <subcommand> --help`) to confirm how pi resumes a session**, then choose the matching helper (`spi.EnsureResumeArgs` for a subcommand, or a flag append for a flag). This plan is written so that only ~3 lines of `parsePiRunCommand` depend on the answer; everything else is pattern-identical to the siblings. If pi turns out to have **no** resume-by-id capability at all, `ReconstructSession`+`NativeSessionPath` are still fully shippable (they only need the write-side), and `run`/`watch` still ship for *new* sessions — record that limitation in the docs note instead of claiming cross-machine resume works end-to-end for pi.

---

## Capability 1 — `ExecAgentAndWatch` (`specstory run pi`)

### 1.1 Methods & files

| File | New/changed | Contents |
|---|---|---|
| `pkg/providers/piagent/piagent_exec.go` | **new** | `parsePiRunCommand(customCommand, resumeSessionID) (string, []string)`, `getDefaultPiCommand() string`, `ExecutePi(customCommand, resumeSessionID string) error`. Mirrors `claudecode/claude_code_exec.go` + `codexcli/codex_cli_exec.go`. |
| `pkg/providers/piagent/watcher.go` | **new** | Package-global watcher machinery (see Capability 2) — shared by run and watch. |
| `pkg/providers/piagent/provider.go` | **changed** | Replace the `ExecAgentAndWatch` stub (`:176`) with a real body that registers the callback, starts the watcher, runs the binary (blocking), stops the watcher. |

> Naming note: claudecode names its exec file `claude_code_exec.go`; codexcli names its `codex_cli_exec.go`. The analogous pi name is **`piagent_exec.go`** (the issue's suggested name). Confirmed convention: exec helpers live in a `*_exec.go`; the `ExecAgentAndWatch` *method* stays in `provider.go` (that is exactly how claudecode splits it — `ExecuteClaude` in `claude_code_exec.go`, `ExecAgentAndWatch` method in `provider.go`).

### 1.2 Approach (mirror claudecode `provider.go:358` `ExecAgentAndWatch`)

New `ExecAgentAndWatch` body:
1. **Validate/normalize `resumeSessionID`** if non-empty. pi session ids come from the header `id` field; unlike Claude's strict UUID check, pi ids may be arbitrary strings, so only `strings.TrimSpace` + reject empty-after-trim (do **not** hard-code a UUID shape unless `pi --help` proves ids are UUIDs).
2. `SetWatcherCallback(sessionCallback); defer ClearWatcherCallback()` and `SetWatcherDebugRaw(debugRaw)` (package-global, mutex-guarded — see §2).
3. Start the watcher **before** launching pi so nothing is missed: `if err := WatchForProjectDir(projectPath); err != nil { slog.Error(…) }` (non-fatal, same as claudecode).
4. `err := ExecutePi(customCommand, resumeSessionID)` — **blocks** until pi exits.
5. `StopWatcher()`; single guarded return: wrap `err` as `fmt.Errorf("pi execution failed: %w", err)` else `nil`.

`ExecutePi` (in `piagent_exec.go`, mirrors `ExecuteClaude`):
- `piCmd, args := parsePiRunCommand(customCommand, resumeSessionID)`.
- `cmd := exec.Command(piCmd, args...)`; `cmd.Stdin/Stdout/Stderr = os.Stdin/os.Stdout/os.Stderr` (interactive TTY passthrough — pi shares the terminal so Ctrl-C reaches it; no explicit `os/signal` handling, same as siblings).
- `cmd.Start()` then `cmd.Wait()`; on `*exec.ExitError` propagate the child's exit code via `os.Exit(exitErr.ExitCode())` (matches claudecode so pi's exit status flows through).
- `parsePiRunCommand`: reuse the existing `parsePiCommand` (`provider.go:47`) for the **binary + base args** split (already handles `spi.SplitCommandLine` quoting + `expandTilde`), then apply the resume step: **the one binary-specific decision from §0** — either `spi.EnsureResumeArgs(args, "<pi-resume-subcommand>", resumeSessionID)` or append a resume flag. Keep `getDefaultPiCommand()` returning `defaultCmd` (`"pi"`) unless `pi --help` reveals a non-PATH default install location worth probing (claudecode probes `~/.local/bin`, codex probes Homebrew/npm — pi may need none; a plain PATH lookup is the safe default and keeps the code DRY).

### 1.3 Edge cases
- Empty `customCommand` → `parsePiCommand` already falls back to `defaultCmd`.
- `resumeSessionID == ""` → new session (no resume args appended). This is the always-safe path even if resume-by-id is unsupported.
- pi binary missing → `cmd.Start()` error surfaces through the wrapped return; `Check` already classifies install errors, so `run` does not need to re-implement detection.

---

## Capability 2 — `WatchAgent` (`specstory watch pi`) + the shared watcher

### 2.1 Methods & files

| File | New/changed | Contents |
|---|---|---|
| `pkg/providers/piagent/watcher.go` | **new** | Package-global watcher state + fsnotify loop + scan/dispatch. Public: `WatchForProjectDir(projectPath) error`, `StopWatcher()`, `SetWatcherCallback`/`ClearWatcherCallback`/`SetWatcherDebugRaw`. |
| `pkg/providers/piagent/provider.go` | **changed** | Replace the `WatchAgent` stub (`:181`) with a real body: register callback, start watcher, block on `ctx.Done()`, stop. |

### 2.2 Approach (mirror claudecode `watcher.go` + `provider.go:412` `WatchAgent`)

Package-global state (copy claudecode `watcher.go:19-26` shape exactly):
```
var ( watcherCtx context.Context; watcherCancel context.CancelFunc;
      watcherWg sync.WaitGroup; watcherCallback func(*spi.AgentChatSession);
      watcherDebugRaw bool; watcherMutex sync.RWMutex )
func init() { watcherCtx, watcherCancel = context.WithCancel(context.Background()) }
```
- **`wg.Go` is mandatory** (CLAUDE.md): start the watch goroutine with `watcherWg.Go(func(){ … })` — **never** `wg.Add(1)`+`go func(){defer wg.Done()}`. Both siblings already use `watcherWg.Go` (confirmed in claudecode & codexcli `watcher.go`).
- **What to watch.** pi is **not** a project-hashed store like claudecode; it is closest to a *directory-of-files-per-encoded-cwd*. Resolve the watch target with the **existing** `ProjectSessionDir(projectPath)` (`path_utils.go:101`) for the default layout, and `piSessionsRoot()` for the flat `PI_CODING_AGENT_SESSION_DIR` layout. Watch that **directory** (not individual files) — mirror claudecode's per-directory reconcile, but simpler: pi keeps `*.jsonl` files flat in one directory (no YYYY/MM/DD tree like codex), so **no hierarchical date-dir watching is needed**. A single watched directory + a periodic reconcile for files that appear after start is sufficient.
- **Bootstrapping a not-yet-existing dir.** The encoded-cwd directory may not exist until pi first writes to it. Mirror claudecode's `WatchForClaudeSetup` idea: if `ProjectSessionDir` doesn't exist yet, watch the nearest existing ancestor (`piSessionsRoot()`), and when the encoded-cwd subdir is created, add it and start scanning. Keep this minimal.
- **Event loop** (`select` over `watcherCtx.Done()`, `watcher.Events`, `watcher.Errors`): on `Create`/`Write` of a `*.jsonl` path, dispatch a scan; ignore non-`.jsonl`; on `Remove`/`Rename` drop it. Dispatch the callback in a **per-session recover-guarded goroutine** so a slow/panicking callback never blocks the watch loop (both siblings do this).
- **Reparse strategy = whole-file, no byte-offset tailing.** Both siblings re-parse the entire changed file on every write event; pi already has a robust full-file reader. **Reuse `ParseSession(path)` (`agent_session.go:115`)** to produce `*schema.SessionData`, then convert to `*spi.AgentChatSession` the same way `parseToAgentSession`/`GetAgentChatSessionByPath` (`provider.go:346`/`:376`) already do (`SessionID`, `CreatedAt`, `Slug: deriveSlug(data)`, `SessionData`, `RawData`). **Do not invent a new tailer** — DRY reuse of `ParseSession` is the whole point of pi's existing read path.

`WatchAgent` method body (mirror claudecode `provider.go:412`):
1. `SetWatcherCallback(sessionCallback); defer ClearWatcherCallback(); SetWatcherDebugRaw(debugRaw)`.
2. Start watcher via the same `WatchForProjectDir(projectPath)` used by `run`.
3. `<-ctx.Done(); StopWatcher(); return ctx.Err()` (single exit).

`StopWatcher()`: `watcherCancel(); watcherWg.Wait()` (graceful join both run and watch rely on).

### 2.3 Edge cases (pi-specific)
- **Partial JSON lines mid-write.** pi's `readLines` (`jsonl_parser.go:37`) uses `bufio.Reader.ReadLine()` with fragment accumulation and a 250 MB per-line cap (deliberately **not** `bufio.Scanner`, whose 16 MB cap rejects legit large tool-result/base64 lines). A half-written trailing line at the moment of an fsnotify `Write` is tolerated because `decodeEntry` (`:117`) returns `ok=false` for a malformed/typeless line and the parser **skips** it rather than aborting; the next write event re-parses and picks it up. **No new partial-line handling is required** — the existing reader already covers it. Add a watcher test that writes a truncated last line then completes it (see §Tests).
- **v1-format (no id/parentId) sessions.** `decodeEntry` already synthesizes `legacy-<n>` ids and chains `parentId` to the prior entry (reproducing pi's own `migrateV1ToV2` load behavior), so a v1 file streamed during watch parses into the same linear active path a migrated file would. No watcher-specific work.
- **Empty / header-only sessions.** `readHeader` returns `(nil,nil)` for empty files; `ParseSession` errors on "no entries" and `scanPiSession` returns `(nil,nil)` for header-only. The watcher must **treat a parse error / nil session as "nothing to emit yet"** (skip, wait for the next write) — do not propagate it as a fatal watch error. This mirrors claudecode skipping a file whose session id can't yet be extracted.
- **Session rename (`session_info`) during live watch.** A `session_info` entry appended mid-conversation changes the display name. Because the watcher re-parses the whole file and `scanName`/name derivation already prefer the **latest** `session_info` name (`jsonl_parser.go` `readScanEntries`; `provider.go:538` `scanName`), the re-emitted `AgentChatSession` naturally reflects the rename. No special handling — just ensure the conversion path used by the watcher runs the same name logic (it does, via the existing parse → convert helpers).
- **Concurrent writes / active branch churn.** Re-parse always selects the current leaf via `leafPathEntries`/`walkToRoot` with the existing parentId-cycle guard, so a branch switch (prompt re-edit) between events just changes which active path is emitted — expected behavior, already covered by the read side.
- **Flat layout (`PI_CODING_AGENT_SESSION_DIR`).** Files land directly in the root with no per-cwd encoding; the watcher must filter emitted files by **header `cwd` == project candidate** (reuse `flatSessionFiles` logic / `projectCandidates`) so `watch` doesn't emit other projects' sessions.

---

## Capability 3 — `ReconstructSession` + `NativeSessionPath` (native JSONL v3 serializer)

### 3.1 Methods & files

| File | New/changed | Contents |
|---|---|---|
| `pkg/providers/piagent/reconstruct.go` | **new** | `ReconstructSession(data, opts) (*spi.ReconstructedSession, error)`, `NativeSessionPath(projectPath, filename) (string, error)`, `SupportsReconstruction() bool` → move these three off provider.go into `reconstruct.go` to match the sibling file layout (claudecode/codexcli keep all three in `reconstruct.go`). Plus small helpers: `piNativeFilename(base time.Time, id string) string`, `resolvePiSessionDir(projectPath) (string, error)`. |
| `pkg/providers/piagent/provider.go` | **changed** | Delete the three stubs (`:186`, `:191`, `:198`) — they move to `reconstruct.go`. |

> Keeping `SupportsReconstruction` next to the serializer (as siblings do) makes the "must agree with ReconstructSession/NativeSessionPath" contract self-evident in one file.

### 3.2 Approach — `ReconstructSession` (mirror claudecode `reconstruct.go:36`)

Reconstruction fidelity bar (per `docs/SESSION-PORTABILITY.md`): output must be **valid to pi's own loader** and convey the *gist* of the prior conversation. It is **not** a structural round-trip and **not** faithful tool replay.

Reuse the shared SPI core verbatim (identical to both siblings):
1. `turns, err := spi.PrepareTurns(data, opts)` → guards nil data + ≥1 turn, and **flattens** `schema.SessionData` into `[]spi.Turn{Role,Text}` via `spi.FlattenSessionData`. **Tool calls and thinking are folded into agent text by the flattener — pi must NOT re-synthesize structured `toolCall`/`toolResult` entries** (same decision claudecode & codexcli make). This means pi's read-side `toolResultMessage`/`contentBlock{type:"toolCall"}` structs are irrelevant to the write side.
2. `cwd := spi.ResolveWorkspaceRoot(opts, data)` (opts.WorkspaceRoot, else data.WorkspaceRoot).
3. `newID := uuid.NewString()` (pi header ids observed as `<timestamp>_<uuid>` filenames; the header `id` itself just needs to be a fresh unique string — a v4 UUID is safe. **Stage 2: confirm from a real pi file whether the header `id` equals the uuid part of the filename**; if pi requires them to match, derive both from the same `newID`).
4. `var buf bytes.Buffer; enc := json.NewEncoder(&buf); enc.SetEscapeHTML(false)` (keep `< > &` literal in code/markdown text — same as siblings).
5. Timestamps via `spi.RFC3339Millis(base.Add(i * time.Second))`, `base := time.Now().UTC()`.

**Native pi JSONL v3 shape to emit** (this is the pi-specific part — derived from the read structs `sessionHeader`, `rawEntry`, `userMessage`, `assistantMessage` in `agent_session.go`):

- **Line 0 — session header** (`type=="session"`, no id/parentId): mirror `sessionHeader`:
  ```
  {"type":"session","version":3,"id":<newID>,"timestamp":<ts0>,"cwd":<cwd>,
   "specstorySourceSessionId":<data.SessionID>}
  ```
  `version:3` (v3 tree). `specstorySourceSessionId` is provenance (extra field; pi's `json.Decode` of `sessionHeader` ignores unknown fields, so it's safe and mirrors how claudecode/codexcli stamp provenance). **Stage 2: verify `version:3` is the value pi writes today** (read side maps `header.Version>0` → `"v3"`; confirm the integer pi emits — likely `3`).
- **Lines 1..N — one `message` entry per flattened turn**, wrapping the `rawEntry` envelope with a linear parentId chain (mirrors claudecode's linear `parentUuid` chain, but with pi field names `id`/`parentId` and a wrapped `message` payload):
  ```
  {"type":"message","id":<recID>,"parentId":<prevID-or-null>,"timestamp":<ts_i>,
   "message": <userMessage | assistantMessage>}
  ```
  - `recID := uuid.NewString()` per entry.
  - **First message** entry: `"parentId": null` (matches `rawEntry.ParentID` "null for the first entry"; `walkToRoot` stops at nil). Provenance already on the header, so the first message needs nothing special beyond `parentId:null`.
  - **Subsequent**: `"parentId": <previous recID>` — thread `prevID` through the loop exactly like claudecode threads `prevUUID`.
  - **User turn** (`turn.Role == schema.RoleUser`) → payload mirrors `userMessage`:
    `{"role":"user","content":<turn.Text as string>,"timestamp":<epoch-ms>}`.
    (`userMessage.Content` is `json.RawMessage` and the read side accepts a bare string OR array — emit the **string** form; it's the simplest valid shape.)
  - **Agent turn** → payload mirrors `assistantMessage`:
    `{"role":"assistant","content":[{"type":"text","text":<turn.Text>}],"provider":"specstory","model":<placeholder>,"api":"","stopReason":"end_turn"}`.
    Use a constant placeholder model like the siblings do (`reconstructedClaudeModel="claude-opus-4-8"`, codex `cli_version="0.139.0"`); define `reconstructedPiModel` / `reconstructedPiProvider` consts. **Stage 2: read one real pi assistant entry to confirm which of `provider`/`model`/`api`/`stopReason` pi's loader *requires* vs tolerates empty**; set only what's needed for the loader to accept the file. Omit `usage` (optional, `omitempty`).
- `enc.Encode(rec)` writes one object per line into `buf`.
- Return `&spi.ReconstructedSession{SessionID: newID, Filename: piNativeFilename(base, newID), Content: buf.Bytes()}`.

`piNativeFilename(base, id)` → `fmt.Sprintf("%s_%s.jsonl", base.Format("<pi-timestamp-layout>"), id)` reproducing pi's `<timestamp>_<uuid>.jsonl`. **Stage 2: read a real filename to confirm the timestamp layout** (e.g. `20060102T150405` vs epoch vs RFC3339-with-safe-separators). Filesystem-safe separators only (no `:`), since it's a filename.

### 3.3 `NativeSessionPath` (mirror claudecode `reconstruct.go:112` + codex's project-unaware variant)

```
func (p *Provider) NativeSessionPath(projectPath, filename string) (string, error) {
    dir, err := resolvePiSessionDir(projectPath)   // does NOT require dir to exist
    if err != nil { return "", err }
    return filepath.Join(dir, filename), nil        // caller creates dir + writes file
}
```
- `resolvePiSessionDir` = **reuse `ProjectSessionDir(projectPath)`** (`path_utils.go:101`): default layout → `piSessionsRoot()/EncodeCwd(absCwd)` ; flat layout → the override root itself. `ProjectSessionDir` already resolves *without requiring existence* and already handles both layouts + tilde/env overrides — exactly the write-side path the issue asks us to reuse.
- **Do NOT create the directory.** Both siblings' `NativeSessionPath` doc-comments state the caller (`specstory resume`) does `MkdirAll`+`WriteFile`. `SupportsReconstruction` must stay pure/no-filesystem, so directory creation cannot leak here.
- **cwd encoding for the write side is identical to the read side** — `EncodeCwd` (`path_utils.go:62`) is the single source of truth (strip one leading slash; replace `/ \ :` with `-`; wrap in `--`). This guarantees a reconstructed file lands in the very directory pi (and pi's own read path) will look in.
- Cross-platform: `EncodeCwd` is shape-based (no `runtime.GOOS`), and `projectCandidates` handles symlink-resolved forms. `NativeSessionPath` targets **`projectCandidates(projectPath)[0]`** (the raw abs path) — the deterministic form; that's what `ProjectSessionDir` already returns.

### 3.4 `SupportsReconstruction` → `true`
- Flip `provider.go:198` body from `return false` to `return true` (moving the method to `reconstruct.go`).
- **Blast-radius check (grepped `SupportsReconstruction` on `feat/pi-provider`):**
  - Runtime gates: `pkg/cmd/resume.go:119` (preset-target validation — pi becomes an accepted `resume` preset target) and `pkg/cmd/session_tui.go:1041` (`eligibleTargets` — pi becomes offered as a cross-agent resume target). Both are the **intended** effect of shipping reconstruction; no code change needed there.
  - Test `pkg/cmd/resume_test.go:253` `TestSupportsReconstruction` pins `{"claude":true,"codex":true,"antigravity":false}` — **pi is not in this map, so flipping pi does not break it.** Recommend **adding `"pi":true`** to that map so the capability is regression-guarded (small, in-scope test edit).
  - `TestEligibleTargets` (`resume_test.go:264+`) uses fake providers, not the real pi registry entry → unaffected.
  - The `spi.Provider` contract requires `SupportsReconstruction()==true` to **agree** with `ReconstructSession`/`NativeSessionPath` no longer returning `ErrReconstructionUnsupported` — satisfied because §3.2/§3.3 replace both stubs with real serializers in the same change.

### 3.5 Reconstruction edge cases (the id/parentId active-path questions)
- **"Active path" for reconstruction = a single linear leaf-to-root chain.** We are serializing a *flattened* `[]Turn` (already linear — the source `SessionData` was itself produced by the forward parser's `leafPathEntries`, discarding branches). So the write side emits a **strictly linear parentId chain** (first entry `parentId:null`, each next → previous id). We **discard branches by construction** — there is no branch information in `SessionData` to preserve, which is consistent with how the forward parser collapses the tree to the active leaf path.
- **Write-side cycle guard?** Not needed. The read-side `walkToRoot` guards against corrupted `parentId` cycles, but we *generate* a fresh strictly-increasing linear chain (each `recID` is a new UUID, each `parentId` points only backward), so a cycle is structurally impossible. Add a round-trip test (below) that re-parses our output through `walkToRoot` to prove the chain terminates and preserves order.
- **Empty / no-content session.** `spi.PrepareTurns` already returns `"session has no content to reconstruct"` for zero turns and `"cannot reconstruct nil session data"` for nil — pi inherits identical error messages for free (same as siblings). Add `TestReconstructSession_Empty`.
- **v1-format sessions** are irrelevant to the write side: we always emit a fresh **v3** header + id/parentId entries regardless of the source provider/format. (v1 only matters to pi's *read* path.)
- **Header-only output impossible:** a valid reconstruction always has ≥1 turn (guarded), so we never emit a header-only file.
- **session_info / rename** entries are **not** emitted (the flattener drops names; reconstruction only needs user/agent text turns). A resumed pi session simply starts unnamed — acceptable for Stage-1 cross-provider resume.

---

## Documentation updates (exact edits)

Both docs on `feat/pi-provider` already have a pi row **and** a "not yet supported" note. Stage 2 **edits the existing notes** (does not add rows). Do **not** touch the monorepo-root `README.md` (different higher-level doc, out of scope).

### `specstory-cli/README.md:41`
Replace the note (currently):
> **Note:** Pi support currently covers `sync`, `list`, `search`, `reindex`, `check`, and `detect`. `specstory run pi`, `specstory watch pi`, and cross-machine session resume are not yet supported.

With (adjust wording to the confirmed resume mechanism from §0):
> **Note:** Pi support covers `sync`, `list`, `search`, `reindex`, `check`, `detect`, `run`, and `watch`, plus cross-provider session reconstruction (`specstory resume`). *(If §0 finds pi cannot resume-by-id, keep a trimmed caveat, e.g. "`run`/`watch` are supported; cross-machine resume is limited by pi's own resume support.")*

Keep the pi table row (`:39`) unchanged (Agent/Provider/Data Format/Source Location columns; there is no per-capability column to update in README).

### `specstory-cli/docs/PROVIDER-SPI.md:20`
Replace the note (currently):
> The `pi` provider currently implements sync/list/search/reindex/check/detect; `run`, `watch`, and session reconstruction return descriptive "not yet supported" errors.

With:
> The `pi` provider implements sync/list/search/reindex/check/detect, `run`, `watch`, and native session reconstruction (`ReconstructSession`/`NativeSessionPath`), so it can be a cross-provider `resume` target.

Keep the pi row (`:17`, `Provider ID | Display Name | Session Storage`) unchanged. Neither doc has a per-capability ✓ matrix, so no matrix edits.

### Changelog
Add a `changelog.md` entry (repo keeps `specstory-cli/changelog.md`) noting pi gained `run`/`watch`/reconstruction. **No time/date estimates in docs** (CLAUDE.md).

---

## `notYetSupport` const / stale doc-comments cleanup

- `provider.go:27` `notYetSupport` currently lists v1 scope `(sync, list, search, reindex, check, and detect)`. After all three capabilities ship, **all four methods that used it are gone**, so:
  - If **no** remaining caller uses `notYetSupport` (grep after the edits) → **delete the const**.
  - If §0 concludes pi genuinely cannot resume-by-id and `run`/`watch` are therefore only partially wired, keep a narrowly-scoped error const for that single real gap and reword it — do **not** keep the blanket v1 wording.
- Delete/replace the four stale doc-comments "— out of v1 scope" above the (former) stubs (`:175`, `:180`, `:185`, `:190`) and the `SupportsReconstruction` comment (`:195-197`) that says "must never be offered as a … resume target". New comments should describe the real behavior (mirror the sibling reconstruct.go/watcher.go comment style: "why", not "what").

---

## Test plan (new `*_test.go`, table-driven, mirroring siblings)

Package `piagent`, `*_test.go` alongside source (CLAUDE.md convention). Mirror `claudecode`/`codexcli` test files.

### `pkg/providers/piagent/reconstruct_test.go` (mirror both siblings' `reconstruct_test.go`)
- **`TestReconstructSession_RoundTrip`** — the key round-trip (present in both siblings). Build a `reconstructSampleData()` `*schema.SessionData` exercising user text, agent text, agent thinking, and a tool call (with `FormattedMarkdown`) — i.e. every flatten path. Reconstruct → assert non-empty `SessionID`, filename matches `<timestamp>_<uuid>.jsonl`, then **re-parse `out.Content` back through pi's own `ParseSession`/`readEntries`** and assert the flattened result equals `spi.FlattenSessionData(data, "")` turn-for-turn. Proves flatten→serialize→(pi parse)→flatten is stable and that pi's own reader accepts our output.
- **`TestReconstructSession_Chain`** — assert the linear parentId chain: header is line 0 with `type=="session"`, `id==out.SessionID`, and `specstorySourceSessionId==data.SessionID`; first `message` entry has `parentId==nil`; every subsequent entry's `parentId` == previous entry's `id`; feed the entries through `walkToRoot` and assert it terminates and yields all turns in order (proves no accidental cycle / correct rooting).
- **`TestReconstructSession_Empty`** — nil-content `SessionData` → expect `spi.PrepareTurns` error "session has no content to reconstruct".
- **`TestNativeSessionPath`** — assert returned path contains `filepath.Join(".pi","agent","sessions")` and the `--<encoded-cwd>--` segment (use `EncodeCwd` to compute the expected segment) and ends with the passed filename; add a flat-layout case (`t.Setenv(envSessionDir, tmp)`) asserting the path is `<override>/<filename>` with no encoded segment.
- Helper `parsePiJSONL(t, content)` = scanner/`bufio.Reader` with a large buffer (siblings use 16 MB `bufio.Scanner`; pi should use its own `readLines`-style reader since lines can exceed 16 MB) unmarshalling each line into `map[string]any` / `rawEntry`.

### `pkg/providers/piagent/watcher_test.go` (mirror claudecode `watcher_test.go`)
- **`TestWatch_EmitsOnNewSession`** — start watcher against a temp encoded-cwd dir (`t.Setenv(envAgentDir, tmp)`), write a valid pi session file, assert the callback fires with the expected `SessionID`/`Slug`.
- **`TestWatch_PartialLineThenComplete`** — write a truncated final JSON line, trigger an event (assert no panic and no emit or a graceful skip), then append the completion + newline and assert the callback then fires. Exercises `readLines` fragment handling under live write.
- **`TestWatch_SessionInfoRename`** — append a `session_info` entry mid-session; assert the re-emitted session's `Name` reflects the new name (latest-wins).
- **`TestWatch_FlatLayoutFiltersByCwd`** — flat `PI_CODING_AGENT_SESSION_DIR`; write two files with different header `cwd`; assert only the matching-project session is emitted.
- **`TestWatch_IgnoresNonJSONLAndHeaderOnly`** — a `.txt` file and a header-only `.jsonl` produce no emit.
- If the watcher has a pure reconcile/scope helper (like claudecode's `reconcileFileWatches`), add a table-driven test for it; pi's simpler single-dir model may not need one.

### `pkg/providers/piagent/piagent_exec_test.go` (mirror claudecode `claude_code_exec_test.go`; codex has none)
- **`TestParsePiRunCommand`** — table-driven: empty command → `("pi", nil)`; custom command with quotes → correct split (via `spi.SplitCommandLine`); `~/bin/pi` → tilde-expanded; **resume id present** → correct resume args (subcommand-or-flag per §0), including the "custom command already names resume" case if `spi.EnsureResumeArgs` is used (its dedupe branch).
- **`TestGetDefaultPiCommand`** — only if `getDefaultPiCommand` probes install locations; if it's a plain `"pi"` return, skip per CLAUDE.md ("don't test tautological things").
- Do **not** unit-test `ExecutePi`'s actual process spawn (it execs a real binary + `os.Exit`); cover it via `parsePiRunCommand` instead, exactly as claudecode does.

### Regression edit
- Add `"pi": true` to `pkg/cmd/resume_test.go:253` `TestSupportsReconstruction`.

---

## CLAUDE.md conventions to honor (called out for Stage 2)
- **`wg.Go(func(){…})`** for the watcher goroutine — never `wg.Add(1)`+`go func(){defer wg.Done()}`.
- **`log/slog`** for all logging (the watcher/exec paths); never `fmt.Println`/`fmt.Printf`. User-facing lines use `log.UserMessage` (as elsewhere in `provider.go`).
- **Single function exit point** where practical (guard clauses OK) — structure `ExecAgentAndWatch`/`WatchAgent`/`ReconstructSession` to a single `return` at the end.
- **Cross-platform (macOS/Linux/WSL/Windows)** — reuse the shape-based `EncodeCwd`/`projectCandidates`; no new `runtime.GOOS` branches; `filepath.Join` for all paths; filesystem-safe reconstructed filename (no `:`).
- **DRY / reuse first** — reuse `ParseSession`, `ProjectSessionDir`, `EncodeCwd`, `parsePiCommand`, `spi.PrepareTurns`, `spi.ResolveWorkspaceRoot`, `spi.RFC3339Millis`, `spi.FlattenSessionData`, `spi.EnsureResumeArgs`; don't reimplement.
- **Ask before new files/deps** — this plan adds `piagent_exec.go`, `watcher.go`, `reconstruct.go` and no new third-party dependency (fsnotify + google/uuid are already used by the siblings and are in `go.mod`). Stage 2 should confirm both are already required before importing.
- Run **`gofmt -w .`** and **`golangci-lint run`** (whole project) until clean; **`go test ./...`** green.

---

## Summary of file changes for Stage 2 (GENERATOR)

| Path (under `specstory-cli/`) | Action |
|---|---|
| `pkg/providers/piagent/piagent_exec.go` | **new** — `parsePiRunCommand`, `getDefaultPiCommand`, `ExecutePi` |
| `pkg/providers/piagent/watcher.go` | **new** — package-global fsnotify watcher (run+watch), `wg.Go` |
| `pkg/providers/piagent/reconstruct.go` | **new** — `ReconstructSession`, `NativeSessionPath`, `SupportsReconstruction`(→true), `piNativeFilename` |
| `pkg/providers/piagent/provider.go` | **changed** — real `ExecAgentAndWatch`/`WatchAgent` bodies; remove 3 reconstruct stubs; delete/reword `notYetSupport` + stale v1 comments |
| `pkg/providers/piagent/reconstruct_test.go` | **new** — round-trip, chain, empty, NativeSessionPath (+flat) |
| `pkg/providers/piagent/watcher_test.go` | **new** — new-session, partial-line, rename, flat-cwd filter, ignore-nonjsonl |
| `pkg/providers/piagent/piagent_exec_test.go` | **new** — `TestParsePiRunCommand` (table-driven) |
| `pkg/cmd/resume_test.go` | **changed** — add `"pi":true` to `TestSupportsReconstruction` |
| `README.md` (`:41` note) | **changed** — capabilities note |
| `docs/PROVIDER-SPI.md` (`:20` note) | **changed** — capabilities note |
| `changelog.md` | **changed** — add entry |

**Open items Stage 2 must resolve first (from real `pi` inspection):** (1) pi's resume-by-id mechanism (`pi --help`); (2) exact header `version` integer + whether header `id` must equal the filename uuid; (3) which assistant-message fields pi's loader requires; (4) the `<timestamp>` filename layout. None block the reconstruction *write* design — they only fix constant/format details and the ~3 resume-arg lines.
