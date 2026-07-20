package utils

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
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

	// Resolve identity by walking up to the git root (NOT the launch directory), so a
	// session run from a monorepo subdirectory attributes to the repo, not a fragment.
	// resolvedName always falls back to the root's base name, so it is never empty.
	resolvedGitID, resolvedWorkspaceID, resolvedName, _ := resolveIdentity(m.projectRoot)

	// Determine what needs to be done
	var identity ProjectIdentity
	isModified := false

	isNewProject := false
	if existingIdentity == nil {
		// Case 1: No .project.json yet
		slog.Debug("No existing project identity found, creating new identity")
		identity = ProjectIdentity{
			WorkspaceID:   resolvedWorkspaceID,
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
			identity.WorkspaceID = resolvedWorkspaceID
			identity.WorkspaceIDAt = time.Now().UTC().Format(time.RFC3339)
			isModified = true
		}
	}

	// Check if we need to add git_id
	if identity.GitID == "" && resolvedGitID != "" {
		identity.GitID = resolvedGitID
		identity.GitIDAt = time.Now().UTC().Format(time.RFC3339)
		isModified = true
		slog.Debug("Added git_id to project identity", "git_id", resolvedGitID)
	}

	// Check if we need to add project_name
	if identity.ProjectName == "" {
		identity.ProjectName = resolvedName
		isModified = true
		slog.Debug("Added project_name to project identity", "project_name", resolvedName)
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
	// we'll hash the absolute path for compatibility. The path is canonicalized first so
	// the same physical directory yields one id regardless of the casing a caller passes
	// (os.Getwd may echo a lowercase $PWD; a session's recorded cwd may differ in case).
	return m.createHash(canonicalizeWorkspacePath(absPath))
}

// caseInsensitiveFilesystem is a best-effort, OS-level default for whether paths
// should be case-folded — NOT a per-volume probe. macOS and Windows volumes are
// usually case-insensitive and Linux case-sensitive, so we key off GOOS for
// simplicity and stability. It is a var so tests can exercise both behaviors.
// Windows is included even though the app is built only for macOS/Linux today: the
// in-flight Windows support lives on a branch that does not carry this file, so
// folding case here keeps that branch from silently reintroducing case-divergent
// workspace ids when it merges.
//
// Accepted trade-off: macOS (APFS/HFS+) can be formatted case-SENSITIVE, where
// this default wrongly folds two genuinely-distinct directories that differ only
// by case into one workspace_id. That is rare and low-impact — case-sensitive
// macOS volumes are uncommon, and it only affects the path-based fallback id
// (git-remote projects hash the normalized URL, not the path). It is the
// deliberate inverse of the common bug this fixes: case-divergent references to
// the SAME directory on the usual case-insensitive volume (e.g. a lowercased
// $PWD vs a session's recorded cwd) hashing to two different ids.
var caseInsensitiveFilesystem = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// canonicalizeWorkspacePath normalizes a path before it is hashed into a workspace_id. On
// a case-insensitive filesystem the same directory can be named with different casing,
// which would otherwise hash to different ids for one physical directory; case-folding
// makes the id stable. On case-sensitive filesystems the path is left byte-exact, since
// there two differently-cased paths are genuinely different directories.
func canonicalizeWorkspacePath(path string) string {
	if caseInsensitiveFilesystem {
		return strings.ToLower(path)
	}
	return path
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

// ---------------------------------------------------------------------------
// Walk-up identity resolution (shared by ComputeProjectID and the writer).
//
// The stored .specstory/.project.json fragments a monorepo: it is written per
// launch directory, keyed to a .git at exactly that directory with no walk-up — so a
// session launched from a subdirectory gets no git_id and a path-based workspace_id all
// its own. The resolver below instead walks UP to the git root and computes identity
// from there, so every directory inside one repo resolves to a single project. See
// docs/SESSIONS-DB.md.
// ---------------------------------------------------------------------------

// originRemoteURLRegex captures the origin remote's url value from a git config file.
var originRemoteURLRegex = regexp.MustCompile(`\[remote "origin"\][^\[]*url\s*=\s*([^\n\r]+)`)

// readOriginURL returns the origin remote URL from a git config file, or "" if the
// file is unreadable or has no origin remote.
func readOriginURL(gitConfigPath string) string {
	gitConfig, err := os.ReadFile(gitConfigPath)
	if err != nil {
		return ""
	}
	matches := originRemoteURLRegex.FindSubmatch(gitConfig)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(string(matches[1]))
}

// findGitRoot walks up from dir to the nearest ancestor containing a .git entry and
// returns it. The .git entry may be a directory (normal clone) or a FILE (git
// worktree / submodule); both satisfy the search. Returns false if no .git is found
// before the filesystem root. The walk-up is what lets a session launched from a
// monorepo subdirectory resolve to the repo's identity.
func findGitRoot(dir string) (string, bool) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false // reached the filesystem root without finding .git
		}
		dir = parent
	}
}

// resolveGitConfigPath returns the path to the git config file that holds a root's
// remotes, handling both a normal `.git` directory and a `.git` FILE (worktrees /
// submodules, whose private gitdir points at a commondir that holds the shared
// config). Returns "" when it cannot be resolved.
func resolveGitConfigPath(gitRoot string) string {
	gitPath := filepath.Join(gitRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return filepath.Join(gitPath, "config")
	}

	// `.git` is a file of the form "gitdir: <path>" pointing at the worktree's private
	// git dir; the shared config lives in the common dir, not that private dir.
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir:") {
		return ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(content, "gitdir:"))
	if gitDir == "" {
		return ""
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(gitRoot, gitDir)
	}
	commonDir := gitDir
	if cd, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		c := strings.TrimSpace(string(cd))
		if !filepath.IsAbs(c) {
			c = filepath.Join(gitDir, c)
		}
		commonDir = filepath.Clean(c)
	}
	return filepath.Join(commonDir, "config")
}

// resolveIdentity computes a project's identity from a starting directory by walking
// up to the git root. It reads only git metadata — never any .specstory/.project.json
// — so it cannot be fooled by the per-directory identity files that fragment a
// monorepo. Returns the git_id (empty when there is no resolvable origin remote), the
// workspace_id (hash of the resolved root), the project name, and the resolved root.
func resolveIdentity(startDir string) (gitID, workspaceID, name, root string) {
	root = startDir
	if gr, ok := findGitRoot(startDir); ok {
		root = gr
	}

	// A zero-state manager rooted at the resolved root reuses the existing hashing /
	// normalization / repo-name helpers (they do not depend on manager state).
	rooted := &ProjectIdentityManager{projectRoot: root}
	workspaceID = rooted.generateWorkspaceID()
	if cfg := resolveGitConfigPath(root); cfg != "" {
		if originURL := readOriginURL(cfg); originURL != "" {
			gitID = rooted.createHash(rooted.normalizeGitURL(originURL))
			name = rooted.parseGitRemoteURL(originURL)
		}
	}
	if name == "" {
		name = filepath.Base(root)
	}
	return gitID, workspaceID, name, root
}

// ComputeProjectID resolves the restore-index project identity for a session's
// working directory. It walks up to the git root and computes identity fresh, never
// writing a .specstory/.project.json. Returns the project id (git_id when a remote
// is resolvable, else the path-based workspace_id) and the project name.
//
// Stored .project.json handling differs by branch, on purpose:
//   - With a resolvable remote, the git_id is computed from the remote URL and any
//     stored file is ignored — this is what collapses a monorepo to one id.
//   - In the no-remote fallback, a stored workspace_id IS preferred (see below).
//     That deliberately keeps this id equal to the one cloud sync derives from the
//     same launch dir's .project.json (sync.go reads it directly and does NOT walk
//     up). The trade: a remote-less git monorepo whose subdirs carry legacy
//     per-subdir .project.json files stays fragmented in BOTH the index and cloud,
//     rather than collapsing in the index while diverging from cloud.
func ComputeProjectID(cwd string) (id, name string, err error) {
	if strings.TrimSpace(cwd) == "" {
		return "", "", fmt.Errorf("cannot resolve project id: empty working directory")
	}
	gitID, workspaceID, name, _ := resolveIdentity(cwd)

	// Prefer a resolvable git_id: it hashes the normalized remote URL, not a path, so it is
	// immune to the case divergence handled below and never fragments a monorepo.
	if gitID != "" {
		return gitID, name, nil
	}

	// No git remote → use the path-based workspace_id. Prefer an id already persisted in the
	// launch dir's .project.json so every consumer (reindex, resume, cloud sync) agrees on a
	// single value, even for projects whose file predates path canonicalization. Only when no
	// file exists (e.g. a portable session whose recorded cwd is not present locally) do we
	// fall back to the freshly computed — and now canonical — hash.
	if stored := persistedWorkspaceID(cwd); stored != "" {
		return stored, name, nil
	}
	if workspaceID == "" {
		return "", "", fmt.Errorf("cannot resolve project id for %q", cwd)
	}
	return workspaceID, name, nil
}

// persistedWorkspaceID returns the workspace_id recorded in dir's .specstory/.project.json,
// or "" when there is no readable file or it carries no workspace_id. The file is read from
// the launch directory (where the writer puts it), not the resolved git root.
func persistedWorkspaceID(dir string) string {
	m := &ProjectIdentityManager{projectRoot: dir}
	identity, err := m.ReadProjectIdentity()
	if err != nil || identity == nil {
		return ""
	}
	return identity.WorkspaceID
}
