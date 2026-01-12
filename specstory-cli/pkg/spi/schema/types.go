package schema

import (
	"log/slog"
)

// Role constants for message types
const (
	RoleUser  = "user"
	RoleAgent = "agent"
)

// ContentType constants for content parts
const (
	ContentTypeText     = "text"
	ContentTypeThinking = "thinking"
)

// ToolType constants for tool classification
const (
	ToolTypeWrite   = "write"
	ToolTypeRead    = "read"
	ToolTypeSearch  = "search"
	ToolTypeShell   = "shell"
	ToolTypeTask    = "task"
	ToolTypeGeneric = "generic"
	ToolTypeUnknown = "unknown"
)

// SessionData is the unified data format for sessions from all terminal coding agent providers
type SessionData struct {
	SchemaVersion string       `json:"schemaVersion"`
	Provider      ProviderInfo `json:"provider"`
	SessionID     string       `json:"sessionId"`
	CreatedAt     string       `json:"createdAt"`
	UpdatedAt     string       `json:"updatedAt,omitempty"`
	Slug          string       `json:"slug,omitempty"`
	WorkspaceRoot string       `json:"workspaceRoot"`
	Exchanges     []Exchange   `json:"exchanges"`
}

// ProviderInfo is the information about the agent provider that created the session
type ProviderInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Exchange is a conversational turn with ordered messages
type Exchange struct {
	ExchangeID string                 `json:"exchangeId"`
	StartTime  string                 `json:"startTime,omitempty"`
	EndTime    string                 `json:"endTime,omitempty"`
	Messages   []Message              `json:"messages"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// Message is either a user or agent message
type Message struct {
	ID        string                 `json:"id,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
	Role      string                 `json:"role"` // "user" or "agent"
	Model     string                 `json:"model,omitempty"`
	Content   []ContentPart          `json:"content,omitempty"`
	Tool      *ToolInfo              `json:"tool,omitempty"`
	PathHints []string               `json:"pathHints,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// ContentPart is a content part (text or thinking)
type ContentPart struct {
	Type string `json:"type"` // "text" or "thinking"
	Text string `json:"text"` // Content text (for both text and thinking)
}

// ToolInfo is tool use information
type ToolInfo struct {
	Name string `json:"name"` // Name of the tool

	// Tool type classification:
	// Use `write` for tools that write to the file system
	// Use `read` for tools that read from the file system
	// Use `search` for tools that search the file system or the web (e.g. `grep`, `glob`, `find`, `web_search` etc.)
	// Use `shell` for tools that run shell commands (e.g. `bash`, `sed`, `awk`, `ls` etc.)
	// Use `task` for tools that manage tasks (e.g. `todo`, `todo_write`, `todo_read`, `tasklist`, `task_manager` etc.)
	// Use `generic` for tools that the provider knows about but that don't map well to a specific type
	// Use `unknown` for tools that the provider does not know about
	Type string `json:"type"` // Type of the tool: write, read, search, shell, task, generic, unknown

	UseID             string                 `json:"useId,omitempty"`             // ID of the tool use
	Input             map[string]interface{} `json:"input,omitempty"`             // Input to the tool
	Output            map[string]interface{} `json:"output,omitempty"`            // Output from the tool
	Summary           *string                `json:"summary,omitempty"`           // Summary content (will be wrapped in <summary> tags by CLI)
	FormattedMarkdown *string                `json:"formattedMarkdown,omitempty"` // Pre-formatted markdown from provider (will be wrapped in <formatted-markdown> tags by CLI)
}

// Validate checks the SessionData against schema constraints and logs warnings for any issues.
// Returns true if validation passed, false if there were any issues.
func (s *SessionData) Validate() bool {
	valid := true

	// Check schema version
	if s.SchemaVersion != "1.0" {
		slog.Warn("schema validation: schemaVersion must be '1.0'", "got", s.SchemaVersion)
		valid = false
	}

	// Check required root fields
	if s.Provider.ID == "" {
		slog.Warn("schema validation: provider.id is required")
		valid = false
	}
	if s.Provider.Name == "" {
		slog.Warn("schema validation: provider.name is required")
		valid = false
	}
	if s.Provider.Version == "" {
		slog.Warn("schema validation: provider.version is required")
		valid = false
	}
	if s.SessionID == "" {
		slog.Warn("schema validation: sessionId is required")
		valid = false
	}
	if s.CreatedAt == "" {
		slog.Warn("schema validation: createdAt is required")
		valid = false
	}
	if s.WorkspaceRoot == "" {
		slog.Warn("schema validation: workspaceRoot is required")
		valid = false
	}

	// Validate each exchange
	for i, exchange := range s.Exchanges {
		if exchange.ExchangeID == "" {
			slog.Warn("schema validation: exchange.exchangeId is required", "exchangeIndex", i)
			valid = false
		}

		// Validate each message in the exchange
		for j, msg := range exchange.Messages {
			if !msg.validate(i, j) {
				valid = false
			}
		}
	}

	return valid
}

// validate checks a single message against schema constraints.
func (m *Message) validate(exchangeIndex, messageIndex int) bool {
	valid := true
	logCtx := []any{"exchangeIndex", exchangeIndex, "messageIndex", messageIndex}

	// Check role is valid
	if m.Role != RoleUser && m.Role != RoleAgent {
		slog.Warn("schema validation: message.role must be 'user' or 'agent'",
			append(logCtx, "got", m.Role)...)
		valid = false
	}

	// User message constraints
	if m.Role == RoleUser {
		if len(m.Content) == 0 {
			slog.Warn("schema validation: user message must have non-empty content", logCtx...)
			valid = false
		}
		if m.Tool != nil {
			slog.Warn("schema validation: user message cannot have tool", logCtx...)
			valid = false
		}
		if m.Model != "" {
			slog.Warn("schema validation: user message cannot have model", logCtx...)
			valid = false
		}
	}

	// Agent message constraints: must have at least one of content, tool, or pathHints
	if m.Role == RoleAgent {
		hasContent := len(m.Content) > 0
		hasTool := m.Tool != nil
		hasPathHints := len(m.PathHints) > 0
		if !hasContent && !hasTool && !hasPathHints {
			slog.Warn("schema validation: agent message must have content, tool, or pathHints", logCtx...)
			valid = false
		}
	}

	// Validate content parts
	for k, part := range m.Content {
		if !part.validate(exchangeIndex, messageIndex, k) {
			valid = false
		}
	}

	// Validate tool if present
	if m.Tool != nil {
		if !m.Tool.validate(exchangeIndex, messageIndex) {
			valid = false
		}
	}

	return valid
}

// validate checks a single content part against schema constraints.
func (c *ContentPart) validate(exchangeIndex, messageIndex, partIndex int) bool {
	valid := true
	logCtx := []any{"exchangeIndex", exchangeIndex, "messageIndex", messageIndex, "partIndex", partIndex}

	if c.Type != ContentTypeText && c.Type != ContentTypeThinking {
		slog.Warn("schema validation: contentPart.type must be 'text' or 'thinking'",
			append(logCtx, "got", c.Type)...)
		valid = false
	}

	return valid
}

// validate checks tool info against schema constraints.
func (t *ToolInfo) validate(exchangeIndex, messageIndex int) bool {
	valid := true
	logCtx := []any{"exchangeIndex", exchangeIndex, "messageIndex", messageIndex}

	if t.Name == "" {
		slog.Warn("schema validation: tool.name is required", logCtx...)
		valid = false
	}

	validTypes := map[string]bool{
		ToolTypeWrite:   true,
		ToolTypeRead:    true,
		ToolTypeSearch:  true,
		ToolTypeShell:   true,
		ToolTypeTask:    true,
		ToolTypeGeneric: true,
		ToolTypeUnknown: true,
	}
	if !validTypes[t.Type] {
		slog.Warn("schema validation: tool.type must be one of: write, read, search, shell, task, generic, unknown",
			append(logCtx, "got", t.Type)...)
		valid = false
	}

	return valid
}
