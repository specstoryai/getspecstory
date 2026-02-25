package utils

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	BaseDir      string // Validated absolute path for markdown output
	DebugBaseDir string // Validated absolute path for debug output
}

// Ensure OutputPathConfig implements OutputConfig interface
var _ OutputConfig = (*OutputPathConfig)(nil)

// ExpandTilde expands a leading ~ to the user's home directory.
// Go's filepath.Abs does not handle ~ — it treats it as a literal directory name.
func ExpandTilde(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

// validateDirectory validates a directory path: expands ~, converts to absolute,
// checks existence and write permissions, or creates it if missing.
// Returns the validated absolute path.
func validateDirectory(dir, label string) (string, error) {
	// Expand ~ to home directory before converting to absolute
	dir = ExpandTilde(dir)

	// Convert to absolute path if relative
	absPath, err := filepath.Abs(dir)
	if err != nil {
		return "", ValidationError{Message: fmt.Sprintf("invalid %s path: %v", label, err)}
	}

	// Check if path exists
	info, err := os.Stat(absPath)
	if err == nil {
		// Path exists, check if it's a directory
		if !info.IsDir() {
			return "", ValidationError{Message: fmt.Sprintf("%s exists but is not a directory: %s", label, absPath)}
		}
		// Check write permissions by attempting to create a temp file
		if file, err := os.CreateTemp(absPath, ".specstory_write_test_*"); err != nil {
			return "", ValidationError{Message: fmt.Sprintf("%s is not writable: %s", label, absPath)}
		} else {
			// Clean up test file
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
		slog.Debug("Using existing directory", "label", label, "path", absPath)
	} else if os.IsNotExist(err) {
		// Path doesn't exist, try to create it
		slog.Debug("Creating directory", "label", label, "path", absPath)
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return "", ValidationError{Message: fmt.Sprintf("failed to create %s: %v", label, err)}
		}
		slog.Debug("Created directory", "label", label, "path", absPath)
	} else {
		// Some other error occurred
		return "", ValidationError{Message: fmt.Sprintf("error checking %s: %v", label, err)}
	}

	return absPath, nil
}

// NewOutputPathConfig creates and validates an output configuration.
// dir is the markdown output directory; debugDir is the debug output directory.
// Either or both may be empty to use defaults.
func NewOutputPathConfig(dir string, debugDir string) (*OutputPathConfig, error) {
	config := &OutputPathConfig{}

	if dir != "" {
		absPath, err := validateDirectory(dir, "output directory")
		if err != nil {
			return nil, err
		}
		config.BaseDir = absPath
	}

	if debugDir != "" {
		absPath, err := validateDirectory(debugDir, "debug directory")
		if err != nil {
			return nil, err
		}
		config.DebugBaseDir = absPath
	}

	return config, nil
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

// GetDebugDir returns the debug directory path.
// If DebugBaseDir is set, it is used directly (no /debug appended).
func (c *OutputPathConfig) GetDebugDir() string {
	if c.DebugBaseDir != "" {
		return c.DebugBaseDir
	}
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
// outputDir is the markdown output directory; debugDir is the debug output directory.
func SetupOutputConfig(outputDir string, debugDir string) (*OutputPathConfig, error) {
	config, err := NewOutputPathConfig(outputDir, debugDir)
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
