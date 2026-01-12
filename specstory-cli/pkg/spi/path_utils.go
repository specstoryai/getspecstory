package spi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi/schema"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// extractWordsFromMessage extracts up to maxWords from the message, handling various edge cases
func extractWordsFromMessage(message string, maxWords int) []string {
	if message == "" {
		return []string{}
	}

	// Normalize unicode and remove accents
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	normalized, _, _ := transform.String(t, message)

	// Convert to lowercase
	normalized = strings.ToLower(normalized)

	// Replace special characters with their word equivalents
	normalized = strings.ReplaceAll(normalized, "@", " at ")
	normalized = strings.ReplaceAll(normalized, "&", " and ")
	normalized = strings.ReplaceAll(normalized, "#", " hash ")

	// Remove all punctuation and special characters, replace with spaces
	// This regex keeps letters, numbers, and spaces
	re := regexp.MustCompile(`[^a-z0-9\s]+`)
	normalized = re.ReplaceAllString(normalized, " ")

	// Split into words and filter out empty strings
	words := strings.Fields(normalized)

	// Take up to maxWords
	if len(words) > maxWords {
		words = words[:maxWords]
	}

	return words
}

// generateFilenameFromWords creates a filename from words, handling edge cases
func generateFilenameFromWords(words []string) string {
	if len(words) == 0 {
		return ""
	}

	// Join words with hyphens
	filename := strings.Join(words, "-")

	// Ensure no double hyphens
	for strings.Contains(filename, "--") {
		filename = strings.ReplaceAll(filename, "--", "-")
	}

	// Remove leading/trailing hyphens
	filename = strings.Trim(filename, "-")

	return filename
}

// GenerateFilenameFromUserMessage generates a filename based on the first user message content
func GenerateFilenameFromUserMessage(message string) string {
	words := extractWordsFromMessage(message, 4)
	return generateFilenameFromWords(words)
}

// appendRemainingParts appends the remaining path components from parts[startIdx:] to the
// result path, skipping any empty components. This is used when we can't canonicalize the
// rest of the path (e.g., directory doesn't exist or can't be read).
func appendRemainingParts(result string, parts []string, startIdx int) string {
	for j := startIdx; j < len(parts); j++ {
		if parts[j] != "" {
			result = filepath.Join(result, parts[j])
		}
	}
	return result
}

// GetCanonicalPath resolves a path to its canonical form by resolving symlinks
// and correcting case. This is important on case-insensitive filesystems like macOS
// where "/Users/Foo" and "/users/foo" refer to the same directory but would produce
// different hash values.
//
// The algorithm works by walking the path component by component, starting from the root.
// For each component, it opens the parent directory and reads its actual entries,
// performing case-insensitive matching to find the correct casing on disk.
// This ensures that the returned path matches the real filesystem casing, which is
// important for consistent hashing and avoiding subtle bugs on case-insensitive filesystems.
//
// Note: This can be slow for long paths or directories with many entries, since it
// requires reading each directory's contents.
//
// If a component doesn't exist on disk, the remaining path components are
// appended as-is, making this function safe to use with paths that don't
// fully exist yet.
func GetCanonicalPath(p string) (string, error) {
	// Ensure we have an absolute, clean path
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		var err error
		p, err = filepath.Abs(p)
		if err != nil {
			return "", err
		}
	}

	// Resolve any symlinks in the path to get the real physical path
	// This is critical for ensuring consistent path hashing across different
	// ways of accessing the same directory (e.g., through a symlink vs directly)
	realPath, err := filepath.EvalSymlinks(p)
	if err == nil && realPath != p {
		// Successfully resolved symlinks - update p to the resolved path for case normalization
		p = realPath
	} else if err != nil {
		// Symlink resolution failed (e.g., path doesn't exist yet),
		// continue with the original path - we'll still normalize the case below
		slog.Warn("GetCanonicalPath: symlink resolution failed, using original path",
			"path", p, "error", err)
	}

	// Split into components
	parts := strings.Split(p, string(os.PathSeparator))

	// Start with root
	result := string(os.PathSeparator)

	// Skip empty first element (before leading /) that results from splitting "/foo/bar"
	firstPartIndex := 1

	// Build path component by component
	for i := firstPartIndex; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}

		// Open current result directory to read its contents
		f, err := os.Open(result)
		if err != nil {
			// Can't read directory - might not exist yet
			// Append remaining path as-is
			return appendRemainingParts(result, parts, i), nil
		}

		names, err := f.Readdirnames(-1)
		_ = f.Close() // Best effort close, error doesn't affect result
		if err != nil {
			return "", err
		}

		// Find the correct case
		found := false
		for _, name := range names {
			if strings.EqualFold(name, parts[i]) {
				result = filepath.Join(result, name)
				found = true
				break
			}
		}

		if !found {
			// Component doesn't exist, append remaining as-is
			result = appendRemainingParts(result, parts, i)
			break
		}
	}

	return result, nil
}

// GetDebugDir returns the debug directory path for a given session ID or UUID.
//
// The debug directory is used to store detailed JSON records and debugging
// information for SpecStory CLI sessions. This is shared across all providers
// (Claude Code, Codex CLI, Cursor CLI) to maintain a consistent debug structure.
//
// The directory structure is: .specstory/debug/<sessionID>/
//
// Example:
//
//	debugDir := spi.GetDebugDir("abc123-def456")
//	// Returns: ".specstory/debug/abc123-def456"
func GetDebugDir(sessionID string) string {
	return filepath.Join(".specstory", "debug", sessionID)
}

// WriteDebugSessionData writes the SessionData as formatted JSON to the debug directory.
// This provides a standardized, provider-agnostic debug output alongside provider-specific raw data.
//
// The file is written to: .specstory/debug/<sessionID>/session-data.json
//
// Returns an error if the debug directory cannot be created or the file cannot be written.
func WriteDebugSessionData(sessionID string, sessionData *schema.SessionData) error {
	if sessionData == nil {
		return fmt.Errorf("sessionData is nil")
	}

	// Get debug directory
	debugDir := GetDebugDir(sessionID)

	// Create debug directory if it doesn't exist
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return fmt.Errorf("failed to create debug directory: %w", err)
	}

	// Marshal SessionData to pretty-printed JSON
	jsonData, err := json.MarshalIndent(sessionData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal SessionData: %w", err)
	}

	// Write to session-data.json
	filePath := filepath.Join(debugDir, "session-data.json")
	if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write session-data.json: %w", err)
	}

	// Log absolute path for debugging
	absPath, _ := filepath.Abs(filePath)
	slog.Debug("Wrote debug session data", "sessionId", sessionID, "path", absPath)
	return nil
}
