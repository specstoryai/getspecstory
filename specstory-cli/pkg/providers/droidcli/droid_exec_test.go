package droidcli

import (
	"reflect"
	"testing"
)

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		resumeSessionID string
		expected        []string
	}{
		{
			name:            "returns args unchanged when resumeSessionID is empty",
			args:            []string{"--verbose"},
			resumeSessionID: "",
			expected:        []string{"--verbose"},
		},
		{
			name:            "returns args unchanged when --resume already has value",
			args:            []string{"--resume", "existing-session"},
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "existing-session"},
		},
		{
			name:            "returns args unchanged when -r already has value",
			args:            []string{"-r", "existing-session"},
			resumeSessionID: "new-session",
			expected:        []string{"-r", "existing-session"},
		},
		{
			name:            "returns args unchanged when --resume=value exists",
			args:            []string{"--resume=existing-session"},
			resumeSessionID: "new-session",
			expected:        []string{"--resume=existing-session"},
		},
		{
			name:            "appends --resume when not present",
			args:            []string{"--verbose"},
			resumeSessionID: "new-session",
			expected:        []string{"--verbose", "--resume", "new-session"},
		},
		{
			name:            "appends --resume when args is nil",
			args:            nil,
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "new-session"},
		},
		{
			name:            "appends --resume when args is empty",
			args:            []string{},
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "new-session"},
		},
		{
			name:            "appends when --resume flag exists but has no value",
			args:            []string{"--resume"},
			resumeSessionID: "new-session",
			expected:        []string{"--resume", "--resume", "new-session"},
		},
		{
			name:            "appends when -r flag exists but has no value",
			args:            []string{"-r"},
			resumeSessionID: "new-session",
			expected:        []string{"-r", "--resume", "new-session"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureResumeArgs(tt.args, tt.resumeSessionID)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("ensureResumeArgs() = %v, want %v", got, tt.expected)
			}
		})
	}
}
