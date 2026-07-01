package cursoride

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close database during check", "error", closeErr)
		}
	}()

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

	// Try to find all workspaces for project. A project can match more than one
	// workspace entry (e.g. opened via a .code-workspace file, over SSH, or from WSL),
	// so we search all of them rather than picking just one.
	workspaces, err := FindAllWorkspacesForProject(projectPath)
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

	// Check if any matching workspace has composers
	composerIDs, err := LoadComposerIDsFromAllWorkspaces(workspaces)
	if err != nil {
		slog.Debug("Failed to load composer IDs", "error", err)
		return false
	}

	if len(composerIDs) == 0 {
		slog.Debug("No composers found in any matching workspace")
		if helpOutput {
			fmt.Println("\n⚠️  Cursor IDE workspace found but no conversations yet")
			fmt.Printf("  • Workspaces found: %d\n", len(workspaces))
			fmt.Printf("  • Use Cursor IDE's Composer feature to create conversations\n")
			fmt.Println()
		}
		return false
	}

	slog.Debug("Cursor IDE activity detected",
		"workspaceCount", len(workspaces),
		"composerCount", len(composerIDs))
	return true
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	slog.Info("GetAgentChatSessions: Loading Cursor IDE sessions",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// Step 1: Find all workspaces matching the project path (a project can match more
	// than one workspace entry — e.g. opened via .code-workspace, over SSH, or from WSL).
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace for project: %w", err)
	}

	slog.Info("Found workspaces for project",
		"workspaceCount", len(workspaces),
		"projectPath", projectPath)

	// Step 2: Load composer IDs from all matching workspace databases
	composerIDs, err := LoadComposerIDsFromAllWorkspaces(workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer IDs from workspaces: %w", err)
	}

	slog.Info("Loaded composer IDs from workspaces",
		"count", len(composerIDs))

	if len(composerIDs) == 0 {
		slog.Info("No composers found in any matching workspace")
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

	slog.Debug("Loaded composers from global database",
		"count", len(composers))

	// Step 5: Convert to AgentChatSessions
	sessions := make([]spi.AgentChatSession, 0, len(composers))
	processedCount := 0
	totalCount := len(composers)

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

		// Report progress
		processedCount++
		if progress != nil {
			progress(processedCount, totalCount)
		}
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

	// Step 1: Find all workspaces matching the project path (a project can match more
	// than one workspace entry — e.g. opened via .code-workspace, over SSH, or from WSL).
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "error", err)
		return nil, nil // Return nil (not error) if workspace not found
	}

	slog.Debug("Found workspaces for project",
		"workspaceCount", len(workspaces),
		"projectPath", projectPath)

	// Step 2: Load composer IDs from all matching workspace databases
	composerIDs, err := LoadComposerIDsFromAllWorkspaces(workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer IDs from workspaces: %w", err)
	}

	slog.Debug("Loaded composer IDs from workspaces", "count", len(composerIDs))

	// Step 3: Check if the requested session ID exists in any matching workspace
	found := false
	for _, id := range composerIDs {
		if id == sessionID {
			found = true
			break
		}
	}

	if !found {
		slog.Debug("Session ID not found in any matching workspace",
			"sessionID", sessionID,
			"workspaceComposerCount", len(composerIDs))
		return nil, nil // Return nil (not error) if session not in any matching workspace
	}

	// Step 4: Get global database path
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}

	// Step 5: Load only the requested composer from global database
	composers, err := LoadComposerDataBatch(globalDbPath, []string{sessionID})
	if err != nil {
		return nil, fmt.Errorf("failed to load composer data: %w", err)
	}

	// Step 6: Check if we got the composer
	composer, exists := composers[sessionID]
	if !exists {
		slog.Warn("Composer not found in global database despite being in workspace",
			"sessionID", sessionID)
		return nil, nil // Return nil (not error) if not found in global DB
	}

	// Skip if conversation is empty
	if len(composer.Conversation) == 0 {
		slog.Debug("Skipping composer with no conversation", "sessionID", sessionID)
		return nil, nil
	}

	// Step 7: Convert to AgentChatSession
	session, err := ConvertToAgentChatSession(composer)
	if err != nil {
		return nil, fmt.Errorf("failed to convert composer to session: %w", err)
	}

	// Step 8: Write debug output if requested
	if debugRaw {
		if err := writeDebugOutput(session); err != nil {
			slog.Warn("Failed to write debug output",
				"sessionID", session.SessionID,
				"error", err)
			// Don't fail the operation if debug output fails
		}
	}

	slog.Debug("Successfully loaded single session",
		"sessionID", sessionID,
		"slug", session.Slug)

	return session, nil
}

// ListAgentChatSessions retrieves lightweight metadata for all sessions
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	slog.Debug("ListAgentChatSessions: Loading Cursor IDE session list",
		"projectPath", projectPath)

	// Step 1: Find all workspaces matching the project path (a project can match more
	// than one workspace entry — e.g. opened via .code-workspace, over SSH, or from WSL).
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "error", err)
		return []spi.SessionMetadata{}, nil // Return empty list if no workspace
	}

	slog.Debug("Found workspaces for project",
		"workspaceCount", len(workspaces),
		"projectPath", projectPath)

	// Step 2: Load composer IDs from all matching workspace databases
	composerIDs, err := LoadComposerIDsFromAllWorkspaces(workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer IDs from workspaces: %w", err)
	}

	slog.Debug("Loaded composer IDs from workspaces", "count", len(composerIDs))

	if len(composerIDs) == 0 {
		slog.Debug("No composers found in any matching workspace")
		return []spi.SessionMetadata{}, nil
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

	slog.Debug("Loaded composers from global database", "count", len(composers))

	// Step 5: Extract metadata for each composer
	metadataList := make([]spi.SessionMetadata, 0, len(composers))
	for composerID, composer := range composers {
		// Skip empty conversations
		if len(composer.Conversation) == 0 {
			slog.Debug("Skipping composer with no conversation", "composerID", composerID)
			continue
		}

		metadata := extractCursorIDESessionMetadata(composer)
		metadataList = append(metadataList, metadata)
	}

	slog.Info("Listed Cursor IDE sessions",
		"totalComposers", len(composers),
		"sessionCount", len(metadataList))

	return metadataList, nil
}

// ListAllAgentChatSessions enumerates every Cursor IDE session across all workspaces,
// regardless of project. OriginCwd is derived from the workspace.json that references
// each composer; when a composer appears in multiple workspaces (WSL/SSH setups) the
// first-seen path is used. NativePath is the global database path (shared by all sessions)
// because Cursor IDE stores all conversations as key-value rows in a single state.vscdb.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	slog.Debug("ListAllAgentChatSessions: enumerating all Cursor IDE sessions")

	// Build composerID → project path mapping by scanning all workspace databases.
	composerToPath, err := ScanAllWorkspaceComposerPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to scan workspaces: %w", err)
	}
	if len(composerToPath) == 0 {
		return []spi.GlobalSessionRef{}, nil
	}

	// Load lightweight composer metadata from the global database (no bubble data).
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}
	composers, err := LoadAllComposerDataLightweight(globalDbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer metadata: %w", err)
	}

	var refs []spi.GlobalSessionRef
	for composerID, projectPath := range composerToPath {
		composer, exists := composers[composerID]
		if !exists {
			// Workspace references a composer that no longer exists in the global DB.
			slog.Debug("Composer referenced in workspace but missing from global DB",
				"composerID", composerID)
			continue
		}

		// Skip composers that have never had a conversation.
		if len(composer.Conversation) == 0 && len(composer.FullConversationHeadersOnly) == 0 {
			slog.Debug("Skipping empty composer", "composerID", composerID)
			continue
		}

		var createdAt string
		if composer.CreatedAt > 0 {
			t := time.Unix(composer.CreatedAt/1000, (composer.CreatedAt%1000)*1000000).UTC()
			createdAt = t.Format(time.RFC3339)
		}

		refs = append(refs, spi.GlobalSessionRef{
			SessionID:  composerID,
			CreatedAt:  createdAt,
			Slug:       generateSlug(composer),
			Name:       generateCursorIDESessionName(composer),
			NativePath: globalDbPath,
			OriginCwd:  projectPath,
		})
	}

	slog.Info("Listed all Cursor IDE sessions", "count", len(refs))
	return refs, nil
}

// ExecAgentAndWatch opens Cursor IDE at the project path so the user can continue the
// reconstructed session. Cursor IDE is an IDE, not a CLI, so there is no --resume flag
// to pass the session ID; the session is already in the global database and will appear
// in the composer panel. Watching is not possible, so the function returns once the open
// attempt completes.
func (p *Provider) ExecAgentAndWatch(projectPath string, _ string, _ string, _ bool, _ func(*spi.AgentChatSession)) error {
	fmt.Fprintln(os.Stderr, "\nSession is ready in Cursor IDE. Open the composer panel to find it.")
	if err := openCursorIDE(projectPath); err != nil {
		// Opening is best-effort; a failure here should not surface as a hard error
		// since the session is already written and the user can open Cursor manually.
		slog.Debug("Could not open Cursor IDE automatically", "error", err)
		fmt.Fprintf(os.Stderr, "Open Cursor IDE manually in: %s\n", projectPath)
	}
	return nil
}

// openCursorIDE attempts to launch Cursor IDE at the given project path using the
// platform-specific mechanism. On macOS this is `open -a Cursor`; on Linux the
// `cursor` binary is tried if it is on PATH.
func openCursorIDE(projectPath string) error {
	args := cursorOpenArgs(projectPath)
	if len(args) == 0 {
		return fmt.Errorf("no known way to open Cursor IDE on this platform")
	}
	cmd := execCommand(args[0], args[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// cursorOpenArgs returns the command + arguments to open Cursor IDE at the given path for
// the current platform. Returns nil when no mechanism is known.
func cursorOpenArgs(projectPath string) []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"open", "-a", "Cursor", projectPath}
	default: // Linux
		return []string{"cursor", projectPath}
	}
}

// execCommand is a thin wrapper around exec.Command to allow test patching if needed.
var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// WatchAgent watches for Cursor IDE activity and calls the callback with AgentChatSession
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Cursor IDE activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// Check interval for polling the database
	checkInterval := 2 * time.Minute

	// Create and start watcher
	watcher, err := NewCursorIDEWatcher(projectPath, debugRaw, sessionCallback, checkInterval)
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Start(); err != nil {
		return fmt.Errorf("failed to start watcher: %w", err)
	}

	// Wait for context cancellation
	<-ctx.Done()

	// Stop watcher gracefully
	watcher.Stop()

	slog.Info("WatchAgent: Stopped Cursor IDE activity monitoring")
	return nil
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

// extractCursorIDESessionMetadata extracts lightweight session metadata from a ComposerData
// without fully parsing the conversation
func extractCursorIDESessionMetadata(composer *ComposerData) spi.SessionMetadata {
	// Use composer ID as session ID
	sessionID := composer.ComposerID

	// Convert timestamp (milliseconds to ISO 8601)
	var createdAt string
	if composer.CreatedAt > 0 {
		t := time.Unix(composer.CreatedAt/1000, (composer.CreatedAt%1000)*1000000)
		createdAt = t.Format(time.RFC3339)
	} else {
		createdAt = time.Now().Format(time.RFC3339)
	}

	// Generate slug from composer name or first user message (using existing logic)
	slug := generateSlug(composer)

	// Generate human-readable name
	name := generateCursorIDESessionName(composer)

	return spi.SessionMetadata{
		SessionID: sessionID,
		CreatedAt: createdAt,
		Slug:      slug,
		Name:      name,
	}
}

// generateCursorIDESessionName creates a human-readable session name from composer data
func generateCursorIDESessionName(composer *ComposerData) string {
	// Prefer composer name if available (it's already human-readable)
	if composer.Name != "" {
		return spi.GenerateReadableName(composer.Name)
	}

	// Otherwise, use first user message
	for _, bubble := range composer.Conversation {
		if bubble.Type == 1 && bubble.Text != "" {
			return spi.GenerateReadableName(bubble.Text)
		}
	}

	// Fallback to empty string (shouldn't happen with non-empty conversations)
	return ""
}
