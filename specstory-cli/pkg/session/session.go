package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/telemetry"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// ValidateSessionData runs schema validation on SessionData when in debug mode.
// Validation is only performed when debugRaw is true to avoid overhead in normal operation.
// Returns true if validation passed or was skipped, false if validation failed.
func ValidateSessionData(session *spi.AgentChatSession, debugRaw bool) bool {
	if !debugRaw || session.SessionData == nil {
		return true
	}
	if !session.SessionData.Validate() {
		slog.Warn("SessionData failed schema validation, proceeding anyway",
			"sessionId", session.SessionID)
		return false
	}
	return true
}

// WriteDebugSessionData writes debug session data when debugRaw is enabled.
// Logs warnings on failure but does not fail the operation.
func WriteDebugSessionData(session *spi.AgentChatSession, debugRaw bool) {
	if !debugRaw || session.SessionData == nil {
		return
	}
	if err := spi.WriteDebugSessionData(session.SessionID, session.SessionData); err != nil {
		slog.Warn("Failed to write debug session data", "sessionId", session.SessionID, "error", err)
	}
}

// FormatFilenameTimestamp formats a timestamp for use in filenames.
// The format is filesystem-safe and matches the markdown title format.
func FormatFilenameTimestamp(t time.Time, useUTC bool) string {
	if useUTC {
		// Use UTC with Z suffix: "2006-01-02_15-04-05Z"
		return t.UTC().Format("2006-01-02_15-04-05") + "Z"
	}
	// Use local timezone with offset: "2006-01-02_15-04-05-0700"
	return t.Local().Format("2006-01-02_15-04-05-0700")
}

// BuildSessionFilePath constructs the full markdown file path for a session.
// All callers need consistent filename generation from session metadata.
func BuildSessionFilePath(session *spi.AgentChatSession, historyDir string, useUTC bool) string {
	timestamp, _ := time.Parse(time.RFC3339, session.CreatedAt)
	timestampStr := FormatFilenameTimestamp(timestamp, useUTC)

	filename := timestampStr
	if session.Slug != "" {
		filename = fmt.Sprintf("%s-%s", timestampStr, session.Slug)
	}
	return filepath.Join(historyDir, filename+".md")
}

// ProcessingOptions holds flags that control session processing behavior.
type ProcessingOptions struct {
	OnlyCloudSync      bool // Skip local markdown writes and only sync to cloud
	OnlyStats          bool // Only update statistics, skip local markdown and cloud sync
	ShowOutput         bool // Print progress to stdout
	IsAutosave         bool // Called from run/watch (true) vs sync command (false)
	DebugRaw           bool // Enable schema validation (only in debug mode to avoid overhead)
	UseUTC             bool // Timestamp format: true=UTC, false=local
	NoTelemetryPrompts bool // Exclude user prompt text from telemetry spans for privacy
}

// GetSpecStoryDir determines the .specstory directory path from config.
// When using the default layout, history lives at .specstory/history so
// we go up one level to reach .specstory itself. For custom output dirs
// the history dir IS the base.
func GetSpecStoryDir(config utils.OutputConfig) string {
	historyDir := config.GetHistoryDir()
	if filepath.Base(historyDir) == "history" {
		return filepath.Dir(historyDir)
	}
	return historyDir
}

// ProcessSingleSession writes markdown and triggers cloud sync for a single session.
// ctx is used for OTel trace/span propagation (no-op when telemetry is disabled).
// Returns the size of the markdown content in bytes.
func ProcessSingleSession(ctx context.Context, session *spi.AgentChatSession, config utils.OutputConfig, opts ProcessingOptions) (int, error) {
	if session == nil || session.SessionData == nil {
		return 0, fmt.Errorf("session or session data is nil")
	}

	// Track processing start time for metrics
	processingStart := time.Now()

	// Create a context with a deterministic trace ID from the session ID.
	// This groups all spans for the same session into a single trace, even
	// across multiple invocations in autosave mode.
	ctx = telemetry.ContextWithSessionTrace(ctx, session.SessionID)

	// Start an OTel span for this session processing (no-op when telemetry is disabled)
	ctx, span := telemetry.Tracer("specstory").Start(ctx, "process_session")
	defer span.End()

	// Compute session statistics
	agentName := session.SessionData.Provider.Name
	stats := telemetry.ComputeSessionStats(agentName, session)

	// Record metrics (no-op when telemetry is disabled)
	defer func() {
		telemetry.RecordSessionMetrics(ctx, stats, time.Since(processingStart))
	}()

	// Set session span attributes
	telemetry.SetSessionSpanAttributes(span, stats)

	// Create child spans for each exchange
	if len(session.SessionData.Exchanges) > 0 {
		telemetry.ProcessExchangeSpans(ctx, stats, session.SessionData.Exchanges, opts.NoTelemetryPrompts)
	}

	ValidateSessionData(session, opts.DebugRaw)
	WriteDebugSessionData(session, opts.DebugRaw)

	// Generate markdown from SessionData
	markdownContent, err := GenerateMarkdownFromAgentSession(session.SessionData, false, opts.UseUTC)
	if err != nil {
		slog.Error("Failed to generate markdown from SessionData", "sessionId", session.SessionID, "error", err)
		return 0, fmt.Errorf("failed to generate markdown: %w", err)
	}

	// Calculate markdown size in bytes
	markdownSize := len(markdownContent)

	// Collect statistics (always enabled, even in only-stats mode)
	sessionStats := ComputeSessionStatistics(session.SessionData, markdownContent, agentName)
	specstoryDir := GetSpecStoryDir(config)
	collector := NewStatisticsCollector(specstoryDir)
	collector.AddSessionStats(session.SessionID, sessionStats)

	// In only-stats mode, skip file writes and cloud sync entirely
	if opts.OnlyStats {
		if opts.ShowOutput && !log.IsSilent() {
			fmt.Printf("Processing session %s... statistics collected\n", session.SessionID)
			fmt.Println()
		}
		return markdownSize, nil
	}

	// Generate filename from timestamp and slug
	fileFullPath := BuildSessionFilePath(session, config.GetHistoryDir(), opts.UseUTC)

	if opts.ShowOutput && !log.IsSilent() {
		fmt.Printf("Processing session %s...", session.SessionID)
	}

	// Check if file already exists with same content
	var outcome string
	identicalContent := false
	fileExists := false
	if existingContent, err := os.ReadFile(fileFullPath); err == nil {
		fileExists = true
		if string(existingContent) == markdownContent {
			identicalContent = true
			slog.Info("Markdown file already exists with same content, skipping write",
				"sessionId", session.SessionID,
				"path", fileFullPath)
		}
	}

	// Write file if needed (skip if only-cloud-sync is enabled)
	if !opts.OnlyCloudSync {
		if !identicalContent {
			// Ensure history directory exists (handles deletion during long-running watch/run)
			if err := utils.EnsureHistoryDirectoryExists(config); err != nil {
				return 0, fmt.Errorf("failed to ensure history directory: %w", err)
			}
			err := os.WriteFile(fileFullPath, []byte(markdownContent), 0644)
			if err != nil {
				// Track write error
				if opts.IsAutosave {
					analytics.TrackEvent(analytics.EventAutosaveError, analytics.Properties{
						"session_id":      session.SessionID,
						"error":           err.Error(),
						"only_cloud_sync": opts.OnlyCloudSync,
					})
				} else {
					analytics.TrackEvent(analytics.EventSyncMarkdownError, analytics.Properties{
						"session_id":      session.SessionID,
						"error":           err.Error(),
						"only_cloud_sync": opts.OnlyCloudSync,
					})
				}
				return 0, fmt.Errorf("error writing markdown file: %w", err)
			}

			// Track successful write
			if opts.IsAutosave {
				if !fileExists {
					// New file created during autosave
					analytics.TrackEvent(analytics.EventAutosaveNew, analytics.Properties{
						"session_id":      session.SessionID,
						"only_cloud_sync": opts.OnlyCloudSync,
					})
				} else {
					// File updated during autosave
					analytics.TrackEvent(analytics.EventAutosaveSuccess, analytics.Properties{
						"session_id":      session.SessionID,
						"only_cloud_sync": opts.OnlyCloudSync,
					})
				}
			} else {
				if !fileExists {
					// New file created during manual sync
					analytics.TrackEvent(analytics.EventSyncMarkdownNew, analytics.Properties{
						"session_id":      session.SessionID,
						"only_cloud_sync": opts.OnlyCloudSync,
					})
				} else {
					// File updated during manual sync
					analytics.TrackEvent(analytics.EventSyncMarkdownSuccess, analytics.Properties{
						"session_id":      session.SessionID,
						"only_cloud_sync": opts.OnlyCloudSync,
					})
				}
			}

			slog.Info("Successfully wrote file",
				"sessionId", session.SessionID,
				"path", fileFullPath)
		}

		// Determine outcome for user feedback
		if identicalContent {
			outcome = "up to date (skipped)"
		} else if fileExists {
			outcome = "updated"
		} else {
			outcome = "created"
		}
	} else {
		// Only cloud sync mode - no local file operations
		outcome = "synced to cloud only"
		slog.Info("Skipping local file write (only-cloud-sync mode)",
			"sessionId", session.SessionID)
	}

	// Trigger cloud sync with provider-specific data
	// In only-cloud-sync mode: always sync (no file to check for identical content)
	// In normal mode: skip sync only if identical content AND in autosave mode
	if opts.OnlyCloudSync || !identicalContent || !opts.IsAutosave {
		cloud.SyncSessionToCloud(session.SessionID, fileFullPath, markdownContent, []byte(session.RawData), session.SessionData.Provider.Name, opts.IsAutosave)
	}

	if opts.ShowOutput && !log.IsSilent() {
		fmt.Printf(" %s\n", outcome)
		fmt.Println() // Visual separation
	}

	return markdownSize, nil
}
