package copilotide

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Variant identifies which VS Code distribution a Provider instance targets.
// The Copilot chat storage layout is identical between VS Code and VS Code
// Insiders — only the application identity differs — so a single provider
// implementation serves both when instantiated with the right variant.
type Variant struct {
	ID          string // provider ID used for registration and in generated session data
	AppName     string // user-facing application label (e.g. "VS Code Insiders")
	DataDirName string // application data directory name under the OS config root (e.g. "Code - Insiders")
	Command     string // CLI launcher expected on PATH (e.g. "code-insiders")
}

// VSCode and VSCodeInsiders are the two distributions this provider supports.
var (
	VSCode = Variant{
		ID:          "copilotide",
		AppName:     "VS Code",
		DataDirName: "Code",
		Command:     "code",
	}

	VSCodeInsiders = Variant{
		ID:          "copilotide-insiders",
		AppName:     "VS Code Insiders",
		DataDirName: "Code - Insiders",
		Command:     "code-insiders",
	}

	// VSCodium runs Copilot via a sideloaded VSIX (the extension is not on Open
	// VSX), but the chat session store is VS Code OSS core code, so the storage
	// layout is identical to stock VS Code.
	VSCodium = Variant{
		ID:          "copilotide-vscodium",
		AppName:     "VSCodium",
		DataDirName: "VSCodium",
		Command:     "codium",
	}

	VSCodiumInsiders = Variant{
		ID:          "copilotide-vscodium-insiders",
		AppName:     "VSCodium Insiders",
		DataDirName: "VSCodium - Insiders",
		Command:     "codium-insiders",
	}
)

// Variants returns every VS Code distribution this provider supports.
//
// Exported for the source-tree monitor's Copilot-IDE supervisor
// (specstoryai/getspecstory#257), which enumerates all workspaceStorage
// locations to map chat activity back to repos rather than searching per
// project. It has no caller within this branch yet — it goes live once that
// monitor reaches dev and its `copilotide_monitor` build tag is dropped.
func Variants() []Variant {
	return []Variant{VSCode, VSCodeInsiders, VSCodium, VSCodiumInsiders}
}

// Provider implements the SPI Provider interface for the Copilot chat built
// into a VS Code distribution (stock VS Code or VS Code Insiders).
type Provider struct {
	variant Variant

	// findWorkspaceForReconstruction is the workspace lookup used by the resume
	// flow. Held as an instance field (not a package var) so each variant
	// resolves against its own storage and tests can patch it per instance.
	findWorkspaceForReconstruction func(projectPath string) (*WorkspaceMatch, error)
}

// NewProvider creates a Copilot IDE provider for the given VS Code variant.
func NewProvider(variant Variant) *Provider {
	p := &Provider{variant: variant}
	p.findWorkspaceForReconstruction = func(projectPath string) (*WorkspaceMatch, error) {
		return p.findWorkspaceForProject(projectPath, false)
	}
	return p
}

// Name returns the human-readable name of this provider
func (p *Provider) Name() string {
	return p.variant.AppName + " Copilot IDE"
}

// Check verifies the variant's workspace storage exists and returns info
func (p *Provider) Check(customCommand string) spi.CheckResult {
	slog.Debug("Check: Checking Copilot installation", "app", p.variant.AppName)

	// Check for workspace storage directory
	storagePath := p.workspaceStoragePath()
	if storagePath == "" {
		return spi.CheckResult{
			Success:      false,
			Version:      "",
			Location:     "",
			ErrorMessage: p.variant.AppName + " workspace storage directory not found",
		}
	}

	slog.Debug("Copilot check successful", "app", p.variant.AppName, "storagePath", storagePath)

	return spi.CheckResult{
		Success:      true,
		Version:      p.variant.AppName + " Copilot",
		Location:     storagePath,
		ErrorMessage: "",
	}
}

// DetectAgent checks if Copilot has been used in the given project path
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	slog.Debug("DetectAgent: Checking for Copilot activity", "app", p.variant.AppName, "projectPath", projectPath)

	// Try to find all workspaces matching the project. A project can match several
	// workspace entries (opened directly as a folder AND listed in .code-workspace
	// files), and sessions may live in any of them.
	workspaces, err := p.FindWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "projectPath", projectPath, "error", err)
		if helpOutput {
			fmt.Printf("\n❌ No %s Copilot workspace found for this project\n", p.variant.AppName)
			fmt.Printf("  • Project path: %s\n", projectPath)
			fmt.Printf("  • Workspace storage: %s\n", p.workspaceStoragePath())
			fmt.Printf("  • %s needs to be opened in this directory at least once\n", p.variant.AppName)
			fmt.Println()
		}
		return false
	}

	// Any matching workspace with at least one chat session counts as activity.
	for _, workspace := range workspaces {
		sessionFiles, err := LoadAllSessionFiles(workspace.Dir)
		if err != nil || len(sessionFiles) == 0 {
			slog.Debug("No chat sessions found", "workspace", workspace.Dir, "error", err)
			continue
		}
		slog.Debug("Copilot activity detected",
			"app", p.variant.AppName,
			"workspace", workspace.Dir,
			"sessionCount", len(sessionFiles))
		return true
	}

	slog.Debug("No chat sessions found in any matching workspace",
		"projectPath", projectPath, "workspaceCount", len(workspaces))
	if helpOutput {
		fmt.Printf("\n❌ No %s Copilot chat sessions found\n", p.variant.AppName)
		for _, workspace := range workspaces {
			fmt.Printf("  • Workspace: %s\n", workspace.Dir)
		}
		fmt.Printf("  • Create at least one chat session in %s Copilot\n", p.variant.AppName)
		fmt.Println()
	}
	return false
}

// GetAgentChatSession retrieves a single chat session by ID
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSession", "projectPath", projectPath, "sessionID", sessionID, "debugRaw", debugRaw)

	// Find all workspaces matching the project. The session may live in any of them
	// (e.g. the project was opened both directly and via a .code-workspace file, and
	// the session belongs to a workspace that is not the most recently used one).
	workspaces, err := p.FindWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace: %w", err)
	}

	// Probe each matching workspace newest-first until the session is found. Only a
	// missing session file means "try the next workspace"; any other error (e.g. a
	// corrupt file) is a real failure and is surfaced immediately.
	var session *VSCodeComposer
	var workspaceDir string
	for _, workspace := range workspaces {
		loaded, loadErr := LoadSessionByID(workspace.Dir, sessionID)
		if loadErr != nil {
			if errors.Is(loadErr, ErrSessionNotFound) {
				slog.Debug("Session not in workspace, trying next match",
					"sessionId", sessionID, "workspace", workspace.Dir)
				continue
			}
			return nil, fmt.Errorf("failed to load session: %w", loadErr)
		}
		session = loaded
		workspaceDir = workspace.Dir
		break
	}
	if session == nil {
		return nil, fmt.Errorf("failed to load session: %w: %s (searched %d matching workspaces)",
			ErrSessionNotFound, sessionID, len(workspaces))
	}

	// Load state file (optional) from the same workspace the session was found in
	state, err := LoadStateFile(workspaceDir, sessionID)
	if err != nil {
		slog.Warn("Failed to load state file", "sessionId", sessionID, "error", err)
	}

	// Convert to AgentChatSession
	agentSession := p.ConvertToSessionData(*session, projectPath, state)

	// Write debug files if requested
	if debugRaw {
		if err := WriteDebugFiles(session, sessionID); err != nil {
			slog.Warn("Failed to write debug files", "error", err)
		}
	}

	return &agentSession, nil
}

// sessionFileRef pairs a session file with the workspace it came from, so per-session
// state files can be resolved against the right workspace's chatEditingSessions.
type sessionFileRef struct {
	path         string
	workspaceDir string
}

// collectProjectSessionFiles gathers session files across all matching workspaces,
// iterated newest-first. Deduplication: within a workspace the .jsonl file is preferred
// over the .json file for the same session (matching LoadSessionByID); across workspaces
// the first-seen — i.e. newest — workspace wins for a session ID present in several.
// Per-workspace enumeration failures are logged and skipped so one bad workspace doesn't
// hide sessions in the others; an error is returned only when every workspace failed.
func collectProjectSessionFiles(workspaces []WorkspaceMatch) ([]sessionFileRef, error) {
	seen := make(map[string]bool)
	var refs []sessionFileRef
	var firstErr error
	enumerated := false

	for _, workspace := range workspaces {
		files, err := LoadAllSessionFiles(workspace.Dir)
		if err != nil {
			slog.Warn("Failed to load session files from workspace",
				"workspace", workspace.Dir, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		enumerated = true

		// Pick one file per session ID within this workspace, preferring .jsonl.
		byID := make(map[string]string, len(files))
		for _, file := range files {
			id := GetSessionIDFromPath(file)
			if existing, ok := byID[id]; ok &&
				(strings.HasSuffix(existing, ".jsonl") || !strings.HasSuffix(file, ".jsonl")) {
				continue
			}
			byID[id] = file
		}

		// Iterate the file list (not byID) so output keeps deterministic filename order.
		for _, file := range files {
			id := GetSessionIDFromPath(file)
			if byID[id] != file || seen[id] {
				continue
			}
			seen[id] = true
			refs = append(refs, sessionFileRef{path: file, workspaceDir: workspace.Dir})
		}
	}

	if !enumerated && firstErr != nil {
		return nil, firstErr
	}
	return refs, nil
}

// GetAgentChatSessions retrieves all chat sessions for the given project path,
// aggregated across every matching workspace (a project can match several — opened
// directly as a folder and via .code-workspace files).
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	slog.Debug("GetAgentChatSessions", "projectPath", projectPath, "debugRaw", debugRaw)

	// Find all workspaces matching the project
	workspaces, err := p.FindWorkspacesForProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace: %w", err)
	}

	// Load session files from all matching workspaces (deduplicated, newest workspace wins)
	sessionFiles, err := collectProjectSessionFiles(workspaces)
	if err != nil {
		return nil, fmt.Errorf("failed to load session files: %w", err)
	}

	var sessions []spi.AgentChatSession
	processedCount := 0
	totalCount := len(sessionFiles)

	for _, ref := range sessionFiles {
		composer, err := LoadSessionFile(ref.path)
		if err != nil {
			slog.Warn("Failed to load session", "file", ref.path, "error", err)
			continue
		}

		// Load state file (optional) from the workspace the session file came from
		state, err := LoadStateFile(ref.workspaceDir, composer.SessionID)
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
		session := p.ConvertToSessionData(*composer, projectPath, state)
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
	slog.Debug("ListAgentChatSessions: Loading Copilot session list",
		"app", p.variant.AppName, "projectPath", projectPath)

	// Step 1: Find all workspaces matching the project (sessions may live in any of them)
	workspaces, err := p.FindWorkspacesForProject(projectPath)
	if err != nil {
		slog.Debug("No workspace found for project", "error", err)
		return []spi.SessionMetadata{}, nil // Return empty list if no workspace
	}

	slog.Debug("Found workspaces for project", "workspaceCount", len(workspaces))

	// Step 2: Load session files from all matching workspaces (deduplicated,
	// newest workspace wins for a session ID present in several)
	sessionFiles, err := collectProjectSessionFiles(workspaces)
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
	for _, ref := range sessionFiles {
		composer, err := LoadSessionFile(ref.path)
		if err != nil {
			slog.Warn("Failed to load session file", "file", ref.path, "error", err)
			continue
		}

		// Load state file (from the owning workspace) to check for editing operations
		state, err := LoadStateFile(ref.workspaceDir, composer.SessionID)
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

	slog.Info("Listed Copilot sessions",
		"app", p.variant.AppName,
		"totalFiles", len(sessionFiles),
		"sessionCount", len(metadataList))

	return metadataList, nil
}

// ExecAgentAndWatch opens the VS Code variant at the project path and then watches the
// workspace's chat store, auto-saving session updates via sessionCallback. VS Code
// Copilot is an IDE, not a CLI: there is no child process whose exit ends the session,
// so this blocks until the user interrupts (Ctrl-C) rather than until an agent exits.
//
// The resume flow (resumeSessionID != "") arrives here after ReconstructSession has
// already written the imported session into the workspace's chat store — the "session
// is ready" note only makes sense in that case, not on a plain `specstory run`.
// (Mirrors the Cursor IDE provider's behavior.)
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	if resumeSessionID != "" {
		fmt.Fprintf(os.Stderr, "\nSession is ready in %s. Open the Chat panel to find it.\n", p.variant.AppName)
	}
	if err := p.openApp(projectPath, customCommand); err != nil {
		// Opening is best-effort; a failure here should not surface as a hard error
		// since the user can open the IDE manually and watching still works.
		slog.Debug("Could not open the IDE automatically", "app", p.variant.AppName, "error", err)
		fmt.Fprintf(os.Stderr, "Open %s manually in: %s\n", p.variant.AppName, projectPath)
		if errors.Is(err, errAppCLIMissing) {
			fmt.Fprintf(os.Stderr, "To let SpecStory open the project for you, install the `%s` shell command:\n", p.variant.Command)
			fmt.Fprintf(os.Stderr, "open the command palette in %s (Cmd/Ctrl+Shift+P) and run \"Shell Command: Install '%s' command in PATH\".\n", p.variant.AppName, p.variant.Command)
		}
	}

	fmt.Fprintf(os.Stderr, "Watching %s Copilot sessions in this project — press Ctrl-C to stop.\n", p.variant.AppName)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// The watcher needs the project's workspace entry, which won't exist yet if this
	// project has never been opened in this VS Code variant — the open above creates
	// it a moment later, once the IDE actually opens the folder. Retry until the
	// watcher can start or the user interrupts.
	printedWaiting := false
	for {
		err := p.WatchAgent(ctx, projectPath, debugRaw, sessionCallback)
		if ctx.Err() != nil || err == nil {
			return nil
		}
		if !printedWaiting {
			fmt.Fprintf(os.Stderr, "Waiting for %s to open this project...\n", p.variant.AppName)
			printedWaiting = true
		}
		slog.Debug("Copilot watcher not ready, retrying", "app", p.variant.AppName, "error", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// errAppCLIMissing signals that the variant's CLI launcher is not on PATH. On macOS the
// command is opt-in (installed from the app's command palette), so its absence is an
// expected condition, not a failure — callers use this to print installation guidance
// instead of a generic error.
var errAppCLIMissing = errors.New("the app's shell command is not installed")

// openApp launches the VS Code variant at the given project path. By default it uses
// the variant's own CLI launcher (`code`, `code-insiders`, …) — the only launcher that
// reliably opens the directory as a workspace window (`open -a` on macOS mostly just
// activates an already-running instance on its home screen, so it is deliberately not
// used as a fallback). A custom command (from --command or the matching *_cmd config
// entry) overrides the launcher binary and prepends any extra arguments before the
// project path.
//
// When the default CLI isn't on PATH, errAppCLIMissing is returned so the caller can
// tell the user how to install it; a missing custom launcher returns a plain error,
// since the install guidance only applies to the variant's own command. On Windows the
// launcher is a .cmd shim, which exec.LookPath resolves via PATHEXT.
func (p *Provider) openApp(projectPath, customCommand string) error {
	launcher := p.variant.Command
	var args []string
	if customCommand != "" {
		if parts := spi.SplitCommandLine(customCommand); len(parts) > 0 {
			launcher = parts[0]
			args = parts[1:]
		}
	}

	if _, err := exec.LookPath(launcher); err != nil {
		if customCommand == "" {
			return errAppCLIMissing
		}
		return fmt.Errorf("configured %s launcher %q not found on PATH: %w", p.variant.AppName, launcher, err)
	}

	args = append(args, projectPath)
	if out, err := exec.Command(launcher, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s launcher %q failed: %w: %s", p.variant.AppName, launcher, err, string(out))
	}
	return nil
}

// ListAllAgentChatSessions enumerates every VS Code Copilot session across all
// workspaces, regardless of project. OriginCwd is resolved from each workspace's
// workspace.json (folder URI, or the .code-workspace file path for multi-root
// workspaces — FindWorkspaceForProject matches both back). NativePath is the session
// file itself. Lightweight: sessions are read for metadata only, no SessionData parse.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	slog.Debug("ListAllAgentChatSessions: enumerating all Copilot sessions", "app", p.variant.AppName)

	storagePath := p.workspaceStoragePath()
	if storagePath == "" {
		// No workspace storage means this VS Code variant was never used here — nothing to index.
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

	slog.Info("Listed all Copilot sessions", "app", p.variant.AppName, "count", len(refs))
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

// WatchAgent watches for new/updated chat sessions across ALL workspaces matching the
// project, so activity in any of them (folder-opened or .code-workspace-opened windows)
// is picked up, not just the most recently used one.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Debug("WatchAgent", "projectPath", projectPath, "debugRaw", debugRaw)

	// Find all workspaces matching the project
	workspaces, err := p.FindWorkspacesForProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to find workspace: %w", err)
	}

	workspaceDirs := make([]string, len(workspaces))
	for i, workspace := range workspaces {
		workspaceDirs[i] = workspace.Dir
	}

	// Start watching the chatSessions directory of every matching workspace
	return p.WatchChatSessions(ctx, workspaceDirs, projectPath, debugRaw, sessionCallback)
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
