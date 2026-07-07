package copilotide

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
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

	// Load state file (optional)
	state, err := LoadStateFile(workspace.Dir, sessionID)
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

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
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
	processedCount := 0
	totalCount := len(sessionFiles)

	for _, sessionFile := range sessionFiles {
		composer, err := LoadSessionFile(sessionFile)
		if err != nil {
			slog.Warn("Failed to load session", "file", sessionFile, "error", err)
			continue
		}

		// Load state file (optional)
		state, err := LoadStateFile(workspace.Dir, composer.SessionID)
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

	// Step 1: Find workspace for project
	workspace, err := FindWorkspaceForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "error", err)
		return []spi.SessionMetadata{}, nil // Return empty list if no workspace
	}

	slog.Debug("Found workspace for project", "workspaceDir", workspace.Dir)

	// Step 2: Load all session files
	sessionFiles, err := LoadAllSessionFiles(workspace.Dir)
	if err != nil {
		return nil, fmt.Errorf("failed to load session files: %w", err)
	}

	slog.Debug("Loaded session files", "count", len(sessionFiles))

	if len(sessionFiles) == 0 {
		slog.Debug("No session files found")
		return []spi.SessionMetadata{}, nil
	}

	// Step 3: Extract metadata for each session
	metadataList := make([]spi.SessionMetadata, 0, len(sessionFiles))
	for _, sessionFile := range sessionFiles {
		composer, err := LoadSessionFile(sessionFile)
		if err != nil {
			slog.Warn("Failed to load session file", "file", sessionFile, "error", err)
			continue
		}

		// Load state file to check for editing operations
		state, err := LoadStateFile(workspace.Dir, composer.SessionID)
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
		"totalFiles", len(sessionFiles),
		"sessionCount", len(metadataList))

	return metadataList, nil
}

// ExecAgentAndWatch opens VS Code at the project path so the user can continue the
// session. VS Code Copilot is an IDE, not a CLI, so there is no --resume flag to pass
// the session ID; the session already lives in the workspace's chat store and is found
// via the Chat panel. Watching is not possible, so the function returns once the open
// attempt completes. (Mirrors the Cursor IDE provider's behavior.)
func (p *Provider) ExecAgentAndWatch(projectPath string, _ string, _ string, _ bool, _ func(*spi.AgentChatSession)) error {
	fmt.Fprintln(os.Stderr, "\nSession is ready in VS Code. Open the Chat panel to find it.")
	if err := openVSCode(projectPath); err != nil {
		// Opening is best-effort; a failure here should not surface as a hard error
		// since the session is already in the store and the user can open VS Code manually.
		slog.Debug("Could not open VS Code automatically", "error", err)
		fmt.Fprintf(os.Stderr, "Open VS Code manually in: %s\n", projectPath)
	}
	return nil
}

// openVSCode attempts to launch VS Code at the given project path using the
// platform-specific mechanism. On macOS this is `open -a "Visual Studio Code"`;
// on Linux the `code` binary is tried if it is on PATH.
func openVSCode(projectPath string) error {
	args := vscodeOpenArgs(projectPath)
	if len(args) == 0 {
		return fmt.Errorf("no known way to open VS Code on this platform")
	}
	cmd := execCommand(args[0], args[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// vscodeOpenArgs returns the command + arguments to open VS Code at the given path for
// the current platform. Returns nil when no mechanism is known.
func vscodeOpenArgs(projectPath string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"open", "-a", "Visual Studio Code", projectPath}
	default: // Linux
		return []string{"code", projectPath}
	}
}

// execCommand is a thin wrapper around exec.Command to allow test patching if needed.
var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// ReconstructSession is not yet supported for VS Code Copilot. Making Copilot IDE a
// cross-agent resume target requires serializing the neutral SessionData into VS Code's
// native chatSessions format AND registering the session in the workspace state.vscdb
// chat.ChatSessionStore.index — planned as a follow-up; see docs/SESSION-PORTABILITY.md.
func (p *Provider) ReconstructSession(_ *schema.SessionData, _ spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	return nil, spi.ErrReconstructionUnsupported
}

// NativeSessionPath is not yet supported for VS Code Copilot (see ReconstructSession).
func (p *Provider) NativeSessionPath(_ string, _ string) (string, error) {
	return "", spi.ErrReconstructionUnsupported
}

// ListAllAgentChatSessions enumerates every VS Code Copilot session across all
// workspaces, regardless of project. OriginCwd is resolved from each workspace's
// workspace.json (folder URI, or the .code-workspace file path for multi-root
// workspaces — FindWorkspaceForProject matches both back). NativePath is the session
// file itself. Lightweight: sessions are read for metadata only, no SessionData parse.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	slog.Debug("ListAllAgentChatSessions: enumerating all VS Code Copilot sessions")

	storagePath := GetWorkspaceStoragePath()
	if storagePath == "" {
		// No workspace storage means VS Code was never used here — nothing to index.
		return []spi.GlobalSessionRef{}, nil
	}

	entries, err := os.ReadDir(storagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace storage directory: %w", err)
	}

	// A session can exist as both <id>.json (older) and <id>.jsonl (newer) — keep one
	// ref per session ID, preferring the .jsonl file (matching LoadSessionByID).
	refByID := make(map[string]spi.GlobalSessionRef)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		workspaceDir := filepath.Join(storagePath, entry.Name())
		collectWorkspaceSessionRefs(workspaceDir, refByID)
	}

	refs := make([]spi.GlobalSessionRef, 0, len(refByID))
	for _, ref := range refByID {
		refs = append(refs, ref)
	}

	slog.Info("Listed all VS Code Copilot sessions", "count", len(refs))
	return refs, nil
}

// collectWorkspaceSessionRefs adds one GlobalSessionRef per non-empty session in the
// given workspace storage directory to refByID. Workspaces without a resolvable
// workspace.json or without chat sessions are skipped silently — both are normal
// (settings-only workspaces, projects where Copilot chat was never used).
func collectWorkspaceSessionRefs(workspaceDir string, refByID map[string]spi.GlobalSessionRef) {
	workspaceJSON, err := readWorkspaceJSON(GetWorkspaceMetadataPath(workspaceDir))
	if err != nil {
		slog.Debug("Skipping workspace directory (no valid workspace.json)",
			"workspaceDir", workspaceDir, "error", err)
		return
	}

	// Prefer the multi-root workspace URI over the single folder URI, matching
	// FindWorkspaceForProject, so OriginCwd round-trips through the same matcher.
	workspaceURI := workspaceJSON.Workspace
	if workspaceURI == "" {
		workspaceURI = workspaceJSON.Folder
	}
	if workspaceURI == "" {
		return
	}
	originCwd, err := uriToPath(workspaceURI)
	if err != nil {
		slog.Debug("Skipping workspace directory (invalid URI)",
			"workspaceDir", workspaceDir, "uri", workspaceURI, "error", err)
		return
	}

	sessionFiles, err := LoadAllSessionFiles(workspaceDir)
	if err != nil {
		// Most commonly the chatSessions directory does not exist — not an error.
		slog.Debug("No chat sessions in workspace", "workspaceDir", workspaceDir, "error", err)
		return
	}

	for _, sessionFile := range sessionFiles {
		composer, err := LoadSessionFile(sessionFile)
		if err != nil {
			slog.Warn("Failed to load session file", "file", sessionFile, "error", err)
			continue
		}

		// Same emptiness rule as ListAgentChatSessions: skip sessions with neither
		// chat messages nor editing operations so the index only holds real sessions.
		state, err := LoadStateFile(workspaceDir, composer.SessionID)
		if err != nil {
			slog.Warn("Failed to load state file", "sessionId", composer.SessionID, "error", err)
		}
		if len(composer.Requests) == 0 && !hasEditingActivity(state) {
			slog.Debug("Skipping empty session", "sessionId", composer.SessionID)
			continue
		}

		// Prefer the .jsonl file when the same session exists in both formats.
		if existing, seen := refByID[composer.SessionID]; seen {
			if strings.HasSuffix(existing.NativePath, ".jsonl") || !strings.HasSuffix(sessionFile, ".jsonl") {
				continue
			}
		}

		meta := extractCopilotIDESessionMetadata(composer)
		refByID[composer.SessionID] = spi.GlobalSessionRef{
			SessionID:  meta.SessionID,
			CreatedAt:  meta.CreatedAt,
			Slug:       meta.Slug,
			Name:       meta.Name,
			NativePath: sessionFile,
			OriginCwd:  originCwd,
		}
	}
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
