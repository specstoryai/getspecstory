package cursorcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Network constants.
const (
	defaultAPIBaseURL = "https://api.cursor.com"
	jsonTimeout       = 30 * time.Second
	maxRetries        = 4
	maxBackoff        = 60 * time.Second
	// listPageSize is the API's maximum page size for GET /v1/agents.
	listPageSize = 100
)

// initialBackoff is the first retry delay after a 5xx, 429 or transport
// failure, doubling up to maxBackoff. A variable so tests can shorten it.
var initialBackoff = 5 * time.Second

// apiBaseURL is a variable so tests can point the client at an httptest server.
var apiBaseURL = defaultAPIBaseURL

// streamIdleTimeout bounds how long a stream read may see no bytes before the
// connection is treated as dead. The live stream sends a heartbeat event while
// the agent is quiet, so 90 seconds of silence means the connection is gone.
// A variable so tests can shorten it.
var streamIdleTimeout = 90 * time.Second

// errStreamExpired reports a 410 from the run stream endpoint: the stream is
// retained for 24 hours after a run ends and is gone afterwards.
var errStreamExpired = errors.New("run stream expired")

// apiError is a non-success HTTP response from the Cloud Agents API, carrying
// the error envelope the API returns ({"error":{"code","message"}}).
type apiError struct {
	Status  int
	Code    string
	Message string
}

// permanentAPIError reports a 4xx other than 429: the API refused the request
// itself, so repeating it with the same inputs cannot succeed. Every other
// failure (5xx, throttling, transport) may pass on a later attempt.
func permanentAPIError(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != http.StatusTooManyRequests
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("cursor api: HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("cursor api: HTTP %d", e.Status)
}

// isNotFound reports whether err is a 404 from the API.
func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// throttle holds every caller in the process back once the API pushes back.
// Requests are not paced in advance. The API documents 20 requests per minute,
// but probing it on 2026-10-15 at up to 17 requests per second drew no 429,
// far above anything the CLI sends one request at a time, so pacing only made
// syncs slow. A 429, 5xx or transport failure seen by one caller holds all of
// them (sync, watch polling and live streams) because the limit is per key.
type throttle struct {
	mu    sync.Mutex
	until time.Time
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

func newThrottle() *throttle {
	return &throttle{now: time.Now, sleep: sleepContext}
}

// wait blocks while a hold is in effect.
func (l *throttle) wait(ctx context.Context) error {
	l.mu.Lock()
	d := l.until.Sub(l.now())
	l.mu.Unlock()
	if d > 0 {
		return l.sleep(ctx, d)
	}
	return nil
}

// holdUntil keeps every caller waiting until at least t.
func (l *throttle) holdUntil(t time.Time) {
	l.mu.Lock()
	if l.until.Before(t) {
		l.until = t
	}
	l.mu.Unlock()
}

// sleepContext sleeps for d or until ctx is done.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// sharedThrottle is the process-wide hold shared by every client.
var sharedThrottle = newThrottle()

// apiClient talks to the Cloud Agents API with one key.
type apiClient struct {
	key        string
	baseURL    string
	jsonHTTP   *http.Client
	streamHTTP *http.Client
	throttle   *throttle
}

// newAPIClient builds a client for key. JSON calls carry a whole-request
// timeout; stream calls cannot (a live run streams for minutes), so they rely
// on the idle deadline in openStream for the body and on streamTransport for
// the headers.
func newAPIClient(key string) *apiClient {
	return &apiClient{
		key:        key,
		baseURL:    apiBaseURL,
		jsonHTTP:   &http.Client{Timeout: jsonTimeout},
		streamHTTP: &http.Client{Transport: streamTransport()},
		throttle:   sharedThrottle,
	}
}

// streamTransport is the default transport with a response-header deadline. The
// idle reader only guards the body after Do returns, so without this a server
// that accepts the connection and never answers would hang a replay forever.
func streamTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = jsonTimeout
	return tr
}

// do sends one request built by build, waiting out any hold first and
// retrying 429 responses after the server's requested delay or exponential
// backoff. A GET also retries 5xx responses and transport failures (DNS,
// connection reset, timeout) with the same backoff, because a sync that gave up
// on one agent over a blip would leave it stale until the next run. Any other
// method gets only the 429 retry: a launch that failed in flight may still have
// started a paid agent, and sending it again could start a second. A cancelled
// context is never retried. A 2xx response is returned open; any other status
// is returned as *apiError.
func (c *apiClient) do(ctx context.Context, httpClient *http.Client, build func(ctx context.Context) (*http.Request, error)) (*http.Response, error) {
	backoff := initialBackoff
	for attempt := 0; ; attempt++ {
		if err := c.throttle.wait(ctx); err != nil {
			return nil, err
		}
		req, err := build(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		idempotent := req.Method == http.MethodGet
		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil || !idempotent || attempt >= maxRetries {
				return nil, err
			}
			delay := backoff
			backoff = min(backoff*2, maxBackoff)
			slog.Warn("Cursor API request failed, retrying",
				"path", req.URL.Path, "error", err, "delay", delay, "attempt", attempt+1)
			c.throttle.holdUntil(c.throttle.now().Add(delay))
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		apiErr := readAPIError(resp)
		retryable := resp.StatusCode == http.StatusTooManyRequests || (idempotent && resp.StatusCode >= 500)
		if !retryable || attempt >= maxRetries {
			return nil, apiErr
		}

		delay := retryAfter(resp.Header, c.throttle.now())
		if delay <= 0 {
			delay = backoff
			backoff = min(backoff*2, maxBackoff)
		}
		slog.Warn("Cursor API request throttled or failed, retrying",
			"path", req.URL.Path, "status", resp.StatusCode, "delay", delay, "attempt", attempt+1)
		c.throttle.holdUntil(c.throttle.now().Add(delay))
	}
}

// readAPIError drains and closes resp, decoding the API's error envelope.
func readAPIError(resp *http.Response) *apiError {
	defer func() { _ = resp.Body.Close() }()
	apiErr := &apiError{Status: resp.StatusCode}
	// The error envelope is small; the cap only guards against a proxy page.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
	}
	return apiErr
}

// retryAfter returns how long the server asked the client to wait: Retry-After
// (delay-seconds or an HTTP date), else X-RateLimit-Reset, else zero. The
// OpenAPI document promises both headers on a 429 without saying whether the
// reset is seconds from now or a Unix time, so a value too large to be a delay
// is read as a Unix time. The delay is capped at maxBackoff: holdUntil pauses
// every caller in the process, so an outsized value (or a skewed clock) would
// otherwise stall sync and watch for as long as the header says. Retrying
// early costs one more 429 at most.
func retryAfter(h http.Header, now time.Time) time.Duration {
	var delay time.Duration
	if header := h.Get("Retry-After"); header != "" {
		if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
			delay = time.Duration(secs) * time.Second
		} else if t, err := http.ParseTime(header); err == nil {
			delay = t.Sub(now)
		}
	} else if secs, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && secs > 0 {
		if secs > unixTimeThreshold {
			delay = time.Unix(secs, 0).Sub(now)
		} else {
			delay = time.Duration(secs) * time.Second
		}
	}
	return min(delay, maxBackoff)
}

// unixTimeThreshold separates a reset given as seconds from now from one given
// as a Unix time: no server asks for a delay of a billion seconds.
const unixTimeThreshold = 1_000_000_000

// getJSON fetches path and returns the raw response body. Raw bytes are kept
// rather than a typed decode so the mirror preserves fields this provider does
// not know about.
func (c *apiClient) getJSON(ctx context.Context, path string) (json.RawMessage, error) {
	resp, err := c.do(ctx, c.jsonHTTP, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err == nil {
			req.Header.Set("Accept", "application/json")
		}
		return req, err
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("read %s: response is not JSON", path)
	}
	return json.RawMessage(body), nil
}

// me probes the key's identity; Check uses it as the installation test.
func (c *apiClient) me(ctx context.Context) (apiMe, error) {
	raw, err := c.getJSON(ctx, "/v1/me")
	if err != nil {
		return apiMe{}, err
	}
	var me apiMe
	if err := json.Unmarshal(raw, &me); err != nil {
		return apiMe{}, fmt.Errorf("decode /v1/me: %w", err)
	}
	return me, nil
}

// agentPage is one page of GET /v1/agents.
type agentPage struct {
	records    []agentRecord
	nextCursor string
}

// launchRequest is the body of POST /v1/agents.
type launchRequest struct {
	Name   string       `json:"name,omitempty"`
	Prompt launchPrompt `json:"prompt"`
	Repos  []launchRepo `json:"repos"`
}

type launchPrompt struct {
	Text string `json:"text"`
}

type launchRepo struct {
	URL string `json:"url"`
	// StartingRef is the branch the agent starts from; empty means the
	// repository's default branch.
	StartingRef string `json:"startingRef,omitempty"`
}

// launchAgent creates a cloud agent, which starts its first (paid) run at once.
func (c *apiClient) launchAgent(ctx context.Context, launch launchRequest) (apiAgent, error) {
	body, err := json.Marshal(launch)
	if err != nil {
		return apiAgent{}, err
	}
	resp, err := c.do(ctx, c.jsonHTTP, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/agents", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Accept", "application/json")
			req.Header.Set("Content-Type", "application/json")
		}
		return req, err
	})
	if err != nil {
		return apiAgent{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return apiAgent{}, fmt.Errorf("read launch response: %w", err)
	}
	// The response carries the new agent and its first run.
	var created struct {
		Agent apiAgent `json:"agent"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return apiAgent{}, fmt.Errorf("decode launch response: %w", err)
	}
	agent := created.Agent
	if !validAgentID(agent.ID) {
		return apiAgent{}, fmt.Errorf("launch response has no valid agent id (%q)", agent.ID)
	}
	return agent, nil
}

// unarchiveAgent restores an archived agent so it can take follow-up runs.
func (c *apiClient) unarchiveAgent(ctx context.Context, agentID string) error {
	resp, err := c.do(ctx, c.jsonHTTP, func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/agents/"+url.PathEscape(agentID)+"/unarchive", nil)
	})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// listAgents returns every agent the key can see, newest first, following
// nextCursor. maxPages bounds the walk; zero means no bound.
func (c *apiClient) listAgents(ctx context.Context, maxPages int) ([]agentRecord, error) {
	var records []agentRecord
	cursor := ""
	for page := 0; maxPages == 0 || page < maxPages; page++ {
		current, err := c.listAgentPage(ctx, cursor)
		if err != nil {
			return nil, err
		}
		records = append(records, current.records...)
		if current.nextCursor == "" {
			break
		}
		cursor = current.nextCursor
	}
	return records, nil
}

// listAgentPage fetches the page of agents that starts at cursor ("" for the
// first page).
func (c *apiClient) listAgentPage(ctx context.Context, cursor string) (agentPage, error) {
	query := url.Values{"limit": {strconv.Itoa(listPageSize)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	raw, err := c.getJSON(ctx, "/v1/agents?"+query.Encode())
	if err != nil {
		return agentPage{}, err
	}
	var list struct {
		Items      []json.RawMessage `json:"items"`
		NextCursor string            `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return agentPage{}, fmt.Errorf("decode agent list: %w", err)
	}
	page := agentPage{nextCursor: list.NextCursor}
	for _, item := range list.Items {
		record, err := decodeAgent(item)
		if err != nil {
			slog.Warn("Skipping undecodable agent in list", "error", err)
			continue
		}
		page.records = append(page.records, record)
	}
	return page, nil
}

// getAgent fetches one agent.
func (c *apiClient) getAgent(ctx context.Context, agentID string) (agentRecord, error) {
	raw, err := c.getJSON(ctx, "/v1/agents/"+url.PathEscape(agentID))
	if err != nil {
		return agentRecord{}, err
	}
	return decodeAgent(raw)
}

// listRuns returns every run of an agent, following nextCursor when present.
func (c *apiClient) listRuns(ctx context.Context, agentID string) ([]runRecord, error) {
	var records []runRecord
	cursor := ""
	for {
		path := "/v1/agents/" + url.PathEscape(agentID) + "/runs"
		if cursor != "" {
			path += "?" + url.Values{"cursor": {cursor}}.Encode()
		}
		raw, err := c.getJSON(ctx, path)
		if err != nil {
			return nil, err
		}
		var list struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decode run list: %w", err)
		}
		for _, item := range list.Items {
			record, err := decodeRun(item)
			if err != nil {
				slog.Warn("Skipping undecodable run in list", "agentId", agentID, "error", err)
				continue
			}
			records = append(records, record)
		}
		if list.NextCursor == "" {
			return records, nil
		}
		cursor = list.NextCursor
	}
}

// conversation fetches the text-only v0 transcript, which outlives the stream.
func (c *apiClient) conversation(ctx context.Context, agentID string) (json.RawMessage, error) {
	return c.getJSON(ctx, "/v0/agents/"+url.PathEscape(agentID)+"/conversation")
}

// usage fetches token and cost usage for an agent.
func (c *apiClient) usage(ctx context.Context, agentID string) (json.RawMessage, error) {
	return c.getJSON(ctx, "/v1/agents/"+url.PathEscape(agentID)+"/usage")
}

// openStream opens a run's SSE stream. lastEventID, when set, resumes after
// that event. The returned body cancels its request once no bytes arrive for
// streamIdleTimeout, so a silently dropped connection surfaces as a read error
// instead of hanging forever. A 410 maps to errStreamExpired.
func (c *apiClient) openStream(ctx context.Context, agentID, runID, lastEventID string) (io.ReadCloser, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	path := "/v1/agents/" + url.PathEscape(agentID) + "/runs/" + url.PathEscape(runID) + "/stream"
	resp, err := c.do(streamCtx, c.streamHTTP, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err == nil {
			req.Header.Set("Accept", "text/event-stream")
			if lastEventID != "" {
				req.Header.Set("Last-Event-ID", lastEventID)
			}
		}
		return req, err
	})
	if err != nil {
		cancel()
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusGone {
			return nil, fmt.Errorf("%w: %s", errStreamExpired, ae.Message)
		}
		return nil, err
	}
	return newIdleReader(resp.Body, cancel, streamIdleTimeout), nil
}

// idleReader cancels its request when no read has returned data for the idle
// period. Cancelling the request context makes the blocked Read return.
type idleReader struct {
	body   io.ReadCloser
	cancel context.CancelFunc
	timer  *time.Timer
	idle   time.Duration
}

func newIdleReader(body io.ReadCloser, cancel context.CancelFunc, idle time.Duration) *idleReader {
	return &idleReader{body: body, cancel: cancel, timer: time.AfterFunc(idle, cancel), idle: idle}
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if n > 0 {
		r.timer.Reset(r.idle)
	}
	return n, err
}

func (r *idleReader) Close() error {
	r.timer.Stop()
	r.cancel()
	return r.body.Close()
}
