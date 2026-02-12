package main

import (
	"testing"
	"time"
)

func TestValidateUUID(t *testing.T) {
	tests := []struct {
		name     string
		uuid     string
		expected bool
	}{
		{
			name:     "valid UUID",
			uuid:     "5c5c2876-febd-4c87-b80c-d0655f1cd3fd",
			expected: true,
		},
		{
			name:     "valid UUID with uppercase",
			uuid:     "5C5C2876-FEBD-4C87-B80C-D0655F1CD3FD",
			expected: true,
		},
		{
			name:     "valid UUID mixed case",
			uuid:     "5c5c2876-FeBd-4C87-b80C-d0655f1cd3FD",
			expected: true,
		},
		{
			name:     "invalid UUID - too short",
			uuid:     "5c5c2876-febd-4c87-b80c",
			expected: false,
		},
		{
			name:     "invalid UUID - too long",
			uuid:     "5c5c2876-febd-4c87-b80c-d0655f1cd3fd-extra",
			expected: false,
		},
		{
			name:     "invalid UUID - missing hyphens",
			uuid:     "5c5c2876febd4c87b80cd0655f1cd3fd",
			expected: false,
		},
		{
			name:     "invalid UUID - wrong hyphen positions",
			uuid:     "5c5c287-6febd-4c87-b80c-d0655f1cd3fd",
			expected: false,
		},
		{
			name:     "invalid UUID - non-hex characters",
			uuid:     "5c5c2876-febd-4c87-b80c-d0655f1cd3fz",
			expected: false,
		},
		{
			name:     "empty string",
			uuid:     "",
			expected: false,
		},
		{
			name:     "whitespace only",
			uuid:     "   ",
			expected: false,
		},
		{
			name:     "UUID with surrounding whitespace",
			uuid:     " 5c5c2876-febd-4c87-b80c-d0655f1cd3fd ",
			expected: false,
		},
		{
			name:     "random string",
			uuid:     "not-a-uuid",
			expected: false,
		},
		{
			name:     "session ID prefix",
			uuid:     "session-5c5c2876-febd-4c87-b80c-d0655f1cd3fd",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateUUID(tt.uuid)
			if result != tt.expected {
				t.Errorf("validateUUID(%q) = %v, want %v", tt.uuid, result, tt.expected)
			}
		})
	}
}

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
			result := formatFilenameTimestamp(tt.time, tt.useUTC)
			if result != tt.expected {
				t.Errorf("formatFilenameTimestamp() = %q, want %q", result, tt.expected)
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

	result := formatFilenameTimestamp(testTime, false)

	if result != expected {
		t.Errorf("formatFilenameTimestamp() = %q, want %q", result, expected)
	}
}

func TestValidateFlags_CloudSyncMutualExclusion(t *testing.T) {
	// Save original global flag values
	origOnlyCloudSync := onlyCloudSync
	origNoCloudSync := noCloudSync
	origConsole := console
	origSilent := silent
	origDebug := debug
	origLogFile := logFile

	// Restore original values after test
	defer func() {
		onlyCloudSync = origOnlyCloudSync
		noCloudSync = origNoCloudSync
		console = origConsole
		silent = origSilent
		debug = origDebug
		logFile = origLogFile
	}()

	tests := []struct {
		name          string
		onlyCloudSync bool
		noCloudSync   bool
		expectError   bool
	}{
		{
			name:          "both flags set - mutually exclusive error",
			onlyCloudSync: true,
			noCloudSync:   true,
			expectError:   true,
		},
		{
			name:          "only-cloud-sync alone - valid",
			onlyCloudSync: true,
			noCloudSync:   false,
			expectError:   false,
		},
		{
			name:          "no-cloud-sync alone - valid",
			onlyCloudSync: false,
			noCloudSync:   true,
			expectError:   false,
		},
		{
			name:          "neither flag set - valid",
			onlyCloudSync: false,
			noCloudSync:   false,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set flags to known valid state for other validations
			console = false
			silent = false
			debug = false
			logFile = false

			// Set the flags under test
			onlyCloudSync = tt.onlyCloudSync
			noCloudSync = tt.noCloudSync

			err := validateFlags()

			if tt.expectError && err == nil {
				t.Errorf("validateFlags() expected error for onlyCloudSync=%v, noCloudSync=%v, got nil",
					tt.onlyCloudSync, tt.noCloudSync)
			}
			if !tt.expectError && err != nil {
				t.Errorf("validateFlags() unexpected error for onlyCloudSync=%v, noCloudSync=%v: %v",
					tt.onlyCloudSync, tt.noCloudSync, err)
			}
		})
	}
}
