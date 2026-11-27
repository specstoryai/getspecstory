//go:build !copilotide_monitor

package monitor

// copilotStorageRoots returns no Copilot IDE storage roots in the default build.
// VS Code Copilot IDE detection depends on the copilotide provider package,
// which is not yet present in this tree, so it is gated behind the
// copilotide_monitor build tag. Building with that tag swaps in the real
// implementation in activity_copilotide.go.
func copilotStorageRoots() map[string]string { return nil }
