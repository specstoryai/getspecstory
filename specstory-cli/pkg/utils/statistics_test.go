package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestComputeSessionStatistics(t *testing.T) {
	tests := []struct {
		name             string
		sessionData      *schema.SessionData
		markdownContent  string
		providerID       string
		expectedUserMsgs int
	}{
		{
			name: "single user message",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T10:00:00Z",
				UpdatedAt: "2026-02-09T10:15:00Z",
				Exchanges: []schema.Exchange{
					{
						StartTime: "2026-02-09T10:00:00Z",
						EndTime:   "2026-02-09T10:15:00Z",
						Messages: []schema.Message{
							{Role: schema.RoleUser},
							{Role: schema.RoleAgent},
						},
					},
				},
			},
			markdownContent:  "# Test\n\nSome content",
			providerID:       "claude",
			expectedUserMsgs: 1,
		},
		{
			name: "multiple exchanges with multiple user messages",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T10:00:00Z",
				UpdatedAt: "2026-02-09T10:30:00Z",
				Exchanges: []schema.Exchange{
					{
						StartTime: "2026-02-09T10:00:00Z",
						EndTime:   "2026-02-09T10:10:00Z",
						Messages: []schema.Message{
							{Role: schema.RoleUser},
							{Role: schema.RoleAgent},
						},
					},
					{
						StartTime: "2026-02-09T10:15:00Z",
						EndTime:   "2026-02-09T10:30:00Z",
						Messages: []schema.Message{
							{Role: schema.RoleUser},
							{Role: schema.RoleAgent},
							{Role: schema.RoleUser},
							{Role: schema.RoleAgent},
						},
					},
				},
			},
			markdownContent:  "# Test\n\nMore content",
			providerID:       "codex",
			expectedUserMsgs: 3,
		},
		{
			name: "no user messages",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T10:00:00Z",
				UpdatedAt: "2026-02-09T10:05:00Z",
				Exchanges: []schema.Exchange{
					{
						StartTime: "2026-02-09T10:00:00Z",
						EndTime:   "2026-02-09T10:05:00Z",
						Messages: []schema.Message{
							{Role: schema.RoleAgent},
						},
					},
				},
			},
			markdownContent:  "# Test\n\nAgent-only content",
			providerID:       "cursor",
			expectedUserMsgs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := ComputeSessionStatistics(tt.sessionData, tt.markdownContent, tt.providerID)

			if stats.UserMessageCount != tt.expectedUserMsgs {
				t.Errorf("UserMessageCount = %d, want %d", stats.UserMessageCount, tt.expectedUserMsgs)
			}

			if stats.MarkdownSizeBytes != len(tt.markdownContent) {
				t.Errorf("MarkdownSizeBytes = %d, want %d", stats.MarkdownSizeBytes, len(tt.markdownContent))
			}

			if stats.Provider != tt.providerID {
				t.Errorf("Provider = %s, want %s", stats.Provider, tt.providerID)
			}

			if stats.StartTimestamp == "" {
				t.Error("StartTimestamp should not be empty")
			}

			if stats.EndTimestamp == "" {
				t.Error("EndTimestamp should not be empty")
			}

			if stats.LastUpdated == "" {
				t.Error("LastUpdated should not be empty")
			}
		})
	}
}

func TestStatisticsCollector(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	collector := NewStatisticsCollector(tempDir)
	sessionID := "test-session-123"

	stats := SessionStatistics{
		UserMessageCount:  5,
		StartTimestamp:    "2026-02-09T10:00:00Z",
		EndTimestamp:      "2026-02-09T10:15:00Z",
		MarkdownSizeBytes: 1234,
		Provider:          "claude",
		LastUpdated:       "2026-02-09T10:20:00Z",
	}

	// Test adding statistics
	err := collector.AddSessionStats(sessionID, stats)
	if err != nil {
		t.Fatalf("Failed to add session stats: %v", err)
	}

	// Verify the file was created
	statsPath := filepath.Join(tempDir, "statistics.json")
	if _, err := os.Stat(statsPath); os.IsNotExist(err) {
		t.Fatal("statistics.json was not created")
	}

	// Read and verify the content
	data, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("Failed to read statistics.json: %v", err)
	}

	var statsFile StatisticsFile
	if err := json.Unmarshal(data, &statsFile); err != nil {
		t.Fatalf("Failed to parse statistics.json: %v", err)
	}

	if _, exists := statsFile.Sessions[sessionID]; !exists {
		t.Errorf("Session %s not found in statistics file", sessionID)
	}

	retrievedStats := statsFile.Sessions[sessionID]
	if retrievedStats.UserMessageCount != stats.UserMessageCount {
		t.Errorf("UserMessageCount = %d, want %d", retrievedStats.UserMessageCount, stats.UserMessageCount)
	}

	// Test updating existing statistics
	updatedStats := stats
	updatedStats.UserMessageCount = 7
	updatedStats.LastUpdated = "2026-02-09T10:30:00Z"

	err = collector.AddSessionStats(sessionID, updatedStats)
	if err != nil {
		t.Fatalf("Failed to update session stats: %v", err)
	}

	// Verify the update
	data, err = os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("Failed to read statistics.json after update: %v", err)
	}

	if err := json.Unmarshal(data, &statsFile); err != nil {
		t.Fatalf("Failed to parse statistics.json after update: %v", err)
	}

	retrievedStats = statsFile.Sessions[sessionID]
	if retrievedStats.UserMessageCount != 7 {
		t.Errorf("After update: UserMessageCount = %d, want 7", retrievedStats.UserMessageCount)
	}

	// Test adding a second session
	secondSessionID := "test-session-456"
	secondStats := SessionStatistics{
		UserMessageCount:  3,
		StartTimestamp:    "2026-02-09T11:00:00Z",
		EndTimestamp:      "2026-02-09T11:10:00Z",
		MarkdownSizeBytes: 567,
		Provider:          "codex",
		LastUpdated:       "2026-02-09T11:15:00Z",
	}

	err = collector.AddSessionStats(secondSessionID, secondStats)
	if err != nil {
		t.Fatalf("Failed to add second session stats: %v", err)
	}

	// Verify both sessions exist
	data, err = os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("Failed to read statistics.json after second session: %v", err)
	}

	if err := json.Unmarshal(data, &statsFile); err != nil {
		t.Fatalf("Failed to parse statistics.json after second session: %v", err)
	}

	if len(statsFile.Sessions) != 2 {
		t.Errorf("Expected 2 sessions, got %d", len(statsFile.Sessions))
	}

	if _, exists := statsFile.Sessions[secondSessionID]; !exists {
		t.Errorf("Second session %s not found in statistics file", secondSessionID)
	}
}

func TestStatisticsCollector_CorruptJSON(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	statsPath := filepath.Join(tempDir, "statistics.json")

	// Write corrupt JSON
	err := os.WriteFile(statsPath, []byte("{invalid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to write corrupt JSON: %v", err)
	}

	// Try to add statistics - should start fresh
	collector := NewStatisticsCollector(tempDir)
	sessionID := "test-session-789"
	stats := SessionStatistics{
		UserMessageCount:  2,
		StartTimestamp:    "2026-02-09T12:00:00Z",
		EndTimestamp:      "2026-02-09T12:05:00Z",
		MarkdownSizeBytes: 100,
		Provider:          "cursor",
		LastUpdated:       "2026-02-09T12:10:00Z",
	}

	err = collector.AddSessionStats(sessionID, stats)
	if err != nil {
		t.Fatalf("Failed to add session stats after corrupt JSON: %v", err)
	}

	// Verify the file was recreated with valid JSON
	data, err := os.ReadFile(statsPath)
	if err != nil {
		t.Fatalf("Failed to read statistics.json after recovery: %v", err)
	}

	var statsFile StatisticsFile
	if err := json.Unmarshal(data, &statsFile); err != nil {
		t.Fatalf("Failed to parse statistics.json after recovery: %v", err)
	}

	if len(statsFile.Sessions) != 1 {
		t.Errorf("Expected 1 session after recovery, got %d", len(statsFile.Sessions))
	}

	if _, exists := statsFile.Sessions[sessionID]; !exists {
		t.Errorf("Session %s not found after recovery", sessionID)
	}
}
