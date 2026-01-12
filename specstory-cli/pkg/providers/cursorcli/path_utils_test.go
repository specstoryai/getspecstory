package cursorcli

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetCursorChatsDir(t *testing.T) {
	result, err := GetCursorChatsDir()
	if err != nil {
		t.Errorf("GetCursorChatsDir() returned error: %v", err)
		return
	}

	// Should end with .cursor/chats
	if !strings.HasSuffix(result, filepath.Join(".cursor", "chats")) {
		t.Errorf("GetCursorChatsDir() = %s, expected to end with .cursor/chats", result)
	}

	// Should start with home directory
	homeDir, _ := os.UserHomeDir()
	if !strings.HasPrefix(result, homeDir) {
		t.Errorf("GetCursorChatsDir() = %s, expected to start with home directory %s", result, homeDir)
	}
}

func TestGetProjectHashDir(t *testing.T) {
	tests := []struct {
		name        string
		projectPath string
		wantErr     bool
		validate    func(t *testing.T, result string)
	}{
		{
			name:        "current directory",
			projectPath: ".",
			wantErr:     false,
			validate: func(t *testing.T, result string) {
				// Should be a valid MD5 hash directory
				parts := strings.Split(result, string(os.PathSeparator))
				hashPart := parts[len(parts)-1]
				if len(hashPart) != 32 {
					t.Errorf("Expected 32-character MD5 hash, got %s", hashPart)
				}
				// Check it's valid hex
				if _, err := hex.DecodeString(hashPart); err != nil {
					t.Errorf("Invalid hex in hash: %s", hashPart)
				}
			},
		},
		{
			name:        "absolute path",
			projectPath: "/tmp/test-project",
			wantErr:     false,
			validate: func(t *testing.T, result string) {
				// Calculate expected hash
				canonicalPath := "/tmp/test-project"
				hash := md5.Sum([]byte(canonicalPath))
				expectedHash := hex.EncodeToString(hash[:])

				if !strings.HasSuffix(result, expectedHash) {
					t.Errorf("Expected hash %s, got %s", expectedHash, result)
				}
			},
		},
		{
			name:        "relative path converts to absolute",
			projectPath: "../",
			wantErr:     false,
			validate: func(t *testing.T, result string) {
				// Should still produce a valid hash
				parts := strings.Split(result, string(os.PathSeparator))
				hashPart := parts[len(parts)-1]
				if len(hashPart) != 32 {
					t.Errorf("Expected 32-character MD5 hash, got %s", hashPart)
				}
			},
		},
		{
			name:        "path with trailing slash",
			projectPath: "/tmp/test-project/",
			wantErr:     false,
			validate: func(t *testing.T, result string) {
				// Should normalize and produce same hash as without trailing slash
				canonicalPath := "/tmp/test-project"
				hash := md5.Sum([]byte(canonicalPath))
				expectedHash := hex.EncodeToString(hash[:])

				if !strings.HasSuffix(result, expectedHash) {
					t.Errorf("Expected normalized hash %s, got %s", expectedHash, result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetProjectHashDir(tt.projectPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetProjectHashDir() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.validate != nil {
				tt.validate(t, result)
			}
		})
	}
}

func TestGetCursorSessionDirs(t *testing.T) {
	tests := []struct {
		name          string
		setup         func() (string, func()) // Returns hashDir and cleanup function
		expectedDirs  []string
		expectedError bool
	}{
		{
			name: "valid hash directory",
			setup: func() (string, func()) {
				tempDir, err := os.MkdirTemp("", "cursor-sessions-test")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				// Create some session directories
				session1 := filepath.Join(tempDir, "session-uuid-1")
				session2 := filepath.Join(tempDir, "session-uuid-2")
				if err := os.Mkdir(session1, 0755); err != nil {
					t.Fatalf("Failed to create session1: %v", err)
				}
				if err := os.Mkdir(session2, 0755); err != nil {
					t.Fatalf("Failed to create session2: %v", err)
				}
				// Create a file (should be ignored)
				if err := os.WriteFile(filepath.Join(tempDir, "not-a-dir.txt"), []byte("test"), 0644); err != nil {
					t.Fatalf("Failed to create file: %v", err)
				}
				return tempDir, func() { _ = os.RemoveAll(tempDir) }
			},
			expectedDirs: []string{"session-uuid-1", "session-uuid-2"},
		},
		{
			name: "non-existent directory",
			setup: func() (string, func()) {
				return "/tmp/non-existent-dir-for-test", func() {}
			},
			expectedDirs:  nil,
			expectedError: true,
		},
		{
			name: "empty directory",
			setup: func() (string, func()) {
				emptyDir, err := os.MkdirTemp("", "empty-sessions-test")
				if err != nil {
					t.Fatalf("Failed to create temp dir: %v", err)
				}
				return emptyDir, func() { _ = os.RemoveAll(emptyDir) }
			},
			expectedDirs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hashDir, cleanup := tt.setup()
			defer cleanup()

			result, err := GetCursorSessionDirs(hashDir)

			if tt.expectedError {
				if err == nil {
					t.Errorf("GetCursorSessionDirs() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("GetCursorSessionDirs() unexpected error: %v", err)
				return
			}

			if len(result) != len(tt.expectedDirs) {
				t.Errorf("GetCursorSessionDirs() returned %d dirs, expected %d", len(result), len(tt.expectedDirs))
				t.Errorf("Got: %v", result)
				t.Errorf("Expected: %v", tt.expectedDirs)
				return
			}

			// Check that all expected directories are present
			for _, expected := range tt.expectedDirs {
				found := false
				for _, dir := range result {
					if dir == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected directory %s not found in result", expected)
				}
			}
		})
	}
}

func TestHasStoreDB(t *testing.T) {
	// Create a temporary directory structure
	tempDir, err := os.MkdirTemp("", "storedb-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	// Create session with store.db
	sessionWithDB := filepath.Join(tempDir, "session-with-db")
	if err := os.MkdirAll(sessionWithDB, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}
	storeDBPath := filepath.Join(sessionWithDB, "store.db")
	if err := os.WriteFile(storeDBPath, []byte("fake db"), 0644); err != nil {
		t.Fatalf("Failed to create store.db: %v", err)
	}

	// Create session without store.db
	sessionWithoutDB := filepath.Join(tempDir, "session-without-db")
	if err := os.MkdirAll(sessionWithoutDB, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	tests := []struct {
		name      string
		hashDir   string
		sessionID string
		expected  bool
	}{
		{
			name:      "session with store.db",
			hashDir:   tempDir,
			sessionID: "session-with-db",
			expected:  true,
		},
		{
			name:      "session without store.db",
			hashDir:   tempDir,
			sessionID: "session-without-db",
			expected:  false,
		},
		{
			name:      "non-existent session",
			hashDir:   tempDir,
			sessionID: "non-existent",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasStoreDB(tt.hashDir, tt.sessionID)
			if result != tt.expected {
				t.Errorf("HasStoreDB() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
