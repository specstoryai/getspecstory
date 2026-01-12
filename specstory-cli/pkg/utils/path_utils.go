package utils

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// Directory constants
const SPECSTORY_DIR = ".specstory"
const HISTORY_DIR = "history"
const DEBUG_DIR = "debug"
const DEBUG_LOG_FILE = "debug.log"

// OutputConfig interface defines methods for getting output directories
type OutputConfig interface {
	GetHistoryDir() string
	GetDebugDir() string
}

// OutputPathConfig manages all output directory configuration
type OutputPathConfig struct {
	BaseDir string // The validated absolute path
}

// Ensure OutputPathConfig implements OutputConfig interface
var _ OutputConfig = (*OutputPathConfig)(nil)

// NewOutputPathConfig creates and validates an output configuration
func NewOutputPathConfig(dir string) (*OutputPathConfig, error) {
	if dir == "" {
		return &OutputPathConfig{}, nil // Use defaults
	}

	// Convert to absolute path if relative
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return nil, ValidationError{Message: fmt.Sprintf("invalid output directory path: %v", err)}
	}

	// Check if path exists
	info, err := os.Stat(absPath)
	if err == nil {
		// Path exists, check if it's a directory
		if !info.IsDir() {
			return nil, ValidationError{Message: fmt.Sprintf("output path exists but is not a directory: %s", absPath)}
		}
		// Check write permissions by attempting to create a temp file
		if file, err := os.CreateTemp(absPath, ".specstory_write_test_*"); err != nil {
			return nil, ValidationError{Message: fmt.Sprintf("output directory is not writable: %s", absPath)}
		} else {
			// Clean up test file
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
		slog.Debug("Using existing output directory", "path", absPath)
	} else if os.IsNotExist(err) {
		// Path doesn't exist, try to create it
		slog.Info("Creating output directory", "path", absPath)
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, ValidationError{Message: fmt.Sprintf("failed to create output directory: %v", err)}
		}
		slog.Info("Created output directory", "path", absPath)
	} else {
		// Some other error occurred
		return nil, ValidationError{Message: fmt.Sprintf("error checking output directory: %v", err)}
	}

	return &OutputPathConfig{BaseDir: absPath}, nil
}

// getBasePath returns the base path for specstory files
func (c *OutputPathConfig) getBasePath() string {
	if c.BaseDir != "" {
		return c.BaseDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return SPECSTORY_DIR
	}
	return filepath.Join(cwd, SPECSTORY_DIR)
}

// GetHistoryDir returns the history directory path
func (c *OutputPathConfig) GetHistoryDir() string {
	basePath := c.getBasePath()
	if c.BaseDir != "" {
		return basePath
	}
	return filepath.Join(basePath, HISTORY_DIR)
}

// GetDebugDir returns the debug directory path
func (c *OutputPathConfig) GetDebugDir() string {
	return filepath.Join(c.getBasePath(), DEBUG_DIR)
}

// GetLogPath returns the debug log file path
func (c *OutputPathConfig) GetLogPath() string {
	return filepath.Join(c.GetDebugDir(), DEBUG_LOG_FILE)
}

// ValidationError represents errors from flag validation that should not display usage
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

// EnsureHistoryDirectoryExists creates the .specstory/history directory if it doesn't exist
func EnsureHistoryDirectoryExists(config *OutputPathConfig) error {
	historyPath := config.GetHistoryDir()
	if err := os.MkdirAll(historyPath, 0755); err != nil {
		return fmt.Errorf("error creating history directory: %v", err)
	}

	// Migration: Remove legacy history.json file from earlier versions
	// This file is no longer needed as we track sessions by file existence
	historyFile := filepath.Join(historyPath, ".history.json")
	if _, err := os.Stat(historyFile); err == nil {
		// File exists, try to remove it
		_ = os.Remove(historyFile) // Ignore errors, just move on
	}

	return nil
}

// SetupOutputConfig creates and configures the output configuration
func SetupOutputConfig(outputDir string) (*OutputPathConfig, error) {
	config, err := NewOutputPathConfig(outputDir)
	if err != nil {
		return nil, err
	}
	return config, nil
}

// GetAuthPath returns the path to the auth.json file
func GetAuthPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(homeDir, ".specstory", "cli", "auth.json"), nil
}
