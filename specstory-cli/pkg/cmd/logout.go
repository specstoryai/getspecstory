// Package cmd contains CLI command implementations for the SpecStory CLI.
package cmd

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
)

// CreateLogoutCommand creates the logout command.
// cloudURL is a pointer to the shared cloudURL flag variable in main.go,
// so PersistentPreRunE can read it to configure the cloud API base URL.
func CreateLogoutCommand(cloudURL *string) *cobra.Command {
	cmd := &cobra.Command{
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

	cmd.Flags().Bool("force", false, "skip confirmation prompt and logout immediately")
	cmd.Flags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = cmd.Flags().MarkHidden("cloud-url") // Hidden flag

	return cmd
}
