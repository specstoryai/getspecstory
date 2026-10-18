package cursorcloud

import (
	"context"
	"encoding/json"
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
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
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

// ExecAgentAndWatch is `run` and `resume` for a hosted agent. No local process
// exists, so it decides which agent to open (resumeTarget), opens the agent's
// page with the configured opener, and then watches until Ctrl-C exactly like
// WatchAgent.
func (p *Provider) ExecAgentAndWatch(projectPath string, customCommand string, resumeSessionID string, debugRaw bool, sessionCallback func(*spi.AgentChatSession)) error {
	slog.Info("ExecAgentAndWatch: starting", "projectPath", projectPath, "customCommand", customCommand, "resumeSessionID", resumeSessionID, "debugRaw", debugRaw)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	agentID, err := resumeTarget(ctx, projectPath, resumeSessionID)
	if err != nil {
		slog.Error("ExecAgentAndWatch: no agent to open", "resumeSessionID", resumeSessionID, "error", err)
		return err
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

	err = p.WatchAgent(ctx, projectPath, debugRaw, sessionCallback)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	slog.Info("ExecAgentAndWatch: exiting", "error", err)
	return err
}

// resumeTarget returns the agent to open. `run` (no session) starts a new
// agent. A cloud agent's own session reopens that agent, so the conversation
// continues where it was rather than in a copy. Anything else, or an agent
// that can no longer be reopened here, starts a new agent seeded with the
// session's transcript.
func resumeTarget(ctx context.Context, projectPath, sessionID string) (string, error) {
	key, _ := resolveAPIKey()
	if key == "" {
		return "", errNoAPIKey
	}
	client := newAPIClient(key)

	switch {
	case sessionID == "":
		agent, err := launchSession(ctx, client, projectPath, launchName, launchPromptText)
		if err != nil {
			return "", fmt.Errorf("could not start a Cursor Cloud Agent: %w", err)
		}
		return agent.ID, nil
	case strings.HasPrefix(sessionID, pendingPrefix):
		return resumePending(ctx, client, projectPath, sessionID)
	case validAgentID(sessionID):
		return resumeAgent(ctx, client, projectPath, sessionID)
	default:
		return "", fmt.Errorf("%q is not a Cursor Cloud Agent id", sessionID)
	}
}

// resumePending carries out a launch ReconstructSession prepared: it reopens
// the source cloud agent when there is one to reopen, and otherwise launches a
// new agent with the transcript.
func resumePending(ctx context.Context, client *apiClient, projectPath, sessionID string) (string, error) {
	m, _ := mirrorFor(projectPath)
	path := m.pendingFile(sessionID + ".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the prepared resume: %w", err)
	}
	// The file holds an unredacted transcript and has served its purpose once
	// read; a failed launch is retried by resuming again.
	if err := os.Remove(path); err != nil {
		slog.Warn("resumePending: could not remove the prepared resume", "path", path, "error", err)
	}
	var pending pendingResume
	if err := json.Unmarshal(raw, &pending); err != nil {
		return "", fmt.Errorf("decode the prepared resume: %w", err)
	}
	if pending.SourceAgentID != "" {
		reopened, err := reopenAgent(ctx, client, projectPath, pending.SourceAgentID)
		if err != nil || reopened {
			return pending.SourceAgentID, err
		}
	}
	return launchCopy(ctx, client, projectPath, pending.Name, pending.Prompt)
}

// resumeAgent reopens a cloud agent's own session. When the agent cannot be
// reopened here, the session is copied into a new agent from the mirror,
// which keeps the transcript after Cursor has deleted the agent.
func resumeAgent(ctx context.Context, client *apiClient, projectPath, agentID string) (string, error) {
	reopened, err := reopenAgent(ctx, client, projectPath, agentID)
	if err != nil || reopened {
		return agentID, err
	}
	m, _ := mirrorFor(projectPath)
	snap, err := loadAgentSnapshot(m.agentDir(agentID))
	if err != nil {
		return "", err
	}
	var data *schema.SessionData
	if snap != nil {
		data, _ = buildSessionData(snap, workspaceRootFor(projectPath))
	}
	if data == nil {
		return "", fmt.Errorf("cursor cloud agent %s can't be reopened here, and SpecStory has no copy of its session to start a new agent from", agentID)
	}
	turns, err := spi.PrepareTurns(data, spi.ReconstructOptions{MigrationNote: "Resumed from an earlier Cursor Cloud Agent session via SpecStory."})
	if err != nil {
		return "", err
	}
	return launchCopy(ctx, client, projectPath, spi.ResumedSessionTitle(data.Slug), resumePrompt(turns))
}

// reopenAgent reports whether agentID can be reopened for this project. An
// agent Cursor no longer has, or one on another repository (whose activity
// this project's watch would never pick up), cannot. An archived agent is
// unarchived first, because resuming it is a request to use it again.
func reopenAgent(ctx context.Context, client *apiClient, projectPath, agentID string) (bool, error) {
	record, err := client.getAgent(ctx, agentID)
	if isNotFound(err) {
		slog.Info("reopenAgent: agent no longer exists", "agentID", agentID)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !agentMatchesOrigin(record.agent, projectOrigin(projectPath)) {
		slog.Info("reopenAgent: agent is on another repository", "agentID", agentID, "repos", record.agent.Repos)
		return false, nil
	}
	if record.agent.Status == "ARCHIVED" {
		if err := client.unarchiveAgent(ctx, agentID); err != nil {
			return false, fmt.Errorf("could not unarchive Cursor Cloud Agent %s: %w", agentID, err)
		}
		log.UserMessage("Unarchived Cursor Cloud Agent %s to resume it.\n", agentID)
	}
	slog.Info("reopenAgent: reopening", "agentID", agentID)
	return true, nil
}

// launchCopy starts a new agent seeded with a transcript, refusing one too
// large for a prompt before anything is billed.
func launchCopy(ctx context.Context, client *apiClient, projectPath, name, prompt string) (string, error) {
	if err := checkResumeSize(prompt); err != nil {
		return "", err
	}
	log.UserMessage("Starting a new Cursor Cloud Agent with the session so far. This is a new paid cloud run.\n")
	agent, err := launchSession(ctx, client, projectPath, name, prompt)
	if err != nil {
		return "", fmt.Errorf("could not start a Cursor Cloud Agent: %w", err)
	}
	return agent.ID, nil
}

// launchSession starts a cloud agent on the project's repository and current
// branch. Cursor's agents page cannot be told which repository to use, so an
// agent started there lands wherever the user picks, or in a scratch
// repository when they pick none; launching through the API is the only way
// to guarantee the agent works on, and is saved to, this project.
func launchSession(ctx context.Context, client *apiClient, projectPath, name, prompt string) (apiAgent, error) {
	origin := projectOrigin(projectPath)
	if origin == "" {
		return apiAgent{}, noProjectOriginError(projectPath)
	}
	// The API takes an HTTPS URL, while the origin may be SSH; the normalized
	// host/owner/repo form is the same for both.
	repoURL := "https://" + origin
	branch := spi.GitHeadBranch(projectPath)

	slog.Info("launchSession: launching", "repo", repoURL, "branch", branch, "promptChars", len(prompt))
	agent, err := client.launchAgent(ctx, newLaunchRequest(repoURL, branch, name, prompt))
	if branch != "" && isUnknownBranch(err) {
		log.UserWarn("Branch %s is not on %s, so the agent starts from the default branch. Push the branch to start from it.", branch, origin)
		branch = ""
		agent, err = client.launchAgent(ctx, newLaunchRequest(repoURL, branch, name, prompt))
	}
	if err != nil {
		return apiAgent{}, err
	}
	slog.Info("launchSession: launched", "agentID", agent.ID, "branch", branch)
	return agent, nil
}

// newLaunchRequest is the launch body for repoURL; an empty branch starts the
// agent from the repository's default branch.
func newLaunchRequest(repoURL, branch, name, prompt string) launchRequest {
	return launchRequest{
		Name:   name,
		Prompt: launchPrompt{Text: prompt},
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
