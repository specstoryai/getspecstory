package piagent

import "github.com/specstoryai/getspecstory/specstory-cli/pkg/analytics"

// trackCheckSuccess emits the standard install-check success analytics event,
// matching the shape other providers use.
func trackCheckSuccess(custom bool, commandPath, resolvedPath, version string) {
	analytics.TrackEvent(analytics.EventCheckInstallSuccess, analytics.Properties{
		"provider":       providerID,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version":        version,
		"version_flag":   versionFlag,
	})
}

// trackCheckFailure emits the standard install-check failure analytics event.
func trackCheckFailure(custom bool, commandPath, resolvedPath, errorType, message string) {
	analytics.TrackEvent(analytics.EventCheckInstallFailed, analytics.Properties{
		"provider":       providerID,
		"custom_command": custom,
		"command_path":   commandPath,
		"resolved_path":  resolvedPath,
		"version_flag":   versionFlag,
		"error_type":     errorType,
		"error_message":  message,
	})
}
