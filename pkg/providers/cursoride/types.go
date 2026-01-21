package cursoride

// ComposerData represents the main composer data structure from Cursor IDE database
// This is stored in the global database with key "composerData:{composerId}"
type ComposerData struct {
	ComposerID                  string                       `json:"composerId"`
	Name                        string                       `json:"name,omitempty"`
	Version                     int                          `json:"_v,omitempty"`
	Conversation                []ComposerConversation       `json:"conversation,omitempty"`
	FullConversationHeadersOnly []ComposerConversationHeader `json:"fullConversationHeadersOnly"`
	ModelConfig                 *ModelConfig                 `json:"modelConfig,omitempty"`
	CreatedAt                   int64                        `json:"createdAt"`
	LastUpdatedAt               int64                        `json:"lastUpdatedAt,omitempty"`
}

// ComposerConversationHeader represents a conversation header (used when full conversation isn't loaded)
type ComposerConversationHeader struct {
	BubbleID string `json:"bubbleId"`
}

// ComposerConversation represents a single message/bubble in a conversation
// These are stored separately with key "bubbleId:{composerId}:{bubbleId}"
type ComposerConversation struct {
	BubbleID       string              `json:"bubbleId"`
	Type           int                 `json:"type"` // 1=user, 2=assistant
	Text           string              `json:"text"`
	Thinking       *ThinkingData       `json:"thinking,omitempty"`       // Extended thinking for models with thinking capability
	CapabilityType int                 `json:"capabilityType,omitempty"` // 15=tool
	UnifiedMode    int                 `json:"unifiedMode,omitempty"`    // 1=Ask, 2=Agent, 5=Plan
	TimingInfo     *TimingInfo         `json:"timingInfo,omitempty"`
	ToolFormerData *ToolInvocationData `json:"toolFormerData,omitempty"`
	ModelInfo      *ModelInfo          `json:"modelInfo,omitempty"`
}

// ThinkingData contains the thinking/reasoning text for models with thinking capability
type ThinkingData struct {
	Text string `json:"text"`
}

// ModelInfo contains model information for a bubble
type ModelInfo struct {
	ModelName string `json:"modelName,omitempty"`
}

// ModelConfig contains the model configuration for the composer (V3+)
type ModelConfig struct {
	ModelName string `json:"modelName,omitempty"`
}

// TimingInfo contains timing information for a conversation bubble
// Note: Cursor stores timing values as floats (with fractional milliseconds)
type TimingInfo struct {
	ClientStartTime   float64 `json:"clientStartTime,omitempty"`
	ClientRpcSendTime float64 `json:"clientRpcSendTime,omitempty"`
	ClientSettleTime  float64 `json:"clientSettleTime,omitempty"`
	ClientEndTime     float64 `json:"clientEndTime,omitempty"`
}

// ToolInvocationData represents tool invocation information
type ToolInvocationData struct {
	ToolName string `json:"toolName,omitempty"`
	// Add more fields as needed
}

// WorkspaceComposerRefs represents the workspace-specific composer references
// Stored in workspace database with key "composer.composerData"
type WorkspaceComposerRefs struct {
	AllComposers []ComposerRef `json:"allComposers"`
}

// ComposerRef is a reference to a composer ID in the workspace
type ComposerRef struct {
	ComposerID string `json:"composerId"`
}

// WorkspaceJSON represents the structure of workspace.json files
type WorkspaceJSON struct {
	Workspace string `json:"workspace,omitempty"` // Multi-root workspace file URI
	Folder    string `json:"folder,omitempty"`    // Single folder URI
}
