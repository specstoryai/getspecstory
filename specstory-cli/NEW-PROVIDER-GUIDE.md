# New Provider Guide

This guide is for anyone, human or agent, adding support for a new coding agent to the SpecStory CLI. It tells you what a complete provider contains, the standards a submission is held to, and how it will be exercised before release, so your pull request lands with as few needed changes as possible.

The service provider interface (SPI) is documented in [pkg/spi/provider.go](pkg/spi/provider.go). Read the doc comments on `spi.Provider` and the optional capabilities below it first. A provider must implement `spi.Provider`; it may also implement either or both optional interfaces.

The unified session data format is [pkg/spi/schema/types.go](pkg/spi/schema/types.go), explained in [docs/SPI-SESSION-DATA-SCHEMA.md](docs/SPI-SESSION-DATA-SCHEMA.md).

Once you've followed this guide and developed a provider, you can submit it for inclusion in the SpecStory CLI. Contributions of providers are welcomed! This guide defines the submission requirements. [docs/NEW-PROVIDER-REVIEW.md](docs/NEW-PROVIDER-REVIEW.md) supplements it with guidance for reviewers on evaluating evidence, prioritizing findings, and reporting readiness.

## Before you write any provider code

### Understand a provider's responsibilities and how a provider works

A provider is the part of the SpecStory CLI that knows how to work with one specific coding agent. Each agent stores conversations (sessions) in its own location and format. The provider finds those sessions and translates them into SpecStory's shared session format, preserving the conversation, tool calls and results, and available metadata.

A provider has four main responsibilities:

- **Find the agent and its sessions.** Check the installation, discover sessions, and identify which project each session belongs to.
- **Read and translate sessions.** Parse the agent's stored data, retain the raw transcript, and format its tool activity for readable output.
- **Follow an active session.** Launch the agent for `run`, or observe it for `watch`, and report new or updated sessions as the agent writes them. Finish delivering updates before shutting down.
- **Support resuming conversations.** Launch the agent with a selected local session and, where supported, convert SpecStory's shared format back into a native session the agent can resume.

The provider hands session data back to the CLI through the provider SPI. The CLI, not the providers, handles writing Markdown history, redaction, indexing, and SpecStory Cloud sync. For example, during `specstory run`, the specific coding agent writes its session, the provider detects and reads the session change, and the CLI renders and saves the updated markdown file.

### Use exemplar providers as a starting point

Every provider is judged by behavioral parity with the established siblings. Choose the closest one as a starting point for file organization and method shapes, adapting the layout to your agent's needs. No single provider is reference-grade for everything, so take each concern from the provider named for it:

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

### Learn the agent's on-disk session format

Install the agent, run it, and read what it writes as its sessions to disk (or to a database); the stored sessions are the contract. Capture what you learn in `<AGENT>-FORMAT.md` (the agent's short name in capitals, for example `MUSE-CODE-FORMAT.md`), placed **inside your provider package** next to the code it documents, in the style of [MUSE-CODE-FORMAT.md](pkg/providers/musecode/MUSE-CODE-FORMAT.md) and [ANTIGRAVITY-FORMAT.md](pkg/providers/antigravitycli/ANTIGRAVITY-FORMAT.md):

- Store layout, record envelope, and the shape of every tool call and result you observed.
- The write lifecycle: which file is the durable record, which files are transient (checkpoints, rolling "latest" files, locks), when each is written and deleted, and whether a transient file is shared across concurrent sessions. A session that is still in flight is expected to be invisible until the agent commits it.
- How the agent records its version and the working directory.
- How native resume works and which file the agent actually reads on resume.
- The baseline agent version the provider targets, stated as the exact string the binary prints for `--version`.

Leave out any notes about versions below your baseline; a brand new provider has no backwards compatibility requirement.

### Enumerate the agent's real tools yourself

Build `tools.txt` from the harness's own tool declaration where available, such as startup output, a stream init event, or an extension hook. Prefer that inventory over asking the model or inferring the list from tools used in a session. Record the agent version, operating system, relevant configuration, and enabled extensions alongside the inventory so its scope is clear.

If no declaration is available, run the agent directly (not through `specstory run`) in a scratch directory and ask it:

```text
Hello <agent>, tell me all the tools you have access to. Write all the tool names to the file ./tools.txt.
```

Then run the agent directly in the scratch directory and prompt it to exercise the tools available in that environment:

```text
Please use each of the tools in ./tools.txt that is available in this environment, one-by-one, to show me how they work. Identify any tools you cannot exercise and explain why.
```

Some agents discover and load additional tools after startup. Include those in the inventory where the harness exposes them; when using the model fallback, also ask it to include tools it can discover or load through tool search.

Keep `tools.txt` and a `versions.txt` containing the agent's version banner and the SpecStory CLI version in a scratch directory. After the session ends, save a copy of its native session files for that session there too, leaving the originals in the agent's store so `specstory sync` can read them. These artifacts document what the agent actually did: use them to build test fixtures, verify tool rendering, and prepare the audit and PR attachments described in [How your provider will be tested](#how-your-provider-will-be-tested).

Two rules about the tool list:

- Model answers and observed sessions may omit tools. Check them against the harness declaration where available, and add any observed tools missing from the inventory.
- Strip any namespace prefix the model adds (`functions.`, `<agent>.`); session data records bare names, and those are what your provider matches.

The tool names in that file are the only tool renderings your provider should special-case. Renderers, classifier cases, argument-key aliases, and fallback branches must each trace to an observed tool record. A renderer for a tool that never fires is untestable code that reads as though it were verified behavior, and it may be deleted in review.

Exercise every tool reasonably available in the tested environment. Platform-specific tools, disabled optional extensions, and tools requiring unavailable external accounts may remain unexercised with a documented reason. Keep them in the inventory and distinguish declared, enabled, and exercised tools in the audit. Do not invent bespoke renderers for unobserved payloads; retain generic rendering. Where an attempted call produces an error, capture it and verify failure rendering, without claiming that this verifies the success path.

### Find out what the agent needs to resume a session

Cross-agent resume works by flattening the conversation into ordered user/agent text turns, then writing those turns in the target agent's native session format. Everything the agent said, thought, or did becomes agent text, including rendered tool activity. This shared representation avoids having to translate one agent's tool protocol or thinking blocks into another's. See [the reconstruction model](docs/SESSION-PORTABILITY.md#reconstruction-model).

Before writing the conversion code, find out which files and native structure the target needs to load and continue that flattened conversation. The goal is a valid native session carrying the shared text transcript, not recreation of every original native field.

Use a disposable session in your scratch project:

1. Tell the agent "the magic passphrase is PURPLE-ELEPHANT-42", then exit the agent.
2. Reopen that session using the agent's own resume command and ask "What is the magic passphrase?" Check that it recalls the fact without you supplying it again.
3. Back up the session's stored files. With the agent stopped, temporarily move one file aside, then repeat the resume test. Restore the backup before testing the next file. This helps identify which files are needed to recover the conversation.
4. Build a minimal native session containing plain user/agent text turns in the required structure, then repeat the recall test and continue the conversation. Verify that the agent loads it cleanly and appends to that session. Record required fields, lifecycle markers, and any necessary compatibility defaults in `<AGENT>-FORMAT.md`.

Your conversion code must produce the native structure needed to resume the flattened conversation. Missing source tool structures, thinking signatures, or historical model/usage metadata are expected consequences of flattening, not reasons to declare reconstruction unsupported. If you cannot produce a native session the target can resume using this approach, return `spi.ErrReconstructionUnsupported` from `ReconstructSession` and `false` from `SupportsReconstruction`, and document that resuming other agents' conversations in this agent is unsupported.

## What a complete provider contains

### Package and files

The architecture is one-directional: 

- `pkg/spi` defines the interfaces, the schema, and the shared helpers and imports no provider
- every provider imports `pkg/spi`
- `pkg/spi/factory/registry.go` imports every provider and is the only place that knows them all. 
- Nothing else in the CLI imports a provider package directly.

The package is `pkg/providers/<agent>`: the product's own name, lowercased, with the spaces removed. That is the whole rule for ten of the twelve, including `cursoride`, because Code, CLI, TUI and IDE are part of what those products call themselves. The IDE-backed providers end in `ide`, which Cursor IDE's own name already supplies and VS Code Copilot does not, so it becomes `copilotide`. Pi is `piagent`, the one place a bare product name was too slight to stand on its own. The following file layout is recommended, not mandatory; add, combine, or split files where that makes the provider easier to understand:

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
| `*_test.go`                                                                    | Tests alongside each source file                                                          |
| `testdata/`                                                                    | Inputs consumed by automated tests and expected outputs those tests actually compare      |
| `examples/`                                                                    | Captured examples, rendered histories, QA results and other human-review evidence          |
| `factory/`                                                                     | Software factory scripts (see below)                                                       |

Keep related logic together and avoid unnecessary file sprawl. Small helpers usually belong in the file whose concern they serve; a separate file is reasonable when it groups a distinct concern, such as debug output. File organization may differ from this example while still meeting the provider's behavioral and architectural requirements.

Add `var _ spi.Provider = (*Provider)(nil)` so the compiler enforces the interface, and the same assertion for any optional interface you implement.

### Separate test fixtures from examples

Reserve `testdata/` for files that automated tests actually consume as inputs or compare as expected outputs. For each fixture or fixture set, identify the consuming test and the behavior it asserts. A file does not become test data merely because a test helper copies the directory containing it.

Put captured examples, manual QA results, version banners, tool audits, and generated JSON or Markdown that no test compares in `examples/`, following [Pi's examples](pkg/providers/piagent/examples/). Keep native captures in `testdata/` when parser or rendering tests consume them; link from the examples to those fixtures rather than duplicating them. A generated `session-data.json` or rendered history belongs in `testdata/` only if an automated test actually compares it as a golden file. Do not add a superficial test just to justify keeping a review artifact there.

This distinction is required even when choosing a different source-file layout. Before submitting, audit every file under `testdata/` for an actual test consumer and move unused review artifacts to `examples/`. Update documentation and audit links after moving them.

### Every SPI method, including the ones that are easy to miss

Implement every method on `spi.Provider` in `pkg/spi/provider.go` (count them against the most recent version of the file). Beyond the obvious ones, review these carefully:

- `ListAgentChatSessions` returns lightweight metadata without a full parse.
- `ListAllAgentChatSessions` enumerates every session in the native store across all projects, reading the originating working directory from inside each session. This powers `specstory reindex`, `search`, and `resume`.
- `ReconstructSession`, `NativeSessionPath`, and `SupportsReconstruction` implement cross-agent resume into your agent. `SupportsReconstruction` is a pure constant answer and must agree with the other two. `NativeSessionPath` only resolves the path; the CLI creates the directory and writes the file.
- The `progress` callback on `GetAgentChatSessions` is invoked once per session file, including skipped and failed ones, so the progress bar reaches its total.
- The optional capabilities are also defined in [pkg/spi/provider.go](pkg/spi/provider.go). `spi.PathSessionReader` (`GetAgentChatSessionByPath`) lets reindex open a session by its known path instead of searching by id. `spi.ProgressEnumerator` (`ListAllAgentChatSessionsProgress`) adds live scan counts. Implement either, both, or neither; reindex falls back to the required methods when they are absent. Both are recommended for JSONL stores, which can use `spi.ScanSessionsInParallel` for enumeration (it walks `*.jsonl` files only; other store kinds implement enumeration themselves).

### Wiring outside the package

- `pkg/spi/factory/registry.go`: register under a short lowercase id (`muse`, `antigravity`, `droid`).
- `pkg/config/config.go`: a `<id>_cmd` entry in the default config template, a `ProvidersConfig` field, a `GetProviderCmd` case with its doc comment updated, and rows in the config tests. `specstory run <id>` must honor it; `specstory check <id> -c` honors the flag only. **All three parts are required and none of them fails loudly on its own** — TOML accepts a key with no struct field, and `GetProviderCmd` returns `""` for an unknown id — so a partial wiring ships a config key that silently does nothing. Two tests enforce it: `config.TestProvidersConfigIsFullyWired` checks each `ProvidersConfig` field reaches both the template and a `GetProviderCmd` case, and `cmd.TestEveryRegisteredProviderHasACommandOverride` checks every registered provider id resolves to one.
- `pkg/cmd/session_tui_browser.go`: add an accent color in `colorForAgent` as a hex code (`#RRGGBB`), based on a color associated with the agent's own brand. Check it on both dark and light terminal backgrounds and adjust the shade if needed for legibility. The maintainer may replace it.
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
- Compare a sentinel error with `errors.Is` and match an error type with `errors.As`. Never `err == io.EOF` or `err.(*exec.ExitError)`: a wrapped error fails both, and wrapping gets added later by someone who has no reason to look for a bare comparison.
- `spi.LanguageFromPath`, `spi.RenderGenericJSON`, `spi.TodoSymbol`, `spi.FormatDiffBlock`, `spi.StringValue`, `spi.NormalizeToolName` for tool rendering.
- `spi.LookPathForCheck` for binary lookup, `spi.ClassifyCheckError` for lookup/storage errors, and `spi.ClassifyCheckExecutionError` for probe failures after the binary was found. Populate `CheckResult.ErrorType` with the same `spi.CheckError*` value sent to analytics; leave it empty on success. Empty `--version` output on a successful run is a success reported as `"unknown"`, not a failure (`spi.CheckErrorNoOutput` is a legacy shape).
- `analytics.CheckAttempt` populated once per `Check`, with the event emitted by `analytics.TrackCheckSuccess` or `analytics.TrackCheckFailure`. No inline `analytics.TrackEvent` calls in a provider.
- `spi.SplitCommandLine` for custom commands; `spi.EnsureResumeArgs` when the agent resumes via a subcommand, or `spi.EnsureResumeFlagArgs` when it uses a flag.
- `spi.AgentExitError` to report a non-zero agent exit. Never call `os.Exit` inside a provider; it skips the final session save (the reason is in `pkg/spi/exit.go`).
- `spi.GetDebugDir` for debug output paths. See [Native debug output](#native-debug-output) for the provider's export responsibilities; the CLI writes `session-data.json` itself.
- `spi.NormalizePath` and `spi.ExtractShellPathHints` for path hints; `spi.CanonicalizePathOrClean` for local path comparison; `spi.FileURIToPath` for any `file://` URI.
- `spi.GenerateFilenameFromUserMessage`, `spi.GenerateReadableName`, and `spi.ReadableTitleFromSessionData` for slugs, names, and titles. If the agent records its own title or summary for a session, prefer it for `Name` and fall back to the shared generator.
- `spi.PrepareTurns`, `spi.ResolveWorkspaceRoot`, `spi.ReconstructRole`, `spi.RFC3339Millis`, `spi.ResumedSessionTitle` in `ReconstructSession`.
- `spi.NewFSWatcher` to create every fsnotify watcher, in production code and tests alike. Never call `fsnotify.NewWatcher` or `fsnotify.NewBufferedWatcher` directly; see [The watcher](#the-watcher) for why.
- `spi.WatchWindowDays`, `spi.WatchWindowCutoff`, `spi.DateDirWithinWatchWindow` when watched paths can grow without limit and the store layout supports a rolling date window. A fixed set of directory watches does not need a rolling window merely because the number of session files grows.
- `spi.DeliverSession` for synchronous callback delivery with panic recovery. If using `spi.DispatchSession` for asynchronous delivery, track and join every callback before shutdown.
- SQLite stores: open every read handle as `file:<path>?mode=ro&` plus `spi.BusyTimeoutPragma`. The `file:` scheme is required; the driver ignores `mode=` on a bare path and opens read-write-create. Call `spi.EnsureWALMode` once at watcher startup, never on a read path.

Providers must never import `pkg/utils`, `pkg/session`, or `pkg/cloud` (the import graph cycles through the registry), nor `pkg/telemetry` (layering).

### Honest data over convenient data

- `CheckResult.Version` is the agent's version string or empty. Never a label.
- `ProviderInfo.Version` is the agent's version when the native data records it, otherwise the literal `"unknown"`. The model name is not a substitute; it belongs on each agent message's `Model` field, and it is the real model, never a placeholder such as an automatic-mode label.
- `SessionData.WorkspaceRoot` is never empty: the workspace the agent stated, then the caller's project path, then the process working directory as a documented last resort. These fallbacks support path normalization and rendering; they are not evidence that the session belongs to the caller's project.
- `GlobalSessionRef.OriginCwd` records the session's originating working directory. Leave it empty when the origin cannot be determined; do not fill it from a rendering fallback. Global enumeration still returns the session, and the CLI assigns the `"unknown"` project ID. The literal `"unknown"` belongs in neither `OriginCwd` nor `WorkspaceRoot`.
- Never infer project membership from the paths that tools touched; one read of `~/.gitconfig` would attach the session to every project under the home directory. Session-to-project matching uses containment of stated paths, never a rendering fallback or a guessed common ancestor.
- `GetAgentChatSession` returns `nil, nil` for not found. Errors are for real failures.
- A by-id lookup on a global store must still check that the session belongs to the requested project, or one project's conversation will be written into another's history.
- On an IDE store the same session can appear under several matching workspace entries, and an empty copy can come first; mark an id as seen only after the content check, and keep an empty copy only as a fallback.
- `AgentChatSession.RawData` carries the native transcript on every session you return, whether or not debug output is enabled; SpecStory Cloud stores it. Build it from the records you already parsed, never by reading the file a second time: a session being written grows between the two reads, so the raw transcript would describe turns the converted session never saw, and during `run` that happens on nearly every save. `RawData` contains the accepted native records from the same parsing snapshot; malformed or oversized records skipped during parsing are excluded.
- `Usage` carries only the token fields the native data distinguishes; a session total is not an input count, so leave it nil with a why-comment rather than guess. A token kind not already in `schema.Usage` is a shared change to ask for.
- Timestamps come from the record, never from `time.Now()` in a parse or render path, are consistent across every code path, and are RFC 3339 parseable.

### Comments explain why, and stay true

- Every non-obvious line carries a "why" comment. Restating the code is noise.
- Comments are self-contained. Do not reference other providers ("Claude Code uses X here"), plan documents, decision numbers, or the history of how the code came to be.
- Magic values carry their provenance ("as observed in sessions written by version 3.12").
- A comment that contradicts its code will be treated as a bug.
- Every exported identifier, in particular each SPI method, carries a Go doc comment consistent with the siblings.

### Parsing

- For JSONL session files, read each record with `spi.ReadRecordLine(reader, spi.MaxRecordLineSize)`. It bounds allocation as the record is read and reports an oversized record instead of returning it, so one bad record degrades to one bad record rather than an aborted file. Log the skip at Warn with the file and line and carry on. Sidecar and index files may use a `bufio.Scanner` capped at `spi.MaxRecordLineSize`, mapping `ErrTooLong` to a clear error.
- When a record holds `json.RawMessage`, unmarshal from a copy of the line (`scanner.Text()`, never `scanner.Bytes()`); the scanner reuses its buffer.
- A record that fails to parse is skipped, never silently: log at Warn with the file and line ("Skipping corrupted JSONL line" is the established message shape).
- Preserve the agent's native ordering and branch-selection semantics. Use sequence numbers where provided, parent links for tree formats, and file order where the native format defines it. Test supported out-of-order cases and branch selection where applicable.
- Identify tool-result records using the native format's result markers or envelope; an unfamiliar record is not automatically a tool result. A recognized tool result with an unfamiliar payload type degrades to a generic result instead of vanishing. Pair results to calls using the native correlation fields, preferring an explicit call id; use tool type or first-in-first-out only where the native format makes that pairing unambiguous.
- The parser's kind switch enumerates every record kind observed in real data, rendering it or naming it as known-nothing-to-render with the reason, so the default "unknown kind" log fires only for genuinely new kinds.
- Preserve unsupported native content in `RawData` when its record is accepted, and document what is omitted from normalized data and Markdown in `<AGENT>-FORMAT.md`. Preserve meaningful conversation text where the shared schema can represent it faithfully. Reserve "known-nothing-to-render" for records with no conversational content to display; an unfamiliar role or missing tool-call pairing is not sufficient reason to discard meaningful content.
- Scan and watch only the durable session file; never read a scratch file the agent rewrites in place.

### Native debug output

`--debug-raw` helps provider authors, reviewers, and future maintainers inspect what the agent actually stored, compare it with SpecStory's interpretation, and diagnose missing content or format changes. The provider saves readable native input alongside the normalized `session-data.json` written by the CLI. Populating `AgentChatSession.RawData` does not create these native debug files; exporting them is a separate provider responsibility.

- Honor `debugRaw` in single-session and bulk reads, live `run`/`watch` updates, and optional by-path reads. Write under `spi.GetDebugDir(sessionID)`, which defaults to `.specstory/debug/<session-id>/` and respects `--debug-dir`. When the flag is false, this export creates no files.
- For JSONL, write one pretty-printed file per accepted native record, numbered from `1.json`, `2.json`, etc. in sequenced source order. For JSON or database stores, write pretty-printed session objects or individual records with native identifiers. Preserve native fields and envelopes, including unfamiliar fields, relevant headers, and sidecar data used during conversion. Keep source identity and ordering clear enough to trace an output back to its input.
- Generate the export from the same input snapshot used for conversion. Do not reread a changing transcript or substitute a reduced typed structure that drops unknown fields or records omitted from Markdown. Those details may be exactly what a maintainer needs to understand a format change. Malformed or oversized records may be skipped under the parsing rules above, with the required diagnostic.
- Refresh the export when the session changes, removing obsolete provider-owned files so earlier records cannot masquerade as current data. Preserve CLI-owned files such as `session-data.json`. Log export failures with the session and path, without aborting session processing.
- Test that a field the provider does not recognize still appears in the saved native JSON, and that `--debug-dir` puts the files in the requested directory. For numbered files, export a session with three records, then export the same session with only two: the old `3.json` must be removed. Check that live updates produce current debug files, and that not using the debug flag creates none. Keep test output isolated with `spi.SetDebugBaseDir(t.TempDir())` and reset the override during cleanup.

### The watcher

- Filesystem events through fsnotify are the primary change signal. Supplement them with bounded periodic reconciliation to recover missed events, including writes to files that were not yet watched. Keep reconciliation scoped to the relevant session paths and prune idle watches where applicable.
- Create the watcher with `spi.NewFSWatcher`, never `fsnotify.NewWatcher`. On Windows, fsnotify has a single I/O goroutine that both delivers `Events`/`Errors` and services `Add`/`Remove`. `Add` and `Remove` wait for that goroutine to reply, and the goroutine waits for someone to receive the event or error it is sending. Nearly every watcher calls `Add` or `Remove` from the same loop that drains those channels (adopting a new session directory, re-scoping to an ancestor, pruning idle watches). With a plain fsnotify watcher, that loop deadlocks as soon as another event or error is ready. The watcher then hangs silently and no more sessions are saved. `spi.NewFSWatcher` returns an ordinary `*fsnotify.Watcher` whose channels are relayed through a lossless, ordered queue, so the I/O goroutine is never blocked. Keep `select` on `watcher.Events` and `watcher.Errors` exactly as with fsnotify. The same applies to tests that construct a watcher and call `Add`/`Remove` without draining its channels. macOS and Linux never show this failure, so only the Windows CI job will catch it, and only intermittently.
- Establish a startup baseline without emitting existing, unchanged sessions merely because the watcher started. New or changed sessions after startup count as activity, including changes made while the initial watches and baseline are being established. Each watcher architecture must establish this boundary reliably so initialization neither republishes history nor absorbs new activity into the baseline.
- If a session directory appears after startup, walk it once and adopt the files that arrived inside it, even when they carry old preserved modification times. Arrival is new activity. A pre-existing directory discovered later during the initial scan is part of the baseline, not a late arrival.
- Never let the watcher silently disable itself. If the agent's directory does not exist yet, watch the nearest existing ancestor and wait.
- When the store is keyed by project, watch only this project's subtree, never every project's directory. Walks and watches stop at the session directory; a session's own subdirectories (tool outputs, subagent logs) are neither watched nor walked.
- Watch every path the agent writes session content to, including asynchronous sidecar files. Change detection uses an on-disk signature (size and modification time) of every file the session spans, not a parsed field that a title-only rename would not touch.
- A debounced burst is re-processed once after the burst ends; the burst's last write is often the completed response.
- Deliver callbacks in order (one worker) or synchronously; contain panics in the consumer callback; close the race between `Stop` and in-flight work under one lock. Track goroutines with `wg.Go`, never `wg.Add(1)` paired with `defer wg.Done()`.
- Make the watcher restartable: create the context per start, not in `init()`.
- File descriptor usage stays bounded over a months-long watch. When watched paths can grow without limit, bound them with a strategy appropriate to the store layout: for date-organized stores, use `spi.WatchWindow*`, prune watches at rollover, and re-watch a dormant file when its modification time moves. A single-directory watch or another fixed set of watches already satisfies the descriptor bound and does not need a rolling date window.
- The command layer fingerprints every delivered session and suppresses unchanged parses; add no content-equality guard of your own.

The startup boundary and shutdown behavior must satisfy these acceptance cases:

| Scenario                                                                                             | Expected behavior                                                |
| ---------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| A session file is written just before startup and remains unchanged                                  | No emission merely because the watcher starts                    |
| A session file changes while the initial watches and baseline are being established                  | The update is eventually emitted, not absorbed into the baseline |
| A directory appears after startup containing session files with old preserved modification times     | The arriving sessions are adopted and emitted                    |
| A pre-existing directory is discovered later during the initial scan, with no activity since startup | No emission merely because discovery happened later              |
| Shutdown occurs with no new or pending session activity                                              | No emission merely because the watcher stops                     |

Shutdown must still drain pending updates before returning. These cases define which activity counts, without prescribing a particular snapshot, watch-registration, or timestamp strategy.

### Run and exec

- Honor the custom command from `-c` and `<id>_cmd`; parse it with `spi.SplitCommandLine`.
- When a resume id is requested it wins over any id pinned in the configured command. Use `spi.EnsureResumeFlagArgs` for flag-style resume (`--resume <id>`), passing the agent's supported flag names. It replaces pinned ids, fills bare or empty flags, and preserves the caller's slice.
- Stop the watcher and join in-flight saves before returning the agent's exit status.
- For an IDE provider, `run` opens the project directory with the IDE's own CLI, canonicalizing the path first, prints install guidance if that CLI is missing, waits for the IDE to create the workspace, and then watches until Ctrl-C. No silent fallback to opening the app on its home screen.

### Resume into your agent

- Call `spi.PrepareTurns` to obtain the shared ordered user/agent text turns, then serialize them into your agent's native session format. The flattening policy lives in `pkg/spi/reconstruct.go`; providers must not implement their own conversion policy.
- User text remains user text. Agent speech and thinking become ordinary agent text, and tool activity becomes agent text through `Tool.Summary` and `Tool.FormattedMarkdown`. Do not reconstruct native tool calls, tool-result relationships, or signed thinking blocks. Preserve the prepared text and its order, adapting message grouping only where the target format requires it.
- Produce the native envelope, fresh identifiers, ordering links, and lifecycle markers the target needs to load and continue the flattened conversation cleanly. Include required end-of-run or end-of-session records so the agent shows no crash or unclean-stop warning. Verify that subsequent turns extend the reconstructed session.
- Source model, usage, and path-hint metadata are intentionally dropped by the shared flattening step. Source system and environment scaffolding must not be replayed; the target supplies its own runtime context. Document any additional provider-specific preservation limits in `<AGENT>-FORMAT.md`.
- Do not invent model, usage, or version metadata to make reconstructed sessions resemble native ones. Omit unavailable fields where supported; otherwise use accepted empty or neutral values. A realistic compatibility placeholder is permitted only when testing establishes that the target cannot load or continue the session without it and rejects omission or neutral values. Document the requirement and test evidence in `<AGENT>-FORMAT.md`; an existing provider's placeholder is not evidence that yours needs one. For example, if a native message requires a model field that is absent from `spi.Turn`, write `"model": ""` if the loader accepts it, rather than attributing imported turns to the model configured for future turns.
- Carry a provenance back-link (`specstorySourceSessionId`) in the native metadata.
- Keep user-identifying data (account labels, auth metadata) out of reconstructed files.
- Your parser must exclude your agent's own slash-command and system scaffolding from user turns; the shared resume filter strips only Claude Code's markers.
- If the agent cannot resume from reconstructed data, return `spi.ErrReconstructionUnsupported`, return `false` from `SupportsReconstruction`, and say so in the changelog. Such a provider is offered as a resume target only for its own local sessions.

### Rendering

- Tool inputs render as labeled lines (`Path:`, `Command:`), not JSON blocks. JSON is the last-resort generic fallback only, pretty-printed in a `json` fence. Maps and slices are JSON-marshaled, never formatted with `%v`.
- Edits render as `diff` fences. Files render in a fence tagged by extension. Shell output renders in a `text` fence with control bytes sanitized. Web search results render as a linked list.
- A JSON tool result (an answer envelope, a diff array, a subagent record) is parsed into the idiomatic markdown for its kind and folded into the call block; the result renderer then returns empty so the raw JSON does not also appear.
- A failed call renders the agent's error text; the error branch takes priority over the success formatter for every tool.
- User-entered shell commands render as user messages clearly labeled "User ran a shell command", with the command and its recorded output/status. They do not need an assistant tool call to pair with. Preserve them as historical activity in flattened resume text, even if the native agent excludes them from its own model context.
- Thinking content is captured and rendered once, in place. Narration and tool blocks appear in the order the agent recorded them; an invocation the agent re-serializes on every state update renders once.
- Completeness beats brevity: the full system prompt, the full file content. Results are capped with a visible marker; inputs are not.
- Never nest `<details>` blocks; the wrapper is owned by `pkg/session`.
- Tool type reflects the target: workspace documents are `read`, `write`, `search`; shell is `shell`; todo lists are `task`; agent-state stores (memory, goals, cron, skills, subagents) are `generic`; a URL fetch is `read`. An unenumerated tool must still render, typed `unknown`, never dropped. Every bespoke renderer keeps a generic degradation path so no input is ever lost.
- Strip framework boilerplate the agent injects for the model ("Created At:" headers, "proactively run terminal commands" instructions) but never alter real content; scope any whitespace cleanup to the result type where the noise was observed.
- A malformed element (a todo item that is not an object) is skipped, never rendered as a placeholder row.
- Output is deterministic: never iterate a map into output; sort with one comparator. A second sync with no agent activity must change no bytes.

### Check and analytics

- `Check` resolves the binary with `spi.LookPathForCheck`, probes `--version` capturing stdout and stderr, reports `"unknown"` when the binary prints nothing, and emits exactly one event per outcome through `analytics.TrackCheckSuccess` or `analytics.TrackCheckFailure`.
- Every failed `CheckResult` includes an `ErrorType` and a helpful `ErrorMessage`. Reserve `spi.CheckErrorNotFound` for an absent binary or IDE store: the CLI presents it as informational, except for a missing custom `-c` command. Permission errors, failed probes, and invalid stores remain failures. Use `spi.ClassifyCheckExecutionError` after a successful lookup so a broken interpreter/loader is not mistaken for an uninstalled agent.
- The failure message names the command actually run, including a custom one.
- Providers emit no other analytics. Hidden flags get none.
- IDE providers probe the store, not a binary, preserve filesystem errors for classification, and emit one check event per outcome. Verify the store is readable; merely resolving its path or obtaining a lazy database handle does not prove it is usable.

### Logging

- Call `log/slog` directly; no package-local logger wrapper or adapter. Use `log.UserMessage` and `log.UserWarn` for the guidance `DetectAgent` prints when nothing is found. The only other permitted terminal output is an IDE provider's "waiting for the IDE to open this project" line. No `fmt.Print` anywhere else in a provider.
- `Check`, `ExecAgentAndWatch`, and `WatchAgent` log at Info on start, each transition, and exit. Missing lifecycle logging is a defect, because `--log` is how problems get diagnosed.
- Routine, repeating states (file events, waiting for the agent) log at Debug. Warn is for data loss the run survives. Error is for a unit of work that failed, logged once.
- Structured keys are camelCase (`error`, `path`, `sessionId`, `projectPath`, `command`, `exitCode`); messages are prefixed with the method name (`Check:`, `WatchAgent:`) for SPI entry points.
- Never swallow an error with `_ :=` when the failure would otherwise be silent to the user.

### What counts as a security concern

The CLI reads files the user's own agents wrote, on the user's own machine, as the user. Data already on the local disk is not a threat to that machine, and anything able to place a file or a symlink inside the agent's store already has the access it would be trying to obtain. So a symlink under the agent's store is read as the user pointing at their own files, a relocated project or sessions shared between checkouts, not as an escape to defend against.

The boundary worth defending runs between this machine and everything outside it:

- What leaves: SpecStory Cloud stores `RawData` and generated markdown. Redaction is central in `pkg/redact`, applied by `pkg/session` and `pkg/cloud`; providers do not touch it. A secret reaching the cloud is worse than a rare local rendering break.
- What arrives: session content is untrusted input. It is parsed defensively, never executed, never interpolated into a shell command, and never followed off the machine, and a malformed or hostile record degrades to a skipped record rather than an aborted file or an unbounded allocation.
- User-identifying data (account labels, auth metadata) stays out of generated artifacts and reconstructed files.

Local-only symlink and path checks in the providers exist to keep enumeration inside the store it is scanning, so one project's sessions never land in another's history. They are correctness controls, not security controls, and a bot review that rates one as a vulnerability is rating it too high.

### Cross-platform

The CLI runs on macOS, Linux (including WSL), and native Windows, and CI runs the full test suite on Windows.

- Paths from session data are handled by shape (leading `/` or a drive letter), never with `filepath.IsAbs`, `filepath.Join`, `filepath.Abs`, `filepath.Rel`, or `filepath.Clean`, because a session written on one OS is rendered on another. An absolute-path check accepts both shapes; a `/`-only check rejects every native Windows path.
- Local paths use `os.UserHomeDir()` and `filepath.Join`. Your cwd-to-store-directory encoder reproduces the agent's own algorithm, including its symlink resolution. Only a path naming a directory on this machine is canonicalized, and only at the boundary where it enters: the process working directory, a `--project-path` override, the local destination handed to `ReconstructSession`. Anything recorded in session data is never canonicalized, absolutized, or case-folded, because it may name a directory on another machine.
- On macOS and Windows the filesystem is case-insensitive but case-preserving, so one directory has a single on-disk spelling and many spellings that work. `cd ~/source` into a directory really named `Source` and every local path in the process carries the wrong case, while the agent records the on-disk one. Canonicalize the local path once, at the boundary, with `spi.GetCanonicalPath`, which walks the path a component at a time and reads each parent directory to recover the real case. Then compare exactly.
- `filepath.EvalSymlinks` does **not** correct case. It resolves symlinks, returns the wrong-case path unchanged, and reports `nil` error while doing it, which is what makes it such a convincing wrong answer. `filepath.Abs` and `filepath.Clean` do not correct case either. None of the three is a substitute for `spi.GetCanonicalPath`.
- Never repair a case mismatch by comparing case-insensitively, whether with `strings.EqualFold` or by lowering both sides. A recorded path can come from a case-sensitive machine, where two spellings are two different directories, and folding case silently merges them. Fix the local root's spelling instead, so the exact comparison is correct everywhere. The single exception is hashing a path into a stable identity, where there is no directory to read and the hash must not vary with spelling: fold there, gate it on the host filesystem, and expect to invalidate every id already persisted.
- IDE-style providers branch on `runtime.GOOS` for `Library/Application Support`, `.config`, and `%APPDATA%`, check `spi.IsWSL()` in the Linux branch and read the Windows side with `spi.FindWindowsAppDataPathFromWSL` first, and honor `--user-data-dir`. Any URI written to an IDE store goes through `vscode.WorkspaceURIMap` or `vscode.PathToFileURI`.
- A raw JSONL scan for a path also matches the backslash-escaped form; a Windows cwd inside JSON is written as `C:\\Users`.
- No `pgrep`, `sh -c`, `/bin/...`, or `USER`. Username comes from `os/user.Current()`; process detection on Windows uses `tasklist`, checking that the image name echoes back because it exits 0 even when nothing matched.
- Before opening the PR run `GOOS=windows GOARCH=amd64 go build ./...` and `GOOS=windows GOARCH=amd64 go vet ./...` (the prefix goes on both commands).

### Tests

- Test complicated logic and combinatorial scenarios, not constants. A test that asserts `Name()` returns its own literal will be deleted.
- A test must actually create the condition it is named for. One that cannot, and settles for exercising the ordinary path instead, is deleted or made real: it reads as coverage of the hard case while proving nothing about it, which is worse than its absence because it stops anyone else writing the real one. If the condition is too expensive to build, that is a reason to lower the threshold until it is affordable, not to keep the test.
- Table-driven with `t.Run(tt.name, ...)` where there is a matrix of cases; a single integration test may stay standalone.
- Fixtures are raw shapes captured from real sessions, so the tests encode what the agent actually writes.
- Check [fixture placement](#separate-test-fixtures-from-examples): each `testdata/` fixture has an automated consumer and asserted purpose; review-only artifacts belong in `examples/`.
- A regression test must fail when the fix is backed out. Prove it before you commit it. When a fix adds a guard, the test also proves the guarded path still works for the legitimate case.
- An exhaustive test that walks the agent's real tool inventory and asserts the expected type per name is not tautological; it guards against omission.
- Exercise bespoke renderers through the actual dispatch path using captured invocations. Verify that the recorded tool name, after normalization, selects the intended renderer; directly testing the renderer function cannot catch an unreachable dispatch key.
- Each acceptance case in [The watcher](#the-watcher) gets its own test, covering the startup boundary, late-directory adoption, and shutdown behavior.
- Reconstruction tests verify that the native output preserves the prepared user text, agent text, thinking text, and rendered tool activity in order, with the required native structure and lifecycle markers. Expect plain conversation text rather than native tool or thinking blocks; the shared flattening policy itself is tested in `pkg/spi`.
- Tests for a shared helper live in `pkg/spi` next to the helper, not in the provider.
- When changing shared parsing, rendering, or identity behavior, verify the affected existing providers. Compare regenerated Markdown from representative captured sessions for rendering changes. State any consequences for saved Markdown, indexes, or persisted identifiers.
- Test hygiene: `testutil.SetHome` (never `t.Setenv("HOME", ...)` alone; it does not fake `%APPDATA%`, so IDE-style providers point at a fake install through their user-data-dir override), `testutil.JSONString` when a real path goes into a JSON fixture, `testutil.EqualPaths` after URI round-trips, `t.TempDir()` (a symlink on macOS and an 8.3 name on Windows, so compare canonical to canonical), `t.Chdir`, never a `"file://" + path` splice, deadline polling rather than fixed sleeps for file events, and expected values built with `filepath.Join` or `filepath.FromSlash`. Windows-shaped table rows run Windows behavior on macOS.
- A test that creates a fixture and reads it back with the same spelling can never catch a case-sensitivity bug, because both sides carry the same mistake. When the provider matches recorded paths against a local root, add one case that reaches the project through a differently-cased spelling of a real directory and asserts the sessions still resolve. Skip it where the filesystem is case-sensitive rather than asserting the wrong thing there.
- Any test that enables debug output first calls `spi.SetDebugBaseDir(t.TempDir())` so nothing is written into the package directory.
- Keep test accommodations small: package-level root-path or limit overrides and parameterized internal helpers are acceptable when needed to exercise meaningful behavior affordably. Avoid elaborate interfaces or abstraction layers whose only purpose is testing.

### Dependencies and new files

- No new dependencies without asking first, with the reason. Prefer the standard library.
- Keep provider-specific test helpers in `*_test.go` files alongside the tests. Test helpers shared across packages belong under `internal/`, such as `internal/testutil`.
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

Check the integration cases that ordinary provider commands can miss: bare `run` must select the default described by `run --help` and the README; `watch` must return an error if no watcher can start; and `watch <id> --output-dir <dir>` must save to the requested directory while still discovering the current project's sessions. `sync -s <session-id> --print` must print the session without writing history files.

For both cross-agent resume directions, use a source session containing user and agent text, thinking, tool activity, and a slash command. Verify that the prepared conversation text survives in order, including thinking and rendered tool activity as ordinary agent text, with the migration note first and no source command or system scaffolding replayed. Ask the target about prior conversation content to establish that it loaded the context, then verify that it continues the same reconstructed session cleanly. Native tool replay and historical model/usage preservation are not part of this contract.

Then run the tool enumeration session described above through `./specstory sync --log --debug --debug-raw` and audit the markdown block by block against the raw data. Reconcile native tool invocations with rendered blocks: each invocation appears once, in the native order, with its own results; repeated state snapshots are not additional invocations, and quoted transcripts must not produce phantom tool blocks. Grade every tool-use block as formatted (all important data present and pleasantly presented), partial (formatted but missing important elements), raw (raw JSON or unformatted output), or missing, in a table with the tool name, markdown line, grade, data file, data line, and a comment. Exercised tools should grade as formatted, including failed calls. List unexercised tools separately with the reason for each coverage gap; do not give them a rendering grade. State the tested version, OS, configuration, and extensions, and distinguish declared, enabled, and exercised tools. Attach `tools.txt`, `versions.txt`, the synced history file, and the audit table to the PR.

## Software factory affordances

The SpecStory provider factory watches each agent's release channel and audits new versions. A provider enrolls by shipping executable bash scripts under `pkg/providers/<agent>/factory/` (the package directory, not the registry id). Model them on `pkg/providers/claudecode/factory/` and `pkg/providers/antigravitycli/factory/`, including the header comment that records the channel decision, what was rejected and why, and the contract paragraph. These scripts run unattended in CI on every merge, so they are read as untrusted code: nothing is piped to a shell except the vendor's own pinned installer, and nothing reads outside the named credential.

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

- [ ] Package named for the product, lowercased and unspaced
- [ ] Necessary files only, similar to the recommended files list, no file sprawl
- [ ] `var _ spi.Provider` assertion present
- [ ] Every SPI method implemented, including `ListAllAgentChatSessions` and the three reconstruction methods
- [ ] Tests cover behavioral contracts and capability consistency, including agreement between `SupportsReconstruction` and the reconstruction methods; trivial constant-return methods do not need standalone tests
- [ ] Provider registered in `pkg/spi/factory/registry.go`
- [ ] Config wired: `<id>_cmd` in template, struct, switch, doc comment, and test rows, with both wiring tests passing
- [ ] Brand-specific TUI color added
- [ ] Both READMEs updated
- [ ] Draft changelog.md entry added
- [ ] `<AGENT>-FORMAT.md`, in the provider package, written from the current release with the write lifecycle and baseline version, no legacy notes
- [ ] `tools.txt` from the agent itself, prefixes stripped, declaration preferred over self-report
- [ ] Bespoke renderers and classifier cases use names from `tools.txt` and are backed by observed tool records
- [ ] Tool audit identifies the tested environment and distinguishes declared, enabled, and exercised tools
- [ ] Unexercised tools have documented reasons, and observed failed calls have verified error rendering
- [ ] Tool inventory sweep test exists
- [ ] Markdown code blocks use `spi.CodeFence` instead of hard-coded triple backticks, so backticks in the content cannot prematurely close the block
- [ ] Text truncation uses `spi.CapRunes` instead of byte slicing such as `text[:limit]`, which can split a multibyte Unicode character and produce invalid UTF-8
- [ ] No local copies or equivalents of the helpers available in `pkg/spi`
- [ ] Provider `Check` reports exactly one outcome through `analytics.TrackCheckSuccess` or `analytics.TrackCheckFailure` using `analytics.CheckAttempt`, rather than calling `analytics.TrackEvent` directly, so event names and properties stay consistent across providers
- [ ] No `os.Exit` calls
- [ ] fsnotify events are the primary change signal, supplemented by bounded periodic reconciliation
- [ ] Every fsnotify watcher, including those in tests, is created with `spi.NewFSWatcher`, so calling `Add`/`Remove` from the event loop cannot deadlock on Windows
- [ ] Watcher does not keep accumulating open files or filesystem watches as session history grows: use a fixed set of directory watches or prune older watches (with `spi.WatchWindow*` for date-organized stores) to avoid exhausting operating-system resources during long-running watches
  - [ ] Watcher leaves existing, unchanged sessions alone at startup
  - [ ] Session activity during watcher initialization is eventually emitted
  - [ ] Late-arriving directories are adopted even when their files have old modification times
  - [ ] Initial discovery of pre-existing directories does not count as new activity
  - [ ] Watcher shutdown drains pending updates without emitting sessions merely because it stops
  - [ ] Watcher startup and shutdown acceptance cases each have a test
  - [ ] Watcher context created per start
- [ ] Session callbacks have panic recovery
- [ ] Goroutines are tracked with `wg.Go` only
- [ ] JSONL session files use `spi.ReadRecordLine` with `spi.MaxRecordLineSize` to cap memory allocation while reading each record and allow reading to continue after an oversized record
  - [ ] Oversized and malformed session records skipped with a Warn, not failing to read the entire session file
- [ ] Native ordering and branch selection preserved and tested where applicable
- [ ] Tool results identified and paired using native semantics
- [ ] Unfamiliar tool-result payloads rendered generically
- [ ] Comments explain why and do not reference other providers or history
- [ ] Magic values carry provenance
- [ ] Exported identifiers are documented
- [ ] `Check` lifecycle logging present
- [ ] No `fmt.Print` outside detection help
- [ ] `RawData` set on every session and built from the parsed records, not a second read
- [ ] [Native debug output](#native-debug-output) preserves readable native input, honors the flag and debug directory across read and live paths, and refreshes without stale records or deleting CLI-owned files
- [ ] `gofmt -w .` leaves code formatted
- [ ] `golangci-lint run` passes for the whole project
- [ ] Good, reliable, robust automated tests
  - [ ] Tests table-driven where useful
  - [ ] No tautological tests
  - [ ] Test fixtures are captured from real data
  - [ ] Every `testdata/` fixture has an automated test consumer and asserted purpose; unused generated outputs and manual QA evidence are in `examples/`
  - [ ] Windows-safe test helpers are used
  - [ ] Symlinked and special-character project paths are tested
  - [ ] Debug tests call `spi.SetDebugBaseDir`
  - [ ] `go test ./...` passes
  - [ ] `GOOS=windows GOARCH=amd64 go build ./...` passes
  - [ ] `GOOS=windows GOARCH=amd64 go vet ./...` passes
- [ ] Every SpecStory command in the manual test table was exercised against the real agent and provider
- [ ] Same agent session resumption works with a native session
- [ ] Cross-agent session resumption was verified in both directions
  - [ ] Cross-agent sessions reconstruction uses `spi.PrepareTurns` and preserves its text and order
  - [ ] Thinking and tool activity are reconstructed as ordinary agent text
  - [ ] Required native reconstruction structure and lifecycle markers verified
  - [ ] Unavailable reconstruction metadata omitted or neutral, with test evidence for any required realistic placeholders
  - [ ] Reconstructed files contain no account or authentication metadata
  - [ ] Additional provider-specific reconstruction limits documented
- [ ] `factory/latest-version` present and tested under an isolated home with its negative case
- [ ] `factory/install` and `factory/list-tools` present if the agent runs headless
- [ ] Repository review pass completed and findings triaged: `/code-review` before the pull request exists, or `/pr-review <number>` once it does
- [ ] Pull request states that the review pass was run and explains the informed decisions about which findings to act on
