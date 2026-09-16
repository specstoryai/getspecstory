# New Provider Guide

This guide is for anyone, human or agent, adding support for a new coding agent to the SpecStory CLI. It tells you what a complete provider contains, the standards a submission is held to, and how it will be exercised before release, so your pull request lands with as few needed changes as possible.

The provider interface is specified by the doc comments on `spi.Provider` in `pkg/spi/provider.go` and on the two optional interfaces in `pkg/spi/global.go`; read those first. The unified session format is `pkg/spi/schema/types.go`, explained in [docs/SPI-SESSION-DATA-SCHEMA.md](docs/SPI-SESSION-DATA-SCHEMA.md).

Maintainers review every provider submission with [docs/NEW-PROVIDER-REVIEW.md](docs/NEW-PROVIDER-REVIEW.md). Reading it is the fastest way to see exactly what will be checked, and in what order.

## Before you write code

### Pick an exemplar and copy its shape, not its code

Every provider is judged by parity with the established siblings. Choose the closest one and mirror its file layout, method shapes, and behaviors. No single provider is reference-grade for everything, so take each concern from the provider named for it:

|                                                Concern                                                |                                              Copy from                                               |
| ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Parsing a JSONL session store, per-record debug output, a watcher that reconciles on a bounded window | `pkg/providers/claudecode`                                                                           |
| Tool rendering keyed to the agent's real inventory, `Check` with shared analytics, lifecycle logging  | `pkg/providers/musecode`, `pkg/providers/antigravitycli`                                             |
| Watch-only startup with adoption of late-arriving directories                                         | `pkg/providers/musecode`                                                                             |
| Reporting the agent's exit status without losing the last save                                        | `pkg/spi/exit.go` (its doc comment states the contract)                                              |
| Bounded line reading                                                                                  | `pkg/spi/jsonl.go` (`spi.ReadRecordLine`); capped `bufio.Scanner` for sidecars: `pkg/providers/musecode` |
| An IDE-backed (likely VSC) agent: workspace discovery, minting, launching                             | `pkg/providers/cursoride`, `pkg/providers/copilotide`, and the shared `pkg/providers/vscode` package |
| Dealing with a SQLite session store                                                                   | `pkg/spi/sqlite.go` and `pkg/providers/cursoride`                                                    |
| The SpecStory CLI Software Factory's maintenance scripts                                              | `pkg/providers/claudecode/factory`, `pkg/providers/antigravitycli/factory`                           |

There is some known drift in some of the exemplars, which you must not copy: exec helpers that call `os.Exit` or return the raw process error instead of `spi.AgentExitError`; flag-style resume helpers that let an id pinned in the configured command win over the requested id; watcher contexts created in `init()`; inline `analytics.TrackEvent` calls and literal triple-backtick fences in the older providers; the Cursor CLI provider's polling watcher and its `run` that re-emits existing sessions.

### Learn the agent's on-disk format from the current release

Install the agent, run it, and read what it writes; the files on disk are the contract. Capture what you learn in `<AGENT>-FORMAT.md` (the agent's short name in capitals, for example `MUSE-FORMAT.md`), placed **inside your provider package** next to the code it documents, in the style of [MUSE-FORMAT.md](pkg/providers/musecode/MUSE-FORMAT.md) and [ANTIGRAVITY-FORMAT.md](pkg/providers/antigravitycli/ANTIGRAVITY-FORMAT.md):

- Store layout, record envelope, and the shape of every tool call and result you observed.
- The write lifecycle: which file is the durable record, which files are transient (checkpoints, rolling "latest" files, locks), when each is written and deleted, and whether a transient file is shared across concurrent sessions. A session that is still in flight is expected to be invisible until the agent commits it.
- How the agent records its version and the working directory.
- How native resume works and which file the agent actually reads on resume.
- The baseline agent version the provider targets, stated as the exact string the binary prints for `--version`.

Leave out any notes about versions below your baseline; a brand new provider has no backwards compatibility requirement.

### Enumerate the agent's real tools yourself

Run the agent directly (not through `specstory run`) in a scratch directory and ask it:

```text
Hello <agent>, tell me all the tools you have access to. Write all the tool names to the file ./tools.txt.

Please use each of your <number> tools one-by-one to show me how they work and how you use them.
```

If the agent has a tool-search tool, add to the first prompt: including any deferred tools you can load or discover through a tool search.

Keep `tools.txt`, a `versions.txt` with the agent's version banner and the CLI version, and the session. Two rules about that list:

- Where the agent declares its own tools (a stream init event in headless mode, an extension hook), that declaration is the inventory and the model's self-report is only a lower bound. Tools you see used in the session that the model did not list belong in `tools.txt` too.
- Strip any namespace prefix the model adds (`functions.`, `<agent>.`); session data records bare names, and those are what your provider matches.

The names in that file are the only tool names your provider may special-case. Renderers, classifier cases, argument-key aliases, and fallback branches must each trace to an observed record. A renderer for a tool that never fires is untestable code that reads as though it were verified behavior, and it will likely be deleted in review.

### Verify native resume with a spike before writing a serializer

Plant a fact in a session ("the magic passphrase is PURPLE-ELEPHANT-42"), resume the session with the agent's own resume command, and ask for the fact. Then move one of the agent's store files aside at a time and repeat, so you know which file the agent actually reads on resume. If the agent cannot resume from anything you can reconstruct, ship `spi.ErrReconstructionUnsupported` honestly rather than a serializer that produces files the agent never loads.

## What a complete provider contains

### Package and files

The architecture is one-directional: 

- `pkg/spi` defines the interfaces, the schema, and the shared helpers and imports no provider
- every provider imports `pkg/spi`
- `pkg/spi/factory/registry.go` imports every provider and is the only place that knows them all. 
- Nothing else in the CLI imports a provider package directly.

The package is `pkg/providers/<agent>`, for example `claudecode`, `codexcli`, `cursoride`, `musecode`). The typical file set is:

|                                      File                                      |                                          Purpose                                           |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------ |
| `provider.go`                                                                  | The `Provider` struct, `NewProvider()`, `Check`, `DetectAgent`, session listing and lookup |
| `agent_session.go`                                                             | Conversion from the native format to `schema.SessionData`                                  |
| `jsonl_parser.go` (or `json_parser.go`, `transcript_parser.go`, `database.go`) | Native format decoding                                                                     |
| `markdown_tools.go`                                                            | Per-tool `Summary` and `FormattedMarkdown` rendering                                       |
| `watcher.go`                                                                   | fsnotify watcher used by `run` and `watch`                                                 |
| `<agent>_exec.go`                                                              | Command line parsing, resume arguments, process launch, exit handling                      |
| `path_utils.go`                                                                | Native store discovery and working directory encoding                                      |
| `reconstruct.go`                                                               | `ReconstructSession`, `NativeSessionPath`, `SupportsReconstruction`                        |
| `*_test.go`                                                                    | Tests alongside each source file, plus `testdata/` fixtures captured from real sessions    |
| `factory/`                                                                     | Software factory scripts (see below)                                                       |

Do not create tons of small files by splitting helpers into small utility files (`text_utils.go`, `reader_utils.go`, and the like). Helpers live in the file whose concern they serve, matching where the other providers keep theirs.

Add `var _ spi.Provider = (*Provider)(nil)` so the compiler enforces the interface, and the same assertion for any optional interface you implement.

### Every SPI method, including the ones that are easy to miss

Implement every method on `spi.Provider` in `pkg/spi/provider.go` (twelve today; count them against the file). Beyond the obvious ones, review these carefully:

- `ListAgentChatSessions` returns lightweight metadata without a full parse.
- `ListAllAgentChatSessions` enumerates every session in the native store across all projects, reading the originating working directory from inside each session. This powers `specstory reindex`, `search`, and `resume`.
- `ReconstructSession`, `NativeSessionPath`, and `SupportsReconstruction` implement cross-agent resume into your agent. `SupportsReconstruction` is a pure constant answer and must agree with the other two. `NativeSessionPath` only resolves the path; the CLI creates the directory and writes the file.
- The `progress` callback on `GetAgentChatSessions` is invoked once per session file, including skipped and failed ones, so the progress bar reaches its total.
- The two optional interfaces in `pkg/spi/global.go`: implement `spi.PathSessionReader` (`GetAgentChatSessionByPath`) so reindex can open a session by its known path instead of a by-id walk, and `spi.ProgressEnumerator` (`ListAllAgentChatSessionsProgress`) so reindex can show live counts. A JSONL store implements both, using `spi.ScanSessionsInParallel` for the enumeration (it walks `*.jsonl` files only; other store kinds implement the enumeration themselves).

### Wiring outside the package

- `pkg/spi/factory/registry.go`: register under a short lowercase id (`muse`, `antigravity`, `droid`).
- `pkg/config/config.go`: a `<id>_cmd` entry in the default config template, a `ProvidersConfig` field, a `GetProviderCmd` case with its doc comment updated, and rows in the config tests. `specstory run <id>` must honor it; `specstory check <id> -c` honors the flag only. **All three parts are required and none of them fails loudly on its own** — TOML accepts a key with no struct field, and `GetProviderCmd` returns `""` for an unknown id — so a partial wiring ships a config key that silently does nothing. Two tests enforce it: `config.TestProvidersConfigIsFullyWired` checks each `ProvidersConfig` field reaches both the template and a `GetProviderCmd` case, and `cmd.TestEveryRegisteredProviderHasACommandOverride` checks every registered provider id resolves to one.
- `pkg/cmd/session_tui_browser.go`: propose an accent color in `colorForAgent`, the agent's brand color if it is legible on both light and dark terminals. The maintainer may replace it.
- `pkg/skills/agents.go`: a row when the agent supports agent skills (it has a project or global skills directory). Its `Name` is the public `npx skills` canonical id, not the provider id.
- `README.md` in this directory: the intro sentence, the Agent Support table row, the `[providers]` example block, the Configuration Options row, and the Debug Raw Mode provider list. Write a prose paragraph only if the provider behaves differently from wrapping a terminal process.
- `../README.md` at the monorepo root: the ASCII diagram line, the Installation table row (leave the Min Version to the maintainer), the lead-in sentence, and a `specstory run <id>` example.
- `changelog.md`: an entry under a placeholder heading such as `## Unreleased`. Use the fixed announcement sentence from the previous provider's entry, listing every previously released provider, followed by a sentence on resume in each direction. State a version floor as the version you tested; the maintainer replaces it with the release-day version and moves your entry under the release heading. Do not also add it under an existing version, and do not add an "Improvements" section for a provider that has never shipped.
- IDE providers only: the per-save line during `run` and the restart note after `resume` are printed by the CLI and keyed to the registry id in `main.go` and `pkg/cmd/resume.go`; those need a change too.

### Naming layers

Five identifiers exist and they are not the same string:

|                                         Identifier                                         |                                                    Convention                                                     |     Example      |
| ------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------- | ---------------- |
| Package name                                                                               | `<agent>`                                                                                                         | `antigravitycli` |
| Registry id (CLI argument, `<id>_cmd`, statistics, index, check-event `provider` property) | short lowercase agent name                                                                                        | `deepseek`       |
| `ProviderInfo.ID` in session data                                                          | the registry id, for every provider from Muse Code onward                                                         | `muse`           |
| `Name()` and `ProviderInfo.Name`                                                           | the product's own name and casing; also the `agent_provider` analytics property and the telemetry agent attribute | `DeepSeek TUI`   |
| Skills registry `Name` in `pkg/skills/agents.go`                                           | the public `npx skills` canonical id                                                                              | `claude-code`    |

Go identifiers follow the brand's casing (`DeepSeekCmd`, not `DeepseekCmd`).

## Rules that will otherwise get your submission changed

These are the changes reviewers make most often. Each one is a real review finding from a past provider.

### Reuse the shared helpers in pkg/spi

Every helper below replaced copies that had drifted apart across providers. Do not reimplement them.

- Use `schema.CurrentSchemaVersion` and the shared `schema.ContentTypeText` / `schema.ContentTypeThinking` constants when constructing `SessionData`, rather than repeating their string values. Native record fields and code-fence language labels follow their own formats.
- `spi.CodeFence` for every fenced block, sized past any backtick run in the content. Never write a literal triple backtick, and never backslash-escape backticks.
- `spi.ReadRecordLine` with `spi.MaxRecordLineSize` for every JSONL session file. It is the only correct way to cap a record: a cap applied after `bufio.Reader.ReadString` returns has already allocated the oversized record it exists to prevent. A capped `bufio.Scanner` is still right for sidecar and index files, where losing the remainder of the file is acceptable.
- `spi.CapRunes` for truncation. Never slice a string by bytes.
- `spi.LanguageFromPath`, `spi.RenderGenericJSON`, `spi.TodoSymbol`, `spi.FormatDiffBlock`, `spi.StringValue`, `spi.NormalizeToolName` for tool rendering.
- `spi.ClassifyCheckError` and the `spi.CheckErrorNotFound`, `spi.CheckErrorPermissionDenied`, `spi.CheckErrorUnknown` constants for `Check` failures. Empty `--version` output on a successful run is a success reported as `"unknown"`, not a failure (`spi.CheckErrorNoOutput` is a legacy shape).
- `analytics.CheckAttempt` populated once per `Check`, with the event emitted by `analytics.TrackCheckSuccess` or `analytics.TrackCheckFailure`. No inline `analytics.TrackEvent` calls in a provider.
- `spi.SplitCommandLine` for custom commands; `spi.EnsureResumeArgs` when the agent resumes via a subcommand.
- `spi.AgentExitError` to report a non-zero agent exit. Never call `os.Exit` inside a provider; it skips the final session save (the reason is in `pkg/spi/exit.go`).
- `spi.GetDebugDir` for debug output paths. Write only provider-specific raw files there; the CLI writes `session-data.json` itself.
- `spi.NormalizePath` and `spi.ExtractShellPathHints` for path hints; `spi.CanonicalizePathOrClean` for local path comparison; `spi.FileURIToPath` for any `file://` URI.
- `spi.GenerateFilenameFromUserMessage`, `spi.GenerateReadableName`, and `spi.ReadableTitleFromSessionData` for slugs, names, and titles. If the agent records its own title or summary for a session, prefer it for `Name` and fall back to the shared generator.
- `spi.PrepareTurns`, `spi.ResolveWorkspaceRoot`, `spi.ReconstructRole`, `spi.RFC3339Millis`, `spi.ResumedSessionTitle` in `ReconstructSession`.
- `spi.WatchWindowDays`, `spi.WatchWindowCutoff`, `spi.DateDirWithinWatchWindow` to bound watches on a store that grows without limit.
- `spi.DispatchSession` for asynchronous callback delivery, or a local `defer recover()` around a synchronous callback.
- SQLite stores: open every read handle as `file:<path>?mode=ro&` plus `spi.BusyTimeoutPragma`. The `file:` scheme is required; the driver ignores `mode=` on a bare path and opens read-write-create. Call `spi.EnsureWALMode` once at watcher startup, never on a read path.

Providers must never import `pkg/utils`, `pkg/session`, or `pkg/cloud` (the import graph cycles through the registry), nor `pkg/telemetry` (layering).

### Honest data over convenient data

- `CheckResult.Version` is the agent's version string or empty. Never a label.
- `ProviderInfo.Version` is the agent's version when the native data records it, otherwise the literal `"unknown"`. The model name is not a substitute; it belongs on each agent message's `Model` field, and it is the real model, never a placeholder such as an automatic-mode label.
- `WorkspaceRoot` is never empty: the workspace the agent stated, then the caller's project path, then the process working directory as a documented last resort.
- A session whose project cannot be determined from what the agent stated is `unknown`. Never infer a workspace from the paths that tools touched; one read of `~/.gitconfig` would attach the session to every project under the home directory. Session-to-project matching uses containment of stated paths, never a guessed common ancestor.
- `GetAgentChatSession` returns `nil, nil` for not found. Errors are for real failures.
- A by-id lookup on a global store must still check that the session belongs to the requested project, or one project's conversation will be written into another's history.
- On an IDE store the same session can appear under several matching workspace entries, and an empty copy can come first; mark an id as seen only after the content check, and keep an empty copy only as a fallback.
- `AgentChatSession.RawData` carries the native transcript on every session you return, whether or not debug output is enabled; SpecStory Cloud stores it. Build it from the records you already parsed, never by reading the file a second time: a session being written grows between the two reads, so the raw transcript would describe turns the converted session never saw, and during `run` that happens on nearly every save.
- `Usage` carries only the token fields the native data distinguishes; a session total is not an input count, so leave it nil with a why-comment rather than guess. A token kind not already in `schema.Usage` is a shared change to ask for.
- Timestamps come from the record, never from `time.Now()` in a parse or render path, are consistent across every code path, and are RFC 3339 parseable.

### Comments explain why, and stay true

- Every non-obvious line carries a "why" comment. Restating the code is noise.
- Comments are self-contained. Do not reference other providers ("Claude Code uses X here"), plan documents, decision numbers, or the history of how the code came to be.
- Magic values carry their provenance ("as observed in sessions written by version 3.12").
- A comment that contradicts its code will be treated as a bug.
- Every exported identifier, in particular each SPI method, carries a Go doc comment consistent with the siblings.

### Parsing

- Read the primary session file with `spi.ReadRecordLine(reader, spi.MaxRecordLineSize)`. It bounds allocation as the record is read and reports an oversized record instead of returning it, so one bad record degrades to one bad record rather than an aborted file. Log the skip at Warn with the file and line and carry on. Sidecar and index files may use a `bufio.Scanner` capped at `spi.MaxRecordLineSize`, mapping `ErrTooLong` to a clear error.
- When a record holds `json.RawMessage`, unmarshal from a copy of the line (`scanner.Text()`, never `scanner.Bytes()`); the scanner reuses its buffer.
- A record that fails to parse is skipped, never silently: log at Warn with the file and line ("Skipping corrupted JSONL line" is the established message shape).
- Order records by the agent's own sequence field, not file order; agents flush asynchronous results ahead of the call that owns them. Identify result records by excluding the known structural types, not by an allow-list, so a new result type degrades to a generic result instead of vanishing. Pair results to pending calls by tool type or id, with first-in-first-out only as the fallback for several in-flight calls, and ship a scrambled-order regression test.
- The parser's kind switch enumerates every record kind observed in real data, rendering it or naming it as known-nothing-to-render with the reason, so the default "unknown kind" log fires only for genuinely new kinds.
- Scan and watch only the durable session file; never read a scratch file the agent rewrites in place.

### The watcher

- fsnotify is the change-detection mechanism, never a polling ticker. A bounded reconcile tick (as in the Claude Code watcher) is expected on top, to catch writes to files that were not yet watched and to prune idle watches.
- At startup, record what already exists and emit nothing. Emit only activity that happens after the watcher started. If a directory appears after the watch was armed, walk it once and adopt what landed inside, because those writes happened unobserved. Never re-publish history.
- Never let the watcher silently disable itself. If the agent's directory does not exist yet, watch the nearest existing ancestor and wait.
- When the store is keyed by project, watch only this project's subtree, never every project's directory. Walks and watches stop at the session directory; a session's own subdirectories (tool outputs, subagent logs) are neither watched nor walked.
- Watch every path the agent writes session content to, including asynchronous sidecar files. Change detection uses an on-disk signature (size and modification time) of every file the session spans, not a parsed field that a title-only rename would not touch.
- A debounced burst is re-processed once after the burst ends; the burst's last write is often the completed response. A safety-net poll catches what fsnotify missed.
- Deliver callbacks in order (one worker) or synchronously; contain panics in the consumer callback; close the race between `Stop` and in-flight work under one lock. Track goroutines with `wg.Go`, never `wg.Add(1)` paired with `defer wg.Done()`.
- Make the watcher restartable: create the context per start, not in `init()`.
- File descriptors stay flat over a months-long watch: bound the window with `spi.WatchWindow*`, prune watches at rollover, and re-watch a dormant file when its modification time moves.
- The command layer fingerprints every delivered session and suppresses unchanged parses; add no content-equality guard of your own.

### Run and exec

- Honor the custom command from `-c` and `<id>_cmd`; parse it with `spi.SplitCommandLine`.
- When a resume id is requested it wins over any id pinned in the configured command. For a flag-style resume (`--resume <id>`) there is no shared helper yet; write one that replaces a pinned id, inserts after a bare flag even when the next token is another flag, repairs `--flag=`, and never appends to the caller's slice. The DeepSeek TUI helper is the closest model for the bare-flag and `--flag=` handling, but it lets a pinned id win, which is wrong.
- Stop the watcher and join in-flight saves before returning the agent's exit status.
- For an IDE provider, `run` opens the project directory with the IDE's own CLI, canonicalizing the path first, prints install guidance if that CLI is missing, waits for the IDE to create the workspace, and then watches until Ctrl-C. No silent fallback to opening the app on its home screen.

### Resume into your agent

- The reconstructed native file must be one the agent considers clean and complete: match the shape the agent itself writes field for field, including any end-of-session record, so the agent shows no crash or unclean-stop warning.
- Carry a provenance back-link (`specstorySourceSessionId`) in the native metadata.
- Keep user-identifying data (account labels, auth metadata) out of reconstructed files.
- Your parser must exclude your agent's own slash-command and system scaffolding from user turns; the shared resume filter strips only Claude Code's markers.
- If the agent cannot resume from reconstructed data, return `spi.ErrReconstructionUnsupported`, return `false` from `SupportsReconstruction`, and say so in the changelog. Such a provider is offered as a resume target only for its own local sessions.

### Rendering

- Tool inputs render as labeled lines (`Path:`, `Command:`), not JSON blocks. JSON is the last-resort generic fallback only, pretty-printed in a `json` fence. Maps and slices are JSON-marshaled, never formatted with `%v`.
- Edits render as `diff` fences. Files render in a fence tagged by extension. Shell output renders in a `text` fence with control bytes sanitized. Web search results render as a linked list.
- A JSON tool result (an answer envelope, a diff array, a subagent record) is parsed into the idiomatic markdown for its kind and folded into the call block; the result renderer then returns empty so the raw JSON does not also appear.
- A failed call renders the agent's error text; the error branch takes priority over the success formatter for every tool.
- Thinking content is captured and rendered once, in place. Narration and tool blocks appear in the order the agent recorded them; an invocation the agent re-serializes on every state update renders once.
- Completeness beats brevity: the full system prompt, the full file content. Results are capped with a visible marker; inputs are not.
- Never nest `<details>` blocks; the wrapper is owned by `pkg/session`.
- Tool type reflects the target: workspace documents are `read`, `write`, `search`; shell is `shell`; todo lists are `task`; agent-state stores (memory, goals, cron, skills, subagents) are `generic`; a URL fetch is `read`. An unenumerated tool must still render, typed `unknown`, never dropped. Every bespoke renderer keeps a generic degradation path so no input is ever lost.
- Strip framework boilerplate the agent injects for the model ("Created At:" headers, "proactively run terminal commands" instructions) but never alter real content; scope any whitespace cleanup to the result type where the noise was observed.
- A malformed element (a todo item that is not an object) is skipped, never rendered as a placeholder row.
- Output is deterministic: never iterate a map into output; sort with one comparator. A second sync with no agent activity must change no bytes.

### Check and analytics

- `Check` resolves the binary with `exec.LookPath`, probes `--version` capturing stdout and stderr, classifies failures with `spi.ClassifyCheckError`, reports `"unknown"` when the binary prints nothing, and emits exactly one event per outcome through `analytics.TrackCheckSuccess` or `analytics.TrackCheckFailure`.
- The failure message names the command actually run, including a custom one.
- Providers emit no other analytics. Hidden flags get none.
- IDE providers probe the store, not a binary, and emit one check event per outcome in the shape the existing IDE providers use.

### Logging

- Call `log/slog` directly; no package-local logger wrapper or adapter. Use `log.UserMessage` and `log.UserWarn` for the guidance `DetectAgent` prints when nothing is found. The only other permitted terminal output is an IDE provider's "waiting for the IDE to open this project" line. No `fmt.Print` anywhere else in a provider.
- `Check`, `ExecAgentAndWatch`, and `WatchAgent` log at Info on start, each transition, and exit. Missing lifecycle logging is a defect, because `--log` is how problems get diagnosed.
- Routine, repeating states (file events, waiting for the agent) log at Debug. Warn is for data loss the run survives. Error is for a unit of work that failed, logged once.
- Structured keys are camelCase (`error`, `path`, `sessionId`, `projectPath`, `command`, `exitCode`); messages are prefixed with the method name (`Check:`, `WatchAgent:`) for SPI entry points.
- Never swallow an error with `_ :=` when the failure would otherwise be silent to the user.

### Cross-platform

The CLI runs on macOS, Linux (including WSL), and native Windows, and CI runs the full test suite on Windows.

- Paths from session data are handled by shape (leading `/` or a drive letter), never with `filepath.IsAbs`, `filepath.Join`, `filepath.Abs`, `filepath.Rel`, or `filepath.Clean`, because a session written on one OS is rendered on another. An absolute-path check accepts both shapes; a `/`-only check rejects every native Windows path.
- Local paths use `os.UserHomeDir()` and `filepath.Join`. Your cwd-to-store-directory encoder reproduces the agent's own algorithm, including its symlink resolution; the local project path is canonicalized once at the boundary, and a recorded or remote path is never case-folded or canonicalized.
- IDE-style providers branch on `runtime.GOOS` for `Library/Application Support`, `.config`, and `%APPDATA%`, check `spi.IsWSL()` in the Linux branch and read the Windows side with `spi.FindWindowsAppDataPathFromWSL` first, and honor `--user-data-dir`. Any URI written to an IDE store goes through `vscode.WorkspaceURIMap` or `vscode.PathToFileURI`.
- A raw JSONL scan for a path also matches the backslash-escaped form; a Windows cwd inside JSON is written as `C:\\Users`.
- No `pgrep`, `sh -c`, `/bin/...`, or `USER`. Username comes from `os/user.Current()`; process detection on Windows uses `tasklist`, checking that the image name echoes back because it exits 0 even when nothing matched.
- Before opening the PR run `GOOS=windows GOARCH=amd64 go build ./...` and `GOOS=windows GOARCH=amd64 go vet ./...` (the prefix goes on both commands).

### Tests

- Test complicated logic and combinatorial scenarios, not constants. A test that asserts `Name()` returns its own literal will be deleted.
- Table-driven with `t.Run(tt.name, ...)` where there is a matrix of cases; a single integration test may stay standalone.
- Fixtures are raw shapes captured from real sessions, so the tests encode what the agent actually writes.
- A regression test must fail when the fix is backed out. Prove it before you commit it. When a fix adds a guard, the test also proves the guarded path still works for the legitimate case.
- An exhaustive test that walks the agent's real tool inventory and asserts the expected type per name is not tautological; it guards against omission.
- Each side of the watcher startup policy (nothing emitted for pre-existing sessions; adoption of a late-arriving directory) gets its own test.
- Tests for a shared helper live in `pkg/spi` next to the helper, not in the provider.
- Test hygiene: `testutil.SetHome` (never `t.Setenv("HOME", ...)` alone; it does not fake `%APPDATA%`, so IDE-style providers point at a fake install through their user-data-dir override), `testutil.JSONString` when a real path goes into a JSON fixture, `testutil.EqualPaths` after URI round-trips, `t.TempDir()` (a symlink on macOS and an 8.3 name on Windows, so compare canonical to canonical), `t.Chdir`, never a `"file://" + path` splice, deadline polling rather than fixed sleeps for file events, and expected values built with `filepath.Join` or `filepath.FromSlash`. Windows-shaped table rows run Windows behavior on macOS.
- Any test that enables debug output first calls `spi.SetDebugBaseDir(t.TempDir())` so nothing is written into the package directory.
- No injectable interface or seam in production code whose only consumer is a test. A package-level root-path variable that lets a test point the store at a temp directory is the accepted form.

### Dependencies and new files

- No new dependencies without asking first, with the reason. Prefer the standard library.
- No new files beyond the canonical set without asking first. Test-only helpers belong under `internal/`.
- No planning documents in `docs/`. Keep `<AGENT>-FORMAT.md` as a description of what is, not a plan.
- `.specstory/history` is committed in this repository; do not add `.specstory/` to any `.gitignore`, and do not add ignore entries for directories that do not exist.

## How your provider will be tested

Reviewers exercise every command against the real agent, at the version shipping that week, from a scratch project, invoking the freshly built `./specstory`. Test these yourself before submitting and say in the PR which you ran and on which operating systems.

|                                         Command                                         |                                                                                                                                  What must be true                                                                                                                                  |
| --------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `./specstory check <id>`                                                                | Version and location reported; `-c` with a bad path fails with guidance naming that path; the success hint advertises a command that works                                                                                                                                          |
| `./specstory sync`, `sync <id>`, `sync -s <session-id>`, `sync -s <session-id> --print` | One markdown file per non-empty session; a second sync with no agent activity reports every session as up to date and changes no bytes; from a directory with no sessions, `sync <id>` prints your provider's own guidance and exits 0                                              |
| `./specstory sync --debug-raw --log --debug`                                            | `.specstory/debug/<session-id>/` holds pretty-printed per-record `N.json` files (JSONL agents) or one raw file, plus `session-data.json`; `debug.log` reconstructs every decision and contains no `schema validation:` warnings                                                     |
| `./specstory run <id>`                                                                  | The agent takes the terminal with no interleaved output; markdown appears after the first turn and grows; the CLI exits with the agent's status and the last turn is saved even on a non-zero exit; this also works from a project the agent has never seen, and with `--debug-raw` |
| `./specstory watch <id>`                                                                | Existing sessions are left alone; the first new update prints and is saved; works when started in a project the agent has never seen                                                                                                                                                |
| `./specstory resume <id>` (same agent)                                                  | The agent opens with prior turns visible and answers a question about them; the same native file grows; the same markdown file updates                                                                                                                                              |
| `./specstory resume claude` from your agent's session                                   | The reconstructed session opens in Claude Code with the migration note first and every user prompt present; no command or system scaffolding is replayed. Use a source session with plain turns, thinking, at least one tool call, and a slash command                              |
| `./specstory resume <id>` from a Claude Code session                                    | Your agent opens the reconstructed session with the prior conversation, answers a question about it, shows no warning about the session file, and appends to that same file rather than starting a fresh session                                                                    |
| `./specstory list <id>`, `search`, `reindex`                                            | Every synced session is listed with the same slug as its filename; reindex counts your sessions across all projects and attributes them to the right project                                                                                                                        |

Also run `sync`, `list`, and `watch` from the scratch project reached through a symlink and from a path containing a space and an underscore; each must find the same sessions as the canonical path.

Then run the tool enumeration session described above through `./specstory sync --log --debug --debug-raw` and audit the markdown block by block against the raw data. Grade every tool-use block as formatted (all important data present and pleasantly presented), partial (formatted but missing important elements), raw (raw JSON or unformatted output), or missing, in a table with the tool name, markdown line, grade, data file, data line, and a comment. Every tool the agent has should grade as formatted. Attach `tools.txt`, `versions.txt`, the synced history file, and the audit table to the PR.

## Software factory affordances

The SpecStory provider factory watches each agent's release channel and audits new versions. A provider enrolls by shipping executable bash scripts under `pkg/providers/<agent><kind>/factory/` (the package directory, not the registry id). Model them on `pkg/providers/claudecode/factory/` and `pkg/providers/antigravitycli/factory/`, including the header comment that records the channel decision, what was rejected and why, and the contract paragraph. These scripts run unattended in CI on every merge, so they are read as untrusted code: nothing is piped to a shell except the vendor's own pinned installer, and nothing reads outside the named credential.

- `latest-version` (required): prints the current released version to stdout and exits 0. A non-zero exit means sensor failure, never "no change"; an empty extraction becomes a non-zero exit with a diagnosis on stderr. Output is opaque, byte-compared day to day, so it carries no build counters, shas, or dates unless they are part of the string the binary prints for `--version`, and the header states which dist-tag or release train is tracked. Accepted channels are an npm `latest` dist-tag, a GitHub `releases/latest` redirect, or the vendor's installer script read as a manifest; never the GitHub API or any tool needing auth. Every curl carries `--connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2`. Runs with no credentials.
- `install <version>` (required when the agent has a headless mode): takes the first line of `latest-version`'s output verbatim, installs exactly that version under `$HOME` at `$HOME/.local/bin/<binary>`, disables the agent's self-update where it has one, prints the version read back from the binary as the only stdout line (installer chatter goes to stderr), and exits non-zero on any mismatch.
- `list-tools` (required when the agent has a headless mode): runs `$HOME/.local/bin/<binary>` explicitly with self-update disabled and prints the tool inventory one name per line, spelled as the agent records tool names in its session data. Prefer the harness's own declaration (a stream init event, an extension hook) over asking the model; if a model run is unavoidable it must authenticate through one environment variable named in the header, with a fail-fast check when it is unset, and any settings file the agent needs is seeded under `$HOME` inside the script. Passes the agent's isolation flags so user configuration cannot leak in, is bounded by a timeout or the harness's own turn limit, leaves its diagnostics in the working directory, and exits non-zero rather than printing an empty or partial list.

Test them the way the factory runs them, under an isolated home directory (`timeout` is GNU coreutils; on macOS install it with Homebrew):

```zsh
env -i HOME="$(mktemp -d)" PATH="$PATH" timeout 120 bash pkg/providers/<package>/factory/latest-version
H=$(mktemp -d); V=$(env -i HOME="$(mktemp -d)" PATH="$PATH" bash pkg/providers/<package>/factory/latest-version | head -1)
env -i HOME="$H" PATH="$PATH" bash pkg/providers/<package>/factory/install "$V"
for n in 1 2; do ( cd "$(mktemp -d)" && env -i HOME="$H" PATH="$PATH" <VAR>="${<VAR>:-}" bash pkg/providers/<package>/factory/list-tools > "/tmp/reading-$n" ); done
diff <(sort -u /tmp/reading-1) <(sort -u /tmp/reading-2) && echo "readings agree"
```

Also run each script's negative case (an unreachable channel, a bogus version, the credential unset); every one must exit non-zero rather than print an empty reading. IDE providers ship `latest-version` only and say why in its header; a terminal agent with no credential-free headless mode may do the same, with the reason recorded.

## Submitting the pull request

- Open the pull request against `dev`, never `main`. If you are contributing from a fork, enable "Allow edits by maintainers".
- Merge `dev` into your branch first and resolve conflicts, so the diff contains only your provider.
- In the description state: the agent version you built against and tested on, the operating systems you tested on, which rows of the test table you ran, the attached enumeration artifacts and audit, any SPI or shared-code changes and why, and known limitations (for example, resume into the agent unsupported and why).
- Expect the GitHub Copilot reviewer to comment. Its findings are triaged, not obeyed; fix the real ones and comment on and resolve the rest.
- Expect the maintainer to review by pushing commits directly onto your branch rather than requesting changes, and to cut the release.

## Self-review checklist

- [ ] Package named `<agent><kind>`; canonical files only; `var _ spi.Provider` assertion present
- [ ] Every SPI method implemented, including `ListAllAgentChatSessions` and the three reconstruction methods, each with a test
- [ ] Registry, config (`<id>_cmd` in template, struct, switch, doc comment, and test rows — the two wiring tests must pass), TUI color, both READMEs, changelog
- [ ] `<AGENT>-FORMAT.md`, in the provider package, written from the current release with the write lifecycle and baseline version, no legacy notes
- [ ] `tools.txt` from the agent itself, prefixes stripped, declaration preferred over self-report; renderers and type tables list exactly those names; an inventory sweep test exists
- [ ] No literal fences, no byte slicing, no local copies of `pkg/spi` helpers, no inline `analytics.TrackEvent`
- [ ] No `os.Exit`, no polling watcher, no emit at startup, panic recovery around the callback, `wg.Go` only, context created per start
- [ ] Session files read through `spi.ReadRecordLine`; oversized and malformed records skipped with a Warn, never failing the file; results paired by the agent's sequence index
- [ ] Every comment says why; none reference other providers or history; magic values carry provenance; exported identifiers documented
- [ ] `Check` lifecycle logging present; no `fmt.Print` outside detection help; `RawData` set on every session and built from the parsed records, not a second read
- [ ] Tests table-driven where useful, no tautological tests, fixtures from real data, Windows-safe helpers used, `spi.SetDebugBaseDir` in debug tests
- [ ] `gofmt -w .`, `golangci-lint run` (whole project), `go test ./...`, `GOOS=windows GOARCH=amd64 go build ./...` and `GOOS=windows GOARCH=amd64 go vet ./...` all clean
- [ ] Every command in the test table exercised against the real agent; resume verified in both directions; symlinked and special-character project paths tried
- [ ] `factory/latest-version` present and tested under an isolated home with its negative case; `install` and `list-tools` present if the agent runs headless
