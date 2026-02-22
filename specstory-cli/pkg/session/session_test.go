package session

import (
	"testing"
	"time"
)

func TestFormatFilenameTimestamp(t *testing.T) {
	// Create a fixed timezone for predictable UTC conversion tests
	fixedZone := time.FixedZone("TEST", -7*60*60) // UTC-7

	tests := []struct {
		name     string
		time     time.Time
		useUTC   bool
		expected string
	}{
		{
			name:     "UTC format - standard time",
			time:     time.Date(2026, 1, 25, 15, 30, 45, 0, time.UTC),
			useUTC:   true,
			expected: "2026-01-25_15-30-45Z",
		},
		{
			name:     "UTC format - midnight",
			time:     time.Date(2026, 1, 25, 0, 0, 0, 0, time.UTC),
			useUTC:   true,
			expected: "2026-01-25_00-00-00Z",
		},
		{
			name:     "UTC format - end of day",
			time:     time.Date(2026, 1, 25, 23, 59, 59, 0, time.UTC),
			useUTC:   true,
			expected: "2026-01-25_23-59-59Z",
		},
		{
			name:     "UTC format - time in non-UTC zone gets converted",
			time:     time.Date(2026, 1, 25, 8, 30, 45, 0, fixedZone), // 8:30 in UTC-7 = 15:30 UTC
			useUTC:   true,
			expected: "2026-01-25_15-30-45Z",
		},
		{
			name:     "UTC format - leap year date",
			time:     time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			useUTC:   true,
			expected: "2024-02-29_12-00-00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatFilenameTimestamp(tt.time, tt.useUTC)
			if result != tt.expected {
				t.Errorf("FormatFilenameTimestamp() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatFilenameTimestamp_LocalTimezone(t *testing.T) {
	// Compute expected output using the same logic as the function under test.
	// This makes the test deterministic regardless of machine timezone.
	testTime := time.Date(2026, 1, 25, 15, 30, 45, 0, time.UTC)
	localTime := testTime.Local()
	expected := localTime.Format("2006-01-02_15-04-05-0700")

	result := FormatFilenameTimestamp(testTime, false)

	if result != expected {
		t.Errorf("FormatFilenameTimestamp() = %q, want %q", result, expected)
	}
}
