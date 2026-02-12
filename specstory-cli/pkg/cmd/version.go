// Package cmd contains CLI command implementations for the SpecStory CLI.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
)

// CreateVersionCommand creates the version command.
// The version string is passed in because it's set at build time in main.go.
func CreateVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
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
}
