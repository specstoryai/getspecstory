package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/specstoryai/SpecStoryCLI/pkg/utils"
)

// ===== Device Metadata Types =====

// DeviceMetadata contains information about the device for authentication
type DeviceMetadata struct {
	Hostname      string `json:"hostname,omitempty"`
	OS            string `json:"os,omitempty"`
	OSVersion     string `json:"os_version,omitempty"`
	OSDisplayName string `json:"os_display_name,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	Username      string `json:"username,omitempty"`
	Client        string `json:"client,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
}

// ===== Auth File Structure =====

// CloudRefreshData represents the refresh token portion of auth.json
type CloudRefreshData struct {
	Token       string `json:"token"`
	As          string `json:"as"`          // User email
	CreatedAt   string `json:"createdAt"`   // When the refresh token was created
	ExpiresAt   string `json:"expiresAt"`   // When the refresh token expires (10 years)
	LastValidAt string `json:"lastValidAt"` // Last successful use
}

// CloudAccessData represents the access token portion of auth.json
type CloudAccessData struct {
	Token     string `json:"token"`
	UpdatedAt string `json:"updatedAt"` // When the access token was obtained
	ExpiresAt string `json:"expiresAt"` // When the access token expires (1 hour)
}

// AuthData represents the structure of the auth.json file
type AuthData struct {
	CloudRefresh *CloudRefreshData `json:"cloud_refresh,omitempty"`
	CloudAccess  *CloudAccessData  `json:"cloud_access,omitempty"`
}

// ===== API Request/Response Types =====

// DeviceLoginRequest represents the request payload for device login
type DeviceLoginRequest struct {
	DeviceCode    string `json:"device_code"`
	Hostname      string `json:"hostname,omitempty"`
	OS            string `json:"os,omitempty"`
	OSVersion     string `json:"os_version,omitempty"`
	OSDisplayName string `json:"os_display_name,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	Username      string `json:"username,omitempty"`
	Client        string `json:"client,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
}

// DeviceLoginResponse represents the response from device login
type DeviceLoginResponse struct {
	RefreshToken string `json:"refreshToken"`
	CreatedAt    string `json:"createdAt"`
	ExpiresAt    string `json:"expiresAt"`
	User         struct {
		Email string `json:"email"`
	} `json:"user"`
}

// DeviceRefreshResponse represents the response from device refresh
type DeviceRefreshResponse struct {
	AccessToken string `json:"accessToken"`
	CreatedAt   string `json:"createdAt"`
	ExpiresAt   string `json:"expiresAt"`
}

// ===== Error Types =====

// ErrAuthenticationFailed indicates a 401 authentication failure
type ErrAuthenticationFailed struct {
	Message string
}

func (e *ErrAuthenticationFailed) Error() string {
	return e.Message
}

// ===== Cache Variables =====

var (
	// Cache the authentication status to avoid repeated file reads
	authChecked     bool
	isAuthenticated bool
	// Track if we had an authentication failure (401)
	hadAuthFailure bool

	// Session-only token support (not persisted to disk)
	// When set via `--cloud-token` flag, these override the normal auth file
	sessionRefreshToken string
	sessionAccessToken  string
	hasSessionToken     bool
)

// ===== Device Metadata Functions =====

// getMacOSVersion retrieves the macOS version using sw_vers command
func getMacOSVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		slog.Debug("Failed to get macOS version", "error", err)
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// getLinuxVersionFromOSRelease attempts to get Linux version from /etc/os-release
func getLinuxVersionFromOSRelease() (string, bool) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", false
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "VERSION=") {
			version := strings.TrimPrefix(line, "VERSION=")
			version = strings.Trim(version, `"`)
			return version, true
		}
	}
	return "", false
}

// getLinuxVersionFromUname fallback to get Linux version using uname command
func getLinuxVersionFromUname() string {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		slog.Debug("Failed to get Linux version from uname", "error", err)
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// getLinuxVersion retrieves the Linux version using multiple strategies
func getLinuxVersion() string {
	// Try /etc/os-release first (preferred method)
	if version, found := getLinuxVersionFromOSRelease(); found {
		return version
	}

	// Fallback to uname -r
	return getLinuxVersionFromUname()
}

// GetDeviceMetadata collects metadata about the current device
func GetDeviceMetadata() DeviceMetadata {
	metadata := DeviceMetadata{
		OS:            runtime.GOOS,
		Architecture:  runtime.GOARCH,
		Client:        ClientName,         // Client name from sync.go
		ClientVersion: GetClientVersion(), // Get the version from sync.go
	}

	// Get hostname
	if hostname, err := os.Hostname(); err == nil {
		metadata.Hostname = hostname
	} else {
		slog.Debug("Failed to get hostname", "error", err)
	}

	// Get username
	if currentUser, err := user.Current(); err == nil {
		metadata.Username = currentUser.Username
	} else {
		slog.Debug("Failed to get username", "error", err)
	}

	// Get OS-specific information
	switch runtime.GOOS {
	case "darwin":
		metadata.OSDisplayName = "macOS"
		metadata.OSVersion = getMacOSVersion()
	case "linux":
		metadata.OSDisplayName = "Linux"
		metadata.OSVersion = getLinuxVersion()
	default:
		metadata.OSDisplayName = runtime.GOOS
		metadata.OSVersion = "unknown"
	}

	return metadata
}

// ===== Main Authentication Functions =====

// IsAuthenticated checks if the user is authenticated for cloud sync
func IsAuthenticated() bool {
	// If session token was set via `--cloud-token` flag, we're authenticated
	if hasSessionToken {
		return true
	}

	// Return cached result if already checked
	if authChecked {
		return isAuthenticated
	}

	// Mark as checked
	authChecked = true
	isAuthenticated = false

	// Get auth file path
	authPath, err := utils.GetAuthPath()
	if err != nil {
		slog.Error("Failed to get auth path", "error", err)
		return false
	}
	slog.Info("Checking authentication", "path", authPath)

	// Check if file exists
	fileInfo, err := os.Stat(authPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("Auth file does not exist", "path", authPath)
		} else {
			slog.Error("Error accessing auth file", "error", err)
		}
		return false
	}

	// Check if it's a regular file
	if !fileInfo.Mode().IsRegular() {
		slog.Error("Auth path is not a regular file", "path", authPath)
		return false
	}

	// Read auth data
	authData, err := readAuthData(authPath)
	if err != nil {
		slog.Error("Failed to read auth data", "error", err)
		return false
	}

	// Check for valid access token
	if authData.CloudAccess != nil && authData.CloudAccess.Token != "" {
		if !isTokenExpired(authData.CloudAccess.ExpiresAt) {
			slog.Info("Authentication successful (access token valid)")
			isAuthenticated = true
			return true
		}
	}

	// Check if we have a valid refresh token to get/refresh the access token
	if authData.CloudRefresh != nil && authData.CloudRefresh.Token != "" {
		if !isTokenExpired(authData.CloudRefresh.ExpiresAt) {
			// Determine if we need to refresh the access token
			needsRefresh := authData.CloudAccess == nil ||
				authData.CloudAccess.Token == "" ||
				isTokenExpired(authData.CloudAccess.ExpiresAt)

			if needsRefresh {
				// Log appropriate message based on why we're refreshing
				if authData.CloudAccess == nil || authData.CloudAccess.Token == "" {
					slog.Info("No access token, attempting to get one with refresh token")
				} else {
					slog.Info("Access token expired, attempting refresh")
				}

				if err := refreshAccessToken(authData.CloudRefresh.Token); err != nil {
					slog.Error("Failed to refresh access token", "error", err)
					// Check if this was a 401 authentication failure
					var authErr *ErrAuthenticationFailed
					if errors.As(err, &authErr) {
						return false
					}
					// Refresh token is valid but refresh failed (maybe network issue)
					// Still consider authenticated
					isAuthenticated = true
					return true
				}
				// Refresh successful
				isAuthenticated = true
				return true
			}

			// Have valid refresh token but access token is still valid
			// This shouldn't happen but handle gracefully
			isAuthenticated = true
			return true
		}
	}

	slog.Warn("No valid authentication tokens found")
	return false
}

// LoginWithDeviceCode exchanges a device code for authentication tokens
func LoginWithDeviceCode(code string) error {
	// Collect device metadata
	metadata := GetDeviceMetadata()

	// Create request payload
	reqBody := DeviceLoginRequest{
		DeviceCode:    code,
		Hostname:      metadata.Hostname,
		OS:            metadata.OS,
		OSVersion:     metadata.OSVersion,
		OSDisplayName: metadata.OSDisplayName,
		Architecture:  metadata.Architecture,
		Username:      metadata.Username,
		Client:        metadata.Client,
		ClientVersion: metadata.ClientVersion,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API request
	apiURL := GetAPIBaseURL() + "/api/v1/device-login"
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", GetUserAgent())

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	slog.Debug("Exchanging device code for refresh token", "url", apiURL)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check response status
	if resp.StatusCode == 401 {
		// Try to parse error response
		var errorResp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(respBody, &errorResp); err == nil && errorResp.Error != "" {
			return fmt.Errorf("%s", errorResp.Error)
		}
		return fmt.Errorf("invalid or expired device code")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("Device login failed",
			"status", resp.StatusCode,
			"responseBody", string(respBody))
		return fmt.Errorf("device login failed with status %d", resp.StatusCode)
	}

	// Parse response
	var respData DeviceLoginResponse
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	// Create auth data with refresh token
	authData := &AuthData{
		CloudRefresh: &CloudRefreshData{
			Token:       respData.RefreshToken,
			As:          respData.User.Email,
			CreatedAt:   respData.CreatedAt,
			ExpiresAt:   respData.ExpiresAt,
			LastValidAt: respData.CreatedAt, // Initially same as created
		},
	}

	// Get auth file path
	authPath, err := utils.GetAuthPath()
	if err != nil {
		return fmt.Errorf("failed to get auth path: %w", err)
	}

	// Save initial auth data with refresh token
	if err := writeAuthData(authPath, authData); err != nil {
		return fmt.Errorf("failed to save authentication: %w", err)
	}

	// Now get an access token using the refresh token
	if err := refreshAccessToken(respData.RefreshToken); err != nil {
		// Not fatal - we have the refresh token saved
		slog.Warn("Failed to get initial access token", "error", err)
	}

	slog.Info("Successfully authenticated with device code", "user", respData.User.Email)
	return nil
}

// SetSessionRefreshToken sets a session-only refresh token and verifies it by exchanging for an access token.
// This is used for the `--cloud-token` flag to bypass normal authentication.
// The token is not persisted to disk and only valid for the current CLI session.
// Returns an error if the token cannot be exchanged for an access token.
func SetSessionRefreshToken(refreshToken string) error {
	slog.Info("Setting session-only refresh token")

	// Exchange refresh token for access token to verify it works
	respData, err := exchangeRefreshTokenForAccess(refreshToken)
	if err != nil {
		slog.Error("Failed to verify session refresh token", "error", err)
		return fmt.Errorf("failed to verify refresh token: %w", err)
	}

	// Store the session tokens (not persisted to disk)
	sessionRefreshToken = refreshToken
	sessionAccessToken = respData.AccessToken
	hasSessionToken = true

	// Mark authentication as checked and valid
	authChecked = true
	isAuthenticated = true

	slog.Info("Session refresh token verified successfully")
	return nil
}

// HasSessionToken returns true if a session-only token was set via `--cloud-token` flag
func HasSessionToken() bool {
	return hasSessionToken
}

// RefreshSessionAccessToken refreshes the session access token using the stored session refresh token.
// This should be called if the session access token expires during a long CLI session.
// Returns an error if no session token is set or if refresh fails.
func RefreshSessionAccessToken() error {
	if !hasSessionToken || sessionRefreshToken == "" {
		return fmt.Errorf("no session refresh token available")
	}

	respData, err := exchangeRefreshTokenForAccess(sessionRefreshToken)
	if err != nil {
		return fmt.Errorf("failed to refresh session access token: %w", err)
	}

	sessionAccessToken = respData.AccessToken
	slog.Info("Session access token refreshed successfully")
	return nil
}

// exchangeRefreshTokenForAccess exchanges a refresh token for an access token without persisting.
// Returns the full response including access token and expiry timestamps.
// This is used both for session tokens `--cloud-token“ flag, and for normal refresh flow.
func exchangeRefreshTokenForAccess(refreshToken string) (*DeviceRefreshResponse, error) {
	// Make API request
	apiURL := GetAPIBaseURL() + "/api/v1/device-refresh"
	req, err := http.NewRequest("POST", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+refreshToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", GetUserAgent())

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	slog.Debug("Exchanging refresh token for access token", "url", apiURL)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check response status
	if resp.StatusCode == 401 {
		return nil, &ErrAuthenticationFailed{Message: "invalid or expired refresh token"}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("Token exchange failed",
			"status", resp.StatusCode,
			"responseBody", string(respBody))
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	// Parse response
	var respData DeviceRefreshResponse
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &respData, nil
}

// refreshAccessToken uses a refresh token to get a new access token and persists it to disk.
// This is used for the normal auth flow where tokens are stored in the auth file.
func refreshAccessToken(refreshToken string) error {
	// Exchange refresh token for access token
	respData, err := exchangeRefreshTokenForAccess(refreshToken)
	if err != nil {
		// Check if this was a 401 authentication failure and set the flag
		var authErr *ErrAuthenticationFailed
		if errors.As(err, &authErr) {
			hadAuthFailure = true
		}
		return err
	}

	// Get auth file path
	authPath, err := utils.GetAuthPath()
	if err != nil {
		return fmt.Errorf("failed to get auth path: %w", err)
	}

	// Read current auth data
	authData, err := readAuthData(authPath)
	if err != nil {
		return fmt.Errorf("failed to read auth data: %w", err)
	}

	// Update access token with response data
	authData.CloudAccess = &CloudAccessData{
		Token:     respData.AccessToken,
		UpdatedAt: respData.CreatedAt,
		ExpiresAt: respData.ExpiresAt,
	}

	// Update last valid timestamp for refresh token
	if authData.CloudRefresh != nil {
		authData.CloudRefresh.LastValidAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Save updated auth data
	if err := writeAuthData(authPath, authData); err != nil {
		return fmt.Errorf("failed to save access token: %w", err)
	}

	slog.Info("Successfully refreshed access token")
	return nil
}

// AuthenticatedAs returns the user's authentication details (who they're logged in as and when)
func AuthenticatedAs() (username string, loginTime string) {
	// Check if authenticated first
	if !IsAuthenticated() {
		return "", ""
	}

	// Get auth file path
	authPath, err := utils.GetAuthPath()
	if err != nil {
		slog.Error("Failed to get auth path for authenticated user info", "error", err)
		return "", ""
	}

	// Read auth data
	if authData, err := readAuthData(authPath); err == nil {
		if authData.CloudRefresh != nil {
			return authData.CloudRefresh.As, authData.CloudRefresh.CreatedAt
		}
	}

	return "", ""
}

// GetCloudToken returns the current access token for API calls
func GetCloudToken() string {
	// If session token was set via `--cloud-token` flag, return the session access token
	if hasSessionToken && sessionAccessToken != "" {
		return sessionAccessToken
	}

	var token string

	// Since IsAuthenticated() caches the result and we know we're authenticated,
	// we can safely read the auth file
	authPath, _ := utils.GetAuthPath()

	// Read auth data
	if authData, err := readAuthData(authPath); err == nil {
		if authData.CloudAccess != nil && authData.CloudAccess.Token != "" {
			return authData.CloudAccess.Token
		}
	}

	return token
}

// Logout removes the authentication by deleting the auth.json file
// and revoking the refresh token on the server if using device auth
func Logout() error {
	// Get auth file path
	authPath, err := utils.GetAuthPath()
	if err != nil {
		slog.Error("Failed to get auth path for logout", "error", err)
		return fmt.Errorf("failed to get auth path: %w", err)
	}
	slog.Info("Logging out", "path", authPath)

	// Logout flow:
	// 1. Check if auth file exists (handle missing, corrupted, or valid states)
	// 2. If file exists and readable, try to revoke token on server (best effort)
	// 3. Always delete the local auth file if it exists (even if corrupted)
	// 4. Clear the auth cache to ensure subsequent checks reflect logout state
	// This approach ensures we can recover from corrupted auth files while still
	// properly revoking valid tokens when possible.

	// Check if auth file exists
	if _, err := os.Stat(authPath); err == nil {
		// File exists, try to read auth data to check if we need to revoke server-side
		if authData, readErr := readAuthData(authPath); readErr == nil {
			// If we have a refresh token, revoke it on the server
			if authData.CloudRefresh != nil && authData.CloudRefresh.Token != "" {
				// Make API request to revoke token
				apiURL := GetAPIBaseURL() + "/api/v1/device-logout"
				req, err := http.NewRequest("POST", apiURL, nil)
				if err != nil {
					slog.Warn("Failed to create logout request", "error", err)
				} else {
					req.Header.Set("Authorization", "Bearer "+authData.CloudRefresh.Token)
					req.Header.Set("User-Agent", GetUserAgent())

					client := &http.Client{Timeout: 10 * time.Second}
					slog.Debug("Revoking refresh token on server", "url", apiURL)

					if resp, err := client.Do(req); err != nil {
						slog.Warn("Failed to revoke token on server", "error", err)
					} else {
						_ = resp.Body.Close()
						slog.Info("Revoked refresh token on server")
					}
				}
			}
		} else {
			// Could not read auth data, but file exists - we'll still delete it
			slog.Warn("Could not read auth data, but will still delete file", "error", readErr)
		}

		// Delete the auth file
		if err := os.Remove(authPath); err != nil {
			slog.Error("Failed to delete auth file", "error", err, "path", authPath)

			if os.IsPermission(err) {
				return fmt.Errorf("permission denied: cannot delete auth file at %s", authPath)
			}

			return fmt.Errorf("failed to delete auth file: %w", err)
		}
		slog.Info("Successfully deleted auth file")
	} else if os.IsNotExist(err) {
		// File doesn't exist, nothing to delete
		slog.Info("Auth file does not exist, already logged out", "path", authPath)
	} else {
		// Some other error accessing the file
		slog.Error("Error checking auth file", "error", err, "path", authPath)
		return fmt.Errorf("error checking auth file: %w", err)
	}

	// Reset the auth cache
	ResetAuthCache()

	slog.Info("Successfully logged out")
	return nil
}

// ResetAuthCache resets the authentication cache (useful for testing)
func ResetAuthCache() {
	authChecked = false
	isAuthenticated = false
	hadAuthFailure = false
	// Also reset session tokens
	sessionRefreshToken = ""
	sessionAccessToken = ""
	hasSessionToken = false
}

// HadAuthFailure returns true if we encountered a 401 authentication failure
func HadAuthFailure() bool {
	return hadAuthFailure
}

// ===== Helper Functions =====

// readAuthData reads and parses the auth.json file
func readAuthData(authPath string) (*AuthData, error) {
	data, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read auth file: %w", err)
	}

	var authData AuthData
	if err := json.Unmarshal(data, &authData); err != nil {
		return nil, fmt.Errorf("failed to parse auth JSON: %w", err)
	}

	return &authData, nil
}

// writeAuthData writes the auth data to auth.json
func writeAuthData(authPath string, authData *AuthData) error {
	jsonData, err := json.MarshalIndent(authData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth data: %w", err)
	}

	// Ensure the directory exists
	dir := filepath.Dir(authPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create auth directory: %w", err)
	}

	if err := os.WriteFile(authPath, jsonData, 0600); err != nil {
		return fmt.Errorf("failed to write auth file: %w", err)
	}

	// Reset the auth cache so next check will reload
	ResetAuthCache()

	slog.Debug("Updated auth.json", "path", authPath)
	return nil
}

// isTokenExpired checks if a token has expired based on the expiresAt timestamp
func isTokenExpired(expiresAt string) bool {
	if expiresAt == "" {
		return true // No expiry means expired
	}

	expiryTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		slog.Error("Failed to parse expiry time", "expiresAt", expiresAt, "error", err)
		return true // Can't parse means expired
	}

	// Add a small buffer (5 minutes) to avoid edge cases
	buffer := 5 * time.Minute
	return time.Now().UTC().Add(buffer).After(expiryTime)
}
