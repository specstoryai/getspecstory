# `specstory resume` — the picker TUI

The interactive session picker that makes SpecStory the best way to resume a coding-agent
session. It reads the [`sessions.db` index](SESSIONS-DB.md) and launches the chosen session
via the existing reconstruct + `ExecAgentAndWatch` plumbing (see
[SESSION-PORTABILITY.md](SESSION-PORTABILITY.md)). This doc covers the UX and the build plan;
it replaces the old plain numbered-menu selection in `pkg/cmd/resume.go`.

## Decisions (ratified)

- **TUI stack:** Bubble Tea v2 + Bubbles v2 + Lipgloss v2 (the `charm.land/*/v2` modules —
  latest, and aligned with the `lipgloss/v2` already pulled in by `fang`). Not the v1 stack
  stoa-cli pins; we use the latest.
- **No source-agent step.** The picker shows **all sessions across all agents** for the
  current project by default, each row tagged with its agent. (The old flow's "pick the source
  agent first" is gone.)
- **Empty current project → all-projects view.** If the current project has no sessions, skip
  the project list and open directly in the all-projects view.
- **Empty/missing `sessions.db` → reindex first.** If the index doesn't exist, run `reindex`
  (with its normal progress UI) and then continue straight into the picker (don't exit).
- **Dense / sparse view modes.** Dense = more sessions, less per-session detail; sparse = more
  detail, fewer sessions. Toggle is easy/obvious; the choice is **remembered** in
  `~/.specstory/cli/config.toml` `[resume] view_mode` (via `config.SaveResumePrefs`).
- **Preview pane** shows the session: first user message · truncated middle · final message,
  sized to fit. Sourced from the stored FTS body (`Store.SessionBody`); Cursor/metadata-only
  sessions fall back to name/slug.
- **All-projects view rolls up by relative date** (Today · Yesterday · Previous 7 days ·
  Previous 30 days · Older) by each project's latest activity, showing per-agent session counts
  (`Store.ListProjects`); the user expands a project to see its sessions.
- **Target agent (last step).** `specstory resume` lets the user pick the target agent as the
  final step; `specstory resume <agent>` pre-selects it. The **last-resumed agent** is the
  enter-default, remembered in `[resume] last_agent`.
- **Two distinct searches, not one mode-switching box.** Project search (filter the project
  list by name) and session full-text search (FTS over conversation bodies) are separate
  affordances with separate entry points — typing in the project list filters projects; a
  dedicated full-text search is its own surface.

## Data foundation (built)

- `config`: `[resume]` section (`view_mode`, `last_agent`), `GetResumeViewMode()` /
  `GetResumeLastAgent()`, and `SaveResumePrefs()` — a section-preserving writer that upserts
  only `[resume]` so the self-documenting template's comments survive.
- `sessionindex`: `ListByProject(projectID)` (sessions, newest first), `ListProjects()`
  (date-sortable rollup with per-agent counts), `SessionBody(agent, sessionID)` (preview),
  `Search(query)` (global FTS). All tested.

## Build plan

### Stage A — current-project picker **(built)**

`pkg/cmd/resume_tui.go` — a Bubble Tea v2 model wired into `resume.go`:

- Mixed-agent session list for the current project (`ComputeProjectID(cwd)` →
  `ListByProject`), newest first, colored agent tags.
- Agent filter (`a` cycles all → each present agent); dense/sparse toggle (`v`, persisted
  via `SaveResumePrefs`); preview overlay (`space`, first/middle/last from `SessionBody`);
  full-text search (`/` → FTS, scoped to the project).
- Missing/empty `sessions.db` → `reindex` (normal progress UI) then continue.
- Select a session (`↵`) → target-agent step (pre-selected by `resume <agent>`; else the
  last-resumed agent; else the session's own agent) → hands off to the existing
  `prepareResumeTarget` + `ExecAgentAndWatch`.
- Keys: `↑↓`/`jk` move · `↵` resume · `space` preview · `/` search · `a` agent · `v`
  dense/sparse · `q`/`esc` quit.

**Deferred to Stage B / follow-up:** empty current project currently shows a message rather
than jumping to all-projects (that view *is* Stage B); the `tab` scope toggle; and persisting
the view-mode on cancel (today it saves only on a committed resume). The interactive UX itself
is validated by running it in a real terminal (it can't be exercised headless).

### Stage B — all-projects browser

- Date-bucketed rollup (`ListProjects`) → expand a project → its sessions.
- Scope toggle current ↔ all (easy/obvious).
- Project search (filter list) + session full-text search (FTS).

## Out of scope (here)

- Reconstruction / launch plumbing — unchanged (`prepareResumeTarget`, `ExecAgentAndWatch`).
- Index population / freshness — see [SESSIONS-DB.md](SESSIONS-DB.md). `resume` is one of the
  staleness-trigger occasions, handled in the warm-keeping thread.
