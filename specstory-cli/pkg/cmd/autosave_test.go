package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every config-derived default reaches these commands through
// SessionFlagDefaults, so a flag registered with a hardcoded default silently
// ignores config.toml no matter what the user put there. That is exactly how
// output_dir came to be honored by run and sync but not by watch: markdown
// written during a watch landed in the project directory while the configured
// directory held a stale copy.
func TestSessionProcessingFlagDefaults(t *testing.T) {
	defaults := SessionFlagDefaults{
		LocalTimeZone:      true,
		OutputDir:          "/config/markdown",
		DebugDir:           "/config/debug",
		NoTelemetryPrompts: true,
		NoRedactSecrets:    true,
	}

	var cloudURL string
	commands := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"watch", CreateWatchCommand(&cloudURL, defaults)},
		{"resume", CreateResumeCommand(&cloudURL, defaults)},
		{"search", CreateSearchCommand(&cloudURL, defaults)},
	}

	// Every flag the defaults struct feeds, and the value it must carry.
	expected := map[string]string{
		"output-dir":           defaults.OutputDir,
		"debug-dir":            defaults.DebugDir,
		"local-time-zone":      "true",
		"no-telemetry-prompts": "true",
		"no-redact-secrets":    "true",
	}

	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			for flagName, want := range expected {
				flag := command.cmd.Flags().Lookup(flagName)
				if flag == nil {
					t.Errorf("%s does not register --%s", command.name, flagName)
					continue
				}
				if flag.DefValue != want {
					t.Errorf("%s --%s default = %q, want %q",
						command.name, flagName, flag.DefValue, want)
				}
			}
		})
	}
}

// An explicit flag still has to beat the configured default, matching the
// precedence run and sync have always had.
func TestSessionProcessingFlags_ExplicitFlagBeatsConfiguredDefault(t *testing.T) {
	var cloudURL string
	watchCmd := CreateWatchCommand(&cloudURL, SessionFlagDefaults{OutputDir: "/config/markdown"})

	if err := watchCmd.Flags().Set("output-dir", "/explicit/markdown"); err != nil {
		t.Fatalf("failed to set output-dir: %v", err)
	}

	got, err := watchCmd.Flags().GetString("output-dir")
	if err != nil {
		t.Fatalf("failed to read output-dir: %v", err)
	}
	if got != "/explicit/markdown" {
		t.Errorf("output-dir = %q, want the explicitly set value", got)
	}
}
