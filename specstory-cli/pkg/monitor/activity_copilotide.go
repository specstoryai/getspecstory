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
// This is the copilotide_monitor build's implementation: it depends on the
// copilotide provider package, which is not yet present in this tree, so the
// whole file is gated behind the copilotide_monitor tag. The default build uses
// the stub in activity_stub.go, which returns nil.
func copilotStorageRoots() map[string]string {
	copilotRoots := make(map[string]string)
	for _, variant := range copilotide.Variants() {
		if p := copilotide.GetWorkspaceStoragePath(variant.DataDirName); p != "" {
			copilotRoots[variant.ID] = p
		}
	}
	return copilotRoots
}
