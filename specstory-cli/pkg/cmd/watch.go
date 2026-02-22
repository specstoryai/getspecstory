package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	gosync "sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
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
func CreateWatchCommand(cloudURL *string) *cobra.Command {
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
			slog.Info("Running in watch mode")

			registry := factory.GetRegistry()

			// Read flags from the command
			debugRaw, _ := cmd.Flags().GetBool("debug-raw")
			useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
			useUTC := !useLocalTimezone
			jsonOutput, _ := cmd.Flags().GetBool("json")
			outputDir, _ := cmd.Flags().GetString("output-dir")
			noCloudSync, _ := cmd.Flags().GetBool("no-cloud-sync")
			onlyCloudSync, _ := cmd.Flags().GetBool("only-cloud-sync")
			provenanceEnabled, _ := cmd.Flags().GetBool("provenance")

			// Setup output configuration
			config, err := utils.SetupOutputConfig(outputDir)
			if err != nil {
				return err
			}

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
			if _, err := utils.NewProjectIdentityManager(cwd).EnsureProjectIdentity(); err != nil {
				// Log error but don't fail the command
				slog.Error("Failed to ensure project identity", "error", err)
			}

			// Check authentication for cloud sync
			CheckAndWarnAuthentication(noCloudSync)

			// Validate that --only-cloud-sync requires authentication
			if onlyCloudSync && !cloud.IsAuthenticated() {
				return utils.ValidationError{Message: "--only-cloud-sync requires authentication. Please run 'specstory login' first"}
			}

			// Start provenance engine if enabled (used in later phases for event correlation)
			provenanceEngine, provenanceCleanup, err := provenance.StartEngine(provenanceEnabled)
			if err != nil {
				return err
			}
			defer provenanceCleanup()

			providerIDs := registry.ListIDs()
			if len(providerIDs) == 0 {
				return fmt.Errorf("no providers registered")
			}
			if len(args) > 0 {
				providerIDs = []string{args[0]}
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

			// Track sessions we've seen to suppress initial-scan output.
			// Existing sessions found at startup get their markdown refreshed
			// but don't produce output — only new activity does.
			var seenMu gosync.Mutex
			seenSessions := make(map[string]bool)

			// Session callback for watch mode output
			sessionCallback := func(providerID string, sess *spi.AgentChatSession) {
				// Check if markdown file already exists to determine if this is an update or creation
				fileFullPath := session.BuildSessionFilePath(sess, config.GetHistoryDir(), useUTC)
				_, fileExistsErr := os.Stat(fileFullPath)
				fileExists := fileExistsErr == nil

				// Process the session (write markdown and sync to cloud)
				// Don't show output during watch mode
				// This is autosave mode (true)
				markdownSize, err := session.ProcessSingleSession(sess, config, onlyCloudSync, false, true, debugRaw, useUTC)
				if err != nil {
					// Log error but continue - don't fail the whole watch
					// In watch mode, we prioritize keeping the watcher running.
					// Failed markdown writes or cloud syncs can be retried later via
					// the sync command, so we just log and continue.
					slog.Error("Failed to process session update",
						"sessionId", sess.SessionID,
						"provider", providerID,
						"error", err)
					return
				}

				// Suppress output for existing sessions seen for the first time (initial scan).
				// New sessions (!fileExists) always get output since they represent real activity.
				seenMu.Lock()
				firstSeen := !seenSessions[sess.SessionID]
				seenSessions[sess.SessionID] = true
				seenMu.Unlock()
				if firstSeen && fileExists {
					return
				}

				// Output formatted line to stdout for watch mode
				if !log.IsSilent() {
					// Determine if this was an update or creation
					action := "updated"
					if !fileExists {
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
						if !onlyCloudSync {
							record["markdown_file"] = fileFullPath
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

				// Push agent events to provenance engine for correlation
				provenance.ProcessEvents(ctx, provenanceEngine, sess)
			}

			return utils.WatchProviders(ctx, cwd, providers, debugRaw, sessionCallback)
		},
	}

	// Watch-specific flags
	watchCmd.Flags().Bool("json", false, "output session updates as JSON lines (one JSON object per line)")
	watchCmd.Flags().String("output-dir", "", "custom output directory for markdown and debug files (default: ./.specstory/history)")
	watchCmd.Flags().Bool("only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
	watchCmd.Flags().Bool("no-cloud-sync", false, "disable cloud sync functionality")
	watchCmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = watchCmd.Flags().MarkHidden("debug-raw") // Hidden flag
	watchCmd.Flags().Bool("local-time-zone", false, "use local timezone for file name and content timestamps (when not present: UTC)")
	watchCmd.Flags().Bool("provenance", false, "enable AI provenance tracking (correlate file changes to agent activity)")
	_ = watchCmd.Flags().MarkHidden("provenance") // Hidden flag
	watchCmd.Flags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = watchCmd.Flags().MarkHidden("cloud-url") // Hidden flag

	return watchCmd
}
