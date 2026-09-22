package droidcli

import "encoding/json"

// fdBlockKind describes the semantic type of a parsed session chunk.
type fdBlockKind int

const (
	blockText fdBlockKind = iota
	blockTool
	blockTodo
	blockSummary
)

// fdBlock represents a normalized slice of the Factory Droid transcript.
type fdBlock struct {
	Kind        fdBlockKind
	Role        string
	MessageID   string
	Timestamp   string
	Model       string
	Text        string
	IsReasoning bool
	Tool        *fdToolCall
	Todo        *fdTodoState
	Summary     *fdSummary
}

// fdToolCall captures a tool invocation plus its result.
type fdToolCall struct {
	ID         string
	Name       string
	Input      json.RawMessage
	RiskLevel  string
	RiskReason string
	Result     *fdToolResult
	InvokedAt  string
}

// fdToolResult contains the textual output of a tool.
type fdToolResult struct {
	Content string
	IsError bool
}

// fdTodoState tracks todo list snapshots emitted by the agent.
type fdTodoState struct {
	Items []fdTodoItem
}

// fdTodoItem represents a single todo entry.
type fdTodoItem struct {
	Description string
	Status      string
}

// fdSummary stores compaction summary text from Factory Droid.
type fdSummary struct {
	Title string
	Body  string
}

// fdSession aggregates parsed metadata and blocks.
type fdSession struct {
	ID            string
	Title         string
	CreatedAt     string
	WorkspaceRoot string
	Blocks        []fdBlock
	Slug          string
	RawData       string
	TokenUsage    *fdTokenUsage // Session-level token usage from settings file
}

// fdTokenUsage holds token usage data from the Droid settings.json file.
// This is session-level aggregate data (not per-message).
type fdTokenUsage struct {
	InputTokens         int `json:"inputTokens"`
	OutputTokens        int `json:"outputTokens"`
	CacheCreationTokens int `json:"cacheCreationTokens"`
	CacheReadTokens     int `json:"cacheReadTokens"`
	ThinkingTokens      int `json:"thinkingTokens"`
}

// fdSettings represents the Droid .settings.json file structure.
type fdSettings struct {
	Model      string        `json:"model"`
	TokenUsage *fdTokenUsage `json:"tokenUsage"`
}
