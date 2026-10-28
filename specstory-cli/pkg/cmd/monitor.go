package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/monitor"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// CreateMonitorCommand creates the monitor command. The default values arrive
// resolved from config (like CreateWatchCommand's parameters) so config-file
// settings show up as flag defaults; CLI flags still win.
// Global --console/--log/--debug behave exactly as they do for watch: they are
// root persistent flags inherited by this command.
func CreateMonitorCommand(defaultIdleTimeout time.Duration, defaultMaxDepth int, defaultExclude []string) *cobra.Command {
	monitorCmd := &cobra.Command{
		Use:     "monitor <root-dir>",
		Aliases: []string{"m"},
		Short:   "Supervise 'specstory watch' across all git repos under a directory",
		Long: `Discover every git repository under <root-dir> and supervise per-repo 'specstory watch' processes.

The monitor watches the coding agents' own session storage (Claude Code, Codex CLI, and Cursor CLI)
for new activity, maps that activity back to a discovered repository, and starts a 'specstory watch'
child in that repository. Children idle past the timeout are stopped and respawned on new activity.

Each child follows your own SpecStory configuration (cloud sync, redaction, logging, ...).`,
		Example: `
# Supervise all git repos under ~/src
specstory monitor ~/src

# Stop idle watchers sooner and search deeper
specstory monitor ~/src --idle-timeout 2m --max-depth 6

# Skip directories during repo discovery
specstory monitor ~/src --exclude "archive/*" --exclude scratch`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			slog.Info("Running in monitor mode")

			// Read flags from the command
			idleTimeoutStr, _ := cmd.Flags().GetString("idle-timeout")
			maxDepth, _ := cmd.Flags().GetInt("max-depth")
			excludes, _ := cmd.Flags().GetStringArray("exclude")
			storageRootOverrides, _ := cmd.Flags().GetStringArray("storage-root")

			idleTimeout, err := time.ParseDuration(idleTimeoutStr)
			if err != nil || idleTimeout <= 0 {
				return utils.ValidationError{Message: fmt.Sprintf("invalid --idle-timeout %q: must be a positive Go duration (e.g. \"5m\")", idleTimeoutStr)}
			}

			rootDir, err := filepath.Abs(utils.ExpandTilde(args[0]))
			if err != nil {
				return utils.ValidationError{Message: fmt.Sprintf("invalid root directory %q: %v", args[0], err)}
			}
			info, err := os.Stat(rootDir)
			if err != nil || !info.IsDir() {
				return utils.ValidationError{Message: fmt.Sprintf("root directory %q does not exist or is not a directory", rootDir)}
			}

			roots, err := monitor.DefaultStorageRoots()
			if err != nil {
				return fmt.Errorf("failed to determine agent storage roots: %w", err)
			}
			if err := applyStorageRootOverrides(&roots, storageRootOverrides); err != nil {
				return err
			}

			// Repo discovery is startup-only: repos created under the root
			// after the monitor starts are picked up on the next monitor run.
			repos, err := monitor.DiscoverRepos(rootDir, maxDepth, excludes)
			if err != nil {
				return fmt.Errorf("failed to discover git repos under %s: %w", rootDir, err)
			}
			slog.Info("Monitor: discovered git repos", "root", rootDir, "count", len(repos), "repos", repos)
			if len(repos) == 0 {
				return utils.ValidationError{Message: fmt.Sprintf("no git repos found under %s (searched %d levels deep)", rootDir, maxDepth)}
			}

			analytics.TrackEvent(analytics.EventMonitorActivated, analytics.Properties{
				"repo_count":   len(repos),
				"idle_timeout": idleTimeout.String(),
			})

			resolver := monitor.NewResolver(repos, roots)
			supervisor := monitor.NewSupervisor(idleTimeout)

			// Graceful cancellation (Ctrl+C / SIGTERM), mirroring watch: the
			// context stops the storage-root watchers and the reap loop, then
			// Shutdown below terminates every child before we return.
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			// The monitor captures activity forward from launch: pre-existing
			// storage entries are deliberately NOT scanned or spawned for.
			// Anything already there is historical — possibly minutes,
			// possibly months old — and spawning watchers for history would
			// start children for repos with no live agent, only for the reaper
			// to churn through them. New events tell us something is live NOW.
			onActivity := func(eventPath string) {
				if repo, ok := resolver.Resolve(eventPath); ok {
					supervisor.NotifyActivity(repo)
				}
			}
			started := 0
			for provider, storageRoot := range map[string]string{
				"claude": roots.Claude,
				"codex":  roots.Codex,
				"cursor": roots.Cursor,
			} {
				if _, statErr := os.Stat(storageRoot); statErr != nil {
					slog.Info("Monitor: skipping missing storage root", "provider", provider, "path", storageRoot)
					continue
				}
				started++
				go func(provider, storageRoot string) {
					slog.Info("Monitor: watching storage root", "provider", provider, "path", storageRoot)
					if watchErr := monitor.WatchRootForNewEntries(ctx, storageRoot, onActivity); watchErr != nil {
						slog.Error("Monitor: storage root watcher failed", "provider", provider, "path", storageRoot, "error", watchErr)
					}
				}(provider, storageRoot)
			}
			// VS Code Copilot chat storage gets dedicated selective watchers
			// (one per installed VS Code variant) rather than the generic
			// recursive root watcher above, because workspaceStorage can hold
			// thousands of workspace directories and would exhaust file
			// descriptors. That code depends on the copilotide provider, which
			// is not yet present in this tree, so it is compiled only under the
			// copilotide_monitor build tag; the default build's stub starts none.
			started += startCopilotWatchers(ctx, roots, resolver, supervisor)
			if started == 0 {
				return utils.ValidationError{Message: "no agent storage roots exist to watch (looked for Claude Code, Codex CLI, and Cursor CLI session storage)"}
			}

			go supervisor.Run(ctx)

			if !log.IsSilent() {
				fmt.Println()
				repoWord := "repos"
				if len(repos) == 1 {
					repoWord = "repo"
				}
				fmt.Printf("🔭 Monitoring %d git %s under %s\n", len(repos), repoWord, rootDir)
				fmt.Println("   Watch processes start on agent activity and stop after", idleTimeout, "idle")
				fmt.Println("   Press Ctrl+C to stop monitoring")
				fmt.Println()
			}

			<-ctx.Done()
			slog.Info("Monitor: shutting down")
			supervisor.Shutdown()
			return nil
		},
	}

	// Monitor-specific flags. --idle-timeout is a string (not a cobra Duration)
	// so config values pass through verbatim and the error message is ours.
	monitorCmd.Flags().String("idle-timeout", defaultIdleTimeout.String(), "stop a repo's watch process after this much inactivity (Go duration, e.g. \"5m\")")
	monitorCmd.Flags().Int("max-depth", defaultMaxDepth, "how many directory levels below <root-dir> to search for git repos")
	monitorCmd.Flags().StringArray("exclude", defaultExclude, "path glob (relative to <root-dir>) to skip during repo discovery (repeatable)")
	monitorCmd.Flags().StringArray("storage-root", nil, "TEST ONLY: override an agent storage root as provider:path (providers: claude, codex, cursor; repeatable)")
	_ = monitorCmd.Flags().MarkHidden("storage-root") // Hidden test-only flag

	return monitorCmd
}

// applyStorageRootOverrides applies hidden --storage-root provider:path
// overrides. This exists solely so tests (and the release smoke test) can
// point a provider's storage root at a fixture directory instead of the real
// ~ paths; it is not part of the public interface.
func applyStorageRootOverrides(roots *monitor.StorageRoots, overrides []string) error {
	for _, override := range overrides {
		provider, path, found := strings.Cut(override, ":")
		if !found || path == "" {
			return utils.ValidationError{Message: fmt.Sprintf("invalid --storage-root %q: expected provider:path", override)}
		}
		abs, err := filepath.Abs(utils.ExpandTilde(path))
		if err != nil {
			return utils.ValidationError{Message: fmt.Sprintf("invalid --storage-root path %q: %v", path, err)}
		}
		switch provider {
		case "claude":
			roots.Claude = abs
		case "codex":
			roots.Codex = abs
		case "cursor":
			roots.Cursor = abs
		default:
			// copilotStorageRootOverride handles the copilotide provider, but
			// only in the copilotide_monitor build; the default build's stub
			// returns false, so an unknown provider falls through to the error.
			if copilotStorageRootOverride(roots, provider, abs) {
				continue
			}
			return utils.ValidationError{Message: fmt.Sprintf("invalid --storage-root provider %q: must be claude, codex, or cursor", provider)}
		}
	}
	return nil
}
