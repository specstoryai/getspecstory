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
	GetSpecstoryDir() string
}

// OutputPathConfig manages all output directory configuration
type OutputPathConfig struct {
	BaseDir      string // Validated absolute path for --output-dir (history/*.md only)
	SpecstoryDir string // Validated absolute path for --specstory-dir (.project.json + history/)
}

// Ensure OutputPathConfig implements OutputConfig interface
var _ OutputConfig = (*OutputPathConfig)(nil)

// resolveDir converts dir to an absolute path, creates it if needed, and verifies it is writable.
// Returns the resolved absolute path or an error.
func resolveDir(dir string) (string, error) {
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return "", ValidationError{Message: fmt.Sprintf("invalid directory path: %v", err)}
	}

	info, err := os.Stat(absPath)
	if err == nil {
		if !info.IsDir() {
			return "", ValidationError{Message: fmt.Sprintf("path exists but is not a directory: %s", absPath)}
		}
		// Verify write permission
		if file, err := os.CreateTemp(absPath, ".specstory_write_test_*"); err != nil {
			return "", ValidationError{Message: fmt.Sprintf("directory is not writable: %s", absPath)}
		} else {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
		slog.Debug("Using existing directory", "path", absPath)
	} else if os.IsNotExist(err) {
		slog.Info("Creating directory", "path", absPath)
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return "", ValidationError{Message: fmt.Sprintf("failed to create directory: %v", err)}
		}
		slog.Info("Created directory", "path", absPath)
	} else {
		return "", ValidationError{Message: fmt.Sprintf("error checking directory: %v", err)}
	}

	return absPath, nil
}

// NewOutputPathConfig creates and validates an output configuration
func NewOutputPathConfig(dir string) (*OutputPathConfig, error) {
	if dir == "" {
		return &OutputPathConfig{}, nil // Use defaults
	}

	absPath, err := resolveDir(dir)
	if err != nil {
		return nil, err
	}

	return &OutputPathConfig{BaseDir: absPath}, nil
}

// getBasePath returns the .specstory-equivalent base directory.
// When --specstory-dir is set it uses that; otherwise falls back to {cwd}/.specstory.
// Note: --output-dir (BaseDir) does NOT affect this — it only overrides the history path.
func (c *OutputPathConfig) getBasePath() string {
	if c.SpecstoryDir != "" {
		return c.SpecstoryDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		return SPECSTORY_DIR
	}
	return filepath.Join(cwd, SPECSTORY_DIR)
}

// GetHistoryDir returns the history directory path.
// --output-dir takes precedence; otherwise history lives inside the specstory base dir.
func (c *OutputPathConfig) GetHistoryDir() string {
	if c.BaseDir != "" {
		return c.BaseDir
	}
	return filepath.Join(c.getBasePath(), HISTORY_DIR)
}

// GetDebugDir returns the debug directory path
func (c *OutputPathConfig) GetDebugDir() string {
	return filepath.Join(c.getBasePath(), DEBUG_DIR)
}

// GetSpecstoryDir returns the .specstory base directory path.
// This is where .project.json, statistics.json, and other non-history files live.
func (c *OutputPathConfig) GetSpecstoryDir() string {
	return c.getBasePath()
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

// EnsureHistoryDirectoryExists creates the .specstory/history directory if it doesn't exist.
// This should be called before writing markdown files to handle cases where the directory
// is deleted during a long-running watch or run command.
func EnsureHistoryDirectoryExists(config OutputConfig) error {
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

// SetupOutputConfig creates and configures the output configuration.
// outputDir (--output-dir) overrides where history/*.md files are written.
// specstoryDir (--specstory-dir) sets the base directory for all .specstory outputs
// (.project.json, history/, debug/). outputDir takes precedence over specstoryDir for history.
func SetupOutputConfig(outputDir, specstoryDir string) (*OutputPathConfig, error) {
	config, err := NewOutputPathConfig(outputDir)
	if err != nil {
		return nil, err
	}

	if specstoryDir != "" {
		absPath, err := resolveDir(specstoryDir)
		if err != nil {
			return nil, ValidationError{Message: fmt.Sprintf("invalid --specstory-dir: %v", err)}
		}
		config.SpecstoryDir = absPath
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
