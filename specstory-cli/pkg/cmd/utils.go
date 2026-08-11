package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/cursoride"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// AddProvidersFlag registers the --providers filter flag on a command. Shared so
// the flag stays identical across every command that supports provider filtering.
// The flag is hidden because it's internal — provider filtering isn't part of the
// public CLI surface (the positional provider-id argument is).
func AddProvidersFlag(cmd *cobra.Command) {
	cmd.Flags().StringSlice("providers", []string{}, "comma-separated list of provider IDs to limit the operation to (e.g., claude,cursor)")
	_ = cmd.Flags().MarkHidden("providers")
}

// AddUserDataDirFlag registers the --user-data-dir override flag on a command. Shared so
// the flag stays identical across every command that resolves IDE storage locations.
// Unlike --providers this flag stays visible: a portable or otherwise non-standard IDE
// install cannot be found any other way, so it has to be discoverable from --help.
func AddUserDataDirFlag(cmd *cobra.Command) {
	cmd.Flags().StringSlice("user-data-dir", []string{}, "per-provider IDE user-data-dir override formatted as provider_id:path (repeatable, e.g., cursoride:D:\\apps\\cursor\\current\\data\\user-data)")
}

// ResolveProviderIDs resolves the effective list of provider IDs from a positional
// arg and/or --providers flag. Returns nil to indicate "use all providers" when
// neither is specified. Returns an error if both are specified simultaneously or
// if a provider ID in --providers is invalid.
func ResolveProviderIDs(registry *factory.Registry, args []string, providersFlag []string) ([]string, error) {
	hasPositionalArg := len(args) > 0
	hasProvidersFlag := len(providersFlag) > 0

	if hasPositionalArg && hasProvidersFlag {
		return nil, utils.ValidationError{Message: "cannot use both a positional provider argument and --providers flag; use one or the other"}
	}

	if hasPositionalArg {
		// Return without validating — callers handle validation with tailored error messages
		return []string{args[0]}, nil
	}

	if hasProvidersFlag {
		ids := make([]string, 0, len(providersFlag))
		seen := make(map[string]bool, len(providersFlag))
		for _, id := range providersFlag {
			id = strings.TrimSpace(strings.ToLower(id))
			if id == "" {
				continue
			}
			if _, err := registry.Get(id); err != nil {
				return nil, utils.ValidationError{
					Message: fmt.Sprintf("'%s' is not a valid provider ID.\nAvailable providers: %s", id, registry.GetProviderList()),
				}
			}
			// Deduplicate while preserving the order of first occurrence
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, utils.ValidationError{Message: "--providers requires at least one provider ID"}
		}
		return ids, nil
	}

	// Neither specified: caller should use all providers
	return nil, nil
}

// ApplyUserDataDirOverrides parses --user-data-dir entries of the form `provider_id:path`
// and dispatches each to the matching provider's package-level setter. Splits on the
// first ':' so Windows paths like `cursoride:D:\apps\cursor\...` parse correctly.
// Invalid entries (no ':') and unknown provider IDs are logged as warnings and skipped,
// so a typo in one entry never silently disables other valid overrides.
func ApplyUserDataDirOverrides(entries []string) {
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		id, path, ok := strings.Cut(entry, ":")
		if !ok {
			slog.Warn("ignoring --user-data-dir entry: expected provider_id:path", "entry", entry)
			continue
		}
		id = strings.ToLower(strings.TrimSpace(id))
		path = strings.TrimSpace(path)
		if id == "" || path == "" {
			slog.Warn("ignoring --user-data-dir entry: provider_id and path must both be non-empty", "entry", entry)
			continue
		}
		switch id {
		case "cursoride":
			cursoride.SetUserDataDirOverride(path)
			slog.Debug("Applied --user-data-dir override", "provider", id, "path", path)
		case copilotide.VSCode.ID, copilotide.VSCodeInsiders.ID, copilotide.VSCodium.ID, copilotide.VSCodiumInsiders.ID:
			copilotide.SetUserDataDirOverride(id, path)
			slog.Debug("Applied --user-data-dir override", "provider", id, "path", path)
		default:
			slog.Warn("ignoring --user-data-dir entry: unknown provider ID "+
				"(supported: cursoride, copilotide, copilotide-insiders, copilotide-vscodium, copilotide-vscodium-insiders)",
				"providerId", id)
		}
	}
}

// CheckAndWarnAuthentication warns the user if cloud sync is enabled but authentication
// is missing or has failed. Uses log.IsSilent() to respect silent mode.
func CheckAndWarnAuthentication(noCloudSync bool) {
	if !noCloudSync && !cloud.IsAuthenticated() && !log.IsSilent() {
		// Check if this was due to a 401 authentication failure
		if cloud.HadAuthFailure() {
			// Show the specific message for auth failures with orange warning and emoji
			slog.Warn("Cloud sync authentication failed (401)")
			log.UserWarn("⚠️ Unable to authenticate with SpecStory Cloud. This could be due to revoked or expired credentials, or network/server issues.\n")
			log.UserMessage("ℹ️ If this persists, run `specstory logout` then `specstory login` to reset your SpecStory Cloud authentication.\n")
		} else {
			// Regular "not authenticated" message
			msg := "⚠️ Cloud sync not available. You're not authenticated."
			slog.Warn(msg)
			log.UserWarn("%s\n", msg)
			log.UserMessage("ℹ️ Use `specstory login` to authenticate, or `--no-cloud-sync` to skip this warning.\n")
		}
	}
}
