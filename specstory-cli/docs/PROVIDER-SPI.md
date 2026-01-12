# SpecStory CLI's Agent SPI

The SpecStory CLI's Agent SPI (Service Provider Interface) allows developers to extend the SpecStory CLI by implementing support for new AI coding agents.

## Overview

The SPI defines a standard interface that all agent providers must implement. This enables SpecStory CLI to work uniformly with multiple AI coding agents while each provider handles the specifics of its native data formats.

### Currently Supported Providers

| Provider ID | Display Name | Session Storage |
|-------------|--------------|-----------------|
| `claude` | Claude Code | `~/.claude/projects/<hash>/<session-id>.jsonl` |
| `cursor` | Cursor CLI | `~/.cursor/chats/<project-hash>/<session-id>/store.db` |
| `codex` | Codex CLI | `~/.codex/sessions/YYYY/MM/DD/<session-id>.jsonl` |
| `gemini` | Gemini CLI | `~/.gemini/tmp/<hash>/chats/<session-id>.json` |

## Quick Start: Implementing a New Provider

To add support for a new AI coding agent:

1. Create a new package under `pkg/providers/<yourprovider>/`
2. Implement the `spi.Provider` interface (see [Provider Interface](#provider-interface))
3. Register your provider in `pkg/spi/factory/registry.go`

## Architecture

### Package Structure

```
pkg/
├── spi/
│   ├── provider.go        # Provider interface and CheckResult type
│   ├── cmdline.go         # Command line parsing utilities
│   ├── path_utils.go      # Path utilities (canonical paths, slugs, debug output)
│   ├── factory/
│   │   └── registry.go    # Provider registry (imports all providers)
│   └── schema/
│       └── types.go       # Unified SessionData schema
└── providers/
    ├── claudecode/        # Claude Code provider implementation
    ├── cursorcli/         # Cursor CLI provider implementation
    ├── codexcli/          # Codex CLI provider implementation
    └── gemini/            # Gemini CLI provider implementation
```

### Dependency Flow

```
main.go
  → spi/factory (registry)
      → spi (interfaces)
      → providers/claudecode
      → providers/cursorcli
      → providers/codexcli
      → providers/gemini

providers/* → spi (interfaces and schema)
```

Key design principles:
- No circular dependencies (unidirectional flow)
- `spi` package defines interfaces and shared types only
- Providers import `spi` to implement interfaces
- Factory package handles all provider registration
- Schema types are shared by all providers via `spi/schema`

## Provider Interface

All providers must implement the `spi.Provider` interface defined in `pkg/spi/provider.go`:

```go
type Provider interface {
    // Name returns the human-readable name of the provider
    // Examples: "Claude Code", "Cursor CLI", "Codex CLI"
    Name() string

    // Check verifies the provider is properly installed
    // customCommand: empty string uses detected/default binary, non-empty uses specific path
    Check(customCommand string) CheckResult

    // DetectAgent checks if this provider has been used in the given project
    // helpOutput: if true AND no activity found, output helpful guidance to stdout
    // Returns true if agent has created sessions in the specified path
    DetectAgent(projectPath string, helpOutput bool) bool

    // GetAgentChatSession retrieves a single session by ID
    // sessionID is always provided (never empty)
    // debugRaw: if true, write provider-specific raw files to .specstory/debug/<sessionID>/
    // Returns nil if session not found, error for actual errors
    GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*AgentChatSession, error)

    // GetAgentChatSessions retrieves all sessions for the project
    // debugRaw: if true, write provider-specific raw files to .specstory/debug/<sessionID>/
    GetAgentChatSessions(projectPath string, debugRaw bool) ([]AgentChatSession, error)

    // ExecAgentAndWatch launches the agent and watches for session updates
    // Blocks until the agent exits
    // customCommand: empty uses default binary with default args
    // resumeSessionID: empty starts new session, non-empty resumes specific session
    // debugRaw: if true, write debug files
    // sessionCallback: called with each session update (do not block)
    ExecAgentAndWatch(
        projectPath string,
        customCommand string,
        resumeSessionID string,
        debugRaw bool,
        sessionCallback func(*AgentChatSession),
    ) error

    // WatchAgent watches for agent activity WITHOUT launching the agent
    // Runs until error or context cancellation
    // ctx: Context for cancellation and timeout
    // debugRaw: if true, write debug files
    // sessionCallback: called with each session update (do not block)
    WatchAgent(
        ctx context.Context,
        projectPath string,
        debugRaw bool,
        sessionCallback func(*AgentChatSession),
    ) error
}
```

### CheckResult

Returned by the `Check()` method:

```go
type CheckResult struct {
    Success      bool   // Whether the check succeeded
    Version      string // Version string (empty on failure)
    Location     string // Path to the executable
    ErrorMessage string // Error message (empty on success)
}
```

### AgentChatSession

Returned by session retrieval methods:

```go
type AgentChatSession struct {
    SessionID   string              // Unique identifier (often a UUID)
    CreatedAt   string              // ISO 8601 timestamp when session was created
    Slug        string              // Human-readable, filename-safe slug
    SessionData *schema.SessionData // Unified session data (see [schema docs](SPI-SESSION-DATA-SCHEMA.md))
    RawData     string              // Provider-specific raw data (for debugging)
}
```

## Session Data Schema

All providers must convert their native session format into the unified `schema.SessionData` structure. See [SPI-SESSION-DATA-SCHEMA.md](SPI-SESSION-DATA-SCHEMA.md) for the complete schema reference.

Key points:
- Schema version is always `"1.0"`
- Provider ID must match your registered ID (e.g., `"claude"`, `"cursor"`)
- All timestamps must be ISO 8601 format
- User messages have `role: "user"` with required `content`
- Agent messages have `role: "agent"` with at least one of: `content`, `tool`, or `pathHints`

## Implementing a Provider

### Step 1: Create the Provider Package

Create `pkg/providers/yourprovider/provider.go`:

```go
package yourprovider

import (
    "context"

    "github.com/specstoryai/specstory-cli/pkg/spi"
    "github.com/specstoryai/specstory-cli/pkg/spi/schema"
)

type Provider struct{}

func NewProvider() *Provider {
    return &Provider{}
}

func (p *Provider) Name() string {
    return "Your Agent Name"
}

// Implement remaining interface methods...
```

### Step 2: Register the Provider

Add your provider to `pkg/spi/factory/registry.go`:

```go
import (
    // ... existing imports
    "github.com/specstoryai/specstory-cli/pkg/providers/yourprovider"
)

func (r *Registry) registerAll() {
    // ... existing registrations
    r.register("yourid", yourprovider.NewProvider())
}
```

### Step 3: Implement Core Methods

#### Name() and Check()

The `Name()` method returns the display name for UI/logging. The `Check()` method verifies installation:

```go
func (p *Provider) Check(customCommand string) spi.CheckResult {
    cmd := customCommand
    if cmd == "" {
        cmd = "your-agent-cli" // default command
    }

    // Try to get version
    out, err := exec.Command(cmd, "--version").Output()
    if err != nil {
        return spi.CheckResult{
            Success:      false,
            ErrorMessage: "Your Agent CLI not found. Install from...",
        }
    }

    return spi.CheckResult{
        Success:  true,
        Version:  strings.TrimSpace(string(out)),
        Location: cmd,
    }
}
```

#### DetectAgent()

Check if your agent has been used in the project:

```go
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
    sessionDir := getSessionDir(projectPath) // your logic

    entries, err := os.ReadDir(sessionDir)
    if err != nil || len(entries) == 0 {
        if helpOutput {
            fmt.Println("No Your Agent sessions found.")
            fmt.Println("Run 'your-agent-cli' to create a session.")
        }
        return false
    }

    return true
}
```

#### GetAgentChatSession() and GetAgentChatSessions()

Convert your native format to `schema.SessionData`:

```go
func (p *Provider) GetAgentChatSession(projectPath, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
    // 1. Load your native session data
    rawData, err := loadNativeSession(projectPath, sessionID)
    if err != nil {
        return nil, err
    }
    if rawData == nil {
        return nil, nil // session not found
    }

    // 2. Convert to unified schema
    sessionData := convertToSchema(rawData)

    // 3. Validate the converted data
    if err := sessionData.Validate(); err != nil {
        return nil, fmt.Errorf("validation failed: %w", err)
    }

    // 4. Write debug files if requested
    if debugRaw {
        debugDir := spi.GetDebugDir(projectPath, sessionID)
        os.MkdirAll(debugDir, 0755)
        // Write your provider-specific raw files
        os.WriteFile(filepath.Join(debugDir, "raw-session.json"), rawData, 0644)
    }

    return &spi.AgentChatSession{
        SessionID:   sessionID,
        CreatedAt:   sessionData.CreatedAt,
        Slug:        sessionData.Slug,
        SessionData: sessionData,
        RawData:     string(rawData),
    }, nil
}
```

#### ExecAgentAndWatch()

Launch the agent and watch for updates:

```go
func (p *Provider) ExecAgentAndWatch(
    projectPath, customCommand, resumeSessionID string,
    debugRaw bool,
    sessionCallback func(*spi.AgentChatSession),
) error {
    // Build command
    cmd := customCommand
    if cmd == "" {
        cmd = "your-agent-cli"
    }
    args := []string{}
    if resumeSessionID != "" {
        args = append(args, "--resume", resumeSessionID)
    }

    // Set up file watcher for session directory
    watcher, _ := fsnotify.NewWatcher()
    defer watcher.Close()
    watcher.Add(getSessionDir(projectPath))

    // Start the agent process
    process := exec.Command(cmd, args...)
    process.Dir = projectPath
    process.Stdin = os.Stdin
    process.Stdout = os.Stdout
    process.Stderr = os.Stderr

    if err := process.Start(); err != nil {
        return err
    }

    // Watch for file changes in goroutine
    go func() {
        for event := range watcher.Events {
            if event.Op&fsnotify.Write != 0 {
                session, _ := p.GetAgentChatSession(projectPath, extractSessionID(event.Name), debugRaw)
                if session != nil {
                    sessionCallback(session)
                }
            }
        }
    }()

    // Wait for process to exit
    return process.Wait()
}
```

#### WatchAgent()

Watch for activity without launching (used for watch-only mode):

```go
func (p *Provider) WatchAgent(
    ctx context.Context,
    projectPath string,
    debugRaw bool,
    sessionCallback func(*spi.AgentChatSession),
) error {
    watcher, _ := fsnotify.NewWatcher()
    defer watcher.Close()
    watcher.Add(getSessionDir(projectPath))

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case event := <-watcher.Events:
            if event.Op&fsnotify.Write != 0 {
                session, _ := p.GetAgentChatSession(projectPath, extractSessionID(event.Name), debugRaw)
                if session != nil {
                    sessionCallback(session)
                }
            }
        case err := <-watcher.Errors:
            return err
        }
    }
}
```

## Utility Functions

The `spi` package provides helper functions:

### Path Utilities (`pkg/spi/path_utils.go`)

```go
// GetCanonicalPath resolves symlinks and corrects filesystem casing
func GetCanonicalPath(path string) (string, error)

// GenerateFilenameFromUserMessage creates a filename-safe slug from text
// Truncates to maxLen characters, handles unicode, removes special chars
func GenerateFilenameFromUserMessage(text string, maxLen int) string

// GetDebugDir returns the debug output directory for a session
// Returns: <projectPath>/.specstory/debug/<sessionID>/
func GetDebugDir(projectPath, sessionID string) string

// WriteDebugSessionData writes the unified session-data.json file
func WriteDebugSessionData(projectPath, sessionID string, sessionData *schema.SessionData) error
```

### Command Line Parsing (`pkg/spi/cmdline.go`)

```go
// SplitCommandLine parses a command string into arguments
// Handles quoted strings and escape sequences
func SplitCommandLine(cmdline string) ([]string, error)
```

## Schema Validation

The `schema.SessionData` type includes a `Validate()` method that checks:

- SchemaVersion is `"1.0"`
- Provider fields (ID, Name, Version) are non-empty
- Required fields (SessionID, CreatedAt, WorkspaceRoot) are present
- Each Exchange has a non-empty ExchangeID
- Message roles are valid (`"user"` or `"agent"`)
- User messages have content and no tool/model fields
- Agent messages have at least one of: content, tool, or pathHints
- ContentPart types are valid (`"text"` or `"thinking"`)
- ToolInfo types are valid (see [schema docs](SPI-SESSION-DATA-SCHEMA.md#tool-types))

Always validate converted session data before returning it.

## Error Handling

- Providers format their own error messages with helpful guidance
- Return `nil, nil` from `GetAgentChatSession()` when session not found
- Return actual errors for filesystem/parsing failures
- Use `slog` for logging (not `fmt.Print`)

## Analytics

Analytics tracking happens in command handlers and the registry, not in providers. Providers don't need to know about analytics.

## Testing

Test your provider implementation:

```go
func TestProviderInterface(t *testing.T) {
    p := NewProvider()

    // Test Name
    if p.Name() == "" {
        t.Error("Name() should return non-empty string")
    }

    // Test Check with invalid command
    result := p.Check("nonexistent-command")
    if result.Success {
        t.Error("Check should fail for nonexistent command")
    }

    // Test DetectAgent on empty directory
    tmpDir := t.TempDir()
    if p.DetectAgent(tmpDir, false) {
        t.Error("DetectAgent should return false for empty directory")
    }
}
```

## Example: Minimal Provider

Here's a minimal but complete provider implementation:

```go
package minimal

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "github.com/specstoryai/specstory-cli/pkg/spi"
    "github.com/specstoryai/specstory-cli/pkg/spi/schema"
)

type Provider struct{}

func NewProvider() *Provider { return &Provider{} }

func (p *Provider) Name() string { return "Minimal Agent" }

func (p *Provider) Check(customCommand string) spi.CheckResult {
    cmd := customCommand
    if cmd == "" {
        cmd = "minimal-agent"
    }
    out, err := exec.Command(cmd, "--version").Output()
    if err != nil {
        return spi.CheckResult{Success: false, ErrorMessage: "Not installed"}
    }
    return spi.CheckResult{
        Success:  true,
        Version:  strings.TrimSpace(string(out)),
        Location: cmd,
    }
}

func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
    dir := filepath.Join(projectPath, ".minimal-agent")
    if _, err := os.Stat(dir); os.IsNotExist(err) {
        if helpOutput {
            fmt.Println("No Minimal Agent sessions found.")
        }
        return false
    }
    return true
}

func (p *Provider) GetAgentChatSession(projectPath, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
    // Implementation depends on your agent's data format
    return nil, nil
}

func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
    return []spi.AgentChatSession{}, nil
}

func (p *Provider) ExecAgentAndWatch(projectPath, customCommand, resumeSessionID string, debugRaw bool, cb func(*spi.AgentChatSession)) error {
    return fmt.Errorf("not implemented")
}

func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, cb func(*spi.AgentChatSession)) error {
    return fmt.Errorf("not implemented")
}
```
