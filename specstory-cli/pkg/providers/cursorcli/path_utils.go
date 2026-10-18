package cursorcli

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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

// ProjectHash returns the Cursor project-hash for a workspace path: the hex MD5 of its canonical
// absolute path. This is the <projectHash> directory name Cursor files a project's sessions under
// (~/.cursor/chats/<projectHash>/). Returns "" if the path can't be made absolute. The reindex
// cwd-recovery (RecoverOriginCwds) relies on this producing the SAME hash Cursor itself used, so
// GetProjectHashDir is built on it to keep the two in lockstep.
func ProjectHash(projectPath string) string {
	// Get absolute path of the project directory
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return ""
	}

	// Resolve to canonical path with correct case (important for case-insensitive filesystems like macOS)
	canonicalPath, err := spi.GetCanonicalPath(absPath)
	if err != nil {
		// If getting canonical path fails, use absPath as fallback
		canonicalPath = absPath
	}

	hash := md5.Sum([]byte(canonicalPath))
	return hex.EncodeToString(hash[:])
}

// GetProjectHashDir calculates the MD5 hash of the project path and returns the Cursor project directory
// Returns the hash directory path and nil on success, or empty string and error on failure
func GetProjectHashDir(projectPath string) (string, error) {
	// Get the cursor chats directory
	chatsDir, err := GetCursorChatsDir()
	if err != nil {
		return "", err
	}

	projectHash := ProjectHash(projectPath)
	if projectHash == "" {
		return "", fmt.Errorf("failed to resolve cursor project hash for %q", projectPath)
	}

	// Build and return the project-specific hash directory path
	return filepath.Join(chatsDir, projectHash), nil
}

// RecoverOriginCwds fills in OriginCwd for Cursor session refs whose project can't be resolved from
// the session itself (Cursor records no cwd in its store). Cursor files sessions under
// ~/.cursor/chats/md5(cwd)/, and md5 is one-way, so we can't invert the hash — instead we build
// md5(cwd)→cwd from the caller's known cwds (the real cwds of every OTHER provider's sessions,
// collected during reindex) and look each Cursor session up by the project-hash embedded in its
// NativePath. Refs already carrying an OriginCwd, and those whose hash has no known cwd (a project
// touched by no other agent), are left unchanged. Mutates refs in place.
func RecoverOriginCwds(refs []spi.GlobalSessionRef, knownCwds []string) {
	if len(refs) == 0 || len(knownCwds) == 0 {
		return
	}

	hashToCwd := make(map[string]string, len(knownCwds))
	for _, cwd := range knownCwds {
		if h := ProjectHash(cwd); h != "" {
			hashToCwd[h] = cwd
		}
	}

	for i := range refs {
		if refs[i].OriginCwd != "" {
			continue
		}
		if cwd, ok := hashToCwd[cursorProjectHash(refs[i].NativePath)]; ok {
			refs[i].OriginCwd = cwd
		}
	}
}

// cursorProjectHash extracts the project-hash directory name from a Cursor session's NativePath
// (<chatsDir>/<projectHash>/<sessionID>/store.db → <projectHash>, i.e. two dirs up from store.db).
// Returns "" when the path doesn't have that shape (guards against matching an empty-string hash).
func cursorProjectHash(nativePath string) string {
	if nativePath == "" {
		return ""
	}
	h := filepath.Base(filepath.Dir(filepath.Dir(nativePath)))
	if !isMD5Hex(h) {
		return ""
	}
	return h
}

// isMD5Hex reports whether s is a 32-char lowercase hex string (an MD5 digest), so a malformed
// NativePath component can't be mistaken for a project hash.
func isMD5Hex(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		isHexDigit := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHexDigit {
			return false
		}
	}
	return true
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
