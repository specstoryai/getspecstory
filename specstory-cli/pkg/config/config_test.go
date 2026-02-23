package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Helper to create a temporary config file with the given content
func createTempConfigFile(t *testing.T, dir, content string) string {
	t.Helper()
	configDir := filepath.Join(dir, SpecStoryDir, CLIDir)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}
	return configPath
}

// TestProcessTemplate tests the template processing for user/project levels
func TestProcessTemplate(t *testing.T) {
	template := `# Header
# {u This is user-level text.}
# {p This is project-level text.}
# Shared line
`

	t.Run("user level keeps {u} and strips {p}", func(t *testing.T) {
		result := processTemplate(template, "user")
		if !strings.Contains(result, "This is user-level text.") {
			t.Errorf("User template should contain user-level text, got:\n%s", result)
		}
		if strings.Contains(result, "This is project-level text.") {
			t.Errorf("User template should not contain project-level text, got:\n%s", result)
		}
		if !strings.Contains(result, "Shared line") {
			t.Errorf("User template should contain shared lines, got:\n%s", result)
		}
		// Should not contain raw markers
		if strings.Contains(result, "{u") || strings.Contains(result, "{p") {
			t.Errorf("Processed template should not contain raw markers, got:\n%s", result)
		}
	})

	t.Run("project level keeps {p} and strips {u}", func(t *testing.T) {
		result := processTemplate(template, "project")
		if strings.Contains(result, "This is user-level text.") {
			t.Errorf("Project template should not contain user-level text, got:\n%s", result)
		}
		if !strings.Contains(result, "This is project-level text.") {
			t.Errorf("Project template should contain project-level text, got:\n%s", result)
		}
		if !strings.Contains(result, "Shared line") {
			t.Errorf("Project template should contain shared lines, got:\n%s", result)
		}
	})

	t.Run("multi-line blocks", func(t *testing.T) {
		multiLine := `# {u Line one of user block.
# Line two of user block.}
# {p Line one of project block.
# Line two of project block.}
# Common line
`
		result := processTemplate(multiLine, "user")
		if !strings.Contains(result, "Line one of user block.") {
			t.Errorf("Should contain first line of user block, got:\n%s", result)
		}
		if !strings.Contains(result, "Line two of user block.") {
			t.Errorf("Should contain second line of user block, got:\n%s", result)
		}
		if strings.Contains(result, "Line one of project block.") {
			t.Errorf("Should not contain project block content, got:\n%s", result)
		}
		if !strings.Contains(result, "Common line") {
			t.Errorf("Should contain common line, got:\n%s", result)
		}
	})

	t.Run("defaultConfigTemplate produces valid output for user level", func(t *testing.T) {
		result := processTemplate(defaultConfigTemplate, "user")
		if strings.Contains(result, "{u") || strings.Contains(result, "{p") {
			t.Errorf("Processed default template should not contain raw markers, got:\n%s", result)
		}
		// Should still contain the shared content
		if !strings.Contains(result, "SpecStory CLI Configuration") {
			t.Errorf("Processed template should contain header, got:\n%s", result)
		}
	})
}

// TestLoadPrecedence tests that project config overrides user config
func TestLoadPrecedence(t *testing.T) {
	// Save and restore original working directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Save and restore original HOME
	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	tests := []struct {
		name           string
		userConfig     string
		projectConfig  string
		expectedOutDir string
	}{
		{
			name:           "project config overrides user config",
			userConfig:     "[local_sync]\noutput_dir = \"/user/path\"",
			projectConfig:  "[local_sync]\noutput_dir = \"/project/path\"",
			expectedOutDir: "/project/path",
		},
		{
			name:           "user config used when no project config",
			userConfig:     "[local_sync]\noutput_dir = \"/user/path\"",
			projectConfig:  "",
			expectedOutDir: "/user/path",
		},
		{
			name:           "project config used when no user config",
			userConfig:     "",
			projectConfig:  "[local_sync]\noutput_dir = \"/project/path\"",
			expectedOutDir: "/project/path",
		},
		{
			name:           "empty when no config files",
			userConfig:     "",
			projectConfig:  "",
			expectedOutDir: "",
		},
		{
			name: "project nested config overrides user nested config",
			userConfig: `
[cloud_sync]
enabled = true
`,
			projectConfig: `
[cloud_sync]
enabled = false
`,
			expectedOutDir: "", // We check cloud_sync separately below
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directories for HOME and project
			tempHome := t.TempDir()
			tempProject := t.TempDir()

			// Set HOME to temp directory
			if err := os.Setenv("HOME", tempHome); err != nil {
				t.Fatalf("Failed to set HOME: %v", err)
			}

			// Change to temp project directory
			if err := os.Chdir(tempProject); err != nil {
				t.Fatalf("Failed to chdir: %v", err)
			}

			// Create user config if provided
			if tt.userConfig != "" {
				createTempConfigFile(t, tempHome, tt.userConfig)
			}

			// Create project config if provided
			if tt.projectConfig != "" {
				createTempConfigFile(t, tempProject, tt.projectConfig)
			}

			// Load config
			cfg, err := Load(nil)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			// Check output_dir precedence
			if cfg.GetOutputDir() != tt.expectedOutDir {
				t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), tt.expectedOutDir)
			}

			// Special check for nested config test
			if tt.name == "project nested config overrides user nested config" {
				if cfg.IsCloudSyncEnabled() {
					t.Errorf("IsCloudSyncEnabled() = true, want false (project should override user)")
				}
			}
		})
	}
}

// TestConfigMerge tests that user and project configs are merged, not replaced.
// Non-overlapping settings from both levels should coexist, with project-level
// taking precedence where both define the same key.
func TestConfigMerge(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	t.Run("non-overlapping settings from both configs are preserved", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// User config sets output_dir and disables analytics
		createTempConfigFile(t, tempHome, `
[local_sync]
output_dir = "/user/output"

[analytics]
enabled = false
`)
		// Project config sets debug_dir and disables cloud sync — no overlap
		createTempConfigFile(t, tempProject, `
[logging]
debug_dir = "/project/debug"

[cloud_sync]
enabled = false
`)

		cfg, err := Load(nil)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		// User-level output_dir should survive (project didn't set it)
		if cfg.GetOutputDir() != "/user/output" {
			t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/user/output")
		}
		// User-level analytics=false should survive (project didn't set it)
		if cfg.IsAnalyticsEnabled() {
			t.Errorf("IsAnalyticsEnabled() = true, want false (from user config)")
		}
		// Project-level debug_dir should be present
		if cfg.GetDebugDir() != "/project/debug" {
			t.Errorf("GetDebugDir() = %q, want %q", cfg.GetDebugDir(), "/project/debug")
		}
		// Project-level cloud_sync=false should be present
		if cfg.IsCloudSyncEnabled() {
			t.Errorf("IsCloudSyncEnabled() = true, want false (from project config)")
		}
	})

	t.Run("project overrides user for same key, preserves rest", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// User sets output_dir and enables console logging
		createTempConfigFile(t, tempHome, `
[local_sync]
output_dir = "/user/output"

[logging]
console = true
`)
		// Project overrides output_dir but doesn't mention console
		createTempConfigFile(t, tempProject, `
[local_sync]
output_dir = "/project/output"
`)

		cfg, err := Load(nil)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		// output_dir should be the project value
		if cfg.GetOutputDir() != "/project/output" {
			t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/project/output")
		}
		// console should still be true from user config
		if !cfg.IsConsoleEnabled() {
			t.Errorf("IsConsoleEnabled() = false, want true (from user config)")
		}
	})

	t.Run("CLI flags override both user and project config", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// User sets output_dir
		createTempConfigFile(t, tempHome, `
[local_sync]
output_dir = "/user/output"
`)
		// Project also sets output_dir
		createTempConfigFile(t, tempProject, `
[local_sync]
output_dir = "/project/output"
`)

		// CLI flag overrides both
		cfg, err := Load(&CLIOverrides{OutputDir: "/cli/output"})
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		if cfg.GetOutputDir() != "/cli/output" {
			t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/cli/output")
		}
	})
}

// TestCLIOverrides tests that CLI flags override config file settings
func TestCLIOverrides(t *testing.T) {
	// Save and restore original working directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Save and restore original HOME
	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	tests := []struct {
		name       string
		configFile string
		overrides  *CLIOverrides
		checkFunc  func(t *testing.T, cfg *Config)
	}{
		{
			name:       "OutputDir override",
			configFile: "[local_sync]\noutput_dir = \"/config/path\"",
			overrides:  &CLIOverrides{OutputDir: "/cli/path"},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.GetOutputDir() != "/cli/path" {
					t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/cli/path")
				}
			},
		},
		{
			name: "NoVersionCheck override (--no-version-check)",
			configFile: `
[version_check]
enabled = true
`,
			overrides: &CLIOverrides{NoVersionCheck: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.IsVersionCheckEnabled() {
					t.Errorf("IsVersionCheckEnabled() = true, want false")
				}
			},
		},
		{
			name: "NoCloudSync override (--no-cloud-sync)",
			configFile: `
[cloud_sync]
enabled = true
`,
			overrides: &CLIOverrides{NoCloudSync: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.IsCloudSyncEnabled() {
					t.Errorf("IsCloudSyncEnabled() = true, want false")
				}
			},
		},
		{
			name: "OnlyCloudSync override (--only-cloud-sync)",
			configFile: `
[local_sync]
enabled = true
`,
			overrides: &CLIOverrides{OnlyCloudSync: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.IsLocalSyncEnabled() {
					t.Errorf("IsLocalSyncEnabled() = true, want false")
				}
			},
		},
		{
			name: "Console logging override (--console)",
			configFile: `
[logging]
console = false
`,
			overrides: &CLIOverrides{Console: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if !cfg.IsConsoleEnabled() {
					t.Errorf("IsConsoleEnabled() = false, want true")
				}
			},
		},
		{
			name: "Log file override (--log)",
			configFile: `
[logging]
log = false
`,
			overrides: &CLIOverrides{Log: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if !cfg.IsLogEnabled() {
					t.Errorf("IsLogEnabled() = false, want true")
				}
			},
		},
		{
			name: "Debug override (--debug)",
			configFile: `
[logging]
debug = false
`,
			overrides: &CLIOverrides{Debug: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if !cfg.IsDebugEnabled() {
					t.Errorf("IsDebugEnabled() = false, want true")
				}
			},
		},
		{
			name: "Silent override (--silent)",
			configFile: `
[logging]
silent = false
`,
			overrides: &CLIOverrides{Silent: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if !cfg.IsSilentEnabled() {
					t.Errorf("IsSilentEnabled() = false, want true")
				}
			},
		},
		{
			name: "NoAnalytics override (--no-usage-analytics)",
			configFile: `
[analytics]
enabled = true
`,
			overrides: &CLIOverrides{NoAnalytics: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.IsAnalyticsEnabled() {
					t.Errorf("IsAnalyticsEnabled() = true, want false")
				}
			},
		},
		{
			name: "DebugDir override (--debug-dir)",
			configFile: `
[logging]
debug_dir = "/config/debug"
`,
			overrides: &CLIOverrides{DebugDir: "/cli/debug"},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.GetDebugDir() != "/cli/debug" {
					t.Errorf("GetDebugDir() = %q, want %q", cfg.GetDebugDir(), "/cli/debug")
				}
			},
		},
		{
			name:       "LocalTimeZone override (--local-time-zone)",
			configFile: ``,
			overrides:  &CLIOverrides{LocalTimeZone: true},
			checkFunc: func(t *testing.T, cfg *Config) {
				if !cfg.IsLocalTimeZoneEnabled() {
					t.Errorf("IsLocalTimeZoneEnabled() = false, want true")
				}
			},
		},
		{
			name:       "Empty CLI override doesn't change config value",
			configFile: "[local_sync]\noutput_dir = \"/config/path\"",
			overrides:  &CLIOverrides{OutputDir: ""},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.GetOutputDir() != "/config/path" {
					t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/config/path")
				}
			},
		},
		{
			name:       "Nil CLI overrides doesn't panic",
			configFile: "[local_sync]\noutput_dir = \"/config/path\"",
			overrides:  nil,
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.GetOutputDir() != "/config/path" {
					t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/config/path")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directories
			tempHome := t.TempDir()
			tempProject := t.TempDir()

			// Set HOME to empty temp dir (no user config)
			if err := os.Setenv("HOME", tempHome); err != nil {
				t.Fatalf("Failed to set HOME: %v", err)
			}

			// Change to temp project directory
			if err := os.Chdir(tempProject); err != nil {
				t.Fatalf("Failed to chdir: %v", err)
			}

			// Create project config
			if tt.configFile != "" {
				createTempConfigFile(t, tempProject, tt.configFile)
			}

			// Load config with overrides
			cfg, err := Load(tt.overrides)
			if err != nil {
				t.Fatalf("Load() returned error: %v", err)
			}

			tt.checkFunc(t, cfg)
		})
	}
}

// TestParseErrorHandling tests that TOML parse errors are returned to the caller
func TestParseErrorHandling(t *testing.T) {
	// Save and restore original working directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Save and restore original HOME
	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	tests := []struct {
		name          string
		configContent string
		wantError     bool
		errorContains string
	}{
		{
			name:          "valid TOML parses successfully",
			configContent: "[local_sync]\noutput_dir = \"/valid/path\"",
			wantError:     false,
		},
		{
			name:          "invalid TOML returns error",
			configContent: `this is not valid toml [[[`,
			wantError:     true,
			errorContains: "failed to load project config",
		},
		{
			name:          "unclosed quote returns error",
			configContent: "[local_sync]\noutput_dir = \"/unclosed",
			wantError:     true,
			errorContains: "failed to load project config",
		},
		{
			name:          "invalid table syntax returns error",
			configContent: `[cloud_sync`,
			wantError:     true,
			errorContains: "failed to load project config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directories
			tempHome := t.TempDir()
			tempProject := t.TempDir()

			// Set HOME to empty temp dir (no user config)
			if err := os.Setenv("HOME", tempHome); err != nil {
				t.Fatalf("Failed to set HOME: %v", err)
			}

			// Change to temp project directory
			if err := os.Chdir(tempProject); err != nil {
				t.Fatalf("Failed to chdir: %v", err)
			}

			// Create project config with test content
			createTempConfigFile(t, tempProject, tt.configContent)

			// Load config
			_, err := Load(nil)

			if tt.wantError {
				if err == nil {
					t.Errorf("Load() returned no error, want error containing %q", tt.errorContains)
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Load() error = %q, want error containing %q", err.Error(), tt.errorContains)
				}
			} else {
				if err != nil {
					t.Errorf("Load() returned error: %v, want no error", err)
				}
			}
		})
	}
}

// TestMissingFileHandling tests that missing config files don't cause errors
func TestMissingFileHandling(t *testing.T) {
	// Save and restore original working directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Save and restore original HOME
	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	tests := []struct {
		name           string
		createUserDir  bool
		createProjDir  bool
		createUserConf bool
		createProjConf bool
		wantError      bool
	}{
		{
			name:           "no config files - no error",
			createUserDir:  false,
			createProjDir:  false,
			createUserConf: false,
			createProjConf: false,
			wantError:      false,
		},
		{
			name:           "only user config exists - no error",
			createUserDir:  true,
			createProjDir:  false,
			createUserConf: true,
			createProjConf: false,
			wantError:      false,
		},
		{
			name:           "only project config exists - no error",
			createUserDir:  false,
			createProjDir:  true,
			createUserConf: false,
			createProjConf: true,
			wantError:      false,
		},
		{
			name:           "both config files exist - no error",
			createUserDir:  true,
			createProjDir:  true,
			createUserConf: true,
			createProjConf: true,
			wantError:      false,
		},
		{
			name:           ".specstory dirs exist but no config files - no error",
			createUserDir:  true,
			createProjDir:  true,
			createUserConf: false,
			createProjConf: false,
			wantError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directories
			tempHome := t.TempDir()
			tempProject := t.TempDir()

			// Set HOME
			if err := os.Setenv("HOME", tempHome); err != nil {
				t.Fatalf("Failed to set HOME: %v", err)
			}

			// Change to temp project directory
			if err := os.Chdir(tempProject); err != nil {
				t.Fatalf("Failed to chdir: %v", err)
			}

			// Create directories if needed
			if tt.createUserDir {
				userConfigDir := filepath.Join(tempHome, SpecStoryDir, CLIDir)
				if err := os.MkdirAll(userConfigDir, 0755); err != nil {
					t.Fatalf("Failed to create user config dir: %v", err)
				}
				if tt.createUserConf {
					configPath := filepath.Join(userConfigDir, ConfigFileName)
					if err := os.WriteFile(configPath, []byte("[local_sync]\noutput_dir = \"/user\""), 0644); err != nil {
						t.Fatalf("Failed to create user config: %v", err)
					}
				}
			}

			if tt.createProjDir {
				projConfigDir := filepath.Join(tempProject, SpecStoryDir, CLIDir)
				if err := os.MkdirAll(projConfigDir, 0755); err != nil {
					t.Fatalf("Failed to create project config dir: %v", err)
				}
				if tt.createProjConf {
					configPath := filepath.Join(projConfigDir, ConfigFileName)
					if err := os.WriteFile(configPath, []byte("[local_sync]\noutput_dir = \"/project\""), 0644); err != nil {
						t.Fatalf("Failed to create project config: %v", err)
					}
				}
			}

			// Load config
			_, err := Load(nil)

			if tt.wantError && err == nil {
				t.Errorf("Load() returned no error, want error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("Load() returned error: %v, want no error", err)
			}
		})
	}
}

// TestDefaultValues tests that default values are returned when config is empty
func TestDefaultValues(t *testing.T) {
	// Save and restore original working directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Save and restore original HOME
	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	// Create temp directories with no config files
	tempHome := t.TempDir()
	tempProject := t.TempDir()

	if err := os.Setenv("HOME", tempHome); err != nil {
		t.Fatalf("Failed to set HOME: %v", err)
	}
	if err := os.Chdir(tempProject); err != nil {
		t.Fatalf("Failed to chdir: %v", err)
	}

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	// Test default values
	tests := []struct {
		name     string
		got      bool
		expected bool
	}{
		{"IsVersionCheckEnabled default", cfg.IsVersionCheckEnabled(), true},
		{"IsCloudSyncEnabled default", cfg.IsCloudSyncEnabled(), true},
		{"IsLocalSyncEnabled default", cfg.IsLocalSyncEnabled(), true},
		{"IsAnalyticsEnabled default", cfg.IsAnalyticsEnabled(), true},
		{"IsConsoleEnabled default", cfg.IsConsoleEnabled(), false},
		{"IsLogEnabled default", cfg.IsLogEnabled(), false},
		{"IsDebugEnabled default", cfg.IsDebugEnabled(), false},
		{"IsSilentEnabled default", cfg.IsSilentEnabled(), false},
		{"IsLocalTimeZoneEnabled default", cfg.IsLocalTimeZoneEnabled(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.expected)
			}
		})
	}

	// OutputDir should be empty string (no default path)
	if cfg.GetOutputDir() != "" {
		t.Errorf("GetOutputDir() = %q, want empty string", cfg.GetOutputDir())
	}

	// DebugDir should be empty string (no default path)
	if cfg.GetDebugDir() != "" {
		t.Errorf("GetDebugDir() = %q, want empty string", cfg.GetDebugDir())
	}
}

// TestEnsureDefaultUserConfig tests auto-creation of the default user config file
func TestEnsureDefaultUserConfig(t *testing.T) {
	// Save and restore original working directory
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// Save and restore original HOME
	origHome := os.Getenv("HOME")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	t.Run("creates file when none exists", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// Load config — no user config exists, should auto-create
		cfg, err := Load(nil)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		// Verify the file was created at the correct path
		expectedPath := filepath.Join(tempHome, SpecStoryDir, CLIDir, ConfigFileName)
		if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
			t.Fatalf("Default config file was not created at %s", expectedPath)
		}

		// Verify content matches the processed user-level template
		content, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("Failed to read created config: %v", err)
		}
		expected := processTemplate(defaultConfigTemplate, "user")
		if string(content) != expected {
			t.Errorf("Created config content does not match processed user template")
		}

		// Verify all config values are still defaults (everything is commented out)
		if cfg.GetOutputDir() != "" {
			t.Errorf("GetOutputDir() = %q, want empty", cfg.GetOutputDir())
		}
		if !cfg.IsVersionCheckEnabled() {
			t.Errorf("IsVersionCheckEnabled() = false, want true")
		}
		if !cfg.IsCloudSyncEnabled() {
			t.Errorf("IsCloudSyncEnabled() = false, want true")
		}
		if !cfg.IsLocalSyncEnabled() {
			t.Errorf("IsLocalSyncEnabled() = false, want true")
		}
		if !cfg.IsAnalyticsEnabled() {
			t.Errorf("IsAnalyticsEnabled() = false, want true")
		}
	})

	t.Run("does not overwrite existing file", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// Create an existing user config with custom settings
		existingContent := "[local_sync]\noutput_dir = \"/my/custom/path\""
		createTempConfigFile(t, tempHome, existingContent)

		// Load config — file exists, should NOT overwrite
		cfg, err := Load(nil)
		if err != nil {
			t.Fatalf("Load() returned error: %v", err)
		}

		// Verify existing config was preserved and loaded
		if cfg.GetOutputDir() != "/my/custom/path" {
			t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/my/custom/path")
		}

		// Verify file content is still the original
		configPath := filepath.Join(tempHome, SpecStoryDir, CLIDir, ConfigFileName)
		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}
		if string(content) != existingContent {
			t.Errorf("Config file was modified, got %q, want %q", string(content), existingContent)
		}
	})

	t.Run("handles unwritable directory gracefully", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// Make .specstory directory read-only so config creation fails
		specstoryDir := filepath.Join(tempHome, SpecStoryDir)
		if err := os.MkdirAll(specstoryDir, 0755); err != nil {
			t.Fatalf("Failed to create dir: %v", err)
		}
		if err := os.Chmod(specstoryDir, 0555); err != nil {
			t.Fatalf("Failed to chmod: %v", err)
		}
		// Restore permissions for cleanup
		defer func() { _ = os.Chmod(specstoryDir, 0755) }()

		// Load should succeed even though config creation fails
		_, err := Load(nil)
		if err != nil {
			t.Fatalf("Load() returned error: %v, want no error", err)
		}

		// Verify no config file was created
		configPath := filepath.Join(tempHome, SpecStoryDir, CLIDir, ConfigFileName)
		if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
			t.Errorf("Config file should not exist at %s", configPath)
		}
	})

	t.Run("created file is loadable on subsequent calls", func(t *testing.T) {
		tempHome := t.TempDir()
		tempProject := t.TempDir()

		if err := os.Setenv("HOME", tempHome); err != nil {
			t.Fatalf("Failed to set HOME: %v", err)
		}
		if err := os.Chdir(tempProject); err != nil {
			t.Fatalf("Failed to chdir: %v", err)
		}

		// First Load — creates the default config
		_, err := Load(nil)
		if err != nil {
			t.Fatalf("First Load() returned error: %v", err)
		}

		// Second Load — reads the created config
		cfg, err := Load(nil)
		if err != nil {
			t.Fatalf("Second Load() returned error: %v", err)
		}

		// Defaults should still hold
		if cfg.GetOutputDir() != "" {
			t.Errorf("GetOutputDir() = %q, want empty", cfg.GetOutputDir())
		}
		if !cfg.IsVersionCheckEnabled() {
			t.Errorf("IsVersionCheckEnabled() = false, want true")
		}
	})
}

// TestDefaultConfigTemplateParsesWhenUncommented verifies the default config
// template is valid TOML when all comment prefixes are removed. This guards
// against template syntax rot. We process the template first to strip markers.
func TestDefaultConfigTemplateParsesWhenUncommented(t *testing.T) {
	// Process template to strip {u ...} / {p ...} markers before uncommenting
	processed := processTemplate(defaultConfigTemplate, "user")

	var uncommented strings.Builder
	for line := range strings.SplitSeq(processed, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip blank lines
		if trimmed == "" {
			continue
		}
		// Strip leading "# " to inspect the content
		stripped := strings.TrimPrefix(trimmed, "# ")
		// Keep section headers like [logging] and key = value lines
		// A TOML key-value line starts with a word character before the =
		// Skip prose comment lines that happen to contain = (e.g. examples in parentheses)
		isSection := strings.HasPrefix(stripped, "[")
		isKeyValue := strings.Contains(stripped, " = ") && !strings.Contains(stripped, "(")
		if strings.HasPrefix(trimmed, "#") && !isSection && !isKeyValue {
			continue
		}
		uncommented.WriteString(stripped)
		uncommented.WriteString("\n")
	}

	var cfg Config
	if _, err := toml.Decode(uncommented.String(), &cfg); err != nil {
		t.Fatalf("Default config template is not valid TOML when uncommented:\n%s\nError: %v",
			uncommented.String(), err)
	}
}
