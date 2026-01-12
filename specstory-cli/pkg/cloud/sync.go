package cloud

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/specstoryai/SpecStoryCLI/pkg/utils"
)

const (
	// ClientName is the name of this client for API identification
	ClientName = "specstory-cli"

	// CloudSyncTimeout is the maximum time to wait for cloud sync operations to complete
	CloudSyncTimeout = 120 * time.Second // Allow 2 minutes for large sessions to upload

	// MaxConcurrentHTTPRequests limits the total number of concurrent HTTP requests (HEAD + PUT combined)
	MaxConcurrentHTTPRequests = 10

	// SessionDebounceInterval is the minimum time between cloud syncs for the same session in run mode
	// This prevents excessive syncing when providers generate rapid response sequences
	SessionDebounceInterval = 10 * time.Second
)

// syncCategory represents the category a session was counted in
type syncCategory string

const (
	categorySkipped syncCategory = "skipped" // Lowest precedence
	categoryErrored syncCategory = "errored" // Replaces skipped
	categoryUpdated syncCategory = "updated" // Replaces skipped and errored
	categoryCreated syncCategory = "created" // Highest precedence, never replaced
)

// SyncSession represents the data needed to sync a session to the cloud
type SyncSession struct {
	SessionID    string
	MDPath       string
	JSONLPath    string
	JSONLContent []byte
	MDContent    string
}

// pendingSyncRequest holds a queued sync request during debounce period
// Debounced syncs always skip HEAD check since we know content just changed
type pendingSyncRequest struct {
	sessionID string
	mdPath    string
	mdContent string
	rawData   []byte
	agentName string
}

// BulkSizesResponse represents the API response for bulk session sizes
type BulkSizesResponse struct {
	Success bool                  `json:"success"`
	Data    BulkSizesResponseData `json:"data"`
}

// BulkSizesResponseData represents the data portion of the bulk sizes response
type BulkSizesResponseData struct {
	Sessions map[string]int `json:"sessions"` // sessionID -> markdown size in bytes
}

// APIRequest represents the JSON payload for the cloud sync API
type APIRequest struct {
	ProjectID   string             `json:"projectId"`
	ProjectName string             `json:"projectName"`
	Name        string             `json:"name"`
	Markdown    string             `json:"markdown"`
	RawData     string             `json:"rawData"`
	Metadata    APIRequestMetadata `json:"metadata"`
}

// APIRequestMetadata represents the metadata portion of the API request
type APIRequestMetadata struct {
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
	AgentName     string `json:"agentName"`
	DeviceID      string `json:"deviceId"`
}

// ProjectData represents the structure of the .specstory/.project.json file
type ProjectData struct {
	ProjectName string `json:"project_name"`
	GitID       string `json:"git_id"`
	WorkspaceID string `json:"workspace_id"`
}

// CloudSyncStats tracks the results of cloud sync operations
type CloudSyncStats struct {
	SessionsAttempted int32    // Total sessions that started sync attempt
	SessionsSkipped   int32    // Already up to date on server
	SessionsUpdated   int32    // Existed but needed update
	SessionsCreated   int32    // New sessions created
	SessionsErrored   int32    // Failed with non-2xx response
	SessionsTimedOut  int32    // Timed out waiting for overall sync completion (calculated at shutdown)
	countedSessions   sync.Map // Maps session ID -> syncCategory (tracks which category each session is in)
}

// calculateTimedOut computes the number of sessions that timed out during sync.
// This is calculated as: attempted - (skipped + updated + created + errored)
// Includes defensive validation to prevent negative values in case of counter bugs.
func (stats *CloudSyncStats) calculateTimedOut() {
	finished := stats.SessionsSkipped + stats.SessionsUpdated + stats.SessionsCreated + stats.SessionsErrored

	// Defensive validation: finished should never exceed attempted
	if finished > stats.SessionsAttempted {
		slog.Error("CloudSyncStats counter inconsistency detected",
			"attempted", stats.SessionsAttempted,
			"finished", finished,
			"skipped", stats.SessionsSkipped,
			"updated", stats.SessionsUpdated,
			"created", stats.SessionsCreated,
			"errored", stats.SessionsErrored)
		stats.SessionsTimedOut = 0
		return
	}

	stats.SessionsTimedOut = stats.SessionsAttempted - finished
}

// sessionDebounceState tracks debounce state for a single session
type sessionDebounceState struct {
	mu           sync.Mutex          // Protects concurrent access to this session's debounce state
	lastSyncTime time.Time           // Timestamp when last sync started (used to calculate debounce window)
	pending      *pendingSyncRequest // Most recent sync request queued during debounce window (newer requests replace older)
	timer        *time.Timer         // Fires after debounce interval to flush pending sync (nil if no pending sync)
}

// SyncManager handles cloud synchronization operations
type SyncManager struct {
	enabled          bool
	wg               sync.WaitGroup
	silent           bool
	syncCount        int32          // Atomic counter for active syncs
	httpSemaphore    chan struct{}  // Semaphore to limit concurrent HTTP requests
	stats            CloudSyncStats // Statistics for the current sync operation
	debounceInterval time.Duration  // Debounce interval for run mode (default: SessionDebounceInterval)
	debounceSessions sync.Map       // Maps sessionID -> *sessionDebounceState
	bulkSizes        map[string]int // Optional: sessionID -> size cache for batch operations
	bulkSizesMu      sync.RWMutex   // Protects bulkSizes map
}

var (
	globalSyncManager *SyncManager
	syncManagerMutex  sync.RWMutex
	deviceID          string         // Cached device ID
	clientVersion     string = "dev" // Will be set from main
	apiBaseURL        string         // Base URL for API calls
)

// incrementCategory atomically increments the counter for a given category
func (stats *CloudSyncStats) incrementCategory(category syncCategory) {
	switch category {
	case categorySkipped:
		atomic.AddInt32(&stats.SessionsSkipped, 1)
	case categoryErrored:
		atomic.AddInt32(&stats.SessionsErrored, 1)
	case categoryUpdated:
		atomic.AddInt32(&stats.SessionsUpdated, 1)
	case categoryCreated:
		atomic.AddInt32(&stats.SessionsCreated, 1)
	}
}

// decrementCategory atomically decrements the counter for a given category
func (stats *CloudSyncStats) decrementCategory(category syncCategory) {
	switch category {
	case categorySkipped:
		atomic.AddInt32(&stats.SessionsSkipped, -1)
	case categoryErrored:
		atomic.AddInt32(&stats.SessionsErrored, -1)
	case categoryUpdated:
		atomic.AddInt32(&stats.SessionsUpdated, -1)
	case categoryCreated:
		atomic.AddInt32(&stats.SessionsCreated, -1)
	}
}

// shouldReplaceCategory determines if newCategory should replace oldCategory based on precedence
// Precedence (highest to lowest): created > updated > errored > skipped
func shouldReplaceCategory(oldCategory, newCategory syncCategory) bool {
	// Created never gets replaced
	if oldCategory == categoryCreated {
		return false
	}
	// Updated replaces skipped and errored
	if newCategory == categoryUpdated {
		return oldCategory == categorySkipped || oldCategory == categoryErrored
	}
	// Errored replaces only skipped
	if newCategory == categoryErrored {
		return oldCategory == categorySkipped
	}
	// Created replaces everything (but we already handled oldCategory == created above)
	if newCategory == categoryCreated {
		return true
	}
	// Skipped doesn't replace anything
	return false
}

// trackSessionInCategory records a session in a category and updates counters atomically
// Handles precedence rules and automatically increments/decrements appropriate counters
func (stats *CloudSyncStats) trackSessionInCategory(sessionID string, newCategory syncCategory) {
	for {
		// Try to load existing category
		existing, loaded := stats.countedSessions.Load(sessionID)

		if !loaded {
			// No existing entry, try to store
			actual, loaded := stats.countedSessions.LoadOrStore(sessionID, newCategory)
			if !loaded {
				// Successfully stored new category - increment counter
				stats.incrementCategory(newCategory)
				return
			}
			// Someone else stored it first, loop to check precedence
			existing = actual
		}

		oldCategory := existing.(syncCategory)

		// Check if we should replace the old category
		if !shouldReplaceCategory(oldCategory, newCategory) {
			// Don't replace - higher precedence category already exists
			return
		}

		// Try to replace old category with new category
		if stats.countedSessions.CompareAndSwap(sessionID, oldCategory, newCategory) {
			// Successfully replaced - decrement old counter and increment new counter
			stats.decrementCategory(oldCategory)
			stats.incrementCategory(newCategory)
			return
		}
		// CompareAndSwap failed, someone else modified it, loop again
	}
}

// acquireHTTPSemaphore acquires the HTTP semaphore (blocks until available)
// Returns a release function that should be called with defer
//
// No timeout - blocks indefinitely until semaphore is available. Goroutines that don't acquire
// before the overall sync timeout (CloudSyncTimeout) are orphaned, but this is acceptable because
// the process terminates after sync timeout. The overall timeout handles cleanup and stats reporting.
func (syncMgr *SyncManager) acquireHTTPSemaphore(sessionID, requestType string) (releaseFunc func()) {
	// Block until semaphore slot is available
	syncMgr.httpSemaphore <- struct{}{}

	slog.Debug("Acquired HTTP semaphore",
		"sessionId", sessionID,
		"requestType", requestType,
		"currentConcurrency", len(syncMgr.httpSemaphore))

	releaseFunc = func() {
		<-syncMgr.httpSemaphore
		slog.Debug("Released HTTP semaphore",
			"sessionId", sessionID,
			"requestType", requestType,
			"currentConcurrency", len(syncMgr.httpSemaphore))
	}
	return releaseFunc
}

// fetchBulkSessionSizes fetches markdown sizes for all sessions in a project from the SpecStory Cloud API.
// This enables batch sync optimization by avoiding individual HEAD requests for each session.
//
// The function makes a GET request to /api/v1/projects/{projectID}/sessions/sizes with automatic
// retry logic (3 attempts with 1 second delay between retries) to handle transient network failures.
// This function is only called once during batch sync initialization, so no HTTP semaphore is needed.
//
// Parameters:
//   - projectID: The project identifier (git_id, workspace_id, or directory name)
//
// Returns:
//   - map[string]int: Map of sessionID to markdown content size in bytes. Empty map if project has no sessions.
//   - error: Non-nil if all retry attempts fail, including request creation, network, parsing, or API errors.
//
// The function logs at Debug level for normal operation and Error level for failures. On success, it logs
// at Info level with the count of sessions fetched.
func (syncMgr *SyncManager) fetchBulkSessionSizes(projectID string) (map[string]int, error) {
	const maxAttempts = 3
	const retryDelay = 1 * time.Second

	apiURL := GetAPIBaseURL() + "/api/v1/projects/" + projectID + "/sessions/sizes"

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Log attempt
		if attempt == 1 {
			slog.Debug("Fetching bulk session sizes",
				"projectId", projectID,
				"url", apiURL,
				"attempt", attempt)
		} else {
			slog.Debug("Retrying bulk session sizes fetch",
				"projectId", projectID,
				"url", apiURL,
				"attempt", attempt)
		}

		// Make GET request
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			slog.Error("Failed to create GET request for bulk sizes",
				"projectId", projectID,
				"attempt", attempt,
				"error", err)
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("failed to create request after %d attempts: %w", maxAttempts, err)
		}

		// Add auth headers
		cloudToken := GetCloudToken()
		req.Header.Set("Authorization", "Bearer "+cloudToken)
		req.Header.Set("User-Agent", GetUserAgent())

		// Create HTTP client with timeout
		client := &http.Client{
			Timeout: 30 * time.Second,
		}

		// Execute request
		resp, err := client.Do(req)

		if err != nil {
			slog.Debug("Bulk sizes GET request failed",
				"projectId", projectID,
				"attempt", attempt,
				"error", err)
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("GET request failed after %d attempts: %w", maxAttempts, err)
		}

		// Read response body
		defer func() { _ = resp.Body.Close() }()
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Failed to read bulk sizes response body",
				"projectId", projectID,
				"attempt", attempt,
				"status", resp.StatusCode,
				"error", err)
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("failed to read response body after %d attempts: %w", maxAttempts, err)
		}

		// Check status code
		if resp.StatusCode != 200 {
			slog.Debug("Bulk sizes GET returned non-200 status",
				"projectId", projectID,
				"attempt", attempt,
				"status", resp.StatusCode,
				"responseBody", string(respBody))
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("GET request returned status %d after %d attempts", resp.StatusCode, maxAttempts)
		}

		// Parse JSON response
		var bulkResponse BulkSizesResponse
		err = json.Unmarshal(respBody, &bulkResponse)
		if err != nil {
			slog.Error("Failed to parse bulk sizes JSON response",
				"projectId", projectID,
				"attempt", attempt,
				"error", err,
				"responseBody", string(respBody))
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("failed to parse JSON after %d attempts: %w", maxAttempts, err)
		}

		// Check success field
		if !bulkResponse.Success {
			slog.Warn("Bulk sizes response indicated failure",
				"projectId", projectID,
				"attempt", attempt)
			if attempt < maxAttempts {
				time.Sleep(retryDelay)
				continue
			}
			return nil, fmt.Errorf("API returned success=false after %d attempts", maxAttempts)
		}

		// Success - log and return
		sessionCount := len(bulkResponse.Data.Sessions)
		slog.Info("Fetched bulk session sizes",
			"projectId", projectID,
			"sessionCount", sessionCount)
		return bulkResponse.Data.Sessions, nil
	}

	// Should never reach here, but just in case
	return nil, fmt.Errorf("failed to fetch bulk sizes after %d attempts", maxAttempts)
}

// PreloadSessionSizes fetches and caches session sizes for all sessions in a project
// This is a blocking call that should be made before syncing multiple sessions
// If fetch fails, logs warning and leaves cache nil (enables HEAD request fallback)
func (syncMgr *SyncManager) PreloadSessionSizes(projectID string) {
	if syncMgr == nil {
		slog.Warn("PreloadSessionSizes called with nil sync manager")
		return
	}

	slog.Debug("Preloading session sizes for batch sync",
		"projectId", projectID)

	// Fetch bulk sizes (with retry logic)
	sizes, err := syncMgr.fetchBulkSessionSizes(projectID)
	if err != nil {
		// Log error and leave cache nil to trigger HEAD request fallback
		slog.Error("Failed to fetch bulk session sizes, will fall back to HEAD requests",
			"projectId", projectID,
			"error", err)
		return
	}

	// Store in cache with write lock
	syncMgr.bulkSizesMu.Lock()
	defer syncMgr.bulkSizesMu.Unlock()
	syncMgr.bulkSizes = sizes

	sessionCount := len(sizes)
	slog.Info("Preloaded session sizes from server",
		"projectId", projectID,
		"sessionCount", sessionCount)
}

// requiresSync checks if a session needs to be synced to the cloud
// Returns true if sync is needed, false if already up-to-date
func (syncMgr *SyncManager) requiresSync(sessionID, mdPath, mdContent, projectID string, skipHeadCheck bool) (bool, error) {
	// Skip HEAD check if requested (caller determined sync is needed)
	if skipHeadCheck {
		slog.Debug("Skipping HEAD check (skipHeadCheck=true)",
			"sessionId", sessionID)
		return true, nil
	}

	// Get size from passed content
	localSize := len(mdContent)

	// Check if bulk sizes are preloaded (batch sync mode)
	syncMgr.bulkSizesMu.RLock()
	bulkSizes := syncMgr.bulkSizes
	syncMgr.bulkSizesMu.RUnlock()

	if bulkSizes != nil {
		// Bulk sizes are available - use cached data instead of HEAD request
		serverSize, exists := bulkSizes[sessionID]
		if !exists {
			// Session not in bulk sizes map - doesn't exist on server yet
			slog.Debug("Using preloaded size for sync check: session not on server",
				"sessionId", sessionID,
				"localSize", localSize)
			return true, nil
		}

		// Compare sizes
		needsSync := localSize > serverSize
		if needsSync {
			slog.Debug("Using preloaded size for sync check: local is larger",
				"sessionId", sessionID,
				"localSize", localSize,
				"serverSize", serverSize)
		} else {
			slog.Info("Session already up-to-date on server (using preloaded sizes), skipping sync",
				"sessionId", sessionID,
				"localSize", localSize,
				"serverSize", serverSize)
		}
		return needsSync, nil
	}

	// Bulk sizes not available - fall back to HEAD request
	slog.Debug("Using HEAD request for sync check (no bulk sizes)",
		"sessionId", sessionID)

	// Acquire semaphore for HEAD request (blocks until available)
	release := syncMgr.acquireHTTPSemaphore(sessionID, "HEAD")
	defer release()

	// Make HEAD request
	apiURL := GetAPIBaseURL() + "/api/v1/projects/" + projectID + "/sessions/" + sessionID
	req, err := http.NewRequest("HEAD", apiURL, nil)
	if err != nil {
		slog.Error("Failed to create HEAD request",
			"sessionId", sessionID,
			"error", err)
		return false, err
	}

	// Add auth headers
	cloudToken := GetCloudToken()
	req.Header.Set("Authorization", "Bearer "+cloudToken)
	req.Header.Set("User-Agent", GetUserAgent())

	// Create HTTP client with shorter timeout for HEAD
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	slog.Debug("Cloud sync HEAD request",
		"sessionId", sessionID,
		"url", apiURL,
		"localMarkdownSize", localSize)

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("HEAD request failed, will attempt sync",
			"sessionId", sessionID,
			"error", err)
		return true, nil // Err on side of syncing if HEAD fails
	}
	defer func() { _ = resp.Body.Close() }()

	slog.Debug("Cloud sync HEAD response",
		"sessionId", sessionID,
		"status", resp.StatusCode,
		"headers", resp.Header)

	// Check response
	if resp.StatusCode == 404 {
		slog.Debug("Session not found on server, will sync",
			"sessionId", sessionID)
		return true, nil
	}

	if resp.StatusCode == 200 {
		serverSizeStr := resp.Header.Get("X-Markdown-Size")
		if serverSizeStr == "" {
			slog.Debug("No X-Markdown-Size header, will sync",
				"sessionId", sessionID)
			return true, nil
		}

		serverSize, err := strconv.Atoi(serverSizeStr)
		if err != nil {
			slog.Debug("Invalid X-Markdown-Size header, will sync",
				"sessionId", sessionID,
				"headerValue", serverSizeStr,
				"error", err)
			return true, nil
		}

		if localSize > serverSize {
			slog.Debug("Local markdown larger than server, will sync",
				"sessionId", sessionID,
				"localSize", localSize,
				"serverSize", serverSize)
			return true, nil
		}

		slog.Info("Session already up-to-date on server, skipping sync",
			"sessionId", sessionID,
			"localSize", localSize,
			"serverSize", serverSize)
		return false, nil
	}

	// For any other status codes, err on side of syncing
	slog.Debug("Unexpected HEAD response status, will sync",
		"sessionId", sessionID,
		"status", resp.StatusCode)
	return true, nil
}

// SetClientVersion sets the client version for API requests
func SetClientVersion(version string) {
	clientVersion = version
}

// GetClientVersion returns the current client version
func GetClientVersion() string {
	return clientVersion
}

// SetAPIBaseURL sets the base URL for API requests
func SetAPIBaseURL(url string) {
	apiBaseURL = url
}

// GetAPIBaseURL returns the base URL for API requests
func GetAPIBaseURL() string {
	if apiBaseURL == "" {
		return "https://cloud.specstory.com" // Default API base URL
	}
	return apiBaseURL
}

// GetUserAgent returns the User-Agent string for API requests
func GetUserAgent() string {
	return fmt.Sprintf("%s/%s SpecStory, Inc.", ClientName, clientVersion)
}

// getDeviceID generates a unique device ID based on MAC address
func getDeviceID() string {
	// Return cached value if available
	if deviceID != "" {
		return deviceID
	}

	// Get all network interfaces
	interfaces, err := net.Interfaces()
	if err != nil {
		slog.Warn("Failed to get network interfaces", "error", err)
		// Fallback to hostname-based ID
		hostname, _ := os.Hostname()
		h := sha256.Sum256([]byte("specstory-" + hostname))
		deviceID = hex.EncodeToString(h[:])
		return deviceID
	}

	// Find the first non-loopback interface with a MAC address
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback == 0 && len(iface.HardwareAddr) > 0 {
			// Hash the MAC address
			h := sha256.Sum256(iface.HardwareAddr)
			deviceID = hex.EncodeToString(h[:])
			slog.Debug("Generated device ID from interface", "interface", iface.Name)
			return deviceID
		}
	}

	// Fallback if no suitable interface found
	hostname, _ := os.Hostname()
	h := sha256.Sum256([]byte("specstory-fallback-" + hostname))
	deviceID = hex.EncodeToString(h[:])
	slog.Debug("Using fallback device ID based on hostname")
	return deviceID
}

// InitSyncManager initializes the global sync manager
func InitSyncManager(enabled bool) {
	syncManagerMutex.Lock()
	defer syncManagerMutex.Unlock()
	globalSyncManager = &SyncManager{
		enabled:          enabled,
		silent:           false,
		httpSemaphore:    make(chan struct{}, MaxConcurrentHTTPRequests),
		debounceInterval: SessionDebounceInterval,
	}
	slog.Debug("Cloud sync manager initialized", "enabled", enabled, "maxConcurrentHTTPRequests", MaxConcurrentHTTPRequests)
}

// SetSilent configures whether to show user feedback during shutdown
func SetSilent(silent bool) {
	syncManagerMutex.Lock()
	defer syncManagerMutex.Unlock()
	if globalSyncManager != nil {
		globalSyncManager.silent = silent
	}
}

// GetSyncManager returns the global sync manager
func GetSyncManager() *SyncManager {
	syncManagerMutex.RLock()
	defer syncManagerMutex.RUnlock()
	return globalSyncManager
}

// performSync executes the actual sync operation (HEAD check + PUT request)
func (syncMgr *SyncManager) performSync(sessionID, mdPath, mdContent string, rawData []byte, agentName string, skipHeadCheck bool) {
	timestamp := time.Now().UTC().Format(time.RFC3339)

	// Log the sync attempt
	slog.Info("Cloud sync initiated",
		"sessionId", sessionID,
		"mdPath", mdPath,
		"agentName", agentName,
		"timestamp", timestamp)

	// Track that this session attempted to sync (for timeout calculation at shutdown)
	// Only count each unique session once, even if performSync is called multiple times
	// (e.g., due to debouncing in run mode triggering multiple syncs for the same session)
	// Note: We check the map here but don't store yet - trackSessionInCategory() (called
	// later in this function on every code path) will store the session in the map
	if _, alreadyTracked := syncMgr.stats.countedSessions.Load(sessionID); !alreadyTracked {
		atomic.AddInt32(&syncMgr.stats.SessionsAttempted, 1)
	}

	// Content is now passed in as parameters, no need to read files

	// Create sync session data
	syncData := SyncSession{
		SessionID:    sessionID,
		MDPath:       mdPath,
		JSONLPath:    "", // No longer using file path
		JSONLContent: rawData,
		MDContent:    mdContent,
	}

	// Read project configuration
	projectData, err := readProjectConfig()
	if err != nil {
		slog.Warn("Cannot sync session to cloud: failed to read project config", "error", err, "sessionId", sessionID)
		syncMgr.stats.trackSessionInCategory(sessionID, categoryErrored)
		return
	}

	// Determine project ID - use git_id if present, otherwise workspace_id
	projectID := ""
	if projectData != nil && projectData.GitID != "" {
		projectID = projectData.GitID
	} else if projectData != nil && projectData.WorkspaceID != "" {
		projectID = projectData.WorkspaceID
	}

	// Project ID is required for cloud sync
	if projectID == "" {
		slog.Warn("Cannot sync session to cloud: no project ID available", "sessionId", sessionID)
		syncMgr.stats.trackSessionInCategory(sessionID, categoryErrored)
		return
	}

	// Get project name (use directory name as fallback - this is cosmetic only)
	projectName := ""
	if projectData != nil && projectData.ProjectName != "" {
		projectName = projectData.ProjectName
	} else {
		// Fallback to extracting from mdPath for display purposes only
		dir := filepath.Dir(filepath.Dir(mdPath))      // Go up from history to .specstory
		projectName = filepath.Base(filepath.Dir(dir)) // Get the project directory name
		slog.Debug("No project name in config, using directory name", "projectName", projectName)
	}

	// Check if sync is needed using HEAD request
	shouldSync, err := syncMgr.requiresSync(sessionID, mdPath, mdContent, projectID, skipHeadCheck)
	if err != nil {
		slog.Error("Failed to check if sync needed",
			"sessionId", sessionID,
			"error", err)
		// Continue with sync attempt anyway
	} else if !shouldSync {
		// Track skipped session with precedence rules
		syncMgr.stats.trackSessionInCategory(sessionID, categorySkipped)
		slog.Info("Skipping cloud sync, already up-to-date",
			"sessionId", sessionID)
		return
	}

	// Extract name from markdown filename (without path or extension)
	name := filepath.Base(mdPath)
	if ext := filepath.Ext(name); ext != "" {
		name = name[:len(name)-len(ext)]
	}

	// Create API request
	metadata := APIRequestMetadata{
		ClientName:    ClientName,
		ClientVersion: clientVersion,
		AgentName:     agentName, // Use the passed-in agent name
		DeviceID:      getDeviceID(),
	}
	apiReq := APIRequest{
		ProjectID:   projectID,
		ProjectName: projectName,
		Name:        name,
		Markdown:    syncData.MDContent,
		RawData:     string(syncData.JSONLContent),
		Metadata:    metadata,
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(apiReq)
	if err != nil {
		slog.Error("Cloud sync error marshaling JSON", "sessionId", sessionID, "error", err)
		return
	}

	// Acquire semaphore for PUT request (blocks until available)
	release := syncMgr.acquireHTTPSemaphore(sessionID, "PUT")
	defer release()

	// Make HTTP request
	apiURL := GetAPIBaseURL() + "/api/v1/projects/" + projectID + "/sessions/" + sessionID
	req, err := http.NewRequest("PUT", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		slog.Error("Cloud sync error creating request", "sessionId", sessionID, "error", err)
		return
	}

	// Get cloud token for authorization
	cloudToken := GetCloudToken()
	req.Header.Set("Authorization", "Bearer "+cloudToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", GetUserAgent())

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	slog.Debug("Cloud sync making API call",
		"sessionId", sessionID,
		"url", apiURL,
		"projectId", projectID,
		"projectName", projectName,
		"name", name,
		"metadata", metadata,
		"jsonlSize", len(syncData.JSONLContent),
		"mdSize", len(syncData.MDContent),
		"deviceId", getDeviceID())

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		// Track network/request errors with precedence rules
		syncMgr.stats.trackSessionInCategory(sessionID, categoryErrored)
		slog.Error("Cloud sync API error", "sessionId", sessionID, "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Get ETag from response header
		etag := resp.Header.Get("ETag")
		slog.Info("Cloud sync completed successfully",
			"sessionId", sessionID,
			"status", resp.StatusCode,
			"etag", etag)

		// Track as created or updated based on status code with precedence rules
		if resp.StatusCode == 201 {
			syncMgr.stats.trackSessionInCategory(sessionID, categoryCreated)
		} else {
			syncMgr.stats.trackSessionInCategory(sessionID, categoryUpdated)
		}
	} else {
		// Track as error with precedence rules
		syncMgr.stats.trackSessionInCategory(sessionID, categoryErrored)

		// Try to read error response body
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("Cloud sync API returned error",
				"sessionId", sessionID,
				"status", resp.StatusCode,
				"bodyReadError", err)
		} else {
			slog.Error("Cloud sync API returned error",
				"sessionId", sessionID,
				"status", resp.StatusCode,
				"responseBody", string(respBody))
		}
	}
}

// debouncedSync implements debouncing logic for a session
// Always skips HEAD check since we know content just changed in autosave mode
func (syncMgr *SyncManager) debouncedSync(sessionID, mdPath, mdContent string, rawData []byte, agentName string) {
	// Get or create debounce state for this session
	stateInterface, _ := syncMgr.debounceSessions.LoadOrStore(sessionID, &sessionDebounceState{})
	state := stateInterface.(*sessionDebounceState)

	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	timeSinceLastSync := now.Sub(state.lastSyncTime)

	// Check if we can sync immediately (no recent sync or first sync)
	if state.lastSyncTime.IsZero() || timeSinceLastSync >= syncMgr.debounceInterval {
		// Sync immediately
		state.lastSyncTime = now
		state.pending = nil

		// Cancel any pending timer
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}

		// Start sync in goroutine (always skip HEAD check in autosave mode)
		syncMgr.wg.Add(1)
		atomic.AddInt32(&syncMgr.syncCount, 1)
		go func() {
			defer func() {
				syncMgr.wg.Done()
				atomic.AddInt32(&syncMgr.syncCount, -1)
			}()
			syncMgr.performSync(sessionID, mdPath, mdContent, rawData, agentName, true)
		}()

		// Log with timeSinceLastSync only if meaningful (not after cleanup)
		if state.lastSyncTime.IsZero() {
			slog.Debug("Cloud sync started immediately",
				"sessionId", sessionID)
		} else {
			slog.Debug("Cloud sync started immediately",
				"sessionId", sessionID,
				"timeSinceLastSync", timeSinceLastSync)
		}
		return
	}

	// Within debounce window - queue or replace pending request
	state.pending = &pendingSyncRequest{
		sessionID: sessionID,
		mdPath:    mdPath,
		mdContent: mdContent,
		rawData:   rawData,
		agentName: agentName,
	}

	// Set timer if not already set
	if state.timer == nil {
		timeUntilSync := syncMgr.debounceInterval - timeSinceLastSync
		state.timer = time.AfterFunc(timeUntilSync, func() {
			syncMgr.flushPendingSync(sessionID)
		})

		slog.Debug("Cloud sync queued with new timer",
			"sessionId", sessionID,
			"timeUntilSync", timeUntilSync)
	} else {
		slog.Debug("Cloud sync queued (replaced pending)",
			"sessionId", sessionID)
	}
}

// flushPendingSync is called by timer to flush a queued sync
func (syncMgr *SyncManager) flushPendingSync(sessionID string) {
	stateInterface, ok := syncMgr.debounceSessions.Load(sessionID)
	if !ok {
		return
	}
	state := stateInterface.(*sessionDebounceState)

	state.mu.Lock()
	defer state.mu.Unlock()

	// Sync the pending request (timer is only set when pending is non-nil)
	req := state.pending
	state.pending = nil
	state.timer = nil

	// Guard against nil pending request (defensive programming)
	if req == nil {
		slog.Warn("Timer fired but no pending request found",
			"sessionId", sessionID)
		syncMgr.debounceSessions.Delete(sessionID)
		return
	}

	// Clean up the session state from the map since we're done with this debounce cycle
	// If another event comes later, LoadOrStore will create a fresh state
	syncMgr.debounceSessions.Delete(sessionID)

	// Start sync in goroutine (always skip HEAD check for debounced syncs)
	syncMgr.wg.Add(1)
	atomic.AddInt32(&syncMgr.syncCount, 1)
	go func() {
		defer func() {
			syncMgr.wg.Done()
			atomic.AddInt32(&syncMgr.syncCount, -1)
		}()
		syncMgr.performSync(req.sessionID, req.mdPath, req.mdContent, req.rawData, req.agentName, true)
	}()

	slog.Debug("Flushed pending sync after debounce, cleaned up session state",
		"sessionId", sessionID)
}

// flushAllPending flushes all pending debounced syncs (called on shutdown)
func (syncMgr *SyncManager) flushAllPending() {
	slog.Debug("Flushing all pending debounced syncs")

	syncMgr.debounceSessions.Range(func(key, value interface{}) bool {
		sessionID := key.(string)
		state := value.(*sessionDebounceState)

		state.mu.Lock()
		defer state.mu.Unlock()

		// Cancel timer
		if state.timer != nil {
			state.timer.Stop()
			state.timer = nil
		}

		// If there's a pending request, sync it immediately
		if state.pending != nil {
			req := state.pending
			state.pending = nil

			// Start sync in goroutine (always skip HEAD check for debounced syncs)
			syncMgr.wg.Add(1)
			atomic.AddInt32(&syncMgr.syncCount, 1)
			go func() {
				defer func() {
					syncMgr.wg.Done()
					atomic.AddInt32(&syncMgr.syncCount, -1)
				}()
				syncMgr.performSync(req.sessionID, req.mdPath, req.mdContent, req.rawData, req.agentName, true)
			}()

			slog.Info("Flushing pending sync on shutdown",
				"sessionId", sessionID)
		}

		return true
	})
}

// SyncSessionToCloud asynchronously syncs a session to the cloud
// When isAutosaveMode is true (run command), syncs are debounced and skip HEAD checks for efficiency
// When isAutosaveMode is false (manual sync), syncs are immediate with HEAD checks
func SyncSessionToCloud(sessionID string, mdPath string, mdContent string, rawData []byte, agentName string, isAutosaveMode bool) {
	syncManagerMutex.RLock()
	syncMgr := globalSyncManager
	syncManagerMutex.RUnlock()

	if syncMgr == nil || !syncMgr.enabled {
		if syncMgr == nil {
			slog.Warn("Cloud sync not initialized, skipping sync", "sessionId", sessionID)
		} else {
			slog.Debug("Cloud sync disabled by flag, skipping sync", "sessionId", sessionID)
		}
		return
	}

	// Check authentication
	if !IsAuthenticated() {
		slog.Warn("Cloud sync skipped: user not authenticated", "sessionId", sessionID)
		return
	}

	// Route to debounced or immediate sync based on mode
	if isAutosaveMode {
		// Autosave mode: debounce syncs and skip HEAD checks
		syncMgr.debouncedSync(sessionID, mdPath, mdContent, rawData, agentName)
	} else {
		// Manual sync mode: immediate sync with HEAD check
		syncMgr.wg.Add(1)
		atomic.AddInt32(&syncMgr.syncCount, 1)
		go func() {
			defer func() {
				syncMgr.wg.Done()
				atomic.AddInt32(&syncMgr.syncCount, -1)
			}()
			syncMgr.performSync(sessionID, mdPath, mdContent, rawData, agentName, false)
		}()
	}
}

// getStats loads atomic counters and returns a CloudSyncStats with calculated timeout count.
// This consolidates the pattern of loading counters + calculating timeouts used in multiple shutdown paths.
func (sm *SyncManager) getStats() *CloudSyncStats {
	stats := &CloudSyncStats{
		SessionsAttempted: atomic.LoadInt32(&sm.stats.SessionsAttempted),
		SessionsSkipped:   atomic.LoadInt32(&sm.stats.SessionsSkipped),
		SessionsUpdated:   atomic.LoadInt32(&sm.stats.SessionsUpdated),
		SessionsCreated:   atomic.LoadInt32(&sm.stats.SessionsCreated),
		SessionsErrored:   atomic.LoadInt32(&sm.stats.SessionsErrored),
	}
	stats.calculateTimedOut()
	return stats
}

// Shutdown waits for all cloud sync operations to complete with a timeout and returns statistics
func Shutdown(timeout time.Duration) *CloudSyncStats {
	syncManagerMutex.RLock()
	sm := globalSyncManager
	syncManagerMutex.RUnlock()

	if sm == nil || !sm.enabled {
		return nil
	}

	// Flush all pending debounced syncs before waiting
	sm.flushAllPending()

	// Create a channel to signal when all syncs are done
	done := make(chan struct{})
	go func() {
		sm.wg.Wait()
		close(done)
	}()

	// Give goroutines a moment to start and check if we have any work
	time.Sleep(100 * time.Millisecond)

	// Check if all work is already done (e.g., all sessions were skipped)
	select {
	case <-done:
		// All work completed very quickly (likely all skipped)
		stats := sm.getStats()
		// If we have stats, return them
		if stats.SessionsAttempted > 0 {
			return stats
		}
		// No work was done at all
		return nil
	default:
		// Work is still in progress, continue to show countdown
	}

	// Create a ticker for countdown updates
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Track elapsed time
	start := time.Now()

	// Show initial countdown immediately
	if !sm.silent {
		fmt.Printf("Waiting for cloud sync to complete... %ds until timeout", int(timeout.Seconds()))
	}

	for {
		select {
		case <-done:
			// Ensure countdown is visible for at least 1 second
			elapsed := time.Since(start)
			if elapsed < 1*time.Second {
				time.Sleep(1*time.Second - elapsed)
			}

			// All syncs completed - clear the progress line
			if !sm.silent {
				// Clear the line by printing spaces over it, then return to beginning
				fmt.Printf("\r%60s\r", "")
			}

			// Return stats
			return sm.getStats()
		case <-ticker.C:
			// Update countdown
			elapsed := time.Since(start)
			remaining := timeout - elapsed
			if remaining <= 0 {
				// Timeout reached - clear the progress line
				if !sm.silent {
					fmt.Printf("\r%60s\r", "")
					fmt.Printf("Cloud sync timeout. Some syncs may not have completed.\n")
				}
				slog.Warn("Cloud sync shutdown timeout", "timeout", timeout)

				// Return partial stats
				return sm.getStats()
			}
			// Show countdown
			if !sm.silent {
				fmt.Printf("\rWaiting for cloud sync to complete... %ds until timeout", int(remaining.Seconds()))
			}
		}
	}
}

// readProjectConfig reads the .specstory/.project.json file
func readProjectConfig() (*ProjectData, error) {
	var projectData ProjectData
	var err error

	// Read the project config file
	configPath := filepath.Join(utils.SPECSTORY_DIR, utils.PROJECT_JSON_FILE)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	// Parse JSON
	err = json.Unmarshal(data, &projectData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse project config: %w", err)
	}

	return &projectData, nil
}
