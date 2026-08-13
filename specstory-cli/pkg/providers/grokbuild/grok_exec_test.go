package grokbuild

import (
	"reflect"
	"testing"
)

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		resumeSessionID string
		want            []string
	}{
		{
			name:            "no resume id leaves the command alone",
			args:            []string{"--permission-mode", "auto"},
			resumeSessionID: "",
			want:            []string{"--permission-mode", "auto"},
		},
		{
			name:            "appends the flag when it is absent",
			args:            []string{},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "abc-123"},
		},
		{
			name:            "an existing choice of session is respected",
			args:            []string{"--resume", "other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "other-id"},
		},
		{
			// A bare --resume is meaningful to grok on its own: it opens the
			// session picker. Appending a second flag would make grok read
			// "--resume" as the session id, so fill the value in instead.
			name:            "a trailing bare --resume is filled in",
			args:            []string{"--resume"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "abc-123"},
		},
		{
			name:            "a bare -r before another flag is filled in",
			args:            []string{"-r", "--permission-mode", "auto"},
			resumeSessionID: "abc-123",
			want:            []string{"-r", "abc-123", "--permission-mode", "auto"},
		},
		{
			name:            "an empty --resume= is repaired",
			args:            []string{"--resume="},
			resumeSessionID: "abc-123",
			want:            []string{"--resume=abc-123"},
		},
		{
			name:            "a populated --resume= is respected",
			args:            []string{"--resume=other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume=other-id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := make([]string, len(tt.args))
			copy(original, tt.args)

			got := ensureResumeArgs(tt.args, tt.resumeSessionID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ensureResumeArgs(%v, %q) = %v, want %v", original, tt.resumeSessionID, got, tt.want)
			}
			// The caller's slice must never be mutated in place.
			for i := range original {
				if tt.args[i] != original[i] {
					t.Errorf("input slice was mutated at %d: %q, was %q", i, tt.args[i], original[i])
				}
			}
		})
	}
}

func TestParseGrokCommand(t *testing.T) {
	cmd, args := parseGrokCommand(`/custom/grok --permission-mode auto`)
	if cmd != "/custom/grok" {
		t.Errorf("cmd = %q, want /custom/grok", cmd)
	}
	if !reflect.DeepEqual(args, []string{"--permission-mode", "auto"}) {
		t.Errorf("args = %v", args)
	}

	cmd, args = parseGrokCommand("")
	if cmd != "grok" {
		t.Errorf("default cmd = %q, want grok", cmd)
	}
	if len(args) != 0 {
		t.Errorf("default args = %v, want none", args)
	}
}
