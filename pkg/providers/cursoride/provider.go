package cursoride

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// Provider implements the SPI Provider interface for Cursor IDE
type Provider struct{}

// NewProvider creates a new Cursor IDE provider instance
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the human-readable name of this provider
func (p *Provider) Name() string {
	return "Cursor IDE"
}

// Check verifies Cursor IDE database exists and returns info
func (p *Provider) Check(customCommand string) spi.CheckResult {
	slog.Debug("Check: Checking Cursor IDE installation")

	// Check for global database
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     "",
			ErrorMessage: fmt.Sprintf("Cursor IDE global database not found: %v", err),
		}
	}

	// Try to open the database
	db, err := OpenDatabase(globalDbPath)
	if err != nil {
		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     globalDbPath,
			ErrorMessage: fmt.Sprintf("Failed to open global database: %v", err),
		}
	}
	defer db.Close()

	slog.Debug("Cursor IDE check successful", "dbPath", globalDbPath)

	return spi.CheckResult{
		Success:      true,
		Version:      "Cursor IDE",
		Location:     globalDbPath,
		ErrorMessage: "",
	}
}

// DetectAgent checks if Cursor IDE has been used in the given project path
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	slog.Debug("DetectAgent: Checking for Cursor IDE activity", "projectPath", projectPath)

	// Check if global database exists
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		slog.Debug("Global database not found", "error", err)
		return false
	}

	// Try to find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "projectPath", projectPath, "error", err)
		if helpOutput {
			fmt.Println("\n❌ No Cursor IDE workspace found for this project")
			fmt.Printf("  • Project path: %s\n", projectPath)
			fmt.Printf("  • Global database: %s\n", globalDbPath)
			fmt.Println("  • Cursor IDE needs to be opened in this directory at least once")
			fmt.Println()
		}
		return false
	}

	// Check if workspace has any composers
	composerIDs, err := LoadWorkspaceComposerIDs(workspace.DBPath)
	if err != nil {
		slog.Debug("Failed to load composer IDs", "error", err)
		return false
	}

	if len(composerIDs) == 0 {
		slog.Debug("No composers found in workspace")
		if helpOutput {
			fmt.Println("\n⚠️  Cursor IDE workspace found but no conversations yet")
			fmt.Printf("  • Workspace ID: %s\n", workspace.ID)
			fmt.Printf("  • Use Cursor IDE's Composer feature to create conversations\n")
			fmt.Println()
		}
		return false
	}

	slog.Debug("Cursor IDE activity detected",
		"workspaceID", workspace.ID,
		"composerCount", len(composerIDs))
	return true
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	slog.Info("GetAgentChatSessions: Loading Cursor IDE sessions",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// Step 1: Find workspace for project path
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace for project: %w", err)
	}

	slog.Info("Found workspace for project",
		"workspaceID", workspace.ID,
		"projectPath", projectPath)

	// Step 2: Load composer IDs from workspace database
	composerIDs, err := LoadWorkspaceComposerIDs(workspace.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer IDs from workspace: %w", err)
	}

	slog.Info("Loaded composer IDs from workspace",
		"count", len(composerIDs))

	if len(composerIDs) == 0 {
		slog.Info("No composers found in workspace")
		return []spi.AgentChatSession{}, nil
	}

	// Step 3: Get global database path
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}

	// Step 4: Load composer data from global database
	composers, err := LoadComposerDataBatch(globalDbPath, composerIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer data: %w", err)
	}

	slog.Info("Loaded composers from global database",
		"count", len(composers))

	// Step 5: Convert to AgentChatSessions
	sessions := make([]spi.AgentChatSession, 0, len(composers))
	for composerID, composer := range composers {
		// Skip empty conversations
		if len(composer.Conversation) == 0 {
			slog.Debug("Skipping composer with no conversation",
				"composerID", composerID)
			continue
		}

		session, err := ConvertToAgentChatSession(composer)
		if err != nil {
			slog.Warn("Failed to convert composer to session",
				"composerID", composerID,
				"error", err)
			continue
		}

		// Write debug output if requested
		if debugRaw {
			if err := writeDebugOutput(session); err != nil {
				slog.Warn("Failed to write debug output",
					"sessionID", session.SessionID,
					"error", err)
				// Don't fail the operation if debug output fails
			}
		}

		sessions = append(sessions, *session)
	}

	slog.Info("Converted sessions",
		"totalComposers", len(composers),
		"sessionCount", len(sessions))

	return sessions, nil
}

// GetAgentChatSession retrieves a single chat session by ID for the given project path
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSession: Loading single session",
		"projectPath", projectPath,
		"sessionID", sessionID,
		"debugRaw", debugRaw)

	// TODO: Implement single session loading
	return nil, fmt.Errorf("not implemented yet")
}

// ExecAgentAndWatch is not supported for Cursor IDE (IDE-based, not CLI)
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	return fmt.Errorf("Cursor IDE does not support execution via CLI (IDE-based, not CLI-based)")
}

// WatchAgent watches for Cursor IDE activity and calls the callback with AgentChatSession
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Cursor IDE activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// TODO: Implement watching
	return fmt.Errorf("not implemented yet")
}

// writeDebugOutput writes debug JSON files for a Cursor IDE session
func writeDebugOutput(session *spi.AgentChatSession) error {
	// Get the debug directory path
	debugDir := spi.GetDebugDir(session.SessionID)

	// Create the debug directory
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	// Write raw composer data
	rawComposerPath := filepath.Join(debugDir, "raw-composer.json")
	if err := os.WriteFile(rawComposerPath, []byte(session.RawData), 0644); err != nil {
		return fmt.Errorf("failed to write raw composer data: %w", err)
	}

	slog.Debug("Wrote debug output",
		"sessionID", session.SessionID,
		"path", debugDir)

	return nil
}
