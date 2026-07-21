//go:build !copilotide_monitor

package cmd

import (
	"context"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/monitor"
)

// startCopilotWatchers starts no watchers in the default build. VS Code Copilot
// IDE detection depends on the copilotide provider package, which is not yet
// present in this tree, so it is gated behind the copilotide_monitor build tag.
// Building with that tag swaps in the real implementation in monitor_copilotide.go.
func startCopilotWatchers(_ context.Context, _ monitor.StorageRoots, _ *monitor.Resolver, _ *monitor.Supervisor) int {
	return 0
}

// copilotStorageRootOverride handles no providers in the default build, so a
// --storage-root copilotide:... override falls through to the caller's
// unknown-provider error. Gated behind copilotide_monitor; see
// monitor_copilotide.go for the real implementation.
func copilotStorageRootOverride(_ *monitor.StorageRoots, _, _ string) bool {
	return false
}
