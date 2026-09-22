package provenance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultMatchWindow = 5 * time.Second

// defaultDBPath returns ~/.specstory/provenance/provenance.db.
// Returns an error if the home directory cannot be determined.
func defaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".specstory", "provenance", "provenance.db"), nil
}

// Option configures the Engine.
type Option func(*Engine)

// WithDBPath overrides the default database location (~/.specstory/provenance/provenance.db).
func WithDBPath(path string) Option {
	return func(e *Engine) { e.dbPath = path }
}

// WithMatchWindow sets the time window for correlating file events to agent events.
// A file event and agent event match only if their timestamps are within ±window.
// Defaults to 5 seconds.
func WithMatchWindow(d time.Duration) Option {
	return func(e *Engine) { e.matchWindow = d }
}

// Engine correlates file events to agent activity and produces provenance records.
type Engine struct {
	dbPath      string
	store       *Store
	matchWindow time.Duration
}

// NewEngine creates a correlation engine backed by a SQLite database.
// By default the database is stored at ~/.specstory/provenance/provenance.db.
// Use WithDBPath to override.
func NewEngine(opts ...Option) (*Engine, error) {
	e := &Engine{
		matchWindow: defaultMatchWindow,
	}
	for _, opt := range opts {
		opt(e)
	}

	// Resolve the default DB path only if no override was provided.
	if e.dbPath == "" {
		path, err := defaultDBPath()
		if err != nil {
			return nil, err
		}
		e.dbPath = path
	}

	store, err := OpenStore(e.dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	e.store = store

	slog.Info("Provenance engine started",
		"dbPath", e.dbPath,
		"matchWindow", e.matchWindow)

	return e, nil
}

// Close closes the underlying database connection.
func (e *Engine) Close() error {
	return e.store.Close()
}

// PushFileEvent stores a file event and attempts to correlate it with an existing
// unmatched agent event. Returns a ProvenanceRecord if a match is found, nil otherwise.
func (e *Engine) PushFileEvent(ctx context.Context, event FileEvent) (*ProvenanceRecord, error) {
	if err := e.store.PushFileEvent(ctx, event); err != nil {
		return nil, err
	}

	// Only fetch agent events within the match window — events outside
	// this range can never correlate, so there's no reason to load them.
	since := event.Timestamp.Add(-e.matchWindow)
	until := event.Timestamp.Add(e.matchWindow)
	agentEvents, err := e.store.QueryUnmatchedAgentEvents(ctx, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying unmatched agent events: %w", err)
	}

	best := findBestMatch(event.Timestamp, agentEvents, e.matchWindow, func(agentPath string) bool {
		return pathSuffixMatch(event.Path, agentPath)
	})
	if best == nil {
		slog.Debug("No agent match for file event",
			"fileEventID", event.ID,
			"filePath", event.Path,
			"candidates", len(agentEvents))
		return nil, nil
	}

	var agentEvt AgentEvent
	if err := json.Unmarshal([]byte(best.Payload), &agentEvt); err != nil {
		return nil, fmt.Errorf("unmarshaling agent event payload: %w", err)
	}

	delta := absDuration(event.Timestamp.Sub(best.Timestamp))
	record := buildRecord(event, agentEvt)

	if err := e.store.SetMatchedWith(ctx, event.ID, best.ID); err != nil {
		return nil, fmt.Errorf("setting matched: %w", err)
	}

	slog.Info("Matched file event to agent event",
		"fileEventID", event.ID,
		"agentEventID", best.ID,
		"filePath", event.Path,
		"delta", delta)

	return &record, nil
}

// PushAgentEvent stores an agent event and attempts to correlate it with an existing
// unmatched file event. Returns a ProvenanceRecord if a match is found, nil otherwise.
func (e *Engine) PushAgentEvent(ctx context.Context, event AgentEvent) (*ProvenanceRecord, error) {
	if err := e.store.PushAgentEvent(ctx, event); err != nil {
		return nil, err
	}

	normalizedPath := NormalizePath(event.FilePath)

	// Only fetch file events within the match window — events outside
	// this range can never correlate, so there's no reason to load them.
	since := event.Timestamp.Add(-e.matchWindow)
	until := event.Timestamp.Add(e.matchWindow)
	fileEvents, err := e.store.QueryUnmatchedFileEvents(ctx, since, until)
	if err != nil {
		return nil, fmt.Errorf("querying unmatched file events: %w", err)
	}

	bestFE := findBestMatch(event.Timestamp, fileEvents, e.matchWindow, func(fePath string) bool {
		return pathSuffixMatch(fePath, normalizedPath)
	})
	if bestFE == nil {
		slog.Debug("No file match for agent event",
			"agentEventID", event.ID,
			"agentPath", event.FilePath,
			"candidates", len(fileEvents))
		return nil, nil
	}

	// Reconstruct the FileEvent from the stored payload
	var fileEvt FileEvent
	if err := json.Unmarshal([]byte(bestFE.Payload), &fileEvt); err != nil {
		return nil, fmt.Errorf("unmarshaling file event payload: %w", err)
	}

	delta := absDuration(event.Timestamp.Sub(bestFE.Timestamp))
	record := buildRecord(fileEvt, event)

	if err := e.store.SetMatchedWith(ctx, bestFE.ID, event.ID); err != nil {
		return nil, fmt.Errorf("setting matched: %w", err)
	}

	slog.Info("Matched agent event to file event",
		"agentEventID", event.ID,
		"fileEventID", bestFE.ID,
		"filePath", bestFE.FilePath,
		"delta", delta)

	return &record, nil
}

// findBestMatch finds the candidate closest in time whose path satisfies pathMatches.
// Returns nil if no candidate matches within the window.
func findBestMatch(refTime time.Time, candidates []unmatchedEvent, window time.Duration, pathMatches func(candidatePath string) bool) *unmatchedEvent {
	var best *unmatchedEvent
	var bestDelta time.Duration

	for i := range candidates {
		candidate := &candidates[i]
		if !pathMatches(candidate.FilePath) {
			continue
		}
		delta := absDuration(refTime.Sub(candidate.Timestamp))
		if delta > window {
			continue
		}
		if best == nil || delta < bestDelta {
			best = candidate
			bestDelta = delta
		}
	}

	return best
}

// pathSuffixMatch returns true if the normalized agent path is a suffix of the FS path
// aligned to a directory boundary. A raw suffix like "/foo.go" inside "/project/afoo.go"
// is rejected because the match doesn't start at a "/" separator.
func pathSuffixMatch(fsPath, agentPath string) bool {
	if !strings.HasSuffix(fsPath, agentPath) {
		return false
	}
	if len(fsPath) == len(agentPath) {
		return true
	}
	// The character just before the matched suffix must be '/' to ensure
	// we're matching at a directory boundary, not mid-filename.
	return fsPath[len(fsPath)-len(agentPath)] == '/'
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// buildRecord creates a ProvenanceRecord from a matched file event + agent event pair.
func buildRecord(fe FileEvent, ae AgentEvent) ProvenanceRecord {
	return ProvenanceRecord{
		FilePath:      fe.Path,
		ChangeType:    fe.ChangeType,
		Timestamp:     fe.Timestamp,
		SessionID:     ae.SessionID,
		ExchangeID:    ae.ExchangeID,
		AgentType:     ae.AgentType,
		AgentModel:    ae.AgentModel,
		MessageID:     ae.MessageID,
		ActorHost:     ae.ActorHost,
		ActorUsername: ae.ActorUsername,
		MatchedAt:     time.Now(),
	}
}
