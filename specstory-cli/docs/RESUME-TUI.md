# `specstory resume` — the picker TUI

The interactive session picker. It reads the [`sessions.db` index](SESSIONS-DB.md), blends in
sessions from SpecStory Cloud, and launches the chosen one via the reconstruct +
`ExecAgentAndWatch` plumbing (see [SESSION-PORTABILITY.md](SESSION-PORTABILITY.md)).

> **Shared model.** `resume` and [`search`](SESSION-SEARCH.md) are the **same** Bubble Tea
> model (`sessionTUI` in `pkg/cmd/session_tui.go`), differing only in entry point: `resume`
> opens on the current project's session list; `search` opens straight into the all-projects
> FTS with the input focused. Everything below is therefore true of both.

This document records the **design decisions and their reasons** — the things the code shows
*what* but not *why*. For how the index behind it is built, see
[SESSIONS-DB.md](SESSIONS-DB.md); for the cloud blend's design, see
[CLOUD-RESUME.md](CLOUD-RESUME.md).

## Keymap

| Key | Action |
|---|---|
| `↑` `↓` / `k` `j` | move · `pgup`/`pgdn`, `g`/`home`, `G`/`end` also work |
| `r` | **resume** the highlighted session |
| `space` | preview (glamour-rendered). `enter` is silently aliased to it |
| `/` | full-text search — scope follows the view (see below) |
| `a` | cycle the agent filter |
| `m` | cycle the machine filter (all → this machine → each remote machine) |
| `v` | toggle dense / sparse |
| `d` | soft-delete, behind a `y`/`N` confirmation |
| `tab` | toggle between the current project and the all-projects browser |
| `p` | filter the project list by name (browser only) |
| `u` | open the upgrade page — only when the Pro nudge is showing |
| `esc` | back one level |
| `q` / `ctrl+c` | quit |

Skipping the picker entirely — `specstory resume --session <uri>` — is documented in the
README; the URI forms and the local-first resolution order are
[CLOUD-RESUME.md](CLOUD-RESUME.md)'s Chunk 5.

## Why the interaction works the way it does

- **`r` resumes; `enter` is deliberately inert as a resume trigger.** Resuming launches an
  agent, so it must be an explicit, unusual keystroke — a stray `↵` must never start a
  session. `enter` is aliased to `space` (preview) instead, so pressing it does something
  harmless rather than nothing. The one place `↵` *does* commit is the final target-agent
  step, which is an explicit confirmation screen.

- **`d` is a soft delete, and the tombstone is the point.** The native session files on disk
  are never touched; the index row is tombstoned (`sessions.deleted = 1`, FTS body stripped).
  A plain `DELETE` would be undone by the very next `reindex` or live write, because the
  native file is still there to be re-enumerated. The tombstone makes the delete *stick* —
  see the write-path guard in [SESSIONS-DB.md](SESSIONS-DB.md#soft-delete-the-d-key).
  Consequences worth knowing:
  - In a session list (or a search hit) `d` removes the **session**; in the all-projects
    browser it removes the **project** — all its sessions at once.
  - Deleting a project does **not** blacklist it. New sessions started there later index
    normally.
  - The only way back is deleting `~/.specstory/sessions.db` and running `specstory reindex`.
    The confirmation screen spells this out, and every key other than `y` cancels, so the
    destructive path is never the default.

- **`/` is always session full-text search; only its scope changes.** In a session list it is
  FTS scoped to that project; in the all-projects browser it is FTS across every project (a
  flat result list, project shown per row). Filtering the *project list by name* is a
  separate key, **`p`** — deliberately not one box that mode-switches between two different
  kinds of search, which would make the same keystroke mean different things depending on
  invisible state.

- **Search results show the match, not the title.** Each row renders the FTS `snippet()` with
  matched terms highlighted, so you see *why* it matched. The query runs async and debounced
  off the UI thread, `LIMIT`-bounded and newest-first; snippets are fetched lazily for just
  the visible window, so the main search never pays for every match.

- **Preview falls back when a session can't be re-parsed.** `space` renders the real
  SpecStory markdown (`session.GenerateMarkdownFromAgentSession` → `glamour`). When the
  session has no resolvable cwd — Cursor CLI being the case that motivated this — it falls
  back to the stored FTS body (`Store.SessionBody`) rather than showing nothing.

- **No source-agent step.** The picker shows all sessions across all agents for the current
  project, each row tagged with its agent. Choosing the *target* agent is the last step, not
  the first: `specstory resume <agent>` pre-selects it, otherwise the default is the
  last-resumed agent (remembered in `[resume] last_agent`), falling back to the session's own
  agent.

- **Filters are client-side re-filters, not re-queries.** Both `a` and `m` re-filter already
  cached rows. That keeps them instant and, importantly, keeps them working when the cloud
  half of a blended list is unreachable.

- **The machine filter's "this machine" entry is identity-based, not local-index-based.** It
  covers local rows *plus* cloud rows whose `deviceId` matches this machine — so sessions
  this machine synced and then pruned locally still appear under it. See
  [CLOUD-RESUME.md](CLOUD-RESUME.md) D15.

- **Dense / sparse is remembered.** Dense shows more sessions with less detail each; sparse
  the reverse. The choice persists in `~/.specstory/cli/config.toml` `[resume] view_mode` via
  `config.SaveResumePrefs`, a section-preserving writer that upserts only `[resume]` so the
  config template's explanatory comments survive.

- **Missing index is recovered, not reported.** If `sessions.db` doesn't exist the picker
  runs `reindex` with its normal progress UI and then continues straight into the picker,
  rather than exiting with an error telling the user to run a command.

- **Empty current project opens the browser.** If the current project has no sessions there is
  nothing to show, so the picker opens directly in the all-projects view instead of an empty
  list.

- **The all-projects view rolls up by relative date** (Today · Yesterday · Previous 7 days ·
  Previous 30 days · Older) on each project's latest activity, with per-agent count chips.
  Sorting by name would bury what you were just working on.

## Out of scope here

- Reconstruction and launch plumbing — `prepareResumeTarget`, `ExecAgentAndWatch`. See
  [SESSION-PORTABILITY.md](SESSION-PORTABILITY.md).
- Index population and freshness — see [SESSIONS-DB.md](SESSIONS-DB.md). `resume` is one of
  the staleness-trigger occasions, handled by the background warm.
- The cloud blend's design (eligibility gating, dedup, the async supplement model) — see
  [CLOUD-RESUME.md](CLOUD-RESUME.md).
