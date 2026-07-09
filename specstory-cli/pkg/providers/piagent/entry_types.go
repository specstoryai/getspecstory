package piagent

import "encoding/json"

// entryType constants for pi JSONL v3 session entries.
const (
	entrySession             = "session"
	entryMessage             = "message"
	entryModelChange         = "model_change"
	entryThinkingLevelChange = "thinking_level_change"
	entryCompaction          = "compaction"
	entryBranchSummary       = "branch_summary"
	entryCustom              = "custom"
	entryCustomMessage       = "custom_message"
	entryLabel               = "label"
	entrySessionInfo         = "session_info"
)

// messageRole constants for pi message entries.
const (
	roleUser          = "user"
	roleAssistant     = "assistant"
	roleToolResult    = "toolResult"
	roleBashExecution = "bashExecution"
	roleCustom        = "custom"
	roleBranchSummary = "branchSummary"
	roleCompaction    = "compactionSummary"
)

// rawEntry is the minimal envelope every pi entry shares. The message payload
// (for type=="message") is kept as raw json.RawMessage so we can decode it per
// role without fighting a single union struct.
type rawEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"` // null for the first entry
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message,omitempty"`
}

// sessionHeader is the first line of a pi session file (no id/parentId).
type sessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

// contentBlock is one element of an assistant or user message content array.
type contentBlock struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	Thinking string         `json:"thinking,omitempty"`
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name,omitempty"`
	Args     map[string]any `json:"arguments,omitempty"`
}

// userMessage is a pi user-role message: content is string OR []contentBlock.
type userMessage struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
}

// assistantMessage is a pi assistant-role message.
type assistantMessage struct {
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	Provider   string         `json:"provider"`
	Model      string         `json:"model"`
	API        string         `json:"api"`
	StopReason string         `json:"stopReason"`
	Usage      *piUsage       `json:"usage,omitempty"`
}

// piUsage is the token-usage shape on a pi assistant message.
type piUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
}

// toolResultMessage is a pi toolResult-role message (a top-level entry).
type toolResultMessage struct {
	Role       string         `json:"role"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []contentBlock `json:"content"`
	IsError    bool           `json:"isError"`
}
