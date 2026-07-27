# Implementation Plan: Add VS Code Copilot IDE Provider (copilotide)

## Overview

Add a new provider called `copilotide` that reads VS Code Copilot's JSON session files directly (same as the extension does) to export conversations to the `.specstory` folder. This is phase 1 - focusing on JSON file reading, conversation extraction, workspace filtering, and debug output. Tool export formatting differences will be addressed in later phases.

## Quick Summary

**Goal:** Port extension's VS Code Copilot file reading functionality to CLI as a new provider

**Key Features:**
- Read from workspace-specific chat sessions (`~/.vscode/User/workspaceStorage/[workspace-id]/chatSessions/`)
- Filter by workspace (matches Claude Code's project-scoped behavior)
- Export conversations to markdown in `.specstory/history/`
- Debug mode for raw data inspection
- Load optional editing state from `chatEditingSessions/`

**Technology:**
- JSON file reading (no SQLite needed - VS Code uses JSON files)
- Workspace filtering via workspace metadata matching
- Standard SPI provider interface
- SQLite only for workspace matching (reading `state.vscdb` for workspace identification)

**Complexity:** Medium (simpler than Cursor - JSON files vs global SQLite database)

## Background

The existing extension (in `ts-extension/`) reads from VS Code's workspace storage at `~/.vscode/User/workspaceStorage/[workspace-id]/chatSessions/`. Each chat session is stored as a separate JSON file. We're porting this functionality to the CLI as a new provider.

**Key Architectural Difference from Cursor:**
- **Cursor**: Single global SQLite database + workspace filtering
- **VS Code**: Individual JSON files per session in workspace-specific directories

## Type Strategy (Important)

**Direct conversion, no intermediate types:**
1. **Raw types:** Define Go structs matching VS Code's actual JSON structure (`VSCodeComposer`, `VSCodeRequestBlock`, etc.)
2. **CLI types:** Convert directly to `schema.SessionData` (the CLI's unified format)
3. **No intermediate types:** Don't port the TS extension's unified conversation types - that's unnecessary complexity

**Why this is simpler than Cursor:**
- JSON files are self-contained (no SQL queries needed)
- Workspace filtering is inherent (files live in workspace directories)
- No database locking concerns (just file reads)
- Start with minimal type definitions, expand as needed based on debug output

**Development approach:**
1. Implement basic version with minimal types
2. Get debug-raw mode working
3. Export actual JSON and inspect
4. Refine types incrementally based on real data

## Critical Files

### New Files to Create
1. `pkg/providers/copilotide/provider.go` - Main provider implementation
2. `pkg/providers/copilotide/workspace.go` - Workspace matching and path resolution
3. `pkg/providers/copilotide/parser.go` - Parse VS Code native format to internal representation
4. `pkg/providers/copilotide/agent_session.go` - Convert to unified SessionData format
5. `pkg/providers/copilotide/types.go` - Go structs for VS Code data structures
6. `pkg/providers/copilotide/path_utils.go` - VS Code-specific path resolution

### Files to Modify
1. `pkg/spi/factory/registry.go` - Register the new provider
2. `go.mod` - Verify SQLite dependency exists (already added for cursoride)

## Implementation Steps

### Step 1: Verify Dependencies

- `modernc.org/sqlite` already added for `cursoride` provider
- Only needed for workspace matching (reading `state.vscdb`)
- No new dependencies required

### Step 2: Create Provider Structure

Create `pkg/providers/copilotide/provider.go`:
- Define `Provider` struct (empty struct, stateless)
- Implement `NewProvider()` constructor
- Implement `Name()` → return "VS Code Copilot"
- Stub out all 6 required interface methods initially

### Step 3: Path Resolution

Create `pkg/providers/copilotide/path_utils.go`:
- `GetWorkspaceStoragePath()` → Find `~/.vscode/User/workspaceStorage/`
  - macOS: `~/Library/Application Support/Code/User/workspaceStorage/`
  - Linux: `~/.config/Code/User/workspaceStorage/`
- `FindMatchingWorkspace(projectPath)` → Match projectPath to workspace directory
- `GetChatSessionsPath(workspaceDir)` → Return `[workspaceDir]/chatSessions/`
- `GetChatEditingSessionsPath(workspaceDir)` → Return `[workspaceDir]/chatEditingSessions/`
- Handle case where directories don't exist
- Handle multiple matching workspaces (use newest based on `state.vscdb` mtime)

### Step 4: Define Data Structures

Create `pkg/providers/copilotide/types.go` - **Raw VS Code JSON types only** (not unified types):

**Important:** These types match VS Code's actual JSON structure. We'll convert directly from these to `schema.SessionData` (the CLI's unified format). Don't create intermediate unified types - that's unnecessary complexity.

```go
// VSCodeComposer is the root structure stored in chatSessions/[sessionId].json
type VSCodeComposer struct {
    Host              string               `json:"host"` // Always "vscode"
    SessionID         string               `json:"sessionId"`
    Name              string               `json:"name,omitempty"`
    CustomTitle       string               `json:"customTitle,omitempty"`
    Version           int                  `json:"version"`
    RequesterUsername string               `json:"requesterUsername"`
    ResponderUsername string               `json:"responderUsername"`
    CreationDate      int64                `json:"creationDate"`
    LastMessageDate   int64                `json:"lastMessageDate"`
    InitialLocation   string               `json:"initialLocation"`
    IsImported        bool                 `json:"isImported"`
    Requests          []VSCodeRequestBlock `json:"requests"`
}

// VSCodeRequestBlock represents one conversational turn (user message + agent responses)
type VSCodeRequestBlock struct {
    RequestID string            `json:"requestId"`
    Timestamp int64             `json:"timestamp"`
    Message   VSCodeMessage     `json:"message"`
    Response  []json.RawMessage `json:"response"` // Polymorphic array - parse by "kind"
    Result    VSCodeResult      `json:"result"`
    ModelID   string            `json:"modelId,omitempty"`
}

// VSCodeMessage is the user's input message
type VSCodeMessage struct {
    Text  string              `json:"text"`
    Parts []VSCodeMessagePart `json:"parts,omitempty"`
}

// VSCodeMessagePart (optional - only parse if needed for tool references)
type VSCodeMessagePart struct {
    Kind string `json:"kind"`
    // Add other fields as needed during implementation
}

// Response types - each has a "kind" discriminator
// Parse json.RawMessage into these based on kind field

type VSCodeToolInvocationResponse struct {
    Kind        string                 `json:"kind"` // "toolInvocationSerialized"
    ID          string                 `json:"id,omitempty"`
    ToolID      string                 `json:"toolId,omitempty"`
    ToolCallID  string                 `json:"toolCallId,omitempty"`
    Presentation string                `json:"presentation,omitempty"` // "hidden" or empty
    // Add invocationMessage, pastTenseMessage if needed for tool parameters
}

type VSCodeTextEditGroupResponse struct {
    Kind string       `json:"kind"` // "textEditGroup"
    Uri  VSCodeUri    `json:"uri"`
    Edits [][]VSCodeEdit `json:"edits"` // Nested array
    Done bool         `json:"done,omitempty"`
}

type VSCodeCodeblockUriResponse struct {
    Kind   string    `json:"kind"` // "codeblockUri"
    Uri    VSCodeUri `json:"uri"`
    IsEdit bool      `json:"isEdit,omitempty"`
}

type VSCodeConfirmationResponse struct {
    Kind   string `json:"kind"` // "confirmation"
    Title  string `json:"title"`
    Message string `json:"message"`
    IsUsed bool   `json:"isUsed"`
}

type VSCodeInlineReferenceResponse struct {
    Kind            string    `json:"kind"` // "inlineReference"
    InlineReference VSCodeUri `json:"inlineReference"`
}

// Result metadata - contains tool call rounds and results
type VSCodeResult struct {
    Metadata VSCodeResultMetadata `json:"metadata"`
    Timings  VSCodeTimings        `json:"timings"`
}

type VSCodeResultMetadata struct {
    ToolCallRounds  []VSCodeToolCallRound            `json:"toolCallRounds,omitempty"`
    ToolCallResults map[string]VSCodeToolCallResult  `json:"toolCallResults,omitempty"`
    Messages        []VSCodeMetadataMessage          `json:"messages,omitempty"`
}

type VSCodeToolCallRound struct {
    Response  string                `json:"response"` // May contain thinking
    ToolCalls []VSCodeToolCallInfo  `json:"toolCalls"`
}

type VSCodeToolCallInfo struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string - parse if needed
}

type VSCodeToolCallResult struct {
    Content []struct {
        Value string `json:"value,omitempty"`
    } `json:"content,omitempty"`
}

type VSCodeMetadataMessage struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type VSCodeTimings struct {
    FirstProgress int64 `json:"firstProgress"`
    TotalElapsed  int64 `json:"totalElapsed"`
}

// Common types
type VSCodeUri struct {
    Scheme string `json:"scheme,omitempty"` // "file", "vscode-chat-code-block", etc.
    Path   string `json:"path,omitempty"`
    FSPath string `json:"fsPath,omitempty"`
}

type VSCodeEdit struct {
    Text  string     `json:"text"`
    Range VSCodeRange `json:"range"`
}

type VSCodeRange struct {
    StartLineNumber int `json:"startLineNumber"`
    StartColumn     int `json:"startColumn"`
    EndLineNumber   int `json:"endLineNumber"`
    EndColumn       int `json:"endColumn"`
}

// Optional state file types (for chatEditingSessions/[sessionId]/state.json)
// Only define these if needed - can defer to phase 2
type VSCodeStateFile struct {
    Version         int                   `json:"version"`
    SessionID       string                `json:"sessionId"`
    LinearHistory   []VSCodeLinearHistory `json:"linearHistory,omitempty"`
    // Add other fields as needed
}

type VSCodeLinearHistory struct {
    RequestID string         `json:"requestId"`
    Stops     []VSCodeStop   `json:"stops,omitempty"`
}

type VSCodeStop struct {
    // Define as needed - can defer to phase 2
}
```

**Key Points:**
- These match VS Code's actual JSON structure
- Use `json.RawMessage` for polymorphic response array
- Parse "kind" field first, then unmarshal into specific type
- Add fields incrementally as needed - start minimal
- State file types can be deferred to phase 2

**Fields we can safely omit** (from TS extension types):
- `requesterAvatarIconUri`, `responderAvatarIconUri` - UI only
- `agent.*` - Agent metadata (extensionId, publisher, etc.)
- `variableData` - Variable references (not needed for basic export)
- `followups`, `contentReferences`, `codeCitations` - Not needed for markdown export
- `isCanceled` - Session state (not relevant for export)
- Most message part fields - only parse if needed for tool references

**Start with these essentials:**
- Session metadata (id, dates, title)
- Request blocks (user message + responses)
- Tool call metadata (rounds, results)
- Response types: toolInvocationSerialized, codeblockUri, textEditGroup
- Skip everything else until debug output shows we need it

### Step 5: Workspace Matching

Create `pkg/providers/copilotide/workspace.go`:
- `FindWorkspaceForProject(projectPath string)` → Find matching workspace directory
- `GetWorkspaceStoragePath()` → Get macOS/Linux workspace storage location
  - macOS: `~/Library/Application Support/Code/User/workspaceStorage/`
  - Linux: `~/.config/Code/User/workspaceStorage/`
- `MatchWorkspaceURI(projectPath, workspaceURI string)` → Compare paths accounting for symlinks
- `LoadWorkspaceMetadata(workspaceDir)` → Read `workspace.json` to get workspace URI
- `LoadSessionIndex(workspaceDir)` → Read `chat.ChatSessionStore.index` from workspace `state.vscdb`
- `GetNewestWorkspace(matches []string)` → Select workspace with newest `state.vscdb` mtime
- Handle errors gracefully:
  - Workspace storage doesn't exist
  - No matching workspace found
  - Multiple matches (use newest)
  - No chat sessions in workspace

**Implementation Notes:**
```go
// Workspace matching process:
// 1. Scan all directories in ~/.vscode/User/workspaceStorage/
// 2. For each directory, read workspace.json
// 3. Extract "workspace" or "folder" field
// 4. Compare with projectPath (canonical paths)
// 5. If multiple matches, use newest based on state.vscdb mtime

func FindWorkspaceForProject(projectPath string) (string, error) {
    storageBase := GetWorkspaceStoragePath()
    dirs, err := os.ReadDir(storageBase)
    // ... iterate and match workspace.json
}

// Example workspace.json structure:
// {"folder": "file:///Users/bago/code/project"}
// or
// {"workspace": "file:///Users/bago/project.code-workspace"}
```

### Step 6: Load Chat Sessions

Create `pkg/providers/copilotide/loader.go`:
- `LoadAllSessionFiles(workspaceDir string)` → List all JSON files in `chatSessions/` directory
- `LoadSessionFile(sessionPath string)` → Read and parse single session JSON file
- `LoadStateFile(sessionID, workspaceDir string)` → Load optional state file from `chatEditingSessions/[sessionID]/state.json`
- `LoadSessionByID(workspaceDir, sessionID string)` → Load specific session
- `LoadAllSessions(workspaceDir string)` → Load all sessions for workspace
- Error handling:
  - Missing chatSessions directory
  - Malformed JSON files
  - Missing state files (non-fatal)
  - Empty sessions (filter out)

**Implementation Notes:**
```go
func LoadAllSessionFiles(workspaceDir string) ([]string, error) {
    chatSessionsPath := filepath.Join(workspaceDir, "chatSessions")
    files, err := os.ReadDir(chatSessionsPath)
    if err != nil {
        return nil, fmt.Errorf("failed to read chat sessions: %w", err)
    }

    var sessionFiles []string
    for _, file := range files {
        if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
            sessionFiles = append(sessionFiles, filepath.Join(chatSessionsPath, file.Name()))
        }
    }
    return sessionFiles, nil
}

func LoadSessionFile(sessionPath string) (*VSCodeComposer, error) {
    data, err := os.ReadFile(sessionPath)
    if err != nil {
        return nil, fmt.Errorf("failed to read session file: %w", err)
    }

    var composer VSCodeComposer
    if err := json.Unmarshal(data, &composer); err != nil {
        return nil, fmt.Errorf("failed to parse session JSON: %w", err)
    }

    return &composer, nil
}

// Load optional state file (for editing sessions)
func LoadStateFile(sessionID, workspaceDir string) (*VSCodeStateFile, error) {
    statePath := filepath.Join(workspaceDir, "chatEditingSessions", sessionID, "state.json")

    // State file is optional
    if _, err := os.Stat(statePath); os.IsNotExist(err) {
        return nil, nil // Not an error - state files are optional
    }

    data, err := os.ReadFile(statePath)
    if err != nil {
        return nil, fmt.Errorf("failed to read state file: %w", err)
    }

    var state VSCodeStateFile
    if err := json.Unmarshal(data, &state); err != nil {
        return nil, fmt.Errorf("failed to parse state JSON: %w", err)
    }

    return &state, nil
}
```

### Step 7: Parse Response Array (Simple)

Create `pkg/providers/copilotide/parser.go`:

**Goal:** Simple response parsing - just identify tool calls and extract basic info. No complex intermediate structures.

```go
// ParseResponseKind identifies the response type without fully parsing
func ParseResponseKind(rawResponse json.RawMessage) (string, error) {
    var kindOnly struct {
        Kind string `json:"kind"`
    }
    if err := json.Unmarshal(rawResponse, &kindOnly); err != nil {
        return "", fmt.Errorf("failed to parse response kind: %w", err)
    }
    return kindOnly.Kind, nil
}

// IsHiddenTool checks if a tool invocation response is marked as hidden
func IsHiddenTool(rawResponse json.RawMessage) bool {
    var invocation VSCodeToolInvocationResponse
    if err := json.Unmarshal(rawResponse, &invocation); err != nil {
        return false
    }
    return invocation.Presentation == "hidden"
}

// BuildToolCallMap creates a lookup map from toolCallId to tool call info
func BuildToolCallMap(metadata VSCodeResultMetadata) map[string]VSCodeToolCallInfo {
    toolCalls := make(map[string]VSCodeToolCallInfo)

    for _, round := range metadata.ToolCallRounds {
        for _, call := range round.ToolCalls {
            toolCalls[call.ID] = call
        }
    }

    return toolCalls
}

// ExtractThinkingFromMetadata extracts thinking text from tool call rounds
func ExtractThinkingFromMetadata(metadata VSCodeResultMetadata) string {
    var thinking strings.Builder

    for _, round := range metadata.ToolCallRounds {
        if round.Response != "" {
            // Tool call round responses often contain thinking
            thinking.WriteString(round.Response)
            thinking.WriteString("\n\n")
        }
    }

    return strings.TrimSpace(thinking.String())
}

// ExtractFinalAgentMessage gets the final text response from metadata
func ExtractFinalAgentMessage(metadata VSCodeResultMetadata) string {
    // VS Code stores final messages in metadata.messages
    for _, msg := range metadata.Messages {
        if msg.Role == "assistant" {
            return msg.Content
        }
    }
    return ""
}

// GenerateSlug creates a URL-safe slug from composer name or first message
func GenerateSlug(composer VSCodeComposer) string {
    // Use custom title if available
    if composer.CustomTitle != "" {
        return slugify(composer.CustomTitle)
    }
    if composer.Name != "" {
        return slugify(composer.Name)
    }

    // Fall back to first request message
    if len(composer.Requests) > 0 {
        firstMsg := composer.Requests[0].Message.Text
        // Take first 50 chars
        if len(firstMsg) > 50 {
            firstMsg = firstMsg[:50]
        }
        return slugify(firstMsg)
    }

    return "untitled"
}

// FormatTimestamp converts Unix timestamp (ms) to ISO 8601
func FormatTimestamp(unixMs int64) string {
    t := time.Unix(0, unixMs*int64(time.Millisecond))
    return t.Format(time.RFC3339)
}

// slugify converts text to URL-safe slug
func slugify(text string) string {
    // Basic implementation - lowercase, replace spaces/special chars with hyphens
    text = strings.ToLower(text)
    text = strings.Map(func(r rune) rune {
        if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
            return r
        }
        return '-'
    }, text)
    // Remove consecutive hyphens
    text = strings.Join(strings.FieldsFunc(text, func(r rune) bool {
        return r == '-'
    }), "-")
    return strings.Trim(text, "-")
}
```

**Key Points:**
- Simple helper functions, no complex state tracking
- Parse response "kind" field only when needed
- Build tool call map from metadata for quick lookup
- Extract thinking from `toolCallRounds[].response`
- Skip hidden tools (presentation="hidden")
- Let the conversion logic in `agent_session.go` orchestrate everything

### Step 8: Convert to CLI's SessionData Format

Create `pkg/providers/copilotide/agent_session.go`:

**Goal:** Convert `VSCodeComposer` (raw JSON) directly to `schema.SessionData` (CLI format). No intermediate types.

```go
// ConvertToSessionData converts VS Code raw format to CLI's unified schema
func ConvertToSessionData(composer VSCodeComposer, projectPath string) spi.AgentChatSession {
    sessionData := &schema.SessionData{
        SchemaVersion: "1.0",
        Provider: schema.ProviderInfo{
            ID:      "copilotide",
            Name:    "VS Code Copilot",
            Version: "1.0",
        },
        SessionID:     composer.SessionID,
        CreatedAt:     FormatTimestamp(composer.CreationDate),
        UpdatedAt:     FormatTimestamp(composer.LastMessageDate),
        Slug:          GenerateSlug(composer),
        WorkspaceRoot: projectPath,
        Exchanges:     ConvertRequestsToExchanges(composer.Requests),
    }

    return spi.AgentChatSession{
        SessionID:   composer.SessionID,
        CreatedAt:   sessionData.CreatedAt,
        Slug:        sessionData.Slug,
        SessionData: sessionData,
        // RawData will be set by caller with raw JSON
    }
}

// ConvertRequestsToExchanges converts VS Code request blocks to exchanges
func ConvertRequestsToExchanges(requests []VSCodeRequestBlock) []schema.Exchange {
    var exchanges []schema.Exchange

    for _, req := range requests {
        exchange := schema.Exchange{
            ExchangeID: req.RequestID,
            StartTime:  FormatTimestamp(req.Timestamp),
            Messages:   ConvertRequestToMessages(req),
        }
        exchanges = append(exchanges, exchange)
    }

    return exchanges
}

// ConvertRequestToMessages converts one request block to message array
// Returns: [user message, agent message(s), tool message(s)]
func ConvertRequestToMessages(req VSCodeRequestBlock) []schema.Message {
    var messages []schema.Message

    // 1. User message
    userMsg := schema.Message{
        Role: schema.RoleUser,
        Content: []schema.ContentPart{
            {Type: schema.ContentTypeText, Text: req.Message.Text},
        },
    }
    messages = append(messages, userMsg)

    // 2. Extract thinking from tool call rounds (if present)
    thinking := ExtractThinkingFromMetadata(req.Result.Metadata)
    if thinking != "" {
        thinkingMsg := schema.Message{
            Role: schema.RoleAgent,
            Content: []schema.ContentPart{
                {Type: schema.ContentTypeThinking, Text: thinking},
            },
        }
        messages = append(messages, thinkingMsg)
    }

    // 3. Parse responses and extract tool calls
    toolMessages := ParseResponsesForTools(req.Response, req.Result.Metadata)
    messages = append(messages, toolMessages...)

    // 4. Final agent text message (from metadata.messages if present)
    finalText := ExtractFinalAgentMessage(req.Result.Metadata)
    if finalText != "" {
        agentMsg := schema.Message{
            Role: schema.RoleAgent,
            Content: []schema.ContentPart{
                {Type: schema.ContentTypeText, Text: finalText},
            },
            Model: req.ModelID,
        }
        messages = append(messages, agentMsg)
    }

    return messages
}

// ParseResponsesForTools extracts tool invocations from response array
func ParseResponsesForTools(responses []json.RawMessage, metadata VSCodeResultMetadata) []schema.Message {
    var toolMessages []schema.Message

    // Build tool call map from metadata
    toolCalls := BuildToolCallMap(metadata)

    // Process each response
    for _, rawResp := range responses {
        // Parse "kind" field first
        var kindOnly struct {
            Kind string `json:"kind"`
        }
        if err := json.Unmarshal(rawResp, &kindOnly); err != nil {
            continue
        }

        switch kindOnly.Kind {
        case "toolInvocationSerialized":
            var invocation VSCodeToolInvocationResponse
            if err := json.Unmarshal(rawResp, &invocation); err != nil {
                continue
            }

            // Skip hidden tools
            if invocation.Presentation == "hidden" {
                continue
            }

            // Find matching tool call and result
            toolInfo := BuildToolInfoFromInvocation(invocation, toolCalls)
            if toolInfo != nil {
                toolMsg := schema.Message{
                    Role: schema.RoleAgent,
                    Tool: toolInfo,
                }
                toolMessages = append(toolMessages, toolMsg)
            }

        // Handle other response types (textEditGroup, codeblockUri, etc.)
        // Can defer detailed handling to phase 2
        }
    }

    return toolMessages
}

// BuildToolInfoFromInvocation creates ToolInfo from VS Code invocation + metadata
func BuildToolInfoFromInvocation(invocation VSCodeToolInvocationResponse, toolCalls map[string]VSCodeToolCallInfo) *schema.ToolInfo {
    // Find tool call details from metadata
    toolCall, ok := toolCalls[invocation.ToolCallID]
    if !ok {
        return nil
    }

    toolInfo := &schema.ToolInfo{
        Name:  toolCall.Name,
        Type:  MapToolType(toolCall.Name),
        UseID: invocation.ToolCallID,
    }

    // Parse arguments if present
    if toolCall.Arguments != "" {
        var args map[string]interface{}
        if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err == nil {
            toolInfo.Input = args
        }
    }

    // Add output from results map
    // Can enhance this in phase 2

    return toolInfo
}

// MapToolType maps VS Code tool names to schema.ToolType constants
func MapToolType(toolName string) string {
    mapping := map[string]string{
        "bash":                schema.ToolTypeShell,
        "search_files":        schema.ToolTypeSearch,
        "read_file":           schema.ToolTypeRead,
        "write_to_file":       schema.ToolTypeWrite,
        "str_replace_editor":  schema.ToolTypeWrite,
        "list_files":          schema.ToolTypeSearch,
        // Add more as needed
    }

    if toolType, ok := mapping[toolName]; ok {
        return toolType
    }
    return schema.ToolTypeUnknown
}
```

**Key Points:**
- Convert directly from `VSCodeComposer` to `schema.SessionData`
- No intermediate unified types
- Start with basic tool detection, refine in phase 2
- Thinking extracted from `toolCallRounds[].response` if present
- Tool results from `toolCallResults` map
- Use schema constants: `schema.RoleUser`, `schema.ToolTypeShell`, etc.

### Step 9: Implement Core Provider Methods

In `pkg/providers/copilotide/provider.go`:

1. **Check(customCommand):**
   - Verify workspace storage directory exists
   - Check for at least one workspace directory
   - Return version info (could check VS Code version from paths)
   - Return CheckResult with success/error

2. **DetectAgent(projectPath, helpOutput):**
   - Check if workspace storage directory exists
   - Try to match projectPath to a workspace
   - Check if matched workspace has chatSessions directory
   - Check if any session files exist
   - Return true if sessions found
   - If helpOutput=true, print guidance about:
     - Workspace storage location
     - Expected workspace path
     - chatSessions directory

3. **GetAgentChatSession(projectPath, sessionID, debugRaw):**
   - Find matching workspace
   - Load specific session by ID from `chatSessions/[sessionID].json`
   - Load optional state file
   - Parse conversation
   - Convert to AgentChatSession
   - If debugRaw=true, write raw session JSON to `.specstory/debug/<sessionID>/raw-session.json`
   - Return single session

4. **GetAgentChatSessions(projectPath, debugRaw):**
   - Find workspace storage path
   - Match projectPath to workspace (scan workspace.json files)
   - If multiple matches, use newest workspace
   - Load all session files from `chatSessions/` directory
   - For each session, load optional state file
   - Filter out empty conversations
   - Convert each to AgentChatSession
   - If debugRaw=true, write debug files for each
   - Return array of sessions (workspace-filtered)

5. **ExecAgentAndWatch(...):**
   - Return error: "VS Code Copilot does not support execution via CLI"
   - This provider is read-only (IDE-based, not CLI-based)

6. **WatchAgent(ctx, projectPath, debugRaw, callback):**
   - Find matching workspace directory
   - Use fsnotify to watch `chatSessions/` directory
   - On new/modified files, parse and invoke callback
   - Support graceful shutdown via context
   - Debounce rapid changes (multiple events for same file)
   - Watch pattern: `*.json` files in chatSessions directory

**Implementation Notes:**
```go
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
    // Find matching workspace
    workspaceDir, err := FindWorkspaceForProject(projectPath)
    if err != nil {
        return nil, fmt.Errorf("failed to find workspace: %w", err)
    }

    // Load all session files
    sessionFiles, err := LoadAllSessionFiles(workspaceDir)
    if err != nil {
        return nil, fmt.Errorf("failed to load session files: %w", err)
    }

    var sessions []spi.AgentChatSession
    for _, sessionFile := range sessionFiles {
        composer, err := LoadSessionFile(sessionFile)
        if err != nil {
            slog.Warn("Failed to load session", "file", sessionFile, "error", err)
            continue
        }

        // Load optional state file
        state, err := LoadStateFile(composer.SessionID, workspaceDir)
        if err != nil {
            slog.Warn("Failed to load state file", "sessionId", composer.SessionID, "error", err)
            // Continue without state - it's optional
        }
        composer.State = state

        // Filter empty conversations
        if len(composer.Requests) == 0 {
            continue
        }

        // Convert to unified format
        session := ConvertToSessionData(composer)
        sessions = append(sessions, session)

        // Debug output
        if debugRaw {
            writeDebugFiles(composer, sessionFile)
        }
    }

    return sessions, nil
}

func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, callback spi.WatchCallback) error {
    workspaceDir, err := FindWorkspaceForProject(projectPath)
    if err != nil {
        return fmt.Errorf("failed to find workspace: %w", err)
    }

    chatSessionsPath := filepath.Join(workspaceDir, "chatSessions")

    watcher, err := fsnotify.NewWatcher()
    if err != nil {
        return fmt.Errorf("failed to create watcher: %w", err)
    }
    defer watcher.Close()

    if err := watcher.Add(chatSessionsPath); err != nil {
        return fmt.Errorf("failed to watch directory: %w", err)
    }

    // Debouncing map
    debounce := make(map[string]time.Time)
    const debounceWindow = 500 * time.Millisecond

    for {
        select {
        case <-ctx.Done():
            return nil

        case event := <-watcher.Events:
            // Only process JSON files
            if !strings.HasSuffix(event.Name, ".json") {
                continue
            }

            // Debounce rapid events
            now := time.Now()
            if lastEvent, ok := debounce[event.Name]; ok && now.Sub(lastEvent) < debounceWindow {
                continue
            }
            debounce[event.Name] = now

            // Process create/write events
            if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
                composer, err := LoadSessionFile(event.Name)
                if err != nil {
                    slog.Warn("Failed to load session", "file", event.Name, "error", err)
                    continue
                }

                session := ConvertToSessionData(composer)
                callback(session)
            }

        case err := <-watcher.Errors:
            slog.Warn("Watcher error", "error", err)
        }
    }
}
```

### Step 10: Register Provider

Modify `pkg/spi/factory/registry.go`:
```go
import (
    // ... existing imports ...
    "github.com/specstoryai/SpecStoryCLI/pkg/providers/copilotide"
)

func (r *Registry) registerAll() {
    // ... existing registrations ...

    copilotideProvider := copilotide.NewProvider()
    r.providers["copilotide"] = copilotideProvider
    slog.Debug("Registered provider", "id", "copilotide", "name", copilotideProvider.Name())

    r.initialized = true
}
```

### Step 11: Debug Output Support

When `--debug-raw` flag is passed:
- Write to `.specstory/debug/<sessionID>/`
- Files to write:
  - `raw-session.json` - Raw composer/session data from JSON file
  - `state.json` - Optional state file (if exists)
  - `session-data.json` - Unified SessionData format (handled by central CLI)
- Use `spi.WriteDebugSessionData()` utility

## Usage Examples

```zsh
# Check if VS Code Copilot sessions exist
./specstory check copilotide

# Sync all VS Code Copilot conversations
./specstory sync copilotide

# Sync specific conversation
./specstory sync copilotide -u <session-id>

# Debug mode - see raw data
./specstory sync copilotide --debug-raw

# Watch mode - monitor for new conversations
./specstory watch copilotide
```

## Phase 1 Scope (This Plan)

**In Scope:**
- ✅ JSON file reading from workspace chatSessions directory
- ✅ Workspace matching and filtering
- ✅ Conversation parsing (requests + responses)
- ✅ Tool invocation detection (from response array)
- ✅ Conversion to unified SessionData
- ✅ Debug output support
- ✅ Provider registration
- ✅ Optional state file loading

**Out of Scope (Future Phases):**
- ❌ Detailed tool output formatting differences
- ❌ Code block diff rendering
- ❌ MessagePart line range visualization
- ❌ Tool-specific markdown formatting
- ❌ Linear history visualization
- ❌ Edit snapshot handling
- ❌ InlineReference rendering

## Important Implementation Notes

### File System Access Patterns
1. **No SQLite for Sessions:** VS Code stores sessions as individual JSON files (simpler than Cursor's SQLite approach)
2. **SQLite Only for Workspace Matching:** Use SQLite to read workspace `state.vscdb` for identifying workspace
3. **Two Directory Types:**
   - `chatSessions/` - Main conversation JSON files
   - `chatEditingSessions/[sessionId]/state.json` - Optional editing state files
4. **File Watching:** Watch `chatSessions/` directory for new/modified `*.json` files

### Workspace Matching Challenges
1. **Symlinks:** Must use canonical paths for matching (resolve symlinks before comparison)
2. **Multiple Workspaces:** User may have same project in multiple workspace IDs - use newest
3. **Workspace Types:** Can be single folder or multi-root workspace file
4. **OS-Specific Paths:**
   - macOS: `~/Library/Application Support/Code/User/workspaceStorage/`
   - Linux: `~/.config/Code/User/workspaceStorage/`
   - WSL: `/mnt/c/Users/<username>/AppData/Roaming/Code/User/workspaceStorage/`

### WSL (Windows Subsystem for Linux) Support

When VS Code runs on Windows but operates on WSL projects, the CLI (running inside WSL) must handle several unique challenges:

**Storage location:** VS Code stores all data on the Windows filesystem, even for WSL projects. The CLI detects WSL via `/proc/version` (looking for "microsoft" or "wsl") and searches the Windows side at `/mnt/c/Users/*/AppData/Roaming/Code/User/workspaceStorage/`.

**WSL-specific workspace URI formats:** VS Code uses two non-standard URI formats for WSL projects, neither of which is a plain `file:///path`:

1. **`file://wsl.localhost/{DistroName}/path`** — The host is `wsl.localhost` and the first path component is the WSL distro name (e.g., `Ubuntu`). The `uriToPath()` function must detect the `wsl.localhost` host and strip the distro name prefix to recover the actual WSL filesystem path.

2. **`vscode-remote://wsl%2B{distro}/path`** — The scheme is `vscode-remote` and the host encodes the distro as `wsl+ubuntu` (percent-encoded as `wsl%2Bubuntu`). Go's `url.Parse()` rejects `%2B` in the host component, so the `parseVSCodeRemoteURI()` function parses these URIs manually (string splitting) to extract the path.

**Multiple workspace entries per project:** Because VS Code can store the same WSL project under different URI formats, a single project may have two or more matching workspace directories — each potentially containing different chat sessions. The `FindAllWorkspacesForProject()` function returns all matches, and session files are aggregated from all matching workspaces with deduplication by session ID. This ensures no conversations are missed regardless of which URI format was active when they were created.

### SSH Remote Support

When VS Code connects to remote servers via SSH (using the Remote-SSH extension), chat sessions are stored locally on the client machine but reference remote filesystem paths. The implementation approach is identical to Cursor IDE's SSH support since both use VS Code's remote infrastructure.

**Storage location:** All chat session data remains on the local client machine in the standard VS Code storage locations (`workspaceStorage/*/chatSessions/`), even though the code being edited is on a remote server via SSH.

**SSH remote workspace URI format:** VS Code uses a `vscode-remote://ssh-remote+{config}/path` URI format where:
- Scheme: `vscode-remote`
- Host: `ssh-remote+{hex-encoded-json-config}` (e.g., `ssh-remote%2B7b22686f73744e616d65223a226d61632d6d696e69227d` where the hex decodes to `{"hostName":"mac-mini"}`)
- Path: The remote filesystem path (e.g., `/Users/bago/code/myproject`)

**Path matching challenge:** The remote path in the workspace URI (e.g., `/Users/bago/code/myproject` on a remote server) won't directly match the local path where the CLI runs (e.g., `C:\Users\Admin\code\myproject` on Windows). Direct path comparison fails because:
- Different operating systems (Windows client vs Unix remote)
- Different user directories
- Only the repository name is guaranteed to match

**Repository name matching solution:** The `parseVSCodeRemoteURI()` function extracts the remote path from SSH URIs, and `FindAllWorkspacesForProject()` implements a fallback matching strategy:
1. **Primary:** Direct path matching (for local and WSL workspaces)
2. **Fallback for SSH:** Repository basename matching when:
   - The workspace URI is detected as SSH remote (`vscode-remote://ssh-remote+...`)
   - Direct path comparison fails
   - The repository basename (directory name) matches between local and remote paths

This approach works because users typically clone the same repository with the same name on both machines (e.g., both have a directory named `myproject`). While this is a heuristic that could theoretically match different repositories with the same name, in practice it reliably identifies the same project across local and remote environments.

**Example SSH workspace matching:**
```
Local path:  C:\Users\Admin\code\myproject (Windows)
Remote URI:  vscode-remote://ssh-remote%2B.../home/user/code/myproject (Linux)
Match by:    Both end with basename "myproject" ✓
```

**Implementation note:** Since Copilot IDE uses the same workspace infrastructure as Cursor IDE, the SSH support implementation can directly reuse the URI parsing logic from the Cursor IDE provider (`parseVSCodeRemoteURI()` function). This ensures consistent behavior across both providers.

### Workspace Matching Strategy and Trade-offs

**Current implementation:** The CLI matches workspaces by **repository basename** (folder name) rather than by exact workspace URI. This design decision has important implications:

**Why basename matching:**
When a user runs the CLI from a local directory (e.g., `C:\Users\Admin\code\myrepo`), we want to find conversations from all environments where they've worked on the same repository:
- Local workspace: `file:///c:/Users/Admin/code/myrepo`
- WSL workspace: `vscode-remote://wsl+ubuntu/home/user/code/myrepo`
- SSH workspace: `vscode-remote://ssh-remote+server/home/ubuntu/code/myrepo`

All three workspaces represent the same repository (`myrepo`) in different environments. Basename matching allows the CLI to find conversations across all these environments with a single command.

**The trade-off:**
This approach returns conversations from **all matching workspaces**, not just the current environment. For example, running `specstory sync` from the local Windows path will include conversations from local, WSL, and SSH remote workspaces if they all exist.

**For VS Code extension use (precise matching needed):**
When the CLI is invoked from a VS Code extension, the extension typically wants conversations from only its specific workspace (not all environments). This requires a different approach:

**WSL scenario (minor challenge):**
- The CLI can detect it's running in WSL (check `/proc/version`)
- Filter workspaces to only match WSL-type URIs (`vscode-remote://wsl+...`)
- Implementation: Add environment detection and URI type filtering
- **Solution complexity:** Low - code change only

**SSH scenario (requires parameter):**
- The extension knows its workspace URI via `vscode.workspace.workspaceFolders[0].uri.toString()`
- The extension needs to pass this URI to the CLI as a parameter
- The CLI filters to only the workspace matching that specific URI
- Implementation: Add `--workspace-uri` flag to accept and filter by exact URI
- **Solution complexity:** Medium - requires new parameter and exact URI matching logic

**Future enhancement:**
```bash
# Extension passes exact workspace URI for precise matching
specstory sync --workspace-uri="vscode-remote://ssh-remote%2Bserver/home/ubuntu/code/myrepo"

# CLI returns only conversations from that specific workspace
# Not from local or WSL workspaces of the same repository
```

This enhancement would provide precise workspace filtering while maintaining backward compatibility with the current basename matching behavior (when `--workspace-uri` is not specified).

### Response Polymorphism
VS Code uses polymorphic response arrays with "kind" discriminator:
```json
{
  "response": [
    {"kind": "toolInvocationSerialized", ...},
    {"kind": "codeblockUri", ...},
    {"kind": "textEditGroup", ...}
  ]
}
```
- Must parse each response element to check "kind" field first
- Then unmarshal into specific type
- Handle unknown kinds gracefully (log warning, skip)

### Tool Call Sequence Tracking
VS Code spreads tool invocation data across multiple locations:
1. **Invocation**: `response[].toolInvocationSerialized` with parameters
2. **Rounds**: `result.metadata.toolCallRounds[]` with LLM calls
3. **Results**: `result.metadata.toolCallResults[callId]` with outputs
4. **Code**: `response[].codeblockUri` or `response[].textEditGroup`

Must correlate these using IDs:
- `toolInvocationId` links invocation to sequence
- `toolCallId` links to specific LLM call and result

### Error Handling
1. **Missing Directories:** Graceful failure if workspace storage doesn't exist
2. **No Workspace Match:** Clear error message explaining workspace detection
3. **Empty Conversations:** Filter out sessions with no requests
4. **Malformed JSON:** Skip corrupted files, log warning, continue processing
5. **Missing State Files:** Non-fatal - state files are optional

## Testing Strategy

Manual testing approach (no unit tests initially per project conventions):
1. Create test workspace with sample chat sessions
2. Test `check` command - verify workspace detection
3. Test `sync` command - verify conversation export
4. Test `--debug-raw` - verify debug files are written
5. Verify markdown files are created in `.specstory/history/`
6. Test with empty workspace
7. Test with missing workspace storage
8. Test with optional state files

## Verification Checklist

After implementation:
- [ ] `./specstory check copilotide` successfully finds workspace
- [ ] `./specstory sync copilotide` exports conversations to `.specstory/history/`
- [ ] `./specstory sync copilotide --debug-raw` creates debug files
- [ ] Markdown files have correct format (timestamps, speakers, messages)
- [ ] Tool invocations are detected (basic level)
- [ ] Empty conversations are filtered out
- [ ] Provider appears in help text for all commands
- [ ] No errors with missing workspace storage (graceful failure)
- [ ] Watch mode detects new sessions in real-time

## Provider Comparison: cursoride vs copilotide

### Overview
Both providers read from IDE-based editors but use different storage mechanisms:
- **cursoride** = Cursor IDE (SQLite database)
- **copilotide** = VS Code Copilot (JSON files)

### Detailed Comparison

| Aspect | cursoride (Cursor IDE) | copilotide (VS Code Copilot) |
|--------|------------------------|------------------------------|
| **Product** | Cursor IDE (VS Code fork) | VS Code with GitHub Copilot extension |
| **Data Location** | Global: `~/.cursor/extensions/.../state.vscdb`<br>Workspace: `~/Library/.../workspaceStorage/` | `~/Library/Application Support/Code/User/workspaceStorage/[id]/chatSessions/` |
| **Storage Format** | SQLite database (single global DB) | Individual JSON files per session |
| **Database Schema** | Global DB: `cursorDiskKV` (key, value)<br>Workspace DB: `ItemTable` (key, value)<br>Keys: `composerData:*`, `bubbleId:*` | No database for sessions<br>JSON files: `[sessionId].json` |
| **Conversation Structure** | Flat `conversation` array on composer | Nested `requests` array with `response[]` arrays |
| **Tool Invocations** | `toolFormerData`, `capabilityType=15` | `response[].toolInvocationSerialized` with kind discriminator |
| **Code Blocks** | Part of conversation with `codeBlocks` array | Separate `codeblockUri` and `textEditGroup` response types |
| **Database Access** | SQL queries with WAL mode | Direct file reads with `os.ReadFile()` |
| **Session Metadata** | Indexed in `composer.composerData` | Stored in `chat.ChatSessionStore.index` in workspace `state.vscdb` |
| **Editing State** | Embedded in composer object | Separate `chatEditingSessions/[id]/state.json` files |
| **Project Matching** | Workspace URI matching via `workspace.json` | Same - workspace URI matching |
| **SQLite Library** | `modernc.org/sqlite` (pure Go) | `modernc.org/sqlite` (only for workspace matching) |
| **Execution** | ❌ Cannot execute (IDE-based) | ❌ Cannot execute (IDE-based) |
| **Watch Mode** | Watches global database + WAL file | Watches `chatSessions/` directory for `*.json` files |
| **Message Storage** | Separate bubble records in database | Self-contained in session JSON file |
| **Complexity** | High (global DB + workspace filtering + SQL) | Medium (JSON files + workspace matching) |

### Key Architectural Differences

**cursoride (SQLite):**
```
~/.cursor/extensions/cursor-context-manager-*/
  └── globalStorage/cursor-context-manager/
      └── state.vscdb (ALL composer data in SQLite)
        - cursorDiskKV table:
          - composerData:uuid → JSON
          - bubbleId:uuid:bubbleId → JSON
```
- **Complex:** Global database + workspace filtering with SQL queries
- **WAL Mode:** Required for non-blocking reads
- **Query Optimization:** IN clauses and LIKE patterns

**copilotide (JSON):**
```
~/Library/Application Support/Code/User/workspaceStorage/
  └── [workspace-id]/
      ├── workspace.json (workspace metadata)
      ├── chatSessions/
      │   ├── [sessionId].json (complete session data)
      │   └── [sessionId].json
      └── chatEditingSessions/
          └── [sessionId]/
              └── state.json (optional editing state)
```
- **Simpler:** Individual JSON files, no SQL needed
- **Self-Contained:** Each session file is complete
- **No Database Locking:** Just file reads

### Why Simpler Than Cursor

1. **No Global Database:** Each session is a separate file
2. **No SQL Queries:** Direct JSON parsing
3. **No WAL Mode:** No database locking concerns
4. **No Key Filtering:** No need to skip checkpoint/context keys
5. **Inherently Project-Scoped:** Sessions live in workspace directories

## Decisions

1. **SQLite Usage:** Minimal - only for workspace matching
   - Read workspace `state.vscdb` to find matching workspace
   - No SQLite needed for session data (JSON files)
   - Reuse existing `modernc.org/sqlite` dependency from cursoride

2. **Workspace Filtering:** Same approach as cursoride
   - Match projectPath to workspace by comparing URIs
   - Select newest workspace if multiple matches
   - Load sessions only from matched workspace

3. **Session ID Mapping:** Use sessionID directly from JSON
   - Maintains compatibility with extension
   - Simple mapping, no translation needed

4. **State File Loading:** Optional
   - Load if exists, continue if missing
   - Only needed for editing session visualization
   - Not required for basic conversation export

## Dependencies

- `modernc.org/sqlite` - Already added for cursoride (only for workspace matching)
- Existing SPI infrastructure
- fsnotify (already in use for claudecode)
- Standard library: encoding/json, path/filepath, os

## Success Criteria

- New `copilotide` provider is registered and available
- Can read VS Code Copilot JSON session files
- Can parse conversations from JSON files
- Can export conversations to markdown files
- Debug mode shows raw data structures
- No crashes or panics with missing/invalid data
- Graceful error messages for common issues
- Watch mode detects new sessions in real-time
