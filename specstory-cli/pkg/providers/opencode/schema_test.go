package opencode

import (
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestMapToolType(t *testing.T) {
	tests := []struct {
		toolName string
		expected string
	}{
		// Read tools
		{"read", schema.ToolTypeRead},
		{"Read", schema.ToolTypeRead},
		{"READ", schema.ToolTypeRead},
		{"read_file", schema.ToolTypeRead},
		{"view_file", schema.ToolTypeRead},
		{"cat", schema.ToolTypeRead},
		{"webfetch", schema.ToolTypeRead},
		{"web_fetch", schema.ToolTypeRead},

		// Write/Edit tools
		{"write", schema.ToolTypeWrite},
		{"edit", schema.ToolTypeWrite},
		{"write_file", schema.ToolTypeWrite},
		{"edit_file", schema.ToolTypeWrite},
		{"create_file", schema.ToolTypeWrite},
		{"append_to_file", schema.ToolTypeWrite},
		{"patch", schema.ToolTypeWrite},

		// Shell tools
		{"bash", schema.ToolTypeShell},
		{"shell", schema.ToolTypeShell},
		{"shell_command", schema.ToolTypeShell},
		{"run", schema.ToolTypeShell},
		{"exec", schema.ToolTypeShell},
		{"execute", schema.ToolTypeShell},

		// Search tools
		{"glob", schema.ToolTypeSearch},
		{"grep", schema.ToolTypeSearch},
		{"find", schema.ToolTypeSearch},
		{"search", schema.ToolTypeSearch},
		{"ripgrep", schema.ToolTypeSearch},
		{"rg", schema.ToolTypeSearch},
		{"websearch", schema.ToolTypeSearch},
		{"web_search", schema.ToolTypeSearch},

		// Task tools
		{"task", schema.ToolTypeTask},
		{"todo", schema.ToolTypeTask},
		{"todowrite", schema.ToolTypeTask},
		{"todo_write", schema.ToolTypeTask},
		{"update_plan", schema.ToolTypeTask},
		{"plan", schema.ToolTypeTask},

		// Generic tools
		{"mcp", schema.ToolTypeGeneric},
		{"lsp", schema.ToolTypeGeneric},
		{"git", schema.ToolTypeGeneric},
		{"agent", schema.ToolTypeGeneric},
		{"delegate", schema.ToolTypeGeneric},
		{"memory", schema.ToolTypeGeneric},
		{"save_memory", schema.ToolTypeGeneric},

		// Unknown tools
		{"unknown_tool", schema.ToolTypeUnknown},
		{"something_random", schema.ToolTypeUnknown},

		// Substring matching fallbacks
		{"my_read_tool", schema.ToolTypeRead},
		{"custom_write_helper", schema.ToolTypeWrite},
		{"run_bash_script", schema.ToolTypeShell},
		{"file_search_util", schema.ToolTypeSearch},
		{"task_manager", schema.ToolTypeTask},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			got := MapToolType(tt.toolName)
			if got != tt.expected {
				t.Errorf("MapToolType(%q) = %q, want %q", tt.toolName, got, tt.expected)
			}
		})
	}
}

func TestConvertToSessionData_Success(t *testing.T) {
	text := "Hello, how can I help?"
	userText := "What is 2+2?"

	fullSession := &FullSession{
		Session: &Session{
			ID:        "ses_abc123",
			Slug:      "test-session",
			ProjectID: "proj_xyz",
			Directory: "/home/user/project",
			Time: TimeInfo{
				Created: "2025-01-01T10:00:00Z",
				Updated: "2025-01-01T11:00:00Z",
			},
		},
		Project: &Project{
			ID:       "proj_xyz",
			Worktree: "/home/user/project",
		},
		Messages: []FullMessage{
			{
				Message: &Message{
					ID:        "msg_001",
					SessionID: "ses_abc123",
					Role:      RoleUser,
					Time:      MessageTime{Created: "2025-01-01T10:00:00Z"},
				},
				Parts: []Part{
					{
						ID:   "prt_001",
						Type: PartTypeText,
						Text: &userText,
					},
				},
			},
			{
				Message: &Message{
					ID:        "msg_002",
					SessionID: "ses_abc123",
					Role:      RoleAssistant,
					ModelID:   strPtr("claude-3-opus"),
					Time:      MessageTime{Created: "2025-01-01T10:00:01Z"},
				},
				Parts: []Part{
					{
						ID:   "prt_002",
						Type: PartTypeText,
						Text: &text,
					},
				},
			},
		},
	}

	sessionData, err := ConvertToSessionData(fullSession, "1.0.0")
	if err != nil {
		t.Fatalf("ConvertToSessionData returned error: %v", err)
	}

	// Verify basic fields
	if sessionData.SchemaVersion != "1.0" {
		t.Errorf("SchemaVersion = %q, want %q", sessionData.SchemaVersion, "1.0")
	}
	if sessionData.Provider.ID != "opencode" {
		t.Errorf("Provider.ID = %q, want %q", sessionData.Provider.ID, "opencode")
	}
	if sessionData.Provider.Name != "OpenCode" {
		t.Errorf("Provider.Name = %q, want %q", sessionData.Provider.Name, "OpenCode")
	}
	if sessionData.Provider.Version != "1.0.0" {
		t.Errorf("Provider.Version = %q, want %q", sessionData.Provider.Version, "1.0.0")
	}
	if sessionData.SessionID != "ses_abc123" {
		t.Errorf("SessionID = %q, want %q", sessionData.SessionID, "ses_abc123")
	}
	if sessionData.WorkspaceRoot != "/home/user/project" {
		t.Errorf("WorkspaceRoot = %q, want %q", sessionData.WorkspaceRoot, "/home/user/project")
	}

	// Verify exchanges
	if len(sessionData.Exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(sessionData.Exchanges))
	}

	exchange := sessionData.Exchanges[0]
	if exchange.ExchangeID != "ses_abc123:0" {
		t.Errorf("ExchangeID = %q, want %q", exchange.ExchangeID, "ses_abc123:0")
	}
}

func TestConvertToSessionData_NilSession(t *testing.T) {
	_, err := ConvertToSessionData(nil, "1.0.0")
	if err == nil {
		t.Fatal("ConvertToSessionData should return error for nil session")
	}
}

func TestConvertToSessionData_NilSessionField(t *testing.T) {
	fullSession := &FullSession{
		Session: nil,
	}

	_, err := ConvertToSessionData(fullSession, "1.0.0")
	if err == nil {
		t.Fatal("ConvertToSessionData should return error for nil Session field")
	}
}

func TestConvertToSessionData_WithParentID(t *testing.T) {
	text := "Hello"
	parentID := "ses_parent"

	fullSession := &FullSession{
		Session: &Session{
			ID:        "ses_child",
			ParentID:  &parentID,
			Directory: "/home/user/project",
			Time:      TimeInfo{Created: "2025-01-01T10:00:00Z"},
		},
		Messages: []FullMessage{
			{
				Message: &Message{
					ID:   "msg_001",
					Role: RoleUser,
					Time: MessageTime{Created: "2025-01-01T10:00:00Z"},
				},
				Parts: []Part{
					{ID: "prt_001", Type: PartTypeText, Text: &text},
				},
			},
		},
	}

	sessionData, err := ConvertToSessionData(fullSession, "1.0.0")
	if err != nil {
		t.Fatalf("ConvertToSessionData returned error: %v", err)
	}

	// ParentID should be stored in the first exchange's metadata
	if len(sessionData.Exchanges) == 0 {
		t.Fatal("expected at least 1 exchange")
	}

	metadata := sessionData.Exchanges[0].Metadata
	if metadata == nil {
		t.Fatal("expected metadata to be set with parentID")
	}

	if metadata["sessionParentID"] != parentID {
		t.Errorf("sessionParentID = %v, want %q", metadata["sessionParentID"], parentID)
	}
}

func TestConvertToSessionData_AutoGeneratedSlugReplacement(t *testing.T) {
	userText := "Help me write a function"

	tests := []struct {
		name           string
		sessionSlug    string
		expectGenerate bool
	}{
		{
			name:           "ses_ prefix triggers generation",
			sessionSlug:    "ses_abc123",
			expectGenerate: true,
		},
		{
			name:           "session_ prefix triggers generation",
			sessionSlug:    "session_xyz",
			expectGenerate: true,
		},
		{
			name:           "hex string triggers generation",
			sessionSlug:    "abcdef1234567890",
			expectGenerate: true,
		},
		{
			name:           "UUID format triggers generation",
			sessionSlug:    "12345678-1234-1234-1234-123456789abc",
			expectGenerate: true,
		},
		{
			name:           "human readable slug preserved",
			sessionSlug:    "my-cool-session",
			expectGenerate: false,
		},
		{
			name:           "empty slug triggers generation",
			sessionSlug:    "",
			expectGenerate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullSession := &FullSession{
				Session: &Session{
					ID:        "ses_test",
					Slug:      tt.sessionSlug,
					Directory: "/home/user/project",
					Time:      TimeInfo{Created: "2025-01-01T10:00:00Z"},
				},
				Messages: []FullMessage{
					{
						Message: &Message{
							ID:   "msg_001",
							Role: RoleUser,
							Time: MessageTime{Created: "2025-01-01T10:00:00Z"},
						},
						Parts: []Part{
							{ID: "prt_001", Type: PartTypeText, Text: &userText},
						},
					},
				},
			}

			sessionData, err := ConvertToSessionData(fullSession, "1.0.0")
			if err != nil {
				t.Fatalf("ConvertToSessionData returned error: %v", err)
			}

			if tt.expectGenerate {
				// Should have generated a new slug from user message
				if sessionData.Slug == tt.sessionSlug {
					t.Errorf("expected slug to be generated, but got original: %q", sessionData.Slug)
				}
			} else {
				// Should preserve the original slug
				if sessionData.Slug != tt.sessionSlug {
					t.Errorf("expected slug to be preserved as %q, got %q", tt.sessionSlug, sessionData.Slug)
				}
			}
		})
	}
}

func TestConvertMessage_AllPartTypes(t *testing.T) {
	text := "Hello"
	reasoning := "Let me think..."
	compacted := "Summarized content"
	toolName := "read"
	callID := "call_123"

	tests := []struct {
		name              string
		part              Part
		expectedRole      string
		expectedContent   string
		expectTool        bool
		expectContentType string
	}{
		{
			name:              "text part",
			part:              Part{ID: "prt_1", Type: PartTypeText, Text: &text},
			expectedRole:      schema.RoleAgent,
			expectedContent:   text,
			expectContentType: schema.ContentTypeText,
		},
		{
			name:              "reasoning part",
			part:              Part{ID: "prt_2", Type: PartTypeReasoning, Text: &reasoning},
			expectedRole:      schema.RoleAgent,
			expectedContent:   reasoning,
			expectContentType: schema.ContentTypeThinking,
		},
		{
			name:            "compaction part",
			part:            Part{ID: "prt_3", Type: PartTypeCompaction, Text: &compacted},
			expectedRole:    schema.RoleAgent,
			expectedContent: "[Compacted] " + compacted,
		},
		{
			name: "tool part",
			part: Part{
				ID:     "prt_4",
				Type:   PartTypeTool,
				Tool:   &toolName,
				CallID: &callID,
				State: &ToolState{
					Status: ToolStatusCompleted,
					Input:  map[string]interface{}{"file_path": "/test.txt"},
				},
			},
			expectedRole: schema.RoleAgent,
			expectTool:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &Message{
				ID:   "msg_1",
				Role: RoleAssistant,
				Time: MessageTime{Created: "2025-01-01T10:00:00Z"},
			}

			messages := ConvertMessage(msg, []Part{tt.part}, "/workspace")

			if len(messages) == 0 {
				t.Fatal("expected at least one message")
			}

			schemaMsg := messages[0]

			if schemaMsg.Role != tt.expectedRole {
				t.Errorf("Role = %q, want %q", schemaMsg.Role, tt.expectedRole)
			}

			if tt.expectTool {
				if schemaMsg.Tool == nil {
					t.Error("expected Tool to be set")
				} else {
					if schemaMsg.Tool.Name != toolName {
						t.Errorf("Tool.Name = %q, want %q", schemaMsg.Tool.Name, toolName)
					}
					if schemaMsg.Tool.UseID != callID {
						t.Errorf("Tool.UseID = %q, want %q", schemaMsg.Tool.UseID, callID)
					}
				}
			} else {
				if len(schemaMsg.Content) == 0 {
					t.Error("expected Content to be non-empty")
				} else {
					if schemaMsg.Content[0].Text != tt.expectedContent {
						t.Errorf("Content[0].Text = %q, want %q", schemaMsg.Content[0].Text, tt.expectedContent)
					}
					if tt.expectContentType != "" && schemaMsg.Content[0].Type != tt.expectContentType {
						t.Errorf("Content[0].Type = %q, want %q", schemaMsg.Content[0].Type, tt.expectContentType)
					}
				}
			}
		})
	}
}

func TestConvertMessage_SkippedPartTypes(t *testing.T) {
	// When all parts are internal markers (step-start, step-finish, patch),
	// they are skipped and no messages are generated. This is intentional -
	// we don't want to generate empty messages for internal markers.
	skippedTypes := []string{PartTypeStepStart, PartTypeStepFinish, PartTypePatch}

	for _, partType := range skippedTypes {
		t.Run(partType, func(t *testing.T) {
			msg := &Message{
				ID:   "msg_1",
				Role: RoleAssistant,
				Time: MessageTime{Created: "2025-01-01T10:00:00Z"},
			}

			text := "content"
			parts := []Part{
				{ID: "prt_1", Type: partType, Text: &text},
			}

			messages := ConvertMessage(msg, parts, "/workspace")

			// Should return empty slice since all parts were skipped.
			// This is correct behavior - we don't want to generate empty messages
			// for internal marker parts.
			if len(messages) != 0 {
				t.Fatalf("expected 0 messages for skipped part type %s, got %d", partType, len(messages))
			}
		})
	}
}

func TestConvertMessage_EmptyParts(t *testing.T) {
	msg := &Message{
		ID:      "msg_1",
		Role:    RoleAssistant,
		ModelID: strPtr("claude-3-opus"),
		Time:    MessageTime{Created: "2025-01-01T10:00:00Z"},
	}

	messages := ConvertMessage(msg, []Part{}, "/workspace")

	// Should create a basic message even with no parts
	if len(messages) != 1 {
		t.Fatalf("expected 1 message for empty parts, got %d", len(messages))
	}

	if messages[0].ID != "msg_1" {
		t.Errorf("ID = %q, want %q", messages[0].ID, "msg_1")
	}
	if messages[0].Model != "claude-3-opus" {
		t.Errorf("Model = %q, want %q", messages[0].Model, "claude-3-opus")
	}
}

func TestConvertMessage_NilMessage(t *testing.T) {
	messages := ConvertMessage(nil, []Part{}, "/workspace")

	if messages != nil {
		t.Errorf("expected nil for nil message, got %v", messages)
	}
}

func TestConvertPart_NilPart(t *testing.T) {
	schemaMsg := ConvertPart(nil, schema.RoleAgent, "model", "/workspace")

	if schemaMsg != nil {
		t.Error("expected nil for nil part")
	}
}

func TestConvertPart_EmptyText(t *testing.T) {
	emptyText := ""

	tests := []struct {
		name string
		part Part
	}{
		{
			name: "text part with empty text",
			part: Part{ID: "prt_1", Type: PartTypeText, Text: &emptyText},
		},
		{
			name: "text part with nil text",
			part: Part{ID: "prt_2", Type: PartTypeText, Text: nil},
		},
		{
			name: "reasoning part with empty text",
			part: Part{ID: "prt_3", Type: PartTypeReasoning, Text: &emptyText},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schemaMsg := ConvertPart(&tt.part, schema.RoleAgent, "model", "/workspace")

			if schemaMsg != nil {
				t.Error("expected nil for empty/nil text")
			}
		})
	}
}

func TestConvertPart_ToolWithError(t *testing.T) {
	toolName := "bash"
	callID := "call_123"
	errorMsg := "command failed"

	part := Part{
		ID:     "prt_1",
		Type:   PartTypeTool,
		Tool:   &toolName,
		CallID: &callID,
		State: &ToolState{
			Status: ToolStatusError,
			Input:  map[string]interface{}{"command": "rm -rf /"},
			Error:  &errorMsg,
		},
	}

	schemaMsg := ConvertPart(&part, schema.RoleAgent, "model", "/workspace")

	if schemaMsg == nil {
		t.Fatal("expected message for tool part")
	}

	if schemaMsg.Tool == nil {
		t.Fatal("expected Tool to be set")
	}

	if schemaMsg.Tool.Output == nil {
		t.Fatal("expected Output to be set")
	}

	output := schemaMsg.Tool.Output
	if output["is_error"] != true {
		t.Errorf("expected is_error=true, got %v", output["is_error"])
	}
	if output["error"] != errorMsg {
		t.Errorf("expected error=%q, got %v", errorMsg, output["error"])
	}
}

func TestConvertPart_FilePart(t *testing.T) {
	part := Part{
		ID:   "prt_1",
		Type: PartTypeFile,
		Metadata: map[string]any{
			"path": "/home/user/test.txt",
			"name": "test.txt",
		},
	}

	schemaMsg := ConvertPart(&part, schema.RoleAgent, "model", "/workspace")

	if schemaMsg == nil {
		t.Fatal("expected message for file part")
	}

	if len(schemaMsg.Content) == 0 {
		t.Fatal("expected content for file part")
	}

	if schemaMsg.Content[0].Text != "File: /home/user/test.txt" {
		t.Errorf("Content = %q, want 'File: /home/user/test.txt'", schemaMsg.Content[0].Text)
	}
}

func TestExtractPathHintsFromTool(t *testing.T) {
	tests := []struct {
		name          string
		toolName      string
		input         map[string]interface{}
		workspaceRoot string
		expected      []string
	}{
		{
			name:     "single file_path",
			toolName: "read",
			input: map[string]interface{}{
				"file_path": "/workspace/test.txt",
			},
			workspaceRoot: "/workspace",
			expected:      []string{"test.txt"},
		},
		{
			name:     "path field",
			toolName: "read",
			input: map[string]interface{}{
				"path": "/workspace/src/main.go",
			},
			workspaceRoot: "/workspace",
			expected:      []string{"src/main.go"},
		},
		{
			name:     "multiple path fields",
			toolName: "diff",
			input: map[string]interface{}{
				"source":      "/workspace/a.txt",
				"destination": "/workspace/b.txt",
			},
			workspaceRoot: "/workspace",
			expected:      []string{"a.txt", "b.txt"},
		},
		{
			name:     "array of paths",
			toolName: "batch_read",
			input: map[string]interface{}{
				"files": []interface{}{"/workspace/a.txt", "/workspace/b.txt"},
			},
			workspaceRoot: "/workspace",
			expected:      []string{"a.txt", "b.txt"},
		},
		{
			name:          "nil input",
			toolName:      "read",
			input:         nil,
			workspaceRoot: "/workspace",
			expected:      nil,
		},
		{
			name:     "absolute path outside workspace",
			toolName: "read",
			input: map[string]interface{}{
				"file_path": "/etc/passwd",
			},
			workspaceRoot: "/workspace",
			expected:      []string{"/etc/passwd"},
		},
		{
			name:     "relative path",
			toolName: "read",
			input: map[string]interface{}{
				"file_path": "relative/path.txt",
			},
			workspaceRoot: "/workspace",
			expected:      []string{"relative/path.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathHintsFromTool(tt.toolName, tt.input, tt.workspaceRoot)

			if tt.expected == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}

			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d paths, got %d: %v", len(tt.expected), len(got), got)
			}

			for i, expected := range tt.expected {
				if got[i] != expected {
					t.Errorf("path[%d] = %q, want %q", i, got[i], expected)
				}
			}
		})
	}
}

func TestIsAutoGeneratedSlug(t *testing.T) {
	tests := []struct {
		slug     string
		expected bool
	}{
		// Auto-generated prefixes
		{"ses_abc123", true},
		{"session_xyz", true},

		// Hex strings (8+ chars)
		{"abcdef12", true},
		{"1234567890abcdef", true},

		// UUID format (32 hex chars no dashes)
		{"12345678123412341234123456789abc", true},

		// UUID format (with dashes)
		{"12345678-1234-1234-1234-123456789abc", true},

		// Human readable slugs
		{"my-cool-session", false},
		{"feature-branch", false},
		{"bugfix-123", false},

		// Short hex-like strings (less than 8 chars)
		{"abc123", false},

		// Mixed content
		{"abc123xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			got := isAutoGeneratedSlug(tt.slug)
			if got != tt.expected {
				t.Errorf("isAutoGeneratedSlug(%q) = %v, want %v", tt.slug, got, tt.expected)
			}
		})
	}
}

func TestIsHexString(t *testing.T) {
	tests := []struct {
		s        string
		expected bool
	}{
		{"abc123", true},
		{"ABCDEF", true},
		{"0123456789abcdef", true},
		{"xyz", false},
		{"abc-123", false},
		{"", true}, // Empty string is technically all hex
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := isHexString(tt.s)
			if got != tt.expected {
				t.Errorf("isHexString(%q) = %v, want %v", tt.s, got, tt.expected)
			}
		})
	}
}

func TestIsUUIDFormat(t *testing.T) {
	tests := []struct {
		s        string
		expected bool
	}{
		{"12345678-1234-1234-1234-123456789abc", true},
		{"ABCDEF12-ABCD-ABCD-ABCD-ABCDEF123456", true},
		{"12345678-1234-1234-1234-12345678", false},      // Too short
		{"12345678123412341234123456789abc", false},      // No dashes
		{"12345678-1234-1234-1234-123456789abcx", false}, // Too long
		{"1234567g-1234-1234-1234-123456789abc", false},  // Invalid char
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := isUUIDFormat(tt.s)
			if got != tt.expected {
				t.Errorf("isUUIDFormat(%q) = %v, want %v", tt.s, got, tt.expected)
			}
		})
	}
}

func TestBuildExchangesFromMessages(t *testing.T) {
	userText := "Hello"
	agentText := "Hi there!"
	userText2 := "Second question"
	agentText2 := "Second answer"

	tests := []struct {
		name             string
		messages         []FullMessage
		expectedCount    int
		firstExchangeLen int
	}{
		{
			name:             "empty messages",
			messages:         []FullMessage{},
			expectedCount:    0,
			firstExchangeLen: 0,
		},
		{
			name: "single user-agent exchange",
			messages: []FullMessage{
				{
					Message: &Message{ID: "msg_1", Role: RoleUser, Time: MessageTime{Created: "2025-01-01T10:00:00Z"}},
					Parts:   []Part{{ID: "prt_1", Type: PartTypeText, Text: &userText}},
				},
				{
					Message: &Message{ID: "msg_2", Role: RoleAssistant, Time: MessageTime{Created: "2025-01-01T10:00:01Z"}},
					Parts:   []Part{{ID: "prt_2", Type: PartTypeText, Text: &agentText}},
				},
			},
			expectedCount:    1,
			firstExchangeLen: 2,
		},
		{
			name: "multiple exchanges",
			messages: []FullMessage{
				{
					Message: &Message{ID: "msg_1", Role: RoleUser, Time: MessageTime{Created: "2025-01-01T10:00:00Z"}},
					Parts:   []Part{{ID: "prt_1", Type: PartTypeText, Text: &userText}},
				},
				{
					Message: &Message{ID: "msg_2", Role: RoleAssistant, Time: MessageTime{Created: "2025-01-01T10:00:01Z"}},
					Parts:   []Part{{ID: "prt_2", Type: PartTypeText, Text: &agentText}},
				},
				{
					Message: &Message{ID: "msg_3", Role: RoleUser, Time: MessageTime{Created: "2025-01-01T10:00:02Z"}},
					Parts:   []Part{{ID: "prt_3", Type: PartTypeText, Text: &userText2}},
				},
				{
					Message: &Message{ID: "msg_4", Role: RoleAssistant, Time: MessageTime{Created: "2025-01-01T10:00:03Z"}},
					Parts:   []Part{{ID: "prt_4", Type: PartTypeText, Text: &agentText2}},
				},
			},
			expectedCount:    2,
			firstExchangeLen: 2,
		},
		{
			name: "agent message first creates exchange",
			messages: []FullMessage{
				{
					Message: &Message{ID: "msg_1", Role: RoleAssistant, Time: MessageTime{Created: "2025-01-01T10:00:00Z"}},
					Parts:   []Part{{ID: "prt_1", Type: PartTypeText, Text: &agentText}},
				},
			},
			expectedCount:    1,
			firstExchangeLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exchanges, err := buildExchangesFromMessages(tt.messages, "/workspace")
			if err != nil {
				t.Fatalf("buildExchangesFromMessages returned error: %v", err)
			}

			if len(exchanges) != tt.expectedCount {
				t.Fatalf("expected %d exchanges, got %d", tt.expectedCount, len(exchanges))
			}

			if tt.expectedCount > 0 && len(exchanges[0].Messages) != tt.firstExchangeLen {
				t.Errorf("first exchange has %d messages, want %d", len(exchanges[0].Messages), tt.firstExchangeLen)
			}
		})
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		workspaceRoot string
		expected      string
	}{
		{
			name:          "absolute path inside workspace",
			path:          "/workspace/src/main.go",
			workspaceRoot: "/workspace",
			expected:      "src/main.go",
		},
		{
			name:          "absolute path outside workspace",
			path:          "/etc/passwd",
			workspaceRoot: "/workspace",
			expected:      "/etc/passwd",
		},
		{
			name:          "relative path",
			path:          "relative/path.txt",
			workspaceRoot: "/workspace",
			expected:      "relative/path.txt",
		},
		{
			name:          "empty workspace root",
			path:          "/absolute/path.txt",
			workspaceRoot: "",
			expected:      "/absolute/path.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePath(tt.path, tt.workspaceRoot)
			if got != tt.expected {
				t.Errorf("normalizePath(%q, %q) = %q, want %q", tt.path, tt.workspaceRoot, got, tt.expected)
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	tests := []struct {
		slice    []string
		value    string
		expected bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{}, "a", false},
		{nil, "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got := containsString(tt.slice, tt.value)
			if got != tt.expected {
				t.Errorf("containsString(%v, %q) = %v, want %v", tt.slice, tt.value, got, tt.expected)
			}
		})
	}
}
