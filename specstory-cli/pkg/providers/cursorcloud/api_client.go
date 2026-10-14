package cursorcloud

import (
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

// Network constants. The request interval keeps every caller inside the
// documented 20-requests-per-minute limit of the Cloud Agents API; the API
// returns no rate-limit headers to pace against, so the client paces itself.
const (
	defaultAPIBaseURL = "https://api.cursor.com"
	requestInterval   = 3 * time.Second
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

// rateLimiter spaces requests at least interval apart across every caller in
// the process: sync, watch polling and live streams all draw from one budget
// because the API limit is per key, not per connection.
type rateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
}

func newRateLimiter(interval time.Duration) *rateLimiter {
	return &rateLimiter{interval: interval, now: time.Now, sleep: sleepContext}
}

// wait blocks until the caller may send its request, reserving the slot before
// sleeping so concurrent callers queue behind each other instead of all waking
// at the same instant.
func (l *rateLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	slot := l.next
	if slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	if d := slot.Sub(now); d > 0 {
		return l.sleep(ctx, d)
	}
	return nil
}

// holdUntil pushes every future slot to at least t, so a 429 seen by one
// caller also slows the others rather than letting them hit the limit again.
func (l *rateLimiter) holdUntil(t time.Time) {
	l.mu.Lock()
	if l.next.Before(t) {
		l.next = t
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

// sharedLimiter is the process-wide request budget.
var sharedLimiter = newRateLimiter(requestInterval)

// apiClient talks to the Cloud Agents API with one key.
type apiClient struct {
	key        string
	baseURL    string
	jsonHTTP   *http.Client
	streamHTTP *http.Client
	limiter    *rateLimiter
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
		limiter:    sharedLimiter,
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

// do sends one request built by build, waiting for the rate limiter first and
// retrying 429 and 5xx responses with Retry-After or exponential backoff.
// Transport failures (DNS, connection reset, timeout) take the same backoff,
// because every request here is an idempotent GET and a sync that gave up on
// one agent over a blip would leave it stale until the next run. A cancelled
// context is never retried. A 2xx response is returned open; any other status
// is returned as *apiError.
func (c *apiClient) do(ctx context.Context, httpClient *http.Client, build func(ctx context.Context) (*http.Request, error)) (*http.Response, error) {
	backoff := initialBackoff
	for attempt := 0; ; attempt++ {
		if err := c.limiter.wait(ctx); err != nil {
			return nil, err
		}
		req, err := build(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil || attempt >= maxRetries {
				return nil, err
			}
			delay := backoff
			backoff = min(backoff*2, maxBackoff)
			slog.Warn("Cursor API request failed, retrying",
				"path", req.URL.Path, "error", err, "delay", delay, "attempt", attempt+1)
			c.limiter.holdUntil(c.limiter.now().Add(delay))
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		apiErr := readAPIError(resp)
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if !retryable || attempt >= maxRetries {
			return nil, apiErr
		}

		delay := retryAfter(resp.Header.Get("Retry-After"), c.limiter.now())
		if delay <= 0 {
			delay = backoff
			backoff = min(backoff*2, maxBackoff)
		}
		slog.Warn("Cursor API request throttled or failed, retrying",
			"path", req.URL.Path, "status", resp.StatusCode, "delay", delay, "attempt", attempt+1)
		c.limiter.holdUntil(c.limiter.now().Add(delay))
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

// retryAfter parses a Retry-After header, either delay-seconds or an HTTP
// date, returning zero when the header is absent or unparseable.
func retryAfter(header string, now time.Time) time.Duration {
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		return t.Sub(now)
	}
	return 0
}

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

// listAgents returns every agent the key can see, newest first, following
// nextCursor. maxPages bounds the walk; zero means no bound.
func (c *apiClient) listAgents(ctx context.Context, maxPages int) ([]agentRecord, error) {
	var records []agentRecord
	cursor := ""
	for page := 0; maxPages == 0 || page < maxPages; page++ {
		query := url.Values{"limit": {strconv.Itoa(listPageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		raw, err := c.getJSON(ctx, "/v1/agents?"+query.Encode())
		if err != nil {
			return nil, err
		}
		var list struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor string            `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decode agent list: %w", err)
		}
		for _, item := range list.Items {
			record, err := decodeAgent(item)
			if err != nil {
				slog.Warn("Skipping undecodable agent in list", "error", err)
				continue
			}
			records = append(records, record)
		}
		if list.NextCursor == "" {
			break
		}
		cursor = list.NextCursor
	}
	return records, nil
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
