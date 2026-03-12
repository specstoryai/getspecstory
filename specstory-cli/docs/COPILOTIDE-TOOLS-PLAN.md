# VS Code Copilot IDE - Tool Implementation Plan

## Current Status

### ✅ What's Working
- All sessions load successfully
- Basic conversation parsing (user messages, agent responses)
- Thinking blocks correctly identified
- Empty session filtering
- Debug output for raw JSON

### ❌ What's Not Working
- Tool calls don't appear in markdown output
- **Root Cause:** ID mismatch between two systems:
  - `toolCallRounds` metadata uses OpenAI-style IDs: `call_mlWUQhVaoFaj26CRtR7sqD1j__vscode-1763048081513`
  - `toolInvocationSerialized` responses use VS Code IDs: `52142529-aec6-4fb4-94ac-ca0deb467986`

## Tool Usage Analysis

Based on analysis of all VS Code Copilot sessions in workspace storage:

### Most Common Tools (by frequency)
1. **apply_patch** - 14 calls (Write: apply code changes)
2. **grep_search** - 6 calls (Search: find text in files)
3. **read_file** - 6 calls (Read: read file contents)
4. **mcp_*** - 3 calls (MCP server tools)
5. **create_file** - 1 call (Write: create new files)
6. **file_search** - 1 call (Search: find files by name)
7. **list_dir** - 1 call (Generic: list directory)
8. **get_errors** - 1 call (Generic: get errors)
9. **manage_todo_list** - 1 call (Task: manage TODOs)
10. **semantic_search** - 1 call (Search: semantic search)
11. **insert_edit_into_file** - 1 call (Write: insert text)

### Tool Name Mapping

**OpenAI API Names → VS Code Internal IDs:**

| OpenAI Tool Name | VS Code Tool ID | Type | Priority |
|------------------|-----------------|------|----------|
| `grep_search` | `copilot_findTextInFiles` | search | HIGH |
| `apply_patch` | `copilot_applyPatch` | write | HIGH |
| `read_file` | `copilot_readFile` | read | HIGH |
| `insert_edit_into_file` | `copilot_insertEdit` | write | MEDIUM |
| `create_file` | `copilot_createFile` | write | MEDIUM |
| `file_search` | `copilot_searchFiles` | search | MEDIUM |
| `list_dir` | `copilot_listDirectory` | generic | LOW |
| `semantic_search` | `copilot_searchCodebase` | search | MEDIUM |
| `manage_todo_list` | `copilot_manageTodoList` | task | LOW |
| `get_errors` | `copilot_getErrors` | generic | LOW |
| `mcp_*` | `mcp_*` | mcp | MEDIUM |

## Implementation Strategy

### Phase 1: Fix Tool Correlation (ID Matching)

**Problem:** Two different ID systems don't match up.

**Solution Options:**

#### Option A: Match by Sequence/Order ⭐ RECOMMENDED
- Tool invocations in `response` array appear in sequence
- Tool calls in `toolCallRounds` also appear in sequence
- Match them by order within each round
- Ignore the mismatched IDs entirely

**Pros:**
- Simpler implementation
- Doesn't rely on ID matching
- Works even if ID formats change

**Cons:**
- Assumes ordering is consistent (need to verify)

#### Option B: Build Cross-Reference Map
- Parse both ID systems from raw JSON
- Look for correlation patterns (timestamps, sequence numbers, etc.)
- Build mapping table

**Pros:**
- More precise matching
- Handles out-of-order responses

**Cons:**
- Complex, fragile
- Relies on reverse-engineering VS Code's internal logic

#### Option C: Use invocationMessage Text Parsing
- Extract tool info from `invocationMessage.value` strings
- Example: "Searching text for `syncServiceUrl` (`**/apps/vscode-extension/src/**`)"
- Parse these messages to extract parameters

**Pros:**
- User-facing messages are stable
- Already formatted for display

**Cons:**
- Fragile (relies on message format)
- Loses structured data

### Phase 2: Implement Priority Tools

**Implementation Order:**

1. **HIGH Priority** (Most used, essential for understanding sessions)
   - ✅ `grep_search` / `copilot_findTextInFiles` - 6 calls
   - ✅ `read_file` / `copilot_readFile` - 6 calls
   - ✅ `apply_patch` / `copilot_applyPatch` - 14 calls

2. **MEDIUM Priority** (Less common but important)
   - `insert_edit_into_file` / `copilot_insertEdit` - 1 call
   - `create_file` / `copilot_createFile` - 1 call
   - `semantic_search` / `copilot_searchCodebase` - 1 call
   - `file_search` / `copilot_searchFiles` - 1 call
   - `mcp_*` - MCP server tools - 3 calls

3. **LOW Priority** (Rare, nice-to-have)
   - `list_dir` / `copilot_listDirectory`
   - `get_errors` / `copilot_getErrors`
   - `manage_todo_list` / `copilot_manageTodoList`

### Phase 3: Tool Handler Implementation

For each tool, implement:

1. **Parameter Extraction**
   - Parse from `toolCallRounds[].toolCalls[].arguments` (JSON string)
   - Map to schema.ToolInfo.Input

2. **Result Extraction**
   - Parse from `toolCallResults[toolCallId].content[].value`
   - Handle complex objects (use `valueToString` helper)
   - Map to schema.ToolInfo.Output

3. **Summary Generation** (optional)
   - Create human-readable summary
   - Store in schema.ToolInfo.Summary

4. **Formatted Markdown** (optional)
   - Generate provider-specific markdown
   - Store in schema.ToolInfo.FormattedMarkdown

## Detailed Implementation Plan

### Step 1: Fix Tool Matching (Use Sequence-Based Matching)

**File:** `pkg/providers/copilotide/agent_session.go`

**Current logic:**
```go
// Tries to match by toolCallId (doesn't work)
toolCall, ok := toolCalls[invocation.ToolCallID]
```

**New logic:**
```go
// Match by tracking sequence across rounds and response array
// Build a sequence-based correlation
```

**Implementation:**
1. Track position in response array for each `toolInvocationSerialized`
2. Track position in `toolCallRounds` for each tool call
3. Match visible (non-hidden) tool invocations to tool calls by sequence
4. Handle hidden tools (skip them in correlation)

### Step 2: Implement High-Priority Tools

#### 2.1: grep_search / copilot_findTextInFiles

**Input Parameters:**
```json
{
  "query": "syncServiceUrl",
  "isRegexp": false,
  "includePattern": "apps/vscode-extension/src/**"
}
```

**Output:**
- Complex object with search results
- Extract: file paths, match count, preview text

**Display:**
```markdown
<tool-use data-tool-type="search" data-tool-name="grep_search">
<details>
<summary>Search for "syncServiceUrl" in apps/vscode-extension/src/**</summary>

Found 20 matches across 8 files:
- apps/vscode-extension/src/services/AuthService.ts (5 matches)
- apps/vscode-extension/src/config.ts (2 matches)
...
</details>
</tool-use>
```

#### 2.2: read_file / copilot_readFile

**Input Parameters:**
```json
{
  "path": "apps/vscode-extension/src/services/AuthService.ts"
}
```

**Output:**
- File content (may be truncated)
- Line count

**Display:**
```markdown
<tool-use data-tool-type="read" data-tool-name="read_file">
<details>
<summary>Read file: apps/vscode-extension/src/services/AuthService.ts</summary>

```typescript
[file content]
```

</details>
</tool-use>
```

#### 2.3: apply_patch / copilot_applyPatch

**Input Parameters:**
```json
{
  "path": "apps/vscode-extension/src/services/AuthService.ts",
  "patch": "... unified diff format ..."
}
```

**Output:**
- Success/failure status
- Applied changes summary

**Display:**
```markdown
<tool-use data-tool-type="write" data-tool-name="apply_patch">
<details>
<summary>Applied patch to apps/vscode-extension/src/services/AuthService.ts</summary>

```diff
- syncServiceUrl: string;
+ syncUrl: string;
```

</details>
</tool-use>
```

### Step 3: Generic Tool Handler

For tools we don't have specific handlers for:

```go
func BuildGenericToolInfo(invocation, toolCall, result) *schema.ToolInfo {
    return &schema.ToolInfo{
        Name: toolCall.Name,
        Type: schema.ToolTypeGeneric,
        UseID: invocation.ToolCallID,
        Input: parseArguments(toolCall.Arguments),
        Output: extractResult(result),
        Summary: createGenericSummary(toolCall.Name, input, output),
    }
}
```

## Testing Strategy

### Test Cases

1. **Session with grep_search**
   - Verify: Parameters extracted correctly
   - Verify: Results shown with file paths
   - Verify: Match count displayed

2. **Session with apply_patch**
   - Verify: Patch content displayed
   - Verify: File path shown
   - Verify: Diff formatting

3. **Session with read_file**
   - Verify: File path displayed
   - Verify: Content shown (or truncated notice)

4. **Session with multiple tools**
   - Verify: Tools appear in correct order
   - Verify: Each tool has correct parameters
   - Verify: No tool invocations are lost

5. **Session with hidden tools**
   - Verify: Hidden tools don't appear in output

## Success Criteria

- ✅ Tool calls appear in markdown output
- ✅ At least 3 high-priority tools render correctly
- ✅ Tool parameters and results are extracted
- ✅ Tool sequence matches conversation flow
- ✅ Hidden tools are properly filtered out
- ✅ Generic tools (unknown types) render with basic info

## Future Enhancements (Phase 3+)

- Implement all medium-priority tools
- Add tool-specific markdown formatting
- Handle tool errors gracefully
- Support MCP tool variations
- Add tool summary generation
- Implement tool result streaming (for long outputs)
