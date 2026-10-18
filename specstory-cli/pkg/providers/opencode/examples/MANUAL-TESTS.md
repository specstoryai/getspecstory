# OpenCode Manual Test Results

Real-agent verification of the provider against the guide's command matrix. Environment: see `versions.txt` (OpenCode 2.0.14, macOS 26.6.2 arm64, a freshly built `./specstory` from this branch). Models: `opencode/big-pickle` (free OpenCode Zen) for new sessions, `fireworks-ai/accounts/fireworks/models/glm-5p3-flash` for sessions resumed from reconstructed history (Zen declines imported history, see below). Linux and native Windows were not exercised against the real agent; CI runs the unit tests on Windows.

| Command | Result |
|---|---|
| `./specstory check opencode` | Pass: `opencode v2.0.14`, `/opt/homebrew/bin/opencode`, success hint advertises `specstory run opencode`. |
| `./specstory check opencode -c /nope/opencode` | Pass: fails with "OpenCode could not be found" and names `/nope/opencode`. |
| `./specstory sync`, `sync opencode` | Pass: one markdown file per session with a prompt; a second sync reports every session up to date and changes no bytes (checksums compared). |
| `./specstory sync -s <id>` | Pass. |
| `./specstory sync -s <id> --print` | Pass: prints the session, writes no history file. Run from another project it reports the session as not found, because OpenCode sessions are scoped to the directory they were started in. |
| `./specstory sync opencode` in a directory with no sessions | Pass: OpenCode's own guidance, exit 0. |
| `./specstory sync --debug-raw --log --debug` | Pass: `.specstory/debug/<id>/` holds `1.json` (session row) and one numbered file per message row, plus `session-data.json`; `debug.log` has no `schema validation:` warnings. |
| `./specstory run opencode` (never-seen project, `--debug-raw`) | Pass: the TUI owns the terminal; markdown appears after the first turn and grows with the second; the watcher stops and the last turn is saved on exit; exit status 0. |
| `./specstory run opencode -c <wrapper that exits 3>` | Pass: the CLI exits 3 and the last turn is saved. |
| `./specstory watch opencode` | Pass: four existing sessions left alone; a new headless session is saved (created, then updated). |
| `./specstory watch opencode --output-dir <dir>` (never-seen project) | Pass: the new session is saved to `<dir>`, nothing under the project's own history. |
| `watch` with no possible store (`OPENCODE_DB=:memory:`) | Pass: exits 1 with "failed to start watcher". |
| `./specstory resume opencode` (same agent, via the picker) | Pass: OpenCode opens the session with prior turns, answers "hello" about an earlier turn, the same native session grows (8 to 11 records), the same markdown file updates. |
| `./specstory resume claude` from an OpenCode session | Pass: Claude Code opens the reconstructed session with "Resumed from a OpenCode session via SpecStory." first, every user prompt present (including a `!` shell command), no OpenCode scaffolding (Plan-mode reminders, shell output echoes) replayed; recalls PURPLE-ELEPHANT-42. Source had thinking, tools, a user shell command, `/review` and a compaction. |
| `./specstory resume opencode` from a Claude Code session | Pass: the export is staged privately, imported with `opencode session import`, and opened with `opencode -s`; OpenCode answers BLUE-GIRAFFE-7 from the imported context on the first prompt, shows no warning, and appends to the same session. Source had thinking, a Bash call and `/context`. |
| `./specstory list opencode` | Pass: every synced session listed; each slug matches its filename. |
| `./specstory reindex` | Pass: 27 OpenCode sessions (every top-level session with a prompt in the database), each attributed to the project it was started in; subagent child sessions excluded. |
| `sync`, `list`, `watch` via a symlink to a directory whose name has a space and an underscore | Pass: the same sessions as the canonical path; `watch` via the symlink saved the new session. |
| Bare `./specstory run` | Launches Claude Code as documented (unchanged by this provider). |

## Limitations found

- OpenCode stores an expanded slash command (for example `/review`) as a plain user record with no marker naming the command, so the template text renders as the user's prompt and is replayed when resumed into another agent.
- OpenCode's free Zen models refuse to continue any conversation whose assistant turns were not produced through Zen (`OpenCode's free tier can only be used from within OpenCode`, HTTP 403). A session resumed into OpenCode needs another configured model; this is independent of the reconstruction format.
- `opencode run -s <id>` stalls on the first prompt into a freshly imported session; the TUI, which `specstory resume` launches, is not affected.
- `specstory resume --session <id>` accepts only UUID-shaped ids (shared CLI parsing), so an OpenCode `ses_...` id cannot be passed there; the picker works.

## Outside this provider (observed while testing)

- Claude Code 2.1.280 records `/context` output twice: as tagged `local_command` system records (filtered) and as an `isMeta` user record with a markdown copy, which the Claude Code provider renders and replays as a user prompt.
- `specstory resume` blocks while a provider's `Check` hangs (`agy --version` hung here), because checks have no timeout.
