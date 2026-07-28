package utils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestProjectIdentityManager_EnsureProjectIdentity tests the project identity creation and update logic
func TestProjectIdentityManager_EnsureProjectIdentity(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "specstory-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create .specstory directory (simulating it was created by other code)
	specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
	if err := os.MkdirAll(specstoryDir, 0755); err != nil {
		t.Fatalf("Failed to create .specstory directory: %v", err)
	}

	// Test case 1: No .project.json yet
	t.Run("CreateNewProjectIdentity", func(t *testing.T) {
		manager := NewProjectIdentityManager(tempDir)

		modified, err := manager.EnsureProjectIdentity()
		if err != nil {
			t.Errorf("EnsureProjectIdentity failed: %v", err)
		}
		if !modified {
			t.Error("Expected identity to be modified when creating new")
		}

		// Verify the file was created
		identity, err := manager.ReadProjectIdentity()
		if err != nil {
			t.Errorf("ReadProjectIdentity failed: %v", err)
		}
		if identity == nil {
			t.Error("Expected identity to exist")
		} else {
			if identity.WorkspaceID == "" {
				t.Error("Expected workspace_id to be set")
			}
			if identity.WorkspaceIDAt == "" {
				t.Error("Expected workspace_id_at to be set")
			}
			// Verify the format matches expected pattern
			if !IsValidProjectID(identity.WorkspaceID) {
				t.Errorf("Invalid workspace_id format: %s", identity.WorkspaceID)
			}
			// Check project_name - should be the directory name
			expectedName := filepath.Base(tempDir)
			if identity.ProjectName != expectedName {
				t.Errorf("Expected project_name to be '%s', got '%s'", expectedName, identity.ProjectName)
			}
		}
	})

	// Test case 2: .project.json exists with workspace_id but no git_id
	t.Run("AddGitIDToExistingIdentity", func(t *testing.T) {
		// Create a test git repository
		gitDir := filepath.Join(tempDir, ".git")
		if err := os.MkdirAll(gitDir, 0755); err != nil {
			t.Fatalf("Failed to create .git directory: %v", err)
		}

		// Create a git config with origin
		gitConfig := `[core]
	repositoryformatversion = 0
[remote "origin"]
	url = https://github.com/specstoryai/test-repo.git
	fetch = +refs/heads/*:refs/remotes/origin/*`

		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
			t.Fatalf("Failed to write git config: %v", err)
		}

		manager := NewProjectIdentityManager(tempDir)

		// Run EnsureProjectIdentity again - should add git_id
		modified, err := manager.EnsureProjectIdentity()
		if err != nil {
			t.Errorf("EnsureProjectIdentity failed: %v", err)
		}
		if !modified {
			t.Error("Expected identity to be modified when adding git_id")
		}

		// Verify git_id was added
		identity, err := manager.ReadProjectIdentity()
		if err != nil {
			t.Errorf("ReadProjectIdentity failed: %v", err)
		}
		if identity.GitID == "" {
			t.Error("Expected git_id to be set")
		}
		if identity.GitIDAt == "" {
			t.Error("Expected git_id_at to be set")
		}
		// Verify the format matches expected pattern
		if !IsValidProjectID(identity.GitID) {
			t.Errorf("Invalid git_id format: %s", identity.GitID)
		}
	})

	// Test case 3: .project.json exists with both workspace_id and git_id
	t.Run("NoModificationWhenComplete", func(t *testing.T) {
		manager := NewProjectIdentityManager(tempDir)

		// Run EnsureProjectIdentity again - should not modify
		modified, err := manager.EnsureProjectIdentity()
		if err != nil {
			t.Errorf("EnsureProjectIdentity failed: %v", err)
		}
		if modified {
			t.Error("Expected identity to NOT be modified when already complete")
		}
	})
}

// TestGitIDFromURL verifies the git_id derivation that resolveIdentity uses —
// createHash(normalizeGitURL(url)) — is well-formed for assorted URL shapes, and that
// HTTPS and SSH clones of the same repo converge to one id (the point of normalizeGitURL).
func TestGitIDFromURL(t *testing.T) {
	manager := &ProjectIdentityManager{}
	gitID := func(url string) string { return manager.createHash(manager.normalizeGitURL(url)) }

	formats := []struct {
		name   string
		gitURL string
	}{
		{"GitHub HTTPS", "https://github.com/specstoryai/specstory-cli.git"},
		{"GitHub SSH", "git@github.com:specstoryai/specstory-cli.git"},
		{"GitLab HTTPS", "https://gitlab.com/patterns-ai-core/langchainrb.git"},
		{"GitLab SSH", "git@gitlab.com:patterns-ai-core/langchainrb.git"},
		{"Custom Git Server", "git@custom.example.com:myorg/myrepo.git"},
	}
	for _, tt := range formats {
		t.Run(tt.name, func(t *testing.T) {
			if id := gitID(tt.gitURL); !IsValidProjectID(id) {
				t.Errorf("invalid git_id format for %q: %s", tt.gitURL, id)
			}
		})
	}

	// HTTPS and SSH URLs for the same repo must hash to the same id: a clone over either
	// protocol resolves to one project.
	convergence := []struct {
		name       string
		https, ssh string
	}{
		{"GitHub", "https://github.com/specstoryai/specstory-cli.git", "git@github.com:specstoryai/specstory-cli.git"},
		{"GitLab", "https://gitlab.com/patterns-ai-core/langchainrb.git", "git@gitlab.com:patterns-ai-core/langchainrb.git"},
	}
	for _, c := range convergence {
		t.Run(c.name+" HTTPS==SSH", func(t *testing.T) {
			if gitID(c.https) != gitID(c.ssh) {
				t.Errorf("%s HTTPS and SSH should converge, got %s vs %s", c.name, gitID(c.https), gitID(c.ssh))
			}
		})
	}
}

// TestProjectIdentityManager_createHash tests the hash creation function
func TestProjectIdentityManager_createHash(t *testing.T) {
	manager := &ProjectIdentityManager{}

	tests := []struct {
		name     string
		input    string
		expected string // We'll validate format, not exact value
	}{
		{
			name:  "Simple string",
			input: "test",
		},
		{
			name:  "GitHub repo path",
			input: "specstoryai/specstory-cli.git",
		},
		{
			name:  "Full path",
			input: "/Users/test/projects/myproject",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := manager.createHash(tt.input)

			// Verify format: xxxx-xxxx-xxxx-xxxx
			if !IsValidProjectID(hash) {
				t.Errorf("Invalid hash format: %s", hash)
			}

			// Verify length (4 groups of 4 chars + 3 dashes = 19)
			if len(hash) != 19 {
				t.Errorf("Expected hash length 19, got %d", len(hash))
			}

			// Verify deterministic - same input should produce same output
			hash2 := manager.createHash(tt.input)
			if hash != hash2 {
				t.Errorf("Hash not deterministic: %s != %s", hash, hash2)
			}
		})
	}
}

// TestProjectIdentityManager_ReadProjectIdentity tests reading project identity from file
func TestProjectIdentityManager_ReadProjectIdentity(t *testing.T) {
	tests := []struct {
		name         string
		fileContent  string
		expectError  bool
		validateFunc func(*testing.T, *ProjectIdentity)
	}{
		{
			name: "Valid full identity",
			fileContent: `{
  "workspace_id": "1234-5678-9abc-def0",
  "workspace_id_at": "2024-01-01T00:00:00Z",
  "git_id": "abcd-efgh-ijkl-mnop",
  "git_id_at": "2024-01-01T00:00:00Z"
}`,
			expectError: false,
			validateFunc: func(t *testing.T, identity *ProjectIdentity) {
				if identity.WorkspaceID != "1234-5678-9abc-def0" {
					t.Errorf("Expected workspace_id '1234-5678-9abc-def0', got '%s'", identity.WorkspaceID)
				}
				if identity.GitID != "abcd-efgh-ijkl-mnop" {
					t.Errorf("Expected git_id 'abcd-efgh-ijkl-mnop', got '%s'", identity.GitID)
				}
			},
		},
		{
			name: "Valid workspace only",
			fileContent: `{
  "workspace_id": "1234-5678-9abc-def0",
  "workspace_id_at": "2024-01-01T00:00:00Z"
}`,
			expectError: false,
			validateFunc: func(t *testing.T, identity *ProjectIdentity) {
				if identity.WorkspaceID != "1234-5678-9abc-def0" {
					t.Errorf("Expected workspace_id '1234-5678-9abc-def0', got '%s'", identity.WorkspaceID)
				}
				if identity.GitID != "" {
					t.Errorf("Expected empty git_id, got '%s'", identity.GitID)
				}
			},
		},
		{
			name:        "Invalid JSON",
			fileContent: `{invalid json}`,
			expectError: true,
		},
		{
			name:        "Empty file",
			fileContent: ``,
			expectError: true,
		},
		{
			name: "Valid identity with trailing comma",
			fileContent: `{
  "workspace_id": "3a48-60f5-e185-d674",
  "workspace_id_at": "2025-07-29T22:01:08Z",
  "git_id": "328f-1980-81e0-0afd",
  "git_id_at": "2025-07-29T22:01:08Z",
}`,
			expectError: false,
			validateFunc: func(t *testing.T, identity *ProjectIdentity) {
				if identity.WorkspaceID != "3a48-60f5-e185-d674" {
					t.Errorf("Expected workspace_id '3a48-60f5-e185-d674', got '%s'", identity.WorkspaceID)
				}
				if identity.GitID != "328f-1980-81e0-0afd" {
					t.Errorf("Expected git_id '328f-1980-81e0-0afd', got '%s'", identity.GitID)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "specstory-read-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tempDir) }()

			// Create .specstory directory and .project.json file
			specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
			if err := os.MkdirAll(specstoryDir, 0755); err != nil {
				t.Fatalf("Failed to create .specstory directory: %v", err)
			}

			if tt.fileContent != "" {
				projectFile := filepath.Join(specstoryDir, PROJECT_JSON_FILE)
				if err := os.WriteFile(projectFile, []byte(tt.fileContent), 0644); err != nil {
					t.Fatalf("Failed to write project file: %v", err)
				}
			}

			manager := NewProjectIdentityManager(tempDir)
			identity, err := manager.ReadProjectIdentity()

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if identity == nil {
					t.Error("Expected identity but got nil")
				} else if tt.validateFunc != nil {
					tt.validateFunc(t, identity)
				}
			}
		})
	}
}

// TestIsValidProjectID tests the project ID validation function
func TestIsValidProjectID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Valid project ID",
			input:    "1234-5678-9abc-def0",
			expected: true,
		},
		{
			name:     "Valid project ID all numbers",
			input:    "0000-0000-0000-0000",
			expected: true,
		},
		{
			name:     "Valid project ID all letters",
			input:    "abcd-efab-cdef-abcd",
			expected: true,
		},
		{
			name:     "Invalid - uppercase letters",
			input:    "ABCD-EFAB-CDEF-ABCD",
			expected: false,
		},
		{
			name:     "Invalid - too short",
			input:    "1234-5678-9abc",
			expected: false,
		},
		{
			name:     "Invalid - too long",
			input:    "1234-5678-9abc-def0-1234",
			expected: false,
		},
		{
			name:     "Invalid - wrong separator",
			input:    "1234_5678_9abc_def0",
			expected: false,
		},
		{
			name:     "Invalid - no separators",
			input:    "123456789abcdef0",
			expected: false,
		},
		{
			name:     "Invalid - UUID format",
			input:    "550e8400-e29b-41d4-a716-446655440000",
			expected: false,
		},
		{
			name:     "Invalid - empty",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidProjectID(tt.input)
			if result != tt.expected {
				t.Errorf("IsValidProjectID(%s) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestGetProjectID tests the logic for returning git_id or workspace_id
func TestGetProjectID(t *testing.T) {
	tests := []struct {
		name        string
		identity    *ProjectIdentity
		expectedID  string
		expectError bool
	}{
		{
			name: "Both IDs present - should return git_id",
			identity: &ProjectIdentity{
				WorkspaceID: "1111-2222-3333-4444",
				GitID:       "aaaa-bbbb-cccc-dddd",
			},
			expectedID:  "aaaa-bbbb-cccc-dddd",
			expectError: false,
		},
		{
			name: "Only workspace_id present",
			identity: &ProjectIdentity{
				WorkspaceID: "1111-2222-3333-4444",
			},
			expectedID:  "1111-2222-3333-4444",
			expectError: false,
		},
		{
			name: "Only git_id present",
			identity: &ProjectIdentity{
				GitID: "aaaa-bbbb-cccc-dddd",
			},
			expectedID:  "aaaa-bbbb-cccc-dddd",
			expectError: false,
		},
		{
			name:        "No IDs present",
			identity:    &ProjectIdentity{},
			expectedID:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "specstory-getid-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tempDir) }()

			// Create .specstory directory
			specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
			if err := os.MkdirAll(specstoryDir, 0755); err != nil {
				t.Fatalf("Failed to create .specstory directory: %v", err)
			}

			// Write identity file if provided
			if tt.identity != nil {
				projectFile := filepath.Join(specstoryDir, PROJECT_JSON_FILE)
				data, _ := json.MarshalIndent(tt.identity, "", "  ")
				if err := os.WriteFile(projectFile, data, 0644); err != nil {
					t.Fatalf("Failed to write project file: %v", err)
				}
			}

			manager := NewProjectIdentityManager(tempDir)
			projectID, err := manager.GetProjectID()

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if projectID != tt.expectedID {
					t.Errorf("Expected project ID '%s', got '%s'", tt.expectedID, projectID)
				}
			}
		})
	}
}

// TestNormalizeGitURL tests the git URL normalization function
func TestNormalizeGitURL(t *testing.T) {
	manager := &ProjectIdentityManager{}

	tests := []struct {
		name     string
		gitURL   string
		expected string
	}{
		// GitHub tests
		{
			name:     "GitHub HTTPS with .git",
			gitURL:   "https://github.com/specstoryai/specstory-cli.git",
			expected: "github.com/specstoryai/specstory-cli",
		},
		{
			name:     "GitHub SSH with .git",
			gitURL:   "git@github.com:specstoryai/specstory-cli.git",
			expected: "github.com/specstoryai/specstory-cli",
		},
		// GitLab tests
		{
			name:     "GitLab HTTPS",
			gitURL:   "https://gitlab.com/patterns-ai-core/langchainrb.git",
			expected: "gitlab.com/patterns-ai-core/langchainrb",
		},
		{
			name:     "GitLab SSH",
			gitURL:   "git@gitlab.com:patterns-ai-core/langchainrb.git",
			expected: "gitlab.com/patterns-ai-core/langchainrb",
		},
		// Bitbucket tests
		{
			name:     "Bitbucket HTTPS",
			gitURL:   "https://bitbucket.org/myteam/myrepo.git",
			expected: "bitbucket.org/myteam/myrepo",
		},
		{
			name:     "Bitbucket SSH",
			gitURL:   "git@bitbucket.org:myteam/myrepo.git",
			expected: "bitbucket.org/myteam/myrepo",
		},
		// Custom git server
		{
			name:     "Custom server HTTPS",
			gitURL:   "https://git.company.com/project/repo.git",
			expected: "git.company.com/project/repo",
		},
		{
			name:     "Custom server SSH",
			gitURL:   "git@git.company.com:project/repo.git",
			expected: "git.company.com/project/repo",
		},
		// Other protocols
		{
			name:     "Git protocol",
			gitURL:   "git://github.com/user/repo.git",
			expected: "github.com/user/repo",
		},
		{
			name:     "Git+SSH protocol",
			gitURL:   "git+ssh://github.com/user/repo.git",
			expected: "github.com/user/repo",
		},
		// Without .git suffix
		{
			name:     "HTTPS without .git",
			gitURL:   "https://github.com/user/repo",
			expected: "github.com/user/repo",
		},
		{
			name:     "SSH without .git",
			gitURL:   "git@github.com:user/repo",
			expected: "github.com/user/repo",
		},
		// Already normalized
		{
			name:     "Already normalized",
			gitURL:   "github.com/user/repo",
			expected: "github.com/user/repo",
		},
		// SSH with different user
		{
			name:     "SSH with custom user",
			gitURL:   "customuser@github.com:owner/repo.git",
			expected: "github.com/owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.normalizeGitURL(tt.gitURL)
			if result != tt.expected {
				t.Errorf("normalizeGitURL(%s) = %s, expected %s", tt.gitURL, result, tt.expected)
			}
		})
	}
}

// TestNormalizedGitURLConsistency tests that HTTPS and SSH URLs for the same repo generate the same git ID
func TestNormalizedGitURLConsistency(t *testing.T) {
	manager := &ProjectIdentityManager{}

	// Test pairs of URLs that should generate the same normalized form
	urlPairs := []struct {
		name  string
		https string
		ssh   string
	}{
		{
			name:  "GitHub",
			https: "https://github.com/specstoryai/specstory-cli.git",
			ssh:   "git@github.com:specstoryai/specstory-cli.git",
		},
		{
			name:  "GitLab",
			https: "https://gitlab.com/patterns-ai-core/langchainrb.git",
			ssh:   "git@gitlab.com:patterns-ai-core/langchainrb.git",
		},
		{
			name:  "Bitbucket",
			https: "https://bitbucket.org/myteam/myrepo.git",
			ssh:   "git@bitbucket.org:myteam/myrepo.git",
		},
		{
			name:  "Custom server",
			https: "https://git.company.com/project/repo.git",
			ssh:   "git@git.company.com:project/repo.git",
		},
	}

	for _, pair := range urlPairs {
		t.Run(pair.name, func(t *testing.T) {
			httpsNormalized := manager.normalizeGitURL(pair.https)
			sshNormalized := manager.normalizeGitURL(pair.ssh)

			if httpsNormalized != sshNormalized {
				t.Errorf("HTTPS and SSH URLs should normalize to the same value:\nHTTPS: %s -> %s\nSSH: %s -> %s",
					pair.https, httpsNormalized, pair.ssh, sshNormalized)
			}

			// Also verify that the hashes would be the same
			httpsHash := manager.createHash(httpsNormalized)
			sshHash := manager.createHash(sshNormalized)

			if httpsHash != sshHash {
				t.Errorf("HTTPS and SSH URLs should generate the same hash:\nHTTPS hash: %s\nSSH hash: %s",
					httpsHash, sshHash)
			}
		})
	}
}

// TestWorkspaceIDConsistency tests that workspace ID is consistent for the same path
func TestWorkspaceIDConsistency(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specstory-consistency-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create multiple managers for the same directory
	manager1 := NewProjectIdentityManager(tempDir)
	manager2 := NewProjectIdentityManager(tempDir)

	// Generate workspace IDs
	id1 := manager1.generateWorkspaceID()
	id2 := manager2.generateWorkspaceID()

	if id1 != id2 {
		t.Errorf("Workspace IDs should be consistent for same path, got %s and %s", id1, id2)
	}
}

// TestTimestampFormat tests that timestamps are in correct ISO 8601 format
func TestTimestampFormat(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specstory-timestamp-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create .specstory directory
	specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
	if err := os.MkdirAll(specstoryDir, 0755); err != nil {
		t.Fatalf("Failed to create .specstory directory: %v", err)
	}

	manager := NewProjectIdentityManager(tempDir)

	// Create project identity
	if _, err := manager.EnsureProjectIdentity(); err != nil {
		t.Fatalf("Failed to ensure project identity: %v", err)
	}

	identity, err := manager.ReadProjectIdentity()
	if err != nil {
		t.Fatalf("Failed to read project identity: %v", err)
	}

	// Verify timestamp format
	_, err = time.Parse(time.RFC3339, identity.WorkspaceIDAt)
	if err != nil {
		t.Errorf("workspace_id_at is not valid RFC3339 format: %s", identity.WorkspaceIDAt)
	}

	// If git_id_at exists, verify its format too
	if identity.GitIDAt != "" {
		_, err = time.Parse(time.RFC3339, identity.GitIDAt)
		if err != nil {
			t.Errorf("git_id_at is not valid RFC3339 format: %s", identity.GitIDAt)
		}
	}
}

// TestParseGitRemoteURL tests parsing repository names from various git URL formats
func TestParseGitRemoteURL(t *testing.T) {
	manager := &ProjectIdentityManager{}

	tests := []struct {
		name         string
		url          string
		expectedName string
	}{
		// SSH format
		{
			name:         "SSH with .git",
			url:          "git@github.com:specstoryai/specstory-monorepo.git",
			expectedName: "specstory-monorepo",
		},
		{
			name:         "SSH without .git",
			url:          "git@github.com:specstoryai/specstory-monorepo",
			expectedName: "specstory-monorepo",
		},
		{
			name:         "SSH GitLab",
			url:          "git@gitlab.com:patterns-ai-core/langchainrb.git",
			expectedName: "langchainrb",
		},
		// HTTPS format
		{
			name:         "HTTPS with .git",
			url:          "https://github.com/specstoryai/specstory-monorepo.git",
			expectedName: "specstory-monorepo",
		},
		{
			name:         "HTTPS without .git",
			url:          "https://github.com/specstoryai/specstory-monorepo",
			expectedName: "specstory-monorepo",
		},
		// HTTP format
		{
			name:         "HTTP with .git",
			url:          "http://github.com/specstoryai/specstory-monorepo.git",
			expectedName: "specstory-monorepo",
		},
		// GIT protocol
		{
			name:         "GIT protocol",
			url:          "git://github.com/specstoryai/specstory-monorepo.git",
			expectedName: "specstory-monorepo",
		},
		// GIT+SSH protocol
		{
			name:         "GIT+SSH protocol",
			url:          "git+ssh://github.com/specstoryai/specstory-monorepo.git",
			expectedName: "specstory-monorepo",
		},
		// Implicit HTTPS
		{
			name:         "Implicit HTTPS",
			url:          "github.com/specstoryai/specstory-monorepo.git",
			expectedName: "specstory-monorepo",
		},
		{
			name:         "Implicit HTTPS nested path",
			url:          "github.com/org/team/project/repo.git",
			expectedName: "repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.parseGitRemoteURL(tt.url)
			if result != tt.expectedName {
				t.Errorf("parseGitRemoteURL(%s) = %s, expected %s", tt.url, result, tt.expectedName)
			}
		})
	}
}

// TestProjectNameFromRemote tests repo-name extraction from a git remote URL
// (parseGitRemoteURL, which resolveIdentity uses to name a project from its origin).
func TestProjectNameFromRemote(t *testing.T) {
	manager := &ProjectIdentityManager{}
	tests := []struct {
		name, url, want string
	}{
		{"GitHub HTTPS", "https://github.com/specstoryai/my-awesome-project.git", "my-awesome-project"},
		{"GitHub SSH", "git@github.com:specstoryai/my-awesome-project.git", "my-awesome-project"},
		{"Implicit host/path", "github.com/specstoryai/my-awesome-project", "my-awesome-project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := manager.parseGitRemoteURL(tt.url); got != tt.want {
				t.Errorf("parseGitRemoteURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// TestProjectNameFallsBackToDirName verifies a project with no resolvable git remote is
// named by its directory basename — the fallback branch in resolveIdentity.
func TestProjectNameFallsBackToDirName(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-project-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	_, _, name, _ := resolveIdentity(tempDir)
	if want := filepath.Base(tempDir); name != want {
		t.Errorf("expected project name %q, got %q", want, name)
	}
}

// TestProjectNameInFullFlow tests project_name in the complete identity flow
func TestProjectNameInFullFlow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specstory-fullflow-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create .specstory directory
	specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
	if err := os.MkdirAll(specstoryDir, 0755); err != nil {
		t.Fatalf("Failed to create .specstory directory: %v", err)
	}

	// Create a git config
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("Failed to create .git directory: %v", err)
	}

	gitConfig := `[remote "origin"]
	url = git@github.com:myorg/test-repo.git`

	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
		t.Fatalf("Failed to write git config: %v", err)
	}

	// First run - should create everything including project_name
	manager := NewProjectIdentityManager(tempDir)
	modified, err := manager.EnsureProjectIdentity()
	if err != nil {
		t.Fatalf("First EnsureProjectIdentity failed: %v", err)
	}
	if !modified {
		t.Error("Expected first run to modify identity")
	}

	// Read and verify
	identity, err := manager.ReadProjectIdentity()
	if err != nil {
		t.Fatalf("ReadProjectIdentity failed: %v", err)
	}

	if identity.ProjectName != "test-repo" {
		t.Errorf("Expected project_name 'test-repo', got '%s'", identity.ProjectName)
	}

	// Second run - should not modify
	modified, err = manager.EnsureProjectIdentity()
	if err != nil {
		t.Fatalf("Second EnsureProjectIdentity failed: %v", err)
	}
	if modified {
		t.Error("Expected second run to NOT modify identity")
	}
}

// TestProjectNameMigration tests adding project_name to existing identity
func TestProjectNameMigration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specstory-migration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create .specstory directory
	specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
	if err := os.MkdirAll(specstoryDir, 0755); err != nil {
		t.Fatalf("Failed to create .specstory directory: %v", err)
	}

	// Create an existing .project.json without project_name
	existingIdentity := `{
  "workspace_id": "1234-5678-9abc-def0",
  "workspace_id_at": "2024-01-01T00:00:00Z",
  "git_id": "abcd-efgh-ijkl-mnop",
  "git_id_at": "2024-01-01T00:00:00Z"
}`

	projectFile := filepath.Join(specstoryDir, ".project.json")
	if err := os.WriteFile(projectFile, []byte(existingIdentity), 0644); err != nil {
		t.Fatalf("Failed to write existing project file: %v", err)
	}

	// Add git config
	gitDir := filepath.Join(tempDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatalf("Failed to create .git directory: %v", err)
	}

	gitConfig := `[remote "origin"]
	url = https://github.com/user/migrated-repo.git`

	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(gitConfig), 0644); err != nil {
		t.Fatalf("Failed to write git config: %v", err)
	}

	// Run EnsureProjectIdentity - should add project_name
	manager := NewProjectIdentityManager(tempDir)
	modified, err := manager.EnsureProjectIdentity()
	if err != nil {
		t.Fatalf("EnsureProjectIdentity failed: %v", err)
	}
	if !modified {
		t.Error("Expected identity to be modified when adding project_name")
	}

	// Verify project_name was added
	identity, err := manager.ReadProjectIdentity()
	if err != nil {
		t.Fatalf("ReadProjectIdentity failed: %v", err)
	}

	if identity.ProjectName != "migrated-repo" {
		t.Errorf("Expected project_name 'migrated-repo', got '%s'", identity.ProjectName)
	}

	// Verify other fields were preserved
	if identity.WorkspaceID != "1234-5678-9abc-def0" {
		t.Error("workspace_id was not preserved")
	}
	if identity.GitID != "abcd-efgh-ijkl-mnop" {
		t.Error("git_id was not preserved")
	}
}

// TestProjectIdentityManager_EnsureProjectIdentity_NoSpecstoryDir tests behavior when .specstory doesn't exist
func TestProjectIdentityManager_EnsureProjectIdentity_NoSpecstoryDir(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "specstory-nodir-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// DO NOT create .specstory directory - it should be created automatically
	manager := NewProjectIdentityManager(tempDir)

	modified, err := manager.EnsureProjectIdentity()
	if err != nil {
		t.Errorf("Expected no error when .specstory directory doesn't exist (should create it), but got: %v", err)
	}
	if !modified {
		t.Error("Expected modification when creating new project identity")
	}

	// Verify .specstory directory was created
	specstoryDir := filepath.Join(tempDir, SPECSTORY_DIR)
	if _, err := os.Stat(specstoryDir); os.IsNotExist(err) {
		t.Error("Expected .specstory directory to be created")
	}

	// Verify project.json was created
	projectJSONPath := filepath.Join(specstoryDir, PROJECT_JSON_FILE)
	if _, err := os.Stat(projectJSONPath); os.IsNotExist(err) {
		t.Error("Expected .project.json file to be created")
	}
}

// TestComputeProjectID is the executable spec for walk-up identity resolution:
// the monorepo-subdir + two-clone scenario, plus worktree and remote-less cases.
func TestComputeProjectID(t *testing.T) {
	// newRepo creates a temp dir with a .git/config carrying the given origin url
	// (empty url = a git repo with no remote).
	newRepo := func(t *testing.T, url string) string {
		t.Helper()
		root := t.TempDir()
		gitDir := filepath.Join(root, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := "[core]\n\tbare = false\n"
		if url != "" {
			cfg += "[remote \"origin\"]\n\turl = " + url + "\n"
		}
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		return root
	}
	mkSub := func(t *testing.T, root, rel string) string {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	compute := func(t *testing.T, dir string) string {
		t.Helper()
		id, _, err := ComputeProjectID(dir)
		if err != nil {
			t.Fatalf("ComputeProjectID(%q): %v", dir, err)
		}
		return id
	}

	t.Run("monorepo subdirs collapse to one git_id", func(t *testing.T) {
		root := newRepo(t, "git@github.com:acme/widgets.git")
		a := mkSub(t, root, "a")
		b := mkSub(t, root, "pkg/b")

		rootID := compute(t, root)
		_, rootName, _ := ComputeProjectID(root)
		if aID := compute(t, a); aID != rootID {
			t.Errorf("subdir a did not collapse: root=%s a=%s", rootID, aID)
		}
		if bID := compute(t, b); bID != rootID {
			t.Errorf("nested subdir b did not collapse: root=%s b=%s", rootID, bID)
		}
		if rootName != "widgets" {
			t.Errorf("project name = %q, want widgets", rootName)
		}
	})

	t.Run("two clones share git_id across different paths and url forms", func(t *testing.T) {
		ssh := newRepo(t, "git@github.com:acme/widgets.git")
		https := newRepo(t, "https://github.com/acme/widgets.git")
		if compute(t, ssh) != compute(t, https) {
			t.Errorf("clones diverged: ssh=%s https=%s", compute(t, ssh), compute(t, https))
		}
	})

	t.Run("worktree (.git file) resolves to the main repo git_id", func(t *testing.T) {
		main := newRepo(t, "git@github.com:acme/widgets.git")
		wtGitDir := filepath.Join(main, ".git", "worktrees", "wt")
		if err := os.MkdirAll(wtGitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// commondir points back to the shared .git (relative to the private gitdir).
		if err := os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wt := t.TempDir()
		if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+wtGitDir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if compute(t, wt) != compute(t, main) {
			t.Errorf("worktree did not resolve to main repo: main=%s wt=%s", compute(t, main), compute(t, wt))
		}
	})

	t.Run("remote-less repo: subdirs collapse, distinct repos differ", func(t *testing.T) {
		root := newRepo(t, "")
		a := mkSub(t, root, "a")
		if compute(t, a) != compute(t, root) {
			t.Errorf("remote-less subdir did not collapse: root=%s a=%s", compute(t, root), compute(t, a))
		}
		if compute(t, newRepo(t, "")) == compute(t, root) {
			t.Error("distinct remote-less repos collided")
		}
	})

	t.Run("no-git dir yields a stable, non-empty id", func(t *testing.T) {
		d := t.TempDir()
		id := compute(t, d)
		if id == "" {
			t.Error("empty id for no-git dir")
		}
		if id2 := compute(t, d); id != id2 {
			t.Errorf("unstable id: %s vs %s", id, id2)
		}
	})

	t.Run("persisted workspace_id is preferred over the computed hash", func(t *testing.T) {
		// A no-git dir falls back to the path-based workspace_id; an id already persisted in
		// .project.json must win so reindex/resume/cloud all agree on one value even when the
		// file predates path canonicalization.
		d := t.TempDir()
		specDir := filepath.Join(d, SPECSTORY_DIR)
		if err := os.MkdirAll(specDir, 0o755); err != nil {
			t.Fatal(err)
		}
		const sentinel = "dead-beef-cafe-0001"
		body := `{"workspace_id":"` + sentinel + `","workspace_id_at":"2020-01-01T00:00:00Z"}`
		if err := os.WriteFile(filepath.Join(specDir, PROJECT_JSON_FILE), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if id := compute(t, d); id != sentinel {
			t.Errorf("expected persisted workspace_id %q, got %q", sentinel, id)
		}
	})

	t.Run("git_id wins over a persisted workspace_id", func(t *testing.T) {
		root := newRepo(t, "git@github.com:acme/widgets.git")
		specDir := filepath.Join(root, SPECSTORY_DIR)
		if err := os.MkdirAll(specDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(specDir, PROJECT_JSON_FILE),
			[]byte(`{"workspace_id":"should-not-be-used"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if id := compute(t, root); id == "should-not-be-used" {
			t.Error("git_id should win over a persisted workspace_id")
		}
	})

	t.Run("empty cwd errors", func(t *testing.T) {
		if _, _, err := ComputeProjectID(""); err == nil {
			t.Error("expected error for empty cwd")
		}
	})
}

// TestCanonicalizeWorkspacePath verifies the case-fold rule: paths are lowercased on
// case-insensitive filesystems (macOS/Windows) and left byte-exact otherwise.
func TestCanonicalizeWorkspacePath(t *testing.T) {
	orig := caseInsensitiveFilesystem
	defer func() { caseInsensitiveFilesystem = orig }()

	const mixed = "/Users/Sean/Source/Proj"

	caseInsensitiveFilesystem = true
	if got := canonicalizeWorkspacePath(mixed); got != "/users/sean/source/proj" {
		t.Errorf("case-insensitive: got %q, want lowercased", got)
	}

	caseInsensitiveFilesystem = false
	if got := canonicalizeWorkspacePath(mixed); got != mixed {
		t.Errorf("case-sensitive: got %q, want byte-exact %q", got, mixed)
	}
}

// TestWorkspaceIDCaseFolding is the regression test for the reported bug: on a
// case-insensitive filesystem, the same physical directory named with different casing
// (e.g. os.Getwd echoing a lowercase $PWD vs a recorded capital-cased cwd) must hash to
// one workspace_id. On a case-sensitive filesystem the two are genuinely distinct.
func TestWorkspaceIDCaseFolding(t *testing.T) {
	orig := caseInsensitiveFilesystem
	defer func() { caseInsensitiveFilesystem = orig }()

	upper := &ProjectIdentityManager{projectRoot: "/Users/Sean/Source/Proj"}
	lower := &ProjectIdentityManager{projectRoot: "/users/sean/source/proj"}

	caseInsensitiveFilesystem = true
	if upper.generateWorkspaceID() != lower.generateWorkspaceID() {
		t.Error("case-insensitive FS: differently-cased paths produced different workspace ids")
	}

	caseInsensitiveFilesystem = false
	if upper.generateWorkspaceID() == lower.generateWorkspaceID() {
		t.Error("case-sensitive FS: differently-cased paths should yield distinct workspace ids")
	}
}
