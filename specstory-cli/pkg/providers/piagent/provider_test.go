package piagent

import (
	"strings"
	"testing"
)

// TestBuildCheckErrorMessage locks in the user-facing wording for each Check
// failure classification, matching the pattern used by sibling providers
// (see e.g. deepseektui/antigravitycli).
func TestBuildCheckErrorMessage(t *testing.T) {
	tests := []struct {
		name      string
		errorType string
		command   string
		isCustom  bool
		stderr    string
		mustHave  []string
	}{
		{
			name:      "not_found default command suggests install",
			errorType: "not_found",
			command:   "pi",
			isCustom:  false,
			mustHave:  []string{"pi coding agent was not found", "PATH", "Install"},
		},
		{
			name:      "not_found custom command echoes provided path",
			errorType: "not_found",
			command:   "/opt/foo",
			isCustom:  true,
			mustHave:  []string{"pi coding agent was not found", "/opt/foo"},
		},
		{
			// The command passed here must be the resolved binary path, not a bare
			// command name — Check() only reaches this branch after exec.LookPath
			// has already succeeded, so a resolved path is always available.
			name:      "permission_denied includes chmod hint with resolved path",
			errorType: "permission_denied",
			command:   "/usr/local/bin/pi",
			mustHave:  []string{"chmod", "/usr/local/bin/pi"},
		},
		{
			name:      "unclassified failure includes stderr verbatim",
			errorType: "version_failed",
			command:   "pi",
			stderr:    "pi: bad runtime, no biscuit",
			mustHave:  []string{"pi --version", "pi: bad runtime, no biscuit"},
		},
		{
			name:      "unclassified failure without stderr still gives diagnosis hint",
			errorType: "version_failed",
			command:   "pi",
			mustHave:  []string{"pi --version"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCheckErrorMessage(tt.errorType, tt.command, tt.isCustom, tt.stderr)
			for _, want := range tt.mustHave {
				if !strings.Contains(got, want) {
					t.Errorf("buildCheckErrorMessage missing %q\nfull message:\n%s", want, got)
				}
			}
		})
	}
}
