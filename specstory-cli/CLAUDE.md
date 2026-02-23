# CLAUDE.md

This file provides guidance to coding agents when working with code in this repository.

## Project Overview

SpecStory CLI is a wrapper for coding agents that tracks conversations and generates markdown files. The project uses the Go language and follows standard Go project conventions.

## Key Commands

### Building

The command is always built as `specstory`, located in the root of the repository.

```zsh
# Build for current platform
go build -o specstory
```

### Testing

```zsh
# Run all tests
go test ./...

# Run tests with verbose output
go test -v ./...

# Run tests for specific package
go test -v ./pkg/cli
go test -v ./pkg/utils

# Run specific test
go test -v -run TestGenerateFilename ./pkg/utils
```

### Linting

Don't try linting individual files. This often fails with noise in Go Lang, just lint the whole project, it's fast.

```zsh
# Run linter
golangci-lint run

# Format code
gofmt -w .
```

### Running the CLI

There are two main modes of operation:

```zsh
# Interactive interactive auto-save mode
./specstory run

# Sync mode - process all sessions
./specstory sync

# Sync mode - process specific session
./specstory sync -u <uuid>
```

### Debugging

To see debug output, you can use the following commands:

```zsh
# Debug output to stdout
./specstory sync --debug

# Debug log output in ./.specstory/debug/debug.log
./specstory sync --log

# Hidden debug flag (not in public docs)
./specstory sync --debug-raw          # Debug mode to output pretty-printed raw data files
```

## Technical Details

### Code Structure

The codebase package structure:

- `main.go` - CLI entry point
- `pkg/analytics/` - PostHog analytics integration
- `pkg/cloud` - Cloud sync integration
- `pkg/cmd` - CLI commands and command parsing
- `pkg/config` - Optional TOML config file handling
- `pkg/log/` - Logging utilities
- `pkg/provenance/` - Optional AI provenance tracking
- `pkg/providers/*` - Provider implementations including file watching, data processing, and session data generation
- `pkg/session/` - Session data validation, helpers and markdown generation
- `pkg/spi/` - SPI implementation for provider implementations
- `pkg/utils/` - Helper functions

### JSONL File Behavior

- Session data files grow during conversation (append-only)
- `run` and `watch` commands uses fsnotify for real-time monitoring of the agent's session data directory
- Many agents (e.g. Claude Code) use JSONL files for session data (e.g. `~/.claude/projects/<dir-derived-from-project-dir>/<session-id>.jsonl`)

### Analytics Events

When adding new features, track usage with PostHog:

```go
analytics.TrackEvent(analytics.EventExtensionActivated, analytics.Properties{
	"property": "value",
})
```

## Code Conventions

- Write only idomatic Go code.
- Prioritize simplicity and readability over terse or clever code
- Emphasize DRY code, look for existing code that can be reused, don't just write new code first.
- Use Go lang libraries, not external dependencies where possible, if a dependency is needed explain why
- Comment everything that's not obvious, if in doubt, comment it.
- Use "Why" comments, not "what" or "how" unless specifically requested
- Use single function exit point where possible (immediate guard clauses are OK)
- Provide consistent observability and tracing with log/slog for logging, not fmt.Println or fmt.Printf
- Follow existing patterns in the codebase
- The application doesn't support Windows, only Linux and macOS, don't include Windows support in the codebase.

## Testing Strategy & Conventions

- Tests use Go's standard `testing` package
- Write unit tests for things with complicated logic
- Don't write unit tests for simple, tautological things
- Test files follow Go convention: *_test.go alongside source files in the same package
- Table-driven tests: tests are structured with test cases defined in slices of structs
  - Each struct contains: name, input parameters, and expected results
  - Uses t.Run(tt.name, func(t *testing.T) {...}) for subtests
- Use clear test function naming: TestFunctionName or TestFunctionName_Scenario
- Make manual assertions using t.Errorf()
- Unit Tests: Most tests focus on individual functions
- Edge Case Testing: Comprehensive coverage of error conditions, empty inputs, invalid data
- Integration-style Tests: Some tests like TestSessionProcessingFlow test multiple components together
- Tests both success and failure paths
- Validates error messages contain expected strings
- Test permission errors, missing files, invalid inputs

## Writing Conventions

- Never put text immediately after the header in markdown, put in a newline first.
- Use `zsh` code blocks in markdown (not `bash`)
- Keep the repository `README.md` up to date with the latest changes.
- When planning, never write time/calendar estimates into documents.

## Development Workflow

When searching for code, ALWAYS exclude the `.specstory` directory.

Don't just make your own decision, explain the options, the pros and cons, and what you recommend. Have the user make the decision.

Don't ever respond with just code changes. Always include explanations of what you're doing, why you're doing it, and how.

Always ask before introducing any new dependencies.

Always ask before introducing any new code files.

Run the linter after every code change `golangci-lint run`. Fix formatting errors yourself with `gofmt -w .`.

Run tests after every major code change `go test -v ./...`.

Don't just run commands, explain what you're doing and why.