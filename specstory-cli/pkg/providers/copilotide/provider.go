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

	// Try to find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
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

	// Check if workspace has any chat sessions
	sessionFiles, err := LoadAllSessionFiles(workspace.Dir)
	if err != nil || len(sessionFiles) == 0 {
		slog.Debug("No chat sessions found", "workspace", workspace.Dir, "error", err)
		if helpOutput {
			fmt.Println("\n❌ No VS Code Copilot chat sessions found")
			fmt.Printf("  • Workspace: %s\n", workspace.Dir)
			fmt.Println("  • Create at least one chat session in VS Code Copilot")
			fmt.Println()
		}
		return false
	}

	slog.Debug("VS Code Copilot activity detected", "sessionCount", len(sessionFiles))
	return true
}

// GetAgentChatSession retrieves a single chat session by ID
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSession", "projectPath", projectPath, "sessionID", sessionID, "debugRaw", debugRaw)

	// Find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace: %w", err)
	}

	// Load specific session
	session, err := LoadSessionByID(workspace.Dir, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to load session: %w", err)
	}

	// Convert to AgentChatSession
	agentSession := ConvertToSessionData(*session, projectPath)

	// Write debug files if requested
	if debugRaw {
		if err := WriteDebugFiles(session, sessionID); err != nil {
			slog.Warn("Failed to write debug files", "error", err)
		}
	}

	return &agentSession, nil
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSessions", "projectPath", projectPath, "debugRaw", debugRaw)

	// Find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace: %w", err)
	}

	// Load all session files
	sessionFiles, err := LoadAllSessionFiles(workspace.Dir)
	if err != nil {
		return nil, fmt.Errorf("failed to load session files: %w", err)
	}

	var sessions []spi.AgentChatSession
	for _, sessionFile := range sessionFiles {
		composer, err := LoadSessionFile(sessionFile)
		if err != nil {
			slog.Warn("Failed to load session", "file", sessionFile, "error", err)
			continue
		}

		// Filter empty conversations
		if len(composer.Requests) == 0 {
			slog.Debug("Skipping empty session", "sessionId", composer.SessionID)
			continue
		}

		// Convert to AgentChatSession
		session := ConvertToSessionData(*composer, projectPath)
		sessions = append(sessions, session)

		// Write debug files if requested
		if debugRaw {
			if err := WriteDebugFiles(composer, composer.SessionID); err != nil {
				slog.Warn("Failed to write debug files", "sessionId", composer.SessionID, "error", err)
			}
		}
	}

	slog.Debug("Loaded sessions", "count", len(sessions))
	return sessions, nil
}

// ExecAgentAndWatch is not supported for VS Code Copilot (IDE-based, not CLI)
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	return fmt.Errorf("VS Code Copilot does not support execution via CLI (it is an IDE-based tool)")
}

// WatchAgent watches for new/updated chat sessions
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Debug("WatchAgent", "projectPath", projectPath, "debugRaw", debugRaw)

	// Find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to find workspace: %w", err)
	}

	// Start watching the chatSessions directory
	return WatchChatSessions(ctx, workspace.Dir, projectPath, debugRaw, sessionCallback)
}
