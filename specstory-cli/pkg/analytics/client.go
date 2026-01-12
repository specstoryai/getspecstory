package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/posthog/posthog-go"
)

var (
	client posthog.Client

	// Injected at build time via ldflags. Empty in dev builds (analytics disabled).
	apiKey = ""

	cliCommand string // Store the CLI command globally
	distinctID string // Store the shared analytics ID
	cliVersion string // Store the CLI version globally

	// OS information (gathered once at initialization)
	osArch     string
	osName     string
	osPlatform string
	osVersion  string

	// Agent provider information
	agentProviders []string // Store current provider(s) being used
)

// slogAdapter adapts PostHog's logger interface to use slog at DEBUG level
// This prevents PostHog from writing directly to stderr
type slogAdapter struct{}

// Logf implements PostHog's Logger interface for info-level messages
// All PostHog log messages are routed to slog.Debug to keep them internal
func (s *slogAdapter) Logf(format string, args ...interface{}) {
	slog.Debug("posthog: "+format, args...)
}

// Errorf implements PostHog's Logger interface for error-level messages
// Even errors from PostHog (like API failures) are logged at DEBUG level
// since they represent analytics infrastructure issues, not application errors
func (s *slogAdapter) Errorf(format string, args ...interface{}) {
	slog.Debug("posthog error: "+format, args...)
}

// Debugf implements PostHog's Logger interface for debug-level messages
func (s *slogAdapter) Debugf(format string, args ...interface{}) {
	slog.Debug("posthog debug: "+format, args...)
}

// Warnf implements PostHog's Logger interface for warning-level messages
func (s *slogAdapter) Warnf(format string, args ...interface{}) {
	slog.Debug("posthog warning: "+format, args...)
}

// Init initializes the PostHog analytics client
func Init(command string, version string) error {
	cliCommand = command
	cliVersion = version

	// Gather OS information once
	osArch = runtime.GOARCH
	osName = getOSName()      // Descriptive name from uname (like Node's os.type())
	osPlatform = runtime.GOOS // Generic platform identifier (like Node's process.platform)
	osVersion = getOSVersion()

	// Try to load shared analytics ID on macOS
	id, err := loadOrCreateSharedAnalyticsID()
	if err != nil {
		// Use hostname/username hash on non-macOS or if shared ID fails
		distinctID = generateFallbackID()
	} else {
		distinctID = id
	}

	if apiKey == "" {
		// Analytics disabled if no API key
		return nil
	}

	// Create client with GeoIP enabled (disabled by default in Go)
	enableGeoIP := false // Enable GeoIP tracking (PostHog library defaults to disabled)
	client, _ = posthog.NewWithConfig(apiKey, posthog.Config{
		DisableGeoIP: &enableGeoIP,
		Interval:     100 * time.Millisecond, // Send events quickly (default is 5s)
		BatchSize:    1,                      // Send immediately, don't batch
		Logger:       &slogAdapter{},         // Route PostHog logs through slog at DEBUG level
	})
	return nil
}

// Close closes the PostHog client connection
func Close() error {
	if client != nil {
		return client.Close()
	}
	return nil
}

// IsEnabled returns true if analytics is enabled
func IsEnabled() bool {
	return client != nil
}

// GetDistinctID returns the shared analytics ID
func GetDistinctID() string {
	return distinctID
}

// generateFallbackID generates a fallback ID from hostname and username
func generateFallbackID() string {
	// Fallback ID ensures consistent tracking when shared ID is unavailable
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")

	data := hostname + "-" + username
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// GetCLICommand returns the stored CLI command
func GetCLICommand() string {
	return cliCommand
}

// GetCLIVersion returns the stored CLI version
func GetCLIVersion() string {
	return cliVersion
}

// GetOSInfo returns the stored OS information
func GetOSInfo() (arch, name, platform, version string) {
	return osArch, osName, osPlatform, osVersion
}

// getOSName returns the descriptive OS name from uname (called once during init)
func getOSName() string {
	// Use uname -s for Unix-like systems (Linux, macOS)
	cmd := exec.Command("uname", "-s")
	output, err := cmd.Output()
	if err != nil {
		// Fallback to capitalized GOOS
		return strings.ToUpper(runtime.GOOS[:1]) + runtime.GOOS[1:]
	}
	return strings.TrimSpace(string(output))
}

// getOSVersion returns the OS version (called once during init)
func getOSVersion() string {
	switch runtime.GOOS {
	case "darwin":
		return getMacOSVersion()
	case "linux":
		return getLinuxVersion()
	default:
		return "unknown"
	}
}

// getMacOSVersion gets the Darwin kernel version using uname to match up with our other extensions os_version property
func getMacOSVersion() string {
	cmd := exec.Command("uname", "-r")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

// getLinuxVersion gets the Linux version from various sources
func getLinuxVersion() string {
	// Try /etc/os-release first
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "VERSION_ID=") {
				version := strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), `"`)
				return version
			}
		}
	}

	// Fallback to uname command
	cmd := exec.Command("uname", "-r")
	if output, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(output))
	}

	return "unknown"
}

// SetAgentProviders sets the agent provider(s) being used
func SetAgentProviders(providers []string) {
	agentProviders = providers
}

// GetAgentProviderName returns the agent provider(s) as an array
// Returns nil when no providers are set (property won't be sent)
func GetAgentProviderName() interface{} {
	if len(agentProviders) == 0 {
		return nil // Don't send the property at all
	}
	return agentProviders // Always return as array when set
}
