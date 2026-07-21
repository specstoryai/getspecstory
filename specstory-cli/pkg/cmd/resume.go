package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/provenance"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/providers/copilotide"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
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

	// fromCloud marks a session sourced from SpecStory Cloud (another machine) rather than the
	// local index. It has no local native file, so resume always fetches its SessionData from the
	// cloud and reconstructs — even into the same agent. projectID is the cloud project to fetch
	// from (the source session's project_id).
	fromCloud bool
	projectID string

	// via records how the session was chosen, so EventResumeActivated can attribute the resume to
	// its entry point: "picker" (the `resume` TUI), "search" (the `search` TUI's `r` action), or
	// "session_uri" (`resume --session <uri>`, the web-copy funnel). Empty for older callers reads
	// as "picker" at the analytics site.
	via string
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
func CreateResumeCommand(cloudURL *string, defaults SessionFlagDefaults) *cobra.Command {
	longDesc := `Resume a past coding-agent session — in the same agent, or a different one.

'resume' opens an interactive picker of the sessions in the current project across all agents. Press tab to switch projects. Pick a session, then choose which installed agent to continue it in, and go. Resuming into a different agent reconstructs the conversation into that agent's native format first.

Pass an agent to set the resume target up front, e.g. 'specstory resume codex' — the picker then skips the target-selection step and resumes straight into that agent. The agent must be a known, installed provider, or the command errors.

Pass --session <uri> to resume a specific session without first browsing: a specstory:// URI, a cloud permalink, or a bare session UUID. When you also specify a target agent, you launch right into the resumed session; without a target agent the agent picker opens first.

Resuming SpecStory Cloud sessions (from your other machines) requires an active SpecStory Cloud login + SpecStory Pro.`

	resumeCmd := &cobra.Command{
		Use:   "resume [agent]",
		Short: "Resume a past session, optionally in a different project or agent",
		Long:  longDesc,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, _ := cmd.Flags().GetString("config-dir")
			config.EnsureDefaultProjectConfig(configDir)
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

			// --session <uri>: direct session addressing. The URI resolves to the same session
			// identity the picker would produce. With a preset agent the resume is fully
			// non-interactive (no TUI); without one the resolved session is pinned and the shared
			// picker tail below opens the TUI at the target-selection step.
			var pinned *sessionindex.Session
			sessionArg, _ := cmd.Flags().GetString("session")
			if sessionArg != "" {
				resolved, rerr := resolveSessionURI(sessionArg, store, agentIDByNameFromRegistry(registry))
				if rerr != nil {
					return rerr
				}
				if presetTarget != "" {
					// `resume <agent> --session <uri>`: build the plan directly and launch — no TUI.
					plan, perr := resumePlanFromSession(registry, store, resolved, presetTarget)
					if perr != nil {
						return perr
					}
					return launchResume(plan, cwd, launchOpts)
				}
				pinned = resolved
			}

			plan, err := selectResumeViaTUI(registry, store, projectID, projectName, presetTarget, builtFresh, pinned)
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

	registerSessionProcessingFlags(resumeCmd, cloudURL, defaults)
	// --session is registered on `resume` only — deliberately NOT in the shared
	// flag registration, so `search` is untouched.
	resumeCmd.Flags().String("session", "", "resume a specific session by URI or UUID (specstory://…, cloud permalink, or session UUID)")
	return resumeCmd
}

// readResumeLaunchOpts reads the session-affecting flags registered by
// registerSessionProcessingFlags into the launch options, applying the debug-dir override as
// a side effect (mirrors run/watch). cloud-url and the telemetry endpoint/service-name are
// consumed elsewhere (PersistentPreRunE and main's startup scan, respectively), so they are
// deliberately absent from the returned opts.
func readResumeLaunchOpts(cmd *cobra.Command) resumeLaunchOpts {
	debugRaw, _ := cmd.Flags().GetBool("debug-raw")
	outputDir, _ := cmd.Flags().GetString("output-dir")
	flagDebugDir, _ := cmd.Flags().GetString("debug-dir")
	noCloudSync, _ := cmd.Flags().GetBool("no-cloud-sync")
	provenanceEnabled, _ := cmd.Flags().GetBool("provenance")

	if flagDebugDir != "" {
		spi.SetDebugBaseDir(flagDebugDir)
	}

	return resumeLaunchOpts{
		outputDir:         outputDir,
		flagDebugDir:      flagDebugDir,
		debugRaw:          debugRaw,
		noCloudSync:       noCloudSync,
		provenanceEnabled: provenanceEnabled,
		processing:        ResolveProcessingOptions(cmd, true /* isAutosave */, false /* showOutput */),
	}
}

// resumeLaunchOpts carries the resume launch configuration shared by `resume` and
// `search` (whose `r` action resumes a found session through the same path).
type resumeLaunchOpts struct {
	outputDir         string
	flagDebugDir      string
	debugRaw          bool
	noCloudSync       bool
	provenanceEnabled bool
	processing        session.ProcessingOptions // shared autosave processing options
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
	if _, err := utils.NewProjectIdentityManager(cwd, outConfig.GetSpecstoryDir()).EnsureProjectIdentity(); err != nil {
		slog.Error("Failed to ensure project identity", "error", err)
	}

	CheckAndWarnAuthentication(o.noCloudSync)
	if o.processing.OnlyCloudSync && !cloud.IsAuthenticated() {
		return utils.ValidationError{Message: "--only-cloud-sync requires authentication. Please run 'specstory login' first"}
	}

	analytics.SetAgentProviders([]string{plan.to.Name()})
	via := plan.via
	if via == "" {
		via = "picker"
	}
	analytics.TrackEvent(analytics.EventResumeActivated, analytics.Properties{
		"from_provider": plan.fromID,
		"to_provider":   plan.toID,
		"cross_agent":   plan.fromID != plan.toID,
		"via":           via,
		"from_cloud":    plan.fromCloud,
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

	// Auto-save callback: the shared run/watch handling (markdown + cloud sync
	// + live index + provenance), recorded against the agent we resumed INTO.
	autosave := NewAutosaveCallback(AutosaveDeps{
		Ctx:        ctx,
		Config:     outConfig,
		Processing: o.processing,
		LiveIndex:  liveIndex,
		Provenance: provenanceEngine,
	})
	sessionCallback := func(sess *spi.AgentChatSession) {
		autosave(plan.toID, sess)
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
	// Cloud-sourced session: there is no local native file to resume in place, so always fetch its
	// SessionData from the cloud and reconstruct into the target — even when source and target agent
	// are the same.
	if plan.fromCloud {
		return prepareCloudResumeTarget(plan, cwd, out)
	}

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
			"source":     "local",
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
	rec, err := materializeReconstructed(plan, cwd, fromSession.SessionData, note)
	if err != nil {
		if errors.Is(err, spi.ErrReconstructionUnsupported) {
			track("unsupported")
			return "", utils.ValidationError{Message: fmt.Sprintf(
				"%s can't yet be a cross-agent resume target. Choose a different target agent (or resume in %s itself).",
				plan.to.Name(), plan.from.Name())}
		}
		slog.Warn("resume: reconstruction failed", "from", plan.fromID, "to", plan.toID, "error", err)
		track("error")
		return "", fmt.Errorf("failed to reconstruct session for %s: %w", plan.to.Name(), err)
	}
	track("success")
	fprintf(out, "\nReconstructed %s session into %s as %s.\n", plan.from.Name(), plan.to.Name(), shortID(rec.SessionID))
	printIDERestartNote(plan, out)
	return rec.SessionID, nil
}

// prepareCloudResumeTarget resumes a session that lives only in SpecStory Cloud: fetch its
// SessionData, refuse a schema newer than this CLI understands, then reconstruct into the target
// agent's native store (always — there is no local file to resume in place). Returns the fresh
// native session ID, just like the cross-agent path.
func prepareCloudResumeTarget(plan *resumePlan, cwd string, out io.Writer) (string, error) {
	track := func(outcome string) {
		analytics.TrackEvent(analytics.EventResumeReconstructed, analytics.Properties{
			"from_agent": plan.fromID,
			"to_agent":   plan.toID,
			"outcome":    outcome,
			"source":     "cloud",
		})
	}

	fprintf(out, "\nFetching %s session %s from SpecStory Cloud...\n", plan.from.Name(), shortID(plan.sessionID))
	raw, err := cloud.FetchSessionData(plan.projectID, plan.sessionID)
	if err != nil {
		track("error")
		return "", fmt.Errorf("failed to fetch session from cloud: %w", err)
	}

	var data schema.SessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		track("error")
		return "", fmt.Errorf("failed to parse cloud session data: %w", err)
	}

	// Refuse a blob produced by a newer CLI schema than this build understands — reconstructing it
	// could silently drop or mangle fields this version doesn't know about.
	if schemaVersionNewer(data.SchemaVersion, schema.CurrentSchemaVersion) {
		track("schema_too_new")
		return "", utils.ValidationError{Message: "Resuming this session requires an updated SpecStory CLI."}
	}

	note := fmt.Sprintf("Resumed from a %s session on another machine via SpecStory.", plan.from.Name())
	rec, err := materializeReconstructed(plan, cwd, &data, note)
	if err != nil {
		if errors.Is(err, spi.ErrReconstructionUnsupported) {
			track("unsupported")
			return "", utils.ValidationError{Message: fmt.Sprintf(
				"%s can't yet be a resume target. Choose Claude Code or Codex CLI.", plan.to.Name())}
		}
		slog.Warn("resume: cloud reconstruction failed", "from", plan.fromID, "to", plan.toID, "error", err)
		track("error")
		return "", fmt.Errorf("failed to reconstruct cloud session for %s: %w", plan.to.Name(), err)
	}
	track("success")
	fprintf(out, "Reconstructed cloud %s session into %s as %s.\n", plan.from.Name(), plan.to.Name(), shortID(rec.SessionID))
	printIDERestartNote(plan, out)
	return rec.SessionID, nil
}

// printIDERestartNote tells the user how to surface a session reconstructed into an
// IDE-backed store. Unlike CLI targets, these are not visible until the app restarts:
// Cursor only picks up imported sessions from its SQLite database on startup, and VS
// Code holds its chat session index in memory and flushes it over ours on shutdown.
func printIDERestartNote(plan *resumePlan, out io.Writer) {
	if plan.toID == "cursoride" {
		fprintf(out, "\nNote: only Cursor 3 is supported. Restart Cursor to see the imported session in the Agent sidebar.\n")
	}
	// A type check (not an ID comparison) so every Copilot IDE variant — stock
	// VS Code, Insiders, and any added later — gets the restart note.
	if _, ok := plan.to.(*copilotide.Provider); ok {
		// "Developer: Reload Window" is NOT enough — it keeps the main process (and
		// the in-memory index) alive; verified empirically.
		fprintf(out, "\nNote: quit and restart VS Code to see the imported session in the Chat panel.\n")
	}
}

// materializeReconstructed reconstructs data into plan.to's native store under cwd, durably writes
// the native file, and waits for it to become readable, returning the fresh session. Shared by the
// cross-agent and cloud resume paths (both reconstruct from a SessionData already in hand). A
// spi.ErrReconstructionUnsupported from the provider is returned unwrapped so callers can detect it.
func materializeReconstructed(plan *resumePlan, cwd string, data *schema.SessionData, note string) (*spi.ReconstructedSession, error) {
	rec, err := plan.to.ReconstructSession(data, spi.ReconstructOptions{
		WorkspaceRoot: cwd,
		MigrationNote: note,
	})
	if err != nil {
		return nil, err
	}

	path, err := plan.to.NativeSessionPath(cwd, rec.Filename)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target session path: %w", err)
	}
	if err := writeReconstructedSession(path, rec.Content); err != nil {
		return nil, err
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
			return nil, fmt.Errorf("reconstructed %s session is not ready to resume: %w", plan.to.Name(), err)
		}
	}

	slog.Info("resume: wrote reconstructed session", "path", path, "newID", rec.SessionID)
	return rec, nil
}

// schemaVersionNewer reports whether SessionData version v is newer than this CLI's supported
// version (dotted numeric, e.g. "1.0" vs "1.1"). Unparseable components compare as 0, so a
// malformed version is treated as not-newer and allowed through rather than blocking resume.
func schemaVersionNewer(v, current string) bool {
	return compareSchemaVersion(v, current) > 0
}

func compareSchemaVersion(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(strings.TrimSpace(as[i]))
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(strings.TrimSpace(bs[i]))
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
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
