package claudecode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/SpecStoryCLI/pkg/spi"
)

// Global variable to track jsonl burst mode
var jsonlBurstMode bool

// Global variable to track specstory burst mode
var specstoryBurstMode bool

// Global variable to track target file for conditional debug output
var debugTargetFile string

// SetJsonlBurst sets the jsonl burst debug mode
func SetJsonlBurst(enabled bool) {
	jsonlBurstMode = enabled
}

// GetJsonlBurst returns whether jsonl burst mode is enabled
func GetJsonlBurst() bool {
	return jsonlBurstMode
}

// SetSpecstoryBurst sets the specstory burst debug mode
func SetSpecstoryBurst(enabled bool) {
	specstoryBurstMode = enabled
}

// GetSpecstoryBurst returns whether specstory burst mode is enabled
func GetSpecstoryBurst() bool {
	return specstoryBurstMode
}

// SetDebugTargetFile sets the target file for conditional debug output
func SetDebugTargetFile(targetFile string) {
	debugTargetFile = targetFile
}

// ClearDebugTargetFile clears the target file filter
func ClearDebugTargetFile() {
	debugTargetFile = ""
}

// ExtractUUIDFromFilename extracts the UUID from a JSONL filename (exported for use in watcher)
func ExtractUUIDFromFilename(filename string) string {
	// Remove directory path and .jsonl extension
	base := filepath.Base(filename)
	base = strings.TrimSuffix(base, ".jsonl")

	// The UUID is the last part of the filename after the last hyphen
	// e.g., "-Users-sean-Source-SpecStory-compositions-game-4-30cc3569-a9d4-429e-981a-ab73e3ddee5f"
	parts := strings.Split(base, "-")
	if len(parts) >= 5 {
		// UUID format: 8-4-4-4-12 characters
		// So we need at least 5 parts from the end
		uuidParts := parts[len(parts)-5:]
		uuid := strings.Join(uuidParts, "-")
		// Validate it looks like a UUID
		if len(uuid) == 36 && len(uuidParts[0]) == 8 && len(uuidParts[1]) == 4 &&
			len(uuidParts[2]) == 4 && len(uuidParts[3]) == 4 && len(uuidParts[4]) == 12 {
			return uuid
		}
	}

	// Fallback: use the whole filename
	return base
}

// writeJSONToDebugDir writes pretty-printed JSON to a debug directory
func writeJSONToDebugDir(uuid string, filename string, data map[string]interface{}, logPrefix string) error {
	// Create debug directory structure
	debugDir := spi.GetDebugDir(uuid)
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		slog.Error("Failed to create debug directory",
			"directory", debugDir,
			"error", err)
		return nil // Continue without failing
	}

	// Create the full file path
	jsonFilePath := filepath.Join(debugDir, filename)

	// Pretty print the JSON with 2 spaces indentation
	prettyJSON, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		slog.Error("Failed to marshal JSON",
			"filename", filename,
			"error", err)
		return nil // Continue without failing
	}

	// Write the file
	if err := os.WriteFile(jsonFilePath, prettyJSON, 0644); err != nil {
		slog.Error("Failed to write JSON file",
			"logPrefix", logPrefix,
			"path", jsonFilePath,
			"error", err)
		return nil // Continue without failing
	}

	slog.Debug("Debug JSON written",
		"logPrefix", logPrefix,
		"path", jsonFilePath)

	return nil
}

// WriteDebugJSON writes a pretty-printed JSON file for a JSONL record
func WriteDebugJSON(jsonlFile string, lineNumber int, data map[string]interface{}) error {
	if !jsonlBurstMode {
		return nil
	}

	// If we have a target file set, only write debug for that file
	if debugTargetFile != "" {
		if jsonlFile != debugTargetFile {
			// Also try comparing just the base filenames in case paths differ
			if filepath.Base(jsonlFile) != filepath.Base(debugTargetFile) {
				return nil
			}
		}
	}

	// Extract UUID from filename
	uuid := ExtractUUIDFromFilename(jsonlFile)
	filename := fmt.Sprintf("%d.json", lineNumber)

	return writeJSONToDebugDir(uuid, filename, data, "Debug")
}

// CleanDebugDirectory removes all files from a debug directory before writing new ones
func CleanDebugDirectory(uuid string) error {
	debugDir := spi.GetDebugDir(uuid)

	// Check if directory exists
	if _, err := os.Stat(debugDir); os.IsNotExist(err) {
		// Directory doesn't exist, nothing to clean
		return nil
	}

	// Remove all files in the directory
	entries, err := os.ReadDir(debugDir)
	if err != nil {
		return fmt.Errorf("failed to read debug directory for UUID %s: %v", uuid, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			filePath := filepath.Join(debugDir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				slog.Warn("Failed to remove file",
					"path", filePath,
					"uuid", uuid,
					"error", err)
			}
		}
	}

	return nil
}

// WriteSpecstoryBurstJSON writes a pretty-printed JSON file for a record in specstory burst mode
func WriteSpecstoryBurstJSON(sessionUuid string, lineNumber int, data map[string]interface{}) error {
	if !specstoryBurstMode {
		return nil
	}

	filename := fmt.Sprintf("%d.json", lineNumber)
	return writeJSONToDebugDir(sessionUuid, filename, data, "Specstory burst")
}

// WriteDebugRawJSON writes a pretty-printed JSON file for a record in debug-raw mode
// This replaces the old specstory-burst functionality
func WriteDebugRawJSON(sessionUuid string, lineNumber int, data map[string]interface{}) error {
	filename := fmt.Sprintf("%d.json", lineNumber)
	return writeJSONToDebugDir(sessionUuid, filename, data, "Debug raw")
}

// writeDebugRawFiles writes debug JSON files for a Claude Code session.
// Returns a map from record index to file number for reference in markdown generation.
func writeDebugRawFiles(session Session) map[int]int {
	recordToFileNumber := make(map[int]int)

	// Clean the debug directory first
	if err := CleanDebugDirectory(session.SessionUuid); err != nil {
		slog.Warn("Failed to clean debug directory for debug-raw", "error", err)
		return recordToFileNumber
	}

	// Write ALL records as JSON files, numbered sequentially
	fileNumber := 0
	for i, record := range session.Records {
		fileNumber++
		_ = WriteDebugRawJSON(session.SessionUuid, fileNumber, record.Data)
		recordToFileNumber[i] = fileNumber
	}

	return recordToFileNumber
}
