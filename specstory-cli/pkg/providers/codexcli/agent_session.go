package codexcli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
)

// Type aliases for convenience - use the shared schema types
type (
	SessionData  = schema.SessionData
	ProviderInfo = schema.ProviderInfo
	Exchange     = schema.Exchange
	Message      = schema.Message
	ContentPart  = schema.ContentPart
	ToolInfo     = schema.ToolInfo
)

// Codex-specific record structures for type safety

// CodexSessionMeta represents the first line of a Codex session JSONL file
type CodexSessionMeta struct {
	Type      string `json:"type"` // "session_meta"
	Timestamp string `json:"timestamp"`
	Payload   struct {
		ID        string `json:"id"`        // Session UUID
		Timestamp string `json:"timestamp"` // Session creation timestamp
		CWD       string `json:"cwd"`       // Working directory
	} `json:"payload"`
}

// CodexEventMsg represents user messages, agent messages, and agent reasoning
type CodexEventMsg struct {
	Type      string `json:"type"` // "event_msg"
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type    string `json:"type"`    // "user_message", "agent_message", "agent_reasoning"
		Message string `json:"message"` // For user_message and agent_message
		Text    string `json:"text"`    // For agent_reasoning
	} `json:"payload"`
}

// CodexResponseItem represents function calls and custom tool calls
type CodexResponseItem struct {
	Type      string `json:"type"` // "response_item"
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type      string `json:"type"` // "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output"
		Name      string `json:"name"` // Tool name
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"` // JSON string for function_call
		Input     string `json:"input"`     // Plain text for custom_tool_call
		Output    string `json:"output"`    // JSON string for outputs
	} `json:"payload"`
}

// CodexTurnContext represents model information
type CodexTurnContext struct {
	Type    string `json:"type"` // "turn_context"
	Payload struct {
		Model string `json:"model"`
	} `json:"payload"`
}

// PendingToolCall holds tool call information waiting for output
type PendingToolCall struct {
	message   *Message // The message containing this tool call
	toolInfo  *ToolInfo
	timestamp string
}

// GenerateAgentSession creates a SessionData from Codex CLI JSONL records
// The records should be in chronological order from the JSONL file
func GenerateAgentSession(records []map[string]interface{}, workspaceRoot string) (*SessionData, error) {
	slog.Info("GenerateAgentSession: Starting", "recordCount", len(records))

	if len(records) == 0 {
		return nil, fmt.Errorf("no records provided")
	}

	// Extract session metadata from first record (session_meta)
	sessionID, createdAt, cwd, err := extractSessionMetadata(records)
	if err != nil {
		return nil, fmt.Errorf("failed to extract session metadata: %w", err)
	}

	// Use provided workspaceRoot or fall back to CWD from session metadata
	if workspaceRoot == "" {
		workspaceRoot = cwd
	}

	slog.Info("GenerateAgentSession: Session metadata",
		"sessionID", sessionID,
		"createdAt", createdAt,
		"workspaceRoot", workspaceRoot)

	// Build exchanges from records
	exchanges, err := buildExchangesFromRecords(records, workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to build exchanges: %w", err)
	}

	// Assign exchangeId to each exchange (format: sessionId:index)
	for i := range exchanges {
		exchanges[i].ExchangeID = fmt.Sprintf("%s:%d", sessionID, i)
	}

	// Populate Summary and FormattedMarkdown for all tools
	for i := range exchanges {
		for j := range exchanges[i].Messages {
			msg := &exchanges[i].Messages[j]
			if msg.Tool != nil {
				summary, formattedMd := formatToolWithSummary(msg.Tool, workspaceRoot)
				if summary != "" {
					msg.Tool.Summary = &summary
				}
				if formattedMd != "" {
					msg.Tool.FormattedMarkdown = &formattedMd
				}
			}
		}
	}

	slog.Info("GenerateAgentSession: Built exchanges", "count", len(exchanges))

	sessionData := &SessionData{
		SchemaVersion: "1.0",
		Provider: ProviderInfo{
			ID:      "codex-cli",
			Name:    "Codex CLI",
			Version: "unknown", // Codex doesn't currently include version in JSONL
		},
		SessionID:     sessionID,
		CreatedAt:     createdAt,
		WorkspaceRoot: workspaceRoot,
		Exchanges:     exchanges,
	}

	return sessionData, nil
}

// extractSessionMetadata parses the session_meta record (first line)
func extractSessionMetadata(records []map[string]interface{}) (sessionID, createdAt, cwd string, err error) {
	if len(records) == 0 {
		return "", "", "", fmt.Errorf("no records to extract metadata from")
	}

	firstRecord := records[0]
	recordType, ok := firstRecord["type"].(string)
	if !ok || recordType != "session_meta" {
		return "", "", "", fmt.Errorf("first record is not session_meta: %v", recordType)
	}

	payload, ok := firstRecord["payload"].(map[string]interface{})
	if !ok {
		return "", "", "", fmt.Errorf("session_meta payload is missing or invalid")
	}

	sessionID, _ = payload["id"].(string)
	createdAt, _ = payload["timestamp"].(string)
	cwd, _ = payload["cwd"].(string)

	if sessionID == "" {
		return "", "", "", fmt.Errorf("session_meta is missing session ID")
	}

	if createdAt == "" {
		// Fall back to record timestamp
		createdAt, _ = firstRecord["timestamp"].(string)
	}

	return sessionID, createdAt, cwd, nil
}

// buildExchangesFromRecords groups JSONL records into exchanges
// An exchange starts with a user message and contains all subsequent agent responses and tool uses
func buildExchangesFromRecords(records []map[string]interface{}, workspaceRoot string) ([]Exchange, error) {
	var exchanges []Exchange
	var currentExchange *Exchange
	var currentModel string
	pendingTools := make(map[string]*PendingToolCall) // callID -> pending tool call

	for i, record := range records {
		recordType, _ := record["type"].(string)
		timestamp, _ := record["timestamp"].(string)

		switch recordType {
		case "session_meta":
			// Skip session metadata - already processed
			continue

		case "turn_context":
			// Update current model for subsequent agent messages
			payload, ok := record["payload"].(map[string]interface{})
			if ok {
				if model, ok := payload["model"].(string); ok {
					currentModel = model
					slog.Debug("Updated current model", "model", currentModel)
				}
			}
			continue

		case "event_msg":
			// Handle user messages, agent messages, and agent reasoning
			payload, ok := record["payload"].(map[string]interface{})
			if !ok {
				continue
			}

			payloadType, _ := payload["type"].(string)

			switch payloadType {
			case "user_message":
				// Start a new exchange
				message, ok := payload["message"].(string)
				if !ok || message == "" {
					continue
				}

				// Save previous exchange if exists
				if currentExchange != nil && len(currentExchange.Messages) > 0 {
					exchanges = append(exchanges, *currentExchange)
				}

				// Start new exchange
				currentExchange = &Exchange{
					StartTime: timestamp,
					Messages:  []Message{},
				}

				// Add user message
				userMsg := Message{
					ID:        fmt.Sprintf("u_%d", i),
					Timestamp: timestamp,
					Role:      "user",
					Content: []ContentPart{
						{Type: "text", Text: message},
					},
				}
				currentExchange.Messages = append(currentExchange.Messages, userMsg)

			case "agent_message":
				// Agent text response - keep as separate message per user's requirement
				message, ok := payload["message"].(string)
				if !ok || message == "" {
					continue
				}

				if currentExchange == nil {
					// Create exchange if missing (shouldn't happen)
					currentExchange = &Exchange{
						StartTime: timestamp,
						Messages:  []Message{},
					}
				}

				agentMsg := Message{
					ID:        fmt.Sprintf("a_%d", i),
					Timestamp: timestamp,
					Role:      "agent",
					Model:     currentModel,
					Content: []ContentPart{
						{Type: "text", Text: message},
					},
				}
				currentExchange.Messages = append(currentExchange.Messages, agentMsg)
				currentExchange.EndTime = timestamp

			case "agent_reasoning":
				// Agent thinking - keep as separate message with thinking content type
				text, ok := payload["text"].(string)
				if !ok || text == "" {
					continue
				}

				if currentExchange == nil {
					// Create exchange if missing (shouldn't happen)
					currentExchange = &Exchange{
						StartTime: timestamp,
						Messages:  []Message{},
					}
				}

				reasoningMsg := Message{
					ID:        fmt.Sprintf("r_%d", i),
					Timestamp: timestamp,
					Role:      "agent",
					Model:     currentModel,
					Content: []ContentPart{
						{Type: "thinking", Text: text},
					},
				}
				currentExchange.Messages = append(currentExchange.Messages, reasoningMsg)
				currentExchange.EndTime = timestamp
			}

		case "response_item":
			// Handle function calls and custom tool calls
			payload, ok := record["payload"].(map[string]interface{})
			if !ok {
				continue
			}

			payloadType, _ := payload["type"].(string)

			switch payloadType {
			case "function_call":
				// Function call (e.g., shell command)
				if currentExchange == nil {
					currentExchange = &Exchange{
						StartTime: timestamp,
						Messages:  []Message{},
					}
				}

				toolName, _ := payload["name"].(string)
				argumentsJSON, _ := payload["arguments"].(string)
				callID, _ := payload["call_id"].(string)

				if toolName == "" {
					continue
				}

				// Parse arguments to extract input
				var input map[string]interface{}
				if argumentsJSON != "" {
					if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
						slog.Warn("Failed to parse function call arguments", "error", err, "arguments", argumentsJSON)
						input = map[string]interface{}{"raw": argumentsJSON}
					}
				}

				// Create tool message
				toolMsg := Message{
					ID:        fmt.Sprintf("t_%d", i),
					Timestamp: timestamp,
					Role:      "agent",
					Model:     currentModel,
					Tool: &ToolInfo{
						Name:   toolName,
						Type:   classifyToolType(toolName),
						UseID:  callID,
						Input:  input,
						Output: nil, // Will be filled when we find the output
					},
					PathHints: extractPathHints(toolName, input, "", workspaceRoot),
				}

				currentExchange.Messages = append(currentExchange.Messages, toolMsg)
				currentExchange.EndTime = timestamp

				// Track this tool call to match with output later
				if callID != "" {
					pendingTools[callID] = &PendingToolCall{
						message:   &currentExchange.Messages[len(currentExchange.Messages)-1],
						toolInfo:  currentExchange.Messages[len(currentExchange.Messages)-1].Tool,
						timestamp: timestamp,
					}
				}

			case "function_call_output":
				// Function call output - merge into the pending tool call
				callID, _ := payload["call_id"].(string)
				outputJSON, _ := payload["output"].(string)

				if callID == "" || outputJSON == "" {
					continue
				}

				// Find the pending tool call
				if pending, exists := pendingTools[callID]; exists {
					// Parse the output JSON
					var outputData map[string]interface{}
					if err := json.Unmarshal([]byte(outputJSON), &outputData); err == nil {
						pending.toolInfo.Output = outputData
					} else {
						pending.toolInfo.Output = map[string]interface{}{"raw": outputJSON}
					}
					delete(pendingTools, callID)
				}

			case "custom_tool_call":
				// Custom tool call (e.g., apply_patch)
				if currentExchange == nil {
					currentExchange = &Exchange{
						StartTime: timestamp,
						Messages:  []Message{},
					}
				}

				toolName, _ := payload["name"].(string)
				inputText, _ := payload["input"].(string)
				callID, _ := payload["call_id"].(string)

				if toolName == "" {
					continue
				}

				// For custom tools, input is plain text, not JSON
				input := map[string]interface{}{
					"input": inputText,
				}

				// Create tool message
				toolMsg := Message{
					ID:        fmt.Sprintf("ct_%d", i),
					Timestamp: timestamp,
					Role:      "agent",
					Model:     currentModel,
					Tool: &ToolInfo{
						Name:   toolName,
						Type:   classifyToolType(toolName),
						UseID:  callID,
						Input:  input,
						Output: nil, // Will be filled when we find the output
					},
					PathHints: extractPathHints(toolName, input, inputText, workspaceRoot),
				}

				currentExchange.Messages = append(currentExchange.Messages, toolMsg)
				currentExchange.EndTime = timestamp

				// Track this tool call to match with output later
				if callID != "" {
					pendingTools[callID] = &PendingToolCall{
						message:   &currentExchange.Messages[len(currentExchange.Messages)-1],
						toolInfo:  currentExchange.Messages[len(currentExchange.Messages)-1].Tool,
						timestamp: timestamp,
					}
				}

			case "custom_tool_call_output":
				// Custom tool call output - merge into the pending tool call
				callID, _ := payload["call_id"].(string)
				outputJSON, _ := payload["output"].(string)

				if callID == "" || outputJSON == "" {
					continue
				}

				// Find the pending tool call
				if pending, exists := pendingTools[callID]; exists {
					// Parse the output JSON
					var outputData map[string]interface{}
					if err := json.Unmarshal([]byte(outputJSON), &outputData); err == nil {
						pending.toolInfo.Output = outputData
					} else {
						pending.toolInfo.Output = map[string]interface{}{"raw": outputJSON}
					}
					delete(pendingTools, callID)
				}
			}
		}
	}

	// Add the last exchange if exists
	if currentExchange != nil && len(currentExchange.Messages) > 0 {
		exchanges = append(exchanges, *currentExchange)
	}

	return exchanges, nil
}

// formatToolWithSummary generates custom summary and formatted markdown for a Codex tool
// Returns (summary, formattedMarkdown) where summary is the custom content for <summary> tag
// and formattedMarkdown is additional content to display in the tool-use block
func formatToolWithSummary(tool *ToolInfo, workspaceRoot string) (string, string) {
	var summary string
	var formattedMd strings.Builder

	// Format tool input based on whether it's a function call or custom tool
	if tool.Input != nil {
		// Check if this is a custom tool (has string input in "input" field)
		if inputStr, ok := tool.Input["input"].(string); ok {
			// Custom tool with text input (e.g., apply_patch)
			formattedMd.WriteString(formatCustomToolCall(tool.Name, inputStr))
		} else if tool.Name == "shell_command" {
			// Shell command: check if single-line or multi-line
			inputJSON, _ := json.Marshal(tool.Input)
			shellSummary, shellBody := formatShellWithSummary(string(inputJSON))
			if shellSummary != "" {
				// Single-line command goes in summary
				summary = fmt.Sprintf("Tool use: **%s** %s", tool.Name, shellSummary)
			}
			if shellBody != "" {
				formattedMd.WriteString(shellBody)
			}
		} else {
			// Other function calls with JSON arguments
			inputJSON, _ := json.Marshal(tool.Input)
			formattedMd.WriteString(formatToolCall(tool.Name, string(inputJSON)))
		}
	}

	// Format tool output if present
	if tool.Output != nil {
		if outputStr, ok := tool.Output["raw"].(string); ok {
			// Clean and truncate output if needed
			cleaned := strings.TrimSpace(outputStr)
			// Only show output if there's actual content
			if cleaned != "" {
				if formattedMd.Len() > 0 {
					formattedMd.WriteString("\n")
				}
				if len(cleaned) > 5000 {
					cleaned = cleaned[:5000] + "\n... (truncated)"
				}
				formattedMd.WriteString("```\n" + cleaned + "\n```")
			}
		}
	}

	return summary, strings.TrimSpace(formattedMd.String())
}

// classifyToolType maps Codex tool names to standard tool types
func classifyToolType(toolName string) string {
	switch toolName {
	case "shell", "shell_command": // `shell` is legacy
		return "shell"
	case "update_plan":
		return "task"
	case "view_image":
		return "read"
	case "apply_patch":
		return "write"
	case "list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource":
		return "generic"
	default:
		return "unknown"
	}
}

// extractPathHints extracts file paths from tool inputs.
// For apply_patch: parses patch format for file paths.
// For other tools: checks common path fields like "path", "file", etc.
func extractPathHints(toolName string, input map[string]interface{}, inputText string, workspaceRoot string) []string {
	var paths []string

	if toolName == "apply_patch" {
		// Parse patch format to extract file paths
		// Look for lines like "*** Modify File: path", "*** Create File: path", "*** Delete File: path"
		if inputText != "" {
			paths = extractPathsFromPatch(inputText, workspaceRoot)
		} else if inputStr, ok := input["input"].(string); ok {
			paths = extractPathsFromPatch(inputStr, workspaceRoot)
		}
	} else {
		// For other tools, check common path fields
		pathFields := []string{"path", "file", "filename", "file_path"}
		for _, field := range pathFields {
			if value, ok := input[field].(string); ok && value != "" {
				normalizedPath := normalizePath(value, workspaceRoot)
				if !contains(paths, normalizedPath) {
					paths = append(paths, normalizedPath)
				}
			}
		}
	}

	return paths
}

// extractPathsFromPatch parses apply_patch input to find file paths
func extractPathsFromPatch(patchText string, workspaceRoot string) []string {
	var paths []string
	lines := strings.Split(patchText, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for patch markers - both "Create File" and "Add File" are valid
		markers := []string{
			"*** Modify File:",
			"*** Update File:",
			"*** Create File:",
			"*** Add File:",
			"*** Delete File:",
			"*** Rename File:",
			"*** Remove File:",
		}

		for _, marker := range markers {
			if strings.HasPrefix(line, marker) {
				path := strings.TrimSpace(strings.TrimPrefix(line, marker))
				if path != "" {
					normalizedPath := normalizePath(path, workspaceRoot)
					if !contains(paths, normalizedPath) {
						paths = append(paths, normalizedPath)
					}
				}
				break
			}
		}

		// For rename, also check for "*** New Name:" marker
		if strings.HasPrefix(line, "*** New Name:") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** New Name:"))
			if path != "" {
				normalizedPath := normalizePath(path, workspaceRoot)
				if !contains(paths, normalizedPath) {
					paths = append(paths, normalizedPath)
				}
			}
		}
	}

	return paths
}

// normalizePath converts absolute paths to workspace-relative paths when possible
func normalizePath(path, workspaceRoot string) string {
	if workspaceRoot == "" {
		return path
	}

	// If path is absolute and starts with workspace root, make it relative
	if filepath.IsAbs(path) && strings.HasPrefix(path, workspaceRoot) {
		relPath, err := filepath.Rel(workspaceRoot, path)
		if err == nil {
			return relPath
		}
	}

	return path
}

// contains checks if a string slice contains a value
func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
