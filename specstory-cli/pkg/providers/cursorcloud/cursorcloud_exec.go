package cursorcloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// dashboardURL is where cloud agents are launched and followed.
const dashboardURL = "https://cursor.com/agents"

// ExecAgentAndWatch is `run` for a hosted agent. No local process exists, so
// it opens the cloud agents dashboard (or one agent's page when resuming) with
// the configured opener and then watches until Ctrl-C exactly like WatchAgent.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: starting", "projectPath", projectPath, "customCommand", customCommand, "resumeSessionID", resumeSessionID, "debugRaw", debugRaw)

	target := dashboardURL
	if resumeSessionID != "" {
		if validAgentID(resumeSessionID) {
			target = dashboardURL + "/" + resumeSessionID
		} else {
			slog.Warn("ExecAgentAndWatch: ignoring malformed agent id", "resumeSessionID", resumeSessionID)
		}
	}
	if err := openURL(customCommand, target); err != nil {
		// Watching still works when the browser could not be opened; the
		// user can follow the printed URL.
		slog.Warn("ExecAgentAndWatch: opening the dashboard failed", "url", target, "error", err)
		log.UserWarn("Could not open %s (%v). Open it in a browser; watching for Cursor Cloud Agent activity — press Ctrl-C to stop.", target, err)
	} else {
		log.UserMessage("Opened %s — watching for Cursor Cloud Agent activity, press Ctrl-C to stop.\n", target)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := p.WatchAgent(ctx, projectPath, debugRaw, sessionCallback)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	slog.Info("ExecAgentAndWatch: exiting", "error", err)
	return err
}

// openURL runs the opener with url appended. The default is the platform's
// URL handler; a custom command (cursorcloud_cmd or -c) replaces it.
func openURL(customCommand, url string) error {
	args := spi.SplitCommandLine(customCommand)
	if len(args) == 0 {
		args = defaultOpener()
	}
	if len(args) == 0 {
		return fmt.Errorf("no URL opener for %s", runtime.GOOS)
	}
	cmd := exec.Command(args[0], append(args[1:], url)...)
	slog.Info("ExecAgentAndWatch: opening dashboard", "command", args[0], "url", url)
	return cmd.Run()
}

// defaultOpener is the platform's own URL handler.
func defaultOpener() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"open"}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler"}
	default:
		return []string{"xdg-open"}
	}
}

// WatchAgent polls for cloud agent activity on this project's repository and
// delivers each new or updated session until ctx is cancelled, or until the
// API refuses the agent listing permanently (rejected key, Privacy Mode), which
// is returned as an error so `run` and `watch` report it instead of hanging.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: starting Cursor Cloud Agent polling", "projectPath", projectPath, "debugRaw", debugRaw)
	key, source := resolveAPIKey()
	if key == "" {
		slog.Info("WatchAgent: no API key configured")
		return errors.New("no Cursor API key: set CURSOR_API_KEY or cursor_api_key under [providers] in ~/.specstory/cli/config.toml")
	}
	origin := projectOrigin(projectPath)
	if origin == "" {
		slog.Info("WatchAgent: project has no git origin", "projectPath", projectPath)
		return noProjectOriginError(projectPath)
	}

	watcher := newCloudWatcher(projectPath, origin, newAPIClient(key), debugRaw, sessionCallback)
	watcher.start(ctx)
	slog.Info("WatchAgent: polling", "origin", origin, "keySource", source, "mirror", watcher.mirror.root, "interval", pollInterval)

	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
		slog.Info("WatchAgent: context cancelled, stopping")
	case err = <-watcher.fatal:
		slog.Error("WatchAgent: the API refused the agent listing; stopping", "error", err)
	}
	watcher.stop()
	slog.Info("WatchAgent: stopped")
	return err
}
