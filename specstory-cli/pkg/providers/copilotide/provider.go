package copilotide

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Provider implements the SPI Provider interface for VS Code Copilot IDE
type Provider struct{}

// NewProvider creates a new VS Code Copilot IDE provider instance
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the human-readable name of this provider
func (p *Provider) Name() string {
	return "VS Code Copilot IDE"
}

// Check verifies VS Code workspace storage exists and returns info
func (p *Provider) Check(customCommand string) spi.CheckResult {
	slog.Debug("Check: Checking VS Code Copilot installation")

	// Check for workspace storage directory
	storagePath := GetWorkspaceStoragePath()
	if storagePath == "" {
		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     "",
			ErrorMessage: "VS Code workspace storage directory not found",
		}
	}

	slog.Debug("VS Code Copilot check successful", "storagePath", storagePath)

	return spi.CheckResult{
		Success:      true,
		Version:      "VS Code Copilot",
		Location:     storagePath,
		ErrorMessage: "",
	}
}

// DetectAgent checks if VS Code Copilot has been used in the given project path
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	slog.Debug("DetectAgent: Checking for VS Code Copilot activity", "projectPath", projectPath)

	// Try to find all workspaces for project (WSL may have multiple entries)
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "projectPath", projectPath, "error", err)
		if helpOutput {
			fmt.Println("\n❌ No VS Code Copilot workspace found for this project")
			fmt.Printf("  • Project path: %s\n", projectPath)
			fmt.Printf("  • Workspace storage: %s\n", GetWorkspaceStoragePath())
			fmt.Println("  • VS Code needs to be opened in this directory at least once")
			fmt.Println()
		}
		return false
	}

	// Check if any workspace has chat sessions
	totalSessions := 0
	for _, ws := range workspaces {
		sessionFiles, err := LoadAllSessionFiles(ws.Dir)
		if err != nil {
			continue
		}
		totalSessions += len(sessionFiles)
	}

	if totalSessions == 0 {
		slog.Debug("No chat sessions found in any workspace")
		if helpOutput {
			fmt.Println("\n❌ No VS Code Copilot chat sessions found")
			fmt.Printf("  • Workspaces found: %d\n", len(workspaces))
			fmt.Println("  • Create at least one chat session in VS Code Copilot")
			fmt.Println()
		}
		return false
	}

	slog.Debug("VS Code Copilot activity detected", "sessionCount", totalSessions)
	return true
}

// GetAgentChatSession retrieves a single chat session by ID
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSession", "projectPath", projectPath, "sessionID", sessionID, "debugRaw", debugRaw)

	// Find all workspaces for project (WSL may have multiple entries)
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace: %w", err)
	}

	// Try to load the session from each workspace
	for _, ws := range workspaces {
		session, err := LoadSessionByID(ws.Dir, sessionID)
		if err != nil {
			continue
		}

		// Load state file (optional)
		state, err := LoadStateFile(ws.Dir, sessionID)
		if err != nil {
			slog.Warn("Failed to load state file", "sessionId", sessionID, "error", err)
		}

		// Convert to AgentChatSession
		agentSession := ConvertToSessionData(*session, projectPath, state)

		// Write debug files if requested
		if debugRaw {
			if err := WriteDebugFiles(session, sessionID); err != nil {
				slog.Warn("Failed to write debug files", "error", err)
			}
		}

		return &agentSession, nil
	}

	return nil, fmt.Errorf("session %s not found in any workspace", sessionID)
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSessions", "projectPath", projectPath, "debugRaw", debugRaw)

	// Find all workspaces for project (WSL may have multiple entries)
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace: %w", err)
	}

	// Collect session files from all matching workspaces
	type sessionSource struct {
		file         string
		workspaceDir string
	}
	var allSources []sessionSource
	for _, ws := range workspaces {
		files, err := LoadAllSessionFiles(ws.Dir)
		if err != nil {
			slog.Warn("Failed to load session files from workspace", "workspaceID", ws.ID, "error", err)
			continue
		}
		for _, f := range files {
			allSources = append(allSources, sessionSource{file: f, workspaceDir: ws.Dir})
		}
	}

	var sessions []spi.AgentChatSession
	processedCount := 0
	totalCount := len(allSources)
	seenIDs := make(map[string]bool)

	for _, src := range allSources {
		composer, err := LoadSessionFile(src.file)
		if err != nil {
			slog.Warn("Failed to load session", "file", src.file, "error", err)
			continue
		}

		// Deduplicate by session ID across workspaces
		if seenIDs[composer.SessionID] {
			continue
		}
		seenIDs[composer.SessionID] = true

		// Load state file (optional)
		state, err := LoadStateFile(src.workspaceDir, composer.SessionID)
		if err != nil {
			slog.Warn("Failed to load state file", "sessionId", composer.SessionID, "error", err)
		}

		// Check if session has content (either chat messages or editing operations)
		hasConversations := len(composer.Requests) > 0
		hasEditingOperations := hasEditingActivity(state)

		if !hasConversations && !hasEditingOperations {
			slog.Debug("Skipping empty session (no chat or editing activity)", "sessionId", composer.SessionID)
			continue
		}

		// Convert to AgentChatSession
		session := ConvertToSessionData(*composer, projectPath, state)
		sessions = append(sessions, session)

		// Write debug files if requested
		if debugRaw {
			if err := WriteDebugFiles(composer, composer.SessionID); err != nil {
				slog.Warn("Failed to write debug files", "sessionId", composer.SessionID, "error", err)
			}
		}

		// Report progress
		processedCount++
		if progress != nil {
			progress(processedCount, totalCount)
		}
	}

	slog.Debug("Loaded sessions", "count", len(sessions))
	return sessions, nil
}

// ListAgentChatSessions retrieves lightweight metadata for all sessions
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	slog.Debug("ListAgentChatSessions: Loading VS Code Copilot session list",
		"projectPath", projectPath)

	// Step 1: Find all workspaces for project (WSL may have multiple entries)
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "error", err)
		return []spi.SessionMetadata{}, nil // Return empty list if no workspace
	}

	slog.Debug("Found workspaces for project", "workspaceCount", len(workspaces))

	// Step 2: Collect session files from all matching workspaces
	type sessionSource struct {
		file         string
		workspaceDir string
	}
	var allSources []sessionSource
	for _, ws := range workspaces {
		files, err := LoadAllSessionFiles(ws.Dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			allSources = append(allSources, sessionSource{file: f, workspaceDir: ws.Dir})
		}
	}

	if len(allSources) == 0 {
		slog.Debug("No session files found in any workspace")
		return []spi.SessionMetadata{}, nil
	}

	slog.Debug("Loaded session files from all workspaces", "count", len(allSources))

	// Step 3: Extract metadata for each session
	metadataList := make([]spi.SessionMetadata, 0, len(allSources))
	seenIDs := make(map[string]bool)

	for _, src := range allSources {
		composer, err := LoadSessionFile(src.file)
		if err != nil {
			slog.Warn("Failed to load session file", "file", src.file, "error", err)
			continue
		}

		// Deduplicate by session ID across workspaces
		if seenIDs[composer.SessionID] {
			continue
		}
		seenIDs[composer.SessionID] = true

		// Load state file to check for editing operations
		state, err := LoadStateFile(src.workspaceDir, composer.SessionID)
		if err != nil {
			slog.Warn("Failed to load state file", "sessionId", composer.SessionID, "error", err)
		}

		// Check if session has content (either chat messages or editing operations)
		hasConversations := len(composer.Requests) > 0
		hasEditingOperations := hasEditingActivity(state)

		if !hasConversations && !hasEditingOperations {
			slog.Debug("Skipping empty session (no chat or editing activity)", "sessionId", composer.SessionID)
			continue
		}

		metadata := extractCopilotIDESessionMetadata(composer)
		metadataList = append(metadataList, metadata)
	}

	slog.Info("Listed VS Code Copilot sessions",
		"totalFiles", len(allSources),
		"sessionCount", len(metadataList))

	return metadataList, nil
}

// ExecAgentAndWatch is not supported for VS Code Copilot (IDE-based, not CLI)
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	return fmt.Errorf("VS Code Copilot does not support execution via CLI (it is an IDE-based tool)")
}

// WatchAgent watches for new/updated chat sessions
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Debug("WatchAgent", "projectPath", projectPath, "debugRaw", debugRaw)

	// Find all workspaces for project (WSL may have multiple entries)
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to find workspace: %w", err)
	}

	// Watch the newest workspace (most likely to have active sessions)
	newest, err := selectNewestWorkspace(workspaces)
	if err != nil {
		return fmt.Errorf("failed to select workspace: %w", err)
	}

	// Start watching the chatSessions directory
	return WatchChatSessions(ctx, newest.Dir, projectPath, debugRaw, sessionCallback)
}

// extractCopilotIDESessionMetadata extracts lightweight session metadata from a VSCodeComposer
// without fully parsing the conversation
func extractCopilotIDESessionMetadata(composer *VSCodeComposer) spi.SessionMetadata {
	// Use session ID
	sessionID := composer.SessionID

	// Convert timestamp (milliseconds to ISO 8601)
	createdAt := FormatTimestamp(composer.CreationDate)

	// Generate slug using existing GenerateSlug function
	slug := GenerateSlug(*composer)

	// Generate human-readable name
	name := generateCopilotIDESessionName(composer)

	return spi.SessionMetadata{
		SessionID: sessionID,
		CreatedAt: createdAt,
		Slug:      slug,
		Name:      name,
	}
}

// generateCopilotIDESessionName creates a human-readable session name from composer data
func generateCopilotIDESessionName(composer *VSCodeComposer) string {
	// Prefer custom title if available (it's already human-readable)
	if composer.CustomTitle != "" {
		return spi.GenerateReadableName(composer.CustomTitle)
	}

	// Use name if available
	if composer.Name != "" {
		return spi.GenerateReadableName(composer.Name)
	}

	// Otherwise, use first request message
	if len(composer.Requests) > 0 {
		firstMsg := composer.Requests[0].Message.Text
		if firstMsg != "" {
			return spi.GenerateReadableName(firstMsg)
		}
	}

	// Fallback to empty string (shouldn't happen with non-empty conversations)
	return ""
}

// hasEditingActivity checks if a state file contains editing operations
func hasEditingActivity(state *VSCodeStateFile) bool {
	if state == nil {
		return false
	}

	// Version 2 format: check timeline.operations
	if state.Timeline != nil && len(state.Timeline.Operations) > 0 {
		return true
	}

	// Version 1 format: check recentSnapshot for entries
	if state.RecentSnapshot != nil {
		// Handle array format
		if stopsArray, ok := state.RecentSnapshot.([]any); ok {
			for _, stopData := range stopsArray {
				if stopMap, ok := stopData.(map[string]any); ok {
					if entriesData, ok := stopMap["entries"].([]any); ok && len(entriesData) > 0 {
						return true
					}
				}
			}
		}
		// Handle object format
		if stopMap, ok := state.RecentSnapshot.(map[string]any); ok {
			if entriesData, ok := stopMap["entries"].([]any); ok && len(entriesData) > 0 {
				return true
			}
		}
	}

	return false
}
