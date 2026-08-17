# Provider fixes

**Date:** 2026-08-17 · **Status:** All three implemented. §1 resolved as option A.
**Scope:** Startup emit behavior across all ten watching providers, Codex's full-history scan, and Codex's resume argument construction. Surveyed against every `pkg/providers/*/watcher.go`, `pkg/cmd/watch.go`, `pkg/cmd/autosave.go`, `pkg/utils/watch_agents.go`, and the provider exec paths.

---

## 1. The startup emit inconsistency — resolved as option A

Every provider now watches without emitting at startup, and adopts a directory or file when its watch lands late. The table and analysis below describe the state before the fix; what changed is recorded at the end of this section.



What `specstory watch` does with sessions that already exist on disk depends entirely on which agent is being watched. There are three different policies and no shared rule.

|     Provider     |  Startup policy   |                                  Scope of the emit                                  |
| ---------------- | ----------------- | ----------------------------------------------------------------------------------- |
| `codexcli`       | scan and emit     | entire history — every year/month/day dir found (`watcher.go:290`, `:386`)          |
| `droidcli`       | scan and emit     | every session file in scope; `lastProcessed` starts empty (`watcher.go:40`, `:204`) |
| `deepseektui`    | scan and emit     | same shape as droid (`watcher.go:41`)                                               |
| `antigravitycli` | scan and emit     | "Initial scan so existing sessions are emitted immediately" (`watcher.go:54`)       |
| `copilotide`     | seed and suppress | stats every existing file into `knownFiles`, emits none (`watcher.go:194`)          |
| `cursoride`      | seed and suppress | `seedKnownComposers` records timestamps only (`watcher.go:194`)                     |
| `cursorcli`      | seed and suppress | `SetInitialState` takes existing IDs from the caller (`watcher.go:63`)              |
| `claudecode`     | watch only        | `reconcile(false)` at startup, `reconcile(true)` after (`watcher.go:236`, `:296`)   |
| `geminicli`      | watch only        | pure event loop, no scan anywhere (`watcher.go:205`)                                |
| `musecode`       | watch only        | event-driven, plus bootstrap adoption (`watcher.go:210`)                            |

`vscode` has no `WatchAgent` and is sync-only, so it does not participate.

Seed-and-suppress and watch-only reach the same user-visible outcome by different means: the former walks the store to record what is already there so the first real check will not re-emit it, the latter simply never looks. The extra work buys robustness — a seeded watcher cannot later mistake pre-existing state for new activity.

### What each policy costs

Scan-and-emit sends every pre-existing session through `NewAutosaveCallback` (`pkg/cmd/autosave.go:119`): markdown regenerated and written, cloud sync pushed, live index updated, provenance events processed. There is no unchanged-content guard on that path. The upside is that a session finished moments before `watch` started is still captured, and stale or missing markdown self-heals.

Seed-and-suppress and watch-only do no redundant work, but a session completed just before `watch` started stays invisible until `sync` runs.

The shared dedup in `utils.WatchProviders` does not soften this: `lastFingerprint` (`watch_agents.go:66`) is created per run and starts empty, so the first emit of each session always reaches the writer.

### Two things that make this confusing

The command layer is built around scan-and-emit. `pkg/cmd/watch.go:216` carries a `seenSessions` map whose only purpose is to swallow console output from a startup emit — "Existing sessions found at startup get their markdown refreshed but don't produce output". The markdown refresh and cloud sync it describes still happen, silently, and only for the four scan-and-emit providers. For the other six that suppression logic is dead code.

Claudecode's justification points at something that does not exist. Its startup comment says "startup sync is the command layer's job" (`watcher.go:296`), but `watch` has no startup sync — `pkg/cmd/watch.go:321` goes straight to `utils.WatchProviders`, and there is no pre-watch processing pass anywhere in the command. The reasoning holds for `run`, where the agent is being started fresh, but not for `watch`.

### What was implemented

The four scan-and-emit providers were converted, by the means each one's structure made natural:

|     Provider     |                                                                                                                                                                        Change                                                                                                                                                                        |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `codexcli`       | `watchDayDir`/`watchMonthDir`/`watchYearDir` take a `scan` flag — false for the startup walk, true when a directory is adopted from a Create event. The unconditional `scanDayDir(initialDayDir)` after the walk is gone, and with it the `initialDayDir` parameter, which existed only to feed it; `pinnedDayDir` still carries the resumed session |
| `droidcli`       | `seedProcessedSessions` records existing files' mtimes without parsing, replacing the initial `scanAndProcessSessions`                                                                                                                                                                                                                               |
| `deepseektui`    | same as droid                                                                                                                                                                                                                                                                                                                                        |
| `antigravitycli` | `seedProcessedConversations`, same shape over `listConversationFiles`                                                                                                                                                                                                                                                                                |

Seeding rather than not-looking is the right implementation for the three `lastProcessed`-based providers: their scan function is also the adopt path, so suppressing the emit has to happen through the state the scan already consults, and their later scans keep adopting unchanged.

Codex needed the flag instead because it holds no per-session state — `ScanCodexSessions` re-emits a directory wholesale every time — so startup and adoption had to be told apart at the call site. `watchDayDir` serves both, and the day-rollover path passes `scan: true` because a directory appearing mid-flight holds activity nobody observed.

**The command layer lost its suppression, which was also a live bug.** `pkg/cmd/watch.go` kept a `seenSessions` map that dropped output for any session whose markdown already existed and which it was seeing for the first time. That correctly hid startup noise from scan-and-emit providers, but for the six that never emitted at startup it swallowed the *first real update* to any pre-existing session: the user's first prompt after starting `watch` printed nothing, and only the second one appeared. With no provider emitting at startup the map has no legitimate work left, so it is gone and the first update prints.

Not changed: the seed-and-suppress trio (`copilotide`, `cursoride`, `cursorcli`) and the watch-only trio (`claudecode`, `geminicli`, `musecode`) already behaved this way.

## 2. Codex's full-history scan — implemented

Treat this as a bug independent of whichever way item 1 is decided.

At startup `watchDayDir` scans every day directory it discovers, and `shouldWatch` gates only the fsnotify watch, not the scan (`codexcli/watcher.go:281`). `watchMonthDir` and `watchYearDir` walk every year and month present. The doc comment at `watcher.go:167` states this plainly: "All historical day directories are scanned at startup, but fsnotify watches are limited to directories within the trailing `spi.WatchWindowDays` window."

So every `specstory watch` or `specstory run` against Codex re-emits years of sessions, each one triggering a markdown rewrite and a cloud sync. The watch window that exists to bound fd usage does not bound this.

**Fixed.** `watchDayDir` now returns early for a day outside scope instead of scanning it unwatched, so the window bounds reprocessing as well as fds. The gate moved out of the closure into `dirInWatchScope` (`codexcli/watcher.go:163`), which `pruneStaleWatches` now shares rather than repeating the window check with its own pinned-directory exception. `watchDayDir` serves both startup and day rollover (`:436`), and the same predicate is correct for both: a rolled-over day is today, so it is always in scope.

Two things deliberately left alone. The walk still descends through out-of-window year and month directories doing `ReadDir`, which is a handful of cheap syscalls now that no parsing follows; pruning the descent would need a `pinnedDayDir` containment check for the case where the resumed session is in an old year. And `scanDayDir(initialDayDir)` (`:398`) still runs after the walk, which double-scans today's directory since the walk already covered it — harmless (the `lastFingerprint` dedup suppresses the second emit) and worth keeping as the guarantee that a resumed session's directory is scanned even if the walk failed to reach it.

## 3. Codex's resume arguments — implemented

`ExecuteCodex` builds the resume command line unconditionally (`codexcli/codex_cli_exec.go:234`):

```go
args := append(customArgs, "resume", resumeSessionID)
```

Nothing checks whether `customArgs` already names the resume subcommand, so a configured command that does produces a malformed line:

|  Configured command   | Requested id |               Result                |
| --------------------- | ------------ | ----------------------------------- |
| `codex`               | `new-id`     | `codex resume new-id` — correct     |
| `codex resume`        | `new-id`     | `codex resume resume new-id`        |
| `codex resume old-id` | `new-id`     | `codex resume old-id resume new-id` |

Both broken cases violate the same contract as the Muse defect: `pkg/spi/provider.go:101` specifies that a non-empty `resumeSessionID` means "resume this specific session ID". Codex fails louder than Muse did — a malformed argument list rather than silently opening the wrong conversation — but it still cannot honor the request.

The fix already exists in `musecode.ensureResumeArgs` (`pkg/providers/musecode/muse_exec.go:40`): find an existing `resume` subcommand, fill in the id after it or replace a pinned one, and only append the pair when the subcommand is absent. Both providers use the identical `<binary> resume <id>` shape, so this is a candidate to lift into `pkg/spi` rather than copy — see `docs/PROVIDER-REFACTOR.md` for the standing helper-duplication work.

Two smaller notes while in this code. `append(customArgs, ...)` writes into the backing array of `parts[1:]` returned by `parseCodexCommand` (`:33`) whenever that slice has spare capacity; nothing else holds `parts` today, so this is latent rather than a live bug, but the Muse equivalent deliberately avoids it with `slices.Concat`/`slices.Clone`. And the `resumeSessionID != ""` branch duplicates the parse-and-exec sequence of the `else` branch (`:225-248`) where only the argument list differs.

**Fixed, as the shared helper rather than a copy.** Muse's `ensureResumeArgs` was lifted into `spi.EnsureResumeArgs` (`pkg/spi/cmdline.go`), taking the subcommand as a parameter so the helper does not hardcode one provider's vocabulary. Both providers now call it — Codex at `codex_cli_exec.go:236`, Muse at `muse_exec.go:39` — and the table test moved with it to `pkg/spi/cmdline_test.go`. This also retires the aliasing note above, since the helper never appends to the caller's slice.

The duplicated parse-and-exec branches in `ExecuteCodex` are untouched and still worth collapsing.

## 4. Options for item 1 — A was chosen

**A — standardize on watch-only plus adopt-on-appearance.** What Muse and Claude Code do now: never emit at startup, but adopt a directory or file when its watch lands after the watcher was already running, since that means activity happened unobserved.

- Pro: no redundant writes or cloud syncs; one rule to reason about; the `seenSessions` suppression in `watch.go` can then be deleted rather than left half-dead.
- Con: a session that completed just before `watch` started needs `sync` to be picked up. Four providers change behavior.

**B — standardize on catch-up, but move it to the command layer.** One explicit pass that runs once for all providers before `WatchProviders`, rather than four providers each doing it their own way.

- Pro: keeps the self-healing property; makes it a visible, testable, opt-in-able step; fixes Codex by construction.
- Con: new surface in the command layer; needs a decision about whether it is default-on or a flag.

**C — leave the policies alone, fix only Codex.** Bound its startup scan to the trailing window.

- Pro: smallest change; removes the worst offender.
- Con: the inconsistency survives, and so does the dead suppression code.

Recommendation is **A**, with **C** applied regardless since Codex's full-history scan is indefensible under any of the three.

Both were done: C first as a standalone fix (§2), then A across the four scan-and-emit providers. The self-healing property named in A's con column is genuinely gone — a session finished moments before `watch` starts now needs `sync` — and the changelog says so.

## 5. How the survey was verified

Each provider's watcher was read directly; the emit paths were traced to `spi.DispatchSession` (`pkg/spi/global.go:148`) or the provider's own `triggerCallback`, and the absence of a startup sync in `watch` was confirmed by grepping the command for `ProcessSingleSession`, `ProcessAllSessions`, `GetAgentSessions`, and `SyncSessions` — none appear.

Codex's resume construction was read directly; the two broken cases in §3 follow from `append` being unconditional and are not reproduced against the real binary.

The Muse counterpart of §3 is already fixed: `ensureResumeArgs` now replaces a pinned id rather than discarding the requested one, covered by the "requested session replaces one pinned in the command" and "pinned id is replaced without disturbing surrounding args" cases in `pkg/providers/musecode/muse_exec_test.go`. One Muse edge remains open there — `codex`-style `resume --last` in a configured command yields `resume <id> --last`, and how Muse resolves an id alongside `--last` has not been checked against the binary.

Coverage for §1 is uneven by design. `antigravitycli` carries the full contract as a test — seed, scan emits nothing, bump the mtime, scan emits — verified to fail when the seed is neutered. `droidcli` and `deepseektui` have their seed functions tested directly (every transcript recorded, non-transcripts left out, missing store tolerated) because neither package has a fixture helper for writing a parseable session, and the mtime guard their scan applies runs before parsing, so a garbage file cannot tell the seeded and unseeded paths apart. `codexcli`'s change is control flow inside the watcher goroutine's closures, which has no test harness; only `dirInWatchScope` is unit-tested.

The Muse bootstrap case from the same investigation is already fixed and covered by `TestRefresh_AdoptsSessionsWrittenWhileTheStoreDidNotExist` and `TestRefresh_DoesNotAdoptSessionsThatPredateTheWatcher` in `pkg/providers/musecode/watcher_test.go`. The second of those is the guard that keeps a future change from turning every watcher start into a re-publish of the trailing window.
