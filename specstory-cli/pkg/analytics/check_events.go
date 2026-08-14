package analytics

// CheckAttempt describes one "<agent> --version" probe performed by a
// provider's Check implementation. It is filled in progressively as the probe
// proceeds — ResolvedPath is only known once the binary has been located — and
// then reported with TrackCheckSuccess or TrackCheckFailure.
//
// Grouping the fields is what keeps the two report calls honest: they otherwise
// take a run of same-typed string arguments that is easy to transpose silently.
type CheckAttempt struct {
	Provider      string // provider ID, e.g. "deepseek"
	CustomCommand bool   // whether the command came from user configuration rather than PATH
	CommandPath   string // the command as configured or discovered, before resolution
	ResolvedPath  string // absolute path the command resolved to; empty if it never resolved
	VersionFlag   string // flag used to probe the version, e.g. "--version"
}

// properties renders the fields common to both check outcomes.
func (a CheckAttempt) properties() Properties {
	return Properties{
		"provider":       a.Provider,
		"custom_command": a.CustomCommand,
		"command_path":   a.CommandPath,
		"resolved_path":  a.ResolvedPath,
		"version_flag":   a.VersionFlag,
	}
}

// TrackCheckSuccess reports an agent installation check that resolved the
// binary and read a version from it.
func TrackCheckSuccess(attempt CheckAttempt, version string) {
	props := attempt.properties()
	props["version"] = version
	TrackEvent(EventCheckInstallSuccess, props)
}

// TrackCheckFailure reports an agent installation check that failed, with
// errorType one of the spi.CheckError* values. stderr is only reported when the
// probe actually produced some, to keep the event free of empty noise.
func TrackCheckFailure(attempt CheckAttempt, errorType string, message string, stderr string) {
	props := attempt.properties()
	props["error_type"] = errorType
	props["error_message"] = message
	if stderr != "" {
		props["stderr"] = stderr
	}
	TrackEvent(EventCheckInstallFailed, props)
}
