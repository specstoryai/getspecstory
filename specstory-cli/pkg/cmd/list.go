// Package cmd contains CLI command implementations for the SpecStory CLI.
package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

// Column widths for table output (excluding NAME which is dynamic).
const (
	sessionIDWidth = 36 // UUID length
	providerWidth  = 12 // Enough for longer provider names
	createdWidth   = 17 // "Jan 02, 2006 15:04"
	columnSpacing  = 2  // Spaces between columns
)

// listFlags holds the flags for the list command.
type listFlags struct {
	json bool
}

var flags listFlags

// sessionMetadataWithProvider embeds SessionMetadata and adds provider information.
// Used when listing sessions from all providers to show which provider each session came from.
type sessionMetadataWithProvider struct {
	spi.SessionMetadata        // Embedded struct - promotes all fields with their JSON tags
	Provider            string `json:"provider"` // Provider ID (e.g., "claude", "cursor", "codex")
}

// CreateListCommand creates the list command with provider information.
func CreateListCommand() *cobra.Command {
	registry := factory.GetRegistry()
	ids := registry.ListIDs()
	providerList := registry.GetProviderList()

	// Build dynamic examples
	var examplesBuilder strings.Builder
	examplesBuilder.WriteString(`
# List all sessions from all agents
specstory list`)

	if len(ids) > 0 {
		examplesBuilder.WriteString("\n\n# List sessions from specific agent")
		for _, id := range ids {
			fmt.Fprintf(&examplesBuilder, "\nspecstory list %s", id)
		}
	}

	examplesBuilder.WriteString(`

# Output as JSON (for programmatic use)
specstory list --json | jq`)
	examples := examplesBuilder.String()

	longDesc := `List all sessions showing session ID, creation date, and name.

By default, outputs a human-readable table. Use --json for machine-readable output.

By default, lists sessions from all registered providers that have activity.
Provide a specific agent ID to list sessions from only that provider.`
	if providerList != "No providers registered" {
		longDesc += "\n\nAvailable provider IDs: " + providerList + "."
	}

	cmd := &cobra.Command{
		Use:     "list [provider-id]",
		Aliases: []string{"ls"},
		Short:   "List all sessions for terminal coding agents",
		Long:    longDesc,
		Example: examples,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("Running list command")

			if len(args) > 0 {
				return listSingleProvider(registry, args[0])
			}
			return listAllProviders(registry)
		},
	}

	cmd.Flags().BoolVar(&flags.json, "json", false, "Output as JSON (default is human-readable table)")

	return cmd
}

// listSingleProvider lists sessions from a specific provider.
func listSingleProvider(registry *factory.Registry, providerID string) error {
	provider, err := registry.Get(providerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Provider '%s' is not a valid provider implementation\n\n", providerID)

		ids := registry.ListIDs()
		if len(ids) > 0 {
			fmt.Fprintln(os.Stderr, "The registered providers are:")
			for _, id := range ids {
				if p, _ := registry.Get(id); p != nil {
					fmt.Fprintf(os.Stderr, "  - %s - %s\n", id, p.Name())
				}
			}
			fmt.Fprintf(os.Stderr, "\nExample: specstory list %s\n", ids[0])
		}
		return err
	}

	analytics.SetAgentProviders([]string{provider.Name()})

	projectPath, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return err
	}

	if !provider.DetectAgent(projectPath, true) {
		return nil
	}

	sessions, err := provider.ListAgentChatSessions(projectPath)
	if err != nil {
		return fmt.Errorf("failed to list sessions for %s: %w", provider.Name(), err)
	}

	sortSessionMetadataOldestFirst(sessions)

	// Convert to sessionMetadataWithProvider for unified output handling
	sessionsWithProvider := make([]sessionMetadataWithProvider, len(sessions))
	for i, s := range sessions {
		sessionsWithProvider[i] = sessionMetadataWithProvider{
			SessionMetadata: s,
			Provider:        providerID,
		}
	}

	if err := outputSessions(sessionsWithProvider); err != nil {
		return err
	}

	analytics.TrackEvent(analytics.EventListSessions, analytics.Properties{
		"provider":      providerID,
		"session_count": len(sessions),
	})

	return nil
}

// listAllProviders lists sessions from all providers that have activity.
func listAllProviders(registry *factory.Registry) error {
	projectPath, err := os.Getwd()
	if err != nil {
		slog.Error("Failed to get current working directory", "error", err)
		return err
	}

	providerIDs := registry.ListIDs()
	providersWithActivity := []string{}

	for _, id := range providerIDs {
		provider, err := registry.Get(id)
		if err != nil {
			slog.Warn("Failed to get provider", "id", id, "error", err)
			continue
		}

		if provider.DetectAgent(projectPath, false) {
			providersWithActivity = append(providersWithActivity, id)
		}
	}

	if len(providersWithActivity) == 0 {
		if !log.IsSilent() {
			fmt.Fprintln(os.Stderr)
			log.UserWarn("No coding agent activity found for this project directory.\n\n")

			log.UserMessage("We checked for activity in '%s' from the following agents:\n", projectPath)
			for _, id := range providerIDs {
				if provider, err := registry.Get(id); err == nil {
					log.UserMessage("- %s\n", provider.Name())
				}
			}
			log.UserMessage("\nBut didn't find any activity.\n")
		}

		if flags.json {
			fmt.Println("[]")
		}
		return nil
	}

	var providerNames []string
	for _, id := range providersWithActivity {
		if provider, err := registry.Get(id); err == nil {
			providerNames = append(providerNames, provider.Name())
		}
	}
	analytics.SetAgentProviders(providerNames)

	allSessions := []sessionMetadataWithProvider{}
	var lastError error

	for _, id := range providersWithActivity {
		provider, err := registry.Get(id)
		if err != nil {
			continue
		}

		sessions, err := provider.ListAgentChatSessions(projectPath)
		if err != nil {
			lastError = err
			slog.Error("Error listing sessions for provider", "provider", id, "error", err)
			continue
		}

		for _, session := range sessions {
			allSessions = append(allSessions, sessionMetadataWithProvider{
				SessionMetadata: session,
				Provider:        id,
			})
		}
	}

	sortSessionMetadataWithProviderOldestFirst(allSessions)

	if err := outputSessions(allSessions); err != nil {
		return err
	}

	analytics.TrackEvent(analytics.EventListSessions, analytics.Properties{
		"provider":      "all",
		"session_count": len(allSessions),
	})

	return lastError
}

// outputSessions outputs sessions as JSON or a human-readable table.
func outputSessions(sessions []sessionMetadataWithProvider) error {
	if flags.json {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(sessions); err != nil {
			return fmt.Errorf("failed to encode sessions as JSON: %w", err)
		}
	} else {
		printSessionTable(sessions)
	}
	return nil
}

// printSessionTable outputs sessions as a formatted table.
func printSessionTable(sessions []sessionMetadataWithProvider) {
	if len(sessions) == 0 {
		return
	}

	termWidth := getTerminalWidth()
	nameWidth := calculateNameWidth(termWidth)

	fmt.Println() // Visual separation before table

	// Print header
	fmt.Printf("%-*s  %-*s  %-*s  %s\n",
		sessionIDWidth, "SESSION ID",
		providerWidth, "PROVIDER",
		createdWidth, "CREATED",
		"NAME")

	// Print separator line
	fmt.Printf("%s  %s  %s  %s\n",
		strings.Repeat("-", sessionIDWidth),
		strings.Repeat("-", providerWidth),
		strings.Repeat("-", createdWidth),
		strings.Repeat("-", min(nameWidth, 20))) // Cap separator at 20 chars for NAME

	// Print each session
	for _, s := range sessions {
		createdFormatted := formatCreatedAt(s.CreatedAt)
		nameTruncated := truncateString(s.Name, nameWidth)

		fmt.Printf("%-*s  %-*s  %-*s  %s\n",
			sessionIDWidth, s.SessionID,
			providerWidth, s.Provider,
			createdWidth, createdFormatted,
			nameTruncated)
	}

	fmt.Println() // Visual separation after table
}

// getTerminalWidth returns the terminal width, defaulting to 80 if unavailable.
func getTerminalWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		return 80
	}
	return width
}

// calculateNameWidth calculates the available width for the NAME column.
func calculateNameWidth(termWidth int) int {
	// Fixed columns: SESSION ID + PROVIDER + CREATED + spacing
	fixedWidth := sessionIDWidth + providerWidth + createdWidth + (columnSpacing * 3)
	nameWidth := termWidth - fixedWidth

	return max(nameWidth, 10) // Minimum width of 10 for NAME
}

// truncateString truncates a string to maxLen runes, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// formatCreatedAt formats an ISO 8601 timestamp to human-readable format.
func formatCreatedAt(timestamp string) string {
	t, ok := parseCreatedAtTimestamp(timestamp)
	if !ok {
		return timestamp // Return original if parsing fails
	}
	return t.Local().Format("Jan 02, 2006 15:04")
}

// parseCreatedAtTimestamp parses various ISO 8601 timestamp formats.
func parseCreatedAtTimestamp(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}

	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, true
	}

	return time.Time{}, false
}

// compareCreatedAtOldestFirst compares two timestamps for sorting (oldest first).
func compareCreatedAtOldestFirst(a, b string) int {
	if a == b {
		return 0
	}

	ta, oka := parseCreatedAtTimestamp(a)
	tb, okb := parseCreatedAtTimestamp(b)

	if oka && okb {
		if ta.Before(tb) {
			return -1
		}
		if ta.After(tb) {
			return 1
		}
		return strings.Compare(a, b)
	}
	if oka && !okb {
		return -1
	}
	if !oka && okb {
		return 1
	}

	return strings.Compare(a, b)
}

// sortSessionMetadataOldestFirst sorts sessions by creation time (oldest first).
func sortSessionMetadataOldestFirst(sessions []spi.SessionMetadata) {
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if cmp := compareCreatedAtOldestFirst(a.CreatedAt, b.CreatedAt); cmp != 0 {
			return cmp < 0
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		return a.Slug < b.Slug
	})
}

// sortSessionMetadataWithProviderOldestFirst sorts sessions with provider info by creation time.
func sortSessionMetadataWithProviderOldestFirst(sessions []sessionMetadataWithProvider) {
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if cmp := compareCreatedAtOldestFirst(a.CreatedAt, b.CreatedAt); cmp != 0 {
			return cmp < 0
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.SessionID != b.SessionID {
			return a.SessionID < b.SessionID
		}
		return a.Slug < b.Slug
	})
}
