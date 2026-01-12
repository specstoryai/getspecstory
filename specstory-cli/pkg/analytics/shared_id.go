package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"
)

// SharedAnalyticsID represents the shared analytics configuration
type SharedAnalyticsID struct {
	AnalyticsID string    `json:"analytics_id"`
	CreatedAt   time.Time `json:"created_at"`
	Source      string    `json:"source"`
}

// getSharedAnalyticsPath returns the path to the shared analytics file (macOS only)
func getSharedAnalyticsPath() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("shared analytics path only available on macOS")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}

	// macOS-specific path
	return filepath.Join(homeDir, "Library", "Application Support", "SpecStory", "analytics-id.json"), nil
}

// loadOrCreateSharedAnalyticsID loads the shared analytics ID or creates a new one
func loadOrCreateSharedAnalyticsID() (string, error) {
	// Only use shared ID on macOS
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("shared analytics ID only supported on macOS")
	}

	filePath, err := getSharedAnalyticsPath()
	if err != nil {
		return "", err
	}

	// Try to read existing file
	data, err := os.ReadFile(filePath)
	if err == nil {
		// File exists, parse it
		var shared SharedAnalyticsID
		if err := json.Unmarshal(data, &shared); err != nil {
			return "", fmt.Errorf("failed to parse analytics file: %w", err)
		}

		return shared.AnalyticsID, nil
	}

	// File doesn't exist, create new one
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to read analytics file: %w", err)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Generate new analytics ID
	newID := uuid.New().String()

	shared := &SharedAnalyticsID{
		AnalyticsID: newID,
		CreatedAt:   time.Now().UTC(),
		Source:      "specstory-cli",
	}

	if err := saveSharedAnalyticsID(filePath, shared); err != nil {
		return "", err
	}

	return newID, nil
}

// saveSharedAnalyticsID saves the shared analytics configuration to file
func saveSharedAnalyticsID(filePath string, shared *SharedAnalyticsID) error {
	data, err := json.MarshalIndent(shared, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal analytics data: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write analytics file: %w", err)
	}

	return nil
}
