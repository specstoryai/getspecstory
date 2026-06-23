# `specstory search` — find & read your sessions

> **STATUS: BUILT (v1).** A read-first search TUI over the [`sessions.db` index](SESSIONS-DB.md),
> sibling to [`specstory resume`](RESUME-TUI.md) — shared UI, keymap vocabulary, and code
> (`launchResume`, `renderSnippet`, styles). The reader uses **glamour**. See
> [Build plan](#build-plan-built) for what shipped and what's deferred.

## Purpose

`specstory resume` answers *"continue a session"* — its end state is **launching an agent**.
`specstory search` answers *"find and read across my history"* — its end state is **reading
the session**. Same index, same look and feel; different terminal action.

- **Read-first.** You search, you open a result, you read it. Viewing is the destination.
- **Resume is one keystroke away.** `r` on a result (or in the reader) hands off to the resume
  flow, so "I found the session I want to continue" is frictionless.
- **Pre-seeded query.** Anything after the command becomes the initial query and the search
  runs immediately — `specstory search max cpu` is exactly `specstory search` then typing
  `max cpu`. (Mirrors `specstory resume <agent>` pre-selecting the target.)

## Relationship to `specstory resume`

Consistency is the whole point — learnings and patterns must transfer both ways.

**Shared (reuse, don't reinvent):**

- The `sessions.db` index and its queries (`Search`, lazy `Snippets`, `ListByProject`,
  `ListProjects`, `SessionBody`).
- The Bubble Tea v2 model patterns, lipgloss styles, agent color tags, relative-time
  formatting, snippet highlighting (`renderSnippet`), and the **async + debounced FTS** path
  (`searchDebounceMsg`/`searchResultMsg`). These live in `pkg/cmd` already; the search TUI
  reuses them directly (same package).
- Result-row layout (`agent · time · project · highlighted snippet`) and keymap vocabulary:
  `↑↓`/`jk` move, `↵` primary action, `esc`/`q` back/quit, `a` agent filter, `tab` scope,
  `v` dense/sparse.

**Different:**

| | `resume` | `search` |
|---|---|---|
| Opens to | the current project's session list | **the search input** (you're here to search) |
| Default scope | current project, else all-projects; `tab` toggles | **all projects**; `tab` narrows to this project |
| `↵` does | → target-agent step → launch | → **open the reader** (read the session) |
| `r` | n/a | → resume this session (hands to the resume flow) |
| End state | agent running | session on screen |

Scope **diverges deliberately**: search opens across **all projects** (it's a
find-across-history tool — you want the whole corpus first), and `tab` narrows to the
current project when it has indexed sessions. Resume does the opposite (current project
first). The always-on search input is the other intentional divergence.

## UX

Search input is always active at the top (it's a search tool), results below, a reader on
`↵`. Rough layout:

```
 search · all projects                                   42 results   ·   agent: all
 / max cpu▌
 ─────────────────────────────────────────────────────────────────────────────────────
 ▸ claude   2d   specstory-cli   …diagnose the …max cpu… spike when reindex runs…
   codex    1w   intent-server   …the worker pegs …max… …cpu… at 100% during…
   gemini   3w   stoa-cli        …cap …cpu… usage; the …max… concurrency was…
 ─────────────────────────────────────────────────────────────────────────────────────
 ↑↓ move   ↵ read   r resume   a agent   tab this project   v dense/sparse   esc quit
```

The **reader** (`↵`) — a full-screen, scrollable view of the session transcript with matches
highlighted and jumpable:

```
 specstory-cli · claude · 2d                                              match 2 / 7
 ─────────────────────────────────────────────────────────────────────────────────────
   user ⟶  diagnose the [max cpu] spike when reindex runs on a big index
   agent ⟶ Let me profile… the snippet generation over hundreds of matches…
   …
 ─────────────────────────────────────────────────────────────────────────────────────
 ↑↓ scroll   n/N next/prev match   r resume   esc back   q quit
```

## Command

`specstory search [query…]` — `cobra.ArbitraryArgs`; the args (joined with spaces) are the
initial query. No args → empty input, ready to type. Reuses `openOrBuildResumeIndex()`
(reindex if the index is missing/empty) before opening.

## Decisions

- **Scope defaults to all projects (ratified).** Search opens across the whole corpus and
  `tab` narrows to the current project (when it has sessions) — a deliberate divergence from
  resume, which defaults to the current project. Search is a find-across-history tool.
- **Query performance (ratified, built).** Full-text search only fires at **2+ characters**
  (a 1-char prefix like `t*` matches nearly the whole corpus); the main query returns session
  rows newest-first **without** `snippet()`, then fetches highlighted snippets lazily for only
  the visible rows. The browse path opens a **multi-connection reader pool** so a slow query
  can't freeze the UI, and each keystroke **cancels** the prior in-flight query (via `context`),
  freeing its connection. The results pane shows **`Searching…`** while a query is in flight
  rather than a premature "No matches". This lives in the shared FTS layer (`pkg/sessionindex`,
  `queryReady`), so resume gets it too.
- **`r` reuses resume's full target-agent flow (ratified).** Cross-agent power + one consistent
  resume path, rather than a separate same-agent shortcut.
- **Always-on search input (ratified).** The input is focused at the top from the start; type to
  search live. This is the one intentional divergence from resume's `/`-to-search.
- **Reader = glamour (ratified, built).** The reader renders the session as markdown
  (`session.GenerateMarkdownFromAgentSession`) through `glamour` for a rich "pretty" read, in a
  scrollable `viewport`. Falls back to the plain FTS body for sessions that can't be re-parsed
  (no resolvable cwd, e.g. Cursor). In-reader match highlighting + `n`/`N` nav is deferred
  (glamour emits its own ANSI; needs a post-process pass or a plain match-view toggle).

## Build plan **(built)**

1. **`pkg/cmd/search.go`** — the `specstory search [query…]` command; args pre-seed the query;
   reuses `openOrBuildResumeIndex`; launches the TUI; on `r`, builds a `resumePlan` and calls
   the shared `launchResume`.
2. **`pkg/cmd/search_tui.go`** — Bubble Tea model: always-on input, async-debounced
   cross-project results (reusing `renderSnippet`, styles, `renderAgentTag`, `sessionTitle`,
   relative time, the FTS path), `tab` scope toggle, `ctrl+a` agent filter, glamour reader.
3. **Reader** — `bubbles/viewport` over **glamour-rendered** session markdown
   (`session.GenerateMarkdownFromAgentSession` → `glamour`; falls back to the FTS body for
   sessions that can't be re-parsed, e.g. Cursor). Scrollable; `r` resumes.
4. **`r` → resume** — `launchResume` factored out of `resume.go` and shared; `r` (reader) /
   `ctrl+r` (results) → target-agent step → reconstruct + `ExecAgentAndWatch`.

**Keybinding adaptation (the always-on-input consequence).** Because the input is always
focused, bare letters type into the query, so secondary actions use non-letter / `ctrl`
bindings, and `r` lives where there's no input:

- Results: type to search · `↑↓`/`ctrl+p`/`ctrl+n` move · `↵` **read** · `ctrl+r` resume ·
  `tab` scope · `ctrl+a` agent · `esc` clear-then-quit.
- Reader: `↑↓`/`pgup`/`pgdn` scroll · `r` resume · `esc` back · `q` quit.

**Dependency note.** glamour (`github.com/charmbracelet/glamour` v1.0.0) is still on the old
charm stack; it collides with our v2 `x/ansi` until `x/cellbuf` is pinned to **v0.0.15** (done
in go.mod). Main build stays green.

**Deferred:** in-reader match highlighting + `n`/`N` navigation (glamour emits its own ANSI, so
this needs a post-process pass or a plain "match view" toggle); dense/sparse in results (rows
are always snippet rows); a shared-component extraction pass if the duplication with
`resume_tui.go` grows. Interactive UX validated by running in a terminal.

## Out of scope

- Index population / freshness — see [SESSIONS-DB.md](SESSIONS-DB.md). `search` joins `resume`
  as a staleness-refresh occasion in the warm-keeping thread.
- Cross-machine / cloud search — later stages of session portability.

## Related Documents

- [RESUME-TUI.md](RESUME-TUI.md) — the sibling picker whose UI/keymap/code this shares.
- [SESSIONS-DB.md](SESSIONS-DB.md) — the index both read from.
