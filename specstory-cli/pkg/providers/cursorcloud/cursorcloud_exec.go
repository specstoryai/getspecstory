package cursorcloud

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// dashboardURL is where cloud agents are followed; an agent's page is
// dashboardURL/<id>.
const dashboardURL = "https://cursor.com/agents"

// launchName names every agent `run` starts. Cursor would otherwise name the
// agent after launchPromptText, which says nothing about the session, so the
// session title comes from the user's first real prompt instead (sessionName).
const launchName = "SpecStory session"

// launchPromptText opens every agent `run` starts. The API cannot create an
// agent without a prompt, and this one keeps the paid first run short: no
// tools, just a greeting, so the user takes over in the browser.
const launchPromptText = "Session started by SpecStory. Don't read files or run any tools yet. Briefly greet the user and ask what they'd like to work on."

// ExecAgentAndWatch is `run` for a hosted agent. No local process exists, so it
// starts a cloud agent on this project's repository (or, when resuming, uses
// that agent), opens the agent's page with the configured opener, and then
// watches until Ctrl-C exactly like WatchAgent.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: starting", "projectPath", projectPath, "customCommand", customCommand, "resumeSessionID", resumeSessionID, "debugRaw", debugRaw)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	agentID := resumeSessionID
	if agentID == "" {
		agent, err := launchSession(ctx, projectPath)
		if err != nil {
			slog.Error("ExecAgentAndWatch: launch failed", "error", err)
			return fmt.Errorf("could not start a Cursor Cloud Agent: %w", err)
		}
		agentID = agent.ID
	} else if !validAgentID(agentID) {
		return fmt.Errorf("%q is not a Cursor Cloud Agent id", agentID)
	}

	target := dashboardURL + "/" + agentID
	if err := openURL(customCommand, target); err != nil {
		// Watching still works when the browser could not be opened; the
		// user can follow the printed URL.
		slog.Warn("ExecAgentAndWatch: opening the agent page failed", "url", target, "error", err)
		log.UserWarn("Could not open %s (%v). Open it in a browser; watching for Cursor Cloud Agent activity — press Ctrl-C to stop.", target, err)
	} else {
		log.UserMessage("Opened %s — watching for Cursor Cloud Agent activity, press Ctrl-C to stop.\n", target)
	}

	err := p.WatchAgent(ctx, projectPath, debugRaw, sessionCallback)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	slog.Info("ExecAgentAndWatch: exiting", "error", err)
	return err
}

// launchSession starts a cloud agent on the project's repository and current
// branch. Cursor's agents page cannot be told which repository to use, so an
// agent started there lands wherever the user picks, or in a scratch
// repository when they pick none; launching through the API is the only way
// to guarantee the agent works on, and is saved to, this project.
func launchSession(ctx context.Context, projectPath string) (apiAgent, error) {
	key, _ := resolveAPIKey()
	if key == "" {
		return apiAgent{}, errNoAPIKey
	}
	origin := projectOrigin(projectPath)
	if origin == "" {
		return apiAgent{}, noProjectOriginError(projectPath)
	}
	// The API takes an HTTPS URL, while the origin may be SSH; the normalized
	// host/owner/repo form is the same for both.
	repoURL := "https://" + origin
	branch := spi.GitHeadBranch(projectPath)
	client := newAPIClient(key)

	slog.Info("launchSession: launching", "repo", repoURL, "branch", branch)
	agent, err := client.launchAgent(ctx, newLaunchRequest(repoURL, branch))
	if branch != "" && isUnknownBranch(err) {
		log.UserWarn("Branch %s is not on %s, so the agent starts from the default branch. Push the branch to start from it.", branch, origin)
		branch = ""
		agent, err = client.launchAgent(ctx, newLaunchRequest(repoURL, branch))
	}
	if err != nil {
		return apiAgent{}, err
	}
	slog.Info("launchSession: launched", "agentID", agent.ID, "branch", branch)
	return agent, nil
}

// newLaunchRequest is the launch body for repoURL; an empty branch starts the
// agent from the repository's default branch.
func newLaunchRequest(repoURL, branch string) launchRequest {
	return launchRequest{
		Name:   launchName,
		Prompt: launchPrompt{Text: launchPromptText},
		Repos:  []launchRepo{{URL: repoURL, StartingRef: branch}},
	}
}

// isUnknownBranch reports the launch refusal for a branch the remote does not
// have (an unpushed local branch). Cursor validates the branch before creating
// the agent, so nothing was started or billed, and the launch can be retried
// from the default branch. The API has no dedicated code for this, so the
// validation message is matched.
func isUnknownBranch(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		return false
	}
	return apiErr.Code == "validation_error" && strings.HasPrefix(apiErr.Message, "Branch ") && strings.Contains(apiErr.Message, "does not exist")
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
	slog.Info("ExecAgentAndWatch: opening agent page", "command", args[0], "url", url)
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

// errNoAPIKey is returned by `run` and `watch` when no key is configured.
var errNoAPIKey = errors.New("no Cursor API key: set CURSOR_API_KEY or cursor_api_key under [providers] in ~/.specstory/cli/config.toml")

// WatchAgent polls for cloud agent activity on this project's repository and
// delivers each new or updated session until ctx is cancelled, or until the
// API refuses the agent listing permanently (rejected key, Privacy Mode), which
// is returned as an error so `run` and `watch` report it instead of hanging.
func (p *Provider) WatchAgent(ctx context.Context, projectPath string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("WatchAgent: starting Cursor Cloud Agent polling", "projectPath", projectPath, "debugRaw", debugRaw)
	key, source := resolveAPIKey()
	if key == "" {
		slog.Info("WatchAgent: no API key configured")
		return errNoAPIKey
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
