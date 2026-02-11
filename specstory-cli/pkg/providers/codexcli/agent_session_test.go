package codexcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/xeipuuv/gojsonschema"
)

// getSchemaPath returns the absolute path to the agent session schema
func getSchemaPath() string {
	// Get the directory of this test file
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)

	// Navigate to the schema file: pkg/providers/codexcli -> pkg/spi/schema
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

// TestGetIntFromMap tests the getIntFromMap helper function
func TestGetIntFromMap(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]interface{}
		key      string
		expected int
	}{
		{
			name:     "nil map",
			m:        nil,
			key:      "key",
			expected: 0,
		},
		{
			name:     "missing key",
			m:        map[string]interface{}{"other": 123},
			key:      "key",
			expected: 0,
		},
		{
			name:     "float64 value (JSON default)",
			m:        map[string]interface{}{"key": float64(42)},
			key:      "key",
			expected: 42,
		},
		{
			name:     "int64 value",
			m:        map[string]interface{}{"key": int64(99)},
			key:      "key",
			expected: 99,
		},
		{
			name:     "int value",
			m:        map[string]interface{}{"key": 123},
			key:      "key",
			expected: 123,
		},
		{
			name:     "string value (wrong type)",
			m:        map[string]interface{}{"key": "not a number"},
			key:      "key",
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

// TestExtractUsageFromTokenCount tests the extractUsageFromTokenCount function
func TestExtractUsageFromTokenCount(t *testing.T) {
	tests := []struct {
		name           string
		payload        map[string]interface{}
		expectNil      bool
		expectedInput  int
		expectedOutput int
		expectedCached int
	}{
		{
			name:      "nil payload",
			payload:   nil,
			expectNil: true,
		},
		{
			name:      "missing info",
			payload:   map[string]interface{}{"type": "token_count"},
			expectNil: true,
		},
		{
			name: "missing last_token_usage",
			payload: map[string]interface{}{
				"info": map[string]interface{}{
					"total_token_usage": map[string]interface{}{
						"input_tokens":  float64(100),
						"output_tokens": float64(50),
					},
				},
			},
			expectNil: true,
		},
		{
			name: "valid token_count event",
			payload: map[string]interface{}{
				"type": "token_count",
				"info": map[string]interface{}{
					"last_token_usage": map[string]interface{}{
						"input_tokens":            float64(100),
						"cached_input_tokens":     float64(20),
						"output_tokens":           float64(50),
						"reasoning_output_tokens": float64(10),
						"total_tokens":            float64(180),
					},
					"total_token_usage": map[string]interface{}{
						"input_tokens":  float64(500),
						"output_tokens": float64(200),
					},
				},
			},
			expectNil:      false,
			expectedInput:  100,
			expectedOutput: 50,
			expectedCached: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractUsageFromTokenCount(tt.payload)
			if tt.expectNil {
				if result != nil {
					t.Errorf("extractUsageFromTokenCount() = %+v, want nil", result)
				}
				return
			}
			if result == nil {
				t.Fatal("extractUsageFromTokenCount() = nil, want non-nil")
			}
			if result.InputTokens != tt.expectedInput {
				t.Errorf("InputTokens = %d, want %d", result.InputTokens, tt.expectedInput)
			}
			if result.OutputTokens != tt.expectedOutput {
				t.Errorf("OutputTokens = %d, want %d", result.OutputTokens, tt.expectedOutput)
			}
			if result.CachedInputTokens != tt.expectedCached {
				t.Errorf("CachedInputTokens = %d, want %d", result.CachedInputTokens, tt.expectedCached)
			}
		})
	}
}

// TestGenerateAgentSession_WithTokenUsage tests that token_count events are processed
func TestGenerateAgentSession_WithTokenUsage(t *testing.T) {
	// Create sample records including a token_count event
	records := []map[string]interface{}{
		{
			"type":      "session_meta",
			"timestamp": "2025-11-16T00:00:00Z",
			"payload": map[string]interface{}{
				"id":        "test-session-123",
				"timestamp": "2025-11-16T00:00:00Z",
				"cwd":       "/test/workspace",
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2025-11-16T00:00:01Z",
			"payload": map[string]interface{}{
				"type":    "user_message",
				"message": "Hello, what's the weather?",
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2025-11-16T00:00:02Z",
			"payload": map[string]interface{}{
				"type":    "agent_message",
				"message": "I don't have access to weather data.",
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2025-11-16T00:00:03Z",
			"payload": map[string]interface{}{
				"type": "token_count",
				"info": map[string]interface{}{
					"last_token_usage": map[string]interface{}{
						"input_tokens":            float64(150),
						"cached_input_tokens":     float64(30),
						"output_tokens":           float64(75),
						"reasoning_output_tokens": float64(0),
						"total_tokens":            float64(255),
					},
					"total_token_usage": map[string]interface{}{
						"input_tokens":  float64(150),
						"output_tokens": float64(75),
					},
					"model_context_window": float64(128000),
				},
			},
		},
	}

	// Generate agent session
	session, err := GenerateAgentSession(records, "/test/workspace")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	// Validate we have one exchange
	if len(session.Exchanges) != 1 {
		t.Fatalf("Expected 1 exchange, got %d", len(session.Exchanges))
	}

	// Find the agent message and check it has usage attached
	exchange := session.Exchanges[0]
	var agentMsg *Message
	for i := range exchange.Messages {
		if exchange.Messages[i].Role == "agent" {
			agentMsg = &exchange.Messages[i]
			break
		}
	}

	if agentMsg == nil {
		t.Fatal("No agent message found")
	}

	if agentMsg.Usage == nil {
		t.Fatal("Agent message should have usage attached")
	}

	if agentMsg.Usage.InputTokens != 150 {
		t.Errorf("InputTokens = %d, want 150", agentMsg.Usage.InputTokens)
	}
	if agentMsg.Usage.OutputTokens != 75 {
		t.Errorf("OutputTokens = %d, want 75", agentMsg.Usage.OutputTokens)
	}
	if agentMsg.Usage.CachedInputTokens != 30 {
		t.Errorf("CachedInputTokens = %d, want 30", agentMsg.Usage.CachedInputTokens)
	}
}

// TestAgentSessionTypes validates that our type definitions are correct
func TestAgentSessionTypes(t *testing.T) {
	// Create a minimal valid session data for Codex
	session := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "codex-cli",
			Name:    "Codex CLI",
			Version: "unknown",
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
								Text: "Run ls command",
							},
						},
					},
					{
						ID:        "t1",
						Timestamp: "2025-11-16T00:00:05Z",
						Role:      "agent",
						Model:     "gpt-5-codex",
						Tool: &ToolInfo{
							Name:  "shell",
							Type:  "shell",
							UseID: "call_123",
							Input: map[string]interface{}{
								"command": []string{"ls", "-la"},
							},
							Output: map[string]interface{}{
								"output": "total 8\ndrwxr-xr-x  3 user  staff   96 Nov 16 10:00 .\ndrwxr-xr-x  5 user  staff  160 Nov 16 09:00 ..",
								"metadata": map[string]interface{}{
									"exit_code": 0,
								},
							},
						},
						PathHints: []string{},
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
