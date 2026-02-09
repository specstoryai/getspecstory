// Package config provides configuration management for the SpecStory CLI.
// Configuration is loaded with the following priority (highest to lowest):
//  1. CLI flags
//  2. Local project config: .specstory/cli-config.toml
//  3. User-level config: ~/.specstory/cli-config.toml
//
// Note: For telemetry settings, environment variables (OTEL_*) take highest priority
// per OpenTelemetry conventions.
package config

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	// ConfigFileName is the name of the configuration file
	ConfigFileName = "cli-config.toml"
	// SpecStoryDir is the directory name for SpecStory files
	SpecStoryDir = ".specstory"
)

// Config represents the complete CLI configuration
type Config struct {
	// OutputDir is the custom output directory for markdown and debug files
	OutputDir string `toml:"output_dir"`

	VersionCheck VersionCheckConfig `toml:"version_check"`
	CloudSync    CloudSyncConfig    `toml:"cloud_sync"`
	LocalSync    LocalSyncConfig    `toml:"local_sync"`
	Logging      LoggingConfig      `toml:"logging"`
	Analytics    AnalyticsConfig    `toml:"analytics"`
}

// VersionCheckConfig holds version check settings
type VersionCheckConfig struct {
	// Enabled controls whether to check for newer versions on startup
	Enabled *bool `toml:"enabled"`
}

// CloudSyncConfig holds cloud sync settings
type CloudSyncConfig struct {
	// Enabled controls whether cloud sync is active
	Enabled *bool `toml:"enabled"`
}

// LocalSyncConfig holds local file sync settings
type LocalSyncConfig struct {
	// Enabled controls whether local markdown files are written
	// When false, only cloud sync is performed (equivalent to --only-cloud-sync)
	Enabled *bool `toml:"enabled"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	// Console enables error/warn/info output to stdout
	Console *bool `toml:"console"`
	// Log enables writing error/warn/info output to .specstory/debug/debug.log
	Log *bool `toml:"log"`
	// Debug enables debug-level output (requires Console or Log)
	Debug *bool `toml:"debug"`
	// Silent suppresses all non-error output
	Silent *bool `toml:"silent"`
}

// AnalyticsConfig holds analytics settings
type AnalyticsConfig struct {
	// Enabled controls whether usage analytics are sent
	Enabled *bool `toml:"enabled"`
}

// CLIOverrides holds CLI flag values that override config file settings.
// These are applied after config files are loaded.
type CLIOverrides struct {
	// General
	OutputDir string

	// Version check
	NoVersionCheck bool

	// Cloud sync
	NoCloudSync   bool
	OnlyCloudSync bool

	// Logging
	Console bool
	Log     bool
	Debug   bool
	Silent  bool

	// Analytics
	NoAnalytics bool
}

// Load reads configuration from files and CLI flags.
// Priority: CLI flags > local project config > user-level config
func Load(cliOverrides *CLIOverrides) (*Config, error) {
	cfg := &Config{}

	// Load user-level config first (lowest priority)
	userConfigPath := getUserConfigPath()
	if userConfigPath != "" {
		if err := loadTOMLFile(userConfigPath, cfg); err != nil {
			slog.Debug("No user-level config loaded", "path", userConfigPath, "error", err)
		} else {
			slog.Debug("Loaded user-level config", "path", userConfigPath)
		}
	}

	// Load local project config (overwrites user-level)
	localConfigPath := getLocalConfigPath()
	if localConfigPath != "" {
		if err := loadTOMLFile(localConfigPath, cfg); err != nil {
			slog.Debug("No local project config loaded", "path", localConfigPath, "error", err)
		} else {
			slog.Debug("Loaded local project config", "path", localConfigPath)
		}
	}

	// Apply CLI flag overrides (highest priority for most settings)
	if cliOverrides != nil {
		applyCLIOverrides(cfg, cliOverrides)
	}

	return cfg, nil
}

// getUserConfigPath returns the path to ~/.specstory/cli-config.toml
func getUserConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Debug("Could not determine home directory", "error", err)
		return ""
	}
	return filepath.Join(home, SpecStoryDir, ConfigFileName)
}

// getLocalConfigPath returns the path to .specstory/cli-config.toml in the current directory
func getLocalConfigPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		slog.Debug("Could not determine current directory", "error", err)
		return ""
	}
	return filepath.Join(cwd, SpecStoryDir, ConfigFileName)
}

// loadTOMLFile reads a TOML file and decodes it into the config struct.
// Fields are merged (later calls overwrite earlier values).
func loadTOMLFile(path string, cfg *Config) error {
	_, err := toml.DecodeFile(path, cfg)
	return err
}

// applyCLIOverrides applies CLI flag overrides to the config.
func applyCLIOverrides(cfg *Config, o *CLIOverrides) {
	// General
	if o.OutputDir != "" {
		cfg.OutputDir = o.OutputDir
	}

	// Version check (--no-version-check sets enabled to false)
	if o.NoVersionCheck {
		disabled := false
		cfg.VersionCheck.Enabled = &disabled
	}

	// Cloud sync (--no-cloud-sync sets enabled to false)
	if o.NoCloudSync {
		disabled := false
		cfg.CloudSync.Enabled = &disabled
	}

	// Local sync (--only-cloud-sync sets enabled to false)
	if o.OnlyCloudSync {
		disabled := false
		cfg.LocalSync.Enabled = &disabled
	}

	// Logging flags only override if explicitly set (true)
	if o.Console {
		enabled := true
		cfg.Logging.Console = &enabled
	}
	if o.Log {
		enabled := true
		cfg.Logging.Log = &enabled
	}
	if o.Debug {
		enabled := true
		cfg.Logging.Debug = &enabled
	}
	if o.Silent {
		enabled := true
		cfg.Logging.Silent = &enabled
	}

	// Analytics (--no-usage-analytics sets enabled to false)
	if o.NoAnalytics {
		disabled := false
		cfg.Analytics.Enabled = &disabled
	}
}

// --- Getter methods ---

// GetOutputDir returns the output directory, or empty string to use default.
func (c *Config) GetOutputDir() string {
	return c.OutputDir
}

// IsVersionCheckEnabled returns whether version check is enabled.
// Defaults to true if not explicitly set.
func (c *Config) IsVersionCheckEnabled() bool {
	if c.VersionCheck.Enabled != nil {
		return *c.VersionCheck.Enabled
	}
	return true // default enabled
}

// IsCloudSyncEnabled returns whether cloud sync is enabled.
// Defaults to true if not explicitly set.
func (c *Config) IsCloudSyncEnabled() bool {
	if c.CloudSync.Enabled != nil {
		return *c.CloudSync.Enabled
	}
	return true // default enabled
}

// IsLocalSyncEnabled returns whether local sync is enabled.
// Defaults to true if not explicitly set.
// When false, only cloud sync is performed (equivalent to --only-cloud-sync).
func (c *Config) IsLocalSyncEnabled() bool {
	if c.LocalSync.Enabled != nil {
		return *c.LocalSync.Enabled
	}
	return true // default enabled
}

// IsConsoleEnabled returns whether console logging is enabled.
// Defaults to false if not explicitly set.
func (c *Config) IsConsoleEnabled() bool {
	if c.Logging.Console != nil {
		return *c.Logging.Console
	}
	return false // default disabled
}

// IsLogEnabled returns whether file logging is enabled.
// Defaults to false if not explicitly set.
func (c *Config) IsLogEnabled() bool {
	if c.Logging.Log != nil {
		return *c.Logging.Log
	}
	return false // default disabled
}

// IsDebugEnabled returns whether debug logging is enabled.
// Defaults to false if not explicitly set.
func (c *Config) IsDebugEnabled() bool {
	if c.Logging.Debug != nil {
		return *c.Logging.Debug
	}
	return false // default disabled
}

// IsSilentEnabled returns whether silent mode is enabled.
// Defaults to false if not explicitly set.
func (c *Config) IsSilentEnabled() bool {
	if c.Logging.Silent != nil {
		return *c.Logging.Silent
	}
	return false // default disabled
}

// IsAnalyticsEnabled returns whether analytics are enabled.
// Defaults to true if not explicitly set.
func (c *Config) IsAnalyticsEnabled() bool {
	if c.Analytics.Enabled != nil {
		return *c.Analytics.Enabled
	}
	return true // default enabled
}
