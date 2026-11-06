package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// A session must be findable from where the index says it lives. Terminal agents
// run in, and record, a real directory; IDE-backed providers are looked up by the
// workspace the user named, even when that is a file nothing could run in.
func TestLiveIndexOrigin(t *testing.T) {
	launchDir := spi.CanonicalizePathOrClean(t.TempDir())
	t.Chdir(launchDir)
	project := t.TempDir()
	workspaceFile := filepath.Join(t.TempDir(), "team.code-workspace")
	if err := os.WriteFile(workspaceFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, providerID, projectPath, want string
	}{
		{"terminal agent in a local project", "copilot", project, project},
		{"terminal agent with a workspace file", "claude", workspaceFile, launchDir},
		{"terminal agent with a remote path", "copilot", "/no/such/remote/project", launchDir},
		{"Copilot IDE with a workspace file", "copilotide", workspaceFile, workspaceFile},
		{"Copilot IDE variant with a workspace file", "copilotide-insiders", workspaceFile, workspaceFile},
		{"Cursor IDE with a workspace file", "cursoride", workspaceFile, workspaceFile},
		{"hosted agent with a remote path", "cursorcloud", "/no/such/remote/project", "/no/such/remote/project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveIndexOrigin(tt.providerID, tt.projectPath); got != tt.want {
				t.Errorf("liveIndexOrigin(%q, %q) = %q, want %q", tt.providerID, tt.projectPath, got, tt.want)
			}
		})
	}
}

func TestValidateFlags_CloudSyncMutualExclusion(t *testing.T) {
	// Save original global flag values
	origOnlyCloudSync := onlyCloudSync
	origNoCloudSync := noCloudSync
	origConsole := console
	origSilent := silent
	origDebug := debug
	origLogFile := logFile

	// Restore original values after test
	defer func() {
		onlyCloudSync = origOnlyCloudSync
		noCloudSync = origNoCloudSync
		console = origConsole
		silent = origSilent
		debug = origDebug
		logFile = origLogFile
	}()

	tests := []struct {
		name          string
		onlyCloudSync bool
		noCloudSync   bool
		expectError   bool
	}{
		{
			name:          "both flags set - mutually exclusive error",
			onlyCloudSync: true,
			noCloudSync:   true,
			expectError:   true,
		},
		{
			name:          "only-cloud-sync alone - valid",
			onlyCloudSync: true,
			noCloudSync:   false,
			expectError:   false,
		},
		{
			name:          "no-cloud-sync alone - valid",
			onlyCloudSync: false,
			noCloudSync:   true,
			expectError:   false,
		},
		{
			name:          "neither flag set - valid",
			onlyCloudSync: false,
			noCloudSync:   false,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set flags to known valid state for other validations
			console = false
			silent = false
			debug = false
			logFile = false

			// Set the flags under test
			onlyCloudSync = tt.onlyCloudSync
			noCloudSync = tt.noCloudSync

			err := validateFlags()

			if tt.expectError && err == nil {
				t.Errorf("validateFlags() expected error for onlyCloudSync=%v, noCloudSync=%v, got nil",
					tt.onlyCloudSync, tt.noCloudSync)
			}
			if !tt.expectError && err != nil {
				t.Errorf("validateFlags() unexpected error for onlyCloudSync=%v, noCloudSync=%v: %v",
					tt.onlyCloudSync, tt.noCloudSync, err)
			}
		})
	}
}
