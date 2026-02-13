// Package cmd contains CLI command implementations for the SpecStory CLI.
package cmd

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
)

// Login command quit constants - commands that cancel the login flow
const (
	quitCommandFull  = "QUIT"
	quitCommandShort = "Q"
	exitCommand      = "EXIT"
)

// CreateLoginCommand creates the login command.
// cloudURL is a pointer to the shared cloudURL flag variable in main.go,
// so PersistentPreRunE can read it to configure the cloud API base URL.
func CreateLoginCommand(cloudURL *string) *cobra.Command {
	cmd := &cobra.Command{
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
				if upperCode == quitCommandFull || upperCode == quitCommandShort || upperCode == exitCommand {
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

	cmd.Flags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = cmd.Flags().MarkHidden("cloud-url") // Hidden flag

	return cmd
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

// openBrowser opens the default browser to the specified URL
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
