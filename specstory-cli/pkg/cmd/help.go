// Package cmd contains CLI command implementations for the SpecStory CLI.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// DisplayLogoAndHelp prints the SpecStory logo followed by the command's help text.
// Exported because it's used by both the help command and the root command's Run handler.
func DisplayLogoAndHelp(cmd *cobra.Command) {
	fmt.Println() // Add visual separation before the logo
	fmt.Println(utils.GetRandomLogo())
	_ = cmd.Help()
}

// CreateHelpCommand creates a custom help command that displays the SpecStory logo.
// rootCmd is needed to look up subcommands when the user types "specstory help <command>".
func CreateHelpCommand(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
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
					DisplayLogoAndHelp(rootCmd)
				} else {
					// Valid command - track the specific topic
					helpTopic = args[0]
					DisplayLogoAndHelp(targetCmd)
				}
			} else {
				// No command specified - general help requested
				DisplayLogoAndHelp(rootCmd)
			}

			// Track analytics after determining the context
			analytics.TrackEvent(analytics.EventHelpCommand, analytics.Properties{
				"help_topic":  helpTopic,
				"help_reason": helpReason,
			})
		},
	}
}
