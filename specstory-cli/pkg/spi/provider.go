package spi

import (
	"context"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
)

// CheckResult contains the result of a provider check operation
type CheckResult struct {
	Success      bool   // Whether the check succeeded
	Version      string // Version of the provider (empty on failure)
	Location     string // File path/location of the provider executable
	ErrorMessage string // Error message if check failed (empty on success)
}

// AgentChatSession represents a chat session from an AI coding agent
type AgentChatSession struct {
	SessionID   string              // Stable and unique identifier for the session (often a UUID)
	CreatedAt   string              // Stable ISO 8601 timestamp when session was created
	Slug        string              // Stable human-readable but file name safe slug, often derived from first user message
	SessionData *schema.SessionData // Structured session data in unified format
	RawData     string              // Raw session data (e.g., JSON blobs for Cursor CLI, JSONL for Claude Code and Codex CLI, etc.)
}

// Provider defines the interface that all agent coding tool providers must implement
type Provider interface {
	// Name returns the human-readable name of the provider (e.g., "Claude Code", "Cursor CLI", "Codex CLI")
	Name() string

	// Check verifies if the provider is properly installed and returns version info
	// customCommand: empty string = use detected/default binary path, non-empty = use this specific command/path
	Check(customCommand string) CheckResult

	// DetectAgent checks if the provider's agent has been used in the given project path
	// projectPath: Agent's working directory
	// helpOutput: if true AND no activity is found, the provider should output helpful guidance
	// Returns true if the agent has created artifacts/sessions in the specified path
	DetectAgent(projectPath string, helpOutput bool) bool

	// GetAgentChatSession retrieves a single chat session by ID for the given project path
	// projectPath: Agent's working directory
	// sessionID: specific session identifier to retrieve (always provided, never empty)
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// Returns nil if the session is not found, error for actual errors
	GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*AgentChatSession, error)

	// GetAgentChatSessions retrieves all chat sessions for the given project path
	// projectPath: Agent's working directory
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// Returns a slice of AgentChatSession structs containing session data
	GetAgentChatSessions(projectPath string, debugRaw bool) ([]AgentChatSession, error)

	// ExecAgentAndWatch executes the agent in interactive mode and watches for session updates
	// Blocks until the agent exits, calling sessionCallback for each new/updated session
	// projectPath: Agent's working directory
	// customCommand: empty string = use detected/default binary with default args, non-empty = use this specific command/path
	// resumeSessionID: empty string = start new session, non-empty = resume this specific session ID
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// sessionCallback is called with each session update (provider should not block on callback)
	// The implementation should handle its own file watching and session tracking
	ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*AgentChatSession)) error

	// WatchAgent watches for agent activity and calls the callback with an updated AgentChatSession
	// Does NOT execute the agent - only watches for new activity
	// Runs until error or context cancellation
	// ctx: Context for cancellation and timeout control
	// projectPath: Agent's working directory
	// debugRaw: if true, provider should write provider-specific raw debug files to .specstory/debug/<sessionID>/
	//           (e.g., numbered JSON files). The unified session-data.json is written centrally by the CLI.
	// sessionCallback: called with AgentChatSession on each update (provider should not block on callback)
	// The implementation should handle its own file watching and session tracking
	WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*AgentChatSession)) error
}
