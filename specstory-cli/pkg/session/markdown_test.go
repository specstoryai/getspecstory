package session

import (
	"testing"
	"time"
)

func TestFormatTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		timestamp string
		useUTC    bool
		expected  string
	}{
		{
			name:      "valid RFC3339 timestamp with UTC - useUTC true",
			timestamp: "2026-01-25T15:30:45Z",
			useUTC:    true,
			expected:  "2026-01-25 15:30:45Z",
		},
		{
			name:      "valid RFC3339 with positive offset - useUTC true converts to UTC",
			timestamp: "2026-01-25T17:30:45+02:00",
			useUTC:    true,
			expected:  "2026-01-25 15:30:45Z", // +02:00 converted to UTC
		},
		{
			name:      "valid RFC3339 with negative offset - useUTC true converts to UTC",
			timestamp: "2026-01-25T08:30:45-07:00",
			useUTC:    true,
			expected:  "2026-01-25 15:30:45Z", // -07:00 converted to UTC
		},
		{
			name:      "midnight UTC",
			timestamp: "2026-01-25T00:00:00Z",
			useUTC:    true,
			expected:  "2026-01-25 00:00:00Z",
		},
		{
			name:      "end of day UTC",
			timestamp: "2026-01-25T23:59:59Z",
			useUTC:    true,
			expected:  "2026-01-25 23:59:59Z",
		},
		{
			name:      "leap year date",
			timestamp: "2024-02-29T12:00:00Z",
			useUTC:    true,
			expected:  "2024-02-29 12:00:00Z",
		},
		{
			name:      "invalid timestamp - returns original",
			timestamp: "not-a-timestamp",
			useUTC:    true,
			expected:  "not-a-timestamp",
		},
		{
			name:      "empty timestamp - returns empty",
			timestamp: "",
			useUTC:    true,
			expected:  "",
		},
		{
			name:      "partial timestamp - returns original",
			timestamp: "2026-01-25",
			useUTC:    true,
			expected:  "2026-01-25",
		},
		{
			name:      "timestamp with milliseconds - parsed correctly",
			timestamp: "2026-01-25T15:30:45.123Z",
			useUTC:    true,
			expected:  "2026-01-25 15:30:45Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTimestamp(tt.timestamp, tt.useUTC)
			if result != tt.expected {
				t.Errorf("formatTimestamp(%q, %v) = %q, want %q",
					tt.timestamp, tt.useUTC, result, tt.expected)
			}
		})
	}
}

func TestFormatTimestamp_LocalTimezone(t *testing.T) {
	// Compute expected output using the same logic as the function under test.
	// This makes the test deterministic regardless of machine timezone.
	tests := []struct {
		name      string
		timestamp string
	}{
		{
			name:      "valid timestamp produces offset format",
			timestamp: "2026-01-25T15:30:45Z",
		},
		{
			name:      "timestamp with offset",
			timestamp: "2026-01-25T08:30:45-07:00",
		},
		{
			name:      "timestamp with milliseconds - milliseconds stripped",
			timestamp: "2026-01-25T15:30:45.123Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Compute expected value by parsing and formatting in local timezone
			parsed, _ := time.Parse(time.RFC3339, tt.timestamp)
			expected := parsed.Local().Format("2006-01-02 15:04:05-0700")

			result := formatTimestamp(tt.timestamp, false)

			if result != expected {
				t.Errorf("formatTimestamp(%q, false) = %q, want %q",
					tt.timestamp, result, expected)
			}
		})
	}
}
