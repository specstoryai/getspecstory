package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	cmdpkg "github.com/specstoryai/getspecstory/specstory-cli/pkg/cmd"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"
	sessionpkg "github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// The current version of the CLI
var version = "dev" // Replaced with actual version in the production build process

// Flags / Modes / Options

// General Options
var noAnalytics bool    // flag to disable usage analytics
var noVersionCheck bool // flag to skip checking for newer versions
var outputDir string    // custom output directory for markdown and debug files
// Sync Options
var noCloudSync bool   // flag to disable cloud sync
var onlyCloudSync bool // flag to skip local markdown writes and only sync to cloud
var printToStdout bool // flag to output markdown to stdout instead of saving (only with -s flag)
var cloudURL string    // custom cloud API URL (hidden flag)
// Authentication Options
var cloudToken string // cloud refresh token for this session only (used by VSC VSIX, bypasses normal login)
// Logging and Debugging Options
var console bool // flag to enable logging to the console
var logFile bool // flag to enable logging to the log file
var debug bool   // flag to enable debug level logging
var silent bool  // flag to enable silent output (no user messages)
// Provenance Options
var provenanceEnabled bool // flag to enable AI provenance tracking

// Run Mode State
var lastRunSessionID string // tracks the session ID from the most recent run command for deep linking

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

// validateFlags checks for mutually exclusive flag combinations
func validateFlags() error {
	if console && silent {
		return utils.ValidationError{Message: "cannot use --console and --silent together. These flags are mutually exclusive"}
	}
	if debug && !console && !logFile {
		return utils.ValidationError{Message: "--debug requires either --console or --log to be specified"}
	}
	if onlyCloudSync && noCloudSync {
		return utils.ValidationError{Message: "cannot use --only-cloud-sync and --no-cloud-sync together. These flags are mutually exclusive"}
	}
	if printToStdout && onlyCloudSync {
		return utils.ValidationError{Message: "cannot use --print and --only-cloud-sync together. These flags are mutually exclusive"}
	}
	if printToStdout && console {
		return utils.ValidationError{Message: "cannot use --print and --console together. Console debug output would interleave with markdown on stdout"}
	}
	return nil
}

// validateUUID checks if the given string is a valid UUID format
func validateUUID(uuid string) bool {
	return uuidRegex.MatchString(uuid)
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

# Generate markdown files for specific agent sessions
specstory sync -s <session-id>
specstory sync -s <session-id-1> -s <session-id-2>

# Watch for any agent activity in the current directory and generate markdown files
specstory watch`

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

			// Validate that --only-cloud-sync requires authentication
			if onlyCloudSync && !cloud.IsAuthenticated() {
				return utils.ValidationError{Message: "--only-cloud-sync requires authentication. Please run 'specstory login' first"}
			}

			return nil
		},
		Run: func(c *cobra.Command, args []string) {
			// Track help command usage (when no command is specified)
			analytics.TrackEvent(analytics.EventHelpCommand, analytics.Properties{
				"help_topic":  "general",
				"help_reason": "requested",
			})
			// If no command is specified, show logo then help
			cmdpkg.DisplayLogoAndHelp(c)
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

			// Create context for graceful cancellation (Ctrl+C handling)
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Start provenance infrastructure before the agent so all file changes are captured
			provenanceEngine, provenanceCleanup, err := provenance.StartEngine(provenanceEnabled)
			if err != nil {
				return err
			}
			defer provenanceCleanup()

			fsCleanup, err := provenance.StartFSWatcher(ctx, provenanceEngine, cwd)
			if err != nil {
				return err
			}
			defer fsCleanup()

			// Check authentication for cloud sync
			cmdpkg.CheckAndWarnAuthentication(noCloudSync)
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
			useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
			useUTC := !useLocalTimezone

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

				// Track the session ID for deep linking on exit
				lastRunSessionID = session.SessionID

				// Process the session (write markdown and sync to cloud)
				// Don't show output during interactive run mode
				// This is autosave mode (true)
				_, err := sessionpkg.ProcessSingleSession(session, config, onlyCloudSync, false, true, debugRaw, useUTC)
				if err != nil {
					// Log error but continue - don't fail the whole run
					// In interactive mode, we prioritize keeping the agent running.
					// Failed markdown writes or cloud syncs can be retried later via
					// the sync command, so we just log and continue.
					slog.Error("Failed to process session update",
						"sessionId", session.SessionID,
						"error", err)
				}

				// Push agent events to provenance engine for correlation
				provenance.ProcessEvents(ctx, provenanceEngine, session)
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

# Sync multiple sessions
specstory sync -s <session-id-1> -s <session-id-2> -s <session-id-3>

# Output session markdown to stdout without saving
specstory sync -s <session-id> --print

# Output multiple sessions to stdout
specstory sync -s <session-id-1> -s <session-id-2> --print

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
			// Validate that --print requires -s flag
			sessionIDs, _ := cmd.Flags().GetStringSlice("session")
			if printToStdout && len(sessionIDs) == 0 {
				return utils.ValidationError{Message: "--print requires the -s/--session flag to specify which sessions to output"}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get session IDs if provided via flag
			sessionIDs, _ := cmd.Flags().GetStringSlice("session")

			// Handle specific session sync if -s flag is provided
			if len(sessionIDs) > 0 {
				return syncSpecificSessions(cmd, args, sessionIDs)
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

// syncSpecificSessions syncs one or more sessions by their IDs
// When printToStdout is set, outputs markdown to stdout instead of saving to files.
// args[0] is the optional provider ID
func syncSpecificSessions(cmd *cobra.Command, args []string, sessionIDs []string) error {
	if len(sessionIDs) == 1 {
		slog.Info("Running single session sync", "sessionId", sessionIDs[0])
	} else {
		slog.Info("Running multiple session sync", "sessionCount", len(sessionIDs))
	}

	// Get debug-raw flag value
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")
	useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
	useUTC := !useLocalTimezone

	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return err
	}

	// Setup file output and cloud sync (not needed for --print mode)
	var config utils.OutputConfig
	if !printToStdout {
		config, err = utils.SetupOutputConfig(outputDir)
		if err != nil {
			return err
		}

		identityManager := utils.NewProjectIdentityManager(cwd)
		if _, err := identityManager.EnsureProjectIdentity(); err != nil {
			slog.Error("Failed to ensure project identity", "error", err)
		}

		cmdpkg.CheckAndWarnAuthentication(noCloudSync)

		if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
			return err
		}
	}

	registry := factory.GetRegistry()

	// Track statistics for summary output
	var successCount, notFoundCount, errorCount int
	var lastError error

	// Resolve provider once if specified (fail fast if provider not found)
	var specifiedProvider spi.Provider
	if len(args) > 0 {
		providerID := args[0]
		provider, err := registry.Get(providerID)
		if err != nil {
			if !printToStdout {
				fmt.Printf("❌ Provider '%s' not found\n", providerID)
			}
			return fmt.Errorf("provider '%s' not found: %w", providerID, err)
		}
		specifiedProvider = provider
	}

	// Process each session ID
	var printedSessions int // tracks sessions printed to stdout for separator logic
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			continue // Skip empty session IDs
		}

		// Find session across providers
		var session *spi.AgentChatSession

		// Case A: Provider was specified - use it directly
		if specifiedProvider != nil {
			session, err = specifiedProvider.GetAgentChatSession(cwd, sessionID, debugRaw)
			if err != nil {
				if !printToStdout {
					fmt.Printf("❌ Error getting session '%s' from %s: %v\n", sessionID, specifiedProvider.Name(), err)
				}
				slog.Error("Error getting session from provider", "sessionId", sessionID, "provider", specifiedProvider.Name(), "error", err)
				errorCount++
				lastError = err
				continue
			}
			if session == nil {
				if !printToStdout {
					fmt.Printf("❌ Session '%s' not found in %s\n", sessionID, specifiedProvider.Name())
				}
				slog.Warn("Session not found in provider", "sessionId", sessionID, "provider", specifiedProvider.Name())
				notFoundCount++
				continue
			}
		} else {
			// Case B: No provider specified - try all providers
			providerIDs := registry.ListIDs()
			for _, id := range providerIDs {
				provider, err := registry.Get(id)
				if err != nil {
					continue
				}

				session, err = provider.GetAgentChatSession(cwd, sessionID, debugRaw)
				if err != nil {
					slog.Debug("Error checking provider for session", "provider", id, "sessionId", sessionID, "error", err)
					continue
				}
				if session != nil {
					if !silent && !printToStdout {
						fmt.Printf("✅ Found session '%s' for %s\n", sessionID, provider.Name())
					}
					break // Found it, don't check other providers
				}
			}

			if session == nil {
				if !printToStdout {
					fmt.Printf("❌ Session '%s' not found in any provider\n", sessionID)
				}
				slog.Warn("Session not found in any provider", "sessionId", sessionID)
				notFoundCount++
				continue
			}
		}

		// Process the found session
		if printToStdout {
			sessionpkg.ValidateSessionData(session, debugRaw)
			sessionpkg.WriteDebugSessionData(session, debugRaw)

			markdownContent, err := sessionpkg.GenerateMarkdownFromAgentSession(session.SessionData, false, useUTC)
			if err != nil {
				slog.Error("Failed to generate markdown", "sessionId", session.SessionID, "error", err)
				analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
					"session_id": session.SessionID,
					"error":      err.Error(),
					"mode":       "print",
				})
				errorCount++
				lastError = err
				continue
			}

			// Separate multiple sessions with a horizontal rule
			if printedSessions > 0 {
				fmt.Print("\n---\n\n")
			}
			fmt.Print(markdownContent)
			printedSessions++
			successCount++
			analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
				"session_id": session.SessionID,
				"mode":       "print",
			})
		} else {
			// Normal sync: write to file and optionally cloud sync
			if _, err := sessionpkg.ProcessSingleSession(session, config, onlyCloudSync, true, false, debugRaw, useUTC); err != nil {
				errorCount++
				lastError = err
			} else {
				successCount++
			}
		}
	}

	// Print summary if multiple sessions were processed (not for --print mode)
	if !printToStdout && len(sessionIDs) > 1 && !silent {
		fmt.Println()
		fmt.Println("📊 Session sync summary:")
		fmt.Printf("  ✅ %d %s successfully synced\n", successCount, pluralSession(successCount))
		if notFoundCount > 0 {
			fmt.Printf("  ❌ %d %s not found\n", notFoundCount, pluralSession(notFoundCount))
		}
		if errorCount > 0 {
			fmt.Printf("  ❌ %d %s failed with errors\n", errorCount, pluralSession(errorCount))
		}
		fmt.Println()
	}

	// Return error if any sessions failed
	if errorCount > 0 || (notFoundCount > 0 && successCount == 0) {
		if lastError != nil {
			return lastError
		}
		return fmt.Errorf("%d %s not found", notFoundCount, pluralSession(notFoundCount))
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
func syncProvider(provider spi.Provider, providerID string, config utils.OutputConfig, debugRaw bool, useUTC bool) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return 0, err
	}

	// Create progress callback for parsing phase
	// The callback updates the "Parsing..." line in place with [n/m] progress
	var parseProgress spi.ProgressCallback
	if !silent {
		providerName := provider.Name()
		parseProgress = func(current, total int) {
			fmt.Printf("\rParsing %s sessions [%d/%d]", providerName, current, total)
			_ = os.Stdout.Sync()
		}
	}

	// Get all sessions from the provider
	sessions, err := provider.GetAgentChatSessions(cwd, debugRaw, parseProgress)
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
		sessionpkg.ValidateSessionData(session, debugRaw)
		sessionpkg.WriteDebugSessionData(session, debugRaw)

		// Generate markdown from SessionData
		markdownContent, err := sessionpkg.GenerateMarkdownFromAgentSession(session.SessionData, false, useUTC)
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
		fileFullPath := sessionpkg.BuildSessionFilePath(session, historyPath, useUTC)

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

		// Write file if needed (skip if only-cloud-sync is enabled)
		if !onlyCloudSync {
			if !identicalContent {
				// Ensure history directory exists (handles deletion during long-running sync)
				if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
					slog.Error("Failed to ensure history directory", "error", err)
					continue
				}
				err := os.WriteFile(fileFullPath, []byte(markdownContent), 0644)
				if err != nil {
					slog.Error("Error writing markdown file",
						"sessionId", session.SessionID,
						"error", err)
					// Track sync error
					analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
						"session_id":      session.SessionID,
						"error":           err.Error(),
						"only_cloud_sync": onlyCloudSync,
					})
					continue
				}
				slog.Info("Successfully wrote file",
					"sessionId", session.SessionID,
					"path", fileFullPath)

				// Track successful sync
				if !fileExists {
					analytics.TrackEvent(analytics.EventSyncMarkdownNew, analytics.Properties{
						"session_id":      session.SessionID,
						"only_cloud_sync": onlyCloudSync,
					})
				} else {
					analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
						"session_id":      session.SessionID,
						"only_cloud_sync": onlyCloudSync,
					})
				}
			}

			// Update statistics for normal mode
			if identicalContent {
				stats.SessionsSkipped++
			} else if fileExists {
				stats.SessionsUpdated++
			} else {
				stats.SessionsCreated++
			}
		} else {
			// In cloud-only mode, count as skipped since no local file operation occurred
			stats.SessionsSkipped++
			slog.Info("Skipping local file write (only-cloud-sync mode)",
				"sessionId", session.SessionID)
		}

		// Trigger cloud sync with provider-specific data
		// Manual sync command: perform immediate sync with HEAD check (not autosave mode)
		// In only-cloud-sync mode: always sync
		cloud.SyncSessionToCloud(session.SessionID, fileFullPath, markdownContent, []byte(session.RawData), provider.Name(), false)

		// Print progress with [n/m] format
		if !silent {
			fmt.Printf("\rSyncing markdown files for %s [%d/%d]", provider.Name(), i+1, sessionCount)
			_ = os.Stdout.Sync()
		}
	}

	// Print newline after progress
	if !silent && sessionCount > 0 && !onlyCloudSync {
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
	useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
	useUTC := !useLocalTimezone

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
	cmdpkg.CheckAndWarnAuthentication(noCloudSync)

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

		sessionCount, err := syncProvider(provider, id, config, debugRaw, useUTC)
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
	useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
	useUTC := !useLocalTimezone

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
	cmdpkg.CheckAndWarnAuthentication(noCloudSync)

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
	sessionCount, syncErr := syncProvider(provider, providerID, config, debugRaw, useUTC)

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
	watchCmd := cmdpkg.CreateWatchCommand(&cloudURL)
	syncCmd = createSyncCommand()
	listCmd := cmdpkg.CreateListCommand()
	checkCmd := cmdpkg.CreateCheckCommand()
	versionCmd := cmdpkg.CreateVersionCommand(version)
	loginCmd := cmdpkg.CreateLoginCommand(&cloudURL)
	logoutCmd := cmdpkg.CreateLogoutCommand(&cloudURL)

	// Set version for the automatic version flag
	rootCmd.Version = version

	// Override the default version template to match our version command output
	rootCmd.SetVersionTemplate("{{.Version}} (SpecStory)")

	// Set our custom help command (for "specstory help")
	helpCmd := cmdpkg.CreateHelpCommand(rootCmd)
	rootCmd.SetHelpCommand(helpCmd)

	// Add the subcommands
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(listCmd)
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
	syncCmd.Flags().StringSliceP("session", "s", []string{}, "optional session IDs to sync (can be specified multiple times, provider-specific format)")
	syncCmd.Flags().BoolVar(&printToStdout, "print", false, "output session markdown to stdout instead of saving (requires -s flag)")
	syncCmd.Flags().StringVar(&outputDir, "output-dir", "", "custom output directory for markdown and debug files (default: ./.specstory/history)")
	syncCmd.Flags().BoolVar(&noCloudSync, "no-cloud-sync", false, "disable cloud sync functionality")
	syncCmd.Flags().BoolVar(&onlyCloudSync, "only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
	syncCmd.Flags().StringVar(&cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = syncCmd.Flags().MarkHidden("cloud-url") // Hidden flag
	syncCmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = syncCmd.Flags().MarkHidden("debug-raw") // Hidden flag
	syncCmd.Flags().BoolP("local-time-zone", "", false, "use local timezone for file name and content timestamps (when not present: UTC)")

	runCmd.Flags().BoolVar(&provenanceEnabled, "provenance", false, "enable AI provenance tracking (correlate file changes to agent activity)")
	_ = runCmd.Flags().MarkHidden("provenance") // Hidden flag
	runCmd.Flags().StringP("command", "c", "", "custom agent execution command for the provider")
	runCmd.Flags().String("resume", "", "resume a specific session by ID")
	runCmd.Flags().StringVar(&outputDir, "output-dir", "", "custom output directory for markdown and debug files (default: ./.specstory/history)")
	runCmd.Flags().BoolVar(&noCloudSync, "no-cloud-sync", false, "disable cloud sync functionality")
	runCmd.Flags().BoolVar(&onlyCloudSync, "only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
	runCmd.Flags().StringVar(&cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = runCmd.Flags().MarkHidden("cloud-url") // Hidden flag
	runCmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = runCmd.Flags().MarkHidden("debug-raw") // Hidden flag
	runCmd.Flags().BoolP("local-time-zone", "", false, "use local timezone for file name and content timestamps (when not present: UTC)")

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
				fmt.Println() // Visual separation from provider sync output

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

				// Display link to SpecStory Cloud (deep link to session if from run command)
				cwd, cwdErr := os.Getwd()
				if cwdErr == nil {
					identityManager := utils.NewProjectIdentityManager(cwd)
					if projectID, err := identityManager.GetProjectID(); err == nil {
						fmt.Printf("💡 Search and chat with your AI conversation history at:\n")
						if lastRunSessionID != "" {
							// Deep link to the specific session from run command
							fmt.Printf("   %shttps://cloud.specstory.com/projects/%s/sessions/%s%s\n\n",
								log.ColorBoldCyan, projectID, lastRunSessionID, log.ColorReset)
						} else {
							// Link to project overview for sync command
							fmt.Printf("   %shttps://cloud.specstory.com/projects/%s%s\n\n",
								log.ColorBoldCyan, projectID, log.ColorReset)
						}
					}
				}
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
