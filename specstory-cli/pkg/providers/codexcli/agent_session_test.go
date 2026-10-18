package codexcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestParseToolOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   interface{}
		expected map[string]interface{}
	}{
		{
			name:     "Empty string records nothing",
			output:   "",
			expected: nil,
		},
		{
			name:     "Plain text string wrapped as raw",
			output:   "Exit code: 0\nOutput:\nhello",
			expected: map[string]interface{}{"raw": "Exit code: 0\nOutput:\nhello"},
		},
		{
			name:     "JSON object string parsed into map",
			output:   `{"output":"Success","metadata":{"exit_code":0}}`,
			expected: map[string]interface{}{"output": "Success", "metadata": map[string]interface{}{"exit_code": float64(0)}},
		},
		{
			name: "Content item array flattened to raw text",
			output: []interface{}{
				map[string]interface{}{"type": "input_text", "text": "Script completed\nOutput:\n"},
				map[string]interface{}{"type": "input_text", "text": "中文输出"},
			},
			expected: map[string]interface{}{"raw": "Script completed\nOutput:\n\n中文输出"},
		},
		{
			name: "Image items replaced with marker instead of base64",
			output: []interface{}{
				map[string]interface{}{"type": "input_text", "text": "Screenshot:"},
				map[string]interface{}{"type": "input_image", "image_url": "data:image/png;base64,iVBORw0KGgo"},
			},
			expected: map[string]interface{}{"raw": "Screenshot:\n[image]"},
		},
		{
			name:     "Empty array records nothing",
			output:   []interface{}{},
			expected: nil,
		},
		{
			name:     "Array without usable items records nothing",
			output:   []interface{}{"stray", map[string]interface{}{"type": "input_text", "text": ""}},
			expected: nil,
		},
		{
			name:     "Missing output records nothing",
			output:   nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseToolOutput(tt.output)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("parseToolOutput() = %#v, want %#v", result, tt.expected)
			}
		})
	}
}

func TestFormatToolWithSummary_OutputTruncation(t *testing.T) {
	const limit = 5000
	tests := []struct {
		name          string
		output        string
		wantTruncated bool
	}{
		{name: "ASCII under limit", output: strings.Repeat("a", limit-1), wantTruncated: false},
		{name: "ASCII at limit", output: strings.Repeat("a", limit), wantTruncated: false},
		{name: "ASCII over limit", output: strings.Repeat("a", limit+1), wantTruncated: true},
		{name: "Chinese under limit", output: strings.Repeat("中", limit-1), wantTruncated: false},
		{name: "Chinese at limit despite 15000 bytes", output: strings.Repeat("中", limit), wantTruncated: false},
		{name: "Chinese over limit", output: strings.Repeat("中", limit+1), wantTruncated: true},
		{name: "Emoji over limit", output: strings.Repeat("🎉", limit+1), wantTruncated: true},
		// Byte 5000 lands one byte into a three-byte character: the exact
		// case a byte-offset cut turns into invalid UTF-8.
		{name: "Character straddling byte 5000", output: "x" + strings.Repeat("中", 2000), wantTruncated: false},
		{name: "Mixed over limit", output: "x" + strings.Repeat("中", limit), wantTruncated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &ToolInfo{Name: "exec", Output: map[string]interface{}{"raw": tt.output}}
			_, md := formatToolWithSummary(tool, "/test/workspace")

			if !utf8.ValidString(md) {
				t.Fatalf("formatted markdown is invalid UTF-8")
			}
			truncated := strings.Contains(md, "(output truncated)")
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if !tt.wantTruncated && !strings.Contains(md, tt.output) {
				t.Errorf("untruncated output not rendered in full")
			}
			if tt.wantTruncated && !strings.Contains(md, string([]rune(tt.output)[:limit])) {
				t.Errorf("truncated output does not keep the first %d characters", limit)
			}
		})
	}
}

// TestGenerateAgentSession_ArrayToolOutput covers code-mode exec, whose output is an array
// of content items rather than a string. It used to be dropped from the transcript.
func TestGenerateAgentSession_ArrayToolOutput(t *testing.T) {
	records := []map[string]interface{}{
		{
			"type":      "session_meta",
			"timestamp": "2026-09-22T19:00:46Z",
			"payload": map[string]interface{}{
				"id":        "01a0ca7d",
				"timestamp": "2026-09-22T19:00:46Z",
				"cwd":       "/test/workspace",
			},
		},
		{
			"type":      "response_item",
			"timestamp": "2026-09-22T19:00:47Z",
			"payload": map[string]interface{}{
				"type":    "message",
				"role":    "user",
				"content": []interface{}{map[string]interface{}{"type": "input_text", "text": "Print some Chinese"}},
			},
		},
		{
			"type":      "response_item",
			"timestamp": "2026-09-22T19:00:48Z",
			"payload": map[string]interface{}{
				"type":    "custom_tool_call",
				"name":    "exec",
				"call_id": "call-1",
				"input":   `text(await tools.exec_command({cmd:"echo 中文"}));`,
			},
		},
		{
			"type":      "response_item",
			"timestamp": "2026-09-22T19:00:49Z",
			"payload": map[string]interface{}{
				"type":    "custom_tool_call_output",
				"call_id": "call-1",
				"output": []interface{}{
					map[string]interface{}{"type": "input_text", "text": "Script completed\nOutput:\n"},
					map[string]interface{}{"type": "input_text", "text": "中文"},
				},
			},
		},
	}

	sessionData, err := GenerateAgentSession(records, "/test/workspace")
	if err != nil {
		t.Fatalf("GenerateAgentSession failed: %v", err)
	}

	var tool *ToolInfo
	for _, exchange := range sessionData.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool != nil {
				tool = msg.Tool
			}
		}
	}
	if tool == nil {
		t.Fatal("exec tool call not found in session")
	}
	if tool.FormattedMarkdown == nil || !strings.Contains(*tool.FormattedMarkdown, "Script completed\nOutput:\n\n中文") {
		t.Errorf("exec output missing from formatted markdown: %v", tool.FormattedMarkdown)
	}
}

// TestGenerateAgentSession_StructuredToolOutputInMarkdown covers tool outputs that arrive
// as a JSON-object string (e.g. request_user_input answers). They parse into a map with no
// "raw" key, and used to render as an empty Result, silently losing the user's answers.
func TestGenerateAgentSession_StructuredToolOutputInMarkdown(t *testing.T) {
	colorQuestion := `{"id":"color","header":"Color","question":"Which color?","options":[{"label":"Blue","description":"Use blue"},{"label":"Green","description":"Use green"}]}`
	sizeQuestion := `{"id":"size","header":"Size","question":"Which size?","options":[{"label":"Small","description":"Compact"},{"label":"Large","description":"Roomy"}]}`

	tests := []struct {
		name        string
		toolName    string
		arguments   string
		output      string // empty means the call never received an output
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:      "Selected option and user note are rendered",
			toolName:  "request_user_input",
			arguments: `{"questions":[` + colorQuestion + `]}`,
			output:    `{"answers":{"color":{"answers":["Blue","user_note: SYNTHETIC_ANSWER_ONLY_MARKER"]}}}`,
			wantContain: []string{
				"Which color?", "Blue", "Use blue", "Green", "Use green",
				"SYNTHETIC_ANSWER_ONLY_MARKER",
			},
			// The Go map representation is what the generic fallback used to print
			wantAbsent: []string{"map["},
		},
		{
			name:      "Each question gets its own answer",
			toolName:  "request_user_input",
			arguments: `{"questions":[` + colorQuestion + `,` + sizeQuestion + `]}`,
			output:    `{"answers":{"color":{"answers":["Green"]},"size":{"answers":["Large"]}}}`,
			wantContain: []string{
				"Which color?", "Which size?",
				"**Answer:** Green", "**Answer:** Large",
			},
		},
		{
			name:        "Cancelled question is marked as unanswered",
			toolName:    "request_user_input",
			arguments:   `{"questions":[` + colorQuestion + `]}`,
			output:      `{"answers":{}}`,
			wantContain: []string{"Which color?", "_No answer_"},
		},
		{
			name:        "Pending question still renders its question",
			toolName:    "request_user_input",
			arguments:   `{"questions":[` + colorQuestion + `]}`,
			wantContain: []string{"Which color?", "Blue", "Green"},
			wantAbsent:  []string{"_No answer_"},
		},
		{
			name:        "Answer to an unknown question id is not dropped",
			toolName:    "request_user_input",
			arguments:   `{"questions":[` + colorQuestion + `]}`,
			output:      `{"answers":{"color":{"answers":["Blue"]},"extra":{"answers":["ORPHAN_ANSWER"]}}}`,
			wantContain: []string{"**Answer:** Blue", "ORPHAN_ANSWER"},
		},
		{
			name:        "Unrecognized JSON object output is preserved",
			toolName:    "some_future_tool",
			arguments:   `{"target":"INPUT_MARKER"}`,
			output:      `{"status":"ok","details":{"value":"STRUCTURED_OUTPUT_MARKER"}}`,
			wantContain: []string{"INPUT_MARKER", "STRUCTURED_OUTPUT_MARKER"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := []map[string]interface{}{
				{
					"type":      "session_meta",
					"timestamp": "2026-09-25T00:00:00Z",
					"payload": map[string]interface{}{
						"id":        "00000000-0000-4000-8000-000000000001",
						"timestamp": "2026-09-25T00:00:00Z",
						"cwd":       "/test/workspace",
					},
				},
				{
					"type":      "event_msg",
					"timestamp": "2026-09-25T00:00:01Z",
					"payload":   map[string]interface{}{"type": "user_message", "message": "Ask me something."},
				},
				{
					"type":      "response_item",
					"timestamp": "2026-09-25T00:00:02Z",
					"payload": map[string]interface{}{
						"type":      "function_call",
						"name":      tt.toolName,
						"call_id":   "call-1",
						"arguments": tt.arguments,
					},
				},
			}
			if tt.output != "" {
				records = append(records, map[string]interface{}{
					"type":      "response_item",
					"timestamp": "2026-09-25T00:00:03Z",
					"payload": map[string]interface{}{
						"type":    "function_call_output",
						"call_id": "call-1",
						"output":  tt.output,
					},
				})
			}

			sessionData, err := GenerateAgentSession(records, "/test/workspace")
			if err != nil {
				t.Fatalf("GenerateAgentSession failed: %v", err)
			}
			// The session renderer writes FormattedMarkdown verbatim, and skips its generic
			// input/output rendering when it's set, so this is exactly what lands in the file.
			var tool *ToolInfo
			for _, exchange := range sessionData.Exchanges {
				for _, msg := range exchange.Messages {
					if msg.Tool != nil {
						tool = msg.Tool
					}
				}
			}
			if tool == nil {
				t.Fatal("tool call not found in session")
			}
			if tool.FormattedMarkdown == nil {
				t.Fatal("tool has no formatted markdown")
			}
			markdown := *tool.FormattedMarkdown

			for _, want := range tt.wantContain {
				if !strings.Contains(markdown, want) {
					t.Errorf("markdown missing %q:\n%s", want, markdown)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(markdown, absent) {
					t.Errorf("markdown unexpectedly contains %q:\n%s", absent, markdown)
				}
			}
		})
	}
}
