package cursoride

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
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
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":      "cursoride",
			"error_type":    "database_not_found",
			"error_message": err.Error(),
		})
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
		analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
			"provider":      "cursoride",
			"error_type":    "database_open_failed",
			"error_message": err.Error(),
		})
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

	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider": "cursoride",
		"location": globalDbPath,
	})

	return spi.CheckResult{
		Success: true,
		// Cursor IDE's version isn't discoverable from its database, so no version
		// is reported rather than a placeholder that isn't actually a version.
		Version:      "",
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

	// Check if any composers are associated with this project
	composerIDs, err := FindProjectComposerIDs(globalDbPath, projectPath, workspaces)
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

// loadProjectComposers loads the full composer records associated with the project
// from the global database. It is the shared preamble of the bulk session readers
// (GetAgentChatSessions, ListAgentChatSessions), which differ only in how they treat a
// missing workspace and what they convert the composers into. Returns an empty map
// (not an error) when the project has no composers.
func loadProjectComposers(projectPath string, workspaces []WorkspaceMatch) (map[string]*ComposerData, error) {
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}

	composerIDs, err := FindProjectComposerIDs(globalDbPath, projectPath, workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer IDs: %w", err)
	}

	if len(composerIDs) == 0 {
		slog.Debug("No composers found for project")
		return map[string]*ComposerData{}, nil
	}

	composers, err := LoadComposerDataBatch(globalDbPath, composerIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer data: %w", err)
	}

	slog.Debug("Loaded composers from global database", "count", len(composers))
	return composers, nil
}

// GetAgentChatSessions retrieves all chat sessions for the given project path
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	slog.Info("GetAgentChatSessions: Loading Cursor IDE sessions",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// Find all workspaces matching the project path (a project can match more than
	// one workspace entry — e.g. opened via .code-workspace, over SSH, or from WSL).
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace for project: %w", err)
	}

	slog.Info("Found workspaces for project",
		"workspaceCount", len(workspaces),
		"projectPath", projectPath)

	composers, err := loadProjectComposers(projectPath, workspaces)
	if err != nil {
		return nil, err
	}
	if len(composers) == 0 {
		return []spi.AgentChatSession{}, nil
	}

	// Convert to AgentChatSessions
	sessions := make([]spi.AgentChatSession, 0, len(composers))
	processedCount := 0
	totalCount := len(composers)

	// Progress counts every composer examined — including skipped/failed ones — so
	// the progress bar always reaches totalCount instead of stalling short of it.
	reportProgress := func() {
		processedCount++
		if progress != nil {
			progress(processedCount, totalCount)
		}
	}

	for composerID, composer := range composers {
		// Skip empty conversations
		if len(composer.Conversation) == 0 {
			slog.Debug("Skipping composer with no conversation",
				"composerID", composerID)
			reportProgress()
			continue
		}

		session, err := ConvertToAgentChatSession(composer, projectPath)
		if err != nil {
			slog.Warn("Failed to convert composer to session",
				"composerID", composerID,
				"error", err)
			reportProgress()
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
		reportProgress()
	}

	slog.Info("Converted sessions",
		"totalComposers", len(composers),
		"sessionCount", len(sessions))

	return sessions, nil
}

// GetAgentChatSession retrieves a single chat session by ID for the given project path.
// It deliberately doesn't share loadProjectComposers with the bulk readers: after the
// membership check it loads only the one requested composer (and its bubbles), not
// every composer in the project.
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

	// Step 2: Get global database path
	globalDbPath, err := GetGlobalDatabasePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get global database path: %w", err)
	}

	// Step 3: Load the project's composer IDs (workspace DBs + embedded workspaceIdentifier)
	composerIDs, err := FindProjectComposerIDs(globalDbPath, projectPath, workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to load composer IDs: %w", err)
	}

	// Step 4: Check if the requested session ID belongs to this project
	found := false
	for _, id := range composerIDs {
		if id == sessionID {
			found = true
			break
		}
	}

	if !found {
		slog.Debug("Session ID not associated with this project",
			"sessionID", sessionID,
			"projectComposerCount", len(composerIDs))
		return nil, nil // Return nil (not error) if session not in this project
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
	session, err := ConvertToAgentChatSession(composer, projectPath)
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

	// Find all workspaces matching the project path (a project can match more than
	// one workspace entry — e.g. opened via .code-workspace, over SSH, or from WSL).
	workspaces, err := FindAllWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "error", err)
		return []spi.SessionMetadata{}, nil // Return empty list if no workspace
	}

	slog.Debug("Found workspaces for project",
		"workspaceCount", len(workspaces),
		"projectPath", projectPath)

	composers, err := loadProjectComposers(projectPath, workspaces)
	if err != nil {
		return nil, err
	}
	if len(composers) == 0 {
		return []spi.SessionMetadata{}, nil
	}

	// Extract metadata for each composer
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
// regardless of project. OriginCwd comes from the workspaceIdentifier embedded in each
// composer (the live association in Cursor >= 3.12), falling back to the workspace-DB
// scan for older sessions that predate the embedded field; when a composer appears in
// multiple workspaces (WSL/SSH setups) the first-seen path is used. NativePath is the
// global database path (shared by all sessions) because Cursor IDE stores all
// conversations as key-value rows in a single state.vscdb.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	slog.Debug("ListAllAgentChatSessions: enumerating all Cursor IDE sessions")

	// Build the fallback composerID → project path mapping from the workspace databases.
	composerToPath, err := ScanAllWorkspaceComposerPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to scan workspaces: %w", err)
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
	for composerID, composer := range composers {
		// Prefer the embedded workspaceIdentifier, then the workspace-DB scan. A
		// composer with neither has no known project and can't be indexed.
		projectPath := ""
		if composer.WorkspaceIdentifier != nil && composer.WorkspaceIdentifier.URI != nil {
			projectPath = composer.WorkspaceIdentifier.URI.FsPath
		}
		if projectPath == "" {
			projectPath = composerToPath[composerID]
		}
		if projectPath == "" {
			slog.Debug("Composer has no known project association", "composerID", composerID)
			continue
		}

		// Skip composers that have never had a conversation.
		if len(composer.Conversation) == 0 && len(composer.FullConversationHeadersOnly) == 0 {
			slog.Debug("Skipping empty composer", "composerID", composerID)
			continue
		}

		// Local time, not UTC, so the same session gets an identical CreatedAt string
		// here and in extractCursorIDESessionMetadata/ConvertToAgentChatSession.
		var createdAt string
		if composer.CreatedAt > 0 {
			t := time.Unix(composer.CreatedAt/1000, (composer.CreatedAt%1000)*1000000)
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

// ExecAgentAndWatch opens Cursor IDE at the project path and then watches the Cursor
// databases, auto-saving session updates via sessionCallback. Cursor IDE is an IDE, not
// a CLI: there is no child process whose exit ends the session, so this blocks until the
// user interrupts (Ctrl-C) rather than until an agent exits.
//
// The resume flow (resumeSessionID != "") arrives here after ReconstructSession has
// already written the imported session into the global database — the "session is ready"
// note only makes sense in that case, not on a plain `specstory run cursoride`.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if resumeSessionID != "" {
		fmt.Fprintln(os.Stderr, "\nSession is ready in the Cursor IDE. Open the Agents panel to find it.")
	}
	if err := openCursorIDE(projectPath, customCommand); err != nil {
		// Opening is best-effort; a failure here should not surface as a hard error
		// since the user can open Cursor manually and watching still works.
		slog.Debug("Could not open Cursor IDE automatically", "error", err)
		fmt.Fprintf(os.Stderr, "Open Cursor IDE manually in: %s\n", projectPath)
		if errors.Is(err, errCursorCLIMissing) {
			fmt.Fprintln(os.Stderr, "To let SpecStory open the project for you, install Cursor's shell command:")
			fmt.Fprintln(os.Stderr, "open the command palette in Cursor (Cmd/Ctrl+Shift+P) and run \"Shell Command: Install 'cursor' command\".")
		}
	}

	fmt.Fprintln(os.Stderr, "Watching Cursor IDE sessions in this project — press Ctrl-C to stop.")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// The watcher needs the project's workspace entry, which won't exist yet if this
	// project has never been opened in Cursor — the open above creates it a moment
	// later, once Cursor actually opens the folder. Retry until the watcher can start
	// or the user interrupts.
	printedWaiting := false
	for {
		err := p.WatchAgent(ctx, projectPath, debugRaw, sessionCallback)
		if ctx.Err() != nil || err == nil {
			return nil
		}
		if !printedWaiting {
			fmt.Fprintln(os.Stderr, "Waiting for Cursor to open this project...")
			printedWaiting = true
		}
		slog.Debug("Cursor IDE watcher not ready, retrying", "error", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// errCursorCLIMissing signals that Cursor's `cursor` shell command is not on PATH. The
// command is opt-in (installed from Cursor's command palette), so its absence is an
// expected condition, not a failure — callers use this to print installation guidance
// instead of a generic error.
var errCursorCLIMissing = errors.New("the `cursor` shell command is not installed")

// openCursorIDE launches Cursor IDE at the given project path. By default it uses
// Cursor's own `cursor` CLI — the only launcher that reliably opens the directory as
// a workspace window (`open -a Cursor` on macOS mostly just activates an
// already-running instance on its home screen, so it is deliberately not used as a
// fallback). A custom command (from --command or the cursoride_cmd config) overrides
// the launcher binary and prepends any extra arguments before the project path.
//
// When the default `cursor` CLI isn't on PATH, errCursorCLIMissing is returned so the
// caller can tell the user how to install it; a missing custom launcher returns a
// plain error, since the install guidance only applies to Cursor's own command.
func openCursorIDE(projectPath, customCommand string) error {
	launcher := "cursor"
	var args []string
	if customCommand != "" {
		if parts := spi.SplitCommandLine(customCommand); len(parts) > 0 {
			launcher = parts[0]
			args = parts[1:]
		}
	}

	if _, err := exec.LookPath(launcher); err != nil {
		if customCommand == "" {
			return errCursorCLIMissing
		}
		return fmt.Errorf("configured Cursor IDE launcher %q not found on PATH: %w", launcher, err)
	}

	args = append(args, projectPath)
	if out, err := exec.Command(launcher, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("cursor IDE launcher %q failed: %w: %s", launcher, err, string(out))
	}
	return nil
}

// WatchAgent watches for Cursor IDE activity and calls the callback with AgentChatSession
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: Starting Cursor IDE activity monitoring",
		"projectPath", projectPath,
		"debugRaw", debugRaw)

	// Create and start watcher
	watcher, err := NewCursorIDEWatcher(projectPath, debugRaw, sessionCallback, defaultCheckInterval)
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

// generateCursorIDESessionName creates a human-readable session name from composer data,
// taking the first candidate text (composer name, then first user message) that survives
// the readable-name transform. Falls back to empty (shouldn't happen with non-empty
// conversations).
func generateCursorIDESessionName(composer *ComposerData) string {
	for _, text := range composerNameCandidates(composer) {
		if name := spi.GenerateReadableName(text); name != "" {
			return name
		}
	}
	return ""
}
