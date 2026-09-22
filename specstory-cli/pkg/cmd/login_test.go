package cmd

import "testing"

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
