package opencode

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Package-level function variables for testing dependency injection.
var (
	osGetwd       = os.Getwd
	osUserHomeDir = os.UserHomeDir
	osStat        = os.Stat
	execCommand   = exec.Command
	osReadFile    = os.ReadFile
)

// GlobalProjectHash is the special hash used by OpenCode for global sessions.
// SpecStory is project-centric and doesn't support global sessions.
const GlobalProjectHash = "global"

// OpenCodePathError describes actionable filesystem failures when locating OpenCode data.
type OpenCodePathError struct {
	Kind        string   // storage_missing, project_missing, global_session
	Path        string   // offending path
	ProjectHash string   // computed hash when relevant
	KnownHashes []string // hashes discovered on disk (optional)
	Message     string   // user-facing explanation
}

func (e *OpenCodePathError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// GetStorageDir returns the OpenCode storage directory.
// OpenCode stores all data at ~/.local/share/opencode/storage/
func GetStorageDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(homeDir, ".local", "share", "opencode", "storage"), nil
}

// ComputeProjectHash computes the project hash for a given directory.
// OpenCode uses the git root commit hash to identify projects, not a hash of the path.
// The algorithm:
// 1. Find the .git directory by walking up from projectPath
// 2. Check for a cached hash in .git/opencode
// 3. Run `git rev-list --max-parents=0 --all` to get root commit(s)
// 4. Sort the commits and use the first one (alphabetically)
// 5. Returns "global" if not in a git repo or no commits found
func ComputeProjectHash(projectPath string) (string, error) {
	if projectPath == "" {
		return "", fmt.Errorf("project path is empty")
	}

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path for %q: %w", projectPath, err)
	}

	// Find .git directory by walking up from the project path
	gitDir, err := findGitDir(absPath)
	if err != nil {
		slog.Debug("ComputeProjectHash: Not in a git repository, returning global",
			"projectPath", absPath,
			"error", err)
		return GlobalProjectHash, nil
	}

	// Check for cached hash in .git/opencode (OpenCode caches the hash here)
	cacheFile := filepath.Join(gitDir, "opencode")
	if cachedHash, err := osReadFile(cacheFile); err == nil {
		hash := strings.TrimSpace(string(cachedHash))
		if hash != "" {
			slog.Debug("ComputeProjectHash: Using cached hash from .git/opencode",
				"hash", hash,
				"cacheFile", cacheFile)
			return hash, nil
		}
	}

	// Get the git worktree directory (parent of .git)
	worktree := filepath.Dir(gitDir)

	// Get root commit(s) using git rev-list --max-parents=0 --all
	cmd := execCommand("git", "rev-list", "--max-parents=0", "--all")
	cmd.Dir = worktree
	output, err := cmd.Output()
	if err != nil {
		slog.Debug("ComputeProjectHash: Failed to get root commits, returning global",
			"projectPath", absPath,
			"error", err)
		return GlobalProjectHash, nil
	}

	// Parse and sort root commits
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var roots []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			roots = append(roots, line)
		}
	}

	if len(roots) == 0 {
		slog.Debug("ComputeProjectHash: No root commits found, returning global",
			"projectPath", absPath)
		return GlobalProjectHash, nil
	}

	// Sort and take the first one (alphabetically, matching OpenCode's toSorted())
	sort.Strings(roots)
	hash := roots[0]

	slog.Debug("ComputeProjectHash: Computed hash from git root commit",
		"projectPath", absPath,
		"hash", hash,
		"rootCommitCount", len(roots))

	return hash, nil
}

// findGitDir walks up from the given path to find a .git directory.
// Returns the path to the .git directory or an error if not found.
func findGitDir(startPath string) (string, error) {
	current := startPath
	for {
		gitPath := filepath.Join(current, ".git")
		info, err := osStat(gitPath)
		if err == nil && info.IsDir() {
			return gitPath, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Reached root without finding .git
			return "", fmt.Errorf("not a git repository: %s", startPath)
		}
		current = parent
	}
}

// GetProjectDir returns the OpenCode project directory path for the given project path.
// This computes the expected directory path but does not verify it exists.
// If projectPath is empty, uses the current working directory.
// Note: This function does not validate global sessions - use ResolveProjectDir for that.
func GetProjectDir(projectPath string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	projectHash, err := ComputeProjectHash(projectPath)
	if err != nil {
		return "", err
	}

	storageDir, err := GetStorageDir()
	if err != nil {
		return "", err
	}

	resolvedDir := filepath.Join(storageDir, "session", projectHash)

	slog.Debug("GetProjectDir: Computed OpenCode project directory",
		"projectPath", projectPath,
		"projectHash", projectHash,
		"storageDir", storageDir,
		"resolvedDir", resolvedDir)

	return resolvedDir, nil
}

// ResolveProjectDir ensures both storage and project session directories exist on disk.
// Returns an error if the project hash resolves to "global" (not supported by SpecStory).
func ResolveProjectDir(projectPath string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	projectHash, err := ComputeProjectHash(projectPath)
	if err != nil {
		return "", err
	}

	// Explicitly reject global sessions - SpecStory is project-centric
	if projectHash == GlobalProjectHash {
		return "", &OpenCodePathError{
			Kind:        "global_session",
			ProjectHash: projectHash,
			Message:     "Global OpenCode sessions are not supported. SpecStory requires a project-specific context.",
		}
	}

	storageDir, err := GetStorageDir()
	if err != nil {
		return "", err
	}

	slog.Debug("ResolveProjectDir: Checking for OpenCode directories",
		"storageDir", storageDir,
		"projectHash", projectHash)

	// Check if storage directory exists
	if _, err := osStat(storageDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("ResolveProjectDir: OpenCode storage directory not found", "storageDir", storageDir)
			return "", &OpenCodePathError{
				Kind:    "storage_missing",
				Path:    storageDir,
				Message: fmt.Sprintf("OpenCode storage directory %q not found. Run OpenCode at least once or verify ~/.local/share/opencode exists.", storageDir),
			}
		}
		return "", fmt.Errorf("failed to read OpenCode storage directory %q: %w", storageDir, err)
	}

	projectDir := filepath.Join(storageDir, "session", projectHash)
	if _, err := osStat(projectDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			hashes, listErr := ListProjectHashes()
			if listErr != nil {
				hashes = nil
			}

			var known string
			if len(hashes) > 0 {
				known = strings.Join(hashes, ", ")
			} else {
				known = "(none discovered)"
			}

			slog.Warn("ResolveProjectDir: OpenCode project directory not found",
				"projectDir", projectDir,
				"knownHashes", known)

			return "", &OpenCodePathError{
				Kind:        "project_missing",
				Path:        projectDir,
				ProjectHash: projectHash,
				KnownHashes: hashes,
				Message: fmt.Sprintf("No OpenCode data found for this project (expected %q). Known project hashes: %s. Start an OpenCode session in your repo to create it.",
					projectDir, known),
			}
		}
		return "", fmt.Errorf("failed to read OpenCode project directory %q: %w", projectDir, err)
	}

	slog.Debug("ResolveProjectDir: Successfully resolved OpenCode project directory", "projectDir", projectDir)

	return projectDir, nil
}

// ListProjectHashes returns the list of project hash directories currently on disk.
func ListProjectHashes() ([]string, error) {
	storageDir, err := GetStorageDir()
	if err != nil {
		return nil, err
	}

	sessionDir := filepath.Join(storageDir, "session")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenCode session directory %q: %w", sessionDir, err)
	}

	var hashes []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != GlobalProjectHash {
			hashes = append(hashes, entry.Name())
		}
	}

	sort.Strings(hashes)
	return hashes, nil
}

// GetSessionsDir returns the sessions directory path for a given project hash.
// Sessions are stored at: storage/session/{projectHash}/
func GetSessionsDir(projectHash string) (string, error) {
	if projectHash == "" {
		return "", fmt.Errorf("project hash is empty")
	}

	storageDir, err := GetStorageDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(storageDir, "session", projectHash), nil
}

// GetMessagesDir returns the messages directory path for a given session ID.
// Messages are stored at: storage/message/ses_{id}/ or storage/message/{sessionID}/
// The sessionID should be the full session ID (e.g., "ses_abc123").
func GetMessagesDir(sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("session ID is empty")
	}

	storageDir, err := GetStorageDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(storageDir, "message", sessionID), nil
}

// GetPartsDir returns the parts directory path for a given message ID.
// Parts are stored at: storage/part/msg_{id}/ or storage/part/{messageID}/
// The messageID should be the full message ID (e.g., "msg_abc123").
func GetPartsDir(messageID string) (string, error) {
	if messageID == "" {
		return "", fmt.Errorf("message ID is empty")
	}

	storageDir, err := GetStorageDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(storageDir, "part", messageID), nil
}

// GetProjectFilePath returns the path to a project's JSON file.
// Project files are stored at: storage/project/{projectHash}.json
func GetProjectFilePath(projectHash string) (string, error) {
	if projectHash == "" {
		return "", fmt.Errorf("project hash is empty")
	}

	storageDir, err := GetStorageDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(storageDir, "project", projectHash+".json"), nil
}
