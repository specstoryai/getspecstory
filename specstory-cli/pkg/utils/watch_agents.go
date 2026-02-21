package utils

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

// WatchAgents starts watchers for all registered providers concurrently.
// Convenience wrapper around WatchProviders that resolves the full provider registry.
func WatchAgents(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(providerID string, session *spi.AgentChatSession)) error {
	registry := factory.GetRegistry()
	providers := registry.GetAll()

	if len(providers) == 0 {
		return fmt.Errorf("no providers registered")
	}

	return WatchProviders(ctx, projectPath, providers, debugRaw, sessionCallback)
}

// WatchProviders starts watchers for the given providers concurrently.
// Calls sessionCallback when any provider detects activity.
// Runs until context is cancelled or all watchers stop.
// Context cancellation (Ctrl+C) is treated as a clean exit, not an error.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - projectPath: Agent's working directory to watch
//   - providers: map of provider ID to provider instance to watch
//   - debugRaw: whether to write debug raw data files
//   - sessionCallback: called with provider ID and AgentChatSession data on each update
//
// The callback includes the provider ID to help consumers route/filter sessions.
// The callback should not block as it may delay other provider notifications.
func WatchProviders(ctx context.Context, projectPath string, providers map[string]spi.Provider, debugRaw bool, sessionCallback func(providerID string, session *spi.AgentChatSession)) error {
	slog.Info("WatchProviders: Starting multi-provider watch", "projectPath", projectPath, "providerCount", len(providers), "debugRaw", debugRaw)

	var wg sync.WaitGroup
	errChan := make(chan error, len(providers))

	for providerID, provider := range providers {
		providerID := providerID // Capture loop variable
		provider := provider     // Capture loop variable

		wg.Add(1)
		go func() {
			defer wg.Done()

			slog.Info("WatchProviders: Starting watcher for provider", "providerID", providerID, "providerName", provider.Name())

			// Wrap the callback to include provider ID
			wrappedCallback := func(session *spi.AgentChatSession) {
				if session == nil || session.SessionData == nil {
					return
				}

				slog.Debug("WatchProviders: Provider callback fired",
					"providerID", providerID,
					"sessionID", session.SessionID,
					"exchanges", len(session.SessionData.Exchanges))

				sessionCallback(providerID, session)
			}

			err := provider.WatchAgent(ctx, projectPath, debugRaw, wrappedCallback)
			if err != nil {
				// Context cancellation is expected when user presses Ctrl+C, not an error
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					slog.Info("WatchProviders: Provider watcher stopped", "provider", provider.Name())
					errChan <- nil
				} else {
					slog.Error("WatchProviders: Provider watcher failed", "provider", provider.Name(), "error", err)
					errChan <- fmt.Errorf("%s: %w", provider.Name(), err)
				}
			} else {
				errChan <- nil
			}
		}()
	}

	// Wait for all watchers to complete (they run until Ctrl+C)
	wg.Wait()

	// Collect errors
	var lastError error
	for range len(providers) {
		if err := <-errChan; err != nil {
			lastError = err
			slog.Error("WatchProviders: Provider watch error", "error", err)
		}
	}

	return lastError
}
