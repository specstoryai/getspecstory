// Package cmd contains CLI command implementations for the SpecStory CLI.
package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// CreateCheckCommand dynamically creates the check command with provider information.
func CreateCheckCommand() *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()

	// Build dynamic examples
	var examplesBuilder strings.Builder
	examplesBuilder.WriteString(`
# Check all coding agents
specstory check`)

	if len(ids) > 0 {
		examplesBuilder.WriteString("\n\n# Check specific coding agent")
		for _, id := range ids {
			fmt.Fprintf(&examplesBuilder, "\nspecstory check %s", id)
		}

		// Use first provider for custom command example
		fmt.Fprintf(&examplesBuilder, "\n\n# Check a specific coding agent with a custom command\nspecstory check %s -c \"/custom/path/to/agent\"", ids[0])
	}
	examples := examplesBuilder.String()

	cmd := &cobra.Command{
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

	cmd.Flags().StringP("command", "c", "", "custom agent execution command for the provider")

	return cmd
}

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
