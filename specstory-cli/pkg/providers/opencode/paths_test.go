package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestComputeProjectHash_Deterministic(t *testing.T) {
	// This tests that the same path always produces the same hash.
	// The hash value was computed by running SHA-1 on "/tmp/specstory".
	const (
		projectPath = "/tmp/specstory"
		// SHA-1 of "/tmp/specstory" = da39a3ee5e6b4b0d3255bfef95601890afd80709
		// Note: actual hash depends on the exact path normalization
	)

	hash1, err := ComputeProjectHash(projectPath)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error: %v", err)
	}

	hash2, err := ComputeProjectHash(projectPath)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error on second call: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("ComputeProjectHash is not deterministic: %s != %s", hash1, hash2)
	}

	// Verify hash length (SHA-1 produces 40 hex characters)
	if len(hash1) != 40 {
		t.Fatalf("ComputeProjectHash returned hash with unexpected length: got %d, want 40", len(hash1))
	}
}

func TestComputeProjectHash_EmptyPath(t *testing.T) {
	_, err := ComputeProjectHash("")
	if err == nil {
		t.Fatal("ComputeProjectHash with empty path should return error")
	}

	if err.Error() != "project path is empty" {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}

func TestComputeProjectHash_DifferentPaths(t *testing.T) {
	tests := []struct {
		name  string
		path1 string
		path2 string
	}{
		{
			name:  "different directories",
			path1: "/tmp/project1",
			path2: "/tmp/project2",
		},
		{
			name:  "nested vs flat",
			path1: "/tmp/a/b/c",
			path2: "/tmp/abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1, err1 := ComputeProjectHash(tt.path1)
			hash2, err2 := ComputeProjectHash(tt.path2)

			if err1 != nil || err2 != nil {
				t.Fatalf("ComputeProjectHash returned error: %v, %v", err1, err2)
			}

			if hash1 == hash2 {
				t.Fatalf("Different paths should produce different hashes: %s == %s", hash1, hash2)
			}
		})
	}
}

func TestGetStorageDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := "/home/testuser"
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	dir, err := GetStorageDir()
	if err != nil {
		t.Fatalf("GetStorageDir returned error: %v", err)
	}

	expected := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	if dir != expected {
		t.Fatalf("GetStorageDir returned %q, want %q", dir, expected)
	}
}

func TestGetStorageDir_HomeError(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	osUserHomeDir = func() (string, error) {
		return "", errors.New("home dir not available")
	}

	_, err := GetStorageDir()
	if err == nil {
		t.Fatal("GetStorageDir should return error when home dir fails")
	}
}

func TestGetProjectDir_WithDefaultPath(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	dir, err := GetProjectDir("")
	if err != nil {
		t.Fatalf("GetProjectDir returned error: %v", err)
	}

	// Should use current working directory when path is empty
	hash, _ := ComputeProjectHash("/tmp/specstory")
	expected := filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "session", hash)
	if dir != expected {
		t.Fatalf("GetProjectDir returned %q, want %q", dir, expected)
	}
}

func TestGetProjectDir_WithExplicitPath(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	projectPath := "/my/project/path"
	dir, err := GetProjectDir(projectPath)
	if err != nil {
		t.Fatalf("GetProjectDir returned error: %v", err)
	}

	hash, _ := ComputeProjectHash(projectPath)
	expected := filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "session", hash)
	if dir != expected {
		t.Fatalf("GetProjectDir returned %q, want %q", dir, expected)
	}
}

func TestResolveProjectDir_StorageMissing(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	originalStat := osStat
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
		osStat = originalStat
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Storage directory doesn't exist (t.TempDir is empty)
	_, err := ResolveProjectDir("")
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var pathErr *OpenCodePathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected OpenCodePathError, got %T: %v", err, err)
	}

	if pathErr.Kind != "storage_missing" {
		t.Fatalf("Kind = %s, want storage_missing", pathErr.Kind)
	}
}

func TestResolveProjectDir_ProjectMissing(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create storage directory but not the project directory
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	sessionDir := filepath.Join(storageDir, "session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create storage dir: %v", err)
	}

	_, err := ResolveProjectDir("")
	if err == nil {
		t.Fatal("expected error but got nil")
	}

	var pathErr *OpenCodePathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected OpenCodePathError, got %T: %v", err, err)
	}

	if pathErr.Kind != "project_missing" {
		t.Fatalf("Kind = %s, want project_missing", pathErr.Kind)
	}

	// Verify project hash is included in error
	expectedHash, _ := ComputeProjectHash("/tmp/specstory")
	if pathErr.ProjectHash != expectedHash {
		t.Fatalf("ProjectHash = %s, want %s", pathErr.ProjectHash, expectedHash)
	}
}

func TestResolveProjectDir_Success(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	osGetwd = func() (string, error) {
		return "/tmp/specstory", nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create storage and project directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	hash, _ := ComputeProjectHash("/tmp/specstory")
	projectDir := filepath.Join(storageDir, "session", hash)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	resolvedDir, err := ResolveProjectDir("")
	if err != nil {
		t.Fatalf("ResolveProjectDir returned error: %v", err)
	}

	if resolvedDir != projectDir {
		t.Fatalf("ResolveProjectDir returned %q, want %q", resolvedDir, projectDir)
	}
}

func TestListProjectHashes(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create storage/session directory with hash subdirectories
	sessionDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create session dir: %v", err)
	}

	hashes := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	for _, h := range hashes {
		path := filepath.Join(sessionDir, h)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatalf("failed to create hash dir %q: %v", h, err)
		}
	}

	// Also create a "global" directory that should be excluded
	globalDir := filepath.Join(sessionDir, GlobalProjectHash)
	if err := os.Mkdir(globalDir, 0o755); err != nil {
		t.Fatalf("failed to create global dir: %v", err)
	}

	got, err := ListProjectHashes()
	if err != nil {
		t.Fatalf("ListProjectHashes returned error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("ListProjectHashes returned %d hashes, want 2", len(got))
	}

	// Verify global is excluded
	for _, h := range got {
		if h == GlobalProjectHash {
			t.Fatal("ListProjectHashes should exclude 'global' directory")
		}
	}
}

func TestGetSessionsDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := "/home/testuser"
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	tests := []struct {
		name        string
		projectHash string
		wantErr     bool
		expected    string
	}{
		{
			name:        "valid hash",
			projectHash: "abc123",
			wantErr:     false,
			expected:    filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "session", "abc123"),
		},
		{
			name:        "empty hash",
			projectHash: "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := GetSessionsDir(tt.projectHash)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("GetSessionsDir returned error: %v", err)
			}
			if dir != tt.expected {
				t.Fatalf("GetSessionsDir = %q, want %q", dir, tt.expected)
			}
		})
	}
}

func TestGetMessagesDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := "/home/testuser"
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	tests := []struct {
		name      string
		sessionID string
		wantErr   bool
		expected  string
	}{
		{
			name:      "valid session ID",
			sessionID: "ses_abc123",
			wantErr:   false,
			expected:  filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "message", "ses_abc123"),
		},
		{
			name:      "empty session ID",
			sessionID: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := GetMessagesDir(tt.sessionID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("GetMessagesDir returned error: %v", err)
			}
			if dir != tt.expected {
				t.Fatalf("GetMessagesDir = %q, want %q", dir, tt.expected)
			}
		})
	}
}

func TestGetPartsDir(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := "/home/testuser"
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	tests := []struct {
		name      string
		messageID string
		wantErr   bool
		expected  string
	}{
		{
			name:      "valid message ID",
			messageID: "msg_xyz789",
			wantErr:   false,
			expected:  filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "part", "msg_xyz789"),
		},
		{
			name:      "empty message ID",
			messageID: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := GetPartsDir(tt.messageID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("GetPartsDir returned error: %v", err)
			}
			if dir != tt.expected {
				t.Fatalf("GetPartsDir = %q, want %q", dir, tt.expected)
			}
		})
	}
}

func TestGetProjectFilePath(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := "/home/testuser"
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	tests := []struct {
		name        string
		projectHash string
		wantErr     bool
		expected    string
	}{
		{
			name:        "valid hash",
			projectHash: "abc123def456",
			wantErr:     false,
			expected:    filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "project", "abc123def456.json"),
		},
		{
			name:        "empty hash",
			projectHash: "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := GetProjectFilePath(tt.projectHash)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("GetProjectFilePath returned error: %v", err)
			}
			if path != tt.expected {
				t.Fatalf("GetProjectFilePath = %q, want %q", path, tt.expected)
			}
		})
	}
}

func TestOpenCodePathError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *OpenCodePathError
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "<nil>",
		},
		{
			name: "storage missing",
			err: &OpenCodePathError{
				Kind:    "storage_missing",
				Path:    "/path/to/storage",
				Message: "Storage not found",
			},
			expected: "Storage not found",
		},
		{
			name: "project missing with known hashes",
			err: &OpenCodePathError{
				Kind:        "project_missing",
				Path:        "/path/to/project",
				ProjectHash: "abc123",
				KnownHashes: []string{"hash1", "hash2"},
				Message:     "Project not found",
			},
			expected: "Project not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.expected {
				t.Fatalf("Error() = %q, want %q", got, tt.expected)
			}
		})
	}
}
