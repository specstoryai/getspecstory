package utils

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/specstoryai/SpecStoryCLI/pkg/analytics"
)

const PROJECT_JSON_FILE = ".project.json"

// ProjectIdentity represents the project identity stored in .specstory/.project.json
type ProjectIdentity struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceIDAt string `json:"workspace_id_at"`
	GitID         string `json:"git_id,omitempty"`
	GitIDAt       string `json:"git_id_at,omitempty"`
	ProjectName   string `json:"project_name,omitempty"`
}

// ProjectIdentityManager handles project identity operations
type ProjectIdentityManager struct {
	projectRoot string
}

// NewProjectIdentityManager creates a new project identity manager
func NewProjectIdentityManager(projectRoot string) *ProjectIdentityManager {
	return &ProjectIdentityManager{
		projectRoot: projectRoot,
	}
}

// getProjectJSONPath returns the path to .specstory/.project.json
func (m *ProjectIdentityManager) getProjectJSONPath() string {
	return filepath.Join(m.projectRoot, SPECSTORY_DIR, PROJECT_JSON_FILE)
}

// EnsureProjectIdentity initializes or updates the project identity
// Returns true if the identity was created or modified
func (m *ProjectIdentityManager) EnsureProjectIdentity() (bool, error) {
	slog.Debug("Ensuring project identity", "projectRoot", m.projectRoot)

	// Ensure .specstory directory exists (create if needed)
	specstoryDir := filepath.Join(m.projectRoot, SPECSTORY_DIR)
	if err := os.MkdirAll(specstoryDir, 0755); err != nil {
		return false, fmt.Errorf("failed to create .specstory directory: %w", err)
	}

	// Read existing project identity if it exists
	projectJSONPath := m.getProjectJSONPath()
	existingIdentity, err := m.ReadProjectIdentity()
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to read existing project identity: %w", err)
	}

	// Determine what needs to be done
	var identity ProjectIdentity
	isModified := false

	isNewProject := false
	if existingIdentity == nil {
		// Case 1: No .project.json yet
		slog.Debug("No existing project identity found, creating new identity")
		identity = ProjectIdentity{
			WorkspaceID:   m.generateWorkspaceID(),
			WorkspaceIDAt: time.Now().UTC().Format(time.RFC3339),
		}
		isModified = true
		isNewProject = true
	} else {
		// Case 2 or 3: .project.json exists
		identity = *existingIdentity

		// Check if workspace_id is missing (shouldn't happen, but be defensive)
		if identity.WorkspaceID == "" {
			slog.Warn("Project identity file exists but has no workspace_id")
			identity.WorkspaceID = m.generateWorkspaceID()
			identity.WorkspaceIDAt = time.Now().UTC().Format(time.RFC3339)
			isModified = true
		}
	}

	// Check if we need to add git_id
	if identity.GitID == "" {
		gitID, err := m.generateGitID()
		if err == nil && gitID != "" {
			identity.GitID = gitID
			identity.GitIDAt = time.Now().UTC().Format(time.RFC3339)
			isModified = true
			slog.Debug("Added git_id to project identity", "git_id", gitID)
		} else if err != nil {
			slog.Debug("Could not generate git_id", "error", err)
		}
	}

	// Check if we need to add project_name
	if identity.ProjectName == "" {
		projectName := m.generateProjectName()
		if projectName != "" {
			identity.ProjectName = projectName
			isModified = true
			slog.Debug("Added project_name to project identity", "project_name", projectName)
		} else {
			slog.Warn("Could not generate project_name, using workspace_id instead")
			identity.ProjectName = identity.WorkspaceID
		}
	}

	// Write the project identity file if modified
	if isModified {
		jsonData, err := json.MarshalIndent(identity, "", "  ")
		if err != nil {
			return false, fmt.Errorf("failed to marshal project identity: %w", err)
		}

		if err := os.WriteFile(projectJSONPath, jsonData, 0644); err != nil {
			return false, fmt.Errorf("failed to write project identity: %w", err)
		}

		slog.Info("Project identity saved", "path", projectJSONPath, "identity", identity)

		// Track analytics for new project creation
		if isNewProject {
			properties := analytics.Properties{
				"has_git_id":       identity.GitID != "",
				"has_workspace_id": identity.WorkspaceID != "",
				"project_name":     identity.ProjectName,
			}

			// Determine the ID type being used
			if identity.GitID != "" {
				properties["id_type"] = "git"
			} else {
				properties["id_type"] = "workspace"
			}

			analytics.TrackEvent(analytics.EventProjectIdentityCreated, properties)
		}

		return true, nil
	}

	slog.Debug("Project identity is up-to-date, no changes written")
	return false, nil
}

// ReadProjectIdentity reads the project identity from .project.json
func (m *ProjectIdentityManager) ReadProjectIdentity() (*ProjectIdentity, error) {
	projectJSONPath := m.getProjectJSONPath()

	data, err := os.ReadFile(projectJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to read project identity: %w", err)
	}

	// Remove trailing commas before closing braces/brackets to handle malformed JSON
	// This regex finds commas followed by optional whitespace and a closing brace/bracket
	cleanedData := regexp.MustCompile(`,\s*([}\]])`).ReplaceAll(data, []byte("$1"))

	var identity ProjectIdentity
	if err := json.Unmarshal(cleanedData, &identity); err != nil {
		return nil, fmt.Errorf("failed to unmarshal project identity: %w", err)
	}

	return &identity, nil
}

// generateWorkspaceID generates a workspace ID by hashing the full project path
func (m *ProjectIdentityManager) generateWorkspaceID() string {
	// Get absolute path to ensure consistency
	absPath, err := filepath.Abs(m.projectRoot)
	if err != nil {
		slog.Warn("Failed to get absolute path, using relative path", "error", err)
		absPath = m.projectRoot
	}

	// The TypeScript version hashes the workspace URI, but in our case
	// we'll hash the absolute path for compatibility
	return m.createHash(absPath)
}

// generateGitID generates a git ID by finding and hashing the git origin URL
func (m *ProjectIdentityManager) generateGitID() (string, error) {
	gitConfigPath := filepath.Join(m.projectRoot, ".git", "config")

	// Check if git config exists
	if _, err := os.Stat(gitConfigPath); err != nil {
		return "", fmt.Errorf("no git config found: %w", err)
	}

	// Read git config file
	gitConfig, err := os.ReadFile(gitConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to read git config: %w", err)
	}

	// Find origin remote URL using regex
	// Match [remote "origin"] section and capture the url value
	originURLRegex := regexp.MustCompile(`\[remote "origin"\][^\[]*url\s*=\s*([^\n\r]+)`)
	matches := originURLRegex.FindSubmatch(gitConfig)
	if len(matches) < 2 {
		return "", fmt.Errorf("no origin URL found in git config")
	}

	originURL := strings.TrimSpace(string(matches[1]))
	if originURL == "" {
		return "", fmt.Errorf("no URL found for origin remote")
	}

	// Normalize git URLs to ensure HTTPS and SSH URLs for the same repo generate the same ID
	normalizedURL := m.normalizeGitURL(originURL)

	return m.createHash(normalizedURL), nil
}

// normalizeGitURL normalizes a git URL to ensure HTTPS and SSH URLs for the same repo generate the same ID
// It extracts the host and repository path, normalizing different URL formats to a common form
func (m *ProjectIdentityManager) normalizeGitURL(gitURL string) string {
	// Remove .git suffix if present
	normalized := strings.TrimSuffix(gitURL, ".git")

	// Handle SSH format (git@host:owner/repo or user@host:owner/repo)
	if strings.Contains(normalized, "@") && strings.Contains(normalized, ":") {
		// Split by @ to get the host part
		parts := strings.SplitN(normalized, "@", 2)
		if len(parts) == 2 {
			// Split by : to separate host from path
			hostAndPath := strings.SplitN(parts[1], ":", 2)
			if len(hostAndPath) == 2 {
				// Return normalized as "host/path"
				return hostAndPath[0] + "/" + hostAndPath[1]
			}
		}
	}

	// Handle HTTPS and other protocol URLs (https://host/owner/repo, git://host/owner/repo, etc.)
	if strings.Contains(normalized, "://") {
		// Remove the protocol
		protocolIndex := strings.Index(normalized, "://")
		if protocolIndex != -1 {
			// Return everything after the protocol
			return normalized[protocolIndex+3:]
		}
	}

	// Handle implicit format (host/owner/repo) - already normalized
	return normalized
}

// createHash creates a hash from the input string matching the TypeScript implementation
func (m *ProjectIdentityManager) createHash(input string) string {
	hash := sha256.Sum256([]byte(input))
	fullHash := fmt.Sprintf("%x", hash)

	// Take first 16 characters and format as UUID-like string
	truncated := fullHash[:16]

	// Insert dashes after every 4 characters: xxxx-xxxx-xxxx-xxxx
	formatted := fmt.Sprintf("%s-%s-%s-%s",
		truncated[0:4],
		truncated[4:8],
		truncated[8:12],
		truncated[12:16])

	return formatted
}

// GetWorkspaceID returns the workspace ID from the project identity
func (m *ProjectIdentityManager) GetWorkspaceID() (string, error) {
	identity, err := m.ReadProjectIdentity()
	if err != nil {
		return "", err
	}
	if identity == nil || identity.WorkspaceID == "" {
		return "", fmt.Errorf("no workspace ID found")
	}
	return identity.WorkspaceID, nil
}

// GetGitID returns the git ID from the project identity
func (m *ProjectIdentityManager) GetGitID() (string, error) {
	identity, err := m.ReadProjectIdentity()
	if err != nil {
		return "", err
	}
	if identity == nil || identity.GitID == "" {
		return "", fmt.Errorf("no git ID found")
	}
	return identity.GitID, nil
}

// GetProjectID returns the git ID if available, otherwise the workspace ID
func (m *ProjectIdentityManager) GetProjectID() (string, error) {
	identity, err := m.ReadProjectIdentity()
	if err != nil {
		return "", err
	}
	if identity == nil {
		return "", fmt.Errorf("no project identity found")
	}

	// Prefer git_id over workspace_id (like the TypeScript implementation)
	if identity.GitID != "" {
		return identity.GitID, nil
	}
	if identity.WorkspaceID != "" {
		return identity.WorkspaceID, nil
	}

	return "", fmt.Errorf("no project ID available")
}

// ValidateUUID checks if a string is a valid UUID format
func ValidateUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// IsValidProjectID checks if a string matches the expected project ID format
// Project IDs are 16 hex characters formatted as: xxxx-xxxx-xxxx-xxxx
func IsValidProjectID(s string) bool {
	// Match the format: 4 groups of 4 hex characters separated by dashes
	matched, _ := regexp.MatchString(`^[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}$`, s)
	return matched
}

// parseGitRemoteURL parses a git remote URL and returns the repository name
func (m *ProjectIdentityManager) parseGitRemoteURL(remoteURL string) string {
	// Remove .git suffix if present
	withoutDotGit := strings.TrimSuffix(remoteURL, ".git")

	var repoName string

	// Case 1: SSH format (git@github.com:owner/repo)
	if strings.HasPrefix(withoutDotGit, "git@") {
		// Split by : and get the part after it
		parts := strings.Split(withoutDotGit, ":")
		if len(parts) >= 2 {
			pathParts := strings.Split(parts[1], "/")
			if len(pathParts) > 0 {
				repoName = pathParts[len(pathParts)-1]
			}
		}
	} else if matched := regexp.MustCompile(`^(https?|git|git\+ssh)://`).MatchString(withoutDotGit); matched {
		// Case 2: HTTP-like protocols (http://, https://, git://, git+ssh://)
		// Remove the protocol part and extract repo name
		urlParts := strings.Split(withoutDotGit, "/")
		if len(urlParts) > 0 {
			repoName = urlParts[len(urlParts)-1]
		}
	} else {
		// Case 3: Implicit HTTPS (github.com/owner/repo)
		// Match domain/path pattern
		if matched := regexp.MustCompile(`^[a-zA-Z0-9.-]+/(.+)`).FindStringSubmatch(withoutDotGit); len(matched) > 1 {
			pathParts := strings.Split(matched[1], "/")
			if len(pathParts) > 0 {
				repoName = pathParts[len(pathParts)-1]
			}
		}
	}

	return repoName
}

// generateProjectName generates a project name from git remote or directory name
func (m *ProjectIdentityManager) generateProjectName() string {
	// First, try to get from git remote
	gitConfigPath := filepath.Join(m.projectRoot, ".git", "config")

	if _, err := os.Stat(gitConfigPath); err == nil {
		// Git config exists, try to parse it
		gitConfig, err := os.ReadFile(gitConfigPath)
		if err == nil {
			// Find origin remote URL using regex
			originURLRegex := regexp.MustCompile(`\[remote "origin"\][^\[]*url\s*=\s*([^\n\r]+)`)
			matches := originURLRegex.FindSubmatch(gitConfig)
			if len(matches) >= 2 {
				originURL := strings.TrimSpace(string(matches[1]))
				if repoName := m.parseGitRemoteURL(originURL); repoName != "" {
					return repoName
				}
			}
		}
	}

	// Fallback: use the last component of the project directory
	return filepath.Base(m.projectRoot)
}

// GetProjectName returns the project name from the project identity
func (m *ProjectIdentityManager) GetProjectName() (string, error) {
	identity, err := m.ReadProjectIdentity()
	if err != nil {
		return "", err
	}
	if identity == nil || identity.ProjectName == "" {
		return "", fmt.Errorf("no project name found")
	}
	return identity.ProjectName, nil
}
