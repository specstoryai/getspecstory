package opencode

// This file contains OpenCode-specific type definitions for parsing JSON session data.
// OpenCode stores session data in a distributed file structure:
// ~/.local/share/opencode/storage/
// ├── project/{projectHash}.json      # Project metadata
// ├── session/{projectHash}/          # Session files per project
// │   └── ses_{id}.json
// ├── message/ses_{id}/               # Message files per session
// │   └── msg_{id}.json
// └── part/msg_{id}/                  # Part files per message
//     └── prt_{id}.json

// -----------------------------------------------------------------------------
// Time Types
// -----------------------------------------------------------------------------

// TimeInfo represents creation and update timestamps used in Project and Session.
type TimeInfo struct {
	Created string `json:"created"`
	Updated string `json:"updated"`
}

// MessageTime represents timestamps for messages including optional completion time.
// Completed is a pointer because it's only present when the message processing finishes,
// whereas Created and Updated are always set when the message exists.
type MessageTime struct {
	Created   string  `json:"created"`
	Updated   string  `json:"updated"`
	Completed *string `json:"completed,omitempty"`
}

// PartTime represents timestamps for parts with start/end timing.
type PartTime struct {
	Start *string `json:"start,omitempty"`
	End   *string `json:"end,omitempty"`
}

// -----------------------------------------------------------------------------
// Project Types
// -----------------------------------------------------------------------------

// Project represents an OpenCode project stored in project/{projectHash}.json.
// The projectHash is a SHA-1 hash of the absolute worktree path.
type Project struct {
	ID       string   `json:"id"`
	Worktree string   `json:"worktree"`
	VCS      string   `json:"vcs"`
	Time     TimeInfo `json:"time"`
	// Sandboxes contains sandbox environment configurations (structure varies by sandbox type).
	Sandboxes []any     `json:"sandboxes"`
	Icon      *IconInfo `json:"icon,omitempty"`
}

// IconInfo represents optional project icon information.
type IconInfo struct {
	Type  string `json:"type,omitempty"`
	Value string `json:"value,omitempty"`
}

// -----------------------------------------------------------------------------
// Session Types
// -----------------------------------------------------------------------------

// Session represents an OpenCode session stored in session/{projectHash}/ses_{id}.json.
// Sessions can have a parent (for branching/subagent scenarios).
type Session struct {
	ID         string          `json:"id"`
	Slug       string          `json:"slug"`
	Version    string          `json:"version"`
	ProjectID  string          `json:"projectID"`
	Directory  string          `json:"directory"`
	ParentID   *string         `json:"parentID,omitempty"`
	Title      *string         `json:"title,omitempty"`
	Permission []Permission    `json:"permission,omitempty"`
	Time       TimeInfo        `json:"time"`
	Summary    *SessionSummary `json:"summary,omitempty"`
}

// Permission represents a permission granted to the session.
type Permission struct {
	Type  string `json:"type"`
	Path  string `json:"path,omitempty"`
	Scope string `json:"scope,omitempty"`
}

// SessionSummary contains summary information about a session.
type SessionSummary struct {
	MessageCount int     `json:"messageCount,omitempty"`
	TokenCount   int     `json:"tokenCount,omitempty"`
	TotalCost    float64 `json:"totalCost,omitempty"`
	Title        string  `json:"title,omitempty"`
}

// -----------------------------------------------------------------------------
// Message Types
// -----------------------------------------------------------------------------

// Message represents an OpenCode message stored in message/ses_{id}/msg_{id}.json.
// Messages can be from "user" or "assistant" roles.
type Message struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"sessionID"`
	Role       string          `json:"role"` // "user" or "assistant"
	Time       MessageTime     `json:"time"`
	ParentID   *string         `json:"parentID,omitempty"`
	ModelID    *string         `json:"modelID,omitempty"`
	ProviderID *string         `json:"providerID,omitempty"`
	Mode       *string         `json:"mode,omitempty"`
	Agent      *string         `json:"agent,omitempty"`
	Path       *PathInfo       `json:"path,omitempty"`
	Cost       *float64        `json:"cost,omitempty"`
	Tokens     *TokenInfo      `json:"tokens,omitempty"`
	Finish     *string         `json:"finish,omitempty"` // Finish reason: "stop", "length", "tool_use", etc.
	Summary    *MessageSummary `json:"summary,omitempty"`
}

// PathInfo contains path-related information for a message.
// Used for tracking file context during the conversation.
type PathInfo struct {
	Root  string   `json:"root,omitempty"`
	Files []string `json:"files,omitempty"`
}

// TokenInfo contains token usage statistics.
type TokenInfo struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cache  int `json:"cache,omitempty"`
	Total  int `json:"total,omitempty"`
}

// MessageSummary contains summary information about a message.
type MessageSummary struct {
	Content string   `json:"content,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// -----------------------------------------------------------------------------
// Part Types
// -----------------------------------------------------------------------------

// Part represents a content part within a message stored in part/msg_{id}/prt_{id}.json.
// Parts have different types that determine which fields are populated:
// - text: Regular text content (Text field)
// - reasoning: Internal reasoning/thinking content (Text field)
// - tool: Tool invocation with state tracking (CallID, Tool, State fields)
// - step-start: Marks beginning of an agentic step (Snapshot field)
// - step-finish: Marks end of an agentic step with stats (Reason, Cost, Tokens fields)
// - patch: File change tracking (Hash, Files fields)
// - file: File reference (metadata contains file info)
// - compaction: Summarized/compacted content (Text field with summary)
type Part struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"` // text, tool, reasoning, step-start, step-finish, patch, file, compaction

	// Common fields
	Time     *PartTime      `json:"time,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`

	// Text and reasoning type fields
	Text *string `json:"text,omitempty"`

	// Tool type fields
	CallID *string    `json:"callID,omitempty"`
	Tool   *string    `json:"tool,omitempty"`
	State  *ToolState `json:"state,omitempty"`

	// Step-start and step-finish fields
	Snapshot *string `json:"snapshot,omitempty"`
	Reason   *string `json:"reason,omitempty"` // For step-finish

	// Step-finish cost/token tracking
	Cost   *float64   `json:"cost,omitempty"`
	Tokens *TokenInfo `json:"tokens,omitempty"`

	// Patch type fields (for file changes)
	Hash  *string  `json:"hash,omitempty"`
	Files []string `json:"files,omitempty"`
}

// Part type constants for easier comparison.
const (
	PartTypeText       = "text"
	PartTypeReasoning  = "reasoning"
	PartTypeTool       = "tool"
	PartTypeStepStart  = "step-start"
	PartTypeStepFinish = "step-finish"
	PartTypePatch      = "patch"
	PartTypeFile       = "file"
	PartTypeCompaction = "compaction"
)

// ToolState represents the state of a tool invocation.
// Tool invocations progress through states: pending -> running -> completed/error.
// The Error field captures error messages when Status is "error".
type ToolState struct {
	Status string         `json:"status"` // pending, running, completed, error
	Input  map[string]any `json:"input,omitempty"`
	Output any            `json:"output,omitempty"`
	Time   *PartTime      `json:"time,omitempty"`
	Title  *string        `json:"title,omitempty"`
	Error  *string        `json:"error,omitempty"`
}

// Tool state constants for easier comparison.
const (
	ToolStatusPending   = "pending"
	ToolStatusRunning   = "running"
	ToolStatusCompleted = "completed"
	ToolStatusError     = "error"
)

// -----------------------------------------------------------------------------
// Assembled Types
// -----------------------------------------------------------------------------

// FullSession represents a complete session with all its messages and parts assembled.
// This is used internally for processing and conversion to the SpecStory schema.
type FullSession struct {
	Session  *Session
	Project  *Project
	Messages []FullMessage
}

// FullMessage represents a message with all its parts assembled.
type FullMessage struct {
	Message *Message
	Parts   []Part
}

// -----------------------------------------------------------------------------
// Role Constants
// -----------------------------------------------------------------------------

// Message role constants matching OpenCode's conventions.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)
