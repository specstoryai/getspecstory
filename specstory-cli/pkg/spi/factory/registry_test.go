package factory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// TestRegisterAll_VariantRegistersViaUserDataDirOverride guards the startup
// ordering that --user-data-dir depends on: Copilot IDE variants are only
// registered when their storage holds at least one chat, so overrides must be
// in effect before registerAll runs (main() pre-parses the flag for exactly
// this reason — cobra's RunE fires only after the registry has initialized).
// With an override pointing at a fake VSCodium install containing a chat
// session, a fresh registry must register the copilotide-vscodium provider.
func TestRegisterAll_VariantRegistersViaUserDataDirOverride(t *testing.T) {
	variant := copilotide.VSCodium

	// If the host has a real install with chats, the variant registers with or
	// without the override and the assertion below would prove nothing.
	if copilotide.HasAnyChatSessions(variant) {
		t.Skipf("host has a real %s install with Copilot chats; cannot isolate the override path", variant.AppName)
	}

	// Fake user-data-dir with one workspace holding a chatSessions directory —
	// the marker HasAnyChatSessions gates variant registration on.
	userDataDir := t.TempDir()
	chatSessions := filepath.Join(userDataDir, "User", "workspaceStorage", "ws1", "chatSessions")
	if err := os.MkdirAll(chatSessions, 0755); err != nil {
		t.Fatalf("Failed to create fake chatSessions: %v", err)
	}

	copilotide.SetUserDataDirOverride(variant.ID, userDataDir)
	t.Cleanup(func() { copilotide.SetUserDataDirOverride(variant.ID, "") })

	// A fresh Registry rather than the global singleton: the singleton's
	// registration may already have run without the override in other tests.
	r := &Registry{providers: make(map[string]spi.Provider)}
	r.registerAll()

	if _, ok := r.providers[variant.ID]; !ok {
		t.Errorf("variant %q not registered with a valid --user-data-dir override; registered providers: %v",
			variant.ID, r.ListIDsUnsafe())
	}
}
