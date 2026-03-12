package session

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
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

// ComputeSessionStatistics extracts statistics from SessionData and markdown content
func ComputeSessionStatistics(sessionData *schema.SessionData, markdownContent string) SessionStatistics {
	stats := SessionStatistics{
		Provider:          sessionData.Provider.ID,
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

// Flush writes all pending session statistics to the statistics.json file in a
// single read-modify-write cycle, then clears the pending buffer.
func (c *StatisticsCollector) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Nothing to flush
	if len(c.pending) == 0 {
		return nil
	}

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
