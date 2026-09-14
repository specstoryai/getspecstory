package codexcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// Codex 0.147's TUI records the conversation as thread items. A session written
// that way has to yield the same shape as the older event stream, and — because
// the item stream carries tool items alongside the response_item records that
// have always supplied tool calls — must not render a tool twice.
func TestGenerateAgentSession_ThreadItemStream(t *testing.T) {
	itemEvent := func(ts string, item map[string]interface{}) map[string]interface{} {
		return map[string]interface{}{
			"type":      "event_msg",
			"timestamp": ts,
			"payload":   map[string]interface{}{"type": "item_completed", "item": item},
		}
	}

	records := []map[string]interface{}{
		{
			"type":      "session_meta",
			"timestamp": "2026-08-17T20:47:00Z",
			"payload": map[string]interface{}{
				"id":        "01a0117a",
				"timestamp": "2026-08-17T20:47:00Z",
				"cwd":       "/test/workspace",
			},
		},
		itemEvent("2026-08-17T20:47:01Z", map[string]interface{}{
			"type":    "UserMessage",
			"content": []interface{}{map[string]interface{}{"type": "text", "text": "How many bits in a word?"}},
		}),
		// The preamble the agent prints before acting. The older stream emitted
		// it as agent_message, so it has to survive here too.
		itemEvent("2026-08-17T20:47:02Z", map[string]interface{}{
			"type":    "AgentMessage",
			"phase":   "commentary",
			"content": []interface{}{map[string]interface{}{"type": "Text", "text": "I'll check."}},
		}),
		// A tool item. Tool calls come from the response_item below, so this one
		// must be dropped rather than rendered as a second copy.
		itemEvent("2026-08-17T20:47:03Z", map[string]interface{}{
			"type":    "CommandExecution",
			"command": "ls -a",
			"stdout":  "hello_word.c",
		}),
		{
			"type":      "response_item",
			"timestamp": "2026-08-17T20:47:03Z",
			"payload": map[string]interface{}{
				"type":    "custom_tool_call",
				"name":    "exec",
				"call_id": "call-1",
				"input":   "ls -a",
			},
		},
		itemEvent("2026-08-17T20:47:04Z", map[string]interface{}{
			"type":    "AgentMessage",
			"phase":   "final_answer",
			"content": []interface{}{map[string]interface{}{"type": "Text", "text": "64 bits."}},
		}),
	}

	sessionData, err := GenerateAgentSession(records, "/test/workspace")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}
	if len(sessionData.Exchanges) != 1 {
		t.Fatalf("got %d exchanges, want 1", len(sessionData.Exchanges))
	}

	var roles []string
	var texts []string
	tools := 0
	for _, msg := range sessionData.Exchanges[0].Messages {
		roles = append(roles, msg.Role)
		if msg.Tool != nil {
			tools++
		}
		for _, part := range msg.Content {
			if part.Text != "" {
				texts = append(texts, part.Text)
			}
		}
	}

	wantTexts := []string{"How many bits in a word?", "I'll check.", "64 bits."}
	for _, want := range wantTexts {
		if !slices.Contains(texts, want) {
			t.Errorf("missing message text %q; got %v", want, texts)
		}
	}
	if roles[0] != "user" {
		t.Errorf("first message role = %q, want user", roles[0])
	}
	// One tool message, from the response_item — not two.
	if tools != 1 {
		t.Errorf("rendered %d tool messages, want exactly 1 (the CommandExecution item must be dropped)", tools)
	}
}

func TestCodexItemAsLegacyEvent(t *testing.T) {
	tests := []struct {
		name         string
		item         map[string]interface{}
		expectedType string
		expectedText string
	}{
		{
			name: "user message",
			item: map[string]interface{}{
				"type":    "UserMessage",
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "hello"}},
			},
			expectedType: "user_message",
			expectedText: "hello",
		},
		{
			name: "agent commentary is kept, not just the final answer",
			item: map[string]interface{}{
				"type":    "AgentMessage",
				"phase":   "commentary",
				"content": []interface{}{map[string]interface{}{"type": "Text", "text": "working on it"}},
			},
			expectedType: "agent_message",
			expectedText: "working on it",
		},
		{
			name: "multi-part content is joined",
			item: map[string]interface{}{
				"type": "AgentMessage",
				"content": []interface{}{
					map[string]interface{}{"type": "Text", "text": "one "},
					map[string]interface{}{"type": "Text", "text": "two"},
				},
			},
			expectedType: "agent_message",
			expectedText: "one two",
		},
		{
			name: "reasoning from summary_text strings",
			item: map[string]interface{}{
				"type":         "Reasoning",
				"summary_text": []interface{}{"first ", "second"},
			},
			expectedType: "agent_reasoning",
			expectedText: "first second",
		},
		{
			name: "reasoning from summary_text objects",
			item: map[string]interface{}{
				"type":         "Reasoning",
				"summary_text": []interface{}{map[string]interface{}{"text": "thinking"}},
			},
			expectedType: "agent_reasoning",
			expectedText: "thinking",
		},
		{
			name: "reasoning falls back to raw_content",
			item: map[string]interface{}{
				"type":         "Reasoning",
				"summary_text": []interface{}{},
				"raw_content":  []interface{}{"raw"},
			},
			expectedType: "agent_reasoning",
			expectedText: "raw",
		},
		{
			// Tool items are the response_item stream's job; translating them
			// here would render every tool call twice.
			name:         "tool item is not translated",
			item:         map[string]interface{}{"type": "CommandExecution", "command": "ls"},
			expectedType: "",
		},
		{
			name:         "unknown item type is not translated",
			item:         map[string]interface{}{"type": "SomethingCodexAddedLater"},
			expectedType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payloadType, payload := codexItemAsLegacyEvent(map[string]interface{}{
				"type": "item_completed",
				"item": tt.item,
			})

			if payloadType != tt.expectedType {
				t.Fatalf("payload type = %q, want %q", payloadType, tt.expectedType)
			}
			if tt.expectedType == "" {
				return
			}

			field := "message"
			if tt.expectedType == "agent_reasoning" {
				field = "text"
			}
			if got, _ := payload[field].(string); got != tt.expectedText {
				t.Errorf("%s = %q, want %q", field, got, tt.expectedText)
			}
		})
	}
}

// The header scan screens lines cheaply before parsing; it has to recognise a
// first prompt in either shape, and still reject a line that merely mentions one.
func TestCodexUserMessageText_BothShapes(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name:     "legacy user_message event",
			line:     `{"type":"event_msg","payload":{"type":"user_message","message":"legacy prompt"}}`,
			expected: "legacy prompt",
		},
		{
			name:     "thread item user message",
			line:     `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"item prompt"}]}}}`,
			expected: "item prompt",
		},
		{
			name:     "agent item is not a prompt",
			line:     `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","content":[{"type":"Text","text":"reply"}]}}}`,
			expected: "",
		},
		{
			// The marker screen matches on raw bytes, so an injected context
			// record quoting the marker still has to be rejected here.
			name:     "record merely containing the marker text",
			line:     `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"the \"UserMessage\" type"}]}}`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexUserMessageText(tt.line); got != tt.expected {
				t.Errorf("codexUserMessageText() = %q, want %q", got, tt.expected)
			}
		})
	}
}
