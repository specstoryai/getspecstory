package opencode

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// createTestGitRepo creates a temporary git repository with a known root commit hash.
// Returns the repo path and the expected hash.
func createTestGitRepo(t *testing.T) (string, string) {
	t.Helper()

	repoDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Configure git user for commit
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git email: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git name: %v", err)
	}

	// Create a file and commit
	testFile := filepath.Join(repoDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", "test.txt")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "initial commit")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	// Get the root commit hash
	cmd = exec.Command("git", "rev-list", "--max-parents=0", "--all")
	cmd.Dir = repoDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get root commit: %v", err)
	}

	hash := string(output)
	if len(hash) > 40 {
		hash = hash[:40]
	}

	return repoDir, hash
}

func TestComputeProjectHash_InGitRepo(t *testing.T) {
	repoDir, expectedHash := createTestGitRepo(t)

	hash, err := ComputeProjectHash(repoDir)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error: %v", err)
	}

	if hash != expectedHash {
		t.Fatalf("ComputeProjectHash returned %q, want %q", hash, expectedHash)
	}

	// Verify hash length (git commit hash is 40 hex characters)
	if len(hash) != 40 {
		t.Fatalf("ComputeProjectHash returned hash with unexpected length: got %d, want 40", len(hash))
	}
}

func TestComputeProjectHash_Deterministic(t *testing.T) {
	repoDir, _ := createTestGitRepo(t)

	hash1, err := ComputeProjectHash(repoDir)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error: %v", err)
	}

	hash2, err := ComputeProjectHash(repoDir)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error on second call: %v", err)
	}

	if hash1 != hash2 {
		t.Fatalf("ComputeProjectHash is not deterministic: %s != %s", hash1, hash2)
	}
}

func TestComputeProjectHash_NonGitDir(t *testing.T) {
	// Create a non-git directory
	nonGitDir := t.TempDir()

	hash, err := ComputeProjectHash(nonGitDir)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error: %v", err)
	}

	// Non-git directories should return "global"
	if hash != GlobalProjectHash {
		t.Fatalf("ComputeProjectHash for non-git dir returned %q, want %q", hash, GlobalProjectHash)
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

func TestComputeProjectHash_SubdirectoryOfGitRepo(t *testing.T) {
	repoDir, expectedHash := createTestGitRepo(t)

	// Create a subdirectory
	subDir := filepath.Join(repoDir, "subdir", "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}

	// ComputeProjectHash from subdirectory should return the same hash
	hash, err := ComputeProjectHash(subDir)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error: %v", err)
	}

	if hash != expectedHash {
		t.Fatalf("ComputeProjectHash from subdir returned %q, want %q", hash, expectedHash)
	}
}

func TestComputeProjectHash_CachedHash(t *testing.T) {
	repoDir, _ := createTestGitRepo(t)

	// Write a cached hash to .git/opencode
	cachedHash := "abcd1234567890abcd1234567890abcd12345678"
	cacheFile := filepath.Join(repoDir, ".git", "opencode")
	if err := os.WriteFile(cacheFile, []byte(cachedHash+"\n"), 0o644); err != nil {
		t.Fatalf("failed to write cache file: %v", err)
	}

	hash, err := ComputeProjectHash(repoDir)
	if err != nil {
		t.Fatalf("ComputeProjectHash returned error: %v", err)
	}

	// Should return the cached hash
	if hash != cachedHash {
		t.Fatalf("ComputeProjectHash returned %q, want cached hash %q", hash, cachedHash)
	}
}

func TestComputeProjectHash_MultipleCallsSameRepo(t *testing.T) {
	// This test verifies that multiple calls to ComputeProjectHash with different
	// repos return the correct hash for each repo (not a cached value).
	repoDir1, hash1 := createTestGitRepo(t)
	repoDir2, hash2 := createTestGitRepo(t)

	// Verify ComputeProjectHash returns the expected hashes for each repo
	computedHash1, err := ComputeProjectHash(repoDir1)
	if err != nil {
		t.Fatalf("ComputeProjectHash for repo1 returned error: %v", err)
	}
	if computedHash1 != hash1 {
		t.Fatalf("ComputeProjectHash for repo1 returned %q, want %q", computedHash1, hash1)
	}

	computedHash2, err := ComputeProjectHash(repoDir2)
	if err != nil {
		t.Fatalf("ComputeProjectHash for repo2 returned error: %v", err)
	}
	if computedHash2 != hash2 {
		t.Fatalf("ComputeProjectHash for repo2 returned %q, want %q", computedHash2, hash2)
	}

	// The hashes might be the same if created at the same timestamp with same content,
	// but we're testing that each call correctly computes the hash for its repo.
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

func TestGetProjectDir_WithGitRepo(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	repoDir, expectedHash := createTestGitRepo(t)

	dir, err := GetProjectDir(repoDir)
	if err != nil {
		t.Fatalf("GetProjectDir returned error: %v", err)
	}

	expected := filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "session", expectedHash)
	if dir != expected {
		t.Fatalf("GetProjectDir returned %q, want %q", dir, expected)
	}
}

func TestGetProjectDir_WithNonGitPath(t *testing.T) {
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osUserHomeDir = originalUserHome
	})

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	nonGitDir := t.TempDir()

	dir, err := GetProjectDir(nonGitDir)
	if err != nil {
		t.Fatalf("GetProjectDir returned error: %v", err)
	}

	// Non-git directories get "global" hash
	expected := filepath.Join(fakeHome, ".local", "share", "opencode", "storage", "session", GlobalProjectHash)
	if dir != expected {
		t.Fatalf("GetProjectDir returned %q, want %q", dir, expected)
	}
}

func TestResolveProjectDir_StorageMissing(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	repoDir, _ := createTestGitRepo(t)
	osGetwd = func() (string, error) {
		return repoDir, nil
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

	repoDir, expectedHash := createTestGitRepo(t)
	osGetwd = func() (string, error) {
		return repoDir, nil
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

	repoDir, expectedHash := createTestGitRepo(t)
	osGetwd = func() (string, error) {
		return repoDir, nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create storage and project directories
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	projectDir := filepath.Join(storageDir, "session", expectedHash)
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

func TestResolveProjectDir_GlobalRejected(t *testing.T) {
	originalGetwd := osGetwd
	originalUserHome := osUserHomeDir
	t.Cleanup(func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHome
	})

	// Use a non-git directory (which returns "global" hash)
	nonGitDir := t.TempDir()
	osGetwd = func() (string, error) {
		return nonGitDir, nil
	}

	fakeHome := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// Create storage directory
	storageDir := filepath.Join(fakeHome, ".local", "share", "opencode", "storage")
	sessionDir := filepath.Join(storageDir, "session")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("failed to create storage dir: %v", err)
	}

	_, err := ResolveProjectDir("")
	if err == nil {
		t.Fatal("expected error for global session, got nil")
	}

	var pathErr *OpenCodePathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected OpenCodePathError, got %T: %v", err, err)
	}

	if pathErr.Kind != "global_session" {
		t.Fatalf("Kind = %s, want global_session", pathErr.Kind)
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

func TestFindGitDir(t *testing.T) {
	repoDir, _ := createTestGitRepo(t)

	// Test from repo root
	gitDir, err := findGitDir(repoDir)
	if err != nil {
		t.Fatalf("findGitDir from root returned error: %v", err)
	}
	expected := filepath.Join(repoDir, ".git")
	if gitDir != expected {
		t.Fatalf("findGitDir returned %q, want %q", gitDir, expected)
	}

	// Test from subdirectory
	subDir := filepath.Join(repoDir, "sub", "dir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	gitDir, err = findGitDir(subDir)
	if err != nil {
		t.Fatalf("findGitDir from subdir returned error: %v", err)
	}
	if gitDir != expected {
		t.Fatalf("findGitDir from subdir returned %q, want %q", gitDir, expected)
	}

	// Test from non-git directory
	nonGitDir := t.TempDir()
	_, err = findGitDir(nonGitDir)
	if err == nil {
		t.Fatal("findGitDir should return error for non-git directory")
	}
}
