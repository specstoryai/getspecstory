package markdown

import (
	"testing"
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
	// Testing local timezone is tricky because output depends on the machine's timezone.
	// We test that the format structure is correct rather than exact values.

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTimestamp(tt.timestamp, false)

			// Result should be in format "2006-01-02 15:04:05-0700" or "+0700"
			// Length should be 24 characters (date + space + time + offset)
			if len(result) != 24 {
				t.Errorf("formatTimestamp(%q, false) = %q, expected length 24, got %d",
					tt.timestamp, result, len(result))
			}

			// Should contain a space between date and time
			if result[10] != ' ' {
				t.Errorf("formatTimestamp(%q, false) = %q, expected space at position 10",
					tt.timestamp, result)
			}

			// Last 5 characters should be timezone offset (+0000 or -0000 format)
			offset := result[19:]
			if len(offset) != 5 {
				t.Errorf("formatTimestamp(%q, false) offset = %q, expected 5 chars",
					tt.timestamp, offset)
			}
			if offset[0] != '+' && offset[0] != '-' {
				t.Errorf("formatTimestamp(%q, false) offset = %q, expected to start with + or -",
					tt.timestamp, offset)
			}
		})
	}
}
