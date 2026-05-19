package cmd

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

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
