package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xeipuuv/gojsonschema"
)

// getSchemaPath returns the absolute path to the agent session schema
func getSchemaPath() string {
	// Get the directory of this test file
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)

	// Navigate to the schema file: pkg/providers/claudecode -> pkg/spi/schema
	schemaPath := filepath.Join(testDir, "..", "..", "spi", "schema", "session-data-v1.json")
	return schemaPath
}

// loadSchemaJSON loads the schema JSON from disk
func loadSchemaJSON() ([]byte, error) {
	return os.ReadFile(getSchemaPath())
}

// validateJSONDocument validates the JSON document against the schema using xeipuuv/gojsonschema
func validateJSONDocument(t *testing.T, jsonData []byte) error {
	// Load the schema
	agentSessionSchemaJSON, err := loadSchemaJSON()
	if err != nil {
		return fmt.Errorf("failed to load schema: %w", err)
	}

	// Create schema loader from schema
	schemaLoader := gojsonschema.NewBytesLoader(agentSessionSchemaJSON)

	// Create document loader from generated JSON
	documentLoader := gojsonschema.NewBytesLoader(jsonData)

	// Validate
	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return fmt.Errorf("validation error: %w", err)
	}

	if result.Valid() {
		t.Log("  The document is valid")
		return nil
	}

	// Document is not valid, report errors
	t.Log("  The document is NOT valid. Errors:")
	for i, desc := range result.Errors() {
		t.Logf("    %d. %s", i+1, desc)
	}

	return fmt.Errorf("document failed schema validation with %d error(s)", len(result.Errors()))
}

// TestAgentSessionTypes validates that our type definitions are correct
func TestAgentSessionTypes(t *testing.T) {
	// Create a minimal valid session data
	session := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "claude-code",
			Name:    "Claude Code",
			Version: "1.0.0",
		},
		SessionID:     "test-session",
		CreatedAt:     "2025-11-16T00:00:00Z",
		WorkspaceRoot: "/test",
		Exchanges: []Exchange{
			{
				StartTime: "2025-11-16T00:00:00Z",
				EndTime:   "2025-11-16T00:00:10Z",
				Messages: []Message{
					{
						ID:        "u1",
						Timestamp: "2025-11-16T00:00:00Z",
						Role:      "user",
						Content: []ContentPart{
							{
								Type: "text",
								Text: "Hello world",
							},
						},
					},
					{
						ID:        "a1",
						Timestamp: "2025-11-16T00:00:05Z",
						Role:      "agent",
						Model:     "claude-sonnet-4-5-20250929",
						Content: []ContentPart{
							{
								Type: "text",
								Text: "Creating a file...",
							},
						},
						Tool: &ToolInfo{
							Name:  "Write",
							Type:  "write",
							UseID: "tool_1",
							Input: map[string]interface{}{
								"file_path": "test.txt",
							},
						},
						PathHints: []string{"test.txt"},
					},
				},
			},
		},
	}

	// Serialize to JSON
	jsonData, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal minimal session: %v", err)
	}

	// Validate against schema
	if err := validateJSONDocument(t, jsonData); err != nil {
		t.Errorf("Minimal session validation failed: %v", err)
	} else {
		t.Log("✓ Minimal session validation passed")
	}
}

func TestClassifyToolType(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected string
	}{
		// Write tools
		{"Write tool", "Write", "write"},
		{"Edit tool", "Edit", "write"},
		{"write lowercase", "write", "write"},

		// Read tools
		{"Read tool", "Read", "read"},
		{"WebFetch tool", "WebFetch", "read"},

		// Search tools
		{"Grep tool", "Grep", "search"},
		{"Glob tool", "Glob", "search"},
		{"WebSearch tool", "WebSearch", "search"},

		// Shell tools
		{"Bash tool", "Bash", "shell"},
		{"TaskOutput tool", "TaskOutput", "shell"},
		{"KillShell tool", "KillShell", "shell"},

		// Task tools
		{"NotebookEdit tool", "NotebookEdit", "task"},
		{"TodoWrite tool", "TodoWrite", "task"},

		// Generic tools
		{"EnterPlanMode tool", "EnterPlanMode", "generic"},
		{"ExitPlanMode tool", "ExitPlanMode", "generic"},
		{"AskUserQuestion tool", "AskUserQuestion", "generic"},
		{"Skill tool", "Skill", "generic"},
		{"LSP tool", "LSP", "generic"},
		{"Task tool", "Task", "generic"},

		// Unknown tools
		{"Unknown tool", "SomethingElse", "unknown"},
		{"Empty string", "", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyToolType(tt.toolName)
			if result != tt.expected {
				t.Errorf("classifyToolType(%q) = %q, want %q", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestFormatToolAsMarkdown_AskUserQuestion(t *testing.T) {
	tests := []struct {
		name        string
		tool        *ToolInfo
		contains    []string
		notContains []string
	}{
		{
			name: "Parseable answer formats nicely",
			tool: &ToolInfo{
				Name:  "AskUserQuestion",
				Input: map[string]interface{}{},
				Output: map[string]interface{}{
					"content": `User has answered your questions: "What color?"="Blue". You can now continue with the user's answers in mind.`,
				},
			},
			contains:    []string{"**Answer:** Blue"},
			notContains: []string{"```"},
		},
		{
			name: "Unparseable content falls back to code block",
			tool: &ToolInfo{
				Name:  "AskUserQuestion",
				Input: map[string]interface{}{},
				Output: map[string]interface{}{
					"content": "Some unexpected format that cannot be parsed",
				},
			},
			contains:    []string{"```", "Some unexpected format that cannot be parsed"},
			notContains: []string{"**Answer:**"},
		},
		{
			name: "Empty content produces no output",
			tool: &ToolInfo{
				Name:  "AskUserQuestion",
				Input: map[string]interface{}{},
				Output: map[string]interface{}{
					"content": "",
				},
			},
			contains:    []string{},
			notContains: []string{"**Answer:**", "```"},
		},
		{
			name: "Whitespace-only content produces no output",
			tool: &ToolInfo{
				Name:  "AskUserQuestion",
				Input: map[string]interface{}{},
				Output: map[string]interface{}{
					"content": "   \t  ",
				},
			},
			contains:    []string{},
			notContains: []string{"**Answer:**", "```"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolAsMarkdown(tt.tool, "/workspace")
			for _, substr := range tt.contains {
				if !strings.Contains(result, substr) {
					t.Errorf("formatToolAsMarkdown() result doesn't contain %q\nGot: %q", substr, result)
				}
			}
			for _, substr := range tt.notContains {
				if strings.Contains(result, substr) {
					t.Errorf("formatToolAsMarkdown() result should not contain %q\nGot: %q", substr, result)
				}
			}
		})
	}
}

func TestGetIntFromMap(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected int
	}{
		{
			name:     "float64 value (JSON default)",
			m:        map[string]interface{}{"tokens": float64(1234)},
			key:      "tokens",
			expected: 1234,
		},
		{
			name:     "int value",
			m:        map[string]interface{}{"tokens": 5678},
			key:      "tokens",
			expected: 5678,
		},
		{
			name:     "int64 value",
			m:        map[string]interface{}{"tokens": int64(9999)},
			key:      "tokens",
			expected: 9999,
		},
		{
			name:     "missing key returns 0",
			m:        map[string]interface{}{"other": float64(100)},
			key:      "tokens",
			expected: 0,
		},
		{
			name:     "nil map returns 0",
			m:        nil,
			key:      "tokens",
			expected: 0,
		},
		{
			name:     "wrong type returns 0",
			m:        map[string]interface{}{"tokens": "not a number"},
			key:      "tokens",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIntFromMap(tt.m, tt.key)
			if result != tt.expected {
				t.Errorf("getIntFromMap() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestBuildAgentMessage_WithUsage(t *testing.T) {
	// Simulate a JSONL record with usage data (as it would be parsed from JSON)
	// JSONLRecord.Data contains the raw parsed JSON with type, uuid, timestamp, and message fields
	record := JSONLRecord{
		Data: map[string]interface{}{
			"type":      "assistant",
			"uuid":      "test-uuid-123",
			"timestamp": "2025-01-15T10:00:00Z",
			"message": map[string]interface{}{
				"model": "claude-opus-4-5-20251101",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Hello, I can help with that.",
					},
				},
				"usage": map[string]interface{}{
					"input_tokens":                float64(3660),
					"output_tokens":               float64(150),
					"cache_creation_input_tokens": float64(5562),
					"cache_read_input_tokens":     float64(19345),
				},
			},
		},
	}

	msg := buildAgentMessage(record, "/workspace", false)

	// Verify usage was extracted
	if msg.Usage == nil {
		t.Fatal("Expected Usage to be populated, got nil")
	}

	if msg.Usage.InputTokens != 3660 {
		t.Errorf("InputTokens = %d, want 3660", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 150 {
		t.Errorf("OutputTokens = %d, want 150", msg.Usage.OutputTokens)
	}
	if msg.Usage.CacheCreationInputTokens != 5562 {
		t.Errorf("CacheCreationInputTokens = %d, want 5562", msg.Usage.CacheCreationInputTokens)
	}
	if msg.Usage.CacheReadInputTokens != 19345 {
		t.Errorf("CacheReadInputTokens = %d, want 19345", msg.Usage.CacheReadInputTokens)
	}
}

func TestBuildAgentMessage_WithoutUsage(t *testing.T) {
	// Simulate a JSONL record without usage data
	record := JSONLRecord{
		Data: map[string]interface{}{
			"type":      "assistant",
			"uuid":      "test-uuid-456",
			"timestamp": "2025-01-15T10:00:00Z",
			"message": map[string]interface{}{
				"model": "claude-opus-4-5-20251101",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "No usage data here.",
					},
				},
			},
		},
	}

	msg := buildAgentMessage(record, "/workspace", false)

	// Verify usage is nil when not present
	if msg.Usage != nil {
		t.Errorf("Expected Usage to be nil, got %+v", msg.Usage)
	}
}
