package markdown

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
)

// TestGenerateMarkdownFromAgentSession tests markdown generation from agent session JSON
func TestGenerateMarkdownFromAgentSession(t *testing.T) {
	// Test session details
	// sessionID := "dc6cd9c4-ea2b-466c-a974-182442180eb3"
	// workspaceRoot := "/Users/sean/Source/SpecStory/compositions/cross-agent-1"
	sessionID := "f805e355-f4ee-410e-8ee1-8376ed42a729"
	workspaceRoot := "/Users/sean/Source/SpecStory/specstory-cli"

	// Derive debug path from workspace root and session ID
	debugPath := filepath.Join(workspaceRoot, ".specstory", "debug", sessionID)

	t.Logf("Testing markdown generation for session: %s", sessionID)
	t.Logf("Workspace root: %s", workspaceRoot)
	t.Logf("Debug path: %s", debugPath)

	// Step 1: Load the agent-session.json file
	t.Log("\nStep 1: Loading agent-session.json...")
	agentSessionPath := filepath.Join(debugPath, "agent-session.json")

	if _, err := os.Stat(agentSessionPath); os.IsNotExist(err) {
		t.Skipf("agent-session.json does not exist: %s (run TestGenerateAgentSession first)", agentSessionPath)
		return
	}

	jsonData, err := os.ReadFile(agentSessionPath)
	if err != nil {
		t.Fatalf("Failed to read agent-session.json: %v", err)
	}
	t.Logf("✓ Loaded %d bytes of JSON", len(jsonData))

	// Step 2: Parse JSON into SessionData struct
	t.Log("\nStep 2: Parsing JSON into SessionData struct...")
	var sessionData schema.SessionData
	if err := json.Unmarshal(jsonData, &sessionData); err != nil {
		t.Fatalf("Failed to unmarshal session-data.json: %v", err)
	}
	t.Logf("✓ Parsed session with %d exchanges", len(sessionData.Exchanges))

	// Step 3: Generate markdown
	t.Log("\nStep 3: Generating markdown...")
	markdown, err := GenerateMarkdownFromAgentSession(&sessionData, false)
	if err != nil {
		t.Fatalf("Failed to generate markdown: %v", err)
	}
	t.Logf("✓ Generated %d bytes of markdown", len(markdown))

	// Step 4: Write markdown to debug directory
	t.Log("\nStep 4: Writing markdown to debug directory...")
	outputPath := filepath.Join(debugPath, "agent-session.md")

	if err := os.WriteFile(outputPath, []byte(markdown), 0644); err != nil {
		t.Fatalf("Failed to write markdown file: %v", err)
	}
	t.Logf("✓ Wrote markdown to: %s", outputPath)

	// Step 5: Summary
	t.Log("\n=== Summary ===")
	t.Logf("Session ID: %s", sessionData.SessionID)
	t.Logf("Provider: %s (%s) v%s", sessionData.Provider.Name, sessionData.Provider.ID, sessionData.Provider.Version)
	t.Logf("Exchanges: %d", len(sessionData.Exchanges))

	// Count messages and tools
	messageCount := 0
	toolCount := 0
	thinkingCount := 0
	formattedMarkdownCount := 0
	for _, exchange := range sessionData.Exchanges {
		messageCount += len(exchange.Messages)
		for _, msg := range exchange.Messages {
			if msg.Tool != nil {
				toolCount++
				if msg.Tool.FormattedMarkdown != nil && *msg.Tool.FormattedMarkdown != "" {
					formattedMarkdownCount++
				}
			}
			for _, part := range msg.Content {
				if part.Type == "thinking" {
					thinkingCount++
				}
			}
		}
	}
	t.Logf("Total Messages: %d", messageCount)
	t.Logf("Tool Uses: %d", toolCount)
	t.Logf("Tools with FormattedMarkdown: %d", formattedMarkdownCount)
	t.Logf("Thinking Blocks: %d", thinkingCount)

	t.Log("\n✓ Markdown generation completed successfully!")
	t.Logf("\nTo compare with existing markdown:")
	t.Logf("  Generated: %s", outputPath)
	t.Logf("  Existing:  %s/.specstory/history/2025-11-13_21-12-14Z-make-a-hello-world.md", workspaceRoot)
}
