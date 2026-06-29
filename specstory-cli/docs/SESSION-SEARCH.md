# `specstory search` — find & resume your sessions

> **STATUS: BUILT.** `search` is the **same interactive TUI as [`resume`](RESUME-TUI.md)**
> (`sessionTUI` in `pkg/cmd/session_tui.go`), entered straight into the all-projects full-text
> search over the [`sessions.db` index](SESSIONS-DB.md). There is no separate search model —
> the two commands share one codebase, keymap, and look. The preview uses **glamour**.

## Purpose

`specstory search` answers *"find something across all my history."* It opens directly in the
cross-project full-text search with the input focused, so you type immediately. From a hit you
**preview** it (`space`) or **resume** it (`r`) — the same preview and resume flow as `resume`.

- **Pre-seeded query.** Anything after the command becomes the initial query and the search
  runs immediately — `specstory search max cpu` is exactly `specstory search` then typing
  `max cpu`. (Mirrors `specstory resume <agent>` pre-selecting the target.)

## Relationship to `specstory resume`

They are **one UI, two doors**. The only differences:

| | `resume [agent]` | `search [query]` |
|---|---|---|
| Opens to | the current project's session list | the all-projects FTS, **input focused** |
| Positional arg | pre-selects the target agent | pre-seeds the query |
| Header title | `SpecStory Resume` | `SpecStory Search` |

Everything else is shared *because it is literally the same model* (`newSessionTUI` +
`sessionTUIOpts`): keymap, the glamour preview, agent filter, dense/sparse, snippet
highlighting, the async + debounced FTS path, and the target-agent step. `search` reaches the
cross-project results via the model's existing global-search screen (`globalActive`); `resume`
reaches the same screen with `tab` → `/`.

## UX

The search input is the footer line (bottom), results above, consistent with `resume`'s
`/`-search. Rough layout:

```
 SpecStory Search · all projects                              42 matches   ·   agent: all
 ─────────────────────────────────────────────────────────────────────────────────────
 ▸ claude   2d   specstory-cli   …diagnose the …max cpu… spike when reindex runs…
   codex    1w   intent-server   …the worker pegs …max… …cpu… at 100% during…
   gemini   3w   stoa-cli        …cap …cpu… usage; the …max… concurrency was…
 ─────────────────────────────────────────────────────────────────────────────────────
 ↑↓ move · r resume · space preview · a agent · v dense · / edit search · esc back · q quit
 / max cpu▌
```

`space` opens a scrollable, **glamour-rendered** reader of the session (the real specstory
markdown via `session.GenerateMarkdownFromAgentSession`, falling back to the plain FTS body for
sessions that can't be re-parsed, e.g. Cursor). `r` (in the list or the preview) hands off to
the shared target-agent step → reconstruct + `ExecAgentAndWatch`. `↵` is inert in the list, so
a stray return can't accidentally launch an agent.

## Command

`specstory search [query…]` — `cobra.ArbitraryArgs`; the args (joined with spaces) are the
initial query. No args → empty input, ready to type. Reuses `openOrBuildResumeIndex()`
(reindex if the index is missing/empty) before opening the shared TUI with
`sessionTUIOpts{startInSearch: true}`.

## Decisions

- **One model, two entry points (ratified, built).** `search` was originally a separate
  always-on-input model (`search_tui.go`); it has been folded into `sessionTUI`. This removed
  the `ctrl`-key bindings that the always-on input forced, and gave `search` the agent filter,
  dense/sparse, and glamour preview for free. `search_tui.go` is deleted.
- **Query performance (ratified, built).** Full-text search only fires at **2+ characters**
  (a 1-char prefix like `t*` matches nearly the whole corpus); the main query returns session
  rows newest-first **without** `snippet()`, then fetches highlighted snippets lazily for only
  the visible rows. Each keystroke **cancels** the prior in-flight query (via `context`),
  freeing its connection. Lives in the shared FTS layer (`pkg/sessionindex`, `queryReady`).
- **Resume = `r`, not `↵` (ratified, built).** Launching an agent is an explicit keystroke in
  every list; `↵` only commits on the final target-agent confirmation screen.
- **Preview = glamour (ratified, built).** The session renders as markdown through `glamour` in
  a scrollable `viewport`, shared verbatim with `resume`. In-preview match highlighting +
  `n`/`N` nav is deferred (glamour emits its own ANSI; needs a post-process pass).

## Out of scope

- Index population / freshness — see [SESSIONS-DB.md](SESSIONS-DB.md). `search` joins `resume`
  as a staleness-refresh occasion in the warm-keeping thread.
- Cross-machine / cloud search — later stages of session portability.

## Related Documents

- [RESUME-TUI.md](RESUME-TUI.md) — the shared `sessionTUI` model, keymap, and browser.
- [SESSIONS-DB.md](SESSIONS-DB.md) — the index both read from.
