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
			userConfig:     `output_dir = "/user/path"`,
			projectConfig:  `output_dir = "/project/path"`,
			expectedOutDir: "/project/path",
		},
		{
			name:           "user config used when no project config",
			userConfig:     `output_dir = "/user/path"`,
			projectConfig:  "",
			expectedOutDir: "/user/path",
		},
		{
			name:           "project config used when no user config",
			userConfig:     "",
			projectConfig:  `output_dir = "/project/path"`,
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
			configFile: `output_dir = "/config/path"`,
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
			name:       "Empty CLI override doesn't change config value",
			configFile: `output_dir = "/config/path"`,
			overrides:  &CLIOverrides{OutputDir: ""},
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.GetOutputDir() != "/config/path" {
					t.Errorf("GetOutputDir() = %q, want %q", cfg.GetOutputDir(), "/config/path")
				}
			},
		},
		{
			name:       "Nil CLI overrides doesn't panic",
			configFile: `output_dir = "/config/path"`,
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
			configContent: `output_dir = "/valid/path"`,
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
			configContent: `output_dir = "/unclosed`,
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
					if err := os.WriteFile(configPath, []byte(`output_dir = "/user"`), 0644); err != nil {
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
					if err := os.WriteFile(configPath, []byte(`output_dir = "/project"`), 0644); err != nil {
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

		// Verify content matches the template
		content, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("Failed to read created config: %v", err)
		}
		if string(content) != defaultConfigTemplate {
			t.Errorf("Created config content does not match template")
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
		existingContent := `output_dir = "/my/custom/path"`
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
// against template syntax rot.
func TestDefaultConfigTemplateParsesWhenUncommented(t *testing.T) {
	var uncommented strings.Builder
	for line := range strings.SplitSeq(defaultConfigTemplate, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip blank lines and pure prose comment lines (no "=" or "[")
		if trimmed == "" || (strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed, "=") && !strings.Contains(trimmed, "[")) {
			continue
		}
		// Strip leading "# " from commented-out TOML lines
		trimmed = strings.TrimPrefix(trimmed, "# ")
		uncommented.WriteString(trimmed)
		uncommented.WriteString("\n")
	}

	var cfg Config
	if _, err := toml.Decode(uncommented.String(), &cfg); err != nil {
		t.Fatalf("Default config template is not valid TOML when uncommented:\n%s\nError: %v",
			uncommented.String(), err)
	}
}
