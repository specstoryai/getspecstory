package analytics

import (
	"os"

	"github.com/posthog/posthog-go"
)

// Event name constants
const (
	EventExtensionActivated     = "ext_activated"
	EventProjectIdentityCreated = "ext_project_identity_created" // Tracks when new project identity is created
	EventCheckInstallSuccess    = "ext_check_install_success"    // Tracks successful agent installation check
	EventCheckInstallFailed     = "ext_check_install_failed"     // Tracks failed agent installation check
	EventVersionCommand         = "ext_version_command"          // Tracks when users check the version
	EventHelpCommand            = "ext_help_command"             // Tracks when users view help
	EventAutosaveNew            = "ext_autosave_new"             // Tracks when a new markdown file is created during `run` or `--sync-markdown`
	EventAutosaveSuccess        = "ext_autosave_success"         // Tracks when a markdown file is updated during `run`
	EventAutosaveError          = "ext_autosave_error"           // Tracks when an error occurs generating or writing markdown during `run`
	EventSyncMarkdownNew        = "ext_sync_markdown_new"        // Tracks when a new markdown file is created during `sync`
	EventSyncMarkdownSuccess    = "ext_sync_markdown_success"    // Tracks when a markdown file is updated during `sync`
	EventSyncMarkdownError      = "ext_sync_markdown_error"      // Tracks when an error occurs generating or writing markdown during `sync`
	EventLoginAttempted         = "ext_login_attempted"          // Tracks when user starts the login flow
	EventLoginCancelled         = "ext_login_cancelled"          // Tracks when user cancels the login flow
	EventLoginSuccess           = "ext_login_success"            // Tracks successful login to SpecStory Cloud
	EventLoginFailed            = "ext_login_failed"             // Tracks failed login attempts
	EventLogout                 = "ext_logout"                   // Tracks user logout from SpecStory Cloud
	EventCloudSyncComplete      = "ext_cloudsync_complete"       // Tracks cloud sync completion with statistics
)

// Properties is a type alias for event properties to avoid exposing PostHog types
type Properties map[string]interface{}

// TrackEvent sends a generic event to PostHog
func TrackEvent(eventName string, properties Properties) {
	if !IsEnabled() {
		return
	}

	distinctID := GetDistinctID()

	// Convert to PostHog properties
	phProperties := make(posthog.Properties)
	for k, v := range properties {
		phProperties[k] = v
	}

	// Always include CLI command, device ID, and project path
	phProperties["cli_command"] = GetCLICommand()
	phProperties["$device_id"] = distinctID

	// Capture current working directory as project_path
	if cwd, err := os.Getwd(); err == nil {
		phProperties["project_path"] = cwd
	}

	// Agent provider information
	phProperties["agent_provider"] = GetAgentProviderName()

	// Editor information
	phProperties["editor_name"] = "SpecStory CLI"
	phProperties["editor_type"] = "CLI"
	phProperties["extension_version"] = GetCLIVersion()

	// OS information (gathered once during initialization)
	osArch, osName, osPlatform, osVersion := GetOSInfo()
	phProperties["os_arch"] = osArch
	phProperties["os_name"] = osName
	phProperties["os_platform"] = osPlatform
	phProperties["os_version"] = osVersion

	err := client.Enqueue(posthog.Capture{
		DistinctId:       distinctID,
		Event:            eventName,
		Properties:       phProperties,
		SendFeatureFlags: false,
	})

	if err != nil {
		// Silently fail - analytics should not break the app
		return
	}
}
