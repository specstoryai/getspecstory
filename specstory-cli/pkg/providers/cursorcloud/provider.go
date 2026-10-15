package cursorcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/log"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
)

// Compile-time assertions that Provider satisfies the full spi.Provider
// contract plus the optional by-path reader used by reindex.
var (
	_ spi.Provider          = (*Provider)(nil)
	_ spi.PathSessionReader = (*Provider)(nil)
)

// checkProbe names the endpoint Check calls, reported in analytics where a
// binary provider reports its version flag.
const checkProbe = "GET /v1/me"

// apiKeyPage is where a user creates a key.
const apiKeyPage = "https://cursor.com/dashboard/api"

// Provider implements spi.Provider for Cursor Cloud Agents, whose
// conversations exist only in Cursor's backend. The Cloud Agents API is the
// native store; a local mirror of it is what every read path parses.
type Provider struct{}

// NewProvider creates a Cursor Cloud Agent provider.
func NewProvider() *Provider {
	return &Provider{}
}

// Name returns the product's name.
func (p *Provider) Name() string {
	return providerName
}

// Check verifies a key is configured and accepted by probing GET /v1/me.
// There is no binary; customCommand is the dashboard opener used by run, so a
// custom one is checked for existence.
func (p *Provider) Check(customCommand string) spi.CheckResult {
	attempt := analytics.CheckAttempt{
		Provider:      ProviderID,
		CustomCommand: customCommand != "",
		CommandPath:   customCommand,
		VersionFlag:   checkProbe,
	}
	slog.Info("Check: verifying Cursor Cloud Agent access", "customCommand", customCommand != "")

	fail := func(errorType, detail, message string) spi.CheckResult {
		slog.Info("Check: failed", "errorType", errorType, "error", detail)
		analytics.TrackCheckFailure(attempt, errorType, detail, "")
		return spi.CheckResult{Success: false, ErrorType: errorType, ErrorMessage: message}
	}

	if customCommand != "" {
		opener := spi.SplitCommandLine(customCommand)
		if len(opener) == 0 {
			return fail(spi.CheckErrorNotFound, "empty command", "The custom command is empty.\n")
		}
		resolved, err := spi.LookPathForCheck(opener[0])
		if err != nil {
			errorType := spi.ClassifyCheckError(err)
			return fail(errorType, err.Error(), fmt.Sprintf(
				"Could not find the dashboard opener: %s\n\n"+
					"• cursorcloud_cmd (or -c) is the command run cursorcloud uses to open https://cursor.com/agents.\n"+
					"• Check the path, or remove the setting to use the system URL opener.\n", opener[0]))
		}
		attempt.ResolvedPath = resolved
	}

	key, source := resolveAPIKey()
	if key == "" {
		return fail(spi.CheckErrorNotFound, "no API key configured", missingKeyMessage())
	}

	ctx, cancel := context.WithTimeout(context.Background(), jsonTimeout)
	defer cancel()
	me, err := newAPIClient(key).me(ctx)
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden) {
			return fail(spi.CheckErrorPermissionDenied, err.Error(), fmt.Sprintf(
				"Cursor rejected the API key from %s (HTTP %d).\n\n"+
					"• Create a new key at %s and set %s or cursor_api_key under [providers] in specstory.toml.\n",
				source, ae.Status, apiKeyPage, apiKeyEnvVar))
		}
		return fail(spi.CheckErrorUnknown, err.Error(), fmt.Sprintf(
			"Could not reach the Cursor Cloud Agents API (%s).\n\nError: %v\n\n"+
				"• Accounts on Privacy Mode (Legacy) cannot use cloud agents; switch to standard Privacy Mode.\n"+
				"• Check your network connection and try again.\n", checkProbe, err))
	}

	// The API exposes no agent version, so Version stays empty and the CLI omits
	// the version line, as it does for IDE providers.
	location := defaultAPIBaseURL
	if me.KeyLabel != "" {
		location = fmt.Sprintf("%s (key %q)", defaultAPIBaseURL, me.KeyLabel)
	}
	slog.Info("Check: succeeded", "keySource", source, "location", location)
	analytics.TrackCheckSuccess(attempt, "")
	return spi.CheckResult{Success: true, Location: location}
}

func missingKeyMessage() string {
	return fmt.Sprintf("No Cursor API key is configured.\n\n"+
		"• Create a key at %s\n"+
		"• Then set the %s environment variable, or add it to specstory.toml:\n\n"+
		"    [providers]\n    cursor_api_key = \"crsr_...\"\n", apiKeyPage, apiKeyEnvVar)
}

// DetectAgent reports whether this project has cloud agents: the project has
// a git origin, and either the mirror already holds an agent for that
// repository or, when a key is configured, the first page of the agent list
// names one. The mirror is consulted without a key so that histories already
// mirrored can still be synced after the key is unset or rotated.
func (p *Provider) DetectAgent(projectPath string, helpOutput bool) bool {
	key, _ := resolveAPIKey()
	origin := projectOrigin(projectPath)
	found := false
	if origin != "" {
		m, _ := mirrorFor(projectPath)
		found = len(m.matchingAgentIDs(origin)) > 0
		if !found && key != "" {
			ctx, cancel := context.WithTimeout(context.Background(), jsonTimeout)
			client := newAPIClient(key)
			page, err := client.listAgentPage(ctx, "")
			cancel()
			if err != nil {
				slog.Warn("DetectAgent: listing cloud agents failed", "error", err)
			} else {
				rememberDetectedPage(client, page)
			}
			for _, record := range page.records {
				if agentMatchesOrigin(record.agent, origin) {
					found = true
					break
				}
			}
		}
	}

	if helpOutput && !found {
		log.UserWarn("\nNo Cursor Cloud Agents were found for this project.\n")
		switch {
		case key == "":
			log.UserMessage("%s\n", missingKeyMessage())
		case origin == "":
			log.UserMessage("Cloud agents are matched to a project by its git origin URL, and this directory has no origin remote.\n")
			log.UserMessage("Run SpecStory from a clone of the repository the cloud agents worked on.\n\n")
		default:
			log.UserMessage("Cloud agents are matched to a project by repository URL. None of your agents worked on %s.\n", origin)
			log.UserMessage("Launch one at https://cursor.com/agents for this repository, then sync again.\n\n")
		}
	}
	return found
}

// GetAgentChatSessions refreshes the mirror for this project's agents, then
// builds every matching session from the mirror. Without a key or network it
// still returns what the mirror already holds.
func (p *Provider) GetAgentChatSessions(projectPath string, debugRaw bool, progress spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	origin := projectOrigin(projectPath)
	if origin == "" {
		slog.Debug("GetAgentChatSessions: project has no git origin; no cloud agents can match", "projectPath", projectPath)
		return []spi.AgentChatSession{}, nil
	}
	m, projectLocal := mirrorFor(projectPath)
	refreshProject(context.Background(), m, projectLocal, origin, progress)

	// Progress is reported again once per mirrored agent, including those that
	// fail to convert, so the bar reaches its total. An agent whose refresh
	// failed is still read from whatever the mirror held before.
	snapshots := m.matchingSnapshots(origin)
	var sessions []spi.AgentChatSession
	for i, snap := range snapshots {
		if session := convertSnapshot(snap, projectPath, debugRaw); session != nil {
			sessions = append(sessions, *session)
		}
		if progress != nil {
			progress(i+1, len(snapshots))
		}
	}
	return sessions, nil
}

// refreshProject refreshes every agent for origin. Failures are logged per
// agent; the mirror still serves the rest. Progress is reported per agent
// because at one request every 3 seconds a first sync of a busy repository
// spends minutes here, before any session is parsed.
func refreshProject(ctx context.Context, m mirror, projectLocal bool, origin string, progress spi.ProgressCallback) {
	key, _ := resolveAPIKey()
	if key == "" {
		slog.Debug("No Cursor API key; reading the mirror only")
		return
	}
	client := newAPIClient(key)
	agents, err := client.listAgentsFrom(ctx, takeDetectedPage(client), 0)
	if err != nil {
		slog.Warn("Failed to list cloud agents; using the mirror only", "error", err)
		return
	}
	var matched []agentRecord
	for _, record := range agents {
		if agentMatchesOrigin(record.agent, origin) {
			matched = append(matched, record)
		}
	}
	if len(matched) == 0 {
		return
	}
	if err := m.ensureRoot(projectLocal); err != nil {
		slog.Error("Failed to create mirror", "path", m.root, "error", err)
		return
	}
	for i, record := range matched {
		slog.Info("Refreshing cloud agent", "agentId", record.agent.ID, "index", i+1, "total", len(matched))
		if err := m.refreshAgent(ctx, client, record, refreshOptions{now: time.Now()}); err != nil {
			slog.Error("Failed to refresh cloud agent", "agentId", record.agent.ID, "error", err)
		}
		if progress != nil {
			progress(i+1, len(matched))
		}
	}
}

// detectedPage hands the first agent-list page DetectAgent fetched to the
// refresh that follows it. sync calls the two back to back, and at one request
// every 3 seconds listing the same page twice is a visible pause. The page is
// taken at most once, only by a client for the same key and server, and only
// while fresh, so a later refresh in a long-lived process lists again.
var detectedPage struct {
	sync.Mutex
	baseURL string
	key     string
	at      time.Time
	page    *agentPage
}

// detectedPageReuse bounds how old a handed-over page may be.
const detectedPageReuse = 30 * time.Second

func rememberDetectedPage(c *apiClient, page agentPage) {
	detectedPage.Lock()
	defer detectedPage.Unlock()
	detectedPage.baseURL, detectedPage.key = c.baseURL, c.key
	detectedPage.at = time.Now()
	detectedPage.page = &page
}

// takeDetectedPage returns the remembered page for c and forgets it, or nil
// when there is none, it is stale, or it came from another key or server.
func takeDetectedPage(c *apiClient) *agentPage {
	detectedPage.Lock()
	defer detectedPage.Unlock()
	page := detectedPage.page
	detectedPage.page = nil
	sameClient := detectedPage.baseURL == c.baseURL && detectedPage.key == c.key
	if page == nil || !sameClient || time.Since(detectedPage.at) > detectedPageReuse {
		return nil
	}
	return page
}

// matchingSnapshots loads every mirrored agent whose repository is origin.
func (m mirror) matchingSnapshots(origin string) []*agentSnapshot {
	dirs, err := m.agentDirs()
	if err != nil {
		slog.Warn("Failed to read mirror", "path", m.root, "error", err)
		return nil
	}
	var snaps []*agentSnapshot
	for _, dir := range dirs {
		snap, err := loadAgentSnapshot(dir)
		if err != nil {
			slog.Warn("Skipping unreadable mirrored agent", "path", dir, "error", err)
			continue
		}
		if snap != nil && agentMatchesOrigin(snap.agent, origin) {
			snaps = append(snaps, snap)
		}
	}
	return snaps
}

// GetAgentChatSession refreshes and returns one agent when it belongs to this
// project, else nil. Membership is checked because the agent list is global
// to the key: an id alone could name another repository's agent.
func (p *Provider) GetAgentChatSession(projectPath string, sessionID string, debugRaw bool) (*spi.AgentChatSession, error) {
	if !validAgentID(sessionID) {
		return nil, nil
	}
	origin := projectOrigin(projectPath)
	if origin == "" {
		return nil, nil
	}
	m, projectLocal := mirrorFor(projectPath)

	if key, _ := resolveAPIKey(); key != "" {
		ctx := context.Background()
		client := newAPIClient(key)
		record, err := client.getAgent(ctx, sessionID)
		switch {
		case isNotFound(err):
			return nil, nil
		case err != nil:
			slog.Warn("GetAgentChatSession: fetch failed; using the mirror", "sessionId", sessionID, "error", err)
		case !agentMatchesOrigin(record.agent, origin):
			return nil, nil
		default:
			if err := m.ensureRoot(projectLocal); err != nil {
				return nil, err
			}
			if err := m.refreshAgent(ctx, client, record, refreshOptions{now: time.Now()}); err != nil {
				slog.Error("GetAgentChatSession: refresh failed", "sessionId", sessionID, "error", err)
			}
		}
	}

	snap, err := loadAgentSnapshot(m.agentDir(sessionID))
	if err != nil {
		return nil, err
	}
	if snap == nil || !agentMatchesOrigin(snap.agent, origin) {
		return nil, nil
	}
	return convertSnapshot(snap, projectPath, debugRaw), nil
}

// GetAgentChatSessionByPath reads the agent whose agent.json is nativePath.
func (p *Provider) GetAgentChatSessionByPath(nativePath string, originCwd string, debugRaw bool) (*spi.AgentChatSession, error) {
	snap, err := loadAgentSnapshot(filepath.Dir(nativePath))
	if err != nil || snap == nil {
		return nil, err
	}
	return convertSnapshot(snap, originCwd, debugRaw), nil
}

// convertSnapshot builds the session, its RawData and, when asked, the
// native debug export, all from one snapshot.
func convertSnapshot(snap *agentSnapshot, projectPath string, debugRaw bool) *spi.AgentChatSession {
	data, _ := buildSessionData(snap, workspaceRootFor(projectPath))
	if data == nil {
		slog.Debug("Skipping cloud agent with no recorded prompt yet", "agentId", snap.agent.ID)
		return nil
	}
	raw, err := buildRawData(snap)
	if err != nil {
		slog.Error("Failed to build raw data", "sessionId", snap.agent.ID, "error", err)
		return nil
	}
	if debugRaw {
		if err := writeDebugOutput(snap); err != nil {
			slog.Warn("Failed to write debug output", "sessionId", snap.agent.ID, "path", spi.GetDebugDir(snap.agent.ID), "error", err)
		}
	}
	return &spi.AgentChatSession{
		SessionID:   snap.agent.ID,
		CreatedAt:   snap.agent.CreatedAt,
		Slug:        data.Slug,
		SessionData: data,
		RawData:     raw,
	}
}

// ListAgentChatSessions lists this project's mirrored agents without any
// network access.
func (p *Provider) ListAgentChatSessions(projectPath string) ([]spi.SessionMetadata, error) {
	origin := projectOrigin(projectPath)
	result := []spi.SessionMetadata{}
	if origin == "" {
		return result, nil
	}
	m, _ := mirrorFor(projectPath)
	for _, snap := range m.matchingSnapshots(origin) {
		data, firstPrompt := buildSessionData(snap, workspaceRootFor(projectPath))
		if data == nil {
			continue
		}
		result = append(result, spi.SessionMetadata{
			SessionID: snap.agent.ID,
			CreatedAt: snap.agent.CreatedAt,
			Slug:      data.Slug,
			Name:      sessionName(snap.agent, firstPrompt),
		})
	}
	return result, nil
}

// ListAllAgentChatSessions enumerates the mirror reindex can locate.
// OriginCwd stays empty: the API records a repository, never a directory.
// OriginRepoURL carries that repository instead, so reindex attributes the
// session to the project whose git_id it hashes to and still indexes its text.
func (p *Provider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	m := globalMirror()
	refs := []spi.GlobalSessionRef{}
	if m.root == "" {
		return refs, nil
	}
	dirs, err := m.agentDirs()
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		snap, err := loadAgentSnapshot(dir)
		if err != nil {
			slog.Warn("reindex: skipping unreadable mirrored agent", "path", dir, "error", err)
			continue
		}
		if snap == nil {
			continue
		}
		data, firstPrompt := buildSessionData(snap, workspaceRootFor(""))
		if data == nil {
			continue
		}
		refs = append(refs, spi.GlobalSessionRef{
			SessionID:     snap.agent.ID,
			CreatedAt:     snap.agent.CreatedAt,
			Slug:          data.Slug,
			Name:          sessionName(snap.agent, firstPrompt),
			NativePath:    filepath.Join(dir, "agent.json"),
			OriginRepoURL: originRepoURL(snap.agent),
			Fingerprint:   agentDirFingerprint(dir),
		})
	}
	return refs, nil
}

// originRepoURL is the agent's repository. Every observed agent has exactly
// one; for an agent with several, the first is used.
func originRepoURL(agent apiAgent) string {
	if len(agent.Repos) == 0 {
		return ""
	}
	return agent.Repos[0].URL
}

// runDebugFileRe matches the per-run debug files this provider writes.
var runDebugFileRe = regexp.MustCompile(`^run-[1-9][0-9]*-run-[0-9A-Fa-f-]+\.json$`)

// ownsDebugFile identifies provider-owned debug files, so a refresh removes
// stale ones (a run file from a longer earlier export) and leaves the
// CLI-owned session-data.json alone.
func ownsDebugFile(name string) bool {
	switch name {
	case "agent.json", "conversation.json", "usage.json":
		return true
	}
	return runDebugFileRe.MatchString(name)
}

// writeDebugOutput exports the snapshot's native objects pretty-printed:
// agent.json, conversation.json, usage.json and run-<n>-<runId>.json holding
// the run object and its events, numbered in run order.
func writeDebugOutput(snap *agentSnapshot) error {
	dir := spi.GetDebugDir(snap.agent.ID)
	if err := spi.PrepareDebugDir(dir, ownsDebugFile); err != nil {
		return err
	}
	write := func(name string, raw []byte) error {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			return fmt.Errorf("format debug file %s: %w", name, err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write debug file %s: %w", path, err)
		}
		return nil
	}
	if err := write("agent.json", snap.agentRaw); err != nil {
		return err
	}
	if len(snap.conversationRaw) > 0 {
		if err := write("conversation.json", snap.conversationRaw); err != nil {
			return err
		}
	}
	if len(snap.usageRaw) > 0 {
		if err := write("usage.json", snap.usageRaw); err != nil {
			return err
		}
	}
	for i, run := range snap.runs {
		events := run.events
		if events == nil {
			events = []sseEvent{}
		}
		raw, err := json.Marshal(rawRun{Run: run.runRaw, Events: events})
		if err != nil {
			return err
		}
		if err := write(fmt.Sprintf("run-%d-%s.json", i+1, run.run.ID), raw); err != nil {
			return err
		}
	}
	return nil
}

// noProjectOriginError explains why watch cannot start in a directory with no
// git origin.
func noProjectOriginError(projectPath string) error {
	return fmt.Errorf("cursor cloud agents are matched by repository, and %s has no git origin remote", strings.TrimSpace(projectPath))
}
