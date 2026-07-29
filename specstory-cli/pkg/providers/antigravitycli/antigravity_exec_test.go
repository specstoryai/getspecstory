package antigravitycli

import (
	"slices"
	"testing"
)

func TestParseCommand(t *testing.T) {
	if name, args := parseCommand(""); name != defaultCommand || len(args) != 0 {
		t.Errorf("parseCommand(\"\") = (%q, %v), want (%q, [])", name, args, defaultCommand)
	}
	if name, args := parseCommand("agy --sandbox"); name != "agy" || !slices.Equal(args, []string{"--sandbox"}) {
		t.Errorf("parseCommand custom = (%q, %v)", name, args)
	}
}

func TestEnsureResumeArgs(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		resume string
		want   []string
	}{
		{name: "empty resume leaves args unchanged", args: []string{"--sandbox"}, resume: "", want: []string{"--sandbox"}},
		{name: "appends conversation flag", args: nil, resume: "conv-1", want: []string{"--conversation", "conv-1"}},
		{name: "fills empty existing flag", args: []string{"--conversation"}, resume: "conv-1", want: []string{"--conversation", "conv-1"}},
		{name: "respects existing flag value", args: []string{"--conversation", "other"}, resume: "conv-1", want: []string{"--conversation", "other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureResumeArgs(tt.args, tt.resume)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ensureResumeArgs(%v, %q) = %v, want %v", tt.args, tt.resume, got, tt.want)
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	if got := expandTilde("/abs/path"); got != "/abs/path" {
		t.Errorf("expandTilde should not change absolute paths, got %q", got)
	}
	if got := expandTilde("~/bin/agy"); got == "~/bin/agy" {
		t.Errorf("expandTilde should expand leading ~/")
	}
}
