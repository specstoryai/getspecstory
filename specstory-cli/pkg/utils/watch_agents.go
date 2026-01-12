package utils

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
	"github.com/specstoryai/SpecStoryCLI/pkg/spi/factory"
)

// WatchAgents starts watchers for all registered providers concurrently
// Calls sessionCallback when any provider detects activity
// Continues watching even if individual providers error (logs errors but keeps running)
// Runs until context is cancelled or all watchers stop
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - projectPath: Agent's working directory to watch
//   - debugRaw: whether to write debug raw data files
//   - sessionCallback: called with provider ID and AgentChatSession data on each update
//
// The callback includes the provider ID to help consumers route/filter sessions.
// The callback should not block as it may delay other provider notifications.
func WatchAgents(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(providerID string, session *spi.AgentChatSession)) error {
	slog.Info("WatchAgents: Starting multi-provider watch", "projectPath", projectPath, "debugRaw", debugRaw)

	// Get all registered providers
	registry := factory.GetRegistry()
	providers := registry.GetAll()

	if len(providers) == 0 {
		return fmt.Errorf("no providers registered")
	}

	slog.Info("WatchAgents: Found providers", "count", len(providers))

	// Create a WaitGroup to track all watcher goroutines
	var wg sync.WaitGroup

	// Create a channel to collect errors from watchers
	errorChan := make(chan error, len(providers))

	// Start a watcher for each provider
	for providerID, provider := range providers {
		providerID := providerID // Capture loop variable
		provider := provider     // Capture loop variable

		wg.Add(1)
		go func() {
			defer wg.Done()

			slog.Info("WatchAgents: Starting watcher for provider", "providerID", providerID, "providerName", provider.Name())

			// Wrap the callback to include provider ID
			wrappedCallback := func(session *spi.AgentChatSession) {
				slog.Debug("WatchAgents: Provider callback fired",
					"providerID", providerID,
					"sessionID", session.SessionID,
					"exchanges", len(session.SessionData.Exchanges))

				// Call the user's callback with provider ID
				sessionCallback(providerID, session)
			}

			// Call the provider's WatchAgent method with context
			err := provider.WatchAgent(ctx, projectPath, debugRaw, wrappedCallback)

			if err != nil {
				slog.Warn("WatchAgents: Provider watcher stopped with error",
					"providerID", providerID,
					"providerName", provider.Name(),
					"error", err)

				// Send error to channel (non-blocking)
				select {
				case errorChan <- fmt.Errorf("provider %s (%s): %w", providerID, provider.Name(), err):
				default:
					// Error channel full, log but continue
					slog.Warn("WatchAgents: Error channel full, discarding error", "providerID", providerID)
				}
			} else {
				slog.Info("WatchAgents: Provider watcher stopped normally",
					"providerID", providerID,
					"providerName", provider.Name())
			}
		}()
	}

	// Wait for either context cancellation or all watchers to stop
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		slog.Info("WatchAgents: Context cancelled, stopping all watchers")
		// Wait for all goroutines to finish
		wg.Wait()
		return ctx.Err()

	case <-done:
		slog.Info("WatchAgents: All watchers stopped")
		// Collect any errors (non-blocking)
		close(errorChan)

		var errors []error
		for err := range errorChan {
			errors = append(errors, err)
		}

		if len(errors) > 0 {
			slog.Warn("WatchAgents: Some watchers encountered errors", "errorCount", len(errors))
			// Log all errors but don't fail - we continue watching even if some providers error
			for _, err := range errors {
				slog.Warn("WatchAgents: Watcher error", "error", err)
			}
		}

		return nil
	}
}
