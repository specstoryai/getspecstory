package geminicli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

var (
	osGetwd = os.Getwd
)

// GeminiPathError describes actionable filesystem failures when locating Gemini data.
type GeminiPathError struct {
	Kind        string   // tmp_missing, project_missing
	Path        string   // offending path
	ProjectHash string   // computed hash when relevant
	KnownHashes []string // hashes discovered on disk (optional)
	Message     string   // user-facing explanation
}

func (e *GeminiPathError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

// HashProjectPath deterministically hashes an absolute project path using SHA256.
// On case-insensitive filesystems (macOS), it resolves the path to its canonical
// form with the correct case to ensure consistent hashing.
func HashProjectPath(projectPath string) (string, error) {
	if projectPath == "" {
		return "", fmt.Errorf("project path is empty")
	}

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute path for %q: %w", projectPath, err)
	}

	// Resolve to canonical path with correct case (important for case-insensitive filesystems like macOS)
	canonicalPath, err := spi.GetCanonicalPath(absPath)
	if err != nil {
		// If getting canonical path fails, use absPath as fallback
		slog.Warn("HashProjectPath: Failed to get canonical path, using absolute path",
			"absPath", absPath,
			"error", err)
		canonicalPath = absPath
	} else if canonicalPath != absPath {
		// Log when the path was canonicalized (case was corrected)
		slog.Debug("HashProjectPath: Resolved path to canonical form",
			"original", absPath,
			"canonical", canonicalPath)
	}

	hash := sha256.Sum256([]byte(canonicalPath))
	return hex.EncodeToString(hash[:]), nil
}

// GetGeminiTmpDir returns the path to the Gemini tmp directory
func GetGeminiTmpDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}
	return filepath.Join(homeDir, ".gemini", "tmp"), nil
}

// GetGeminiProjectDir returns the Gemini project directory for the given path.
// It calculates the SHA256 hash of the project path.
func GetGeminiProjectDir(projectPath string) (string, error) {
	if projectPath == "" {
		var err error
		projectPath, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %v", err)
		}
	}

	projectHash, err := HashProjectPath(projectPath)
	if err != nil {
		return "", err
	}

	tmpDir, err := GetGeminiTmpDir()
	if err != nil {
		return "", err
	}

	resolvedDir := filepath.Join(tmpDir, projectHash)

	slog.Debug("GetGeminiProjectDir: Computed Gemini project directory",
		"projectPath", projectPath,
		"projectHash", projectHash,
		"tmpDir", tmpDir,
		"resolvedDir", resolvedDir)

	return resolvedDir, nil
}

// ResolveGeminiProjectDir locates the Gemini project directory on disk.
// It tries three strategies in order:
//  1. Basename hint — tmpDir/<project-basename> with matching .project_root (fastest, most common for new CLI)
//  2. Hash-based — tmpDir/<sha256-hash> (legacy Gemini CLI format)
//  3. Full scan — read every .project_root in tmpDir (handles suffixed dirs like my-project-1)
func ResolveGeminiProjectDir(projectPath string) (string, error) {
	// Resolve projectPath once — used both for hash computation and .project_root matching
	if projectPath == "" {
		var err error
		projectPath, err = osGetwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current working directory: %v", err)
		}
	}

	hashDir, err := GetGeminiProjectDir(projectPath)
	if err != nil {
		return "", err
	}

	tmpDir := filepath.Dir(hashDir)

	slog.Debug("ResolveGeminiProjectDir: Checking for Gemini directories",
		"tmpDir", tmpDir,
		"hashDir", hashDir)

	if _, err := osStat(tmpDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("ResolveGeminiProjectDir: Gemini tmp directory not found", "tmpDir", tmpDir)
			return "", &GeminiPathError{
				Kind:    "tmp_missing",
				Path:    tmpDir,
				Message: fmt.Sprintf("Gemini tmp directory %q not found. Run the Gemini CLI at least once or verify ~/.gemini exists.", tmpDir),
			}
		}
		return "", fmt.Errorf("failed to read Gemini tmp directory %q: %w", tmpDir, err)
	}

	// 1. Basename hint — check tmpDir/<basename> with .project_root match
	canonicalProjectPath := canonicalizeProjectPath(projectPath)
	if dir := findProjectDirByBasename(tmpDir, projectPath, canonicalProjectPath); dir != "" {
		return dir, nil
	}

	// 2. Hash-based — legacy Gemini CLI format
	if _, err := osStat(hashDir); err == nil {
		slog.Info("ResolveGeminiProjectDir: Successfully resolved via hash directory", "hashDir", hashDir)
		return hashDir, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Warn("ResolveGeminiProjectDir: Unexpected error checking hash directory",
			"hashDir", hashDir, "error", err)
	}

	// 3. Full scan — handles suffixed dirs like my-project-1
	if dir, scanErr := findProjectDirByProjectRoot(tmpDir, projectPath); scanErr != nil {
		slog.Warn("ResolveGeminiProjectDir: Error during .project_root scan",
			"tmpDir", tmpDir, "error", scanErr)
	} else if dir != "" {
		slog.Info("ResolveGeminiProjectDir: Found project via .project_root scan", "dir", dir)
		return dir, nil
	}

	// Nothing found — build a helpful error
	hashes, listErr := ListGeminiProjectHashes()
	if listErr != nil {
		hashes = nil
	}

	var known string
	if len(hashes) > 0 {
		known = strings.Join(hashes, ", ")
	} else {
		known = "(none discovered)"
	}

	slog.Warn("ResolveGeminiProjectDir: Gemini project directory not found",
		"hashDir", hashDir,
		"knownHashes", known)

	return "", &GeminiPathError{
		Kind:        "project_missing",
		Path:        hashDir,
		ProjectHash: filepath.Base(hashDir),
		KnownHashes: hashes,
		Message: fmt.Sprintf("No Gemini data found for this project (expected %q). Known project hashes: %s. Start a Gemini CLI session in your repo to create it.",
			hashDir, known),
	}
}

// matchesProjectRoot checks whether dir contains a .project_root file whose
// content matches canonicalProjectPath. The stored path is canonicalized before
// comparison, but canonicalProjectPath must already be in canonical form (via
// canonicalizeProjectPath or spi.GetCanonicalPath).
func matchesProjectRoot(dir, canonicalProjectPath string) bool {
	content, err := os.ReadFile(filepath.Join(dir, ".project_root"))
	if err != nil {
		return false
	}

	storedPath := strings.TrimSpace(string(content))
	canonicalStored, err := spi.GetCanonicalPath(storedPath)
	if err != nil {
		slog.Warn("matchesProjectRoot: Failed to canonicalize stored path, using as-is",
			"storedPath", storedPath, "error", err)
		canonicalStored = storedPath
	}

	return canonicalStored == canonicalProjectPath
}

// canonicalizeProjectPath returns the canonical form of projectPath for
// case-insensitive filesystem matching. Falls back to the original path on error.
func canonicalizeProjectPath(projectPath string) string {
	canonical, err := spi.GetCanonicalPath(projectPath)
	if err != nil {
		slog.Warn("canonicalizeProjectPath: Failed to canonicalize, using as-is",
			"projectPath", projectPath, "error", err)
		return projectPath
	}
	return canonical
}

// findProjectDirByBasename tries the most likely name-based directory first:
// tmpDir/<basename>. Returns the directory path if its .project_root matches,
// or empty string otherwise.
func findProjectDirByBasename(tmpDir, projectPath, canonicalProjectPath string) string {
	basename := filepath.Base(projectPath)
	candidate := filepath.Join(tmpDir, basename)

	info, err := osStat(candidate)
	if err != nil || !info.IsDir() {
		return ""
	}

	if matchesProjectRoot(candidate, canonicalProjectPath) {
		slog.Info("findProjectDirByBasename: Matched project directory via basename hint",
			"dir", candidate, "projectPath", projectPath)
		return candidate
	}

	return ""
}

// findProjectDirByProjectRoot scans tmpDir for any subdirectory whose .project_root
// file content matches projectPath. This supports Gemini CLI v0.30.0+ which uses
// project-name-based directories (possibly with numeric suffixes like my-project-1).
func findProjectDirByProjectRoot(tmpDir, projectPath string) (string, error) {
	canonicalProjectPath := canonicalizeProjectPath(projectPath)

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", fmt.Errorf("failed to read Gemini tmp directory %q: %w", tmpDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(tmpDir, entry.Name())
		if matchesProjectRoot(dir, canonicalProjectPath) {
			slog.Info("findProjectDirByProjectRoot: Found matching project directory via .project_root",
				"dir", dir, "projectPath", projectPath)
			return dir, nil
		}
	}

	return "", nil
}

// ListGeminiProjectHashes returns the list of project hash directories currently on disk.
func ListGeminiProjectHashes() ([]string, error) {
	tmpDir, err := GetGeminiTmpDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read Gemini tmp directory %q: %w", tmpDir, err)
	}

	var hashes []string
	for _, entry := range entries {
		if entry.IsDir() {
			hashes = append(hashes, entry.Name())
		}
	}

	sort.Strings(hashes)
	return hashes, nil
}
