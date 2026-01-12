package cursorcli

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// GetCursorChatsDir returns the path to the Cursor chats directory
// Returns the path and nil on success, or empty string and error on failure
func GetCursorChatsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".cursor", "chats"), nil
}

// GetProjectHashDir calculates the MD5 hash of the project path and returns the Cursor project directory
// Returns the hash directory path and nil on success, or empty string and error on failure
func GetProjectHashDir(projectPath string) (string, error) {
	// Get the cursor chats directory
	chatsDir, err := GetCursorChatsDir()
	if err != nil {
		return "", err
	}

	// Get absolute path of the project directory
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}

	// Resolve to canonical path with correct case (important for case-insensitive filesystems like macOS)
	canonicalPath, err := spi.GetCanonicalPath(absPath)
	if err != nil {
		// If getting canonical path fails, use absPath as fallback
		canonicalPath = absPath
	}

	// Calculate MD5 hash of the canonical path
	hash := md5.Sum([]byte(canonicalPath))
	projectHash := hex.EncodeToString(hash[:])

	// Build and return the project-specific hash directory path
	return filepath.Join(chatsDir, projectHash), nil
}

// GetCursorSessionDirs returns all session directories within a project hash directory
// Returns a slice of session directory names (UUIDs) and nil on success, or nil and error on failure
func GetCursorSessionDirs(hashDir string) ([]string, error) {
	// Check if the hash directory exists
	if _, err := os.Stat(hashDir); err != nil {
		return nil, err
	}

	// Read the hash directory to get session subdirectories
	entries, err := os.ReadDir(hashDir)
	if err != nil {
		return nil, err
	}

	// Filter to only directories
	var sessionDirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			sessionDirs = append(sessionDirs, entry.Name())
		}
	}

	return sessionDirs, nil
}

// HasStoreDB checks if a store.db file exists in the given session directory
func HasStoreDB(hashDir, sessionID string) bool {
	storeDbPath := filepath.Join(hashDir, sessionID, "store.db")
	_, err := os.Stat(storeDbPath)
	return err == nil
}
