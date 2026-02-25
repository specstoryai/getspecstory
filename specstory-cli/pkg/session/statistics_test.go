package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestComputeSessionStatistics(t *testing.T) {
	tests := []struct {
		name              string
		sessionData       *schema.SessionData
		markdownContent   string
		providerID        string
		expectedUserMsgs  int
		expectedAgentMsgs int
		expectedStart     string
		expectedEnd       string
	}{
		{
			name: "single exchange with both timestamps",
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
			markdownContent:   "# Test\n\nSome content",
			providerID:        "claude",
			expectedUserMsgs:  1,
			expectedAgentMsgs: 1,
			expectedStart:     "2026-02-09T10:00:00Z",
			expectedEnd:       "2026-02-09T10:15:00Z",
		},
		{
			name: "multiple exchanges uses first start and last end",
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
			markdownContent:   "# Test\n\nMore content",
			providerID:        "codex",
			expectedUserMsgs:  3,
			expectedAgentMsgs: 3,
			expectedStart:     "2026-02-09T10:00:00Z",
			expectedEnd:       "2026-02-09T10:30:00Z",
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
			markdownContent:   "# Test\n\nAgent-only content",
			providerID:        "cursor",
			expectedUserMsgs:  0,
			expectedAgentMsgs: 1,
			expectedStart:     "2026-02-09T10:00:00Z",
			expectedEnd:       "2026-02-09T10:05:00Z",
		},
		{
			name: "empty exchanges falls back to CreatedAt for both timestamps",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T09:00:00Z",
				UpdatedAt: "",
				Exchanges: []schema.Exchange{},
			},
			markdownContent:   "",
			providerID:        "claude",
			expectedUserMsgs:  0,
			expectedAgentMsgs: 0,
			expectedStart:     "2026-02-09T09:00:00Z",
			expectedEnd:       "2026-02-09T09:00:00Z",
		},
		{
			name: "empty exchanges with UpdatedAt uses UpdatedAt for end",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T09:00:00Z",
				UpdatedAt: "2026-02-09T09:30:00Z",
				Exchanges: []schema.Exchange{},
			},
			markdownContent:   "# Empty",
			providerID:        "codex",
			expectedUserMsgs:  0,
			expectedAgentMsgs: 0,
			expectedStart:     "2026-02-09T09:00:00Z",
			expectedEnd:       "2026-02-09T09:30:00Z",
		},
		{
			name: "exchange missing EndTime falls back to UpdatedAt",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T10:00:00Z",
				UpdatedAt: "2026-02-09T10:20:00Z",
				Exchanges: []schema.Exchange{
					{
						StartTime: "2026-02-09T10:00:00Z",
						EndTime:   "",
						Messages: []schema.Message{
							{Role: schema.RoleUser},
						},
					},
				},
			},
			markdownContent:   "# Test",
			providerID:        "claude",
			expectedUserMsgs:  1,
			expectedAgentMsgs: 0,
			expectedStart:     "2026-02-09T10:00:00Z",
			expectedEnd:       "2026-02-09T10:20:00Z",
		},
		{
			name: "exchange missing EndTime and empty UpdatedAt falls back to CreatedAt",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T10:00:00Z",
				UpdatedAt: "",
				Exchanges: []schema.Exchange{
					{
						StartTime: "2026-02-09T10:00:00Z",
						EndTime:   "",
						Messages: []schema.Message{
							{Role: schema.RoleUser},
						},
					},
				},
			},
			markdownContent:   "# Test",
			providerID:        "cursor",
			expectedUserMsgs:  1,
			expectedAgentMsgs: 0,
			expectedStart:     "2026-02-09T10:00:00Z",
			expectedEnd:       "2026-02-09T10:00:00Z",
		},
		{
			name: "exchange missing StartTime falls back to CreatedAt for start",
			sessionData: &schema.SessionData{
				CreatedAt: "2026-02-09T09:00:00Z",
				UpdatedAt: "2026-02-09T10:00:00Z",
				Exchanges: []schema.Exchange{
					{
						StartTime: "",
						EndTime:   "2026-02-09T10:00:00Z",
						Messages: []schema.Message{
							{Role: schema.RoleUser},
							{Role: schema.RoleAgent},
						},
					},
				},
			},
			markdownContent:   "# Test",
			providerID:        "claude",
			expectedUserMsgs:  1,
			expectedAgentMsgs: 1,
			expectedStart:     "2026-02-09T09:00:00Z",
			expectedEnd:       "2026-02-09T10:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := ComputeSessionStatistics(tt.sessionData, tt.markdownContent, tt.providerID)

			if stats.UserMessageCount != tt.expectedUserMsgs {
				t.Errorf("UserMessageCount = %d, want %d", stats.UserMessageCount, tt.expectedUserMsgs)
			}

			if stats.AgentMessageCount != tt.expectedAgentMsgs {
				t.Errorf("AgentMessageCount = %d, want %d", stats.AgentMessageCount, tt.expectedAgentMsgs)
			}

			if stats.MarkdownSizeBytes != len(tt.markdownContent) {
				t.Errorf("MarkdownSizeBytes = %d, want %d", stats.MarkdownSizeBytes, len(tt.markdownContent))
			}

			if stats.Provider != tt.providerID {
				t.Errorf("Provider = %s, want %s", stats.Provider, tt.providerID)
			}

			if stats.StartTimestamp != tt.expectedStart {
				t.Errorf("StartTimestamp = %q, want %q", stats.StartTimestamp, tt.expectedStart)
			}

			if stats.EndTimestamp != tt.expectedEnd {
				t.Errorf("EndTimestamp = %q, want %q", stats.EndTimestamp, tt.expectedEnd)
			}

			if stats.LastUpdated == "" {
				t.Error("LastUpdated should not be empty")
			}
		})
	}
}

// readStatisticsFile is a test helper that reads and parses the statistics.json file.
func readStatisticsFile(t *testing.T, dir string) StatisticsFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "statistics.json"))
	if err != nil {
		t.Fatalf("Failed to read statistics.json: %v", err)
	}
	var sf StatisticsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatalf("Failed to parse statistics.json: %v", err)
	}
	return sf
}

func TestStatisticsCollector_CreateNewFile(t *testing.T) {
	tempDir := t.TempDir()
	collector := NewStatisticsCollector(tempDir)

	stats := SessionStatistics{
		UserMessageCount:  5,
		AgentMessageCount: 8,
		StartTimestamp:    "2026-02-09T10:00:00Z",
		EndTimestamp:      "2026-02-09T10:15:00Z",
		MarkdownSizeBytes: 1234,
		Provider:          "claude",
		LastUpdated:       "2026-02-09T10:20:00Z",
	}

	if err := collector.AddSessionStats("session-1", stats); err != nil {
		t.Fatalf("AddSessionStats failed: %v", err)
	}

	if err := collector.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	sf := readStatisticsFile(t, tempDir)
	if len(sf.Sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(sf.Sessions))
	}
	retrieved := sf.Sessions["session-1"]
	if retrieved.UserMessageCount != 5 {
		t.Errorf("UserMessageCount = %d, want 5", retrieved.UserMessageCount)
	}
	if retrieved.Provider != "claude" {
		t.Errorf("Provider = %s, want claude", retrieved.Provider)
	}
}

func TestStatisticsCollector_UpdateExisting(t *testing.T) {
	tempDir := t.TempDir()
	collector := NewStatisticsCollector(tempDir)

	original := SessionStatistics{
		UserMessageCount:  5,
		AgentMessageCount: 8,
		StartTimestamp:    "2026-02-09T10:00:00Z",
		EndTimestamp:      "2026-02-09T10:15:00Z",
		MarkdownSizeBytes: 1234,
		Provider:          "claude",
		LastUpdated:       "2026-02-09T10:20:00Z",
	}
	if err := collector.AddSessionStats("session-1", original); err != nil {
		t.Fatalf("AddSessionStats (original) failed: %v", err)
	}
	if err := collector.Flush(); err != nil {
		t.Fatalf("Flush (original) failed: %v", err)
	}

	// Update same session with new counts
	updated := original
	updated.UserMessageCount = 7
	updated.LastUpdated = "2026-02-09T10:30:00Z"
	if err := collector.AddSessionStats("session-1", updated); err != nil {
		t.Fatalf("AddSessionStats (update) failed: %v", err)
	}
	if err := collector.Flush(); err != nil {
		t.Fatalf("Flush (update) failed: %v", err)
	}

	sf := readStatisticsFile(t, tempDir)
	if len(sf.Sessions) != 1 {
		t.Errorf("Expected 1 session after update, got %d", len(sf.Sessions))
	}
	if sf.Sessions["session-1"].UserMessageCount != 7 {
		t.Errorf("UserMessageCount = %d, want 7", sf.Sessions["session-1"].UserMessageCount)
	}
}

func TestStatisticsCollector_MultipleSessions(t *testing.T) {
	tempDir := t.TempDir()
	collector := NewStatisticsCollector(tempDir)

	sessions := map[string]SessionStatistics{
		"session-1": {
			UserMessageCount: 5, AgentMessageCount: 8,
			StartTimestamp: "2026-02-09T10:00:00Z", EndTimestamp: "2026-02-09T10:15:00Z",
			MarkdownSizeBytes: 1234, Provider: "claude", LastUpdated: "2026-02-09T10:20:00Z",
		},
		"session-2": {
			UserMessageCount: 3, AgentMessageCount: 4,
			StartTimestamp: "2026-02-09T11:00:00Z", EndTimestamp: "2026-02-09T11:10:00Z",
			MarkdownSizeBytes: 567, Provider: "codex", LastUpdated: "2026-02-09T11:15:00Z",
		},
	}

	for id, stats := range sessions {
		if err := collector.AddSessionStats(id, stats); err != nil {
			t.Fatalf("AddSessionStats(%s) failed: %v", id, err)
		}
	}

	if err := collector.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	sf := readStatisticsFile(t, tempDir)
	if len(sf.Sessions) != 2 {
		t.Errorf("Expected 2 sessions, got %d", len(sf.Sessions))
	}
	for id := range sessions {
		if _, exists := sf.Sessions[id]; !exists {
			t.Errorf("Session %s not found in statistics file", id)
		}
	}
}

func TestStatisticsCollector_CorruptJSON(t *testing.T) {
	tempDir := t.TempDir()
	statsPath := filepath.Join(tempDir, "statistics.json")

	// Seed a corrupt file
	if err := os.WriteFile(statsPath, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("Failed to write corrupt JSON: %v", err)
	}

	collector := NewStatisticsCollector(tempDir)
	stats := SessionStatistics{
		UserMessageCount:  2,
		AgentMessageCount: 3,
		StartTimestamp:    "2026-02-09T12:00:00Z",
		EndTimestamp:      "2026-02-09T12:05:00Z",
		MarkdownSizeBytes: 100,
		Provider:          "cursor",
		LastUpdated:       "2026-02-09T12:10:00Z",
	}

	if err := collector.AddSessionStats("session-corrupt", stats); err != nil {
		t.Fatalf("AddSessionStats after corrupt file failed: %v", err)
	}

	if err := collector.Flush(); err != nil {
		t.Fatalf("Flush after corrupt file failed: %v", err)
	}

	// Should have recovered with only the new session
	sf := readStatisticsFile(t, tempDir)
	if len(sf.Sessions) != 1 {
		t.Errorf("Expected 1 session after recovery, got %d", len(sf.Sessions))
	}
	if _, exists := sf.Sessions["session-corrupt"]; !exists {
		t.Error("Session session-corrupt not found after recovery")
	}
}
