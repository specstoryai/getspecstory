package cursorcli

import (
	"encoding/json"
	"testing"
)

// TestGenerateAgentSession_Basic tests basic agent session generation from blob records
func TestGenerateAgentSession_Basic(t *testing.T) {
	// Create sample blob records representing a simple conversation
	blobRecords := []BlobRecord{
		// User message
		{
			RowID: 1,
			ID:    "blob1",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [
					{
						"type": "text",
						"text": "Hello, can you help me?"
					}
				]
			}`),
		},
		// Agent response
		{
			RowID: 2,
			ID:    "blob2",
			Data: json.RawMessage(`{
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "Of course! What do you need help with?"
					}
				]
			}`),
		},
	}

	sessionID := "test-session-123"
	createdAt := "2025-01-15T10:30:00Z"
	slug := "hello-can-you-help"
	workspaceRoot := "/test/workspace"

	// Generate agent session
	agentSession, err := GenerateAgentSession(blobRecords, workspaceRoot, sessionID, createdAt, slug)
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// Validate basic fields
	if agentSession.SchemaVersion != "1.0" {
		t.Errorf("Expected schema version 1.0, got %s", agentSession.SchemaVersion)
	}

	if agentSession.Provider.ID != "cursor" {
		t.Errorf("Expected provider ID 'cursor', got %s", agentSession.Provider.ID)
	}

	if agentSession.SessionID != sessionID {
		t.Errorf("Expected session ID %s, got %s", sessionID, agentSession.SessionID)
	}

	if agentSession.CreatedAt != createdAt {
		t.Errorf("Expected created at %s, got %s", createdAt, agentSession.CreatedAt)
	}

	if agentSession.Slug != slug {
		t.Errorf("Expected slug %s, got %s", slug, agentSession.Slug)
	}

	if agentSession.WorkspaceRoot != workspaceRoot {
		t.Errorf("Expected workspace root %s, got %s", workspaceRoot, agentSession.WorkspaceRoot)
	}

	// Validate exchanges
	if len(agentSession.Exchanges) != 1 {
		t.Fatalf("Expected 1 exchange, got %d", len(agentSession.Exchanges))
	}

	exchange := agentSession.Exchanges[0]
	if len(exchange.Messages) != 2 {
		t.Fatalf("Expected 2 messages in exchange, got %d", len(exchange.Messages))
	}

	// Validate user message
	userMsg := exchange.Messages[0]
	if userMsg.Role != "user" {
		t.Errorf("Expected user role, got %s", userMsg.Role)
	}
	if len(userMsg.Content) != 1 {
		t.Fatalf("Expected 1 content part in user message, got %d", len(userMsg.Content))
	}
	if userMsg.Content[0].Type != "text" {
		t.Errorf("Expected text content type, got %s", userMsg.Content[0].Type)
	}
	if userMsg.Content[0].Text != "Hello, can you help me?" {
		t.Errorf("Unexpected user message text: %s", userMsg.Content[0].Text)
	}

	// Validate agent message
	agentMsg := exchange.Messages[1]
	if agentMsg.Role != "agent" {
		t.Errorf("Expected agent role, got %s", agentMsg.Role)
	}
	if len(agentMsg.Content) != 1 {
		t.Fatalf("Expected 1 content part in agent message, got %d", len(agentMsg.Content))
	}
	if agentMsg.Content[0].Text != "Of course! What do you need help with?" {
		t.Errorf("Unexpected agent message text: %s", agentMsg.Content[0].Text)
	}
}

// TestGenerateAgentSession_WithTools tests agent session generation with tool use
func TestGenerateAgentSession_WithTools(t *testing.T) {
	blobRecords := []BlobRecord{
		// User message
		{
			RowID: 1,
			ID:    "blob1",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [
					{
						"type": "text",
						"text": "Create a test file"
					}
				]
			}`),
		},
		// Agent with tool call
		{
			RowID: 2,
			ID:    "blob2",
			Data: json.RawMessage(`{
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "I'll create that file for you."
					},
					{
						"type": "tool-call",
						"toolName": "Write",
						"toolCallId": "call-123",
						"args": {
							"path": "test.txt",
							"contents": "Hello World"
						}
					}
				]
			}`),
		},
		// Tool result
		{
			RowID: 3,
			ID:    "blob3",
			Data: json.RawMessage(`{
				"role": "tool",
				"content": [
					{
						"type": "tool-result",
						"toolName": "Write",
						"toolCallId": "call-123",
						"result": "File created successfully"
					}
				]
			}`),
		},
	}

	agentSession, err := GenerateAgentSession(blobRecords, "/test", "session-1", "2025-01-15T10:00:00Z", "test")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// Should have 1 exchange
	if len(agentSession.Exchanges) != 1 {
		t.Fatalf("Expected 1 exchange, got %d", len(agentSession.Exchanges))
	}

	exchange := agentSession.Exchanges[0]
	// Should have 3 messages (user + agent text + agent tool)
	if len(exchange.Messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(exchange.Messages))
	}

	// Check first agent message (text)
	agentTextMsg := exchange.Messages[1]
	if agentTextMsg.Role != "agent" {
		t.Errorf("Expected agent role, got %s", agentTextMsg.Role)
	}
	if len(agentTextMsg.Content) != 1 {
		t.Fatalf("Expected 1 content part, got %d", len(agentTextMsg.Content))
	}
	if agentTextMsg.Content[0].Text != "I'll create that file for you." {
		t.Errorf("Unexpected text: %s", agentTextMsg.Content[0].Text)
	}

	// Check second agent message (tool)
	agentMsg := exchange.Messages[2]
	if agentMsg.Tool == nil {
		t.Fatal("Expected agent message to have tool info")
	}

	if agentMsg.Tool.Name != "Write" {
		t.Errorf("Expected tool name 'Write', got %s", agentMsg.Tool.Name)
	}

	if agentMsg.Tool.Type != "write" {
		t.Errorf("Expected tool type 'write', got %s", agentMsg.Tool.Type)
	}

	if agentMsg.Tool.UseID != "call-123" {
		t.Errorf("Expected tool use ID 'call-123', got %s", agentMsg.Tool.UseID)
	}

	// Check tool input
	if agentMsg.Tool.Input == nil {
		t.Fatal("Expected tool to have input")
	}

	path, ok := agentMsg.Tool.Input["path"].(string)
	if !ok || path != "test.txt" {
		t.Errorf("Expected tool input path 'test.txt', got %v", path)
	}

	// Check tool output (should be merged from tool result)
	if agentMsg.Tool.Output == nil {
		t.Fatal("Expected tool to have output")
	}

	content, ok := agentMsg.Tool.Output["content"].(string)
	if !ok || content != "File created successfully" {
		t.Errorf("Expected tool output content 'File created successfully', got %v", content)
	}

	// Check path hints
	if len(agentMsg.PathHints) != 1 {
		t.Fatalf("Expected 1 path hint, got %d", len(agentMsg.PathHints))
	}
	if agentMsg.PathHints[0] != "test.txt" {
		t.Errorf("Expected path hint 'test.txt', got %s", agentMsg.PathHints[0])
	}

	// Check FormattedMarkdown is populated
	if agentMsg.Tool.FormattedMarkdown == nil {
		t.Error("Expected FormattedMarkdown to be populated")
	}
}

// TestGenerateAgentSession_MultipleExchanges tests multiple conversation exchanges
func TestGenerateAgentSession_MultipleExchanges(t *testing.T) {
	blobRecords := []BlobRecord{
		// First exchange - user message
		{
			RowID: 1,
			ID:    "blob1",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [{"type": "text", "text": "First question"}]
			}`),
		},
		// First exchange - agent response
		{
			RowID: 2,
			ID:    "blob2",
			Data: json.RawMessage(`{
				"role": "assistant",
				"content": [{"type": "text", "text": "First answer"}]
			}`),
		},
		// Second exchange - user message
		{
			RowID: 3,
			ID:    "blob3",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [{"type": "text", "text": "Second question"}]
			}`),
		},
		// Second exchange - agent response
		{
			RowID: 4,
			ID:    "blob4",
			Data: json.RawMessage(`{
				"role": "assistant",
				"content": [{"type": "text", "text": "Second answer"}]
			}`),
		},
	}

	agentSession, err := GenerateAgentSession(blobRecords, "/test", "session-1", "2025-01-15T10:00:00Z", "test")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// Should have 2 exchanges
	if len(agentSession.Exchanges) != 2 {
		t.Fatalf("Expected 2 exchanges, got %d", len(agentSession.Exchanges))
	}

	// Each exchange should have 2 messages (user + agent)
	for i, exchange := range agentSession.Exchanges {
		if len(exchange.Messages) != 2 {
			t.Errorf("Exchange %d: expected 2 messages, got %d", i, len(exchange.Messages))
		}

		// First message should be user
		if exchange.Messages[0].Role != "user" {
			t.Errorf("Exchange %d: expected first message to be user, got %s", i, exchange.Messages[0].Role)
		}

		// Second message should be agent
		if exchange.Messages[1].Role != "agent" {
			t.Errorf("Exchange %d: expected second message to be agent, got %s", i, exchange.Messages[1].Role)
		}

		// Exchange should have an ID
		if exchange.ExchangeID == "" {
			t.Errorf("Exchange %d: expected exchange ID to be set", i)
		}
	}
}

// TestClassifyCursorToolType tests tool type classification
func TestClassifyCursorToolType(t *testing.T) {
	tests := []struct {
		toolName     string
		expectedType string
	}{
		{"Write", "write"},
		{"StrReplace", "write"},
		{"MultiStrReplace", "write"},
		{"Delete", "write"},
		{"Read", "read"},
		{"Glob", "search"},
		{"LS", "shell"},
		{"Grep", "search"},
		{"Shell", "shell"},
		{"TodoWrite", "task"},
		{"UnknownTool", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := classifyCursorToolType(tt.toolName)
			if result != tt.expectedType {
				t.Errorf("classifyCursorToolType(%s) = %s, want %s", tt.toolName, result, tt.expectedType)
			}
		})
	}
}

// TestExtractCursorPathHints tests path hint extraction from tool inputs
func TestExtractCursorPathHints(t *testing.T) {
	tests := []struct {
		name          string
		input         map[string]interface{}
		workspaceRoot string
		expectedPaths []string
	}{
		{
			name: "Write tool with path",
			input: map[string]interface{}{
				"path":     "src/main.go",
				"contents": "package main",
			},
			workspaceRoot: "/workspace",
			expectedPaths: []string{"src/main.go"},
		},
		{
			name: "StrReplace with file_path",
			input: map[string]interface{}{
				"file_path":  "test.txt",
				"old_string": "old",
				"new_string": "new",
			},
			workspaceRoot: "/workspace",
			expectedPaths: []string{"test.txt"},
		},
		{
			name: "MultiStrReplace with paths array",
			input: map[string]interface{}{
				"paths": []interface{}{
					map[string]interface{}{"file_path": "file1.txt"},
					map[string]interface{}{"file_path": "file2.txt"},
				},
			},
			workspaceRoot: "/workspace",
			expectedPaths: []string{"file1.txt", "file2.txt"},
		},
		{
			name:          "Empty input",
			input:         map[string]interface{}{},
			workspaceRoot: "/workspace",
			expectedPaths: []string(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := extractCursorPathHints(tt.input, tt.workspaceRoot)

			if len(paths) != len(tt.expectedPaths) {
				t.Errorf("Expected %d paths, got %d", len(tt.expectedPaths), len(paths))
				return
			}

			for i, expectedPath := range tt.expectedPaths {
				if paths[i] != expectedPath {
					t.Errorf("Path %d: expected %s, got %s", i, expectedPath, paths[i])
				}
			}
		})
	}
}

// TestGenerateAgentSession_SkipUserInfo tests that <user_info> messages are skipped
func TestGenerateAgentSession_SkipUserInfo(t *testing.T) {
	blobRecords := []BlobRecord{
		// User info (should be skipped)
		{
			RowID: 1,
			ID:    "blob1",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [{"type": "text", "text": "<user_info>System info</user_info>"}]
			}`),
		},
		// Real user message
		{
			RowID: 2,
			ID:    "blob2",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [{"type": "text", "text": "Real question"}]
			}`),
		},
		// Agent response
		{
			RowID: 3,
			ID:    "blob3",
			Data: json.RawMessage(`{
				"role": "assistant",
				"content": [{"type": "text", "text": "Answer"}]
			}`),
		},
	}

	agentSession, err := GenerateAgentSession(blobRecords, "/test", "session-1", "2025-01-15T10:00:00Z", "test")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// Should have 1 exchange (user_info skipped)
	if len(agentSession.Exchanges) != 1 {
		t.Fatalf("Expected 1 exchange, got %d", len(agentSession.Exchanges))
	}

	// First message should be "Real question"
	firstMsg := agentSession.Exchanges[0].Messages[0]
	if firstMsg.Content[0].Text != "Real question" {
		t.Errorf("Expected 'Real question', got %s", firstMsg.Content[0].Text)
	}
}

// TestGenerateAgentSession_StripUserQueryTags tests user_query tag stripping
func TestGenerateAgentSession_StripUserQueryTags(t *testing.T) {
	blobRecords := []BlobRecord{
		{
			RowID: 1,
			ID:    "blob1",
			Data: json.RawMessage(`{
				"role": "user",
				"content": [{"type": "text", "text": "<user_query>\nHello\n</user_query>"}]
			}`),
		},
		{
			RowID: 2,
			ID:    "blob2",
			Data: json.RawMessage(`{
				"role": "assistant",
				"content": [{"type": "text", "text": "Hi"}]
			}`),
		},
	}

	agentSession, err := GenerateAgentSession(blobRecords, "/test", "session-1", "2025-01-15T10:00:00Z", "test")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// Tags should be stripped
	userMsg := agentSession.Exchanges[0].Messages[0]
	if userMsg.Content[0].Text != "Hello" {
		t.Errorf("Expected 'Hello', got %s", userMsg.Content[0].Text)
	}
}

func TestStripUserQueryTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Text with user_query tags and newlines",
			input:    "<user_query>\nHey Cursor! Create mastermind at the terminal in Ruby. Go!\n</user_query>",
			expected: "Hey Cursor! Create mastermind at the terminal in Ruby. Go!",
		},
		{
			name:     "Text with user_query tags without newlines",
			input:    "<user_query>Simple query here</user_query>",
			expected: "Simple query here",
		},
		{
			name:     "Text without tags should return unchanged",
			input:    "Just regular text without any tags",
			expected: "Just regular text without any tags",
		},
		{
			name:     "Text with only opening tag should return unchanged",
			input:    "<user_query>\nIncomplete tag structure",
			expected: "<user_query>\nIncomplete tag structure",
		},
		{
			name:     "Text with only closing tag should return unchanged",
			input:    "Incomplete tag structure\n</user_query>",
			expected: "Incomplete tag structure\n</user_query>",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Just the tags with no content",
			input:    "<user_query>\n</user_query>",
			expected: "",
		},
		{
			name:     "Tags with extra whitespace around content",
			input:    "<user_query>\n\nContent with blank line\n\n</user_query>",
			expected: "\nContent with blank line\n",
		},
		{
			name:     "Tags with leading and trailing whitespace",
			input:    "  <user_query>\nContent here\n</user_query>  ",
			expected: "Content here",
		},
		{
			name:     "Multiline content with tags",
			input:    "<user_query>\nLine 1\nLine 2\nLine 3\n</user_query>",
			expected: "Line 1\nLine 2\nLine 3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripUserQueryTags(tt.input)
			if result != tt.expected {
				t.Errorf("stripUserQueryTags() = %q, want %q", result, tt.expected)
			}
		})
	}
}
