package opencode

import (
	"context"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Provider implements the SPI Provider interface for OpenCode.
// OpenCode is a terminal-based AI coding assistant by SST that stores
// session data in JSON files at ~/.local/share/opencode/storage/
type Provider struct{}

// NewProvider creates a new OpenCode provider instance.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the human-readable name of this provider.
func (p *Provider) Name() string {
	return "OpenCode"
}

// Check verifies OpenCode installation and returns version info.
// TODO: Implement in Step 6
func (p *Provider) Check(customCommand string) spi.CheckResult {
	return spi.CheckResult{
		Success:      false,
		Version:      "",
		Location:     "",
		ErrorMessage: "OpenCode provider not yet implemented",
	}
}

// DetectAgent checks if OpenCode has been used in the given project path.
// TODO: Implement in Step 6
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	return false
}

// GetAgentChatSession retrieves a single chat session by ID.
// TODO: Implement in Step 6
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	return nil, nil
}

// GetAgentChatSessions retrieves all chat sessions for the given project path.
// TODO: Implement in Step 6
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool) ([]spi.AgentChatSession, error) {
	return []spi.AgentChatSession{}, nil
}

// ExecAgentAndWatch executes OpenCode in interactive mode and watches for session updates.
// TODO: Implement in Step 6
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	return nil
}

// WatchAgent watches for OpenCode agent activity without executing.
// TODO: Implement in Step 7
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	return nil
}
