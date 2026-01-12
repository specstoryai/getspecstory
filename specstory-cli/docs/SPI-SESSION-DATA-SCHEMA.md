# Session Data Schema

The unified session data format used by all terminal coding agent providers (Claude Code, Cursor CLI, Codex CLI, Gemini CLI, etc.).

## Purpose

This schema serves as the common data format that providers produce when converting their native session formats. It enables:

1. **Unified Markdown Rendering**: A single renderer converts any provider's SessionData to readable markdown transcripts
2. **Provider-Neutral Storage**: Sessions from any agent can be stored and processed uniformly
3. **Streaming Support**: Append-only timeline of exchanges/messages as sessions evolve

## Design Principles

- **Provider-neutral**: No provider-specific fields required for core usage
- **Streaming-friendly**: Append-only timeline of exchanges as sessions evolve
- **Markdown-ready**: Structure supports rendering readable transcripts with tool sections
- **Stable IDs**: Use stable message IDs when available; otherwise rely on array order

## Go Types Reference

The schema is defined in `pkg/spi/schema/types.go`. All providers must produce data conforming to these types.

### SessionData (Top-Level)

```go
type SessionData struct {
    SchemaVersion string       `json:"schemaVersion"` // Must be "1.0"
    Provider      ProviderInfo `json:"provider"`
    SessionID     string       `json:"sessionId"`
    CreatedAt     string       `json:"createdAt"`           // ISO 8601 timestamp (required)
    UpdatedAt     string       `json:"updatedAt,omitempty"` // ISO 8601 timestamp (optional)
    Slug          string       `json:"slug,omitempty"`      // Human-readable session name
    WorkspaceRoot string       `json:"workspaceRoot"`       // Project root for relative paths
    Exchanges     []Exchange   `json:"exchanges"`
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `schemaVersion` | Yes | Must be `"1.0"` |
| `provider` | Yes | Provider identification |
| `sessionId` | Yes | Unique session identifier (often UUID) |
| `createdAt` | Yes | ISO 8601 timestamp when session started |
| `updatedAt` | No | ISO 8601 timestamp of last update |
| `slug` | No | Human-readable, filename-safe name (e.g., `"fix-login-bug"`) |
| `workspaceRoot` | Yes | Absolute path to the project directory |
| `exchanges` | Yes | Array of conversational turns |

### ProviderInfo

```go
type ProviderInfo struct {
    ID      string `json:"id"`      // Provider identifier
    Name    string `json:"name"`    // Display name
    Version string `json:"version"` // Provider version
}
```

| Provider ID | Display Name |
|-------------|--------------|
| `claude` | Claude Code |
| `cursor` | Cursor CLI |
| `codex` | Codex CLI |
| `gemini` | Gemini CLI |

### Exchange

An exchange represents a conversational turn, typically starting with a user message followed by agent responses.

```go
type Exchange struct {
    ExchangeID string                 `json:"exchangeId"`           // Required unique ID
    StartTime  string                 `json:"startTime,omitempty"`  // ISO 8601 timestamp
    EndTime    string                 `json:"endTime,omitempty"`    // ISO 8601 timestamp
    Messages   []Message              `json:"messages"`
    Metadata   map[string]interface{} `json:"metadata,omitempty"`
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `exchangeId` | Yes | Unique identifier for this exchange |
| `startTime` | No | When the exchange began |
| `endTime` | No | When the exchange completed |
| `messages` | Yes | Ordered array of messages |
| `metadata` | No | Provider-specific metadata |

### Message

Messages can be from users or the agent.

```go
type Message struct {
    ID        string                 `json:"id,omitempty"`
    Timestamp string                 `json:"timestamp,omitempty"` // ISO 8601 timestamp
    Role      string                 `json:"role"`                // "user" or "agent"
    Model     string                 `json:"model,omitempty"`     // Model used (agent only)
    Content   []ContentPart          `json:"content,omitempty"`
    Tool      *ToolInfo              `json:"tool,omitempty"`
    PathHints []string               `json:"pathHints,omitempty"` // File paths referenced
    Metadata  map[string]interface{} `json:"metadata,omitempty"`
}
```

#### Message Constraints

**User messages** (`role: "user"`):
- Must have non-empty `content`
- Cannot have `tool` field
- Cannot have `model` field

**Agent messages** (`role: "agent"`):
- Must have at least one of: `content`, `tool`, or `pathHints`
- May have `model` field indicating which model responded

### ContentPart

```go
type ContentPart struct {
    Type string `json:"type"` // "text" or "thinking"
    Text string `json:"text"` // The actual content
}
```

| Type | Description |
|------|-------------|
| `text` | Regular text content |
| `thinking` | Model's reasoning/thinking (often displayed differently) |

### ToolInfo

```go
type ToolInfo struct {
    Name              string                 `json:"name"`                          // Tool name (required)
    Type              string                 `json:"type"`                          // Tool type classification
    UseID             string                 `json:"useId,omitempty"`               // Tool use identifier
    Input             map[string]interface{} `json:"input,omitempty"`               // Tool input parameters
    Output            map[string]interface{} `json:"output,omitempty"`              // Tool output
    Summary           *string                `json:"summary,omitempty"`             // Summary for display
    FormattedMarkdown *string                `json:"formattedMarkdown,omitempty"`   // Pre-formatted markdown
}
```

#### Tool Types

| Type | Description | Examples |
|------|-------------|----------|
| `write` | File system writes | `Write`, `Edit`, `CreateFile` |
| `read` | File system reads | `Read`, `Cat`, `ViewFile` |
| `search` | Search operations | `Grep`, `Glob`, `Find`, `WebSearch` |
| `shell` | Shell commands | `Bash`, `Execute`, `RunCommand` |
| `task` | Task management | `TodoWrite`, `TaskManager` |
| `generic` | Known but uncategorized tools | Provider-specific tools |
| `unknown` | Unrecognized tools | Any unrecognized tool name |

#### Tool Fields

| Field | Description |
|-------|-------------|
| `name` | The tool name as used by the agent (e.g., `"Write"`, `"Bash"`) |
| `type` | Classification for rendering (see table above) |
| `useId` | Unique identifier for this specific tool invocation |
| `input` | The parameters passed to the tool |
| `output` | The result returned by the tool |
| `summary` | Brief description for display (wrapped in `<summary>` tags by CLI) |
| `formattedMarkdown` | Pre-rendered markdown from provider (wrapped in `<formatted-markdown>` tags) |

## JSON Schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://specstory.ai/schemas/session-data-v1.json",
  "title": "Session Data",
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "provider", "sessionId", "createdAt", "workspaceRoot", "exchanges"],
  "properties": {
    "schemaVersion": { "type": "string", "const": "1.0" },
    "provider": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "name", "version"],
      "properties": {
        "id": { "type": "string", "enum": ["claude", "cursor", "codex", "gemini"] },
        "name": { "type": "string" },
        "version": { "type": "string" }
      }
    },
    "sessionId": { "type": "string" },
    "createdAt": { "type": "string", "format": "date-time" },
    "updatedAt": { "type": "string", "format": "date-time" },
    "slug": { "type": "string" },
    "workspaceRoot": { "type": "string" },
    "exchanges": {
      "type": "array",
      "items": { "$ref": "#/$defs/exchange" }
    }
  },
  "$defs": {
    "exchange": {
      "type": "object",
      "additionalProperties": false,
      "required": ["exchangeId", "messages"],
      "properties": {
        "exchangeId": { "type": "string" },
        "startTime": { "type": "string", "format": "date-time" },
        "endTime": { "type": "string", "format": "date-time" },
        "messages": {
          "type": "array",
          "items": { "$ref": "#/$defs/message" }
        },
        "metadata": { "type": "object", "additionalProperties": true }
      }
    },
    "message": {
      "type": "object",
      "additionalProperties": true,
      "required": ["role"],
      "properties": {
        "id": { "type": "string" },
        "timestamp": { "type": "string", "format": "date-time" },
        "role": { "type": "string", "enum": ["user", "agent"] },
        "model": { "type": "string" },
        "content": {
          "type": "array",
          "items": { "$ref": "#/$defs/contentPart" }
        },
        "tool": { "$ref": "#/$defs/toolInfo" },
        "pathHints": {
          "type": "array",
          "items": { "type": "string" }
        },
        "metadata": { "type": "object", "additionalProperties": true }
      }
    },
    "contentPart": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type", "text"],
      "properties": {
        "type": { "type": "string", "enum": ["text", "thinking"] },
        "text": { "type": "string" }
      }
    },
    "toolInfo": {
      "type": "object",
      "additionalProperties": true,
      "required": ["name", "type"],
      "properties": {
        "name": { "type": "string" },
        "type": { "type": "string", "enum": ["write", "read", "search", "shell", "task", "generic", "unknown"] },
        "useId": { "type": "string" },
        "input": { "type": "object", "additionalProperties": true },
        "output": { "type": "object", "additionalProperties": true },
        "summary": { "type": "string" },
        "formattedMarkdown": { "type": "string" }
      }
    }
  }
}
```

## Validation

The `SessionData.Validate()` method checks all constraints and logs warnings via `slog`. It returns `true` if valid, `false` otherwise.

Validation rules:
- `schemaVersion` must be `"1.0"`
- `provider.id`, `provider.name`, `provider.version` must be non-empty
- `sessionId`, `createdAt`, `workspaceRoot` must be non-empty
- Each exchange must have non-empty `exchangeId`
- Each message must have valid `role` (`"user"` or `"agent"`)
- User messages must have non-empty `content` and cannot have `tool` or `model`
- Agent messages must have at least one of: `content`, `tool`, or `pathHints`
- `contentPart.type` must be `"text"` or `"thinking"`
- `tool.name` must be non-empty
- `tool.type` must be one of the valid types

## Examples

### Minimal Session

```json
{
  "schemaVersion": "1.0",
  "provider": { "id": "claude", "name": "Claude Code", "version": "1.0.0" },
  "sessionId": "30cc3569-a9d4-429e-981a-ab73e3ddee5f",
  "createdAt": "2025-10-24T10:00:00Z",
  "workspaceRoot": "/Users/alice/project",
  "exchanges": []
}
```

### User Message

```json
{
  "id": "u1",
  "timestamp": "2025-10-24T10:00:00Z",
  "role": "user",
  "content": [
    { "type": "text", "text": "Please create src/main.go" }
  ]
}
```

### Agent Text Response

```json
{
  "id": "a1",
  "timestamp": "2025-10-24T10:00:10Z",
  "role": "agent",
  "model": "claude-3-5-sonnet-20241022",
  "content": [
    { "type": "text", "text": "I'll create the file with a basic Go program." }
  ]
}
```

### Agent Thinking

```json
{
  "id": "a2",
  "timestamp": "2025-10-24T10:00:08Z",
  "role": "agent",
  "model": "claude-3-5-sonnet-20241022",
  "content": [
    { "type": "thinking", "text": "The user wants me to create a Go file. I should use the Write tool..." }
  ]
}
```

### Agent Tool Use

```json
{
  "id": "t1",
  "timestamp": "2025-10-24T10:00:12Z",
  "role": "agent",
  "model": "claude-3-5-sonnet-20241022",
  "tool": {
    "name": "Write",
    "type": "write",
    "useId": "tool_abc123",
    "input": { "file_path": "src/main.go", "content": "package main\n\nfunc main() {\n}" }
  },
  "pathHints": ["src/main.go"]
}
```

### Complete Exchange

```json
{
  "exchangeId": "ex_001",
  "startTime": "2025-10-24T10:00:00Z",
  "endTime": "2025-10-24T10:00:15Z",
  "messages": [
    {
      "id": "u1",
      "timestamp": "2025-10-24T10:00:00Z",
      "role": "user",
      "content": [{ "type": "text", "text": "Create src/main.go with Hello World" }]
    },
    {
      "id": "a1",
      "timestamp": "2025-10-24T10:00:10Z",
      "role": "agent",
      "model": "claude-3-5-sonnet-20241022",
      "content": [{ "type": "text", "text": "I'll create a simple Hello World program in Go." }]
    },
    {
      "id": "t1",
      "timestamp": "2025-10-24T10:00:15Z",
      "role": "agent",
      "model": "claude-3-5-sonnet-20241022",
      "tool": {
        "name": "Write",
        "type": "write",
        "useId": "tool_001",
        "input": { "file_path": "src/main.go" }
      },
      "pathHints": ["src/main.go"]
    }
  ]
}
```

### Full Session Example

```json
{
  "schemaVersion": "1.0",
  "provider": { "id": "claude", "name": "Claude Code", "version": "1.0.0" },
  "sessionId": "sess-1234",
  "createdAt": "2025-11-13T10:00:00Z",
  "updatedAt": "2025-11-13T10:05:00Z",
  "slug": "create-go-hello-world",
  "workspaceRoot": "/home/alice/project",
  "exchanges": [
    {
      "exchangeId": "ex_001",
      "startTime": "2025-11-13T10:00:00Z",
      "endTime": "2025-11-13T10:00:15Z",
      "messages": [
        {
          "id": "u1",
          "timestamp": "2025-11-13T10:00:00Z",
          "role": "user",
          "content": [{ "type": "text", "text": "Create src/main.go with Hello World" }]
        },
        {
          "id": "a1",
          "timestamp": "2025-11-13T10:00:10Z",
          "role": "agent",
          "model": "claude-3-5-sonnet-20241022",
          "content": [{ "type": "text", "text": "Creating the file now." }],
          "tool": {
            "name": "Write",
            "type": "write",
            "useId": "tool_001",
            "input": { "file_path": "src/main.go" }
          },
          "pathHints": ["src/main.go"]
        }
      ]
    },
    {
      "exchangeId": "ex_002",
      "startTime": "2025-11-13T10:01:00Z",
      "endTime": "2025-11-13T10:01:20Z",
      "messages": [
        {
          "id": "u2",
          "timestamp": "2025-11-13T10:01:00Z",
          "role": "user",
          "content": [{ "type": "text", "text": "Run the program" }]
        },
        {
          "id": "t2",
          "timestamp": "2025-11-13T10:01:10Z",
          "role": "agent",
          "model": "claude-3-5-sonnet-20241022",
          "tool": {
            "name": "Bash",
            "type": "shell",
            "useId": "tool_002",
            "input": { "command": "go run src/main.go" },
            "output": { "stdout": "Hello World\n", "exit_code": 0 }
          }
        },
        {
          "id": "a2",
          "timestamp": "2025-11-13T10:01:20Z",
          "role": "agent",
          "model": "claude-3-5-sonnet-20241022",
          "content": [{ "type": "text", "text": "The program ran successfully and printed 'Hello World'." }]
        }
      ]
    }
  ]
}
```

## Provider Implementation Notes

### Converting Native Formats

Each provider must convert its native session format to this schema:

- **Claude Code**: Convert JSONL entries to exchanges, map tool names to types
- **Cursor CLI**: Parse SQLite blob records, reconstruct message flow
- **Codex CLI**: Similar to Claude Code JSONL format
- **Gemini CLI**: Parse JSON session files

### PathHints

- Use `pathHints` to indicate files referenced by the message
- Paths can be absolute or relative to `workspaceRoot`
- Do not expand glob patterns into `pathHints` - keep patterns in `tool.input`

### Timestamps

- All timestamps must be ISO 8601 format
- Message timestamps are optional; renderers can fall back to exchange times
- Use `createdAt` for session start, `updatedAt` for last modification

### Exchange IDs

- Must be unique within the session
- Suggested format: `ex_<index>` or `ex_<hash>`
- Can include context like `ex_0_9f86d081` (index + short hash)

## Versioning

- Current version: `"1.0"`
- Backward-compatible additions allowed (new optional properties)
- Breaking changes require bumping `schemaVersion`

## Markdown Rendering

A single renderer consumes `exchanges[].messages[]`:

1. Process messages in order (user → agent → agent ...)
2. Render `content` parts as text (detect and format code blocks)
3. Show tool uses with type/name and list any `pathHints`
4. Use `thinking` content type for collapsible/styled reasoning sections
5. If `formattedMarkdown` is present, use it; otherwise render from `input`/`output`
