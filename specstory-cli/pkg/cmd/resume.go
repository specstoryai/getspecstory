package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
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

// resumePageSize is the number of sessions shown per page in the picker.
const resumePageSize = 10

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

// CreateResumeCommand builds the `specstory resume` command (Stage 1: cross-agent).
// It mirrors the run/watch setup, then drives an interactive selection: pick a
// source agent + session, pick a target agent, then reconstruct (cross-agent) or
// native-resume (same agent) and launch with auto-save.
func CreateResumeCommand(cloudURL *string, localTimeZone bool, debugDir string) *cobra.Command {
	longDesc := `Resume a past coding-agent session — in the same agent, or a different one.

'resume' lists the sessions found in the current project across all agents, lets you
pick one, and lets you choose which installed agent to continue it in. Resuming into a
different agent reconstructs the conversation into that agent's native format first.`

	resumeCmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a past session, optionally in a different agent",
		Long:  longDesc,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			config.EnsureDefaultProjectConfig()
			slog.Info("Running in resume mode")

			registry := factory.GetRegistry()

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

			// Interactive selection of source agent, session, and target agent.
			plan, err := selectResumePlan(registry, cwd, os.Stdin, os.Stdout)
			if err != nil {
				return err
			}
			if plan == nil {
				// User quit or nothing to resume; selectResumePlan already explained why.
				return nil
			}

			// Setup output configuration and project identity (needed for auto-save + cloud).
			outConfig, err := utils.SetupOutputConfig(outputDir, flagDebugDir)
			if err != nil {
				return err
			}
			if err := utils.EnsureHistoryDirectoryExists(outConfig); err != nil {
				return err
			}
			if _, err := utils.NewProjectIdentityManager(cwd).EnsureProjectIdentity(); err != nil {
				slog.Error("Failed to ensure project identity", "error", err)
			}

			CheckAndWarnAuthentication(noCloudSync)
			if onlyCloudSync && !cloud.IsAuthenticated() {
				return utils.ValidationError{Message: "--only-cloud-sync requires authentication. Please run 'specstory login' first"}
			}

			analytics.SetAgentProviders([]string{plan.to.Name()})
			analytics.TrackEvent(analytics.EventResumeActivated, analytics.Properties{
				"from_provider": plan.fromID,
				"to_provider":   plan.toID,
				"cross_agent":   plan.fromID != plan.toID,
			})

			// Resolve the native session ID to resume.
			resumeSessionID, err := prepareResumeTarget(plan, cwd, os.Stdout)
			if err != nil {
				return err
			}

			// Context for graceful cancellation (Ctrl+C).
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// Provenance infrastructure (mirrors run/watch).
			provenanceEngine, provenanceCleanup, err := provenance.StartEngine(provenanceEnabled)
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
					OnlyCloudSync:      onlyCloudSync,
					IsAutosave:         true,
					DebugRaw:           debugRaw,
					UseUTC:             useUTC,
					NoTelemetryPrompts: noTelemetryPrompts,
				})
				if perr != nil {
					slog.Error("Failed to process session update", "sessionId", sess.SessionID, "error", perr)
				}
				provenance.ProcessEvents(ctx, provenanceEngine, sess)
			}

			slog.Info("Launching resume", "provider", plan.to.Name(), "resumeSessionID", resumeSessionID)
			if err := plan.to.ExecAgentAndWatch(cwd, "", resumeSessionID, debugRaw, sessionCallback); err != nil {
				slog.Error("Agent resume failed", "provider", plan.to.Name(), "error", err)
				return err
			}
			return nil
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

// selectResumePlan runs the interactive selection. Returns nil (no error) when
// there is nothing to resume or the user quits.
func selectResumePlan(registry *factory.Registry, projectPath string, in io.Reader, out io.Writer) (*resumePlan, error) {
	reader := bufio.NewReader(in)

	// 1. Source agents: those with at least one session in this project.
	var sources []agentChoice
	sessionsByID := map[string][]spi.SessionMetadata{}
	for _, id := range registry.ListIDs() {
		provider, err := registry.Get(id)
		if err != nil {
			continue
		}
		metas, err := provider.ListAgentChatSessions(projectPath)
		if err != nil {
			slog.Debug("resume: listing sessions failed", "provider", id, "error", err)
			continue
		}
		if len(metas) == 0 {
			continue
		}
		sortSessionsDesc(metas)
		sources = append(sources, agentChoice{id: id, provider: provider})
		sessionsByID[id] = metas
	}
	if len(sources) == 0 {
		fprintln(out, "\nNo resumable sessions found for any agent in this project.")
		return nil, nil
	}

	fprintln(out, "\nResume from which agent?")
	fprintln(out)
	for i, s := range sources {
		metas := sessionsByID[s.id]
		fprintf(out, "  %d. %s — %d session%s (%s)\n", i+1, s.provider.Name(), len(metas), plural(len(metas)), dateRange(metas))
	}
	fromIdx, err := promptIndex(reader, out, "\nEnter number (or q to quit): ", len(sources))
	if err != nil || fromIdx < 0 {
		return nil, err
	}
	from := sources[fromIdx]

	// 2. Session within the chosen agent (paginated, reverse-chronological).
	session := pickSession(reader, out, sessionsByID[from.id])
	if session == nil {
		return nil, nil
	}

	// 3. Target agent: installed agents (including the source).
	var targets []agentChoice
	for _, id := range registry.ListIDs() {
		provider, err := registry.Get(id)
		if err != nil {
			continue
		}
		if provider.Check("").Success {
			targets = append(targets, agentChoice{id: id, provider: provider})
		}
	}
	if len(targets) == 0 {
		fprintln(out, "\nNo installed agents found to resume into.")
		return nil, nil
	}

	fprintln(out, "\nResume into which agent?")
	fprintln(out)
	for i, t := range targets {
		label := t.provider.Name()
		if t.id == from.id {
			label += " (same agent — native resume)"
		}
		fprintf(out, "  %d. %s\n", i+1, label)
	}
	toIdx, err := promptIndex(reader, out, "\nEnter number (or q to quit): ", len(targets))
	if err != nil || toIdx < 0 {
		return nil, err
	}
	to := targets[toIdx]

	return &resumePlan{
		from:      from.provider,
		fromID:    from.id,
		sessionID: session.SessionID,
		to:        to.provider,
		toID:      to.id,
	}, nil
}

// pickSession shows a paginated, reverse-chronological list and returns the
// chosen session, or nil if the user quits.
func pickSession(reader *bufio.Reader, out io.Writer, sessions []spi.SessionMetadata) *spi.SessionMetadata {
	total := len(sessions)
	pages := (total + resumePageSize - 1) / resumePageSize
	page := 0

	for {
		start := page * resumePageSize
		end := min(start+resumePageSize, total)

		fprintf(out, "\nSessions (page %d of %d):\n\n", page+1, pages)
		for i := start; i < end; i++ {
			s := sessions[i]
			fprintf(out, "  %d. %s  %s\n", i+1, sessionDate(s.CreatedAt), sessionLabel(s))
		}

		hint := "Enter a number to pick"
		if page < pages-1 {
			hint += ", n=next"
		}
		if page > 0 {
			hint += ", p=prev"
		}
		hint += ", q=quit: "
		fprint(out, "\n"+hint)

		line, err := reader.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))
		switch choice {
		case "n":
			if page < pages-1 {
				page++
			}
			continue
		case "p":
			if page > 0 {
				page--
			}
			continue
		case "q", "":
			return nil
		}
		// io.EOF with no input also lands here; treat a parse failure as a re-prompt.
		n, perr := strconv.Atoi(choice)
		if perr != nil || n < 1 || n > total {
			fprintln(out, "Invalid choice.")
			if err != nil {
				// No more input (e.g., piped/closed stdin); avoid looping forever.
				return nil
			}
			continue
		}
		return &sessions[n-1]
	}
}

// promptIndex prompts for a 1-based selection and returns a 0-based index, or -1
// if the user quits.
func promptIndex(reader *bufio.Reader, out io.Writer, prompt string, n int) (int, error) {
	for {
		fprint(out, prompt)
		line, err := reader.ReadString('\n')
		choice := strings.ToLower(strings.TrimSpace(line))
		if choice == "q" || choice == "" {
			return -1, nil
		}
		idx, perr := strconv.Atoi(choice)
		if perr == nil && idx >= 1 && idx <= n {
			return idx - 1, nil
		}
		fprintln(out, "Invalid choice.")
		if err != nil {
			// No more input; stop rather than loop forever.
			return -1, nil
		}
	}
}

// sortSessionsDesc orders sessions newest-first by CreatedAt (ISO 8601 sorts
// lexicographically), falling back to SessionID for stability.
func sortSessionsDesc(sessions []spi.SessionMetadata) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt != sessions[j].CreatedAt {
			return sessions[i].CreatedAt > sessions[j].CreatedAt
		}
		return sessions[i].SessionID > sessions[j].SessionID
	})
}

// dateRange returns a human-readable span of session creation dates.
func dateRange(sessions []spi.SessionMetadata) string {
	var earliest, latest time.Time
	for _, s := range sessions {
		t, err := time.Parse(time.RFC3339, s.CreatedAt)
		if err != nil {
			continue
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
		if t.After(latest) {
			latest = t
		}
	}
	if earliest.IsZero() {
		return "dates unknown"
	}
	if earliest.Format("2006-01-02") == latest.Format("2006-01-02") {
		return earliest.Format("Jan 2, 2006")
	}
	return earliest.Format("Jan 2, 2006") + " – " + latest.Format("Jan 2, 2006")
}

// sessionDate formats a session's creation timestamp for the picker.
func sessionDate(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return createdAt
	}
	return t.Local().Format("2006-01-02 15:04")
}

// sessionLabel returns a human-friendly label for a session, preferring its name,
// then slug, then a shortened ID.
func sessionLabel(s spi.SessionMetadata) string {
	switch {
	case strings.TrimSpace(s.Name) != "":
		return s.Name
	case strings.TrimSpace(s.Slug) != "":
		return s.Slug
	default:
		return shortID(s.SessionID)
	}
}

// shortID abbreviates a session ID for display.
func shortID(id string) string {
	if len(id) <= 13 {
		return id
	}
	return id[:5] + "..." + id[len(id)-5:]
}

// plural returns "s" unless n == 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
