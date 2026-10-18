//go:build copilotide_monitor

package monitor

import (
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
)

// copilotStorageRoots returns the workspaceStorage directory for each installed
// VS Code Copilot IDE variant, keyed by variant ID (copilotide.Variant.ID).
// Only variants whose workspaceStorage actually exists get an entry
// (GetWorkspaceStoragePath returns "" otherwise); the monitor command skips
// absent variants.
//
// This is the copilotide_monitor build's implementation, gated behind the
// copilotide_monitor tag so Copilot IDE detection stays opt-in. The default
// build uses the stub in activity_stub.go, which returns nil.
func copilotStorageRoots() map[string]string {
	copilotRoots := make(map[string]string)
	variants := []copilotide.Variant{
		copilotide.VSCode, copilotide.VSCodeInsiders, copilotide.VSCodium, copilotide.VSCodiumInsiders,
	}
	for _, variant := range variants {
		if p := copilotide.GetWorkspaceStoragePath(variant); p != "" {
			copilotRoots[variant.ID] = p
		}
	}
	return copilotRoots
}
