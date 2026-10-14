# Copilot CLI manual tests

Run on macOS against the versions in [`versions.txt`](versions.txt), with an isolated `HOME` and `COPILOT_HOME` (only the login copied) unless noted. Cloud sync, analytics and version checks were off.

| Test | Command | Result |
| --- | --- | --- |
| check | `specstory check copilot`; then `-c /nonexistent/copilot` | Pass: version reported; the bad path exits non-zero and the error names the path. |
| sync | `sync copilot` twice in a project with sessions | Pass: files written once; the second run changes no bytes. |
| sync one session | `sync copilot -s <id>` and `-s <id> --print` | Pass: one file written; `--print` writes it to stdout only. |
| sync, nothing to do | `sync copilot` in a directory with no sessions | Pass: guidance printed, exit 0. |
| debug raw | `sync copilot --debug-raw --log --debug` | Pass: one numbered file per record (114) plus `session-data.json`; no schema warnings. |
| run | `run copilot` in a never-seen project | Pass: exit 0, session saved; agent output not interleaved with SpecStory's. |
| run, failing agent | `run copilot` where the agent exits 3 | Pass: exit status 3 propagates; the last turn is saved. |
| run with debug raw | `run copilot --debug-raw` | Pass: raw records written. |
| watch | `watch copilot` in a never-seen project, then a new Copilot session there | Pass: existing sessions left alone; the new session printed and saved. |
| resume, same agent | `resume <copilot session>` into Copilot | Pass: the model recalled earlier content; the native file grew (23 to 33 lines) and the same markdown file was updated. |
| resume into Copilot | `resume copilot` from a Claude Code session | Pass: migration note first, provenance recorded, prior content recalled, no warnings. |
| resume into Copilot, slash command | `resume copilot` from a real Claude Code turn that starts with `/unit-tests:add` and has thinking, Bash/Read/Edit calls and agent text | Pass: migration note first, text and rendered tool activity in order, thinking as agent text; Copilot recalled the request and a command it ran. The command tags are not replayed. Claude's skill-expansion records (`isMeta` user records) are replayed as user turns: that is the shared `spi.PrepareTurns` path every target uses, not this provider. |
| resume from Copilot | `resume claude` from a Copilot session that starts with the `/customize-cloud-agent` skill command and has tool calls | Pass, through a `claude` shim (Claude Code is not installed and needs a login): the reconstructed Claude file has the migration note first, the typed prompt, each tool call as agent text in order, then the answer; the skill's injected context (`skill.*` records) is not replayed; Claude is launched with `--resume <new id>` in the project. Recall by a live Claude was not tested. In the author's 150 most recent sessions, only skill commands appear as user messages; built-in commands leave no user record. |
| list | `list copilot` | Pass: slugs match the history filenames. |
| reindex / search | `reindex`, then the search index | Pass: 13 sessions under the right projects; FTS rows verified directly (the search TUI needs a TTY). |
| space and underscore path | `sync` and `list` from `/tmp/…/proj with space_x` | Pass. |
| symlinked path | `sync` and `list` from a symlink to the same project | Pass: same sessions found as from the real path. |
| watch via symlink with output dir | `watch copilot --output-dir <dir>` started from the symlink; Copilot then run from the real path | Pass: the new session was saved to `<dir>` and nothing was written into the project. |
| whole store | `sync copilot --console` in each of the 52 project directories in the author's store | Pass: 126 sessions written, 0 Warn or Error log records. |
| factory: latest-version | `factory/latest-version` under `env -i` | Pass: prints `1.0.94` (npm `latest`). |
| factory: install | `factory/install 1.0.94` under `env -i` | Pass: installs and verifies the banner; a bogus version and an empty version exit 1. |
| factory: list-tools | `factory/list-tools` twice with `COPILOT_GITHUB_TOKEN` set | Pass: both readings list the same 25 tools; missing token exits 1. |
