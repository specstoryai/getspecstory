package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/cloud"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
)

// Direct session addressing for `specstory resume --session <uri>`.
//
// A session can be chosen interactively in the TUI, or now directly via a session URI. The
// driving use case is the Cloud web app's Resume button, which copies a paste-able command
// carrying exactly the two IDs the cloud API needs (project_id + native session id). The URI
// resolves to the same session identity the picker would produce — including the
// local-preferred guard: a session that also exists locally resumes in place — and feeds the
// identical resumePlan → launchResume machinery, so there is no parallel resume implementation.

// sessionURI is a parsed --session argument: a project id + native session id, plus (for
// pasted web permalinks only) the origin the link points at so resolveSessionURI can refuse
// a host that differs from the configured cloud.
// projectID is empty for the bare-UUID form (resolved local-first, then cloud-discovered).
type sessionURI struct {
	projectID string // "" for a bare session UUID
	sessionID string // normalized (lowercase) native session uuid
	host      string // permalink origin (scheme://host[:port]); "" for specstory:// and bare UUID
}

// Project ids are the CLI-minted truncated-SHA form: xxxx-xxxx-xxxx-xxxx (16 hex chars,
// createHash in pkg/utils/project_identity.go). Session ids are provider-generated UUIDs:
// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx. Both are matched case-insensitively.
var (
	projectIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}$`)
	sessionIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// parseSessionURI parses the three accepted --session forms:
//
//  1. specstory://projects/{projectID}/sessions/{sessionID} — the canonical, host-free form the
//     web app copies. url.Parse puts "projects" in Host for a custom scheme, so host+path
//     are rejoined before matching.
//  2. https://{host}/projects/{projectID}/sessions/{sessionID} — a pasted browser permalink.
//     Trailing slashes are tolerated and any path segments after the session id (e.g.
//     /chats/{chatId}) are ignored, so any session-page URL pastes cleanly.
//  3. {sessionUUID} — bare session id shorthand; resolves local-first then cloud.
//
// ID shapes are validated up front for early, clear errors.
func parseSessionURI(raw string) (sessionURI, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return sessionURI{}, utils.ValidationError{Message: "--session requires a session URI or UUID"}
	}

	// A bare UUID has no scheme delimiter. Detect it before url.Parse (which would treat it as a
	// path and lose nothing, but validating the shape here gives the clearest error).
	if !strings.Contains(raw, "://") && sessionIDRe.MatchString(raw) {
		return sessionURI{sessionID: strings.ToLower(raw)}, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return sessionURI{}, utils.ValidationError{Message: fmt.Sprintf("invalid session URI %q: %v", raw, err)}
	}

	switch u.Scheme {
	case "specstory":
		// For a custom (non-special) scheme, url.Parse lands "projects" in Host and the rest in
		// Path. Rejoin them so the /projects/{id}/sessions/{id} grammar matches the permalink form.
		segs := pathSegments(u.Host + u.Path)
		uri, ok := matchSessionPath(segs)
		if !ok {
			return sessionURI{}, utils.ValidationError{Message: fmt.Sprintf(
				"invalid specstory: URI %q — expected specstory://projects/{projectId}/sessions/{sessionId}", raw)}
		}
		return uri, nil
	case "http", "https":
		segs := pathSegments(u.Path)
		uri, ok := matchSessionPath(segs)
		if !ok {
			return sessionURI{}, utils.ValidationError{Message: fmt.Sprintf(
				"invalid session URL %q — expected /projects/{projectId}/sessions/{sessionId}", raw)}
		}
		uri.host = u.Scheme + "://" + u.Host
		return uri, nil
	default:
		// A bare non-UUID string (or any other scheme): report a shape error rather than silently
		// treating garbage as a session id.
		return sessionURI{}, utils.ValidationError{Message: fmt.Sprintf(
			"unsupported session URI %q — use specstory://projects/{projectId}/sessions/{sessionId}, a cloud permalink, or a session UUID", raw)}
	}
}

// matchSessionPath validates the /projects/{pid}/sessions/{sid}[/…] segment grammar and returns
// the parsed URI (shapes validated, ids lowercased) or ok=false.
func matchSessionPath(segs []string) (sessionURI, bool) {
	if len(segs) < 4 || segs[0] != "projects" || segs[2] != "sessions" {
		return sessionURI{}, false
	}
	pid, sid := segs[1], segs[3]
	if !projectIDRe.MatchString(pid) {
		return sessionURI{}, false
	}
	if !sessionIDRe.MatchString(sid) {
		return sessionURI{}, false
	}
	return sessionURI{
		projectID: strings.ToLower(pid),
		sessionID: strings.ToLower(sid),
	}, true
}

// pathSegments splits a URL path (or rejoined host+path) on "/" and drops empty segments, so
// trailing slashes ("/…/sessions/sid/") and any "/chats/{id}" tail collapse to the same token
// stream as the canonical form.
func pathSegments(s string) []string {
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveSessionURI resolves a --session argument to a sessionindex.Session ready for resume,
// applying the local-first resolution order and the permalink host check:
//
//  1. Local index first: look the session id up across all agents. A hit means an in-place
//     local resume using the row's agent/origin_cwd — the same local-preferred rule the TUI
//     applies when a cloud-badged row is selected. This also makes
//     a pasted CLOUD permalink resume locally (offline) when the session exists on this machine.
//  2. Else cloud, gated by eligibility (login + Pro) surfaced as actionable errors: not logged in → the
//     login message; a 403 → the "requires SpecStory Pro" mapping. With a project id in hand,
//     the session summary is fetched directly; a bare UUID discovers the owning project via the
//     current project's cloud list, then the all-projects ?resumable=true listing.
//  3. Else a "session not found" error naming where it looked.
//
// The cloud summary (not the SessionData blob) supplies the agent/machine attribution the
// resumePlan and TUI need; the blob fetch happens later, once, in prepareCloudResumeTarget.
func resolveSessionURI(
	raw string,
	store *sessionindex.Store,
	agentIDByName map[string]string,
) (*sessionindex.Session, error) {
	uri, err := parseSessionURI(raw)
	if err != nil {
		return nil, err
	}

	// (1) Local-first: a session present on this machine resumes in place, offline. This runs
	// BEFORE the host-mismatch check on purpose — a pasted cloud-dev permalink for a session that
	// exists locally should resume offline without tripping the host-mismatch guard. The check
	// only needs to gate the cloud call below (the token never goes to the permalink's host);
	// a local hit makes no cloud call at all.
	if local, ok, lerr := store.GetSessionByID(uri.sessionID); lerr != nil {
		return nil, fmt.Errorf("looking up session locally: %w", lerr)
	} else if ok {
		return &local, nil
	}

	// A pasted permalink contributes IDs only — the Bearer token never goes to the
	// permalink's host. Now that we know the session isn't local (so a cloud call is required),
	// refuse if the permalink's host differs from the configured cloud host and point at
	// --cloud-url. specstory:// and bare-UUID forms carry no host, so the check is skipped.
	if uri.host != "" {
		configured := cloud.GetAPIBaseURL()
		if !strings.EqualFold(uri.host, configured) {
			return nil, utils.ValidationError{Message: fmt.Sprintf(
				"This link points at %s, but the CLI is configured for %s. Pass --cloud-url %s if this is intentional.",
				uri.host, configured, uri.host)}
		}
	}

	// (2) Cloud. Not-logged-in is surfaced up front; Pro is surfaced via the API's 403.
	if !cloud.IsAuthenticated() {
		return nil, utils.ValidationError{Message: "Log into SpecStory Cloud (specstory login) to resume sessions from your other machines."}
	}

	var (
		cs        *cloud.CloudSession
		projectID string
	)
	if uri.projectID != "" {
		// Direct form: recover the session summary from the named project. The per-project list
		// is capped at 500 (cloudResumeLimit), so a resumable session older than the project's
		// newest 500 misses it — fall back to the uncapped all-projects listing (which embeds
		// every resumable session) on a miss, accepting only a match in the named project.
		cs, err = findCloudSessionInProject(uri.projectID, uri.sessionID)
		if err != nil {
			return nil, mapCloudResumeErr(err)
		}
		projectID = uri.projectID
		if cs == nil {
			cs, projectID, err = findCloudSessionAnywhere(uri.sessionID)
			if err != nil {
				return nil, mapCloudResumeErr(err)
			}
			// The URI's project is authoritative for the direct form. A match in a different
			// project (a near-impossible UUID collision, or a stale/wrong URI) means the session
			// isn't in the named project — treat as not found.
			if cs != nil && projectID != uri.projectID {
				cs = nil
			}
		}
	} else {
		// Bare UUID: the all-projects ?resumable=true listing embeds every resumable session
		// uncapped, including the current project's, so a dedicated current-project probe
		// is redundant — scan all projects directly to discover the owning project.
		cs, projectID, err = findCloudSessionAnywhere(uri.sessionID)
		if err != nil {
			return nil, mapCloudResumeErr(err)
		}
	}

	if cs == nil {
		return nil, utils.ValidationError{Message: fmt.Sprintf(
			"session %s not found locally or in SpecStory Cloud", shortID(uri.sessionID))}
	}

	// Convert the cloud summary to an index row (resolves the agent from its display name).
	rows := cloudToSessions([]cloud.CloudSession{*cs}, agentIDByName)
	if len(rows) == 0 {
		return nil, utils.ValidationError{Message: fmt.Sprintf(
			"session %s is from an agent this CLI doesn't know (%q)", shortID(uri.sessionID), cs.Metadata.AgentName)}
	}
	s := rows[0]
	// The per-project cloud list omits projectId (context supplies it); restore it from the
	// resolved project so the resumePlan and cloud fetch key on the right project.
	if projectID != "" {
		s.ProjectID = projectID
	}
	return &s, nil
}

// findCloudSessionInProject lists a project's resumable cloud sessions and returns the one whose
// native session id (clientId) matches, or nil if none. NOTE: the per-project list is capped at
// cloudResumeLimit (500) by the server, so a resumable session older than the project's newest 500
// misses it — callers that must not miss should fall back to findCloudSessionAnywhere (uncapped).
func findCloudSessionInProject(projectID, sessionID string) (*cloud.CloudSession, error) {
	if projectID == "" {
		return nil, nil
	}
	sessions, err := cloud.ListCloudSessions(projectID)
	if err != nil {
		return nil, err
	}
	return cloudSessionByID(sessions, sessionID), nil
}

// findCloudSessionAnywhere discovers the owning project for a bare session id via the all-projects
// ?resumable=true listing (it embeds every resumable session in one request). Returns the
// matching summary and its project id, or (nil, "", nil) when the id isn't in any project.
func findCloudSessionAnywhere(sessionID string) (*cloud.CloudSession, string, error) {
	projects, err := cloud.ListCloudProjects()
	if err != nil {
		return nil, "", err
	}
	for _, p := range projects {
		if cs := cloudSessionByID(p.Sessions, sessionID); cs != nil {
			// The embedded summaries carry their projectId (derived from the workspace row), so
			// prefer it when present; fall back to the project's own id for safety.
			pid := cs.ProjectID
			if pid == "" {
				pid = p.ID
			}
			return cs, pid, nil
		}
	}
	return nil, "", nil
}

// cloudSessionByID returns a pointer to the first session in cs whose clientId matches id, or nil.
func cloudSessionByID(cs []cloud.CloudSession, id string) *cloud.CloudSession {
	for i := range cs {
		if strings.EqualFold(cs[i].ClientID, id) {
			return &cs[i]
		}
	}
	return nil
}

// mapCloudResumeErr converts cloud API errors from the resolution path into the actionable,
// actionable messages a non-TUI caller prints. A 401 (expired/missing token) → the login nudge;
// a 403 / "upgrade_required" → the Pro message (mirroring FetchSessionData's mapping); anything
// else passes through unwrapped so genuine network/server failures still read clearly.
func mapCloudResumeErr(err error) error {
	if err == nil {
		return nil
	}
	var authErr *cloud.ErrAuthenticationFailed
	if errors.As(err, &authErr) {
		return utils.ValidationError{Message: "Log into SpecStory Cloud (specstory login) to resume sessions from your other machines."}
	}
	if strings.Contains(err.Error(), "upgrade_required") {
		return utils.ValidationError{Message: "Resuming cloud sessions requires SpecStory Pro."}
	}
	return err
}
