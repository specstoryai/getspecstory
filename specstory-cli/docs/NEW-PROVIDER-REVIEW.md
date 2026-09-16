# New Provider Review

This document guides an agent through the work of taking a new provider contribution from "pull request opened" to "released", reproducing both the technical checks and the judgement the maintainer applies. It was distilled from the reviews of the Cursor IDE, VS Code Copilot, Antigravity CLI, DeepSeek TUI, and Muse Code providers (with the Factory Droid CLI review as an older secondary example): the commits the maintainer made on those branches, the pull request threads, and the recorded working sessions in which the maintainer directed an agent through each review.

The contributor-facing summary of the standards is [NEW-PROVIDER-GUIDE.md](../NEW-PROVIDER-GUIDE.md). This document is the reviewer's side: what to check, in what order, how to decide, and how to work with the maintainer.

The mechanical checklist that every review starts from lives in `.claude/commands/pr-review.md` and `.claude/commands/code-review.md`. Everything here is in addition to that.

## Relationship to the software factory

This process will eventually run inside the SpecStory provider factory (the `NEW-PROVIDER-PR` and `IMPLEMENT-PROVIDER` workflows). It is written so it can be lifted there as-is: every step names its inputs, its evidence, and its definition of done. Until then it runs as an interactive review with the maintainer, and the operating model in the next section describes that collaboration.

## Repositories

Four repositories take part. On the maintainer's machine they are sibling checkouts under `~/Source/SpecStory/`.

| Repository | Role |
| --- | --- |
| `specstoryai/getspecstory` (this one; the CLI is the `specstory-cli/` subdirectory) | The provider, docs, changelog, and CI |
| `specstory-cli-multi-agent` (private) | Tool enumeration sessions and audits, one hyphenated agent directory per agent (`muse-code`, `copilot-ide`) with a dated subdirectory per run |
| `provider-factory` | The software factory: workflow definitions, the `versions/` and `tools/` state per provider package, and the tool-audit harness scripts |
| `specstoryai/specstory-website` | The end-user docs site under `content/docs/` |

Scratch projects for manual testing live under `~/Source/SpecStory/compositions/`, one directory per experiment.

## 0. Operating model

### You propose, the maintainer decides

Every review in the record follows the same loop. You produce numbered findings. The maintainer replies with numbers and terse modifiers ("address 6, 14, 27 (blank), ... 31 (local only), 33 (use directly), ... 39 add timeout ...", "MIP 1 and 4", "fix deepseektui too", "delete for #3", "1"). A modifier is a decision, not an opening for debate. Apply it, verify it, report it, and move on. "We don't want this: N" rejects the pull request change that observation N describes: undo exactly what the observation names, not the observation (".idea is fine, just not .specstory"). When the boundary is ambiguous, ask one line rather than guess.

Rules the maintainer has stated or enforced, in their words where they said it:

- Number everything so it can be referenced. When a list came back grouped instead of numbered: "put the numbers in order... I can't reason about these this way".
- Explain before acting. When an agent started coding on a hypothesis: "that's NOT the only problem... doesn't your .CLAUDE mention not just changing code but explaining what you're going to do?" For a design question: "Explain the overbroad inference... what the current issue is, where/when it applies, what the impact is. Then explain each of the possible fixes, and why you recommend the one you do."
- Offer options with pros and cons and a recommendation. The maintainer usually takes the recommendation, sometimes overrides it, and occasionally picks something not on the menu ("hmmm why not just ditch the inference all together").
- Do not change the SPI, add a package, add a file, or add a dependency without asking. "uhhh... WHAT? Why change the SPI man? This can just be a detail of the Copilot provider". Once a new file is approved for one provider, the identical case in a sibling is pre-approved.
- Never commit. The maintainer commits every batch under a plain message ("Code cleanup.", "Minor code cleanup.") and a descriptive one for user-visible fixes. Say what is uncommitted at the end of every turn.
- Never run git operations that change state (merge, checkout, pull, branch) unless told to. When the maintainer merges `dev` and hands you conflicts, explain each conflict's cause and proposed resolution as a numbered list; resolve on disk when told; do not stage or commit.
- Do not post the review or its items to the pull request (`gh pr review`, inline comments). Fixes land as commits on the branch; the only PR comment is the maintainer's release note.
- Keep the maintainer's ledger. When asked to write findings to `docs/<PROVIDER>-REVIEW.md`, that file is theirs: mark items done when told, never rewrite it on your own, and expect it never to be committed.
- Be interruptible. Every batch ends with two lists: "what changed and why" and "still open". The maintainer picks from the second list.
- Before each batch run `git branch --show-current` and `git rev-parse HEAD`; the maintainer switches between `dev` and provider branches between turns ("no, we were on dev"). Every "what changed" report names the branch each change landed on.
- Do not overclaim. When a summary said deferred items "are now written into the release doc", the maintainer asked "Where were they written?" and they were not. State only what exists on disk.
- Rebuild the repo-root binary (`go build -o specstory`) before any manual verification and invoke it as `./specstory`. A stale binary silently regenerates markdown with old logic, and a bare `specstory` is whatever release is installed on the path; both have cost real review time.
- After every change: `gofmt -w .`, `golangci-lint run` on the whole project (never a single file), `go test ./...`, `go build -o specstory`. For watcher changes add `go test -race` on the package. For anything touching paths add `GOOS=windows GOARCH=amd64 go vet ./...`.

### Evidence over assertion

- Cite `file:line` for every finding and a commit or a captured session for every claim about behavior.
- Prefer a real session from the current agent release over a fixture. The maintainer verifies against the agent version shipping that week and has rewritten specs that were eight weeks old.
- A regression test must be shown to fail without the fix; a test that passes without the fix proves nothing.
- The cheapest discriminating experiment first. When the agent proposed a synthetic fixture: "isn't there an easier confirmation first? ... Only if that works is it worth creating a synthetic session, no?" And the experiment must isolate the hypothesis: "that's the wrong falsification, no? ... That won't teach us anything."
- Distrust implausible numbers until the mechanism is shown: "In computer time 1m48s is an absolute eternity. I'm not sure I trust your test/data."

### What the maintainer does personally

Plan for these to come back to you as pasted terminal output or screenshots rather than doing them yourself, unless you are running autonomously in the factory:

- Merging `dev`, committing, pushing, tagging, and the release itself.
- Interactive steps such as OAuth logins and the agent's own TUI.
- Running each command against the real agent in scratch projects and reporting what was seen ("This isn't good.", "`watch` doesn't work for Muse:", "IT WORKED!").
- Choosing the accent color, the changelog wording, and the version floor.

## 1. Intake

Establish the facts before reading code.

1. Pull request number, author, target branch, and whether the contributor is external: `gh api repos/specstoryai/getspecstory/pulls/<n> -q .author_association` (MEMBER or OWNER is the team; anyone else is thanked in the changelog).
2. The agent: product name, vendor, binary name, release channel, install method, and the version currently installed on the review machine (`<agent> --version`). Note it as the provisional floor; it is re-read on release day.
3. The store: where sessions live, the data format family (JSONL, JSON, SQLite, IDE workspace storage), whether the agent has a headless mode, and the write lifecycle: which file is the durable record, which files are transient (checkpoints, rolling "latest" files, locks), when each is written and deleted, and whether a transient file is shared across concurrent sessions. When a real session is not found, clone the agent's source to a scratch directory and read the writer rather than guessing.
4. Which exemplar provider the submission was modeled on. The PR body often says. If it was copied from a sibling, plan to review the sibling too: fixes found here are mirrored there.
5. Whether `<AGENT>-FORMAT.md` exists in the provider package and what version it was verified against.
6. Whether a tool enumeration session exists (see section 7), at what agent version, and whether the agent declares its own tools (a stream init event, an extension hook) or only the model's self-report is available.
7. Test files and functions versus the exemplar. A fraction of the peers' count is a High ledger item, resolved by tests for the parser, watcher, and reconstruction logic that warrant them, not by coverage padding.
8. Whether the branch compiles against the current SPI. A contribution that predates an SPI change fails at the registry with "does not implement spi.Provider (missing method ...)". Those gaps are yours to close.
9. Whether the contributor ran this repository's own review pass (`/code-review`, or `/pr-review <number>` once the PR exists) and what they changed as a result. The PR body should say. If it is absent, run it yourself before reading further: it is the same standard this document applies, and a submission that has not been through it will spend its first round on findings the contributor could have taken themselves.
10. Read the PR body's open questions. Answer them with code, not comments. When the Copilot IDE PR asked whether the two IDE providers should share workspace code, the answer was a shared `pkg/providers/vscode` package.

## 2. Bring the branch current

1. Confirm the local checkout is exactly the PR head before anything moves: `gh pr view <n> --json headRefOid` against `git rev-parse HEAD`.
2. Have the maintainer merge `dev` into the PR branch (or do it if explicitly told). Explain each textual conflict: cause, and what to do. Number them.
3. Find the semantic gaps the merge does not surface: new SPI methods (`SupportsReconstruction`, `ListAllAgentChatSessions`, `progress` callbacks) that every other provider gained while this branch sat. Wire them, each with a test built on the package's existing fixture helpers: `ListAllAgentChatSessions` against a scoped and an unscoped session asserting `SessionID`, `NativePath`, `Slug`, and `OriginCwd`; the three reconstruction methods asserted consistent; the registry-level `TestSupportsReconstruction` in `pkg/cmd` updated.
4. After the merge, list `docs/` and confirm no plan or gap-analysis document has returned (one deleted at release has already come back through a later merge), and re-read this document's ledger for claims the merge has made false, such as "still open" above the section that closed it.
5. Build, lint, test, Windows vet. Note any pre-existing failures separately from the PR's.

A noisy diff wastes a review. If the PR still carries CI, toolchain, or history files from a stale branch point, get it merged with `dev` and re-run the review on the clean diff.

## 3. First contact

Do this before, or in parallel with, the static review. The real binary is the fastest signal.

```zsh
go build -o specstory
./specstory check
./specstory check <id>
./specstory check <id> -c "nonexistent-binary"
```

Then, from a scratch project where the agent has already completed at least one turn:

```zsh
./specstory sync <id> --no-cloud-sync --no-usage-analytics --debug-raw --log --debug
./specstory sync -s <session-id> --print
./specstory list <id>
```

What to look at:

- `check` must not contradict the registry. The first Droid smoke test showed `check` advertising `specstory run droid` while `run droid` said the provider was invalid. "This isn't good."
- The version line is real or absent, never a label.
- `sync` produced one markdown file per non-empty session and `.specstory/debug/<session-id>/` holds per-record `N.json` files (JSONL agents) or one raw file, plus `session-data.json`. The provider clears that directory before writing, so the file count equals the record count.
- `debug.log` shows detection, the files read, every skip decision with a reason, and the write, and contains no `schema validation:` warnings. That validation runs only under `--debug-raw`, never fails the write, and is the one place the schema's structural rules are enforced.
- Open the markdown and read it end to end. Prompts and replies present, thinking present if the agent records it, tool blocks present, nothing rendered as raw JSON that a reader would care about.
- From a directory with none of the agent's sessions, `sync <id>` and `list <id>` print the provider's own guidance and exit 0; the command layer prints nothing itself.

Anything wrong here becomes the first items in the ledger.

## 4. Static review: build the numbered ledger

Run the project's review command against the PR, naming the exemplar: `/pr-review <n>` with a note such as the one used for Copilot, "Let's play close attention to the Cursor IDE provider as an exemplar and note important omissions/deviations in this provider". For local branches use `/code-review this branch against dev`. If the first pass ran against a stale diff, discard it and re-run after the merge.

Persist the output verbatim to `docs/<PROVIDER>-REVIEW.md` when asked ("Write this (exactly as it was presented here) to ./docs/WINDOWS-REVIEW.md"). Mark items DONE, with sub-item numbers, as they land.

The `pr-review` checklist covers naming, idiomatic Go, simplicity, why-comments, missing comments, logging, analytics, single exit, `wg.Go`, code that has to exist, DRY, and data-driven tests. It deliberately ignores `changelog.md`, so the release artifacts in section 11 never appear in the ledger and are reviewed by hand. The sweep below is what the maintainer looks for on top of the checklist. Every item has been a real finding.

### 4.1 SPI conformance and symmetry

Build a matrix of this provider against `pkg/spi/provider.go` and against the exemplar, one row per method, and against the shared behaviors recorded in §4.6 (watcher) and §4.2 (shared helpers).

- Every method on `spi.Provider` present (twelve today; count them against the file); a compile-time `_ spi.Provider = (*Provider)(nil)` assertion present, in a `var` line or a grouped block. Check assertions for each implemented optional interface too (`spi.PathSessionReader`, `spi.ProgressEnumerator`).
- `GetAgentChatSession` returns `nil, nil` for not found and errors only for real failures.
- `GetAgentChatSessions` calls `progress` once per file, including skips and failures, so the bar reaches the total.
- `ListAllAgentChatSessions` reads the originating cwd from inside the session; `spi.PathSessionReader` implemented if by-id lookup walks the store; `spi.ProgressEnumerator` via `spi.ScanSessionsInParallel` for a JSONL store (it walks `*.jsonl` only; other stores implement the enumeration themselves).
- `SupportsReconstruction` is a constant that agrees with `ReconstructSession` and `NativeSessionPath`, and neither of those touches the filesystem.
- The registry id equals the `ProviderInfo.ID` stamped in session data; that has held for every provider from Muse Code onward (older providers stamp hyphenated forms such as `deepseek-tui` and `antigravity-cli`, which are legacy, not a second convention).
- The comparison baseline for a terminal agent is Claude Code ("That's not the best comparison, try Claude Code as the definitive provider."), refined by the newest accepted providers for `Check` shape, watcher startup policy, and exit handling. For an IDE agent the baseline is Cursor IDE plus `pkg/providers/vscode`. Appendix B names which provider is the reference for each concern and which are drift.

### 4.2 Shared-code reuse audit

Produce a table: local helper, shared equivalent, behavioral delta if swapped. This is the audit the maintainer asked for on every provider branch in flight ("Review the new provider and see if it needs any of the same or similar adjustments", "OK, same exact thing."). The shared inventory is the exported surface of `pkg/spi` (`CodeFence`, `LanguageFromPath`, `TodoSymbol`, `RenderGenericJSON`, `StringValue`, `ClassifyCheckError`, `DispatchSession`, `CapRunes`, `ReadRecordLine`, watch-window and shell-path helpers) plus `analytics.TrackCheckSuccess`/`TrackCheckFailure`; each helper's doc comment records the behavior it standardized.

Check specifically:

- `SessionData` construction uses `schema.CurrentSchemaVersion` and `schema.ContentTypeText` / `schema.ContentTypeThinking`, not duplicated string values. Do not apply these constants to native record fields or code-fence language labels.
- Fences: the reliable check is which call sites do not go through `spi.CodeFence`, not which lines contain backticks. Fences assembled across `WriteString` calls hide from grep. Backslash-escaped backticks are a bug.
- `spi.LanguageFromPath`, `spi.RenderGenericJSON`, `spi.TodoSymbol`, `spi.FormatDiffBlock`, `spi.StringValue`, `spi.NormalizeToolName`, `spi.CapRunes` (no `s[:N]`).
- `errors.Is` for a sentinel and `errors.As` for an error type. `grep -rnE '(==|!=) *(io\.EOF|sql\.ErrNoRows)'` and `grep -rnE '\.\(\*[a-z]+\.[A-Za-z]+Error\)'` over the provider; both forms miss a wrapped error, and wrapping tends to be added later by someone with no reason to look for a bare comparison.
- `spi.ClassifyCheckError` with the `spi.CheckError*` constants; an `analytics.CheckAttempt` emitted through `TrackCheckSuccess` and `TrackCheckFailure`; a `versionFlag` constant that is what actually runs.
- `spi.SplitCommandLine`; `spi.EnsureResumeArgs` for subcommand-style resume; for flag-style resume a helper that replaces a pinned id, inserts after a bare flag even when the next token is another flag, repairs `--flag=`, and never appends to the caller's slice. No shared flag-style helper exists yet, and every existing flag-style copy lets a pinned id win, so this is a new helper to write, not one to copy.
- `spi.CanonicalizePathOrClean` for local comparisons; `spi.FileURIToPath` and `spi.ParseVSCodeRemoteURI` for URIs; `spi.NormalizePath` and `spi.ExtractShellPathHints` for hints.
- `spi.GenerateFilenameFromUserMessage`, `spi.GenerateReadableName`, `spi.ReadableTitleFromSessionData` for slugs, names, and titles; a local slug copy drops the IDE-XML and heading stripping.
- `spi.ReadRecordLine` with `spi.MaxRecordLineSize` on every JSONL session file. A local read loop that measures the line after reading it is wrong twice over: the oversized record is allocated before the check can reject it, so the cap prevents nothing, and returning an error for it costs the user every remaining record in the file. Grep for `ReadString('\n')` in the provider; a capped `bufio.Scanner` remains correct for sidecar and index files, where losing the remainder is acceptable.
- `spi.GetDebugDir` only; the provider never writes `session-data.json`; `AgentChatSession.RawData` is populated on every session returned, independent of `debugRaw`, because cloud sync uploads it, and is built from the records already parsed rather than a second read of the file, so the raw transcript and the converted session cannot disagree about a session that is still being written.
- `spi.DispatchSession` or a local `defer recover()`; `spi.WatchWindow*` for unbounded stores; `spi.PrepareTurns`, `spi.ResolveWorkspaceRoot`, `spi.ReconstructRole`, `spi.RFC3339Millis`, `spi.ResumedSessionTitle` in reconstruction.
- SQLite: `grep -rn 'sql.Open("sqlite", ' pkg/providers/<p>`; every read DSN starts with `file:` and carries `mode=ro&` plus `spi.BusyTimeoutPragma` (see 4.12).
- Semantically different helpers may stay local, but say so in the ledger with the reason (the refactor doc's "left alone deliberately" lists are the model).

Judgement on when to hoist: two providers sharing a shape means hoist now, tests moving with the code ("hoist and refactor now" overrode a recommendation to defer until Windows work). A third copy of anything is extracted immediately, even when the review recommended a follow-up PR ("Let's address MIP 1, 2, 3, 4, 5" took the watch-window extraction that the review wanted deferred). A large cross-provider dedup that would widen a release PR is instead planned as its own researched refactor with a written plan (options, pros and cons, recommendation, decisions taken, what landed).

### 4.3 The delete list

The maintainer removes rather than restructures. Look for:

- Dead code, unused exported wrappers, "kept for backward compatibility" helpers with no callers, test seams with no test ("33 (use directly)"). A one-caller extraction made for readability or single exit is not on this list; an extraction that dropped behavior is ("remove the extract for 124").
- Renderers, classifier cases, and argument-key aliases for tools the agent does not have. Ground truth is the agent's enumeration, taken as the union of the harness declaration and the session (section 7). "Why do we have all those tooltypes in markdown_tools / test? These are the 14 Droid actually has". "remove any tool renderers that are just speculative". The resulting comment in the Antigravity renderers reads: each renderer reads exactly the arg keys its tool has been observed to emit, no speculative aliases.
- Plausible-looking fallback paths nobody can show ever existed. They imply the primary path is unreliable. This is distinct from the generic degradation path every bespoke renderer must keep for an unmatched shape (section 7).
- Narrative about versions below the floor in a brand new provider's docs, code, or comments: "That's all just noise." When the store shape changed between the contributor's baseline and this week's release, that is drift inside the supported range: keep both paths, comment each with the version it was observed in, and add a tolerance test for the older shape.
- Planning and gap-analysis documents in `docs/` ("84 (delete them)"). A format spec describing what is may stay.
- Small utility files that split a provider into sprawl ("This is too much file sprawl. Put this function where the other providers have their similar functionality.").
- Unexplained repository hygiene changes riding along, such as a widened `.gitignore` (history files are tracked in this repo) or an ignore entry for a directory that does not exist.
- Workspace inference from tool paths. "hmmm why not just ditch the inference all together. Not sure we really care about -p sessions."

### 4.4 Honest data

- `CheckResult.Version`: real or empty ("27 (blank)" chose an empty string over the label "Cursor IDE").
- `ProviderInfo.Version` is the CLI's version, `"unknown"` if unrecorded; the model goes on the message ("50 is wrong... that's the CLI version in the other providers, yeah?").
- Empty `--version` output on a successful run reports `"unknown"`, matching the newest siblings ("use \"unknown\" as the version").
- Unknown workspace stays unknown. Session matching then uses containment of stated paths, never a guessed common ancestor.
- `WorkspaceRoot` is never empty: stated workspace, then the caller's project path, then the process cwd as a documented last resort.
- On an IDE store the same session id can appear under several matching workspace entries (WSL, remote, `.code-workspace`), enumerated in hash order, so an empty copy can come first. Every read path enumerates all matching entries and marks an id seen only after the content check; the by-id path keeps an empty copy only as a fallback. The regression test's empty-first case must fail against the old ordering.
- Timestamps are taken from the record, never from `time.Now()` in a parse or render path (reconstruction is the one place "now" is legitimate), consistent across every code path (local time was chosen where the provider already used it: "31 (local only)"), and RFC 3339 parseable, because the filename builder and the renderer parse with `time.RFC3339` and silently fall back on failure.
- `Model` is the real model when the data has it, never a placeholder like an automatic-mode label or the responder's display name.
- `Usage` carries only the token fields the native data distinguishes: a session total is not an input count, so leave it nil with a why-comment rather than guess; populate it whenever the agent records per-message usage, because it is uploaded to the cloud and nothing local would notice its absence. A token kind not already in `schema.Usage` is a shared change (schema field, telemetry attribute, README row), never a stuffed existing field.
- `Name` prefers a title or summary the agent itself recorded, falling back to the shared generator, treated as an overlay because such indexes lag ("do it now, as best effort name, falling back to current").

### 4.5 Comments

- Every "what" comment becomes a "why" comment. Every magic value carries provenance ("as observed in sessions written by Cursor 3.12"; the workspace id salt is "verified byte-for-byte against real workspaceStorage entries").
- Comments must be self-contained: no references to other providers ("Don't need a comment about what Claude Code uses, it's immaterial here."), no plan document or decision numbers, no burn-down history ("don't need those historical comments on 72-74").
- A comment that contradicts its code is a defect, fixed by making the comment true ("Fix comment (11)").
- Future-facing risk is written down at the pin: what happens if the agent bumps its schema version ("68 (add comment about what might happen if Cursor bumps)").
- Sentinel returns are documented in the caller's terms: what `nil, nil` or `"", nil` means and what the caller should do.
- Step-by-step narration comments ("Step 1...") are removed.
- Every exported identifier, in particular each SPI method, carries a Go doc comment consistent with the siblings; nothing mechanical enforces it.
- A contributed doc or README that names a flag or behavior the code does not implement is a ledger item, as is contributed markdown that breaks the writing conventions (`zsh` fences, a blank line after every heading).

### 4.6 Watcher

The maintainer reads watchers for real races and startup behavior, and has rewritten several.

- fsnotify, never a poll ("Why aren't we using fsnotify for Droid? It's the general preferred pattern for our providers."). A bounded reconcile tick on top of fsnotify, to catch writes to unwatched files and prune idle watches, is the reference shape.
- Startup policy, uniform across every watching provider: emit nothing that exists at startup; adopt what appears after the watch is armed, including walking a newly created directory once, because those writes happened unobserved. Never re-publish history. Each side of that rule gets its own regression test.
- The watcher must never permanently disable itself. Missing directory: watch the nearest existing ancestor and wait ("With `watch` it's worse. Just doesn't work at all. SpecStory never seems to see the session at all.").
- When the store is keyed by project, watch only this project's subtree, never every project's directory. Walks and watches stop at the store layout's session depth: a session's own subdirectories (tool outputs, subagent logs) are neither watched nor walked, with a test on the boundary.
- Every path the agent writes to is watched, including asynchronous sidecars (task logs). Change detection uses an on-disk signature (size and modification time) of every file the session spans, not a parsed field that a title-only rename would not touch, so a sidecar-only write re-emits.
- Suppressed events are re-processed on the trailing edge; a safety-net poll catches what fsnotify missed.
- Callback delivery is ordered (one worker) or synchronous; panics are contained; goroutines use `wg.Go`; the shutdown race between `Stop` and in-flight work is closed under the same lock; the watcher is restartable (per-start context, not `init()`).
- Dedup is the command layer's job: it fingerprints every delivered session and suppresses unchanged parses, so the provider fires on every relevant event and adds no content-equality guard of its own (a message-count guard swallows a streamed last message that grows in place). Debouncing bursts is fine.
- File descriptors stay flat over a months-long watch: bounded trailing window via `spi.WatchWindow*`, watches pruned at rollover, dormant files re-watched when their mtime moves ("we're considering MUCH longer lived watch... (could be weeks/months)").
- Routine states log at Debug, not Warn.

### 4.7 Run and exec

- `-c` is honored by `run` and `check`; `<id>_cmd` from the config file is honored by `run` (the check command reads only the flag). Whichever command was used is the one echoed in the check failure message.
- The `<id>_cmd` wiring is now machine-checked rather than a matrix row to remember: `go test ./pkg/config/ ./pkg/cmd/` covers `TestProvidersConfigIsFullyWired` (each `ProvidersConfig` field reaches the template and a `GetProviderCmd` case) and `TestEveryRegisteredProviderHasACommandOverride` (each registered id resolves to a field). A partial wiring is otherwise silent — TOML accepts an unknown key and `GetProviderCmd` returns `""` — so neither the build nor a manual run surfaces it. Still confirm by hand that the provider actually *uses* the resolved command, since the tests only prove it is reachable.
- The requested resume id wins over a pinned one in the configured command (this only arises through `run <id> --resume`; `resume` passes no custom command).
- Non-zero agent exit: stop the watcher, join in-flight saves, then return `spi.AgentExitError`. `os.Exit` in the exec helper loses the last save; returning the raw process error collapses the status to exit 1 with an error box.
- Process launch is non-blocking when the launcher may run for the whole session (`--wait`).
- IDE providers: `run` opens the project directory with the IDE's own CLI, canonicalizing the path first so a differently-cased spelling does not mint a second workspace; a missing CLI prints install guidance and never falls back to opening the app on its home screen ("plus I don't want the fallback to open on macOS"); it waits for the IDE to create the workspace and prints progress while it does; it prints a per-save line, because the IDE leaves the terminal idle. CLI agents own the terminal and get no interleaved output. The per-save line and the post-resume restart note are printed by the CLI, keyed to the registry id in `main.go` and `pkg/cmd/resume.go`, so a new IDE provider needs those edits too.
- Provider variants (Insiders, VSCodium) register only when they have data, so `check` and `watch` banners are not noisy ("We don't want all the extra variants here... just "VS Code Copilot IDE" and nothing more").
- `--user-data-dir` overrides apply before variants register.
- Provider-printed notes use the IDE's current panel name as shown in the running app, name the selected variant, print only on the path they describe (a resume-only note never prints on plain `run`), and get the same wording and grammar review as any user-facing output.

### 4.8 Resume and reconstruction

- A provider that cannot reconstruct returns `spi.ErrReconstructionUnsupported`, returns `false` from `SupportsReconstruction`, is offered as a target only for its own local sessions (excluded for other agents' sessions and for every cloud session, because cloud resume always reconstructs), and `resume <id>` as a preset fails immediately ("Antigravity should not appear in the resume target list", "this is also allowed, and shouldn't be (should error right away)").
- Capability answers are pure: no filesystem side effects from `SupportsReconstruction` or `NativeSessionPath`; the resume command creates the directory before writing.
- The reconstructed file matches what the agent writes, field for field, verified against a session the agent authored at the current version; unknown fields in existing records are carried through as raw JSON.
- It ends cleanly. If the agent writes an end-of-session record, synthesize one so the agent shows no crash warning ("could we synthesize a clean stop?"). Match the minimal shape the agent itself writes; do not fabricate telemetry.
- Provenance back-link (`specstorySourceSessionId`) present. User-identifying data (account labels, auth metadata) omitted.
- The shared turn filter strips only Claude Code's markers (`spi.SyntheticCommandTags` and its interrupt prefix). The new provider's parser is what keeps its own agent's slash-command and system scaffolding out of `SessionData`.
- IDE targets: workspace minted if the folder was never opened (with the IDE's exact id scheme), the running app detected and the user asked to quit it before writing (skipped with a warning when stdin is not a terminal, so automation is not wedged), sidebar indexes updated in one transaction, restart note worded per OS ("Isn't Cmd+Q a mac specific affordance?").
- The error for the unfixable case is friendly and carries the remedy: "It's a scary error and what the user needs to do is buried behind error text."
- By-id lookups on a global store verify project ownership; returning another project's session by id writes one project's conversation into another's history, which is corrupted output, not just a bad lookup.
- Every native-store write path (index rows, sidebar registration, workspace minting, empty-store creation) ships with its test against a synthetic store in the same change.

### 4.9 Check and analytics

- Canonical `Check` shape: parse command, build an `analytics.CheckAttempt`, `exec.LookPath`, probe `--version` capturing stdout and stderr, `spi.ClassifyCheckError`, `"unknown"` for empty output, exactly one `TrackCheckFailure` per failure branch and one `TrackCheckSuccess`.
- No inline `analytics.TrackEvent` in a provider, no provider-invented `error_type` strings, no analytics for hidden or internal flags ("no on MIP 2 (this is an internal flag, no analytics desired)").
- IDE providers probe a store, not a binary, so `CheckAttempt` does not fit; they still emit exactly one check event per outcome (success with `location`, failure with a store-shaped `error_type`) in the shape the existing IDE providers use, and never spawn the IDE's CLI, because `Check` gates availability in search and the session picker across every registered variant. Raise the shape with the maintainer rather than accept no analytics or a CLI-shaped fake.
- A new provider adopts the shared analytics shape immediately; existing providers' telemetry is changed only with dashboard review. New event names and property keys go in `docs/POSTHOG.md`; value changes go in the refactor ledger.
- Analytics cannot be observed by any test (`TrackEvent` is a no-op in dev and test builds), so verify the check events by reading the provider's `Check` against `pkg/analytics/check_events.go` and the reference providers; do not request a test for them.

### 4.10 Logging

- `Check`, `ExecAgentAndWatch`, `WatchAgent` log at Info on start, each transition, and exit, at the density of the reference provider. The instruction the maintainer adopted for DeepSeek (the review's own wording, pasted back as the order): "Add lifecycle slog.Info logging in ExecAgentAndWatch and Check ... Compare to claudecode's logging density." Missing logging in `Check` is a defect because analytics captures it remotely but `--log` captures nothing.
- Repeating messages are Debug. Level equals the severity of the event, logged once at the layer with the most context.
- Never swallow errors that would otherwise be silent to the user: `_ :=` on an index load that makes sync report no sessions becomes a Debug log naming the degradation; a skipped malformed line logs the file and line at Warn ("Skipping corrupted JSONL line" is the established message shape; a bare `continue` is the defect).
- Call `slog` directly; no package-local logger wrapper or adapter ("just use slog like we do everywhere else"). `fmt.Print` only inside detection help, and prefer `log.UserMessage` and `log.UserWarn` there; the IDE providers' "waiting for the IDE to open this project" line is the other accepted terminal output.
- Structured keys are camelCase: `error`, `path`, `sessionId`, `projectPath`, `command`, `resolved`, `version`, `exitCode`, `debugRaw`. Messages are prefixed `Method:` for SPI entry points or `<label>:` for internal code.

### 4.11 Performance

Taken when the fix is also a simplification or the cost is on a hot path; skipped when it adds caching complexity for an unmeasured cost.

- No whole-file body retained by a listing path that only needs metadata.
- Direct `os.Stat` for a file whose name is derivable, not a directory scan.
- No per-call map literals in a classifier; no per-row recomputation of a value that is constant per session.
- Session files read through `spi.ReadRecordLine` at `spi.MaxRecordLineSize` (16MB), one constant for every provider; the maintainer's ruling when Copilot flagged a 250MB cap was to match the siblings ("For #2 let's do the same as anti/deepseek, at 16MB"). The size check happens before the record is materialized, which is the whole point: a cap tested against a finished line has already made the allocation it was meant to prevent.
- `RawData` assembled from the parsed records, not a second read of the file.
- Reindex enumeration in parallel; by-path reads instead of by-id walks.

### 4.12 Security and robustness

Rate findings against the boundary the CLI actually defends. It reads files the user's own agents wrote, on their own machine, as them. Data already on the local disk is not a threat to that machine, and anything able to place a file or a symlink inside the agent's store already holds the access it would be trying to gain, so a symlink under the store is the user pointing at their own files, not an escape. The boundary runs between this machine and everything outside it: what leaves for SpecStory Cloud, and untrusted session content arriving from anywhere. A finding that assumes a local attacker is re-rated down to robustness, with that reason written in the ledger; a finding about what reaches the cloud, about content executed or interpolated rather than parsed, or about an unbounded allocation from a hostile record keeps its severity.

- The provider is read-only against the agent's store on the read paths. A SQLite handle is read-only only when the DSN is `file:<path>?mode=ro&` plus `spi.BusyTimeoutPragma`; the driver ignores `mode=` on a bare path and opens read-write-create (the Cursor CLI review found this when a test opened a nonexistent path and got an empty database). The one sanctioned write on the read side is `spi.EnsureWALMode`, once per database at watcher startup. Where the provider must write (IDE resume), it uses busy timeouts, transactions, idempotent inserts, and checkpoints, and it never writes while the app is running.
- Redaction is central in `pkg/redact`, applied by `pkg/session` and `pkg/cloud`; providers do not touch it. The maintainer's risk asymmetry: a secret in the cloud is worse than a rare local rendering break.
- A capped scanner belongs on sidecar and index files where a bad line can be skipped; the primary session file goes through `spi.ReadRecordLine` so that one oversized record degrades to one bad record, never an aborted file, because `ErrTooLong` stops the scan and loses everything after it (a streaming-parse suggestion was rejected on that ground; the right response was a rename and an honest comment). Each provider carries a test proving the records on both sides of an oversized one survive. When a record holds `json.RawMessage`, unmarshal from a copy of the line (`scanner.Text()`, never `scanner.Bytes()`); the scanner reuses its buffer and records outlive the loop.
- No user-identifying data in generated artifacts.
- A discovery heuristic that scans other users' profiles (the WSL `/mnt/c/Users` first-match scan) is raised as a ledger item: refuse when ambiguous, or document the trade-off at the decision site; the maintainer decides.
- The providers' symlink and path checks keep enumeration inside the store being scanned, so one project's sessions never land in another's history. They are correctness controls; do not accept or file them as security controls.

### 4.13 Tests

The five criteria from `pr-review.md` apply: not trivial, not tautological, well-designed, reliable, not repetitive. The maintainer's refinements:

- Delete tests that cannot disagree with the code, including ones a reviewer wrote. The DeepSeek instruction, adopted from the review's own MIP wording: "Delete tautological tests (T1, T2, T3, possibly T4) — they assert literal constants and dilute the signal of the rest of the suite."
- The same treatment for a test that never creates the condition it is named for and exercises the ordinary path instead. It is harder to spot than a constant assertion, because the name and the setup read as though the hard case were covered, and it deters anyone from writing the real one. The tell is a comment conceding the limitation, as in a size-limit test whose body explains that the limit cannot be reached in a unit test and then parses a normal file. Delete it, or lower the threshold until the condition is affordable to build and assert the real behavior.
- A non-assertion (`t.Logf("accepted")`) becomes a real assertion.
- Table-driven "where needed and would benefit from them", not blanket; an integration test may stay standalone.
- Fixtures are raw shapes captured from the real session; tests assert what must not leak (`[topic]`, `diffLines`, `systemPrompt`, a raw json fence).
- Every parser, renderer, or native-store write fix ships with its test ("suspect some tests would be good, no?", "tests?").
- Combinatorial logic (resume-argument parsing) and non-obvious heuristics (a basename-occurrence threshold) get tests; guard clauses and file-not-found paths do not.
- An exhaustive sweep driven by an external inventory (the agent's tool list) asserting the expected type per name is not tautological ("yes we want the full sweep to prevent any known tools rendering as unknown.. not tautological").
- When a fix adds a guard (project ownership, adopt-versus-never-republish), the test also proves the guarded path still works for the legitimate case, or the guard may have disabled the feature rather than scoped it.
- Tests move with the code they test; provider-local duplicates of shared-helper tests are deleted.
- Test inversion (flip assertions to prove they bite) is the maintainer's own pre-review step; do not repeat it in the review unless asked ("Already done the test inversion testing.").
- No injectable interface or wrapper whose only consumer is a test, and no seam without a test. A package-level root-path variable that lets a test point the store at `t.TempDir()` is the accepted form.
- Any test that enables debug output first calls `spi.SetDebugBaseDir(t.TempDir())` so nothing is written into the package directory.
- A test cleanup that waits on a channel a callback closes bounds the wait and waits only if the callback started; the `-timeout 5m` on both CI test steps is never removed.

### 4.14 Naming and layout

- Package named for the product, lowercased and unspaced ("it should be pkg/providers/deepseektui, update everywhere"); registry id short; user-facing ids, TOML keys, and display names unchanged by a rename.
- Brand casing in Go identifiers ("Let's use DeepSeekCmd for (3)").
- Names telegraph the difference between two similar helpers (a strict store-layout parser versus a take-anything fallback).
- Field names read well ("InvocationAt ... could be InvokedAt for better grammar").
- Package-private constants unexported.
- Test-only helpers under `internal/`.
- Modern idiom: `any`, not `interface{}`, and one spelling per package ("Pick one"); `slices.Concat` and `slices.Clone` instead of an `append` chain that could alias the caller's slice, with the why-comment; comma-ok results only where the bool is used; no shadowed `ok` or `err`. Modern-Go rewrites are taken on request ("address 64 w/ modern Go").

### 4.15 Most important improvements

Close the ledger with two to five most important improvements (MIPs), each naming the observation numbers it rolls up, and a test assessment. Recommend whether to hold the PR and on which items.

## 5. Working the ledger

### Order

Observed ordering across reviews:

1. README and docs first when the provider is missing from them ("update the README" was the first instruction after the Cursor IDE review; the Droid review started with a docs commit before touching code).
2. The functional bug in the headline feature, then DRY.
3. Cheap, contained items in any order: dead code, honest data, comments, log levels, naming.
4. The two or three hardest watcher changes last, back to back, or in their own session with a written plan ("Implement the following plan:").
5. A big refactor is cut out of the review session into its own effort with a plan document.

### Selection: what to take and what to skip

The maintainer takes concrete code-level items and leaves speculative ones. Calibrate on these.

Taken: dead code and seams; determinism (sorted map iteration, one total-order comparator); wrong or lying comments; log-level corrections; missing lifecycle logs; DRY within the package and hoists to `pkg/spi`; honest values; robustness against another app's live database (busy timeout); real bugs found by smoke tests; naming nits worth a one-field commit; single-exit consolidation where a function has more than one non-guard return ("Consolidate returns (13)"), including extracting a small helper when that is what removes the extra returns; real-but-low-probability correctness items from bots (a fingerprint collision); layering hygiene (the cmd layer must not import a concrete provider for session processing; the existing imports for user-data-dir overrides and Cursor cwd recovery are the accepted exceptions); tests for combinatorial logic; pre-existing sibling bugs when cheap and symmetric ("for both providers").

Skipped, usually by silence: design-smell suggestions with no bug; theoretical gaps ("extremely unlikely"); process nits about the PR description; comment-wording polish; cosmetic residue that comes from the agent's own data; inherited cross-provider warts that belong to a separate change; documented deferred debt; external fact-checks; restructuring for its own sake (map versus switch, parameter struct, single-exit rewrites whose only extra returns are immediate guard clauses); speculative scaling limits; test suggestions that would need an injectable seam.

Roadmap context overrides the checklist: "leave all the Windows stuff alone... this provider is used with a Windows support branch that'll soon get merged in".

When the answer to "why is this here" is "intentional" and the reasoning holds, no change ("Tell me about this: ... consider if this is intentional" was answered and accepted).

### Batch discipline

- Work items in the order given. Paste the exact observation text back when it is not a numbered MIP so there is no ambiguity.
- After each batch: `gofmt -w .`, `golangci-lint run`, `go test ./...`, `go build -o specstory`, re-sync a real session and diff its markdown. When a batch touches `pkg/spi` rendering helpers or `pkg/session/markdown.go`, re-sync every provider's committed enumeration session with the rebuilt binary and diff against the committed history; only intended changes may differ. Report "what changed and why" and "still open". The maintainer commits.
- Mirror every fix into the sibling provider the code was copied from, in the same batch ("fix deepseektui too", "check and mirror the fix if needed"). When a finding is a pattern rather than a provider bug (`wg.Add`/`Done` pairing, curl timeouts, watch startup policy), fix it everywhere it occurs and add the rule to `CLAUDE.md`, `.claude/commands/pr-review.md`, and `.claude/commands/code-review.md` so the next review checks it.
- Re-run the review after the first batch, with fresh numbering. A different model is fine. Address the second pass the same way and correct the reviewer when its framing is wrong.
- Ask "where do we stand" checkpoints before moving phases ("Where do we stand with the MIPs?").

## 6. Behavioral test matrix

Every command is exercised against the real agent, at the version shipping this week, from scratch project directories under `compositions/`, invoking the freshly built `./specstory`. Failures come back as pasted terminal output or screenshots and must be reproduced in a sandbox before they are fixed (`XDG_DATA_HOME` or `HOME` redirected to a temp dir, a fixture transcript, `./specstory watch <id> --console --debug`). State the operating system each row ran on.

Common flags: `--no-cloud-sync --no-usage-analytics` unless cloud sync is under test; `--log --debug` for anything being diagnosed; `--debug-raw` whenever markdown quality is being judged. Because the matrix runs with cloud sync off, confirm `RawData` by reading the conversion function, or run one sync with cloud enabled and check the upload log.

Artifacts to inspect: `.specstory/history/*.md`, `.specstory/debug/<session-id>/session-data.json` and the raw files next to it, `.specstory/debug/debug.log`, `.specstory/statistics.json`, `~/.specstory/sessions.db`, and the agent's own native store.

### check

Commands: `check`, `check <id>`, `check <id> -c "/abs/path"`, `check <id> -c "missing"`.

Correct: version and location printed (IDE providers omit the version line because they never spawn the IDE's CLI); the failure message names the command actually attempted, including a custom one, and gives install guidance; the success hint advertises a command that works; exit code 2 on a single-provider failure.

### sync

Commands: `sync`, `sync <id>`, `sync -s <session-id>`, `sync <id> -s <session-id>`, `sync -s <session-id> --print`, `sync --debug-raw --log --debug`, `sync --only-stats`; then `sync`, `list`, and `watch` from the scratch project reached through a symlink and from a path containing a space, a special character, and an underscore.

Correct: one file per non-empty session; progress counts reach the total; a second sync with no agent activity reports every session as up to date and changes no bytes (the maintainer checks `git diff` of the history directory: "We can't have this indeterminent ordering...."); sessions with tool calls, compaction, slash commands, and an empty first turn all render sensibly; `--print` writes nothing; `sessions.db` gains a row per session; the symlinked and special-character paths find the same sessions as the canonical path; from a directory with no sessions, `sync <id>` prints the provider's own guidance and exits 0.

Regressions to re-test: header-only markdown after an agent format change; markdown churn from fence sizing or divider changes; relative paths turning absolute after a differently-cased cwd; a session from project B written into project A by id.

### run

Commands: `run <id>`, `run <id> -c "$(which <agent>)"`, `run <id> -c "<agent> --resume"` with `--resume <session-id>` (bare-flag repair), `run <id> -c "<agent> --resume <old>" --resume <new>` (the requested id must win), `run <id> --resume <session-id>`, `run <id> --log --debug --debug-raw`, `run <id>` with `<id>_cmd` set in `.specstory/cli/config.toml`, `run <id>` from a project the agent has never seen (move its per-project store aside first), and bare `run` plus `run --help` (the default is the alphabetically first registry id, so a new id that sorts first changes it; the README says the default is Claude Code).

Correct: the agent owns the terminal (no interleaved output for CLI agents; per-save lines for IDE providers); markdown appears after the first turn and grows, including in the never-seen project; per-record debug files appear for the live session under `--debug-raw`; on exit the CLI returns the agent's status and the last turn was saved, including on a non-zero exit; `debug.log` shows launch, watcher start, each detected change, and the final save.

IDE providers: the IDE opens the project directory itself (not its home screen); a missing launcher prints the install instruction; the wait for the workspace shows progress; a launcher with `--wait` does not stall the watcher.

### watch

Commands: `watch`, `watch <id>`, `watch <id> --json`, `watch <id> --debug-raw`, `watch <id>` in a project the agent has never seen, `watch --output-dir <dir>`.

Correct: the banner lists every registered provider (IDE variants appear only when they hold data); nothing is printed or rewritten for existing sessions; the first new update prints and saves; a session created in a new day directory (or any directory created after the watch armed) is picked up; Ctrl-C exits 0; if no watcher could start the command errors rather than exiting 0 with a banner. Over a long watch, `lsof -p <pid> | wc -l` stays flat.

### resume, same agent

Commands: `resume` then pick a session and choose the same agent; `resume <id>`; `resume <id> --session <session-id>`.

Correct: "Resuming <Agent> session <id> in place..."; the agent opens with the prior turns and answers a question about them; the same native file grows; the same markdown file updates; the index row's timestamp advances.

### resume, cross-provider from the new agent

Commands: `resume claude` picking one of the new agent's sessions; `resume codex --session <session-id>`; `search <phrase from the session>` then `r`.

Preconditions: a session with plain turns, thinking, at least one tool call, and a slash command.

Correct: "Reconstructed <Agent> session into Claude Code as <new session id>."; the first assistant turn is the migration note; every user prompt is present and every user turn was typed by the user (the shared filter strips only Claude Code's markers, so the new provider's parser must have excluded its own scaffolding); tool calls appear as their rendered summaries; a new native file exists whose first record carries `specstorySourceSessionId`; a new markdown file and index row appear for the target session.

Failure to recognize: "source session has no data to reconstruct" means the new provider's by-id lookup could not map the index row's origin cwd back to the session.

### resume, cross-provider into the new agent

Commands: `resume <id>` picking a Claude Code session; `resume <id> --session <session-id>`; from another project via `tab`; for a provider that cannot reconstruct, a cloud copy of its own session must not list it as a target.

Preconditions: a project the agent has never seen. `NativeSessionPath` only resolves the path; `resume` creates the directory before writing, so a provider whose `NativeSessionPath` has no filesystem side effects is correct.

Correct: the agent opens with the prior conversation and answers a memory question from it; the agent shows no warning about the session file (the maintainer tested resume into Muse and asked to synthesize a clean stop when Muse warned about a crash); the agent appends to the reconstructed file rather than starting a fresh session under another id; markdown and index rows appear for the new id. IDE targets: the session is listed in the IDE's own panel, opens with content, and survives backing out, after a full restart of the IDE ("There at the start / Content when I clicked in / Still there when I backed out").

Failure to recognize, IDE targets: the row is in the database but the panel stays empty after a full restart, or the session loads then vanishes. Stop guessing at fields. Grep the app bundle for the key and table names it reads (an IDE often gates on a different index than the one you wrote), read its deserializer and window log (one throw on any part drops the whole session, and the app then deletes it), check how it discovers sessions (an index row, not a directory scan), and compare a minted entry byte-for-byte with one the IDE wrote itself. Do not use older session files as format references.

Verify a resumed session actually loaded: the console line, the native file present and growing, the in-agent context question, the migration note visible, the target markdown file, the index row, the exit status, and any agent-side warning captured verbatim.

### list, search, reindex

`list <id>` shows every synced session with the same slug as its markdown filename; `list --json` is well-formed; sessions with no user prompt are absent; from a directory with no sessions the provider's guidance prints and the exit is 0. `search` finds a distinctive phrase, previews the real markdown, and resumes through the same path as above. `reindex` counts the new agent's sessions across all projects, attributes them to the right project (an `unknown` project count means a transcript with no recorded cwd), and a second run reports everything up to date. `reindex` output wording is user-facing and gets reviewed too ("\"unattributed\" here is vague. \"unattributed sessions\"").

### Reporting a failure

Report with the terminal transcript or screenshot, the markdown line number, and the raw data file and line that should have produced it ("The data for it is here: `.../debug/<session-id>/22.json`"). Then reproduce in a sandbox, fix, prove the regression test fails without the fix, and ask the maintainer to re-run the manual test.

## 7. Tool enumeration and rendering audit

This is the maintainer's standard for "the markdown is right". The procedure lives in the `specstory-cli-multi-agent` repository's README; the factory's TOOL-AUDIT workflow inherits its enumeration half.

### The ritual

1. Create `<agent-name>/<YYYY-MM-DD>/` in the multi-agent repository (hyphenated product name, for example `muse-code`).
2. Run the agent directly in that directory, not via `specstory run`, with: "Hello <agent>, tell me all the tools you have access to. Write all the tool names to the file ./tools.txt." then "Please use each of your <N> tools one-by-one to show me how they work and how you use them." For agents with a tool-search tool, extend the first prompt to ask for any deferred tools it can load or discover through a tool search.
3. Write `versions.txt` with the agent's version banner as printed by the binary and the CLI version under test.
4. From that directory, with a freshly built binary: `./specstory sync --log --debug --debug-raw`. Re-run it after every renderer change.
5. Commit the history file as the baseline for future regressions.

### The inventory

Where the harness declares its own tools (a stream init event in headless mode, an extension hook, a `--list-tools` flag), that declaration is the inventory and the model's self-report is a lower bound; Antigravity's binary declares 57 tools where its self-report named 19. Strip self-report namespace prefixes (`functions.`, `muse.`) before comparing, because session data records bare names. Tools that appear in the session but not in `tools.txt` are inventory additions to record, not junk. The delete rule in 4.3 grounds on that union.

### Coverage

Diff the inventory against the `data-tool-name` attributes in the markdown. "What tools hasn't it tried to use yet?" Account for aliases the agent used. Count raw tool calls against rendered blocks; they must be one to one, in the order the agent recorded them, with no phantom blocks leaking from tool output that quoted other transcripts. Diff the inventory against the format doc's tool list as well and correct the doc. Capture at least one error envelope per renderer in the enumeration session.

### The audit

Run the audit prompt from the multi-agent README: for every tool-use block, grade against the raw data as formatted (all important data present and pleasantly presented), partial (formatted but missing important elements), raw or unformatted (raw JSON, Go map formatting, IDE-internal node-tree JSON and `$mid` URIs, empty-label links leaked from the IDE, or unfenced output), or missing, in a table with tool name, markdown line, grade, data file, data line, and comment. Add a catalog split into fixes (correctness and data loss: results attached to the wrong call, tools dropped, unknown types, control bytes leaking) and improvements (formatting quality). Separate the agent's own artifacts (its redaction markers, HTML entities, encrypted reasoning) from renderer defects.

### The fix loop

- Fixes first, then improvements ("Let's start with F1-F4", then "OK, let's address I1-I9 now"); the generic layer first, then bespoke handlers only for tools whose data proves they add something ("Any tools which seem especially important and the JSON contains important stuff we're missing that would benefit from a custom tool handler?").
- Read the other providers' formatters before writing new ones so the house style stays one style.
- Only render shapes that were observed. Defer anything whose success envelope was never captured to the next run ("Yes, 1-3" took only the items observed data supported). A renderer matches the observed shape and otherwise degrades to `spi.RenderGenericJSON` (body) or the default result formatter, so no input is lost; a sub-field never observed renders as JSON rather than being guessed.
- After a fix, ask what remains and go through the residue one by one ("tell me about the 5 😢"), with line numbers and a before-and-after preview before cosmetic polish ("what would 265-275 look like after the polish?").
- Add the full-inventory classifier test asserting the expected type per enumerated name, and confirm an out-of-inventory tool name still renders visibly with no allow-list. The house rule is an `unknown` default; where a provider defaults to `generic`, a not-unknown sweep can never fail, so the sweep must assert the type per name.
- Pair the sweep with the inverse check: every bespoke renderer key equals `spi.NormalizeToolName` of a name in the inventory, or each renderer has a test driven by a captured invocation; a key nothing produces is a dead renderer for a real tool (Antigravity shipped renderers keyed `websearch` for a tool that normalizes to `searchweb`).
- Correlation bugs are parser fixes, not renderer fixes: the parser orders records by the agent's own sequence field, not file order, because agents flush asynchronous results ahead of the call that owns them; identifies result records by excluding the known structural types, not by an allow-list, so a new dedicated result type degrades to a generic result instead of vanishing; pairs results to pending calls by tool type or id, with first-in-first-out only as the fallback for several in-flight calls; and ships a scrambled-order regression test. The parser's kind switch enumerates every kind observed in real data, rendering it or naming it as known-nothing-to-render with the reason; "phase 2" stubs for kinds that carry user-visible data are not shipped.

### The rendering standard

General: for any known tool, scalar inputs render as `Label: value` lines ("Skill: agent-browser", "Path: ..."); a pretty-printed `json` fence is the last resort; maps and slices are JSON-marshaled, never formatted with `%v`. A failed call renders the agent's error text, and the error branch takes priority over the success formatter for every tool; an error shown inline grades as formatted. Narration and tool blocks appear in the order the agent recorded them, not tools appended after the text; an invocation the agent re-serializes on every state update renders once; results pair by exact id before any positional claim; thinking is not duplicated into the body when the agent stores streamed narration as both. Any cleanup that strips whitespace, indentation, or boilerplate is scoped to the result type where the noise was observed and removes only what the agent added (the block's shared indent, not every leading tab), with a test that feeds content legitimately containing the stripped characters. A malformed element (a todo item that is not an object) is skipped, never rendered as a placeholder row. The tool type reflects what the tool acts on, not its verb; rendering dispatches on the tool name, so a generic-typed tool may still render as a fence or diff.

Per category:

- Read (`read`): path in the summary, content in a fence tagged by extension.
- Write (`write`): `Path:` line, full content in a tagged fence, trailing newline trimmed, one-line result.
- Edit (`write`): `Path:`, the change as a `diff` fence, the tool's own result diff also as a `diff` fence with native markers stripped.
- Search (`search`): pattern and path, hits in a `text` fence (they are arbitrary content and can contain markdown).
- Shell (`shell`): description, command in a `bash` fence, `Directory:` when relevant, output in a `text` fence, exit code only on failure, PTY control bytes sanitized, background and terminated states stated.
- Web: query in the summary; results as a linked list with a count; fetched bodies fenced; fetch is type `read`, search is type `search`.
- Todo (`task`): `- [x]` / `- [ ]` items with the real item text (check the item key).
- Subagent (`generic`): the ask as prose; the result as status plus the child's summary in a blockquote; a full system prompt under a bold label, never nested `<details>` ("Short MD is not a goal. Completeness is.").
- Ask-user (`generic`): bold question, option bullets, bold answer.
- Agent-state stores (memory, skills, goals, cron, peer sessions): `generic` even when named read or write ("I meant the trio as generic").
- Unknown: pretty-printed JSON in a `json` fence with agent-injected label keys dropped; `unknown` only for unenumerated tools. The list-directory type is unsettled across providers; record the maintainer's ruling when made.

## 8. Cross-platform

Native Windows is a release target and the CI workflow runs the full test suite on `windows-latest`; nothing in branch protection enforces it, so the reviewer confirms the job passed. The rules are in `CLAUDE.md` and the shared helpers; the recurring failures are in the Windows fixes of PR #191 (Appendix C).

Static checks to run on the provider package. The first five must be empty; read every hit of the last one and confirm each joins a local path:

```zsh
grep -rn 't.Setenv("HOME"' --include='*_test.go' pkg internal
grep -rn 'os.Chdir\|os.MkdirTemp' --include='*_test.go' pkg/providers/<p>
grep -rn 'Getenv("HOME")\|Getenv("USER")' pkg/providers/<p>
grep -rn 'pgrep\|"sh"\|"/bin/' pkg/providers/<p>
grep -rn 'HasPrefix(.*, "/")\|strings.Split(.*"/")' pkg/providers/<p>
grep -rnE 'filepath\.(IsAbs|Join|Abs|Rel|Clean|Dir|Base|EvalSymlinks)' pkg/providers/<p>
GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...
```

Review rules:

- Paths from session data are handled by shape (`spi.NormalizePath`, `spi.FileURIToPath`, `filepath.ToSlash` plus substring checks); local paths use `os.UserHomeDir()` and `filepath.Join`; IDE-style providers branch on `runtime.GOOS` and honor `--user-data-dir`; WSL reads the Windows side via `spi.IsWSL` where the tool stores data there. An absolute-path check accepts both shapes, `strings.HasPrefix(p, "/") || filepath.IsAbs(p)`; a `/`-only check rejects every native Windows path (provenance was silently disabled on Windows by one).
- The provider's cwd-to-store-directory encoder reproduces the agent's own algorithm, including its symlink resolution; the local project path (cwd, `--project-path`, `--output-dir`) is canonicalized once at the boundary to its on-disk spelling; a recorded path from session data or a remote (SSH, WSL) path is never case-folded, `Abs`ed, or canonicalized, because remote filesystems are case-sensitive and folding collides distinct directories.
- Build tags only where the syscall type differs; `runtime.GOOS` switches for layout; shape checks for data.
- Raw JSONL scans for a path also match the backslash-escaped form.
- No `pgrep`, `sh -c`, `/bin/...`, or `USER`. Username comes from `os/user.Current()` with the `DOMAIN\` prefix stripped ("94 stdlib"); `USER` or `USERNAME` only as the fallback. Process detection on Windows uses `tasklist /FI "IMAGENAME eq <exe>" /NH`, and the hit test is the image name appearing in the output, because `tasklist` exits 0 and prints a localized "no tasks" message when nothing matched.
- Any URI written to an IDE store goes through `vscode.WorkspaceURIMap` or `vscode.PathToFileURI`; never hand-roll the `workspaceIdentifier.uri` fields.
- Tests: `testutil.SetHome` (it leaves `%APPDATA%` untouched by design, so IDE-style providers point at a fake install with the package's `SetUserDataDirOverride` plus a `t.Cleanup` reset), `testutil.JSONString`, `testutil.EqualPaths`, `t.TempDir`, `t.Chdir`; expectations via `filepath.Join` or `filepath.FromSlash`; `t.TempDir()` is a symlink on macOS and an 8.3 short name on Windows, so canonicalize the fixture too and compare canonical to canonical, and create the files an attribution test references; a hardcoded hash of a Unix path literal is pinned only off Windows, with a shape assertion there; when an expected value legitimately differs per OS, compute `want` from `runtime.GOOS` instead of skipping; documented `t.Skip` only for chmod semantics and POSIX shell fixtures; deadline polling for fs events, never fixed sleeps; never a `"file://" + path` splice; Windows-shaped table rows so Windows behavior runs on macOS.
- Fixture choices favor real Windows-shaped native paths over dodging them: the maintainer chose JSON-escaping the substituted path over forward slashes, and making the function slash-tolerant over changing the test.
- CI lints on Linux only; when a provider adds a `//go:build windows` file, run `GOOS=windows golangci-lint run` locally.
- Code paths unit tests cannot reach (process detection, launchers, per-OS quit gates, WSL discovery; CI runs Linux and Windows, never WSL) are either smoke-tested on that OS or listed in the handoff as untested.

Process: CI runs only on `pull_request` (path-filtered; docs-only changes run nothing) and on pushes to `dev` and `main`, so open the PR to get a run. Pull failures with `gh run list -R specstoryai/getspecstory --branch <branch>`, `gh run view <run-id>` to find the "Test (Windows)" job, then `gh run view --job <job-id> --log | grep -E '^--- FAIL|FAIL\s|panic:'`; the log can come back empty immediately after completion, so retry. Before burning down by bucket (home dir, JSON fixtures, path expectations, chdir, chmod, hashes), classify each red test as a product defect or a fixture problem; shapes that have presented as test failures but were product bugs include a `/`-only absolute-path check, a URI decoder that strips the root from Unix-shaped paths, `filepath.Abs` stapling a drive letter onto a recorded Unix cwd, raw JSONL scans that miss the escaped form, and an empty origin cwd for Windows workspaces. Batch the last fixes into one run you believe is zero ("No, I don't want to do a run for 11->10... this takes too long."); keep no burn-down narrative in comments or docs.

## 9. Bot review triage

GitHub Copilot reviews every PR, often across dozens of rounds. Its comments are inputs for issue identification, never patches to apply ("Ignore their patches... it's more about issue identification"). Nothing it says is taken as correct because it said it; every finding is confirmed, re-rated, and then either fixed or rejected on the record.

1. Pull every review comment with `gh api --paginate repos/specstoryai/getspecstory/pulls/<n>/comments` (the REST endpoint carries no resolved flag; thread resolution and thread ids come from the GraphQL `reviewThreads` query) and present them as one numbered list in order, each with: already resolved, or real issue, or not an issue; and if real, what to do.
2. Do nothing until told which numbers to act on ("Don't DO anything with them yet.").
3. Check what the bot was looking at before triaging anything. A review is pinned to the commit it ran against, and later commits routinely fix what it flagged, so compare its findings against the branch as it stands now and mark the ones already handled. Skipping this costs a round re-fixing solved problems and, worse, invites a fix that reintroduces something.
4. Every thread ends resolved, with a reply saying which way it went. Agreed: fix it, then reply naming what changed, then resolve. Disagreed: reply with the technical reason, then resolve. A thread resolved silently leaves the next reviewer, and the next bot round, no way to tell a considered rejection from an oversight. Where the finding described real behavior but the behavior is intended, say both: that the report is accurate and why it stands.
5. Verify each claim against the code and real data before rating it, rather than reasoning from the comment. A throwaway test inside the package that plants the condition and reports what happens settles most of them in one run and is deleted afterwards; it also catches the finding that is real but scoped differently than described. Re-rate the bot's severity with a reason (a prior review wrote that a High rating overstated a robustness concern that was not a security boundary; see 4.12 for which concerns are security concerns at all).
6. Reject wrong ones with a one-line technical reason (a prior review rejected a nil-map guard because `delete` on a nil map is defined as a no-op in Go); leave a documented trade-off where the concern is real but out of scope. Check the remedy as well as the diagnosis: a comment can identify a real defect and propose a fix that does not address it.
7. Act by number; write your own fix; mirror into the sibling.
8. Keep re-pulling ("Check the latest Copilot feedback too. It's got some new stuff.") and expect the maintainer to paste individual comments with a confidence flag ("may or may not be right").
9. Accept trivial suggested edits (a stale doc path in a comment) as-is; they arrive as co-authored commits.
10. Old comments the contributor deferred are still fair game if the finding was real (the `isEmptyCapabilityBubble` extraction landed six months after the comment).

## 10. Factory affordances

Every new provider ships bash sensors under `pkg/providers/<package>/factory/` (the package directory, not the registry id). Enrollment is file presence: `latest-version` enrolls the release sensor; `install` plus `list-tools` enroll the tool audit. The reference implementations are `pkg/providers/claudecode/factory/` and `pkg/providers/antigravitycli/factory/`; their header comments are the contract. A terminal agent may ship `latest-version` alone when it has no automatable headless mode or no credential-free install, and its header must say which; IDE providers always do. When `install` and `list-tools` are absent, skip their validation and record the reason in the ledger.

These scripts run unattended in factory CI on every merge to `dev`, and this review is the only trust gate; `install` and `list-tools` run with the factory's vendor keys in the environment. Read each script for: every network destination is the vendor's canonical channel named in its header; nothing is piped to a shell except the vendor's own pinned installer; no reads of `~/.config`, keychains, or environment variables beyond the one named credential; nothing written outside `$HOME` and the working directory; nothing sent anywhere.

### Contracts

- Shebang `#!/usr/bin/env bash`, `set -euo pipefail`, git mode `100755`.
- Header: a title line; a `Channel:` (sensors, install) or `Source:` (list-tools) paragraph naming the exact URL or package, why it was chosen, what was rejected and why, and the expected failure mode; a `Factory SPI contract:` paragraph in the established wording; why-comments on every non-obvious line (auto-update variables, `--engine-strict`, `jq` over `sed`).
- `latest-version`: no arguments, no credentials, stdout is the reading, exit 0; empty extraction converted to a non-zero exit with a stderr diagnosis; every curl carries `--connect-timeout 10 --max-time 30 --retry 2 --retry-delay 2`; output byte-stable day to day (no build counters or shas unless they are part of the string the binary prints for `--version`, and the header says so when the reading carries a suffix); prerelease and dist-tag policy stated. Accepted channels: an npm `latest` dist-tag; GitHub `releases/latest` resolved by a `curl -sI` redirect; the vendor's installer script read as the release manifest with the "installer format changed" failure pre-declared. Rejected: the GitHub API (unauthenticated rate limits on shared runners) and any tool needing auth in the credential-less step. Assume only `curl`, `npm`, and `jq`. For composite output the first line is the release-meaningful one: the factory uses it for the issue title and passes it to `install`.
- `install <version>`: receives verbatim the first line of `latest-version`'s stdout (no `v` on one side, no build suffix on one side); installs under `$HOME` at `$HOME/.local/bin/<binary>`, disables self-update at read-back where the agent has one, prints the version read from the binary as the only stdout line (installer chatter goes to stderr), exits non-zero on mismatch ("never close enough"); channel tracks what `latest-version` reads; carries the same curl flags on any direct download and wraps a vendor installer pipe in `timeout`.
- `list-tools`: runs in the current empty directory under an isolated `$HOME`; invokes `$HOME/.local/bin/<binary>` explicitly, never a bare name from the path, with the agent's self-update disabled for the run; prefers the harness's own declaration (stream init event, extension hook) over a model self-report; prints names one per line as the agent records them in session data; extra facts to stderr; isolation flags so user config cannot leak in; auth arrives only as an environment variable named in the header with a fail-fast check when it is unset (a browser, device-code, or keyring login disqualifies the self-report path, so use a declaration source that needs no model call or record why the provider is sensor-only); any settings file the agent needs to run headless is seeded under `$HOME` inside the script; bounded by a `timeout` wrapper or the harness's own turn limit; diagnostics left in the directory; exits non-zero rather than printing an empty or partial list.

### Validation

`timeout` is GNU coreutils (Homebrew on macOS); `shellcheck` is not installed by default.

```zsh
env -i HOME="$(mktemp -d)" PATH="$PATH" timeout 120 bash pkg/providers/<package>/factory/latest-version; echo "rc=$?"
V=$(env -i HOME="$(mktemp -d)" PATH="$PATH" bash pkg/providers/<package>/factory/latest-version | head -1)
H=$(mktemp -d); env -i HOME="$H" PATH="$PATH" bash pkg/providers/<package>/factory/install "$V" > /tmp/installed; echo "rc=$?"; cat /tmp/installed; ls "$H/.local/bin"
for n in 1 2; do W=$(mktemp -d); ( cd "$W" && env -i HOME="$H" PATH="$PATH" <VAR>="${<VAR>:-}" bash pkg/providers/<package>/factory/list-tools > "/tmp/reading-$n" ); echo "rc=$?"; done
diff <(sort -u /tmp/reading-1) <(sort -u /tmp/reading-2) && echo "readings agree"
```

Also: `install`'s stdout equals the requested version, one line; a negative test per script (broken channel, bogus version, the credential unset, which is the same command with the variable empty) must fail non-zero, never print an empty reading; compare the reading with `<agent> --version` on a fresh user-style install; confirm the tool names match the session-data spellings in the provider's `markdown_tools.go` and in a real session after stripping any self-report prefix; run `bash -n` and shellcheck (neither CI lints these files); `grep -n 'local/bin' pkg/providers/<package>/factory/list-tools` must hit. To run the factory's own harness before merge (`scripts/tool-audit-enumerate.sh` in the provider-factory repository), seed `versions/<package>` in a scratch clone with `latest-version`'s output; never hand-edit the real file.

### Cross-repository touchpoints

After merge to `dev` the factory's daily sensor creates `versions/<package>` and opens a version-changed issue. If a tool list exists from the enumeration session, seed `tools/<package>` in the factory repository so the first audit is a real comparison. If the agent needs a credential variable, add it to the pass-through list in `scripts/tool-audit-enumerate.sh`, because a variable not on that list is silently absent under `env -i`. Add a dry-run fixture and a row in the table at `Workflows/coding-agent-release/dry-runs/README.md`. Add the provider's reading source to the per-provider sentence in the factory's `TOOL-AUDIT.md`. Update the factory build plan's inventory of enrolled and deliberately unenrolled providers.

## 11. Docs and release artifacts

A provider release touches these, and the maintainer checks each. The version floor must be identical everywhere it appears, and it is the full string the binary prints for `--version`, including any build suffix.

### changelog.md

Heading `## vX.Y.Z YYYY-MM-DD` (a new provider is a minor bump; the date is the real release date and gets bumped if the release slips), section `### 📢 Announcements`, one bullet in the fixed sentence:

```markdown
- The SpecStory CLI now supports [<Agent>](<product url>) (i.e. `<binary or id>`) for sessions created from <Agent> version `<X.Y.Z>` or higher. Sessions from earlier versions may work, but are not officially supported. This provides the same support for saving to local markdown files and to the SpecStory Cloud as for <every previously released provider, linked, in the order of the previous entry, with the previous provider appended>.
```

Then a resume sentence or bullet (both directions supported, one direction, or the explicit negative with the technical reason, stating that cloud resume into the agent is unavailable too), extra capability bullets if any, and a thanks line with a PR link for external contributors only. No "Improvements" section for a provider that never shipped before; a property of a new provider belongs under Announcements. Preview the notes exactly as the release workflow extracts them:

```zsh
awk -v version="vX.Y.Z" 'BEGIN{p=0} /^## /{if ($0 ~ "^## " version){p=1; next} else if (p==1 && /^## /){exit}} p==1{print}' changelog.md
```

The provider is announced exactly once. Move the contributor's placeholder entry under the release heading rather than adding a second one; `grep -n '<Agent>' changelog.md` must hit exactly one announcement bullet, because the awk preview shows only the requested block and is blind to a leftover. The product URL and the floor are identical in the changelog, the CLI README, and the root README.

The version floor is the agent version actually run on release day, not the oldest the parser might handle and not the version the contributor tested ("why \"latest\", you know what we put in the Changelog"; a stale floor lifted from a PR description was rejected the same way).

When the new provider supersedes an existing one: a deprecation bullet in the fixed wording (deprecated, will eventually be completely replaced, remains for legacy sync and cross-agent resume), the deprecated provider dropped from the peer list from this entry on and from both README intro lists, and a docs-site name swap planned. A fix in shared rendering that rewrites existing markdown on the next sync gets a user-facing one-time note ("your existing markdown files may update once on your next sync"); every bullet states symptom, cause, and side effect.

### README.md (CLI README, repository root)

Intro sentence list; Agent Support table row (agent link, package link, data format, source location; keep the columns aligned); a prose paragraph only when the provider behaves differently from wrapping a terminal process (IDE providers: where the store is per OS, how projects are matched, that `run` opens the IDE and saves until Ctrl-C); the `[providers]` example block entry with a blank line before it; the Configuration Options row; the Debug Raw Mode provider list and file naming (stale for several providers; bring it current in the same batch); the default-provider sentence under `run` if the new id sorts first. Then "make sure we actually respect that config option in the code".

### Monorepo README.md

The ASCII diagram line, the Installation table row with Min Version equal to the changelog floor, the lead-in sentence, and a `specstory run <id>` example line.

### Code-adjacent artifacts

`pkg/config/config.go` (template, struct field, `GetProviderCmd` case and its doc comment, test rows); `pkg/spi/factory/registry.go`; the accent color in `pkg/cmd/session_tui_browser.go` chosen by the maintainer and legible on light and dark themes; `pkg/skills/agents.go` row when the agent supports agent skills (a project or global skills directory), with the `Name` taken from the public `npx skills` registry, and a ledger note when it does not; `<AGENT>-FORMAT.md` (the agent's short name in capitals) inside the provider package, with its baseline version line matching the floor and every code comment that cites it updated; `docs/POSTHOG.md` if an event or property key is new. Shared strings in `pkg/cmd` that list providers by name must be generic or derived from the registry (an earlier resume error naming "Claude Code or Codex CLI" went wrong once a third target existed).

### Outside the repository

The end-user docs site (`specstoryai/specstory-website`, `content/docs/**`) has been skipped for several releases and needs, per provider: a page `content/docs/integrations/<agent>.mdx` modeled on `antigravity-cli.mdx` (intro, Run, List, Sync, Watch, Resuming, Best Practices, Troubleshooting), the nav entry in `content/docs/meta.json`, the version line in `faqs.mdx`, the provider lists and config block in `integrations/terminal-coding-agents/{index,usage,resume}.mdx`, the lists in `index.mdx`, `quickstart.mdx`, `started.mdx`, the cloud pages, and `guides/specflow.mdx`, plus the marketing `app/**/*.tsx` pages and a logo asset. Enumerate with `grep -rl '<previous agent>' content app`. This is post-release work per `docs/CLI-RELEASE.md`, which also covers the single tag, the GitHub release notes check, and the Homebrew tap; the handoff lists the files still to change.

### Ancillary surfaces that have been missed before

Skills registry ("We added an Antigravity provider but failed to update ... pkg/skills/agents.go for it"); TUI column widths for a long agent id; the accent color pass across all providers when one is added; the root README min version; the docs site provider lists.

## 12. Final gate and handoff

Before recommending merge and release:

- [ ] Ledger: every MIP resolved or explicitly deferred with a reason; skipped items listed; no planning docs in `docs/`, including ones reintroduced by a merge.
- [ ] `gofmt -w .`, `golangci-lint run`, `go test ./...`, `go build -o specstory`, `GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go vet ./...` clean; CI green including the Windows job.
- [ ] Shared-code audit table complete; no local copies of shared helpers without a stated reason.
- [ ] Every row of the behavioral matrix run against the real agent at the release-week version, with the OS noted; both resume directions verified in the target agent's own UI; `run` and `watch` verified in a fresh project; code paths CI cannot reach smoke-tested or listed as untested.
- [ ] Tool enumeration session captured, inventory taken from the harness declaration where one exists, audit written, fixes and improvements landed, inventory sweep test present, residue listed.
- [ ] Bot comments triaged with dispositions.
- [ ] Factory scripts present, read for what they execute and fetch, validated under an isolated home, cross-repository seeds noted.
- [ ] In-repo docs and release artifacts complete; the provider announced once; product URL and floor identical across changelog, both READMEs, and the format doc, and equal to `<agent> --version` as run on release day (that reading goes in the handoff verbatim).
- [ ] Sibling providers that received mirrored fixes listed; the branch each change landed on named.

Hand back a summary the maintainer can act on without reading the session: what was fixed (grouped, with commit-ready descriptions), what was deferred and why, what was verified and how, what remains open including the docs-site files still to change, and the exact release notes text.

## Appendix A: Judgement calibration

Rulings from the record, in the maintainer's words, for when a decision is not covered above.

- "verify the code has to exist, is actually needed, and is in use" (the review checklist).
- "Better to not have a unit test than to have tests that are simplistic or tautological. We also don't test 3rd party library or language features, only our own code."
- "remove any tool renderers that are just speculative" (and the format doc written at the maintainer's direction adds: do not special-case a tool name that is not in the enumerated list).
- "hmmm why not just ditch the inference all together. Not sure we really care about -p sessions." (the format doc written afterward records: if neither stated source resolves, the workspace is unknown; do not guess).
- "Why are there these two callback paths at all? This is no good." then "Refactor now" (structural duplication is fixed on the branch, not deferred).
- "Seems have a secret in the Cloud is worse than the unlikely case that we 'break' either of these during redaction." (risk asymmetry).
- "Don't think we want the JSON validity check though... especially to do nothing w/ it but log/analytics. Seems expensive since it's JSON parsing." (no safeguards that cost more than they protect).
- "new dep. is fine / low risk behavirol diff. is fine / tiny slower is fine" (a dependency is accepted once its benefit is measured).
- "don't think 24h is defensible actually cause you're overlooking long running agent sessions and only thinking of resume".
- "We want all the system prompt in the MD. Short MD is not a goal. Completeness is."
- "a <details> tag w/in a <details> tag is unlikely to work and/or be helpful when this is renederred."
- "If we can't, we need clean output telling a user what to do. But let's see if we can." (attempt the real fix before settling for guidance).
- "We shouldn't just assume it's there for specstory run cursoride... shouldn't we check and/or catch it failing to run and tell them".
- "Let's ship w/ ErrReconstructionUnsupported" (honest capability over forced convention, after the experiment).
- "palette rebrand is fine" (harmless scope creep is accepted when its one real defect is fixed).
- "Ignore their patches... it's more about issue identification".
- "Let's rework their test scenarios to be compatible with the current code here on dev" (salvage tests from a superseded PR, not the code).
- "run and other commands don't need --providers" (provider filters belong on sync, list, check, watch).
- "no on MIP 2 (this is an internal flag, no analytics desired)".

## Appendix B: Reference implementations per pattern

Use these as the baseline for each concern. Where an older provider disagrees, it is drift, not a second acceptable pattern.

| Concern | Reference |
| --- | --- |
| Parser, per-record debug-raw numbering, watcher reconcile with a bounded window | `pkg/providers/claudecode` |
| `Check` with `analytics.CheckAttempt` and `spi.ClassifyCheckError` | `pkg/providers/deepseektui`, `pkg/providers/antigravitycli`, `pkg/providers/droidcli` |
| `Check` lifecycle logging and a `versionFlag` constant | `pkg/providers/deepseektui`, `pkg/providers/antigravitycli` |
| Watch-only startup with adopt-on-appearance and bootstrap adoption inside the window | `pkg/providers/musecode` |
| Non-zero agent exit via `spi.AgentExitError` after joining saves | `pkg/spi/exit.go` (its doc comment states the contract; no long-shipped provider is the reference yet) |
| Bounded line reading | `pkg/spi/jsonl.go` (`spi.ReadRecordLine`) |
| Capped scanner on sidecar files, `json.RawMessage` copy | `pkg/providers/musecode`, `pkg/providers/antigravitycli` |
| Honest unsupported reconstruction, enforced in the picker and preset | `pkg/providers/antigravitycli` |
| Tool renderer keys and arg keys from the enumerated inventory | `pkg/providers/antigravitycli`, `pkg/providers/musecode` |
| IDE workspace discovery, minting, launching, restart notes | `pkg/providers/cursoride`, `pkg/providers/copilotide`, `pkg/providers/vscode` |
| SQLite access | `pkg/spi/sqlite.go`, `pkg/providers/cursoride` |
| Factory scripts | `pkg/providers/claudecode/factory`, `pkg/providers/antigravitycli/factory` |
| Format spec | `pkg/providers/musecode/MUSE-FORMAT.md`, `pkg/providers/antigravitycli/ANTIGRAVITY-FORMAT.md` |
| Shared helpers and standardized behaviors | `pkg/spi` (helper doc comments), `pkg/analytics/check_events.go` |

Known drift in shipped providers that a reviewer must not treat as a baseline: `os.Exit` in the Claude Code, Gemini CLI, Muse Code, and Cursor CLI exec helpers; exec helpers that return the plain process error (Droid CLI, DeepSeek TUI, Antigravity CLI), which the CLI collapses to exit 1 with an error box; flag-style resume helpers that let a pinned id win and append to the caller's slice (Antigravity CLI, DeepSeek TUI, Droid CLI, Gemini CLI); watcher contexts created in `init()`; literal fences in some tool-result renderers (Claude Code, Codex CLI, DeepSeek TUI, Droid CLI); the Claude Code provider's inline `analytics.TrackEvent`, its `claude-code` `ProviderInfo.ID`, and its unhonored `progress` callback, plus inline `TrackEvent` with drifting keys in the other pre-refactor providers; the IDE providers' store-shaped check events, which are correct for them and not a baseline for a CLI provider; `generic` as the default tool type in Antigravity CLI, DeepSeek TUI, Droid CLI, and VS Code Copilot, and Antigravity's URL fetch typed `search`; a verbatim JSONL copy as Antigravity's debug-raw output; the Cursor CLI provider's polling watcher and its `run` that still emits existing sessions at startup; bare-path `?mode=ro` SQLite DSNs in Cursor CLI, Cursor IDE, and Antigravity CLI. When the submission copied one of these, fix the submission and note the sibling as a follow-up.

## Appendix C: Evidence index

Pull requests: #159 Cursor IDE, #160 Copilot IDE, #191 Windows and remote workspaces, #218 DeepSeek TUI, #222 Antigravity CLI, #269 Muse Code, #150 Factory Droid CLI (secondary), #241 fence sizing (inspiration, not merged), #258 Copilot all-workspaces (superseded; tests salvaged), #204 the `--providers` flag and #221 the Cursor CLI database fix (adjacent reviews of the same period).

Review and refine commits, by provider (all by the maintainer):

- Cursor IDE (#159): `713f3aa` gap analysis, `b47d66b`, `24f9937`, `8137813`, `89713d8`, `6749d30`, `5e1668d`, `60a5016`, `0bb8c42`, `57503af`.
- Copilot IDE (#160): `0bb8c42`, `d06f2f8`, `029f4ce`, `8c00845`, `2061475`, `d6caa06`, `48b947c`, `84372c6`, `0b0e15c`, `290661e`, `fdb8297`, `57503af`, `bec0205` README, `2046ca6` changelog.
- Windows and remote workspaces (#191): `5efb74b`, `a2bb733`, `39f1535`, `19fedee`, `d4c4cf6`, `f3dd227`, `adedebf`, `787aa56`, `00df199`, `f4360f3`, `546f5a0`, `25c6fc5`.
- Antigravity CLI (#222): `6ad6e24`, `f45cab4`, `13ab49d`, `d8c1c0f`, `2bc5a96`, `1e3a9d9`, `64ae209`, `98f40aa`, `f9d3e73`, `195266a`, `842a93a`, `5ff98dc`, `2d6c782`.
- DeepSeek TUI (#218): `6675fa6`, `cdb5d18`, `0a73359`, `a1c596c`, `8d9f7b8`, `195266a`, `e4b8ff2`.
- Muse Code (#269): `f5e2862`, `79dc598`, `db9b4b5`, `a82aa38`, `c031d18`, `e6d3508`, `5b8cc88`, `734fe6d`, `6c28563`, `672b496`, `e56d6cc`, `633e9a0`.
- Cross-provider: `89a254e` dedup refactor, `e4b8ff2` watch startup normalization, `57503af` fences, `6b762a4` sensor timeouts, `50fed4e` and `633e9a0` version sensors, `1e49496` factory install and list-tools.

Recorded working sessions (`.specstory/history/`):

- Cursor IDE: `2026-07-16_20-14-28Z-update-the-readme.md`; adjacent, with the `cursoride_cmd` config fix inside the secret-redaction review: `2026-07-20_12-35-45Z-diff-this-branch-to.md`, `2026-07-20_15-13-10Z-uhhhhh-not-sure-i.md`; adjacent Cursor CLI database fix (#221): `2026-07-29_16-22-34Z-let-s-address-mip.md`.
- Copilot IDE: `2026-08-04_00-58-51Z-merge-dev-into-copilot.md`, `2026-08-04_15-05-09Z-let-s-play-close.md`; Windows and remote workspaces (#191): `2026-08-11_15-10-08Z-write-this-exactly-as.md`.
- Antigravity CLI: `2026-07-21_14-19-33Z-we-re-on-a.md`, `2026-07-24_19-13-09Z-adress-a-c-108.md`, `2026-07-24_20-01-58Z-adress-68.md`, `2026-07-27_15-48-50Z-we-added-an-antigravity.md`.
- DeepSeek TUI: `2026-05-13_13-48-53Z-i-ve-got-a.md`, `2026-05-18_12-47-08Z-that-s-not-the.md`, `2026-05-18_13-34-43Z-finish-line-33-of.md`.
- Muse Code: `2026-08-14_16-04-49Z-tests-on-muse-provider.md`, `2026-08-16_12-33-25Z-let-s-address-mip.md`.
- Cross-provider dedup refactor (ran during the Muse review, with the Muse audit inside it): `2026-08-13_13-43-21Z-cross-provider-refactor-200.md`.
- Factory Droid CLI (secondary): `2026-01-28_13-36-04Z-command-name-pr-review.md`, `2026-01-28_13-42-57Z-this-isn-t-good.md`, `2026-01-28_14-55-51Z-command-message-pr-review.md`, `2026-01-29_14-35-22Z-command-message-pr-review.md`, `2026-01-29_15-58-30Z-the-droid-provider-uses.md`, `2026-01-29_16-26-43Z-command-message-pr-review.md`, `2026-01-29_17-39-47Z-the-droid-factory-provider.md`.
- Adjacent: `2026-07-29_15-13-55Z-for-mip-1-let.md` (#204 `--providers`), `2026-07-28_13-49-50Z-merge-dev-into-current.md` (a `dev` merge with conflicts explained), `2026-08-03_15-41-34Z-take-a-look-at.md` (the Codex and Claude Code watcher file-descriptor fixes).

Tool enumeration sessions: the `specstory-cli-multi-agent` repository, one dated directory per agent, with `versions.txt`, `tools.txt`, the synced history, and `tool-use-audit.md` where an audit was written.
