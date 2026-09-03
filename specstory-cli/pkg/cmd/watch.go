package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// truncateSessionID shortens a UUID to first5...last5 for display.
// Full IDs are available in --json output; the short form is enough
// to visually distinguish sessions.
func truncateSessionID(id string) string {
	if len(id) <= 13 {
		return id
	}
	return id[:5] + "..." + id[len(id)-5:]
}

// CreateWatchCommand dynamically creates the watch command with provider information.
// cloudURL binds to the parent's --cloud-url flag so PersistentPreRunE can apply it.
// defaults carries the config-derived flag defaults shared with resume/search.
func CreateWatchCommand(cloudURL *string, defaults SessionFlagDefaults) *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()
	providerList := registry.GetProviderList()

	// Build dynamic examples
	examples := `
# Watch all registered agent providers for activity
specstory watch`

	if len(ids) > 0 {
		examples += "\n\n# Watch for activity from a specific agent"
		for _, id := range ids {
			examples += fmt.Sprintf("\nspecstory watch %s", id)
		}
	}

	examples += `

# Watch with custom output directory
specstory watch --output-dir ~/my-sessions`

	longDesc := `Watch for coding agent activity in the current directory and auto-save markdown files.

Unlike 'run', this command does not launch a coding agent - it only monitors for agent activity.

Use this when you want to run the agent separately, but still want auto-saved markdown files.

By default, 'watch' is for activity from all registered agent providers. Specify a specific agent ID to watch for activity from only that agent.`
	if providerList != "No providers registered" {
		longDesc += "\n\nAvailable provider IDs: " + providerList + "."
	}

	watchCmd := &cobra.Command{
		Use:     "watch [provider-id]",
		Aliases: []string{"w"},
		Short:   "Watch for coding agent activity with auto-save",
		Long:    longDesc,
		Example: examples,
		Args:    cobra.MaximumNArgs(1), // Accept 0 or 1 argument (provider ID)
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			config.EnsureDefaultProjectConfig(configDir)
			slog.Info("Running in watch mode")

			registry := factory.GetRegistry()

			// Read flags from the command. The session-processing flags
			// resolve through the shared helper; watch-specific flags are
			// read individually.
			processing := ResolveProcessingOptions(cmd, true /* isAutosave */, false /* showOutput */)
			jsonOutput, _ := cmd.Flags().GetBool("json")
			outputDir, _ := cmd.Flags().GetString("output-dir")
			flagDebugDir, _ := cmd.Flags().GetString("debug-dir")
			noCloudSync, _ := cmd.Flags().GetBool("no-cloud-sync")
			provenanceEnabled, _ := cmd.Flags().GetBool("provenance")

			// Apply debug dir override from flag if provided
			if flagDebugDir != "" {
				spi.SetDebugBaseDir(flagDebugDir)
			}

			// Apply per-provider user-data-dir overrides before any provider initializes
			// its watchers (they read the override at first lookup).
			userDataDirOverrides, _ := cmd.Flags().GetStringSlice("user-data-dir")
			ApplyUserDataDirOverrides(userDataDirOverrides)

			// Setup output configuration
			config, err := utils.SetupOutputConfig(outputDir, flagDebugDir)
			if err != nil {
				return err
			}
			// Tell cloud sync where .project.json lives (respects --output-dir)
			cloud.SetSpecstoryDir(config.GetSpecstoryDir())

			// Ensure history directory exists for watch mode
			if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
				return err
			}

			// Initialize project identity (needed for cloud sync)
			cwd, err := os.Getwd()
			if err != nil {
				slog.Error("Failed to get current working directory", "error", err)
				return err
			}
			// Read project identity override flags (inherited from root persistent flags)
			projectPathOverride, _ := cmd.Flags().GetString("project-path")
			gitOriginOverride, _ := cmd.Flags().GetString("git-origin")
			// effectiveProjectPath is what providers use for session discovery.
			// When --project-path is set, it resolves to that path; otherwise uses cwd.
			effectiveProjectPath := utils.ResolveProjectPath(projectPathOverride, cwd)
			identityManager := utils.NewProjectIdentityManagerWithOverrides(cwd, config.GetSpecstoryDir(), projectPathOverride, gitOriginOverride)
			if _, err := identityManager.EnsureProjectIdentity(); err != nil {
				// Log error but don't fail the command
				slog.Error("Failed to ensure project identity", "error", err)
			}

			// Check authentication for cloud sync
			CheckAndWarnAuthentication(noCloudSync)

			// Validate that --only-cloud-sync requires authentication
			if processing.OnlyCloudSync && !cloud.IsAuthenticated() {
				return utils.ValidationError{Message: "--only-cloud-sync requires authentication. Please run 'specstory login' first"}
			}

			// Start provenance engine if enabled (used in later phases for event correlation)
			provenanceEngine, provenanceCleanup, err := provenance.StartEngine(provenanceEnabled)
			if err != nil {
				return err
			}
			defer provenanceCleanup()

			if len(registry.ListIDs()) == 0 {
				return fmt.Errorf("no providers registered")
			}

			providersFlag, _ := cmd.Flags().GetStringSlice("providers")
			resolvedIDs, err := ResolveProviderIDs(registry, args, providersFlag)
			if err != nil {
				return err
			}
			providerIDs := registry.ListIDs()
			if len(resolvedIDs) > 0 {
				providerIDs = resolvedIDs
			}

			// Collect provider names for analytics
			providers := make(map[string]spi.Provider)
			for _, id := range providerIDs {
				if provider, err := registry.Get(id); err == nil {
					providers[id] = provider
				} else {
					return fmt.Errorf("no provider %s found", id)
				}
			}
			var providerNames []string
			// Get all provider names from the providers map
			for _, provider := range providers {
				providerNames = append(providerNames, provider.Name())
			}
			analytics.SetAgentProviders(providerNames)

			// Track watch command activation
			analytics.TrackEvent(analytics.EventWatchActivated, nil)

			// Create context for graceful cancellation (Ctrl+C handling)
			// This allows providers to clean up resources when user presses Ctrl+C
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Start filesystem watcher for provenance correlation if enabled (uses signal context for Ctrl+C)
			fsCleanup, err := provenance.StartFSWatcher(ctx, provenanceEngine, cwd)
			if err != nil {
				return err
			}
			defer fsCleanup()

			if !log.IsSilent() && !jsonOutput {
				fmt.Println()
				agentWord := "agents"
				if len(providerNames) == 1 {
					agentWord = "agent"
				}
				fmt.Println("👀 Watching for activity from " + agentWord + ": " + strings.Join(providerNames, ", "))
				fmt.Println("   Press Ctrl+C to stop watching")
				fmt.Println()
			}

			// Keep sessions.db current in real time alongside the markdown writes (nil/no-op
			// if the index can't be opened — never block the watcher on it).
			liveIndex := NewLiveIndexer(cwd)
			defer liveIndex.Close()

			// The per-update output decoration on top of the shared autosave
			// handling: print a console or JSON line per update.
			//
			// Every callback that arrives here is new activity, so none of it is
			// filtered. Providers no longer emit what was already on disk when
			// the watcher started; suppressing a session's first sighting used to
			// swallow the first real update to any session that already had
			// markdown.
			onSaved := func(providerID string, sess *spi.AgentChatSession, fileExisted bool, markdownSize int) {
				if log.IsSilent() {
					return
				}

				// Determine if this was an update or creation
				action := "updated"
				if !fileExisted {
					action = "created"
				}

				// Get timestamps from session data
				startTime := sess.CreatedAt
				endTime := startTime
				if sess.SessionData != nil && sess.SessionData.UpdatedAt != "" {
					endTime = sess.SessionData.UpdatedAt
				}

				// Count messages by role
				userPrompts := 0
				agentActivity := 0
				if sess.SessionData != nil {
					for _, exchange := range sess.SessionData.Exchanges {
						for _, msg := range exchange.Messages {
							if msg.Role == schema.RoleUser {
								userPrompts++
							} else {
								agentActivity++
							}
						}
					}
				}

				// Output the formatted line
				if jsonOutput {
					record := map[string]interface{}{
						"timestamp":          time.Now().Format(time.RFC3339),
						"action":             action,
						"session_id":         sess.SessionID,
						"start_time":         startTime,
						"end_time":           endTime,
						"provider":           providerID,
						"markdown_size":      markdownSize,
						"total_user_prompts": userPrompts,
						"agent_activity":     agentActivity,
					}
					if !processing.OnlyCloudSync {
						record["markdown_file"] = session.BuildSessionFilePath(sess, config.GetHistoryDir(), processing.UseUTC)
					}
					_ = json.NewEncoder(os.Stdout).Encode(record)
				} else {
					emoji := "♻️"
					if action == "created" {
						emoji = "✨"
					}
					activityWord := "activities"
					if agentActivity == 1 {
						activityWord = "activity"
					}
					promptWord := "prompts"
					if userPrompts == 1 {
						promptWord = "prompt"
					}
					fmt.Printf("  %s  %s  %s · %s · %d %s · %d agent %s\n",
						time.Now().Format("15:04:05"),
						emoji,
						providerID,
						truncateSessionID(sess.SessionID),
						userPrompts,
						promptWord,
						agentActivity,
						activityWord)
				}
			}

			// Shared autosave handling (markdown + cloud sync + live index +
			// provenance), decorated with watch's output hook.
			sessionCallback := NewAutosaveCallback(AutosaveDeps{
				Ctx:        ctx,
				Config:     config,
				Processing: processing,
				LiveIndex:  liveIndex,
				Provenance: provenanceEngine,
				OnSaved:    onSaved,
			})

			return utils.WatchProviders(ctx, effectiveProjectPath, providers, processing.DebugRaw, sessionCallback)
		},
	}

	// Shared session-processing flags plus watch's own json output and provider
	// filter. The provider filter stays here rather than in the shared helper
	// because resume and search act on one already-identified session, where
	// filtering by provider has no meaning.
	registerSessionProcessingFlags(watchCmd, cloudURL, defaults)
	watchCmd.Flags().Bool("json", false, "output session updates as JSON lines (one JSON object per line)")
	watchCmd.Flags().String("config-dir", "", "custom directory for the project-level config.toml (default: ./.specstory/cli)")
	AddProvidersFlag(watchCmd)
	AddUserDataDirFlag(watchCmd)

	return watchCmd
}
