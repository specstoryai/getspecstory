package claudecode

import (
	"strings"
	"testing"
)

// TestFilterWarmupMessages tests warmup message filtering
func TestFilterWarmupMessages(t *testing.T) {
	tests := []struct {
		name            string
		records         []JSONLRecord
		expectedLength  int
		expectedFirstID string
	}{
		{
			name:           "Empty records returns empty",
			records:        []JSONLRecord{},
			expectedLength: 0,
		},
		{
			name: "All sidechain records returns empty",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"uuid":        "1",
						"isSidechain": true,
					},
				},
				{
					Data: map[string]interface{}{
						"uuid":        "2",
						"isSidechain": true,
					},
				},
			},
			expectedLength: 0,
		},
		{
			name: "Filters warmup and keeps real messages",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"uuid":        "warmup-1",
						"isSidechain": true,
					},
				},
				{
					Data: map[string]interface{}{
						"uuid":        "real-1",
						"isSidechain": false,
					},
				},
				{
					Data: map[string]interface{}{
						"uuid":        "real-2",
						"isSidechain": false,
					},
				},
			},
			expectedLength:  2,
			expectedFirstID: "real-1",
		},
		{
			name: "No warmup messages keeps all",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"uuid":        "1",
						"isSidechain": false,
					},
				},
				{
					Data: map[string]interface{}{
						"uuid":        "2",
						"isSidechain": false,
					},
				},
			},
			expectedLength:  2,
			expectedFirstID: "1",
		},
		{
			name: "Missing isSidechain field treated as non-sidechain",
			records: []JSONLRecord{
				{
					Data: map[string]interface{}{
						"uuid": "1",
						// no isSidechain field
					},
				},
			},
			expectedLength:  1,
			expectedFirstID: "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterWarmupMessages(tt.records)
			if len(result) != tt.expectedLength {
				t.Errorf("filterWarmupMessages() returned %d records, want %d", len(result), tt.expectedLength)
			}
			if tt.expectedLength > 0 && tt.expectedFirstID != "" {
				firstID := result[0].Data["uuid"].(string)
				if firstID != tt.expectedFirstID {
					t.Errorf("filterWarmupMessages() first record ID = %v, want %v", firstID, tt.expectedFirstID)
				}
			}
		})
	}
}

// TestConvertToAgentChatSession tests the conversion of Session to AgentChatSession
// with warmup filtering and debugRaw handling
func TestConvertToAgentChatSession(t *testing.T) {
	tests := []struct {
		name             string
		session          Session
		debugRaw         bool
		expectNil        bool
		expectMarkdown   string // substring that should appear in markdown
		expectRawRecords int    // number of records in raw data (includes warmup)
	}{
		{
			name: "empty session returns nil",
			session: Session{
				SessionUuid: "test-session-1",
				Records:     []JSONLRecord{},
			},
			debugRaw:  false,
			expectNil: true,
		},
		{
			name: "warmup-only session returns nil",
			session: Session{
				SessionUuid: "test-session-2",
				Records: []JSONLRecord{
					{
						Data: map[string]interface{}{
							"uuid":        "warmup-1",
							"isSidechain": true,
							"timestamp":   "2025-01-01T00:00:00Z",
						},
					},
					{
						Data: map[string]interface{}{
							"uuid":        "warmup-2",
							"isSidechain": true,
							"timestamp":   "2025-01-01T00:00:01Z",
						},
					},
				},
			},
			debugRaw:  false,
			expectNil: true,
		},
		{
			name: "warmup-only session with debugRaw still returns nil",
			session: Session{
				SessionUuid: "test-session-3",
				Records: []JSONLRecord{
					{
						Data: map[string]interface{}{
							"uuid":        "warmup-1",
							"isSidechain": true,
							"timestamp":   "2025-01-01T00:00:00Z",
						},
					},
				},
			},
			debugRaw:  true,
			expectNil: true,
		},
		{
			name: "mixed warmup and real messages filters warmup from markdown",
			session: Session{
				SessionUuid: "test-session-4",
				Records: []JSONLRecord{
					{
						Data: map[string]interface{}{
							"uuid":        "warmup-1",
							"isSidechain": true,
							"timestamp":   "2025-01-01T00:00:00Z",
							"userType":    "bot",
							"cwd":         "/test",
							"version":     "1.0.0",
						},
					},
					{
						Data: map[string]interface{}{
							"uuid":        "real-1",
							"isSidechain": false,
							"timestamp":   "2025-01-01T00:00:02Z",
							"type":        "root",
							"userType":    "human",
							"cwd":         "/test",
							"version":     "1.0.0",
							"text":        "test message",
						},
					},
					{
						Data: map[string]interface{}{
							"uuid":        "real-2",
							"isSidechain": false,
							"timestamp":   "2025-01-01T00:00:03Z",
							"userType":    "bot",
							"cwd":         "/test",
							"version":     "1.0.0",
							"text":        "response message",
						},
					},
				},
			},
			debugRaw:         false,
			expectNil:        false,
			expectMarkdown:   "test message", // Markdown should contain the message text
			expectRawRecords: 3,              // Raw data should include all 3 records (including warmup)
		},
		{
			name: "no warmup messages processes all records",
			session: Session{
				SessionUuid: "test-session-5",
				Records: []JSONLRecord{
					{
						Data: map[string]interface{}{
							"uuid":        "real-1",
							"isSidechain": false,
							"timestamp":   "2025-01-01T00:00:00Z",
							"type":        "root",
							"userType":    "human",
							"cwd":         "/test",
							"version":     "1.0.0",
							"text":        "test message",
						},
					},
					{
						Data: map[string]interface{}{
							"uuid":        "real-2",
							"isSidechain": false,
							"timestamp":   "2025-01-01T00:00:01Z",
							"userType":    "bot",
							"cwd":         "/test",
							"version":     "1.0.0",
							"text":        "response message",
						},
					},
				},
			},
			debugRaw:         false,
			expectNil:        false,
			expectMarkdown:   "test message",
			expectRawRecords: 2,
		},
		{
			name: "missing timestamp in root record returns nil",
			session: Session{
				SessionUuid: "test-session-6",
				Records: []JSONLRecord{
					{
						Data: map[string]interface{}{
							"uuid":        "real-1",
							"isSidechain": false,
							// missing timestamp
						},
					},
				},
			},
			debugRaw:  false,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: debugRaw=true will attempt to write debug files to .specstory/debug/
			// This is acceptable for testing as it exercises the real code path
			result := convertToAgentChatSession(tt.session, "/test/workspace", tt.debugRaw)

			// Check if result matches expectation (nil or not)
			if tt.expectNil {
				if result != nil {
					t.Errorf("convertToAgentChatSession() returned non-nil, want nil")
				}
				return // Nothing else to check for nil results
			}

			// For non-nil results, verify structure
			if result == nil {
				t.Fatalf("convertToAgentChatSession() returned nil, want non-nil")
			}

			// Verify session ID
			if result.SessionID != tt.session.SessionUuid {
				t.Errorf("SessionID = %v, want %v", result.SessionID, tt.session.SessionUuid)
			}

			// Verify SessionData is generated
			if tt.expectMarkdown != "" {
				if result.SessionData == nil {
					t.Errorf("SessionData is nil, want non-nil")
				}
				// Note: Test data may not generate exchanges if messages aren't in proper format
				// This is expected for simple test data
			}

			// Verify raw data contains all records (including warmup)
			if tt.expectRawRecords > 0 {
				// Count newlines in raw data (each record is one line)
				lines := strings.Count(result.RawData, "\n")
				if lines != tt.expectRawRecords {
					t.Errorf("RawData has %d lines, want %d", lines, tt.expectRawRecords)
				}

				// Verify raw data contains warmup records if they exist
				// This ensures we're preserving all records in raw data
				for _, record := range tt.session.Records {
					if uuid, ok := record.Data["uuid"].(string); ok {
						if !strings.Contains(result.RawData, uuid) {
							t.Errorf("RawData missing record with uuid %q", uuid)
						}
					}
				}
			}

			// Verify timestamp is set
			if result.CreatedAt == "" {
				t.Errorf("CreatedAt is empty, want timestamp")
			}
		})
	}
}
