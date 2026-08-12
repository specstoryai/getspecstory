package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// SessionStatistics contains computed statistics for a single session
type SessionStatistics struct {
	UserMessageCount  int    `json:"user_message_count"`
	AgentMessageCount int    `json:"agent_message_count"`
	StartTimestamp    string `json:"start_timestamp"`
	EndTimestamp      string `json:"end_timestamp"`
	MarkdownSizeBytes int    `json:"markdown_size_bytes"`
	Provider          string `json:"provider"`
	LastUpdated       string `json:"last_updated"`
}

// StatisticsFile is the root structure for the statistics.json file
type StatisticsFile struct {
	Sessions map[string]SessionStatistics `json:"sessions"`
}

// StatisticsCollector handles thread-safe statistics collection and persistence.
// Stats are accumulated in memory via AddSessionStats and written to disk in a
// single read-modify-write cycle when Flush is called.
type StatisticsCollector struct {
	statsPath string
	mu        sync.Mutex
	pending   map[string]SessionStatistics // in-memory buffer of stats awaiting flush
}

// NewStatisticsCollector creates a new statistics collector that writes to the given path
func NewStatisticsCollector(statsPath string) *StatisticsCollector {
	return &StatisticsCollector{
		statsPath: statsPath,
		pending:   make(map[string]SessionStatistics),
	}
}

// sharedCollectors hands out one collector per statistics file so every writer
// in the process serializes on the same mutex. The collector's locking only
// protects its own instance: separate instances racing through Flush's
// read-modify-write would silently drop sessions when concurrent watch-mode
// callbacks save at the same time. Serialization across processes (a running
// `watch` plus a manual `sync`) is handled separately, by the file lock Flush
// takes around the read-merge-write cycle.
var (
	sharedCollectorsMu sync.Mutex
	sharedCollectors   = map[string]*StatisticsCollector{}

	// lockTokenCounter makes lock-file owner tokens unique within the process
	// even when minted in the same clock tick (see acquireFileLock).
	lockTokenCounter atomic.Uint64
)

// SharedStatisticsCollector returns the process-wide collector for statsPath,
// creating it on first use. All statistics writers — bulk sync batching,
// per-session saves from run/watch callbacks, `sync -s` — should go through
// this rather than NewStatisticsCollector so their writes serialize.
func SharedStatisticsCollector(statsPath string) *StatisticsCollector {
	sharedCollectorsMu.Lock()
	defer sharedCollectorsMu.Unlock()

	collector, ok := sharedCollectors[statsPath]
	if !ok {
		collector = NewStatisticsCollector(statsPath)
		sharedCollectors[statsPath] = collector
	}
	return collector
}

// ComputeSessionStatistics extracts statistics from SessionData and markdown content.
// providerID is the registry/CLI provider id (e.g. "claude"), deliberately NOT
// sessionData.Provider.ID (e.g. "claude-code") — statistics.json has stored
// registry ids since the feature shipped, and external consumers group by them,
// so switching id spaces would silently rewrite documented output.
func ComputeSessionStatistics(sessionData *schema.SessionData, markdownContent string, providerID string) SessionStatistics {
	stats := SessionStatistics{
		Provider:          providerID,
		MarkdownSizeBytes: len(markdownContent),
		LastUpdated:       time.Now().UTC().Format(time.RFC3339),
	}

	// Count user and agent messages by iterating through exchanges
	userMsgCount := 0
	agentMsgCount := 0
	for _, exchange := range sessionData.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Role == schema.RoleUser {
				userMsgCount++
			} else {
				agentMsgCount++
			}
		}
	}
	stats.UserMessageCount = userMsgCount
	stats.AgentMessageCount = agentMsgCount

	// Extract start timestamp - use first exchange start time if available, else session CreatedAt
	if len(sessionData.Exchanges) > 0 && sessionData.Exchanges[0].StartTime != "" {
		stats.StartTimestamp = sessionData.Exchanges[0].StartTime
	} else {
		stats.StartTimestamp = sessionData.CreatedAt
	}

	// Extract end timestamp - use last exchange end time if available, else session UpdatedAt, else CreatedAt
	if len(sessionData.Exchanges) > 0 {
		lastExchange := sessionData.Exchanges[len(sessionData.Exchanges)-1]
		if lastExchange.EndTime != "" {
			stats.EndTimestamp = lastExchange.EndTime
		} else if sessionData.UpdatedAt != "" {
			stats.EndTimestamp = sessionData.UpdatedAt
		} else {
			stats.EndTimestamp = sessionData.CreatedAt
		}
	} else if sessionData.UpdatedAt != "" {
		stats.EndTimestamp = sessionData.UpdatedAt
	} else {
		stats.EndTimestamp = sessionData.CreatedAt
	}

	return stats
}

// AddSessionStats accumulates session statistics in memory. Call Flush to
// persist all pending stats to disk in a single I/O operation.
func (c *StatisticsCollector) AddSessionStats(sessionID string, stats SessionStatistics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pending[sessionID] = stats
	slog.Debug("Buffered session statistics", "sessionId", sessionID)
}

// acquireFileLock takes a cross-process lock by creating lockPath with O_EXCL,
// which is atomic on every platform (no syscall flock needed, so no platform
// build tags and no new dependency). The in-process mutex alone cannot stop two
// processes — a long-running `watch` and a manual `sync` — from interleaving
// Flush's read-merge-write and silently dropping the loser's sessions.
//
// Polls briefly while the lock is held: flushes are millisecond-scale, so a
// two-second budget is generous. A lock file older than staleLockAge is
// presumed abandoned by a crashed process and stolen; the steal itself has a
// benign race (two stealers, one wins O_EXCL, the other retries).
//
// The lock file carries an owner token, and release deletes the file only when
// the token is still ours. Without that, a steal cascades: if a live-but-slow
// owner is stolen from, its unconditional release would delete the NEW owner's
// lock and let a third writer into the critical section. With the token, the
// stale original's release becomes a no-op. A small read-then-remove window
// remains (this is a lock file, not an OS lock); the residual worst case is the
// pre-lock behavior — one lost merge — not corruption.
// Returns the release func on success.
func acquireFileLock(lockPath string) (func(), error) {
	const (
		retryDelay   = 10 * time.Millisecond
		retryBudget  = 2 * time.Second
		staleLockAge = 30 * time.Second
	)

	// The counter disambiguates tokens minted within one clock tick: Windows'
	// timer granularity is coarse enough that two acquisitions in the same
	// process can observe the same UnixNano, and identical tokens would let a
	// stolen-from owner's release delete the new owner's lock after all.
	token := fmt.Sprintf("%d-%d-%d", os.Getpid(), lockTokenCounter.Add(1), time.Now().UnixNano())
	deadline := time.Now().Add(retryBudget)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_, _ = f.WriteString(token)
			_ = f.Close()
			return func() {
				if data, readErr := os.ReadFile(lockPath); readErr == nil && string(data) == token {
					_ = os.Remove(lockPath)
				}
			}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("failed to create lock file: %w", err)
		}
		// Held by another process — steal it if it looks abandoned.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("statistics lock still held after %s: %s", retryBudget, lockPath)
		}
		time.Sleep(retryDelay)
	}
}

// Flush writes all pending session statistics to the statistics.json file in a
// single read-modify-write cycle, then clears the pending buffer. A file lock
// spans the whole cycle so concurrent processes can't lose each other's merges.
func (c *StatisticsCollector) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Nothing to flush
	if len(c.pending) == 0 {
		return nil
	}

	unlock, err := acquireFileLock(c.statsPath + ".lock")
	if err != nil {
		return fmt.Errorf("failed to lock statistics file: %w", err)
	}
	defer unlock()

	statsPath := c.statsPath

	// Read existing statistics file
	statsFile := StatisticsFile{
		Sessions: make(map[string]SessionStatistics),
	}

	data, err := os.ReadFile(statsPath)
	if err == nil {
		// File exists, try to parse it
		if err := json.Unmarshal(data, &statsFile); err != nil {
			// Corrupt JSON - log warning and start fresh
			slog.Warn("Failed to parse existing statistics.json, starting fresh", "error", err)
			statsFile.Sessions = make(map[string]SessionStatistics)
		}
	} else if !os.IsNotExist(err) {
		// Error other than "file doesn't exist"
		return fmt.Errorf("failed to read statistics file: %w", err)
	}

	// Merge all pending stats into the file
	for sessionID, stats := range c.pending {
		statsFile.Sessions[sessionID] = stats
	}

	// Marshal to JSON with indentation for readability
	jsonData, err := json.MarshalIndent(statsFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal statistics: %w", err)
	}
	jsonData = append(jsonData, '\n')

	// Write atomically using temp file + rename
	tempPath := statsPath + ".tmp"
	if err := os.WriteFile(tempPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write temp statistics file: %w", err)
	}

	if err := os.Rename(tempPath, statsPath); err != nil {
		// Clean up temp file on failure
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to rename temp statistics file: %w", err)
	}

	slog.Debug("Flushed session statistics", "count", len(c.pending), "path", statsPath)

	// Clear pending buffer
	c.pending = make(map[string]SessionStatistics)
	return nil
}
