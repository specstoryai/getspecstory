<img width="1649" height="158" alt="Group 6 (1)" src="https://github.com/user-attachments/assets/93f0210f-c3ce-4035-91df-ec597e00a3ce" />

# Intent is the new source code

**Turn your AI development conversations into searchable, shareable knowledge.**

## SpecStory CLI

SpecStory CLI is a cross-platform command-line tool for saving AI coding coversations from terminal coding agents (e.g. Claude Code, Cursor CLI, Codex CLI, Gemini CLI, etc.).

It saves your AI coding conversations as local markdown files of each session. It can optionally sync your markdown files to the [SpecStory Cloud](https://cloud.specstory.com), turning your AI chat history into a centralized knowledge system that you can chat with and search.

## Features

- Cross-platform support (Linux, macOS)
- Seamless integration with terminal coding agents
- Command-line wrapper for terminal coding agents with markdown auto-save
- Sync all your prior conversations to local markdown files
- Optional: Syncs your markdown files to the SpecStory Cloud for easy search and chat
- Open source under the Apache 2.0 license

## Agent Support

Agent providers:

- [Claude Code](https://www.claude.com/product/claude-code): [Claude Code Provider](pkg/providers/claudecode/)
- [Cursor CLI](https://cursor.com/cli): [Cursor CLI Provider](pkg/providers/cursorcli/)
- [Codex CLI](https://www.openai.com/codex/cli/): [Codex CLI Provider](pkg/providers/codexcli/)
- [Gemini CLI](https://ai.google.dev/gemini-cli): [Gemini CLI Provider](pkg/providers/gemini/)

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

## Development 

### Development Prerequisites

- macOS development environment
- Go 1.25.1 or later
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

The `--debug-raw` flag enables a debug mode that is useful for developers working on the SpecStory CLI. It outputs the raw data from AI coding agents in a pretty-printed format. This hidden flag works with all operation modes and supports all providers (Claude Code, Cursor CLI, Codex CLI, Gemini CLI).

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
    └── ...
    └── session-data.json # JSON version of the SessionData returned from the provider for this session
```

Each JSON file is pretty-printed with 2-space indentation. For Claude Code, files are numbered sequentially based on their position in the JSONL file. For Cursor CLI, files are numbered based on the SQLite rowid.

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

```bash
specstory --no-usage-analytics [other options]
```

**Note**: Error tracking events include a 500ms delay before the program exits to ensure events are sent successfully. This is necessary because PostHog sends events asynchronously.

### Development Analytics

Analytics are disabled in development builds by default. To enable analytics during local development, build with the PostHog API key:

```zsh
export POSTHOG_API_KEY="your-posthog-api-key"
go build -ldflags "-X github.com/specstoryai/SpecStoryCLI/pkg/analytics.apiKey=$POSTHOG_API_KEY" -o specstory ./
```

## License

The SpecStory CLI is licensed under the [Apache 2.0 open source license](LICENSE.txt).

Copyright 2025-2026 by SpecStory, Inc., All Rights Reserved.

SpecStory® is a registered trademark of SpecStory, Inc.