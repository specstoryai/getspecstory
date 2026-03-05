package copilotide

import "encoding/json"

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

// VSCodeToolInvocationResponse represents a tool invocation
type VSCodeToolInvocationResponse struct {
	Kind         string `json:"kind"` // "toolInvocationSerialized"
	ID           string `json:"id,omitempty"`
	ToolID       string `json:"toolId,omitempty"`
	ToolCallID   string `json:"toolCallId,omitempty"`
	Presentation string `json:"presentation,omitempty"` // "hidden" or empty
	// Add invocationMessage, pastTenseMessage if needed for tool parameters
}

// VSCodeTextEditGroupResponse represents file edits
type VSCodeTextEditGroupResponse struct {
	Kind  string         `json:"kind"` // "textEditGroup"
	Uri   VSCodeUri      `json:"uri"`
	Edits [][]VSCodeEdit `json:"edits"` // Nested array
	Done  bool           `json:"done,omitempty"`
}

// VSCodeCodeblockUriResponse represents a code block
type VSCodeCodeblockUriResponse struct {
	Kind   string    `json:"kind"` // "codeblockUri"
	Uri    VSCodeUri `json:"uri"`
	IsEdit bool      `json:"isEdit,omitempty"`
}

// VSCodeConfirmationResponse represents a user confirmation request
type VSCodeConfirmationResponse struct {
	Kind    string `json:"kind"` // "confirmation"
	Title   string `json:"title"`
	Message any    `json:"message"` // Can be string or object with value field
	IsUsed  bool   `json:"isUsed"`
}

// GetMessageText extracts the text from the message field (handles both string and object formats)
func (c *VSCodeConfirmationResponse) GetMessageText() string {
	if c.Message == nil {
		return ""
	}

	// Try string first
	if str, ok := c.Message.(string); ok {
		return str
	}

	// If it's an object, try to get the "value" field
	if msgMap, ok := c.Message.(map[string]any); ok {
		if value, ok := msgMap["value"].(string); ok {
			return value
		}
	}

	return ""
}

// VSCodeInlineReferenceResponse represents a reference to code/files
type VSCodeInlineReferenceResponse struct {
	Kind            string    `json:"kind"` // "inlineReference"
	InlineReference VSCodeUri `json:"inlineReference"`
}

// Result metadata - contains tool call rounds and results
type VSCodeResult struct {
	Metadata VSCodeResultMetadata `json:"metadata"`
	Timings  VSCodeTimings        `json:"timings"`
}

// VSCodeResultMetadata contains tool call information and final messages
type VSCodeResultMetadata struct {
	ToolCallRounds  []VSCodeToolCallRound           `json:"toolCallRounds,omitempty"`
	ToolCallResults map[string]VSCodeToolCallResult `json:"toolCallResults,omitempty"`
	Messages        []VSCodeMetadataMessage         `json:"messages,omitempty"`
}

// VSCodeToolCallRound represents one round of tool calls from the LLM
type VSCodeToolCallRound struct {
	Response  string               `json:"response"` // May contain thinking
	ToolCalls []VSCodeToolCallInfo `json:"toolCalls"`
}

// VSCodeToolCallInfo contains the tool call details
type VSCodeToolCallInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string - parse if needed
}

// VSCodeToolCallResult contains the result of a tool call
type VSCodeToolCallResult struct {
	Content []VSCodeToolCallContent `json:"content,omitempty"`
}

// VSCodeToolCallContent contains the content of a tool call result
type VSCodeToolCallContent struct {
	Value any `json:"value,omitempty"` // Can be string or complex object
}

// VSCodeMetadataMessage contains messages from the result metadata
type VSCodeMetadataMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// VSCodeTimings contains timing information
type VSCodeTimings struct {
	FirstProgress int64 `json:"firstProgress"`
	TotalElapsed  int64 `json:"totalElapsed"`
}

// Common types

// VSCodeUri represents a file URI or code block URI
type VSCodeUri struct {
	Scheme string `json:"scheme,omitempty"` // "file", "vscode-chat-code-block", etc.
	Path   string `json:"path,omitempty"`
	FSPath string `json:"fsPath,omitempty"`
}

// VSCodeEdit represents a text edit
type VSCodeEdit struct {
	Text  string      `json:"text"`
	Range VSCodeRange `json:"range"`
}

// VSCodeRange represents a range in a file
type VSCodeRange struct {
	StartLineNumber int `json:"startLineNumber"`
	StartColumn     int `json:"startColumn"`
	EndLineNumber   int `json:"endLineNumber"`
	EndColumn       int `json:"endColumn"`
}

// Optional state file types (for chatEditingSessions/[sessionId]/state.json)
// Only define these if needed - can defer to phase 2

// VSCodeStateFile represents the state file for editing sessions
type VSCodeStateFile struct {
	Version         int                   `json:"version"`
	SessionID       string                `json:"sessionId"`
	LinearHistory   []VSCodeLinearHistory `json:"linearHistory,omitempty"`
	RecentSnapshot  any                   `json:"recentSnapshot,omitempty"`  // Can be array or object
	PendingSnapshot any                   `json:"pendingSnapshot,omitempty"` // Can be array or object
	Timeline        *VSCodeTimeline       `json:"timeline,omitempty"`        // Version 2 format
}

// VSCodeTimeline represents the timeline in version 2 state format
type VSCodeTimeline struct {
	Operations []VSCodeOperation `json:"operations,omitempty"`
}

// VSCodeOperation represents a file operation in version 2 state format
type VSCodeOperation struct {
	Type  string     `json:"type"`            // "create", "textEdit", "delete"
	URI   *VSCodeUri `json:"uri,omitempty"`   // File URI
	Edits []any      `json:"edits,omitempty"` // Edit details
}

// VSCodeLinearHistory represents the linear history of edits
type VSCodeLinearHistory struct {
	RequestID string       `json:"requestId"`
	Stops     []VSCodeStop `json:"stops,omitempty"`
}

// VSCodeStop represents a stop in the edit history
type VSCodeStop struct {
	Entries []VSCodeStopEntry `json:"entries,omitempty"`
}

// VSCodeStopEntry represents an entry in a stop
type VSCodeStopEntry struct {
	Resource string `json:"resource,omitempty"` // File path or URI
}
