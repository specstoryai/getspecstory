package cloud

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Cloud-resume client. These methods surface SpecStory Cloud sessions/projects for
// the blended resume browser and fetch a session's SessionData blob for cross-machine resume.
// They mirror the request/auth conventions of skillsAPIRequest (Bearer token, User-Agent,
// {success,data,error} envelope) and are best-effort: callers in the TUI swallow errors and
// degrade to local-only.

const (
	// cloudResumeHTTPTimeout bounds the SessionData blob fetch. Blobs are a few MB at most and
	// resume is interactive/low-frequency, so a normal request timeout is plenty.
	cloudResumeHTTPTimeout = 30 * time.Second

	// sessionDataMediaType is the Accept value that selects the SessionData blob variant of the
	// shared GET session route (server-side: SESSION_DATA_MEDIA_TYPE), rather than markdown/JSON.
	sessionDataMediaType = "application/vnd.specstory.session-data+json"
)

// CloudSessionMetadata is the subset of a cloud session's metadata the resume browser needs:
// the agent (display name), and the machine identity for the machine filter.
type CloudSessionMetadata struct {
	AgentName   string `json:"agentName"`   // human name, e.g. "Claude Code" (not the provider id)
	DeviceID    string `json:"deviceId"`    // stable machine id; sessions group/filter by this
	MachineName string `json:"machineName"` // human machine label, e.g. "Mac-Studio.local"
	Title       string `json:"title"`       // readable prompt title (first user message); "" for pre-title blobs
}

// CloudSession is a cloud-resumable session summary (server type: SessionSummary). Only the
// fields the browser + dedup + resume need are decoded. (The server's firstExchangeContent /
// exchangeCount are intentionally omitted: the ?resumable=true list takes a lean path that never
// populates them, so decoding them would only carry guaranteed-empty dead weight.)
type CloudSession struct {
	ID        string `json:"id"`        // internal session uuid (cloud-side)
	ClientID  string `json:"clientId"`  // native session id (== local session_id)
	ProjectID string `json:"projectId"` // git_id / workspace_id
	// ProjectName is the workspace's display name. Returned by the search endpoint (which shows a
	// project column in cross-project results); the per-project list omits it (context supplies it).
	ProjectName     string `json:"projectName"`
	Name            string `json:"name"`
	UserTitle       string `json:"userTitle,omitempty"`
	MarkdownSize    int    `json:"markdownSize"`
	SessionDataSize int    `json:"sessionDataSize"` // > 0 (list is ?resumable), 0 if absent
	CreatedAt       string `json:"createdAt"`       // row created_at (sync time)
	UpdatedAt       string `json:"updatedAt"`       // row updated_at (sync time)
	// StartedAt/EndedAt are the session's real activity times (first/last exchange), set by the
	// server's duration worker. Empty until it has processed the session. Preferred over the sync
	// timestamps above for display + recency sort so a cloud row reflects when it actually happened.
	StartedAt string               `json:"startedAt"`
	EndedAt   string               `json:"endedAt"`
	Metadata  CloudSessionMetadata `json:"metadata"`

	// Owner attribution (Teams). A team-shared project's listings union your own rows with
	// teammates' copies, so these say whose row this is. Absent on an older cloud deployment,
	// and OwnerName/OwnerEmail are empty when the owner's profile has not been cached yet —
	// so never branch on OwnerID alone, use ownerLabel() in pkg/cmd.
	OwnerID    string `json:"ownerId"`
	OwnerName  string `json:"ownerName"`
	OwnerEmail string `json:"ownerEmail"`
}

// CloudProject is a project (workspace) known to the cloud (server type: Project). Used to blend
// cloud-only projects into the all-projects browser.
//
// Field notes, verified against the live GET /api/v1/projects response:
//   - id IS the project_id (git_id/workspace_id) — handleListProjects remaps `id := projectId`
//     (its `{ ...workspace, id: projectId }` override wins), and `projectId` itself is absent on
//     the wire. So dedup-by-project_id keys on ID.
//   - lastUpdated is the real recency signal (last session activity). The response also carries
//     createdAt/updatedAt, but those are server-defaulted to "now" per request — NOT real
//     timestamps — so blended recency sort must use LastUpdated. (Under ?resumable=true the server
//     now derives lastUpdated from per-session ended_at/started_at, but the CLI's rollup
//     re-derives activity per session anyway, so this field is informational only.)
//   - sessions is populated only when the request passes ?resumable=true: metadata summaries of
//     the project's resumable sessions, inlined in the project object (NOT the SessionData
//     blobs). Each entry carries agentName/machineName/deviceId + real activity times, which the
//     CLI groups by project and merges with its local index to produce accurate per-agent chips
//     and dates. Absent on the web-app's ungated listing.
type CloudProject struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Icon         string         `json:"icon"`
	Color        string         `json:"color"`
	LastUpdated  string         `json:"lastUpdated"`
	SessionCount int            `json:"sessionCount"`
	Sessions     []CloudSession `json:"sessions"` // populated only with ?resumable=true

	// Owner attribution (Teams). A team-shared project's listings union your own rows with
	// teammates' copies, so these say whose row this is. Absent on an older cloud deployment,
	// and OwnerName/OwnerEmail are empty when the owner's profile has not been cached yet —
	// so never branch on OwnerID alone, use ownerLabel() in pkg/cmd.
	OwnerID    string `json:"ownerId"`
	OwnerName  string `json:"ownerName"`
	OwnerEmail string `json:"ownerEmail"`

	// Team members holding their own copy of this repo (including you), collapsed into this one
	// entry by the server. 1 (or 0 on an older deployment) means only you have synced it.
	ContributorCount int `json:"contributorCount"`
}

// ResumeEligibility reports whether cross-machine cloud resume is available to the user, for
// the TUI's soft gate + nudge. This is a NETWORK call (fetches entitlement) and MUST run
// off the UI thread. Interpret the result: !loggedIn → "log in" nudge; loggedIn && !pro →
// "upgrade" nudge; both true → fetch cloud sessions. An entitlement-fetch failure degrades to
// not-pro (silent), never blocking local-first.
func ResumeEligibility() (loggedIn bool, pro bool) {
	if !IsAuthenticated() {
		return false, false
	}
	ent, err := GetEntitlement()
	if err != nil || ent == nil {
		return true, false
	}
	return true, ent.Features.Resume
}

// ListCloudSessions returns the cloud-resumable sessions for a project — those with a
// SessionData blob (?resumable=true), newest-first. The server returns the complete set
// (paged internally on its side), so no limit is sent and a session missing from the
// result genuinely isn't resumable in that project.
func ListCloudSessions(projectID string) ([]CloudSession, error) {
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Sessions []CloudSession `json:"sessions"`
		} `json:"data"`
		Error string `json:"error"`
	}

	path := fmt.Sprintf(
		"/api/v1/projects/%s/sessions?resumable=true",
		url.PathEscape(projectID),
	)
	if err := skillsAPIRequest(http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("list cloud sessions failed: %s", resp.Error)
	}
	return resp.Data.Sessions, nil
}

// CloudSearchHit is one cloud-resume search result: a resumable session plus a highlighted snippet
// from its first matching exchange. The snippet uses the same STX/ETX markers as local search, so
// the TUI renders local and cloud hits identically.
type CloudSearchHit struct {
	CloudSession
	Snippet string `json:"snippet"`
}

// SearchCloudSessions runs the aligned cloud-resume search for a query and returns every
// matching resumable session (newest first, uncapped), each with a highlighted snippet. The
// server mirrors local search semantics, so results blend with local hits coherently.
func SearchCloudSessions(query, projectID string) ([]CloudSearchHit, error) {
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Results []CloudSearchHit `json:"results"`
		} `json:"data"`
		Error string `json:"error"`
	}

	reqBody := map[string]string{"query": query}
	// Scope to one project server-side when the caller is the project-scoped list search; omitted
	// (all projects) for the global cross-project search.
	if projectID != "" {
		reqBody["projectId"] = projectID
	}
	if err := skillsAPIRequest(http.MethodPost, "/api/v1/search/resume", reqBody, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("cloud search failed: %s", resp.Error)
	}
	return resp.Data.Results, nil
}

// ListCloudProjects returns all projects the user has in the cloud, for blending cloud-only
// projects into the all-projects browser. It passes ?resumable=true so each project carries its
// resumable session summaries inline; the CLI groups those summaries by project and merges with
// its local index to produce accurate per-agent chips + real activity dates. Projects with no
// resumable sessions are dropped server-side, so every returned project has Sessions len > 0.
func ListCloudProjects() ([]CloudProject, error) {
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Projects []CloudProject `json:"projects"`
		} `json:"data"`
		Error string `json:"error"`
	}

	if err := skillsAPIRequest(http.MethodGet, "/api/v1/projects?resumable=true", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("list cloud projects failed: %s", resp.Error)
	}
	return resp.Data.Projects, nil
}

// DeleteCloudSession permanently deletes a session from SpecStory Cloud (across all the user's
// machines). Owner-validated server-side.
func DeleteCloudSession(projectID, sessionID string) error {
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	path := "/api/v1/projects/" + url.PathEscape(projectID) + "/sessions/" + url.PathEscape(sessionID)
	if err := skillsAPIRequest(http.MethodDelete, path, nil, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("delete cloud session failed: %s", resp.Error)
	}
	return nil
}

// DeleteCloudProject permanently deletes a project and all its sessions from SpecStory Cloud.
// Owner-validated server-side.
func DeleteCloudProject(projectID string) error {
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	path := "/api/v1/projects/" + url.PathEscape(projectID)
	if err := skillsAPIRequest(http.MethodDelete, path, nil, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("delete cloud project failed: %s", resp.Error)
	}
	return nil
}

// FetchSessionData streams a session's SessionData blob and returns the raw JSON bytes.
// It does NOT unmarshal into schema.SessionData — the resume layer does that (and checks
// schemaVersion), keeping this package decoupled from the schema package, exactly as the sync
// push treats SessionData as an opaque string. Uses a custom request (not skillsAPIRequest)
// because it needs the SessionData Accept header and a raw-bytes body, not JSON decoding.
func FetchSessionData(projectID, sessionID string) ([]byte, error) {
	apiURL := GetAPIBaseURL() + "/api/v1/projects/" +
		url.PathEscape(projectID) + "/sessions/" + url.PathEscape(sessionID)

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating session-data request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+GetCloudToken())
	req.Header.Set("Accept", sessionDataMediaType)
	req.Header.Set("User-Agent", GetUserAgent())

	client := &http.Client{Timeout: cloudResumeHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch session data failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading session data response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, &ErrAuthenticationFailed{Message: "not authenticated to SpecStory Cloud"}
	case resp.StatusCode == http.StatusForbidden:
		// The requireFeature("resume") gate — the user isn't on a qualifying plan.
		return nil, &APIError{StatusCode: resp.StatusCode, Message: "Resuming cloud sessions requires SpecStory Pro."}
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("session data not found in cloud")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("fetch session data returned status %d", resp.StatusCode)
	}

	return body, nil
}
