package main

import (
	"testing"
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

func TestNormalizeDeviceCode(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedCode  string
		expectedValid bool
	}{
		// Valid cases
		{
			name:          "valid code without dash",
			input:         "ABC123",
			expectedCode:  "ABC123",
			expectedValid: true,
		},
		{
			name:          "valid code with dash",
			input:         "ABC-123",
			expectedCode:  "ABC123",
			expectedValid: true,
		},
		{
			name:          "valid code lowercase",
			input:         "abc-123",
			expectedCode:  "abc123",
			expectedValid: true,
		},
		{
			name:          "valid code mixed case no dash",
			input:         "aBc1D2",
			expectedCode:  "aBc1D2",
			expectedValid: true,
		},
		{
			name:          "valid code mixed case with dash",
			input:         "aBc-1D2",
			expectedCode:  "aBc1D2",
			expectedValid: true,
		},
		{
			name:          "valid all numbers",
			input:         "123456",
			expectedCode:  "123456",
			expectedValid: true,
		},
		{
			name:          "valid all numbers with dash",
			input:         "123-456",
			expectedCode:  "123456",
			expectedValid: true,
		},
		{
			name:          "valid all letters uppercase",
			input:         "ABCDEF",
			expectedCode:  "ABCDEF",
			expectedValid: true,
		},
		{
			name:          "valid all letters lowercase",
			input:         "abcdef",
			expectedCode:  "abcdef",
			expectedValid: true,
		},
		// Invalid cases
		{
			name:          "double dash",
			input:         "ABC--123",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "dash in wrong position",
			input:         "AB-C123",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "too long",
			input:         "ABC1234",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "too long with dash",
			input:         "ABC-1234",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "too short",
			input:         "ABC12",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "too short with dash",
			input:         "ABC-12",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "special characters exclamation",
			input:         "ABC!23",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "special characters with dash",
			input:         "ABC-!23",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "spaces",
			input:         "ABC 123",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "underscore",
			input:         "ABC_123",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "empty string",
			input:         "",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "only dash",
			input:         "-",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "multiple dashes",
			input:         "A-B-C-1-2-3",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "dash at beginning",
			input:         "-ABC123",
			expectedCode:  "",
			expectedValid: false,
		},
		{
			name:          "dash at end",
			input:         "ABC123-",
			expectedCode:  "",
			expectedValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, valid := normalizeDeviceCode(tt.input)
			if valid != tt.expectedValid {
				t.Errorf("normalizeDeviceCode(%q) valid = %v, want %v", tt.input, valid, tt.expectedValid)
			}
			if code != tt.expectedCode {
				t.Errorf("normalizeDeviceCode(%q) code = %q, want %q", tt.input, code, tt.expectedCode)
			}
		})
	}
}
