//go:build copilotide_monitor

package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/monitor"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
)

// startCopilotWatchers launches a dedicated selective watcher for each installed
// VS Code Copilot IDE variant's workspaceStorage and returns how many it started.
//
// VS Code Copilot chat storage gets a dedicated selective watcher
// (monitor.WatchCopilotWorkspaceStorage): workspaceStorage can hold thousands of
// workspace directories, so the generic recursive root watcher would exhaust
// file descriptors. The watcher resolves events to repos itself, so activity
// feeds the supervisor directly. Absent variants are common (most machines run
// at most one VS Code distribution), hence the quiet Debug-level skip.
//
// This is the copilotide_monitor build's implementation; it depends on the
// copilotide provider package, which is not yet present in this tree, so it is
// gated behind the copilotide_monitor tag. The default build uses the stub in
// monitor_stub.go, which starts nothing.
func startCopilotWatchers(ctx context.Context, roots monitor.StorageRoots, resolver *monitor.Resolver, supervisor *monitor.Supervisor) int {
	started := 0
	for _, variant := range copilotide.Variants() {
		storageRoot := roots.CopilotIDE[variant.ID]
		if storageRoot == "" {
			slog.Debug("Monitor: skipping absent Copilot IDE variant", "variant", variant.AppName)
			continue
		}
		if _, statErr := os.Stat(storageRoot); statErr != nil {
			slog.Debug("Monitor: skipping missing Copilot IDE workspace storage", "variant", variant.AppName, "path", storageRoot)
			continue
		}
		started++
		go func(appName, storageRoot string) {
			slog.Info("Monitor: watching Copilot IDE workspace storage", "variant", appName, "path", storageRoot)
			if watchErr := monitor.WatchCopilotWorkspaceStorage(ctx, storageRoot, resolver, supervisor.NotifyActivity); watchErr != nil {
				slog.Error("Monitor: Copilot IDE storage watcher failed", "variant", appName, "path", storageRoot, "error", watchErr)
			}
		}(variant.AppName, storageRoot)
	}
	return started
}

// copilotStorageRootOverride handles a hidden --storage-root override targeting
// the copilotide provider. It returns true when it handled the provider, false
// otherwise (so the caller falls through to its unknown-provider error). The
// override targets the stock "Code" variant only; that is enough for fixture
// testing, and per-variant overrides would complicate the provider:path syntax
// for no test benefit.
func copilotStorageRootOverride(roots *monitor.StorageRoots, provider, abs string) bool {
	if provider != "copilotide" {
		return false
	}
	if roots.CopilotIDE == nil {
		roots.CopilotIDE = make(map[string]string)
	}
	roots.CopilotIDE[copilotide.VSCode.ID] = abs
	return true
}
