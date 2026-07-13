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

	// Track a per-session content fingerprint to suppress duplicate callbacks.
	// Providers fire callbacks on every session-file change, but not all changes
	// produce new content in the parsed SessionData (e.g. UI metadata updates).
	// The message count alone is not enough: patch-log providers (e.g. VS Code
	// Copilot) stream an agent response by growing the text of an existing
	// message in place, so the count stays flat while the content changes.
	var mu sync.Mutex
	lastFingerprint := make(map[string]sessionFingerprint)

	var wg sync.WaitGroup
	errChan := make(chan error, len(providers))

	for providerID, provider := range providers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			slog.Info("WatchProviders: Starting watcher for provider", "providerID", providerID, "providerName", provider.Name())

			// Wrap the callback to deduplicate and include provider ID
			wrappedCallback := func(session *spi.AgentChatSession) {
				if session == nil || session.SessionData == nil {
					return
				}

				fingerprint := fingerprintSession(session)

				// Skip if the parsed content hasn't changed for this session
				mu.Lock()
				prev, seen := lastFingerprint[session.SessionID]
				if seen && prev == fingerprint {
					mu.Unlock()
					slog.Debug("WatchProviders: Skipping duplicate callback",
						"providerID", providerID,
						"sessionID", session.SessionID,
						"messages", fingerprint.messages,
						"contentBytes", fingerprint.contentBytes)
					return
				}
				lastFingerprint[session.SessionID] = fingerprint
				mu.Unlock()

				slog.Debug("WatchProviders: Provider callback fired",
					"providerID", providerID,
					"sessionID", session.SessionID,
					"messages", fingerprint.messages,
					"contentBytes", fingerprint.contentBytes)

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

	// Drain error channel — errors are already logged in the goroutines with full context
	for range len(providers) {
		<-errChan
	}

	return nil
}

// sessionFingerprint summarizes the parsed content of a session for callback
// deduplication. Comparable by value.
type sessionFingerprint struct {
	messages     int // total messages across all exchanges
	contentBytes int // total bytes of message text and pre-rendered tool markdown
}

// fingerprintSession computes the dedup fingerprint for a session. Content size
// is tracked alongside the message count because a streaming agent response can
// grow the text of an existing message without adding new messages.
func fingerprintSession(session *spi.AgentChatSession) sessionFingerprint {
	var fp sessionFingerprint
	for _, exchange := range session.SessionData.Exchanges {
		fp.messages += len(exchange.Messages)
		for _, msg := range exchange.Messages {
			for _, part := range msg.Content {
				fp.contentBytes += len(part.Text)
			}
			if msg.Tool != nil && msg.Tool.FormattedMarkdown != nil {
				fp.contentBytes += len(*msg.Tool.FormattedMarkdown)
			}
		}
	}
	return fp
}
