# Specstory CLI Changelog

## v0.18.0 2026-01-16

### 📢 Announcements

- The SpecStory CLI is now open sourced under the [Apache 2.0 license](https://github.com/specstoryai/getspecstory/blob/main/specstory-cli/LICENSE.txt) at `https://github.com/specstoryai/getspecstory/tree/main/specstory-cli` and [contributions](https://github.com/specstoryai/getspecstory/blob/main/specstory-cli/CONTRIBUTING.md) are welcome!

### ⚙️ Improvements

- Include the agent's question in the markdown output from `AskUserQuestion` tool use with the Claude Code provider.

## v0.17.0 2026-01-07

### ⚙️ Improvements

- A major internal refactor was done to move markdown generation from the providers to the CLI layer, and to standardize the markdown output across all providers.
- Updated the markdown version to `v2.1.0` with tool types standardized as ` write`, `read`, `search`, `shell`, `task`, `generic`, `unknown`, and with timestamps on all user and agent messages, where available.
- The Cursor CLI provider now generates SpecStory markdown `v2.1.0` files, which support a significantly enhanced web UI when sessions are viewed in the SpecStory Cloud web app.
- The Cursor CLI provider now preserves model "thinking" content in the markdown output.
- The Claude Code provider now provides timestamps for user and agent messages in the markdown output.
- The Claude Code provider no longer includes the `<system-reminder>...</system-reminder>` content about malicious content in the generated markdown on "Read" tool results.
- The Codex CLI provider has improved markdown output for the `shell_command` tool.

### 🐛 Bug Fixes

- Fixed an issue where the CLI would still initialize the analytics client library when the `--no-usage-analytics` flag was used.

## v0.16.0 2025-11-21

### ⚙️ Improvements

- The SpecStory CLI now supports Google's [Gemini CLI](https://github.com/google-gemini/gemini-cli) (i.e. `gemini`) for sessions created from Gemini CLI version `0.15.1` or higher. Sessions from earlier versions may work, but are not officially supported. This provides the same support for saving to local markdown files and to the SpecStory Cloud as for [Claude Code](https://claude.ai/docs/api/claude-code), [Cursor CLI](https://cursor.com/docs/cli) and [Codex CLI](https://developers.openai.com/codex/cli/).

### 🐛 Bug Fixes

- Fixed an issue where the Cursor CLI provider would not find sessions when a symlink was anywhere in the current working directory.

## v0.15.0 2025-11-15

### ⚙️ Improvements

- The Codex CLI provider now generates SpecStory markdown v2.0.0 files, which support a significantly enhanced web UI when sessions are viewed in the SpecStory Cloud web app.
- The Codex CLI provider now supports markdown output for the `apply_patch` tool, which is used to apply a patch to a file.

## v0.14.1 2025-11-14

### 🐛 Bug Fixes

- `specstory run` with the Claude Code provider no longer creates additional markdown files for "Warmup" messages.
- `specstory run` would give SpecStory Cloud sync stats timeout count of n - 1 sessions, where n is the number of incremental syncs attempted. This has been fixed.

## v0.14.0 2025-11-12

### ⚙️ Improvements

- The Claude Code provider now skips putting "Warmup" messages in the markdown output.
- The Claude Code provider now generates SpecStory markdown v2.0.0 files, which support a significantly enhanced web UI when sessions are viewed in the SpecStory Cloud web app.
- "Bash" tool use now supports multi-line commands in the markdown output for the Claude Code provider.
- "Grep" tool use now extracts the pattern and path from the tool use arguments and displays them in the markdown output for the Claude Code provider.
- Reduced network activity during more efficient bulk SpecStory Cloud syncs.
- Display count of session syncs that timed out during SpecStory Cloud syncs.
- Increased the timeout for SpecStory Cloud syncs to 120 seconds to account for initial syncs of large projects with many sessions.

## v0.13.0 2025-10-26

### ⚙️ Improvements

- Introduced a SpecStory Cloud sync debounce in `specstory run` to reduce the number of network syncs to the SpecStory Cloud.
- Removed the `<user_query>` tags around user messages in the markdown file names and markdown content that's output from newer versions of the Cursor CLI.

### 🐛 Bug Fixes

- Fixed the SpecStory cloud counters displayed after `specstory run` to match the actual number of sessions created/updated/skipped.

## v0.12.0 2025-10-23

### ⚙️ Improvements

- Skip the synthetic "warmup" user message that's always present in newer Claude Code sessions to generate more unique slugs for SpecStory history markdown filenames.
- A pure Go SQLite library now replaces the C-based SQLite library for Cursor CLI provider database operations, improving cross-platform compatibility and performance.

## v0.11.0 2025-10-05

### ⚙️ Improvements

- The SpecStory CLI now supports OpenAI's [Codex CLI](https://developers.openai.com/codex/cli/) (i.e. `codex`) for sessions created from Codex CLI version `0.42.0` or higher. Sessions from earlier versions may work, but are not officially supported. This provides the same support for saving to local markdown files and to the SpecStory Cloud as for [Claude Code](https://claude.ai/docs/api/claude-code) and [Cursor CLI](https://cursor.com/docs/cli).


## v0.10.1 2025-09-30

### 🐛 Bug Fixes

- Fixed an issue where the CLI would abort when encountering JSON in a Claude Code JSONL file that was larger than 10MB [issue #108](https://github.com/specstoryai/getspecstory/issues/108) The new limit is 250MB.


## v0.10.0 2025-09-26

### ⚙️ Improvements

- The SpecStory CLI now supports the [Cursor CLI](https://cursor.com/cli) (i.e. `cursor-agent`) for sessions created from Cursor CLI version `2025.09.18-7ae6800` or higher. Sessions from earlier versions may work, but are not officially supported. This provides the same support for saving to local markdown files and to the SpecStory Cloud as for [Claude Code](https://claude.ai/docs/api/claude-code).

### 🔧 CLI Configuration & Commands

- The `check`, `run` and `sync` commands now take an optional `<agent-provider>` argument to specify the provider to use for the command, e.g. `specstory check claude`, `specstory run cursor`, or `specstory sync claude`,  if unspecified it defaults to `claude` for `run` and both providers for `check` and `sync`
- The `-s` / `--session` flag replaces the `-u` / `--session-uuid` flag to specify a specific session ID for the `sync` command 
- The `--command` flag replaces the `--claude-code-cmd` flag to specify a custom command to run the agent

## v0.9.1 2025-09-10

### 🐛 Bug Fixes

- Fixed an issue where special characters in the project name were not being properly handled to find the Claude Code project directory [issue #102](https://github.com/specstoryai/getspecstory/issues/102)
- Fixed an issue where a symlink anywhere in the project directory was not being properly handled to find the Claude Code project directory [issue #85](https://github.com/specstoryai/getspecstory/issues/85)


## v0.9.0 2025-09-07

### ⚙️ Improvements

- Search across all your AI Chat history with the [SpecStory Cloud](https://cloud.specstory.com) web app
- Automatically synchronize your Claude Code AI chat sessions with SpecStory Cloud, when logged in, using `specstory sync` and `specstory run`

### 🔧 CLI Configuration & Commands

- Login to SpecStory Cloud using `specstory login`, logout using `specstory logout`
- Skip cloud sync, when logged in, using the `--no-cloud-sync` flag

### 🐛 Bug Fixes

- Don't create a new markdown file for a resumed or compacted session, append the resumed session to the existing markdown file
- Fixed stray usage output during non-usage related errors
- Fixed stray log output to stdout when running `specstory -v`, `specstory --version`, `specstory --help`, `specstory -h`, and invalid flags.


## v0.8.0 2025-08-06

### ⚙️ Improvements

- Skip writing markdown files for sessions that have no user messages, this is especially useful to avoid creating empty markdown files when using interactive mode (`specstory run`)
- Improved the output of `specstory sync`, to include showing the progress of parsing and processing JSONL files

### 🔧 CLI Configuration & Commands

- Fancy-schmancy new SpecStory logo that shows for `specstory` and  `specstory help`

### 🐛 Bug Fixes

- Use of `/clear` command in Claude Code was resulting in subsequent sessions that were not being written to markdown files
- Use of `/compact` or force-compacting the history in Claude Code was resulting in subsequent sessions that were not being written to markdown files
- Fixed stray log output to stdout when running `specstory run`, `specstory version`, and `specstory help`


## v0.7.0 2025-08-04

### ⚙️ Improvements

- `specstory check` now tells you where the `claude` command that SpecStory will use is located
- Uses the Claude Code provided summary of the session as the header of the markdown file, if available
- Normalized the header formatting of markdown files to match the Cursor/VSC extension's markdown
- Improved the explanation provided when `specstory sync` is run in a directory that is not an existing Claude Code project directory
- Creates a `.specstory/.project.json` file to track the project ID and name the project, in preparation for the future cloud sync feature in the CLI
- Moved to structured logging with `log/slog`
- Moved the log output (`--log` flag) to `.specstory/debug/debug.log` file
- Improved the output of `specstory sync` to show the progress of parsing JSONL files and syncing markdown files

### 🔧 CLI Configuration & Commands

- New `--console` flag for the `run` and `sync` commands to enable error/warn/info debug output to stdout, replaces the `--verbose` flag
- New `--debug` flag for the `run` and `sync` commands to enable debug level output to `--log` or `--console` output


## v0.6.0 2025-07-17

### ⚙️ Improvements

- Support for the new "Native binary installation" of Claude Code (`curl -fsSL claude.ai/install.sh | bash`) which results in a `~/.local/bin/claude` binary

### 🔧 CLI Configuration & Commands

- New `--output-dir` flag for the `sync` and `run` commands to specify the output directory for the markdown files and logs, [issue #86](https://github.com/specstoryai/getspecstory/issues/86)
- Added `--resume <session-id>` flag to the `run` command to resume a specific session by ID

### 🐛 Bug Fixes

- Fixed an issue where sometimes the `run` command would result in duplicate markdown files with slightly offset filename timestamps for the interactive session


## v0.5.0 2025-07-14

### ✏️ Markdown Enhancements

- Include content from the agent `thinking` in the markdown output
- Improved markdown formatting for agent tool use for "MultiEdit"
- Indicate if tool use was successful or not in the markdown output
- Fix the `---` separator between user messages and agent messages

### ⚙️ Improvements

- No longer use a `.specstory/.history.json` file to track sessions

### 🔧 CLI Configuration & Commands

- More helpful output from `specstory check` when Claude Code installed and accessible
- Helpful output from `specstory check` when Claude Code cannot be run

### 📦 Distribution & Build

- Improved the version update check to include the new version number in the output

### 🐛 Bug Fixes

- `--version` flag was not working, only the `version` command was working
- Fixed duplicate output of error messages


## v0.4.0 2025-07-09

### ✏️ Markdown Enhancements

- Improved markdown formatting for agent tools use for "Bash", "Write", "Read" and "Grep"
- Change attribution of chats from Claude Code to "Agent" rather than "Assistant"
- Include the model and model version in the "Agent" attribution
- Don't include the `isMeta`/`true` user messages "Caveat: The messages below were generated by the user.." in the markdown file
- Good markdown formatting for `/` commands entered by the user in Claude Code
- Better markdown file naming by skipping `isMeta` and `/` commands from the user message for naming the filename
- Don't include the `<system-reminder>...</system-reminder>` about malicious content in the generated markdown on "Read" tool results

### 🔧 CLI Configuration & Commands

- Moved from a flag based CLI to one based on commands
  - Commands for: `run`, `sync`, `check`, `help`, `version`
  - Flags for: `--log`, `--verbose`, `--silent`, `--no-version-check`, `--no-usage-analytics`
- Command specific help, e.g. `specstory help sync`
- Fully styled help via [Fang](https://github.com/charmbracelet/fang)
- Improved help text and example usage

### 🐛 Bug Fixes

- Remove the proactive check for Claude Code in the PATH, as this no longer works with Anthropic's self-managed install approach (`~/.claude/local/claude` with a `claude` alias)
- Instead of defaulting to `claude` when no Claude Code Command (`-c`) is provided, first check for the presence of `~/.claude/local/claude` and use that as the default if present, to work with Anthropic's self-managed install approach
- If `~` is used in the Claude Code Command (`-c`) it was being treated literally, rather than expanded


## v0.3.0 2025-07-03

### ✏️ Markdown Enhancements

- Generate markdown files with formatted todos including priority indicators (🔥/🌡️/❄️) and completion status (`[ ]`, `[⚡]`, `[X]`) for the `TodoWrite` tool

### ⚙️ Improvements

- Added progress and summary output to runs of the `-s` sync markdown command
- Add result output to runs of the `-u` single session command

### 🔧 CLI Configuration & Commands

- Added `--silent` mode to suppress output during single session (`-u`) or sync (`-s`)

### 🐛 Bug Fixes

- Flag validation - Using `--silent` and `-v` flags together returns an error


## v0.2.0 2025-07-01

### ✏️ Markdown Enhancements

- Now when the user message to Claude Code consists of just an image, or includes one or more images, `specstory` will include this fact in the generated markdown file.
- Now when Claude Code breaks up user messages into multiple parts, `specstory` includes all the parts in the generated markdown file, not just the first part.

### 🐛 Bug Fixes

- Fixed an issue where `specstory` would not find the Claude Code `~/.claude/projects` directory for the project if the project path contained the `_` character.
- Fixed an issue where running with the `-u` flag when there was no `.specstory/history` directory would cause `specstory` to exit with an error


## v0.1.0 2025-06-26

### ⚙️ Improvements

- Deterministic filenames - Implemented deterministic filenames for consistent builds

### 📦 Distribution & Build

- Automatic version checking - Check for newer versions of `specstory` and output a message if available
- Version check bypass - Use `--no-version-check` to disable version checking for current run

### 🔧 CLI Configuration & Commands

- Installation verification - Added `--check-install` command for system validation
- Logging control - New `--log` flag for enhanced debugging capabilities
- Flag validation - Using `-s` and `-u` flags together now properly returns an error
- Analytics control - Use `--no-usage-analytics` to disable analytics for current run
- Version check bypass - Use `--no-version-check` to disable version checking for current run

### 📊 Analytics

- Analytics control - Use `--no-usage-analytics` to disable analytics for current run
- Session tracking - Enhanced analytics integration with configurable opt-out options

### 🐛 Bug Fixes

- Sidechain stability - Fixed hanging issues in sidechain operations
- Exception handling - Resolved exception errors in sidechain processing
- Duplicate prevention - CLI no longer writes duplicate files
- Major stability fix - Resolved critical stability issue (the big fix)



## v0.0.3 2025-06-20

### 📦 Distribution & Build

- Version updates - Update pinned CLI version

### 📊 Analytics

- Shared analytics ID - CLI now sync analytics IDs (macOS only)
- PostHog tracking - Added basic PostHog tracking of activated sessions when CLI runs in interactive mode



## v0.0.2 2025-06-20

### ✏️ Markdown Enhancements

- Tool argument display - Show grep tool arguments in markdown output
- Tool result messages - Tool outputs now properly display as assistant responses

### ⚙️ Improvements

- Better error handling - No longer checks if CLI path exists, just tries to run and shows errors if they occur

### 📦 Distribution & Installation

- Homebrew support - Fixed Homebrew setup and artifact URLs for easier installation
- Dual archive format - CLI now distributed as both tar.gz and zip archives
- Build improvements - Added ID to archives for better build tracking

### 🔧 CLI Configuration & Commands

- Verbose flag - Added `-v` verbose flag to show informational logging only when needed
- Updated flags - Improved CLI flag handling and configuration
