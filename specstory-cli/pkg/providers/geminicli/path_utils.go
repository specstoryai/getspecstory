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

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
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

// ResolveGeminiProjectDir ensures both tmp and hash directories exist on disk.
func ResolveGeminiProjectDir(projectPath string) (string, error) {
	projectDir, err := GetGeminiProjectDir(projectPath)
	if err != nil {
		return "", err
	}

	tmpDir := filepath.Dir(projectDir)

	slog.Debug("ResolveGeminiProjectDir: Checking for Gemini directories",
		"tmpDir", tmpDir,
		"projectDir", projectDir)

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

	if _, err := osStat(projectDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
				"projectDir", projectDir,
				"knownHashes", known)

			return "", &GeminiPathError{
				Kind:        "project_missing",
				Path:        projectDir,
				ProjectHash: filepath.Base(projectDir),
				KnownHashes: hashes,
				Message: fmt.Sprintf("No Gemini data found for this project (expected %q). Known project hashes: %s. Start a Gemini CLI session in your repo to create it.",
					projectDir, known),
			}
		}
		return "", fmt.Errorf("failed to read Gemini project directory %q: %w", projectDir, err)
	}

	slog.Info("ResolveGeminiProjectDir: Successfully resolved Gemini project directory", "projectDir", projectDir)

	return projectDir, nil
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
