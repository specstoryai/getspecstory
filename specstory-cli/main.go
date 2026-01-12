package main

import (
	"bufio" // For reading user terminal input
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/specstoryai/SpecStoryCLI/pkg/analytics"
	"github.com/specstoryai/SpecStoryCLI/pkg/cloud"
	"github.com/specstoryai/SpecStoryCLI/pkg/log"
	"github.com/specstoryai/SpecStoryCLI/pkg/markdown"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi/factory"
	"github.com/specstoryai/SpecStoryCLI/pkg/utils"
)

// The current version of the CLI
var version = "dev" // Replaced with actual version in the production build process

// Flags / Modes / Options

// General Options
var noAnalytics bool    // flag to disable usage analytics
var noVersionCheck bool // flag to skip checking for newer versions
var outputDir string    // custom output directory for markdown and debug files
// Sync Options
var noCloudSync bool // flag to disable cloud sync
var cloudURL string  // custom cloud API URL (hidden flag)
// Authentication Options
var cloudToken string // cloud refresh token for this session only (used by VSC VSIX, bypasses normal login)
// Logging and Debugging Options
var console bool // flag to enable logging to the console
var logFile bool // flag to enable logging to the log file
var debug bool   // flag to enable debug level logging
var silent bool  // flag to enable silent output (no user messages)

// UUID regex pattern: 8-4-4-4-12 hexadecimal characters
var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// pluralSession returns "session" or "sessions" based on count for proper grammar
func pluralSession(count int) string {
	if count == 1 {
		return "session"
	}
	return "sessions"
}

// SyncStats tracks the results of a sync operation
type SyncStats struct {
	TotalSessions   int
	SessionsSkipped int // Already up to date
	SessionsUpdated int // Existed but needed update
	SessionsCreated int // Newly created markdown files
}

// Login command quit constants - commands that cancel the login flow
const (
	QuitCommandFull  = "QUIT"
	QuitCommandShort = "Q"
	ExitCommand      = "EXIT"
)

// validateFlags checks for mutually exclusive flag combinations
func validateFlags() error {
	if console && silent {
		return utils.ValidationError{Message: "cannot use --console and --silent together. These flags are mutually exclusive"}
	}
	if debug && !console && !logFile {
		return utils.ValidationError{Message: "--debug requires either --console or --log to be specified"}
	}
	return nil
}

// validateUUID checks if the given string is a valid UUID format
func validateUUID(uuid string) bool {
	return uuidRegex.MatchString(uuid)
}

// normalizeDeviceCode normalizes and validates a 6-character device code.
// Accepts formats: "ABC123" or "ABC-123" (case insensitive)
// Returns the normalized code (without dash) and whether it's valid
func normalizeDeviceCode(code string) (string, bool) {
	// Remove dash if present (handle xxx-xxx format)
	// Only remove a single dash in the middle position
	if len(code) == 7 && code[3] == '-' {
		code = code[:3] + code[4:]
	}

	// Validate code format (6 alphanumeric characters after normalization)
	// Accept both uppercase and lowercase letters
	if len(code) != 6 {
		return "", false
	}

	for _, ch := range code {
		// Check if character is alphanumeric
		if (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return "", false
		}
	}

	return code, true
}

func checkAndWarnAuthentication() {
	if !noCloudSync && !cloud.IsAuthenticated() && !silent {
		// Check if this was due to a 401 authentication failure
		if cloud.HadAuthFailure() {
			// Show the specific message for auth failures with orange warning and emoji
			slog.Warn("Cloud sync authentication failed (401)")
			log.UserWarn("⚠️ Unable to authenticate with SpecStory Cloud. This could be due to revoked or expired credentials, or network/server issues.\n")
			log.UserMessage("ℹ️ If this persists, run `specstory logout` then `specstory login` to reset your SpecStory Cloud authentication.\n")
		} else {
			// Regular "not authenticated" message
			msg := "⚠️ Cloud sync not available. You're not authenticated."
			slog.Warn(msg)
			log.UserWarn("%s\n", msg)
			log.UserMessage("ℹ️ Use `specstory login` to authenticate, or `--no-cloud-sync` to skip this warning.\n")
		}
	}
}

func displayLogoAndHelp(cmd *cobra.Command) {
	fmt.Println() // Add visual separation before the logo
	fmt.Println(utils.GetRandomLogo())
	_ = cmd.Help()
}

// Opens the default browser to the specified URL, used in the login command to auth the user
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	default:
		slog.Warn("Unsupported platform for opening browser", "platform", runtime.GOOS)
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	slog.Debug("Opening browser", "command", cmd, "url", url)
	return exec.Command(cmd, args...).Start()
}

// createRootCommand dynamically creates the root command with provider information
func createRootCommand() *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()
	providerList := registry.GetProviderList()

	// Build dynamic examples based on registered providers
	examples := `
# Check terminal coding agents installation
specstory check

# Run the default agent with auto-saving
specstory run`

	// Add provider-specific examples if we have providers
	if len(ids) > 0 {
		examples += "\n\n# Run a specific agent with auto-saving"
		for _, id := range ids {
			if provider, _ := registry.Get(id); provider != nil {
				examples += fmt.Sprintf("\nspecstory run %s", id)
			}
		}

		// Use first provider for custom command example
		examples += fmt.Sprintf("\n\n# Run with custom command\nspecstory run %s -c \"/custom/path/to/agent\"", ids[0])
	}

	examples += `

# Generate markdown files for all agent sessions associated with the current directory
specstory sync

# Generate a markdown file for a specific agent session
specstory sync -s <session-id>`

	longDesc := `SpecStory is a wrapper for terminal coding agents that auto-saves markdown files of all your chat interactions.`
	if providerList != "No providers registered" {
		longDesc += "\n\nSupported agents: " + providerList + "."
	}

	return &cobra.Command{
		Use:               "specstory [command]",
		Short:             "SpecStory auto-saves terminal coding agent chat interactions",
		Long:              longDesc,
		Example:           examples,
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Validate flags before any other setup
			if err := validateFlags(); err != nil {
				fmt.Println() // Add visual separation before error message for better CLI readability
				return err
			}

			// Configure logging based on flags
			if console || logFile {
				// Create output config to get proper log path if needed
				var logPath string
				if logFile {
					config, err := utils.SetupOutputConfig(outputDir)
					if err != nil {
						return err
					}
					logPath = config.GetLogPath()
				}

				// Set up logger
				if err := log.SetupLogger(console, logFile, debug, logPath); err != nil {
					return fmt.Errorf("failed to set up logger: %v", err)
				}

				// Log startup information
				slog.Info("=== SpecStory Starting ===")
				slog.Info("Version", "version", version)
				slog.Info("Command line", "args", strings.Join(os.Args, " "))
				if cwd, err := os.Getwd(); err == nil {
					slog.Info("Current working directory", "cwd", cwd)
				}
				slog.Info("========================")
			} else {
				// No logging - set up discard logger
				if err := log.SetupLogger(false, false, false, ""); err != nil {
					return err
				}
			}

			// Set silent mode for user messages
			log.SetSilent(silent)

			// Initialize cloud sync manager
			cloud.InitSyncManager(!noCloudSync)
			cloud.SetSilent(silent)
			cloud.SetClientVersion(version)
			// Set custom cloud URL if provided (otherwise cloud package uses its default)
			if cloudURL != "" {
				cloud.SetAPIBaseURL(cloudURL)
			}

			// If --cloud-token flag was provided, verify the refresh token works
			// This bypasses normal authentication and uses the token for this session only
			if cloudToken != "" {
				slog.Info("Using session-only refresh token from --cloud-token flag")
				if err := cloud.SetSessionRefreshToken(cloudToken); err != nil {
					fmt.Fprintln(os.Stderr) // Visual separation
					fmt.Fprintln(os.Stderr, "❌ Failed to authenticate with the provided token:")
					fmt.Fprintf(os.Stderr, "   %v\n", err)
					fmt.Fprintln(os.Stderr)
					fmt.Fprintln(os.Stderr, "💡 The token may be invalid, expired, or revoked.")
					fmt.Fprintln(os.Stderr, "   Please check your token and try again, or use 'specstory login' for interactive authentication.")
					fmt.Fprintln(os.Stderr)
					return fmt.Errorf("authentication failed with provided token")
				}
				if !silent {
					fmt.Println()
					fmt.Println("🔑 Authenticated using provided refresh token (session-only)")
					fmt.Println()
				}
			}

			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			// Track help command usage (when no command is specified)
			analytics.TrackEvent(analytics.EventHelpCommand, analytics.Properties{
				"help_topic":  "general",
				"help_reason": "requested",
			})
			// If no command is specified, show logo then help
			displayLogoAndHelp(cmd)
		},
	}
}

var rootCmd *cobra.Command

// createRunCommand dynamically creates the run command with provider information
func createRunCommand() *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()
	providerList := registry.GetProviderList()

	// Build dynamic examples
	examples := `
# Run default agent with auto-saving
specstory run`

	if len(ids) > 0 {
		examples += "\n\n# Run specific agent"
		for _, id := range ids {
			examples += fmt.Sprintf("\nspecstory run %s", id)
		}

		// Use first provider for custom command example
		examples += fmt.Sprintf("\n\n# Run with custom command\nspecstory run %s -c \"/custom/path/to/agent\"", ids[0])
	}

	examples += `

# Resume a specific session
specstory run --resume 5c5c2876-febd-4c87-b80c-d0655f1cd3fd

# Run with custom output directory
specstory run --output-dir ~/my-sessions`

	// Determine default agent name
	defaultAgent := "the default agent"
	if len(ids) > 0 {
		if provider, err := registry.Get(ids[0]); err == nil {
			defaultAgent = provider.Name()
		}
	}

	longDesc := fmt.Sprintf(`Launch terminal coding agents in interactive mode with auto-save markdown file generation.

By default, launches %s. Specify a specific agent ID to use a different agent.`, defaultAgent)
	if providerList != "No providers registered" {
		longDesc += "\n\nAvailable provider IDs: " + providerList + "."
	}

	return &cobra.Command{
		Use:     "run [provider-id]",
		Aliases: []string{"r"},
		Short:   "Launch terminal coding agents in interactive mode with auto-save",
		Long:    longDesc,
		Example: examples,
		Args:    cobra.MaximumNArgs(1), // Accept 0 or 1 argument (provider ID)
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Get custom command if provided via flag
			customCmd, _ := cmd.Flags().GetString("command")

			// Validate that -c flag requires a provider
			if customCmd != "" && len(args) == 0 {
				registry := factory.GetRegistry()
				ids := registry.ListIDs()
				example := "specstory run <provider> -c \"/custom/path/to/agent\""
				if len(ids) > 0 {
					example = fmt.Sprintf("specstory run %s -c \"/custom/path/to/agent\"", ids[0])
				}
				return utils.ValidationError{
					Message: "The -c/--command flag requires a provider to be specified.\n" +
						"Example: " + example,
				}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("Running in interactive mode")

			// Get custom command if provided via flag
			customCmd, _ := cmd.Flags().GetString("command")

			// Get the provider
			registry := factory.GetRegistry()
			var providerID string
			if len(args) == 0 {
				// Default to first registered provider
				ids := registry.ListIDs()
				if len(ids) > 0 {
					providerID = ids[0]
				} else {
					return fmt.Errorf("no providers registered")
				}
			} else {
				providerID = args[0]
			}

			provider, err := registry.Get(providerID)
			if err != nil {
				// Provider not found - show helpful error
				fmt.Printf("❌ Provider '%s' is not a valid provider implementation\n\n", providerID)

				ids := registry.ListIDs()
				if len(ids) > 0 {
					fmt.Println("The registered providers are:")
					for _, id := range ids {
						if p, _ := registry.Get(id); p != nil {
							fmt.Printf("  • %s - %s\n", id, p.Name())
						}
					}
					fmt.Println("\nExample: specstory run " + ids[0])
				}
				return err
			}

			slog.Info("Launching agent", "provider", provider.Name())

			// Set the agent provider for analytics
			analytics.SetAgentProviders([]string{provider.Name()})

			// Setup output configuration
			config, err := utils.SetupOutputConfig(outputDir)
			if err != nil {
				return err
			}
			// Ensure history directory exists for interactive mode
			if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
				return err
			}

			// Initialize project identity
			cwd, err := os.Getwd()
			if err != nil {
				slog.Error("Failed to get current working directory", "error", err)
				return err
			}
			identityManager := utils.NewProjectIdentityManager(cwd)
			if _, err := identityManager.EnsureProjectIdentity(); err != nil {
				// Log error but don't fail the command
				slog.Error("Failed to ensure project identity", "error", err)
			}

			// Check authentication for cloud sync
			checkAndWarnAuthentication()
			// Track extension activation
			analytics.TrackEvent(analytics.EventExtensionActivated, nil)

			// Get resume session ID if provided
			resumeSessionID, _ := cmd.Flags().GetString("resume")
			if resumeSessionID != "" {
				resumeSessionID = strings.TrimSpace(resumeSessionID)
				// Note: Different providers may have different session ID formats
				// Let the provider validate its own format
				slog.Info("Resuming session", "sessionId", resumeSessionID)
			}

			// Get debug-raw flag value (must be before callback to capture in closure)
			debugRaw, _ := cmd.Flags().GetBool("debug-raw")

			// This callback pattern enables real-time processing of agent sessions
			// without blocking the agent's execution. As the agent writes updates to its
			// data files, the provider's watcher detects changes and invokes this callback,
			// allowing immediate markdown generation and cloud sync. Errors are logged but
			// don't stop execution because transient failures (e.g., network issues) shouldn't
			// interrupt the user's coding session.
			sessionCallback := func(session *spi.AgentChatSession) {
				if session == nil {
					return
				}

				// Process the session (write markdown and sync to cloud)
				// Don't show output during interactive run mode
				// This is autosave mode (true)
				err := processSingleSession(session, provider, config, false, true, debugRaw)
				if err != nil {
					// Log error but continue - don't fail the whole run
					// In interactive mode, we prioritize keeping the agent running.
					// Failed markdown writes or cloud syncs can be retried later via
					// the sync command, so we just log and continue.
					slog.Error("Failed to process session update",
						"sessionId", session.SessionID,
						"error", err)
				}
			}

			// Execute the agent and watch for updates
			slog.Info("Starting agent execution and monitoring", "provider", provider.Name())
			err = provider.ExecAgentAndWatch(cwd, customCmd, resumeSessionID, debugRaw, sessionCallback)

			if err != nil {
				slog.Error("Agent execution failed", "provider", provider.Name(), "error", err)
			}

			return err
		},
	}
}

var runCmd *cobra.Command

// createSyncCommand dynamically creates the sync command with provider information
func createSyncCommand() *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()
	providerList := registry.GetProviderList()

	// Build dynamic examples
	examples := `
# Sync all agents with activity
specstory sync`

	if len(ids) > 0 {
		examples += "\n\n# Sync specific agent"
		for _, id := range ids {
			examples += fmt.Sprintf("\nspecstory sync %s", id)
		}
	}

	examples += `

# Sync a specific session by UUID
specstory sync -s <session-id>

# Sync all sessions for the current directory, with console output
specstory sync --console

# Sync all sessions for the current directory, with a log file
specstory sync --log`

	longDesc := `Create or update markdown files for the agent sessions in the current working directory.

By default, syncs all registered providers that have activity.
Provide a specific agent ID to sync a specific provider.`
	if providerList != "No providers registered" {
		longDesc += "\n\nAvailable provider IDs: " + providerList + "."
	}

	return &cobra.Command{
		Use:     "sync [provider-id]",
		Aliases: []string{"s"},
		Short:   "Sync markdown files for terminal coding agent sessions",
		Long:    longDesc,
		Example: examples,
		Args:    cobra.MaximumNArgs(1), // Accept 0 or 1 argument (provider ID)
		PreRunE: func(cmd *cobra.Command, args []string) error {
			// Session ID validation is now provider-specific, so no validation here
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get session ID if provided via flag
			sessionID, _ := cmd.Flags().GetString("session")

			// Handle single session sync if -s flag is provided
			if sessionID != "" {
				return syncSingleSession(cmd, args, sessionID)
			}

			slog.Info("Running sync command")
			registry := factory.GetRegistry()

			// Check if user specified a provider
			if len(args) > 0 {
				// Sync specific provider
				return syncSingleProvider(registry, args[0], cmd)
			} else {
				// Sync all providers with activity
				return syncAllProviders(registry, cmd)
			}
		},
	}
}

// syncSingleSession syncs a single session by ID
// args[0] is the optional provider ID
func syncSingleSession(cmd *cobra.Command, args []string, sessionID string) error {
	slog.Info("Running single session sync", "sessionId", sessionID)

	// Get debug-raw flag value
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")

	// Setup output configuration
	config, err := utils.SetupOutputConfig(outputDir)
	if err != nil {
		return err
	}

	// Initialize project identity
	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return err
	}
	identityManager := utils.NewProjectIdentityManager(cwd)
	if _, err := identityManager.EnsureProjectIdentity(); err != nil {
		slog.Error("Failed to ensure project identity", "error", err)
	}

	// Check authentication for cloud sync
	checkAndWarnAuthentication()

	// Ensure history directory exists
	if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
		return err
	}

	registry := factory.GetRegistry()

	// Case A: Provider specified (e.g., "specstory sync <provider> -s <session-id>")
	if len(args) > 0 {
		providerID := args[0]
		provider, err := registry.Get(providerID)
		if err != nil {
			fmt.Printf("❌ Provider '%s' not found\n", providerID)
			return err
		}

		session, err := provider.GetAgentChatSession(cwd, sessionID, debugRaw)
		if err != nil {
			return fmt.Errorf("error getting session from %s: %w", provider.Name(), err)
		}
		if session == nil {
			fmt.Printf("❌ Session '%s' not found in %s\n", sessionID, provider.Name())
			return fmt.Errorf("session not found")
		}

		// Process the session (show output for sync command)
		// This is manual sync mode (false)
		return processSingleSession(session, provider, config, true, false, debugRaw)
	}

	// Case B: No provider specified - try all providers
	providerIDs := registry.ListIDs()
	for _, id := range providerIDs {
		provider, err := registry.Get(id)
		if err != nil {
			continue
		}

		session, err := provider.GetAgentChatSession(cwd, sessionID, debugRaw)
		if err != nil {
			slog.Debug("Error checking provider for session", "provider", id, "error", err)
			continue
		}
		if session != nil {
			// Found the session!
			if !silent {
				fmt.Printf("✅ Found session for %s\n", provider.Name())
			}
			// This is manual sync mode (false)
			return processSingleSession(session, provider, config, true, false, debugRaw)
		}
	}

	// Session not found in any provider
	fmt.Printf("❌ Session '%s' not found in any provider\n", sessionID)
	return fmt.Errorf("session not found")
}

// validateSessionData runs schema validation on SessionData when in debug mode.
// Validation is only performed when debugRaw is true to avoid overhead in normal operation.
// Returns true if validation passed or was skipped, false if validation failed.
func validateSessionData(session *spi.AgentChatSession, debugRaw bool) bool {
	if !debugRaw || session.SessionData == nil {
		return true
	}
	if !session.SessionData.Validate() {
		slog.Warn("SessionData failed schema validation, proceeding anyway",
			"sessionId", session.SessionID)
		return false
	}
	return true
}

// writeDebugSessionData writes debug session data when debugRaw is enabled.
// Logs warnings on failure but does not fail the operation.
func writeDebugSessionData(session *spi.AgentChatSession, debugRaw bool) {
	if !debugRaw || session.SessionData == nil {
		return
	}
	if err := spi.WriteDebugSessionData(session.SessionID, session.SessionData); err != nil {
		slog.Warn("Failed to write debug session data", "sessionId", session.SessionID, "error", err)
	}
}

// processSingleSession writes markdown and triggers cloud sync for a single session
// isAutosave indicates if this is being called from the run command (true) or sync command (false)
// debugRaw enables schema validation (only run in debug mode to avoid overhead)
func processSingleSession(session *spi.AgentChatSession, provider spi.Provider, config utils.OutputConfig, showOutput bool, isAutosave bool, debugRaw bool) error {
	validateSessionData(session, debugRaw)
	writeDebugSessionData(session, debugRaw)

	// Generate markdown from SessionData
	markdownContent, err := markdown.GenerateMarkdownFromAgentSession(session.SessionData, false)
	if err != nil {
		slog.Error("Failed to generate markdown from SessionData", "sessionId", session.SessionID, "error", err)
		return fmt.Errorf("failed to generate markdown: %w", err)
	}

	// Generate filename from timestamp and slug
	timestamp, _ := time.Parse(time.RFC3339, session.CreatedAt)
	timestampStr := timestamp.Format("2006-01-02_15-04-05Z")

	filename := timestampStr
	if session.Slug != "" {
		filename = fmt.Sprintf("%s-%s", timestampStr, session.Slug)
	}
	fileFullPath := filepath.Join(config.GetHistoryDir(), filename+".md")

	if showOutput && !silent {
		fmt.Printf("Processing session %s...", session.SessionID)
	}

	// Check if file already exists with same content
	var outcome string
	identicalContent := false
	fileExists := false
	if existingContent, err := os.ReadFile(fileFullPath); err == nil {
		fileExists = true
		if string(existingContent) == markdownContent {
			identicalContent = true
			slog.Info("Markdown file already exists with same content, skipping write",
				"sessionId", session.SessionID,
				"path", fileFullPath)
		}
	}

	// Write file if needed and track analytics
	if !identicalContent {
		err := os.WriteFile(fileFullPath, []byte(markdownContent), 0644)
		if err != nil {
			// Track write error
			if isAutosave {
				analytics.TrackEvent(analytics.EventAutosaveError, analytics.Properties{
					"session_id": session.SessionID,
					"error":      err.Error(),
				})
			} else {
				analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
					"session_id": session.SessionID,
					"error":      err.Error(),
				})
			}
			return fmt.Errorf("error writing markdown file: %w", err)
		}

		// Track successful write
		if isAutosave {
			if !fileExists {
				// New file created during autosave
				analytics.TrackEvent(analytics.EventAutosaveNew, analytics.Properties{
					"session_id": session.SessionID,
				})
			} else {
				// File updated during autosave
				analytics.TrackEvent(analytics.EventAutosaveSuccess, analytics.Properties{
					"session_id": session.SessionID,
				})
			}
		} else {
			if !fileExists {
				// New file created during manual sync
				analytics.TrackEvent(analytics.EventSyncMarkdownNew, analytics.Properties{
					"session_id": session.SessionID,
				})
			} else {
				// File updated during manual sync
				analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
					"session_id": session.SessionID,
				})
			}
		}

		slog.Info("Successfully wrote file",
			"sessionId", session.SessionID,
			"path", fileFullPath)
	}

	// Trigger cloud sync with provider-specific data
	// Skip sync only if: identical content AND in autosave mode (run command)
	// For manual sync, always call (HEAD check will determine if cloud needs update)
	if !identicalContent || !isAutosave {
		cloud.SyncSessionToCloud(session.SessionID, fileFullPath, markdownContent, []byte(session.RawData), provider.Name(), isAutosave)
	}

	// Determine outcome for user feedback
	if identicalContent {
		outcome = "up to date (skipped)"
	} else if fileExists {
		outcome = "updated"
	} else {
		outcome = "created"
	}

	if showOutput && !silent {
		fmt.Printf(" %s\n", outcome)
		fmt.Println() // Visual separation
	}

	return nil
}

// preloadBulkSessionSizesIfNeeded optimizes bulk syncs by fetching all session sizes upfront.
// This avoids making individual HEAD requests for each session during sync operations.
//
// The function only performs the preload if:
//   - Cloud sync is enabled (noCloudSync flag is false)
//   - User is authenticated with SpecStory Cloud
//   - A valid projectID can be determined from the identity manager
//
// Parameters:
//   - identityManager: Provides project identity (git_id or workspace_id) for the current project
//
// The preloaded sizes are cached in the SyncManager and shared across all providers since they
// use the same projectID. If the bulk fetch fails or if projectID cannot be determined, individual
// sessions will gracefully fall back to HEAD requests during sync.
//
// This function is safe to call multiple times but should typically be called once before processing
// multiple sessions in batch sync operations.
func preloadBulkSessionSizesIfNeeded(identityManager *utils.ProjectIdentityManager) {
	// Skip if cloud sync is disabled or user is not authenticated
	if noCloudSync || !cloud.IsAuthenticated() {
		return
	}

	// Get projectID from identity manager - required for cloud sync
	projectID, err := identityManager.GetProjectID()
	if err != nil {
		slog.Warn("Cannot preload session sizes: failed to get project ID", "error", err)
		return
	}

	// Preload bulk sizes once - shared across all providers since they use same projectID
	if syncMgr := cloud.GetSyncManager(); syncMgr != nil {
		syncMgr.PreloadSessionSizes(projectID)
	} else {
		slog.Warn("Cannot preload session sizes: sync manager is nil despite authentication")
	}
}

// syncProvider performs the actual sync for a single provider
// Returns (sessionCount, error) for analytics tracking
func syncProvider(provider spi.Provider, providerID string, config utils.OutputConfig, debugRaw bool) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return 0, err
	}

	// Get all sessions from the provider
	sessions, err := provider.GetAgentChatSessions(cwd, debugRaw)
	if err != nil {
		return 0, fmt.Errorf("failed to get sessions: %w", err)
	}

	sessionCount := len(sessions)

	if sessionCount == 0 && !silent {
		// This comes after "Parsing..." message, on the same line
		fmt.Printf(", no non-empty sessions found for %s\n", provider.Name())
		fmt.Println()
		return 0, nil
	}

	if !silent {
		fmt.Printf("\nSyncing markdown files for %s", provider.Name())
	}

	// Initialize statistics
	stats := &SyncStats{
		TotalSessions: sessionCount,
	}

	historyPath := config.GetHistoryDir()

	// Process each session
	for i := range sessions {
		session := &sessions[i]
		validateSessionData(session, debugRaw)
		writeDebugSessionData(session, debugRaw)

		// Generate markdown from SessionData
		markdownContent, err := markdown.GenerateMarkdownFromAgentSession(session.SessionData, false)
		if err != nil {
			slog.Error("Failed to generate markdown from SessionData",
				"sessionId", session.SessionID,
				"error", err)
			// Track sync error
			analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
				"session_id": session.SessionID,
				"error":      err.Error(),
			})
			continue
		}

		// Generate filename from timestamp and slug
		timestamp, _ := time.Parse(time.RFC3339, session.CreatedAt)
		timestampStr := timestamp.Format("2006-01-02_15-04-05Z")

		filename := timestampStr
		if session.Slug != "" {
			filename = fmt.Sprintf("%s-%s", timestampStr, session.Slug)
		}
		fileFullPath := filepath.Join(historyPath, filename+".md")

		// Check if file already exists with same content
		identicalContent := false
		fileExists := false
		if existingContent, err := os.ReadFile(fileFullPath); err == nil {
			fileExists = true
			if string(existingContent) == markdownContent {
				identicalContent = true
				slog.Info("Markdown file already exists with same content, skipping write",
					"sessionId", session.SessionID,
					"path", fileFullPath)
			}
		}

		// Write file if needed
		if !identicalContent {
			err := os.WriteFile(fileFullPath, []byte(markdownContent), 0644)
			if err != nil {
				slog.Error("Error writing markdown file",
					"sessionId", session.SessionID,
					"error", err)
				// Track sync error
				analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
					"session_id": session.SessionID,
					"error":      err.Error(),
				})
				continue
			}
			slog.Info("Successfully wrote file",
				"sessionId", session.SessionID,
				"path", fileFullPath)

			// Track successful sync
			if !fileExists {
				analytics.TrackEvent(analytics.EventSyncMarkdownNew, analytics.Properties{
					"session_id": session.SessionID,
				})
			} else {
				analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
					"session_id": session.SessionID,
				})
			}
		}

		// Trigger cloud sync with provider-specific data
		// Manual sync command: perform immediate sync with HEAD check (not autosave mode)
		cloud.SyncSessionToCloud(session.SessionID, fileFullPath, markdownContent, []byte(session.RawData), provider.Name(), false)

		// Update statistics
		if identicalContent {
			stats.SessionsSkipped++
		} else if fileExists {
			stats.SessionsUpdated++
		} else {
			stats.SessionsCreated++
		}

		// Print progress dot
		if !silent {
			fmt.Print(".")
			_ = os.Stdout.Sync()
		}
	}

	// Print newline after progress dots
	if !silent && sessionCount > 0 {
		fmt.Println()

		// Calculate actual total of processed sessions
		actualTotal := stats.SessionsSkipped + stats.SessionsUpdated + stats.SessionsCreated

		// Print summary message with proper pluralization
		fmt.Printf("\n%s✅ %s sync complete!%s 📊 %s%d%s %s processed\n",
			log.ColorBoldGreen, provider.Name(), log.ColorReset,
			log.ColorBoldCyan, actualTotal, log.ColorReset, pluralSession(actualTotal))
		fmt.Println()

		fmt.Printf("  ⏭️ %s%d %s up to date (skipped)%s\n",
			log.ColorCyan, stats.SessionsSkipped, pluralSession(stats.SessionsSkipped), log.ColorReset)
		fmt.Printf("  ♻️ %s%d %s updated%s\n",
			log.ColorYellow, stats.SessionsUpdated, pluralSession(stats.SessionsUpdated), log.ColorReset)
		fmt.Printf("  ✨ %s%d new %s created%s\n",
			log.ColorGreen, stats.SessionsCreated, pluralSession(stats.SessionsCreated), log.ColorReset)
		fmt.Println()
	}

	return sessionCount, nil
}

// syncAllProviders syncs all providers that have activity in the current directory
func syncAllProviders(registry *factory.Registry, cmd *cobra.Command) error {
	// Get debug-raw flag value
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")

	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return err
	}

	providerIDs := registry.ListIDs()
	providersWithActivity := []string{}

	// Check each provider for activity
	for _, id := range providerIDs {
		provider, err := registry.Get(id)
		if err != nil {
			slog.Warn("Failed to get provider", "id", id, "error", err)
			continue
		}

		if provider.DetectAgent(cwd, false) {
			providersWithActivity = append(providersWithActivity, id)
		}
	}

	// If no providers have activity, show helpful message
	if len(providersWithActivity) == 0 {
		if !silent {
			fmt.Println() // Add visual separation
			log.UserWarn("No coding agent activity found for this project directory.\n\n")

			log.UserMessage("We checked for activity in '%s' from the following agents:\n", cwd)
			for _, id := range providerIDs {
				if provider, err := registry.Get(id); err == nil {
					log.UserMessage("- %s\n", provider.Name())
				}
			}
			log.UserMessage("\nBut didn't find any activity.\n\n")

			log.UserMessage("To fix this:\n")
			log.UserMessage("  1. Run 'specstory run' to start the default agent in this directory\n")
			log.UserMessage("  2. Run 'specstory run <agent>' to start a specific agent in this directory\n")
			log.UserMessage("  3. Or run the agent directly first, then try syncing again\n")
			fmt.Println() // Add trailing newline
		}
		return nil
	}

	// Collect provider names for analytics
	var providerNames []string
	for _, id := range providersWithActivity {
		if provider, err := registry.Get(id); err == nil {
			providerNames = append(providerNames, provider.Name())
		}
	}
	analytics.SetAgentProviders(providerNames)

	// Setup output configuration (once for all providers)
	config, err := utils.SetupOutputConfig(outputDir)
	if err != nil {
		return err
	}

	// Initialize project identity (once for all providers)
	identityManager := utils.NewProjectIdentityManager(cwd)
	if _, err := identityManager.EnsureProjectIdentity(); err != nil {
		slog.Error("Failed to ensure project identity", "error", err)
	}

	// Check authentication for cloud sync (once)
	checkAndWarnAuthentication()

	// Ensure history directory exists (once)
	if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
		return err
	}

	// Preload session sizes for batch sync optimization (ONCE for all providers)
	preloadBulkSessionSizesIfNeeded(identityManager)

	// Sync each provider with activity
	totalSessionCount := 0
	var lastError error

	for _, id := range providersWithActivity {
		provider, err := registry.Get(id)
		if err != nil {
			continue
		}

		if !silent {
			fmt.Printf("\nParsing %s sessions", provider.Name())
		}

		sessionCount, err := syncProvider(provider, id, config, debugRaw)
		totalSessionCount += sessionCount

		if err != nil {
			lastError = err
			slog.Error("Error syncing provider", "provider", id, "error", err)
		}
	}

	// Track overall analytics
	if lastError != nil {
		analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
			"provider":      "all",
			"error":         lastError.Error(),
			"session_count": totalSessionCount,
		})
		// Brief delay to ensure error event is sent before exit
		// TODO: This is a workaround for async analytics - should be fixed properly
		time.Sleep(500 * time.Millisecond)
	} else {
		analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
			"provider":      "all",
			"session_count": totalSessionCount,
		})
	}

	return lastError
}

// syncSingleProvider syncs a specific provider
func syncSingleProvider(registry *factory.Registry, providerID string, cmd *cobra.Command) error {
	// Get debug-raw flag value
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")

	provider, err := registry.Get(providerID)
	if err != nil {
		// Provider not found - show helpful error
		fmt.Printf("❌ Provider '%s' is not a valid provider implementation\n\n", providerID)

		ids := registry.ListIDs()
		if len(ids) > 0 {
			fmt.Println("The registered providers are:")
			for _, id := range ids {
				if p, _ := registry.Get(id); p != nil {
					fmt.Printf("  • %s - %s\n", id, p.Name())
				}
			}
			fmt.Println("\nExample: specstory sync " + ids[0])
		}
		return err
	}

	// Set the agent provider for analytics
	analytics.SetAgentProviders([]string{provider.Name()})

	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return err
	}

	// Check if provider has activity, with helpful output if not
	if !provider.DetectAgent(cwd, true) {
		// Provider already output helpful message
		return nil
	}

	// Setup output configuration
	config, err := utils.SetupOutputConfig(outputDir)
	if err != nil {
		return err
	}

	// Initialize project identity
	identityManager := utils.NewProjectIdentityManager(cwd)
	if _, err := identityManager.EnsureProjectIdentity(); err != nil {
		slog.Error("Failed to ensure project identity", "error", err)
	}

	// Check authentication for cloud sync
	checkAndWarnAuthentication()

	// Ensure history directory exists
	if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
		return err
	}

	// Preload session sizes for batch sync optimization
	preloadBulkSessionSizesIfNeeded(identityManager)

	if !silent {
		fmt.Printf("\nParsing %s sessions", provider.Name())
	}

	// Perform the sync
	sessionCount, syncErr := syncProvider(provider, providerID, config, debugRaw)

	// Track analytics
	if syncErr != nil {
		analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
			"provider":      providerID,
			"error":         syncErr.Error(),
			"session_count": sessionCount,
		})
		// Brief delay to ensure error event is sent before exit
		// TODO: This is a workaround for async analytics - should be fixed properly
		time.Sleep(500 * time.Millisecond)
	} else {
		analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
			"provider":      providerID,
			"session_count": sessionCount,
		})
	}

	return syncErr
}

var syncCmd *cobra.Command

// Command to show current version information
var versionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v", "ver"},
	Short:   "Show SpecStory version information",
	Run: func(cmd *cobra.Command, args []string) {
		// Track version command usage
		analytics.TrackEvent(analytics.EventVersionCommand, analytics.Properties{
			"version": version,
		})
		fmt.Printf("%s (SpecStory)\n", version)
	},
}

// createCheckCommand dynamically creates the check command with provider information
func createCheckCommand() *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()

	// Build dynamic examples
	examples := `
# Check all coding agents
specstory check`

	if len(ids) > 0 {
		examples += "\n\n# Check specific coding agent"
		for _, id := range ids {
			examples += fmt.Sprintf("\nspecstory check %s", id)
		}

		// Use first provider for custom command example
		examples += fmt.Sprintf("\n\n# Check a specific coding agent with a custom command\nspecstory check %s -c \"/custom/path/to/agent\"", ids[0])
	}

	return &cobra.Command{
		Use:   "check [provider-id]",
		Short: "Check if terminal coding agents are properly installed",
		Long: `Check if terminal coding agents are properly installed and can be invoked by SpecStory.

By default, checks all registered coding agents providers.
Specify a specific agent ID to check only a specific coding agent.`,
		Example: examples,
		Args:    cobra.MaximumNArgs(1), // Accept 0 or 1 argument
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("Running in check-install mode")
			registry := factory.GetRegistry()

			// Get custom command if provided via flag
			customCmd, _ := cmd.Flags().GetString("command")

			// Validate that -c flag requires a provider
			if customCmd != "" && len(args) == 0 {
				registry := factory.GetRegistry()
				ids := registry.ListIDs()
				example := "specstory check <provider> -c \"/custom/path/to/agent\""
				if len(ids) > 0 {
					example = fmt.Sprintf("specstory check %s -c \"/custom/path/to/agent\"", ids[0])
				}
				return utils.ValidationError{
					Message: "The -c/--command flag requires a provider to be specified.\n" +
						"Example: " + example,
				}
			}

			if len(args) == 0 {
				// Check all providers
				return checkAllProviders(registry)
			} else {
				// Check specific provider
				return checkSingleProvider(registry, args[0], customCmd)
			}
		},
	}
}

var checkCmd *cobra.Command

// printDivider prints a divider line for visual separation
func printDivider() {
	fmt.Println("\n--------")
}

// checkSingleProvider checks a specific provider
func checkSingleProvider(registry *factory.Registry, providerID, customCmd string) error {
	provider, err := registry.Get(providerID)
	if err != nil {
		// Provider not found - show helpful error
		fmt.Printf("❌ Provider '%s' is not a valid provider implementation\n\n", providerID)

		ids := registry.ListIDs()
		if len(ids) > 0 {
			fmt.Println("The registered providers are:")
			for _, id := range ids {
				if p, _ := registry.Get(id); p != nil {
					fmt.Printf("  • %s - %s\n", id, p.Name())
				}
			}
			fmt.Println("\nExample: specstory check " + ids[0])
		}
		return err
	}

	// Set the agent provider for analytics
	analytics.SetAgentProviders([]string{provider.Name()})

	// Run the check
	result := provider.Check(customCmd)

	// Display results with the nice formatting
	if result.Success {
		fmt.Printf("\n✨ %s is installed and ready! ✨\n\n", provider.Name())
		fmt.Printf("  📦 Version: %s\n", result.Version)
		fmt.Printf("  📍 Location: %s\n", result.Location)
		fmt.Printf("  ✅ Status: All systems go!\n\n")

		fmt.Println("🚀 Ready to sync your sessions! 💪")
		normalizedID := strings.ToLower(providerID)
		fmt.Printf("   • specstory run %s\n", normalizedID)
		fmt.Println("   • specstory sync - Save markdown files for existing sessions")
		fmt.Println()

		return nil
	} else {
		fmt.Printf("\n❌ %s check failed!\n", provider.Name())
		if result.ErrorMessage != "" {
			fmt.Printf("\n%s\n", result.ErrorMessage)
		}
		return errors.New("check failed")
	}
}

// checkAllProviders checks all registered providers
func checkAllProviders(registry *factory.Registry) error {
	// Sort for consistent output
	ids := registry.ListIDs()

	// Collect all provider names for analytics
	var providerNames []string
	for _, id := range ids {
		if provider, err := registry.Get(id); err == nil {
			providerNames = append(providerNames, provider.Name())
		}
	}
	analytics.SetAgentProviders(providerNames)

	anySuccess := false
	type providerInfo struct {
		id   string
		name string
	}
	var successfulProviders []providerInfo
	first := true

	for _, id := range ids {
		provider, _ := registry.Get(id)
		// Invoke Check() here to keep registry limited to registration/lookup
		result := provider.Check("")

		// Add divider between providers (but not before the first one)
		if !first {
			printDivider()
		}
		first = false

		if result.Success {
			anySuccess = true
			successfulProviders = append(successfulProviders, providerInfo{id: id, name: provider.Name()})
			fmt.Printf("\n✨ %s is installed and ready! ✨\n\n", provider.Name())
			fmt.Printf("  📦 Version: %s\n", result.Version)
			fmt.Printf("  📍 Location: %s\n", result.Location)
			fmt.Printf("  ✅ Status: All systems go!\n")
		} else {
			fmt.Printf("\n❌ %s check failed!\n\n", provider.Name())
			if result.ErrorMessage != "" {
				// Show just first line of error for summary view
				lines := strings.Split(result.ErrorMessage, "\n")
				fmt.Printf("  Error: %s\n", strings.TrimSpace(lines[0]))
			}
		}
	}

	// Show ready message if at least one provider is working
	if anySuccess {
		printDivider()
		fmt.Println("\n🚀 Ready to sync your sessions! 💪")
		for _, info := range successfulProviders {
			fmt.Printf("   • specstory run %s\n", info.id)
		}
		fmt.Println("   • specstory sync - Save markdown files for existing sessions")
		fmt.Println()
	} else {
		printDivider()
		fmt.Println("\n⚠️  No providers are currently available")
		fmt.Println("   Install at least one provider to use SpecStory")
		fmt.Println("\n💡 Tip: Use 'specstory check <provider>' for detailed installation help")

		// Try to show an example with the first registered provider ID
		ids := registry.ListIDs()
		if len(ids) > 0 {
			fmt.Printf("   Example: specstory check %s\n", ids[0])
		} else {
			fmt.Println("   Example: specstory check <provider>")
		}
	}

	// Return error only if ALL providers failed
	if !anySuccess {
		return errors.New("check failed")
	}

	return nil
}

// Command to log in to SpecStory Cloud
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to SpecStory Cloud",
	Long:  `Log in to SpecStory Cloud to enable cloud sync functionality.`,
	Example: `
# Log in
specstory login`,
	RunE: func(cmd *cobra.Command, args []string) error {
		slog.Info("Running login command")

		// Check if user is already authenticated
		if cloud.IsAuthenticated() {
			// Get user details
			username, loginTime := cloud.AuthenticatedAs()

			// Already logged in - be friendly and show who they are!
			fmt.Println()
			fmt.Println("🔐 You're already logged in!")
			fmt.Println()

			// Show user details if available
			if username != "" {
				fmt.Printf("👤 Logged in as: %s\n", username)
			}
			if loginTime != "" {
				// Parse and format the login time
				if t, err := time.Parse(time.RFC3339, loginTime); err == nil {
					fmt.Printf("🕐 Logged in at: %s (UTC)\n", t.Format("2006-01-02 15:04:05"))
				} else {
					// Fallback to raw time if parsing fails
					fmt.Printf("🕐 Logged in at: %s\n", loginTime)
				}
			}

			fmt.Println()
			fmt.Println("🚀 Ready to sync with SpecStory Cloud! 💪")
			fmt.Println()
			return nil
		}

		// User is not authenticated - start login flow
		slog.Info("Starting login flow")
		analytics.TrackEvent(analytics.EventLoginAttempted, nil)
		loginURL := cloud.GetAPIBaseURL() + "/cli-login"

		fmt.Println()
		fmt.Println("🌐 Opening your browser to log in to SpecStory Cloud...")
		fmt.Println()

		// Try to open the browser
		if err := openBrowser(loginURL); err != nil {
			slog.Warn("Failed to open browser automatically", "error", err)
			// Don't fail, just inform the user
		}

		// Display the URL for manual access
		fmt.Println("📋 If your browser didn't open, please visit:")
		fmt.Printf("   %s\n", loginURL)
		fmt.Println()

		// Loop to allow retrying code entry
		reader := bufio.NewReader(os.Stdin)

		// Track number of invalid attempts
		const maxAttempts = 5
		var invalidAttempts int

		for {
			fmt.Println("🔑 Enter the 6-character code shown in your browser (or 'quit' to cancel):")
			fmt.Print("   Code: ")

			// Read the code from user input
			code, err := reader.ReadString('\n')
			if err != nil {
				slog.Error("Failed to read authentication code", "error", err)
				analytics.TrackEvent(analytics.EventLoginFailed, analytics.Properties{
					"error": err.Error(),
					"stage": "reading_code",
				})
				return fmt.Errorf("failed to read authentication code: %w", err)
			}

			// Trim whitespace
			code = strings.TrimSpace(code)

			// Check if user wants to quit (case-insensitive)
			upperCode := strings.ToUpper(code)
			if upperCode == QuitCommandFull || upperCode == QuitCommandShort || upperCode == ExitCommand {
				slog.Info("Login cancelled by user")
				analytics.TrackEvent(analytics.EventLoginCancelled, nil)
				fmt.Println()
				fmt.Println("👋 Login cancelled.")
				fmt.Println()
				return nil
			}

			// Normalize and validate the device code
			normalizedCode, valid := normalizeDeviceCode(code)
			if !valid {
				invalidAttempts++
				slog.Debug("Invalid code format entered", "original", code, "attempt", invalidAttempts)

				if invalidAttempts >= maxAttempts {
					analytics.TrackEvent(analytics.EventLoginFailed, analytics.Properties{
						"error": "max_attempts_exceeded",
						"stage": "code_validation",
					})
					return fmt.Errorf("maximum login attempts exceeded")
				}

				fmt.Println()
				fmt.Println("❌ Invalid code format. The code should be 6 alphanumeric characters.")
				fmt.Println("   Examples: Ab1c23 or Ab1-c23")
				fmt.Println()
				continue // Try again
			}

			// Valid code format - proceed with authentication
			// Format the code for display (add dash back for readability)
			displayCode := normalizedCode[:3] + "-" + normalizedCode[3:]
			slog.Info("Valid authentication code received", "code", displayCode)

			fmt.Println()
			fmt.Printf("✅ Code received: %s\n", displayCode)
			fmt.Println("🔄 Authenticating...")
			fmt.Println()

			// Exchange code for authentication tokens
			if err := cloud.LoginWithDeviceCode(normalizedCode); err != nil {
				invalidAttempts++
				slog.Error("Failed to authenticate with device code", "error", err)
				analytics.TrackEvent(analytics.EventLoginFailed, analytics.Properties{
					"error": err.Error(),
					"stage": "device_login",
				})

				// Check if it's an invalid code error
				if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "expired") {
					if invalidAttempts >= maxAttempts {
						analytics.TrackEvent(analytics.EventLoginFailed, analytics.Properties{
							"error": "max_attempts_exceeded",
							"stage": "device_login",
						})
						return fmt.Errorf("maximum login attempts exceeded")
					}

					fmt.Println("❌ Authentication failed: " + err.Error())
					fmt.Println()
					fmt.Println("Please try entering the code again.")
					fmt.Println()
					continue // Let user try again
				}

				// Other errors are fatal
				return fmt.Errorf("authentication failed: %w", err)
			}

			// Get user details to show who they logged in as
			username, _ := cloud.AuthenticatedAs()

			fmt.Println("🎉 Success! You're now logged in to SpecStory Cloud!")
			if username != "" {
				fmt.Printf("👤 Logged in as: %s\n", username)
			}
			fmt.Println()
			fmt.Println("🚀 Ready to sync your sessions to SpecStory Cloud! 💪")
			fmt.Println("   • specstory run  - Launch terminal coding agents with auto-sync'ing")
			fmt.Println("   • specstory sync - Sync markdown files for existing sessions")
			fmt.Println()

			analytics.TrackEvent(analytics.EventLoginSuccess, analytics.Properties{
				"user":   username,
				"method": "device_code",
			})
			slog.Info("Login flow completed successfully", "user", username)
			return nil
		}
	},
}

// Command to log out from SpecStory Cloud
var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out from SpecStory Cloud",
	Long:  `Log out from SpecStory Cloud by removing authentication credentials.`,
	Example: `
# Log out
specstory logout

# Log out without a confirmation prompt
specstory logout --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		slog.Info("Running logout command")

		// Check if user is authenticated
		if !cloud.IsAuthenticated() {
			// Not logged in - but still clean up any auth file that might exist
			_ = cloud.Logout() // Always attempt to remove the file

			// Not logged in - be friendly and funky!
			fmt.Println()
			fmt.Println("🎉 Good news! You're not logged in!")
			fmt.Println("✨ Nothing to log out from - All is well in the universe! 💫")
			fmt.Println()
			return nil
		}

		// Get the force flag value
		force, _ := cmd.Flags().GetBool("force")

		// If not forcing, ask for confirmation
		if !force {
			// User is logged in - ask for confirmation with style
			fmt.Println()
			fmt.Println("🔐 You're currently logged in to SpecStory Cloud.")
			fmt.Println("🤔 Are you sure you want to log out?")
			fmt.Println()
			fmt.Print("Type 'yes' to confirm logout, or anything else to stay connected: ")

			// Read user input
			reader := bufio.NewReader(os.Stdin)
			input, err := reader.ReadString('\n')
			if err != nil {
				slog.Error("Failed to read user input", "error", err)
				return fmt.Errorf("failed to read confirmation: %w", err)
			}

			// Trim whitespace and convert to lowercase
			input = strings.TrimSpace(strings.ToLower(input))

			if input != "yes" {
				// User chose not to logout - celebrate their decision!
				fmt.Println()
				fmt.Println("🎊 Awesome! You're staying connected!")
				fmt.Println("🚀 Keep on building amazing things! 💪")
				fmt.Println()
				return nil
			}
		}

		// Proceed with logout
		if err := cloud.Logout(); err != nil {
			slog.Error("Logout failed", "error", err)
			// Track logout error
			analytics.TrackEvent(analytics.EventLogout, analytics.Properties{
				"success": false,
				"forced":  force,
				"error":   err.Error(),
			})
			fmt.Println()
			fmt.Println("❌ Oops! Something went wrong during logout:")
			fmt.Printf("   %v\n", err)
			fmt.Println()
			return err
		}

		// Track successful logout
		analytics.TrackEvent(analytics.EventLogout, analytics.Properties{
			"success": true,
			"forced":  force,
		})

		// Success!
		fmt.Println()
		fmt.Println("👋 Successfully logged out from SpecStory Cloud.")
		fmt.Println("💝 Thanks for using SpecStory! See you again soon! ✨")
		fmt.Println()

		return nil
	},
}

// Custom help command, needed over built-in help command to display our logo
var helpCmd = &cobra.Command{
	Use:     "help [command]",
	Aliases: []string{"h"},
	Short:   "Help about any command",
	Run: func(cmd *cobra.Command, args []string) {
		// Track help command usage with context about why help was shown
		helpTopic := "general"
		helpReason := "requested"

		// If a subcommand is specified, determine if it's valid
		if len(args) > 0 {
			targetCmd, _, err := rootCmd.Find(args)
			if err != nil {
				// Unknown command - track as error-triggered help
				helpReason = "unknown_command"
				fmt.Printf("Unknown command: %s\n", args[0])
				displayLogoAndHelp(rootCmd)
			} else {
				// Valid command - track the specific topic
				helpTopic = args[0]
				displayLogoAndHelp(targetCmd)
			}
		} else {
			// No command specified - general help requested
			displayLogoAndHelp(rootCmd)
		}

		// Track analytics after determining the context
		analytics.TrackEvent(analytics.EventHelpCommand, analytics.Properties{
			"help_topic":  helpTopic,
			"help_reason": helpReason,
		})
	},
}

// Main entry point for the CLI
func main() {
	// Parse critical flags early by manually checking os.Args
	// This is necessary because cobra's ParseFlags doesn't work correctly before subcommands are added
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--no-usage-analytics":
			noAnalytics = true
		case "--console":
			console = true
		case "--log":
			logFile = true
		case "--debug":
			debug = true
		case "--silent":
			silent = true
		case "--no-version-check":
			noVersionCheck = true
		}
		// Handle --output-dir=value format
		if strings.HasPrefix(arg, "--output-dir=") {
			outputDir = strings.TrimPrefix(arg, "--output-dir=")
		}
	}

	// Set up logging early before creating commands (which access the registry)
	if console || logFile {
		var logPath string
		if logFile {
			config, _ := utils.SetupOutputConfig(outputDir)
			logPath = config.GetLogPath()
		}
		_ = log.SetupLogger(console, logFile, debug, logPath)
	} else {
		// Set up discard logger to prevent default slog output
		_ = log.SetupLogger(false, false, false, "")
	}

	// NOW create the commands - after logging is configured
	rootCmd = createRootCommand()
	runCmd = createRunCommand()
	syncCmd = createSyncCommand()
	checkCmd = createCheckCommand()

	// Set version for the automatic version flag
	rootCmd.Version = version

	// Override the default version template to match our version command output
	rootCmd.SetVersionTemplate("{{.Version}} (SpecStory)")

	// Set our custom help command (for "specstory help")
	rootCmd.SetHelpCommand(helpCmd)

	// Add the subcommands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)

	// Global flags available on all commands
	rootCmd.PersistentFlags().BoolVar(&console, "console", false, "enable error/warn/info output to stdout")
	rootCmd.PersistentFlags().BoolVar(&logFile, "log", false, "write error/warn/info output to ./.specstory/debug/debug.log")
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug-level output (requires --console or --log)")
	rootCmd.PersistentFlags().BoolVar(&noAnalytics, "no-usage-analytics", false, "disable usage analytics")
	rootCmd.PersistentFlags().BoolVar(&silent, "silent", false, "suppress all non-error output")
	rootCmd.PersistentFlags().BoolVar(&noVersionCheck, "no-version-check", false, "skip checking for newer versions")
	rootCmd.PersistentFlags().StringVar(&cloudToken, "cloud-token", "", "use a SpecStory Cloud refresh token for this session (bypasses login)")
	_ = rootCmd.PersistentFlags().MarkHidden("cloud-token") // Hidden flag

	// Command-specific flags
	syncCmd.Flags().StringP("session", "s", "", "optional session ID to sync (provider-specific format)")
	syncCmd.Flags().StringVar(&outputDir, "output-dir", "", "custom output directory for markdown and debug files (default: ./.specstory/history)")
	syncCmd.Flags().BoolVar(&noCloudSync, "no-cloud-sync", false, "disable cloud sync functionality")
	syncCmd.Flags().StringVar(&cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = syncCmd.Flags().MarkHidden("cloud-url") // Hidden flag
	syncCmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = syncCmd.Flags().MarkHidden("debug-raw") // Hidden flag

	runCmd.Flags().StringP("command", "c", "", "custom agent execution command for the provider")
	runCmd.Flags().String("resume", "", "resume a specific session by ID")
	runCmd.Flags().StringVar(&outputDir, "output-dir", "", "custom output directory for markdown and debug files (default: ./.specstory/history)")
	runCmd.Flags().BoolVar(&noCloudSync, "no-cloud-sync", false, "disable cloud sync functionality")
	runCmd.Flags().StringVar(&cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = runCmd.Flags().MarkHidden("cloud-url") // Hidden flag
	runCmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = runCmd.Flags().MarkHidden("debug-raw") // Hidden flag

	checkCmd.Flags().StringP("command", "c", "", "custom agent execution command for the provider")

	logoutCmd.Flags().Bool("force", false, "skip confirmation prompt and logout immediately")
	logoutCmd.Flags().StringVar(&cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = logoutCmd.Flags().MarkHidden("cloud-url") // Hidden flag

	loginCmd.Flags().StringVar(&cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = loginCmd.Flags().MarkHidden("cloud-url") // Hidden flag

	// Initialize analytics with the full CLI command (unless disabled)
	slog.Debug("Analytics initialization check", "noAnalytics", noAnalytics, "flag_should_disable", noAnalytics)
	if !noAnalytics {
		slog.Debug("Initializing analytics")
		fullCommand := strings.Join(os.Args, " ")
		if err := analytics.Init(fullCommand, version); err != nil {
			// Log error but don't fail - analytics should not break the app
			slog.Warn("Failed to initialize analytics", "error", err)
		}
		defer func() { _ = analytics.Close() }() // Analytics errors shouldn't break the app
	} else {
		slog.Debug("Analytics disabled by --no-usage-analytics flag")
	}

	// Check for updates (blocking)
	utils.CheckForUpdates(version, noVersionCheck, silent)

	// Ensure proper cleanup and logging on exit
	defer func() {
		if r := recover(); r != nil {
			slog.Error("=== SpecStory PANIC ===", "panic", r)
			// Still try to wait for cloud sync even on panic
			_ = cloud.Shutdown(cloud.CloudSyncTimeout)
			log.CloseLogger()
			panic(r) // Re-panic after logging
		}
		// Wait for cloud sync operations to complete before exiting
		cloudStats := cloud.Shutdown(cloud.CloudSyncTimeout)

		// Track cloud sync analytics if we have stats
		if cloudStats != nil {
			// Calculate total attempted (all sessions that started sync)
			totalCloudSessions := cloudStats.SessionsAttempted

			// Track cloud sync completion event
			if cloudStats.SessionsUpdated > 0 || cloudStats.SessionsCreated > 0 || cloudStats.SessionsErrored > 0 || cloudStats.SessionsTimedOut > 0 {
				analytics.TrackEvent(analytics.EventCloudSyncComplete, analytics.Properties{
					"sessions_created":   cloudStats.SessionsCreated,
					"sessions_updated":   cloudStats.SessionsUpdated,
					"sessions_skipped":   cloudStats.SessionsSkipped,
					"sessions_errored":   cloudStats.SessionsErrored,
					"sessions_timed_out": cloudStats.SessionsTimedOut,
					"total":              totalCloudSessions,
				})
			}

			// Display cloud sync stats if not in silent mode
			if !silent && totalCloudSessions > 0 {
				// Determine if sync was complete or incomplete based on errors/timeouts
				if cloudStats.SessionsErrored > 0 || cloudStats.SessionsTimedOut > 0 {
					fmt.Printf("❌ Cloud sync incomplete! 📊 %s%d%s %s processed\n",
						log.ColorBoldCyan, totalCloudSessions, log.ColorReset, pluralSession(int(totalCloudSessions)))
				} else {
					fmt.Printf("☁️  Cloud sync complete! 📊 %s%d%s %s processed\n",
						log.ColorBoldCyan, totalCloudSessions, log.ColorReset, pluralSession(int(totalCloudSessions)))
				}
				fmt.Println()

				fmt.Printf("  ⏭️ %s%d %s up to date (skipped)%s\n",
					log.ColorCyan, cloudStats.SessionsSkipped, pluralSession(int(cloudStats.SessionsSkipped)), log.ColorReset)
				fmt.Printf("  ♻️ %s%d %s updated%s\n",
					log.ColorYellow, cloudStats.SessionsUpdated, pluralSession(int(cloudStats.SessionsUpdated)), log.ColorReset)
				fmt.Printf("  ✨ %s%d new %s created%s\n",
					log.ColorGreen, cloudStats.SessionsCreated, pluralSession(int(cloudStats.SessionsCreated)), log.ColorReset)

				// Only show errors if there were any
				if cloudStats.SessionsErrored > 0 {
					fmt.Printf("  ❌ %s%d %s errored%s\n",
						log.ColorRed, cloudStats.SessionsErrored, pluralSession(int(cloudStats.SessionsErrored)), log.ColorReset)
				}

				// Only show timed out sessions if there were any
				if cloudStats.SessionsTimedOut > 0 {
					fmt.Printf("  ⏱️  %s%d %s timed out%s\n",
						log.ColorRed, cloudStats.SessionsTimedOut, pluralSession(int(cloudStats.SessionsTimedOut)), log.ColorReset)
				}
				fmt.Println()
			}
		}

		if console || logFile {
			slog.Info("=== SpecStory Exiting ===", "code", 0, "status", "normal termination")
		}
		log.CloseLogger()
	}()

	if err := fang.Execute(context.Background(), rootCmd, fang.WithVersion(version)); err != nil {
		// Check if we're running the check command by looking at the executed command
		executedCmd, _, _ := rootCmd.Find(os.Args[1:])
		if executedCmd == checkCmd {
			if console || logFile {
				slog.Error("=== SpecStory Exiting ===", "code", 2, "status", "agent execution failure")
				slog.Error("Error", "error", err)
			}
			// For check command, the error details are handled by checkSingleProvider/checkAllProviders
			// So we just exit silently here
			_ = cloud.Shutdown(cloud.CloudSyncTimeout)
			os.Exit(2)
		} else {
			if console || logFile {
				slog.Error("=== SpecStory Exiting ===", "code", 1, "status", "error")
				slog.Error("Error", "error", err)
			}
			fmt.Fprintln(os.Stderr) // Visual separation makes error output more noticeable

			// Only show usage for actual command/flag errors from Cobra
			// These are errors like "unknown command", "unknown flag", "invalid argument", etc.
			// For all other errors (authentication, network, file system, etc.), we should NOT show usage
			errMsg := err.Error()
			isCommandError := strings.Contains(errMsg, "unknown command") ||
				strings.Contains(errMsg, "unknown flag") ||
				strings.Contains(errMsg, "invalid argument") ||
				strings.Contains(errMsg, "required flag") ||
				strings.Contains(errMsg, "accepts") || // e.g., "accepts 1 arg(s), received 2"
				strings.Contains(errMsg, "no such flag") ||
				strings.Contains(errMsg, "flag needs an argument")

			if isCommandError {
				_ = rootCmd.Usage() // Ignore error; we're exiting anyway
				fmt.Println()       // Add visual separation after usage for better CLI readability
			}
			_ = cloud.Shutdown(cloud.CloudSyncTimeout)
			os.Exit(1)
		}
	}
}
