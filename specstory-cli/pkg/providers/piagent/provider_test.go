package piagent

import (
	"strings"
	"testing"
)

// TestBuildCheckErrorMessage locks in the user-facing wording for each Check
// failure classification and names the command the user can retry.
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
			name:      "permission_denied names binary",
			errorType: "permission_denied",
			command:   "/usr/local/bin/pi",
			mustHave:  []string{"permissions", "/usr/local/bin/pi"},
		},
		{
			name:      "unclassified failure includes stderr verbatim",
			errorType: "unknown",
			command:   "pi",
			stderr:    "pi: bad runtime, no biscuit",
			mustHave:  []string{"pi --version", "pi: bad runtime, no biscuit"},
		},
		{
			name:      "unclassified failure without stderr still gives diagnosis hint",
			errorType: "unknown",
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
