package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// This file is the single home for the session-processing plumbing shared by
// every command that saves markdown (run, sync, watch, resume, search). Each
// command previously registered its own copies of these flags and hand-rolled
// its own autosave callback, and the copies drifted: watch and resume ignored
// the config default for no-telemetry-prompts, and resume lacked
// no-redact-secrets entirely, silently skipping secret redaction. New
// processing options should be added here — registration, resolution, and the
// callback — so no command can be missed.

// SessionFlagDefaults carries the config-derived defaults for the shared
// session-processing flags. main resolves the config file once and passes the
// result here so every command registers the same flags with the same
// defaults.
type SessionFlagDefaults struct {
	LocalTimeZone      bool
	DebugDir           string
	NoTelemetryPrompts bool
	NoRedactSecrets    bool
}

// registerSessionProcessingFlags registers the flag set shared by watch,
// resume, and search. run and sync register the same flag names in main via
// BoolVar bindings to package globals that are also consumed outside command
// context; their names and defaults must stay in lockstep with this list.
//
// cloud-url binds to the shared pointer the root applies in PersistentPreRunE.
// The telemetry endpoint/service-name flags are inert here — they are consumed
// process-wide by main's startup arg scan — and registered only so cobra
// accepts them. debug-raw, provenance and cloud-url are hidden, matching
// run/sync.
func registerSessionProcessingFlags(cmd *cobra.Command, cloudURL *string, d SessionFlagDefaults) {
	cmd.Flags().String("output-dir", "", "custom output directory for markdown files (default: ./.specstory/history)")
	cmd.Flags().String("debug-dir", d.DebugDir, "custom output directory for debug data (default: ./.specstory/debug)")
	cmd.Flags().Bool("only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
	cmd.Flags().Bool("no-cloud-sync", false, "disable cloud sync functionality")
	cmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = cmd.Flags().MarkHidden("debug-raw")
	cmd.Flags().Bool("local-time-zone", d.LocalTimeZone, "use local timezone for file name and content timestamps (when not present: UTC)")
	cmd.Flags().Bool("provenance", false, "enable AI provenance tracking (correlate file changes to agent activity)")
	_ = cmd.Flags().MarkHidden("provenance")
	cmd.Flags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = cmd.Flags().MarkHidden("cloud-url")
	cmd.Flags().Bool("no-telemetry-prompts", d.NoTelemetryPrompts, "exclude prompt text from telemetry spans, if telemetry is enabled")
	cmd.Flags().Bool("no-redact-secrets", d.NoRedactSecrets, "disable redaction of API keys and tokens from saved markdown history and cloud-synced session data")
	cmd.Flags().Bool("no-stats", false, "skip statistics entirely, do not read or write statistics.json")
	cmd.Flags().String("telemetry-endpoint", "", "Open Telemetry Protocol (OTLP) gRPC collector endpoint (default is off, e.g., localhost:4317)")
	cmd.Flags().String("telemetry-service-name", "", "override the default service name for telemetry, if telemetry is enabled")
}

// ResolveProcessingOptions reads the shared session-processing flags into a
// session.ProcessingOptions. isAutosave and showOutput are per-command
// decisions, not flags. Works for both flag registration styles: plain
// registrations here and main's BoolVar bindings both answer GetBool.
//
// A flag missing from a command resolves to its zero value, so the negative
// no-* flags fail toward their feature staying ON — a command that forgets to
// register one cannot silently disable redaction or telemetry privacy.
func ResolveProcessingOptions(cmd *cobra.Command, isAutosave, showOutput bool) session.ProcessingOptions {
	onlyCloudSync, _ := cmd.Flags().GetBool("only-cloud-sync")
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")
	useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
	noTelemetryPrompts, _ := cmd.Flags().GetBool("no-telemetry-prompts")
	noRedactSecrets, _ := cmd.Flags().GetBool("no-redact-secrets")
	noStats, _ := cmd.Flags().GetBool("no-stats")

	return session.ProcessingOptions{
		ShowOutput:         showOutput,
		OnlyCloudSync:      onlyCloudSync,
		IsAutosave:         isAutosave,
		DebugRaw:           debugRaw,
		UseUTC:             !useLocalTimezone,
		NoTelemetryPrompts: noTelemetryPrompts,
		RedactSecrets:      !noRedactSecrets,
		NoStats:            noStats,
	}
}

// AutosaveDeps carries the per-command dependencies of the shared autosave
// callback.
type AutosaveDeps struct {
	Ctx        context.Context
	Config     utils.OutputConfig
	Processing session.ProcessingOptions
	LiveIndex  *LiveIndexer
	Provenance *provenance.Engine

	// OnSaved, when set, runs after each successful save with whether the
	// markdown file existed before the save and the generated markdown size.
	// watch uses it for its per-update console/JSON output.
	OnSaved func(providerID string, sess *spi.AgentChatSession, fileExisted bool, markdownSize int)
}

// NewAutosaveCallback returns the per-update session handler shared by run,
// watch, and resume/search: process the session (markdown write + cloud
// sync), mirror it into the live restore index, and feed the provenance
// engine. Errors are logged but never propagated — autosave must not
// interrupt the user's coding session, and failed writes can be retried later
// via the sync command. On error the index, provenance, and OnSaved steps are
// skipped: a failed save must not be indexed as if it happened.
//
// The callback takes providerID per invocation because watch monitors many
// providers through one callback; run and resume wrap it with their fixed
// provider ID.
func NewAutosaveCallback(d AutosaveDeps) func(providerID string, sess *spi.AgentChatSession) {
	return func(providerID string, sess *spi.AgentChatSession) {
		if sess == nil {
			return
		}

		// Capture pre-save file existence for OnSaved (created vs updated)
		// before the save makes the file exist.
		fileExisted := false
		if d.OnSaved != nil {
			_, statErr := os.Stat(session.BuildSessionFilePath(sess, d.Config.GetHistoryDir(), d.Processing.UseUTC))
			fileExisted = statErr == nil
		}

		markdownSize, err := session.ProcessSingleSession(d.Ctx, sess, d.Config, d.Processing)
		if err != nil {
			slog.Error("Failed to process session update",
				"sessionId", sess.SessionID,
				"provider", providerID,
				"error", err)
			return
		}

		// Mirror the markdown write into the restore index in real time.
		d.LiveIndex.Record(providerID, sess)

		// Push agent events to provenance engine for correlation.
		provenance.ProcessEvents(d.Ctx, d.Provenance, sess)

		if d.OnSaved != nil {
			d.OnSaved(providerID, sess, fileExisted, markdownSize)
		}
	}
}
