package provenance

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// StartEngine creates and returns a provenance engine if enabled.
// Returns nil engine and no-op cleanup if provenance is not enabled.
// The caller must invoke the returned cleanup function (typically via defer).
func StartEngine(enabled bool) (*Engine, func(), error) {
	if !enabled {
		return nil, func() {}, nil
	}

	engine, err := NewEngine()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start provenance engine: %w", err)
	}

	cleanup := func() {
		if closeErr := engine.Close(); closeErr != nil {
			slog.Error("Failed to close provenance engine", "error", closeErr)
		}
		slog.Info("Provenance engine stopped")
	}

	return engine, cleanup, nil
}

// StartFSWatcher creates a filesystem watcher that pushes FileEvents
// to the provenance engine for correlation with agent activity. Returns a cleanup
// function the caller must invoke (typically via defer). If the engine is nil
// (provenance disabled), returns a no-op cleanup.
func StartFSWatcher(ctx context.Context, engine *Engine, rootDir string) (func(), error) {
	if engine == nil {
		return func() {}, nil
	}

	watcher, err := NewFSWatcher(engine, rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to start provenance FS watcher: %w", err)
	}

	watcher.Start(ctx)
	return watcher.Stop, nil
}

// ProcessEvents extracts agent events from the session and pushes them
// to the provenance engine. Safe to call with nil engine (no-op).
func ProcessEvents(ctx context.Context, engine *Engine, session *spi.AgentChatSession) {
	if engine == nil || session == nil || session.SessionData == nil {
		return
	}
	ProcessSessionEvents(ctx, engine, session.SessionData)
}
