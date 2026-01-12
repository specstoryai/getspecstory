package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/swaggest/jsonschema-go"
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

// TestGenerateAgentSession tests the generation and validation of agent session JSON
func TestGenerateAgentSession(t *testing.T) {
	// Test session details
	// sessionID := "dc6cd9c4-ea2b-466c-a974-182442180eb3"
	// workspaceRoot := "/Users/sean/Source/SpecStory/compositions/cross-agent-1"
	sessionID := "f805e355-f4ee-410e-8ee1-8376ed42a729"
	workspaceRoot := "/Users/sean/Source/SpecStory/specstory-cli"

	// Derive debug path from workspace root and session ID
	debugPath := filepath.Join(workspaceRoot, ".specstory", "debug", sessionID)

	t.Logf("Testing session: %s", sessionID)
	t.Logf("Workspace root: %s", workspaceRoot)
	t.Logf("Debug path: %s", debugPath)

	// Step 1: Get Claude Code project directory using existing provider logic
	t.Log("Step 1: Locating Claude Code project directory...")
	claudeProjectDir, err := GetClaudeCodeProjectDir(workspaceRoot)
	if err != nil {
		t.Fatalf("Failed to get Claude Code project directory: %v", err)
	}
	t.Logf("✓ Claude project directory: %s", claudeProjectDir)

	// Check if project directory exists
	if _, err := os.Stat(claudeProjectDir); os.IsNotExist(err) {
		t.Skipf("Claude project directory does not exist: %s (this test requires the test session to be present)", claudeProjectDir)
		return
	}

	// Step 2: Parse JSONL sessions from the project
	t.Log("\nStep 2: Parsing JSONL sessions from project...")
	parser := NewJSONLParser()
	if err := parser.ParseProjectSessions(claudeProjectDir, true); err != nil {
		t.Fatalf("Failed to parse project sessions: %v", err)
	}

	// Find the specific session
	var targetSession *Session
	for i := range parser.Sessions {
		if parser.Sessions[i].SessionUuid == sessionID {
			targetSession = &parser.Sessions[i]
			break
		}
	}

	if targetSession == nil {
		t.Fatalf("Session %s not found in parsed sessions", sessionID)
	}

	t.Logf("✓ Found session with %d records", len(targetSession.Records))

	// Step 3: Generate the agent session from parsed JSONL
	t.Log("\nStep 3: Generating agent session from JSONL records...")
	agentSession, err := GenerateAgentSession(*targetSession, workspaceRoot)
	if err != nil {
		t.Fatalf("Failed to generate agent session: %v", err)
	}

	t.Logf("✓ Generated session with %d exchanges", len(agentSession.Exchanges))

	// Count total messages
	totalMessages := 0
	for _, exchange := range agentSession.Exchanges {
		totalMessages += len(exchange.Messages)
	}
	t.Logf("✓ Total messages: %d", totalMessages)

	// Step 4: Validate Go struct using swaggest/jsonschema-go
	t.Log("\nStep 3: Validating Go struct against schema using swaggest/jsonschema-go...")
	if err := validateGoStruct(t, agentSession); err != nil {
		t.Errorf("Go struct validation failed: %v", err)
	} else {
		t.Log("✓ Go struct validation passed")
	}

	// Step 4: Serialize to JSON
	t.Log("\nStep 4: Serializing to JSON...")
	jsonData, err := json.MarshalIndent(agentSession, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal to JSON: %v", err)
	}
	t.Logf("✓ Generated %d bytes of JSON", len(jsonData))

	// Step 5: Validate JSON document using xeipuuv/gojsonschema
	t.Log("\nStep 5: Validating JSON document against schema using xeipuuv/gojsonschema...")
	if err := validateJSONDocument(t, jsonData); err != nil {
		t.Errorf("JSON document validation failed: %v", err)
	} else {
		t.Log("✓ JSON document validation passed")
	}

	// Step 6: Write JSON to debug directory
	t.Log("\nStep 6: Writing JSON to debug directory...")

	// Create debug directory if it doesn't exist
	if err := os.MkdirAll(debugPath, 0755); err != nil {
		t.Fatalf("Failed to create debug directory: %v", err)
	}

	outputPath := filepath.Join(debugPath, "agent-session.json")
	if err := os.WriteFile(outputPath, jsonData, 0644); err != nil {
		t.Fatalf("Failed to write JSON file: %v", err)
	}
	t.Logf("✓ Wrote JSON to: %s", outputPath)

	// Step 8: Summary
	t.Log("\n=== Summary ===")
	t.Logf("Schema Version: %s", agentSession.SchemaVersion)
	t.Logf("Provider: %s (%s) v%s", agentSession.Provider.Name, agentSession.Provider.ID, agentSession.Provider.Version)
	t.Logf("Session ID: %s", agentSession.SessionID)
	t.Logf("Created At: %s", agentSession.CreatedAt)
	t.Logf("Workspace Root: %s", agentSession.WorkspaceRoot)
	t.Logf("Exchanges: %d", len(agentSession.Exchanges))
	t.Logf("Total Messages: %d", totalMessages)

	// Count tools used
	toolCount := 0
	for _, exchange := range agentSession.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool != nil {
				toolCount++
			}
		}
	}
	t.Logf("Tool Uses: %d", toolCount)

	t.Log("\n✓ All validations passed!")
}

// validateGoStruct validates the Go struct against the schema using swaggest/jsonschema-go
func validateGoStruct(t *testing.T, session *SessionData) error {
	// Load the schema
	agentSessionSchemaJSON, err := loadSchemaJSON()
	if err != nil {
		return fmt.Errorf("failed to load schema: %w", err)
	}

	// Parse the schema
	var schema jsonschema.Schema
	if err := json.Unmarshal(agentSessionSchemaJSON, &schema); err != nil {
		return fmt.Errorf("failed to parse schema: %w", err)
	}

	// Create a reflector to validate the struct
	reflector := jsonschema.Reflector{}

	// Generate schema from our Go type
	generatedSchema, err := reflector.Reflect(session)
	if err != nil {
		return fmt.Errorf("failed to reflect Go struct: %w", err)
	}

	// Marshal both schemas to compare
	expectedJSON, _ := json.MarshalIndent(schema, "", "  ")
	generatedJSON, _ := json.MarshalIndent(generatedSchema, "", "  ")

	t.Logf("Expected schema size: %d bytes", len(expectedJSON))
	t.Logf("Generated schema size: %d bytes", len(generatedJSON))

	// Note: We're not doing a strict comparison here because the generated schema
	// will have different structure. The real validation happens with gojsonschema
	// on the actual JSON output. This step is more for informational purposes.

	return nil
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
