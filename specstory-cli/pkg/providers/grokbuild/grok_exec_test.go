package grokbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
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
			name:            "the selected session overrides the configured id",
			args:            []string{"--resume", "other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume", "abc-123"},
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
			name:            "a populated --resume= is replaced",
			args:            []string{"--resume=other-id"},
			resumeSessionID: "abc-123",
			want:            []string{"--resume=abc-123"},
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

func TestExecuteGrokUsesProjectDirectory(t *testing.T) {
	if path := os.Getenv("GROK_QA_CWD_FILE"); path != "" {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(cwd), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	withFakeGrokHome(t)
	project := t.TempDir()
	output := filepath.Join(t.TempDir(), "child-cwd")
	t.Setenv("GROK_QA_CWD_FILE", output)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := NewProvider().ExecAgentAndWatch(project, fmt.Sprintf("%q -test.run=^TestExecuteGrokUsesProjectDirectory$", exe), "", false, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if spi.CanonicalizePathOrClean(string(got)) != spi.CanonicalizePathOrClean(project) {
		t.Fatalf("child ran in %q, wanted %q", got, project)
	}
}

func TestRelativeGrokHomeSharedWithChild(t *testing.T) {
	if output := os.Getenv("GROK_QA_HOME_FILE"); output != "" {
		if err := os.WriteFile(output, []byte(os.Getenv("GROK_HOME")), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	launch, project := t.TempDir(), t.TempDir()
	t.Chdir(launch)
	t.Setenv("GROK_HOME", "relative-grok")
	want := filepath.Join(launch, "relative-grok")
	home, err := GetGrokHome()
	if err != nil {
		t.Fatal(err)
	}
	if home != want {
		t.Errorf("discovery home %q, want %q", home, want)
	}
	output := filepath.Join(t.TempDir(), "child-home")
	t.Setenv("GROK_QA_HOME_FILE", output)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := NewProvider().ExecAgentAndWatch(project, fmt.Sprintf("%q -test.run=^TestRelativeGrokHomeSharedWithChild$", exe), "", false, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("child home %q, want shared absolute %q", raw, want)
	}
}
