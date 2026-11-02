package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/internal/testutil"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	sessionpkg "github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

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

type bulkSyncTestProvider struct {
	spi.Provider
	sessions []spi.AgentChatSession
}

func (p bulkSyncTestProvider) Name() string { return "Claude Code" }
func (p bulkSyncTestProvider) GetAgentChatSessions(string, bool, spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	return p.sessions, nil
}

func TestSyncProviderRotatesMarkdown(t *testing.T) {
	testutil.SetHome(t, t.TempDir())
	projectDir := t.TempDir()
	t.Chdir(projectDir)
	oldOnlyStats, oldOnlyCloud, oldNoCloud, oldNoStats, oldSilent := onlyStats, onlyCloudSync, noCloudSync, noStats, silent
	t.Cleanup(func() {
		onlyStats, onlyCloudSync, noCloudSync, noStats, silent = oldOnlyStats, oldOnlyCloud, oldNoCloud, oldNoStats, oldSilent
		cloud.InitSyncManager(false)
	})
	onlyStats, onlyCloudSync, noCloudSync, noStats, silent = false, false, true, true, true
	cloud.InitSyncManager(false)

	exchanges := make([]schema.Exchange, 4)
	for i := range exchanges {
		timestamp := "2026-01-02T03:04:0" + string(rune('0'+i)) + "Z"
		exchanges[i] = schema.Exchange{StartTime: timestamp, Messages: []schema.Message{{
			Role: schema.RoleUser, Timestamp: timestamp,
			Content: []schema.ContentPart{{Type: schema.ContentTypeText, Text: "bulk sync prompt"}},
		}}}
	}
	provider := bulkSyncTestProvider{sessions: []spi.AgentChatSession{{
		SessionID: "bulk-rotation",
		CreatedAt: "2026-01-02T03:04:00Z",
		Slug:      "bulk-rotation",
		SessionData: &schema.SessionData{
			SessionID: "bulk-rotation", CreatedAt: "2026-01-02T03:04:00Z",
			Provider: schema.ProviderInfo{Name: "Claude Code"}, Exchanges: exchanges,
		},
	}}}
	outputConfig, err := utils.SetupOutputConfig(filepath.Join(projectDir, "history"), "")
	if err != nil {
		t.Fatal(err)
	}
	collector := sessionpkg.NewStatisticsCollector(filepath.Join(projectDir, "statistics.json"))
	if _, err := syncProvider(provider, "claude", outputConfig, false, true, collector, 300); err != nil {
		t.Fatalf("syncProvider: %v", err)
	}

	basePath := sessionpkg.BuildSessionFilePath(&provider.sessions[0], outputConfig.GetHistoryDir(), true)
	secondPath := strings.TrimSuffix(basePath, ".md") + ".part-2.md"
	firstPart, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("read first part: %v", err)
	}
	secondPart, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("bulk sync did not rotate at the configured cap: %v", err)
	}
	if !strings.Contains(string(firstPart), "[Continued in part 2]") || !strings.HasPrefix(string(secondPart), "[Continued from part 1]") {
		t.Errorf("bulk sync parts are not linked\nfirst:\n%s\nsecond:\n%s", firstPart, secondPart)
	}
}
