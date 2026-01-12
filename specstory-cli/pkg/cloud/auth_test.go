package cloud

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsAuthenticated(t *testing.T) {
	// Create a temporary directory for test auth files
	tempDir := t.TempDir()

	// Override home directory for testing
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempDir); err != nil {
		t.Fatalf("Failed to set HOME env var: %v", err)
	}
	defer func() {
		if err := os.Setenv("HOME", origHome); err != nil {
			t.Errorf("Failed to restore HOME env var: %v", err)
		}
	}()

	// Create the .specstory/cli directory structure
	authDir := filepath.Join(tempDir, ".specstory", "cli")
	authPath := filepath.Join(authDir, "auth.json")

	tests := []struct {
		name           string
		setup          func()
		expectedResult bool
		description    string
	}{
		{
			name: "auth file does not exist",
			setup: func() {
				// Reset cache before each test
				ResetAuthCache()
				// Ensure directory doesn't exist
				_ = os.RemoveAll(filepath.Join(tempDir, ".specstory"))
			},
			expectedResult: false,
			description:    "Should return false when auth.json doesn't exist",
		},
		{
			name: "auth file exists with valid cloud token",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				futureTime := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
				authData := AuthData{
					CloudAccess: &CloudAccessData{
						Token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
						UpdatedAt: time.Now().UTC().Format(time.RFC3339),
						ExpiresAt: futureTime,
					},
				}
				data, _ := json.Marshal(authData)
				if err := os.WriteFile(authPath, data, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				}
			},
			expectedResult: true,
			description:    "Should return true when auth.json exists with valid cloud token",
		},
		{
			name: "auth file exists with expired cloud token",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				pastTime := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
				authData := AuthData{
					CloudAccess: &CloudAccessData{
						Token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
						UpdatedAt: time.Now().UTC().Format(time.RFC3339),
						ExpiresAt: pastTime,
					},
				}
				data, _ := json.Marshal(authData)
				if err := os.WriteFile(authPath, data, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when cloud token is expired",
		},
		{
			name: "auth file exists but cannot be parsed as JSON",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				if err := os.WriteFile(authPath, []byte("invalid json"), 0644); err != nil {
					t.Fatalf("Failed to write invalid auth file: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when auth.json is not valid JSON",
		},
		{
			name: "auth file exists but no authentication tokens",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				data := map[string]string{
					"other_key": "some_value",
				}
				jsonData, _ := json.Marshal(data)
				if err := os.WriteFile(authPath, jsonData, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when no authentication tokens are present",
		},
		{
			name: "auth file exists with empty cloud token",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				authData := AuthData{
					CloudAccess: &CloudAccessData{
						Token: "",
					},
				}
				data, _ := json.Marshal(authData)
				if err := os.WriteFile(authPath, data, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when cloud token is empty",
		},
		{
			name: "auth file exists with invalid cloud token format - not three parts",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				authData := AuthData{
					CloudAccess: &CloudAccessData{
						Token: "invalid.jwt",
					},
				}
				data, _ := json.Marshal(authData)
				if err := os.WriteFile(authPath, data, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when JWT doesn't have three parts",
		},
		{
			name: "auth file exists with invalid cloud token format - invalid base64",
			setup: func() {
				ResetAuthCache()
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				authData := AuthData{
					CloudAccess: &CloudAccessData{
						Token: "not-base64!@#.also-not-base64!@#.definitely-not-base64!@#",
					},
				}
				data, _ := json.Marshal(authData)
				if err := os.WriteFile(authPath, data, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when JWT parts are not valid base64",
		},
		{
			name: "auth file is a directory instead of file",
			setup: func() {
				ResetAuthCache()
				// Clean up any existing file/directory
				_ = os.RemoveAll(authPath)
				// First create the parent directory
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				// Then create a directory where the file should be
				if err := os.Mkdir(authPath, 0755); err != nil {
					t.Fatalf("Failed to create directory at auth path: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when auth.json is a directory",
		},
		{
			name: "auth file exists but no read permissions",
			setup: func() {
				ResetAuthCache()
				// Clean up any existing file/directory
				_ = os.RemoveAll(authPath)
				if err := os.MkdirAll(authDir, 0755); err != nil {
					t.Fatalf("Failed to create auth directory: %v", err)
				}
				futureTime := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
				authData := AuthData{
					CloudAccess: &CloudAccessData{
						Token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
						UpdatedAt: time.Now().UTC().Format(time.RFC3339),
						ExpiresAt: futureTime,
					},
				}
				data, _ := json.Marshal(authData)
				if err := os.WriteFile(authPath, data, 0644); err != nil {
					t.Fatalf("Failed to write auth file: %v", err)
				} // Write with normal permissions first
				if err := os.Chmod(authPath, 0000); err != nil {
					t.Fatalf("Failed to change file permissions: %v", err)
				}
			},
			expectedResult: false,
			description:    "Should return false when auth.json cannot be read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			result := IsAuthenticated()
			if result != tt.expectedResult {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expectedResult, result)
			}
		})
	}
}

func TestIsTokenExpired(t *testing.T) {
	tests := []struct {
		name           string
		expiresAt      string
		expectedResult bool
		description    string
	}{
		{
			name:           "empty expiry string",
			expiresAt:      "",
			expectedResult: true,
			description:    "Should return true for empty expiry",
		},
		{
			name:           "invalid RFC3339 format",
			expiresAt:      "not-a-date",
			expectedResult: true,
			description:    "Should return true for invalid date format",
		},
		{
			name:           "expired token",
			expiresAt:      time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
			expectedResult: true,
			description:    "Should return true for expired token",
		},
		{
			name:           "token expiring in 3 minutes",
			expiresAt:      time.Now().UTC().Add(3 * time.Minute).Format(time.RFC3339),
			expectedResult: true,
			description:    "Should return true for token expiring within buffer window",
		},
		{
			name:           "token expiring in exactly 5 minutes",
			expiresAt:      time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339),
			expectedResult: true,
			description:    "Should return true for token expiring exactly at buffer boundary (After includes equality)",
		},
		{
			name:           "token expiring in 10 minutes",
			expiresAt:      time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339),
			expectedResult: false,
			description:    "Should return false for token expiring outside buffer window",
		},
		{
			name:           "token expired 10 minutes ago",
			expiresAt:      time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339),
			expectedResult: true,
			description:    "Should return true for token expired 10 minutes ago",
		},
		{
			name:           "token expiring in 24 hours",
			expiresAt:      time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
			expectedResult: false,
			description:    "Should return false for token with plenty of time left",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTokenExpired(tt.expiresAt)
			if result != tt.expectedResult {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expectedResult, result)
			}
		})
	}
}

func TestAuthenticationCaching(t *testing.T) {
	// Create a temporary directory for test auth files
	tempDir := t.TempDir()

	// Override home directory for testing
	origHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", tempDir); err != nil {
		t.Fatalf("Failed to set HOME env var: %v", err)
	}
	defer func() {
		if err := os.Setenv("HOME", origHome); err != nil {
			t.Errorf("Failed to restore HOME env var: %v", err)
		}
	}()

	// Create the .specstory/cli directory structure
	authDir := filepath.Join(tempDir, ".specstory", "cli")
	authPath := filepath.Join(authDir, "auth.json")

	// Reset cache before test
	ResetAuthCache()

	// Initially no auth file
	result1 := IsAuthenticated()
	if result1 != false {
		t.Errorf("Expected false when no auth file exists, got %v", result1)
	}

	// Create auth file
	if err := os.MkdirAll(authDir, 0755); err != nil {
		t.Fatalf("Failed to create auth directory: %v", err)
	}
	futureTime := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	authData := AuthData{
		CloudAccess: &CloudAccessData{
			Token:     "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			ExpiresAt: futureTime,
		},
	}
	data, _ := json.Marshal(authData)
	if err := os.WriteFile(authPath, data, 0644); err != nil {
		t.Fatalf("Failed to write auth file: %v", err)
	}

	// Second call should still return false (cached)
	result2 := IsAuthenticated()
	if result2 != false {
		t.Errorf("Expected false from cache even after creating auth file, got %v", result2)
	}

	// Reset cache and try again
	ResetAuthCache()
	result3 := IsAuthenticated()
	if result3 != true {
		t.Errorf("Expected true after cache reset with valid auth file, got %v", result3)
	}

	// Remove auth file
	_ = os.RemoveAll(filepath.Join(tempDir, ".specstory"))

	// Should still return true (cached)
	result4 := IsAuthenticated()
	if result4 != true {
		t.Errorf("Expected true from cache even after removing auth file, got %v", result4)
	}
}
