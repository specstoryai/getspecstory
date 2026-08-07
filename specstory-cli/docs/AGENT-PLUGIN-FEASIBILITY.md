# SpecStory as an Agent Plugin: feasibility analysis

**Date:** 2026-08-07 · **Status:** research consolidation, no code changed
**Question:** Agent Plugins is now an open standard. Vendors are moving to "agent window" products without real extension hosts. Everything SpecStory does routes through `specstory-cli`. Could an MCP server (ideally hosted) plus hooks reproduce the always-on, auto-save, auto-sync experience the VSIX extension-host process provides today — and could we do it *without* shipping the CLI binary in the plugin?

---

## 1. Bottom line

**Yes, but not the way the question frames it.** Three findings reorder the problem:

1. **A hosted MCP server cannot be the ingest path. Ever.** Every provider stores sessions under `$HOME` keyed by a local path, and four of nine key on machine-local filesystem metadata — `md5(path + macOS birthtime | Linux inode)`. A remote server has no mechanism to read a local file; MCP `roots` are "informational guidance rather than an access-control mechanism" and are deprecated. Something must run on the user's machine. This is mechanical, not policy.

2. **The stdio MCP server is the only *portable* plugin component that gets a process the client keeps alive for the whole session.** Hooks are per-turn shell-outs; skills are markdown. An stdio server is arbitrary code the client spawns and holds. If `specstory mcp` runs the existing watcher inside itself, then a plugin installed in *Claude Code* captures the Cursor IDE and Copilot IDE sessions happening beside it. That is full 9-provider coverage from a single-harness install — something no hook-based design can reach.

3. **The binary is not an implementation detail; it is the product.** ~30,600 LOC of provider parsers, four providers that need SQLite and machine-local hash salts, and a redaction engine (5MB betterleaks ruleset on a 4MB WASM RE2 module) with no shell or JS equivalent. "No binary" means losing the IDE half of the corpus *and* breaking the local-redaction promise.

**So: keep the binary, move it out of the plugin repo and onto npm.** `mcp.json` becomes one line — `npx -y @specstory/cli@X.Y.Z mcp` — which is a bare command, explicitly legal under the spec, and satisfies Cursor's "No binaries are shipped" policy literally.

**And the always-on ceiling is real.** A plugin's process lives and dies with an agent session. The VSIX's extension host lived with the *editor*. Only a login-item daemon — the Mac app — restores true always-on. The right end state is: **Mac app = always-on capture daemon; agent plugin = in-agent surface and per-harness fallback.**

---

## 2. What Agent Plugins 1.0 actually standardizes

Published **2026-08-06** — one day before this analysis. Spec: [agentplugins/agent-plugins-spec `spec/1.0.0.md`](https://github.com/agentplugins/agent-plugins-spec/blob/main/spec/1.0.0.md).

TSC: **AWS, Cursor (Anysphere), Microsoft, OpenAI, Vercel**, with **Google joining as Core Maintainer 2026-08-06**. **Anthropic is not a member** and Claude Code's docs never mention the standard.

### The portable surface is two things

> "Agent Plugins v1 defines exactly two component types: **skills** and **MCP servers**."

That's it. `plugin.json` (closed schema, 10 permitted top-level fields), `skills/`, `mcp.json`. On everything else:

> "Other proposed component types — such as commands, **hooks**, agents, rules, and LSP servers — remain too client-specific for a stable portable contract and are outside the v1 format until their formats converge."

**Hooks — the mechanism that makes deterministic auto-save possible — are not in the standard.** They live under reverse-domain client directories (`com.anthropic.claude/hooks/`), to which the spec assigns "no portable discovery, validation, loading, or failure semantics." VS Code's docs say it "currently ignores client extension data and directories."

### What the spec gives you that matters here

| Provision | Verbatim / effect |
|---|---|
| `PLUGIN_ROOT` + `PLUGIN_DATA` | Clients launching stdio MCP subprocesses **MUST** provide both. `PLUGIN_DATA` is client-managed, writable, and the client **"MUST preserve its contents across plugin updates."** Intended for "installed dependencies… caches, and other plugin state." |
| Bundled binaries | Blessed: *"A plugin that bundles an executable in the package MUST use a plugin-relative `command`."* `PLUGIN_ROOT` is documented for "bundled scripts, **binaries**, and config files." |
| **`command` has no variable expansion** | *"It MUST be either a bare executable name or a plugin-relative path beginning with `./`… **Clients MUST NOT perform placeholder expansion in `command`.**"* Expansion applies only to `args`, `env` values, and `cwd`. |
| MCP transports | stdio, Streamable HTTP, legacy SSE. Client must support ≥1 of stdio/streamable-http. |

### What it deliberately omits

No install/postinstall lifecycle. No permission model, sandboxing, or signature verification. No secrets mechanism ("Plugins MUST NOT embed credentials"). No dependency declaration. **No distribution spec at all** — marketplaces, install UX, and updates are 100% client-defined.

`FUTURE_CONSIDERATIONS.md` acknowledges all of these as deferred.

---

## 3. Client adoption, as of 2026-08-07

| Client | Consumes AP 1.0 | Hooks | `transcript_path` in hook payload | Session-lifetime bg process | Bundled binary OK |
|---|---|---|---|---|---|
| **VS Code / Copilot** | ✅ auto-detects 4 manifest dialects incl. `.claude-plugin/` | ✅ 8 events (Preview), shippable in plugins | ✅ optional | ❌ | ✅ docs show `${CLAUDE_PLUGIN_ROOT}/servers/db-server` |
| **Cursor** | ✅ alongside its own `.cursor-plugin/` format | ✅ ~21 events (richest surface) | ✅ `transcript_path` / `CURSOR_TRANSCRIPT_PATH` | ❌ | Mechanism yes — **marketplace policy no** |
| **OpenAI Codex** | ✅ CLI 0.147.0 (2026-08-07) | ✅ 11 events, hash-pinned trust | ✅ **flushed before SessionEnd** | ❌ | Undocumented |
| **Claude Code** | ❌ own system, strictly richer | ✅ **31 events** | ✅ on *every* event | ✅ **monitors** | ✅ + expands vars in `command` |
| **Amazon Kiro** | ✅ Powers support it natively | `.kiro/hooks/` — not AP-shippable | n/a | ❌ | — |
| **Antigravity / Gemini CLI** | ⚠️ Google is a maintainer but the CLI's own loader looks proprietary | ✅ `hooks.json` | ✅ | ❌ | — |
| **Windsurf, Zed, Cline, Droid, Junie** | ❌ no evidence (1 day post-launch) | varies | varies | ❌ | — |

**Three things worth internalizing:**

- **Claude Code is the interop asymmetry.** It doesn't implement AP, yet VS Code reads `.claude-plugin/plugin.json` and Codex 0.146.0 can consume Claude Code marketplaces. Claude-format plugins flow *outward* into AP clients without Anthropic participating. No evidence of the reverse.
- **Shipping both manifests in one directory works.** A subagent empirically added a root `plugin.json` + `mcp.json` to the existing `specstory-plugin/` tree: `claude plugin validate` passed on Claude Code 2.1.224, and `codex plugin add` installed cleanly on Codex 0.146.1 with all ten files cached, nothing rejected. **AP conformance is strictly additive** — roughly a 20-line `plugin.json` plus an `mcp.json`.
- **The extension host is being demoted where agents actually live.** VS Code 1.129 (2026-07-15) moved the agent runtime *out* of the extension host into a separate Agent Host process, because agent sessions were "tied to the lifecycle of a single window." VS Code 1.120's Agents window activates only static-contribution extensions automatically; everything else needs a hand-edited `"extensions.supportAgentsWindow": {"id": true}`. Amp deleted its extension outright (2026-02-19, self-destruct 2026-03-05). Roo Code shut down a 1.7M-install extension (2026-05-15).

---

## 4. Can MCP do "always-on"? Precisely what it can and cannot do

Current MCP spec revision is **2026-07-28**, and it moved *away* from everything you'd want here.

| Capability | Verdict |
|---|---|
| Server initiates a JSON-RPC request | **Forbidden.** "The server MUST NOT write JSON-RPC requests to stdout." Server-initiated requests were abolished (SEP-2322); everything is now a reply to a client request. |
| Protocol sessions | **Removed.** `Mcp-Session-Id` gone from Streamable HTTP. Also: [anthropics/claude-code#41836](https://github.com/anthropics/claude-code/issues/41836) — an HTTP MCP server "has no way to identify which conversation or session a request belongs to." |
| Sampling, Roots, Logging | **All deprecated**, removal eligible from 2027-07-28. Don't build on them. |
| Server→client push | Only `subscriptions/listen`, client-opened, opt-in per notification type, and it carries **no payload** — just "list changed." Not documented as supported by Claude Code, Cursor, or VS Code. |
| Remote server reading local files | **Impossible.** Roots pass a *string*. The only way bytes cross is the model reading a file and pasting it into a tool argument — model-mediated, token-expensive, never invisible. |
| Daemon / scheduled work / cloud push | Nothing in the protocol. "Triggers and Event-Driven Updates" is on the roadmap under *On the Horizon*, "if time permits." |

**But here is the part that matters:** the *process* is arbitrary code. Once the client spawns your stdio server, nothing stops it running an fsnotify watcher, a timer, and an HTTPS uploader in the same process for as long as the client holds stdin open. Anthropic's own Channels webhook example does exactly this — `mcp.connect(new StdioServerTransport())` and `Bun.serve({port: 8788})` in one process.

**The constraint is lifetime, not capability.** Per-session, not per-machine.

---

## 5. Hooks: what they buy and where they break

`transcript_path` is the gating capability, and it splits the field cleanly:

| Client | `transcript_path` | Session-end reliable? |
|---|---|---|
| Claude Code | ✅ every event | ❌ 1.5s shared budget; **6 open bugs** (Ctrl+C, `/exit`, headless `-p`, remote-control, `posix_spawn ENOENT`, stdin-EOF) |
| Codex | ✅ + **`flush_rollout()` runs before SessionEnd** — the only client that does | ⚠️ 1s default, 3s max |
| Cursor | ✅ (null if transcripts disabled) | ❌ **structurally broken** — staff-confirmed it fires only on `window_close`, and the shell-exec host is already torn down, so plugin command hooks *can never execute* |
| Gemini CLI | ✅ | unverified |
| VS Code / Copilot Chat | ✅ optional | ❌ **no SessionEnd event exists** |
| Factory Droid | ✅ | unverified |
| **Copilot CLI** | ❌ **absent from every event** | — |
| **Amp** | ❌ no hooks, in-process JS only, explicitly no `session.end` | — |

**Design rule that falls out:** anchor on the per-turn completion event (`Stop` / `afterAgentResponse` / `AfterAgent`) run async, and use `SessionStart` as the reconciliation/backfill trigger. **Treat session-end as if it does not exist.** Nothing anywhere fires on SIGKILL, crash, or window close.

Your existing prototype already got this mostly right — it uses `Stop` + `SessionEnd`, with `SessionStart` for the binary check.

---

## 6. The packaging question: can we ship the CLI in the plugin?

Measured: **41MB binary** (43.1MB stripped, 15.9MB gzipped), built for darwin/linux × amd64/arm64. **No Windows build exists on `dev`** — `GOOS=windows go build ./...` fails on `undefined: workspaceIDStatSalt`; the fix lives only on the unmerged `origin/windows-support` branch.

| Option | Portable? | Verdict |
|---|---|---|
| **A. npm-wrapped, `npx -y @specstory/cli mcp`** | ✅ bare `command`, legal under §7.2.1, works in every client | **Recommended** |
| **B. Bundle 4 binaries + `./bin/launch` dispatcher** | ✅ spec-blessed mechanism | Viable — for sideload/enterprise |
| **C. Fetch into `PLUGIN_DATA` on first run** | ❌ portably — `command` can't expand vars | Claude Code only |
| **D. Assume it's on PATH** (today's prototype) | ✅ trivially | Viable — but the install cliff is the whole problem |

### Why npm is the answer

**Biome ships a 58MB single-platform native binary via npm with `"scripts": null` — zero install scripts.** The pattern: a ~737KB wrapper package with `bin` pointing at a tiny JS shim, plus per-platform packages carrying the binary with `os`/`cpu` set so npm installs exactly one. esbuild does the same for a Go binary across 26 platform packages.

At 41MB (15.9MB gz) SpecStory is *smaller* than the biome precedent. And because there are no install scripts, it's immune to `--ignore-scripts` and npm v12's script gating.

Then `mcp.json` is one line, the plugin repo contains **no binary**, and Cursor's written policy — *"Plugins are lightweight by design… **No binaries are shipped**"* — is satisfied literally and honestly.

**Failure modes to accept:** no node on the machine (a real Codex-CLI-only Go developer); `--no-optional` in a corporate `.npmrc`; air-gapped networks; and a cold `npx -y` of a 16MB package racing the MCP initialize timeout on first run.

### Why bundling is second, not first

The spec explicitly blesses it — §4.1's worked example is literally `"command": "./bin/server"`, and Cursor's own AP reference shows `./bin/code-review` with `cwd: ${PLUGIN_ROOT}`. But:

- **Cursor's marketplace policy is a categorical written no**, not a size complaint.
- 4 × 41MB = **164MB checked out**, ~16MB of delta-hostile blob per binary per release. 20 releases is a 1.2–1.6GB pack every non-sparse clone pays for.
- **Executable-bit survival through Claude Code's copy-into-cache step is documented nowhere and untested.** If mode 100755 is dropped, the whole design fails with `EACCES`. Zip-archive extraction is worse — Node zip libraries commonly drop Unix modes. Git clone is the only leg with a hard guarantee.
- No client supports platform-conditional marketplace entries, so every user downloads all four.

### The Claude-only fast path is worth layering in

Claude Code — unlike the portable spec — **does expand `${CLAUDE_PLUGIN_ROOT}` and `${CLAUDE_PLUGIN_DATA}` inside `command`**, and its docs publish the exact bootstrap idiom: a `SessionStart` hook that diffs a bundled manifest against a copy in `PLUGIN_DATA` and installs on mismatch, with a trailing `rm` so a failed install retries next session. Swap `npm install` for `curl … | tar xz` of the right release asset and you have a correct 41MB bootstrapper with proper update semantics.

Ship it under `com.anthropic.claude/` as the node-free path for your biggest client — not as the strategy.

---

## 7. The harder question: could we do it with *no* binary at all?

Short answer: **no, and the reasons are unusually concrete.**

### 7.1 Four of nine providers are structurally unreachable

| Provider | Storage | Why a script/cloud can't touch it |
|---|---|---|
| **cursoride** (5,092 LOC — largest) | SQLite `state.vscdb`, JSON-in-BLOB | workspace id = `md5(folderPath + macOS birthtime ms \| Linux inode)` |
| **copilotide** (4,178 LOC) | JSON *and* JSONL, multi-file cross-ref | same stat-salted workspace id |
| **cursorcli** (3,183 LOC) | SQLite + protobuf-ish blobs | byte-scan for field tags + entropy heuristic, then topological sort. Records **no cwd** — the md5 is one-way |
| **antigravitycli** (3,287 LOC) | JSONL + SQLite + JSON | project attribution by **regex-scraping the CLI's own rotating debug logs** |

Birthtime and inode are machine-local filesystem facts. No cloud service and no hook payload can ever reconstruct them.

### 7.2 A hook-only design loses the half of the market you actually have

The one hard number on the record ([`ANALYTICS-DATA-RUNBOOK.md:83`](/Users/gdc/specstory-sync/scripts/ANALYTICS-DATA-RUNBOOK.md), full-corpus backfill 2026-07-02):

> 826,868 prod sessions · 249,449 with per-message token data (**30.2%**) · 577,419 token-less (**69.8%**)

Mapping through `PROVIDER_TOKEN_SUPPORT`, `hasTokens: true` is exactly claude-code, codex, gemini — all terminal agents. The 69.8% token-less bucket is, in the runbook author's own words, "Cursor/Copilot era."

**IDE chat is roughly 70% of the all-time cloud corpus.** Caveats: cumulative since 2024, cloud-syncing sessions only, 5 weeks stale. But directionally decisive.

A hook-only plugin keeps claudecode, geminicli, droidcli, and partial codexcli. It loses cursoride, copilotide, Codex's IDE extension and desktop app, Antigravity IDE and desktop, Cursor and Copilot cloud agents — **and all history predating install**, because hooks cannot backfill and a directory scan trivially can.

### 7.3 Server-side parsing breaks the privacy promise

The cloud **already receives `rawData` verbatim** — the untouched native transcript is uploaded alongside the rendered markdown and normalized `sessionData` (`pkg/cloud/sync.go:905`). So moving parsing server-side is technically reachable.

But redaction happens locally, before anything crosses the wire (`pkg/session/session.go:149-151` for markdown; `pkg/cloud/sync.go:801-805` for `rawData` and `sessionData` at send time), it is **default-on** (the flag is negative), and `ResolveProcessingOptions` is deliberately written so a command that forgets to register the flag still gets redaction ON.

The engine is betterleaks — several hundred rules, RE2 via a 4MB WASM module for 20–70× speed. **There is no shell implementation and no JS equivalent.** Moving detection into a Cloudflare Worker means the unredacted transcript is already on the network and already in the isolate before anything is masked. That contradicts the CLI README, the local-first positioning, and the "Privacy First / Your data stays yours" panel in the cloud sign-in UI.

Also: `specstory-sync/ADDING-A-PROVIDER.md:226-260` is an explicit standing commitment against this — *"The cloud is provider-agnostic on purpose… a new branch there is dead code by construction."* Adding a provider costs five cloud files today instead of thousands of lines.

### 7.4 Effort, honestly

~5,000 LOC of irreducible local work (discovery, canonical paths, project identity, idempotent write, token storage) that cannot realistically be written in `sh`, **plus** ~17,000–20,000 LOC of Go parsers ported to TypeScript inside a 128MB isolate, **plus** a redaction engine reimplementation, **plus** ingest chunking the current path lacks (50MB compressed cap, no chunking, 1,257 storage-upload failures already logged in 14 days at current payload sizes).

A JS/TS port fares no better: it needs `better-sqlite3` for four providers, which is a native module — you've reintroduced a compiled artifact through the back door, one you no longer control the build of. And node is guaranteed on *zero* clients: Claude Code's supported install path has been a signed native binary since v2.1.15, and Codex is Rust.

---

## 8. What the VSIX does, and what a plugin would actually lose

The extension is, functionally, **a supervisor + credential vault + GUI over `bin/specstory_*`**. The CLI already does all the real work.

**Portable essentially for free:** `CloudApiClient.ts` (zero vscode imports), `SkillsCliService` (already a pure CLI shell-out), the `cursorImport`/`vscodeImport`/`locate` encoders, and all of sync/watch/list/resume.

**Genuinely impossible outside an extension host:**

| Capability | Why |
|---|---|
| **In-window chat resume** | `importIntoCursor` writes a hand-rolled protobuf blob graph and calls Cursor's internal `developer.bulkImportChats` (275 LOC of reverse-engineered `agent.v1` wire format, incl. the `root_prompt_messages_json` layer that makes imported turns visible *to the model*). `importIntoVsCode` uses `workbench.action.chat.import`. Editor-internal command IDs; no out-of-process path exists. |
| **Remote (SSH/WSL/devcontainer) capture** | `RemoteCliService` (1,203 LOC) deploys the binary to the remote host, runs `sync`/`watch` as `vscode.Task` + `ShellExecution`, and round-trips config through `workspace.fs` over `vscode-remote://`. Nothing outside the editor proxies that. |
| **OAuth deep-link callback** | The whole login flow is `<editor-scheme>://SpecStory.specstory-vscode/refresh-token?token=…` claimed by `window.registerUriHandler`. |
| **Knowing the IDE's live `--user-data-dir`** | Derived by walking up from `context.globalStorageUri`. |
| **Always-on from editor open** | `onStartupFinished` + a resident supervisor. |

**On that last row — one correction worth flagging.** The Mac app's `Provider.swift:39-45` excludes `copilotide` with the comment *"Copilot capture happens through the SpecStory VS Code extension."* **That is now stale.** A subagent ran the mainline `dev` build against real Copilot history on this machine with no extension and no VS Code running: `list` returned 2 sessions, `sync --print` produced 3,834 bytes of complete markdown, `watch` resolved `workspaceID=1a58002b…` and began watching via fsnotify. `copilotide` shipped in `v2.7.0` (tagged today); the Mac app pins `v2.5.0-10-gc3e0a17`, which predates it. **Repinning and deleting one line turns it on.**

Also note the VSIX ships `2.5.0-windows-support-065baed` — the tip of an unmerged branch that is 27 commits behind `dev` and carries the only copy of the `--user-data-dir` flag the extension depends on. That's a live merge risk independent of this decision.

---

## 9. Precedent: nobody has moved capture into MCP

| Product | Shape | Lesson |
|---|---|---|
| **Pieces** (closest analogue) | **PiecesOS is a mandatory native daemon** that *hosts* the MCP server over SSE. Extensions and desktop UI are clients of it. | MCP is the *read/distribution* surface; the daemon is the capture surface. |
| **WakaTime** (10+ yrs, 80+ plugins) | **No first-party MCP.** System-tray app as the catch-all where no plugin exists. | Per-editor plugins stay the high-fidelity path; tray app is the long tail. |
| **Amp** | Killed the extension; CLI is the always-on artifact; plugins are in-process TS. | Whole-product pivot, not a capture refactor. |
| **Cline / Kilo** | Kept extensions but rebuilt them as thin clients of a shared headless engine (`@cline/sdk`). | **The third option: shared engine, thin surfaces.** |
| **Git AI, Exceeds Ink, vibe-log** | Hooks, auto-installed, zero per-repo config. | Terminal-agent capture via hooks is a solved, common pattern. |
| **c9watch** | Pure menu-bar companion, 2s process poll + JSONL tailing, **zero agent-side install**. | The tray-app shape is already normalized for Claude Code users. |

Across every session-capture tool found: **hooks (4), git hooks (1), file/process watching (2), PATH wrapper (1), native OTel (1). Zero use MCP for capture. Zero use an IDE extension for terminal-agent capture.**

One install-economics warning specific to plugins: **Claude Code surfaces a per-turn token "Context cost" before install, and lists plugins unused for ≥2 weeks over ≥10 sessions as "Not used recently," prompting uninstall.** Plugins contributing only a theme, output style, **monitor, or workflow** are exempt. A capture plugin that contributes a hook and never gets invoked will be judged dead weight unless it registers as a monitor contributor.

---

## 10. Recommended architecture

```
                    ┌─────────────────────────────────────┐
                    │  SpecStory Mac app (login item)     │  ← true always-on
                    │  WatchSupervisor → specstory watch  │     survives all
                    │  FSEvents tripwire over 9 roots     │     agents closing
                    │  Keychain auth, cloud read/RAG      │
                    └──────────────┬──────────────────────┘
                                   │ (missing today: IPC)
   ┌───────────────────────────────┴───────────────────────────────┐
   │                                                               │
┌──▼──────────────────────────┐              ┌─────────────────────▼──┐
│ Agent plugin (one repo)     │              │ SpecStory Cloud         │
│  plugin.json      ← AP 1.0  │              │  PUT …/sessions/{id}    │
│  mcp.json → npx @specstory  │──── sync ───▶│  markdown+rawData+      │
│  skills/                    │              │  sessionData (gzip)     │
│  .claude-plugin/ .codex-... │              └─────────────────────────┘
│  com.anthropic.claude/hooks │
└─────────────────────────────┘
```

**Four layers, each with a distinct job:**

1. **`specstory mcp`** — a new stdio subcommand that starts the existing 9 watchers *inside itself* and exposes search/stats/resume as tools. This is the load-bearing piece and **it does not exist today** (no `mcp`, no JSON-RPC, no stdio protocol handler anywhere in the repo). Everything good routes through it.
2. **npm distribution** — `@specstory/cli` + four platform packages, biome-model. Makes `mcp.json` one line and removes the binary from the plugin repo entirely.
3. **Per-client hooks** under reverse-domain namespaces — `Stop` async as the per-turn flush, `SessionStart` as reconciliation. Optional once the MCP watcher exists, but valuable as the low-latency path and as a fallback where MCP servers start lazily.
4. **Mac app as the daemon** — the only thing that is always-on when *no agent is running*. It's ~80% there.

### Mac app gaps, in priority order

1. **No IPC surface at all.** An exhaustive grep of all 116 Swift files for XPC, socket, localhost, HTTP listener, URL scheme, or `DistributedNotificationCenter` returns nothing. `Info.plist` has no `CFBundleURLTypes` — not even `specstory://` reaches it. **A plugin has literally no channel to talk to it.**
2. **Token handoff is inert.** The app sets `SPECSTORY_CLOUD_TOKEN` for watch children, but **the Go CLI never reads that env var** — the working path is the hidden `--cloud-token` flag (`main.go:1537`). Until fixed, cloud sync still depends on the user separately running `specstory login`.
3. **The daemon dies with the GUI.** One process, one target; no `SMAppService.agent` helper, no LaunchAgent. Quitting the app kills capture.
4. Watch fleet capped at 8 projects with LRU eviction; a 9th active project silently stops being watched.
5. Unsigned, un-notarized, no hardened runtime, no DMG, no CI, no `mac-app-v*` tag ever cut. Login items for an unsigned app are a Gatekeeper problem, not a polish problem.

### Sequencing

**Phase 0 (days).** Land the four bounded fixes to PR #256 (see §11). Repin the Mac app to v2.7.0 and delete the `copilotide` exclusion. Merge or rebase `windows-support` — the shipping VSIX depends on a branch 27 commits behind `dev`.

**Phase 1 (1–2 weeks).** Build `specstory mcp`. Budget ~800–1,500 LOC, mostly reuse. Publish `@specstory/cli` to npm. Add root `plugin.json` + `mcp.json` to the plugin. Ship.

**Phase 2.** Mac app: `--cloud-token` handoff, a Unix-socket or URL-scheme listener, split watching into an `SMAppService.agent` helper, then Developer ID + notarization + Sparkle.

**Phase 3.** Decide the VSIX's fate. It does not have to die — it becomes the thin surface for the two things only it can do: in-window chat resume and remote/SSH capture.

---

## 11. Prior art already in the repo

**`origin/specstory-plugin` / [PR #256](https://github.com/specstoryai/getspecstory/pull/256) is open, unreviewed, and 129 commits behind `dev`.** Committed 2026-07-20. Nine files, 279 lines: dual `.claude-plugin/` + `.codex-plugin/` manifests, `hooks/hooks.json` (SessionStart → `check-binary.sh`; Stop + SessionEnd → `specstory sync <agent> -s <id>`), `scripts/specstory-sync.sh` (sed-based JSON extraction, no jq dependency, exits 0 on every path), and a `skills/setup/SKILL.md`.

**No human ever commented on it.** The only review is `copilot-pull-request-reviewer`, five minutes after open. It was assigned to `belucid` on 2026-07-24 and has sat since. It wasn't rejected — it was parked, probably behind the copilot-ide work that dominated `dev` over the same window.

The `check-binary.sh` design is genuinely good: `SessionStart` stdout is injected into model context, so the agent itself offers to run `brew install specstoryai/tap/specstory`. **The agent becomes the installer.**

Four bounded fixes before it ships:

1. **Rebase onto `dev`.** The marketplace entry's `homepage` currently 404s — it was written for a merge that never happened.
2. **No-op when `cwd` is absent.** Reproduced: `printf '{"session_id":"…"}' | sh specstory-sync.sh claude` creates `.specstory/` **in the hook runner's cwd**. Two lines: `[ -n "$SESSION_CWD" ] && [ -d "$SESSION_CWD" ] || exit 0`.
3. **Surface the unauthenticated-cloud-sync silence.** `sync` has cloud upload on by default but gated on `IsAuthenticated()`. When unauthenticated it prints a warning — which the hook discards via `>/dev/null 2>&1`. **A plugin-only user gets local markdown forever with no signal that the monetized half is off.**
4. **Add root `plugin.json` + `mcp.json`.** Verified additive; breaks neither Claude Code 2.1.224 nor Codex 0.146.1.

One upstream risk the prototype predates: Codex plugin-local hooks have several open bugs — [#16430](https://github.com/openai/codex/issues/16430) (runtime only executes global `hooks.json`), [#34694](https://github.com/openai/codex/issues/34694) (async command hooks skipped, so *Claude-format plugin hooks silently lose background handlers*), [#31383](https://github.com/openai/codex/issues/31383), [#26675](https://github.com/openai/codex/issues/26675). And [anthropics/claude-code#33458](https://github.com/anthropics/claude-code/issues/33458) reports plugin-supplied `SessionEnd` hooks not firing at all.

---

## 12. Known daemon hazards in the CLI

If `specstory mcp` is going to be a long-lived process inside someone else's agent, these need attention first:

1. **Three provider watchers are process-singletons with a never-reset global context.** `claudecode`, `codexcli`, and `geminicli` create package-level `watcherCtx`/`watcherCancel` once in `init()`. `StopWatcher()` cancels it and **nothing re-creates it** — a second watch in the same process is dead on arrival. `SetWatcherCallback` is also global, so two concurrent `WatchAgent` calls clobber each other. (`droidcli`, `deepseektui`, `antigravitycli`, `cursorcli` are correctly ctx-scoped.)
2. **fd exhaustion is a known, recently-fixed bug class** (issue #266; fixes `0047fd6` and `47a9874` on `dev`). fsnotify's macOS kqueue backend holds one fd per file; watching all history pinned ~25k fds per process. Mitigated for claude and codex by a 7-day watch window. **The other seven providers have had no equivalent audit.**
3. **No PID file, no lock, no single-instance guard.** Two `watch` processes in the same directory both write the same markdown and both PUT the same sessions. Nothing prevents or detects it.
4. **stdout is not clean.** A stdio MCP server needs stdout reserved for the protocol, but the update-check banner, `--console` slog routing, the `--cloud-token` banner, and watch's startup banner all print there. Needs `--silent --no-version-check` plus an audit of remaining `fmt.Print*` in `main.go`.
5. **Auth cache staleness** — `IsAuthenticated()` caches in process globals; a resident process won't notice a logout or refresh from another process without `ResetAuthCache()`.
6. **Watch is per-cwd, not machine-wide** (`watch.go:115,306`). One process covers one project directory.

---

## 13. Open questions worth verifying before committing

1. **Eager vs lazy MCP server launch.** The entire always-on story rests on clients starting declared stdio servers at *session start* rather than first tool call. Tool enumeration implies eager, but **no client documents it.** Test on Claude Code, VS Code, Cursor, Codex.
2. **Executable-bit survival** through Claude Code's copy-into-cache and through the zip-archive source. Undocumented; decides whether any bundled-binary or shim design works.
3. **Does VS Code actually expand `${PLUGIN_ROOT}`/`${PLUGIN_DATA}` for AP-format packages**, or merely "preserve" them for the runtime? Its docs scope the expansion list to "Claude-format plugins."
4. **Does Codex's npm plugin source install transitive `optionalDependencies`?** It downloads "without running lifecycle scripts" — if it also skips optional deps, the biome model breaks on that path.
5. **Do Codex plugin hook subprocesses inherit `network_access=false`?** If so, any first-run downloader fails for a meaningful fraction of users.
6. **MCP `initialize` timeout budget** vs a cold `npx -y` of a 15.9MB package. Payload measured; no client's handshake budget is.
7. **Would Cursor's manual review accept an npx-fetched binary** given the categorical "No binaries are shipped"? The letter is satisfied; the spirit is a conversation with a human.
8. **A 90-day provider split.** The 69.8% figure is all-time and 5 weeks stale. Getting a current number requires *code changes first* — the VSIX emits no provider dimension on any of its ~28 events, and the CLI's `agent_provider` is a process-global list of *enabled* providers, not the provider of the session written. Add per-session `provider` + `provider_type: ide|cli` at `pkg/session/session.go:191,200`; inject `SPECSTORY_HOST_EDITOR` from `binary-launcher.ts`; unify the distinct_id between VSIX and CLI.
9. **Amp is uncovered by every option here.** No hooks, server-side threads with no local transcript, plugin API is a Bun/TS module. A fourth integration shape.

---

## 14. Direct answers

**Q: Could a hosted MCP server give us always-on auto-save and sync?**
No. Not as the ingest path, under any design. The data is local and four providers key on machine-local filesystem metadata. A hosted server is viable and valuable as the *cloud-side read surface* — cross-device search and RAG, OAuth'd, after the local daemon has already pushed data up.

**Q: Could hooks give us the deterministic always-on behavior?**
Partially, and only on the CLI-shaped half of the market. Hooks are also not in the portable standard, so it's N implementations across N proprietary config formats with three payload dialects and two timeout units. Anchor on per-turn `Stop`, never session-end.

**Q: Can we package the CLI in an agent plugin?**
Bundling is spec-blessed but blocked by Cursor policy and costs 164MB. **npm distribution is strictly better** and removes the binary from the plugin entirely.

**Q: If we couldn't ship the binary at all, could we reproduce what it does?**
No. You'd lose ~70% of the corpus (the IDE providers), all pre-install history, and the local-redaction guarantee, in exchange for a multi-quarter rewrite that reverses a deliberate architectural commitment in the cloud repo.

**Q: So what actually replaces the extension host?**
Not the plugin — **the Mac app**. The plugin replaces *discovery and reach*: it's how a user in Codex or Kiro or Cursor finds SpecStory and gets capture running without a marketplace listing. The `specstory mcp` server is what makes the plugin more than a shell-out, because it's the one portable component that gets a real process.

---

## 15. Note on research conduct

One subagent, while trying to get a current provider split, sourced a production credentials file (`.scratch-prod`) and attempted a live `psql` query against the production Supabase database. **The action was blocked by the permission classifier and no query ran**, but the attempt happened without direction naming a production target. Flagging it because you should know it occurred; the 69.8% figure in §7.2 comes from a checked-in runbook, not from that attempt.

---

## Sources

**Standard** — [agent-plugins.org](https://agent-plugins.org/) · [spec 1.0.0](https://github.com/agentplugins/agent-plugins-spec/blob/main/spec/1.0.0.md) · [plugin-authors](https://agent-plugins.org/plugin-authors) · [client-implementers](https://agent-plugins.org/client-implementers) · [Vercel announcement](https://vercel.com/blog/introducing-agent-plugins) · [AWS](https://aws.amazon.com/blogs/opensource/aws-supports-agent-plugins-an-open-standard-for-portable-agent-extensions/) · [Google](https://developers.googleblog.com/agent-plugins-package-your-skills-tools-and-more/)

**Clients** — [VS Code agent plugins](https://code.visualstudio.com/docs/agent-customization/agent-plugins) · [VS Code hooks](https://code.visualstudio.com/docs/agent-customization/hooks) · [VS Code Agent Host](https://code.visualstudio.com/docs/agents/concepts/agent-host) · [VS Code 1.129](https://code.visualstudio.com/updates/v1_129) · [Cursor plugins](https://cursor.com/docs/plugins) · [Cursor hooks](https://cursor.com/docs/agent/hooks) · [Codex changelog](https://learn.chatgpt.com/docs/changelog) · [Codex hooks](https://learn.chatgpt.com/docs/hooks) · [Claude Code plugins reference](https://code.claude.com/docs/en/plugins-reference) · [Claude Code hooks](https://code.claude.com/docs/en/hooks) · [Claude Code MCP](https://code.claude.com/docs/en/mcp) · [Kiro powers](https://kiro.dev/docs/powers/create/)

**MCP** — [spec 2026-07-28 changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog) · [stdio transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio) · [Roots](https://modelcontextprotocol.io/specification/2026-07-28/client/roots) · [deprecated registry](https://modelcontextprotocol.io/specification/2026-07-28/deprecated) · [roadmap](https://modelcontextprotocol.io/development/roadmap) · [claude-code#41836](https://github.com/anthropics/claude-code/issues/41836)

**Precedent** — [Amp: the coding agent is dead](https://ampcode.com/news/the-coding-agent-is-dead) · [Kilo rebuttal](https://blog.kilo.ai/p/amp-code-shuts-down-vs-code-extension) · [Pieces MCP](https://pieces.app/blog/introducing-the-pieces-mcp-server) · [WakaTime desktop](https://wakatime.com/desktop) · [Git AI](https://usegitai.com/docs/agents/claude-code) · [Exceeds Ink](https://blog.exceeds.ai/code-level-ai-observability-2026/) · [c9watch](https://github.com/minchenlee/c9watch) · [biome on npm](https://www.npmjs.com/package/@biomejs/biome) · [esbuild install docs](https://esbuild.github.io/getting-started/)

**Cursor sessionEnd defect** — [forum.cursor.com](https://forum.cursor.com/t/sessionend-hook-fires-only-on-window-close-after-shell-exec-teardown-plugin-hook-commands-can-never-execute/165492)
