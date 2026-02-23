// Package config provides configuration management for the SpecStory CLI.
// Configuration is loaded with the following priority (highest to lowest):
//  1. CLI flags
//  2. Local project config: .specstory/cli/config.toml
//  3. User-level config: ~/.specstory/cli/config.toml
//
// Note: For telemetry settings, environment variables (OTEL_*) take highest priority
// per OpenTelemetry conventions.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// ConfigFileName is the name of the configuration file
	ConfigFileName = "config.toml"
	// SpecStoryDir is the directory name for SpecStory files
	SpecStoryDir = ".specstory"
	// CLIDir is the subdirectory for CLI-specific files (config, auth, etc.)
	CLIDir = "cli"
)

// defaultConfigTemplate is the content written to a newly created config file.
// All options are commented out so the file is self-documenting but inert.
const defaultConfigTemplate = `# SpecStory CLI Configuration
#
# {u This is the user-level config file for SpecStory CLI.
# All settings here apply to every project unless overridden
# by a project-level config at ./.specstory/cli/config.toml
# or overridden by CLI flags.}
# {p This is the project-level config file for SpecStory CLI.
# All settings here apply to this project unless overridden by CLI flags.}
#
# Uncomment (remove the #) the line and edit any setting below to change the default behavior.
# For more information, see: https://docs.specstory.com/integrations/terminal-coding-agents/usage

[local_sync]
# Write markdown files locally. (default: true)
# enabled = false # equivalent to --only-cloud-sync

# Custom output directory for markdown files.
# Default: ./.specstory/history (relative to the project directory)
# output_dir = "~/.specstory/history" # equivalent to --output-dir "~/.specstory/history"

# Use local timezone for file name and content timestamps (default: false, UTC)
# local_time_zone = true # equivalent to --local-time-zone

[cloud_sync]
# Sync session data to SpecStory Cloud. (default: true, when logged in to SpecStory Cloud)
# enabled = false # equivalent to --no-cloud-sync

[logging]
# Custom output directory for debug data.
# Default: ./.specstory/debug (relative to the project directory)
# debug_dir = "~/.specstory/debug" # equivalent to --debug-dir "~/.specstory/debug"

# Error/warn/info output to stdout (default: false)
# console = true # equivalent to --console

# Write logs to .specstory/debug/debug.log (default: false)
# log = true # equivalent to --log        

# Debug-level output, requires console or log (default: false)
# debug = true # equivalent to --debug 

# Suppress all non-error output (default: false)
# silent = true	# equivalent to --silence

[version_check]
# Check for new versions of the CLI on startup.
# Default: true
# enabled = false # equivalent to --no-version-check

[analytics]
# Send anonymous product usage analytics to help improve SpecStory.
# Default: true
# enabled = false # equivalent to --no-usage-analytics

[providers]
# Agent execution commands by provider (used by specstory run)
# Pass custom flags (e.g. claude_cmd = "claude --allow-dangerously-skip-permissions")
# Use of these is equivalent to -c "custom command"

# Claude Code command
# claude_cmd = "claude"

# Codex CLI command
# codex_cmd = "codex"

# Cursor CLI command
# cursor_cmd = "cursor-agent"

# Droid CLI command
# droid_cmd = "droid"

# Gemini CLI command
# gemini_cmd = "gemini"
`

// Config represents the complete CLI configuration
type Config struct {
	VersionCheck VersionCheckConfig `toml:"version_check"`
	CloudSync    CloudSyncConfig    `toml:"cloud_sync"`
	LocalSync    LocalSyncConfig    `toml:"local_sync"`
	Logging      LoggingConfig      `toml:"logging"`
	Analytics    AnalyticsConfig    `toml:"analytics"`
	Providers    ProvidersConfig    `toml:"providers"`
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
	// OutputDir is the custom output directory for markdown files
	OutputDir string `toml:"output_dir"`
	// LocalTimeZone enables local timezone for file name and content timestamps
	LocalTimeZone *bool `toml:"local_time_zone"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	// DebugDir is the custom output directory for debug data
	DebugDir string `toml:"debug_dir"`
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

// ProvidersConfig holds custom agent execution commands by provider.
// These are used by `specstory run` as the equivalent of the -c flag,
// scoped to a specific provider.
type ProvidersConfig struct {
	ClaudeCmd string `toml:"claude_cmd"`
	CodexCmd  string `toml:"codex_cmd"`
	CursorCmd string `toml:"cursor_cmd"`
	DroidCmd  string `toml:"droid_cmd"`
	GeminiCmd string `toml:"gemini_cmd"`
}

// CLIOverrides holds CLI flag values that override config file settings.
// These are applied after config files are loaded.
type CLIOverrides struct {
	// General
	OutputDir     string
	LocalTimeZone bool

	// Version check
	NoVersionCheck bool

	// Cloud sync
	NoCloudSync   bool
	OnlyCloudSync bool

	// Logging
	DebugDir string
	Console  bool
	Log      bool
	Debug    bool
	Silent   bool

	// Analytics
	NoAnalytics bool
}

// Load reads configuration from files and CLI flags.
// Priority: CLI flags > local project config > user-level config
//
// Returns an error if a config file exists but cannot be parsed (TOML syntax error)
// or cannot be read (permission denied, I/O error). Missing config files are not
// treated as errors - they are simply skipped.
func Load(cliOverrides *CLIOverrides) (*Config, error) {
	cfg := &Config{}

	// Load user-level config first (lowest priority)
	userConfigPath := getUserConfigPath()
	if userConfigPath != "" {
		if err := loadTOMLFile(userConfigPath, cfg); err != nil {
			if os.IsNotExist(err) {
				slog.Debug("No user-level config file found", "path", userConfigPath)
				ensureDefaultUserConfig(userConfigPath)
			} else {
				// Parse error or permission denied - return error to caller
				return cfg, fmt.Errorf("failed to load user config %s: %w", userConfigPath, err)
			}
		} else {
			slog.Debug("Loaded user-level config", "path", userConfigPath)
		}
	}

	// Load local project config (overwrites user-level)
	localConfigPath := getLocalConfigPath()
	if localConfigPath != "" {
		if err := loadTOMLFile(localConfigPath, cfg); err != nil {
			if os.IsNotExist(err) {
				slog.Debug("No local project config file found", "path", localConfigPath)
			} else {
				// Parse error or permission denied - return error to caller
				return cfg, fmt.Errorf("failed to load project config %s: %w", localConfigPath, err)
			}
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

// getUserConfigPath returns the path to ~/.specstory/cli/config.toml
func getUserConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Debug("Could not determine home directory", "error", err)
		return ""
	}
	return filepath.Join(home, SpecStoryDir, CLIDir, ConfigFileName)
}

// getLocalConfigPath returns the path to .specstory/cli/config.toml in the current directory
func getLocalConfigPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		slog.Debug("Could not determine current directory", "error", err)
		return ""
	}
	return filepath.Join(cwd, SpecStoryDir, CLIDir, ConfigFileName)
}

// processTemplate processes the default config template for a given level
// ("user" or "project"). Template markers {u ...} and {p ...} delimit content
// specific to user-level or project-level config files respectively.
//
// For level "user": content inside {u ...} is kept (markers stripped),
// content inside {p ...} is removed entirely.
// For level "project": the opposite applies.
//
// Markers can span multiple lines. The opening marker ({u or {p) appears at
// the start of a line (after optional whitespace/comment prefix), and the
// closing } appears at the end of a line.
func processTemplate(template, level string) string {
	var result strings.Builder
	keepTag := "{u"
	stripTag := "{p"
	if level == "project" {
		keepTag = "{p"
		stripTag = "{u"
	}

	insideKeep := false
	insideStrip := false

	for line := range strings.SplitSeq(template, "\n") {
		trimmed := strings.TrimSpace(line)
		// Remove any leading "# " to inspect the marker
		bare := strings.TrimPrefix(trimmed, "# ")

		switch {
		case insideStrip:
			// Check for closing brace — ends the stripped block
			if strings.HasSuffix(bare, "}") {
				insideStrip = false
			}
			// Drop the entire line regardless
			continue

		case insideKeep:
			// Check for closing brace — ends the kept block
			if strings.HasSuffix(bare, "}") {
				insideKeep = false
				// Strip the trailing } from the line
				idx := strings.LastIndex(line, "}")
				line = line[:idx]
				// Only emit if something remains after stripping
				if strings.TrimSpace(line) == "" || strings.TrimSpace(line) == "#" {
					continue
				}
			}
			result.WriteString(line)
			result.WriteString("\n")

		case strings.HasPrefix(bare, keepTag+" "):
			insideKeep = true
			// prefix is everything before the bare marker (e.g. "# ")
			prefix := line[:strings.Index(line, bare)]
			// Check if single-line block (closing } on same line)
			if strings.HasSuffix(bare, "}") {
				insideKeep = false
				// Extract content between tag and closing brace
				content := strings.TrimSpace(bare[len(keepTag)+1 : len(bare)-1])
				reconstructed := prefix + content
				if strings.TrimSpace(reconstructed) == "" || strings.TrimSpace(reconstructed) == "#" {
					continue
				}
				result.WriteString(reconstructed)
				result.WriteString("\n")
			} else {
				// Multi-line: strip the marker prefix, keep content after it
				content := strings.TrimSpace(bare[len(keepTag)+1:])
				reconstructed := prefix + content
				if strings.TrimSpace(reconstructed) == "" || strings.TrimSpace(reconstructed) == "#" {
					continue
				}
				result.WriteString(reconstructed)
				result.WriteString("\n")
			}

		case strings.HasPrefix(bare, stripTag+" "):
			insideStrip = true
			// Check if single-line block (closing } on same line)
			if strings.HasSuffix(bare, "}") {
				insideStrip = false
			}
			continue

		default:
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	// Remove trailing extra newline that accumulates from the split
	out := result.String()
	if strings.HasSuffix(out, "\n\n") && !strings.HasSuffix(template, "\n\n") {
		out = out[:len(out)-1]
	}
	return out
}

// ensureDefaultUserConfig creates a default user-level config file with all
// options commented out. This makes the config discoverable without changing
// any behavior. Failures are silently ignored — this is a convenience, not
// a requirement.
func ensureDefaultUserConfig(path string) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Debug("Could not create config directory", "path", dir, "error", err)
		return
	}
	processed := processTemplate(defaultConfigTemplate, "user")
	if err := os.WriteFile(path, []byte(processed), 0644); err != nil {
		slog.Debug("Could not write default config file", "path", path, "error", err)
		return
	}
	slog.Debug("Created default user config file", "path", path)
}

// EnsureDefaultProjectConfig creates a default project-level config file at
// .specstory/cli/config.toml if one doesn't already exist. All options are
// commented out so the file is self-documenting but inert.
//
// This should only be called from commands that imply active project work
// (run, sync, watch) to avoid scattering config files in arbitrary directories.
// Failures are silently ignored — this is a convenience, not a requirement.
func EnsureDefaultProjectConfig() {
	path := getLocalConfigPath()
	if path == "" {
		return
	}
	// Don't overwrite an existing file
	if _, err := os.Stat(path); err == nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Debug("Could not create config directory", "path", dir, "error", err)
		return
	}
	processed := processTemplate(defaultConfigTemplate, "project")
	if err := os.WriteFile(path, []byte(processed), 0644); err != nil {
		slog.Debug("Could not write default config file", "path", path, "error", err)
		return
	}
	slog.Debug("Created default project config file", "path", path)
}

// loadTOMLFile reads a TOML file and decodes it into the config struct.
// Fields are merged (later calls overwrite earlier values).
func loadTOMLFile(path string, cfg *Config) error {
	_, err := toml.DecodeFile(path, cfg)
	return err
}

// applyCLIOverrides applies CLI flag overrides to the config.
func applyCLIOverrides(cfg *Config, o *CLIOverrides) {
	// Local sync
	if o.OutputDir != "" {
		cfg.LocalSync.OutputDir = o.OutputDir
	}
	if o.LocalTimeZone {
		enabled := true
		cfg.LocalSync.LocalTimeZone = &enabled
	}
	if o.DebugDir != "" {
		cfg.Logging.DebugDir = o.DebugDir
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
	return c.LocalSync.OutputDir
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

// GetDebugDir returns the custom debug directory, or empty string to use default.
func (c *Config) GetDebugDir() string {
	return c.Logging.DebugDir
}

// IsLocalTimeZoneEnabled returns whether local timezone is enabled.
// Defaults to false if not explicitly set (UTC is default).
func (c *Config) IsLocalTimeZoneEnabled() bool {
	if c.LocalSync.LocalTimeZone != nil {
		return *c.LocalSync.LocalTimeZone
	}
	return false
}

// GetProviderCmd returns the custom execution command for a provider, or empty
// string if none is configured. The providerID should match a registered
// provider ID (e.g., "claude", "codex", "cursor", "droid", "gemini").
func (c *Config) GetProviderCmd(providerID string) string {
	switch strings.ToLower(providerID) {
	case "claude":
		return c.Providers.ClaudeCmd
	case "codex":
		return c.Providers.CodexCmd
	case "cursor":
		return c.Providers.CursorCmd
	case "droid":
		return c.Providers.DroidCmd
	case "gemini":
		return c.Providers.GeminiCmd
	default:
		return ""
	}
}
