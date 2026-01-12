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

// TestGenerateAgentSession tests the generation and validation of agent session JSON for Codex
func TestGenerateAgentSession(t *testing.T) {
	// Test session details
	sessionID := "019a7f10-45f9-7b13-9a4a-3383dc6db0d3"
	workspaceRoot := "/Users/sean/Source/SpecStory/compositions/cross-agent-1"

	// Derive debug path from workspace root and session ID
	debugPath := filepath.Join(workspaceRoot, ".specstory", "debug", sessionID)

	t.Logf("Testing Codex session: %s", sessionID)
	t.Logf("Workspace root: %s", workspaceRoot)
	t.Logf("Debug path: %s", debugPath)

	// Step 1: Find the Codex session using existing provider logic
	t.Log("\nStep 1: Finding Codex session...")
	sessions, err := findCodexSessions(workspaceRoot, sessionID, false)
	if err != nil {
		t.Fatalf("Failed to find Codex sessions: %v", err)
	}

	if len(sessions) == 0 {
		t.Skipf("Codex session %s not found for workspace %s (session may not exist)", sessionID, workspaceRoot)
		return
	}

	sessionInfo := sessions[0]
	t.Logf("✓ Found session at: %s", sessionInfo.SessionPath)

	// Step 2: Read JSONL records from the session file
	t.Log("\nStep 2: Reading JSONL records from session file...")
	records, _, err := readSessionRawData(sessionInfo.SessionPath)
	if err != nil {
		t.Fatalf("Failed to read session JSONL: %v", err)
	}
	t.Logf("✓ Read %d JSONL records", len(records))

	// Step 3: Generate the agent session from JSONL records
	t.Log("\nStep 3: Generating agent session from JSONL records...")
	agentSession, err := GenerateAgentSession(records, workspaceRoot)
	if err != nil {
		t.Fatalf("Failed to generate agent session: %v", err)
	}

	t.Logf("✓ Generated session with %d exchanges", len(agentSession.Exchanges))

	// Count total messages and tools
	totalMessages := 0
	toolCount := 0
	thinkingCount := 0
	for _, exchange := range agentSession.Exchanges {
		totalMessages += len(exchange.Messages)
		for _, msg := range exchange.Messages {
			if msg.Tool != nil {
				toolCount++
			}
			for _, part := range msg.Content {
				if part.Type == "thinking" {
					thinkingCount++
				}
			}
		}
	}
	t.Logf("✓ Total messages: %d", totalMessages)
	t.Logf("✓ Tool uses: %d", toolCount)
	t.Logf("✓ Thinking blocks: %d", thinkingCount)

	// Step 4: Serialize to JSON
	t.Log("\nStep 4: Serializing to JSON...")
	jsonData, err := json.MarshalIndent(agentSession, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal to JSON: %v", err)
	}
	t.Logf("✓ Generated %d bytes of JSON", len(jsonData))

	// Step 5: Validate JSON document using xeipuuv/gojsonschema
	t.Log("\nStep 5: Validating JSON document against schema...")
	if err := validateJSONDocument(t, jsonData); err != nil {
		t.Errorf("JSON document validation failed: %v", err)
		// Still write the output even if validation fails for debugging
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

	// Step 7: Summary
	t.Log("\n=== Summary ===")
	t.Logf("Schema Version: %s", agentSession.SchemaVersion)
	t.Logf("Provider: %s (%s) v%s", agentSession.Provider.Name, agentSession.Provider.ID, agentSession.Provider.Version)
	t.Logf("Session ID: %s", agentSession.SessionID)
	t.Logf("Created At: %s", agentSession.CreatedAt)
	t.Logf("Workspace Root: %s", agentSession.WorkspaceRoot)
	t.Logf("Exchanges: %d", len(agentSession.Exchanges))
	t.Logf("Total Messages: %d", totalMessages)
	t.Logf("Tool Uses: %d", toolCount)
	t.Logf("Thinking Blocks: %d", thinkingCount)

	// Show breakdown by tool type
	toolTypeCount := make(map[string]int)
	for _, exchange := range agentSession.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Tool != nil {
				toolTypeCount[msg.Tool.Type]++
			}
		}
	}

	t.Log("\nTool Type Breakdown:")
	for toolType, count := range toolTypeCount {
		t.Logf("  %s: %d", toolType, count)
	}

	t.Log("\n✓ All steps completed!")
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
