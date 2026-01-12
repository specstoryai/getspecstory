package schema

import (
	"testing"
)

// Helper function to create a valid SessionData for tests
func validSessionData() SessionData {
	return SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "test-provider",
			Name:    "Test Provider",
			Version: "1.0.0",
		},
		SessionID:     "test-session-id",
		CreatedAt:     "2025-01-01T00:00:00Z",
		WorkspaceRoot: "/test/workspace",
		Exchanges:     []Exchange{},
	}
}

func TestSessionData_Validate(t *testing.T) {
	tests := []struct {
		name     string
		modify   func(*SessionData)
		expected bool
	}{
		{
			name:     "valid session with all required fields",
			modify:   func(s *SessionData) {},
			expected: true,
		},
		{
			name:     "valid session with empty exchanges array",
			modify:   func(s *SessionData) { s.Exchanges = []Exchange{} },
			expected: true,
		},
		{
			name: "valid session with exchanges",
			modify: func(s *SessionData) {
				s.Exchanges = []Exchange{
					{
						ExchangeID: "exchange-1",
						Messages: []Message{
							{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}}},
						},
					},
				}
			},
			expected: true,
		},
		{
			name:     "missing schemaVersion",
			modify:   func(s *SessionData) { s.SchemaVersion = "" },
			expected: false,
		},
		{
			name:     "wrong schemaVersion",
			modify:   func(s *SessionData) { s.SchemaVersion = "2.0" },
			expected: false,
		},
		{
			name:     "missing provider.id",
			modify:   func(s *SessionData) { s.Provider.ID = "" },
			expected: false,
		},
		{
			name:     "missing provider.name",
			modify:   func(s *SessionData) { s.Provider.Name = "" },
			expected: false,
		},
		{
			name:     "missing provider.version",
			modify:   func(s *SessionData) { s.Provider.Version = "" },
			expected: false,
		},
		{
			name:     "missing sessionId",
			modify:   func(s *SessionData) { s.SessionID = "" },
			expected: false,
		},
		{
			name:     "missing createdAt",
			modify:   func(s *SessionData) { s.CreatedAt = "" },
			expected: false,
		},
		{
			name:     "missing workspaceRoot",
			modify:   func(s *SessionData) { s.WorkspaceRoot = "" },
			expected: false,
		},
		{
			name: "multiple missing fields",
			modify: func(s *SessionData) {
				s.SchemaVersion = ""
				s.SessionID = ""
				s.Provider.ID = ""
			},
			expected: false,
		},
		{
			name: "exchange with missing exchangeId",
			modify: func(s *SessionData) {
				s.Exchanges = []Exchange{{ExchangeID: "", Messages: []Message{}}}
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := validSessionData()
			tt.modify(&session)
			got := session.Validate()
			if got != tt.expected {
				t.Errorf("SessionData.Validate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMessage_validate(t *testing.T) {
	tests := []struct {
		name     string
		message  Message
		expected bool
	}{
		// User message tests
		{
			name: "user message with content - valid",
			message: Message{
				Role:    RoleUser,
				Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}},
			},
			expected: true,
		},
		{
			name: "user message without content - invalid",
			message: Message{
				Role:    RoleUser,
				Content: []ContentPart{},
			},
			expected: false,
		},
		{
			name: "user message with nil content - invalid",
			message: Message{
				Role: RoleUser,
			},
			expected: false,
		},
		{
			name: "user message with tool - invalid",
			message: Message{
				Role:    RoleUser,
				Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}},
				Tool:    &ToolInfo{Name: "test", Type: ToolTypeGeneric},
			},
			expected: false,
		},
		{
			name: "user message with model - invalid",
			message: Message{
				Role:    RoleUser,
				Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}},
				Model:   "claude-3",
			},
			expected: false,
		},
		// Agent message tests
		{
			name: "agent message with content only - valid",
			message: Message{
				Role:    RoleAgent,
				Content: []ContentPart{{Type: ContentTypeText, Text: "Response"}},
			},
			expected: true,
		},
		{
			name: "agent message with tool only - valid",
			message: Message{
				Role: RoleAgent,
				Tool: &ToolInfo{Name: "read_file", Type: ToolTypeRead},
			},
			expected: true,
		},
		{
			name: "agent message with pathHints only - valid",
			message: Message{
				Role:      RoleAgent,
				PathHints: []string{"/some/path"},
			},
			expected: true,
		},
		{
			name: "agent message with content and tool - valid",
			message: Message{
				Role:    RoleAgent,
				Content: []ContentPart{{Type: ContentTypeText, Text: "Let me read that file"}},
				Tool:    &ToolInfo{Name: "read_file", Type: ToolTypeRead},
			},
			expected: true,
		},
		{
			name: "agent message with nothing - invalid",
			message: Message{
				Role: RoleAgent,
			},
			expected: false,
		},
		{
			name: "agent message with empty content and no tool - invalid",
			message: Message{
				Role:      RoleAgent,
				Content:   []ContentPart{},
				PathHints: []string{},
			},
			expected: false,
		},
		{
			name: "agent message with model - valid (model allowed for agent)",
			message: Message{
				Role:    RoleAgent,
				Content: []ContentPart{{Type: ContentTypeText, Text: "Response"}},
				Model:   "claude-3",
			},
			expected: true,
		},
		// Invalid role tests
		{
			name: "invalid role - empty string",
			message: Message{
				Role:    "",
				Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}},
			},
			expected: false,
		},
		{
			name: "invalid role - unknown role",
			message: Message{
				Role:    "assistant",
				Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.message.validate(0, 0)
			if got != tt.expected {
				t.Errorf("Message.validate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestContentPart_validate(t *testing.T) {
	tests := []struct {
		name     string
		part     ContentPart
		expected bool
	}{
		{
			name:     "type text - valid",
			part:     ContentPart{Type: ContentTypeText, Text: "Some text"},
			expected: true,
		},
		{
			name:     "type thinking - valid",
			part:     ContentPart{Type: ContentTypeThinking, Text: "Some thinking"},
			expected: true,
		},
		{
			name:     "type text with empty text - valid (text content not validated)",
			part:     ContentPart{Type: ContentTypeText, Text: ""},
			expected: true,
		},
		{
			name:     "invalid type - other",
			part:     ContentPart{Type: "other", Text: "Content"},
			expected: false,
		},
		{
			name:     "invalid type - empty string",
			part:     ContentPart{Type: "", Text: "Content"},
			expected: false,
		},
		{
			name:     "invalid type - image",
			part:     ContentPart{Type: "image", Text: "Content"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.part.validate(0, 0, 0)
			if got != tt.expected {
				t.Errorf("ContentPart.validate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestToolInfo_validate(t *testing.T) {
	tests := []struct {
		name     string
		tool     ToolInfo
		expected bool
	}{
		// Valid tool types
		{
			name:     "type write - valid",
			tool:     ToolInfo{Name: "write_file", Type: ToolTypeWrite},
			expected: true,
		},
		{
			name:     "type read - valid",
			tool:     ToolInfo{Name: "read_file", Type: ToolTypeRead},
			expected: true,
		},
		{
			name:     "type search - valid",
			tool:     ToolInfo{Name: "grep", Type: ToolTypeSearch},
			expected: true,
		},
		{
			name:     "type shell - valid",
			tool:     ToolInfo{Name: "bash", Type: ToolTypeShell},
			expected: true,
		},
		{
			name:     "type task - valid",
			tool:     ToolInfo{Name: "todo_write", Type: ToolTypeTask},
			expected: true,
		},
		{
			name:     "type generic - valid",
			tool:     ToolInfo{Name: "custom_tool", Type: ToolTypeGeneric},
			expected: true,
		},
		{
			name:     "type unknown - valid",
			tool:     ToolInfo{Name: "mysterious_tool", Type: ToolTypeUnknown},
			expected: true,
		},
		// Invalid cases
		{
			name:     "invalid type - empty string",
			tool:     ToolInfo{Name: "some_tool", Type: ""},
			expected: false,
		},
		{
			name:     "invalid type - unsupported",
			tool:     ToolInfo{Name: "some_tool", Type: "execute"},
			expected: false,
		},
		{
			name:     "missing name - invalid",
			tool:     ToolInfo{Name: "", Type: ToolTypeRead},
			expected: false,
		},
		{
			name:     "missing name and invalid type - invalid",
			tool:     ToolInfo{Name: "", Type: "bad"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.tool.validate(0, 0)
			if got != tt.expected {
				t.Errorf("ToolInfo.validate() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestSessionData_Validate_WithMessages tests SessionData validation with
// embedded message validation to ensure the integration works correctly.
func TestSessionData_Validate_WithMessages(t *testing.T) {
	tests := []struct {
		name     string
		session  SessionData
		expected bool
	}{
		{
			name: "valid session with valid user and agent messages",
			session: SessionData{
				SchemaVersion: "1.0",
				Provider:      ProviderInfo{ID: "test", Name: "Test", Version: "1.0"},
				SessionID:     "session-1",
				CreatedAt:     "2025-01-01T00:00:00Z",
				WorkspaceRoot: "/test",
				Exchanges: []Exchange{
					{
						ExchangeID: "exchange-1",
						Messages: []Message{
							{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}}},
							{Role: RoleAgent, Content: []ContentPart{{Type: ContentTypeText, Text: "Hi there"}}},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "invalid - user message without content",
			session: SessionData{
				SchemaVersion: "1.0",
				Provider:      ProviderInfo{ID: "test", Name: "Test", Version: "1.0"},
				SessionID:     "session-1",
				CreatedAt:     "2025-01-01T00:00:00Z",
				WorkspaceRoot: "/test",
				Exchanges: []Exchange{
					{
						ExchangeID: "exchange-1",
						Messages: []Message{
							{Role: RoleUser, Content: []ContentPart{}},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "invalid - agent message with empty response",
			session: SessionData{
				SchemaVersion: "1.0",
				Provider:      ProviderInfo{ID: "test", Name: "Test", Version: "1.0"},
				SessionID:     "session-1",
				CreatedAt:     "2025-01-01T00:00:00Z",
				WorkspaceRoot: "/test",
				Exchanges: []Exchange{
					{
						ExchangeID: "exchange-1",
						Messages: []Message{
							{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}}},
							{Role: RoleAgent},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "invalid - message with invalid content type",
			session: SessionData{
				SchemaVersion: "1.0",
				Provider:      ProviderInfo{ID: "test", Name: "Test", Version: "1.0"},
				SessionID:     "session-1",
				CreatedAt:     "2025-01-01T00:00:00Z",
				WorkspaceRoot: "/test",
				Exchanges: []Exchange{
					{
						ExchangeID: "exchange-1",
						Messages: []Message{
							{Role: RoleUser, Content: []ContentPart{{Type: "invalid", Text: "Hello"}}},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "invalid - agent message with invalid tool type",
			session: SessionData{
				SchemaVersion: "1.0",
				Provider:      ProviderInfo{ID: "test", Name: "Test", Version: "1.0"},
				SessionID:     "session-1",
				CreatedAt:     "2025-01-01T00:00:00Z",
				WorkspaceRoot: "/test",
				Exchanges: []Exchange{
					{
						ExchangeID: "exchange-1",
						Messages: []Message{
							{Role: RoleUser, Content: []ContentPart{{Type: ContentTypeText, Text: "Hello"}}},
							{Role: RoleAgent, Tool: &ToolInfo{Name: "tool", Type: "invalid"}},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.session.Validate()
			if got != tt.expected {
				t.Errorf("SessionData.Validate() = %v, want %v", got, tt.expected)
			}
		})
	}
}
