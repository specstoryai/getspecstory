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
	"time"

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
	// fromCwd is the directory the source session was originally launched from
	// (the index's origin_cwd). It can differ from the user's current cwd when a
	// session is picked from another project via the all-projects browser. The
	// source data load must use this, because some providers (e.g. Codex) locate a
	// session by matching its recorded cwd — looking under the current cwd would
	// find nothing. Empty for older index rows; callers fall back to the current cwd.
	fromCwd string
	to      spi.Provider
	toID    string
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

Pass an agent to set the resume target up front, e.g. 'specstory resume codex' — the picker
then skips the target-selection step and resumes straight into that agent. The agent must be
a known, installed provider, or the command errors.`

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
			// An unknown agent name should fail the run rather than be silently ignored, so
			// validate it against the registry up front. When set and valid, the picker skips
			// the target-selection step entirely (see selectResumeViaTUI / beginResume); whether
			// the named agent is actually installed is checked there, where the installed set
			// is known.
			presetTarget := ""
			if len(args) == 1 {
				presetTarget = strings.ToLower(strings.TrimSpace(args[0]))
				if _, err := registry.Get(presetTarget); err != nil {
					return utils.ValidationError{Message: fmt.Sprintf(
						"unknown agent %q. Valid agents: %s", presetTarget, registry.GetProviderList())}
				}
			}

			// Read the run/watch flags that affect the resumed session (shared with `search`).
			launchOpts := readResumeLaunchOpts(cmd)

			cwd, err := os.Getwd()
			if err != nil {
				slog.Error("Failed to get current working directory", "error", err)
				return err
			}

			// Open (building if needed) the session index and run the interactive picker.
			// builtFresh = the index was just rebuilt in the foreground, so the picker skips
			// the background index warm (it would be redundant).
			store, builtFresh, err := openOrBuildResumeIndex()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			projectID, projectName, idErr := utils.ComputeProjectID(cwd)
			if idErr != nil || projectID == "" {
				projectID, projectName = unknownProjectID, filepath.Base(cwd)
			}

			plan, err := selectResumeViaTUI(registry, store, projectID, projectName, presetTarget, builtFresh)
			if err != nil {
				return err
			}
			if plan == nil {
				// User quit or nothing to resume; the picker already explained why.
				return nil
			}

			return launchResume(plan, cwd, launchOpts)
		},
	}

	registerResumeLaunchFlags(resumeCmd, cloudURL, localTimeZone, debugDir)
	return resumeCmd
}

// registerResumeLaunchFlags registers the run/watch flags that affect the resumed session,
// shared by `resume` and `search` so the two stay in lockstep with each other and with watch.
// cloud-url binds to the shared pointer the root applies in PersistentPreRunE. The telemetry
// endpoint/service-name flags are inert here — they are consumed process-wide by main's startup
// arg scan — and registered only so cobra accepts them. debug-raw, provenance and cloud-url are
// hidden, matching run/watch.
func registerResumeLaunchFlags(cmd *cobra.Command, cloudURL *string, localTimeZone bool, debugDir string) {
	cmd.Flags().String("output-dir", "", "custom output directory for markdown files (default: ./.specstory/history)")
	cmd.Flags().String("debug-dir", debugDir, "custom output directory for debug data (default: ./.specstory/debug)")
	cmd.Flags().Bool("only-cloud-sync", false, "skip local markdown file saves, only upload to cloud (requires authentication)")
	cmd.Flags().Bool("no-cloud-sync", false, "disable cloud sync functionality")
	cmd.Flags().Bool("debug-raw", false, "debug mode to output pretty-printed raw data files")
	_ = cmd.Flags().MarkHidden("debug-raw")
	cmd.Flags().Bool("local-time-zone", localTimeZone, "use local timezone for file name and content timestamps (when not present: UTC)")
	cmd.Flags().Bool("provenance", false, "enable AI provenance tracking (correlate file changes to agent activity)")
	_ = cmd.Flags().MarkHidden("provenance")
	cmd.Flags().StringVar(cloudURL, "cloud-url", "", "override the default cloud API base URL")
	_ = cmd.Flags().MarkHidden("cloud-url")
	cmd.Flags().Bool("no-telemetry-prompts", false, "exclude prompt text from telemetry spans, if telemetry is enabled")
	cmd.Flags().String("telemetry-endpoint", "", "Open Telemetry Protocol (OTLP) gRPC collector endpoint (default is off, e.g., localhost:4317)")
	cmd.Flags().String("telemetry-service-name", "", "override the default service name for telemetry, if telemetry is enabled")
}

// readResumeLaunchOpts reads the session-affecting flags registered by registerResumeLaunchFlags
// into the launch options, applying the debug-dir override as a side effect (mirrors run/watch).
// cloud-url and the telemetry endpoint/service-name are consumed elsewhere (PersistentPreRunE and
// main's startup scan, respectively), so they are deliberately absent from the returned opts.
func readResumeLaunchOpts(cmd *cobra.Command) resumeLaunchOpts {
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")
	useLocalTimezone, _ := cmd.Flags().GetBool("local-time-zone")
	outputDir, _ := cmd.Flags().GetString("output-dir")
	flagDebugDir, _ := cmd.Flags().GetString("debug-dir")
	noCloudSync, _ := cmd.Flags().GetBool("no-cloud-sync")
	onlyCloudSync, _ := cmd.Flags().GetBool("only-cloud-sync")
	provenanceEnabled, _ := cmd.Flags().GetBool("provenance")
	noTelemetryPrompts, _ := cmd.Flags().GetBool("no-telemetry-prompts")

	if flagDebugDir != "" {
		spi.SetDebugBaseDir(flagDebugDir)
	}

	return resumeLaunchOpts{
		outputDir:          outputDir,
		flagDebugDir:       flagDebugDir,
		debugRaw:           debugRaw,
		useUTC:             !useLocalTimezone,
		noCloudSync:        noCloudSync,
		onlyCloudSync:      onlyCloudSync,
		provenanceEnabled:  provenanceEnabled,
		noTelemetryPrompts: noTelemetryPrompts,
	}
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

	// Keep sessions.db current in real time alongside the markdown writes (nil/no-op if the
	// index can't be opened — never block the resumed agent on it).
	liveIndex := NewLiveIndexer(cwd)
	defer liveIndex.Close()

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
		// Mirror the markdown write into the restore index (agent we resumed INTO).
		liveIndex.Record(plan.toID, sess)
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

	// One event records every cross-agent reconstruction outcome so portability usage and
	// failure rates are visible. Fired at each terminal branch below.
	track := func(outcome string) {
		analytics.TrackEvent(analytics.EventResumeReconstructed, analytics.Properties{
			"from_agent": plan.fromID,
			"to_agent":   plan.toID,
			"outcome":    outcome,
		})
	}
	slog.Info("resume: reconstructing cross-agent session",
		"from", plan.fromID, "to", plan.toID, "sessionID", plan.sessionID)

	// Cross-agent: load source SessionData, reconstruct, write into target store.
	// The source must be read from the directory it was originally launched in, not the
	// user's current cwd — a session picked from another project (all-projects browser)
	// lives elsewhere, and providers like Codex locate it by its recorded cwd. The
	// reconstructed session is still written and launched under the current cwd below.
	fromCwd := plan.fromCwd
	if fromCwd == "" {
		fromCwd = cwd
	}
	fromSession, err := plan.from.GetAgentChatSession(fromCwd, plan.sessionID, false)
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
			track("unsupported")
			return "", utils.ValidationError{Message: fmt.Sprintf(
				"%s can't yet be a cross-agent resume target. Choose Claude Code or Codex CLI (or resume in %s itself).",
				plan.to.Name(), plan.from.Name())}
		}
		slog.Warn("resume: reconstruction failed", "from", plan.fromID, "to", plan.toID, "error", err)
		track("error")
		return "", fmt.Errorf("failed to reconstruct session for %s: %w", plan.to.Name(), err)
	}

	path, err := plan.to.NativeSessionPath(cwd, rec.Filename)
	if err != nil {
		return "", fmt.Errorf("failed to resolve target session path: %w", err)
	}
	if err := writeReconstructedSession(path, rec.Content); err != nil {
		return "", err
	}
	// The agent runs in a separate process and resolves the session by reading this file the
	// instant it starts. On a filesystem without strong close-to-open coherence the freshly
	// written file can lag, so confirm it is actually readable before handing the agent a
	// --resume it would otherwise reject as "session not found".
	// Skip when Content is empty: some providers (e.g. Cursor IDE) write the session data
	// directly to their own store (SQLite) and return an empty sentinel file whose only purpose
	// is to satisfy this path — there is no file content to wait for.
	if len(rec.Content) > 0 {
		if err := waitForSessionFileVisible(path, sessionFileVisibleTimeout); err != nil {
			return "", fmt.Errorf("reconstructed %s session is not ready to resume: %w", plan.to.Name(), err)
		}
	}

	slog.Info("resume: wrote reconstructed session", "path", path, "newID", rec.SessionID)
	track("success")
	fprintf(out, "\nReconstructed %s session into %s as %s.\n", plan.from.Name(), plan.to.Name(), shortID(rec.SessionID))
	if plan.toID == "cursoride" {
		fprintf(out, "\nNote: only Cursor 3 is supported. Restart Cursor to see the imported session in the Agent sidebar.\n")
	}
	return rec.SessionID, nil
}

// shortID abbreviates a session ID for display.
func shortID(id string) string {
	if len(id) <= 13 {
		return id
	}
	return id[:5] + "..." + id[len(id)-5:]
}

// sessionFileVisibleTimeout bounds how long resume waits for a just-written reconstructed
// session file to become readable to other processes before launching the agent. It is
// generous enough to absorb the propagation lag of a weak-coherence filesystem (network or
// roaming home, cloud-synced folder, VM/container shared mount) yet short enough not to hang
// an interactive command — on a local disk the very first check passes, so it never waits.
const sessionFileVisibleTimeout = 10 * time.Second

// writeReconstructedSession durably and atomically writes a reconstructed session file. A
// plain os.WriteFile leaves the data in the page cache and the new directory entry possibly
// non-durable; on a filesystem without strong close-to-open coherence the separately-spawned
// agent process can then fail to see a (large) file and report the session as missing. To
// avoid that, the content is written to a temp file in the SAME directory (so the rename is
// atomic rather than cross-device), fsync'd, renamed into place, and the parent directory is
// fsync'd so the entry itself is durable.
func writeReconstructedSession(path string, content []byte) (err error) {
	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return fmt.Errorf("failed to create target session directory: %w", mkErr)
	}

	tmp, err := os.CreateTemp(dir, ".resume-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp session file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup of the temp file on any early return; a no-op once it is renamed away.
	defer func() { _ = os.Remove(tmpPath) }()

	// Preserve the world-readable mode the previous os.WriteFile(…, 0o644) produced
	// (CreateTemp makes the file 0o600).
	if chErr := tmp.Chmod(0o644); chErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to set session file mode: %w", chErr)
	}
	if _, wErr := tmp.Write(content); wErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp session file: %w", wErr)
	}
	// Flush the bytes to the backing store before the rename so a reader cannot observe a
	// renamed-but-empty file.
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to flush temp session file: %w", syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("failed to close temp session file: %w", closeErr)
	}

	if rnErr := os.Rename(tmpPath, path); rnErr != nil {
		return fmt.Errorf("failed to move session file into place: %w", rnErr)
	}

	// Fsync the directory so the new entry is durable and propagates. Best-effort: not all
	// platforms permit fsync on a directory handle, and it is a negligible cost on local disks.
	if d, openErr := os.Open(dir); openErr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// waitForSessionFileVisible polls until path is readable — present AND with at least one
// byte available, since on a weak-coherence filesystem a stat can succeed before the
// content propagates — or the timeout elapses, in which case it returns a diagnostic error
// so the caller can fail clearly rather than launch a --resume the agent would reject.
func waitForSessionFileVisible(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		readable, checkErr := sessionFileReadable(path)
		if readable {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("session file %q did not become readable within %s "+
				"(the target filesystem may lack close-to-open coherence): %w", path, timeout, checkErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// sessionFileReadable reports whether path exists and at least one byte can be read.
// A reconstructed session always has content, so an empty read means it has not propagated
// yet even though the directory entry exists. This is a readiness probe, not a parse: a
// single-byte read keeps it O(1) and format-agnostic, so it stays cheap for a binary native
// format (e.g. Cursor's store.db, which has no newline to scan to) as well as JSONL.
func sessionFileReadable(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	var b [1]byte
	n, err := f.Read(b[:])
	if n > 0 {
		return true, nil
	}
	if err == io.EOF || err == nil {
		return false, fmt.Errorf("session file is empty")
	}
	return false, err
}
