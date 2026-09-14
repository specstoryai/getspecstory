package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/updater"
)

// CreateUpdateCommand follows the version command's output and analytics pattern.
func CreateUpdateCommand(version string) *cobra.Command {
	return createUpdateCommand(version, func(ctx context.Context, checkOnly, rollback bool) (updater.Status, error) {
		manager, err := updater.New(version)
		if err != nil {
			return updater.Status{}, err
		}
		if rollback {
			return manager.Rollback(ctx)
		}
		return manager.Run(ctx, checkOnly, false)
	})
}

func createUpdateCommand(version string, runUpdate func(context.Context, bool, bool) (updater.Status, error)) *cobra.Command {
	var checkOnly, rollback bool
	command := &cobra.Command{
		Use: "update", Short: "Update SpecStory to the latest verified release",
		Long: "Update a native or custom SpecStory installation. Homebrew installations use brew upgrade.\nActive sessions keep running; the new version starts on the next launch.",
		Args: cobra.NoArgs,
		// Updating does not require agent configuration or a Cloud login.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			analytics.TrackEvent(analytics.EventUpdateCommand, analytics.Properties{"check_only": checkOnly, "rollback": rollback})
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
			defer cancel()
			status, err := runUpdate(ctx, checkOnly, rollback)
			if err != nil {
				return err
			}
			// The parent pre-run is skipped, so read the inherited flag rather than
			// relying on logging's silent state having been initialized there.
			silent, _ := cmd.Flags().GetBool("silent")
			if silent {
				return nil
			}
			switch {
			case rollback:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restored SpecStory %s. Automatic updates are paused until you run specstory update.\n", status.Installed)
			case checkOnly:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Current: %s\nLatest stable: %s\n", version, status.Latest)
			case status.Installed != "" && status.Installed != version:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Updated SpecStory to %s. It will run on your next launch.\n", status.Installed)
			default:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "SpecStory %s is up to date.\n", version)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&checkOnly, "check", false, "check the latest version without installing")
	command.Flags().BoolVar(&rollback, "rollback", false, "restore the verified previous version and pause automatic updates")
	command.MarkFlagsMutuallyExclusive("check", "rollback")
	return command
}
