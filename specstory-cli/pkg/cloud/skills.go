package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// This file is the CLI's client for SpecStory Cloud's "skills" (lore) surface: the
// machine-readable contract behind both the `specstory skills` TUI and its --json
// subcommands. The same endpoints back the cloud web UI today, so a future front end
// (e.g. the VS Code extension) can drive identical behavior by shelling out to the CLI.
//
// All endpoints are owner-scoped by the bearer token (GetCloudToken) — there is no
// project path segment; the workspace/owner is derived server-side from the JWT.

// skillsHTTPTimeout bounds a single skills API call. Skill bodies (skillMd) ship inline
// in the library listing, so the payload can be larger than a typical metadata call; the
// timeout is generous enough to absorb that without hanging an interactive command.
const skillsHTTPTimeout = 30 * time.Second

// Entitlement is the signed-in user's plan and per-feature access, from
// GET /api/v1/billing/entitlement. Skills require plan "pro" (Features.Skills == true).
type Entitlement struct {
	Plan     string              `json:"plan"`
	Features EntitlementFeatures `json:"features"`
}

// EntitlementFeatures mirrors the server's per-feature boolean flags. Only the fields the
// CLI acts on are modeled; unknown features are ignored by the JSON decoder.
type EntitlementFeatures struct {
	Resume        bool `json:"resume"`
	Skills        bool `json:"skills"`
	KnowledgeBase bool `json:"knowledge_base"`
	WorkTracking  bool `json:"work_tracking"`
	Governance    bool `json:"governance"`
}

// SkillRow is one skill across every lifecycle state, mirroring the cloud's unified
// SkillRow (shared/services/lore/skill_row.ts). Two sources union behind one identity
// (Name): forged skills (Ready/Installed) and pending dossiers (Review).
//
// State drives what actions are valid:
//   - "review"    -> approvable/rejectable (ApproveDossier/DeclineDossier on DossierID)
//   - "ready"     -> installable (download SkillMd, then SetInstallState installed)
//   - "installed" -> already adopted in the cloud library
type SkillRow struct {
	Name           string   `json:"name"`
	State          string   `json:"state"` // "review" | "ready" | "installed"
	Trigger        string   `json:"trigger"`
	Confidence     string   `json:"confidence"`
	Verdict        string   `json:"verdict"`        // source verdict (needs-edits for review, confirmed for ready)
	SkillMd        string   `json:"skillMd"`        // full SKILL.md text, delivered inline (no separate download call)
	ClusterKey     string   `json:"clusterKey"`     // stable cluster identity for re-fetch
	Fingerprint    string   `json:"fingerprint"`    // beats hash; cross-run cache key
	ContentSha     string   `json:"contentSha"`     // forged only — SHA-256 of SkillMd; lets the CLI detect local/cloud drift
	Recommendation string   `json:"recommendation"` // drift badge, forged only
	DossierID      string   `json:"dossierId"`      // review rows -> the approve/reject target
	EvidenceRefs   []string `json:"evidenceRefs"`   //
	CreatedAt      string   `json:"createdAt"`      // when the skill was minted (ISO)
	Kind           string   `json:"kind"`           // "theme" | "corr" | ""
}

// Skill lifecycle states and install-state values, mirroring the cloud enum strings so the
// CLI never hardcodes a literal at a call site.
const (
	SkillStateReview    = "review"
	SkillStateReady     = "ready"
	SkillStateInstalled = "installed"

	InstallStateAvailable = "available"
	InstallStateInstalled = "installed"
)

// GetEntitlement fetches the signed-in user's plan + feature flags. Callers use
// Features.Skills to gate the skills command and craft an upgrade message.
func GetEntitlement() (*Entitlement, error) {
	var resp struct {
		Success bool        `json:"success"`
		Data    Entitlement `json:"data"`
		Error   string      `json:"error"`
	}
	if err := skillsAPIRequest(http.MethodGet, "/api/v1/billing/entitlement", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("entitlement request failed: %s", resp.Error)
	}
	return &resp.Data, nil
}

// ListSkillLibrary returns the unified skill library (review + ready + installed rows).
// Skill content (SkillMd) is included inline on every row, so no follow-up fetch is needed
// to preview or install.
func ListSkillLibrary() ([]SkillRow, error) {
	var resp struct {
		Success bool       `json:"success"`
		Skills  []SkillRow `json:"skills"`
		Error   string     `json:"error"`
	}
	if err := skillsAPIRequest(http.MethodGet, "/api/v1/lore/skills/library", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("listing skills failed: %s", resp.Error)
	}
	return resp.Skills, nil
}

// ApproveDossier approves a borderline (review-state) skill, which forges it into the
// library (it becomes Ready). dossierID is SkillRow.DossierID.
func ApproveDossier(dossierID string) error {
	return patchDossier(dossierID, "approve", "")
}

// DeclineDossier rejects a borderline (review-state) skill. An optional note records why.
func DeclineDossier(dossierID, note string) error {
	return patchDossier(dossierID, "decline", note)
}

// patchDossier is the shared body of approve/reject — PATCH /api/v1/lore/dossiers/:id.
func patchDossier(dossierID, action, note string) error {
	if dossierID == "" {
		return fmt.Errorf("cannot %s: skill has no dossier id (only review-state skills are %s-able)", action, action)
	}
	body := map[string]string{"action": action}
	if note != "" {
		body["note"] = note
	}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	path := "/api/v1/lore/dossiers/" + url.PathEscape(dossierID)
	if err := skillsAPIRequest(http.MethodPatch, path, body, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s failed: %s", action, resp.Error)
	}
	return nil
}

// SetInstallState flips a forged skill's cloud install state (available <-> installed).
// The CLI calls this after a successful local disk install/uninstall so the cloud library
// reflects what is actually on the user's machine. state must be one of InstallState*.
func SetInstallState(name, state string) error {
	if name == "" {
		return fmt.Errorf("cannot set install state: missing skill name")
	}
	body := map[string]string{"install_state": state}
	var resp struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	path := "/api/v1/lore/skills/" + url.PathEscape(name)
	if err := skillsAPIRequest(http.MethodPatch, path, body, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("setting install state to %q failed: %s", state, resp.Error)
	}
	return nil
}

// skillsAPIRequest performs an authenticated JSON request to the cloud skills surface and
// decodes the response into out. It centralizes the auth header, envelope handling, and
// 401 -> ErrAuthenticationFailed mapping shared by every skills call (the rest of the
// package builds requests inline per-call; these endpoints are new and uniform, so a small
// shared helper keeps them DRY). reqBody is JSON-marshaled when non-nil.
func skillsAPIRequest(method, path string, reqBody any, out any) error {
	apiURL := GetAPIBaseURL() + path

	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		bodyReader = bytes.NewBuffer(data)
	}

	req, err := http.NewRequest(method, apiURL, bodyReader)
	if err != nil {
		return fmt.Errorf("creating %s request: %w", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+GetCloudToken())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", GetUserAgent())
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: skillsHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s failed: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	// A 401 is surfaced as the package's typed auth error so callers can prompt re-login
	// the same way the rest of the cloud code does.
	if resp.StatusCode == http.StatusUnauthorized {
		slog.Debug("skills API unauthorized", "path", path, "status", resp.StatusCode)
		return &ErrAuthenticationFailed{Message: "not authenticated to SpecStory Cloud"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Debug("skills API non-2xx", "path", path, "status", resp.StatusCode, "body", string(respBody))
		return fmt.Errorf("%s %s returned status %d", method, path, resp.StatusCode)
	}

	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("parsing response from %s: %w", path, err)
		}
	}
	return nil
}
