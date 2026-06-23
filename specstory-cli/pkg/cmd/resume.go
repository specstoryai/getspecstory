package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// Best-effort UI writers. The destination is the user's terminal, so write
// errors are not actionable and are intentionally ignored.
func fprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
func fprintln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func fprint(w io.Writer, a ...any)                 { _, _ = fmt.Fprint(w, a...) }

// resumePlan captures the user's choices from the interactive selection.
type resumePlan struct {
	from      spi.Provider
	fromID    string
	sessionID string
	to        spi.Provider
	toID      string
}

// agentChoice pairs a registry ID with its provider.
type agentChoice struct {
	id       string
	provider spi.Provider
}

// CreateResumeCommand builds the `specstory resume` command. It opens the interactive
// picker (a Bubble Tea TUI over the sessions.db index — see docs/RESUME-TUI.md): browse
// the current project's sessions across all agents, pick one, choose a target agent, then
// reconstruct (cross-agent) or native-resume (same agent) and launch with auto-save.
func CreateResumeCommand(cloudURL *string, localTimeZone bool, debugDir string) *cobra.Command {
	longDesc := `Resume a past coding-agent session — in the same agent, or a different one.

'resume' opens an interactive picker of the sessions in the current project across all
agents. Pick one, choose which installed agent to continue it in, and go. Resuming into a
different agent reconstructs the conversation into that agent's native format first.

Pass an agent to pre-select the resume target, e.g. 'specstory resume codex'.`

	resumeCmd := &cobra.Command{
		Use:   "resume [agent]",
		Short: "Resume a past session, optionally in a different agent",
		Long:  longDesc,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			config.EnsureDefaultProjectConfig()
			slog.Info("Running in resume mode")

			registry := factory.GetRegistry()

			// Optional positional arg pre-selects the target agent (e.g. `resume codex`).
			presetTarget := ""
			if len(args) == 1 {
				presetTarget = strings.ToLower(strings.TrimSpace(args[0]))
			}

			// Read flags (subset mirroring run/watch).
			debugRaw, _ := cmd.Flags().GetBool("debug-raw")
			useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
			useUTC := !useLocalTimezone
			outputDir, _ := cmd.Flags().GetString("output-dir")
			flagDebugDir, _ := cmd.Flags().GetString("debug-dir")
			noCloudSync, _ := cmd.Flags().GetBool("no-cloud-sync")
			onlyCloudSync, _ := cmd.Flags().GetBool("only-cloud-sync")
			provenanceEnabled, _ := cmd.Flags().GetBool("provenance")
			noTelemetryPrompts, _ := cmd.Flags().GetBool("no-telemetry-prompts")

			if flagDebugDir != "" {
				spi.SetDebugBaseDir(flagDebugDir)
			}

			cwd, err := os.Getwd()
			if err != nil {
				slog.Error("Failed to get current working directory", "error", err)
				return err
			}

			// Open (building if needed) the session index and run the interactive picker.
			store, err := openOrBuildResumeIndex()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			projectID, projectName, idErr := utils.ComputeProjectID(cwd)
			if idErr != nil || projectID == "" {
				projectID, projectName = unknownProjectID, filepath.Base(cwd)
			}

			plan, err := selectResumeViaTUI(registry, store, projectID, projectName, presetTarget)
			if err != nil {
				return err
			}
			if plan == nil {
				// User quit or nothing to resume; the picker already explained why.
				return nil
			}

			return launchResume(plan, cwd, resumeLaunchOpts{
				outputDir:          outputDir,
				flagDebugDir:       flagDebugDir,
				debugRaw:           debugRaw,
				useUTC:             useUTC,
				noCloudSync:        noCloudSync,
				onlyCloudSync:      onlyCloudSync,
				provenanceEnabled:  provenanceEnabled,
				noTelemetryPrompts: noTelemetryPrompts,
			})
		},
	}

	resumeCmd.Flags().String("output-dir", "", "custom output directory for markdown files (default: ./.specstory/history)")
	resumeCmd.Flags().String("debug-dir", debugDir, "custom output directory for debug data (default: ./.specstory/debug)")
	resumeCmd.Flags().Bool("only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
	resumeCmd.Flags().Bool("no-cloud-sync", false, "disable cloud sync functionality")
	resumeCmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = resumeCmd.Flags().MarkHidden("debug-raw")
	resumeCmd.Flags().Bool("local-time-zone", localTimeZone, "use local timezone for file name and content timestamps (when not present: UTC)")
	resumeCmd.Flags().Bool("provenance", false, "enable AI provenance tracking (correlate file changes to agent activity)")
	_ = resumeCmd.Flags().MarkHidden("provenance")
	resumeCmd.Flags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = resumeCmd.Flags().MarkHidden("cloud-url")
	resumeCmd.Flags().Bool("no-telemetry-prompts", false, "exclude prompt text from telemetry spans, if telemetry is enabled")

	return resumeCmd
}

// resumeLaunchOpts carries the resume launch configuration shared by `resume` and
// `search` (whose `r` action resumes a found session through the same path).
type resumeLaunchOpts struct {
	outputDir          string
	flagDebugDir       string
	debugRaw           bool
	useUTC             bool
	noCloudSync        bool
	onlyCloudSync      bool
	provenanceEnabled  bool
	noTelemetryPrompts bool
}

// launchResume reconstructs (cross-agent) or natively resumes the planned session and
// runs the agent with auto-save + provenance — the shared tail of `resume` and `search`.
func launchResume(plan *resumePlan, cwd string, o resumeLaunchOpts) error {
	// Setup output configuration and project identity (needed for auto-save + cloud).
	outConfig, err := utils.SetupOutputConfig(o.outputDir, o.flagDebugDir)
	if err != nil {
		return err
	}
	if err := utils.EnsureHistoryDirectoryExists(outConfig); err != nil {
		return err
	}
	if _, err := utils.NewProjectIdentityManager(cwd).EnsureProjectIdentity(); err != nil {
		slog.Error("Failed to ensure project identity", "error", err)
	}

	CheckAndWarnAuthentication(o.noCloudSync)
	if o.onlyCloudSync && !cloud.IsAuthenticated() {
		return utils.ValidationError{Message: "--only-cloud-sync requires authentication. Please run 'specstory login' first"}
	}

	analytics.SetAgentProviders([]string{plan.to.Name()})
	analytics.TrackEvent(analytics.EventResumeActivated, analytics.Properties{
		"from_provider": plan.fromID,
		"to_provider":   plan.toID,
		"cross_agent":   plan.fromID != plan.toID,
	})

	resumeSessionID, err := prepareResumeTarget(plan, cwd, os.Stdout)
	if err != nil {
		return err
	}

	// Context for graceful cancellation (Ctrl+C).
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Provenance infrastructure (mirrors run/watch).
	provenanceEngine, provenanceCleanup, err := provenance.StartEngine(o.provenanceEnabled)
	if err != nil {
		return err
	}
	defer provenanceCleanup()
	fsCleanup, err := provenance.StartFSWatcher(ctx, provenanceEngine, cwd)
	if err != nil {
		return err
	}
	defer fsCleanup()

	// Auto-save callback: identical behavior to run/watch.
	sessionCallback := func(sess *spi.AgentChatSession) {
		if sess == nil {
			return
		}
		_, perr := session.ProcessSingleSession(context.Background(), sess, outConfig, session.ProcessingOptions{
			OnlyCloudSync:      o.onlyCloudSync,
			IsAutosave:         true,
			DebugRaw:           o.debugRaw,
			UseUTC:             o.useUTC,
			NoTelemetryPrompts: o.noTelemetryPrompts,
		})
		if perr != nil {
			slog.Error("Failed to process session update", "sessionId", sess.SessionID, "error", perr)
		}
		provenance.ProcessEvents(ctx, provenanceEngine, sess)
	}

	slog.Info("Launching resume", "provider", plan.to.Name(), "resumeSessionID", resumeSessionID)
	if err := plan.to.ExecAgentAndWatch(cwd, "", resumeSessionID, o.debugRaw, sessionCallback); err != nil {
		slog.Error("Agent resume failed", "provider", plan.to.Name(), "error", err)
		return err
	}
	return nil
}

// prepareResumeTarget produces the native session ID to hand to ExecAgentAndWatch.
// Same-agent resume reuses the existing session; cross-agent reconstructs the
// source SessionData into the target's native store and returns the fresh ID.
func prepareResumeTarget(plan *resumePlan, cwd string, out io.Writer) (string, error) {
	// Same agent: native resume on the existing session, no transform.
	if plan.fromID == plan.toID {
		fprintf(out, "\nResuming %s session %s in place...\n", plan.from.Name(), shortID(plan.sessionID))
		return plan.sessionID, nil
	}

	// Cross-agent: load source SessionData, reconstruct, write into target store.
	fromSession, err := plan.from.GetAgentChatSession(cwd, plan.sessionID, false)
	if err != nil {
		return "", fmt.Errorf("failed to load source session: %w", err)
	}
	if fromSession == nil || fromSession.SessionData == nil {
		return "", fmt.Errorf("source session %s has no data to reconstruct", plan.sessionID)
	}

	note := fmt.Sprintf("Resumed from a %s session via SpecStory.", plan.from.Name())
	rec, err := plan.to.ReconstructSession(fromSession.SessionData, spi.ReconstructOptions{
		WorkspaceRoot: cwd,
		MigrationNote: note,
	})
	if err != nil {
		if errors.Is(err, spi.ErrReconstructionUnsupported) {
			return "", utils.ValidationError{Message: fmt.Sprintf(
				"%s can't yet be a cross-agent resume target. Choose Claude Code or Codex CLI (or resume in %s itself).",
				plan.to.Name(), plan.from.Name())}
		}
		return "", fmt.Errorf("failed to reconstruct session for %s: %w", plan.to.Name(), err)
	}

	path, err := plan.to.NativeSessionPath(cwd, rec.Filename)
	if err != nil {
		return "", fmt.Errorf("failed to resolve target session path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("failed to create target session directory: %w", err)
	}
	if err := os.WriteFile(path, rec.Content, 0o644); err != nil {
		return "", fmt.Errorf("failed to write reconstructed session: %w", err)
	}

	slog.Info("resume: wrote reconstructed session", "path", path, "newID", rec.SessionID)
	fprintf(out, "\nReconstructed %s session into %s as %s.\n", plan.from.Name(), plan.to.Name(), shortID(rec.SessionID))
	return rec.SessionID, nil
}

// shortID abbreviates a session ID for display.
func shortID(id string) string {
	if len(id) <= 13 {
		return id
	}
	return id[:5] + "..." + id[len(id)-5:]
}
