package utils

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

// SessionStatistics contains computed statistics for a single session
type SessionStatistics struct {
	UserMessageCount  int    `json:"user_message_count"`
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

// StatisticsCollector handles thread-safe statistics collection and persistence
type StatisticsCollector struct {
	specstoryDir string
	mu           sync.Mutex
}

// NewStatisticsCollector creates a new statistics collector for the given .specstory directory
func NewStatisticsCollector(specstoryDir string) *StatisticsCollector {
	return &StatisticsCollector{
		specstoryDir: specstoryDir,
	}
}

// ComputeSessionStatistics extracts statistics from SessionData and markdown content
func ComputeSessionStatistics(sessionData *schema.SessionData, markdownContent string, providerID string) SessionStatistics {
	stats := SessionStatistics{
		Provider:          providerID,
		MarkdownSizeBytes: len(markdownContent),
		LastUpdated:       time.Now().UTC().Format(time.RFC3339),
	}

	// Count user messages by iterating through exchanges
	userMsgCount := 0
	for _, exchange := range sessionData.Exchanges {
		for _, msg := range exchange.Messages {
			if msg.Role == schema.RoleUser {
				userMsgCount++
			}
		}
	}
	stats.UserMessageCount = userMsgCount

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

// AddSessionStats atomically adds or updates session statistics in the statistics.json file
func (c *StatisticsCollector) AddSessionStats(sessionID string, stats SessionStatistics) error {
	// Lock for thread-safe file operations
	c.mu.Lock()
	defer c.mu.Unlock()

	statsPath := filepath.Join(c.specstoryDir, "statistics.json")

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

	// Add or update the session statistics
	statsFile.Sessions[sessionID] = stats

	// Marshal to JSON with indentation for readability
	jsonData, err := json.MarshalIndent(statsFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal statistics: %w", err)
	}

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

	slog.Debug("Updated session statistics", "sessionId", sessionID, "path", statsPath)
	return nil
}
