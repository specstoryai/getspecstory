<img width="1649" height="158" alt="Group 6 (1)" src="https://github.com/user-attachments/assets/93f0210f-c3ce-4035-91df-ec597e00a3ce" />

# Intent is the new source code

**Turn your AI development conversations into searchable, shareable knowledge.**

## SpecStory CLI

SpecStory CLI is a cross-platform command-line tool for saving AI coding conversations from coding agents — terminal agents (e.g. Claude Code, Cursor CLI, Codex CLI, Gemini CLI, Droid CLI) as well as the Cursor IDE.

It saves your AI coding conversations as local markdown files of each session. It can optionally sync your markdown files to the [SpecStory Cloud](https://cloud.specstory.com), turning your AI chat history into a centralized knowledge system that you can chat with and search.

## Features

- Cross-platform support (Linux, macOS)
- Seamless integration with terminal coding agents
- Command-line wrapper for terminal coding agents with markdown auto-save
- Sync all your prior conversations to local markdown files
- Automatic redaction of secrets (API keys, tokens, credentials) from saved markdown history and cloud-synced session data
- Optional: Syncs your markdown files to the SpecStory Cloud for easy search and chat
- Open source under the Apache 2.0 license

## Agent Support

The following coding agents are supported in the SpecStory CLI:

| Agent                                                     | Provider                                  | Data Format | Source Location              |
|-----------------------------------------------------------|-------------------------------------------|-------------|------------------------------|
| [Claude Code](https://www.claude.com/product/claude-code) | [claudecode](pkg/providers/claudecode/)   | JSONL       | `~/.claude/projects/`        |
| [Codex CLI](https://www.openai.com/codex/cli/)            | [codexcli](pkg/providers/codexcli/)       | JSONL       | `~/.codex/sessions/`         |
| [Cursor CLI](https://cursor.com/cli)                      | [cursorcli](pkg/providers/cursorcli/)     | SQLite      | `~/.cursor/chats/`           |
| [Cursor IDE](https://cursor.com/)                         | [cursoride](pkg/providers/cursoride/)     | SQLite      | `Cursor/User/globalStorage/` |
| [Droid CLI](https://factory.ai/product/cli)               | [droidcli](pkg/providers/droidcli/)       | JSONL       | `~/.factory/sessions/`       |
| [Gemini CLI](https://ai.google.dev/gemini-cli)            | [geminicli](pkg/providers/geminicli/)     | JSON        | `~/.gemini/tmp/`             |
| [DeepSeek TUI](https://github.com/Hmbown/DeepSeek-TUI)    | [deepseektui](pkg/providers/deepseektui/) | JSON        | `~/.deepseek/sessions/`      |

Cursor IDE stores all of its conversations in a single global SQLite database (`state.vscdb`), located at `~/Library/Application Support/Cursor/User/globalStorage/` on macOS and `~/.config/Cursor/User/globalStorage/` on Linux. The `cursoride` provider reads that database directly (Cursor 3 is supported) and filters conversations to the current project via Cursor's workspace storage. Because an IDE has no exiting process to wrap, `specstory run cursoride` opens the project in Cursor and keeps auto-saving conversations until interrupted with `ctrl-c`.

### Agent Provider SPI (Service Provider Interface)

There is also an [Agent SPI (Service Provider Interface)](/pkg/spi/) that allows you to extend the SpecStory CLI with support for new agent providers. Creating a provider to support a new agent, using the Provider SPI is documented [here](./docs/PROVIDER-SPI.md). Pull requests are welcome!

## Installation & Usage

Full end-user installation and usage instructions are in the [SpecStory CLI Documentation](https://docs.specstory.com/integrations/terminal-coding-agents). Installation for developers is covered [here](#development).

### Quickstart Usage

Basic usage: `specstory [flags]`

For help:

```zsh
specstory help
```

or

```zsh
specstory help <command>
# e.g.
specstory help run
```

Interactive auto-save mode:

```zsh
# Defaults to Claude Code if no provider is specified.
specstory run <provider>
# e.g.
specstory run codex
```

Syncing all files for the current project:

```zsh
# Defaults to syncing for all providers if no provider is specified
specstory sync <provider>
```

With a specific session UUID:

```zsh
specstory sync -s <session-uuid>
```

### Resume & Search

SpecStory indexes every session it sees into `~/.specstory/sessions.db` so you can pick up
a past session — in the same agent or a different one — without re-reading the transcript. Two
commands share the same interactive picker:

```zsh
# Open a picker of the current project's sessions across all agents. Pick one, choose which
# installed agent to continue it in, and go. `tab` switches to the all-projects browser.
specstory resume

# Pre-select the target agent — the picker then skips the target step and resumes straight
# into that agent.
specstory resume codex

# Full-text search across every indexed session, then read or resume a match. Anything after
# the command pre-seeds the query.
specstory search
specstory search max cpu
```

Inside the picker: `↑↓` move, `r` resumes, `space` previews (glamour-rendered), `/` searches,
`a` cycles the agent filter, `v` toggles dense/sparse, and `d` soft-deletes (hides from the
picker; native files on disk are untouched). See `docs/RESUME-TUI.md` and
`docs/SESSION-SEARCH.md` for the full keymap.

#### Resume a specific session directly (`--session`)

Skip the picker entirely by passing a session URI. With an agent it is fully non-interactive;
without one the target-agent picker opens pinned to that session.

```zsh
# Canonical form (what the SpecStory Cloud web app's Resume button copies):
specstory resume --session specstory://projects/{projectId}/sessions/{sessionId}

# A cloud permalink pasted straight from a browser URL bar:
specstory resume --session https://cloud.specstory.com/projects/{projectId}/sessions/{sessionId}

# Shorthand — a bare session UUID, resolved local-first then cloud:
specstory resume --session 550e8400-e29b-41d4-a716-446655440000

# Fully non-interactive: pin both the session and the target agent.
specstory resume claude --session specstory://projects/{projectId}/sessions/{sessionId}
```

A session that exists on this machine resumes in place (offline, instant) even when the URI
came from the cloud. A session from another machine is fetched from SpecStory Cloud and
reconstructed into the target agent — that path requires a SpecStory Cloud login
(`specstory login`) and a Pro plan. A pasted permalink only contributes IDs; the CLI never
sends your token to the permalink's host — if it differs from your configured cloud, pass
`--cloud-url` to match (see [Targeting a non-production cloud](#targeting-a-non-production-cloud)).

### Skills

SpecStory Cloud mines your synced sessions into reusable skills. The `skills` command lets you browse, approve, and install them into your coding agents. It requires a SpecStory Cloud login (`specstory login`) and a Pro plan.

```zsh
# Interactive browser with two tabs (press `tab` to switch):
#   • Library — preview skills, approve/reject those awaiting review, install ready
#     ones, and uninstall/reinstall installed ones.
#   • Run Activity — see past runs, kick one off (`m`), and watch it live.
specstory skills
```

Skills install using the same layout as the public `npx skills` CLI: a canonical
`~/.agents/skills/<name>` store, symlinked into each detected agent's skills directory
(Claude Code, Codex, Cursor, and more), tracked in the shared `~/.agents/.skill-lock.json`.
Installs default to global; pass `--project` to install into the current repo instead.

Every action is also a non-interactive subcommand with `--json`, so a front end (e.g. the
VS Code extension) can drive the same engine:

```zsh
specstory skills list --json                 # browse the library + local install state
specstory skills show <name>                 # print a skill's SKILL.md
specstory skills approve <name>              # approve a skill awaiting review
specstory skills reject <name> --note "..."  # reject one
specstory skills install <name>              # install for all detected agents (global)
specstory skills install <name> --project --agents claude-code,codex
specstory skills uninstall <name>            # remove files, links, and lock entry
specstory skills install <name>              # reinstall an installed skill to refresh it
specstory skills status --json               # locally installed skills (no login needed)
specstory skills run                         # mine your sessions for new skills
specstory skills runs --json                 # recent runs and their status
```

To generate new skills, kick off a run (`specstory skills run`, or press `m` in the browser to
watch it live). Runs mine your synced sessions in the cloud and take a few minutes.

### Targeting a non-production cloud

By default the CLI talks to `https://cloud.specstory.com`. To point every command at a
dev/staging cloud without passing `--cloud-url` each time, set an environment variable:

```zsh
export SPECSTORY_CLOUD_URL=https://cloud-dev.specstory.com
specstory login   # authenticate against that cloud
specstory skills  # ...and everything else now uses it
```

Resolution order is `--cloud-url` flag → `SPECSTORY_CLOUD_URL` → production default, so a
one-off `--cloud-url` still wins for a single command. `login` prints the target host when it
isn't production, so you always know which cloud you're authenticating against.

## Configuration File

SpecStory CLI supports configuration files in TOML format. Settings can be configured at two levels:

1. **User-level config**: `~/.specstory/cli/config.toml` - applies to all projects
2. **Project-level config**: `.specstory/cli/config.toml` - applies only to the current project

The configuration is determined with the following priority (highest priority to lowest priority):

1. CLI flags
2. Project-level config (`.specstory/cli/config.toml`)
3. User-level config (`~/.specstory/cli/config.toml`)

### Example Configuration

```toml
# SpecStory CLI Configuration
#
# Uncomment (remove the #) the line and edit any setting below to change the default behavior.
# For more information, see: https://docs.specstory.com/integrations/terminal-coding-agents/usage

[local_sync]
# Write markdown files locally. (default: true)
# enabled = false # equivalent to --only-cloud-sync

# Custom output directory for markdown files.
# Default: ./.specstory/history (relative to the project directory)
# output_dir = "~/.specstory/history" # equivalent to --output-dir "~/.specstory/history"

# Use local timezone for file name and content timestamps (default: false, UTC)
# local_time_zone = true # equivalent to --local-time-zone

[cloud_sync]
# Sync session data to SpecStory Cloud. (default: true, when logged in to SpecStory Cloud)
# enabled = false # equivalent to --no-cloud-sync

[logging]
# Write logs to .specstory/debug/debug.log (default: false)
# log = true # equivalent to --log        

# Debug-level output, requires console or log (default: false)
# debug = true # equivalent to --debug 

# Custom output directory for debug data.
# Default: ./.specstory/debug (relative to the project directory)
# debug_dir = "~/.specstory/debug" # equivalent to --debug-dir "~/.specstory/debug"

# Error/warn/info output to stdout (default: false)
# console = true # equivalent to --console

# Suppress all non-error output (default: false)
# silent = true # equivalent to --silent

[version_check]
# Check for new versions of the CLI on startup.
# Default: true
# enabled = false # equivalent to --no-version-check

[analytics]
# Send anonymous product usage analytics to help improve SpecStory.
# Default: true
# enabled = false # equivalent to --no-usage-analytics

[telemetry]
# OTLP gRPC collector endpoint (e.g., "localhost:4317" or "http://localhost:4317")
# endpoint = "localhost:4317"

# Override the default service name (default: "specstory-cli")
# service_name = "my-service-name"

# Include user prompt text in telemetry spans (default: true)
# prompts = false

[redaction]
# Redact secrets and API keys from saved markdown history and cloud-synced
# session data. (default: true)
# Detection uses the betterleaks ruleset, covering API keys, tokens, private
# keys, and other credentials for many providers.
# enabled = false # equivalent to --no-redact-secrets

[providers]
# Agent execution commands by provider (used by specstory run)
# Pass custom flags (e.g. claude_cmd = "claude --allow-dangerously-skip-permissions")
# Use of these is equivalent to -c "custom command"

# Claude Code command
# claude_cmd = "claude"

# Codex CLI command
# codex_cmd = "codex"

# Cursor CLI command
# cursor_cmd = "cursor-agent"

# DeepSeek TUI command
# deepseek_cmd = "deepseek"

# Droid CLI command
# droid_cmd = "droid"

# Gemini CLI command
# gemini_cmd = "gemini"
```

### Configuration Options

| Section           | Option            | Default              | Description                                |
|-------------------|-------------------|----------------------|--------------------------------------------|
| `[local_sync]`    | `enabled`         | `true`               | Write local markdown files                 |
| `[local_sync]`    | `output_dir`      | `.specstory/history` | Custom output directory for markdown files |
| `[local_sync]`    | `local_time_zone` | `false`              | Use local timezone for timestamps          |
| `[cloud_sync]`    | `enabled`         | `true`               | Sync sessions to SpecStory Cloud           |
| `[logging]`       | `debug_dir`       | `.specstory/debug`   | Custom output directory for debug data     |
| `[logging]`       | `console`         | `false`              | Output logs to stdout                      |
| `[logging]`       | `log`             | `false`              | Write logs to debug file                   |
| `[logging]`       | `debug`           | `false`              | Enable debug-level output                  |
| `[logging]`       | `silent`          | `false`              | Suppress non-error output                  |
| `[version_check]` | `enabled`         | `true`               | Check for newer CLI versions on startup    |
| `[analytics]`     | `enabled`         | `true`               | Send anonymous usage analytics             |
| `[telemetry]`     | `endpoint`        | disabled*            | OTLP gRPC collector endpoint               |
| `[telemetry]`     | `service_name`    | `"specstory-cli"`    | Service name for telemetry                 |
| `[telemetry]`     | `prompts`         | `true`               | Include prompt text in telemetry spans     |
| `[redaction]`     | `enabled`         | `true`               | Redact secrets from markdown and cloud data |
| `[providers]`     | `claude_cmd`      | `"claude"`           | Claude Code command                        |
| `[providers]`     | `codex_cmd`       | `"codex"`            | Codex CLI command                          |
| `[providers]`     | `cursor_cmd`      | `"cursor-agent"`     | Cursor CLI command                         |
| `[providers]`     | `deepseek_cmd`    | `"deepseek"`         | DeepSeek TUI command                       |
| `[providers]`     | `droid_cmd`       | `"droid"`            | Droid CLI command                          |
| `[providers]`     | `gemini_cmd`      | `"gemini"`           | Gemini CLI command                         |

\* Telemetry is enabled when an endpoint is configured unless the standard `OTEL_SDK_DISABLED` ENV var is set to `true` or `1`.

## Analytics

SpecStory CLI collects anonymous usage analytics to PostHog to help improve the product. The following events are tracked:

- Extension activation (in interacive mode) - ext_activated
- Successful markdown sync operations - ext_sync_markdown_success
- Failed markdown sync operations - ext_sync_markdown_error
- First-time autosave of new sessions - ext_autosave_success
- Failed first-time autosave of new sessions - ext_autosave_error

All analytics are processed through PostHog with GeoIP enabled for general location data.

### Disabling Analytics

To opt out of analytics tracking, use the `--no-usage-analytics` flag:

```zsh
specstory --no-usage-analytics [other options]
```

**Note**: Error tracking events include a 500ms delay before the program exits to ensure events are sent successfully. This is necessary because PostHog sends events asynchronously.

### Development Analytics

Analytics are disabled in development builds by default. To enable analytics during local development, build with the PostHog API key:

```zsh
export POSTHOG_API_KEY="your-posthog-api-key"
go build -ldflags "-X github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics.apiKey=$POSTHOG_API_KEY" -o specstory ./
```

## OpenTelemetry

SpecStory CLI supports [OpenTelemetry](https://opentelemetry.io/) Protocol (OTLP) tracing and metrics for observability. When enabled, it emits spans for session processing with detailed attributes about exchanges, messages, tool usage, and token consumption.

### Enabling Telemetry

Optional OpenTelemetry data can be enabled in three ways:

1. **Standard OTEL Environment variables** (highest priority for telemetry settings):
   
   ```zsh
   export OTEL_EXPORTER_OTLP_ENDPOINT="localhost:4317"
   export OTEL_SERVICE_NAME="specstory-cli"
   export OTEL_RESOURCE_ATTRIBUTES="user_id_hash=user_name,env=dev"
   
   specstory run
   # or
   specstory sync
   # or
   specstory watch
   ```

2. **CLI flags**:
   
   ```zsh
   specstory sync --telemetry-endpoint localhost:4317 --telemetry-service-name my-service --no-telemetry-prompts
   ```

3. **Configuration files** (`./.specstory/cli/config.toml` or `~/.specstory/cli/config.toml`):

   ```toml
   [telemetry]
   # OTLP gRPC collector endpoint (e.g., "localhost:4317" or "http://localhost:4317")
   endpoint = "localhost:4317"

   # Override the default service name (default: "specstory-cli")
   service_name = "my-service-name"

   # Include user prompt text in telemetry spans (default: true)
   prompts = false
   ```

### Telemetry Configuration Options

| Option       | CLI Flag                   | Environment Variable          | Config Key               | Default         | Description                            |
|--------------|----------------------------|-------------------------------|--------------------------|-----------------|----------------------------------------|
| Disable      | default                    | `OTEL_SDK_DISABLED`           |                          |                 | Disable telemetry                      |
| Endpoint     | `--telemetry-endpoint`     | `OTEL_EXPORTER_OTLP_ENDPOINT` | `telemetry.endpoint`     | `""`            | OTLP gRPC collector endpoint           |
| Service Name | `--telemetry-service-name` | `OTEL_SERVICE_NAME`           | `telemetry.service_name` | `specstory-cli` | Service name for spans/metrics         |
| No Prompts   | `--no-telemetry-prompts`   | -                             | `telemetry.prompts`      | `true`          | Include/exclude prompt text from spans |

**Note**: Telemetry is enabled when an endpoint is configured unless the standard `OTEL_SDK_DISABLED` ENV var is set to `true` or `1`.

### Excluding Prompt Text

You can exclude user prompt text from telemetry spans. When excluded, the `specstory.exchange.prompt_text` attribute will be empty.

Via CLI flag:

```zsh
specstory sync --no-telemetry-prompts
```

Via configuration files:

```toml
[telemetry]
# Include user prompt text in telemetry spans (default: true)
prompts = false
```

### Disabling Telemetry

Telemetry is disabled unless you configure an OTLP endpoint. If you do want to disable telemetry while keeping an endpoint configured, you can use `OTEL_SDK_DISABLED`, which is a standard OTel convention:

```zsh
export OTEL_SDK_DISABLED="true"
```

### Telemetry Data

#### Session Span Attributes

Each session processing span includes the following attributes:

| Attribute                                   | Description                                    |
|---------------------------------------------|------------------------------------------------|
| `specstory.agent`                           | The agent provider name (e.g., "claude-code")  |
| `specstory.session.id`                      | Unique session identifier                      |
| `specstory.session.exchange_count`          | Number of exchanges in the session             |
| `specstory.session.message_count`           | Total messages across all exchanges            |
| `specstory.session.tool_count`              | Total tool invocations                         |
| `specstory.session.tool_type_count`         | Number of unique tool types used               |
| `specstory.project.path`                    | Workspace root path                            |
| `specstory.session.tokens.input`            | Total input tokens (all providers)             |
| `specstory.session.tokens.output`           | Total output tokens (all providers)            |
| `specstory.session.tokens.cache_creation`   | Cache creation tokens (Claude Code, Droid CLI) |
| `specstory.session.tokens.cache_read`       | Cache read tokens (Claude Code, Droid CLI)     |
| `specstory.session.tokens.cached_input`     | Cached input tokens (Codex CLI)                |
| `specstory.session.tokens.reasoning_output` | Reasoning output tokens (Codex CLI)            |
| `specstory.session.tokens.cached`           | Cached tokens (Gemini CLI)                     |
| `specstory.session.tokens.thought`          | Thought/reasoning tokens (Gemini CLI)          |
| `specstory.session.tokens.tool`             | Tool-related tokens (Gemini CLI)               |
| `specstory.session.tokens.thinking`         | Thinking tokens (Droid CLI)                    |

#### Exchange Span Attributes

Each exchange is recorded as a child span with these attributes:

| Attribute                                    | Description                                    |
|----------------------------------------------|------------------------------------------------|
| `specstory.exchange.id`                      | Exchange identifier                            |
| `specstory.exchange.index`                   | Exchange index in session                      |
| `specstory.exchange.model`                   | Model used for this exchange                   |
| `specstory.exchange.prompt_text`             | User prompt text (unless `no_prompts` is set)  |
| `specstory.exchange.start_time`              | Exchange start timestamp                       |
| `specstory.exchange.end_time`                | Exchange end timestamp                         |
| `specstory.exchange.message_count`           | Messages in this exchange                      |
| `specstory.exchange.tools_used`              | Comma-separated tool names                     |
| `specstory.exchange.tool_types`              | Comma-separated tool types                     |
| `specstory.exchange.tool_count`              | Number of tool invocations                     |
| `specstory.exchange.tokens.input`            | Input tokens for this exchange                 |
| `specstory.exchange.tokens.output`           | Output tokens for this exchange                |
| `specstory.exchange.tokens.cache_creation`   | Cache creation tokens (Claude Code, Droid CLI) |
| `specstory.exchange.tokens.cache_read`       | Cache read tokens (Claude Code, Droid CLI)     |
| `specstory.exchange.tokens.cached_input`     | Cached input tokens (Codex CLI)                |
| `specstory.exchange.tokens.reasoning_output` | Reasoning output tokens (Codex CLI)            |
| `specstory.exchange.tokens.cached`           | Cached tokens (Gemini CLI)                     |
| `specstory.exchange.tokens.thought`          | Thought/reasoning tokens (Gemini CLI)          |
| `specstory.exchange.tokens.tool`             | Tool-related tokens (Gemini CLI)               |
| `specstory.exchange.tokens.thinking`         | Thinking tokens (Droid CLI)                    |

## Development

### Development Prerequisites

- macOS development environment
- Go 1.26.5 or later
- golangci-lint, latest version
- Access to one or more terminal coding agents (e.g. Claude Code, Codex CLI, etc.)

You'll want [Homebrew](https://brew.sh/) installed on your macOS system. Then:

```zsh
brew install go golangci-lint
```

You'll also want this test helper:

```zsh
go install gotest.tools/gotestsum@latest
```

### Building from source

```zsh
# Clone the repository
git clone https://github.com/specstoryai/specstory-cli.git

# Navigate to the project directory
cd specstory-cli

# Build the project
go build -o specstory
```

You can then run the built executable from there.

### Check for Outdated Dependencies

```zsh
go list -m -u all
```

### Debug Raw Mode

The `--debug-raw` flag enables a debug mode that is useful for developers working on the SpecStory CLI. It outputs the raw data from AI coding agents in a pretty-printed format. This hidden flag works with all operation modes and supports all providers (Claude Code, Cursor CLI, Cursor IDE, Codex CLI, Gemini CLI, Droid CLI).

When enabled, it creates a debug directory structure under `.specstory/debug/` with individual pretty-printed JSON files for each record in the session as well as a JSON version of the SessionData returned from the provider for that session.

This mode is useful for:
- Understanding the raw data structure from different AI coding agents
- Analyzing conversation flow and metadata
- Debugging parsing issues
- Troubleshooting agent-specific data formats

Run mode with debug output:

```zsh
./specstory run --debug-raw
```

Sync mode with debug output:

```zsh
./specstory sync --debug-raw
```

Sync specific session with debug output:

```zsh
./specstory sync -s <session-id> --debug-raw
```

**Output Structure:**

```
.specstory/debug/
└── <session-uuid>/
    ├── 1.json      # Claude Code: sequential numbering
    ├── 2.json      # Cursor CLI: based on rowid
    ├── 3.json
    ├── ...
    ├── raw-composer.json # Cursor IDE: the full raw composer record for the session
    └── session-data.json # JSON version of the SessionData returned from the provider for this session
```

Each JSON file is pretty-printed with 2-space indentation. For Claude Code, files are numbered sequentially based on their position in the JSONL file. For Cursor CLI, files are numbered based on the SQLite rowid. For Cursor IDE, a single `raw-composer.json` file holds the complete composer record instead of numbered per-record files.

**Example:**

If processing a session with ID `30cc3569-a9d4-429e-981a-ab73e3ddee5f`, the debug files will be created in: `.specstory/debug/30cc3569-a9d4-429e-981a-ab73e3ddee5f/`

## Testing

To run all tests with easy to read output:

```zsh
gotestsum ./...
```

Run tests with verbose output:

```zsh
go test -v ./...
```

Test specific packages:

```zsh
go test -v ./pkg/cli 
```

Run specific tests (e.g., filename generation tests)

```zsh
go test  -v ./pkg/cli -run TestGenerateFilenameFromUserMessage
```

Testing specific features

```zsh
# Test the new filename generation logic
go test -v ./pkg/cli -run "TestExtractWordsFromMessage|TestGenerateFilenameFromWords|TestGenerateFilenameFromUserMessage"
```

## Linting

This project uses [golangci-lint](https://golangci-lint.run/) for code quality checks. The configuration enables all default linters plus `gofmt` and `goimports` for consistent formatting.

### Running the linter

Check all Go files in the project:

```zsh
golangci-lint run
```

Automatically fix issues where possible:

```zsh
golangci-lint run --fix
```

Check a specific package:

```zsh
golangci-lint run ./pkg/analytics/...
```

**Note:** Always run the linter on directories or packages, not individual files. Running on single files can cause false positives where symbols from other files in the same package cannot be resolved.

Format code:

```zsh
gofmt -w .
```

### Linter Configuration

The linter configuration is in `.golangci.yml`. Key linters include:
- **errcheck**: Ensures error return values are checked
- **gofmt**: Enforces standard Go formatting
- **goimports**: Manages import statements
- **staticcheck**: Comprehensive bug detection
- **govet**: Reports suspicious constructs

To see all enabled linters:

```zsh
golangci-lint linters
```

## License

The SpecStory CLI is licensed under the [Apache 2.0 open source license](LICENSE.txt).

Copyright 2025-2026 by SpecStory, Inc., All Rights Reserved.

SpecStory® is a registered trademark of SpecStory, Inc.
