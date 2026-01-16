// Package factory provides the provider registry and factory implementation.
// This package imports all concrete provider implementations and manages
// their registration, avoiding circular dependencies in the SPI layer.
package factory

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/specstoryai/SpecStoryCLI/pkg/providers/claudecode"
	"github.com/specstoryai/SpecStoryCLI/pkg/providers/codexcli"
	"github.com/specstoryai/SpecStoryCLI/pkg/providers/cursorcli"
	"github.com/specstoryai/SpecStoryCLI/pkg/providers/geminicli"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// Registry manages all registered providers
type Registry struct {
	providers         map[string]spi.Provider // key is the provider ID (e.g., "claude", "cursor")
	mu                sync.RWMutex
	initialized       bool
	providerListCache string    // Cached formatted provider list string
	providerListOnce  sync.Once // Ensures provider list is built only once
}

// Global registry instance
var (
	registry = &Registry{
		providers: make(map[string]spi.Provider),
	}
	once sync.Once
)

// ensureInitialized makes sure providers are registered
func (r *Registry) ensureInitialized() {
	once.Do(func() {
		r.registerAll()
	})
}

// registerAll registers all known providers.
// This is the ONLY place that needs to be updated when adding new providers.
func (r *Registry) registerAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.initialized {
		slog.Debug("Provider registry already initialized, skipping registration")
		return
	}

	slog.Debug("Initializing provider registry")

	// Register providers with simple IDs
	// Each provider directly implements the spi.Provider interface
	claudeProvider := claudecode.NewProvider()
	r.providers["claude"] = claudeProvider
	slog.Debug("Registered provider", "id", "claude", "name", claudeProvider.Name())

	cursorProvider := cursorcli.NewProvider()
	r.providers["cursor"] = cursorProvider
	slog.Debug("Registered provider", "id", "cursor", "name", cursorProvider.Name())

	codexProvider := codexcli.NewProvider()
	r.providers["codex"] = codexProvider
	slog.Debug("Registered provider", "id", "codex", "name", codexProvider.Name())

	geminiProvider := geminicli.NewProvider()
	r.providers["gemini"] = geminiProvider
	slog.Debug("Registered provider", "id", "gemini", "name", geminiProvider.Name())

	r.initialized = true
	slog.Info("Provider registry initialized", "count", len(r.providers), "providers", r.ListIDsUnsafe())
}

// ListIDsUnsafe returns provider IDs without locking (for internal use only)
func (r *Registry) ListIDsUnsafe() []string {
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetRegistry returns the global registry instance
func GetRegistry() *Registry {
	registry.ensureInitialized()
	return registry
}

// Get retrieves a provider by ID (case-insensitive)
func (r *Registry) Get(id string) (spi.Provider, error) {
	r.ensureInitialized()
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Case-insensitive lookup
	for provID, provider := range r.providers {
		if strings.EqualFold(provID, id) {
			slog.Debug("Retrieved provider", "requested_id", id, "matched_id", provID, "name", provider.Name())
			return provider, nil
		}
	}

	// Log available providers when lookup fails to aid debugging
	availableIDs := make([]string, 0, len(r.providers))
	for id := range r.providers {
		availableIDs = append(availableIDs, id)
	}
	sort.Strings(availableIDs)

	slog.Warn("Provider not found",
		"requested_id", id,
		"available_providers", availableIDs)

	return nil, fmt.Errorf("provider '%s' not found", id)
}

// GetAll returns all registered providers as a map of ID to Provider
func (r *Registry) GetAll() map[string]spi.Provider {
	r.ensureInitialized()
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]spi.Provider)
	for id, provider := range r.providers {
		result[id] = provider
	}

	return result
}

// ListIDs returns all registered provider IDs (sorted)
func (r *Registry) ListIDs() []string {
	r.ensureInitialized()
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}

	sort.Strings(ids)
	return ids
}

// GetDefault returns the default provider (Claude)
func (r *Registry) GetDefault() (spi.Provider, error) {
	r.ensureInitialized()
	slog.Debug("Getting default provider (claude)")
	return r.Get("claude")
}

// GetProviderList returns a formatted string listing all providers.
// Used for help text and error messages.
// The result is cached since providers are static for the lifetime of the process.
func (r *Registry) GetProviderList() string {
	r.ensureInitialized()

	// Build the provider list only once and cache it
	r.providerListOnce.Do(func() {
		r.mu.RLock()
		defer r.mu.RUnlock()

		if len(r.providers) == 0 {
			r.providerListCache = "No providers registered"
			return
		}

		// Get sorted IDs for consistent ordering
		ids := make([]string, 0, len(r.providers))
		for id := range r.providers {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		// Build the formatted list by accessing providers directly (no Get() calls)
		var parts []string
		for _, id := range ids {
			if provider, ok := r.providers[id]; ok {
				parts = append(parts, fmt.Sprintf("%s (%s)", id, provider.Name()))
			}
		}

		r.providerListCache = strings.Join(parts, ", ")
	})

	return r.providerListCache
}
